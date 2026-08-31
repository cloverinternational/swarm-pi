// cmd/swarmos/daemon_approval.go
//
// daemonApprovalBroker routes tool-call approvals to a remote attached UI over the
// daemon's event stream. On Request it emits EventApprovalRequested via the SDK
// client and BLOCKS until the attached client calls respondApproval — so
// interactive tool approval works through `swarmos attach` instead of the headless
// auto-approve broker. This activates the client's interactive-approval mechanism
// (G1) end-to-end through the daemon.
package main

import (
	"context"
	"fmt"
	"time"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

type daemonApprovalBroker struct {
	client *sdkclient.Client
}

// SetClient wires the SDK client after the SDKIntegration is built (the broker is
// constructed before the client exists, so this resolves the chicken-and-egg).
func (b *daemonApprovalBroker) SetClient(c *sdkclient.Client) { b.client = c }

// Request emits an approval event to the attached UI and blocks for its decision.
//
// Timeout/ownership wiring (Phase 07.C, CONTRACT.md section 3): this reuses
// sdkclient.Client.RequestApprovalInteractiveWithTimeout so a stuck/absent
// attached UI can never hang a tool call forever (previously it only reacted
// to ctx.Done()). req.Timeout (seconds, tools.PermissionApprovalRequest) is
// honored when the caller set a positive value; otherwise the client's
// sdkclient.DefaultApprovalTimeout applies. req itself carries no
// client_session_id field this phase — that field belongs to
// tools.PermissionApprovalRequest / tools.ApprovalContext, both outside this
// worker's # FILES: list and outside this phase's scope (see
// CONTRACT.md section 3: only client/approval.go and this file's read of
// ApprovalOwnership's exact field names are Worker C's territory). Until a
// later phase threads a real client_session_id through
// tools.PermissionApprovalRequest, req.Context.AgentID (when present) is the
// closest existing identifier for "who is asking on behalf of", so it is
// passed as the ownerSessionID hint; when absent, ownerSessionID is "" —
// exactly RequestApprovalInteractive's own no-ownership-restriction default —
// so today's any-session-may-answer behavior is unchanged until real session
// identity is wired end-to-end.
func (b *daemonApprovalBroker) Request(ctx context.Context, req tools.PermissionApprovalRequest) (tools.ApprovalResponse, error) {
	if b.client == nil {
		// Client not wired yet — fail safe (approve once, matching headless) so a
		// tool call before attach doesn't hang the daemon.
		return tools.ApprovalResponse{Decision: tools.DecisionApproveOnce, Outcome: tools.OutcomeApproved}, nil
	}
	reason := req.Reason
	if reason == "" {
		reason = fmt.Sprintf("%s requests %s", req.Tool, req.Permission)
	}
	timeout := sdkclient.DefaultApprovalTimeout
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}
	ownerSessionID := ""
	if req.Context != nil {
		ownerSessionID = req.Context.AgentID
	}
	allow := b.client.RequestApprovalInteractiveWithTimeout(ctx, reason, []tools.Permission{tools.Permission(req.Permission)}, timeout, ownerSessionID)
	if allow {
		return tools.ApprovalResponse{Decision: tools.DecisionApproveOnce, Outcome: tools.OutcomeApproved}, nil
	}
	return tools.ApprovalResponse{Decision: tools.DecisionDeny, Outcome: tools.OutcomeDenied}, nil
}

// Respond / SetConfig / GetPendingRequests complete the tools.ApprovalBroker
// interface. The approval round-trip is owned by the SDK client's registry
// (RequestApprovalInteractive blocks; the attached UI resolves it via
// client.respondApproval over /rpc), so these are no-ops here.
func (b *daemonApprovalBroker) Respond(requestID string, decision tools.Decision) error { return nil }
func (b *daemonApprovalBroker) SetConfig(config tools.PermissionConfig)                 {}
func (b *daemonApprovalBroker) GetPendingRequests() []tools.PermissionApprovalRequest   { return nil }
