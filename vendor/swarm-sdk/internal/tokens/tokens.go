// Package tokens is the canonical place to measure and bound LLM-facing text
// in the Swarm SDK. Everything we send to or receive from a model should be
// budgeted in tokens, not bytes or characters — provider context limits are
// in tokens, so byte caps drift (200KB of dense Go source is ~55K tokens; the
// same 200KB of minified JSON is ~40K; of prose, ~45K). Using a shared helper
// keeps every site honest and lets us swap the heuristic for a real tokenizer
// later without chasing down KB constants.
package tokens

import (
	"unicode/utf8"
)

// charsPerToken is the byte-to-token ratio we assume across providers. 4 is
// Anthropic's documented guideline and lines up with OpenAI's cl100k within
// a small error band for English + code. It over-estimates on dense code and
// under-estimates on whitespace-heavy text; callers that need headroom
// should budget ~15% below the provider's hard limit.
const charsPerToken = 4

// Estimate returns an approximate token count for s. It uses byte length
// rather than rune count because that matches how most tokenizers behave on
// ASCII-dominant text, and it's what the SDK's compaction package has always
// used. Returns 0 for the empty string.
func Estimate(s string) int {
	if s == "" {
		return 0
	}
	n := len(s) / charsPerToken
	if n == 0 {
		return 1
	}
	return n
}

// Truncate returns s clipped so its estimated token count is at most
// maxTokens. Returns s unchanged if it already fits. Truncation happens at a
// UTF-8 boundary so the returned string is always valid UTF-8.
//
// Pass maxTokens <= 0 to get an empty string back — that's the only way a
// caller can express "no budget."
func Truncate(s string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	if Estimate(s) <= maxTokens {
		return s
	}
	cutoff := maxTokens * charsPerToken
	if cutoff >= len(s) {
		return s
	}
	// Step back to a rune boundary so we don't leave a dangling multi-byte
	// sequence. UTF-8 continuation bytes are 10xxxxxx.
	for cutoff > 0 && !utf8.RuneStart(s[cutoff]) {
		cutoff--
	}
	return s[:cutoff]
}

// TruncateWithMarker behaves like Truncate but appends marker to any clipped
// output so downstream consumers (and the model itself) can see that the
// input was shortened. The marker's own token cost is subtracted from the
// budget before truncation, so the combined result still fits.
//
// If the marker alone would exhaust the budget, the budget wins — the marker
// is dropped and the empty string is returned. This trades faithful
// reporting for a guaranteed fit.
func TruncateWithMarker(s string, maxTokens int, marker string) string {
	if maxTokens <= 0 {
		return ""
	}
	if Estimate(s) <= maxTokens {
		return s
	}
	markerTokens := Estimate(marker)
	if markerTokens >= maxTokens {
		return ""
	}
	return Truncate(s, maxTokens-markerTokens) + marker
}
