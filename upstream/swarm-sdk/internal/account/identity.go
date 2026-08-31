// Package account provides abstractions for multi-account OAuth support
package account

import (
	"fmt"
	"strings"
	"time"
)

// AccountIdentity represents a unique account across all providers
// It serves as the core identity abstraction independent of credentials
type AccountIdentity interface {
	// Provider returns the OAuth provider name (e.g., "anthropic", "openai", "gemini")
	Provider() string

	// ID returns the unique account identifier from the provider
	// For OpenAI: ChatGPT account ID; For Anthropic: organization ID
	ID() string

	// Key returns a normalized, globally unique key (provider::id)
	// Format: "provider::account_id" - suitable for use as a map key
	Key() string

	// DisplayName returns a human-readable account name for UI display
	DisplayName() string

	// IsActive indicates if this account is currently usable
	IsActive() bool

	// CreatedAt returns when the account was first added to the system
	CreatedAt() time.Time

	// LastUsed returns the last timestamp this account was accessed
	// Used for sorting, pruning stale accounts, and UI display
	LastUsed() time.Time
}

// StandardAccountIdentity is the default implementation of AccountIdentity
type StandardAccountIdentity struct {
	ProviderName string
	AccountID    string
	Name         string
	Active       bool
	Created      time.Time
	Used         time.Time
}

// Provider returns the provider name
func (s *StandardAccountIdentity) Provider() string {
	return s.ProviderName
}

// ID returns the account ID
func (s *StandardAccountIdentity) ID() string {
	return s.AccountID
}

// Key returns the global unique key
func (s *StandardAccountIdentity) Key() string {
	return strings.ToLower(s.ProviderName) + "::" + s.AccountID
}

// DisplayName returns the human-readable name
func (s *StandardAccountIdentity) DisplayName() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Key()
}

// IsActive returns active status
func (s *StandardAccountIdentity) IsActive() bool {
	return s.Active
}

// CreatedAt returns creation time
func (s *StandardAccountIdentity) CreatedAt() time.Time {
	return s.Created
}

// LastUsed returns last access time
func (s *StandardAccountIdentity) LastUsed() time.Time {
	return s.Used
}

// String implements Stringer for debugging
func (s *StandardAccountIdentity) String() string {
	return fmt.Sprintf("Account{provider=%s, id=%s, name=%s, active=%v}",
		s.ProviderName, s.AccountID, s.Name, s.Active)
}

// ValidateIdentity checks that an identity has required fields
func ValidateIdentity(identity AccountIdentity) error {
	if identity == nil {
		return fmt.Errorf("identity cannot be nil")
	}
	if identity.Provider() == "" {
		return fmt.Errorf("identity provider cannot be empty")
	}
	if identity.ID() == "" {
		return fmt.Errorf("identity account ID cannot be empty")
	}
	return nil
}
