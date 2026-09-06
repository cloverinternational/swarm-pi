package hooks

import "sync"

// TranscriptPathFunc resolves a conversation ID to the absolute path of its
// durable transcript file, or "" when it cannot be resolved.
type TranscriptPathFunc func(conversationID string) string

var (
	transcriptMu       sync.RWMutex
	transcriptResolver TranscriptPathFunc
)

// SetTranscriptPathResolver installs the process-wide resolver used to populate
// the `transcript_path` field of the Claude Code hook payload.
//
// Why this is dependency-injected rather than a direct call into
// internal/conversation/storage: the hooks package is imported by the storage
// and agent layers, so reaching back into them here would invert the dependency
// and drag conversation persistence into every hook consumer. The composition
// root already holds a storage handle; it installs the resolver at startup.
//
// `transcript_path` is part of the Claude Code hook contract, not a SwarmOS
// extension. External hook scripts written for any compatible harness read it
// to recover the assistant turn — its prose and reasoning — that a tool event
// alone does not carry. Leaving it empty silently degrades every portable hook
// to tool-name-and-arguments, which is why it is populated rather than replaced
// by a bespoke field.
func SetTranscriptPathResolver(fn TranscriptPathFunc) {
	transcriptMu.Lock()
	transcriptResolver = fn
	transcriptMu.Unlock()
}

// ResolveTranscriptPath returns the transcript path for a conversation, or ""
// when no resolver is installed or the conversation cannot be located. It never
// panics and never blocks on I/O beyond the resolver itself.
func ResolveTranscriptPath(conversationID string) string {
	if conversationID == "" {
		return ""
	}
	transcriptMu.RLock()
	fn := transcriptResolver
	transcriptMu.RUnlock()
	if fn == nil {
		return ""
	}
	return fn(conversationID)
}

// transcriptPathForEvent picks the transcript path for a hook payload.
//
// An explicit Data["transcript_path"] set by the event producer wins, because a
// producer that already knows the file (compaction, replay, a test harness)
// knows it more cheaply and more precisely than any lookup. Otherwise the
// installed resolver is consulted. The result is "" when neither is available,
// which is exactly the previous behaviour, so hooks that never read the field
// are unaffected.
func transcriptPathForEvent(event Event) string {
	if raw, ok := event.Data["transcript_path"].(string); ok && raw != "" {
		return raw
	}
	return ResolveTranscriptPath(event.ConversationID)
}
