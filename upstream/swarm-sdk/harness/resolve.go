package harness

import (
	"os"
	"path/filepath"
	"strings"
)

// Path resolution policy (documented and tested):
//
//   - Relative asset paths resolve against the MANIFEST directory, never the
//     process CWD. This makes a compiled plan independent of where the tool runs.
//   - Read asset files (system-prompt file, credential file) are containment-
//     guarded: after cleaning AND after evaluating symlinks, the real path must
//     stay within the manifest directory subtree. A "../" escape or a symlink
//     that points outside the subtree is rejected.
//   - Directory settings (workspace, storage) are resolved relative to the
//     manifest directory when relative and accepted as-is when absolute; they
//     are intentionally NOT containment-restricted because a workspace legitimately
//     lives elsewhere. They are resolved to a clean path string only — no
//     stat, create, or write occurs in Phase 1.

// resolveDir resolves a directory setting to a clean absolute path. Empty input
// returns the manifest directory itself (the natural default). No filesystem
// mutation or existence check is performed.
func resolveDir(mdir, ref string) string {
	if ref == "" {
		return mdir
	}
	if filepath.IsAbs(ref) {
		return filepath.Clean(ref)
	}
	return filepath.Clean(filepath.Join(mdir, ref))
}

// resolveContainedFile resolves a read-asset reference against the manifest
// directory and enforces the containment + symlink policy. It returns the real
// (symlink-evaluated) absolute path on success, or a diagnostic.
func resolveContainedFile(sourcePath, fieldPath, mdir, ref string) (string, *Diagnostic) {
	if ref == "" {
		d := newDiag("harness.path.empty", fieldPath, "file reference is empty", sourcePath)
		return "", &d
	}

	// Resolve the logical (pre-symlink) path relative to the manifest dir.
	var joined string
	if filepath.IsAbs(ref) {
		joined = filepath.Clean(ref)
	} else {
		joined = filepath.Clean(filepath.Join(mdir, ref))
	}

	// The manifest dir itself may be under a symlinked root (e.g. /tmp on some
	// systems); evaluate it so containment comparison is apples-to-apples.
	realDir, err := filepath.EvalSymlinks(mdir)
	if err != nil {
		realDir = mdir
	}

	// Pre-symlink containment: reject "../" escapes early with a precise code.
	if escapesDir(realDir, joined) {
		d := newDiag("harness.path.traversal", fieldPath,
			"file reference escapes the manifest directory; paths must stay within the harness file's directory", sourcePath)
		return "", &d
	}

	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if os.IsNotExist(err) {
			d := newDiag("harness.path.missing", fieldPath, "referenced file does not exist", sourcePath)
			return "", &d
		}
		d := newDiag("harness.path.unresolvable", fieldPath, "referenced file could not be resolved", sourcePath)
		return "", &d
	}

	// Post-symlink containment: a symlink target must also stay within the tree.
	if escapesDir(realDir, real) {
		d := newDiag("harness.path.symlinkEscape", fieldPath,
			"file reference resolves (via symlink) outside the manifest directory", sourcePath)
		return "", &d
	}

	return real, nil
}

// escapesDir reports whether target is outside root's subtree. root and target
// must both be clean absolute paths. A target equal to root does not escape.
func escapesDir(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return true
	}
	if rel == "." {
		return false
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// loadContainedFileBytes resolves and reads a contained asset file.
func loadContainedFileBytes(sourcePath, fieldPath, mdir, ref string) ([]byte, string, *Diagnostic) {
	abs, d := resolveContainedFile(sourcePath, fieldPath, mdir, ref)
	if d != nil {
		return nil, "", d
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		nd := newDiag("harness.path.unreadable", fieldPath, "referenced file could not be read", sourcePath)
		return nil, "", &nd
	}
	return data, abs, nil
}
