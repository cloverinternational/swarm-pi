package builtin

import (
	"context"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

// beginTwoPerson starts a Two-Person Integrity approval for a sensitive
// credential. It seeds the broker request with the CURRENT user's share (loaded
// from the provider's identity path) and returns a requestId. A DISTINCT second
// person must then approve via vault_approve, after which vault_exec is retried
// with twoPersonRequestId set to finalize and run the command.
func beginTwoPerson(ctx context.Context, provider vault.VaultProvider, executor *vault.Executor, req vault.ExecutionRequest) (*tools.ToolResult, error) {
	identityPath := provider.GetIdentityPath()
	if identityPath == "" {
		return jsonResult(VaultExecResult{
			Error: "this credential requires two-person approval, but no user identity is configured; the user must create an identity (swarmos vault identity) and be a recipient",
		}), nil
	}
	identity, err := vault.LoadX25519Identity(identityPath)
	if err != nil {
		return jsonResult(VaultExecResult{
			Error: fmt.Sprintf("failed to load user identity for two-person approval: %v", err),
		}), nil
	}

	tpReq, err := executor.BeginTwoPerson(ctx, req, identity)
	if err != nil {
		return jsonResult(VaultExecResult{
			Error: fmt.Sprintf("could not begin two-person approval: %v", err),
		}), nil
	}

	return jsonResult(VaultExecResult{
		Status:             "needs_two_person",
		TwoPersonRequestID: tpReq.ID,
		Threshold:          tpReq.Threshold,
		Contributors:       tpReq.Contributors,
		Warning: fmt.Sprintf(
			"This credential is protected by two-person integrity and needs %d distinct approvers (you count as 1). "+
				"Ask a DIFFERENT authorized person to approve request %q (via vault_approve or 'swarmos vault approve %s'). "+
				"Only AFTER that person's vault_approve returns satisfied:true, call vault_exec again with twoPersonRequestId set to %q to run the command "+
				"(the command+args are fixed at this step and cannot be changed on retry).",
			tpReq.Threshold, tpReq.ID, tpReq.ID, tpReq.ID),
	}), nil
}

// VaultTwoPersonStatusParams are parameters for the vault_two_person_status tool.
type VaultTwoPersonStatusParams struct {
	// UnlockIfNeeded asks an interactive host to unlock a locked vault.
	UnlockIfNeeded bool `json:"unlockIfNeeded,omitempty" description:"If true, ask the host to unlock a locked vault; TUI support is optional and headless hosts return immediately"`

	// RequestID is the two-person request id from a prior needs_two_person response.
	RequestID string `json:"requestId" description:"Two-person request id to inspect (from a needs_two_person response)" required:"true"`
}

// VaultTwoPersonStatusResult reports the live state of a pending two-person request.
type VaultTwoPersonStatusResult struct {
	// RequestID echoes the inspected request id.
	RequestID string `json:"requestId,omitempty"`

	// CredentialID names the credential being accessed.
	CredentialID string `json:"credentialId,omitempty"`

	// Command is the canonical command+args fixed at begin time.
	Command string `json:"command,omitempty"`

	// Threshold is the number of distinct approvers required.
	Threshold int `json:"threshold,omitempty"`

	// Approvals is how many distinct approvers have contributed so far.
	Approvals int `json:"approvals"`

	// Remaining is how many more distinct approvers are still needed.
	Remaining int `json:"remaining"`

	// Satisfied is true when Approvals >= Threshold and the requester may
	// retry vault_exec with twoPersonRequestId set to run the command.
	Satisfied bool `json:"satisfied"`

	// Contributors lists the approver public keys that have contributed so far.
	Contributors []string `json:"contributors,omitempty"`

	// ExpiresAt is the RFC3339 time the pending request expires.
	ExpiresAt string `json:"expiresAt,omitempty"`

	// Expired is true when the request has already lapsed (or was consumed).
	Expired bool `json:"expired,omitempty"`

	// Error message if the status could not be read.
	Error string `json:"error,omitempty"`

	// Warning / next-step guidance.
	Warning string `json:"warning,omitempty"`

	// Status reports structured unlock outcomes when applicable.
	Status string `json:"status,omitempty"`
}

// VaultTwoPersonStatusTool lets an agent poll a pending two-person request to
// learn whether enough distinct approvers have approved yet — without mutating
// broker state (unlike vault_approve). This closes the "how do I know when to
// retry vault_exec" gap for the original requester.
type VaultTwoPersonStatusTool struct {
	tools.BaseTool
	unlocker VaultUnlocker
}

// NewVaultTwoPersonStatusTool creates a new vault_two_person_status tool.
func NewVaultTwoPersonStatusTool() tools.Tool {
	return NewVaultTwoPersonStatusToolWithUnlocker(nil)
}

// NewVaultTwoPersonStatusToolWithUnlocker creates the status tool with an optional host unlock broker.
func NewVaultTwoPersonStatusToolWithUnlocker(unlocker VaultUnlocker) tools.Tool {
	return tools.Typed[VaultTwoPersonStatusParams](&VaultTwoPersonStatusTool{unlocker: unlocker})
}

// Name returns the tool name.
func (t *VaultTwoPersonStatusTool) Name() string { return "vault_two_person_status" }

// Description returns the tool description.
func (t *VaultTwoPersonStatusTool) Description() string {
	return `Check a pending two-person credential request. Set unlockIfNeeded when the vault may be locked.`
}

// Parameters returns the parameter schema.
func (t *VaultTwoPersonStatusTool) Parameters() any {
	return tools.SchemaFor[VaultTwoPersonStatusParams]()
}

// IsIdempotent returns true — reading status has no side effects.
func (t *VaultTwoPersonStatusTool) IsIdempotent() bool { return true }

// RequiresPermission returns secret-access permission.
func (t *VaultTwoPersonStatusTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionSecretAccess}
}

// SupportedContentTypes returns supported types.
func (t *VaultTwoPersonStatusTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// Run reports the status of a pending two-person request.
func (t *VaultTwoPersonStatusTool) Run(ctx context.Context, params VaultTwoPersonStatusParams) (*tools.ToolResult, error) {
	if params.RequestID == "" {
		return jsonResult(VaultTwoPersonStatusResult{Error: "requestId is required"}), nil
	}
	unlock := ensureVaultUnlocked(ctx, params.UnlockIfNeeded, t.unlocker, t.Name(), "project")
	provider := unlock.provider
	if unlock.status != "" {
		return jsonResult(VaultTwoPersonStatusResult{Status: unlock.status, Error: unlock.err}), nil
	}
	if !provider.IsEnabled() {
		return jsonResult(VaultTwoPersonStatusResult{
			Error: "vault is locked — unlock it (type /vault in the TUI) before checking status",
		}), nil
	}

	info, ok := provider.GetExecutor().TwoPersonRequestInfo(params.RequestID)
	if !ok {
		// Not pending: either it was never begun, already finalized (consumed),
		// or it expired and was swept. Report it as gone rather than erroring so
		// a polling agent can stop cleanly.
		return jsonResult(VaultTwoPersonStatusResult{
			RequestID: params.RequestID,
			Expired:   true,
			Error:     "unknown, already finalized, or expired request",
			Warning:   "This request is no longer pending. If it was finalized, the command already ran; if it expired, start over with vault_exec.",
		}), nil
	}

	approvals := len(info.Contributors)
	remaining := info.Threshold - approvals
	if remaining < 0 {
		remaining = 0
	}
	satisfied := approvals >= info.Threshold
	expired := time.Now().After(info.ExpiresAt)

	warning := fmt.Sprintf(
		"%d of %d distinct approvers so far. Need %d more distinct approver(s) via vault_approve %q.",
		approvals, info.Threshold, remaining, info.ID)
	if satisfied {
		warning = "Approval threshold met. The ORIGINAL requester can now retry vault_exec with twoPersonRequestId set to run the command."
	}
	if expired {
		warning = "This request has expired. Start over with vault_exec."
	}

	return jsonResult(VaultTwoPersonStatusResult{
		RequestID:    info.ID,
		CredentialID: info.CredentialID,
		Command:      info.Command,
		Threshold:    info.Threshold,
		Approvals:    approvals,
		Remaining:    remaining,
		Satisfied:    satisfied,
		Contributors: info.Contributors,
		ExpiresAt:    info.ExpiresAt.Format(time.RFC3339),
		Expired:      expired,
		Warning:      warning,
	}), nil
}

// VaultApproveParams are parameters for the vault_approve tool.
type VaultApproveParams struct {
	// UnlockIfNeeded asks an interactive host to unlock a locked vault.
	UnlockIfNeeded bool `json:"unlockIfNeeded,omitempty" description:"If true, ask the host to unlock a locked vault; TUI support is optional and headless hosts return immediately"`

	// RequestID is the two-person request id from a prior needs_two_person response.
	RequestID string `json:"requestId" description:"Two-person request id to approve (from a needs_two_person response)" required:"true"`

	// IdentityPath optionally points to the APPROVER's identity file. If empty,
	// the configured user identity is used (only useful when the approver is a
	// different configured user on the same machine).
	IdentityPath string `json:"identityPath,omitempty" description:"Path to the approver's age identity file (defaults to the configured user identity)"`
}

// VaultApproveResult is the result of vault_approve.
type VaultApproveResult struct {
	// Satisfied is true when enough distinct approvers have now approved.
	Satisfied bool `json:"satisfied"`

	// RequestID echoes the approved request id.
	RequestID string `json:"requestId,omitempty"`

	// Error message if approval failed.
	Error string `json:"error,omitempty"`

	// Warning / next-step guidance.
	Warning string `json:"warning,omitempty"`

	// Status reports structured unlock outcomes when applicable.
	Status string `json:"status,omitempty"`
}

// VaultApproveTool lets a SECOND, distinct principal approve a two-person
// credential access request.
type VaultApproveTool struct {
	tools.BaseTool
	unlocker VaultUnlocker
}

// NewVaultApproveTool creates a new vault_approve tool.
func NewVaultApproveTool() tools.Tool {
	return NewVaultApproveToolWithUnlocker(nil)
}

// NewVaultApproveToolWithUnlocker creates a vault_approve tool with an optional host unlock broker.
func NewVaultApproveToolWithUnlocker(unlocker VaultUnlocker) tools.Tool {
	return tools.Typed[VaultApproveParams](&VaultApproveTool{unlocker: unlocker})
}

// Name returns the tool name.
func (t *VaultApproveTool) Name() string { return "vault_approve" }

// Description returns the tool description.
func (t *VaultApproveTool) Description() string {
	return `Approve a pending two-person credential request. Set unlockIfNeeded when the vault may be locked.`
}

// Parameters returns the parameter schema.
func (t *VaultApproveTool) Parameters() any { return tools.SchemaFor[VaultApproveParams]() }

// IsIdempotent returns false — approving changes broker state.
func (t *VaultApproveTool) IsIdempotent() bool { return false }

// RequiresPermission returns secret-access permission.
func (t *VaultApproveTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionSecretAccess}
}

// SupportedContentTypes returns supported types.
func (t *VaultApproveTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// Run approves a two-person request.
func (t *VaultApproveTool) Run(ctx context.Context, params VaultApproveParams) (*tools.ToolResult, error) {
	if params.RequestID == "" {
		return jsonResult(VaultApproveResult{Error: "requestId is required"}), nil
	}
	unlock := ensureVaultUnlocked(ctx, params.UnlockIfNeeded, t.unlocker, t.Name(), "project")
	provider := unlock.provider
	if unlock.status != "" {
		return jsonResult(VaultApproveResult{Status: unlock.status, Error: unlock.err}), nil
	}
	if !provider.IsEnabled() {
		return jsonResult(VaultApproveResult{
			Error: "vault is locked — unlock it (type /vault in the TUI) before approving",
		}), nil
	}

	identityPath := params.IdentityPath
	if identityPath == "" {
		identityPath = provider.GetIdentityPath()
	}
	if identityPath == "" {
		return jsonResult(VaultApproveResult{
			Error: "no approver identity configured; set identityPath or configure a user identity",
		}), nil
	}
	identity, err := vault.LoadX25519Identity(identityPath)
	if err != nil {
		return jsonResult(VaultApproveResult{
			Error: fmt.Sprintf("failed to load approver identity: %v", err),
		}), nil
	}

	satisfied, err := provider.GetExecutor().ApproveTwoPerson(params.RequestID, identity)
	if err != nil {
		return jsonResult(VaultApproveResult{
			RequestID: params.RequestID,
			Error:     err.Error(),
		}), nil
	}

	warning := "Approval recorded. More distinct approvers are still required."
	if satisfied {
		warning = "Approval threshold met. The original requester can now retry vault_exec with twoPersonRequestId set to run the command."
	}
	return jsonResult(VaultApproveResult{
		Satisfied: satisfied,
		RequestID: params.RequestID,
		Warning:   warning,
	}), nil
}
