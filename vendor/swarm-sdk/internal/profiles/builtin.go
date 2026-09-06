package profiles

import "time"

// GenerateBuiltinProfiles returns the full set of default profiles.
//
// # General-purpose profiles (model-agnostic, multi-provider fallback chains)
//
//   - Balanced     — everyday coding, Claude Sonnet primary + Cerebras fallback
//   - Quality      — production work, Claude Opus across all roles
//   - Performance  — rapid iteration, Cerebras speed-first
//   - Cost-Optimized — minimal API cost, Haiku + Cerebras
//
// # Vendor-specific profiles (single-provider, latest flagship models)
//
//   - Claude Code  — Anthropic only: Opus 4.7 (steering/main), Sonnet 4.6 (background), Haiku 4.5 (fast)
//   - Gemini Code  — Google only: Gemini 2.5 Pro (main/steering), Flash (sub-agent/inference), Flash-Lite (compaction)
//   - Codex        — OpenAI Codex: gpt-5.6-terra (main/vision), gpt-5.6-sol (steering/long-context), gpt-5.6-luna (fast/background)
//   - GLM / Z.AI   — Cerebras GLM: zai-glm-4.7 across all roles with Llama fallbacks
//
// All roles get a fallback.Chain with at least primary + one fallback where
// available — this is the core resilience improvement over single-model configs.
func GenerateBuiltinProfiles() []AgentProfile {
	now := time.Now()

	// ── helpers ─────────────────────────────────────────────────────────────────

	caps := func(maxTokens int, temp float64, maxTurns, timeout int) *AgentCapabilities {
		return &AgentCapabilities{
			MaxTokens:   maxTokens,
			Temperature: temp,
			MaxTurns:    maxTurns,
			Timeout:     timeout,
		}
	}

	// role creates a RoleConfig with primary + optional fallback pairs.
	// fallbacks: alternating "provider", "model" strings.
	role := func(provider, model string, c *AgentCapabilities, fallbacks ...string) RoleConfig {
		rc := NewRoleConfigChain(provider, model)
		rc.Capabilities = c
		for i := 0; i+1 < len(fallbacks); i += 2 {
			rc.Chain.AddFallback(fallbacks[i], fallbacks[i+1])
		}
		return rc
	}

	// ── BALANCED (default) ────────────────────────────────────────────────────
	balanced := AgentProfile{
		ID:          "balanced",
		Name:        "Balanced",
		Description: "Optimal quality/speed balance for everyday coding — Claude Sonnet with Cerebras fallback",
		Icon:        "⚖️",
		Color:       "#3B82F6",
		IsDefault:   true,
		RetryPolicy: DefaultRetryPolicy(),
		CreatedAt:   now,
		UpdatedAt:   now,
		Roles: map[ModelAlias]RoleConfig{
			AliasMain: role(
				"anthropic", "claude-fable-5",
				caps(31999, 0.7, 0, 300),
				"anthropic", "claude-sonnet-4-6",
			),
			AliasSteering: role(
				"anthropic", "claude-opus-4-7",
				caps(16384, 0.3, 5, 120),
				"anthropic", "claude-sonnet-4-6",
			),
			AliasBackground: role(
				"anthropic", "claude-sonnet-4-6",
				caps(16384, 0.5, 0, 1800),
			),
			AliasSubAgent: role(
				"anthropic", "claude-haiku-4-5-20251001",
				caps(8192, 0.5, 3, 60),
				"cerebras", "llama-3.3-70b",
			),
			AliasInference: role(
				"cerebras", "llama-3.3-70b",
				caps(8192, 0.7, 10, 60),
				"anthropic", "claude-haiku-4-5-20251001",
			),
			AliasLongContext: role(
				"anthropic", "claude-sonnet-4-6",
				caps(65536, 0.5, 5, 600),
			),
			AliasCompaction: role(
				"anthropic", "claude-haiku-4-5-20251001",
				caps(16384, 0.3, 3, 120),
				"cerebras", "llama-3.3-70b",
			),
			AliasVision: role(
				"anthropic", "claude-sonnet-4-6",
				caps(16384, 0.5, 5, 300),
			),
		},
	}

	// ── QUALITY ────────────────────────────────────────────────────────────────
	quality := AgentProfile{
		ID:          "quality",
		Name:        "Quality",
		Description: "Best models for production-quality work — Claude Opus across all roles",
		Icon:        "⭐",
		Color:       "#F59E0B",
		IsDefault:   false,
		RetryPolicy: &RetryPolicy{
			RotateOnRateLimit: true,
			RotateOnPayment:   true,
			CooldownSeconds:   60,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Roles: map[ModelAlias]RoleConfig{
			AliasMain: role(
				"anthropic", "claude-fable-5",
				caps(31999, 0.7, 0, 600),
				"anthropic", "claude-opus-4-7",
			),
			AliasSteering: role(
				"anthropic", "claude-opus-4-7",
				caps(16384, 0.2, 5, 180),
			),
			AliasBackground: role(
				"anthropic", "claude-sonnet-4-6",
				caps(16384, 0.5, 0, 1800),
			),
			AliasSubAgent: role(
				"anthropic", "claude-haiku-4-5-20251001",
				caps(8192, 0.5, 3, 90),
			),
			AliasInference: role(
				"anthropic", "claude-opus-4-7",
				caps(31999, 0.7, 15, 600),
			),
			AliasLongContext: role(
				"anthropic", "claude-opus-4-7",
				caps(65536, 0.5, 5, 900),
			),
			AliasCompaction: role(
				"anthropic", "claude-haiku-4-5-20251001",
				caps(16384, 0.3, 3, 120),
			),
			AliasVision: role(
				"anthropic", "claude-opus-4-7",
				caps(16384, 0.5, 5, 300),
			),
		},
	}

	// ── PERFORMANCE ────────────────────────────────────────────────────────────
	performance := AgentProfile{
		ID:          "performance",
		Name:        "Performance",
		Description: "Fastest models for rapid iteration — Cerebras speed-first with Haiku fallback",
		Icon:        "🚀",
		Color:       "#10B981",
		IsDefault:   false,
		RetryPolicy: &RetryPolicy{
			RotateOnRateLimit: true,
			RotateOnPayment:   true,
			CooldownSeconds:   10,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Roles: map[ModelAlias]RoleConfig{
			AliasMain: role(
				"cerebras", "llama-3.3-70b",
				caps(16384, 0.7, 0, 180),
				"anthropic", "claude-haiku-4-5-20251001",
			),
			AliasSteering: role(
				"cerebras", "llama-3.3-70b",
				caps(8192, 0.3, 3, 60),
				"anthropic", "claude-haiku-4-5-20251001",
			),
			AliasBackground: role(
				"cerebras", "llama-3.3-70b",
				caps(16384, 0.5, 0, 1800),
			),
			AliasSubAgent: role(
				"cerebras", "llama-3.3-70b",
				caps(8192, 0.5, 2, 30),
			),
			AliasInference: role(
				"cerebras", "llama-3.3-70b",
				caps(16384, 0.7, 10, 180),
			),
			AliasLongContext: role(
				"anthropic", "claude-sonnet-4-6",
				caps(65536, 0.5, 5, 600),
			),
			AliasCompaction: role(
				"cerebras", "llama-3.3-70b",
				caps(8192, 0.3, 3, 60),
				"anthropic", "claude-haiku-4-5-20251001",
			),
			AliasVision: role(
				"anthropic", "claude-sonnet-4-6",
				caps(16384, 0.5, 5, 300),
				"anthropic", "claude-haiku-4-5-20251001",
			),
		},
	}

	// ── COST-OPTIMIZED ────────────────────────────────────────────────────────
	costOpt := AgentProfile{
		ID:          "cost-optimized",
		Name:        "Cost-Optimized",
		Description: "Minimum API cost — Haiku and Cerebras across all roles",
		Icon:        "💰",
		Color:       "#8B5CF6",
		IsDefault:   false,
		RetryPolicy: DefaultRetryPolicy(),
		CreatedAt:   now,
		UpdatedAt:   now,
		Roles: map[ModelAlias]RoleConfig{
			AliasMain: role(
				"anthropic", "claude-haiku-4-5-20251001",
				caps(16384, 0.7, 0, 300),
				"cerebras", "llama-3.3-70b",
			),
			AliasSteering: role(
				"anthropic", "claude-haiku-4-5-20251001",
				caps(8192, 0.3, 3, 120),
			),
			AliasBackground: role(
				"anthropic", "claude-haiku-4-5-20251001",
				caps(16384, 0.5, 0, 1800),
			),
			AliasSubAgent: role(
				"cerebras", "llama-3.3-70b",
				caps(8192, 0.5, 2, 60),
			),
			AliasInference: role(
				"cerebras", "llama-3.3-70b",
				caps(8192, 0.7, 10, 60),
				"anthropic", "claude-haiku-4-5-20251001",
			),
			AliasLongContext: role(
				"anthropic", "claude-haiku-4-5-20251001",
				caps(31999, 0.5, 5, 600),
			),
			AliasCompaction: role(
				"anthropic", "claude-haiku-4-5-20251001",
				caps(8192, 0.3, 3, 90),
				"cerebras", "llama-3.3-70b",
			),
			AliasVision: role(
				"anthropic", "claude-haiku-4-5-20251001",
				caps(8192, 0.5, 5, 120),
			),
		},
	}

	// ── CLAUDE CODE ────────────────────────────────────────────────────────────
	// Anthropic-only stack: Opus 4 → Sonnet 4 → Haiku 3.5 in order of role weight.
	// Ideal when you have solid Anthropic credits and want the purest Claude experience.
	claudeCode := AgentProfile{
		ID:          "claude-code",
		Name:        "Claude Code",
		Description: "Anthropic-only — Opus 4.7 for heavy lifting, Sonnet 4.6 for everyday, Haiku 4.5 for fast ops",
		Icon:        "🔶",
		Color:       "#D97706",
		IsDefault:   false,
		RetryPolicy: &RetryPolicy{
			RotateOnRateLimit: true,
			RotateOnPayment:   true,
			CooldownSeconds:   45,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Roles: map[ModelAlias]RoleConfig{
			AliasMain: role(
				"anthropic", "claude-sonnet-4-6",
				caps(31999, 0.7, 0, 300),
				"anthropic", "claude-opus-4-7", // fallback to opus if sonnet hits limits
			),
			AliasSteering: role(
				"anthropic", "claude-opus-4-7",
				caps(16384, 0.2, 5, 180),
				"anthropic", "claude-sonnet-4-6",
			),
			AliasBackground: role(
				"anthropic", "claude-sonnet-4-6",
				caps(16384, 0.5, 0, 1800),
				"anthropic", "claude-haiku-4-5-20251001",
			),
			AliasSubAgent: role(
				"anthropic", "claude-haiku-4-5-20251001",
				caps(8192, 0.5, 3, 60),
				"anthropic", "claude-sonnet-4-6",
			),
			AliasInference: role(
				"anthropic", "claude-haiku-4-5-20251001",
				caps(8192, 0.7, 10, 60),
			),
			AliasLongContext: role(
				"anthropic", "claude-opus-4-7",
				caps(65536, 0.5, 5, 900),
				"anthropic", "claude-sonnet-4-6",
			),
			AliasCompaction: role(
				"anthropic", "claude-haiku-4-5-20251001",
				caps(16384, 0.3, 3, 120),
				"anthropic", "claude-sonnet-4-6",
			),
			AliasVision: role(
				"anthropic", "claude-sonnet-4-6",
				caps(16384, 0.5, 5, 300),
			),
		},
	}

	// ── GEMINI CODE ────────────────────────────────────────────────────────────
	// Google-only stack. 2.5 Pro has a 2M token context window — ideal for
	// large codebases. Flash handles high-volume sub-agent / inference work cheaply.
	geminiCode := AgentProfile{
		ID:          "gemini-code",
		Name:        "Gemini Code",
		Description: "Google-only — Gemini 2.5 Pro (2M ctx) for main/steering, Flash for fast ops",
		Icon:        "💠",
		Color:       "#4285F4",
		IsDefault:   false,
		RetryPolicy: &RetryPolicy{
			RotateOnRateLimit: true,
			RotateOnPayment:   true,
			CooldownSeconds:   30,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Roles: map[ModelAlias]RoleConfig{
			AliasMain: role(
				"google", "gemini-2.5-pro",
				caps(65536, 0.7, 0, 300),
				"google", "gemini-2.5-flash",
			),
			AliasSteering: role(
				"google", "gemini-2.5-pro",
				caps(32768, 0.2, 5, 180),
				"google", "gemini-2.5-flash",
			),
			AliasBackground: role(
				"google", "gemini-2.5-flash",
				caps(32768, 0.5, 0, 1800),
				"google", "gemini-2.5-pro",
			),
			AliasSubAgent: role(
				"google", "gemini-2.5-flash",
				caps(16384, 0.5, 3, 60),
				"google", "gemini-2.5-flash-lite",
			),
			AliasInference: role(
				"google", "gemini-2.5-flash",
				caps(16384, 0.7, 10, 60),
				"google", "gemini-2.5-flash-lite",
			),
			AliasLongContext: role(
				"google", "gemini-2.5-pro",
				caps(1000000, 0.5, 5, 900), // 1M effective window (model supports 2M)
			),
			AliasCompaction: role(
				"google", "gemini-2.5-flash-lite",
				caps(16384, 0.3, 3, 90),
				"google", "gemini-2.5-flash",
			),
			AliasVision: role(
				"google", "gemini-2.5-pro",
				caps(16384, 0.5, 5, 300),
				"google", "gemini-2.5-flash",
			),
		},
	}

	// ── CODEX ────────────────────────────────────────────────────────────────
	// OpenAI Codex stack — requires OpenAI OAuth or API key.
	// gpt-5.6-terra for main/vision work, gpt-5.6-sol for steering and long
	// context, gpt-5.6-luna for fast/background ops.
	codex := AgentProfile{
		ID:          "codex",
		Name:        "Codex",
		Description: "OpenAI Codex — gpt-5.6-terra for main work, gpt-5.6-sol for steering/long context, gpt-5.6-luna for fast ops",
		Icon:        "🟢",
		Color:       "#10A37F",
		IsDefault:   false,
		RetryPolicy: &RetryPolicy{
			RotateOnRateLimit: true,
			RotateOnPayment:   true,
			CooldownSeconds:   30,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Roles: map[ModelAlias]RoleConfig{
			AliasMain: role(
				"openai", "gpt-5.6-terra",
				caps(32768, 0.7, 0, 300),
				"openai", "gpt-5.6-luna",
			),
			AliasSteering: role(
				"openai", "gpt-5.6-sol",
				caps(16384, 0.2, 5, 300),
				"openai", "gpt-5.6-terra",
			),
			AliasBackground: role(
				"openai", "gpt-5.6-luna",
				caps(16384, 0.5, 0, 1800),
				"openai", "gpt-5.6-terra",
			),
			AliasSubAgent: role(
				"openai", "gpt-5.6-luna",
				caps(8192, 0.5, 3, 60),
				"openai", "gpt-5.6-terra",
			),
			AliasInference: role(
				"openai", "gpt-5.6-luna",
				caps(8192, 0.7, 10, 60),
			),
			AliasLongContext: role(
				"openai", "gpt-5.6-sol",
				caps(65536, 0.5, 5, 600),
				"openai", "gpt-5.6-terra",
			),
			AliasCompaction: role(
				"openai", "gpt-5.6-luna",
				caps(8192, 0.3, 3, 90),
			),
			AliasVision: role(
				"openai", "gpt-5.6-terra",
				caps(16384, 0.5, 5, 300),
				"openai", "gpt-5.6-luna",
			),
		},
	}

	// ── GLM / Z.AI ────────────────────────────────────────────────────────────
	// Cerebras GLM stack — zai-glm-4.7 has 128K context via Cerebras inference.
	// Fastest freely-available inference. Falls back to llama-3.3-70b on limits.
	glm := AgentProfile{
		ID:          "glm-zai",
		Name:        "GLM / Z.AI",
		Description: "Cerebras GLM — zai-glm-4.7 (128K) across all roles, Llama fallback",
		Icon:        "🔵",
		Color:       "#6366F1",
		IsDefault:   false,
		RetryPolicy: &RetryPolicy{
			RotateOnRateLimit: true,
			RotateOnPayment:   true,
			CooldownSeconds:   10,
		},
		CreatedAt: now,
		UpdatedAt: now,
		Roles: map[ModelAlias]RoleConfig{
			AliasMain: role(
				"cerebras", "zai-glm-4.7",
				caps(32768, 0.7, 0, 180),
				"cerebras", "llama-3.3-70b",
			),
			AliasSteering: role(
				"cerebras", "zai-glm-4.7",
				caps(16384, 0.3, 5, 120),
				"cerebras", "llama-3.3-70b",
			),
			AliasBackground: role(
				"cerebras", "zai-glm-4.7",
				caps(32768, 0.5, 0, 1800),
				"cerebras", "llama-3.3-70b",
			),
			AliasSubAgent: role(
				"cerebras", "zai-glm-4.7",
				caps(8192, 0.5, 3, 30),
				"cerebras", "llama-3.3-70b",
			),
			AliasInference: role(
				"cerebras", "zai-glm-4.7",
				caps(8192, 0.7, 10, 30),
				"cerebras", "llama-3.3-70b",
			),
			AliasLongContext: role(
				"cerebras", "zai-glm-4.7",
				caps(100000, 0.5, 5, 600), // 128K window
				"cerebras", "llama-3.3-70b",
			),
			AliasCompaction: role(
				"cerebras", "llama-3.3-70b",
				caps(8192, 0.3, 3, 60),
				"cerebras", "zai-glm-4.7",
			),
			AliasVision: role(
				"anthropic", "claude-sonnet-4-6",
				caps(16384, 0.5, 5, 300),
			),
		},
	}

	return []AgentProfile{
		balanced,
		quality,
		performance,
		costOpt,
		claudeCode,
		geminiCode,
		codex,
		glm,
	}
}
