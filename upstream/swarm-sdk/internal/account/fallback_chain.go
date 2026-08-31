package account

import (
	"context"
	"fmt"
	"strings"
)

// AccountFallbackChain manages a chain of accounts to try in order
// When the primary account fails, automatically tries fallbacks
type AccountFallbackChain interface {
	// AddPrimary sets the primary account in the chain
	// Existing chain is replaced
	AddPrimary(identity AccountIdentity) error

	// AddFallback appends a fallback account to the chain
	AddFallback(identity AccountIdentity) error

	// GetChain returns all accounts in priority order (primary first)
	GetChain() []AccountIdentity

	// GetPrimary returns the primary account
	GetPrimary() AccountIdentity

	// GetFallbacks returns fallback accounts (excluding primary)
	GetFallbacks() []AccountIdentity

	// FindFirstAvailable returns the first account in chain with valid token
	// Registry is used to check token validity and fetch bindings
	FindFirstAvailable(ctx context.Context, registry AccountRegistry) (AccountProfile, error)

	// GetAllAvailable returns all accounts in chain with valid tokens
	GetAllAvailable(ctx context.Context, registry AccountRegistry) ([]AccountProfile, error)

	// IsExhausted returns true if all fallbacks have been tried without success
	IsExhausted() bool

	// Reset clears exhaustion state
	Reset()

	// MarkFailed marks an account as failed in this attempt
	// Returns true if there are more fallbacks to try
	MarkFailed(identity AccountIdentity) bool

	// Length returns the total number of accounts in chain
	Length() int

	// IsEmpty returns true if chain has no accounts
	IsEmpty() bool
}

// StandardAccountFallbackChain is the default implementation
type StandardAccountFallbackChain struct {
	primary    AccountIdentity
	fallbacks  []AccountIdentity
	exhausted  bool
	triedCount int
}

// NewStandardAccountFallbackChain creates an empty chain
func NewStandardAccountFallbackChain() *StandardAccountFallbackChain {
	return &StandardAccountFallbackChain{
		fallbacks:  make([]AccountIdentity, 0),
		exhausted:  false,
		triedCount: 0,
	}
}

// AddPrimary sets the primary account
func (c *StandardAccountFallbackChain) AddPrimary(identity AccountIdentity) error {
	if err := ValidateIdentity(identity); err != nil {
		return fmt.Errorf("invalid primary account: %w", err)
	}
	c.primary = identity
	c.Reset()
	return nil
}

// AddFallback appends a fallback account
func (c *StandardAccountFallbackChain) AddFallback(identity AccountIdentity) error {
	if err := ValidateIdentity(identity); err != nil {
		return fmt.Errorf("invalid fallback account: %w", err)
	}
	// Avoid duplicates
	if identity.Key() == c.primary.Key() {
		return fmt.Errorf("fallback account is same as primary")
	}
	for _, fb := range c.fallbacks {
		if fb.Key() == identity.Key() {
			return fmt.Errorf("fallback account already in chain")
		}
	}
	c.fallbacks = append(c.fallbacks, identity)
	return nil
}

// GetChain returns all accounts in order
func (c *StandardAccountFallbackChain) GetChain() []AccountIdentity {
	chain := make([]AccountIdentity, 0, 1+len(c.fallbacks))
	if c.primary != nil {
		chain = append(chain, c.primary)
	}
	chain = append(chain, c.fallbacks...)
	return chain
}

// GetPrimary returns the primary
func (c *StandardAccountFallbackChain) GetPrimary() AccountIdentity {
	return c.primary
}

// GetFallbacks returns fallbacks
func (c *StandardAccountFallbackChain) GetFallbacks() []AccountIdentity {
	// Return a copy to prevent external modification
	fallbacks := make([]AccountIdentity, len(c.fallbacks))
	copy(fallbacks, c.fallbacks)
	return fallbacks
}

// FindFirstAvailable tries accounts in order until one has valid token
func (c *StandardAccountFallbackChain) FindFirstAvailable(ctx context.Context, registry AccountRegistry) (AccountProfile, error) {
	if c.IsEmpty() {
		return nil, fmt.Errorf("fallback chain is empty")
	}

	chain := c.GetChain()
	for i, identity := range chain {
		// Check if token is valid
		isExpired := registry.IsExpired(identity.Provider(), identity.ID())
		if !isExpired {
			// Token is valid, try to get binding
			profile, err := registry.GetAccount(identity.Provider(), identity.ID())
			if err == nil {
				c.triedCount = i
				return NewStandardAccountProfile(profile, "", false, nil), nil
			}
		}
	}

	// All accounts tried
	c.exhausted = true
	return nil, fmt.Errorf("no available accounts in fallback chain")
}

// GetAllAvailable returns all valid accounts
func (c *StandardAccountFallbackChain) GetAllAvailable(ctx context.Context, registry AccountRegistry) ([]AccountProfile, error) {
	if c.IsEmpty() {
		return nil, fmt.Errorf("fallback chain is empty")
	}

	var available []AccountProfile
	chain := c.GetChain()

	for _, identity := range chain {
		isExpired := registry.IsExpired(identity.Provider(), identity.ID())
		if !isExpired {
			profile, err := registry.GetAccount(identity.Provider(), identity.ID())
			if err == nil {
				available = append(available, NewStandardAccountProfile(profile, "", false, nil))
			}
		}
	}

	if len(available) == 0 {
		return nil, fmt.Errorf("no available accounts in fallback chain")
	}

	return available, nil
}

// IsExhausted returns true if all fallbacks exhausted
func (c *StandardAccountFallbackChain) IsExhausted() bool {
	return c.exhausted
}

// Reset clears exhaustion
func (c *StandardAccountFallbackChain) Reset() {
	c.exhausted = false
	c.triedCount = 0
}

// MarkFailed marks account as failed
func (c *StandardAccountFallbackChain) MarkFailed(identity AccountIdentity) bool {
	chain := c.GetChain()
	if len(chain) == 0 {
		return false
	}

	// If primary failed, try fallbacks
	if c.primary != nil && identity.Key() == c.primary.Key() {
		return len(c.fallbacks) > 0
	}

	// Count how many we've tried
	currentPos := -1
	for i, acc := range chain {
		if acc.Key() == identity.Key() {
			currentPos = i
			break
		}
	}

	if currentPos < 0 {
		return false // Unknown account
	}

	// If we've tried all, exhausted
	if currentPos >= len(chain)-1 {
		c.exhausted = true
		return false
	}

	return true // More to try
}

// Length returns account count
func (c *StandardAccountFallbackChain) Length() int {
	if c.primary == nil {
		return len(c.fallbacks)
	}
	return 1 + len(c.fallbacks)
}

// IsEmpty returns true if no accounts
func (c *StandardAccountFallbackChain) IsEmpty() bool {
	return c.primary == nil && len(c.fallbacks) == 0
}

// String provides debug representation
func (c *StandardAccountFallbackChain) String() string {
	if c.IsEmpty() {
		return "FallbackChain{empty}"
	}

	var str strings.Builder
	str.WriteString(fmt.Sprintf("FallbackChain{primary=%s", c.primary.Key()))
	if len(c.fallbacks) > 0 {
		str.WriteString(", fallbacks=[")
		for i, fb := range c.fallbacks {
			if i > 0 {
				str.WriteString(", ")
			}
			str.WriteString(fb.Key())
		}
		str.WriteString("]")
	}
	if c.exhausted {
		str.WriteString(", exhausted")
	}
	str.WriteString("}")
	return str.String()
}

// FallbackChainBuilder is a helper for building chains
type FallbackChainBuilder struct {
	chain *StandardAccountFallbackChain
}

// NewFallbackChainBuilder creates a new builder
func NewFallbackChainBuilder() *FallbackChainBuilder {
	return &FallbackChainBuilder{
		chain: NewStandardAccountFallbackChain(),
	}
}

// WithPrimary sets the primary account
func (b *FallbackChainBuilder) WithPrimary(identity AccountIdentity) *FallbackChainBuilder {
	_ = b.chain.AddPrimary(identity)
	return b
}

// WithFallback adds a fallback account
func (b *FallbackChainBuilder) WithFallback(identity AccountIdentity) *FallbackChainBuilder {
	_ = b.chain.AddFallback(identity)
	return b
}

// Build returns the constructed chain
func (b *FallbackChainBuilder) Build() AccountFallbackChain {
	return b.chain
}
