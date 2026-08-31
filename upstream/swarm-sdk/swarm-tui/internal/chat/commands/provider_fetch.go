package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// FetchModelsForProvider queries a provider's model-listing endpoint and returns
// the discovered models as []ModelConfig, ready to be stored in providers.json.
//
// It is the single shared entry point used by BOTH the interactive add-provider
// form (auto-fetch) and the `swarmos provider`/`model` CLI, so the two paths can
// never drift. The endpoint is chosen from apiType:
//
//	anthropic                       -> GET {base}/v1/models   (x-api-key + anthropic-version)
//	openai / openai-compatible / "" -> GET {base}/v1/models   (Bearer apiKey)
//
// baseURL may be empty for well-known api types (a sensible default is used).
// A missing/empty apiKey is allowed — some self-hosted gateways list models
// without auth — but most providers will return 401, surfaced as an error.
func FetchModelsForProvider(ctx context.Context, apiType, baseURL, apiKey string) ([]ModelConfig, error) {
	apiType = strings.ToLower(strings.TrimSpace(apiType))
	base := normalizeModelsBaseURL(apiType, baseURL)
	if base == "" {
		return nil, fmt.Errorf("%s", i18n.T("commands.provider_fetch.no_base_url", apiType))
	}

	switch apiType {
	case "anthropic":
		return fetchAnthropicModels(ctx, base, apiKey)
	default:
		// openai, openai-compatible, and unknown types all speak the
		// OpenAI /v1/models contract.
		return fetchOpenAICompatibleModels(ctx, base, apiKey)
	}
}

// normalizeModelsBaseURL returns the base URL to hit for the models endpoint,
// trimming a trailing slash and supplying a default for known api types.
func normalizeModelsBaseURL(apiType, baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base != "" {
		return base
	}
	switch apiType {
	case "anthropic":
		return "https://api.anthropic.com"
	case "openai":
		return "https://api.openai.com/v1"
	}
	return ""
}

// modelsEndpoint joins the base URL with /models, tolerating base URLs that
// already end in /v1 (OpenAI-compatible) or bare hosts (Anthropic).
func modelsEndpoint(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/models") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/models"
	}
	return base + "/v1/models"
}

func fetchOpenAICompatibleModels(ctx context.Context, base, apiKey string) ([]ModelConfig, error) {
	url := modelsEndpoint(base)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	body, err := doModelsRequest(req, url)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			DisplayName   string `json:"display_name"`
			Description   string `json:"description"`
			ContextLength int    `json:"context_length"`
			ContextWindow int    `json:"context_window"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("commands.provider_fetch.parse_models", url), err)
	}

	models := make([]ModelConfig, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if strings.TrimSpace(d.ID) == "" {
			continue
		}
		ctxWin := d.ContextWindow
		if ctxWin == 0 {
			ctxWin = d.ContextLength
		}
		display := firstNonEmpty(d.DisplayName, d.Name, d.ID)
		models = append(models, ModelConfig{
			ID:            d.ID,
			DisplayName:   display,
			Description:   d.Description,
			ContextWindow: ctxWin,
			Context:       formatContextWindow(ctxWin),
		})
	}
	return dedupeAndSortModels(models), nil
}

func fetchAnthropicModels(ctx context.Context, base, apiKey string) ([]ModelConfig, error) {
	url := modelsEndpoint(base)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	req.Header.Set("anthropic-version", "2023-06-01")

	body, err := doModelsRequest(req, url)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("commands.provider_fetch.parse_anthropic_models", url), err)
	}

	models := make([]ModelConfig, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if strings.TrimSpace(d.ID) == "" {
			continue
		}
		models = append(models, ModelConfig{
			ID:          d.ID,
			DisplayName: firstNonEmpty(d.DisplayName, d.ID),
		})
	}
	return dedupeAndSortModels(models), nil
}

func doModelsRequest(req *http.Request, url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("commands.provider_fetch.request_failed", url), err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4MB cap
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("commands.provider_fetch.read_failed", url), err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%s", i18n.T("commands.provider_fetch.invalid_api_key", url, resp.StatusCode))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s", i18n.T("commands.provider_fetch.status_error", url, resp.StatusCode, truncateErr(string(body), 200)))
	}
	return body, nil
}

func dedupeAndSortModels(models []ModelConfig) []ModelConfig {
	seen := make(map[string]struct{}, len(models))
	out := make([]ModelConfig, 0, len(models))
	for _, m := range models {
		if _, ok := seen[m.ID]; ok {
			continue
		}
		seen[m.ID] = struct{}{}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncateErr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
