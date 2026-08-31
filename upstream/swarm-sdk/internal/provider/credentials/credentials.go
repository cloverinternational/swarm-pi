// Package credentials is the single credential-detection engine for the
// provider catalog. It answers one question — "does the user have usable
// credentials for this provider?" — by consulting every store a credential can
// live in: catalog-declared env vars, the wire family's OAuth token store,
// ~/.swarm/config/credentials.json, and the account registries.
//
// Availability in consumers (e.g. the TUI's providers.json `available` flag)
// is DERIVED from this package on every load rather than hand-maintained, so
// adding a provider to the catalog is all that is needed for login → active
// to work end-to-end.
//
// This package must stay a sibling of the provider subpackages: the token
// stores (provider/anthropic, provider/openai, ...) import internal/provider,
// so the detection engine that imports the stores cannot live in
// internal/provider itself without a cycle.
package credentials

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/gemini"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/xai"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// envAliases maps normalized provider names to the env vars that carry their
// API keys, for providers that are not (yet) in the built-in catalog. Built-in
// entries declare their env vars in the catalog itself (BuiltinProvider.EnvVars).
var envAliases = map[string][]string{
	"openrouter":  {"OPENROUTER_API_KEY"},
	"cerebras":    {"CEREBRAS_API_KEY"},
	"groq":        {"GROQ_API_KEY"},
	"fireworks":   {"FIREWORKS_API_KEY"},
	"deepseek":    {"DEEPSEEK_API_KEY"},
	"perplexity":  {"PERPLEXITY_API_KEY"},
	"together":    {"TOGETHER_API_KEY"},
	"together-ai": {"TOGETHER_API_KEY"},
	"togetherai":  {"TOGETHER_API_KEY"},
	"z.ai":        {"ZAI_API_KEY"},
	"zai":         {"ZAI_API_KEY"},
	"gemini":      {"GEMINI_API_KEY"},
	"openai":      {"OPENAI_API_KEY"},
	"anthropic":   {"ANTHROPIC_API_KEY", "CLAUDE_API_KEY"},
	"xai":         {"XAI_API_KEY"},
	"cursor":      {"CURSOR_API_KEY"},
}

// HasStored reports whether usable credentials exist for the named provider,
// scoped to how that provider authenticates:
//
//   - Built-in OAuth entries: the wire family's stored OAuth token, or an
//     account for the family in either account registry. Env API keys do NOT
//     count — an exported OPENAI_API_KEY must not light up the codex OAuth row.
//   - Built-in API-key entries: catalog-declared env vars, or an entry in
//     ~/.swarm/config/credentials.json. OAuth tokens do not count.
//   - Unknown/custom names: env aliases plus the generic <NAME>_API_KEY
//     pattern, credentials.json, or an exact-name account.
//
// All probes are best-effort: any I/O or parse error counts as "no credential".
func HasStored(name string) bool {
	if bp, ok := provider.LookupBuiltinProvider(name); ok {
		if bp.IsOAuth() {
			return hasOAuthTokenForFamily(bp.APIType) || hasAccountForFamily(bp.Name)
		}
		return anyEnvSet(bp.EnvVars) || hasCredentialsFileEntry(bp.Name)
	}
	normalized := strings.ToLower(strings.TrimSpace(name))
	if anyEnvSet(envAliases[normalized]) || anyEnvSet([]string{genericEnvKey(normalized)}) {
		return true
	}
	return hasCredentialsFileEntry(name) || hasAccountExact(name)
}

// HasAnyForFamily reports whether ANY credential exists that could drive the
// named provider's wire family — OAuth token, env key, credentials.json entry,
// or account — regardless of auth method. This is the loose check used for
// startup auto-selection ("can we use this provider at all"), not for the
// per-row availability shown in settings (use HasStored for that).
func HasAnyForFamily(name string) bool {
	apiType := familyOf(name)
	if apiType != "" && hasOAuthTokenForFamily(apiType) {
		return true
	}
	normalized := provider.NormalizeProviderName(name)
	if anyEnvSet(envAliases[normalized]) || anyEnvSet([]string{genericEnvKey(normalized)}) {
		return true
	}
	if bp, ok := provider.LookupBuiltinProvider(name); ok && anyEnvSet(bp.EnvVars) {
		return true
	}
	return hasCredentialsFileEntry(name) || hasAccountForFamily(name)
}

// familyOf resolves a provider name to its wire family via the catalog, or ""
// when the name is not a built-in and has no canonical mapping.
func familyOf(name string) string {
	if bp, ok := provider.LookupBuiltinProvider(name); ok {
		return bp.APIType
	}
	normalized := provider.NormalizeProviderName(name)
	if bp, ok := provider.LookupBuiltinProvider(normalized); ok {
		return bp.APIType
	}
	switch normalized {
	case "anthropic", "openai", "gemini", "xai", "cursor":
		return normalized
	}
	return ""
}

// hasOAuthTokenForFamily checks the wire family's on-disk OAuth token store.
func hasOAuthTokenForFamily(apiType string) bool {
	switch apiType {
	case "anthropic":
		tok, err := anthropic.GetStoredOAuthToken()
		return err == nil && tok != nil && tok.AccessToken != ""
	case "openai":
		tok, err := openai.GetStoredOAuthToken()
		return err == nil && tok != nil && (tok.AccessToken != "" || tok.APIKey != "")
	case "gemini":
		return gemini.HasStoredOAuthToken()
	case "xai":
		tok, err := xai.GetStoredOAuthToken()
		return err == nil && tok != nil && tok.AccessToken != ""
	case "cursor":
		return cursorCLIToken() != ""
	}
	return false
}

// cursorCLIToken reads the access token captured by `cursor-agent login`
// (~/.cursor/cli-config.json). Refresh/expiry handling is the consumer's job;
// presence is enough to count as a stored credential.
func cursorCLIToken() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".cursor", "cli-config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.AccessToken)
}

func anyEnvSet(keys []string) bool {
	for _, k := range keys {
		if k != "" && os.Getenv(k) != "" {
			return true
		}
	}
	return false
}

// genericEnvKey derives the conventional <NAME>_API_KEY env var for a custom
// provider name ("mylocal" → "MYLOCAL_API_KEY", "z.ai" → "ZAI_API_KEY").
func genericEnvKey(normalized string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(normalized) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String() + "_API_KEY"
}

// hasCredentialsFileEntry checks ~/.swarm/config/credentials.json for a
// non-empty api_key under the given provider name (case-insensitive).
func hasCredentialsFileEntry(name string) bool {
	data, err := os.ReadFile(paths.CredentialsFile())
	if err != nil {
		return false
	}
	var creds struct {
		Providers map[string]struct {
			APIKey string `json:"api_key"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return false
	}
	for key, cred := range creds.Providers {
		if strings.EqualFold(key, name) && cred.APIKey != "" {
			return true
		}
	}
	return false
}

// accountProviders returns the provider labels of every account in both
// account registries (~/.swarm/tui_accounts.json and
// ~/.swarm/accounts/accounts.json).
func accountProviders() []string {
	var out []string
	for _, path := range []string{
		paths.In("tui_accounts.json"),
		paths.In("accounts", "accounts.json"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var f struct {
			Accounts []struct {
				Provider string `json:"provider"`
			} `json:"accounts"`
		}
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}
		for _, a := range f.Accounts {
			if a.Provider != "" {
				out = append(out, a.Provider)
			}
		}
	}
	return out
}

// hasAccountForFamily reports whether either account registry holds an account
// whose provider label belongs to the same wire family as name (so accounts
// stored as "OpenAI" by the OAuth flow count for the "codex" row).
func hasAccountForFamily(name string) bool {
	for _, p := range accountProviders() {
		if provider.ProvidersMatch(name, p) {
			return true
		}
	}
	return false
}

// hasAccountExact reports whether either account registry holds an account
// with exactly this provider label (case-insensitive). Used for custom
// providers, where wire-family matching would be too broad.
func hasAccountExact(name string) bool {
	for _, p := range accountProviders() {
		if strings.EqualFold(p, name) {
			return true
		}
	}
	return false
}
