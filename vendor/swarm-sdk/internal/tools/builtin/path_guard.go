package builtin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdkpaths "github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

func defaultAllowedPaths() []string {
	paths := []string{}

	// Add current working directory
	wd, err := os.Getwd()
	if err == nil && wd != "" {
		if abs, err := filepath.Abs(wd); err == nil {
			paths = append(paths, abs)
		} else {
			paths = append(paths, wd)
		}
	}

	// Add /tmp directory for temporary file access
	paths = append(paths, "/tmp")

	// Add the SwarmOS root (~/.swarm) so agents can read their own conversation
	// history, session-scoped plan files, skills, and other persistent state.
	paths = append(paths, sdkpaths.Root())

	return paths
}

func checkAllowedPath(absPath string, allowedPaths []string, errorCode string) error {
	if len(allowedPaths) == 0 {
		return nil
	}

	resolvedTarget, err := resolvePathForCheck(absPath)
	if err != nil {
		return sdkerr.Permanent(errorCode, fmt.Sprintf("failed to resolve path: %v", err))
	}

	for _, allowedPath := range allowedPaths {
		if allowedPath == "" {
			continue
		}
		absAllowed, err := filepath.Abs(allowedPath)
		if err != nil {
			continue
		}
		resolvedAllowed, err := resolvePathForCheck(absAllowed)
		if err != nil {
			continue
		}
		if pathWithinRoot(resolvedAllowed, resolvedTarget) {
			return nil
		}
	}

	return sdkerr.Permanent(errorCode,
		fmt.Sprintf("Path not allowed (not_allowed): %s", absPath))
}

func resolvePathForCheck(absPath string) (string, error) {
	cleaned := filepath.Clean(absPath)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("path must be absolute")
	}

	resolved, err := filepath.EvalSymlinks(cleaned)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	current := cleaned
	for {
		if _, statErr := os.Stat(current); statErr == nil {
			resolvedCurrent, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			if current == cleaned {
				return resolvedCurrent, nil
			}
			rel, err := filepath.Rel(current, cleaned)
			if err != nil {
				return "", err
			}
			return filepath.Join(resolvedCurrent, rel), nil
		} else if !os.IsNotExist(statErr) {
			return "", statErr
		}

		parent := filepath.Dir(current)
		if parent == current {
			return cleaned, nil
		}
		current = parent
	}
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
