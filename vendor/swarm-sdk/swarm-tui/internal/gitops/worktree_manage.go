package gitops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type AddWorktreeOpts struct {
	RepoPath string
	Path     string
	Branch   string
	BaseSHA  string
}

func AddWorktree(opts AddWorktreeOpts, cfg WorktreeConfig) error {
	args := []string{"worktree", "add"}
	if opts.Branch != "" {
		args = append(args, "-b", opts.Branch)
	}
	args = append(args, opts.Path)
	if opts.BaseSHA != "" {
		args = append(args, opts.BaseSHA)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = opts.RepoPath
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %w\n%s", err, out)
	}

	if len(cfg.SymlinkDirectories) > 0 {
		if err := CreateSymlinks(opts.RepoPath, opts.Path, cfg.SymlinkDirectories); err != nil {
			return fmt.Errorf("create symlinks: %w", err)
		}
	}

	return nil
}

func RemoveWorktree(repoPath, worktreePath string) error {
	worktrees, err := ListWorktrees(repoPath)
	if err != nil {
		return fmt.Errorf("list worktrees: %w", err)
	}

	absWT, err := filepath.Abs(worktreePath)
	if err != nil {
		return fmt.Errorf("resolve worktree path: %w", err)
	}

	for _, wt := range worktrees {
		absExisting, _ := filepath.Abs(wt.Path)
		if absExisting == absWT && wt.IsMain {
			return fmt.Errorf("refusing to remove the main worktree: %s", worktreePath)
		}
	}

	cmd := exec.Command("git", "worktree", "remove", worktreePath)
	cmd.Dir = repoPath
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %w\n%s", err, out)
	}

	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = repoPath
	_ = pruneCmd.Run()

	return nil
}

func CreateSymlinks(mainRepoPath, worktreePath string, symlinkDirs []string) error {
	for _, dir := range symlinkDirs {
		srcAbs := filepath.Join(mainRepoPath, dir)
		if _, err := os.Stat(srcAbs); os.IsNotExist(err) {
			continue
		}

		dstAbs := filepath.Join(worktreePath, dir)

		if fi, err := os.Lstat(dstAbs); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if err := os.RemoveAll(dstAbs); err != nil {
				return fmt.Errorf("remove existing %s in worktree: %w", dir, err)
			}
		}

		rel, err := filepath.Rel(worktreePath, srcAbs)
		if err != nil {
			return fmt.Errorf("compute relative path for %s: %w", dir, err)
		}

		if err := os.Symlink(rel, dstAbs); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w", dstAbs, rel, err)
		}
	}
	return nil
}

func DirDiskUsage(path string) int64 {
	var total int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
