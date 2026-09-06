// Package deepwiki – compress.go
// Context-budget helpers that use compaction.EstimateTokens (4 chars ≈ 1 token)
// to keep every LLM call well inside the model's context window.
//
// The two main entry points are:
//   - capToTokenBudget   – trims a []string of code chunks to a token ceiling
//   - compressWithLLM    – asks the LLM to shorten a large text block
//
// These are used by the synthesis planner (large JSON inputs) and by Pass 2 of
// the page builder (embedding-retrieved code chunks).
package deepwiki

import (
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
)

// Token budgets (model-agnostic; tuned for 128 k-token models like GLM-5).
// All budgets follow the 80/20 rule: inputs must not exceed 80 % of the model
// context window so the remaining 20 % (≈ 25 600 tokens on a 128 k model) is
// available for output tokens and summarization — running out of output room
// causes truncated or broken responses.
const (
	// modelContextTokens is the assumed context window for the target model.
	// GLM-5 / Gemini-Flash / Claude-3-Haiku all sit at or above 128 k tokens.
	modelContextTokens = 128_000

	// sectionChunkBudgetTokens caps the raw code chunks injected into every
	// single Pass-2 section call.  Entity context adds ~750 tokens on top.
	// 8 k / 128 k ≈ 6 % — leaves ample room for the section output.
	sectionChunkBudgetTokens = 8_000
)

// ---------------------------------------------------------------------------
// Proactive compaction — pure Go, no LLM calls
// ---------------------------------------------------------------------------

// synthesisBudgetLevel defines per-field token hard-caps for one compaction level.
// Every level is sized so that structural+thematic+fileTree+~2 k overhead fits
// comfortably inside modelContextTokens (128 k).
type synthesisBudgetLevel struct {
	structuralTokens int // max tokens for structural JSON
	thematicTokens   int // max tokens for thematic JSON (0 = drop field entirely)
	fileTreeTokens   int // max tokens for file tree
}

// synthesisBudgets lists the hard-cap budgets for each compaction level.
// Budgets are sized so that structural+thematic+fileTree stays ≤ 80 % of
// modelContextTokens, leaving 20 % (≈ 25 600 tokens) for the planner's JSON
// output.  The retry loop starts at level 0 and escalates on timeout.
var synthesisBudgets = []synthesisBudgetLevel{
	// Level 0 — generous: 40k+35k+8k = 83k (64.8 % of 128k) → 35.2 % for output
	{structuralTokens: 40_000, thematicTokens: 35_000, fileTreeTokens: 8_000},
	// Level 1 — medium: 20k+15k+4k = 39k (30.5 % of 128k) → 69.5 % for output
	{structuralTokens: 20_000, thematicTokens: 15_000, fileTreeTokens: 4_000},
	// Level 2 — minimal: 25k+0+3k = 28k (21.9 % of 128k); thematic dropped entirely
	{structuralTokens: 25_000, thematicTokens: 0, fileTreeTokens: 3_000},
}

// compressSynthesisToLevel hard-caps each synthesis input to the budget for
// the given compaction level using pure string truncation — NO LLM calls.
// It is deterministic and safe to call from any goroutine.
//
// Level 0 is the most generous; level 2 is the most aggressive.
// Out-of-range levels are clamped to [0, len(synthesisBudgets)-1].
func compressSynthesisToLevel(structural, thematic, fileTree string, level int) (string, string, string) {
	if level < 0 {
		level = 0
	}
	if level >= len(synthesisBudgets) {
		level = len(synthesisBudgets) - 1
	}
	b := synthesisBudgets[level]

	s := hardCapTokens(structural, b.structuralTokens)
	var th string
	if b.thematicTokens > 0 {
		th = hardCapTokens(thematic, b.thematicTokens)
	}
	ft := hardCapTokens(fileTree, b.fileTreeTokens)
	return s, th, ft
}

// hardCapTokens truncates s so it is at most maxTokens tokens (4 chars/token).
// A short truncation marker is appended when the string is cut.
func hardCapTokens(s string, maxTokens int) string {
	maxChars := maxTokens * 4
	if len(s) <= maxChars {
		return s
	}
	return s[:maxChars] + "\n... [truncated by context budget]"
}

// capToTokenBudget joins parts with sep, dropping whole parts once the
// running total would exceed maxTokens.  It never emits a partial part.
// Uses compaction.EstimateTokens (0.25 tokens/char) as the counter.
func capToTokenBudget(parts []string, sep string, maxTokens int) string {
	var sb strings.Builder
	used := 0
	for i, p := range parts {
		pTokens := compaction.EstimateTokens(p)
		if i > 0 {
			pTokens += compaction.EstimateTokens(sep)
		}
		if used+pTokens > maxTokens {
			break
		}
		if i > 0 {
			sb.WriteString(sep)
		}
		sb.WriteString(p)
		used += pTokens
	}
	return sb.String()
}
