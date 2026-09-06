package journalredact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// maxRedactedTextBytes is the ADR-006 "Redaction and access" free-text
// bound: 4 KiB per value, applied after secret masking and before any
// truncation marker is appended.
const maxRedactedTextBytes = 4096

// RedactText sanitizes free text per ADR-006 "Redaction and access": valid
// UTF-8, secret-value masking (replaces recognized secrets with the literal
// "[REDACTED]"), then truncated to 4 KiB with a truncation marker plus a
// SHA-256 hex digest of the already-redacted text appended for change
// detection (never for retrieval).
//
// Order is significant and matches ADR-006 exactly: secrets are masked
// before truncation, so a secret spanning the 4 KiB boundary is still fully
// masked rather than half-truncated into a still-partially-raw fragment;
// and the appended digest is computed over the already-redacted text, so it
// never encodes raw secret bytes even indirectly.
func RedactText(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\uFFFD")
	}

	masked := maskSecrets(s)
	// A value produced by a prior RedactText call may exceed the content
	// bound because the truncation marker is appended after the 4 KiB prefix.
	// Preserve that canonical output when masking made no further change so
	// repeated migration/getter sanitization is byte-for-byte idempotent.
	if masked == s && hasTruncationMarker(s) {
		return s
	}
	s = masked

	if len(s) <= maxRedactedTextBytes {
		return s
	}

	digest := DigestOf(s)
	prefix := truncateToValidUTF8Prefix(s, maxRedactedTextBytes)
	return prefix + fmt.Sprintf(" [TRUNCATED sha256:%s]", digest)
}

func hasTruncationMarker(s string) bool {
	const markerPrefix = " [TRUNCATED sha256:"
	const digestBytes = sha256.Size * 2
	markerLen := len(markerPrefix) + digestBytes + 1
	if len(s) <= maxRedactedTextBytes || len(s) < markerLen {
		return false
	}
	// Canonical output keeps a 4096-byte prefix, trimmed by at most three
	// bytes to avoid splitting a UTF-8 rune, followed by this fixed marker.
	prefixLen := len(s) - markerLen
	if prefixLen < maxRedactedTextBytes-3 || prefixLen > maxRedactedTextBytes {
		return false
	}
	marker := s[len(s)-markerLen:]
	if !strings.HasPrefix(marker, markerPrefix) || marker[len(marker)-1] != ']' {
		return false
	}
	_, err := hex.DecodeString(marker[len(markerPrefix) : len(marker)-1])
	return err == nil
}

// truncateToValidUTF8Prefix returns the first n bytes of s, trimmed
// backward as needed so the result never ends mid-rune. s is assumed to
// already be valid UTF-8 (callers normalize via RedactText before calling
// this), so any trailing decode failure within the n-byte window can only
// be an artifact of cutting a multi-byte rune in half, and is resolved by
// dropping that partial rune rather than emitting invalid UTF-8.
func truncateToValidUTF8Prefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	b := s[:n]
	for len(b) > 0 {
		r, size := utf8.DecodeLastRuneInString(b)
		if r != utf8.RuneError || size != 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

// RedactPath reduces an absolute local path to a workspace-relative path when
// workspaceRoot is a non-empty prefix of path; otherwise returns
// "[LOCAL_PATH]". Never returns a raw absolute path outside workspaceRoot.
//
// The prefix check is separator-aware (via filepath.Clean and
// filepath.Rel), so a sibling directory that merely shares workspaceRoot as
// a *string* prefix -- e.g. workspaceRoot "/w" against path "/w-evil/x" --
// is correctly rejected as outside the workspace rather than misreported as
// a relative path.
func RedactPath(path, workspaceRoot string) string {
	if rel, ok := relativeToWorkspace(path, workspaceRoot); ok {
		return rel
	}
	return localPathLiteral
}

// relativeToWorkspace reports the workspace-relative form of path when path
// is genuinely path.Clean(workspaceRoot) itself or a descendant of it, and
// whether that reduction was possible at all.
func relativeToWorkspace(path, workspaceRoot string) (string, bool) {
	if path == "" || workspaceRoot == "" {
		return "", false
	}

	cleanPath := filepath.Clean(path)
	cleanRoot := filepath.Clean(workspaceRoot)

	if cleanPath == cleanRoot {
		return ".", true
	}

	prefix := cleanRoot + string(filepath.Separator)
	if !strings.HasPrefix(cleanPath, prefix) {
		// Not a genuine descendant -- includes the "shares a string prefix
		// but is actually a sibling" case, e.g. cleanRoot "/w" and
		// cleanPath "/w-evil/x", which does not start with "/w/".
		return "", false
	}

	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// RedactFieldValue redacts one journaled task field's value into its
// sanitized string form. fieldName selects any per-field handling (e.g. path
// fields route through RedactPath); unknown fieldName still redacts via
// RedactText as a safe default.
//
// A field name that itself indicates sensitive content (case-insensitively
// containing "password", "secret", "credential", "token", "apikey", or
// "api_key") is masked outright as "[REDACTED]" regardless of its value's
// shape or of any path-like naming, since the field name alone is
// sufficient evidence that the raw value must never be journaled.
func RedactFieldValue(fieldName, rawValue, workspaceRoot string) string {
	if fieldNameIndicatesSecret(fieldName) {
		return redactedLiteral
	}
	if fieldNameIsPathLike(fieldName) {
		return RedactPath(rawValue, workspaceRoot)
	}
	return RedactText(rawValue)
}

// DigestOf returns the SHA-256 hex digest of s, for change-detection use in
// TaskFieldChangedPayload.PreviousValueDigest. It never appears in place of
// a retrievable value.
func DigestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
