package agent

import (
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/account"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/profiles"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// Credential represents a single credential (OAuth token or API key) for a provider.
type Credential struct {
	// Provider is the provider name (e.g., "anthropic", "openai", "gemini").
	Provider string

	// AccountID is the unique account identifier within the provider.
	AccountID string

	// Token is the access token or API key string used for authentication.
	Token string

	// IsOAuth indicates whether this credential uses OAuth (true) or a static API key (false).
	IsOAuth bool

	// CoolingOff indicates whether this credential is currently in a cooldown period
	// after a rotation-triggering error.
	CoolingOff bool

	// CooldownAt is when the cooldown period started.
	CooldownAt time.Time
}

// CredentialStore is an in-memory store that holds available credentials per provider
// with cooldown tracking. It is loaded from the account registry at agent creation time
// and used by executeWithChain to rotate credentials within a single provider before
// the fallback chain moves to the next provider entirely.
type CredentialStore struct {
	mu          sync.RWMutex
	credentials map[string][]Credential // provider → ordered list of credentials
	cooldown    time.Duration           // how long a credential stays in cooldown
	policy      *profiles.RetryPolicy   // rotation policy (which errors trigger rotation)
}

// NewCredentialStore creates a CredentialStore by loading credentials from the account registry.
// If registry is nil or has no accounts, returns a store with no credentials.
// The policy determines which error types trigger credential rotation.
func NewCredentialStore(registry account.AccountRegistry, policy *profiles.RetryPolicy) *CredentialStore {
	if policy == nil {
		policy = profiles.DefaultRetryPolicy()
	}

	cooldown := time.Duration(policy.CooldownSeconds) * time.Second
	if cooldown == 0 {
		cooldown = 30 * time.Second
	}

	cs := &CredentialStore{
		credentials: make(map[string][]Credential),
		cooldown:    cooldown,
		policy:      policy,
	}

	if registry == nil {
		return cs
	}

	// Load all accounts from the registry
	allAccounts, err := registry.ListAllAccounts()
	if err != nil {
		return cs
	}

	for _, acct := range allAccounts {
		providerName := acct.Provider()
		accountID := acct.ID()

		token, err := registry.GetToken(providerName, accountID)
		if err != nil || token == nil {
			continue
		}

		// Determine the token string: prefer AccessToken, fall back to APIKey
		tokenStr := token.AccessToken
		isOAuth := true
		if tokenStr == "" && token.APIKey != "" {
			tokenStr = token.APIKey
			isOAuth = false
		}
		if tokenStr == "" {
			continue
		}

		cred := Credential{
			Provider:  providerName,
			AccountID: accountID,
			Token:     tokenStr,
			IsOAuth:   isOAuth,
		}

		cs.credentials[providerName] = append(cs.credentials[providerName], cred)
	}

	return cs
}

// GetCredentials returns available (non-cooling-off) credentials for a provider.
// Credentials whose cooldown has expired are automatically reactivated.
// Returns nil if no credentials are available.
func (cs *CredentialStore) Credentials(provider string) []Credential {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	creds, ok := cs.credentials[provider]
	if !ok {
		return nil
	}

	now := time.Now()
	var available []Credential
	for i := range creds {
		// Check if cooldown has expired
		if creds[i].CoolingOff && now.After(creds[i].CooldownAt.Add(cs.cooldown)) {
			creds[i].CoolingOff = false
		}
		if !creds[i].CoolingOff {
			available = append(available, creds[i])
		}
	}

	return available
}

// MarkCooldown puts a credential on cooldown after a rotation-triggering failure.
// The credential will be skipped by GetCredentials until the cooldown expires.
func (cs *CredentialStore) MarkCooldown(provider, accountID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	creds, ok := cs.credentials[provider]
	if !ok {
		return
	}

	for i := range creds {
		if creds[i].AccountID == accountID {
			creds[i].CoolingOff = true
			creds[i].CooldownAt = time.Now()
			return
		}
	}
}

// ShouldRotate checks if the given error type warrants credential rotation
// according to the configured RetryPolicy.
func (cs *CredentialStore) ShouldRotate(err error) bool {
	if cs.policy == nil {
		return false
	}

	if cs.policy.RotateOnAnyError {
		return true
	}
	if cs.policy.RotateOnRateLimit && sdkerr.IsRateLimitError(err) {
		return true
	}
	if cs.policy.RotateOnPayment && sdkerr.IsPaymentError(err) {
		return true
	}
	if cs.policy.RotateOnAuthError && sdkerr.IsAuthError(err) {
		return true
	}
	return false
}

// HasCredentials returns true if the store has any credentials for the given provider.
func (cs *CredentialStore) HasCredentials(provider string) bool {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	creds, ok := cs.credentials[provider]
	return ok && len(creds) > 0
}

// CredentialCount returns the total number of credentials across all providers.
func (cs *CredentialStore) CredentialCount() int {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	count := 0
	for _, creds := range cs.credentials {
		count += len(creds)
	}
	return count
}
