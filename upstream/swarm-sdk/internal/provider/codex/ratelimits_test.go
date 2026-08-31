package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// headersFromProbe mirrors the exact header set captured from a live
// chatgpt.com/backend-api/codex/responses 200 on 2026-07-15 (Pro plan).
func headersFromProbe() http.Header {
	h := http.Header{}
	h.Set("x-codex-active-limit", "premium")
	h.Set("x-codex-plan-type", "pro")
	h.Set("x-codex-primary-used-percent", "37.5")
	h.Set("x-codex-secondary-used-percent", "12")
	h.Set("x-codex-primary-window-minutes", "10080")
	h.Set("x-codex-secondary-window-minutes", "300")
	h.Set("x-codex-primary-reset-after-seconds", "604776")
	h.Set("x-codex-secondary-reset-after-seconds", "9000")
	h.Set("x-codex-primary-reset-at", "1784770613")
	h.Set("x-codex-secondary-reset-at", "")
	return h
}

func TestParseRateLimitHeaders(t *testing.T) {
	snap := ParseRateLimitHeaders(headersFromProbe())
	if snap == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if snap.PlanType != "pro" || snap.ActiveLimit != "premium" {
		t.Errorf("plan/active = %q/%q, want pro/premium", snap.PlanType, snap.ActiveLimit)
	}
	if snap.Primary == nil || snap.Secondary == nil {
		t.Fatal("expected both windows parsed")
	}
	if snap.Primary.UsedPercent != 37.5 || snap.Primary.WindowMinutes != 10080 {
		t.Errorf("primary = %+v", snap.Primary)
	}
	if snap.Primary.ResetAfterSeconds != 604776 || snap.Primary.ResetAt != 1784770613 {
		t.Errorf("primary resets = %+v", snap.Primary)
	}
	if snap.Secondary.UsedPercent != 12 || snap.Secondary.WindowMinutes != 300 {
		t.Errorf("secondary = %+v", snap.Secondary)
	}
	// Empty reset-at header must parse as 0, not error out the whole snapshot.
	if snap.Secondary.ResetAt != 0 {
		t.Errorf("secondary ResetAt = %d, want 0", snap.Secondary.ResetAt)
	}
	if snap.CapturedAt.IsZero() {
		t.Error("CapturedAt not stamped")
	}
}

func TestParseRateLimitHeadersAbsent(t *testing.T) {
	if snap := ParseRateLimitHeaders(http.Header{}); snap != nil {
		t.Errorf("expected nil for headerless response, got %+v", snap)
	}
	// Garbage percent must not produce a phantom window.
	h := http.Header{}
	h.Set("x-codex-primary-used-percent", "not-a-number")
	if snap := ParseRateLimitHeaders(h); snap != nil {
		t.Errorf("expected nil for unparseable headers, got %+v", snap)
	}
}

func TestRateLimitSnapshotStoreRoundTrip(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	snap := ParseRateLimitHeaders(headersFromProbe())
	snap.AccountID = "acct_A"
	if err := SaveRateLimitSnapshot(*snap); err != nil {
		t.Fatalf("save A: %v", err)
	}
	b := *snap
	b.AccountID = "acct_B"
	b.Primary = &RateLimitWindow{UsedPercent: 99, WindowMinutes: 10080}
	if err := SaveRateLimitSnapshot(b); err != nil {
		t.Fatalf("save B: %v", err)
	}

	got, err := LoadRateLimitSnapshots()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d snapshots, want 2", len(got))
	}
	if got["acct_A"].Primary.UsedPercent != 37.5 {
		t.Errorf("acct_A primary = %+v", got["acct_A"].Primary)
	}
	if got["acct_B"].Primary.UsedPercent != 99 {
		t.Errorf("acct_B primary = %+v", got["acct_B"].Primary)
	}
	// Overwrite same account updates in place.
	b.Primary.UsedPercent = 50
	if err := SaveRateLimitSnapshot(b); err != nil {
		t.Fatal(err)
	}
	got, _ = LoadRateLimitSnapshots()
	if len(got) != 2 || got["acct_B"].Primary.UsedPercent != 50 {
		t.Errorf("after overwrite: %+v", got["acct_B"])
	}
}

func TestLoadRateLimitSnapshotsMissingFile(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	got, err := LoadRateLimitSnapshots()
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %+v", got)
	}
}

func TestWindowLabel(t *testing.T) {
	cases := map[int]string{300: "5h", 10080: "Weekly", 60: "1h", 0: "", 90: "90m"}
	for minutes, want := range cases {
		if got := (RateLimitWindow{WindowMinutes: minutes}).Label(); got != want {
			t.Errorf("Label(%d) = %q, want %q", minutes, got, want)
		}
	}
}

func TestWindowResetTime(t *testing.T) {
	w := RateLimitWindow{ResetAt: 1784770613}
	if got := w.ResetTime(); !got.Equal(time.Unix(1784770613, 0)) {
		t.Errorf("ResetTime = %v", got)
	}
	if !(RateLimitWindow{}).ResetTime().IsZero() {
		t.Error("zero ResetAt must yield zero time")
	}
}

func TestFetchRateLimitSnapshot(t *testing.T) {
	var gotAuth, gotAccount, gotOriginator, gotVersion, gotUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("ChatGPT-Account-Id")
		gotOriginator = r.Header.Get("originator")
		gotVersion = r.Header.Get("version")
		gotUserAgent = r.Header.Get("User-Agent")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan_type": "pro",
			"rate_limit": map[string]any{
				"primary_window": map[string]any{
					"used_percent":         23.5,
					"limit_window_seconds": 18000,
					"reset_after_seconds":  900,
					"reset_at":             1784770613,
				},
				"secondary_window": map[string]any{
					"used_percent":         41.0,
					"limit_window_seconds": 604800,
					"reset_at":             1785000000,
				},
			},
		})
	}))
	defer server.Close()

	snapshot, err := fetchRateLimitSnapshot(context.Background(), server.Client(), server.URL, "token-123", "acct-123")
	if err != nil {
		t.Fatalf("fetchRateLimitSnapshot: %v", err)
	}
	if gotAuth != "Bearer token-123" || gotAccount != "acct-123" {
		t.Fatalf("request headers = auth %q account %q", gotAuth, gotAccount)
	}
	if gotOriginator != "swarmos" || gotVersion != codexClientVersion || gotUserAgent != codexUserAgent() {
		t.Fatalf("client headers = originator %q version %q user-agent %q", gotOriginator, gotVersion, gotUserAgent)
	}
	if snapshot.AccountID != "acct-123" || snapshot.PlanType != "pro" {
		t.Fatalf("snapshot identity = %+v", snapshot)
	}
	if snapshot.Primary == nil || snapshot.Primary.WindowMinutes != 300 || snapshot.Primary.UsedPercent != 23.5 {
		t.Fatalf("primary window = %+v", snapshot.Primary)
	}
	if snapshot.Secondary == nil || snapshot.Secondary.WindowMinutes != 10080 || snapshot.Secondary.UsedPercent != 41 {
		t.Fatalf("secondary window = %+v", snapshot.Secondary)
	}
}

func TestFetchRateLimitSnapshotRejectsEmptyUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"plan_type":"plus","rate_limit":{"primary_window":{}}}`))
	}))
	defer server.Close()

	if _, err := fetchRateLimitSnapshot(context.Background(), server.Client(), server.URL, "token", "acct"); err == nil {
		t.Fatal("expected empty usage response to fail")
	}
}

func TestSaveRateLimitSnapshotDoesNotOverwriteNewerData(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	newer := RateLimitSnapshot{
		CapturedAt: time.Now(),
		AccountID:  "acct",
		Primary:    &RateLimitWindow{UsedPercent: 20},
	}
	older := newer
	older.CapturedAt = newer.CapturedAt.Add(-time.Minute)
	older.Primary = &RateLimitWindow{UsedPercent: 90}

	if err := SaveRateLimitSnapshot(newer); err != nil {
		t.Fatal(err)
	}
	if err := SaveRateLimitSnapshot(older); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRateLimitSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if got["acct"].Primary.UsedPercent != 20 {
		t.Fatalf("older generation overwrote newer snapshot: %+v", got["acct"])
	}
}
