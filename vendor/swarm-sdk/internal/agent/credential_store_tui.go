package agent

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/profiles"
)

// tuiAccountsFile mirrors the structure of ~/.swarm/tui_accounts.json.
// It is intentionally self-contained so the SDK has no compile-time dependency
// on TUI internals.
type tuiAccountsFile struct {
	Version  string       `json:"version"`
	Accounts []tuiAccount `json:"accounts"`
}

type tuiAccount struct {
	ID        string          `json:"id"`
	Provider  string          `json:"provider"`
	Email     string          `json:"email,omitempty"`
	IsActive  bool            `json:"is_active"`
	AddedAt   int64           `json:"added_at"`
	TokenData json.RawMessage `json:"token_data,omitempty"`
}

// tuiTokenData extracts the fields we need from any provider's token_data blob.
type tuiTokenData struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	Expiry       int64  `json:"expiry,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
}

// tuiAccountsFilePath returns the canonical path to tui_accounts.json.
func tuiAccountsFilePath() (string, error) {
	return paths.In("tui_accounts.json"), nil
}

// loadTuiAccounts reads and parses tui_accounts.json.
// Returns an empty file (not an error) if the file does not exist.
func loadTuiAccounts() (tuiAccountsFile, error) {
	path, err := tuiAccountsFilePath()
	if err != nil {
		return tuiAccountsFile{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tuiAccountsFile{Version: "1"}, nil
		}
		return tuiAccountsFile{}, err
	}
	var f tuiAccountsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return tuiAccountsFile{}, err
	}
	return f, nil
}

// normaliseTuiProvider maps TUI provider names to the canonical SDK provider names
// used as keys in CredentialStore.
func normaliseTuiProvider(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "claudecode", "anthropic", "claude":
		return "anthropic"
	case "openai", "codex":
		return "openai"
	case "gemini":
		return "gemini"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

// isTokenExpired returns true if the token has expired (with a 5-minute buffer).
func isTokenExpired(expiry int64) bool {
	if expiry == 0 {
		return false // unknown expiry — assume valid
	}
	return time.Now().Unix() >= expiry-300
}

// NewCredentialStoreFromTuiAccounts constructs a CredentialStore by reading all
// accounts from ~/.swarm/tui_accounts.json. This is the primary way to wire the
// TUI's multi-account store into the SDK's credential rotation machinery without
// requiring an AccountRegistry or any TUI package imports.
//
// All non-expired accounts for every provider are loaded; the active account for
// each provider is inserted first so it is tried first in the rotation order.
//
// If policy is nil the default retry policy is used (rotate on rate-limit + payment errors).
// Returns an empty store (not an error) if tui_accounts.json does not exist.
func NewCredentialStoreFromTuiAccounts(policy *profiles.RetryPolicy) (*CredentialStore, error) {
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

	f, err := loadTuiAccounts()
	if err != nil {
		return cs, err
	}

	// Two-pass: active accounts first, then inactive. This gives the rotation
	// order: [active, inactive-1, inactive-2, ...] per provider.
	for _, active := range []bool{true, false} {
		for _, acct := range f.Accounts {
			if acct.IsActive != active {
				continue
			}
			if len(acct.TokenData) == 0 {
				continue
			}

			var td tuiTokenData
			if err := json.Unmarshal(acct.TokenData, &td); err != nil {
				continue
			}

			// Determine the usable token string and whether it is OAuth.
			tokenStr := td.AccessToken
			isOAuth := true
			if tokenStr == "" {
				tokenStr = td.APIKey
				isOAuth = false
			}
			if tokenStr == "" {
				continue
			}

			// Skip clearly expired tokens.
			expiry := td.Expiry
			if expiry == 0 {
				expiry = td.ExpiresAt
			}
			if isTokenExpired(expiry) {
				// Expired OAuth tokens can still be refreshed by the provider, but
				// including them here would cause unnecessary 401s before rotation.
				// Skip them; the TUI's refresh loop handles keeping tokens current.
				continue
			}

			providerName := normaliseTuiProvider(acct.Provider)
			cred := Credential{
				Provider:  providerName,
				AccountID: acct.ID,
				Token:     tokenStr,
				IsOAuth:   isOAuth,
			}
			cs.credentials[providerName] = append(cs.credentials[providerName], cred)
		}
	}

	return cs, nil
}

// TuiAccountCount returns the number of accounts in tui_accounts.json for a given
// provider, or across all providers if providerName is empty. Useful for the TUI
// to display account counts without importing TUI settings packages.
func TuiAccountCount(providerName string) int {
	f, err := loadTuiAccounts()
	if err != nil {
		return 0
	}
	count := 0
	for _, a := range f.Accounts {
		if providerName == "" || strings.EqualFold(normaliseTuiProvider(a.Provider), providerName) {
			count++
		}
	}
	return count
}
