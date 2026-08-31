package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SanitizeHookWorkingDir validates and resolves a hook's working directory.
func SanitizeHookWorkingDir(workingDir, workspaceRoot string) (string, error) {
	if workspaceRoot == "" {
		return "", fmt.Errorf("workspace root is not set")
	}

	rootAbs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace root: %w", err)
	}
	rootResolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace root symlinks: %w", err)
	}

	if workingDir == "" {
		return rootResolved, nil
	}

	if !filepath.IsAbs(workingDir) {
		workingDir = filepath.Join(rootResolved, workingDir)
	}

	workingAbs, err := filepath.Abs(workingDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve hook working dir: %w", err)
	}
	workingResolved, err := filepath.EvalSymlinks(workingAbs)
	if err != nil {
		return "", fmt.Errorf("failed to resolve hook working dir symlinks: %w", err)
	}

	if !pathWithinRoot(rootResolved, workingResolved) {
		return "", fmt.Errorf("hook working dir must be within workspace root")
	}

	info, err := os.Stat(workingResolved)
	if err != nil {
		return "", fmt.Errorf("failed to stat hook working dir: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("hook working dir is not a directory")
	}

	return workingResolved, nil
}

func pathWithinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
