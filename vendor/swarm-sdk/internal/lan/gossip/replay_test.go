package gossip

import (
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"
)

func distinctNonce(t *testing.T) string {
	t.Helper()
	var raw [NonceBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(raw[:])
}

// TestReplayCache_EnforcesBoundedCapacity floods the cache with far more
// distinct, genuinely-random nonces than its capacity and asserts the
// cache never retains more than capacity entries -- proving unbounded
// growth cannot happen even under a flood of never-repeating nonces.
func TestReplayCache_EnforcesBoundedCapacity(t *testing.T) {
	const capacity = 8
	c := newReplayCache(capacity)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// Expiry far in the future so the expiry-sweep never reclaims capacity
	// during this flood -- this isolates the LRU-cap enforcement from the
	// expiry-sweep behavior tested separately below.
	expiresAt := now.Add(24 * time.Hour)

	const flood = capacity * 20
	nonces := make([]string, 0, flood)
	for i := 0; i < flood; i++ {
		n := distinctNonce(t)
		nonces = append(nonces, n)
		if replayed := c.seenOrRecord(n, expiresAt, now); replayed {
			t.Fatalf("nonce %d unexpectedly reported as already seen", i)
		}
		if got := c.len(); got > capacity {
			t.Fatalf("cache grew to %d entries, want <= %d (capacity)", got, capacity)
		}
	}
	if got := c.len(); got != capacity {
		t.Fatalf("expected cache to settle at exactly capacity=%d after flooding, got %d", capacity, got)
	}

	// Design choice: oldest-eviction. The earliest-inserted nonces must
	// have been evicted (so presenting one again is NOT reported as a
	// replay -- it looks "new" because the cache no longer remembers it,
	// which is the documented, accepted tradeoff for a hard memory bound).
	// The most-recently-inserted nonce (last one pushed, never evicted)
	// MUST still be remembered as a replay.
	oldest := nonces[0]
	if replayed := c.seenOrRecord(oldest, expiresAt, now); replayed {
		t.Fatalf("expected the oldest nonce to have been evicted (not remembered), but it was reported as a replay")
	}

	newest := nonces[len(nonces)-1]
	if replayed := c.seenOrRecord(newest, expiresAt, now); !replayed {
		t.Fatalf("expected the most recently inserted nonce to still be remembered as a replay")
	}
}

// TestReplayCache_ExpiredEntryCanBeReusedAfterSweep exercises this
// package's chosen design: entries are associated with the issuing frame's
// ExpiresAt and opportunistically swept once genuinely expired relative to
// the "now" passed to a later seenOrRecord call, freeing the nonce (and a
// capacity slot) for reuse. This is a deliberate, documented choice (see
// replay.go's replayCache doc comment) rather than permanent
// retention-until-LRU-eviction.
func TestReplayCache_ExpiredEntryCanBeReusedAfterSweep(t *testing.T) {
	c := newReplayCache(defaultReplayCacheCapacity)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	nonce := distinctNonce(t)
	expiresAt := now.Add(time.Minute)

	if replayed := c.seenOrRecord(nonce, expiresAt, now); replayed {
		t.Fatalf("first presentation must not be a replay")
	}
	// Immediately replaying (before expiry) must be rejected.
	if replayed := c.seenOrRecord(nonce, expiresAt, now.Add(time.Second)); !replayed {
		t.Fatalf("expected an immediate replay (before expiry) to be detected")
	}

	// Advance "now" to genuinely past expiresAt and let an unrelated call
	// trigger the opportunistic sweep.
	past := expiresAt.Add(time.Second)
	other := distinctNonce(t)
	c.seenOrRecord(other, now.Add(time.Hour), past)

	if got := c.len(); got != 1 {
		t.Fatalf("expected the expired entry to have been swept, leaving only the unrelated entry, got len=%d", got)
	}

	// The swept nonce must now be reusable (no longer considered seen).
	if replayed := c.seenOrRecord(nonce, now.Add(time.Hour), past.Add(time.Second)); replayed {
		t.Fatalf("expected the swept/expired nonce to be reusable, but it was reported as a replay")
	}
}

func TestReplayCache_NonPositiveCapacityFallsBackToDefault(t *testing.T) {
	c := newReplayCache(0)
	if c.capacity != defaultReplayCacheCapacity {
		t.Fatalf("expected non-positive capacity to fall back to default %d, got %d", defaultReplayCacheCapacity, c.capacity)
	}
	c2 := newReplayCache(-5)
	if c2.capacity != defaultReplayCacheCapacity {
		t.Fatalf("expected negative capacity to fall back to default %d, got %d", defaultReplayCacheCapacity, c2.capacity)
	}
}
