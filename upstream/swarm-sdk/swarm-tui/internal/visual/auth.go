package visual

import (
	"crypto/rand"
	"encoding/hex"
)

// NewCSRFToken returns a fresh 32-byte hex-encoded random token.
func NewCSRFToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}
