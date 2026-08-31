package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

// VaultExecParams are parameters for the vault_exec tool.
type VaultExecParams struct {
	// UnlockIfNeeded asks an interactive host to unlock a locked vault.
	UnlockIfNeeded bool `json:"unlockIfNeeded,omitempty" description:"If true, ask the host to unlock a locked vault; TUI support is optional and headless hosts return immediately"`

	// CredentialID is the ID of the credential to use.
	// Use vault_list to see available credentials.
	CredentialID string `json:"credentialId" description:"ID of the credential to use (list with vault_list)" required:"true"`

	// Command is the command to execute.
	Command string `json:"command" description:"Command to execute with the credential" required:"true"`

	// Args are additional command arguments.
	Args []string `json:"args,omitempty" description:"Command arguments"`

	// WorkingDir is the working directory for the command.
	WorkingDir string `json:"workingDir,omitempty" description:"Working directory for command execution"`

	// Timeout in seconds (default 60).
	Timeout int `json:"timeout,omitempty" description:"Timeout in seconds (default 60)"`

	// Host is the target host (extracted from command if not provided).
	Host string `json:"host,omitempty" description:"Target host (auto-extracted from command if not provided)"`

	// Reason explains why the credential is needed.
	Reason string `json:"reason,omitempty" description:"Why this credential is needed (for approval prompts)"`

	// ApprovalID resumes a previously returned needs_approval response once the
	// user has approved it in the TUI.
	ApprovalID string `json:"approvalId,omitempty" description:"Approval ID from a prior needs_approval response, to retry after the user approves"`

	// TwoPersonRequestID finalizes a two-person credential once a distinct second
	// approver has approved it via vault_approve.
	TwoPersonRequestID string `json:"twoPersonRequestId,omitempty" description:"Request ID from a prior needs_two_person response, to finalize+run after a second person has approved via vault_approve"`
}

// VaultExecResult is the result of vault_exec.
type VaultExecResult struct {
	// Stdout from the command (redacted).
	Stdout string `json:"stdout"`

	// Stderr from the command (redacted).
	Stderr string `json:"stderr"`

	// ExitCode of the process.
	ExitCode int `json:"exitCode"`

	// Duration of execution.
	Duration string `json:"duration"`

	// RedactedCount is the number of redactions performed.
	RedactedCount int `json:"redactedCount"`

	// RedactionHints indicates what was redacted (without values).
	RedactionHints []string `json:"redactionHints,omitempty"`

	// SafeToParse is true when no redactions occurred - the output is the real,
	// unaltered command output and safe to parse programmatically. When false,
	// redaction may have changed structurally significant bytes.
	SafeToParse bool `json:"safeToParse"`

	// Error message if execution failed.
	Error string `json:"error,omitempty"`

	// Status is "ok" on success or "needs_approval" when the user must approve.
	Status string `json:"status,omitempty"`

	// ApprovalID is set when Status is "needs_approval". After the user approves
	// in the TUI, call vault_exec again with the SAME command and this approvalId.
	ApprovalID string `json:"approvalId,omitempty"`

	// TwoPersonRequestID is set when Status is "needs_two_person". A DISTINCT
	// second person must call vault_approve with this id, then vault_exec is
	// retried with twoPersonRequestId set to finalize and run the command.
	TwoPersonRequestID string `json:"twoPersonRequestId,omitempty"`

	// Threshold is the number of distinct approvers required (two-person creds).
	Threshold int `json:"threshold,omitempty"`

	// Contributors lists the approver public keys that have contributed so far.
	Contributors []string `json:"contributors,omitempty"`

	// Warning about redaction and security.
	Warning string `json:"warning,omitempty"`
}

// VaultExecTool executes commands with stored credentials.
// Credentials are never exposed - only used internally for execution.
// Output is redacted to prevent credential leakage.
type VaultExecTool struct {
	tools.BaseTool
	unlocker VaultUnlocker
}

// NewVaultExecTool creates a new vault_exec tool.
func NewVaultExecTool() tools.Tool {
	return NewVaultExecToolWithUnlocker(nil)
}

// NewVaultExecToolWithUnlocker creates a vault_exec tool with an optional host unlock broker.
func NewVaultExecToolWithUnlocker(unlocker VaultUnlocker) tools.Tool {
	return tools.Typed[VaultExecParams](&VaultExecTool{unlocker: unlocker})
}

// Name returns the tool name.
func (t *VaultExecTool) Name() string { return "vault_exec" }

// Description returns the tool description.
func (t *VaultExecTool) Description() string {
	return `Execute a command with a stored credential injected into its environment. Find credential IDs with vault_list; set unlockIfNeeded when the vault may be locked.`
}

// Parameters returns the parameter schema.
func (t *VaultExecTool) Parameters() any { return tools.SchemaFor[VaultExecParams]() }

// IsIdempotent returns false - commands may have side effects.
func (t *VaultExecTool) IsIdempotent() bool { return false }

// RequiresPermission returns the required permissions.
func (t *VaultExecTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionSecretAccess}
}

// SupportedContentTypes returns supported types.
func (t *VaultExecTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// Run executes the vault command.
func (t *VaultExecTool) Run(ctx context.Context, params VaultExecParams) (*tools.ToolResult, error) {
	if params.CredentialID == "" {
		return jsonResult(VaultExecResult{
			Error: "credentialId is required",
		}), nil
	}

	if params.Command == "" {
		return jsonResult(VaultExecResult{
			Error: "command is required",
		}), nil
	}

	unlock := ensureVaultUnlocked(ctx, params.UnlockIfNeeded, t.unlocker, t.Name(), "")
	provider := unlock.provider
	if unlock.status != "" {
		return jsonResult(VaultExecResult{Status: unlock.status, Error: unlock.err}), nil
	}
	if !provider.IsEnabled() {
		return jsonResult(VaultExecResult{
			Error: "vault is locked — no credentials available. Tell the user to unlock the vault by typing /vault in the TUI (or Settings → Vault, or 'swarmos vault unlock' in CLI). Use vault_list first to see available credentials.",
		}), nil
	}

	// An unspecified unlock may legitimately be satisfied by an available global
	// vault. If the requested credential is not there and this workspace also has
	// a locked project vault, unlock that exact scope before approval or command
	// execution. This keeps global credentials prompt-free and guarantees that a
	// failed lookup cannot cause a command to run twice.
	if params.UnlockIfNeeded &&
		provider.GetProjectID() != "" &&
		!vault.IsProviderScopeEnabled(provider, vault.ScopeProject) &&
		provider.GetVault() != nil {
		credentials, lookupErr := provider.GetVault().List(ctx, vault.CredentialFilter{
			Scope: vault.ScopeGlobal,
		}, "")
		if lookupErr != nil {
			return jsonResult(VaultExecResult{
				Error: fmt.Sprintf("could not inspect available credentials before project unlock: %v", lookupErr),
			}), nil
		}
		credentialFound := false
		for _, credential := range credentials {
			if credential.ID == params.CredentialID {
				credentialFound = true
				break
			}
		}
		if !credentialFound {
			unlock = ensureVaultUnlocked(ctx, true, t.unlocker, t.Name(), string(vault.ScopeProject))
			provider = unlock.provider
			if unlock.status != "" {
				return jsonResult(VaultExecResult{Status: unlock.status, Error: unlock.err}), nil
			}
		}
	}

	projectID := ""
	if vault.IsProviderScopeEnabled(provider, vault.ScopeProject) {
		projectID = provider.GetProjectID()
	}

	// Parse the command string into command + args if Args is not provided.
	// This lets callers pass a single shell-style command string like
	// "aws s3 ls s3://bucket" instead of splitting command and args manually.
	command := params.Command
	args := params.Args
	if len(args) == 0 {
		parts := splitShellCommand(command)
		if len(parts) > 0 {
			command = parts[0]
			args = parts[1:]
		}
	}

	// Build execution request
	req := vault.ExecutionRequest{
		CredentialID: params.CredentialID,
		Command:      command,
		Args:         args,
		WorkingDir:   params.WorkingDir,
		Host:         params.Host,
		Reason:       params.Reason,
		Tool:         "bash", // Default tool
		ProjectID:    projectID,
	}

	// Set timeout
	if params.Timeout > 0 {
		req.Timeout = time.Duration(params.Timeout) * time.Second
	} else {
		req.Timeout = 60 * time.Second
	}

	executor := provider.GetExecutor()

	// Two-person finalize: a prior needs_two_person response returned a request
	// id; once a distinct approver has approved it via vault_approve, the agent
	// retries with twoPersonRequestId set to reconstruct + run the command.
	if params.TwoPersonRequestID != "" {
		result, err := executor.FinalizeTwoPerson(ctx, params.TwoPersonRequestID)
		if err != nil {
			return jsonResult(VaultExecResult{
				Status:   "failed",
				ExitCode: -1,
				Error:    fmt.Sprintf("two-person finalize failed: %v (ensure a distinct second person approved via vault_approve)", err),
			}), nil
		}
		return jsonResult(VaultExecResult{
			Status:         "ok",
			Stdout:         result.Stdout,
			Stderr:         result.Stderr,
			ExitCode:       result.ExitCode,
			Duration:       result.Duration.String(),
			RedactedCount:  result.RedactedCount,
			RedactionHints: result.RedactionHints,
			SafeToParse:    result.SafeToParse,
			Warning:        "Credential value was never exposed. Two-person approval satisfied.",
		}), nil
	}

	// If the agent is retrying with an approvalId from a prior needs_approval
	// response, consume it first so the execution is authorized.
	if params.ApprovalID != "" {
		if err := executor.Approve(params.ApprovalID); err != nil {
			return jsonResult(VaultExecResult{
				Error: fmt.Sprintf("approval failed: %v", err),
			}), nil
		}
	}

	// Execute
	result, err := executor.Execute(ctx, req)
	if err != nil {
		// Two-Person Integrity: this credential needs a SECOND, distinct human
		// approver. Begin a broker request seeded with the current user's share
		// and return a requestId for the approver + finalize steps.
		if errors.Is(err, vault.ErrTwoPersonRequired) {
			return beginTwoPerson(ctx, provider, executor, req)
		}
		// A structured approval request is resumable: surface the approvalId so
		// the agent can ask the user to approve and then retry with it.
		var approvalErr *vault.ApprovalError
		if errors.As(err, &approvalErr) {
			return jsonResult(VaultExecResult{
				Status:     "needs_approval",
				ApprovalID: approvalErr.ApprovalID,
				Warning:    "This credential requires host approval. After the host records approval, call vault_exec again with the same command and approvalId set to the value above.",
			}), nil
		}
		// A lookup/spawn failure (e.g. "executable file not found in $PATH")
		// or any other pre-execution error never produced a real process exit
		// code, but VaultExecResult.ExitCode has no `omitempty` -- leaving it
		// unset serialized as exitCode:0, which reads as success alongside a
		// populated Error field. That internally contradictory shape (issue
		// #247) is why callers could not distinguish "ran and exited 0" from
		// "never ran at all". -1 is not a real process exit code (those are
		// 0-255) and Status:"failed" makes the failure unambiguous even for
		// callers that only look at Status.
		return jsonResult(VaultExecResult{
			Status:   "failed",
			ExitCode: -1,
			Error:    err.Error(),
		}), nil
	}

	// Return result
	return jsonResult(VaultExecResult{
		Status:         "ok",
		Stdout:         result.Stdout,
		Stderr:         result.Stderr,
		ExitCode:       result.ExitCode,
		Duration:       result.Duration.String(),
		RedactedCount:  result.RedactedCount,
		RedactionHints: result.RedactionHints,
		SafeToParse:    result.SafeToParse,
		Warning:        "Credential value was never exposed. Output was scanned for accidental secret leakage.",
	}), nil
}

// VaultListParams are parameters for the vault_list tool.
type VaultListParams struct {
	// UnlockIfNeeded asks an interactive host to unlock a locked vault.
	UnlockIfNeeded bool `json:"unlockIfNeeded,omitempty" description:"If true, ask the host to unlock a locked vault; TUI support is optional and headless hosts return immediately"`

	// Kind filters by credential kind.
	Kind vault.CredentialKind `json:"kind,omitempty" description:"Filter by credential kind"`

	// Scope filters by scope (global, project).
	Scope vault.CredentialScope `json:"scope,omitempty" description:"Filter by scope: global or project"`

	// Tags filter (credential must have all specified tags).
	Tags []string `json:"tags,omitempty" description:"Filter by tags (credential must have all specified tags)"`
}

// VaultListResult is the result of vault_list.
type VaultListResult struct {
	// Credentials available (without secret values).
	Credentials []CredentialInfo `json:"credentials"`

	// Warning about vault state or locked status.
	Warning string `json:"warning,omitempty"`

	// Status reports structured unlock outcomes when applicable.
	Status string `json:"status,omitempty"`
}

// CredentialInfo shows credential info without the secret.
type CredentialInfo struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	Kind            vault.CredentialKind  `json:"kind"`
	Scope           vault.CredentialScope `json:"scope"`
	AllowedTools    []string              `json:"allowedTools,omitempty"`
	AllowedCommands []string              `json:"allowedCommands,omitempty"`
	AllowedHosts    []string              `json:"allowedHosts,omitempty"`
	Tags            []string              `json:"tags,omitempty"`
}

// VaultListTool lists available credentials.
type VaultListTool struct {
	tools.BaseTool
	unlocker VaultUnlocker
}

// NewVaultListTool creates a new vault_list tool.
func NewVaultListTool() tools.Tool {
	return NewVaultListToolWithUnlocker(nil)
}

// NewVaultListToolWithUnlocker creates a vault_list tool with an optional host unlock broker.
func NewVaultListToolWithUnlocker(unlocker VaultUnlocker) tools.Tool {
	return tools.Typed[VaultListParams](&VaultListTool{unlocker: unlocker})
}

// Name returns the tool name.
func (t *VaultListTool) Name() string { return "vault_list" }

// Description returns the tool description.
func (t *VaultListTool) Description() string {
	return `List stored credential metadata for use with vault_exec. Set unlockIfNeeded when the vault may be locked.`
}

// Parameters returns the parameter schema.
func (t *VaultListTool) Parameters() any { return tools.SchemaFor[VaultListParams]() }

// IsIdempotent returns true.
func (t *VaultListTool) IsIdempotent() bool { return true }

// RequiresPermission returns no permissions for listing.
func (t *VaultListTool) RequiresPermission() []tools.Permission { return nil }

// SupportedContentTypes returns supported types.
func (t *VaultListTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// Run lists credentials.
func (t *VaultListTool) Run(ctx context.Context, params VaultListParams) (*tools.ToolResult, error) {
	unlock := ensureVaultUnlocked(ctx, params.UnlockIfNeeded, t.unlocker, t.Name(), string(params.Scope))
	provider := unlock.provider
	if unlock.status != "" {
		return jsonResult(VaultListResult{
			Credentials: []CredentialInfo{},
			Status:      unlock.status,
			Warning:     unlock.err,
		}), nil
	}

	if !provider.IsEnabled() {
		return jsonResult(VaultListResult{
			Credentials: []CredentialInfo{},
			Warning:     "vault is locked — no credentials available. Tell the user to unlock the vault by typing /vault in the TUI (or Settings → Vault, or 'swarmos vault unlock' in CLI).",
		}), nil
	}

	filter := vault.CredentialFilter{
		Kind:  params.Kind,
		Scope: params.Scope,
		Tags:  params.Tags,
	}

	creds, err := provider.GetVault().List(ctx, filter, provider.GetProjectID())
	if err != nil {
		return nil, fmt.Errorf("failed to list credentials: %w", err)
	}

	// Convert to info (without secrets)
	infos := make([]CredentialInfo, len(creds))
	for i, c := range creds {
		infos[i] = CredentialInfo{
			ID:              c.ID,
			Name:            c.Name,
			Kind:            c.Kind,
			Scope:           c.Scope,
			AllowedTools:    c.AllowedTools,
			AllowedCommands: c.AllowedCommands,
			AllowedHosts:    c.AllowedHosts,
			Tags:            c.Tags,
		}
	}

	return jsonResult(VaultListResult{Credentials: infos}), nil
}

// jsonResult creates an XML tool result wrapping a JSON payload.
func jsonResult(v any) *tools.ToolResult {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return tools.NewXMLResult(tools.NewXML("error").
			Field("message", fmt.Sprintf("error serializing result: %v", err)))
	}
	return tools.NewXMLResult(tools.NewXML("result").
		Field("data", string(data)))
}

// splitShellCommand splits a shell-style command string into command and args,
// respecting double-quoted and single-quoted segments. This is a simple parser
// — it does not handle shell operators like |, &&, or $(). For complex
// commands, callers should use sh -c "..." explicitly.
func splitShellCommand(s string) []string {
	var parts []string
	var current strings.Builder
	inDouble := false
	inSingle := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == ' ' && !inDouble && !inSingle:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}
