package provider

import "strings"

const (
	ReasoningEffortAuto    = "auto"
	ReasoningEffortNone    = "none"
	ReasoningEffortMinimal = "minimal"
	ReasoningEffortLow     = "low"
	ReasoningEffortMedium  = "medium"
	ReasoningEffortHigh    = "high"
	ReasoningEffortXHigh   = "xhigh"
	ReasoningEffortMax     = "max"
)

var reasoningEffortAliases = map[string]string{
	"":          ReasoningEffortAuto,
	"auto":      ReasoningEffortAuto,
	"default":   ReasoningEffortAuto,
	"off":       ReasoningEffortNone,
	"none":      ReasoningEffortNone,
	"disable":   ReasoningEffortNone,
	"disabled":  ReasoningEffortNone,
	"min":       ReasoningEffortMinimal,
	"minimal":   ReasoningEffortMinimal,
	"low":       ReasoningEffortLow,
	"med":       ReasoningEffortMedium,
	"medium":    ReasoningEffortMedium,
	"high":      ReasoningEffortHigh,
	"xhigh":     ReasoningEffortXHigh,
	"x-high":    ReasoningEffortXHigh,
	"very-high": ReasoningEffortXHigh,
	"very_high": ReasoningEffortXHigh,
	// codex-rs maps "ultra" to "max" on the wire (core/src/client.rs), so both
	// canonicalize to max here.
	"max":        ReasoningEffortMax,
	"maximum":    ReasoningEffortMax,
	"ultra":      ReasoningEffortMax,
	"ultra-high": ReasoningEffortMax,
	"ultra_high": ReasoningEffortMax,
}

// ReasoningEffortSettings returns the supported settings values in display order.
func ReasoningEffortSettings() []string {
	return []string{
		ReasoningEffortAuto,
		ReasoningEffortNone,
		ReasoningEffortMinimal,
		ReasoningEffortLow,
		ReasoningEffortMedium,
		ReasoningEffortHigh,
		ReasoningEffortXHigh,
		ReasoningEffortMax,
	}
}

// NormalizeReasoningEffortSetting returns a canonical persisted value.
// Unknown values are coerced to "auto".
func NormalizeReasoningEffortSetting(value string) string {
	var normalized string = strings.ToLower(strings.TrimSpace(value))
	if canonical, ok := reasoningEffortAliases[normalized]; ok {
		return canonical
	}
	return ReasoningEffortAuto
}

// NormalizeReasoningEffort returns a canonical request payload value.
// "auto" becomes empty so providers can omit the parameter.
func NormalizeReasoningEffort(value string) string {
	var normalized string = NormalizeReasoningEffortSetting(value)
	if normalized == ReasoningEffortAuto {
		return ""
	}
	return normalized
}
