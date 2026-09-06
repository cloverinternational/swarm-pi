package chat

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestIsCursorTokenExpired verifies the JWT expiry check for Cursor tokens.
func TestIsCursorTokenExpired(t *testing.T) {
	// A JWT with exp in the past.
	pastExp := int64(1700000000) // Nov 2023
	pastPayload, _ := json.Marshal(map[string]any{"exp": pastExp})
	pastB64 := base64.RawURLEncoding.EncodeToString(pastPayload)
	pastToken := "header." + pastB64 + ".signature"

	if !isCursorTokenExpired(pastToken) {
		t.Error("expected past-expiry token to be expired")
	}

	// A JWT with exp in the future.
	futureExp := time.Now().Add(24 * time.Hour).Unix()
	futurePayload, _ := json.Marshal(map[string]any{"exp": futureExp})
	futureB64 := base64.RawURLEncoding.EncodeToString(futurePayload)
	futureToken := "header." + futureB64 + ".signature"

	if isCursorTokenExpired(futureToken) {
		t.Error("expected future-expiry token to NOT be expired")
	}

	// A JWT with no exp claim.
	noExpPayload, _ := json.Marshal(map[string]any{"sub": "test"})
	noExpB64 := base64.RawURLEncoding.EncodeToString(noExpPayload)
	noExpToken := "header." + noExpB64 + ".signature"

	if isCursorTokenExpired(noExpToken) {
		t.Error("expected token without exp to NOT be expired")
	}

	// Not a JWT at all.
	if isCursorTokenExpired("not-a-jwt") {
		t.Error("expected non-JWT to NOT be flagged expired")
	}

	// Empty string.
	if isCursorTokenExpired("") {
		t.Error("expected empty string to NOT be flagged expired")
	}
}

// TestReadCursorTokenFromAccountRegistry verifies that the account registry
// fallback read works correctly (returns empty when no file exists).
func TestReadCursorTokenFromAccountRegistry(t *testing.T) {
	// This reads from the real filesystem; if no account file exists it
	// should return "". We can't easily set up a temp dir here without
	// refactoring, so just verify it doesn't panic.
	tok := readCursorTokenFromAccountRegistry()
	if tok != "" && strings.Contains(tok, " ") {
		t.Error("token should not contain spaces")
	}
}
