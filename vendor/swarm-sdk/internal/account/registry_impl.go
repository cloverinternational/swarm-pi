package account

import (
	"context"
	"fmt"
	"maps"
	"os"
	"sort"
	"sync"
	"time"
)

// RegistryImpl is the concrete implementation of AccountRegistry
type RegistryImpl struct {
	mu        sync.RWMutex
	config    RegistryConfig
	accounts  map[string]AccountIdentity   // key = "provider::account_id"
	tokens    map[string]*OAuthToken       // key = "provider::account_id"
	defaults  map[string]string            // provider -> default account_id
	bindings  map[string]CredentialBinding // key = "provider::account_id"
	refreshFn TokenRefreshFunc
	storage   RegistryStorage
}

// RegistryStorage handles file persistence
type RegistryStorage interface {
	// LoadAccounts loads the registry from storage
	LoadAccounts() (map[string]AccountIdentity, map[string]string, error)

	// SaveAccounts persists the registry to storage
	SaveAccounts(accounts map[string]AccountIdentity, defaults map[string]string) error

	// LoadToken loads a token from storage
	LoadToken(accountKey string) (*OAuthToken, error)

	// SaveToken persists a token to storage
	SaveToken(accountKey string, token *OAuthToken) error

	// DeleteToken removes a token from storage
	DeleteToken(accountKey string) error

	// DeleteAccount removes an account completely
	DeleteAccount(accountKey string) error
}

// NewRegistryImpl creates a new registry implementation
func NewRegistryImpl(config RegistryConfig, refreshFn TokenRefreshFunc) (*RegistryImpl, error) {
	if config.PersistenceDir == "" {
		return nil, fmt.Errorf("persistence directory required")
	}

	// Create persistence directory if needed
	if err := os.MkdirAll(config.PersistenceDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create persistence directory: %w", err)
	}

	storage := NewFileStorage(config.PersistenceDir)

	reg := &RegistryImpl{
		config:    config,
		accounts:  make(map[string]AccountIdentity),
		tokens:    make(map[string]*OAuthToken),
		defaults:  make(map[string]string),
		bindings:  make(map[string]CredentialBinding),
		refreshFn: refreshFn,
		storage:   storage,
	}

	// Load existing accounts
	if err := reg.load(); err != nil {
		return nil, fmt.Errorf("failed to load registry: %w", err)
	}

	return reg, nil
}

// load loads accounts from storage
func (r *RegistryImpl) load() error {
	accounts, defaults, err := r.storage.LoadAccounts()
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	r.accounts = accounts
	r.defaults = defaults

	// Load tokens for each account
	for key := range accounts {
		token, err := r.storage.LoadToken(key)
		if err == nil && token != nil {
			r.tokens[key] = token
		}
	}

	return nil
}

// RegisterAccount registers a new account
func (r *RegistryImpl) RegisterAccount(ctx context.Context, identity AccountIdentity, token *OAuthToken) error {
	if err := ValidateIdentity(identity); err != nil {
		return NewRegistryError(ErrorCodeInvalidIdentity, "invalid identity", err)
	}
	if token == nil {
		return NewRegistryError(ErrorCodeInvalidToken, "token cannot be nil", nil)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	key := identity.Key()

	// Check max accounts limit
	if r.config.MaxAccountsPerProvider > 0 {
		provider := identity.Provider()
		count := 0
		for existingKey := range r.accounts {
			if keyProvider(existingKey) == provider {
				count++
			}
		}
		if count >= r.config.MaxAccountsPerProvider {
			// Skip this check if account already exists (update case)
			if _, exists := r.accounts[key]; !exists {
				return NewRegistryError(ErrorCodeMaxAccountsExceeded,
					fmt.Sprintf("max accounts per provider exceeded: %d", r.config.MaxAccountsPerProvider),
					nil)
			}
		}
	}

	// Store account
	r.accounts[key] = identity
	r.tokens[key] = token

	// Set as default if it's the first account for this provider
	provider := identity.Provider()
	if _, hasDefault := r.defaults[provider]; !hasDefault {
		r.defaults[provider] = identity.ID()
	}

	// Persist
	if err := r.storage.SaveToken(key, token); err != nil {
		return NewRegistryError(ErrorCodePersistenceFailed, "failed to save token", err)
	}
	if err := r.storage.SaveAccounts(r.accounts, r.defaults); err != nil {
		return NewRegistryError(ErrorCodePersistenceFailed, "failed to save registry", err)
	}

	// Clear binding cache for this account
	delete(r.bindings, key)

	return nil
}

// GetAccount retrieves an account
func (r *RegistryImpl) GetAccount(provider, accountID string) (AccountIdentity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := makeKey(provider, accountID)
	identity, exists := r.accounts[key]
	if !exists {
		return nil, NewRegistryError(ErrorCodeAccountNotFound,
			fmt.Sprintf("account not found: %s", key), nil)
	}
	return identity, nil
}

// ListAccounts lists accounts for a provider
func (r *RegistryImpl) ListAccounts(provider string) ([]AccountIdentity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var accounts []AccountIdentity
	for key, identity := range r.accounts {
		if keyProvider(key) == provider {
			accounts = append(accounts, identity)
		}
	}

	// Sort by last used
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].LastUsed().After(accounts[j].LastUsed())
	})

	return accounts, nil
}

// ListAllAccounts lists all accounts
func (r *RegistryImpl) ListAllAccounts() ([]AccountIdentity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	accounts := make([]AccountIdentity, 0, len(r.accounts))
	for _, identity := range r.accounts {
		accounts = append(accounts, identity)
	}

	// Sort by provider, then by last used
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Provider() != accounts[j].Provider() {
			return accounts[i].Provider() < accounts[j].Provider()
		}
		return accounts[i].LastUsed().After(accounts[j].LastUsed())
	})

	return accounts, nil
}

// SetDefaultAccount sets the default account for a provider
func (r *RegistryImpl) SetDefaultAccount(provider, accountID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := makeKey(provider, accountID)
	if _, exists := r.accounts[key]; !exists {
		return NewRegistryError(ErrorCodeAccountNotFound,
			fmt.Sprintf("account not found: %s", key), nil)
	}

	r.defaults[provider] = accountID

	if err := r.storage.SaveAccounts(r.accounts, r.defaults); err != nil {
		return NewRegistryError(ErrorCodePersistenceFailed, "failed to save registry", err)
	}

	return nil
}

// GetDefaultAccount gets the default account for a provider
func (r *RegistryImpl) GetDefaultAccount(provider string) (AccountIdentity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	accountID, hasDefault := r.defaults[provider]
	if !hasDefault {
		return nil, NewRegistryError(ErrorCodeNoDefaultAccount,
			fmt.Sprintf("no default account for provider: %s", provider), nil)
	}

	key := makeKey(provider, accountID)
	identity, exists := r.accounts[key]
	if !exists {
		return nil, NewRegistryError(ErrorCodeAccountNotFound,
			fmt.Sprintf("default account not found: %s", key), nil)
	}

	return identity, nil
}

// RemoveAccount removes an account
func (r *RegistryImpl) RemoveAccount(provider, accountID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := makeKey(provider, accountID)
	if _, exists := r.accounts[key]; !exists {
		return NewRegistryError(ErrorCodeAccountNotFound,
			fmt.Sprintf("account not found: %s", key), nil)
	}

	delete(r.accounts, key)
	delete(r.tokens, key)
	delete(r.bindings, key)

	// If this was the default, pick next most recent
	if r.defaults[provider] == accountID {
		var nextDefault *AccountIdentity
		var newest time.Time
		for existingKey, identity := range r.accounts {
			if keyProvider(existingKey) == provider {
				if identity.LastUsed().After(newest) {
					nextDefault = &identity
					newest = identity.LastUsed()
				}
			}
		}
		if nextDefault != nil {
			r.defaults[provider] = (*nextDefault).ID()
		} else {
			delete(r.defaults, provider)
		}
	}

	if err := r.storage.DeleteAccount(key); err != nil {
		return NewRegistryError(ErrorCodePersistenceFailed, "failed to delete account", err)
	}
	if err := r.storage.SaveAccounts(r.accounts, r.defaults); err != nil {
		return NewRegistryError(ErrorCodePersistenceFailed, "failed to save registry", err)
	}

	return nil
}

// UpdateLastUsed updates last used time
func (r *RegistryImpl) UpdateLastUsed(provider, accountID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := makeKey(provider, accountID)
	identity, exists := r.accounts[key]
	if !exists {
		return NewRegistryError(ErrorCodeAccountNotFound,
			fmt.Sprintf("account not found: %s", key), nil)
	}

	// Update the lastUsed by creating a new identity with updated time
	// This requires the identity to be mutable or we need to track it separately
	if std, ok := identity.(*StandardAccountIdentity); ok {
		std.Used = time.Now()
	}

	if err := r.storage.SaveAccounts(r.accounts, r.defaults); err != nil {
		return NewRegistryError(ErrorCodePersistenceFailed, "failed to save registry", err)
	}

	return nil
}

// GetToken gets a token
func (r *RegistryImpl) GetToken(provider, accountID string) (*OAuthToken, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := makeKey(provider, accountID)
	token, exists := r.tokens[key]
	if !exists {
		return nil, NewRegistryError(ErrorCodeTokenNotFound,
			fmt.Sprintf("token not found: %s", key), nil)
	}
	return token, nil
}

// UpdateToken updates a token
func (r *RegistryImpl) UpdateToken(provider, accountID string, token *OAuthToken) error {
	if token == nil {
		return NewRegistryError(ErrorCodeInvalidToken, "token cannot be nil", nil)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	key := makeKey(provider, accountID)
	if _, exists := r.accounts[key]; !exists {
		return NewRegistryError(ErrorCodeAccountNotFound,
			fmt.Sprintf("account not found: %s", key), nil)
	}

	r.tokens[key] = token
	delete(r.bindings, key) // Clear binding cache

	if err := r.storage.SaveToken(key, token); err != nil {
		return NewRegistryError(ErrorCodePersistenceFailed, "failed to save token", err)
	}

	return nil
}

// IsExpired checks if token is expired
func (r *RegistryImpl) IsExpired(provider, accountID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := makeKey(provider, accountID)
	token, exists := r.tokens[key]
	if !exists || token == nil || token.ExpiresAt.IsZero() {
		return false // No expiry info means not expired
	}

	// Check with buffer
	bufferTime := token.ExpiresAt.Add(-r.config.TokenExpireBuffer)
	return time.Now().After(bufferTime)
}

// RefreshTokenIfNeeded refreshes token if needed
func (r *RegistryImpl) RefreshTokenIfNeeded(ctx context.Context, provider, accountID string) (*OAuthToken, error) {
	r.mu.Lock()
	key := makeKey(provider, accountID)
	identity, exists := r.accounts[key]
	token, tokenExists := r.tokens[key]
	r.mu.Unlock()

	if !exists {
		return nil, NewRegistryError(ErrorCodeAccountNotFound,
			fmt.Sprintf("account not found: %s", key), nil)
	}
	if !tokenExists || token == nil {
		return nil, NewRegistryError(ErrorCodeTokenNotFound,
			fmt.Sprintf("token not found: %s", key), nil)
	}

	// Check if refresh needed
	if !r.IsExpired(provider, accountID) {
		return token, nil
	}

	// Refresh token
	if r.refreshFn == nil {
		return nil, NewRegistryError(ErrorCodeTokenRefreshFailed,
			"no refresh function configured", nil)
	}

	newToken, err := r.refreshFn(ctx, identity, token)
	if err != nil {
		return nil, NewRegistryError(ErrorCodeTokenRefreshFailed,
			"token refresh failed", err)
	}

	// Store new token
	if err := r.UpdateToken(provider, accountID, newToken); err != nil {
		return nil, err
	}

	return newToken, nil
}

// GetOrCreateBinding gets or creates a credential binding
func (r *RegistryImpl) GetOrCreateBinding(account AccountProfile, refreshFn TokenRefreshFunc) (CredentialBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := makeKey(account.Identity().Provider(), account.Identity().ID())

	// Return cached binding if exists
	if binding, exists := r.bindings[key]; exists {
		return binding, nil
	}

	// Get token
	token, exists := r.tokens[key]
	if !exists {
		return nil, NewRegistryError(ErrorCodeTokenNotFound,
			fmt.Sprintf("token not found: %s", key), nil)
	}

	// Create binding
	binding := NewStandardCredentialBinding(account, token, refreshFn)
	r.bindings[key] = binding

	return binding, nil
}

// Helper functions

func makeKey(provider, accountID string) string {
	return provider + "::" + accountID
}

func keyProvider(key string) string {
	if i := len(key); i >= 2 {
		if key[i-2:i] == "::" {
			return key[:i-2]
		}
	}
	// Parse "provider::id"
	for i := 0; i < len(key)-1; i++ {
		if key[i:i+2] == "::" {
			return key[:i]
		}
	}
	return ""
}

// RegistrySnapshot provides a point-in-time view of the registry
type RegistrySnapshot struct {
	Accounts map[string]AccountIdentity
	Tokens   map[string]*OAuthToken
	Defaults map[string]string
	Snapshot time.Time
}

// Snapshot returns a copy of the current registry state
func (r *RegistryImpl) Snapshot() RegistrySnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	accounts := make(map[string]AccountIdentity)
	maps.Copy(accounts, r.accounts)

	tokens := make(map[string]*OAuthToken)
	for k, v := range r.tokens {
		if v != nil {
			// Deep copy token
			tokenCopy := *v
			tokens[k] = &tokenCopy
		}
	}

	defaults := make(map[string]string)
	maps.Copy(defaults, r.defaults)

	return RegistrySnapshot{
		Accounts: accounts,
		Tokens:   tokens,
		Defaults: defaults,
		Snapshot: time.Now(),
	}
}
