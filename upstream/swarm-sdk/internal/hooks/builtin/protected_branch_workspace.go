package builtin

import (
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

func (h *ProtectedBranchHook) workspaceHandoffBlock(branch string, branches []string, repoRoot, toolName, command string, event hooks.Event) hooks.HookResult {
	result := hooks.Block(h.workspaceHandoffMessage(branch, toolName, command))
	result.Metadata = map[string]any{
		"reason":             "protected_workspace_requires_handoff",
		"project_root":       repoRoot,
		"execution_root":     effectiveTargetDir(event, repoRoot),
		"current_branch":     branch,
		"protected_branches": append([]string(nil), branches...),
		"suggested_command":  "/workspace",
	}
	return result
}

func (h *ProtectedBranchHook) workspaceHandoffMessage(branch, toolName, command string) string {
	what := fmt.Sprintf("the %q tool", toolName)
	if command != "" {
		what = fmt.Sprintf("the shell command %q", truncateForMessage(command, 120))
	}
	return fmt.Sprintf(
		"[PROTECTED BRANCH — BLOCKED] The workspace is on protected branch '%s', so %s was refused. "+
			"Read-only questions, searches, status, log, and diff remain available. "+
			"Run /workspace to select or create an editable worktree before making changes.",
		branch, what,
	)
}

func effectiveTargetDir(event hooks.Event, repoRoot string) string {
	if target := resolveTargetDir(event, repoRoot); target != "" {
		return target
	}
	return repoRoot
}
