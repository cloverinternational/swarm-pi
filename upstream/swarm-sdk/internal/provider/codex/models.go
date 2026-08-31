package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	// codexClientVersion is the Codex CLI compatibility version advertised to
	// the ChatGPT backend. The backend gates model visibility on the
	// client_version query parameter and version header
	// (minimal_client_version is 0.144.0 for the gpt-5.6 family), so this is a
	// pinned compatibility constant — swarm's own version is meaningless to
	// that gate. Bump it deliberately when adopting newer catalog features.
	codexClientVersion = "0.144.0"

	modelsPath = "/models"

	// modelsRefreshTimeout mirrors codex-rs MODELS_REFRESH_TIMEOUT.
	modelsRefreshTimeout = 5 * time.Second
)

// ClientVersion returns the pinned Codex compatibility version used for
// catalog requests and version headers.
func ClientVersion() string {
	return codexClientVersion
}

// ReasoningLevel is one supported reasoning effort for a catalog model.
type ReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description,omitempty"`
}

// CatalogUpgrade describes the replacement model for a deprecated slug.
type CatalogUpgrade struct {
	Model             string `json:"model"`
	MigrationMarkdown string `json:"migration_markdown,omitempty"`
}

// VersionString tolerates both wire encodings of minimal_client_version:
// a plain string ("0.144.0") and a semver triple array ([0, 144, 0]).
type VersionString string

// UnmarshalJSON implements the dual string/array decoding.
func (v *VersionString) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*v = VersionString(s)
		return nil
	}
	var triple []int
	if err := json.Unmarshal(data, &triple); err == nil {
		parts := make([]string, len(triple))
		for i, n := range triple {
			parts[i] = fmt.Sprintf("%d", n)
		}
		*v = VersionString(strings.Join(parts, "."))
		return nil
	}
	// Unknown encoding: ignore rather than failing the whole catalog.
	*v = ""
	return nil
}

// CatalogModel is the model metadata returned by the ChatGPT backend
// /models endpoint (a subset of codex-rs protocol ModelInfo; unknown fields
// are ignored for forward compatibility).
type CatalogModel struct {
	Slug                      string           `json:"slug"`
	DisplayName               string           `json:"display_name"`
	Description               string           `json:"description,omitempty"`
	DefaultReasoningLevel     string           `json:"default_reasoning_level,omitempty"`
	SupportedReasoningLevels  []ReasoningLevel `json:"supported_reasoning_levels,omitempty"`
	Visibility                string           `json:"visibility,omitempty"`
	SupportedInAPI            bool             `json:"supported_in_api,omitempty"`
	Priority                  int              `json:"priority,omitempty"`
	ContextWindow             *int64           `json:"context_window,omitempty"`
	MaxContextWindow          *int64           `json:"max_context_window,omitempty"`
	UseResponsesLite          bool             `json:"use_responses_lite,omitempty"`
	SupportsParallelToolCalls bool             `json:"supports_parallel_tool_calls,omitempty"`
	SupportVerbosity          bool             `json:"support_verbosity,omitempty"`
	DefaultVerbosity          string           `json:"default_verbosity,omitempty"`
	MinimalClientVersion      VersionString    `json:"minimal_client_version,omitempty"`
	BaseInstructions          string           `json:"base_instructions,omitempty"`
	Upgrade                   *CatalogUpgrade  `json:"upgrade,omitempty"`
}

// SupportsMaxEffort reports whether the model's catalog effort list includes
// the top "max" (or "ultra", which collapses into max on the wire) level.
func (m CatalogModel) SupportsMaxEffort() bool {
	for _, level := range m.SupportedReasoningLevels {
		if level.Effort == "max" || level.Effort == "ultra" {
			return true
		}
	}
	return false
}

// Listed reports whether the model should be shown in user-facing pickers.
func (m CatalogModel) Listed() bool {
	return m.Visibility == "list"
}

// ResolvedContextWindow mirrors codex-rs ModelInfo::resolved_context_window.
func (m CatalogModel) ResolvedContextWindow() int64 {
	if m.ContextWindow != nil {
		return *m.ContextWindow
	}
	if m.MaxContextWindow != nil {
		return *m.MaxContextWindow
	}
	return 0
}

// modelsResponse is the wire wrapper for /models.
type modelsResponse struct {
	Models []CatalogModel `json:"models"`
}

// ListModelsOptions configures a catalog fetch against the ChatGPT backend.
type ListModelsOptions struct {
	// BaseURL defaults to https://chatgpt.com/backend-api/codex.
	BaseURL     string
	AccessToken string
	AccountID   string
	HTTPClient  *http.Client
}

// ListModels fetches the model catalog from {base}/models?client_version=...
// using ChatGPT OAuth credentials, returning models sorted by ascending
// priority plus the response ETag (empty when the server omits it).
func ListModels(ctx context.Context, opts ListModelsOptions) ([]CatalogModel, string, error) {
	if opts.AccessToken == "" {
		return nil, "", fmt.Errorf("codex access token is required")
	}

	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = "https://chatgpt.com/backend-api/codex"
	}
	endpoint, err := url.Parse(strings.TrimRight(baseURL, "/") + modelsPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse codex models url: %w", err)
	}
	query := endpoint.Query()
	query.Set("client_version", codexClientVersion)
	endpoint.RawQuery = query.Encode()

	ctx, cancel := context.WithTimeout(ctx, modelsRefreshTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create codex models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+opts.AccessToken)
	if opts.AccountID != "" {
		req.Header.Set("ChatGPT-Account-ID", opts.AccountID)
	}
	req.Header.Set("originator", "swarmos")
	req.Header.Set("version", codexClientVersion)
	req.Header.Set("User-Agent", codexUserAgent())
	req.Header.Set("Accept", "application/json")

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: modelsRefreshTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("codex models request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("codex models http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var parsed modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, "", fmt.Errorf("failed to parse codex models response: %w", err)
	}

	models := parsed.Models
	sort.SliceStable(models, func(i, j int) bool { return models[i].Priority < models[j].Priority })

	return models, resp.Header.Get("ETag"), nil
}

func codexUserAgent() string {
	return fmt.Sprintf("swarmos/%s (%s; %s)", codexClientVersion, runtime.GOOS, runtime.GOARCH)
}
