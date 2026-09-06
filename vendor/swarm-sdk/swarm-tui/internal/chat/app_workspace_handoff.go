package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

func (a *App) handleWorkspaceHandoff(msg commands.WorkspaceHandoffMsg) (tea.Model, tea.Cmd) {
	if a.streamingMessage || a.streamingInProgress {
		a.addNotification("warning", i18n.T("classic_chat.handoff.wait_active"))
		return a, nil
	}
	// os.Chdir below is PROCESS-WIDE: Go has no per-goroutine working directory,
	// so switching workspaces while any background work is still running would
	// silently change the directory underneath in-flight tool calls (bash,
	// relative file paths, the sub-agent "Working Directory:" context stamp),
	// even though that work believes it is still operating in the old
	// directory. Refuse the handoff until nothing is running in the background:
	//   - other conversations whose turn was pushed to the background
	//     (a.bgManager tracks per-conversation background turns), and
	//   - individual Subagent/Delegate tool calls started with
	//     run_in_background=true (tracked by the SDK's background agent
	//     manager, a.sdk.GetBackgroundAgentCount()).
	if a.bgManager != nil && a.bgManager.Count() > 0 {
		a.addNotification("warning", i18n.T("classic_chat.handoff.wait_background"))
		return a, nil
	}
	if a.sdk != nil && a.sdk.GetBackgroundAgentCount() > 0 {
		a.addNotification("warning", i18n.T("classic_chat.handoff.wait_subagents"))
		return a, nil
	}

	binding, err := gitops.ResolveWorkspaceBinding(msg.TargetPath)
	if err != nil || !binding.IsGit {
		if err == nil {
			err = fmt.Errorf("target is not a Git worktree")
		}
		a.addNotification("error", i18n.T("classic_chat.handoff.failed", err))
		return a, nil
	}
	projectRoot := filepath.Clean(msg.ProjectRoot)
	if projectRoot == "." || projectRoot == "" {
		projectRoot = filepath.Clean(a.appOptions.ProjectRoot)
	}
	if projectRoot != "" && filepath.Clean(binding.ProjectRoot) != projectRoot {
		a.addNotification("error", i18n.T("classic_chat.handoff.different_project"))
		return a, nil
	}
	if sameWorkspacePath(binding.ExecutionRoot, a.appOptions.WorkspaceRoot) {
		a.addNotification("info", i18n.T("classic_chat.handoff.already_using", binding.ExecutionRoot))
		return a, nil
	}

	convID := a.currentConvID
	oldCWD, _ := os.Getwd()
	if err := a.teardownForWorkspaceHandoff(); err != nil {
		a.addNotification("error", i18n.T("classic_chat.handoff.teardown_failed", err))
		return a, nil
	}
	if err := os.Chdir(binding.ExecutionRoot); err != nil {
		if oldCWD != "" {
			_ = os.Chdir(oldCWD)
		}
		a.addNotification("error", i18n.T("classic_chat.handoff.failed", err))
		return a, nil
	}

	opts := a.appOptions
	opts.WorkspaceRoot = binding.ExecutionRoot
	opts.ProjectRoot = binding.ProjectRoot
	next := NewAppWithOptions(opts)
	next.width = a.width
	next.height = a.height
	if a.width > 0 && a.height > 0 {
		_, _ = next.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height})
	}
	if convID != "" {
		if !next.openConversationByID(convID) {
			next.addNotification("warning", i18n.T("classic_chat.handoff.resume_failed"))
		}
	}
	verb := i18n.T("classic_chat.handoff.joined")
	if msg.Created {
		verb = i18n.T("classic_chat.handoff.created_joined")
	}
	shared := ""
	if msg.Shared {
		shared = i18n.T("classic_chat.handoff.shared_suffix")
	}
	next.addNotification("success", fmt.Sprintf("%s %s%s", verb, binding.Branch, shared))
	return next, next.Init()
}

func (a *App) teardownForWorkspaceHandoff() error {
	if a.workspaceLease != nil {
		if err := a.workspaceLease.Close(); err != nil && !os.IsNotExist(err) {
			return err
		}
		a.workspaceLease = nil
	}
	if a.sdk != nil {
		if err := a.sdk.Close(); err != nil {
			return err
		}
	}
	if a.bgProcessManager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.bgProcessManager.Shutdown(ctx); err != nil {
			return err
		}
	}
	a.closeVoice()
	if a.preRenderBuffer != nil {
		a.preRenderBuffer.Stop()
	}
	if a.visualReg != nil {
		a.visualReg.ShutdownAll()
	}
	if a.hub != nil {
		a.hub.Stop(context.Background())
	}
	return nil
}

func sameWorkspacePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && filepath.Clean(absA) == filepath.Clean(absB)
}
