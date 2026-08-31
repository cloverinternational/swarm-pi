package account

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// ManagerImpl is the concrete implementation of AccountManager
type ManagerImpl struct {
	mu                 sync.RWMutex
	config             ManagerConfig
	registry           AccountRegistry
	profileStore       ProfileStore
	activeProfile      string
	fallbackChain      AccountFallbackChain
	switchHandlers     []AccountSwitchHandler
	refreshHandlers    []TokenRefreshHandler
	registeredHandlers []AccountRegisteredHandler
}

// NewManagerImpl creates a new account manager
func NewManagerImpl(config ManagerConfig) (*ManagerImpl, error) {
	if config.RegistryConfig.PersistenceDir == "" {
		return nil, fmt.Errorf("registry persistence directory required")
	}
	if config.ProfileStoreConfig.PersistenceDir == "" {
		return nil, fmt.Errorf("profile store persistence directory required")
	}

	// Create registry
	registry, err := NewRegistryImpl(config.RegistryConfig, config.TokenRefreshFunc)
	if err != nil {
		return nil, fmt.Errorf("failed to create registry: %w", err)
	}

	// Create profile store
	profileStore, err := NewProfileStoreImpl(config.ProfileStoreConfig, registry)
	if err != nil {
		return nil, fmt.Errorf("failed to create profile store: %w", err)
	}

	manager := &ManagerImpl{
		config:             config,
		registry:           registry,
		profileStore:       profileStore,
		activeProfile:      "",
		fallbackChain:      NewStandardAccountFallbackChain(),
		switchHandlers:     make([]AccountSwitchHandler, 0),
		refreshHandlers:    make([]TokenRefreshHandler, 0),
		registeredHandlers: make([]AccountRegisteredHandler, 0),
	}

	// Set active profile to default
	if defaultProfile, err := profileStore.GetDefaultProfile(); err == nil {
		manager.activeProfile = defaultProfile.ProfileName()
	}

	return manager, nil
}

// RegisterOAuthAccount registers a new OAuth account
func (m *ManagerImpl) RegisterOAuthAccount(ctx context.Context, provider string, token *OAuthToken) (AccountProfile, error) {
	if token == nil {
		return nil, NewManagerError(ErrorCodeRefreshFailed, "token cannot be nil", nil)
	}

	// Extract account ID from token
	accountID := m.extractAccountID(token)
	if accountID == "" {
		// Generate a unique ID if we can't extract from token
		accountID = "account-" + generateRandomID(8)
	}

	// Create identity
	identity := &StandardAccountIdentity{
		ProviderName: provider,
		AccountID:    accountID,
		Name:         extractDisplayName(token),
		Active:       true,
		Created:      time.Now(),
		Used:         time.Now(),
	}

	// Register in registry
	if err := m.registry.RegisterAccount(ctx, identity, token); err != nil {
		return nil, NewManagerError(ErrorCodeRefreshFailed, "failed to register account", err)
	}

	// Create a profile for this account if it's the first for this provider
	accounts, _ := m.registry.ListAccounts(provider)
	profileName := fmt.Sprintf("%s-%d", provider, len(accounts))

	// Get model to use
	model := "claude-opus-4-20250514"
	if provider == "openai" {
		model = "gpt-4o"
	} else if provider == "gemini" {
		model = "gemini-2.0-flash"
	}

	// Create profile
	profile, err := m.profileStore.CreateProfile(profileName, identity, model)
	if err != nil {
		// Remove account if profile creation fails
		_ = m.registry.RemoveAccount(provider, accountID)
		return nil, NewManagerError(ErrorCodeProfileNotFound, "failed to create profile", err)
	}

	// Set as active if it's the first
	if m.activeProfile == "" {
		m.activeProfile = profileName
		_ = m.profileStore.SetDefaultProfile(profileName)
	}

	// Call registered handlers
	m.callRegisteredHandlers(ctx, profile)

	return profile, nil
}

// GetActiveProfile returns the currently active profile
func (m *ManagerImpl) GetActiveProfile(ctx context.Context) (AccountProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.activeProfile == "" {
		return nil, NewManagerError(ErrorCodeNoActiveProfile, "no active profile set", nil)
	}

	profile, err := m.profileStore.GetProfile(m.activeProfile)
	if err != nil {
		return nil, NewManagerError(ErrorCodeProfileNotFound,
			fmt.Sprintf("active profile not found: %s", m.activeProfile), err)
	}

	return profile, nil
}

// SwitchAccount changes the active account/profile
func (m *ManagerImpl) SwitchAccount(ctx context.Context, provider, accountID string) error {
	m.mu.Lock()
	oldProfileName := m.activeProfile
	m.mu.Unlock()

	// Get the account
	_, err := m.registry.GetAccount(provider, accountID)
	if err != nil {
		return NewManagerError(ErrorCodeAccountNotAssigned, "account not found", err)
	}

	// Find a profile for this account
	profiles, err := m.profileStore.ListProfilesByProvider(provider)
	if err != nil {
		return NewManagerError(ErrorCodeProfileNotFound, "no profiles for provider", err)
	}

	var targetProfile AccountProfile
	for _, p := range profiles {
		if p.Identity().ID() == accountID {
			targetProfile = p
			break
		}
	}

	if targetProfile == nil {
		return NewManagerError(ErrorCodeAccountNotAssigned,
			fmt.Sprintf("no profile found for account: %s::%s", provider, accountID), nil)
	}

	m.mu.Lock()
	m.activeProfile = targetProfile.ProfileName()
	newProfile := targetProfile
	m.mu.Unlock()

	// Update last used
	if err := m.registry.UpdateLastUsed(provider, accountID); err != nil {
		// Non-critical error - silently ignore
		_ = err
	}

	// Call switch handlers
	m.callSwitchHandlers(ctx, oldProfileName, newProfile)

	return nil
}

// GetAccountProfile returns profile for a specific account
func (m *ManagerImpl) GetAccountProfile(provider, accountID string) (AccountProfile, error) {
	// Get account
	_, err := m.registry.GetAccount(provider, accountID)
	if err != nil {
		return nil, NewManagerError(ErrorCodeAccountNotAssigned, "account not found", err)
	}

	// Find profile for this account
	profiles, err := m.profileStore.ListProfilesByProvider(provider)
	if err != nil {
		return nil, err
	}

	for _, p := range profiles {
		if p.Identity().ID() == accountID {
			return p, nil
		}
	}

	return nil, NewManagerError(ErrorCodeAccountNotAssigned,
		fmt.Sprintf("no profile found for account: %s::%s", provider, accountID), nil)
}

// RefreshAccountTokens refreshes tokens for all active accounts
func (m *ManagerImpl) RefreshAccountTokens(ctx context.Context) error {
	accounts, err := m.registry.ListAllAccounts()
	if err != nil {
		return NewManagerError(ErrorCodeRefreshFailed, "failed to list accounts", err)
	}

	var errors []error
	for _, account := range accounts {
		token, err := m.registry.RefreshTokenIfNeeded(ctx, account.Provider(), account.ID())
		if err != nil {
			errors = append(errors, err)
		} else if token != nil {
			// Call refresh handlers
			if profile, err := m.GetAccountProfile(account.Provider(), account.ID()); err == nil {
				m.callRefreshHandlers(ctx, profile, token)
			}
		}
	}

	if len(errors) > 0 {
		return NewManagerError(ErrorCodeRefreshFailed,
			fmt.Sprintf("failed to refresh %d tokens", len(errors)), errors[0])
	}

	return nil
}

// PruneStaleAccounts removes accounts unused for N days
func (m *ManagerImpl) PruneStaleAccounts(maxAgeDays int) error {
	if maxAgeDays <= 0 {
		return fmt.Errorf("max age days must be positive")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	accounts, err := m.registry.ListAllAccounts()
	if err != nil {
		return fmt.Errorf("failed to list accounts: %w", err)
	}

	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	for _, account := range accounts {
		if account.LastUsed().Before(cutoff) {
			// Remove this account
			if err := m.registry.RemoveAccount(account.Provider(), account.ID()); err != nil {
				// Non-critical error - silently ignore
				_ = err
			}
		}
	}

	return nil
}

// ListActiveAccounts returns accounts sorted by last use
func (m *ManagerImpl) ListActiveAccounts() ([]AccountProfile, error) {
	accounts, err := m.registry.ListAllAccounts()
	if err != nil {
		return nil, err
	}

	var profiles []AccountProfile
	for _, account := range accounts {
		if profile, err := m.GetAccountProfile(account.Provider(), account.ID()); err == nil {
			profiles = append(profiles, profile)
		}
	}

	return profiles, nil
}

// ListAccountsByProvider returns accounts for a provider
func (m *ManagerImpl) ListAccountsByProvider(provider string) ([]AccountProfile, error) {
	return m.profileStore.ListProfilesByProvider(provider)
}

// GetBinding returns a credential binding
func (m *ManagerImpl) GetBinding(provider, accountID string) (CredentialBinding, error) {
	profile, err := m.GetAccountProfile(provider, accountID)
	if err != nil {
		return nil, err
	}

	return m.registry.GetOrCreateBinding(profile, m.config.TokenRefreshFunc)
}

// OnAccountSwitch registers a switch handler
func (m *ManagerImpl) OnAccountSwitch(handler AccountSwitchHandler) error {
	if handler == nil {
		return fmt.Errorf("handler cannot be nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.switchHandlers = append(m.switchHandlers, handler)
	return nil
}

// OnTokenRefresh registers a refresh handler
func (m *ManagerImpl) OnTokenRefresh(handler TokenRefreshHandler) error {
	if handler == nil {
		return fmt.Errorf("handler cannot be nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshHandlers = append(m.refreshHandlers, handler)
	return nil
}

// OnAccountRegistered registers a registered handler
func (m *ManagerImpl) OnAccountRegistered(handler AccountRegisteredHandler) error {
	if handler == nil {
		return fmt.Errorf("handler cannot be nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registeredHandlers = append(m.registeredHandlers, handler)
	return nil
}

// Helper methods

func (m *ManagerImpl) extractAccountID(token *OAuthToken) string {
	// Try to extract from ID token (JWT)
	if token.IDToken != "" {
		// This would decode the JWT and extract the sub or account ID
		// For now, return empty to use generated ID
		return ""
	}
	return ""
}

func extractDisplayName(token *OAuthToken) string {
	// Could extract from email or other fields
	return ""
}

func (m *ManagerImpl) callSwitchHandlers(ctx context.Context, oldProfileName string, newProfile AccountProfile) {
	m.mu.RLock()
	handlers := make([]AccountSwitchHandler, len(m.switchHandlers))
	copy(handlers, m.switchHandlers)
	m.mu.RUnlock()

	// Get old profile if available
	var oldProfile AccountProfile
	if oldProfileName != "" {
		if p, err := m.profileStore.GetProfile(oldProfileName); err == nil {
			oldProfile = p
		}
	}

	for _, handler := range handlers {
		if err := handler.Handle(ctx, oldProfile, newProfile); err != nil {
			// Handler error - silently ignore
			_ = err
		}
	}
}

func (m *ManagerImpl) callRefreshHandlers(ctx context.Context, profile AccountProfile, token *OAuthToken) {
	m.mu.RLock()
	handlers := make([]TokenRefreshHandler, len(m.refreshHandlers))
	copy(handlers, m.refreshHandlers)
	m.mu.RUnlock()

	for _, handler := range handlers {
		if err := handler.Handle(ctx, profile, token); err != nil {
			// Handler error - silently ignore
			_ = err
		}
	}
}

func (m *ManagerImpl) callRegisteredHandlers(ctx context.Context, profile AccountProfile) {
	m.mu.RLock()
	handlers := make([]AccountRegisteredHandler, len(m.registeredHandlers))
	copy(handlers, m.registeredHandlers)
	m.mu.RUnlock()

	for _, handler := range handlers {
		if err := handler.Handle(ctx, profile); err != nil {
			// Handler error - silently ignore
			_ = err
		}
	}
}

// GetStats returns manager statistics
func (m *ManagerImpl) GetStats(ctx context.Context) (ManagerStats, error) {
	accounts, err := m.registry.ListAllAccounts()
	if err != nil {
		return ManagerStats{}, err
	}

	stats := ManagerStats{
		TotalAccounts:      len(accounts),
		AccountsByProvider: make(map[string]int),
		TotalProfiles:      m.profileStore.GetProfileCount(),
		ActiveProfile:      m.activeProfile,
	}

	// Count by provider
	for _, account := range accounts {
		stats.AccountsByProvider[account.Provider()]++
		if account.LastUsed().After(stats.LastActivity) {
			stats.LastActivity = account.LastUsed()
		}

		// Count stale (30+ days unused)
		if time.Since(account.LastUsed()) > 30*24*time.Hour {
			stats.StaleAccounts++
		}
	}

	return stats, nil
}

// GetRegistry returns the underlying registry (for advanced usage)
func (m *ManagerImpl) GetRegistry() AccountRegistry {
	return m.registry
}

// GetProfileStore returns the underlying profile store (for advanced usage)
func (m *ManagerImpl) GetProfileStore() ProfileStore {
	return m.profileStore
}

// GetFallbackChain returns the fallback chain
func (m *ManagerImpl) GetFallbackChain() AccountFallbackChain {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.fallbackChain
}

// SetFallbackChain sets the fallback chain
func (m *ManagerImpl) SetFallbackChain(chain AccountFallbackChain) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fallbackChain = chain
}

// generateRandomID generates a random hex string
func generateRandomID(length int) string {
	const charset = "0123456789abcdef"
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		// Fallback if rand fails
		return fmt.Sprintf("id%d", time.Now().UnixNano())
	}
	result := make([]byte, length)
	for i := range b {
		result[i] = charset[b[i]%byte(len(charset))]
	}
	return string(result)
}
