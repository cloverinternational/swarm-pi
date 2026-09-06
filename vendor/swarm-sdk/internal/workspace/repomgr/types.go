// Package workspace provides workspace management for the mobile console app.
// Workspaces are isolated directories containing cloned repositories where
// agent conversations can operate.
package repomgr

import (
	"time"
)

// Workspace represents a cloned repository workspace.
// Contract: features/specs/012-mobile-console-app/contracts/ipc/workspace-list.json
type Workspace struct {
	ID      string    `json:"id"`
	RepoURL string    `json:"repoURL"`
	Branch  string    `json:"branch,omitempty"`
	Path    string    `json:"path"`
	Status  string    `json:"status"` // cloning, ready, error
	Error   string    `json:"error,omitempty"`
	Created time.Time `json:"created"`
}

// CreateWorkspaceParams are the parameters for creating a workspace.
// Contract: features/specs/012-mobile-console-app/contracts/ipc/workspace-create.json
type CreateWorkspaceParams struct {
	RepoURL string `json:"repoURL"`
	Branch  string `json:"branch,omitempty"`
}

// CreateWorkspaceResult is the result of creating a workspace.
// Contract: features/specs/012-mobile-console-app/contracts/ipc/workspace-create.json
type CreateWorkspaceResult struct {
	WorkspaceID string `json:"workspaceID"`
	Path        string `json:"path"`
	Status      string `json:"status"` // cloning, ready, error
	Error       string `json:"error,omitempty"`
}

// ListWorkspacesResult is the result of listing workspaces.
// Contract: features/specs/012-mobile-console-app/contracts/ipc/workspace-list.json
type ListWorkspacesResult struct {
	Workspaces []Workspace `json:"workspaces"`
}

// DeleteWorkspaceParams are the parameters for deleting a workspace.
// Contract: features/specs/012-mobile-console-app/contracts/ipc/workspace-delete.json
type DeleteWorkspaceParams struct {
	WorkspaceID string `json:"workspaceID"`
}

// DeleteWorkspaceResult is the result of deleting a workspace.
// Contract: features/specs/012-mobile-console-app/contracts/ipc/workspace-delete.json
type DeleteWorkspaceResult struct {
	Success bool `json:"success"`
}
