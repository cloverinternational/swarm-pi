package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
)

// Anthropic Claude Code OAuth model refresh.
//
// The set of Claude models available to a Claude Code OAuth login is NOT static:
// Anthropic ships new models (e.g. claude-opus-4-8, claude-sonnet-5) that the
// hardcoded anthropic.DefaultOAuthModels list does not know about. To keep the
// model picker current we call the live Messages API model catalog endpoint
// (GET https://api.anthropic.com/v1/models) with the stored OAuth access token
// and persist the result into ~/.swarmos/providers.json under the "Anthropic"
// entry, exactly like RefreshOpenRouterModels / RefreshCursorModels, so the
// model UI re-reads it on reload.
//
// The refresh also diffs the freshly-fetched model IDs against whatever was in
// providers.json before, so the caller can surface an in-TUI notification when
// brand-new Claude models appear.

const (
	anthropicModelsURL  = "https://api.anthropic.com/v1/models"
	anthropicAPIVersion = "2023-06-01"
	anthropicOAuthBeta  = "oauth-2025-04-20"
)

// isAnthropicProviderName reports whether a providers.json entry name refers to
// an Anthropic/Claude-family provider whose model list should be refreshed from
// the Anthropic /v1/models catalog. This deliberately covers BOTH the API-key
// "Anthropic" entry and the OAuth "ClaudeCode" entry (which are treated as the
// same provider family elsewhere, e.g. settings/auth.go's "claudecode",
// "anthropic", "claude" normalization) so a single refresh keeps every
// Claude-backed picker current.
func isAnthropicProviderName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic", "claudecode", "claude", "claude code", "claude-code":
		return true
	default:
		return false
	}
}

// AnthropicModelInfo is the subset of a /v1/models entry the TUI needs.
type AnthropicModelInfo struct {
	ID          string
	DisplayName string
}

// getAnthropicOAuthToken resolves a usable Claude Code OAuth access token,
// refreshing it first if it is expired (or about to expire). Returns "" with an
// error when no OAuth login is present so callers can silently skip the refresh.
func getAnthropicOAuthToken() (string, error) {
	cfg, err := anthropic.LoadOAuthConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load OAuth config: %w", err)
	}
	if cfg == nil || cfg.Token == nil || cfg.Token.AccessToken == "" {
		return "", fmt.Errorf("no Claude Code OAuth token stored")
	}

	// Refresh proactively when expired to avoid a 401 on the models call.
	if anthropic.IsTokenExpired(cfg.Token) {
		refreshed, rerr := anthropic.RefreshAndStoreToken()
		if rerr != nil {
			// Fall back to the (expired) token; the request may still 401 but we
			// surface a clear error rather than silently doing nothing.
			logDebug("[Anthropic] OAuth token refresh failed before model fetch: %v", rerr)
		} else if refreshed != nil && refreshed.AccessToken != "" {
			return refreshed.AccessToken, nil
		}
	}
	return cfg.Token.AccessToken, nil
}

// FetchAnthropicOAuthModels calls GET /v1/models with the OAuth bearer token and
// returns the full model list, following pagination via has_more/last_id.
func FetchAnthropicOAuthModels(token string) ([]AnthropicModelInfo, error) {
	if token == "" {
		return nil, fmt.Errorf("no OAuth access token provided")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	var models []AnthropicModelInfo
	afterID := ""

	// Hard cap the number of pages to avoid an infinite loop on a misbehaving
	// endpoint. Anthropic currently returns everything in a single page.
	for page := 0; page < 20; page++ {
		url := fmt.Sprintf("%s?limit=1000", anthropicModelsURL)
		if afterID != "" {
			url += "&after_id=" + afterID
		}

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("anthropic-beta", anthropicOAuthBeta)
		req.Header.Set("anthropic-version", anthropicAPIVersion)

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			return nil, fmt.Errorf("anthropic /v1/models returned 401: OAuth token invalid or expired (re-run login)")
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("anthropic /v1/models returned status %d", resp.StatusCode)
		}

		var result struct {
			Data []struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		for _, m := range result.Data {
			if m.ID == "" {
				continue
			}
			display := m.DisplayName
			if display == "" {
				display = m.ID
			}
			models = append(models, AnthropicModelInfo{ID: m.ID, DisplayName: display})
		}

		if !result.HasMore || result.LastID == "" {
			break
		}
		afterID = result.LastID
	}

	return models, nil
}

// readAnthropicModelIDsFromProvidersJSON returns the set of model IDs currently
// stored under any Claude-family provider entry (Anthropic / ClaudeCode) of
// providers.json. Missing file or entry yields an empty set (not an error) so a
// first run reports every model as "new" only when there was genuinely nothing
// before.
func readAnthropicModelIDsFromProvidersJSON() map[string]bool {
	ids := map[string]bool{}
	home, err := os.UserHomeDir()
	if err != nil {
		return ids
	}
	data, err := os.ReadFile(filepath.Join(home, ".swarmos", "providers.json"))
	if err != nil {
		return ids
	}
	var providers []map[string]interface{}
	if err := json.Unmarshal(data, &providers); err != nil {
		return ids
	}
	for _, p := range providers {
		name, _ := p["name"].(string)
		if !isAnthropicProviderName(name) {
			continue
		}
		models, _ := p["models"].([]interface{})
		for _, mv := range models {
			m, ok := mv.(map[string]interface{})
			if !ok {
				continue
			}
			if id, ok := m["id"].(string); ok && id != "" {
				ids[id] = true
			}
		}
	}
	return ids
}

// writeAnthropicModelsToProvidersJSON writes fetched models into EVERY
// Claude-family entry (Anthropic API-key entry and the ClaudeCode OAuth entry)
// of providers.json so both pickers stay current (mirrors
// writeOpenRouterModelsToProvidersJSON, but updates all matching entries rather
// than just the first).
func writeAnthropicModelsToProvidersJSON(models []AnthropicModelInfo) (int, error) {
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

	matched := 0
	for i, provider := range providers {
		name, ok := provider["name"].(string)
		if !ok || !isAnthropicProviderName(name) {
			continue
		}
		matched++

		// The live catalog owns model identity/display metadata, but fields such
		// as thinking_enabled, thinking_budget, context_window, and custom user
		// metadata belong to local configuration. Merge by ID instead of
		// replacing the model list wholesale, which previously erased those
		// settings on every startup refresh.
		existingByID := make(map[string]map[string]interface{})
		var existingOrder []string
		if existingModels, ok := provider["models"].([]interface{}); ok {
			for _, raw := range existingModels {
				existing, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				id, _ := existing["id"].(string)
				if id == "" {
					continue
				}
				existingByID[id] = existing
				existingOrder = append(existingOrder, id)
			}
		}

		providerModels := make([]map[string]interface{}, 0, len(models)+len(existingByID))
		fetchedIDs := make(map[string]bool, len(models))
		for _, model := range models {
			modelConfig := existingByID[model.ID]
			if modelConfig == nil {
				modelConfig = make(map[string]interface{})
			}
			modelConfig["id"] = model.ID
			modelConfig["display_name"] = model.DisplayName
			providerModels = append(providerModels, modelConfig)
			fetchedIDs[model.ID] = true
		}
		// Preserve user-added models that are not in the remote catalog.
		for _, id := range existingOrder {
			if !fetchedIDs[id] {
				providerModels = append(providerModels, existingByID[id])
			}
		}
		providers[i]["models"] = providerModels
		providers[i]["last_refreshed"] = time.Now().Format(time.RFC3339)
		providers[i]["source"] = "anthropic-oauth"
	}

	if matched == 0 {
		return 0, fmt.Errorf("no Anthropic/ClaudeCode provider found in providers.json")
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

// diffNewAnthropicModels returns the IDs present in models but not in prior,
// sorted for stable output.
func diffNewAnthropicModels(prior map[string]bool, models []AnthropicModelInfo) []string {
	var added []string
	for _, m := range models {
		if !prior[m.ID] {
			added = append(added, m.ID)
		}
	}
	sort.Strings(added)
	return added
}

// RefreshAnthropicOAuthModelsDetailed fetches the live Claude model catalog for
// the current OAuth login and saves it to providers.json. It returns the list of
// newly-added model IDs (compared to what was stored before) and the total
// number of models fetched. This is the primitive used by both the startup
// auto-refresh and the settings-UI manual refresh callback.
func RefreshAnthropicOAuthModelsDetailed() (added []string, total int, err error) {
	token, err := getAnthropicOAuthToken()
	if err != nil {
		return nil, 0, err
	}

	// Snapshot the prior model IDs BEFORE overwriting so we can diff.
	prior := readAnthropicModelIDsFromProvidersJSON()

	models, err := FetchAnthropicOAuthModels(token)
	if err != nil {
		return nil, 0, err
	}
	if len(models) == 0 {
		return nil, 0, fmt.Errorf("anthropic /v1/models returned no models")
	}

	n, err := writeAnthropicModelsToProvidersJSON(models)
	if err != nil {
		return nil, 0, err
	}

	added = diffNewAnthropicModels(prior, models)
	logDebug("[Anthropic] Refreshed %d OAuth models (%d new) into providers.json", n, len(added))
	return added, n, nil
}

// RefreshAnthropicOAuthModels adapts RefreshAnthropicOAuthModelsDetailed to the
// func() (int, error) shape expected by the settings-UI refresh callback wiring
// (mirrors RefreshOpenRouterModels / RefreshCursorModels).
func RefreshAnthropicOAuthModels() (int, error) {
	_, total, err := RefreshAnthropicOAuthModelsDetailed()
	return total, err
}

// AnthropicModelsRefreshMsg is delivered on the Bubble Tea loop when the startup
// background refresh of Claude Code OAuth models completes. It carries the IDs of
// any models that are newly available compared to what was stored before, so the
// UI thread can raise an in-TUI notification safely.
type AnthropicModelsRefreshMsg struct {
	Added []string
	Total int
	Err   error
}

// refreshAnthropicModelsCmd returns a tea.Cmd that refreshes the Claude Code
// OAuth model catalog in the background and reports newly-added models back to
// Update(). It is a no-op (returns nil, suppressing any message) when there is
// no OAuth login, so non-OAuth users are never bothered.
func (a *App) refreshAnthropicModelsCmd() tea.Cmd {
	return func() tea.Msg {
		// Skip entirely when no Claude Code OAuth token is present.
		if _, err := getAnthropicOAuthToken(); err != nil {
			logDebug("[Anthropic] Skipping OAuth model refresh: %v", err)
			return nil
		}
		added, total, err := RefreshAnthropicOAuthModelsDetailed()
		return AnthropicModelsRefreshMsg{Added: added, Total: total, Err: err}
	}
}

// handleAnthropicModelsRefresh processes the completion of the startup Claude
// model refresh. On success with newly-appeared models it raises an in-TUI
// notification listing them.
func (a *App) handleAnthropicModelsRefresh(msg AnthropicModelsRefreshMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		logDebug("[Anthropic] OAuth model refresh failed: %v", msg.Err)
		return a, nil
	}
	if len(msg.Added) > 0 {
		list := strings.Join(msg.Added, ", ")
		a.addNotification("info", fmt.Sprintf("✨ %d new Claude model(s) available: %s", len(msg.Added), list))
	}
	logDebug("[Anthropic] OAuth model refresh complete: %d models (%d new)", msg.Total, len(msg.Added))
	return a, nil
}
