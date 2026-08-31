package chat

import (
	"context"
	"runtime"
	"strconv"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/prompttrace"
)

// promptTraceStartupEntry caches one startup-time prompt contribution so it
// can be replayed into the per-request prompttrace.Collector at request
// time. The contribution happened long before any agent execution context
// existed (skills loader, LoadAndInjectContext, headless custom prompt
// override), so we capture its file:line at record-time and replay it
// later as a section entry.
type promptTraceStartupEntry struct {
	label  string
	chars  int
	source string // file:line captured at record time
}

// recordStartupPromptContribution appends one startup-time contribution to
// the per-SDK list. Safe to call from any goroutine. The caller's file:line
// is captured automatically via runtime.Caller(1).
func (sdk *SDKIntegration) recordStartupPromptContribution(label string, chars int) {
	sdk.recordStartupPromptContributionAt(label, chars, 2)
}

// RecordStartupPromptContribution is the exported equivalent for external
// callers (e.g. swarmos main.go) that build prompt sections outside the
// chat package and still want their contributions to show up in the
// [PROMPT PROVENANCE] banner.
func (sdk *SDKIntegration) RecordStartupPromptContribution(label string, chars int) {
	sdk.recordStartupPromptContributionAt(label, chars, 2)
}

func (sdk *SDKIntegration) recordStartupPromptContributionAt(label string, chars int, skip int) {
	if sdk == nil {
		return
	}
	var source string
	if _, file, line, ok := runtime.Caller(skip); ok {
		source = shortPathForTrace(file) + ":" + strconv.Itoa(line)
	}
	sdk.promptTraceMu.Lock()
	sdk.promptTraceStartupEntries = append(sdk.promptTraceStartupEntries, promptTraceStartupEntry{
		label:  label,
		chars:  chars,
		source: source,
	})
	sdk.promptTraceMu.Unlock()
}

// seedPromptTrace is the callback registered with the agent. It replays
// every recorded startup-time contribution into the per-request collector
// so the [PROMPT PROVENANCE] banner reflects the full prompt-construction
// pipeline, not just the contributions that happened during the request.
func (sdk *SDKIntegration) seedPromptTrace(ctx context.Context) {
	if sdk == nil {
		return
	}
	col := prompttrace.From(ctx)
	if col == nil {
		return
	}
	sdk.promptTraceMu.Lock()
	entries := make([]promptTraceStartupEntry, len(sdk.promptTraceStartupEntries))
	copy(entries, sdk.promptTraceStartupEntries)
	sdk.promptTraceMu.Unlock()
	for _, e := range entries {
		// Use AppendChars so the collector records the size without us
		// dragging the original content around. Then override the source
		// with the cached file:line via a tiny shim entry — we want the
		// banner to point at the original contribution site, not at this
		// replay loop.
		appendSeededEntry(col, e.label, e.chars, e.source)
	}
}

// appendSeededEntry pushes a section entry with a caller-supplied source
// override. We can't reach into the prompttrace package's internals, so we
// invoke AppendChars (which will capture _this_ file:line) and then patch
// the last entry's source via a small monkey-patch helper exposed by the
// package. If the package doesn't expose it, we fall back to recording the
// label with a "[seeded]" prefix so it's obvious in the banner that the
// source is the replay site, not the original contribution.
func appendSeededEntry(col *prompttrace.Collector, label string, chars int, source string) {
	// AppendChars captures appendSeededEntry's file:line, which is useless;
	// we want the original contribution site. Use the public Seed helper.
	prompttrace.Seed(col, label, chars, source)
}

// shortPathForTrace keeps the last 3 path segments so banner output stays
// narrow without losing useful disambiguation. Mirrors the helper in
// prompttrace/prompttrace.go.
func shortPathForTrace(p string) string {
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
