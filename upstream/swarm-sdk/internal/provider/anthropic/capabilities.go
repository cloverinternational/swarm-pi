package anthropic

import "github.com/Swarm-Code/mono/swarm-sdk/internal/provider"

// getCapabilities returns the capabilities of the Anthropic provider.
func getCapabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:       true,
		FunctionCalling: true,
		Vision:          true, // Claude supports images
		// Provider-level fallback when no per-model window is configured.
		// Current default Claude models (Opus 4.6+, Sonnet 4.6+/5, Fable 5)
		// publish 1M-token windows, capped at the SDK's 300K default policy
		// (provider.DefaultContextWindowCap). Older 200K models (Opus ≤4.5,
		// Sonnet ≤4.5, Haiku 4.5) should carry an explicit per-model context
		// window in config — user config always overrides this fallback.
		// Use GetModelContextWindow(model) for the per-model default.
		MaxContextWindow:     provider.DefaultContextWindowCap,
		MaxOutputTokens:      8192, // Standard max output (4096 for some models)
		SupportsSystemPrompt: true, // System prompt is a separate parameter
		SupportsTemperature:  true, // Temperature range: 0.0-1.0
		SupportedModels: []string{
			"claude-fable-5",    // Mythos-class (above Opus); most capable, adaptive-only thinking, rejects sampling params
			"claude-opus-4-7",   // Latest Opus; adaptive-only thinking, rejects sampling params, supports xhigh effort
			"claude-opus-4-6",   // Opus with adaptive thinking
			"claude-sonnet-4-6", // Latest Sonnet; default for general-purpose work
			"claude-sonnet-4-5-20250929",
			"claude-haiku-4-5-20251001",
			"claude-opus-4-5-20251101",
		}, // Models loaded from config (not hardcoded)
		PromptCaching: true,  // Native prompt caching support
		SupportsJSON:  false, // Claude doesn't have explicit JSON mode (yet)
	}
}

// Model capability metadata (for reference, not used in code)
// Source: https://docs.anthropic.com/en/docs/models-overview
//
// Claude Opus 4.7 (released 2026-04-16):
//   - Context: 1M tokens (standard pricing, no long-context premium)
//   - Output: 128K tokens (streaming) / 300K tokens (batch with output-300k-2026-03-24 beta)
//   - Vision: 2576px / 3.75MP (up from 1568px)
//   - Thinking: ADAPTIVE ONLY — `thinking.type: "enabled"` with budget_tokens returns HTTP 400
//   - Sampling: REJECTS non-default temperature, top_p, top_k (HTTP 400)
//   - Prefill: assistant-message prefilling NOT supported (HTTP 400)
//   - Effort levels: low / medium / high (default) / max / xhigh (NEW — recommended for coding)
//   - Tokenizer: new tokenizer produces 1.0x–1.35x the token count of Opus 4.6
//   - Beta features: task_budget (task-budgets-2026-03-13), 300K batch output (output-300k-2026-03-24)
//   - Behavior: more literal instruction following, fewer tool calls by default at "high" effort
//
// Claude Opus 4.6:
//   - Context: 1M tokens (GA at standard pricing since March 2026;
//     long-context surcharges eliminated)
//   - Output: 8K tokens
//   - Strengths: Most capable with adaptive thinking
//   - Use cases: Complex reasoning, research, creative tasks
//   - Features: Adaptive thinking (recommended), extended thinking, citations
//
// Claude Sonnet 4.6 / Sonnet 5, Fable 5 / Mythos 5:
//   - Context: 1M tokens (GA at standard pricing; the Sonnet 4.5/4 1M beta
//     header was retired — migrate to 4.6+ for long context)
//
// Claude Opus 4.5:
//   - Context: 200K tokens
//   - Output: 8K tokens
//   - Strengths: Most capable, complex tasks
//   - Use cases: Research, analysis, creative writing
//
// Claude Sonnet 4.5:
//   - Context: 200K tokens
//   - Output: 8K tokens
//   - Strengths: Best for coding and agents
//   - Use cases: Software development, automation
//
// Claude 3.7 Sonnet:
//   - Context: 200K tokens
//   - Output: 8K tokens
//   - Strengths: High performance with extended thinking
//   - Use cases: Complex reasoning, multi-step tasks
//
// Claude 3.5 Haiku:
//   - Context: 200K tokens
//   - Output: 8K tokens
//   - Strengths: Fastest, most compact
//   - Use cases: Real-time applications, high throughput
//
// All models support:
//   - Streaming
//   - Tool calling
//   - Vision (images)
//   - Prompt caching
//   - Extended thinking (Claude 3.7+, manual mode with budget_tokens)
//   - Adaptive thinking (Opus 4.6+, recommended over manual mode)
//   - Citations (beta)
