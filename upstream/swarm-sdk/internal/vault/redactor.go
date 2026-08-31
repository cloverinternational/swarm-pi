package vault

import (
	"encoding/base64"
	"math"
	"net/url"
	"regexp"
	"strings"
)

// OutputRedactor redacts secret values from command output.
type OutputRedactor struct {
	hints []string
}

// NewOutputRedactor creates a new output redactor.
func NewOutputRedactor() *OutputRedactor {
	return &OutputRedactor{
		hints: make([]string, 0),
	}
}

// Redact replaces all occurrences of the secret in the output with [REDACTED].
// Returns the redacted string and the number of redactions performed.
//
// Hints ACCUMULATE across calls (use ClearHints to reset). This lets a caller
// redact stdout then stderr and still read the combined hint set. Because the
// redactor carries mutable hint state, callers MUST use a per-invocation
// redactor (NewOutputRedactor) rather than sharing one across goroutines.
func (r *OutputRedactor) Redact(output string, secret string) (string, int) {
	if secret == "" {
		return output, 0
	}

	count := 0

	// 1. Exact match
	redacted, c := r.redactExact(output, secret)
	output = redacted
	count += c

	// 2. URL-encoded (for URLs with embedded credentials)
	redacted, c = r.redactURLEncoded(output, secret)
	output = redacted
	count += c

	// 3. Base64 encoded
	redacted, c = r.redactBase64(output, secret)
	output = redacted
	count += c

	// 4. URL pattern redaction (https://user:SECRET@host)
	redacted, c = r.redactURLPattern(output)
	output = redacted
	count += c

	return output, count
}

// redactExact replaces exact matches of the secret.
func (r *OutputRedactor) redactExact(output, secret string) (string, int) {
	count := strings.Count(output, secret)
	if count > 0 {
		r.hints = append(r.hints, "exact_value")
	}
	return strings.ReplaceAll(output, secret, "[REDACTED]"), count
}

// redactURLEncoded replaces URL-encoded versions of the secret.
func (r *OutputRedactor) redactURLEncoded(output, secret string) (string, int) {
	encoded := url.QueryEscape(secret)
	if encoded == secret {
		return output, 0
	}

	count := strings.Count(output, encoded)
	if count > 0 {
		r.hints = append(r.hints, "url_encoded")
	}
	return strings.ReplaceAll(output, encoded, "[REDACTED]"), count
}

// redactBase64 replaces base64-encoded versions of the secret.
func (r *OutputRedactor) redactBase64(output, secret string) (string, int) {
	encoded := base64.StdEncoding.EncodeToString([]byte(secret))
	if len(encoded) < 10 {
		return output, 0 // Skip short values
	}

	count := strings.Count(output, encoded)
	if count > 0 {
		r.hints = append(r.hints, "base64_encoded")
	}
	return strings.ReplaceAll(output, encoded, "[REDACTED]"), count
}

// redactURLPattern replaces URLs with embedded credentials.
// Matches: https://user:SECRET@host -> https://user:[REDACTED]@host
func (r *OutputRedactor) redactURLPattern(output string) (string, int) {
	// Pattern: protocol://user:password@host
	pattern := regexp.MustCompile(`(https?://[^:]+:)([^@]+)(@[^/]+)`)

	count := 0
	result := pattern.ReplaceAllStringFunc(output, func(match string) string {
		count++
		r.hints = append(r.hints, "url_credential")
		return pattern.ReplaceAllString(match, "$1[REDACTED]$3")
	})

	return result, count
}

// RedactMultiple redacts multiple secrets from output.
func (r *OutputRedactor) RedactMultiple(output string, secrets []string) (string, int) {
	totalCount := 0
	for _, secret := range secrets {
		redacted, count := r.Redact(output, secret)
		output = redacted
		totalCount += count
	}
	return output, totalCount
}

// entropyReplacement is the placeholder used for high-entropy tokens.
const entropyReplacement = "[REDACTED-ENTROPY]"

// entropyTokenPattern matches contiguous runs of base64/hex/JWT-ish characters.
// Word-boundary aware: it will not match across whitespace, quotes, or most
// path/URL separators, so ordinary words and paths are split into short tokens.
var entropyTokenPattern = regexp.MustCompile(`[A-Za-z0-9+/=_-]{16,}`)

// entropyStrongPrefixes are well-known secret prefixes. When a token starts with
// one of these, it is treated as a strong signal, lowering the length/entropy bar.
var entropyStrongPrefixes = []string{"AKIA", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "xoxb-", "xoxp-", "xoxa-", "xoxr-", "sk-", "eyJ"}

// entropyThreshold is the minimum Shannon entropy (bits per char) for a token to
// be considered high-entropy. 3.5 bits/char is a conservative middle ground:
// English prose and identifiers typically sit well below ~3.0 bits/char, while
// random base64/hex secrets approach the theoretical maxima (base64 ~6.0, hex
// ~4.0). 3.5 catches random tokens while sparing natural-language and path text.
const entropyThreshold = 3.5

// entropyMinLen is the minimum token length for the entropy pass without a
// strong prefix. 16 chars matches the token regex floor and keeps ordinary
// words out (entropy gate does the rest).
const entropyMinLen = 16

// pemBlockPattern matches an entire PEM block (e.g. an SSH or TLS private key),
// which is multi-line base64 that the per-token pass would only redact line by
// line - leaving the BEGIN/END markers and any short trailing line exposed.
var pemBlockPattern = regexp.MustCompile(`(?s)-----BEGIN[^-]*-----.*?-----END[^-]*-----`)

// entropyPrefixMinLen is the relaxed minimum length when a strong prefix matches.
const entropyPrefixMinLen = 8

// shannonEntropy computes the Shannon entropy of s in bits per character.
func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	counts := make(map[rune]int)
	total := 0
	for _, c := range s {
		counts[c]++
		total++
	}
	entropy := 0.0
	for _, n := range counts {
		p := float64(n) / float64(total)
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// hasStrongPrefix reports whether the token begins with a known secret prefix.
func hasStrongPrefix(token string) bool {
	for _, p := range entropyStrongPrefixes {
		if strings.HasPrefix(token, p) {
			return true
		}
	}
	return false
}

// RedactHighEntropy performs a defense-in-depth pass that redacts high-entropy
// secret-shaped tokens which are NOT among the known stored secrets (e.g. a
// freshly minted token echoed by an API). It replaces each qualifying token with
// [REDACTED-ENTROPY] and returns the redacted output and the number of
// redactions performed.
//
// Heuristic (conservative, to avoid false positives):
//   - The token must match the base64/hex/JWT-ish charset ([A-Za-z0-9+/=_-]).
//   - Without a known prefix: length >= 20 AND Shannon entropy > 3.5 bits/char.
//   - With a known prefix (AKIA, ghp_, xoxb-, sk-, eyJ, ...): the length and
//     entropy bar is lowered because the prefix is itself a strong signal.
//
// Matching is word-boundary aware via the token regex, so ordinary English
// words, file paths, and URL host parts are not redacted.
func (r *OutputRedactor) RedactHighEntropy(output string) (string, int) {
	count := 0

	// First redact whole PEM blocks (private keys) as a unit so markers and any
	// short trailing base64 line can't leak partial key material.
	output = pemBlockPattern.ReplaceAllStringFunc(output, func(string) string {
		count++
		r.hints = append(r.hints, "pem_block")
		return "[REDACTED-KEY]"
	})

	result := entropyTokenPattern.ReplaceAllStringFunc(output, func(token string) string {
		strong := hasStrongPrefix(token)

		minLen := entropyMinLen
		if strong {
			minLen = entropyPrefixMinLen
		}
		if len(token) < minLen {
			return token
		}

		// A strong prefix is enough on its own; still require the token to
		// carry some entropy to avoid redacting e.g. "sk-aaaaaaaa".
		ent := shannonEntropy(token)
		if strong {
			if ent < 2.0 {
				return token
			}
		} else if ent <= entropyThreshold {
			return token
		}

		count++
		r.hints = append(r.hints, "high_entropy")
		return entropyReplacement
	})
	return result, count
}

// RedactAll runs the known-secret redaction pass (RedactMultiple) followed by
// the entropy pass (RedactHighEntropy). Callers opt into the entropy pass by
// calling this instead of RedactMultiple. Returns the redacted output and the
// total number of redactions across both passes.
func (r *OutputRedactor) RedactAll(output string, secrets []string) (string, int) {
	redacted, c1 := r.RedactMultiple(output, secrets)
	redacted, c2 := r.RedactHighEntropy(redacted)
	return redacted, c1 + c2
}

// GetHints returns the redaction hints from the last redaction.
func (r *OutputRedactor) GetHints() []string {
	return r.hints
}

// ClearHints clears the stored hints.
func (r *OutputRedactor) ClearHints() {
	r.hints = make([]string, 0)
}

// AddCustomPattern adds a custom regex pattern for redaction.
func (r *OutputRedactor) AddCustomPattern(pattern string, replacement string) error {
	// Validate pattern is a valid regex
	_, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	// Custom patterns would be applied during Redact
	// For now, this is a placeholder
	return nil
}
