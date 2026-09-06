package account

import (
	"context"
	"fmt"
)

// OAuthBridge provides integration between OAuth providers and the account system
// This enables legacy OAuth code to work with the new multi-account manager
type OAuthBridge interface {
	// RegisterFromOAuthToken registers an OAuth token from a provider
	// Provider should be "anthropic", "openai", or "gemini"
	RegisterFromOAuthToken(ctx context.Context, manager AccountManager, provider string, legacyToken any) (AccountProfile, error)

	// GetTokenForAccount retrieves the OAuth token for an account
	GetTokenForAccount(registry AccountRegistry, provider string, accountID string) (any, error)

	// MigrateFromLegacy migrates existing OAuth configs to the new format
	MigrateFromLegacy(ctx context.Context, registry AccountRegistry) error
}

// SimpleOAuthBridge is a basic implementation that adapts legacy token formats
type SimpleOAuthBridge struct {
	providerAdapters map[string]TokenAdapter
}

// TokenAdapter adapts legacy token format to account.OAuthToken
type TokenAdapter interface {
	// ToAccountToken converts legacy token to account.OAuthToken
	ToAccountToken(legacyToken any) (*OAuthToken, error)

	// FromAccountToken converts account.OAuthToken back to legacy format
	FromAccountToken(token *OAuthToken) (any, error)

	// ExtractAccountID extracts the account ID from legacy token
	ExtractAccountID(legacyToken any) (string, error)

	// ExtractDisplayName extracts display name from legacy token
	ExtractDisplayName(legacyToken any) (string, error)
}

// NewSimpleOAuthBridge creates a new OAuth bridge
func NewSimpleOAuthBridge() *SimpleOAuthBridge {
	return &SimpleOAuthBridge{
		providerAdapters: make(map[string]TokenAdapter),
	}
}

// RegisterAdapter registers a token adapter for a provider
func (sb *SimpleOAuthBridge) RegisterAdapter(provider string, adapter TokenAdapter) {
	sb.providerAdapters[provider] = adapter
}

// RegisterFromOAuthToken registers a token
func (sb *SimpleOAuthBridge) RegisterFromOAuthToken(ctx context.Context, manager AccountManager, provider string, legacyToken any) (AccountProfile, error) {
	if legacyToken == nil {
		return nil, fmt.Errorf("legacy token cannot be nil")
	}

	// Get adapter for this provider
	adapter, exists := sb.providerAdapters[provider]
	if !exists {
		return nil, fmt.Errorf("no adapter registered for provider: %s", provider)
	}

	// Convert legacy token to account token
	token, err := adapter.ToAccountToken(legacyToken)
	if err != nil {
		return nil, fmt.Errorf("failed to convert token: %w", err)
	}

	// Register with manager
	return manager.RegisterOAuthAccount(ctx, provider, token)
}

// GetTokenForAccount retrieves a token
func (sb *SimpleOAuthBridge) GetTokenForAccount(registry AccountRegistry, provider string, accountID string) (any, error) {
	// Get adapter
	adapter, exists := sb.providerAdapters[provider]
	if !exists {
		return nil, fmt.Errorf("no adapter registered for provider: %s", provider)
	}

	// Get token from registry
	token, err := registry.GetToken(provider, accountID)
	if err != nil {
		return nil, err
	}

	// Convert to legacy format
	return adapter.FromAccountToken(token)
}

// MigrateFromLegacy migrates existing configs
func (sb *SimpleOAuthBridge) MigrateFromLegacy(ctx context.Context, registry AccountRegistry) error {
	// This would be implemented per-provider
	// For now, return success
	return nil
}

// ProviderTokenAdapter provides adapter implementations for each provider
// Implementations should be defined in the respective provider packages

// AnthropicTokenAdapter adapts Anthropic OAuth tokens
type AnthropicTokenAdapter struct{}

// OpenAITokenAdapter adapts OpenAI OAuth tokens
type OpenAITokenAdapter struct{}

// GeminiTokenAdapter adapts Gemini OAuth tokens
type GeminiTokenAdapter struct{}

// DefaultBridge creates a bridge with adapters for all providers
func DefaultBridge() OAuthBridge {
	bridge := NewSimpleOAuthBridge()
	// Adapters would be registered here
	// bridge.RegisterAdapter("anthropic", &AnthropicTokenAdapter{})
	// bridge.RegisterAdapter("openai", &OpenAITokenAdapter{})
	// bridge.RegisterAdapter("gemini", &GeminiTokenAdapter{})
	return bridge
}

// OAuthTokenStorage wraps the new account system to provide OAuth token access
// This allows existing code to continue using GetStoredOAuthToken() patterns
type OAuthTokenStorage struct {
	manager AccountManager
}

// NewOAuthTokenStorage creates a new token storage wrapper
func NewOAuthTokenStorage(manager AccountManager) *OAuthTokenStorage {
	return &OAuthTokenStorage{
		manager: manager,
	}
}

// GetToken gets the token for the active account
func (ots *OAuthTokenStorage) GetToken(ctx context.Context) (*OAuthToken, error) {
	activeProfile, err := ots.manager.GetActiveProfile(ctx)
	if err != nil {
		return nil, fmt.Errorf("no active profile: %w", err)
	}

	binding, err := ots.manager.GetBinding(activeProfile.Identity().Provider(), activeProfile.Identity().ID())
	if err != nil {
		return nil, fmt.Errorf("failed to get binding: %w", err)
	}

	return binding.Token(), nil
}

// GetTokenForAccount gets token for a specific account
func (ots *OAuthTokenStorage) GetTokenForAccount(provider, accountID string) (*OAuthToken, error) {
	binding, err := ots.manager.GetBinding(provider, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get binding: %w", err)
	}

	return binding.Token(), nil
}

// UpdateToken updates a token
func (ots *OAuthTokenStorage) UpdateToken(provider, accountID string, token *OAuthToken) error {
	registry := ots.manager.(*ManagerImpl).GetRegistry()
	return registry.UpdateToken(provider, accountID, token)
}

// MigrateAccountsFromLegacy is a helper function for migrating from old format
// This should be called during app initialization
func MigrateAccountsFromLegacy(ctx context.Context, oldOAuthPath string, manager AccountManager) error {
	// This would read old oauth.json files and register accounts
	// For now, return success as this is handled separately
	return nil
}
