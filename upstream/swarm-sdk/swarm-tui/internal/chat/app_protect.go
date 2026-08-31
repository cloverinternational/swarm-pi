package chat

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/gitprotect"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
)

// handleProtectCommand applies a /protect action to the current workspace's git
// repo and reports the result as a user-facing notification. It configures the
// protected-branch guardrail persisted in <repoRoot>/config.swarm (a committed,
// shareable file), which the SDK's protected-branch hook reads on every tool
// call.
func (a *App) handleProtectCommand(msg commands.ProtectCommandMsg) {
	workspace := ""
	if a.sdk != nil {
		workspace = a.sdk.WorkspaceRoot()
	}
	if workspace == "" {
		a.addNotification("error", "/protect: no workspace is open.")
		return
	}

	repo, err := gitops.NewGitRepo(workspace)
	if err != nil {
		a.addNotification("error", "/protect: this workspace is not a git repository.")
		return
	}
	repoRoot := repo.Path

	// Current branch (best-effort, for display).
	currentBranch := ""
	if status, serr := repo.GetStatus(); serr == nil {
		currentBranch = status.CurrentBranch
	}

	switch msg.Action {
	case "off":
		if _, err := gitprotect.SetProtection(repoRoot, false, nil); err != nil {
			a.addNotification("error", fmt.Sprintf("/protect: could not disable protection: %s", err.Error()))
			return
		}
		a.addNotification("success", "Branch protection disabled. Edits on any branch are allowed.")

	case "on", "branches":
		cfg, err := gitprotect.SetProtection(repoRoot, true, msg.Branches)
		if err != nil {
			a.addNotification("error", fmt.Sprintf("/protect: could not enable protection: %s", err.Error()))
			return
		}
		branches := cfg.EffectiveProtectedBranches()
		note := fmt.Sprintf("Branch protection enabled (config.swarm). Protected branches: %s.", strings.Join(branches, ", "))
		if currentBranch != "" && cfg.IsBranchProtected(currentBranch) {
			note += fmt.Sprintf(" You are on '%s' (protected) — edits are blocked; use a worktree.", currentBranch)
		} else if currentBranch != "" {
			note += fmt.Sprintf(" You are on '%s' (not protected) — edits are allowed.", currentBranch)
		}
		a.addNotification("success", note)

	default: // "status"
		cfg, _, err := gitprotect.Load(repoRoot)
		if err != nil {
			a.addNotification("error", fmt.Sprintf("/protect: could not read config: %s", err.Error()))
			return
		}
		if !cfg.Protected {
			a.addNotification("info", "Branch protection is OFF. Use `/protect on` to guard main/master.")
			return
		}
		branches := cfg.EffectiveProtectedBranches()
		note := fmt.Sprintf("Branch protection is ON (config.swarm). Protected: %s.", strings.Join(branches, ", "))
		if currentBranch != "" {
			if cfg.IsBranchProtected(currentBranch) {
				note += fmt.Sprintf(" Current branch '%s' is PROTECTED (edits blocked).", currentBranch)
			} else {
				note += fmt.Sprintf(" Current branch '%s' is not protected (edits allowed).", currentBranch)
			}
		}
		a.addNotification("info", note)
	}
}
