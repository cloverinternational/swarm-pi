package builtin

import (
	"context"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/journalredact"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// TaskAuditHook attaches a compact, sanitized audit event to the sole active
// task after each relevant tool completes. It never stores tool output bodies
// or secret values, and never blocks the user's real tool call.
type TaskAuditHook struct{}

// NewTaskAuditHook creates a new compact task-audit hook.
func NewTaskAuditHook() *TaskAuditHook { return &TaskAuditHook{} }

// Name identifies the hook.
func (h *TaskAuditHook) Name() string { return "task-audit-hook" }

// Priority runs low so it observes the final outcome without interfering.
func (h *TaskAuditHook) Priority() int { return 10 }

// Filter only reacts to completed tool executions.
func (h *TaskAuditHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolAfterExecute
}

// taskAuditIgnored lists tools whose events would be pure noise on the task.
var taskAuditIgnored = map[string]bool{
	"taskcreate": true, "taskupdate": true, "tasklist": true, "taskget": true,
	"todowrite": true, "todoread": true,
	"enterplanmode": true, "exitplanmode": true,
	"askuserquestion": true, "pushagentupdate": true,
}

// OnEvent records a compact audit event against the active task.
func (h *TaskAuditHook) OnEvent(_ context.Context, event hooks.Event) (hooks.HookResult, error) {
	toolName, _ := event.Data["tool_name"].(string)
	if toolName == "" {
		return hooks.Continue(), nil
	}
	if taskAuditIgnored[normalizeToolName(toolName)] {
		return hooks.Continue(), nil
	}

	tm := ii.GetTodoManager()
	if tm == nil {
		return hooks.Continue(), nil
	}

	outcome := "success"
	if event.Data["error"] != nil {
		outcome = "failure"
	}

	actor := event.AgentID
	if actor == "" {
		actor = "main"
	}

	summary := summarizeToolArgs(toolName, event.Data["params"])

	evt := ii.TodoAuditEvent{
		Type:      "tool",
		Timestamp: time.Now().UTC(),
		Actor:     actor,
		Summary:   summary,
		Metadata: map[string]any{
			"tool":    toolName,
			"outcome": outcome,
		},
	}
	// Best-effort: an absent active task (no focus) is expected and ignored.
	_ = tm.AppendActiveAuditEvent(evt)
	return hooks.Continue(), nil
}

const maxAuditSummaryLen = 160

// normalizeToolName delegates to the canonical implementation in the base
// hooks package (see hooks/toolclass.go) so this package has one definition
// of "normalize a tool name" shared with autogenskills.
func normalizeToolName(name string) string {
	return hooks.NormalizeToolName(name)
}

// summarizeToolArgs builds a short, sanitized, single-line argument summary.
func summarizeToolArgs(toolName string, rawParams any) string {
	params, ok := rawParams.(map[string]any)
	if !ok || len(params) == 0 {
		return toolName
	}
	// Prefer the most descriptive single argument when present.
	for _, key := range []string{"command", "file_path", "path", "pattern", "query", "url", "subject"} {
		if value, ok := params[key].(string); ok && value != "" {
			return truncateSummary(toolName + ": " + sanitizeScalar(value))
		}
	}
	// Otherwise list a couple of sanitized keys.
	var parts []string
	for key, value := range params {
		if isSecretKey(key) {
			parts = append(parts, key+"=[REDACTED]")
			continue
		}
		if str, ok := value.(string); ok {
			parts = append(parts, key+"="+sanitizeScalar(str))
		} else {
			parts = append(parts, key)
		}
		if len(parts) >= 3 {
			break
		}
	}
	if len(parts) == 0 {
		return toolName
	}
	return truncateSummary(toolName + ": " + strings.Join(parts, " "))
}

func sanitizeScalar(value string) string {
	value = journalredact.RedactText(value)
	value = strings.ReplaceAll(value, "\n", " ")
	if len(value) > 80 {
		value = value[:80] + "…"
	}
	return value
}

func truncateSummary(summary string) string {
	if len(summary) > maxAuditSummaryLen {
		return summary[:maxAuditSummaryLen] + "…"
	}
	return summary
}

func isSecretKey(key string) bool {
	lower := strings.ToLower(key)
	for _, needle := range []string{"token", "password", "secret", "credential", "api_key", "apikey", "private_key", "privatekey"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}
