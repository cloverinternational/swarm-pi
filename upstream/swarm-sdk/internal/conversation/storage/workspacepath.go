package storage

import (
	"encoding/base64"
	"path/filepath"
	"strings"
)

const (
	// DefaultWorkspaceDir is the subdirectory name used for conversations that have
	// no workspace path (e.g. deepwiki, standalone tools).
	DefaultWorkspaceDir = "__default__"

	// IndexDir is the subdirectory that holds per-ID index files mapping
	// conversation ID → encoded workspace dir name.
	IndexDir = "_idx"
)

// reservedNonWorkspaceDirs are sibling directories inside the conversations
// root that are NOT workspaces and must never be scanned for conversations.
//
// "debug" is written by the token-anomaly detector, which drops a full
// before/after conversation snapshot next to the trace file on every anomalous
// turn. It does not start with "_" or "-", so the prefix rule below accepted
// it as a workspace and every all-workspaces listing read all of it: measured
// here at 2.37 GB across 3212 files, which showed up as a multi-second,
// multi-gigabyte disk-read spike (RSS 150 MB → 2.4 GB) whenever a listing ran.
var reservedNonWorkspaceDirs = map[string]struct{}{
	"debug": {},
}

// EncodeWorkspacePath converts an absolute filesystem path into a filesystem-safe,
// URL-safe base64 directory name (no padding characters).
//
// Since every absolute path starts with "/" (which encodes to "L"), the resulting
// string always begins with a letter, cleanly distinct from the reserved "_"-prefixed
// names (DefaultWorkspaceDir, IndexDir).
//
// Example:
//
//	"/home/swarm/mono" → "L2hvbWUvc3dhcm0vbW9ubw"
func EncodeWorkspacePath(path string) string {
	if path == "" {
		return DefaultWorkspaceDir
	}
	return base64.RawURLEncoding.EncodeToString([]byte(path))
}

// DecodeWorkspacePath is the inverse of EncodeWorkspacePath.
// Returns ("", nil) for the DefaultWorkspaceDir sentinel.
func DecodeWorkspacePath(encoded string) (string, error) {
	if encoded == DefaultWorkspaceDir || encoded == "" {
		return "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// workspacePathHintForFile decodes the containing workspace directory when path
// points into the partitioned layout. Legacy flat files and invalid directory
// names deliberately provide no hint.
func workspacePathHintForFile(path string) string {
	parent := filepath.Base(filepath.Dir(path))
	if !IsWorkspaceDir(parent) {
		return ""
	}
	workspacePath, err := DecodeWorkspacePath(parent)
	if err != nil {
		return ""
	}
	return workspacePath
}

// IsWorkspaceDir returns true when name represents a workspace directory
// (as opposed to the index dir or other reserved "_"-prefixed dirs).
//
// Workspace dirs are either the DefaultWorkspaceDir sentinel ("__default__") or
// a base64url-encoded path that starts with a letter or digit.
func IsWorkspaceDir(name string) bool {
	if name == "" {
		return false
	}
	if name == DefaultWorkspaceDir {
		return true
	}
	if _, reserved := reservedNonWorkspaceDirs[name]; reserved {
		return false
	}
	// Reserved dirs start with underscore or hyphen.
	// Base64url output for absolute paths (which start with "/") always begins
	// with a letter (e.g. "L2…"), so this distinguishes them safely.
	//
	// NOTE: this stays deliberately permissive. Legacy layouts left behind
	// conversation directories named with UUIDs and with conversation IDs
	// (e.g. "20260320-141336-xan0xi") that are not base64 of a path. Tightening
	// this to "must decode to an absolute path" would hide hundreds of real
	// conversations, so new non-workspace siblings must be listed above.
	return !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, "-")
}
