package gossip

import (
	"container/list"
	"sync"
	"time"
)

// defaultReplayCacheCapacity bounds how many distinct nonces a replayCache
// retains at once. It is deliberately a small, fixed, documented cap (not
// unbounded, not derived from input) so a flood of distinct-nonce frames
// cannot grow this cache's memory without limit. 4096 comfortably covers
// many advertisers issuing frequent frames while staying tiny in absolute
// memory (each entry is a short hex string plus bookkeeping).
const defaultReplayCacheCapacity = 4096

// replayCache is a bounded-memory nonce replay cache consulted by Verifier.
//
// Design choice (documented per task brief item 2): this is an LRU cache
// with OLDEST-EVICTION when at capacity, keyed by nonce, ordered by
// insertion/most-recent-touch via container/list. It also opportunistically
// sweeps entries whose associated frame ExpiresAt has passed relative to
// the "now" observed on each call, so a nonce from a frame that has
// genuinely expired can be reclaimed (and its capacity slot freed) even
// before the LRU cap forces an eviction. Once evicted -- whether by
// expiry-sweep or by LRU capacity pressure -- a nonce is no longer
// considered "seen": this cache trades a bounded, theoretical
// long-window replay risk (an evicted nonce could technically be reused)
// for a hard, documented memory bound, which is the explicit requirement
// in the task brief ("must never grow unbounded under a flood of distinct
// nonces"). A production hardening of this cache could add a
// probabilistic filter for a longer replay window without unbounded exact
// storage; that is out of scope this phase.
type replayCache struct {
	mu       sync.Mutex
	capacity int

	order *list.List               // list.Element.Value is a *replayEntry, front = oldest
	elems map[string]*list.Element // nonce -> its element in order
}

type replayEntry struct {
	nonce     string
	expiresAt time.Time
}

// newReplayCache constructs a replayCache with the given capacity. A
// non-positive capacity falls back to defaultReplayCacheCapacity so a
// misconfigured caller can never accidentally construct an unbounded (or
// zero-capacity, permanently-rejecting) cache.
func newReplayCache(capacity int) *replayCache {
	if capacity <= 0 {
		capacity = defaultReplayCacheCapacity
	}
	return &replayCache{
		capacity: capacity,
		order:    list.New(),
		elems:    make(map[string]*list.Element, capacity),
	}
}

// seenOrRecord reports whether nonce has already been recorded (a replay)
// as of now. If it has not, it records nonce (associated with expiresAt for
// later opportunistic sweeping) and returns false. It always sweeps expired
// entries first, then evicts the oldest entry if recording nonce would
// exceed capacity.
func (c *replayCache) seenOrRecord(nonce string, expiresAt, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sweepLocked(now)

	if elem, ok := c.elems[nonce]; ok {
		// Already present: this is a replay. Touch it to the back so a
		// repeatedly-replayed nonce doesn't get evicted before a truly
		// idle one -- irrelevant to correctness (it's already rejected
		// either way) but keeps LRU semantics coherent.
		c.order.MoveToBack(elem)
		return true
	}

	if c.capacity > 0 && len(c.elems) >= c.capacity {
		c.evictOldestLocked()
	}

	entry := &replayEntry{nonce: nonce, expiresAt: expiresAt}
	elem := c.order.PushBack(entry)
	c.elems[nonce] = elem
	return false
}

// sweepLocked removes every entry whose expiresAt is at or before now. Must
// be called with c.mu held.
func (c *replayCache) sweepLocked(now time.Time) {
	// Entries are not stored in expiry order (they're stored in
	// insertion/touch order), so we cannot stop at the first non-expired
	// front element the way a purely time-bucketed queue could. Walk the
	// whole list; it is bounded by capacity, so this is still
	// O(capacity), not unbounded.
	for elem := c.order.Front(); elem != nil; {
		next := elem.Next()
		entry := elem.Value.(*replayEntry)
		if !entry.expiresAt.IsZero() && !entry.expiresAt.After(now) {
			c.order.Remove(elem)
			delete(c.elems, entry.nonce)
		}
		elem = next
	}
}

// evictOldestLocked drops the least-recently-inserted-or-touched entry.
// Must be called with c.mu held and the list non-empty.
func (c *replayCache) evictOldestLocked() {
	front := c.order.Front()
	if front == nil {
		return
	}
	entry := front.Value.(*replayEntry)
	c.order.Remove(front)
	delete(c.elems, entry.nonce)
}

// len reports the current number of retained nonces. Exposed for tests.
func (c *replayCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.elems)
}
