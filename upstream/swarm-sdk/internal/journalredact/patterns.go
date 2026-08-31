// Package journalredact implements the ADR-006 "Redaction and access"
// secret-masking and path-sanitizing rules used by the execution journal.
//
// This file defines the compiled secret-detection patterns and the
// field-name heuristic used to decide when a journaled task field's value
// must always be masked regardless of its content. All regular expressions
// are compiled once at package init (via package-level var declarations)
// rather than per call, since they are on the hot path of every journal
// write.
package journalredact

import (
	"regexp"
	"strings"
)

// redactedLiteral is the exact literal ADR-006 requires in place of a
// detected secret value. It is never combined with any fragment of the
// original secret.
const redactedLiteral = "[REDACTED]"

// localPathLiteral is the exact literal ADR-006 requires when a path cannot
// safely be reduced to a workspace-relative form.
const localPathLiteral = "[LOCAL_PATH]"

// secretPattern pairs a compiled detector with its replacement template.
//
// Each entry's Regexp either has no capture groups referenced in its
// replacement (in which case the whole match is replaced by
// redactedLiteral) or has capture groups and a replacement template that
// preserves non-secret context (e.g. a header name) while masking only the
// sensitive span.
type secretPattern struct {
	re          *regexp.Regexp
	replacement string
}

var (
	// reOpenAIKey matches OpenAI-shaped API keys, e.g. "sk-...". The
	// quantifier is bounded (16-100 chars after the prefix) rather than
	// open-ended: an unbounded quantifier would keep consuming any
	// subsequent alphanumeric text that happens to abut the key with no
	// delimiter (e.g. inside a longer identifier), over-redacting
	// unrelated adjacent content instead of just the key.
	reOpenAIKey = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,100}\b`)

	// reGithubToken matches GitHub personal-access and app token shapes:
	// ghp_ (personal), gho_ (oauth), ghu_ (user-to-server), ghs_
	// (server-to-server), ghr_ (refresh). Bounded for the same
	// over-redaction reason as reOpenAIKey.
	reGithubToken = regexp.MustCompile(`\bgh[oprsu]_[A-Za-z0-9]{20,100}\b`)

	// reSlackToken matches Slack token shapes: xoxb-, xoxa-, xoxp-, xoxr-,
	// xoxs-. Bounded for the same over-redaction reason as reOpenAIKey.
	reSlackToken = regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,100}\b`)

	// reAWSAccessKeyID matches AWS access key IDs, e.g. "AKIA...".
	reAWSAccessKeyID = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)

	// reStripeKey matches Stripe secret/publishable key shapes, e.g.
	// "sk_live_...", "pk_test_...". Bounded for the same over-redaction
	// reason as reOpenAIKey.
	reStripeKey = regexp.MustCompile(`\b(?:sk|pk)_(?:live|test)_[A-Za-z0-9]{16,100}\b`)

	// reGoogleAPIKey matches Google API key shapes, e.g. "AIza...".
	reGoogleAPIKey = regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)

	// reAssignedSecret matches "name = value" / "name: value" assignments
	// where the name is a common secret-ish keyword (token, api key,
	// secret, access/refresh token, password) followed by a plausible
	// token-shaped value. This is the "generic long-hex/base64 token with a
	// common prefix" detector: the "common prefix" here is the recognizable
	// key name immediately preceding the value, which is far more reliable
	// than trying to recognize arbitrary long hex/base64 runs out of
	// context (which would false-positive on ordinary identifiers). Group 1
	// is the key name and separator (and optional opening quote), preserved
	// in the replacement; group 2 is the value, masked; group 3 is an
	// optional closing quote, preserved.
	reAssignedSecret = regexp.MustCompile(`(?i)(\b(?:token|api[_-]?key|secret|access[_-]?token|refresh[_-]?token|password)\s*[:=]\s*['"]?)([A-Za-z0-9+/_.=-]{8,256})(['"]?)`)

	// reAuthorizationBearer matches an "Authorization: Bearer <token>"
	// header value. Group 1 (the header name and scheme) is preserved;
	// the token itself is masked.
	reAuthorizationBearer = regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)(\S+)`)

	// reAuthorizationBasic matches an "Authorization: Basic <base64>"
	// header value. Group 1 is preserved; the credential is masked.
	reAuthorizationBasic = regexp.MustCompile(`(?i)(Authorization:\s*Basic\s+)(\S+)`)

	// reCookie matches a "Cookie:" or "Set-Cookie:" header line. Group 1
	// (the header name) is preserved; the entire cookie value is masked,
	// since cookie values are session/authentication material by
	// definition and must never be partially echoed.
	reCookie = regexp.MustCompile(`(?i)((?:Set-)?Cookie:\s*)([^\r\n]+)`)

	// rePEMPrivateKey matches a full PEM private-key block, from its BEGIN
	// marker to its matching END marker inclusive. The entire block,
	// including any base64 body, is masked as a single unit.
	rePEMPrivateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)

	// reURLUserinfo matches a URL of the form "scheme://user:pass@host..."
	// and masks the whole URL, since ADR-006 requires that "endpoint URLs
	// containing credentials" never be retained, not merely the
	// credential substring.
	reURLUserinfo = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s/:@]+:[^\s/@]+@[^\s]*`)
)

// secretPatternTable lists every compiled detector in application order,
// paired with its replacement template. Full-match detectors (no capture
// groups referenced) use redactedLiteral directly; partial-match detectors
// use a template that preserves non-secret context via $1/$3-style
// back-references.
var secretPatternTable = []secretPattern{
	{rePEMPrivateKey, redactedLiteral},
	{reURLUserinfo, redactedLiteral},
	{reAuthorizationBearer, "${1}" + redactedLiteral},
	{reAuthorizationBasic, "${1}" + redactedLiteral},
	{reCookie, "${1}" + redactedLiteral},
	{reAssignedSecret, "${1}" + redactedLiteral + "${3}"},
	{reOpenAIKey, redactedLiteral},
	{reGithubToken, redactedLiteral},
	{reSlackToken, redactedLiteral},
	{reAWSAccessKeyID, redactedLiteral},
	{reStripeKey, redactedLiteral},
	{reGoogleAPIKey, redactedLiteral},
}

// maskSecrets applies every compiled secret detector in secretPatternTable
// to s in order, replacing each recognized secret span with redactedLiteral
// (preserving any non-secret context a pattern's template retains, such as
// a header name). It is the shared helper redact.go uses for RedactText.
func maskSecrets(s string) string {
	for _, p := range secretPatternTable {
		s = p.re.ReplaceAllString(s, p.replacement)
	}
	return s
}

// secretFieldKeywords are the case-insensitive substrings that, when found
// in a journaled field/key name, mean that field's value is always masked
// outright (never merely pattern-scanned), per ADR-006 "values whose field
// names indicate passwords, secrets, or credentials". "apikey" and
// "api_key" are both listed because they are not substrings of one
// another.
var secretFieldKeywords = []string{
	"password",
	"secret",
	"credential",
	"token",
	"apikey",
	"api_key",
}

// fieldNameIndicatesSecret reports whether fieldName case-insensitively
// contains any of secretFieldKeywords, meaning its associated value must be
// masked outright regardless of content.
func fieldNameIndicatesSecret(fieldName string) bool {
	lower := strings.ToLower(fieldName)
	for _, kw := range secretFieldKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// pathLikeFieldSubstrings are case-insensitive substrings that, when found
// in a journaled field/key name, mean that field's value is a filesystem
// path and must be routed through RedactPath rather than RedactText.
var pathLikeFieldSubstrings = []string{
	"path",
	"workspace",
	"directory",
	"cwd",
	"dir",
}

// fieldNameIsPathLike reports whether fieldName case-insensitively contains
// any of pathLikeFieldSubstrings.
func fieldNameIsPathLike(fieldName string) bool {
	lower := strings.ToLower(fieldName)
	for _, kw := range pathLikeFieldSubstrings {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
