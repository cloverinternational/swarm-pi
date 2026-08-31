package settings

import (
	"encoding/json"
	"testing"
	"time"
)

// TestParseExpiryValue covers the heterogeneous expiry encodings parseExpiryValue
// must tolerate: int/float seconds, float milliseconds, numeric strings, and
// RFC3339 date strings. BUG #3 regression guard.
func TestParseExpiryValue(t *testing.T) {
	rfc := "2030-01-02T03:04:05Z"
	rfcUnix, _ := time.Parse(time.RFC3339, rfc)

	cases := []struct {
		name   string
		in     any
		want   int64
		wantOK bool
	}{
		{"float seconds", float64(1717000000), 1717000000, true},
		{"float millis", float64(1717000000000), 1717000000, true},
		{"numeric string seconds", "1717000000", 1717000000, true},
		{"rfc3339 string", rfc, rfcUnix.Unix(), true},
		{"zero", float64(0), 0, false},
		{"empty string", "", 0, false},
		{"garbage string", "not-a-date", 0, false},
		{"nil", nil, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseExpiryValue(c.in)
			if ok != c.wantOK || got != c.want {
				t.Fatalf("parseExpiryValue(%v) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// TestParseExpiryValueJSONNumber verifies json.Number (produced when decoding
// into `any`) is handled, since that is the real path through isAccountTokenExpired.
func TestParseExpiryValueJSONNumber(t *testing.T) {
	got, ok := parseExpiryValue(json.Number("1717000000"))
	if !ok || got != 1717000000 {
		t.Fatalf("json.Number not parsed: got (%d,%v)", got, ok)
	}
}

// TestIsAccountTokenExpired exercises the full expiry decision across encodings
// using in-memory authAccount structs (no disk I/O).
func TestIsAccountTokenExpired(t *testing.T) {
	now := time.Now().Unix()
	mk := func(field string, v any) authAccount {
		td, _ := json.Marshal(map[string]any{field: v})
		return authAccount{TokenData: td}
	}

	cases := []struct {
		name string
		acct authAccount
		want bool
	}{
		{"int expires_at future", mk("expires_at", now+3600), false},
		{"int expires_at past", mk("expires_at", now-3600), true},
		{"float expiry future", mk("expiry", float64(now+3600)), false},
		{"float expiry past", mk("expiry", float64(now-3600)), true},
		{"ms expires_at future", mk("expires_at", float64((now+3600)*1000)), false},
		{"rfc3339 future", mk("expires_at", time.Unix(now+3600, 0).UTC().Format(time.RFC3339)), false},
		{"rfc3339 past", mk("expires_at", time.Unix(now-3600, 0).UTC().Format(time.RFC3339)), true},
		{"within 5min buffer => expired", mk("expires_at", now+120), true},
		{"no token data", authAccount{}, false},
		{"no expiry field => valid", mk("scope", "x"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAccountTokenExpired(c.acct); got != c.want {
				t.Fatalf("isAccountTokenExpired(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// TestIsAccountTokenExpiredPrefersEarlier verifies that when both expiry and
// expires_at are present, the earlier (more conservative) one decides.
func TestIsAccountTokenExpiredPrefersEarlier(t *testing.T) {
	now := time.Now().Unix()
	td, _ := json.Marshal(map[string]any{
		"expiry":     now + 3600, // valid
		"expires_at": now - 3600, // expired (earlier)
	})
	if !isAccountTokenExpired(authAccount{TokenData: td}) {
		t.Fatal("expected expired when the earlier of two expiry fields is in the past")
	}
}
