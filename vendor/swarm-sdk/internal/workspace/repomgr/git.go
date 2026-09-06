package repomgr

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// CloneParams are the parameters for cloning a git repository.
type CloneParams struct {
	URL       string
	Branch    string
	TargetDir string
}

// CloneRepo clones a git repository to the target directory.
// It respects context cancellation for timeout handling.
func CloneRepo(ctx context.Context, params CloneParams) error {
	if !isValidGitURL(params.URL) {
		return fmt.Errorf("invalid git URL: %s", params.URL)
	}

	args := []string{"clone"}

	// Add branch if specified
	if params.Branch != "" {
		args = append(args, "-b", params.Branch)
	}

	// Single branch and shallow clone for faster cloning
	args = append(args, "--single-branch", "--depth", "1")

	// Add URL and target directory
	args = append(args, params.URL, params.TargetDir)

	cmd := exec.CommandContext(ctx, "git", args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("clone cancelled: %w", ctx.Err())
		}
		return fmt.Errorf("clone failed: %w: %s", err, string(output))
	}

	return nil
}

// isValidGitURL checks if a URL is a valid git URL.
// Supports:
// - HTTPS URLs: https://github.com/user/repo.git
// - HTTP URLs: http://github.com/user/repo.git
// - SSH URLs: git@github.com:user/repo.git or user@host:path/to/repo.git
func isValidGitURL(u string) bool {
	if u == "" {
		return false
	}

	// Check HTTPS/HTTP URLs
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		return true
	}

	// Local repository URLs/paths (useful for offline workflows and testing)
	// - file:// URLs
	// - absolute paths
	// - relative paths starting with ./ or ../
	if strings.HasPrefix(u, "file://") || strings.HasPrefix(u, "/") || strings.HasPrefix(u, "./") || strings.HasPrefix(u, "../") {
		return true
	}

	// Check SSH URLs: user@host:path format
	// Must have @ before : with no slashes between them
	sshPattern := regexp.MustCompile(`^[\w.-]+@[\w.-]+:[\w./-]+$`)
	return sshPattern.MatchString(u)
}
