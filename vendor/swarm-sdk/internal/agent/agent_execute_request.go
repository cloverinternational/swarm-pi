package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/prompttrace"
)

// buildProviderRequest constructs a provider chat request from agent state and messages.
//
// Definition fields (Model, SystemPrompt, Capabilities, ProviderConfig) are read
// under a.mu.RLock so concurrent SetModel / SetSystemPrompt / SetProvider calls
// from the TUI's live-switch paths observe a coherent snapshot. We snapshot into
// locals and release the lock before building the request so the rest of the
// function (buildProviderTools, hook emits) doesn't run under a reader lock.
//
// This signature accepts only messages; the per-request provenance trace is
// reached via Agent's stashed traceCtx (set in execute() before the loop).
func (a *Agent) buildProviderRequest(messages []*conversation.Message) provider.ChatRequest {
	// Resolve the trace context lazily — most call paths (and the existing
	// race test) never set traceCtx, so this is just one nil-check.
	traceCtx := a.currentTraceCtx()
	a.mu.RLock()
	model := a.definition.Model
	systemPrompt := a.definition.SystemPrompt
	// Per-request system prompt override (set by ExecuteRequest.SystemPromptOverride)
	// replaces the definition prompt for this turn only; the definition is not
	// mutated, so concurrent/later turns keep the configured prompt.
	if a.reqSystemPromptOverride != "" {
		systemPrompt = a.reqSystemPromptOverride
	}
	var maxTokens int
	var temperature float64
	var forceTemperature bool
	if a.definition.Capabilities != nil {
		maxTokens = a.definition.Capabilities.MaxTokens
		temperature = a.definition.Capabilities.Temperature
		forceTemperature = a.definition.Capabilities.ForceTemperature
	}
	var providerMetadata map[string]any
	if !a.definition.ProviderConfig.IsZero() {
		providerMetadata = a.definition.ProviderConfig.ToMetadata()
	}
	a.mu.RUnlock()

	req := provider.ChatRequest{
		Messages:      messages,
		Model:         model,
		SystemPrompt:  systemPrompt,
		Tools:         a.buildProviderTools(),
		StopSequences: []string{},
		Metadata:      make(map[string]any),
	}
	// Provenance: record the base system prompt (post-injection by the
	// caller layer, which is what Agent sees). The contribution sites in
	// the TUI's prompt-builder layer have already recorded their own rows;
	// this row attributes whatever they handed us to "agent_definition".
	prompttrace.From(traceCtx).Append("agent_definition.SystemPrompt", systemPrompt)

	// Provenance: record each tool's wire size (name + description + serialized
	// parameter schema). Tool schemas live in the request's Tools field, not
	// the system-prompt string, so without this the --raw banner under-reports
	// total context by however many tokens the schemas occupy — often a large
	// share once dozens of tools are registered.
	if col := prompttrace.From(traceCtx); col != nil {
		for _, t := range req.Tools {
			size := len(t.Name) + len(t.Description)
			if t.Parameters != nil {
				if b, err := json.Marshal(t.Parameters); err == nil {
					size += len(b)
				}
			}
			col.AppendTool(t.Name, size)
		}
	}

	if maxTokens > 0 {
		mt := maxTokens
		req.MaxTokens = &mt
	}
	// Emit temperature when it is positive, OR when the caller explicitly forced
	// it (ForceTemperature) and it is a valid >=0 value. The forced path lets
	// headless/benchmark callers pin greedy temperature=0 decoding; provider
	// translators that reject non-default sampling params still drop it.
	if temperature > 0 || (forceTemperature && temperature >= 0) {
		t := temperature
		req.Temperature = &t
	}

	maps.Copy(req.Metadata, providerMetadata)

	// CRITICAL: Add request context (includes thinking config, etc.)
	// This must come AFTER provider config so it can override
	a.mu.RLock()
	if len(a.requestContext) > 0 {
		a.logger.Info(a.ctx, "agent.buildProviderRequest.adding_context",
			observability.F("context_keys", getContextKeys(a.requestContext)),
			observability.F("thinking_enabled", a.requestContext["thinking_enabled"]),
			observability.F("thinking_budget", a.requestContext["thinking_budget"]),
		)
	}
	maps.Copy(req.Metadata, a.requestContext)
	a.mu.RUnlock()

	req.ReasoningEffort = resolveReasoningEffortFromMetadata(req.Metadata)

	// Append ephemeral system content (per-request only, never stored).
	// This is called AFTER the base system prompt is set so the nudge or any
	// other ephemeral text lands in the <swarmos_context> dynamic block, which
	// the Anthropic translator places in the un-cached third system block.
	// DEPRECATED: Ephemeral system prompt injection breaks cache and is not
	// persistent. This path is kept for backward compatibility only.
	a.mu.RLock()
	ephFn := a.ephemeralSystemFn
	a.mu.RUnlock()
	if ephFn != nil {
		if ephText := ephFn(messages); ephText != "" {
			const open = "<swarmos_context>"
			const close = "</swarmos_context>"
			// Only wrap if not already wrapped (the function may return pre-tagged text).
			if !strings.Contains(ephText, open) {
				ephText = open + "\n" + strings.TrimSpace(ephText) + "\n" + close
			}
			req.SystemPrompt = req.SystemPrompt + "\n\n" + ephText
			prompttrace.From(traceCtx).Append("ephemeral_system_block", ephText)
		}
	}

	return req
}

// estimateProviderRequestTokens estimates the provider-visible request fields
// that can grow between two authoritative usage reports.
func estimateProviderRequestTokens(req provider.ChatRequest) int {
	estimated := compaction.EstimateMessagesTokens(req.Messages) +
		compaction.EstimateTokens(req.SystemPrompt)
	if toolsJSON, err := json.Marshal(req.Tools); err == nil {
		estimated += compaction.EstimateTokens(string(toolsJSON))
	}
	return estimated
}

// currentRequestTokens anchors request growth to the last real provider usage.
// The max fallback covers the first request, where no comparable baseline exists.
func (a *Agent) currentRequestTokens(req provider.ChatRequest) int {
	currentEstimate := estimateProviderRequestTokens(req)
	a.mu.RLock()
	realTokens := a.inputTokens
	previousEstimate := a.lastRequestEstimate
	a.mu.RUnlock()
	if realTokens > 0 && previousEstimate > 0 {
		return realTokens + max(0, currentEstimate-previousEstimate)
	}
	return max(realTokens, currentEstimate)
}

// currentTraceCtx returns the per-request context with a prompttrace collector
// attached, set by execute() before the loop. Returns nil when no caller has
// stashed a ctx — buildProviderRequest must remain usable from the race test
// and any future callers that build requests outside of execute().
func (a *Agent) currentTraceCtx() context.Context {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.traceCtx
}

// resolveReasoningEffortFromMetadata reads reasoning_effort / reasoning_level /
// disable_reasoning keys from the request metadata and returns a normalised
// provider.ReasoningEffort* constant (or "" if none is set).
func resolveReasoningEffortFromMetadata(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}

	if rawDisable, ok := metadata["disable_reasoning"]; ok {
		if disabled, ok := toBool(rawDisable); ok && disabled {
			return provider.ReasoningEffortNone
		}
	}

	if rawEffort, ok := metadata["reasoning_effort"]; ok {
		var normalized string = provider.NormalizeReasoningEffort(fmt.Sprintf("%v", rawEffort))
		if normalized != "" {
			return normalized
		}
	}

	if rawLevel, ok := metadata["reasoning_level"]; ok {
		var normalized string = provider.NormalizeReasoningEffort(fmt.Sprintf("%v", rawLevel))
		if normalized != "" {
			return normalized
		}
	}

	return ""
}

// toBool coerces a metadata value to a boolean.
// Returns (value, ok) — ok is false when the type cannot be converted.
func toBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err != nil {
			return false, false
		}
		return parsed, true
	default:
		return false, false
	}
}

// codexSystemMessagesDisallowed reports whether the current provider/model
// combination rejects system-role messages in the input array.
// Codex (OpenAI code-execution transport) errors on system messages.
func codexSystemMessagesDisallowed(prov provider.Provider, model string) bool {
	var normalizedModel string = strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(normalizedModel, "codex") {
		return true
	}
	if prov != nil && strings.EqualFold(strings.TrimSpace(prov.Name()), "codex") {
		return true
	}
	return false
}
