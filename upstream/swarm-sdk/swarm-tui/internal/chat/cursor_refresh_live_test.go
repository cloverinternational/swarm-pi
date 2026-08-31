package chat

import (
	"strings"
	"testing"
)

// TestTryRefreshCursorTokenLive exercises the real refresh endpoint using the
// stored refresh token. Skips if no refresh token is present. This proves Bug 1
// is fixed: an expired access token can be refreshed via the refresh token.
func TestTryRefreshCursorTokenLive(t *testing.T) {
	rt := readCursorRefreshToken()
	if rt == "" {
		t.Skip("no cursor refresh token on disk")
	}
	fresh, err := tryRefreshCursorToken()
	if err != nil {
		t.Fatalf("tryRefreshCursorToken failed: %v", err)
	}
	if fresh == "" {
		t.Fatal("got empty access token")
	}
	if isCursorTokenExpired(fresh) {
		t.Fatal("refreshed token is already expired")
	}
	if strings.Count(fresh, ".") != 2 {
		t.Fatalf("refreshed token is not a JWT: %q", fresh[:min(20, len(fresh))])
	}
	t.Logf("refresh OK: fresh token len=%d not-expired", len(fresh))
}
