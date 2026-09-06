// Package client — approval.go
//
// Interactive tool-call approval over the daemon interface (gap G1). A remote UI
// (TUI/webapp) can approve/deny each tool call without owning any logic: the
// interactive PermissionChecker emits EventApprovalRequested and blocks until
// the UI calls RespondApproval(CallID, allow). This is what makes a thin client
// capable of TUI-parity interactive approval through the client alone.
package client

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ApprovalRequest is the EventApprovalRequested payload. A UI renders it as an
// approval prompt and replies via RespondApproval(CallID, allow).
type ApprovalRequest struct {
	CallID      string             `json:"callID"`
	Reason      string             `json:"reason,omitempty"`
	Permissions []tools.Permission `json:"permissions,omitempty"`
}

// ApprovalOwnership records which client session may answer a given pending
// approval (CallID) and the bounded window it must answer within. Field
// names are FIXED by
// .swarmflow/swarm-attach-architecture/p07-cli-sessions-thin-client/CONTRACT.md
// section 3 byte-for-byte (CallID, OwnerSessionID, RequestedAt, TimeoutAt) —
// do not rename, and do not introduce a parallel ClientID/Owner/Deadline
// spelling anywhere else in this package or in daemon_approval.go.
// OwnerSessionID is the string form of identity.ClientSessionID
// (internal/identity/identity.go), minted by attachclient/serve (Worker B's
// territory in this phase); this package only ever consumes/compares the
// string form, never mints or parses it. An empty OwnerSessionID means "no
// ownership restriction" (any session's answer resolves the approval),
// preserving today's behavior for callers that don't yet have a session
// identity to attach (for example daemon_approval.go, whose
// tools.PermissionApprovalRequest carries no client_session_id field this
// phase — see the comment on daemonApprovalBroker.Request).
type ApprovalOwnership struct {
	CallID         string
	OwnerSessionID string // identity.ClientSessionID.String()
	RequestedAt    time.Time
	TimeoutAt      time.Time
}

// DefaultApprovalTimeout bounds how long RequestApprovalInteractive waits
// for a UI decision before auto-denying, so an approval request can never
// hang the daemon/agent loop forever when only ctx.Done() would otherwise
// unblock it (the previous behavior: no timeout of its own). Five minutes
// is a generous bound for a human to look at a prompt and decide, while
// still guaranteeing eventual forward progress if the UI never answers.
// Callers that need a different bound use
// RequestApprovalInteractiveWithTimeout directly.
const DefaultApprovalTimeout = 5 * time.Minute

// ownershipKey scopes the package-level ownership registry by both the
// owning *Client and CallID. CallID alone is only unique per-Client (each
// Client has its own approvalSeq counter starting at 0), so a bare
// map[string]ApprovalOwnership could collide across two concurrently live
// Client instances (as approval_test.go and daemon tests both construct).
// This lets approval.go track per-call ownership metadata without adding a
// new field to the Client struct in client.go, which is outside this
// worker's # FILES: list.
type ownershipKey struct {
	c      *Client
	callID string
}

var (
	ownershipMu  sync.Mutex
	ownershipReg = map[ownershipKey]ApprovalOwnership{}
)

// interactiveApprovalChecker embeds a base PermissionChecker (so Check /
// CheckWithContext / Grant / Revoke behave normally) and overrides only
// RequestApproval to route the interactive decision to a remote UI over the
// daemon event/command channel.
type interactiveApprovalChecker struct {
	tools.PermissionChecker
	c *Client
}

// RequestApproval emits EventApprovalRequested and blocks until the UI resolves
// it via RespondApproval, or ctx is cancelled (which denies).
func (ic *interactiveApprovalChecker) RequestApproval(ctx context.Context, required []tools.Permission, reason string) bool {
	return ic.c.RequestApprovalInteractive(ctx, reason, required)
}

// RequestApprovalInteractive emits EventApprovalRequested and blocks until the UI
// resolves it via RespondApproval, or ctx is cancelled (which denies). Public so
// daemon/embedding code (e.g. an ApprovalBroker that routes approvals to a remote
// attached UI) can reuse the same per-call event+registry mechanism, not just the
// interactive PermissionChecker.
//
// This is the bounded-default sibling of RequestApprovalInteractiveWithTimeout
// (DefaultApprovalTimeout, no ownership restriction) kept with its original
// signature so every existing call site in this package (and
// daemon_approval.go, which only reads the exported API) keeps compiling
// unchanged.
func (c *Client) RequestApprovalInteractive(ctx context.Context, reason string, required []tools.Permission) bool {
	return c.RequestApprovalInteractiveWithTimeout(ctx, reason, required, DefaultApprovalTimeout, "")
}

// RequestApprovalInteractiveWithTimeout is RequestApprovalInteractive with an
// explicit, configurable timeout and an optional owning session
// (ApprovalOwnership.OwnerSessionID). It emits EventApprovalRequested and
// blocks until exactly one of:
//
//   - the UI resolves it via RespondApproval/RespondApprovalWithSession from
//     the matching owner session (or any session, when ownerSessionID == ""),
//   - ctx is cancelled (deny),
//   - or timeout elapses (deny) — this is new: previously
//     RequestApprovalInteractive could block forever on a UI that never
//     answered and never cancelled ctx.
//
// c.pendingApprovals and the ownership registry are always cleaned up on
// every exit path via defer, mirroring the pre-existing cleanup pattern, so
// no goroutine or map entry is ever leaked regardless of which case fires.
func (c *Client) RequestApprovalInteractiveWithTimeout(ctx context.Context, reason string, required []tools.Permission, timeout time.Duration, ownerSessionID string) bool {
	callID := "appr-" + strconv.FormatUint(c.approvalSeq.Add(1), 10)
	ch := make(chan bool, 1)

	c.approvalMu.Lock()
	if c.pendingApprovals == nil {
		c.pendingApprovals = make(map[string]chan bool)
	}
	c.pendingApprovals[callID] = ch
	c.approvalMu.Unlock()

	now := time.Now()
	key := ownershipKey{c: c, callID: callID}
	ownershipMu.Lock()
	ownershipReg[key] = ApprovalOwnership{
		CallID:         callID,
		OwnerSessionID: ownerSessionID,
		RequestedAt:    now,
		TimeoutAt:      now.Add(timeout),
	}
	ownershipMu.Unlock()

	defer func() {
		c.approvalMu.Lock()
		delete(c.pendingApprovals, callID)
		c.approvalMu.Unlock()
		ownershipMu.Lock()
		delete(ownershipReg, key)
		ownershipMu.Unlock()
	}()

	c.dispatchEvent(Event{
		Kind:    EventApprovalRequested,
		Payload: ApprovalRequest{CallID: callID, Reason: reason, Permissions: required},
		At:      now,
	})

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case allow := <-ch:
		return allow
	case <-ctx.Done():
		return false
	case <-timer.C:
		// ADR-005-adjacent bounded-wait discipline: an unanswered approval
		// must not hang the caller forever. Deny on timeout, exactly like a
		// context cancellation.
		return false
	}
}

// NewInteractiveApprovalChecker returns a PermissionChecker that routes each
// interactive tool-call approval to a remote UI via EventApprovalRequested +
// RespondApproval. Wire it as the tool registry / agent permission checker to
// enable daemon-driven interactive approval.
//
// RUN-GATED: wiring this into the live tool-execution path (and confirming the
// block/resolve round-trips end-to-end) needs a real TUI/webapp smoke test.
func (c *Client) NewInteractiveApprovalChecker() tools.PermissionChecker {
	return &interactiveApprovalChecker{
		PermissionChecker: tools.NewSimplePermissionChecker(tools.DefaultPermissionPolicies()),
		c:                 c,
	}
}

// RespondApproval resolves a pending interactive approval (announced via
// EventApprovalRequested). allow=true permits the tool call, false denies it.
// Safe to call from any goroutine / the daemon transport.
//
// This is the session-agnostic sibling of RespondApprovalWithSession (kept
// with its original signature so serve/client_methods.go's existing
// client.respondApproval RPC handler, outside this worker's # FILES: list,
// keeps compiling and behaving unchanged): it answers with an empty session
// ID, which only resolves calls that have no OwnerSessionID restriction (the
// default for every call site in this package today).
func (c *Client) RespondApproval(callID string, allow bool) error {
	return c.RespondApprovalWithSession(callID, allow, "")
}

// RespondApprovalWithSession resolves a pending interactive approval
// (announced via EventApprovalRequested) on behalf of sessionID. If the
// approval has an OwnerSessionID recorded (RequestApprovalInteractiveWithTimeout
// was given a non-empty ownerSessionID) and sessionID does not match it, the
// answer is refused (an error is returned) and the approval is left pending
// — it is NOT resolved, so a subsequent answer from the correct owning
// session can still resolve it, and an owner that never answers still denies
// via the ordinary ctx.Done()/timeout path. Safe to call from any goroutine /
// the daemon transport.
func (c *Client) RespondApprovalWithSession(callID string, allow bool, sessionID string) error {
	c.approvalMu.Lock()
	ch, ok := c.pendingApprovals[callID]
	c.approvalMu.Unlock()
	if !ok {
		return fmt.Errorf("RespondApproval: no pending approval %q (already resolved or unknown)", callID)
	}

	key := ownershipKey{c: c, callID: callID}
	ownershipMu.Lock()
	ownership, hasOwnership := ownershipReg[key]
	ownershipMu.Unlock()
	if hasOwnership && ownership.OwnerSessionID != "" && ownership.OwnerSessionID != sessionID {
		return fmt.Errorf("RespondApproval: call %q is owned by a different session (refusing answer from mismatched session)", callID)
	}

	select {
	case ch <- allow:
		return nil
	default:
		return fmt.Errorf("RespondApproval: approval %q already resolved", callID)
	}
}

// PendingApprovals returns the call IDs currently awaiting a decision, so a UI
// that (re)connects can re-render outstanding approval prompts.
func (c *Client) PendingApprovals() []string {
	c.approvalMu.Lock()
	defer c.approvalMu.Unlock()
	ids := make([]string, 0, len(c.pendingApprovals))
	for id := range c.pendingApprovals {
		ids = append(ids, id)
	}
	return ids
}
