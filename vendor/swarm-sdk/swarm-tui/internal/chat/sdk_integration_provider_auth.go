package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/xai"
)

// providerAuth holds resolved authentication credentials for any provider.
type providerAuth struct {
	apiKey      string
	accessToken string
	accountID   string
	baseURL     string
	orgID       string
	isOAuth     bool
}

// genericOAuthToken is a generic token struct that works for all providers
type genericOAuthToken struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	IDToken      string   `json:"id_token,omitempty"`
	APIKey       string   `json:"api_key,omitempty"`
	TokenType    string   `json:"token_type,omitempty"`
	ExpiresAt    any      `json:"expires_at,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	AccountID    string   `json:"account_id,omitempty"`
}

// getProviderAliases returns alternate names that map to the same provider config
// This helps when switching between providers where internal name differs from config name
func getProviderAliases(providerName string) []string {
	normalized := strings.ToLower(strings.TrimSpace(providerName))
	aliases := []string{normalized}

	// Map normalized names to their config names (and vice versa)
	switch normalized {
	case "anthropic":
		aliases = append(aliases, "claudecode", "claude-code", "claude_code")
	case "claudecode", "claude-code", "claude_code":
		aliases = append(aliases, "anthropic")
	case "openai":
		aliases = append(aliases, "codex")
	case "codex":
		aliases = append(aliases, "openai")
	case "gemini":
		aliases = append(aliases, "google", "gemini-code-assist")
	case "google", "gemini-code-assist":
		aliases = append(aliases, "gemini")
	case "xai":
		aliases = append(aliases, "grok", "x-ai", "supergrok", "xai-oauth", "grok-oauth")
	case "grok", "x-ai", "supergrok", "xai-oauth", "grok-oauth":
		aliases = append(aliases, "xai")
	case "cursor":
		aliases = append(aliases, "cursor")
	}

	return aliases
}

// getProviderConfigFromFile loads a specific provider's config from providers.json.
//
// Reads in this priority order, returning the first match:
//  1. The canonical paths.ProvidersFile() store, accepting both the SDK map
//     format ({"fireworks": {...}}) and the legacy TUI array format.
//  2. When SWARM_HOME is not explicitly set, the historical
//     ~/.swarmos/config/providers.json and ~/.swarmos/providers.json locations
//     for pre-migration compatibility.
//
// Custom user-defined provider names (e.g. "fire" pointing at Fireworks with a
// custom api_key) frequently live only in the legacy file. The earlier
// implementation treated the SDK file as authoritative and never consulted the
// legacy file when the SDK file existed but was missing the requested name —
// causing the contract violation "Unknown provider: fire" downstream. We now
// always check every compatible store and return the first match.
func getProviderConfigFromFile(providerName string) (*ProviderConfig, error) {
	aliases := getProviderAliases(providerName)
	configPaths := []string{paths.ProvidersFile()}

	if os.Getenv("SWARM_HOME") == "" {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			configPaths = append(configPaths,
				filepath.Join(home, ".swarmos", "config", "providers.json"),
				filepath.Join(home, ".swarmos", "providers.json"),
			)
		}
	}

	for _, configPath := range configPaths {
		data, readErr := os.ReadFile(configPath)
		if readErr != nil {
			continue
		}
		if cfg := decodeProviderConfigMatch(data, aliases); cfg != nil {
			return cfg, nil
		}
	}

	return nil, fmt.Errorf("provider %s not found in config", providerName)
}

func decodeProviderConfigMatch(data []byte, aliases []string) *ProviderConfig {
	var providerMap map[string]ProviderConfig
	if json.Unmarshal(data, &providerMap) == nil {
		for key, cfg := range providerMap {
			if providerConfigMatchesAliases(key, cfg, aliases) {
				if cfg.Name == "" {
					cfg.Name = key
				}
				return &cfg
			}
		}
	}

	var providers []ProviderConfig
	if json.Unmarshal(data, &providers) == nil {
		for _, cfg := range providers {
			if providerConfigMatchesAliases("", cfg, aliases) {
				return &cfg
			}
		}
	}

	return nil
}

func providerConfigMatchesAliases(key string, cfg ProviderConfig, aliases []string) bool {
	for _, candidate := range []string{key, cfg.Name, cfg.DisplayName} {
		if slices.Contains(aliases, strings.ToLower(strings.TrimSpace(candidate))) {
			return true
		}
	}
	return false
}

func loadProviderCredentials(providerName string) (string, string, error) {
	var home string
	var err error
	home, err = os.UserHomeDir()
	if err != nil {
		return "", "", err
	}

	var credsPath string = filepath.Join(home, ".swarmos", "credentials.json")
	var data []byte
	data, err = os.ReadFile(credsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil
		}
		return "", "", err
	}

	var creds struct {
		Providers map[string]struct {
			APIKey  string `json:"api_key"`
			BaseURL string `json:"base_url"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", "", err
	}

	// Try provider name and all aliases so e.g. "google" finds credentials stored under "gemini"
	aliases := getProviderAliases(providerName)
	for _, alias := range aliases {
		for key, cred := range creds.Providers {
			if strings.ToLower(key) == alias {
				return cred.APIKey, cred.BaseURL, nil
			}
		}
	}

	return "", "", nil
}

// getOpenAICompatibleAuth gets credentials for OpenAI-compatible providers (Cerebras, OpenRouter, etc.)
func getOpenAICompatibleAuth(providerName string) (providerAuth, error) {
	normalized := strings.ToLower(providerName)

	// Cursor: the access token is captured by the OAuth login (or `cursor-agent
	// login`) into ~/.cursor/cli-config.json and ~/.swarmos/accounts/cursor/.
	// Resolve it the same way the model-refresh path does.
	if normalized == "cursor" {
		if token, err := getCursorAccessToken(); err == nil && token != "" {
			return providerAuth{apiKey: token, baseURL: cursorAPIBaseURL(), isOAuth: true}, nil
		}
		return providerAuth{}, fmt.Errorf(
			"no Cursor credentials found — run /auth to log in with Cursor (OAuth), or set CURSOR_API_KEY")
	}

	// Check provider-specific env var first
	var envKey string
	switch normalized {
	case "cerebras":
		envKey = "CEREBRAS_API_KEY"
	case "openrouter":
		envKey = "OPENROUTER_API_KEY"
	case "fireworks":
		envKey = "FIREWORKS_API_KEY"
	case "groq":
		envKey = "GROQ_API_KEY"
	case "together", "together-ai", "togetherai":
		envKey = "TOGETHER_API_KEY"
	case "deepseek":
		envKey = "DEEPSEEK_API_KEY"
	case "perplexity":
		envKey = "PERPLEXITY_API_KEY"
	case "plexus":
		// Keep compatibility with the first-class Plexus installer while the
		// Settings flow migrates users to an encrypted Vault reference.
		if key := os.Getenv("SWARM_PLEXUS_API_KEY"); key != "" {
			return providerAuth{apiKey: key, isOAuth: false}, nil
		}
		envKey = "PLEXUS_API_KEY"
	case "z.ai", "zai":
		envKey = "ZAI_API_KEY"
	default:
		envKey = strings.ToUpper(providerName) + "_API_KEY"
	}

	if key := os.Getenv(envKey); key != "" {
		return providerAuth{
			apiKey:  key,
			isOAuth: false,
		}, nil
	}

	// Try credentials.json
	home, err := os.UserHomeDir()
	if err == nil {
		credsPath := filepath.Join(home, ".swarmos", "credentials.json")
		if data, err := os.ReadFile(credsPath); err == nil {
			var creds struct {
				Providers map[string]struct {
					APIKey  string `json:"api_key"`
					BaseURL string `json:"base_url"`
				} `json:"providers"`
			}
			if err := json.Unmarshal(data, &creds); err == nil {
				// Case-insensitive provider lookup
				normalizedName := strings.ToLower(providerName)
				for key, cred := range creds.Providers {
					if strings.ToLower(key) == normalizedName && cred.APIKey != "" {
						keyPreview := cred.APIKey
						if len(keyPreview) > 10 {
							keyPreview = keyPreview[:10]
						}
						return providerAuth{
							apiKey:  cred.APIKey,
							baseURL: cred.BaseURL,
							isOAuth: false,
						}, nil
					}
				}
			} else {
			}
		} else {
		}
	}

	// Try accounts registry (credentials stored via /auth command)
	if token := tryLoadAccountFromRegistry(normalized); token != nil {
		if token.APIKey != "" {
			return providerAuth{apiKey: token.APIKey, isOAuth: false}, nil
		}
		if token.AccessToken != "" {
			return providerAuth{apiKey: token.AccessToken, isOAuth: false}, nil
		}
	}

	// Try providers.json api_key field (set via settings UI)
	if apiKey, baseURL := getAPIKeyFromProviders(normalized); apiKey != "" {
		return providerAuth{apiKey: apiKey, baseURL: baseURL, isOAuth: false}, nil
	}

	return providerAuth{}, fmt.Errorf("no %s credentials found - set %s or configure in settings", providerName, envKey)
}

// tryLoadAccountFromRegistry attempts to load a token from the new account registry system
// Returns nil if no account found (falls back to old system is caller's responsibility)
func tryLoadAccountFromRegistry(provider string) *genericOAuthToken {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	// Look for token file in new registry format: ~/.swarmos/accounts/{provider}/account-*.json
	accountsDir := filepath.Join(homeDir, ".swarmos", "accounts", provider)
	entries, err := os.ReadDir(accountsDir)
	if err != nil {
		// Directory doesn't exist, no accounts in new format
		return nil
	}

	// Find first account file
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "account-") && strings.HasSuffix(entry.Name(), ".json") {
			tokenPath := filepath.Join(accountsDir, entry.Name())
			data, err := os.ReadFile(tokenPath)
			if err != nil {
				continue
			}

			var token genericOAuthToken
			if err := json.Unmarshal(data, &token); err != nil {
				continue
			}

			if token.AccessToken != "" {
				return &token
			}
		}
	}

	return nil
}

func getAnthropicAuth(authType string, providerName string) (providerAuth, error) {
	var normalizedAuthType string = strings.ToLower(strings.TrimSpace(authType))
	if normalizedAuthType == "" {
		// Default to api_key so that a stored API key is always reachable.
		// OAuth tokens are still tried first (below) for backwards compatibility
		// with users who authenticated via OAuth before explicitly configuring a type.
		normalizedAuthType = "api_key"
	}

	// Try OAuth first (always, regardless of authType)
	// Try old OAuth token loading first (for compatibility)
	if token, err := anthropic.GetStoredOAuthToken(); err == nil && token != nil && token.AccessToken != "" {
		if anthropic.IsTokenExpired(token) {
			if refreshed, err := anthropic.RefreshAndStoreToken(); err == nil {
				token = refreshed
			}
		}
		return providerAuth{
			apiKey:  token.AccessToken,
			baseURL: os.Getenv("ANTHROPIC_API_URL"),
			isOAuth: true,
		}, nil
	}

	// Try new account registry as fallback
	token := tryLoadAccountFromRegistry("anthropic")
	if token != nil && token.AccessToken != "" {
		return providerAuth{
			apiKey:  token.AccessToken,
			baseURL: os.Getenv("ANTHROPIC_API_URL"),
			isOAuth: true,
		}, nil
	}

	// If OAuth failed and user explicitly requested api_key, try that
	if normalizedAuthType == "api_key" {
		var apiKey string
		var baseURL string
		var err error
		apiKey, baseURL, err = loadProviderCredentials(providerName)
		if err == nil && apiKey != "" {
			return providerAuth{
				apiKey:  apiKey,
				baseURL: baseURL,
				isOAuth: false,
			}, nil
		}
		if apiKey == "" {
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
		if apiKey == "" {
			apiKey = os.Getenv("CLAUDE_API_KEY")
		}
		if baseURL == "" {
			baseURL = os.Getenv("ANTHROPIC_API_URL")
		}
		if apiKey != "" {
			return providerAuth{
				apiKey:  apiKey,
				baseURL: baseURL,
				isOAuth: false,
			}, nil
		}
	}

	return providerAuth{}, fmt.Errorf("no Anthropic credentials found (OAuth or API key) - run /auth to authenticate")
}

func getOpenAIAuth(ctx context.Context, authType string, providerName string) (providerAuth, error) {
	var normalizedAuthType string = strings.ToLower(strings.TrimSpace(authType))
	if normalizedAuthType == "" {
		// Default to api_key so that a stored API key is always reachable.
		// OAuth tokens are still tried first (below) for backwards compatibility.
		normalizedAuthType = "api_key"
	}

	// Try OAuth first (always, regardless of authType)
	// Try old OAuth token loading first (refresh if needed)
	oldToken, err := openai.RefreshAndStoreToken(ctx)
	if err != nil {
		oldToken, err = openai.GetStoredOAuthToken()
		if err == nil && oldToken != nil && (oldToken.APIKey != "" || oldToken.AccessToken != "") {
			var accountID string = oldToken.AccountID
			if accountID == "" && oldToken.IDToken != "" {
				accountID = openai.ExtractAccountIDFromIDToken(oldToken.IDToken)
			}
			if accountID != "" {
				return providerAuth{
					apiKey:      oldToken.APIKey,
					accessToken: oldToken.AccessToken,
					accountID:   accountID,
					baseURL:     os.Getenv("OPENAI_BASE_URL"),
					orgID:       os.Getenv("OPENAI_ORG_ID"),
					isOAuth:     true,
				}, nil
			}
		}
	} else if oldToken != nil && (oldToken.APIKey != "" || oldToken.AccessToken != "") {
		var accountID string = oldToken.AccountID
		if accountID == "" && oldToken.IDToken != "" {
			accountID = openai.ExtractAccountIDFromIDToken(oldToken.IDToken)
		}
		if accountID != "" {
			return providerAuth{
				apiKey:      oldToken.APIKey,
				accessToken: oldToken.AccessToken,
				accountID:   accountID,
				baseURL:     os.Getenv("OPENAI_BASE_URL"),
				orgID:       os.Getenv("OPENAI_ORG_ID"),
				isOAuth:     true,
			}, nil
		}
	}

	// Try new account registry as fallback
	token := tryLoadAccountFromRegistry("openai")
	if token != nil && (token.APIKey != "" || token.AccessToken != "") {
		var accountID string = token.AccountID
		if accountID == "" && token.IDToken != "" {
			accountID = openai.ExtractAccountIDFromIDToken(token.IDToken)
		}
		// Only return if we have a valid accountID (required for Codex provider)
		if accountID != "" {
			return providerAuth{
				apiKey:      token.APIKey,
				accessToken: token.AccessToken,
				accountID:   accountID,
				baseURL:     os.Getenv("OPENAI_BASE_URL"),
				orgID:       os.Getenv("OPENAI_ORG_ID"),
				isOAuth:     true,
			}, nil
		}
		// AccountID is empty - fall through to try other auth methods or return error
	}

	// If OAuth failed and user explicitly requested api_key, try that
	if normalizedAuthType == "api_key" {
		var apiKey string
		var baseURL string
		var err error
		apiKey, baseURL, err = loadProviderCredentials(providerName)
		if err == nil && apiKey != "" {
			return providerAuth{
				apiKey:  apiKey,
				baseURL: baseURL,
				orgID:   os.Getenv("OPENAI_ORG_ID"),
				isOAuth: false,
			}, nil
		}
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		if baseURL == "" {
			baseURL = os.Getenv("OPENAI_BASE_URL")
		}
		if apiKey != "" {
			return providerAuth{
				apiKey:  apiKey,
				baseURL: baseURL,
				orgID:   os.Getenv("OPENAI_ORG_ID"),
				isOAuth: false,
			}, nil
		}
	}

	return providerAuth{}, fmt.Errorf("no OpenAI credentials found (OAuth or API key) - run /auth to authenticate")
}

func getGeminiAuth(ctx context.Context, authType string, providerName string) (providerAuth, error) {
	var normalizedAuthType string = strings.ToLower(strings.TrimSpace(authType))
	if normalizedAuthType == "" {
		// When no auth type is configured: try API key from credentials/env first;
		// if none is found, fall through to OAuth mode (backwards compat).
		normalizedAuthType = "api_key"
	}

	if normalizedAuthType == "api_key" {
		var apiKey string
		var baseURL string
		var err error
		apiKey, baseURL, err = loadProviderCredentials(providerName)
		if err != nil {
			return providerAuth{}, err
		}
		if apiKey == "" {
			apiKey = os.Getenv("GEMINI_API_KEY")
		}
		if baseURL == "" {
			baseURL = os.Getenv("GEMINI_BASE_URL")
		}
		if apiKey != "" {
			return providerAuth{
				apiKey:  apiKey,
				baseURL: baseURL,
				isOAuth: false,
			}, nil
		}
		// No API key found — fall back to OAuth if the auth type wasn't explicitly
		// set to api_key (i.e. we were using the empty-string default).
		if authType != "" {
			return providerAuth{}, fmt.Errorf("no Gemini API key found - set GEMINI_API_KEY or configure in settings")
		}
	}

	// OAuth mode
	// In Gemini provider, OAuth is handled internally by the provider using the OAuthManager
	// We just need to verify that we have the necessary configuration to initialize it
	// The provider itself will handle the token flow

	return providerAuth{
		isOAuth: true,
	}, nil
}

// getXAIAuth resolves credentials for the xAI / Grok provider.
//
// Resolution order:
//  1. XAI_API_KEY environment variable (fastest, skips OAuth)
//  2. Stored xAI OAuth token (~/.swarmos/xai_oauth.json) — refreshed if expired
//  3. Grok CLI import (~/.grok/auth.json) — imported and stored on first use
//  4. credentials.json API key field
//  5. providers.json api_key field
//  6. Error: prompt user to run /auth
func getXAIAuth(ctx context.Context, authType string, providerName string) (providerAuth, error) {
	normalizedAuthType := strings.ToLower(strings.TrimSpace(authType))

	// ── 1. XAI_API_KEY env var (API key path, always checked first unless oauth forced) ──
	if normalizedAuthType != "oauth" {
		if key := os.Getenv("XAI_API_KEY"); key != "" {
			baseURL := os.Getenv("XAI_BASE_URL")
			if baseURL == "" {
				baseURL = defaultXAIBaseURL
			}
			return providerAuth{apiKey: key, baseURL: baseURL, isOAuth: false}, nil
		}
	}

	// ── 2. Stored xAI OAuth token (~/.swarmos/xai_oauth.json) ──
	if storedToken, err := xai.GetStoredOAuthToken(); err == nil && storedToken != nil {
		token := storedToken
		if token.IsExpired() && token.RefreshToken != "" {
			if refreshed, refErr := xai.RefreshToken(ctx, token.RefreshToken); refErr == nil {
				token = refreshed
			}
		}
		if !token.IsExpired() {
			baseURL := os.Getenv("XAI_BASE_URL")
			if baseURL == "" {
				baseURL = defaultXAIBaseURL
			}
			return providerAuth{apiKey: token.AccessToken, baseURL: baseURL, isOAuth: true}, nil
		}
	}

	// ── 3. Grok CLI import (~/.grok/auth.json) ──
	if imported, impErr := xai.ImportAndStoreFromGrokCLI(""); impErr == nil && imported {
		if storedToken, err := xai.GetStoredOAuthToken(); err == nil && storedToken != nil {
			baseURL := os.Getenv("XAI_BASE_URL")
			if baseURL == "" {
				baseURL = defaultXAIBaseURL
			}
			return providerAuth{apiKey: storedToken.AccessToken, baseURL: baseURL, isOAuth: true}, nil
		}
	}

	// ── 4. credentials.json ──
	if apiKey, baseURL, err := loadProviderCredentials(providerName); err == nil && apiKey != "" {
		if baseURL == "" {
			baseURL = os.Getenv("XAI_BASE_URL")
		}
		if baseURL == "" {
			baseURL = defaultXAIBaseURL
		}
		return providerAuth{apiKey: apiKey, baseURL: baseURL, isOAuth: false}, nil
	}

	// ── 5. providers.json api_key field ──
	if apiKey, baseURL := getAPIKeyFromProviders(strings.ToLower(providerName)); apiKey != "" {
		if baseURL == "" {
			baseURL = defaultXAIBaseURL
		}
		return providerAuth{apiKey: apiKey, baseURL: baseURL, isOAuth: false}, nil
	}

	return providerAuth{}, fmt.Errorf(
		"no xAI credentials found — set XAI_API_KEY, run /auth to log in via SuperGrok OAuth, or run 'grok login' then restart",
	)
}

// GetCredentialsForProvider retrieves credentials for any provider using the TUI's
// comprehensive credential resolution (OAuth, Account Registry, credentials.json, env vars).
// This function is used by the workflow system to ensure workflows have the same credential
// access as the main TUI.
func GetCredentialsForProvider(providerName string) (apiKey, baseURL string, isOAuth bool, err error) {
	normalized := provider.NormalizeProviderName(providerName)

	// Try to load provider config to determine API type and auth type
	providerCfg, cfgErr := getProviderConfigFromFile(providerName)

	var apiType string
	var configBaseURL string
	var authType string
	if cfgErr == nil && providerCfg != nil && providerCfg.APIType != "" {
		apiType = providerCfg.APIType
		configBaseURL = providerCfg.BaseURL
		authType = providerCfg.Type
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

	// Safety guard: if a custom provider was saved with api_type="openai" (via the OpenAI
	// preset in the UI) but is NOT actually openai/codex, treat it as openai-compatible so
	// it uses its own credentials rather than OPENAI_API_KEY.
	if apiType == "openai" && normalized != "openai" && normalized != "codex" {
		apiType = "openai-compatible"
	}

	switch apiType {
	case "anthropic":
		auth, authErr := getAnthropicAuth(authType, providerName)
		if authErr != nil {
			return "", "", false, fmt.Errorf("failed to get credentials for provider %s: %w", providerName, authErr)
		}
		resolvedBaseURL := auth.baseURL
		if resolvedBaseURL == "" {
			resolvedBaseURL = configBaseURL
		}
		return auth.apiKey, resolvedBaseURL, auth.isOAuth, nil

	case "openai":
		auth, authErr := getOpenAIAuth(context.Background(), authType, providerName)
		if authErr != nil {
			return "", "", false, fmt.Errorf("failed to get credentials for provider %s: %w", providerName, authErr)
		}
		// For OpenAI OAuth, prefer accessToken over apiKey (used by Codex provider)
		key := auth.apiKey
		if auth.isOAuth && auth.accessToken != "" {
			key = auth.accessToken
		}
		resolvedBaseURL := auth.baseURL
		if resolvedBaseURL == "" {
			resolvedBaseURL = configBaseURL
		}
		return key, resolvedBaseURL, auth.isOAuth, nil

	case "gemini":
		auth, authErr := getGeminiAuth(context.Background(), authType, providerName)
		if authErr != nil {
			return "", "", false, fmt.Errorf("failed to get credentials for provider %s: %w", providerName, authErr)
		}
		resolvedBaseURL := auth.baseURL
		if resolvedBaseURL == "" {
			resolvedBaseURL = configBaseURL
		}
		return auth.apiKey, resolvedBaseURL, auth.isOAuth, nil

	case "xai":
		// xAI / Grok: supports XAI_API_KEY and SuperGrok OAuth.
		// Bearer token (API key or OAuth) is passed as Authorization header.
		// The underlying transport is OpenAI-compatible at https://api.x.ai/v1.
		auth, authErr := getXAIAuth(context.Background(), authType, providerName)
		if authErr != nil {
			return "", "", false, fmt.Errorf("failed to get credentials for provider %s: %w", providerName, authErr)
		}
		resolvedBaseURL := auth.baseURL
		if resolvedBaseURL == "" {
			resolvedBaseURL = configBaseURL
		}
		if resolvedBaseURL == "" {
			resolvedBaseURL = defaultXAIBaseURL
		}
		return auth.apiKey, resolvedBaseURL, auth.isOAuth, nil

	case "cursor":
		// Cursor (Anysphere) — Connect-RPC provider with OAuth token.
		token, err := getCursorAccessToken()
		if err != nil {
			return "", "", false, fmt.Errorf("failed to get Cursor credentials: %w", err)
		}
		resolvedBaseURL := configBaseURL
		if resolvedBaseURL == "" {
			resolvedBaseURL = "https://api2.cursor.sh"
		}
		return token, resolvedBaseURL, true, nil

	case "openai-compatible":
		auth, authErr := getOpenAICompatibleAuth(providerName)
		if authErr != nil {
			return "", "", false, fmt.Errorf("failed to get credentials for provider %s: %w", providerName, authErr)
		}
		resolvedBaseURL := auth.baseURL
		if resolvedBaseURL == "" {
			resolvedBaseURL = configBaseURL
		}
		return auth.apiKey, resolvedBaseURL, auth.isOAuth, nil

	default:
		return "", "", false, fmt.Errorf("unsupported provider: %s (api type: %s)", providerName, apiType)
	}
}
