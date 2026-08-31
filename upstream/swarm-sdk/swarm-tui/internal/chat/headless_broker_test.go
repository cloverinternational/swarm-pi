package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// Bug 3 (truthfulness audit): the headless broker unconditionally returned
// DecisionApproveOnce, ignoring permission level + config. These tests pin the
// new --approval-mode posture: auto approves (backward compatible), deny/prompt
// refuse mutating/credential/network perms while allowing read-only.

func req(perm tools.Permission, tool string) tools.PermissionApprovalRequest {
	return tools.PermissionApprovalRequest{
		Tool:       tool,
		Permission: string(perm),
		Target:     "/some/target",
	}
}

func TestHeadlessBroker_AutoApprovesEverything(t *testing.T) {
	b := NewHeadlessApprovalBrokerWithMode(ApprovalModeAuto)
	for _, perm := range []tools.Permission{
		tools.PermissionFileRead,
		tools.PermissionFileWrite,
		tools.PermissionFileDelete,
		tools.PermissionBashExecute,
		tools.PermissionSecretAccess,
	} {
		resp, err := b.Request(context.Background(), req(perm, "SomeTool"))
		if err != nil {
			t.Fatalf("auto mode returned error for %s: %v", perm, err)
		}
		if resp.Decision != tools.DecisionApproveOnce || resp.Outcome != tools.OutcomeApproved {
			t.Fatalf("auto mode should approve %s, got decision=%s outcome=%s", perm, resp.Decision, resp.Outcome)
		}
	}
}

func TestHeadlessBroker_DefaultIsAuto(t *testing.T) {
	// NewHeadlessApprovalBroker() must preserve historical auto-approve behavior.
	b := NewHeadlessApprovalBroker()
	resp, err := b.Request(context.Background(), req(tools.PermissionFileWrite, "Write"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Decision != tools.DecisionApproveOnce {
		t.Fatalf("default broker must auto-approve, got %s", resp.Decision)
	}
}

func TestHeadlessBroker_DenyBlocksMutating(t *testing.T) {
	b := NewHeadlessApprovalBrokerWithMode(ApprovalModeDeny)

	mutating := []tools.Permission{
		tools.PermissionFileWrite,
		tools.PermissionFileDelete,
		tools.PermissionBashExecute,
		tools.PermissionNetworkAccess,
		tools.PermissionDatabaseWrite,
		tools.PermissionContainerAccess,
		tools.PermissionSecretAccess,
		tools.PermissionHookManage,
	}
	for _, perm := range mutating {
		resp, err := b.Request(context.Background(), req(perm, "MutatingTool"))
		if err != nil {
			t.Fatalf("deny mode returned error for %s: %v", perm, err)
		}
		if resp.Decision != tools.DecisionDeny || resp.Outcome != tools.OutcomeDenied {
			t.Fatalf("deny mode should DENY %s, got decision=%s outcome=%s", perm, resp.Decision, resp.Outcome)
		}
	}
}

func TestHeadlessBroker_DenyAllowsReadOnly(t *testing.T) {
	b := NewHeadlessApprovalBrokerWithMode(ApprovalModeDeny)
	for _, perm := range []tools.Permission{
		tools.PermissionFileRead,
		tools.PermissionDatabaseRead,
	} {
		resp, err := b.Request(context.Background(), req(perm, "ReadTool"))
		if err != nil {
			t.Fatalf("deny mode returned error for %s: %v", perm, err)
		}
		if resp.Decision != tools.DecisionApproveOnce {
			t.Fatalf("deny mode should ALLOW read-only %s, got %s", perm, resp.Decision)
		}
	}
}

// TestHeadlessBroker_DenyFailsClosedOnUnknownPerm pins M1: an unknown/future
// permission type must be DENIED in deny mode (fail closed), not silently
// allowed. Uses a permission string that is not in readOnlyPermissions.
func TestHeadlessBroker_DenyFailsClosedOnUnknownPerm(t *testing.T) {
	b := NewHeadlessApprovalBrokerWithMode(ApprovalModeDeny)
	resp, err := b.Request(context.Background(), tools.PermissionApprovalRequest{
		Tool:       "FutureTool",
		Permission: "some_future_permission_not_yet_classified",
		Target:     "/x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Decision != tools.DecisionDeny {
		t.Fatalf("deny mode must fail closed on unknown permission, got %s", resp.Decision)
	}
}

// TestHeadlessBroker_DenyReasonReachesAgent pins H1: the denial carries an
// actionable reason in UserContext so the agent can recover instead of hitting
// an opaque dead end.
func TestHeadlessBroker_DenyReasonReachesAgent(t *testing.T) {
	b := NewHeadlessApprovalBrokerWithMode(ApprovalModeDeny)
	resp, err := b.Request(context.Background(), req(tools.PermissionFileWrite, "Write"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.UserContext == "" {
		t.Fatal("deny response must carry an actionable reason in UserContext for the agent")
	}
	if !strings.Contains(resp.UserContext, "read-only") {
		t.Fatalf("denial reason should tell the agent the run is read-only, got: %q", resp.UserContext)
	}
}

func TestHeadlessBroker_PromptFailsClosed(t *testing.T) {
	// No TTY in headless → prompt behaves like deny for mutating perms.
	b := NewHeadlessApprovalBrokerWithMode(ApprovalModePrompt)
	resp, err := b.Request(context.Background(), req(tools.PermissionFileWrite, "Write"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Decision != tools.DecisionDeny {
		t.Fatalf("prompt mode should fail closed (deny) for file_write, got %s", resp.Decision)
	}
}

func TestParseApprovalMode(t *testing.T) {
	cases := []struct {
		in     string
		want   ApprovalMode
		wantOK bool
	}{
		{"auto", ApprovalModeAuto, true},
		{"deny", ApprovalModeDeny, true},
		{"prompt", ApprovalModePrompt, true},
		{"", ApprovalModeAuto, true},
		{"garbage", ApprovalModeAuto, false},
	}
	for _, c := range cases {
		got, ok := ParseApprovalMode(c.in)
		if got != c.want || ok != c.wantOK {
			t.Fatalf("ParseApprovalMode(%q) = (%s,%v), want (%s,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}
