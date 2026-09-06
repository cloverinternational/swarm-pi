# Git Worktree Enhancement Plan

**Date:** 2026-02-22
**Branch:** development/app/version-0.1/git-visualization
**Status:** In Progress

## Goal

Add Claude Code-style git worktree management with automatic symlink support to the
Swarm TUI. This means users can create/remove worktrees from the TUI, configure which
directories get symlinked (saving disk space), and see worktree status at a glance.

## Architecture Layers

### Layer 1: gitops (internal/gitops/)

**New files:**
- `worktree_config.go` — WorktreeConfig struct, Load/Save from `.swarm/worktree.json`
- `worktree_config_test.go` — Roundtrip, missing file, defaults
- `worktree_manage.go` — AddWorktree, RemoveWorktree, symlink helpers, DirDiskUsage
- `worktree_manage_test.go` — Add/remove, symlink creation, cleanup, disk calc

**Modified files:**
- `worktree.go` — Add `SymlinkedDirs []string` and `DiskUsageBytes int64` to WorktreeInfo;
  populate them in GetWorktrees when config is available

### Layer 2: IPC (headless/ipc/)

**Modified files:**
- `server_git.go` — Add 4 new handlers
- `server.go` — Register 4 new methods in dispatch switch
- `server_git_test.go` — Tests for the 4 new handlers

### Layer 3: TUI gitpanel (internal/chatui/components/gitpanel/)

**New files:**
- `worktree_view.go` — WorktreeView struct

**Modified files:**
- `component.go` — Add TabWorktrees, wire up view

### Layer 4: Electron App (swarm-app/src/)

- types.ts, client.ts, git.ts, git.svelte.ts, WorktreePanel.svelte

## Key Design Decisions

- Config lives at `.swarm/worktree.json`
- Symlinks are relative for portability
- Tab number is 8 (key "8")
- AddWorktree follows monorepo branch convention
- RemoveWorktree refuses to remove the main worktree