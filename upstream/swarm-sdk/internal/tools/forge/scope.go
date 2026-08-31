package forge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// blockedRoots are filesystem subtrees an agent tool must never touch even
// when the workspace root would nominally allow it (rare, but can happen if
// the workspace itself is `/` or if a symlink inside the workspace chases
// out). `rg` walking `/nix/store` or `/sys` is a multi-minute hang that the
// agent has no business triggering.
var blockedRoots = []string{
	"/sys",
	"/proc",
	"/dev",
	"/nix/store",
	"/nix/var",
	"/opt/containerd",
	"/var/lib/docker",
	"/var/run",
	"/run",
}

// resolveScopePath normalizes a user-supplied path relative to workspace.
// Relative paths resolve against workspace (or cwd if workspace is empty).
// Returns the absolute, symlink-evaluated path.
func resolveScopePath(path, workspace string) (string, error) {
	if path == "" {
		path = "."
	}
	if !filepath.IsAbs(path) {
		base := workspace
		if base == "" {
			wd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			base = wd
		}
		path = filepath.Join(base, path)
	}
	path = filepath.Clean(path)

	// EvalSymlinks fails for paths that don't exist yet; for scope checks we
	// still want a canonical form, so fall back to the cleaned absolute
	// path. Tools that actually need the file to exist will fail later.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	return path, nil
}

// pathWithin returns true when target is root or lives underneath it.
// Symmetric with builtin/path_guard.go's helper — kept local to forge so the
// two tool sets stay independent.
func pathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".."
}

// validateScope enforces that path (user-supplied or defaulted) is inside
// workspace and not inside any blockedRoots entry. The error returned is a
// permanent sdkerr with code errCode so the agent sees a clean, actionable
// refusal rather than a shell crash when it tries to grep `/`.
//
// Callers pass the raw requested path as-written — this function handles
// normalization. Returns the resolved absolute path on success so the
// caller can pass it to the underlying command without re-resolving.
func validateScope(path, workspace, errCode string) (string, error) {
	resolved, err := resolveScopePath(path, workspace)
	if err != nil {
		return "", sdkerr.Permanent(errCode, fmt.Sprintf("resolve path %q: %v", path, err))
	}

	for _, blocked := range blockedRoots {
		if pathWithin(blocked, resolved) {
			return "", sdkerr.Permanent(errCode, fmt.Sprintf(
				"path %q is inside blocked system directory %q (agents cannot traverse this tree — it causes pathological scans of read-only system state)",
				resolved, blocked,
			))
		}
	}

	if workspace == "" {
		// No workspace configured means every caller is trusted; still
		// blocklist-gated above.
		return resolved, nil
	}
	wsResolved, err := resolveScopePath(workspace, "")
	if err != nil {
		return "", sdkerr.Permanent(errCode, fmt.Sprintf("resolve workspace %q: %v", workspace, err))
	}
	if !pathWithin(wsResolved, resolved) {
		return "", sdkerr.Permanent(errCode, fmt.Sprintf(
			"path %q is outside the agent workspace %q (tools are scoped to the workspace root — pass a path inside it)",
			resolved, wsResolved,
		))
	}
	return resolved, nil
}
