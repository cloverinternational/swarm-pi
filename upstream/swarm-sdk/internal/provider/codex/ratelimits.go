package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// Rate-limit / usage telemetry for the ChatGPT-subscription (Codex) backend.
//
// Every POST /responses reply carries an x-codex-* header suite describing the
// account's subscription rate limits (verified live 2026-07-15):
//
//	x-codex-active-limit: premium          x-codex-plan-type: pro
//	x-codex-primary-used-percent: 0        x-codex-secondary-used-percent: 0
//	x-codex-primary-window-minutes: 10080  x-codex-secondary-window-minutes: 0
//	x-codex-primary-reset-after-seconds: 604776
//	x-codex-primary-reset-at: 1784770613   (unix seconds)
//
// ChatGPT OAuth accounts also expose the same data through the read-only
// /backend-api/wham/usage endpoint. Consumers should prefer that active lookup
// and retain response-header snapshots as an offline/stale fallback.

const rateLimitUsageURL = "https://chatgpt.com/backend-api/wham/usage"

var rateLimitStoreMu sync.Mutex

// RateLimitWindow describes one rate-limit window (primary or secondary).
type RateLimitWindow struct {
	// UsedPercent is how much of the window's quota is consumed (0-100).
	UsedPercent float64 `json:"used_percent"`
	// WindowMinutes is the window length (300 = 5h, 10080 = weekly).
	WindowMinutes int `json:"window_minutes"`
	// ResetAfterSeconds is seconds until the window resets, at capture time.
	ResetAfterSeconds int64 `json:"reset_after_seconds"`
	// ResetAt is the unix-seconds timestamp when the window resets (0 = unknown).
	ResetAt int64 `json:"reset_at"`
}

// Label renders a human name for the window length ("5h", "Weekly", "90m").
func (w RateLimitWindow) Label() string {
	switch {
	case w.WindowMinutes <= 0:
		return ""
	case w.WindowMinutes == 10080:
		return "Weekly"
	case w.WindowMinutes%60 == 0:
		return fmt.Sprintf("%dh", w.WindowMinutes/60)
	default:
		return fmt.Sprintf("%dm", w.WindowMinutes)
	}
}

// ResetTime returns the reset moment, or the zero time when unknown.
func (w RateLimitWindow) ResetTime() time.Time {
	if w.ResetAt <= 0 {
		return time.Time{}
	}
	return time.Unix(w.ResetAt, 0)
}

// RateLimitSnapshot is the usage state of one ChatGPT account at CapturedAt.
type RateLimitSnapshot struct {
	CapturedAt time.Time `json:"captured_at"`
	// AccountID is the ChatGPT account the snapshot belongs to.
	AccountID string `json:"account_id"`
	// PlanType is the subscription plan (x-codex-plan-type: "pro", "plus", ...).
	PlanType string `json:"plan_type"`
	// ActiveLimit names the limit family currently applied (x-codex-active-limit).
	ActiveLimit string `json:"active_limit"`
	// Primary is the main window (weekly on Pro); nil when the header was absent.
	Primary *RateLimitWindow `json:"primary,omitempty"`
	// Secondary is the shorter window (5h) where the plan has one.
	Secondary *RateLimitWindow `json:"secondary,omitempty"`
}

// WorstWindow returns the window with the highest utilization, or nil.
func (s RateLimitSnapshot) WorstWindow() *RateLimitWindow {
	if s.Primary == nil {
		return s.Secondary
	}
	if s.Secondary == nil || s.Primary.UsedPercent >= s.Secondary.UsedPercent {
		return s.Primary
	}
	return s.Secondary
}

// ParseRateLimitHeaders extracts a usage snapshot from a codex backend
// response. Returns nil when the response carries no parseable usage headers
// (e.g. a non-codex proxy error page). Parsing is best-effort per field:
// blank/absent numeric headers become zero values, but a present-yet-garbled
// used-percent invalidates its window rather than reporting a false 0%.
func ParseRateLimitHeaders(h http.Header) *RateLimitSnapshot {
	primary := parseWindow(h, "x-codex-primary")
	secondary := parseWindow(h, "x-codex-secondary")
	if primary == nil && secondary == nil {
		return nil
	}
	return &RateLimitSnapshot{
		CapturedAt:  time.Now(),
		PlanType:    strings.TrimSpace(h.Get("x-codex-plan-type")),
		ActiveLimit: strings.TrimSpace(h.Get("x-codex-active-limit")),
		Primary:     primary,
		Secondary:   secondary,
	}
}

// parseWindow reads one window's header family. The used-percent header is
// the sentinel: absent or unparseable → no window.
func parseWindow(h http.Header, prefix string) *RateLimitWindow {
	raw := strings.TrimSpace(h.Get(prefix + "-used-percent"))
	if raw == "" {
		return nil
	}
	used, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &RateLimitWindow{
		UsedPercent:       used,
		WindowMinutes:     int(headerInt(h, prefix+"-window-minutes")),
		ResetAfterSeconds: headerInt(h, prefix+"-reset-after-seconds"),
		ResetAt:           headerInt(h, prefix+"-reset-at"),
	}
}

func headerInt(h http.Header, key string) int64 {
	raw := strings.TrimSpace(h.Get(key))
	if raw == "" {
		return 0
	}
	// Some values arrive as floats (e.g. "604776.0"); accept both.
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return int64(f)
	}
	return 0
}

type rateLimitUsageWindow struct {
	UsedPercent       *float64 `json:"used_percent"`
	LimitWindowSecs   int64    `json:"limit_window_seconds"`
	ResetAfterSeconds int64    `json:"reset_after_seconds"`
	ResetAt           int64    `json:"reset_at"`
}

type rateLimitUsageResponse struct {
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		PrimaryWindow   *rateLimitUsageWindow `json:"primary_window"`
		SecondaryWindow *rateLimitUsageWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

func windowFromUsageResponse(w *rateLimitUsageWindow) *RateLimitWindow {
	if w == nil || w.UsedPercent == nil {
		return nil
	}
	windowMinutes := int((w.LimitWindowSecs + 59) / 60)
	return &RateLimitWindow{
		UsedPercent:       *w.UsedPercent,
		WindowMinutes:     windowMinutes,
		ResetAfterSeconds: w.ResetAfterSeconds,
		ResetAt:           w.ResetAt,
	}
}

// FetchRateLimitSnapshot reads current subscription usage without consuming a
// model request. accountID is required because ChatGPT OAuth tokens can belong
// to more than one workspace/account.
func FetchRateLimitSnapshot(ctx context.Context, accessToken, accountID string) (*RateLimitSnapshot, error) {
	return fetchRateLimitSnapshot(ctx, http.DefaultClient, rateLimitUsageURL, accessToken, accountID)
}

func fetchRateLimitSnapshot(ctx context.Context, client *http.Client, endpoint, accessToken, accountID string) (*RateLimitSnapshot, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("missing access token")
	}
	if strings.TrimSpace(accountID) == "" {
		return nil, fmt.Errorf("missing ChatGPT account id")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build usage request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("ChatGPT-Account-Id", accountID)
	req.Header.Set("originator", "swarmos")
	req.Header.Set("version", codexClientVersion)
	req.Header.Set("User-Agent", codexUserAgent())

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch usage: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage endpoint returned HTTP %d", resp.StatusCode)
	}

	var payload rateLimitUsageResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("parse usage response: %w", err)
	}
	snapshot := &RateLimitSnapshot{
		CapturedAt: time.Now(),
		AccountID:  accountID,
		PlanType:   strings.TrimSpace(payload.PlanType),
		Primary:    windowFromUsageResponse(payload.RateLimit.PrimaryWindow),
		Secondary:  windowFromUsageResponse(payload.RateLimit.SecondaryWindow),
	}
	if snapshot.Primary == nil && snapshot.Secondary == nil {
		return nil, fmt.Errorf("usage response contained no rate-limit windows")
	}
	return snapshot, nil
}

// ── per-account snapshot store ────────────────────────────────────────────────

// rateLimitStorePath resolves ~/.swarm/codex_rate_limits.json.
func rateLimitStorePath() (string, error) {
	return paths.In("codex_rate_limits.json"), nil
}

// SaveRateLimitSnapshot upserts the snapshot for its AccountID into
// ~/.swarm/codex_rate_limits.json (atomic write under a cross-process lock).
// Snapshots are advisory telemetry: callers should treat errors as non-fatal.
func SaveRateLimitSnapshot(snap RateLimitSnapshot) error {
	if snap.AccountID == "" {
		return fmt.Errorf("snapshot missing account id")
	}
	rateLimitStoreMu.Lock()
	defer rateLimitStoreMu.Unlock()

	path, err := rateLimitStorePath()
	if err != nil {
		return err
	}
	// Lock the whole read-modify-write so concurrent turns can't drop updates.
	return atomicfile.WithLock(path, func() error {
		all, err := LoadRateLimitSnapshots()
		if err != nil {
			// Corrupt store: start over rather than fail forever.
			all = map[string]RateLimitSnapshot{}
		}
		if current, ok := all[snap.AccountID]; ok && current.CapturedAt.After(snap.CapturedAt) {
			return nil
		}
		all[snap.AccountID] = snap

		data, err := json.MarshalIndent(all, "", "  ")
		if err != nil {
			return err
		}
		return atomicfile.Write(path, data)
	})
}

// LoadRateLimitSnapshots returns the latest captured snapshot per ChatGPT
// account id. A missing store file yields an empty map, not an error.
func LoadRateLimitSnapshots() (map[string]RateLimitSnapshot, error) {
	path, err := rateLimitStorePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]RateLimitSnapshot{}, nil
		}
		return nil, err
	}
	var all map[string]RateLimitSnapshot
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	if all == nil {
		all = map[string]RateLimitSnapshot{}
	}
	return all, nil
}
