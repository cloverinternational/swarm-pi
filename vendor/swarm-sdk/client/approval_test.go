package client

import (
	"context"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// TestApprovalMode_InteractiveLeavesDefault confirms that "" / "interactive"
// return nil so the registry's default checker is preserved.
func TestApprovalMode_InteractiveLeavesDefault(t *testing.T) {
	for _, mode := range []string{"", "interactive", "unknown", "banana"} {
		if got := approvalCheckerFor(mode); got != nil {
			t.Errorf("approvalCheckerFor(%q): expected nil (keep default), got %T", mode, got)
		}
	}
}

// TestApprovalMode_Yolo installs a YOLO checker that allows any dangerous
// permission.
func TestApprovalMode_Yolo(t *testing.T) {
	checker := approvalCheckerFor("yolo")
	if checker == nil {
		t.Fatal("yolo: expected a checker, got nil")
	}

	// YOLO must permit dangerous permissions that readonly and default deny.
	dangerous := []tools.Permission{
		tools.PermissionBashExecute,
		tools.PermissionFileWrite,
		tools.PermissionFileDelete,
		tools.PermissionNetworkAccess,
	}
	if !checker.Check(context.Background(), dangerous) {
		t.Error("yolo checker should approve dangerous permissions; got false")
	}
}

// TestApprovalMode_Readonly permits reads and denies writes/deletes/bash.
func TestApprovalMode_Readonly(t *testing.T) {
	checker := approvalCheckerFor("readonly")
	if checker == nil {
		t.Fatal("readonly: expected a checker, got nil")
	}

	// Reads should pass.
	if !checker.Check(context.Background(), []tools.Permission{tools.PermissionFileRead}) {
		t.Error("readonly: FileRead should be allowed")
	}

	// Writes / deletes / bash / network should be denied.
	denied := []tools.Permission{
		tools.PermissionFileWrite,
		tools.PermissionFileDelete,
		tools.PermissionBashExecute,
		tools.PermissionNetworkAccess,
	}
	for _, p := range denied {
		if checker.Check(context.Background(), []tools.Permission{p}) {
			t.Errorf("readonly: %s should be denied, got approved", p)
		}
	}
}

// TestApprovalMode_CaseInsensitive confirms the option normalises input.
func TestApprovalMode_CaseInsensitive(t *testing.T) {
	if approvalCheckerFor(" YOLO ") == nil {
		t.Error("expected trimmed/lowercased mode to resolve to yolo checker")
	}
	if approvalCheckerFor("ReadOnly") == nil {
		t.Error("expected case-insensitive readonly to resolve")
	}
}

// TestApprovalMode_OptionAppliesToOptions verifies WithApprovalMode writes the
// value into the options struct (case-normalised).
func TestApprovalMode_OptionAppliesToOptions(t *testing.T) {
	o := options{}
	WithApprovalMode(" YOLO ")(&o)
	if o.approvalMode != "yolo" {
		t.Errorf("approvalMode: got %q want %q", o.approvalMode, "yolo")
	}
}

// ── Interactive daemon approval (G1) round-trip ────────────────────────────────

// awaitApprovalCallID subscribes and forwards the CallID of each
// EventApprovalRequested.
func awaitApprovalCallID(t *testing.T, c *Client) (<-chan string, func()) {
	t.Helper()
	got := make(chan string, 4)
	unsub := c.Subscribe(func(ev Event) error {
		if ev.Kind == EventApprovalRequested {
			if req, ok := ev.Payload.(ApprovalRequest); ok {
				got <- req.CallID
			}
		}
		return nil
	})
	return got, unsub
}

func TestInteractiveApproval_AllowRoundTrip(t *testing.T) {
	c := &Client{}
	checker := c.NewInteractiveApprovalChecker()
	got, unsub := awaitApprovalCallID(t, c)
	defer unsub()

	result := make(chan bool, 1)
	go func() {
		result <- checker.RequestApproval(context.Background(), []tools.Permission{tools.PermissionBashExecute}, "run ls")
	}()

	var callID string
	select {
	case callID = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no EventApprovalRequested emitted")
	}
	if pend := c.PendingApprovals(); len(pend) != 1 || pend[0] != callID {
		t.Fatalf("PendingApprovals = %v, want [%s]", pend, callID)
	}
	if err := c.RespondApproval(callID, true); err != nil {
		t.Fatalf("RespondApproval: %v", err)
	}
	select {
	case allow := <-result:
		if !allow {
			t.Fatal("RequestApproval returned false after RespondApproval(true)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval did not unblock after RespondApproval")
	}
	if pend := c.PendingApprovals(); len(pend) != 0 {
		t.Fatalf("pending not cleaned up: %v", pend)
	}
}

func TestInteractiveApproval_Deny(t *testing.T) {
	c := &Client{}
	checker := c.NewInteractiveApprovalChecker()
	got, unsub := awaitApprovalCallID(t, c)
	defer unsub()

	result := make(chan bool, 1)
	go func() { result <- checker.RequestApproval(context.Background(), nil, "x") }()
	callID := <-got
	if err := c.RespondApproval(callID, false); err != nil {
		t.Fatalf("RespondApproval: %v", err)
	}
	if <-result {
		t.Fatal("RequestApproval returned true after RespondApproval(false)")
	}
}

func TestInteractiveApproval_ContextCancelDenies(t *testing.T) {
	c := &Client{}
	checker := c.NewInteractiveApprovalChecker()
	got, unsub := awaitApprovalCallID(t, c)
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan bool, 1)
	go func() { result <- checker.RequestApproval(ctx, nil, "y") }()
	<-got // ensure the checker is blocking on the channel
	cancel()
	select {
	case allow := <-result:
		if allow {
			t.Fatal("RequestApproval returned true on context cancel; want deny")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval did not return on context cancel")
	}
}

func TestRespondApproval_UnknownCallID(t *testing.T) {
	c := &Client{}
	if err := c.RespondApproval("does-not-exist", true); err == nil {
		t.Fatal("RespondApproval(unknown) should error")
	}
}

// ── Ownership + timeout (P07.C, CONTRACT.md section 3) ────────────────────

// TestRequestApprovalInteractiveWithTimeout_FiresAfterRealTimeout proves the
// timeout is real (a real time.Duration against the real clock, not a mock):
// with no responder and a ctx that is never cancelled, the call must return
// false only once the timeout has actually elapsed — not before.
func TestRequestApprovalInteractiveWithTimeout_FiresAfterRealTimeout(t *testing.T) {
	c := &Client{}
	const timeout = 75 * time.Millisecond

	start := time.Now()
	result := make(chan bool, 1)
	go func() {
		result <- c.RequestApprovalInteractiveWithTimeout(context.Background(), "slow ui", nil, timeout, "")
	}()

	select {
	case allow := <-result:
		elapsed := time.Since(start)
		if allow {
			t.Fatal("expected timeout to deny (false), got true")
		}
		if elapsed < timeout {
			t.Fatalf("returned after %v, before the %v timeout elapsed", elapsed, timeout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApprovalInteractiveWithTimeout never returned after its timeout")
	}

	// Cleanup on every exit path: pending map and ownership registry must
	// not leak the timed-out call.
	if pend := c.PendingApprovals(); len(pend) != 0 {
		t.Fatalf("pending approvals not cleaned up after timeout: %v", pend)
	}
}

// TestRequestApprovalInteractiveWithTimeout_DoesNotFireEarly proves the
// inverse of the above: a responder that answers well BEFORE the timeout
// must win, i.e. the timeout must not fire early / race the real answer.
func TestRequestApprovalInteractiveWithTimeout_DoesNotFireEarly(t *testing.T) {
	c := &Client{}
	got, unsub := awaitApprovalCallID(t, c)
	defer unsub()

	const timeout = 500 * time.Millisecond
	result := make(chan bool, 1)
	go func() {
		result <- c.RequestApprovalInteractiveWithTimeout(context.Background(), "fast ui", nil, timeout, "")
	}()

	var callID string
	select {
	case callID = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no EventApprovalRequested emitted")
	}
	if err := c.RespondApproval(callID, true); err != nil {
		t.Fatalf("RespondApproval: %v", err)
	}
	select {
	case allow := <-result:
		if !allow {
			t.Fatal("expected true from a real answer delivered before timeout")
		}
	case <-time.After(timeout):
		t.Fatal("answer did not unblock the call before its own timeout")
	}
}

// TestApprovalOwnership_MismatchedSessionDoesNotResolve proves an answer
// from a session ID that does not match ApprovalOwnership.OwnerSessionID
// does not resolve the approval (RespondApprovalWithSession must error and
// leave the approval pending), while the correct owning session's answer
// does resolve it.
func TestApprovalOwnership_MismatchedSessionDoesNotResolve(t *testing.T) {
	c := &Client{}
	got, unsub := awaitApprovalCallID(t, c)
	defer unsub()

	const ownerSessionID = "session-correct"
	result := make(chan bool, 1)
	go func() {
		result <- c.RequestApprovalInteractiveWithTimeout(context.Background(), "owned", nil, DefaultApprovalTimeout, ownerSessionID)
	}()

	var callID string
	select {
	case callID = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no EventApprovalRequested emitted")
	}

	// A mismatched session's answer must be refused and must not resolve
	// the pending approval.
	if err := c.RespondApprovalWithSession(callID, true, "session-wrong"); err == nil {
		t.Fatal("expected RespondApprovalWithSession from a mismatched session to error")
	}
	select {
	case allow := <-result:
		t.Fatalf("approval resolved (%v) by a mismatched session; must remain pending", allow)
	case <-time.After(100 * time.Millisecond):
		// still pending, as required.
	}
	if pend := c.PendingApprovals(); len(pend) != 1 || pend[0] != callID {
		t.Fatalf("expected approval %q to remain pending after mismatched-session answer, got %v", callID, pend)
	}

	// The correct owning session's answer must resolve it.
	if err := c.RespondApprovalWithSession(callID, true, ownerSessionID); err != nil {
		t.Fatalf("RespondApprovalWithSession(correct owner): %v", err)
	}
	select {
	case allow := <-result:
		if !allow {
			t.Fatal("expected true from the correct owning session's answer")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval did not resolve after the correct owning session answered")
	}
	if pend := c.PendingApprovals(); len(pend) != 0 {
		t.Fatalf("pending not cleaned up: %v", pend)
	}
}

// TestApprovalOwnership_EmptyOwnerAllowsAnySession confirms the
// no-ownership-restriction default (OwnerSessionID == "", what every
// existing call site in this phase uses) is unchanged: any session
// (including the empty one RespondApproval uses) may resolve it.
func TestApprovalOwnership_EmptyOwnerAllowsAnySession(t *testing.T) {
	c := &Client{}
	got, unsub := awaitApprovalCallID(t, c)
	defer unsub()

	result := make(chan bool, 1)
	go func() {
		result <- c.RequestApprovalInteractiveWithTimeout(context.Background(), "unowned", nil, DefaultApprovalTimeout, "")
	}()
	callID := <-got
	if err := c.RespondApprovalWithSession(callID, true, "any-session-whatsoever"); err != nil {
		t.Fatalf("RespondApprovalWithSession with no ownership restriction should succeed from any session: %v", err)
	}
	if !<-result {
		t.Fatal("expected true")
	}
}
