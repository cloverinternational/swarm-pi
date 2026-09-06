package hooks

import (
	"strconv"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/reminder"
)

var reminderSequences = struct {
	sync.Mutex
	bySource map[string]int
}{bySource: make(map[string]int)}

// WrapReminder returns the canonical, machine-parseable envelope for injected
// harness context. Attribute values are escaped and body whitespace is trimmed.
func WrapReminder(source, kind string, seq int, body string) string {
	return reminder.Wrap(source, kind, seq, body)
}

// NextReminderSeq returns a process-local monotonic sequence for one source.
func NextReminderSeq(source string) int {
	reminderSequences.Lock()
	defer reminderSequences.Unlock()
	reminderSequences.bySource[source]++
	return reminderSequences.bySource[source]
}

func reminderKind(source, content string) string {
	lower := strings.ToLower(source + " " + content)
	switch {
	case strings.Contains(lower, "skill review") || strings.Contains(lower, "autogenskills"):
		return "review"
	case strings.Contains(lower, "block") || strings.Contains(lower, "enforcement"):
		return "block"
	case strings.Contains(lower, "nudge") || strings.Contains(lower, "reminder"):
		return "nudge"
	default:
		return "context"
	}
}

func unwrapReminder(content string) (body string, ok bool) {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "<system-reminder") || !strings.HasSuffix(trimmed, "</system-reminder>") {
		return "", false
	}
	openEnd := strings.IndexByte(trimmed, '>')
	if openEnd < 0 {
		return "", false
	}
	return strings.TrimSpace(strings.TrimSuffix(trimmed[openEnd+1:], "</system-reminder>")), true
}

// FormatHookContext wraps hook-injected content in a <system-reminder> block so
// the model can distinguish system-generated context from the user's own words.
//
// This matches the convention used by Claude Code and by the SDK's own task
// nudge (see tools/ii/task_nudge.go). Any hook whose AdditionalContext flows
// into conversation history — whether embedded in a tool result (pre-tool) or
// appended as a standalone RoleUser message (post-tool / user-prompt-submit) —
// should route its content through this helper.
//
// Behavior:
//   - Empty or whitespace-only content returns "" (caller should skip injection).
//   - Already canonical content is returned unchanged.
//   - Legacy envelopes are normalized to the canonical schema.
func FormatHookContext(hookName, content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "<system-reminder ") &&
		strings.Contains(trimmed, ` source="`) &&
		strings.Contains(trimmed, ` kind="`) &&
		strings.Contains(trimmed, ` seq="`) {
		return trimmed
	}
	if body, ok := unwrapReminder(trimmed); ok {
		trimmed = body
	}
	if hookName == "" {
		hookName = "hook"
	}
	return WrapReminder(hookName, reminderKind(hookName, trimmed), NextReminderSeq(hookName), trimmed)
}

// ParseReminderSeq is intentionally small and is useful to consumers that only
// need ordering without pulling in an XML parser.
func ParseReminderSeq(envelope string) (int, bool) {
	const marker = ` seq="`
	start := strings.Index(envelope, marker)
	if start < 0 {
		return 0, false
	}
	start += len(marker)
	end := strings.IndexByte(envelope[start:], '"')
	if end < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(envelope[start : start+end])
	return n, err == nil
}
