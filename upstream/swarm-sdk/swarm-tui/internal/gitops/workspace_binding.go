package gitops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/gitprotect"
)

// WorkspaceBinding separates the stable project identity from the directory
// where this TUI executes tools. Linked worktrees share ProjectRoot while each
// has its own ExecutionRoot and Branch.
type WorkspaceBinding struct {
	ProjectRoot   string
	ExecutionRoot string
	Branch        string
	IsGit         bool
	IsProtected   bool
	// IgnoredHomeGit is set when the resolved git top-level was exactly the
	// user's home directory and we were NOT launched from $HOME itself, so we
	// deliberately declined to bind to that (almost always accidental) repo.
	// Callers can surface this to the user. Opt back in with SWARM_ALLOW_HOME_GIT=1.
	IgnoredHomeGit bool
}

// resolveRealPath returns the symlink-resolved, cleaned absolute path, falling
// back to a plain Clean when the path cannot be resolved (e.g. does not exist).
func resolveRealPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	return filepath.Clean(p)
}

// ResolveWorkspaceBinding resolves path into a stable project root and an
// execution root. Non-Git directories use the same absolute path for both.
func ResolveWorkspaceBinding(path string) (WorkspaceBinding, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return WorkspaceBinding{}, fmt.Errorf("get working directory: %w", err)
		}
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return WorkspaceBinding{}, fmt.Errorf("resolve workspace path: %w", err)
	}

	binding := WorkspaceBinding{ProjectRoot: absPath, ExecutionRoot: absPath}
	executionRoot, err := gitOutput(absPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return binding, nil
	}
	binding.ExecutionRoot = filepath.Clean(executionRoot)
	binding.ProjectRoot = binding.ExecutionRoot
	binding.IsGit = true

	// Guard against an accidental `git init` in the home directory. If the
	// resolved git top-level is exactly $HOME but we were launched from some
	// non-home subdirectory, binding to that repo would treat all of $HOME as
	// the workspace — which is both semantically wrong and catastrophically
	// slow (git status / worktree / disk-usage operations then scan the entire
	// home tree, including caches and the module cache). In that case ignore
	// the home repo and fall back to a plain non-git workspace rooted at the
	// launch directory. Set SWARM_ALLOW_HOME_GIT=1 to opt back in.
	if home, herr := os.UserHomeDir(); herr == nil && strings.TrimSpace(home) != "" {
		realHome := resolveRealPath(home)
		if resolveRealPath(binding.ExecutionRoot) == realHome &&
			resolveRealPath(absPath) != realHome &&
			os.Getenv("SWARM_ALLOW_HOME_GIT") != "1" {
			return WorkspaceBinding{
				ProjectRoot:    absPath,
				ExecutionRoot:  absPath,
				IsGit:          false,
				IgnoredHomeGit: true,
			}, nil
		}
	}

	if branch, branchErr := gitOutput(binding.ExecutionRoot, "branch", "--show-current"); branchErr == nil {
		binding.Branch = strings.TrimSpace(branch)
	}

	worktrees, wtErr := ListWorktrees(binding.ExecutionRoot)
	if wtErr == nil {
		for _, wt := range worktrees {
			if wt.IsMain {
				if root, absErr := filepath.Abs(wt.Path); absErr == nil {
					binding.ProjectRoot = filepath.Clean(root)
				} else {
					binding.ProjectRoot = filepath.Clean(wt.Path)
				}
				break
			}
		}
	}

	if cfg, found, cfgErr := gitprotect.Load(binding.ProjectRoot); cfgErr != nil {
		return WorkspaceBinding{}, cfgErr
	} else if found {
		binding.IsProtected = cfg.IsBranchProtected(binding.Branch)
	}
	return binding, nil
}

// FindWorktree resolves a branch name, absolute path, or worktree directory
// basename to exactly one worktree.
func FindWorktree(projectRoot, selector string) (WorktreeInfo, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return WorktreeInfo{}, fmt.Errorf("worktree selector is required")
	}
	worktrees, err := ListWorktrees(projectRoot)
	if err != nil {
		return WorktreeInfo{}, err
	}

	selectorAbs := ""
	if filepath.IsAbs(selector) {
		selectorAbs = filepath.Clean(selector)
	}
	var matches []WorktreeInfo
	for _, wt := range worktrees {
		wtAbs, _ := filepath.Abs(wt.Path)
		if selector == wt.Branch || selector == filepath.Base(wt.Path) || (selectorAbs != "" && selectorAbs == filepath.Clean(wtAbs)) {
			matches = append(matches, wt)
		}
	}
	if len(matches) == 0 {
		return WorktreeInfo{}, fmt.Errorf("no worktree matches %q", selector)
	}
	if len(matches) != 1 {
		return WorktreeInfo{}, fmt.Errorf("worktree selector %q is ambiguous", selector)
	}
	return matches[0], nil
}

// SuggestedWorktreePath returns the organized default path for a branch.
func SuggestedWorktreePath(projectRoot, branch string) string {
	name := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(strings.TrimSpace(branch))
	name = strings.Trim(name, "-.")
	return filepath.Join(projectRoot, ".worktrees", name)
}

// CreateWorkspaceWorktree explicitly creates a new branch worktree from the
// current project HEAD. It never moves or switches the primary checkout.
func CreateWorkspaceWorktree(projectRoot, branch string) (WorktreeInfo, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return WorktreeInfo{}, fmt.Errorf("branch name is required")
	}
	if _, err := gitOutput(projectRoot, "check-ref-format", "--branch", branch); err != nil {
		return WorktreeInfo{}, fmt.Errorf("invalid branch name %q", branch)
	}
	if _, err := gitOutput(projectRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		return WorktreeInfo{}, fmt.Errorf("branch %q already exists", branch)
	}

	path := SuggestedWorktreePath(projectRoot, branch)
	if _, err := os.Stat(path); err == nil {
		return WorktreeInfo{}, fmt.Errorf("worktree path already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return WorktreeInfo{}, fmt.Errorf("inspect worktree path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return WorktreeInfo{}, fmt.Errorf("create worktree parent: %w", err)
	}

	cfg, err := LoadWorktreeConfig(projectRoot)
	if err != nil {
		return WorktreeInfo{}, err
	}
	if len(cfg.SymlinkDirectories) == 0 {
		cfg = DefaultWorktreeConfig()
	}
	if err := AddWorktree(AddWorktreeOpts{
		RepoPath: projectRoot,
		Path:     path,
		Branch:   branch,
	}, cfg); err != nil {
		return WorktreeInfo{}, err
	}
	return FindWorktree(projectRoot, branch)
}

// RemoveWorkspaceWorktree removes an explicit secondary worktree only when it
// is clean. Active-session checks are performed by the TUI before calling this.
func RemoveWorkspaceWorktree(projectRoot, selector string) error {
	wt, err := FindWorktree(projectRoot, selector)
	if err != nil {
		return err
	}
	if wt.IsMain {
		return fmt.Errorf("refusing to remove the primary worktree: %s", wt.Path)
	}
	if wt.IsDirty {
		return fmt.Errorf("refusing to remove dirty worktree: %s", wt.Path)
	}
	return RemoveWorktree(projectRoot, wt.Path)
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
