package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
)

func testWorkspaceCommand() *WorkspaceCommand {
	cmd := NewWorkspaceCommand()
	cmd.resolve = func(string) (gitops.WorkspaceBinding, error) {
		return gitops.WorkspaceBinding{
			ProjectRoot:   "/repo",
			ExecutionRoot: "/repo",
			Branch:        "main",
			IsGit:         true,
			IsProtected:   true,
		}, nil
	}
	cmd.list = func(string) ([]gitops.WorktreeInfo, error) {
		return []gitops.WorktreeInfo{
			{Path: "/repo", Branch: "main", IsMain: true},
			{Path: "/repo/.worktrees/feature", Branch: "feature", IsDirty: true},
		}, nil
	}
	cmd.find = func(_, selector string) (gitops.WorktreeInfo, error) {
		return gitops.WorktreeInfo{Path: "/repo/.worktrees/" + selector, Branch: selector}, nil
	}
	cmd.create = func(_, branch string) (gitops.WorktreeInfo, error) {
		return gitops.WorktreeInfo{Path: "/repo/.worktrees/" + branch, Branch: branch}, nil
	}
	cmd.activeCount = func(path string) int {
		if path == "/repo/.worktrees/feature" {
			return 2
		}
		return 1
	}
	return cmd
}

func runWorkspaceCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	return cmd()
}

func TestWorkspaceCommandLoadsPickerWithStatus(t *testing.T) {
	cmd := testWorkspaceCommand()
	loaded := runWorkspaceCmd(t, cmd.Execute(nil))
	_, next := cmd.Update(loaded)
	if next != nil {
		t.Fatal("loading picker should not emit a command")
	}
	if !cmd.IsInteractive() || cmd.loading {
		t.Fatalf("picker state = interactive %v loading %v", cmd.IsInteractive(), cmd.loading)
	}
	if len(cmd.entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(cmd.entries))
	}
	if !cmd.entries[0].Current || cmd.entries[1].ActiveTUIs != 2 || !cmd.entries[1].Worktree.IsDirty {
		t.Fatalf("unexpected entry status: %#v", cmd.entries)
	}
}

func TestWorkspaceCommandJoinsOccupiedWorktreeExplicitly(t *testing.T) {
	cmd := testWorkspaceCommand()
	_, _ = cmd.Update(runWorkspaceCmd(t, cmd.Execute(nil)))
	_, _ = cmd.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, handoffCmd := cmd.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg, ok := runWorkspaceCmd(t, handoffCmd).(WorkspaceHandoffMsg)
	if !ok {
		t.Fatalf("message type = %T, want WorkspaceHandoffMsg", runWorkspaceCmd(t, handoffCmd))
	}
	if msg.TargetPath != "/repo/.worktrees/feature" || msg.Branch != "feature" || !msg.Shared || msg.Created {
		t.Fatalf("handoff = %#v", msg)
	}
	if cmd.IsInteractive() {
		t.Fatal("picker should close after selection")
	}
}

func TestWorkspaceCommandCreatesNamedWorktree(t *testing.T) {
	cmd := testWorkspaceCommand()
	cmd.Execute([]string{"create"})
	if !cmd.creating || !cmd.IsInteractive() {
		t.Fatal("create without a name should open branch input")
	}
	_, _ = cmd.Update(tea.KeyPressMsg{Code: 'f', Text: "feature/new"})
	_, createCmd := cmd.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	created := runWorkspaceCmd(t, createCmd)
	_, handoffCmd := cmd.Update(created)
	msg, ok := runWorkspaceCmd(t, handoffCmd).(WorkspaceHandoffMsg)
	if !ok {
		t.Fatalf("message type = %T, want WorkspaceHandoffMsg", msg)
	}
	if msg.Branch != "feature/new" || msg.TargetPath != "/repo/.worktrees/feature/new" || !msg.Created {
		t.Fatalf("handoff = %#v", msg)
	}
}

func TestWorkspaceCommandDirectJoinErrorIsVisibleToApp(t *testing.T) {
	cmd := testWorkspaceCommand()
	cmd.find = func(_, _ string) (gitops.WorktreeInfo, error) {
		return gitops.WorktreeInfo{}, os.ErrNotExist
	}
	msg := runWorkspaceCmd(t, cmd.Execute([]string{"join", "missing"}))
	if _, ok := msg.(WorkspaceErrorMsg); !ok {
		t.Fatalf("message type = %T, want WorkspaceErrorMsg", msg)
	}
}

func TestWorkspaceCommandDirectJoin(t *testing.T) {
	cmd := testWorkspaceCommand()
	msg, ok := runWorkspaceCmd(t, cmd.Execute([]string{"join", "feature"})).(WorkspaceHandoffMsg)
	if !ok {
		t.Fatalf("message type = %T, want WorkspaceHandoffMsg", msg)
	}
	if msg.Branch != "feature" || msg.TargetPath != "/repo/.worktrees/feature" || !msg.Shared {
		t.Fatalf("handoff = %#v", msg)
	}
}

func TestWorkspaceCommandEndToEndCreatesAndDiscoversSharedWorktree(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "initial")

	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })

	creator := NewWorkspaceCommand()
	createdResult := runWorkspaceCmd(t, creator.Execute([]string{"create", "feature/e2e"}))
	_, handoffCmd := creator.Update(createdResult)
	handoff, ok := runWorkspaceCmd(t, handoffCmd).(WorkspaceHandoffMsg)
	if !ok || !handoff.Created || handoff.ProjectRoot != repo || handoff.Branch != "feature/e2e" {
		t.Fatalf("created handoff = %#v", handoff)
	}
	binding, err := gitops.ResolveWorkspaceBinding(handoff.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if binding.ProjectRoot != repo || binding.ExecutionRoot != handoff.TargetPath || binding.Branch != "feature/e2e" {
		t.Fatalf("binding = %#v", binding)
	}

	lease, err := gitops.RegisterWorkspaceLease(handoff.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })

	picker := NewWorkspaceCommand()
	_, _ = picker.Update(runWorkspaceCmd(t, picker.Execute(nil)))
	var shared *WorkspaceEntry
	for i := range picker.entries {
		if picker.entries[i].Worktree.Branch == "feature/e2e" {
			shared = &picker.entries[i]
			break
		}
	}
	if shared == nil || shared.ActiveTUIs != 1 || shared.Current {
		t.Fatalf("shared picker entry = %#v", shared)
	}
}
