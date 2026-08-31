package account

import (
	"fmt"
	"time"
)

// ProfileStore manages profiles with account awareness
// A profile bundles provider + account + model configuration
type ProfileStore interface {
	// CreateProfile creates a new profile bound to an account
	// Returns error if profile already exists
	CreateProfile(name string, account AccountIdentity, model string) (AccountProfile, error)

	// GetProfile retrieves a profile by name
	GetProfile(name string) (AccountProfile, error)

	// ListProfiles returns all profiles, ordered by creation time
	ListProfiles() ([]AccountProfile, error)

	// ListProfilesByProvider returns profiles for a specific provider
	ListProfilesByProvider(provider string) ([]AccountProfile, error)

	// UpdateProfile updates profile configuration
	UpdateProfile(name string, account AccountIdentity, model string) error

	// DeleteProfile removes a profile
	DeleteProfile(name string) error

	// RenameProfile renames a profile
	RenameProfile(oldName, newName string) error

	// SetDefaultProfile marks a profile as the default globally
	SetDefaultProfile(name string) error

	// GetDefaultProfile returns the default profile
	GetDefaultProfile() (AccountProfile, error)

	// HasProfile checks if profile exists
	HasProfile(name string) bool

	// GetProfileCount returns total number of profiles
	GetProfileCount() int
}

// ProfileStoreConfig holds configuration for profile store
type ProfileStoreConfig struct {
	// PersistenceDir is the directory to store profiles
	PersistenceDir string

	// AllowDuplicateAccounts allows multiple profiles to use same account
	// Default: true
	AllowDuplicateAccounts bool

	// PreserveOnDelete keeps metadata when profile is deleted
	// Default: false (clean removal)
	PreserveOnDelete bool
}

// DefaultProfileStoreConfig returns sensible defaults
func DefaultProfileStoreConfig(persistenceDir string) ProfileStoreConfig {
	return ProfileStoreConfig{
		PersistenceDir:         persistenceDir,
		AllowDuplicateAccounts: true,
		PreserveOnDelete:       false,
	}
}

// ProfileStoreError represents profile store errors
type ProfileStoreError struct {
	Code    string
	Message string
	Err     error
}

// Error implements the error interface
func (e *ProfileStoreError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("profile store error [%s]: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("profile store error [%s]: %s", e.Code, e.Message)
}

// Profile store error codes
const (
	ErrorCodeProfileNotFound      = "profile_not_found"
	ErrorCodeProfileAlreadyExists = "profile_already_exists"
	ErrorCodeInvalidProfileName   = "invalid_profile_name"
	ErrorCodeCannotDeleteDefault  = "cannot_delete_default"
	ErrorCodeDuplicateAccount     = "duplicate_account"
	ErrorCodeStorageFailed        = "storage_failed"
)

// NewProfileStoreError creates a new profile store error
func NewProfileStoreError(code, message string, err error) *ProfileStoreError {
	return &ProfileStoreError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// IsProfileNotFound checks if error is profile not found
func IsProfileNotFound(err error) bool {
	if stErr, ok := err.(*ProfileStoreError); ok {
		return stErr.Code == ErrorCodeProfileNotFound
	}
	return false
}

// StoredProfile represents a profile in storage format
// This is separate from AccountProfile interface to avoid circular dependencies
type StoredProfile struct {
	// Name is the unique profile name
	Name string `json:"name"`

	// Provider is the provider name
	Provider string `json:"provider"`

	// AccountID is the account ID for this provider
	AccountID string `json:"account_id"`

	// Model is the model to use with this profile
	Model string `json:"model"`

	// IsDefault indicates if this is the default profile
	IsDefault bool `json:"is_default"`

	// CreatedAt is the creation timestamp
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the last update timestamp
	UpdatedAt time.Time `json:"updated_at"`

	// Metadata is provider-specific metadata
	Metadata map[string]any `json:"metadata,omitempty"`

	// LastUsedModel tracks the last model used
	LastUsedModel string `json:"last_used_model,omitempty"`
}

// ToAccountProfile converts StoredProfile to AccountProfile
// Requires an AccountRegistry to look up the account identity
func (sp *StoredProfile) ToAccountProfile(registry AccountRegistry) (AccountProfile, error) {
	identity, err := registry.GetAccount(sp.Provider, sp.AccountID)
	if err != nil {
		return nil, fmt.Errorf("cannot convert stored profile to account profile: %w", err)
	}

	profile := NewStandardAccountProfile(identity, sp.Name, sp.IsDefault, nil)
	profile.lastUsedModel = sp.LastUsedModel
	profile.metadata = sp.Metadata
	if profile.metadata == nil {
		profile.metadata = make(map[string]any)
	}

	return profile, nil
}

// FromAccountProfile converts AccountProfile to StoredProfile for persistence
func FromAccountProfile(profile AccountProfile) *StoredProfile {
	return &StoredProfile{
		Name:          profile.ProfileName(),
		Provider:      profile.Identity().Provider(),
		AccountID:     profile.Identity().ID(),
		Model:         "", // Model should be set separately
		IsDefault:     profile.IsDefault(),
		CreatedAt:     profile.Identity().CreatedAt(),
		UpdatedAt:     time.Now(),
		Metadata:      profile.Metadata(),
		LastUsedModel: profile.LastUsedModel(),
	}
}

// ValidateProfileName validates profile names
func ValidateProfileName(name string) error {
	if name == "" {
		return fmt.Errorf("profile name cannot be empty")
	}
	if len(name) > 256 {
		return fmt.Errorf("profile name too long (max 256 chars)")
	}
	// Allow alphanumeric, dash, underscore, space
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_' || ch == ' ') {
			return fmt.Errorf("profile name contains invalid character: %c", ch)
		}
	}
	return nil
}

// BuiltInProfiles are default profiles created on first use
var BuiltInProfiles = []StoredProfile{
	{
		Name:      "default",
		Provider:  "anthropic",
		AccountID: "",
		Model:     "claude-opus-4-20250514",
		IsDefault: true,
		Metadata:  make(map[string]any),
	},
}
