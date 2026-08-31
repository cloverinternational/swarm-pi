package hooks_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestClassifyToolFailureSignals(t *testing.T) {
	tests := []struct {
		name  string
		event hooks.Event
	}{
		{"failed event", hooks.Event{Type: hooks.EventToolExecutionFailed}},
		{"outcome failure", hooks.Event{Type: hooks.EventToolAfterExecute, ToolOutcome: &toolout.Outcome{Status: toolout.StatusFailure}}},
		{"outcome timeout", hooks.Event{Type: hooks.EventToolAfterExecute, ToolOutcome: &toolout.Outcome{TimedOut: true}}},
		{"data error", hooks.Event{Type: hooks.EventToolAfterExecute, Data: map[string]any{"error": errors.New("boom")}}},
		{"tool output success false", hooks.Event{Type: hooks.EventToolAfterExecute, Data: map[string]any{"tool_output": map[string]any{"success": false}}}},
		{"tool output error", hooks.Event{Type: hooks.EventToolAfterExecute, Data: map[string]any{"tool_output": map[string]any{"error": "boom"}}}},
		{"tool output status", hooks.Event{Type: hooks.EventToolAfterExecute, Data: map[string]any{"tool_output": map[string]any{"status": "timeout"}}}},
		{"tool result error", hooks.Event{Type: hooks.EventToolAfterExecute, Data: map[string]any{"result": tools.NewErrorResult(errors.New("boom"))}}},
		{"tool result outcome", hooks.Event{Type: hooks.EventToolAfterExecute, Data: map[string]any{"result": &tools.ToolResult{Outcome: &toolout.Outcome{Status: toolout.StatusFailure}}}}},
		{"timeout prose", hooks.Event{Type: hooks.EventToolAfterExecute, Data: map[string]any{"output": "request timed out while waiting"}}},
		{"output limit prose", hooks.Event{Type: hooks.EventToolAfterExecute, Data: map[string]any{"tool_output": "maximum output size exceeded"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hooks.ClassifyToolFailure(tt.event)
			if !got.Failed || got.Success || got.Fingerprint == "" {
				t.Fatalf("classification = %+v, want failed with fingerprint", got)
			}
		})
	}
}

func TestClassifyToolFailureSuccessAndToolName(t *testing.T) {
	got := hooks.ClassifyToolFailure(hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "Bash",
			"tool_output": map[string]any{
				"success": true,
				"status":  "success",
				"stdout":  "source text says maximum output size exceeded",
			},
		},
	})
	if got.Failed || !got.Success || got.ToolName != "Bash" {
		t.Fatalf("classification = %+v", got)
	}
}

func TestClassifyToolFailureDoesNotPromoteDegradedSuccess(t *testing.T) {
	got := hooks.ClassifyToolFailure(hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "vault_exec",
			"result":    tools.NewToolResult(`{"status":"interactive_unlock_unavailable","warning":"fell back to a retry"}`),
		},
	})
	if got.Failed || !got.Success {
		t.Fatalf("failure classification = %+v, want unchanged success semantics", got)
	}
}

func TestClassifyToolFrictionIgnoresSuccessfulOutputProse(t *testing.T) {
	output := "source and docs mention fallback, unavailable, retry, malformed, and no output"
	got := hooks.ClassifyToolFriction(hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"tool_name": "Read", "output": output},
	})
	if got.Friction || !got.CleanSuccess {
		t.Fatalf("classification = %+v, want clean success", got)
	}
}

func TestClassifyToolFrictionStructuredFailures(t *testing.T) {
	tests := []struct {
		name     string
		event    hooks.Event
		category string
	}{
		{
			name:     "failure",
			event:    hooks.Event{Type: hooks.EventToolExecutionFailed},
			category: "tool-failure",
		},
		{
			name: "timeout",
			event: hooks.Event{
				Type:        hooks.EventToolAfterExecute,
				ToolOutcome: &toolout.Outcome{TimedOut: true},
			},
			category: "timeout",
		},
		{
			name: "output limit",
			event: hooks.Event{
				Type: hooks.EventToolAfterExecute,
				Data: map[string]any{"output": "maximum output size exceeded"},
			},
			category: "output-limit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hooks.ClassifyToolFriction(tt.event)
			if !got.Friction || got.Category != tt.category || got.Fingerprint == "" {
				t.Fatalf("classification = %+v, want %q friction", got, tt.category)
			}
		})
	}
}

func TestClassifyToolFrictionIgnoresPolicyNonExecution(t *testing.T) {
	tests := []struct {
		name      string
		errorType string
	}{
		{name: "batch cancelled", errorType: "tool.batch_blocked"},
		{name: "hook blocked", errorType: "tool.blocked_by_hook"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hooks.ClassifyToolFriction(hooks.Event{
				Type: hooks.EventToolExecutionFailed,
				Data: map[string]any{
					"tool_name":  "Write",
					"error_type": tt.errorType,
					"error":      errors.New("legacy error field remains present"),
				},
			})
			if got.Friction || got.CleanSuccess {
				t.Fatalf("classification = %+v, want non-friction non-execution", got)
			}
		})
	}
}

func TestClassifyToolFrictionStillClassifiesGenuineErrors(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{name: "ordinary error", message: "tool process exited unexpectedly"},
		{name: "blocked in error prose", message: "request blocked by upstream firewall"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hooks.ClassifyToolFriction(hooks.Event{
				Type: hooks.EventToolAfterExecute,
				Data: map[string]any{
					"tool_name": "Bash",
					"error":     errors.New(tt.message),
				},
			})
			if !got.Friction || got.CleanSuccess || got.Category != "tool-failure" {
				t.Fatalf("classification = %+v, want genuine tool friction", got)
			}
		})
	}
}

func TestClassifyToolFrictionIgnoresOversizedSuccessfulOutput(t *testing.T) {
	output := strings.Repeat("fallback unavailable retry ", 100_000)
	got := hooks.ClassifyToolFriction(hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"tool_name": "HistorySearch", "output": output},
	})
	if got.Friction || !got.CleanSuccess {
		t.Fatalf("classification = %+v, want clean success", got)
	}
}

func TestClassifyToolFrictionExitCodeZeroSuppressesFailureProse(t *testing.T) {
	exitCode := 0
	got := hooks.ClassifyToolFriction(hooks.Event{
		Type:        hooks.EventToolAfterExecute,
		ToolOutcome: &toolout.Outcome{ExitCode: &exitCode},
		Data:        map[string]any{"output": "context deadline exceeded"},
	})
	if got.Friction || !got.CleanSuccess {
		t.Fatalf("classification = %+v, want clean success", got)
	}
}

func TestClassifyToolFrictionNonzeroExitCodeIsFailure(t *testing.T) {
	exitCode := 23
	got := hooks.ClassifyToolFriction(hooks.Event{
		Type:        hooks.EventToolAfterExecute,
		ToolOutcome: &toolout.Outcome{ExitCode: &exitCode},
	})
	if !got.Friction || got.CleanSuccess || got.Category != "tool-failure" {
		t.Fatalf("classification = %+v, want tool failure", got)
	}
}

func TestClassifyToolFrictionNilExitCodeIsNotFailure(t *testing.T) {
	got := hooks.ClassifyToolFriction(hooks.Event{
		Type:        hooks.EventToolAfterExecute,
		ToolOutcome: &toolout.Outcome{ExitCode: nil},
	})
	if got.Friction || !got.CleanSuccess {
		t.Fatalf("classification = %+v, want absence of evidence to remain success", got)
	}
}

func TestClassifyToolFrictionOversizedSuccessfulOutputPreservesExitCodeSuccess(t *testing.T) {
	exitCode := 0
	output := strings.Repeat("ordinary log line\n", 200_000) + "context deadline exceeded"
	got := hooks.ClassifyToolFriction(hooks.Event{
		Type:        hooks.EventToolAfterExecute,
		ToolOutcome: &toolout.Outcome{ExitCode: &exitCode},
		Data:        map[string]any{"output": output},
	})
	if got.Friction || !got.CleanSuccess {
		t.Fatalf("classification = %+v, want exit code 0 to suppress tail prose", got)
	}
}

func TestClassifyToolFrictionBoundsOversizedOutputAndPreservesTail(t *testing.T) {
	output := strings.Repeat("ordinary log line\n", 200_000) + "context deadline exceeded"
	got := hooks.ClassifyToolFriction(hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"output": output},
	})
	if !got.Friction || got.Category != "timeout" {
		t.Fatalf("classification = %+v, want tail timeout friction", got)
	}
}

func TestClassifyToolFrictionIgnoresFailureProseOutsideBoundedHeadAndTail(t *testing.T) {
	output := strings.Repeat("a", 1<<20) +
		" context deadline exceeded " +
		strings.Repeat("z", 1<<20)
	got := hooks.ClassifyToolFriction(hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"output": output},
	})
	if got.Friction || !got.CleanSuccess {
		t.Fatalf("classification = %+v, want middle prose outside scan bound ignored", got)
	}
}

func TestClassifyToolFrictionNamedPassthroughOutputIsNotFailureEvidence(t *testing.T) {
	for _, toolName := range []string{"Read", "grep"} {
		t.Run(toolName, func(t *testing.T) {
			got := hooks.ClassifyToolFriction(hooks.Event{
				Type: hooks.EventToolAfterExecute,
				Data: map[string]any{
					"tool_name": toolName,
					"output":    "matched source says output limit exceeded",
				},
			})
			if got.Friction || !got.CleanSuccess {
				t.Fatalf("classification = %+v, want passthrough prose ignored", got)
			}
		})
	}
}

func TestClassifyToolFrictionFingerprintIsStableAndDistinctForOversizedFailures(t *testing.T) {
	classifyError := func(message string) hooks.ToolFrictionClassification {
		return hooks.ClassifyToolFriction(hooks.Event{
			Type: hooks.EventToolAfterExecute,
			Data: map[string]any{"error": errors.New(message)},
		})
	}

	prefix := strings.Repeat("oversized failure material ", 100_000)
	first := classifyError(prefix + "first terminal detail")
	repeated := classifyError(prefix + "first terminal detail")
	different := classifyError(prefix + "different terminal detail")
	if first.Fingerprint == "" || first.Fingerprint != repeated.Fingerprint {
		t.Fatalf("same failure fingerprints = %q and %q, want identical non-empty values", first.Fingerprint, repeated.Fingerprint)
	}
	if first.Fingerprint == different.Fingerprint {
		t.Fatalf("different failure fingerprints = %q, want distinct values", first.Fingerprint)
	}
}
