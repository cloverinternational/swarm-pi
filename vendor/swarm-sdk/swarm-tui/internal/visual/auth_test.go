package visual

import "testing"

func TestCSRFToken_Unique(t *testing.T) {
	a := NewCSRFToken()
	b := NewCSRFToken()
	if a == "" || b == "" {
		t.Fatal("empty token")
	}
	if a == b {
		t.Error("tokens must be unique")
	}
	if len(a) < 32 {
		t.Errorf("token too short: %d", len(a))
	}
}
