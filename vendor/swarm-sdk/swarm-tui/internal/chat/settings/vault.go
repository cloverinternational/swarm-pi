package settings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// VaultSettings is the settings panel for vault management.
type VaultSettings struct {
	state               *State
	globalPath          string
	projectPath         string
	projectID           string
	identityPath        string // User's identity file (~/.swarm/vault/identity)
	recipientsPath      string // Project recipients file (.swarm/vault/recipients.txt)
	transparentPath     string // Auto-load transparent credentials (~/.swarm/vault/credentials.json)
	storage             *vault.AgeStorage
	projectStore        *vault.AgeStorage
	multiRecipientStore *vault.MultiRecipientStorage // For team vaults
	transparentStore    *vault.TransparentStorage    // Auto-load, no password
	transparentLocked   bool                         // Explicitly disabled by a full in-memory lock
	creds               []vault.Credential
	projectCreds        []vault.Credential
	recipients          []vault.RecipientInfo // Team members who can decrypt
	loaded              bool
	message             string
	// Callback when vault is unlocked or locked
	// The callback receives the vault provider (nil when locked)
	onVaultUnlock func(provider vault.VaultProvider)
}

// NewVaultSettings creates a new vault settings panel.
func NewVaultSettings(state *State) *VaultSettings {
	home, _ := os.UserHomeDir()
	return &VaultSettings{
		state:           state,
		globalPath:      filepath.Join(home, ".swarm", "vault", "global.vault"),
		identityPath:    filepath.Join(home, ".swarm", "vault", "identity"),
		transparentPath: filepath.Join(home, ".swarm", "vault", "credentials.json"),
	}
}

// SetProjectPath sets the project vault path.
func (s *VaultSettings) SetProjectPath(workspaceRoot string) {
	if workspaceRoot == "" {
		s.projectPath = ""
		s.projectID = ""
		s.recipientsPath = ""
		return
	}
	s.projectID = workspaceRoot
	s.projectPath = filepath.Join(workspaceRoot, ".swarm", "vault", "project.vault")
	s.recipientsPath = filepath.Join(workspaceRoot, ".swarm", "vault", "recipients.txt")
}

// SetState updates the panel state.
func (s *VaultSettings) SetState(state *State) {
	s.state = state
}

// GetSection returns the section for this panel.
func (s *VaultSettings) GetSection() Section {
	return SectionVault
}

// GetStorage returns the unlocked global storage.
func (s *VaultSettings) GetStorage() *vault.AgeStorage {
	return s.storage
}

// GetProjectStorage returns the unlocked project storage (passphrase-based).
func (s *VaultSettings) GetProjectStorage() *vault.AgeStorage {
	return s.projectStore
}

// GetMultiRecipientStorage returns the unlocked project storage (identity-based).
func (s *VaultSettings) GetMultiRecipientStorage() *vault.MultiRecipientStorage {
	return s.multiRecipientStore
}

// IsProjectVaultIdentityBased returns true if project vault uses identity encryption.
func (s *VaultSettings) IsProjectVaultIdentityBased() bool {
	return s.recipientsPath != "" && vault.RecipientsExist(s.recipientsPath)
}

// HasIdentity returns true if user has an identity file.
func (s *VaultSettings) HasIdentity() bool {
	return vault.IdentityExists(s.identityPath)
}

// GetRecipients returns the team members who can decrypt the project vault.
func (s *VaultSettings) GetRecipients() []vault.RecipientInfo {
	return s.recipients
}

// GetIdentityPublicKey returns the user's public key (if identity exists).
func (s *VaultSettings) GetIdentityPublicKey() string {
	if !vault.IdentityExists(s.identityPath) {
		return ""
	}
	_, pubkey, err := vault.LoadIdentityWithPublicKey(s.identityPath)
	if err != nil {
		return ""
	}
	return pubkey
}

// GetIdentityPath returns the path to the user's age identity file (private
// key). Used by the /vault team and /vault approve subcommands.
func (s *VaultSettings) GetIdentityPath() string {
	return s.identityPath
}

// GetRecipientsPath returns the path to the project recipients.txt roster file
// (may be "" when no workspace is open).
func (s *VaultSettings) GetRecipientsPath() string {
	return s.recipientsPath
}

// CreateVaultProvider creates a VaultProvider from the unlocked storage.
// Falls back to transparent (auto-load, no-password) storage if no encrypted vault is unlocked.
// Returns nil if no vault is available.
// This is used to enable vault tools for agents.
// This is used to enable vault tools for agents.
func (s *VaultSettings) CreateVaultProvider(projectID string) vault.VaultProvider {
	if projectID == "" {
		projectID = s.projectID
	}
	// Check what's unlocked
	var globalStorage vault.Storage
	var projectStorage vault.Storage

	if s.storage != nil {
		globalStorage = s.storage
	}
	if s.projectStore != nil {
		projectStorage = s.projectStore
	} else if s.multiRecipientStore != nil {
		projectStorage = s.multiRecipientStore
	}

	// Fall back to transparent storage if no encrypted vault is unlocked
	if globalStorage == nil && s.transparentStore != nil && !s.transparentLocked {
		globalStorage = s.transparentStore
	}
	globalAvailable := globalStorage != nil
	projectAvailable := projectStorage != nil

	// Need at least one storage
	if globalStorage == nil && projectStorage == nil {
		return nil
	}

	// Vault.List always consults global storage for an unscoped query, so a
	// project-only unlock still needs a non-nil global fallback.
	if globalStorage == nil {
		globalStorage = vault.NewMemoryStorage()
	}

	// The interactive TUI's permission checker is the single ordinary-credential
	// approval authority for every model-facing vault tool. Delegated mode avoids
	// a second vault-only approval loop after the user has already approved the
	// complete vault_exec invocation. The executor still enforces credential
	// constraints and two-person integrity before this mode is consulted.
	v := vault.NewVault(globalStorage, vault.VaultConfig{
		Enabled:         true,
		DefaultMode:     vault.ModeYOLO,
		TrustedUnlocked: true,
	})

	// Add project storage if available
	if projectStorage != nil && projectID != "" {
		v.AddProjectStorage(projectID, projectStorage)
	}

	// Create executor
	exec := vault.NewExecutor(v, vault.VaultConfig{
		Enabled:         true,
		DefaultMode:     vault.ModeYOLO,
		TrustedUnlocked: true,
	}, nil) // No auditor for now

	provider := vault.NewVaultProvider(exec, v, "").
		WithAvailableScopes(globalAvailable, projectAvailable)

	// Wire the user's identity so Two-Person Integrity begin/approve can load it
	// from within the TUI session (same as the headless auto-load path).
	if s.identityPath != "" && vault.IdentityExists(s.identityPath) {
		provider = provider.WithIdentityPath(s.identityPath)
	}

	// Expose the two-person store + roster so vault_add can seal sensitive
	// credentials and vault_approve/exec can reconstruct them. The store lives
	// beside recipients.txt as twoperson.json in the project vault dir.
	if s.recipientsPath != "" && vault.RecipientsExist(s.recipientsPath) {
		twoPersonPath := filepath.Join(filepath.Dir(s.recipientsPath), "twoperson.json")
		if tps, err := vault.NewTwoPersonStorage(twoPersonPath); err == nil {
			provider = provider.WithTwoPersonStore(tps, s.recipientsPath)
		}
	}

	return provider
}

// LoadTransparentVault attempts to load the transparent (auto-load, no-password) vault.
// Returns true if a transparent vault was found and loaded.
func (s *VaultSettings) LoadTransparentVault() bool {
	if s.transparentPath == "" {
		return false
	}
	if !vault.TransparentStorageExists(s.transparentPath) {
		return false
	}
	store, err := vault.NewTransparentStorage(s.transparentPath)
	if err != nil {
		s.message = i18n.T("settings.vault.error.transparent_load", err)
		return false
	}
	s.transparentStore = store
	s.transparentLocked = false
	creds, err := store.List(context.Background(), vault.CredentialFilter{})
	if err != nil {
		s.message = i18n.T("settings.vault.error.transparent_list", err)
		return false
	}
	s.creds = creds
	s.loaded = true
	if s.state != nil {
		s.state.VaultState = "unlocked"
	}
	s.message = i18n.T("settings.vault.transparent_auto_loaded", len(creds))
	return true
}

// HasTransparentVault returns true if a transparent vault file exists.
func (s *VaultSettings) HasTransparentVault() bool {
	if s.transparentPath == "" {
		return false
	}
	return vault.TransparentStorageExists(s.transparentPath)
}

// IsTransparentLoaded returns true if the transparent vault is currently loaded.
func (s *VaultSettings) IsTransparentLoaded() bool {
	return s.transparentStore != nil
}

// SetOnVaultUnlockCallback sets the callback invoked when the vault is unlocked or locked.
// The callback receives a VaultProvider when unlocked, or nil when locked.
func (s *VaultSettings) SetOnVaultUnlockCallback(callback func(provider vault.VaultProvider)) {
	s.onVaultUnlock = callback
}

// notifyVaultUnlock notifies the callback with the current vault provider.
func (s *VaultSettings) notifyVaultUnlock(projectID string) {
	if s.onVaultUnlock != nil {
		provider := s.CreateVaultProvider(projectID)
		s.onVaultUnlock(provider)
	}
}

// persistUnlockedCredentials copies legacy vault contents into the shared
// cleartext store so every local TUI and agent can use the same credentials.
func (s *VaultSettings) persistUnlockedCredentials(creds []vault.Credential) {
	if len(creds) == 0 || s.transparentPath == "" {
		return
	}
	store := s.transparentStore
	if store == nil {
		var err error
		store, err = vault.NewTransparentStorage(s.transparentPath)
		if err != nil {
			return
		}
		s.transparentStore = store
	}
	for _, cred := range creds {
		cred.Scope = vault.ScopeGlobal
		cred.ProjectID = ""
		_ = store.Store(context.Background(), cred)
	}
}

// notifyVaultLocked notifies the callback that the vault is now locked.
func (s *VaultSettings) notifyVaultLocked() {
	if s.onVaultUnlock != nil {
		s.onVaultUnlock(nil)
	}
}

// IsUnlocked reports whether any vault storage is currently available to the
// agent. It follows provider availability rather than the selected UI scope.
func (s *VaultSettings) IsUnlocked() bool {
	return s.CreateVaultProvider("") != nil
}

// LockAll clears all in-memory vault state and disables agent access. It does
// not delete persistent encrypted or transparent vault files.
func (s *VaultSettings) LockAll(state *State) {
	s.storage = nil
	s.projectStore = nil
	s.multiRecipientStore = nil
	s.transparentLocked = s.transparentStore != nil
	s.creds = nil
	s.projectCreds = nil
	s.recipients = nil
	s.loaded = false
	s.message = i18n.T("settings.vault.locked")
	if state != nil {
		state.VaultState = "locked"
		state.VaultPassphrase = ""
		state.VaultCursorPos = 0
		state.VaultSelectedCred = 0
		state.VaultScrollOffset = 0
	}
	s.notifyVaultLocked()
}

func (s *VaultSettings) scopeUnlocked(scope string) bool {
	if scope == "project" {
		return s.projectStore != nil || s.multiRecipientStore != nil
	}
	return s.storage != nil || (s.transparentStore != nil && !s.transparentLocked)
}

func (s *VaultSettings) switchScope(state *State) {
	if state.VaultScope == "global" && s.projectPath != "" {
		state.VaultScope = "project"
	} else {
		state.VaultScope = "global"
	}
	if s.scopeUnlocked(state.VaultScope) {
		state.VaultState = "unlocked"
	} else {
		state.VaultState = "locked"
	}
	state.VaultSelectedCred = 0
	state.VaultScrollOffset = 0
	s.loaded = s.scopeUnlocked(state.VaultScope)
}

// SelectScope selects the requested scope when it is available and synchronizes
// the panel state with that scope's actual unlock state.
func (s *VaultSettings) SelectScope(scope string, state *State) {
	if state == nil {
		return
	}
	if scope == "project" && s.projectPath != "" {
		state.VaultScope = "project"
	} else if scope == "global" {
		state.VaultScope = "global"
	}
	if s.scopeUnlocked(state.VaultScope) {
		state.VaultState = "unlocked"
	} else {
		state.VaultState = "locked"
	}
	state.VaultSelectedCred = 0
	state.VaultScrollOffset = 0
	s.loaded = s.scopeUnlocked(state.VaultScope)
}

// HandleKey handles keyboard input for vault settings.
// Returns true if the key was handled, false otherwise.
func (s *VaultSettings) HandleKey(key string, state *State) bool {
	s.state = state

	// Handle based on current vault state
	switch state.VaultState {
	case "locked":
		return s.handleLockedKey(key, state)
	case "unlocking":
		return s.handleUnlockingKey(key, state)
	case "unlocked":
		return s.handleUnlockedKey(key, state)
	case "error":
		return s.handleErrorKey(key, state)
	}
	return false
}

func (s *VaultSettings) handleLockedKey(key string, state *State) bool {
	switch key {
	case "enter":
		if state.VaultScope == "global" && s.transparentStore != nil && s.transparentLocked {
			s.transparentLocked = false
			creds, err := s.transparentStore.List(context.Background(), vault.CredentialFilter{})
			if err != nil {
				s.transparentLocked = true
				state.VaultState = "error"
				s.message = i18n.T("settings.vault.error.transparent_load", err)
				return true
			}
			s.creds = creds
			s.loaded = true
			state.VaultState = "unlocked"
			s.message = i18n.T("settings.vault.transparent_enabled", len(creds))
			s.notifyVaultUnlock("")
			return true
		}
		// Transparent store is auto-loaded and not locked — show its credentials
		// instead of prompting for a passphrase on the old encrypted vault.
		if state.VaultScope == "global" && s.transparentStore != nil && !s.transparentLocked {
			creds, err := s.transparentStore.List(context.Background(), vault.CredentialFilter{})
			if err != nil {
				state.VaultState = "error"
				s.message = i18n.T("settings.vault.error.transparent_load", err)
				return true
			}
			s.creds = creds
			s.loaded = true
			state.VaultState = "unlocked"
			s.message = i18n.T("settings.vault.transparent_auto_loaded", len(creds))
			s.notifyVaultUnlock("")
			return true
		}

		// Check if project vault uses identity encryption
		if state.VaultScope == "project" && s.IsProjectVaultIdentityBased() {
			// Identity-based unlock - no passphrase needed
			s.unlockWithIdentity(state, s.projectPath)
			return true
		}
		// Start passphrase input for global or passphrase-based vaults
		state.VaultState = "unlocking"
		state.VaultPassphrase = ""
		state.VaultCursorPos = 0
		state.VaultUnlockingScope = state.VaultScope
		s.message = ""
		return true
	case "tab", "s":
		s.switchScope(state)
		return true
	case "up", "k":
		// Navigate up (handled by manager for sidebar)
		return false
	case "down", "j":
		// Navigate down (handled by manager for sidebar)
		return false
	}
	return false
}

func (s *VaultSettings) handleUnlockingKey(key string, state *State) bool {
	switch key {
	case "enter":
		// Attempt unlock
		s.unlockVault(state)
		return true
	case "esc":
		// Cancel unlock
		state.VaultState = "locked"
		state.VaultPassphrase = ""
		state.VaultCursorPos = 0
		s.message = ""
		return true
	case "backspace":
		if state.VaultCursorPos > 0 {
			pass := []rune(state.VaultPassphrase)
			pass = append(pass[:state.VaultCursorPos-1], pass[state.VaultCursorPos:]...)
			state.VaultPassphrase = string(pass)
			state.VaultCursorPos--
		}
		return true
	case "delete":
		pass := []rune(state.VaultPassphrase)
		if state.VaultCursorPos < len(pass) {
			pass = append(pass[:state.VaultCursorPos], pass[state.VaultCursorPos+1:]...)
			state.VaultPassphrase = string(pass)
		}
		return true
	case "left", "ctrl+b":
		if state.VaultCursorPos > 0 {
			state.VaultCursorPos--
		}
		return true
	case "right", "ctrl+f":
		pass := []rune(state.VaultPassphrase)
		if state.VaultCursorPos < len(pass) {
			state.VaultCursorPos++
		}
		return true
	case "home", "ctrl+a":
		state.VaultCursorPos = 0
		return true
	case "end", "ctrl+e":
		state.VaultCursorPos = len(state.VaultPassphrase)
		return true
	case "ctrl+u":
		// Clear passphrase before cursor
		state.VaultPassphrase = state.VaultPassphrase[state.VaultCursorPos:]
		state.VaultCursorPos = 0
		return true
	case "ctrl+k":
		// Clear passphrase after cursor
		pass := []rune(state.VaultPassphrase)
		state.VaultPassphrase = string(pass[:state.VaultCursorPos])
		return true
	default:
		// Handle character input
		if len(key) == 1 || key == "space" {
			var ch string
			if key == "space" {
				ch = " "
			} else {
				ch = key
			}
			pass := []rune(state.VaultPassphrase)
			pass = append(pass[:state.VaultCursorPos], append([]rune(ch), pass[state.VaultCursorPos:]...)...)
			state.VaultPassphrase = string(pass)
			state.VaultCursorPos++
			return true
		}
	}
	return false
}

func (s *VaultSettings) handleUnlockedKey(key string, state *State) bool {
	switch key {
	case "r":
		// Refresh credentials
		s.loadCredentials(state)
		return true
	case "l":
		// Lock vault
		if state.VaultScope == "global" {
			s.storage = nil
			s.transparentLocked = s.transparentStore != nil
			s.creds = nil
		} else {
			s.projectStore = nil
			s.multiRecipientStore = nil
			s.projectCreds = nil
			s.recipients = nil
		}
		s.loaded = false
		state.VaultState = "locked"
		state.VaultSelectedCred = 0
		state.VaultScrollOffset = 0
		s.message = i18n.T("settings.vault.locked")

		// Rebuild the provider so another unlocked scope remains available.
		if s.IsUnlocked() {
			s.notifyVaultUnlock("")
		} else {
			s.notifyVaultLocked()
		}

		return true
	case "tab", "s":
		s.switchScope(state)
		return true
	case "up", "k":
		// Navigate up in credential list
		creds := s.getCurrentCreds(state)
		if len(creds) > 0 && state.VaultSelectedCred > 0 {
			state.VaultSelectedCred--
			if state.VaultSelectedCred < state.VaultScrollOffset {
				state.VaultScrollOffset = state.VaultSelectedCred
			}
		}
		return true
	case "down", "j":
		// Navigate down in credential list
		creds := s.getCurrentCreds(state)
		if len(creds) > 0 && state.VaultSelectedCred < len(creds)-1 {
			state.VaultSelectedCred++
			if state.VaultSelectedCred >= state.VaultScrollOffset+state.MaxVisible {
				state.VaultScrollOffset = state.VaultSelectedCred - state.MaxVisible + 1
			}
		}
		return true
	case "esc":
		// Lock all scopes and return to locked state.
		s.LockAll(state)
		s.message = ""

		return true
	}
	return false
}

func (s *VaultSettings) handleErrorKey(key string, state *State) bool {
	switch key {
	case "enter", "esc":
		state.VaultState = "locked"
		s.message = ""
		return true
	}
	return false
}

func (s *VaultSettings) getCurrentCreds(state *State) []vault.Credential {
	if state.VaultScope == "global" {
		return s.creds
	}
	return s.projectCreds
}

// unlockVault attempts to unlock the vault with the current passphrase or identity.
func (s *VaultSettings) unlockVault(state *State) {
	scope := state.VaultUnlockingScope
	path := s.globalPath
	if scope == "project" && s.projectPath != "" {
		path = s.projectPath
	}

	// Check if vault file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		state.VaultState = "error"
		s.message = i18n.T("settings.vault.error.file_not_found")
		state.VaultPassphrase = ""
		return
	}

	// For project vault, check if identity-based (has recipients.txt)
	if scope == "project" && s.IsProjectVaultIdentityBased() {
		s.unlockWithIdentity(state, path)
		return
	}

	// Otherwise, use passphrase-based unlock
	s.unlockWithPassphrase(state, path)
}

// unlockWithPassphrase unlocks a vault using a passphrase.
func (s *VaultSettings) unlockWithPassphrase(state *State, path string) {
	scope := state.VaultUnlockingScope

	// Create encryptor from passphrase
	encryptor, err := vault.NewAgeEncryptorFromPassphrase(state.VaultPassphrase)
	if err != nil {
		state.VaultState = "error"
		s.message = i18n.T("settings.vault.error.create_encryptor", err)
		state.VaultPassphrase = ""
		return
	}

	// Load vault
	storage, err := vault.NewAgeStorage(path, encryptor)
	if err != nil {
		state.VaultState = "error"
		s.message = i18n.T("settings.vault.error.open", err)
		state.VaultPassphrase = ""
		return
	}

	// Store based on scope
	if scope == "global" {
		s.storage = storage
	} else {
		s.projectStore = storage
	}

	// Clear passphrase from memory
	state.VaultPassphrase = ""

	creds, err := storage.List(context.Background(), vault.CredentialFilter{IncludeSecrets: true})
	if err != nil {
		state.VaultState = "error"
		s.message = i18n.T("settings.vault.error.load_credentials", err)
		return
	}

	if scope == "global" {
		s.creds = creds
	} else {
		s.projectCreds = creds
	}
	s.persistUnlockedCredentials(creds)
	s.loaded = true
	state.VaultState = "unlocked"
	state.VaultSelectedCred = 0
	state.VaultScrollOffset = 0
	s.message = ""

	// Notify that vault is unlocked
	s.notifyVaultUnlock("")
}

// unlockWithIdentity unlocks a team vault using the user's identity.
func (s *VaultSettings) unlockWithIdentity(state *State, path string) {
	// Check if identity exists
	if !vault.IdentityExists(s.identityPath) {
		state.VaultState = "error"
		s.message = i18n.T("settings.vault.error.identity_missing")
		return
	}

	// Load recipients for display
	recipients, err := vault.ParseRecipientsWithComments(s.recipientsPath)
	if err != nil {
		state.VaultState = "error"
		s.message = i18n.T("settings.vault.error.load_recipients", err)
		return
	}
	s.recipients = recipients

	// Create multi-recipient storage
	storage, err := vault.NewMultiRecipientStorageFromFiles(path, s.identityPath, s.recipientsPath)
	if err != nil {
		state.VaultState = "error"
		s.message = i18n.T("settings.vault.error.open", err)
		return
	}

	// Store
	s.multiRecipientStore = storage

	// Load credentials
	creds, err := storage.List(context.Background(), vault.CredentialFilter{IncludeSecrets: true})
	if err != nil {
		state.VaultState = "error"
		s.message = i18n.T("settings.vault.error.load_credentials", err)
		return
	}

	s.projectCreds = creds
	s.persistUnlockedCredentials(creds)
	s.loaded = true
	state.VaultState = "unlocked"
	state.VaultSelectedCred = 0
	state.VaultScrollOffset = 0
	s.message = i18n.T("settings.vault.identity_unlocked", len(recipients))

	// Notify that vault is unlocked
	s.notifyVaultUnlock("")
}

// loadCredentials reloads credentials from the vault.
func (s *VaultSettings) loadCredentials(state *State) {
	if state.VaultScope == "project" && s.multiRecipientStore != nil {
		creds, err := s.multiRecipientStore.List(context.Background(), vault.CredentialFilter{})
		if err != nil {
			s.message = i18n.T("settings.vault.error.load_credentials", err)
			return
		}
		s.projectCreds = creds
		s.message = i18n.T("settings.vault.refreshed", len(creds))
		return
	}

	storage := s.storage
	if state.VaultScope == "project" {
		storage = s.projectStore
	}

	if storage == nil {
		s.message = i18n.T("settings.vault.not_unlocked")
		return
	}

	creds, err := storage.List(context.Background(), vault.CredentialFilter{})
	if err != nil {
		s.message = i18n.T("settings.vault.error.load_credentials", err)
		return
	}

	if state.VaultScope == "global" {
		s.creds = creds
	} else {
		s.projectCreds = creds
	}
	s.message = i18n.T("settings.vault.refreshed", len(creds))
}

// Render renders the vault settings panel.
func (s *VaultSettings) Render(state *State, width, height int) string {
	s.state = state
	var b strings.Builder

	// Title
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#39D2C0"))
	title := titleStyle.Render(i18n.T("settings.vault.title"))
	b.WriteString(title)
	b.WriteString("\n\n")

	// Scope switcher
	scopeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#39D2C0")).Bold(true)
	inactiveStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	if state.VaultScope == "global" {
		b.WriteString(scopeStyle.Render(i18n.T("settings.vault.scope.global_selected")))
		b.WriteString("  ")
		b.WriteString(inactiveStyle.Render(i18n.T("settings.vault.scope.project")))
	} else {
		b.WriteString(inactiveStyle.Render(i18n.T("settings.vault.scope.global")))
		b.WriteString("  ")
		b.WriteString(scopeStyle.Render(i18n.T("settings.vault.scope.project_selected")))
	}
	b.WriteString("\n\n")

	// Vault file path
	pathStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	currentPath := s.globalPath
	if state.VaultScope == "project" && s.projectPath != "" {
		currentPath = s.projectPath
	} else if state.VaultScope == "global" && s.transparentStore != nil && s.storage == nil {
		currentPath = s.transparentPath
	}
	b.WriteString(pathStyle.Render(i18n.T("settings.vault.path", currentPath)))
	b.WriteString("\n\n")

	// Check if vault file exists
	if _, err := os.Stat(currentPath); os.IsNotExist(err) &&
		!(state.VaultScope == "global" && s.transparentStore != nil) {
		b.WriteString(i18n.T("settings.vault.not_initialized"))
		if state.VaultScope == "global" {
			b.WriteString(i18n.T("settings.vault.init_global"))
			b.WriteString(i18n.T("settings.vault.add_global_after_init"))
		} else {
			b.WriteString(i18n.T("settings.vault.project_not_found"))
			b.WriteString(i18n.T("settings.vault.init_project"))
			b.WriteString(i18n.T("settings.vault.add_project_after_init"))
		}
		b.WriteString("\n")
		b.WriteString(i18n.T("settings.vault.switch_scope"))
		return b.String()
	}

	// Render based on state
	switch state.VaultState {
	case "locked":
		s.renderLockedState(&b, state)
	case "unlocking":
		s.renderUnlockingState(&b, state)
	case "unlocked":
		s.renderUnlockedState(&b, state)
	case "error":
		s.renderErrorState(&b, state)
	}

	return b.String()
}

func (s *VaultSettings) renderLockedState(b *strings.Builder, state *State) {
	if state.VaultScope == "global" && s.transparentStore != nil && s.transparentLocked {
		b.WriteString(i18n.T("settings.vault.transparent_disabled"))
		b.WriteString(i18n.T("settings.vault.enable_transparent_hint"))
		return
	}
	// Check if project vault uses identity encryption
	if state.VaultScope == "project" && s.IsProjectVaultIdentityBased() {
		// Load recipients for display
		recipients, err := vault.ParseRecipientsWithComments(s.recipientsPath)
		if err == nil {
			b.WriteString(i18n.T("settings.vault.team_title"))
			b.WriteString(i18n.T("settings.vault.recipients_count", len(recipients)))

			// Show recipient list (truncated)
			userPubKey := s.GetIdentityPublicKey()
			for i, r := range recipients {
				if i >= 5 {
					b.WriteString(i18n.T("settings.vault.recipients_more", len(recipients)-5))
					break
				}
				// Highlight if this is the user's key
				marker := "  "
				if r.PublicKey == userPubKey {
					marker = "> " // User's key
				}
				label := r.PublicKey
				if len(label) > 20 {
					label = label[:20] + "..."
				}
				if r.Comment != "" {
					b.WriteString(fmt.Sprintf("%s%s (%s)\n", marker, label, r.Comment))
				} else {
					b.WriteString(fmt.Sprintf("%s%s\n", marker, label))
				}
			}
			b.WriteString("\n")

			// Check if user has identity
			if s.HasIdentity() {
				if userPubKey != "" {
					// Check if user's key is in recipients
					found := false
					for _, r := range recipients {
						if r.PublicKey == userPubKey {
							found = true
							break
						}
					}
					if found {
						b.WriteString(i18n.T("settings.vault.unlock_identity_hint"))
					} else {
						b.WriteString(i18n.T("settings.vault.identity_not_recipient"))
						b.WriteString(i18n.T("settings.vault.ask_add_public_key"))
						b.WriteString(i18n.T("settings.vault.public_key", userPubKey))
					}
				}
			} else {
				b.WriteString(i18n.T("settings.vault.identity_not_found"))
				b.WriteString(i18n.T("settings.vault.init_global"))
			}
		}
		b.WriteString(i18n.T("settings.vault.switch_scope"))
	} else {
		// Standard passphrase-based vault
		b.WriteString(i18n.T("settings.vault.locked_body"))
		b.WriteString(i18n.T("settings.vault.unlock_hint"))
	}

	if s.message != "" {
		b.WriteString("\n")
		errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff6666"))
		b.WriteString(errorStyle.Render(s.message))
		b.WriteString("\n")
	}
}

func (s *VaultSettings) renderUnlockingState(b *strings.Builder, state *State) {
	scope := state.VaultUnlockingScope
	if scope == "" {
		scope = state.VaultScope
	}
	scopeLabel := i18n.T("settings.vault.scope.global")
	if scope == "project" {
		scopeLabel = i18n.T("settings.vault.scope.project")
	}
	b.WriteString(i18n.T("settings.vault.passphrase_prompt", scopeLabel))

	// Show masked passphrase input
	inputStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#39D2C0")).
		Padding(0, 1).
		Width(60)

	pass := []rune(state.VaultPassphrase)
	masked := strings.Repeat("*", len(pass))
	cursorPos := state.VaultCursorPos

	// Build input with cursor
	var inputText strings.Builder
	for i, ch := range masked {
		if i == cursorPos {
			// Show cursor as block on current position
			inputText.WriteString("\u2588")
		}
		inputText.WriteRune(ch)
	}
	if cursorPos >= len(masked) {
		inputText.WriteString("\u2588") // Cursor at end
	}

	b.WriteString(inputStyle.Render(inputText.String()))
	b.WriteString("\n\n")

	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Italic(true)
	b.WriteString(hintStyle.Render(i18n.T("settings.vault.passphrase_hint")))
	b.WriteString("\n")

	if s.message != "" {
		b.WriteString("\n")
		errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff6666"))
		b.WriteString(errorStyle.Render(s.message))
		b.WriteString("\n")
	}
}

func (s *VaultSettings) renderUnlockedState(b *strings.Builder, state *State) {
	creds := s.getCurrentCreds(state)

	if !s.loaded {
		b.WriteString(i18n.T("settings.vault.loading"))
		return
	}

	if len(creds) == 0 {
		b.WriteString(i18n.T("settings.vault.empty"))
		if state.VaultScope == "global" {
			b.WriteString(i18n.T("settings.vault.add_global"))
		} else {
			b.WriteString(i18n.T("settings.vault.add_project"))
		}
		b.WriteString(i18n.T("settings.vault.empty_hint"))
		return
	}

	// Credential list
	b.WriteString(i18n.T("settings.vault.stored_count", len(creds)))

	// Header
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#888888"))
	b.WriteString(headerStyle.Render(fmt.Sprintf("%-20s %-15s %-10s",
		i18n.T("settings.vault.column.id"),
		i18n.T("settings.vault.column.kind"),
		i18n.T("settings.vault.column.scope"))))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("-", 55) + "\n")

	// Credentials with selection
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#39D2C0")).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cccccc"))

	for i, c := range creds {
		line := fmt.Sprintf("%-20s %-15s %-10s", c.ID, c.Kind, c.Scope)
		if i == state.VaultSelectedCred {
			b.WriteString(selectedStyle.Render("> " + line))
		} else {
			b.WriteString(normalStyle.Render("  " + line))
		}
		b.WriteString("\n")
	}

	b.WriteString(i18n.T("settings.vault.list_hint"))

	if s.message != "" {
		b.WriteString("\n")
		infoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#66cc66"))
		b.WriteString(infoStyle.Render(s.message))
		b.WriteString("\n")
	}
}

func (s *VaultSettings) renderErrorState(b *strings.Builder, state *State) {
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff6666")).Bold(true)
	b.WriteString(errorStyle.Render(i18n.T("settings.vault.error", s.message)))
	b.WriteString("\n\n")
	b.WriteString(i18n.T("settings.vault.error_hint"))
}

// Update handles tea.Msg updates (for async operations).
func (s *VaultSettings) Update(msg tea.Msg) tea.Cmd {
	return nil
}

// Init initializes the panel.
func (s *VaultSettings) Init() tea.Cmd {
	return nil
}

// GetCredentialCount returns the number of currently loaded credentials.
func (s *VaultSettings) GetCredentialCount() int {
	if s.transparentStore != nil {
		creds, _ := s.transparentStore.List(context.Background(), vault.CredentialFilter{})
		return len(creds)
	}
	return len(s.creds)
}
