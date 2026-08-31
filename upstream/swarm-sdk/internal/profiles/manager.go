package profiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// ============================================================================
// MANAGER
// ============================================================================

// Manager handles loading, saving, and managing agent profiles.
//
// All state is persisted to <configDir>/agent_profiles.json.
// Thread safety: SAFE. All read/write methods are protected by an internal
// RWMutex so concurrent ResolveChain calls from parallel sub-agent
// spawns are safe alongside UI-initiated profile writes.
//
// Usage:
//
//	mgr := profiles.NewManager("")           // ~/.swarm/config/
//	mgr := profiles.NewManager("/etc/myapp") // custom config dir
//	chain, _ := mgr.ResolveChain(profiles.AliasMain)
type Manager struct {
	mu         sync.RWMutex
	config     ProfilesConfig
	configPath string
}

// NewManager creates a Manager, loading from disk or generating defaults.
//
// configDir controls where agent_profiles.json lives.
// Empty string → defaults to the canonical ~/.swarm/config/agent_profiles.json
func NewManager(configDir string) *Manager {
	var configPath string
	if configDir == "" {
		configPath = paths.AgentProfilesFile()
	} else {
		configPath = filepath.Join(configDir, "agent_profiles.json")
	}

	mgr := &Manager{configPath: configPath}
	config, err := mgr.load(configPath)
	if err != nil {
		config = ProfilesConfig{
			DefaultProfile: "balanced",
			Profiles:       GenerateBuiltinProfiles(),
		}
	}
	mgr.config = config
	return mgr
}

// ConfigPath returns the path to the backing JSON file.
func (m *Manager) ConfigPath() string { return m.configPath }

// ============================================================================
// PERSISTENCE
// ============================================================================

// load reads and returns a ProfilesConfig from disk.
// File-not-found → returns built-in defaults (not an error).
func (m *Manager) load(path string) (ProfilesConfig, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ProfilesConfig{
			DefaultProfile: "balanced",
			Profiles:       GenerateBuiltinProfiles(),
		}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return ProfilesConfig{}, fmt.Errorf("read %s: %w", path, err)
	}

	var config ProfilesConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return ProfilesConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}

	// Auto-migrate any profiles still in old Pointers format
	migrated := false
	for i := range config.Profiles {
		if len(config.Profiles[i].Roles) == 0 && len(config.Profiles[i].Pointers) > 0 {
			config.Profiles[i].MigratePointersToRoles()
			migrated = true
		}
	}

	if err := config.Validate(); err != nil {
		return ProfilesConfig{}, fmt.Errorf("invalid config in %s: %w", path, err)
	}

	if migrated {
		_ = m.SaveProfiles(config, path) // best-effort
	}

	return config, nil
}

// SaveProfiles writes config to disk atomically via atomicfile.Write
// (crash-safe temp+fsync+rename) under a cross-process advisory lock.
// Always writes in the new format (roles, not pointers). Indented JSON.
// File mode is 0600 — profiles embed provider/model routing treated as sensitive.
func (m *Manager) SaveProfiles(config ProfilesConfig, path string) error {
	if err := observability.GuardTestWrite(path); err != nil {
		return err
	}
	observability.RecordConfigWrite(path, "save")
	if err := config.Validate(); err != nil {
		return fmt.Errorf("refusing to save invalid config: %w", err)
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if err := atomicfile.WithLock(path, func() error {
		return atomicfile.Write(path, data)
	}); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Save persists the in-memory config.
// Caller must NOT hold m.mu.
func (m *Manager) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.SaveProfiles(m.config, m.configPath)
}

// Reload re-reads from disk, discarding in-memory changes.
func (m *Manager) Reload() error {
	config, err := m.load(m.configPath)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.config = config
	m.mu.Unlock()
	return nil
}

// LoadWithWarnings re-reads from disk and returns any non-fatal warnings.
//
// Currently the SDK does not emit warnings during normal load, so the
// returned slice is always empty. Future versions may warn about
// deprecated aliases, missing recommended roles, etc.
func (m *Manager) LoadWithWarnings() (*ProfilesConfig, []ProfileWarning, error) {
	if err := m.Reload(); err != nil {
		return nil, nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return &m.config, nil, nil
}

// GetConfig returns a pointer to the internal ProfilesConfig.
// Modifications affect manager state — call Save() to persist.
func (m *Manager) GetConfig() *ProfilesConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return &m.config
}

// ============================================================================
// READ OPERATIONS
// ============================================================================

// GetProfile returns a pointer to the in-memory profile with the given ID.
func (m *Manager) GetProfile(id string) (*AgentProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getProfile(id)
}

// getProfile is the unlocked internal version of GetProfile.
func (m *Manager) getProfile(id string) (*AgentProfile, error) {
	for i := range m.config.Profiles {
		if m.config.Profiles[i].ID == id {
			return &m.config.Profiles[i], nil
		}
	}
	return nil, ErrProfileNotFound
}

// GetActiveProfile returns the currently active profile.
func (m *Manager) GetActiveProfile() (*AgentProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.GetDefaultProfile()
}

// GetActiveProfileID returns the ID of the currently active profile.
func (m *Manager) GetActiveProfileID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.DefaultProfile
}

// ListProfiles returns a copy of all profiles.
func (m *Manager) ListProfiles() []AgentProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]AgentProfile, len(m.config.Profiles))
	copy(out, m.config.Profiles)
	return out
}

// ============================================================================
// WRITE OPERATIONS
// ============================================================================

// CreateProfile creates a new empty profile and saves to disk.
// The new profile has no roles — the user fills them in via the UI.
func (m *Manager) CreateProfile(name, description string) (*AgentProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if name == "" {
		return nil, ErrEmptyProfileName
	}

	baseID := toKebabCase(name)
	id := fmt.Sprintf("%s-%d", baseID, time.Now().UnixNano())

	for _, p := range m.config.Profiles {
		if p.ID == id {
			return nil, fmt.Errorf("profile ID collision: %s", id)
		}
	}

	now := time.Now()
	profile := AgentProfile{
		ID:          id,
		Name:        name,
		Description: description,
		Icon:        "●",
		Color:       "#6B7280",
		IsDefault:   false,
		Roles:       make(map[ModelAlias]RoleConfig),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	m.config.Profiles = append(m.config.Profiles, profile)
	if err := m.SaveProfiles(m.config, m.configPath); err != nil {
		m.config.Profiles = m.config.Profiles[:len(m.config.Profiles)-1]
		return nil, fmt.Errorf("save: %w", err)
	}

	return &m.config.Profiles[len(m.config.Profiles)-1], nil
}

// UpdateProfile replaces a profile in-place and saves to disk.
// ID and CreatedAt are always preserved from the existing profile.
func (m *Manager) UpdateProfile(id string, updated AgentProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := -1
	for i := range m.config.Profiles {
		if m.config.Profiles[i].ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return ErrProfileNotFound
	}

	updated.ID = id
	updated.CreatedAt = m.config.Profiles[idx].CreatedAt
	updated.UpdatedAt = time.Now()

	// Roles are always enabled — enforce invariant before persisting.
	for alias, rc := range updated.Roles {
		if !rc.Enabled {
			rc.Enabled = true
			updated.Roles[alias] = rc
		}
	}

	if err := updated.Validate(); err != nil {
		return fmt.Errorf("invalid profile: %w", err)
	}

	m.config.Profiles[idx] = updated
	if err := m.SaveProfiles(m.config, m.configPath); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	return nil
}

// SetRoleInProfile updates a single role within a profile and saves.
// This is the granular edit operation used by the pool editor UI.
func (m *Manager) SetRoleInProfile(profileID string, alias ModelAlias, rc RoleConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	profile, err := m.getProfile(profileID)
	if err != nil {
		return err
	}
	rc.Enabled = true // roles are always enabled
	profile.SetRole(alias, rc)
	profile.UpdatedAt = time.Now()

	if err := m.SaveProfiles(m.config, m.configPath); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	return nil
}

// SetRetryPolicyInProfile updates the retry policy for a profile and saves.
func (m *Manager) SetRetryPolicyInProfile(profileID string, policy *RetryPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	profile, err := m.getProfile(profileID)
	if err != nil {
		return err
	}
	profile.RetryPolicy = policy
	profile.UpdatedAt = time.Now()

	if err := m.SaveProfiles(m.config, m.configPath); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	return nil
}

// DeleteProfile removes a profile. Cannot delete the default or last profile.
func (m *Manager) DeleteProfile(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.config.DefaultProfile == id {
		return fmt.Errorf("cannot delete the active profile — switch profiles first")
	}
	if len(m.config.Profiles) == 1 {
		return fmt.Errorf("cannot delete the last profile")
	}

	idx := -1
	for i := range m.config.Profiles {
		if m.config.Profiles[i].ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return ErrProfileNotFound
	}

	m.config.Profiles = append(m.config.Profiles[:idx], m.config.Profiles[idx+1:]...)
	if err := m.SaveProfiles(m.config, m.configPath); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	return nil
}

// SetDefaultProfile switches the active profile.
func (m *Manager) SetDefaultProfile(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.getProfile(id); err != nil {
		return fmt.Errorf("profile not found: %w", err)
	}

	old := m.config.DefaultProfile
	m.config.DefaultProfile = id
	for i := range m.config.Profiles {
		m.config.Profiles[i].IsDefault = (m.config.Profiles[i].ID == id)
	}

	if err := m.SaveProfiles(m.config, m.configPath); err != nil {
		m.config.DefaultProfile = old
		for i := range m.config.Profiles {
			m.config.Profiles[i].IsDefault = (m.config.Profiles[i].ID == old)
		}
		return fmt.Errorf("save: %w", err)
	}
	return nil
}

// RenameProfile renames a profile and saves to disk.
// The profile ID is NOT changed - only the display name is updated.
func (m *Manager) RenameProfile(id string, newName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if newName == "" {
		return ErrEmptyProfileName
	}

	profile, err := m.getProfile(id)
	if err != nil {
		return fmt.Errorf("profile not found: %w", err)
	}

	profile.Name = newName
	profile.UpdatedAt = time.Now()

	if err := m.SaveProfiles(m.config, m.configPath); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	return nil
}

// CloneProfile deep-copies a profile under a new name and saves.
func (m *Manager) CloneProfile(sourceID, newName string) (*AgentProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	src, err := m.getProfile(sourceID)
	if err != nil {
		return nil, fmt.Errorf("source not found: %w", err)
	}

	now := time.Now()
	id := fmt.Sprintf("%s-%d", toKebabCase(newName), now.UnixNano())

	clone := AgentProfile{
		ID:          id,
		Name:        newName,
		Description: src.Description + " (clone)",
		Icon:        src.Icon,
		Color:       src.Color,
		IsDefault:   false,
		RetryPolicy: cloneRetryPolicy(src.RetryPolicy),
		Roles:       make(map[ModelAlias]RoleConfig, len(src.Roles)),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	for alias, rc := range src.Roles {
		cloned := RoleConfig{
			Chain:        rc.Chain.Clone(),
			Enabled:      rc.Enabled,
			SystemPrompt: rc.SystemPrompt,
		}
		if rc.Capabilities != nil {
			capCopy := *rc.Capabilities
			cloned.Capabilities = &capCopy
		}
		clone.Roles[alias] = cloned
	}

	if err := clone.Validate(); err != nil {
		return nil, fmt.Errorf("invalid clone: %w", err)
	}

	m.config.Profiles = append(m.config.Profiles, clone)
	if err := m.SaveProfiles(m.config, m.configPath); err != nil {
		m.config.Profiles = m.config.Profiles[:len(m.config.Profiles)-1]
		return nil, fmt.Errorf("save: %w", err)
	}

	return &m.config.Profiles[len(m.config.Profiles)-1], nil
}

// ============================================================================
// ALIAS RESOLUTION
// ============================================================================

// ResolveChain resolves an alias to its full fallback.Chain using the active profile.
// This is the primary resolution path — pass the returned chain to fallback.Execute().
// Thread-safe: uses RWMutex so concurrent sub-agent spawns can resolve in parallel.
func (m *Manager) ResolveChain(alias ModelAlias) (*fallback.Chain, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	profile, err := m.config.GetDefaultProfile()
	if err != nil {
		return nil, fmt.Errorf("no active profile: %w", err)
	}

	rc, ok := profile.GetRole(alias)
	if !ok {
		return nil, fmt.Errorf("alias %s not configured in profile %s", alias, profile.Name)
	}
	if rc.Chain == nil || rc.Chain.IsEmpty() {
		return nil, fmt.Errorf("role %s has an empty chain in profile %s", alias, profile.Name)
	}

	return rc.Chain, nil
}

// ResolveChainForProfile resolves an alias within a specific profile by ID.
// Thread-safe: uses RLock.
func (m *Manager) ResolveChainForProfile(profileID string, alias ModelAlias) (*fallback.Chain, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	profile, err := m.getProfile(profileID)
	if err != nil {
		return nil, err
	}

	rc, ok := profile.GetRole(alias)
	if !ok {
		return nil, fmt.Errorf("alias %s not configured in profile %s", alias, profile.Name)
	}
	if rc.Chain == nil || rc.Chain.IsEmpty() {
		return nil, fmt.Errorf("role %s has an empty chain in profile %s", alias, profile.Name)
	}

	return rc.Chain, nil
}

// ResolveRoleConfig resolves an alias to its full RoleConfig using the active profile.
// Thread-safe: uses RLock.
func (m *Manager) ResolveRoleConfig(alias ModelAlias) (RoleConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	profile, err := m.config.GetDefaultProfile()
	if err != nil {
		return RoleConfig{}, fmt.Errorf("no active profile: %w", err)
	}

	rc, ok := profile.GetRole(alias)
	if !ok {
		return RoleConfig{}, fmt.Errorf("alias %s not configured in profile %s", alias, profile.Name)
	}

	return rc, nil
}

// ResolveAlias resolves an alias to a ModelPointer using the active profile.
// Legacy compatibility — returns the primary model of the role's chain.
// New code should use ResolveChain() to get the full pool.
func (m *Manager) ResolveAlias(alias ModelAlias) (ModelPointer, error) {
	rc, err := m.ResolveRoleConfig(alias)
	if err != nil {
		return ModelPointer{}, err
	}
	ptr := rc.ToModelPointer()
	if ptr.Provider == "" {
		return ModelPointer{}, fmt.Errorf("alias %s has no primary provider", alias)
	}
	return ptr, nil
}

// ============================================================================
// HELPERS
// ============================================================================

func toKebabCase(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
