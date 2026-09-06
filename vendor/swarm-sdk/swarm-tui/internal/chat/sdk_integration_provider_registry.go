package chat

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/codex"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/cursor"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/gemini"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
)

// getProviderConfigForResolver resolves provider configuration for Task tool.
// This function loads credentials and base URL for a given provider.
func getProviderConfigForResolver(providerName string, logger observability.Logger, tracer observability.Tracer) (provider.Config, error) {
	// Lowercase the provider name for case-insensitive matching but do NOT
	// call NormalizeProviderName — that rewrites custom names to canonical
	// forms (e.g. "wafer" → "wafer.ai"), breaking registry lookups.  The
	// api_type from providers.json (or the name-based resolver) is the
	// source of truth for the translation layer, not the name.
	lowered := strings.ToLower(strings.TrimSpace(providerName))

	// Try to load provider config from file
	providerCfg, cfgErr := getProviderConfigFromFile(providerName)

	// Determine API type and base URL
	var apiType string
	var configBaseURL string
	var authType string
	var httpMaxRetries *int
	if cfgErr == nil && providerCfg != nil && providerCfg.APIType != "" {
		apiType = providerCfg.APIType
		configBaseURL = providerCfg.BaseURL
		authType = providerCfg.Type
		httpMaxRetries = providerCfg.HTTPMaxRetries
	} else {
		// Fallback: resolve from provider name (covers legacy configs missing api_type)
		resolved := resolveAPITypeFromName(providerName)
		apiType = resolved.APIType
		configBaseURL = resolved.DefaultBaseURL
		if providerCfg != nil {
			authType = providerCfg.Type
			// Prefer the file's base URL if present, even when api_type was missing
			if providerCfg.BaseURL != "" {
				configBaseURL = providerCfg.BaseURL
			}
		}
	}

	// Safety guard: custom providers saved with api_type="openai" (via the OpenAI preset)
	// should use openai-compatible auth, not OPENAI_API_KEY.
	if apiType == "openai" && lowered != "openai" && lowered != "codex" {
		apiType = "openai-compatible"
	}

	// Load credentials based on API type
	var apiKey string
	var baseURL string
	var isOAuth bool

	switch apiType {
	case "gemini":
		auth, err := getGeminiAuth(context.Background(), authType, providerName)
		if err != nil {
			return provider.Config{}, err
		}
		apiKey = auth.apiKey
		baseURL = auth.baseURL
		if baseURL == "" {
			baseURL = configBaseURL
		}
		isOAuth = auth.isOAuth

	case "openai":
		auth, err := getOpenAIAuth(context.Background(), authType, providerName)
		if err != nil {
			return provider.Config{}, err
		}
		apiKey = auth.apiKey
		baseURL = auth.baseURL
		if auth.isOAuth {
			if baseURL == "" {
				baseURL = configBaseURL
			}
			baseURL = normalizeOpenAIOAuthBaseURL(baseURL)
		}
		isOAuth = auth.isOAuth

	case "openai-compatible":
		auth, err := getOpenAICompatibleAuth(providerName)
		if err != nil {
			return provider.Config{}, err
		}
		apiKey = auth.apiKey
		// providers.json (configBaseURL) is what the settings UI edits, so it
		// wins; auth.baseURL may be a stale copy from credentials.json saved
		// when the key was first entered.
		baseURL = configBaseURL
		if baseURL == "" {
			baseURL = auth.baseURL
		}
		isOAuth = auth.isOAuth

	case "anthropic":
		auth, err := getAnthropicAuth(authType, providerName)
		if err != nil {
			return provider.Config{}, err
		}
		apiKey = auth.apiKey
		baseURL = auth.baseURL
		isOAuth = auth.isOAuth

	default:
		return provider.Config{}, fmt.Errorf("unsupported API type: %s", apiType)
	}

	return provider.Config{
		Name:           lowered,
		APIKey:         apiKey,
		BaseURL:        baseURL,
		HTTPMaxRetries: httpMaxRetries,
		Custom: map[string]any{
			"is_oauth": isOAuth,
			"logger":   logger,
			"tracer":   tracer,
		},
	}, nil
}

// registerAllProviders loads all providers from providers.json and registers them in the provider registry.
// This ensures that Task and other agent-spawning tools can use any configured provider.
func registerAllProviders(registry *provider.SimpleRegistry, logger observability.Logger, tracer observability.Tracer, isOAuth bool) error {

	providers, err := loadAllProviders()
	if err != nil {
		logger.Warn(context.Background(), "failed to load providers, only current provider will be available",
			observability.F("error", err.Error()))
		return nil // Non-fatal: we'll still have the current provider registered
	}

	// Register each provider
	for _, pc := range providers {
		// Don't skip based on Available flag - let provider factory handle missing credentials
		// This ensures all providers are registered so custom agents can use any provider

		// Use the user-chosen name (lowercased for case-insensitive matching) as the
		// registry key.  Do NOT call NormalizeProviderName here — that function rewrites
		// custom names to built-in canonical forms (e.g. "wafer" → "wafer.ai", "codex" →
		// "openai"), which breaks registry lookups when the caller still uses the original
		// name.  The api_type field from providers.json (or the fallback resolver) is the
		// sole source of truth for which translation layer to use; the name is just an
		// identity key.
		providerName := strings.ToLower(strings.TrimSpace(pc.Name))

		// Resolve API type: prefer the explicit field, fall back to name-based
		// detection when providers.json entries lack api_type (e.g. older versions).
		resolvedAPIType := pc.APIType
		if resolvedAPIType == "" {
			resolvedAPIType = resolveAPITypeFromName(pc.Name).APIType
		}
		// Safety guard: custom providers saved with api_type="openai" (via the OpenAI
		// preset) must not use OPENAI_API_KEY credentials.
		if resolvedAPIType == "openai" && providerName != "openai" && providerName != "codex" {
			resolvedAPIType = "openai-compatible"
		}

		// Register provider compatibility based on resolved api_type.
		// This allows custom providers (like "local") to work with agents
		// that expect standard providers (like "openai").
		switch resolvedAPIType {
		case "openai", "openai-compatible":
			registry.RegisterOpenAICompatible(providerName)
		case "anthropic":
			registry.RegisterAnthropicCompatible(providerName)
		case "gemini":
			registry.RegisterGeminiCompatible(providerName)
		case "cursor":
			// Cursor is its own wire family (Connect-RPC) — no compatibility
			// registration needed, just register as its own family.
		}

		// Capture pc and resolvedAPIType for closure (avoid loop-variable capture)
		providerConfig := pc
		effectiveAPIType := resolvedAPIType

		// Auto-register alias if the original name differs from the normalized name.
		// This allows fallback chains (which store display names like "ClaudeCode")
		// to resolve to the correct registry entry (e.g., "anthropic").
		if pc.Name != providerName {
			registry.RegisterAlias(pc.Name, providerName)
		}

		// Create provider factory function
		err := registry.Register(providerName, func(cfg provider.Config) (provider.Provider, error) {
			// Use the pre-resolved API type (handles empty api_type from disk)
			switch effectiveAPIType {
			case "cursor":
				// Cursor (Anysphere) — Connect-RPC provider.
				token, err := getCursorAccessToken()
				if err != nil {
					return nil, fmt.Errorf("cursor: %w", err)
				}
				cursorBaseURL := cfg.BaseURL
				if cursorBaseURL == "" {
					cursorBaseURL = providerConfig.BaseURL
				}
				if cursorBaseURL == "" {
					cursorBaseURL = "https://api2.cursor.sh"
				}
				cursorCfg := cursor.Config{
					APIKey:  token,
					BaseURL: cursorBaseURL,
					Name:    providerName,
				}
				return cursor.New(cursorCfg)

			case "gemini":
				// Check for API key or Client Secret (for OAuth)
				var apiKey string
				var authMode gemini.AuthMode = gemini.AuthModeOAuth

				if cfg.APIKey != "" {
					apiKey = cfg.APIKey
					authMode = gemini.AuthModeAPIKey
				} else if os.Getenv("GEMINI_API_KEY") != "" {
					apiKey = os.Getenv("GEMINI_API_KEY")
					authMode = gemini.AuthModeAPIKey
				}

				geminiCfg := gemini.Config{
					AuthMode: authMode,
					Model:    cfg.Model,
					APIKey:   apiKey,
					Timeout:  300,
					Logger:   logger,
					Tracer:   tracer,
				}

				if cfg.BaseURL != "" {
					geminiCfg.BaseURL = cfg.BaseURL
				} else if providerConfig.BaseURL != "" {
					geminiCfg.BaseURL = providerConfig.BaseURL
				}

				return gemini.New(geminiCfg)

			case "anthropic":
				// Resolve token: explicit key > stored OAuth token > env vars.
				// Always re-read oauth.json at call time so that tokens added or
				// switched in the settings screen are picked up without restarting.
				var token string
				resolvedOAuth := false
				if cfg.APIKey != "" {
					token = cfg.APIKey
				} else {
					if oauthTok, err := anthropic.GetStoredOAuthToken(); err == nil && oauthTok != nil && oauthTok.AccessToken != "" {
						if anthropic.IsTokenExpired(oauthTok) {
							if refreshed, err := anthropic.RefreshAndStoreToken(); err == nil {
								oauthTok = refreshed
							}
						}
						token = oauthTok.AccessToken
						resolvedOAuth = true
					}
				}
				if token == "" {
					token = os.Getenv("ANTHROPIC_API_KEY")
					if token == "" {
						token = os.Getenv("CLAUDE_API_KEY")
					}
				}

				if token == "" {
					return nil, fmt.Errorf("no API key or OAuth token found for anthropic provider")
				}

				// Build anthropic config
				anthropicCfg := anthropic.Config{
					APIKey:       token,
					DefaultModel: cfg.Model,
					IsOAuth:      resolvedOAuth,
					Timeout:      -1, // No timeout for streaming
					MaxRetries:   3,
				}

				if cfg.BaseURL != "" {
					anthropicCfg.BaseURL = cfg.BaseURL
				}

				// Extract beta headers from custom config if present
				if betaHeaders, ok := cfg.Custom["beta_headers"].([]string); ok {
					anthropicCfg.BetaHeaders = betaHeaders
				}

				anthropicCfg.Logger = logger
				anthropicCfg.Tracer = tracer
				return anthropic.New(anthropicCfg)

			case "openai", "openai-compatible":
				if providerConfig.APIType == "openai" && strings.EqualFold(providerConfig.Type, "oauth") {
					var token *openai.OAuthToken
					var err error
					token, err = openai.RefreshAndStoreToken(context.Background())
					if err != nil {
						token, err = openai.GetStoredOAuthToken()
						if err != nil {
							return nil, fmt.Errorf("no OpenAI OAuth credentials found - run /auth")
						}
					}
					if token == nil || token.AccessToken == "" {
						return nil, fmt.Errorf("OpenAI OAuth token missing access token; run /auth")
					}

					var accountID string = token.AccountID
					if accountID == "" && token.IDToken != "" {
						accountID = openai.ExtractAccountIDFromIDToken(token.IDToken)
					}
					if accountID == "" {
						return nil, fmt.Errorf("OpenAI OAuth token missing account id; run /auth")
					}

					var codexCfg codex.Config = codex.Config{
						AccessToken: token.AccessToken,
						AccountID:   accountID,
						Logger:      logger,
						Tracer:      tracer,
						// Catalog-driven per-model wire conventions; nil falls
						// back to the provider's prefix heuristics.
						ModelOptions: codex.ModelWireOptionsFromCache(),
					}
					effectiveBaseURL := cfg.BaseURL
					if effectiveBaseURL == "" {
						effectiveBaseURL = providerConfig.BaseURL
					}
					effectiveBaseURL = normalizeOpenAIOAuthBaseURL(effectiveBaseURL)
					codexCfg.BaseURL = effectiveBaseURL
					codexCfg.ExtraQueryParams, codexCfg.ExtraHeaders = resolveCodexPassthrough(&providerConfig)

					return codex.New(codexCfg)
				}

				apiKey := cfg.APIKey
				if apiKey == "" {
					apiKey = os.Getenv(strings.ToUpper(providerName) + "_API_KEY")
				}

				// Also check credentials.json if env var not found
				if apiKey == "" {
					if auth, err := getOpenAICompatibleAuth(providerName); err == nil {
						apiKey = auth.apiKey
						// Use base URL from credentials if available
						if auth.baseURL != "" && cfg.BaseURL == "" && providerConfig.BaseURL == "" {
							cfg.BaseURL = auth.baseURL
						}
					}
				}

				if apiKey == "" {
					return nil, fmt.Errorf("no API key found for %s provider", providerName)
				}

				openaiCfg := openai.Config{
					APIKey:         apiKey,
					Name:           providerName,
					Logger:         logger,
					Tracer:         tracer,
					HTTPMaxRetries: providerConfig.HTTPMaxRetries,
				}

				// Set base URL from config or from provider metadata
				if cfg.BaseURL != "" {
					openaiCfg.BaseURL = cfg.BaseURL
				} else if providerConfig.BaseURL != "" {
					openaiCfg.BaseURL = providerConfig.BaseURL
				}

				return openai.New(openaiCfg)

			default:
				// This should not happen since effectiveAPIType is always
				// one of the cases above, but handle gracefully.
				logger.Warn(context.Background(), "unhandled API type for provider",
					observability.F("provider", providerConfig.Name),
					observability.F("api_type", effectiveAPIType))
				return nil, fmt.Errorf("unhandled API type: %s (provider: %s)", effectiveAPIType, providerName)
			}
		})

		if err != nil {
			logger.Warn(context.Background(), "failed to register provider",
				observability.F("provider", providerName),
				observability.F("error", err.Error()))
		} else {
			logger.Info(context.Background(), "provider.registered_for_agents",
				observability.F("provider", providerName),
				observability.F("api_type", resolvedAPIType))
			// CRITICAL: the agent factory (sub-agents / Task tool) looks providers
			// up by provider.NormalizeProviderName(def.Provider) — e.g. an OAuth
			// "claudecode" session resolves to "anthropic". The factory key above is
			// the RAW name (claudecode), so without this alias IsRegistered("anthropic")
			// is false and EVERY sub-agent spawn fails at startup with
			// "provider 'claudecode' (normalized: 'anthropic') is not registered".
			// Alias normalized -> rawName so the factory's normalized lookup resolves.
			// (resolve() only follows an alias when its TARGET is a real factory, so
			// the alias must point AT the registered raw name, not the reverse.)
			ensureNormalizedAlias(registry, providerName)
		}
	}

	return nil
}

// ensureNormalizedAlias makes the normalized form of rawName resolve to the
// factory registered under rawName. It is a no-op when the normalized name
// equals rawName or when a factory already exists under the normalized name.
// Shared by startup registration (registerAllProviders), the current-provider
// fallback, and ReloadProvider so the factory's NormalizeProviderName lookup
// always resolves regardless of which path registered the provider.
func ensureNormalizedAlias(registry *provider.SimpleRegistry, rawName string) {
	if registry == nil {
		return
	}
	raw := strings.ToLower(strings.TrimSpace(rawName))
	if raw == "" {
		return
	}
	normalized := provider.NormalizeProviderName(raw)
	if normalized == "" || strings.EqualFold(normalized, raw) {
		return
	}
	// Only alias if the normalized name isn't already a real factory of its own
	// (don't shadow a legitimately-registered "anthropic" provider entry).
	if registry.IsRegistered(normalized) {
		return
	}
	registry.RegisterAlias(normalized, raw)
}
