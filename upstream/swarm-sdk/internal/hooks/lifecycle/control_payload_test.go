package lifecycle

import "testing"

// TestApplyControlPayload covers the stdout JSON control-payload dialects the
// lifecycle command executor must honor: flat SwarmOS, Claude Code official
// (nested hookSpecificOutput + continue:false), and marketplace plugin
// (result:block). It also asserts the raw control JSON never leaks into Output.
func TestApplyControlPayload(t *testing.T) {
	tests := []struct {
		name           string
		exitDecision   HookDecision // decision state as produced by exit code
		stdout         string
		wantBlocked    bool
		wantReason     string
		wantOutput     string
		wantPermission string
	}{
		{
			name:        "flat decision block",
			stdout:      `{"decision":"block","reason":"nope"}`,
			wantBlocked: true, wantReason: "nope", wantOutput: "",
		},
		{
			name:        "plugin result block",
			stdout:      `{"result":"block","reason":"dangerous"}`,
			wantBlocked: true, wantReason: "dangerous", wantOutput: "",
		},
		{
			name:           "official nested permissionDecision deny",
			stdout:         `{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"policy"}}`,
			wantBlocked:    true,
			wantReason:     "policy",
			wantOutput:     "",
			wantPermission: "deny",
		},
		{
			name:        "continue false blocks with stopReason",
			stdout:      `{"continue":false,"stopReason":"halt"}`,
			wantBlocked: true, wantReason: "halt", wantOutput: "",
		},
		{
			name:        "additionalContext replaces output",
			stdout:      `{"decision":"block","reason":"bad","hookSpecificOutput":{"additionalContext":"do X instead"}}`,
			wantBlocked: true, wantReason: "bad", wantOutput: "do X instead",
		},
		{
			name:         "non-control json untouched",
			exitDecision: HookDecision{Decision: "allow", Output: `{"foo":"bar"}`},
			stdout:       `{"foo":"bar"}`,
			wantBlocked:  false, wantReason: "", wantOutput: `{"foo":"bar"}`,
		},
		{
			name:         "plain text untouched",
			exitDecision: HookDecision{Decision: "allow", Output: "hello"},
			stdout:       "hello",
			wantBlocked:  false, wantReason: "", wantOutput: "hello",
		},
		{
			name:         "control payload cannot undo exit-code deny",
			exitDecision: HookDecision{Decision: "deny", Reason: "exit 1", Output: `{"result":"continue"}`},
			stdout:       `{"result":"continue"}`,
			wantBlocked:  true, wantReason: "exit 1", wantOutput: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.exitDecision
			applyControlPayload(&d, []byte(tc.stdout))
			if d.IsBlocked() != tc.wantBlocked {
				t.Fatalf("IsBlocked = %v, want %v (decision=%q perm=%q)", d.IsBlocked(), tc.wantBlocked, d.Decision, d.PermissionDecision)
			}
			if d.Reason != tc.wantReason {
				t.Fatalf("Reason = %q, want %q", d.Reason, tc.wantReason)
			}
			if d.Output != tc.wantOutput {
				t.Fatalf("Output = %q, want %q", d.Output, tc.wantOutput)
			}
			if tc.wantPermission != "" && d.PermissionDecision != tc.wantPermission {
				t.Fatalf("PermissionDecision = %q, want %q", d.PermissionDecision, tc.wantPermission)
			}
		})
	}
}
