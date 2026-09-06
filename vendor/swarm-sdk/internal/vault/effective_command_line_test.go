package vault

import (
	"context"
	"testing"
)

// TestCanUseCommandMatchesEffectiveCommandLineWithArgs proves issue #247:
// an allow-list pattern like "agent-browser *" is written against the full
// invocation an operator expects to authorize, but vault_exec's
// command-string convenience parsing splits a single command string into
// Command="agent-browser" + Args=[...] before building the ExecutionRequest.
// Matching CanUseCommand against the bare req.Command alone (losing the
// arguments) made a policy pattern written with a trailing "*" fail to
// match ANY invocation that carried arguments -- exactly the reported
// "agent-browser --session-name isolated get url" reproduction.
func TestCanUseCommandMatchesEffectiveCommandLineWithArgs(t *testing.T) {
	e := newTestExecutor(t, Credential{
		ID: "browser", Kind: CredentialKindAPIKey, Secret: "s3cr3t",
		Scope:           ScopeGlobal,
		AllowedCommands: []string{"agent-browser *"},
		Inject:          InjectConfig{Method: InjectEnv, Target: "TOKEN"},
	})
	realExecReq := ExecutionRequest{
		CredentialID: "browser",
		Command:      "agent-browser",
		Args:         []string{"--session-name", "isolated", "get", "url"},
		Tool:         "bash",
	}
	if !credentialForTest(t, e, "browser").CanUseCommand(realExecReq.EffectiveCommandLine()) {
		t.Fatalf("EffectiveCommandLine() = %q, should match allow-list pattern %q",
			realExecReq.EffectiveCommandLine(), "agent-browser *")
	}

	// The bare command (pre-fix behavior) must NOT match on its own -- this
	// pins the exact defect: matching bare Command against "agent-browser *"
	// fails because the pattern's "*" requires a trailing space-plus-content.
	if credentialForTest(t, e, "browser").CanUseCommand(realExecReq.Command) {
		t.Fatalf("bare Command %q unexpectedly matched %q -- test no longer demonstrates the defect",
			realExecReq.Command, "agent-browser *")
	}
}

// TestEffectiveCommandLineNoArgsIsJustCommand covers the no-args case: the
// reconstruction must be byte-identical to Command when there are no Args,
// so every existing bare-command allow-list pattern keeps working unchanged.
func TestEffectiveCommandLineNoArgsIsJustCommand(t *testing.T) {
	req := ExecutionRequest{Command: "aws"}
	if got := req.EffectiveCommandLine(); got != "aws" {
		t.Fatalf("EffectiveCommandLine() = %q, want %q", got, "aws")
	}
}

// TestExecuteEndToEndWithArgsAllowListPattern exercises the real Execute
// wiring (executor.go's CanUseCommand call site), not just the standalone
// EffectiveCommandLine/CanUseCommand pairing, proving a command split into
// Command+Args by vault_exec's convenience parsing is actually authorized
// and runs end to end against an allow-list pattern written with a
// trailing "*".
func TestExecuteEndToEndWithArgsAllowListPattern(t *testing.T) {
	e := newTestExecutor(t, Credential{
		ID: "echoer", Kind: CredentialKindAPIKey, Secret: "s3cr3t",
		Scope:           ScopeGlobal,
		AllowedCommands: []string{"echo *"},
		Inject:          InjectConfig{Method: InjectEnv, Target: "TOKEN"},
	})
	res, err := e.Execute(context.Background(), ExecutionRequest{
		CredentialID: "echoer",
		Command:      "echo", // as vault_exec's splitShellCommand would produce from "echo hello world"
		Args:         []string{"hello", "world"},
		Tool:         "bash",
	})
	if err != nil {
		t.Fatalf("Execute() with an args-carrying allow-listed command failed: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
}

// credentialForTest resolves a stored credential back out of the executor's
// vault for direct CanUseCommand assertions, bypassing execution entirely.
func credentialForTest(t *testing.T, e *Executor, id string) *Credential {
	t.Helper()
	cred, err := e.vault.ResolveCredential(context.Background(), id, "")
	if err != nil {
		t.Fatalf("resolve %s: %v", id, err)
	}
	return cred
}
