package account

import (
	"context"
	"fmt"
	"time"
)

// AccountRegistry is the central registry for managing accounts across providers
// It provides the primary interface for account CRUD operations
type AccountRegistry interface {
	// RegisterAccount adds or updates an account in the registry
	// If account already exists (same provider + ID), it updates the token
	RegisterAccount(ctx context.Context, identity AccountIdentity, token *OAuthToken) error

	// GetAccount retrieves an account by provider and account ID
	// Returns error if account not found
	GetAccount(provider, accountID string) (AccountIdentity, error)

	// ListAccounts returns all registered accounts for a specific provider
	// Ordered by last used time (most recent first)
	ListAccounts(provider string) ([]AccountIdentity, error)

	// ListAllAccounts returns all registered accounts across all providers
	// Ordered by provider, then by last used time
	ListAllAccounts() ([]AccountIdentity, error)

	// SetDefaultAccount marks an account as the default for its provider
	// If account doesn't exist, returns error
	SetDefaultAccount(provider, accountID string) error

	// GetDefaultAccount returns the default account for a provider
	// Returns error if no accounts exist for provider
	GetDefaultAccount(provider string) (AccountIdentity, error)

	// RemoveAccount deregisters an account from the registry
	// If account was default, the next most recently used becomes default
	RemoveAccount(provider, accountID string) error

	// UpdateLastUsed refreshes the last access timestamp for an account
	// Used to track which accounts are actively used
	UpdateLastUsed(provider, accountID string) error

	// GetToken retrieves the OAuth token for an account
	GetToken(provider, accountID string) (*OAuthToken, error)

	// UpdateToken updates the stored token for an account
	UpdateToken(provider, accountID string, token *OAuthToken) error

	// IsExpired checks if an account's token is expired
	IsExpired(provider, accountID string) bool

	// RefreshTokenIfNeeded refreshes the token if it's expired
	// If token is still valid, returns current token
	RefreshTokenIfNeeded(ctx context.Context, provider, accountID string) (*OAuthToken, error)

	// GetOrCreateBinding returns a CredentialBinding for an account
	// Creating it if necessary with the provided refresh function
	GetOrCreateBinding(account AccountProfile, refreshFn TokenRefreshFunc) (CredentialBinding, error)
}

// RegistryConfig holds configuration for registry behavior
type RegistryConfig struct {
	// TokenExpireBuffer is how long before actual expiry to consider token expired
	// Default: 5 minutes
	TokenExpireBuffer time.Duration

	// MaxAccountsPerProvider limits accounts per provider (0 = unlimited)
	MaxAccountsPerProvider int

	// AutoPruneAfterDays removes accounts unused for N days (0 = disabled)
	AutoPruneAfterDays int

	// PersistenceDir is the directory to store account registry
	PersistenceDir string
}

// DefaultRegistryConfig returns sensible defaults
func DefaultRegistryConfig(persistenceDir string) RegistryConfig {
	return RegistryConfig{
		TokenExpireBuffer:      5 * time.Minute,
		MaxAccountsPerProvider: 0, // unlimited
		AutoPruneAfterDays:     0, // disabled
		PersistenceDir:         persistenceDir,
	}
}

// RegistryError represents registry-specific errors
type RegistryError struct {
	Code    string
	Message string
	Err     error
}

// Error implements the error interface
func (e *RegistryError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("registry error [%s]: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("registry error [%s]: %s", e.Code, e.Message)
}

// Registry error codes
const (
	ErrorCodeAccountNotFound      = "account_not_found"
	ErrorCodeAccountAlreadyExists = "account_already_exists"
	ErrorCodeProviderNotFound     = "provider_not_found"
	ErrorCodeTokenNotFound        = "token_not_found"
	ErrorCodeInvalidIdentity      = "invalid_identity"
	ErrorCodeInvalidToken         = "invalid_token"
	ErrorCodeNoDefaultAccount     = "no_default_account"
	ErrorCodeMaxAccountsExceeded  = "max_accounts_exceeded"
	ErrorCodeTokenRefreshFailed   = "token_refresh_failed"
	ErrorCodePersistenceFailed    = "persistence_failed"
	ErrorCodeCorruptedData        = "corrupted_data"
)

// NewRegistryError creates a new registry error
func NewRegistryError(code, message string, err error) *RegistryError {
	return &RegistryError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// IsAccountNotFound checks if error is account not found
func IsAccountNotFound(err error) bool {
	if regErr, ok := err.(*RegistryError); ok {
		return regErr.Code == ErrorCodeAccountNotFound
	}
	return false
}

// IsTokenNotFound checks if error is token not found
func IsTokenNotFound(err error) bool {
	if regErr, ok := err.(*RegistryError); ok {
		return regErr.Code == ErrorCodeTokenNotFound
	}
	return false
}

// IsTokenRefreshFailed checks if error is token refresh failure
func IsTokenRefreshFailed(err error) bool {
	if regErr, ok := err.(*RegistryError); ok {
		return regErr.Code == ErrorCodeTokenRefreshFailed
	}
	return false
}
