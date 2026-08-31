package hooks

import (
	"context"
	"testing"
	"time"
)

// TestInterpretHookControlPayload covers the stdout JSON control-payload
// dialects (Claude Code official + marketplace plugin) that must be able to
// block a tool even when the hook exits 0. Mirrors code_puppy issue #470.
func TestInterpretHookControlPayload(t *testing.T) {
	tests := []struct {
		name        string
		stdout      string
		wantControl bool
		wantBlock   bool
		wantReason  string
		wantStdout  string
	}{
		{
			name:        "plugin dialect block",
			stdout:      `{"result": "block", "reason": "dangerous command"}`,
			wantControl: true, wantBlock: true, wantReason: "dangerous command", wantStdout: "",
		},
		{
			name:        "plugin dialect continue stripped",
			stdout:      `{"result": "continue"}`,
			wantControl: true, wantBlock: false, wantReason: "", wantStdout: "",
		},
		{
			name:        "official decision block",
			stdout:      `{"decision": "block", "reason": "nope"}`,
			wantControl: true, wantBlock: true, wantReason: "nope", wantStdout: "",
		},
		{
			name:        "official permissionDecision deny",
			stdout:      `{"hookSpecificOutput": {"permissionDecision": "deny", "permissionDecisionReason": "policy violation"}}`,
			wantControl: true, wantBlock: true, wantReason: "policy violation", wantStdout: "",
		},
		{
			name:        "official permissionDecision allow",
			stdout:      `{"hookSpecificOutput": {"permissionDecision": "allow"}}`,
			wantControl: true, wantBlock: false, wantReason: "", wantStdout: "",
		},
		{
			name:        "additionalContext replaces stdout",
			stdout:      `{"decision": "block", "reason": "bad", "hookSpecificOutput": {"additionalContext": "use git push without --force"}}`,
			wantControl: true, wantBlock: true, wantReason: "bad", wantStdout: "use git push without --force",
		},
		{
			name:        "continue false blocks with stopReason",
			stdout:      `{"continue": false, "stopReason": "halting"}`,
			wantControl: true, wantBlock: true, wantReason: "halting", wantStdout: "",
		},
		{
			name:        "non-control json passthrough",
			stdout:      `{"foo": "bar"}`,
			wantControl: false, wantBlock: false, wantReason: "", wantStdout: `{"foo": "bar"}`,
		},
		{
			name:        "plain text passthrough",
			stdout:      "hello",
			wantControl: false, wantBlock: false, wantReason: "", wantStdout: "hello",
		},
		{
			name:        "invalid json passthrough",
			stdout:      `{"result": "block"`,
			wantControl: false, wantBlock: false, wantReason: "", wantStdout: `{"result": "block"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := interpretHookControlPayload(tc.stdout)
			if got.HasControl != tc.wantControl {
				t.Fatalf("HasControl = %v, want %v", got.HasControl, tc.wantControl)
			}
			if got.Block != tc.wantBlock {
				t.Fatalf("Block = %v, want %v", got.Block, tc.wantBlock)
			}
			if got.Reason != tc.wantReason {
				t.Fatalf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if got.Stdout != tc.wantStdout {
				t.Fatalf("Stdout = %q, want %q", got.Stdout, tc.wantStdout)
			}
		})
	}
}

// TestShellHook_ExitZeroJSONBlockVerdictBlocks is the end-to-end guarantee: a
// marketplace-style hook that exits 0 and prints a JSON block verdict must
// produce a Block, and the raw control JSON must be stripped from the output.
func TestShellHook_ExitZeroJSONBlockVerdictBlocks(t *testing.T) {
	h := NewShellHook("gate", `echo '{"result": "block", "reason": "BLOCKED by safety guard"}'`, []string{EventToolBeforeExecute})
	h.HookAction = "block_exit2" // default Claude Code semantics: exit-code only would NOT block here
	h.Timeout = 5 * time.Second

	res, err := h.OnEvent(context.Background(), Event{
		Type: EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != ActionBlock {
		t.Fatalf("expected ActionBlock for exit-0 JSON block verdict, got %v (msg=%q)", res.Action, res.Message)
	}
	if res.Message != "BLOCKED by safety guard" {
		t.Fatalf("block reason = %q, want %q", res.Message, "BLOCKED by safety guard")
	}
	if h.Stdout != "" {
		t.Fatalf("control JSON leaked into stdout: %q", h.Stdout)
	}
}

// TestShellHook_ExitZeroJSONContinueNoLeak verifies a continue verdict does not
// block and does not leak the control JSON into the transcript.
func TestShellHook_ExitZeroJSONContinueNoLeak(t *testing.T) {
	h := NewShellHook("gate", `echo '{"result": "continue"}'`, []string{EventToolBeforeExecute})
	h.Timeout = 5 * time.Second

	res, err := h.OnEvent(context.Background(), Event{
		Type: EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action == ActionBlock {
		t.Fatalf("continue verdict must not block")
	}
	if h.Stdout != "" {
		t.Fatalf("control JSON leaked into stdout: %q", h.Stdout)
	}
}
