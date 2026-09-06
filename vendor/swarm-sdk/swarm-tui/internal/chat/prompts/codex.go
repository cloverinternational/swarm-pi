package prompts

import _ "embed"
import (
	"context"
	"sync"
)

// defaultCodexPrompt is the embedded prompt; used as a final fallback.
//
//go:embed codex_system_prompt.txt
var defaultCodexPrompt string

type codexPromptEntry struct {
	prompt string
	loaded bool
}

var (
	codexPromptMu    sync.RWMutex
	codexPromptCache map[string]codexPromptEntry = make(map[string]codexPromptEntry)
)

// GetCodexSystemPrompt returns the current Codex system prompt. It prefers the
// remote source (cached on disk), then falls back to the embedded prompt.
func GetCodexSystemPrompt(ctx context.Context) string {
	return GetCodexSystemPromptForModel(ctx, "")
}

// GetCodexSystemPromptForModel returns the Codex system prompt that matches the model family.
func GetCodexSystemPromptForModel(ctx context.Context, model string) string {
	var cacheKey string = codexPromptCacheKey(model)
	codexPromptMu.RLock()
	entry, ok := codexPromptCache[cacheKey]
	if ok && entry.loaded {
		prompt := entry.prompt
		codexPromptMu.RUnlock()
		return prompt
	}
	codexPromptMu.RUnlock()

	var prompt string = loadRemoteCodexPrompt(ctx, model)
	if prompt == "" {
		prompt = defaultCodexPrompt
	}

	codexPromptMu.Lock()
	entry, ok = codexPromptCache[cacheKey]
	if !ok || !entry.loaded {
		codexPromptCache[cacheKey] = codexPromptEntry{
			prompt: prompt,
			loaded: true,
		}
	} else {
		prompt = entry.prompt
	}
	codexPromptMu.Unlock()

	return prompt
}

// RefreshCodexSystemPrompt re-fetches the Codex prompt and updates the cached value.
// It returns the refreshed prompt and whether the cached value changed.
func RefreshCodexSystemPrompt(ctx context.Context) (string, bool) {
	return RefreshCodexSystemPromptForModel(ctx, "")
}

// RefreshCodexSystemPromptForModel re-fetches the Codex prompt for a model and updates the cache.
// It returns the refreshed prompt and whether the cached value changed.
func RefreshCodexSystemPromptForModel(ctx context.Context, model string) (string, bool) {
	var prompt string = loadRemoteCodexPrompt(ctx, model)
	if prompt == "" {
		return "", false
	}

	var cacheKey string = codexPromptCacheKey(model)
	codexPromptMu.Lock()
	entry := codexPromptCache[cacheKey]
	changed := prompt != entry.prompt
	codexPromptCache[cacheKey] = codexPromptEntry{
		prompt: prompt,
		loaded: true,
	}
	codexPromptMu.Unlock()

	return prompt, changed
}
