package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/gitprotect"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// newTestProtectedHook builds a hook with a scripted branch and a fixed config,
// bypassing real git/disk access.
func newTestProtectedHook(branch string, cfg gitprotect.Config, cfgFound bool) *ProtectedBranchHook {
	h := NewProtectedBranchHookEnabled()
	h.SetGitCommand(func(args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "branch" && args[1] == "--show-current" {
			return branch + "\n", nil
		}
		if len(args) >= 1 && args[0] == "rev-parse" {
			return "/repo\n", nil
		}
		return "", nil
	})
	h.SetConfigLoader(func(_ string) (gitprotect.Config, bool) {
		return cfg, cfgFound
	})
	return h
}

// newWorktreeTestHook builds a hook where the process cwd is on `mainBranch`
// but a worktree at `wtPath` is on `wtBranch`. The gitCommand script responds
// to:
//   - `git branch --show-current`            → mainBranch (process cwd)
//   - `git -C <path> branch --show-current` → wtBranch if path is inside wtPath
//   - `git rev-parse --show-toplevel`         → repoRoot
func newWorktreeTestHook(mainBranch, wtPath, wtBranch string, repoRoot string, cfg gitprotect.Config, cfgFound bool) *ProtectedBranchHook {
	h := NewProtectedBranchHookEnabled()
	h.SetGitCommand(func(args ...string) (string, error) {
		// git -C <path> branch --show-current
		if len(args) >= 4 && args[0] == "-C" && args[2] == "branch" && args[3] == "--show-current" {
			target := args[1]
			// Match the worktree root or any subdirectory within it.
			if target == wtPath || strings.HasPrefix(target, wtPath+"/") {
				return wtBranch + "\n", nil
			}
			return "", context.DeadlineExceeded // not a known worktree → error
		}
		// git branch --show-current (process cwd)
		if len(args) >= 2 && args[0] == "branch" && args[1] == "--show-current" {
			return mainBranch + "\n", nil
		}
		// git rev-parse --show-toplevel
		if len(args) >= 1 && args[0] == "rev-parse" {
			return repoRoot + "\n", nil
		}
		return "", nil
	})
	h.SetConfigLoader(func(_ string) (gitprotect.Config, bool) {
		return cfg, cfgFound
	})
	return h
}

func toolEvent(toolName string, params map[string]any) hooks.Event {
	return hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": toolName,
			"params":    params,
		},
	}
}

func TestProtectedBranch_BlocksAndAllows(t *testing.T) {
	protectedCfg := gitprotect.Config{Protected: true, ProtectedBranches: []string{"main"}}

	tests := []struct {
		name      string
		branch    string
		cfg       gitprotect.Config
		cfgFound  bool
		toolName  string
		command   string
		wantBlock bool
	}{
		// On protected branch, mutations blocked.
		{"write on main blocked", "main", protectedCfg, true, "write", "", true},
		{"edit on main blocked", "main", protectedCfg, true, "edit", "", true},
		{"apply_patch on main blocked", "main", protectedCfg, true, "apply_patch", "", true},
		{"bash git commit on main blocked", "main", protectedCfg, true, "bash", "git commit -m x", true},
		{"bash rm on main blocked", "main", protectedCfg, true, "bash", "rm -rf foo", true},
		{"bash redirect on main blocked", "main", protectedCfg, true, "bash", "echo hi > file.txt", true},
		{"bash sed -i on main blocked", "main", protectedCfg, true, "bash", "sed -i s/a/b/ f", true},

		// Historical bypass regressions: a read-only command must not mask a
		// later mutation, and pull must not substitute for a blocked merge.
		{"git status cannot mask merge", "main", protectedCfg, true, "bash", "git status --short && git merge --no-ff feature-work", true},
		{"git log cannot mask protected push", "main", protectedCfg, true, "bash", "git log -1; git push origin main", true},
		{"git pull cannot substitute for merge", "main", protectedCfg, true, "bash", "git pull --no-ff --no-edit . feature-work", true},

		// On protected branch, read-only + escape allowed.
		{"read on main allowed", "main", protectedCfg, true, "read", "", false},
		{"grep on main allowed", "main", protectedCfg, true, "grep", "", false},
		{"bash git status allowed", "main", protectedCfg, true, "bash", "git status", false},
		{"bash git checkout allowed", "main", protectedCfg, true, "bash", "git checkout -b feat", false},
		{"bash git worktree add allowed", "main", protectedCfg, true, "bash", "git worktree add ../wt", false},
		{"bash ls allowed", "main", protectedCfg, true, "bash", "ls -la", false},

		// Not on protected branch → everything allowed.
		{"write on feature allowed", "feature", protectedCfg, true, "write", "", false},
		{"bash commit on feature allowed", "feature", protectedCfg, true, "bash", "git commit -m x", false},

		// Protection disabled by config → allowed even on main.
		{"write on main but protection off", "main", gitprotect.Config{Protected: false}, true, "write", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestProtectedHook(tc.branch, tc.cfg, tc.cfgFound)
			var params map[string]any
			if tc.command != "" {
				params = map[string]any{"command": tc.command}
			}
			res, err := h.OnEvent(context.Background(), toolEvent(tc.toolName, params))
			if err != nil {
				t.Fatalf("OnEvent error: %v", err)
			}
			gotBlock := res.Action == hooks.ActionBlock
			if gotBlock != tc.wantBlock {
				t.Fatalf("block=%v want=%v (action=%v msg=%q)", gotBlock, tc.wantBlock, res.Action, res.Message)
			}
			if gotBlock && !strings.Contains(res.Message, "PROTECTED BRANCH") {
				t.Errorf("block message missing marker: %q", res.Message)
			}
			if gotBlock && !strings.Contains(res.Message, "/workspace") {
				t.Errorf("block message should guide to a worktree: %q", res.Message)
			}
		})
	}
}

func TestProtectedBranch_FailOpenOnGitError(t *testing.T) {
	h := NewProtectedBranchHookEnabled()
	h.SetGitCommand(func(_ ...string) (string, error) {
		return "", context.DeadlineExceeded // any error
	})
	res, err := h.OnEvent(context.Background(), toolEvent("write", nil))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionContinue {
		t.Fatalf("expected Continue on git error, got %v", res.Action)
	}
}

func TestProtectedBranch_DetachedHeadAllows(t *testing.T) {
	h := NewProtectedBranchHookEnabled()
	h.SetGitCommand(func(args ...string) (string, error) {
		return "", nil // empty branch = detached HEAD
	})
	res, err := h.OnEvent(context.Background(), toolEvent("write", nil))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionContinue {
		t.Fatalf("expected Continue on detached HEAD, got %v", res.Action)
	}
}

func TestProtectedBranch_DefaultBranchesWhenNoConfig(t *testing.T) {
	// enabledByDefault hook, no config file found → defaults protect master.
	h := newTestProtectedHook("master", gitprotect.Config{}, false)
	res, err := h.OnEvent(context.Background(), toolEvent("write", nil))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionBlock {
		t.Fatalf("expected Block on master with defaults, got %v", res.Action)
	}
}

func TestProtectedBranch_DisabledByDefaultConstructor(t *testing.T) {
	// NewProtectedBranchHook (not enabled), no config → inert even on main.
	h := NewProtectedBranchHook()
	h.SetGitCommand(func(args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "branch" {
			return "main\n", nil
		}
		return "/repo\n", nil
	})
	h.SetConfigLoader(func(_ string) (gitprotect.Config, bool) { return gitprotect.Config{}, false })
	res, err := h.OnEvent(context.Background(), toolEvent("write", nil))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionContinue {
		t.Fatalf("expected Continue when disabled-by-default and no config, got %v", res.Action)
	}
}

func TestProtectedBranch_HandoffMetadata(t *testing.T) {
	protectedCfg := gitprotect.Config{Protected: true, ProtectedBranches: []string{"main"}}
	h := newTestProtectedHook("main", protectedCfg, true)
	res, err := h.OnEvent(context.Background(), toolEvent("edit", map[string]any{
		"file_path": "/repo/file.go",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != hooks.ActionBlock {
		t.Fatalf("expected block, got %v", res.Action)
	}
	if got := res.Metadata["reason"]; got != "protected_workspace_requires_handoff" {
		t.Fatalf("reason=%v", got)
	}
	if got := res.Metadata["project_root"]; got != "/repo" {
		t.Fatalf("project_root=%v", got)
	}
	if got := res.Metadata["execution_root"]; got != "/repo" {
		t.Fatalf("execution_root=%v", got)
	}
	if got := res.Metadata["suggested_command"]; got != "/workspace" {
		t.Fatalf("suggested_command=%v", got)
	}
}

// ---- Worktree-awareness tests ----

func TestProtectedBranch_BashCwdInWorktreeAllowed(t *testing.T) {
	// Process cwd is on "main" (protected), but the bash tool's cwd parameter
	// points to a worktree at /repo/.worktrees/feature-x on branch "feature-x"
	// (not protected). The mutation should be ALLOWED.
	protectedCfg := gitprotect.Config{Protected: true, ProtectedBranches: []string{"main"}}
	wtPath := "/repo/.worktrees/feature-x"
	h := newWorktreeTestHook("main", wtPath, "feature-x", "/repo", protectedCfg, true)

	params := map[string]any{
		"command": "echo hello > file.txt",
		"cwd":     ".worktrees/feature-x",
	}
	res, err := h.OnEvent(context.Background(), toolEvent("bash", params))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionContinue {
		t.Fatalf("expected Continue for bash in worktree on non-protected branch, got %v (%s)", res.Action, res.Message)
	}
}

func TestProtectedBranch_PushMainFromFeatureWorktreeBlocked(t *testing.T) {
	protectedCfg := gitprotect.Config{Protected: true, ProtectedBranches: []string{"main"}}
	wtPath := "/repo/.worktrees/feature-x"
	h := newWorktreeTestHook("main", wtPath, "feature-x", "/repo", protectedCfg, true)

	params := map[string]any{
		"command": "git push origin main",
		"cwd":     ".worktrees/feature-x",
	}
	res, err := h.OnEvent(context.Background(), toolEvent("bash", params))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionBlock {
		t.Fatalf("expected protected main push from feature worktree to be blocked, got %v", res.Action)
	}
	if !strings.Contains(res.Message, "targets protected branch 'main'") {
		t.Fatalf("block message should identify protected destination: %q", res.Message)
	}
}

func TestProtectedBranch_FileToolInWorktreeAllowed(t *testing.T) {
	// Process cwd is on "main" (protected), but the file tool's file_path
	// points inside a worktree on branch "feature-x" (not protected).
	protectedCfg := gitprotect.Config{Protected: true, ProtectedBranches: []string{"main"}}
	wtPath := "/repo/.worktrees/feature-x"
	h := newWorktreeTestHook("main", wtPath, "feature-x", "/repo", protectedCfg, true)

	params := map[string]any{
		"file_path": ".worktrees/feature-x/src/main.go",
	}
	res, err := h.OnEvent(context.Background(), toolEvent("write", params))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionContinue {
		t.Fatalf("expected Continue for file tool in worktree on non-protected branch, got %v (%s)", res.Action, res.Message)
	}
}

func TestProtectedBranch_BashCwdInWorktreeStillProtectedBlocked(t *testing.T) {
	// Bash cwd points to a worktree, but that worktree is ALSO on a protected
	// branch ("main"). The mutation should be BLOCKED.
	protectedCfg := gitprotect.Config{Protected: true, ProtectedBranches: []string{"main"}}
	wtPath := "/repo/.worktrees/main-copy"
	h := newWorktreeTestHook("main", wtPath, "main", "/repo", protectedCfg, true)

	params := map[string]any{
		"command": "echo hello > file.txt",
		"cwd":     ".worktrees/main-copy",
	}
	res, err := h.OnEvent(context.Background(), toolEvent("bash", params))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionBlock {
		t.Fatalf("expected Block for bash in worktree still on protected branch, got %v", res.Action)
	}
	if !strings.Contains(res.Message, "/workspace") {
		t.Errorf("block message should guide to /workspace: %q", res.Message)
	}
	if got := res.Metadata["execution_root"]; got != "/repo/.worktrees/main-copy" {
		t.Errorf("execution_root should identify the targeted worktree, got %v", got)
	}
}

func TestProtectedBranch_FileToolInWorktreeStillProtectedBlocked(t *testing.T) {
	// File tool targets a path inside a worktree, but that worktree is on "main"
	// (protected). Should be BLOCKED, and the message should mention file_path.
	protectedCfg := gitprotect.Config{Protected: true, ProtectedBranches: []string{"main"}}
	wtPath := "/repo/.worktrees/main-copy"
	h := newWorktreeTestHook("main", wtPath, "main", "/repo", protectedCfg, true)

	params := map[string]any{
		"file_path": ".worktrees/main-copy/file.go",
	}
	res, err := h.OnEvent(context.Background(), toolEvent("write", params))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionBlock {
		t.Fatalf("expected Block for file tool in worktree still on protected branch, got %v", res.Action)
	}
	if got := res.Metadata["execution_root"]; got != "/repo/.worktrees/main-copy" {
		t.Errorf("execution_root should identify the targeted worktree, got %v", got)
	}
}

func TestProtectedBranch_WorktreeGitErrorFailsToBlock(t *testing.T) {
	// The git command for `git -C <path> branch --show-current` returns an error
	// (simulating a non-worktree path or git failure). Since we can't confirm
	// the target is a non-protected worktree, the hook falls through to the
	// normal block logic. The agent is never trapped because escape commands
	// (git checkout/switch/worktree) are always allowed.
	protectedCfg := gitprotect.Config{Protected: true, ProtectedBranches: []string{"main"}}
	h := NewProtectedBranchHookEnabled()
	h.SetGitCommand(func(args ...string) (string, error) {
		// Process cwd: main
		if len(args) >= 2 && args[0] == "branch" && args[1] == "--show-current" {
			return "main\n", nil
		}
		if len(args) >= 1 && args[0] == "rev-parse" {
			return "/repo\n", nil
		}
		// Any -C call → error (can't confirm worktree)
		return "", context.DeadlineExceeded
	})
	h.SetConfigLoader(func(_ string) (gitprotect.Config, bool) {
		return protectedCfg, true
	})

	params := map[string]any{
		"command": "echo hi > x.txt",
		"cwd":     ".worktrees/some-path",
	}
	res, err := h.OnEvent(context.Background(), toolEvent("bash", params))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionBlock {
		t.Fatalf("expected Block (can't confirm worktree) when git -C errors, got %v", res.Action)
	}
	// Verify escape commands still pass even with the error-prone git runner.
	res2, err := h.OnEvent(context.Background(), toolEvent("bash", map[string]any{
		"command": "git checkout -b feature-x",
		"cwd":     ".worktrees/some-path",
	}))
	if err != nil {
		t.Fatalf("OnEvent error for escape: %v", err)
	}
	if res2.Action != hooks.ActionContinue {
		t.Fatalf("escape command should always be allowed, got %v", res2.Action)
	}
}

func TestProtectedBranch_NoCwdNoFilePathBlocked(t *testing.T) {
	// On protected branch, no cwd or file_path in params → no worktree target
	// to check → should block as before.
	protectedCfg := gitprotect.Config{Protected: true, ProtectedBranches: []string{"main"}}
	h := newWorktreeTestHook("main", "/repo/.worktrees/feat", "feat", "/repo", protectedCfg, true)

	// write tool with no file_path
	res, err := h.OnEvent(context.Background(), toolEvent("write", nil))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionBlock {
		t.Fatalf("expected Block for write on main with no file_path, got %v", res.Action)
	}
}

// TestProtectedBranch_FeatureCwdTargetsMainFileBlocked is the core regression
// for the reported intermittent bypass: the Swarm process sits on a FEATURE
// branch (unprotected), but a file tool targets a checkout that is on "main".
// Before target-aware resolution, the process-cwd branch being unprotected made
// the hook fail-open and allow the mutation. It must now BLOCK.
func TestProtectedBranch_FeatureCwdTargetsMainFileBlocked(t *testing.T) {
	protectedCfg := gitprotect.Config{Protected: true, ProtectedBranches: []string{"main"}}
	mainPath := "/repo/.worktrees/main-co"
	// Process cwd on "feature"; the target path resolves to "main".
	h := newWorktreeTestHook("feature", mainPath, "main", "/repo", protectedCfg, true)

	res, err := h.OnEvent(context.Background(), toolEvent("write", map[string]any{
		"file_path": ".worktrees/main-co/src/x.go",
	}))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionBlock {
		t.Fatalf("expected Block: file tool on feature process but targeting main checkout, got %v (%s)", res.Action, res.Message)
	}
	if got, _ := res.Metadata["execution_root"].(string); !strings.HasPrefix(got, mainPath) {
		t.Errorf("execution_root should point inside the targeted main checkout %q, got %v", mainPath, got)
	}
}

// TestProtectedBranch_FeatureCwdTargetsMainBashBlocked is the bash variant of the
// same bypass: process on "feature", but the bash `cwd` points at a checkout on
// "main" and the command mutates a file. Must BLOCK.
func TestProtectedBranch_FeatureCwdTargetsMainBashBlocked(t *testing.T) {
	protectedCfg := gitprotect.Config{Protected: true, ProtectedBranches: []string{"main"}}
	mainPath := "/repo/.worktrees/main-co"
	h := newWorktreeTestHook("feature", mainPath, "main", "/repo", protectedCfg, true)

	res, err := h.OnEvent(context.Background(), toolEvent("bash", map[string]any{
		"command": "echo hi > f.txt",
		"cwd":     ".worktrees/main-co",
	}))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionBlock {
		t.Fatalf("expected Block: bash on feature process but cwd targets main checkout, got %v (%s)", res.Action, res.Message)
	}
}

// TestProtectedBranch_FeatureCwdTargetsMainReadOnlyAllowed guards against
// over-blocking: a read-only command that targets the main checkout must stay
// allowed even though the effective branch is protected.
func TestProtectedBranch_FeatureCwdTargetsMainReadOnlyAllowed(t *testing.T) {
	protectedCfg := gitprotect.Config{Protected: true, ProtectedBranches: []string{"main"}}
	mainPath := "/repo/.worktrees/main-co"
	h := newWorktreeTestHook("feature", mainPath, "main", "/repo", protectedCfg, true)

	res, err := h.OnEvent(context.Background(), toolEvent("bash", map[string]any{
		"command": "git status",
		"cwd":     ".worktrees/main-co",
	}))
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}
	if res.Action != hooks.ActionContinue {
		t.Fatalf("expected Continue for read-only command targeting main, got %v (%s)", res.Action, res.Message)
	}
}
