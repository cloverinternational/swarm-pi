package chat

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Cursor (Anysphere) model loading.
//
// Cursor's backend is a Connect-RPC service at api2.cursor.sh. The control-plane
// method AiService/AvailableModels speaks the Connect JSON codec (plain JSON in,
// plain JSON out — no gRPC framing), so we can call it with a normal HTTP POST.
// We authenticate with an access token captured by the `cursor-agent` CLI
// (~/.cursor/cli-config.json), a CURSOR_API_KEY exchanged for a token, or a key
// stored in swarmos credentials. The fetched models are written into
// providers.json under the "Cursor" entry, exactly like the OpenRouter refresh,
// so the model UI re-reads them on reload.

const (
	cursorAPIBaseURLDefault  = "https://api2.cursor.sh"
	cursorClientVersion      = "cli-2026.06.26-7079533"
	cursorClientType         = "cursor-agent-cli"
	cursorAvailableModelsRPC = "/aiserver.v1.AiService/AvailableModels"
	cursorExchangeKeyRPC     = "/auth/exchange_user_api_key"
	// cursorRefreshEndpoint is the standard OAuth token endpoint used to
	// exchange a refresh token for a fresh session access token. Verified
	// live: POST JSON {"grant_type":"refresh_token","refresh_token":"..."}
	// returns 200 {"access_token","id_token","shouldLogout"}. The key MUST be
	// snake_case refresh_token; camelCase yields {"shouldLogout":true}.
	cursorRefreshEndpoint = "/oauth/token"
)

// cursorAPIBaseURL returns the Cursor control-plane base URL, honoring the
// CURSOR_API_BASE_URL override used by the official CLI.
func cursorAPIBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("CURSOR_API_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return cursorAPIBaseURLDefault
}

// cursorConfigDir resolves the directory holding cli-config.json, honoring
// CURSOR_CONFIG_DIR > $XDG_CONFIG_HOME/cursor > ~/.cursor (matching the CLI).
func cursorConfigDir() string {
	if v := strings.TrimSpace(os.Getenv("CURSOR_CONFIG_DIR")); v != "" {
		return v
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "cursor")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cursor")
}

// getCursorAccessToken resolves a Cursor access token for control-plane calls.
// Order: account registry (survives VSCode clobbering) -> existing CLI login
// (cli-config.json) -> CURSOR_API_KEY exchange -> swarmos credentials.json
// ("cursor" entry) exchange. Returns "" if none work.
//
// If the token is expired (JWT exp claim), it exchanges the stored refresh
// token for a fresh access token via the Cursor OAuth token endpoint
// (tryRefreshCursorToken). If no refresh is possible, it returns a clear error.
func getCursorAccessToken() (string, error) {
	// 1. Try account registry first (survives VSCode clobbering cli-config.json).
	if tok := readCursorTokenFromAccountRegistry(); tok != "" {
		if !isCursorTokenExpired(tok) {
			return tok, nil
		}
		// Token expired — try refresh, then fall through to other methods.
		if fresh, err := tryRefreshCursorToken(); err == nil && fresh != "" {
			return fresh, nil
		}
	}

	// 2. Reuse an existing `cursor-agent login` (cli-config.json).
	if tok := readCursorCLIToken(); tok != "" {
		if !isCursorTokenExpired(tok) {
			return tok, nil
		}
		// Token expired — try refresh, then fall through.
		if fresh, err := tryRefreshCursorToken(); err == nil && fresh != "" {
			return fresh, nil
		}
	}

	// 3. Exchange a user API key (env or swarmos credentials) for an access token.
	apiKey := strings.TrimSpace(os.Getenv("CURSOR_API_KEY"))
	if apiKey == "" {
		apiKey = readCursorKeyFromCredentials()
	}
	if apiKey != "" {
		tok, err := exchangeCursorAPIKey(apiKey)
		if err != nil {
			return "", err
		}
		return tok, nil
	}

	// 4. If we had an expired token from steps 1-2, try one last refresh, then
	//    return a clear error if that also fails.
	if tok := readCursorTokenFromAccountRegistry(); tok != "" {
		if fresh, err := tryRefreshCursorToken(); err == nil && fresh != "" {
			return fresh, nil
		}
		return "", fmt.Errorf("cursor token expired and refresh failed — run /auth to re-login with Cursor")
	}
	if tok := readCursorCLIToken(); tok != "" {
		if fresh, err := tryRefreshCursorToken(); err == nil && fresh != "" {
			return fresh, nil
		}
		return "", fmt.Errorf("cursor token expired and refresh failed — run /auth to re-login with Cursor")
	}

	return "", fmt.Errorf("no Cursor credentials found: run `cursor-agent login`, set CURSOR_API_KEY, or store a 'cursor' api_key")
}

// readCursorRefreshToken resolves the stored refresh token, checking the account
// registry (~/.swarmos/accounts/cursor/account-*.json, refresh_token) first and
// then the CLI config (~/.cursor/cli-config.json, refreshToken).
func readCursorRefreshToken() string {
	// Account registry (snake_case refresh_token) — survives VSCode clobbering.
	if home, err := os.UserHomeDir(); err == nil {
		accountsDir := filepath.Join(home, ".swarmos", "accounts", "cursor")
		if entries, err := os.ReadDir(accountsDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasPrefix(entry.Name(), "account-") || !strings.HasSuffix(entry.Name(), ".json") {
					continue
				}
				data, err := os.ReadFile(filepath.Join(accountsDir, entry.Name()))
				if err != nil {
					continue
				}
				var tok struct {
					RefreshToken string `json:"refresh_token"`
				}
				if json.Unmarshal(data, &tok) == nil && tok.RefreshToken != "" {
					return tok.RefreshToken
				}
			}
		}
	}
	// CLI config (camelCase refreshToken).
	dir := cursorConfigDir()
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "cli-config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		RefreshToken string `json:"refreshToken"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	return strings.TrimSpace(cfg.RefreshToken)
}

// tryRefreshCursorToken exchanges the stored refresh token for a fresh access
// token via the Cursor OAuth token endpoint, persists it, and returns it.
// Returns ("", err) when no refresh token is available or the exchange fails.
//
// Verified contract (live, api2.cursor.sh):
//
//	POST /oauth/token  Content-Type: application/json
//	  {"grant_type":"refresh_token","refresh_token":"<RT>"}
//	-> 200 {"access_token":"<JWT>","id_token":"...","shouldLogout":false}
//
// The refresh token is reusable (no new refresh_token is returned). A
// shouldLogout:true response (or empty access_token) means the refresh token is
// no longer valid and the user must re-login.
func tryRefreshCursorToken() (string, error) {
	rt := readCursorRefreshToken()
	if rt == "" {
		return "", fmt.Errorf("cursor: no refresh token available")
	}
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": rt,
	})
	req, err := http.NewRequest("POST", cursorAPIBaseURL()+cursorRefreshEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-cursor-client-version", cursorClientVersion)
	req.Header.Set("x-cursor-client-type", cursorClientType)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("cursor refresh returned status %d", resp.StatusCode)
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		ShouldLogout bool   `json:"shouldLogout"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.ShouldLogout || out.AccessToken == "" {
		return "", fmt.Errorf("cursor refresh rejected (shouldLogout) — run /auth to re-login")
	}
	// Persist the fresh access token to both stores so subsequent reads (and
	// the next process) reuse it without another refresh round-trip.
	persistRefreshedCursorToken(out.AccessToken, rt)
	return out.AccessToken, nil
}

// persistRefreshedCursorToken writes a freshly-refreshed access token (keeping
// the existing refresh token) into cli-config.json and the account registry,
// mirroring how the login flow stores tokens. Best-effort; errors are ignored.
func persistRefreshedCursorToken(accessToken, refreshToken string) {
	// cli-config.json (merge, preserve other fields).
	if dir := cursorConfigDir(); dir != "" {
		path := filepath.Join(dir, "cli-config.json")
		merged := map[string]any{}
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &merged)
		}
		merged["accessToken"] = accessToken
		if refreshToken != "" {
			merged["refreshToken"] = refreshToken
		}
		if out, err := json.MarshalIndent(merged, "", "  "); err == nil {
			_ = os.WriteFile(path, out, 0o600)
		}
	}
	// Account registry (~/.swarmos/accounts/cursor/account-cli.json).
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	accountsDir := filepath.Join(home, ".swarmos", "accounts", "cursor")
	if err := os.MkdirAll(accountsDir, 0o755); err != nil {
		return
	}
	account := map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"provider":      "cursor",
		"is_active":     true,
	}
	if data, err := json.MarshalIndent(account, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(accountsDir, "account-cli.json"), data, 0o600)
	}
}

// isCursorTokenExpired parses the JWT middle segment and checks the exp claim.
// Returns true if the token is expired or unparseable. A missing or zero exp
// means the token never expires (returns false).
func isCursorTokenExpired(accessToken string) bool {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return false // Not a JWT — can't check expiry, assume valid.
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false // Unparseable — assume valid to avoid false negatives.
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false
	}
	if claims.Exp == 0 {
		return false // No exp claim — token doesn't expire.
	}
	return time.Now().Unix() > claims.Exp
}

// readCursorTokenFromAccountRegistry reads the accessToken from
// ~/.swarmos/accounts/cursor/account-cli.json (the account registry format).
// This survives the VSCode cursor app clobbering cli-config.json.
func readCursorTokenFromAccountRegistry() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	accountsDir := filepath.Join(home, ".swarmos", "accounts", "cursor")
	entries, err := os.ReadDir(accountsDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "account-") && strings.HasSuffix(entry.Name(), ".json") {
			data, err := os.ReadFile(filepath.Join(accountsDir, entry.Name()))
			if err != nil {
				continue
			}
			var tok struct {
				AccessToken string `json:"access_token"`
			}
			if err := json.Unmarshal(data, &tok); err != nil {
				continue
			}
			if tok.AccessToken != "" {
				return tok.AccessToken
			}
		}
	}
	return ""
}

// readCursorCLIToken reads accessToken from {configDir}/cli-config.json.
func readCursorCLIToken() string {
	dir := cursorConfigDir()
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "cli-config.json"))
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

// exchangeCursorAPIKey trades a user API key for an access/refresh token pair.
func exchangeCursorAPIKey(apiKey string) (string, error) {
	req, err := http.NewRequest("POST", cursorAPIBaseURL()+cursorExchangeKeyRPC, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("cursor key exchange returned status %d", resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("cursor key exchange returned no accessToken")
	}
	return out.AccessToken, nil
}

// readCursorKeyFromCredentials reads a "cursor" api_key from swarmos
// credentials.json, mirroring getOpenRouterAPIKey's lookup.
func readCursorKeyFromCredentials() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".swarmos", "credentials.json"))
	if err != nil {
		return ""
	}
	var creds struct {
		Providers map[string]struct {
			APIKey string `json:"api_key"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return ""
	}
	for key, cred := range creds.Providers {
		if strings.EqualFold(key, "cursor") && cred.APIKey != "" {
			return cred.APIKey
		}
	}
	return ""
}

// CursorModelInfo is the subset of an AvailableModels entry the TUI needs.
type CursorModelInfo struct {
	ID            string
	Name          string
	ContextLength int
	Description   string
	SupportsAgent bool
	Reasoning     bool
}

// FetchCursorModelsDetailed calls AiService/AvailableModels and returns the
// model list. The token must be a valid Cursor access token.
func FetchCursorModelsDetailed(token string) ([]CursorModelInfo, error) {
	body := []byte(`{"isNightly":false,"includeLongContextModels":true,"excludeMaxNamedModels":false,"additionalModelNames":[]}`)
	req, err := http.NewRequest("POST", cursorAPIBaseURL()+cursorAvailableModelsRPC, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-cursor-client-version", cursorClientVersion)
	req.Header.Set("x-cursor-client-type", cursorClientType)
	req.Header.Set("x-request-id", uuid.NewString())
	req.Header.Set("x-ghost-mode", "false")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("cursor not logged in (401): re-run `cursor-agent login` or refresh CURSOR_API_KEY")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("cursor AvailableModels returned status %d", resp.StatusCode)
	}

	var result struct {
		Models []struct {
			Name              string `json:"name"`
			ServerModelName   string `json:"serverModelName"`
			ClientDisplayName string `json:"clientDisplayName"`
			SupportsAgent     bool   `json:"supportsAgent"`
			SupportsThinking  bool   `json:"supportsThinking"`
			ContextWindow     int    `json:"contextWindow"`
			TooltipData       struct {
				MarkdownContent string `json:"markdownContent"`
			} `json:"tooltipData"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]CursorModelInfo, 0, len(result.Models))
	for _, m := range result.Models {
		id := m.Name
		if id == "" {
			id = m.ServerModelName
		}
		if id == "" {
			continue
		}
		display := m.ClientDisplayName
		if display == "" {
			display = id
		}
		models = append(models, CursorModelInfo{
			ID:            id,
			Name:          display,
			ContextLength: m.ContextWindow,
			Description:   m.TooltipData.MarkdownContent,
			SupportsAgent: m.SupportsAgent,
			Reasoning:     m.SupportsThinking,
		})
	}
	return models, nil
}

// writeCursorModelsToProvidersJSON writes fetched models into the "Cursor"
// entry of providers.json (mirrors writeOpenRouterModelsToProvidersJSON).
func writeCursorModelsToProvidersJSON(models []CursorModelInfo) (int, error) {
	providersJSONMu.Lock()
	defer providersJSONMu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return 0, fmt.Errorf("failed to get home directory: %w", err)
	}
	providersPath := filepath.Join(home, ".swarmos", "providers.json")
	data, err := readProvidersJSONWithRecovery(providersPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read providers.json: %w", err)
	}
	var providers []map[string]interface{}
	if err = json.Unmarshal(data, &providers); err != nil {
		return 0, fmt.Errorf("failed to parse providers.json: %w", err)
	}
	found := false
	for i, p := range providers {
		name, ok := p["name"].(string)
		if !ok || !strings.EqualFold(name, "cursor") {
			continue
		}
		found = true
		providerModels := make([]map[string]interface{}, 0, len(models))
		for _, model := range models {
			providerModels = append(providerModels, map[string]interface{}{
				"id":             model.ID,
				"display_name":   model.Name,
				"context":        formatContextWindow(model.ContextLength),
				"context_window": model.ContextLength,
				"description":    model.Description,
				"reasoning":      model.Reasoning,
				"tool_call":      model.SupportsAgent,
			})
		}
		providers[i]["models"] = providerModels
		providers[i]["last_refreshed"] = time.Now().Format(time.RFC3339)
		providers[i]["source"] = "cursor"
		break
	}
	if !found {
		return 0, fmt.Errorf("Cursor provider not found in providers.json")
	}
	updatedData, err := json.MarshalIndent(providers, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("failed to marshal providers.json: %w", err)
	}
	if err = atomicWriteProvidersJSON(providersPath, updatedData, 0644); err != nil {
		return 0, fmt.Errorf("failed to write providers.json: %w", err)
	}
	return len(models), nil
}

// RefreshCursorModels fetches Cursor's live model catalog and saves it to
// providers.json. Returns the number of models fetched. This is the callback
// wired into the model settings UI (mirrors RefreshOpenRouterModels).
func RefreshCursorModels() (int, error) {
	token, err := getCursorAccessToken()
	if err != nil {
		return 0, err
	}
	models, err := FetchCursorModelsDetailed(token)
	if err != nil {
		return 0, err
	}
	logDebug("[Cursor] Successfully fetched %d models from AvailableModels", len(models))
	return writeCursorModelsToProvidersJSON(models)
}
