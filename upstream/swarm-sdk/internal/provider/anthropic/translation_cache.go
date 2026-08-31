package anthropic

import (
	"strconv"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// TranslationCache caches previously-translated Anthropic messages to avoid
// O(N²) rebuild cost on every API call. Cached messages are stored WITHOUT
// cache_control markers — those are applied to copies in translateRequest.
type TranslationCache struct {
	mu             sync.Mutex
	cachedMessages []Message // translated Anthropic messages WITHOUT cache_control
	cachedCount    int       // number of canonical messages that produced cachedMessages
	cachedPrefix   string    // fingerprint of the canonical messages[:cachedCount]
}

// NewTranslationCache creates a new empty translation cache.
func NewTranslationCache() *TranslationCache {
	return &TranslationCache{}
}

// Get returns the cached translated messages and the canonical message count
// they were built from. The caller must NOT mutate the returned slice.
func (c *TranslationCache) Get() ([]Message, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cachedMessages, c.cachedCount
}

// PrefixMatches reports whether the cache was built from a canonical prefix
// identical to the first cachedCount messages of the given slice. The
// incremental fast path slices messages[cachedCount:] as "new" work and trusts
// messages[:cachedCount] to already be represented by the cache; that is only
// safe when the prefix is byte-for-byte the same conversation. Canonical
// messages that DON'T survive translation 1:1 (e.g. an inline system message
// that gets stripped) make cachedCount larger than the translated prefix, so
// blindly slicing skips real messages. Verifying the prefix here forces a full
// re-translation whenever the assumption is violated, preventing silent
// transcript corruption (a dropped assistant reply that merges two user turns).
func (c *TranslationCache) PrefixMatches(messages []*conversation.Message) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cachedCount == 0 || len(messages) < c.cachedCount {
		return false
	}
	return c.cachedPrefix == fingerprintMessages(messages[:c.cachedCount])
}

// Update replaces the cached messages with the given translated messages and
// records how many canonical messages they came from. The canonical prefix is
// left unrecorded, so PrefixMatches returns false until UpdateWithCanonical is
// used — the incremental fast path then conservatively falls back.
func (c *TranslationCache) Update(messages []Message, canonicalCount int) {
	c.updateWithPrefix(messages, canonicalCount, "")
}

// UpdateWithCanonical is like Update but also records a fingerprint of the
// canonical prefix (canonical[:len(canonical)]) that produced these translated
// messages, so a later call can verify the prefix still matches before reusing
// the cache incrementally.
func (c *TranslationCache) UpdateWithCanonical(messages []Message, canonical []*conversation.Message) {
	c.updateWithPrefix(messages, len(canonical), fingerprintMessages(canonical))
}

func (c *TranslationCache) updateWithPrefix(messages []Message, canonicalCount int, prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cachedMessages = messages
	c.cachedCount = canonicalCount
	c.cachedPrefix = prefix
}

// Reset clears the cache, forcing a full re-translation on the next call.
func (c *TranslationCache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cachedMessages = nil
	c.cachedCount = 0
	c.cachedPrefix = ""
}

// fingerprintMessages builds a cheap identity string for a canonical message
// slice: role + content length + a bounded content head per message. Enough to
// distinguish different conversations/prefixes without hashing whole bodies.
func fingerprintMessages(messages []*conversation.Message) string {
	var b strings.Builder
	for _, m := range messages {
		if m == nil {
			b.WriteString("nil;")
			continue
		}
		b.WriteString(string(m.Role))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(len(m.Content)))
		b.WriteByte(':')
		head := m.Content
		if len(head) > 32 {
			head = head[:32]
		}
		b.WriteString(head)
		b.WriteString(";;")
	}
	return b.String()
}
