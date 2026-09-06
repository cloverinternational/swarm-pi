package account

import (
	"context"
	"fmt"
	"time"
)

// OAuthToken represents OAuth 2.0 token information
type OAuthToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	APIKey       string    `json:"api_key,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes,omitempty"`
}

// AccountProfile represents an account bound to a profile context
// It combines account identity with profile-specific configuration
type AccountProfile interface {
	// Identity returns the underlying account identity
	Identity() AccountIdentity

	// ProfileName returns the associated profile name
	// Examples: "openai", "azure-production", "custom"
	ProfileName() string

	// IsDefault indicates if this is the default account for its profile
	IsDefault() bool

	// Metadata returns provider-specific metadata as a map
	// Examples: organization ID, billing account, team info
	Metadata() map[string]any

	// UpdateMetadata updates metadata with the given key-value pair
	// Changes are persisted immediately to storage
	UpdateMetadata(key string, value any) error

	// LastUsedModel returns the last model used with this account
	// Empty string means the account hasn't been used yet
	LastUsedModel() string

	// SetLastUsedModel updates the last model used and persists it
	SetLastUsedModel(model string) error
}

// StandardAccountProfile is the default implementation of AccountProfile
type StandardAccountProfile struct {
	identity      AccountIdentity
	profileName   string
	isDefault     bool
	metadata      map[string]any
	lastUsedModel string
	metadataStore ProfileMetadataStore
}

// NewStandardAccountProfile creates a new profile
func NewStandardAccountProfile(
	identity AccountIdentity,
	profileName string,
	isDefault bool,
	store ProfileMetadataStore,
) *StandardAccountProfile {
	return &StandardAccountProfile{
		identity:      identity,
		profileName:   profileName,
		isDefault:     isDefault,
		metadata:      make(map[string]any),
		lastUsedModel: "",
		metadataStore: store,
	}
}

// Identity returns the account identity
func (s *StandardAccountProfile) Identity() AccountIdentity {
	return s.identity
}

// ProfileName returns the profile name
func (s *StandardAccountProfile) ProfileName() string {
	return s.profileName
}

// IsDefault returns default status
func (s *StandardAccountProfile) IsDefault() bool {
	return s.isDefault
}

// Metadata returns the metadata map
func (s *StandardAccountProfile) Metadata() map[string]any {
	return s.metadata
}

// UpdateMetadata updates metadata and persists
func (s *StandardAccountProfile) UpdateMetadata(key string, value any) error {
	if s.metadataStore == nil {
		// In-memory update only if no store
		s.metadata[key] = value
		return nil
	}
	return s.metadataStore.UpdateMetadata(s.identity.Key(), key, value)
}

// LastUsedModel returns the last model used
func (s *StandardAccountProfile) LastUsedModel() string {
	return s.lastUsedModel
}

// SetLastUsedModel updates the last model used
func (s *StandardAccountProfile) SetLastUsedModel(model string) error {
	s.lastUsedModel = model
	if s.metadataStore == nil {
		return nil
	}
	return s.metadataStore.UpdateMetadata(s.identity.Key(), "last_used_model", model)
}

// CredentialBinding attaches OAuth credentials to an account profile
type CredentialBinding interface {
	// Account returns the account this binding is for
	Account() AccountProfile

	// Token returns the current OAuth token
	// Returns nil if token is not available
	Token() *OAuthToken

	// RefreshToken refreshes the OAuth token if needed
	// Uses context for timeout and cancellation support
	// Returns the new token or error if refresh failed
	RefreshToken(ctx context.Context) (*OAuthToken, error)

	// IsExpired checks if the token needs refresh
	IsExpired() bool

	// ExpiresIn returns the remaining validity duration
	// Returns negative duration if already expired
	ExpiresIn() time.Duration

	// UpdateToken updates the stored token
	UpdateToken(token *OAuthToken) error
}

// StandardCredentialBinding is the default implementation of CredentialBinding
type StandardCredentialBinding struct {
	profile    AccountProfile
	token      *OAuthToken
	refreshFn  TokenRefreshFunc
	expiresBuf time.Duration // Buffer before expiry (default 5 min)
}

// TokenRefreshFunc is a function type for refreshing tokens
// Implementations should handle network calls and error recovery
type TokenRefreshFunc func(ctx context.Context, account AccountIdentity, oldToken *OAuthToken) (*OAuthToken, error)

// NewStandardCredentialBinding creates a new credential binding
func NewStandardCredentialBinding(
	profile AccountProfile,
	token *OAuthToken,
	refreshFn TokenRefreshFunc,
) *StandardCredentialBinding {
	return &StandardCredentialBinding{
		profile:    profile,
		token:      token,
		refreshFn:  refreshFn,
		expiresBuf: 5 * time.Minute,
	}
}

// Account returns the account
func (s *StandardCredentialBinding) Account() AccountProfile {
	return s.profile
}

// Token returns the token
func (s *StandardCredentialBinding) Token() *OAuthToken {
	return s.token
}

// IsExpired checks if token is expired with buffer
func (s *StandardCredentialBinding) IsExpired() bool {
	if s.token == nil || s.token.ExpiresAt.IsZero() {
		// No expiry info, assume not expired
		return false
	}
	return time.Now().Add(s.expiresBuf).After(s.token.ExpiresAt)
}

// ExpiresIn returns remaining validity
func (s *StandardCredentialBinding) ExpiresIn() time.Duration {
	if s.token == nil || s.token.ExpiresAt.IsZero() {
		// No expiry info, assume 1 year
		return 365 * 24 * time.Hour
	}
	return time.Until(s.token.ExpiresAt)
}

// RefreshToken refreshes the token if needed
func (s *StandardCredentialBinding) RefreshToken(ctx context.Context) (*OAuthToken, error) {
	if s.refreshFn == nil {
		return s.token, fmt.Errorf("no refresh function configured")
	}
	if !s.IsExpired() {
		return s.token, nil
	}
	newToken, err := s.refreshFn(ctx, s.profile.Identity(), s.token)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}
	s.token = newToken
	return newToken, nil
}

// UpdateToken updates the stored token
func (s *StandardCredentialBinding) UpdateToken(token *OAuthToken) error {
	if token == nil {
		return fmt.Errorf("token cannot be nil")
	}
	s.token = token
	return nil
}

// ProfileMetadataStore is the interface for persisting profile metadata
type ProfileMetadataStore interface {
	// UpdateMetadata updates a metadata value for an account
	UpdateMetadata(accountKey string, metadataKey string, value any) error

	// GetMetadata retrieves a metadata value
	GetMetadata(accountKey string, metadataKey string) (any, error)

	// DeleteMetadata removes a metadata value
	DeleteMetadata(accountKey string, metadataKey string) error
}
