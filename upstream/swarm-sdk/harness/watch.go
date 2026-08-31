// Package harness — Phase 4c opt-in transactional manifest watcher.
//
// Watch is a PURE, stdlib-only manifest poller. It has no dependency on any
// runtime, transport, or higher-level package: it only reads a single file,
// hashes its content, and compiles STABLE content into an immutable Plan. It is
// OPT-IN — nothing polls unless a caller invokes Watch — and it is designed so a
// burst of rapid writes coalesces into exactly ONE event once writing settles
// (debounce + stable-write), and so an invalid manifest is reported WITHOUT
// advancing the internal baseline (the next valid version is still detected as a
// change).
package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"time"
)

// Default poll cadence and stability threshold for WatchOptions built with
// DefaultWatchOptions. A change is only surfaced after its content hash has been
// observed identical for StablePolls consecutive polls.
const (
	defaultPollInterval = 200 * time.Millisecond
	defaultStablePolls  = 2
)

// readErrorToken is the stable observed-hash token for a transiently unreadable
// file. Using one constant token keeps a persistent read error from thrashing
// the stability counter (it stays "stable" instead of looking like new content).
const readErrorToken = "\x00harness.watch.read-error"

// WatchEvent is a single surfaced manifest transition. Exactly one side is
// meaningful: a valid stable manifest carries Plan + NewDigest (with OldDigest =
// the prior VALID baseline); an invalid or unreadable manifest carries Err (and
// Diagnostics when the compile produced structured diagnostics) and leaves the
// baseline unchanged.
type WatchEvent struct {
	Path        string
	OldDigest   string
	NewDigest   string
	Plan        *Plan
	Diagnostics Diagnostics
	Err         error
}

// WatchOptions configures Watch. PollInterval and StablePolls are the public
// knobs; now and tick are unexported test seams (a nil tick uses a real
// time.Ticker at PollInterval; a nil now uses time.Now).
type WatchOptions struct {
	PollInterval time.Duration
	StablePolls  int

	now  func() time.Time
	tick <-chan time.Time
}

// DefaultWatchOptions returns WatchOptions with production-sane defaults.
func DefaultWatchOptions() WatchOptions {
	return WatchOptions{PollInterval: defaultPollInterval, StablePolls: defaultStablePolls}
}

// ErrWatchNoPath is returned by Watch when path is empty.
var ErrWatchNoPath = errors.New("harness: Watch requires a non-empty manifest path")

// Watch starts a single-goroutine, lock-free poller over path and returns a
// receive-only channel of WatchEvent. The goroutine stops and CLOSES the channel
// when ctx is cancelled or (in tests) the injected tick channel is closed; it
// never leaks. See the package doc for the debounce/stable-write contract.
func Watch(ctx context.Context, path string, opts WatchOptions) (<-chan WatchEvent, error) {
	if path == "" {
		return nil, ErrWatchNoPath
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultPollInterval
	}
	if opts.StablePolls <= 0 {
		opts.StablePolls = defaultStablePolls
	}
	if opts.now == nil {
		opts.now = time.Now
	}

	out := make(chan WatchEvent)
	go watchLoop(ctx, path, opts, out)
	return out, nil
}

// watchLoop is the single, lock-free polling goroutine. It owns all mutable
// state locally, so it is race-clean by construction.
func watchLoop(ctx context.Context, path string, opts WatchOptions, out chan<- WatchEvent) {
	defer close(out)

	tick := opts.tick
	if tick == nil {
		ticker := time.NewTicker(opts.PollInterval)
		defer ticker.Stop()
		tick = ticker.C
	}

	var (
		candidateHash  string // content hash currently being observed for stability
		candidateRaw   []byte // raw bytes captured when the candidate was set
		candidateErr   error  // read error captured when the candidate was set
		stableCount    int    // consecutive polls the candidate hash has held
		lastEmitted    string // last content hash we emitted (valid OR invalid): anti-spam
		baselineDigest string // digest of the last VALID emitted plan: advances only on valid
	)

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-tick:
			if !ok {
				return
			}
			_ = opts.now() // per-poll clock/barrier seam

			raw, rerr := os.ReadFile(path)
			obs := observedHash(raw, rerr)

			if obs == candidateHash {
				stableCount++
			} else {
				candidateHash = obs
				candidateRaw = raw
				candidateErr = rerr
				stableCount = 1
			}

			// Not yet stable, or identical to what we already emitted: nothing to do.
			if stableCount < opts.StablePolls || candidateHash == lastEmitted {
				continue
			}

			ev := compileWatchEvent(path, baselineDigest, candidateRaw, candidateErr)
			lastEmitted = candidateHash
			if ev.Err == nil && ev.Plan != nil {
				baselineDigest = ev.NewDigest // advance ONLY on a valid compile
			}

			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}
}

// observedHash returns a stable token for the current file state. A read error
// maps to a single constant token so a persistent error does not thrash the
// stability counter; otherwise it is the sha256 of the content.
func observedHash(raw []byte, rerr error) string {
	if rerr != nil {
		return readErrorToken
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// compileWatchEvent builds the event for a stabilized file state. A read error
// or a compile failure yields an Err (and Diagnostics when structured); a valid
// compile yields Plan + NewDigest with OldDigest set to the prior baseline.
func compileWatchEvent(path, oldDigest string, raw []byte, rerr error) WatchEvent {
	if rerr != nil {
		return WatchEvent{Path: path, OldDigest: oldDigest, Err: rerr}
	}
	plan, err := CompileBytes(raw, path)
	if err != nil {
		ev := WatchEvent{Path: path, OldDigest: oldDigest, Err: err}
		if ds, ok := AsDiagnostics(err); ok {
			ev.Diagnostics = ds
		}
		return ev
	}
	return WatchEvent{Path: path, OldDigest: oldDigest, NewDigest: plan.Digest(), Plan: plan}
}
