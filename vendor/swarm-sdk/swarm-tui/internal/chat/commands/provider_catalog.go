package commands

import "strings"

func intPointer(v int) *int { return &v }

// KnownProvider is a curated, well-known OpenAI-compatible (or native) provider
// with its base URL and api_type prefilled so the user does not have to look up
// endpoints. Used by the `swarmos provider add --preset` CLI flag and the
// interactive add-provider catalog picker (feature E).
type KnownProvider struct {
	// Key is the stable, lowercase identifier used by --preset and stored as
	// the provider Name when the user does not override it.
	Key string
	// DisplayName is the human-facing label.
	DisplayName string
	// APIType is one of: "openai-compatible", "openai", "anthropic".
	APIType string
	// BaseURL is the default API endpoint (models are fetched from {BaseURL}/models).
	BaseURL string
	// AuthType is "api_key" or "oauth".
	AuthType string
	// Color is a suggested accent hex color.
	Color string
	// EnvKey is the environment variable the provider's key is conventionally
	// read from (informational; helps the CLI/form suggest a source).
	EnvKey string
	// Docs is the URL where a user obtains an API key.
	Docs string
	// HTTPMaxRetries defaults transport retries for the preset.
	HTTPMaxRetries *int
}

// knownProviders is the built-in catalog. Kept alphabetical by DisplayName.
// All are OpenAI-compatible unless noted. Endpoints verified against each
// provider's public docs; every entry exposes GET {BaseURL}/models.
var knownProviders = []KnownProvider{
	{Key: "cerebras", DisplayName: "Cerebras", APIType: "openai-compatible", BaseURL: "https://api.cerebras.ai/v1", AuthType: "api_key", Color: "#F97316", EnvKey: "CEREBRAS_API_KEY", Docs: "https://cloud.cerebras.ai"},
	{Key: "deepseek", DisplayName: "DeepSeek", APIType: "openai-compatible", BaseURL: "https://api.deepseek.com/v1", AuthType: "api_key", Color: "#4D6BFE", EnvKey: "DEEPSEEK_API_KEY", Docs: "https://platform.deepseek.com"},
	{Key: "fireworks", DisplayName: "Fireworks AI", APIType: "openai-compatible", BaseURL: "https://api.fireworks.ai/inference/v1", AuthType: "api_key", Color: "#7C3AED", EnvKey: "FIREWORKS_API_KEY", Docs: "https://fireworks.ai"},
	{Key: "groq", DisplayName: "Groq", APIType: "openai-compatible", BaseURL: "https://api.groq.com/openai/v1", AuthType: "api_key", Color: "#F55036", EnvKey: "GROQ_API_KEY", Docs: "https://console.groq.com"},
	{Key: "mistral", DisplayName: "Mistral AI", APIType: "openai-compatible", BaseURL: "https://api.mistral.ai/v1", AuthType: "api_key", Color: "#FF7000", EnvKey: "MISTRAL_API_KEY", Docs: "https://console.mistral.ai"},
	{Key: "moonshot", DisplayName: "Moonshot (Kimi)", APIType: "openai-compatible", BaseURL: "https://api.moonshot.ai/v1", AuthType: "api_key", Color: "#111111", EnvKey: "MOONSHOT_API_KEY", Docs: "https://platform.moonshot.ai"},
	{Key: "openrouter", DisplayName: "OpenRouter", APIType: "openai-compatible", BaseURL: "https://openrouter.ai/api/v1", AuthType: "api_key", Color: "#6467F2", EnvKey: "OPENROUTER_API_KEY", Docs: "https://openrouter.ai/keys"},
	{Key: "perplexity", DisplayName: "Perplexity", APIType: "openai-compatible", BaseURL: "https://api.perplexity.ai", AuthType: "api_key", Color: "#20808D", EnvKey: "PERPLEXITY_API_KEY", Docs: "https://www.perplexity.ai/settings/api"},
	{Key: "plexus", DisplayName: "Plexus Gateway", APIType: "openai-compatible", BaseURL: "http://localhost:4000/v1", AuthType: "api_key", Color: "#7C3AED", EnvKey: "PLEXUS_API_KEY", Docs: "https://github.com/mcowger/plexus", HTTPMaxRetries: intPointer(0)},
	{Key: "together", DisplayName: "Together AI", APIType: "openai-compatible", BaseURL: "https://api.together.xyz/v1", AuthType: "api_key", Color: "#0F6FFF", EnvKey: "TOGETHER_API_KEY", Docs: "https://api.together.ai"},
	{Key: "xai", DisplayName: "xAI (Grok)", APIType: "openai-compatible", BaseURL: "https://api.x.ai/v1", AuthType: "api_key", Color: "#000000", EnvKey: "XAI_API_KEY", Docs: "https://console.x.ai"},
	{Key: "zai", DisplayName: "Z.AI (GLM)", APIType: "openai-compatible", BaseURL: "https://api.z.ai/api/paas/v4", AuthType: "api_key", Color: "#2563EB", EnvKey: "ZAI_API_KEY", Docs: "https://z.ai"},
}

// KnownProviders returns the built-in catalog (a copy is not made; callers must
// not mutate entries).
func KnownProviders() []KnownProvider {
	return knownProviders
}

// LookupKnownProvider finds a catalog entry by its Key or DisplayName
// (case-insensitive). Returns the entry and true if found.
func LookupKnownProvider(name string) (KnownProvider, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, p := range knownProviders {
		if strings.ToLower(p.Key) == n || strings.ToLower(p.DisplayName) == n {
			return p, true
		}
	}
	return KnownProvider{}, false
}

// KnownProviderKeys returns the preset keys, for CLI help and validation.
func KnownProviderKeys() []string {
	keys := make([]string, 0, len(knownProviders))
	for _, p := range knownProviders {
		keys = append(keys, p.Key)
	}
	return keys
}
