package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/codex"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// OpenAI Codex (ChatGPT OAuth) model auto-discovery.
//
// The set of models available to a ChatGPT OAuth login is NOT static: OpenAI
// ships new models (e.g. the gpt-5.6 family) and retires old slugs, and the
// authoritative catalog lives behind GET {chatgpt backend}/models — the same
// endpoint the Codex CLI polls. To keep the model picker current we fetch that
// catalog with the stored OpenAI OAuth credentials (cached on disk with a
// short TTL by the SDK codex package) and persist the visible models into
// ~/.swarmos/providers.json under every OpenAI-OAuth provider entry, exactly
// like the Anthropic/Cursor/OpenRouter refreshers.
//
// The refresh also diffs freshly-fetched model IDs against what providers.json
// held before so the caller can surface an in-TUI notification when brand-new
// models appear, and it saves each model's catalog-supplied base instructions
// to ~/.swarmos/codex_prompt_catalog_<slug>.md for the codex prompt loader.

// providersJSONMu serializes every read-modify-write of
// ~/.swarmos/providers.json in this package. The OpenRouter, Cursor,
// Anthropic, and Codex refreshers each rewrite the whole file; without this
// lock two concurrent refreshers can silently drop each other's models
// (last-writer-wins). It aliases the commands package's mutex so the
// settings-UI SaveProviders path is serialized against the refreshers too.
var providersJSONMu = &commands.ProvidersJSONMu

// isOpenAICodexProviderEntry reports whether a providers.json entry is an
// OpenAI ChatGPT-OAuth provider whose model list should be refreshed from the
// Codex backend catalog. Matches on api_type/type first (the authoritative
// signal used at provider construction) with a name fallback.
func isOpenAICodexProviderEntry(entry map[string]interface{}) bool {
	apiType, _ := entry["api_type"].(string)
	authType, _ := entry["type"].(string)
	if strings.EqualFold(apiType, "openai") && strings.EqualFold(authType, "oauth") {
		return true
	}
	name, _ := entry["name"].(string)
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "codex", "openai (oauth)", "openai-oauth", "chatgpt":
		return true
	default:
		return false
	}
}

// getCodexOAuthCredentials resolves a usable OpenAI OAuth access token and
// ChatGPT account id, refreshing the token first when possible. Returns an
// error when no OAuth login is present so callers can silently skip.
func getCodexOAuthCredentials() (accessToken, accountID string, err error) {
	token, rerr := openai.RefreshAndStoreToken(context.Background())
	if rerr != nil {
		token, err = openai.GetStoredOAuthToken()
		if err != nil {
			return "", "", fmt.Errorf("no OpenAI OAuth credentials stored: %w", err)
		}
	}
	if token == nil || token.AccessToken == "" {
		return "", "", fmt.Errorf("OpenAI OAuth token missing access token")
	}
	accountID = token.AccountID
	if accountID == "" && token.IDToken != "" {
		accountID = openai.ExtractAccountIDFromIDToken(token.IDToken)
	}
	if accountID == "" {
		return "", "", fmt.Errorf("OpenAI OAuth token missing ChatGPT account id")
	}
	return token.AccessToken, accountID, nil
}

// FetchCodexModels fetches the Codex backend model catalog using the stored
// OAuth credentials, with the SDK's on-disk cache (5-minute TTL) in front.
// The cache is consulted BEFORE resolving credentials so a fresh cache never
// triggers an OAuth token refresh round-trip.
func FetchCodexModels() ([]codex.CatalogModel, error) {
	if models, ok := codex.LoadFreshModels(codex.DefaultModelsCacheTTL); ok {
		return models, nil
	}
	accessToken, accountID, err := getCodexOAuthCredentials()
	if err != nil {
		return nil, err
	}
	return codex.FetchModelsCached(context.Background(), codex.ListModelsOptions{
		BaseURL:     normalizeOpenAIOAuthBaseURL(os.Getenv("OPENAI_BASE_URL")),
		AccessToken: accessToken,
		AccountID:   accountID,
	})
}

// readCodexModelIDsFromProvidersJSON returns the set of model IDs currently
// stored under any OpenAI-OAuth provider entry. Missing file or entry yields
// an empty set so a first run reports models as "new" only when there was
// genuinely nothing before.
func readCodexModelIDsFromProvidersJSON() map[string]bool {
	providersJSONMu.Lock()
	defer providersJSONMu.Unlock()

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
		if !isOpenAICodexProviderEntry(p) {
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

// codexModelEntry converts a catalog model into a providers.json model map
// with full metadata (context window, reasoning efforts) so the picker and
// per-model effort UI work without any further enrichment pass.
func codexModelEntry(model codex.CatalogModel) map[string]interface{} {
	display := model.DisplayName
	if display == "" {
		display = model.Slug
	}
	entry := map[string]interface{}{
		"id":           model.Slug,
		"display_name": display,
	}
	if model.Description != "" {
		entry["description"] = model.Description
	}
	if ctx := model.ResolvedContextWindow(); ctx > 0 {
		effective := provider.ClampContextWindow(int(ctx))
		entry["context_window"] = effective
		entry["context"] = formatContextWindow(effective)
	}
	if len(model.SupportedReasoningLevels) > 0 {
		efforts := make([]string, 0, len(model.SupportedReasoningLevels))
		seen := map[string]bool{}
		for _, level := range model.SupportedReasoningLevels {
			// Collapse ultra into max exactly like codex-rs does on the wire.
			effort := provider.NormalizeReasoningEffortSetting(level.Effort)
			if effort == "" || effort == provider.ReasoningEffortAuto || seen[effort] {
				continue
			}
			seen[effort] = true
			efforts = append(efforts, effort)
		}
		if len(efforts) > 0 {
			entry["supports_reasoning_effort"] = true
			entry["reasoning_efforts"] = efforts
		}
	}
	return entry
}

// writeCodexModelsToProvidersJSON writes the fetched catalog into every
// OpenAI-OAuth provider entry of providers.json. Listed (visibility=="list")
// models are written first in catalog priority order with full metadata;
// pre-existing models whose IDs are not in the fetched set are preserved and
// appended so API-key models and user additions survive the refresh (this
// deliberately differs from the wholesale-replace Anthropic/Cursor writers).
func writeCodexModelsToProvidersJSON(models []codex.CatalogModel) (int, error) {
	providersJSONMu.Lock()
	defer providersJSONMu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return 0, fmt.Errorf("failed to get home directory: %w", err)
	}

	providersPath := filepath.Join(home, ".swarmos", "providers.json")
	data, err := os.ReadFile(providersPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read providers.json: %w", err)
	}

	var providers []map[string]interface{}
	if err = json.Unmarshal(data, &providers); err != nil {
		return 0, fmt.Errorf("failed to parse providers.json: %w", err)
	}

	listed := make([]codex.CatalogModel, 0, len(models))
	fetchedIDs := map[string]bool{}
	for _, model := range models {
		if model.Slug == "" || !model.Listed() {
			continue
		}
		listed = append(listed, model)
		fetchedIDs[model.Slug] = true
	}
	if len(listed) == 0 {
		return 0, fmt.Errorf("codex catalog contained no listed models")
	}

	matched := 0
	for i, entry := range providers {
		if !isOpenAICodexProviderEntry(entry) {
			continue
		}
		matched++

		providerModels := make([]map[string]interface{}, 0, len(listed)+4)
		for _, model := range listed {
			providerModels = append(providerModels, codexModelEntry(model))
		}
		// Preserve pre-existing models the catalog does not know about.
		existing, _ := entry["models"].([]interface{})
		for _, mv := range existing {
			m, ok := mv.(map[string]interface{})
			if !ok {
				continue
			}
			id, _ := m["id"].(string)
			if id == "" || fetchedIDs[id] {
				continue
			}
			providerModels = append(providerModels, m)
		}

		providers[i]["models"] = providerModels
		providers[i]["last_refreshed"] = time.Now().Format(time.RFC3339)
		providers[i]["source"] = "codex-oauth"
	}

	if matched == 0 {
		return 0, fmt.Errorf("no OpenAI-OAuth provider found in providers.json")
	}

	updatedData, err := json.MarshalIndent(providers, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("failed to marshal providers.json: %w", err)
	}

	if err = atomicWriteProvidersJSON(providersPath, updatedData, 0644); err != nil {
		return 0, fmt.Errorf("failed to write providers.json: %w", err)
	}

	return len(listed), nil
}

// writeCodexCatalogPrompts saves each catalog model's base instructions to
// ~/.swarmos/codex_prompt_catalog_<slug>.md so the codex prompt loader can
// serve the exact server-supplied prompt for new model families without
// depending on upstream GitHub file names.
func writeCodexCatalogPrompts(models []codex.CatalogModel) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".swarmos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	// Slugs are simple identifiers (e.g. gpt-5.6-sol); sanitize anyway so a
	// hostile catalog cannot traverse paths.
	sanitize := func(slug string) string {
		return strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
				return r
			default:
				return '_'
			}
		}, slug)
	}
	current := map[string]bool{}
	for _, model := range models {
		if model.Slug == "" || strings.TrimSpace(model.BaseInstructions) == "" {
			continue
		}
		name := "codex_prompt_catalog_" + sanitize(model.Slug) + ".md"
		current[name] = true
		if err := os.WriteFile(filepath.Join(dir, name), []byte(model.BaseInstructions), 0o644); err != nil {
			logDebug("[Codex] Failed to write catalog prompt for %s: %v", model.Slug, err)
		}
	}
	// Remove prompts for models that dropped out of the catalog (or whose
	// base instructions became empty) so stale files never shadow anything.
	stale, _ := filepath.Glob(filepath.Join(dir, "codex_prompt_catalog_*.md"))
	for _, path := range stale {
		if !current[filepath.Base(path)] {
			os.Remove(path)
		}
	}
}

// diffNewCodexModels returns listed model IDs present in models but not in
// prior, sorted for stable output.
func diffNewCodexModels(prior map[string]bool, models []codex.CatalogModel) []string {
	var added []string
	for _, m := range models {
		if m.Slug == "" || !m.Listed() {
			continue
		}
		if !prior[m.Slug] {
			added = append(added, m.Slug)
		}
	}
	sort.Strings(added)
	return added
}

// RefreshCodexModelsDetailed fetches the live Codex model catalog for the
// current ChatGPT OAuth login and saves it to providers.json (plus the
// per-model prompt cache files). It returns the newly-added listed model IDs
// and the total written. This is the primitive behind both the startup
// auto-refresh and the settings-UI manual refresh callback.
func RefreshCodexModelsDetailed() (added []string, total int, err error) {
	// Snapshot prior IDs BEFORE overwriting so we can diff.
	prior := readCodexModelIDsFromProvidersJSON()

	models, err := FetchCodexModels()
	if err != nil {
		return nil, 0, err
	}
	if len(models) == 0 {
		return nil, 0, fmt.Errorf("codex /models returned no models")
	}

	n, err := writeCodexModelsToProvidersJSON(models)
	if err != nil {
		return nil, 0, err
	}
	writeCodexCatalogPrompts(models)

	added = diffNewCodexModels(prior, models)
	logDebug("[Codex] Refreshed %d catalog models (%d new) into providers.json", n, len(added))
	return added, n, nil
}

// RefreshCodexModels adapts RefreshCodexModelsDetailed to the
// func() (int, error) shape expected by the settings-UI refresh callback
// wiring (mirrors RefreshAnthropicOAuthModels).
func RefreshCodexModels() (int, error) {
	_, total, err := RefreshCodexModelsDetailed()
	return total, err
}

// CodexModelsRefreshMsg is delivered on the Bubble Tea loop when the startup
// background refresh of the Codex model catalog completes.
type CodexModelsRefreshMsg struct {
	Added []string
	Total int
	Err   error
}

// refreshCodexModelsCmd returns a tea.Cmd that refreshes the Codex model
// catalog in the background and reports newly-added models back to Update().
// It is a no-op (returns nil, suppressing any message) when there is no
// OpenAI OAuth login, so non-OAuth users are never bothered. The guard only
// checks the stored token on disk — credential refresh (which can hit the
// network) is deferred to the fetch itself, and skipped entirely when the
// catalog cache is fresh.
func (a *App) refreshCodexModelsCmd() tea.Cmd {
	return func() tea.Msg {
		token, err := openai.GetStoredOAuthToken()
		if err != nil || token == nil || token.AccessToken == "" {
			logDebug("[Codex] Skipping OAuth model refresh: no stored OpenAI OAuth login")
			return nil
		}
		added, total, err := RefreshCodexModelsDetailed()
		return CodexModelsRefreshMsg{Added: added, Total: total, Err: err}
	}
}

// handleCodexModelsRefresh processes the completion of the startup Codex
// model refresh. On success with newly-appeared models it raises an in-TUI
// notification listing them.
func (a *App) handleCodexModelsRefresh(msg CodexModelsRefreshMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		logDebug("[Codex] OAuth model refresh failed: %v", msg.Err)
		return a, nil
	}
	if len(msg.Added) > 0 {
		list := strings.Join(msg.Added, ", ")
		a.addNotification("info", fmt.Sprintf("✨ %d new Codex model(s) available: %s", len(msg.Added), list))
	}
	logDebug("[Codex] OAuth model refresh complete: %d models (%d new)", msg.Total, len(msg.Added))
	return a, nil
}
