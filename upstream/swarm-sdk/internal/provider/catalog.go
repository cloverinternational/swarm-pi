package provider

import "strings"

func intPtr(v int) *int { return &v }

// BuiltinProvider describes a provider that swarm ships as a first-class,
// always-available login/configuration option. It is the canonical source of
// truth for *which* providers exist and *how* a user authenticates with them
// (OAuth device flow vs. API key), independent of the catwalk model catalog.
//
// The catwalk-backed registry (see package registry) owns model metadata; this
// catalog owns provider identity and auth method. Consumers (e.g. the TUI) join
// the two by APIType to build their own richer config structs, and use this
// list to reconcile/self-heal a persisted provider configuration that has lost
// a built-in entry.
//
// Keeping this in the SDK — alongside the OAuth device-flow implementations in
// provider/{anthropic,gemini,openai,xai} and the StandardAliases table — means
// there is exactly one place to add or change a built-in provider, so the
// defaults path and the self-heal path can never drift apart.
type BuiltinProvider struct {
	// Name is the canonical provider identifier (e.g. "ClaudeCode"). It is the
	// key callers persist and match on; it also resolves through StandardAliases
	// to a registered factory.
	Name string
	// DisplayName is the human-facing label, including an auth-method suffix
	// (e.g. "Claude Code (OAuth)").
	DisplayName string
	// APIType is the wire/provider family used to route to the correct provider
	// factory (e.g. "anthropic", "openai", "gemini", "xai", "openai-compatible").
	APIType string
	// RegistryID is the catwalk/registry provider id whose models back this
	// entry. It usually equals APIType; it differs where the wire type and the
	// catalog id diverge (e.g. GLM uses APIType "openai-compatible" but draws
	// models from registry id "zhipu"). Empty means "same as APIType".
	RegistryID string
	// AuthType is how the user authenticates: AuthOAuth or AuthAPIKey.
	AuthType string
	// Color is a suggested UI accent (hex). Presentation hint only.
	Color string
	// BaseURL is a non-default API endpoint, when the provider requires one.
	BaseURL string
	// HTTPMaxRetries overrides shared transport retries for this preset.
	HTTPMaxRetries *int
	// DefaultAvailable marks a provider that is usable out of the box without
	// any stored credential (true only for the bundled default, ClaudeCode).
	DefaultAvailable bool
	// EnvVars lists environment variables whose presence counts as a usable
	// credential for this provider (e.g. OPENAI_API_KEY). Only meaningful for
	// AuthAPIKey entries: OAuth entries authenticate through their token store,
	// and an exported API key must not make an OAuth login appear active.
	EnvVars []string
}

// Auth method constants for BuiltinProvider.AuthType. These match the values
// the TUI persists in providers.json ("oauth" / "api_key").
const (
	AuthOAuth  = "oauth"
	AuthAPIKey = "api_key"
)

// IsOAuth reports whether this provider authenticates via an OAuth device flow.
func (p BuiltinProvider) IsOAuth() bool { return p.AuthType == AuthOAuth }

// builtinProviders is the canonical, ordered list of swarm's first-class
// providers. Add or change a built-in provider here and nowhere else.
var builtinProviders = []BuiltinProvider{
	{
		Name:             "ClaudeCode",
		DisplayName:      "Claude Code (OAuth)",
		APIType:          "anthropic",
		AuthType:         AuthOAuth,
		Color:            "#00FFFF",
		DefaultAvailable: true,
	},
	{
		Name:        "Anthropic",
		DisplayName: "Anthropic (API Key)",
		APIType:     "anthropic",
		AuthType:    AuthAPIKey,
		Color:       "#D4A574",
		EnvVars:     []string{"ANTHROPIC_API_KEY", "CLAUDE_API_KEY"},
	},
	{
		Name:        "OpenAI",
		DisplayName: "OpenAI (API Key)",
		APIType:     "openai",
		AuthType:    AuthAPIKey,
		Color:       "#74C365",
		EnvVars:     []string{"OPENAI_API_KEY"},
	},
	{
		// Codex is the ChatGPT-subscription OAuth flavor of OpenAI: it signs in
		// via the codex device flow (provider/openai/oauth.go), stores its token
		// in the canonical oauth store (~/.swarm/config/oauth/openai.json), and streams against the
		// chatgpt.com/backend-api/codex backend (provider/codex). It shares the
		// "openai" wire family but is a distinct login/config identity — which is
		// why it must be its own catalog entry: the auth screen and availability
		// derivation key off this row, not off the API-key OpenAI entry.
		// RegistryID "codex" is deliberately not catwalk-backed; the TUI seeds a
		// small model list and (with codex model discovery) refreshes it live.
		Name:        "codex",
		DisplayName: "OpenAI Codex (OAuth)",
		APIType:     "openai",
		RegistryID:  "codex",
		AuthType:    AuthOAuth,
		Color:       "#10A37F",
	},
	{
		Name:        "Google",
		DisplayName: "Google Gemini (OAuth)",
		APIType:     "gemini",
		AuthType:    AuthOAuth,
		Color:       "#4285F4",
	},
	{
		Name:        "xai",
		DisplayName: "xAI / Grok (SuperGrok OAuth)",
		APIType:     "xai",
		AuthType:    AuthOAuth,
		Color:       "#F0ABFC",
		BaseURL:     "https://api.x.ai/v1",
	},
	{
		Name:        "xai-api",
		DisplayName: "xAI / Grok (API Key)",
		APIType:     "xai",
		AuthType:    AuthAPIKey,
		Color:       "#C084FC",
		BaseURL:     "https://api.x.ai/v1",
		EnvVars:     []string{"XAI_API_KEY"},
	},
	{
		Name:        "GLM",
		DisplayName: "Zhipu GLM",
		APIType:     "openai-compatible",
		RegistryID:  "zhipu",
		AuthType:    AuthAPIKey,
		Color:       "#FF0000",
		BaseURL:     "https://open.bigmodel.cn/api/paas/v4",
		EnvVars:     []string{"ZHIPU_API_KEY"},
	},
	{
		Name:           "plexus",
		DisplayName:    "Plexus Gateway",
		APIType:        "openai-compatible",
		AuthType:       AuthAPIKey,
		Color:          "#7C3AED",
		BaseURL:        "http://localhost:4000/v1",
		HTTPMaxRetries: intPtr(0),
		EnvVars:        []string{"PLEXUS_API_KEY"},
	},
	{
		// Cursor (Anysphere) authenticates via an OAuth PKCE deeplink flow
		// (the same `cursor-agent login` path): open loginDeepControl, poll
		// /auth/poll, capture the accessToken into ~/.cursor/cli-config.json.
		// Its model catalog is fetched dynamically via the Connect-RPC JSON
		// control-plane at api2.cursor.sh (AiService/AvailableModels); the TUI
		// loads/refreshes models with that captured token and writes them into
		// providers.json.
		Name:        "Cursor",
		DisplayName: "Cursor (OAuth)",
		APIType:     "cursor",
		AuthType:    AuthOAuth,
		Color:       "#6CB6FF",
		BaseURL:     "https://api2.cursor.sh",
	},
}

// BuiltinProviders returns a copy of the canonical built-in provider list.
// The returned slice is safe for the caller to mutate.
func BuiltinProviders() []BuiltinProvider {
	out := make([]BuiltinProvider, len(builtinProviders))
	copy(out, builtinProviders)
	for i := range out {
		if len(out[i].EnvVars) > 0 {
			out[i].EnvVars = append([]string(nil), out[i].EnvVars...)
		}
	}
	return out
}

// BuiltinOAuthProviders returns only the built-in providers that authenticate
// via an OAuth device flow. These are the entries that must appear on an
// authentication/login screen.
func BuiltinOAuthProviders() []BuiltinProvider {
	out := make([]BuiltinProvider, 0, len(builtinProviders))
	for _, p := range builtinProviders {
		if p.IsOAuth() {
			out = append(out, p)
		}
	}
	return out
}

// LookupBuiltinProvider returns the built-in provider whose canonical Name
// matches the given name (case-insensitive), and whether one was found.
func LookupBuiltinProvider(name string) (BuiltinProvider, bool) {
	for _, p := range builtinProviders {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return BuiltinProvider{}, false
}
