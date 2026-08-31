// Package prompttrace records provenance entries describing which code path
// contributed each section of an assembled prompt or constructed message.
//
// It is a diagnostic aid for the --raw provider-dump flow: when a collector
// is attached to the context, prompt-builder sites can call Append/AppendMsg
// to register a short label, a character count, and the file:line of the
// caller. The agent execution layer reads back the entries and prints a
// human-readable banner alongside the existing raw HTTP request body dump.
//
// When no collector is attached, all helpers are zero-cost no-ops — the
// context lookup is the only work performed. Production paths that never
// enable --raw therefore pay nothing for the instrumentation.
package prompttrace

import (
	"context"
	"runtime"
	"sync"
)

// Entry is a single provenance record.
type Entry struct {
	// Kind distinguishes a system-prompt contribution ("section") from a
	// constructed message ("message").
	Kind string
	// Label is a short, stable identifier for the contribution
	// (e.g. "available_skills", "tool_availability_message").
	Label string
	// Chars is the character count of the contribution (len of the string
	// the caller produced). Provenance never carries the content itself —
	// the content already appears verbatim in the REQUEST BODY dump.
	Chars int
	// Source is the captured file:line of the caller. Format: "path/to/file.go:LINE".
	// Empty string if runtime.Caller failed.
	Source string
	// MsgIndex is only meaningful for Kind == "message" — the index in the
	// messages slice. Zero for sections.
	MsgIndex int
	// Role is only meaningful for Kind == "message" — the role of the
	// constructed message (e.g. "user", "system", "assistant").
	Role string
}

// Collector accumulates provenance entries for a single provider request.
// Methods are safe for concurrent use; callers may build the system prompt
// from one goroutine and inject messages from another.
type Collector struct {
	mu      sync.Mutex
	entries []Entry
}

// NewCollector returns a freshly-initialised, empty collector.
func NewCollector() *Collector {
	return &Collector{}
}

// Append records a system-prompt section contribution. The caller's
// file:line is captured automatically via runtime.Caller(1).
//
// Append is a no-op when c is nil so call sites can write:
//
//	prompttrace.From(ctx).Append("base_prompt", text)
//
// without nil-checking.
func (c *Collector) Append(label, content string) {
	if c == nil {
		return
	}
	c.appendAt(1, Entry{
		Kind:  "section",
		Label: label,
		Chars: len(content),
	})
}

// AppendMsg records a constructed-message contribution. The caller's
// file:line is captured automatically.
func (c *Collector) AppendMsg(label, role, content string, msgIndex int) {
	if c == nil {
		return
	}
	c.appendAt(1, Entry{
		Kind:     "message",
		Label:    label,
		Chars:    len(content),
		MsgIndex: msgIndex,
		Role:     role,
	})
}

// AppendChars records a system-prompt section contribution when the caller
// has only a character count, not the full content. Use this for synthetic
// or computed contributions (e.g. "the bytes I prepended on top of the base
// prompt") where building the content string just to take its length would
// be wasteful.
func (c *Collector) AppendChars(label string, chars int) {
	if c == nil {
		return
	}
	c.appendAt(1, Entry{
		Kind:  "section",
		Label: label,
		Chars: chars,
	})
}

// AppendTool records a tool-schema contribution: the byte size a single tool
// (name + description + serialized parameter schema) adds to the request's
// Tools field. Tool schemas are part of provider context but are not part of
// the system-prompt string, so they get their own bucket in the banner —
// previously they were invisible, which hid a major slice of large contexts.
func (c *Collector) AppendTool(name string, chars int) {
	if c == nil {
		return
	}
	c.appendAt(1, Entry{
		Kind:  "tool",
		Label: name,
		Chars: chars,
	})
}

// Seed records a section entry with a caller-supplied source override.
// Use this when the actual contribution happened earlier (and you cached
// its file:line at that time) but you only have access to the collector
// now. The replay site will not pollute the Source field.
func Seed(c *Collector, label string, chars int, source string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = append(c.entries, Entry{
		Kind:   "section",
		Label:  label,
		Chars:  chars,
		Source: source,
	})
	c.mu.Unlock()
}

// appendAt records the entry, capturing the caller's file:line `skip` frames
// above appendAt. skip=1 means "the function that called appendAt".
func (c *Collector) appendAt(skip int, e Entry) {
	// runtime.Caller(0) == this frame; bump by 1 to skip ourselves, then add
	// the caller-supplied skip to reach the original call site.
	if _, file, line, ok := runtime.Caller(skip + 1); ok {
		e.Source = shortFile(file) + ":" + itoa(line)
	}
	c.mu.Lock()
	c.entries = append(c.entries, e)
	c.mu.Unlock()
}

// Entries returns a snapshot of the recorded entries in insertion order.
func (c *Collector) Entries() []Entry {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Entry, len(c.entries))
	copy(out, c.entries)
	return out
}

// Reset clears all entries. The agent execution layer calls Reset between
// turns so traces do not accumulate across requests.
func (c *Collector) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = c.entries[:0]
	c.mu.Unlock()
}

type ctxKey struct{}

// WithCollector attaches c to ctx. From(ctx) returns it; callers that did
// not attach a collector receive nil and pay no overhead.
func WithCollector(ctx context.Context, c *Collector) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// From returns the Collector attached to ctx, or nil.
func From(ctx context.Context) *Collector {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(ctxKey{}).(*Collector)
	return c
}

// shortFile trims an absolute path down to "module/dir/file.go" by keeping
// the last few segments. This keeps the trace table narrow without losing
// useful disambiguation.
func shortFile(p string) string {
	// Find the last 3 separators and keep everything after them.
	const keepSegments = 3
	count := 0
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			count++
			if count == keepSegments+1 {
				return p[i+1:]
			}
		}
	}
	return p
}

// itoa is a tiny dependency-free integer formatter so prompttrace does not
// drag in fmt.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
