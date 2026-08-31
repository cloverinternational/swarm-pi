package account

import (
	"context"
	"fmt"
	"time"
)

// AccountManager is the high-level orchestrator for multi-account operations
// It coordinates registry, profile store, and credential management
type AccountManager interface {
	// RegisterOAuthAccount completes OAuth flow and registers the new account
	// Returns the newly created AccountProfile
	RegisterOAuthAccount(ctx context.Context, provider string, token *OAuthToken) (AccountProfile, error)

	// GetActiveProfile returns the currently active profile
	// Respects fallback chain if configured
	GetActiveProfile(ctx context.Context) (AccountProfile, error)

	// SwitchAccount changes the active account/profile
	// Triggers hooks and updates last-used metadata
	SwitchAccount(ctx context.Context, provider, accountID string) error

	// GetAccountProfile returns profile for a specific account
	GetAccountProfile(provider, accountID string) (AccountProfile, error)

	// RefreshAccountTokens refreshes tokens for all active accounts
	// Skips accounts with valid tokens
	RefreshAccountTokens(ctx context.Context) error

	// PruneStaleAccounts removes accounts unused for N days
	PruneStaleAccounts(maxAgeDays int) error

	// ListActiveAccounts returns accounts sorted by last use (most recent first)
	ListActiveAccounts() ([]AccountProfile, error)

	// ListAccountsByProvider returns all accounts for a provider
	ListAccountsByProvider(provider string) ([]AccountProfile, error)

	// GetBinding returns a CredentialBinding for direct token access
	GetBinding(provider, accountID string) (CredentialBinding, error)

	// OnAccountSwitch registers a hook for account switches
	OnAccountSwitch(handler AccountSwitchHandler) error

	// OnTokenRefresh registers a hook for token refreshes
	OnTokenRefresh(handler TokenRefreshHandler) error

	// OnAccountRegistered registers a hook for new account registration
	OnAccountRegistered(handler AccountRegisteredHandler) error
}

// AccountSwitchHandler is invoked when the active account changes
type AccountSwitchHandler interface {
	// Handle is called when account switches from 'from' to 'to'
	// 'from' can be nil if this is the initial account selection
	Handle(ctx context.Context, from, to AccountProfile) error
}

// TokenRefreshHandler is invoked when tokens are refreshed
type TokenRefreshHandler interface {
	// Handle is called when a token is successfully refreshed
	Handle(ctx context.Context, account AccountProfile, token *OAuthToken) error
}

// AccountRegisteredHandler is invoked when a new account is registered
type AccountRegisteredHandler interface {
	// Handle is called when a new account is registered
	Handle(ctx context.Context, profile AccountProfile) error
}

// SimpleAccountSwitchHandler is a simple function-based implementation
type SimpleAccountSwitchHandler struct {
	Fn func(ctx context.Context, from, to AccountProfile) error
}

func (h *SimpleAccountSwitchHandler) Handle(ctx context.Context, from, to AccountProfile) error {
	if h.Fn == nil {
		return nil
	}
	return h.Fn(ctx, from, to)
}

// SimpleTokenRefreshHandler is a simple function-based implementation
type SimpleTokenRefreshHandler struct {
	Fn func(ctx context.Context, account AccountProfile, token *OAuthToken) error
}

func (h *SimpleTokenRefreshHandler) Handle(ctx context.Context, account AccountProfile, token *OAuthToken) error {
	if h.Fn == nil {
		return nil
	}
	return h.Fn(ctx, account, token)
}

// SimpleAccountRegisteredHandler is a simple function-based implementation
type SimpleAccountRegisteredHandler struct {
	Fn func(ctx context.Context, profile AccountProfile) error
}

func (h *SimpleAccountRegisteredHandler) Handle(ctx context.Context, profile AccountProfile) error {
	if h.Fn == nil {
		return nil
	}
	return h.Fn(ctx, profile)
}

// ManagerConfig holds configuration for account manager
type ManagerConfig struct {
	// RegistryConfig for account storage
	RegistryConfig RegistryConfig

	// ProfileStoreConfig for profile storage
	ProfileStoreConfig ProfileStoreConfig

	// TokenRefreshFunc is the function to call when refreshing tokens
	TokenRefreshFunc TokenRefreshFunc

	// DefaultProfile is the profile to activate on first use
	DefaultProfile string
}

// ManagerError represents manager-specific errors
type ManagerError struct {
	Code    string
	Message string
	Err     error
}

// Error implements the error interface
func (e *ManagerError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("manager error [%s]: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("manager error [%s]: %s", e.Code, e.Message)
}

// Manager error codes
const (
	ErrorCodeNoActiveProfile    = "no_active_profile"
	ErrorCodeAccountNotAssigned = "account_not_assigned"
	ErrorCodeFallbackExhausted  = "fallback_exhausted"
	ErrorCodeRefreshFailed      = "refresh_failed"
	ErrorCodeSwitchFailed       = "switch_failed"
	ErrorCodeHookFailed         = "hook_failed"
)

// NewManagerError creates a new manager error
func NewManagerError(code, message string, err error) *ManagerError {
	return &ManagerError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// AccountManagerOptions provides optional configuration for manager creation
type AccountManagerOptions struct {
	// InitialProfiles are profiles to create on first initialization
	InitialProfiles []InitialProfile

	// AutoMigrate migrates from old format on load
	AutoMigrate bool

	// OnError is called when non-critical errors occur
	OnError func(err error)
}

// InitialProfile represents a profile to create during initialization
type InitialProfile struct {
	Name      string
	Provider  string
	AccountID string
	Model     string
	IsDefault bool
	Metadata  map[string]any
}

// ManagerStats provides statistics about registered accounts
type ManagerStats struct {
	// TotalAccounts is the total number of registered accounts
	TotalAccounts int

	// AccountsByProvider maps provider name to account count
	AccountsByProvider map[string]int

	// TotalProfiles is the number of profiles
	TotalProfiles int

	// ActiveProfile is the name of the currently active profile
	ActiveProfile string

	// LastActivity is the most recent account use time
	LastActivity time.Time

	// StaleAccounts is the number of accounts unused for 30+ days
	StaleAccounts int
}

// GetStats returns current manager statistics
// This method is typically called on the manager interface
// Mock implementation shown here for reference
type ManagerStatsProvider interface {
	GetStats(ctx context.Context) (ManagerStats, error)
}
