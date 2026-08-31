package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/codex"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
)

// Account rotation: when the active OAuth account hits its subscription rate
// limit mid-send, swap in the next stacked account (tui_accounts.json) and
// retry once with a freshly built provider — the provider factory re-reads
// the SDK token store at build time, so the retry rides the new account.
// Also emits usage-threshold warnings from the passive rate-limit snapshots
// the codex provider captures on every turn.

// rotationThrottle is the minimum spacing between two automatic rotations of
// the same wire family — prevents ping-ponging when every stacked account is
// exhausted.
const rotationThrottle = 15 * time.Second

// usageWarnThresholds are the utilization percentages that trigger a warning
// notification, each fired once per window per reset period.
var usageWarnThresholds = []float64{75, 90}

// accountRotationHooks makes the wrapper testable; production code uses
// defaultAccountRotationHooks.
type accountRotationHooks struct {
	rotate          func(providerName string) (fromLabel, toLabel string, total int, err error)
	stackedAccounts func(providerName string) int
	loadSnapshots   func() (map[string]codex.RateLimitSnapshot, error)
	now             func() time.Time
}

func defaultAccountRotationHooks() accountRotationHooks {
	return accountRotationHooks{
		rotate: func(providerName string) (string, string, int, error) {
			from, to, total, err := settings.RotateToNextAccount(providerName)
			if err != nil {
				return "", "", total, err
			}
			return settings.AccountDisplayLabel(from), settings.AccountDisplayLabel(to), total, nil
		},
		stackedAccounts: func(providerName string) int {
			return len(settings.AccountsForProvider(providerName))
		},
		loadSnapshots: codex.LoadRateLimitSnapshots,
		now:           time.Now,
	}
}

// accountRotatingProvider wraps a provider whose wire family supports stacked
// OAuth accounts (openai/codex in v1).
type accountRotatingProvider struct {
	providerName string
	rebuild      func() (provider.Provider, error)
	notify       func(providerEventMsg)
	hooks        accountRotationHooks

	mu           sync.Mutex
	inner        provider.Provider
	lastRotation time.Time
}

// maybeWrapAccountRotation wraps base with account rotation when the entry's
// wire family supports it; otherwise returns base unchanged.
func maybeWrapAccountRotation(
	base provider.Provider,
	providerName string,
	rebuild func() (provider.Provider, error),
	notify func(providerEventMsg),
) provider.Provider {
	if provider.NormalizeProviderName(providerName) != "openai" {
		return base
	}
	return &accountRotatingProvider{
		providerName: providerName,
		rebuild:      rebuild,
		notify:       notify,
		hooks:        defaultAccountRotationHooks(),
		inner:        base,
	}
}

func (p *accountRotatingProvider) current() provider.Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inner
}

func (p *accountRotatingProvider) Name() string { return p.current().Name() }
func (p *accountRotatingProvider) Capabilities() provider.Capabilities {
	return p.current().Capabilities()
}

func (p *accountRotatingProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	resp, err := p.current().Chat(ctx, req)
	if err == nil || !p.tryRotate(err) {
		return resp, err
	}
	return p.current().Chat(ctx, req)
}

func (p *accountRotatingProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch, err := p.current().Stream(ctx, req)
	if err != nil && p.tryRotate(err) {
		ch, err = p.current().Stream(ctx, req)
	}
	if err != nil {
		return nil, err
	}
	return p.watchUsage(ch), nil
}

// tryRotate rotates to the next stacked account when err is a rate limit and
// rotation is possible. Returns true when the caller should retry.
func (p *accountRotatingProvider) tryRotate(err error) bool {
	if !sdkerr.IsRateLimitError(err) {
		return false
	}
	if p.hooks.stackedAccounts(p.providerName) < 2 {
		p.notifyUsageWarning(tr("classic.account.rate_limit", p.providerName))
		return false
	}

	p.mu.Lock()
	if p.hooks.now().Sub(p.lastRotation) < rotationThrottle {
		p.mu.Unlock()
		return false
	}
	p.lastRotation = p.hooks.now()
	p.mu.Unlock()

	fromLabel, toLabel, _, rerr := p.hooks.rotate(p.providerName)
	if rerr != nil {
		return false
	}

	// Rebuild the inner provider so it picks up the newly-activated account's
	// token from the SDK store. Without a rebuild hook the old instance would
	// keep streaming with the limited account's token.
	if p.rebuild == nil {
		return false
	}
	fresh, berr := p.rebuild()
	if berr != nil || fresh == nil {
		return false
	}
	p.mu.Lock()
	p.inner = fresh
	p.mu.Unlock()

	if p.notify != nil {
		p.notify(providerEventMsg{
			Type:         "account_rotate",
			ProviderName: p.providerName,
			FromProvider: fromLabel,
			ToProvider:   toLabel,
			Error:        err.Error(),
		})
	}
	return true
}

// ── usage-threshold warnings ─────────────────────────────────────────────────

// usageWarningsFired dedups threshold warnings: key accountID|window|threshold|resetAt.
var usageWarningsFired sync.Map

// watchUsage forwards the stream and, once it ends, checks the freshest
// persisted snapshot for crossed thresholds.
func (p *accountRotatingProvider) watchUsage(in <-chan provider.StreamChunk) <-chan provider.StreamChunk {
	out := make(chan provider.StreamChunk, 8)
	go func() {
		defer close(out)
		for chunk := range in {
			out <- chunk
		}
		p.checkUsageThresholds()
	}()
	return out
}

func (p *accountRotatingProvider) checkUsageThresholds() {
	snaps, err := p.hooks.loadSnapshots()
	if err != nil || len(snaps) == 0 {
		return
	}
	// The freshest snapshot belongs to the account that just served the send.
	var latest *codex.RateLimitSnapshot
	for id := range snaps {
		s := snaps[id]
		if latest == nil || s.CapturedAt.After(latest.CapturedAt) {
			latest = &s
		}
	}
	if latest == nil {
		return
	}
	single := p.hooks.stackedAccounts(p.providerName) < 2
	for _, w := range []*codex.RateLimitWindow{latest.Primary, latest.Secondary} {
		if w == nil {
			continue
		}
		for _, threshold := range usageWarnThresholds {
			if w.UsedPercent < threshold {
				continue
			}
			key := fmt.Sprintf("%s|%s|%v|%d", latest.AccountID, w.Label(), threshold, w.ResetAt)
			if _, dup := usageWarningsFired.LoadOrStore(key, p.hooks.now()); dup {
				continue
			}
			msg := tr("classic.account.limit_at", w.Label(), w.UsedPercent)
			if reset := w.ResetTime(); !reset.IsZero() {
				msg += tr("classic.account.resets", humanizeUntil(reset, p.hooks.now()))
			}
			if threshold >= 90 && single {
				msg += tr("classic.account.add_account")
			}
			p.notifyUsageWarning(msg)
		}
	}
}

func (p *accountRotatingProvider) notifyUsageWarning(text string) {
	if p.notify == nil {
		return
	}
	p.notify(providerEventMsg{
		Type:         "usage_warning",
		ProviderName: p.providerName,
		Error:        text,
	})
}

// humanizeUntil renders a coarse "in 2d 3h" / "in 45m" delta.
func humanizeUntil(t time.Time, now time.Time) string {
	d := t.Sub(now)
	if d <= 0 {
		return tr("classic.time.soon")
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return tr("classic.time.in_days_hours", days, hours)
	case hours > 0:
		return tr("classic.time.in_hours_minutes", hours, mins)
	default:
		return tr("classic.time.in_minutes", mins)
	}
}

// LastProviderJSON forwards debug payloads from the wrapped provider.
func (p *accountRotatingProvider) LastProviderJSON() json.RawMessage {
	if dp, ok := p.current().(provider.DebugProvider); ok {
		return dp.LastProviderJSON()
	}
	return nil
}

// SetRawEventCallback forwards the raw-event callback so the Debug Inspector
// keeps working through the rotation wrapper.
func (p *accountRotatingProvider) SetRawEventCallback(callback provider.RawEventCallback) {
	if capable, ok := p.current().(rawEventCapable); ok {
		capable.SetRawEventCallback(callback)
	}
}
