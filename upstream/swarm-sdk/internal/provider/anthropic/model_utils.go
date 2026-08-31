package anthropic

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// ModelVersion represents a parsed Claude model version.
type ModelVersion struct {
	Family  string // "opus", "sonnet", "haiku"
	Major   int    // 4, 3, etc.
	Minor   int    // 6, 5, etc.
	RawName string // Original model string
}

// parseModelVersion extracts version information from a model string.
// Examples:
//   - "claude-opus-4-6" -> {Family: "opus", Major: 4, Minor: 6}
//   - "claude-sonnet-4-5-20250929" -> {Family: "sonnet", Major: 4, Minor: 5}
//   - "claude-opus-4-5-20251101" -> {Family: "opus", Major: 4, Minor: 5}
//   - "claude-opus-4-20250514" -> {Family: "opus", Major: 4, Minor: 0} (date stamp, no minor version)
func parseModelVersion(model string) *ModelVersion {
	// Pattern: claude-{family}-{major}[-{minor}][-{date}]. The minor component
	// is optional because modern aliases such as "claude-sonnet-5" are valid.
	re := regexp.MustCompile(`claude-(\w+)-(\d+)(?:-(\d+))?`)
	matches := re.FindStringSubmatch(model)

	if len(matches) < 4 {
		// Unknown model format
		return &ModelVersion{RawName: model}
	}

	major, _ := strconv.Atoi(matches[2])
	minor, _ := strconv.Atoi(matches[3])

	// Detect date stamps (YYYYMMDD format, e.g., 20250514) vs actual version numbers.
	// Date stamps are 8 digits and >= 20200000. Real minor versions are typically 0-99.
	// This fixes a bug where "claude-opus-4-20250514" was incorrectly parsed as
	// having minor=20250514, causing it to incorrectly pass the ">= 6" check.
	if minor >= 20200000 {
		minor = 0 // It's a date stamp, not a minor version
	}

	return &ModelVersion{
		Family:  strings.ToLower(matches[1]),
		Major:   major,
		Minor:   minor,
		RawName: model,
	}
}

// SupportsAdaptiveThinking returns true if the model supports adaptive thinking mode.
// Adaptive thinking is available on Opus and Sonnet 4.6+, plus Mythos-class
// models such as Fable 5 whose IDs do not follow the versioned scheme.
func SupportsAdaptiveThinking(model string) bool {
	if IsMythosClass(model) {
		return true
	}

	version := parseModelVersion(model)
	if version.Family != "opus" && version.Family != "sonnet" {
		return false
	}
	return version.Major > 4 || (version.Major == 4 && version.Minor >= 6)
}

// SupportsMaxEffort returns true if the model supports the "max" effort level.
// Only Opus 4.6+ supports the "max" effort level.
func SupportsMaxEffort(model string) bool {
	if IsMythosClass(model) {
		return true
	}
	version := parseModelVersion(model)
	return version.Family == "opus" && (version.Major > 4 || (version.Major == 4 && version.Minor >= 6))
}

// SupportsExtendedThinking returns true if the model supports extended thinking
// (manual mode with budget_tokens). This is mutually exclusive with adaptive thinking:
// - Models with adaptive thinking (Opus 4.6+) should use adaptive mode, NOT extended thinking
// - Older models (Claude 3+, pre-Opus 4.6) use extended thinking with budget_tokens
func SupportsExtendedThinking(model string) bool {
	// Models that support adaptive thinking should NOT use extended thinking
	if SupportsAdaptiveThinking(model) {
		return false
	}
	// Claude 3+ models (without adaptive thinking) support extended thinking
	version := parseModelVersion(model)
	return version.Major >= 3
}

// GetRecommendedThinkingMode returns the recommended thinking configuration
// based on the model version and user preferences.
// - For Opus 4.6+: Returns "adaptive" (recommended)
// - For older models: Returns "enabled" (manual mode with budget)
func GetRecommendedThinkingMode(model string) string {
	if SupportsAdaptiveThinking(model) {
		return "adaptive"
	}
	return "enabled"
}

// IsOpus47 returns true when the model is Claude Opus 4.7 (any date stamp variant).
// Opus 4.7 enforces a stricter API contract than its predecessors:
//   - Extended-thinking budgets (`thinking.type: "enabled"` + budget_tokens) → HTTP 400
//   - Non-default sampling params (temperature/top_p/top_k)                  → HTTP 400
//   - Assistant-message prefilling                                            → HTTP 400
//
// Use the *Requires* / *Rejects* helpers below to gate enforcement so older
// Claude models and other providers are not affected.
func IsOpus47(model string) bool {
	v := parseModelVersion(model)
	return v.Family == "opus" && v.Major == 4 && v.Minor == 7
}

// IsOpus47OrLater returns true for Claude Opus 4.7 and every later Opus release
// (4.8, 4.9, 5.x, …). Opus 4.7 introduced a stricter API contract that all later
// Opus models inherit (adaptive-only thinking, rejected sampling params, no
// prefill). Gate API-contract enforcement on this (">= 4.7"), NOT on the exact
// IsOpus47 ("== 4.7") — otherwise each new Opus (e.g. claude-opus-4-8) silently
// regresses and 400s with "`temperature` is deprecated for this model".
func IsOpus47OrLater(model string) bool {
	v := parseModelVersion(model)
	if v.Family != "opus" {
		return false
	}
	if v.Major > 4 {
		return true
	}
	return v.Major == 4 && v.Minor >= 7
}

// IsSonnet5OrLater reports whether model is Claude Sonnet 5.x or a later
// Sonnet release. Sonnet 5 adopted the modern adaptive-thinking contract and
// rejects deprecated sampling parameters such as temperature.
func IsSonnet5OrLater(model string) bool {
	v := parseModelVersion(model)
	return v.Family == "sonnet" && v.Major >= 5
}

// IsMythosClass returns true for Mythos-class Claude models (Fable 5, Mythos 5,
// and later). These sit above the Opus tier in capability and inherit the same
// strict API contract as Opus 4.7+ (adaptive-only thinking, rejected sampling
// params, no prefill). Their model IDs (e.g. "claude-fable-5") do NOT follow the
// claude-{family}-{major}-{minor} scheme, so parseModelVersion cannot classify
// them — match by family name prefix instead.
func IsMythosClass(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "claude-fable") || strings.HasPrefix(m, "claude-mythos")
}

// RequiresAdaptiveThinking reports whether the model rejects manual extended-thinking
// configuration (`thinking: {type: "enabled", budget_tokens: N}`) and only accepts
// `thinking: {type: "adaptive"}`. True for Opus 4.7 and later, and Mythos-class models.
func RequiresAdaptiveThinking(model string) bool {
	return IsOpus47OrLater(model) || IsSonnet5OrLater(model) || IsMythosClass(model)
}

// RequiresSummarizedThinking reports whether visible adaptive thinking must be
// requested explicitly. Fable/Mythos and Opus 4.7+ otherwise return no visible
// thinking blocks; Sonnet 5 follows the same modern display contract.
func RequiresSummarizedThinking(model string) bool {
	if IsMythosClass(model) || IsOpus47OrLater(model) {
		return true
	}
	return IsSonnet5OrLater(model)
}

// RejectsSamplingParams reports whether the model returns HTTP 400 when
// temperature, top_p, or top_k are sent. True for Opus 4.7 and later, and
// Mythos-class models (Fable 5 / Mythos 5).
func RejectsSamplingParams(model string) bool {
	return IsOpus47OrLater(model) || IsSonnet5OrLater(model) || IsMythosClass(model)
}

// GetModelContextWindow returns the default context window size in tokens for
// a given model, capped at provider.DefaultContextWindowCap (300K).
//
// Per Anthropic's models overview (2026): the 1M-token context window is GA at
// standard pricing for Claude Opus 4.6 and later, Claude Sonnet 4.6 and later
// (including Sonnet 5), and the Mythos-class models (Fable 5 / Mythos 5). The
// Sonnet 4.5/4 1M beta header was retired. All other Claude 3+ models remain
// at 200K (Opus ≤4.5, Sonnet ≤4.5, Haiku 4.5).
func GetModelContextWindow(model string) int {
	return provider.ClampContextWindow(getPublishedContextWindow(model))
}

// getPublishedContextWindow returns the model's full published context window,
// before the SDK's default cap is applied.
func getPublishedContextWindow(model string) int {
	if IsMythosClass(model) {
		return 1_000_000
	}
	v := parseModelVersion(model)
	switch v.Family {
	case "opus", "sonnet":
		if v.Major > 4 || (v.Major == 4 && v.Minor >= 6) {
			return 1_000_000
		}
	}
	return 200_000
}

// SupportsXHighEffort reports whether the model accepts the new `xhigh` effort
// level in `output_config.effort`. Currently true only for Opus 4.7.
func SupportsXHighEffort(model string) bool {
	return IsOpus47(model)
}
