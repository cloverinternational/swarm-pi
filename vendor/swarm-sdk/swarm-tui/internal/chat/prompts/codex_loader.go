package prompts

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	codexPromptBaseURL           = "https://raw.githubusercontent.com/openai/codex/main/codex-rs/core/"
	codexPromptFileBase          = "prompt.md"
	codexPromptFileGpt5Codex     = "gpt_5_codex_prompt.md"
	codexPromptFileGpt51CodexMax = "gpt-5.1-codex-max_prompt.md"
	codexPromptFileGpt52Codex    = "gpt-5.2-codex_prompt.md"
	codexPromptFileGpt51         = "gpt_5_1_prompt.md"
	codexPromptFileGpt52         = "gpt_5_2_prompt.md"
	codexPromptCacheName         = "codex_prompt_cache.md"
	codexPromptURLVar            = "SWARMOS_CODEX_PROMPT_URL"
	codexPromptDisableVar        = "SWARMOS_CODEX_PROMPT_DISABLE_REMOTE"
	codexPromptTimeout           = 5 * time.Second
	codexPromptMaxSizeBytes      = 512 * 1024 // sanity limit
)

// loadRemoteCodexPrompt fetches the prompt from upstream, caches it to disk, and
// returns the latest contents. If fetching fails, it attempts to read the cache.
// Returns empty string on failure so callers can fall back to the embedded prompt.
// A catalog-supplied prompt (written by the codex /models fetcher from the
// model's base_instructions) always takes priority over the remote source: it
// tracks the exact model build the backend serves, unlike the upstream GitHub
// file names that no longer follow new model families.
func loadRemoteCodexPrompt(ctx context.Context, model string) string {
	if prompt := loadCatalogCodexPrompt(model); strings.TrimSpace(prompt) != "" {
		return prompt
	}

	var cacheName string = codexPromptCacheNameForModel(model)
	if strings.EqualFold(os.Getenv(codexPromptDisableVar), "1") ||
		strings.EqualFold(os.Getenv(codexPromptDisableVar), "true") {
		return loadCachedCodexPrompt(cacheName)
	}

	var url string = codexPromptURLForModel(model)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return loadCachedCodexPrompt(cacheName)
	}

	client := &http.Client{Timeout: codexPromptTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return loadCachedCodexPrompt(cacheName)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return loadCachedCodexPrompt(cacheName)
	}

	limited := io.LimitReader(resp.Body, codexPromptMaxSizeBytes)
	data, err := io.ReadAll(limited)
	if err != nil || len(data) == 0 {
		return loadCachedCodexPrompt(cacheName)
	}

	content := string(data)
	if strings.TrimSpace(content) == "" {
		return loadCachedCodexPrompt(cacheName)
	}

	// Cache best-effort; ignore errors.
	_ = saveCodexPromptCache(content, cacheName)
	return content
}

func loadCachedCodexPrompt(cacheName string) string {
	path, err := codexPromptCachePath(cacheName)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func saveCodexPromptCache(content string, cacheName string) error {
	path, err := codexPromptCachePath(cacheName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("codex prompt: mkdir cache dir: %w", err)
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func codexPromptCachePath(cacheName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("codex prompt: resolve home: %w", err)
	}
	return filepath.Join(home, ".swarmos", cacheName), nil
}

// codexPromptCatalogSlug normalizes a model ID into the slug used for
// catalog-supplied prompt files (lowercase, provider prefix stripped).
// Returns "" when the model does not yield a usable slug.
func codexPromptCatalogSlug(model string) string {
	slug := strings.ToLower(strings.TrimSpace(model))
	if idx := strings.LastIndex(slug, "/"); idx != -1 {
		slug = slug[idx+1:]
	}
	return slug
}

// codexPromptCatalogPath returns the on-disk location of the catalog-supplied
// prompt for a model (~/.swarmos/codex_prompt_catalog_<slug>.md), or "" when
// the model has no slug or the home directory cannot be resolved.
func codexPromptCatalogPath(model string) string {
	var slug string = codexPromptCatalogSlug(model)
	if slug == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".swarmos", "codex_prompt_catalog_"+slug+".md")
}

// loadCatalogCodexPrompt reads the catalog-supplied prompt for a model, if one
// has been written by the codex /models fetcher. Returns "" when absent.
func loadCatalogCodexPrompt(model string) string {
	var path string = codexPromptCatalogPath(model)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func codexPromptFileNameForModel(model string) string {
	var normalized string = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(normalized, "gpt-5.2-codex"),
		strings.HasPrefix(normalized, "bengalfox"),
		strings.HasPrefix(normalized, "exp-codex"),
		strings.HasPrefix(normalized, "codex-1p"):
		return codexPromptFileGpt52Codex
	case strings.HasPrefix(normalized, "gpt-5.1-codex-max"):
		return codexPromptFileGpt51CodexMax
	case strings.HasPrefix(normalized, "gpt-5-codex"),
		strings.HasPrefix(normalized, "gpt-5.1-codex"):
		return codexPromptFileGpt5Codex
	case strings.HasPrefix(normalized, "gpt-5.6"),
		strings.HasPrefix(normalized, "gpt-5.5"),
		strings.HasPrefix(normalized, "gpt-5.4"):
		// New catalog families (gpt-5.6-sol/terra/luna, gpt-5.5, gpt-5.4):
		// upstream ships no dedicated prompt file, so fall back to the
		// gpt-5.2-codex prompt. A catalog-supplied prompt file
		// (~/.swarmos/codex_prompt_catalog_<slug>.md) takes priority in
		// loadRemoteCodexPrompt when present.
		return codexPromptFileGpt52Codex
	case strings.HasPrefix(normalized, "codex-mini-latest"):
		return codexPromptFileBase
	case strings.HasPrefix(normalized, "codex-"):
		return codexPromptFileGpt5Codex
	case strings.HasPrefix(normalized, "gpt-5.2"),
		strings.HasPrefix(normalized, "boomslang"):
		return codexPromptFileGpt52
	case strings.HasPrefix(normalized, "gpt-5.1"):
		return codexPromptFileGpt51
	default:
		return codexPromptFileBase
	}
}

func codexPromptCacheNameForModel(model string) string {
	var fileName string = codexPromptFileNameForModel(model)
	if fileName == codexPromptFileBase {
		return codexPromptCacheName
	}
	return "codex_prompt_cache_" + fileName
}

func codexPromptURLForModel(model string) string {
	var url string = os.Getenv(codexPromptURLVar)
	if url != "" {
		return url
	}
	var fileName string = codexPromptFileNameForModel(model)
	return codexPromptBaseURL + fileName
}

func codexPromptCacheKey(model string) string {
	// Catalog-supplied prompts are per-model-slug, so key the in-memory cache
	// by slug when one exists — otherwise two models sharing the same
	// fallback prompt file (e.g. gpt-5.6-sol and gpt-5.6-terra) would alias
	// one cache entry despite carrying different catalog prompts.
	if path := codexPromptCatalogPath(model); path != "" {
		if _, err := os.Stat(path); err == nil {
			return "codex_prompt_catalog_" + codexPromptCatalogSlug(model)
		}
	}
	return codexPromptCacheNameForModel(model)
}
