// Package forge provides 1:1 implementations of Forge's tools.
package forge

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// DefaultTools returns the standard set of Forge tools.
func DefaultTools(workspacePath string) []tools.Tool {
	snapshotDir := ""
	if workspacePath != "" {
		snapshotDir = workspacePath + "/.swarm/snapshots"
	}
	return []tools.Tool{
		NewFSRead(workspacePath),
		NewApplyPatchTool(workspacePath),
		NewFSUndo(workspacePath, snapshotDir),
	}
}

// FileTools returns just the file-related tools.
func FileTools(workspacePath string) []tools.Tool {
	snapshotDir := ""
	if workspacePath != "" {
		snapshotDir = workspacePath + "/.swarm/snapshots"
	}
	return []tools.Tool{
		NewFSRead(workspacePath),
		NewApplyPatchTool(workspacePath),
		NewFSUndo(workspacePath, snapshotDir),
	}
}
