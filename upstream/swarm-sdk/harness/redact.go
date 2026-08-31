package harness

import (
	"crypto/sha256"
	"encoding/hex"
)

// redactedPlaceholder is the single sentinel used everywhere a secret would
// otherwise appear. No secret value is ever rendered.
const redactedPlaceholder = "[REDACTED]"

// hashBytes returns "sha256:<hex>" for content. It is used to describe prompt
// and other content by hash without exposing the content itself.
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// hashString is the string convenience form of hashBytes.
func hashString(s string) string {
	return hashBytes([]byte(s))
}
