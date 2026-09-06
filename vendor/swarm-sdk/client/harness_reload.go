// Harness Phase 4c reload controller — WatchHarness.
//
// WatchHarness is the OPT-IN bridge between the pure harness.Watch poller and the
// Phase 4b ApplyHarnessPlan transaction. It is only valid on a harness-built
// client; for each stabilized manifest change it classifies + applies per the
// change-class policy: HOT changes auto-apply (when the policy allows),
// RESTART-REQUIRED changes are reported but never applied, and FORBIDDEN/invalid
// changes are rejected. It holds NO client lock across the blocking channel
// receive (ApplyHarnessPlan takes the lock internally, per-event).
package client

import (
	"context"
	"errors"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

// ReloadPolicy governs the reload controller. AutoApplyHot (default true) gates
// automatic application of HOT-only changes; RESTART-REQUIRED and FORBIDDEN
// changes are NEVER auto-applied regardless of policy.
type ReloadPolicy struct {
	AutoApplyHot             bool
	ExecutableConsentDigests []string
}

// DefaultReloadPolicy returns the sane default posture (hot changes auto-apply).
func DefaultReloadPolicy() ReloadPolicy {
	return ReloadPolicy{AutoApplyHot: true}
}

// ReloadOutcome is the per-event report delivered to the WatchHarness callback.
// Applied, RestartRequired, and Rejected are mutually exclusive result flags;
// Result carries the full redacted ApplyHarnessResult when an apply was
// attempted (nil for a plan-less/invalid event or when auto-apply is disabled).
type ReloadOutcome struct {
	Event           harness.WatchEvent
	Result          *ApplyHarnessResult
	Applied         bool
	RestartRequired bool
	Rejected        bool
	Err             error
}

// WatchHarness starts a harness.Watch over path and drives ApplyHarnessPlan for
// each stabilized change, invoking onOutcome with the classified result. It is
// OPT-IN and blocks until ctx is cancelled or the watch channel closes. It is
// only supported on a harness-built client (else ErrNotHarnessClient, the same
// sentinel ApplyHarnessPlan uses).
func (c *Client) WatchHarness(ctx context.Context, path string, policy ReloadPolicy, onOutcome func(ReloadOutcome)) error {
	c.mu.RLock()
	hc := c.opts.harness
	allowYolo := hc != nil && hc.allowYolo
	requireExecutableConsent := hc != nil && hc.requireExecutableConsent
	harnessOK := hc != nil && hc.plan != nil
	c.mu.RUnlock()

	if !harnessOK {
		return ErrNotHarnessClient
	}

	events, err := harness.Watch(ctx, path, harness.DefaultWatchOptions())
	if err != nil {
		return err
	}
	consentDigests := make(map[string]struct{}, len(policy.ExecutableConsentDigests))
	for _, digest := range policy.ExecutableConsentDigests {
		if validHarnessDigest(digest) {
			consentDigests[digest] = struct{}{}
		}
	}

	for {
		select {
		case <-ctx.Done():
			// harness.Watch observes the same ctx and closes its channel; return
			// the cancellation cause without holding any lock.
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			outcome := c.classifyAndApplyReload(
				ctx,
				ev,
				policy,
				allowYolo,
				requireExecutableConsent,
				consentDigests,
			)
			if onOutcome != nil {
				onOutcome(outcome)
			}
		}
	}
}

// classifyAndApplyReload turns one WatchEvent into a ReloadOutcome, applying only
// HOT changes (and only when policy.AutoApplyHot is set). It holds no client
// lock; the lock is taken internally by ApplyHarnessPlan.
func (c *Client) classifyAndApplyReload(
	ctx context.Context,
	ev harness.WatchEvent,
	policy ReloadPolicy,
	allowYolo bool,
	requireExecutableConsent bool,
	consentDigests map[string]struct{},
) ReloadOutcome {
	out := ReloadOutcome{Event: ev}

	// Invalid, unreadable, or otherwise plan-less event: never applied.
	if ev.Err != nil || ev.Plan == nil {
		out.Rejected = true
		out.Err = ev.Err
		return out
	}

	// Auto-apply disabled: observe the change but do not mutate the live client.
	if !policy.AutoApplyHot {
		return out
	}

	// Audited (Phase 11b): the reload controller performs privileged live
	// reconfiguration on every stabilized watch event, so it must not bypass
	// the audit wrapper — see harness_audit.go's ApplyHarnessPlanAudited.
	consentDigest := ""
	if requireExecutableConsent {
		if _, ok := consentDigests[ev.Plan.Digest()]; ok {
			consentDigest = ev.Plan.Digest()
		}
	}
	res, err := c.ApplyHarnessPlanAudited(ctx, ev.Plan, ApplyHarnessOptions{
		AllowYolo:               allowYolo,
		ExecutableConsentDigest: consentDigest,
	})
	rc := res
	out.Result = &rc

	switch {
	case err == nil:
		out.Applied = len(res.Applied) > 0
	case errors.Is(err, ErrHarnessRestartRequired):
		out.RestartRequired = true
	default:
		out.Rejected = true
		out.Err = err
	}
	return out
}
