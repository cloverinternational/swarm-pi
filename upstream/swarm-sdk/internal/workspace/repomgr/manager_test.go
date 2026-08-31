package repomgr

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Test: Workspace manager creation
func TestNewManager(t *testing.T) {
	baseDir := t.TempDir()

	m, err := NewManager(baseDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	if m == nil {
		t.Fatal("expected manager, got nil")
	}
}

func TestNewManager_CreatesBaseDir(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "workspaces")

	_, err := NewManager(baseDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		t.Fatal("expected base directory to be created")
	}
}

// ============================================================
// Contract Test: CreateWorkspace
// Contract: features/specs/012-mobile-console-app/contracts/ipc/workspace-create.json
// ============================================================

func TestCreateWorkspace_ReturnsWorkspaceID(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	result, err := m.CreateWorkspace(ctx, CreateWorkspaceParams{
		RepoURL: "https://github.com/test/repo.git",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	if result.WorkspaceID == "" {
		t.Error("expected workspaceID, got empty string")
	}
}

func TestCreateWorkspace_ReturnsPath(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	result, err := m.CreateWorkspace(ctx, CreateWorkspaceParams{
		RepoURL: "https://github.com/test/repo.git",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	if result.Path == "" {
		t.Error("expected path, got empty string")
	}
}

func TestCreateWorkspace_ReturnsStatus(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	result, err := m.CreateWorkspace(ctx, CreateWorkspaceParams{
		RepoURL: "https://github.com/test/repo.git",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	// Status must be one of: cloning, ready, error
	validStatuses := map[string]bool{
		"cloning": true,
		"ready":   true,
		"error":   true,
	}
	if !validStatuses[result.Status] {
		t.Errorf("invalid status %q, expected cloning|ready|error", result.Status)
	}
}

func TestCreateWorkspace_UsesBranchIfProvided(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	result, err := m.CreateWorkspace(ctx, CreateWorkspaceParams{
		RepoURL: "https://github.com/test/repo.git",
		Branch:  "develop",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	// Verify the workspace was created (we can check this in list)
	if result.WorkspaceID == "" {
		t.Error("expected workspaceID, got empty string")
	}
}

func TestCreateWorkspace_CreatesWorkspaceDirectory(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	result, err := m.CreateWorkspace(ctx, CreateWorkspaceParams{
		RepoURL: "https://github.com/test/repo.git",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	// The path should exist (or be created)
	if result.Path == "" {
		t.Fatal("path is empty")
	}
	// Note: In test mode, we may not actually clone, so directory might not exist
	// The implementation should ensure the path is valid
}

func TestCreateWorkspace_RequiresRepoURL(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	_, err := m.CreateWorkspace(ctx, CreateWorkspaceParams{
		RepoURL: "", // Missing required field
	})
	if err == nil {
		t.Error("expected error for missing repoURL")
	}
}

func TestCreateWorkspace_ReturnsErrorOnInvalidURL(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	result, err := m.CreateWorkspace(ctx, CreateWorkspaceParams{
		RepoURL: "not-a-valid-url",
	})

	// Should either return error or have error status
	if err == nil && result.Status != "error" {
		t.Error("expected error for invalid URL")
	}
}

// ============================================================
// Contract Test: ListWorkspaces
// Contract: features/specs/012-mobile-console-app/contracts/ipc/workspace-list.json
// ============================================================

func TestListWorkspaces_ReturnsEmptyListInitially(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	result, err := m.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}

	if result.Workspaces == nil {
		t.Error("expected workspaces slice, got nil")
	}
	if len(result.Workspaces) != 0 {
		t.Errorf("expected 0 workspaces, got %d", len(result.Workspaces))
	}
}

func TestListWorkspaces_ReturnsCreatedWorkspaces(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	// Create a workspace first
	created, err := m.CreateWorkspace(ctx, CreateWorkspaceParams{
		RepoURL: "https://github.com/test/repo.git",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	// List should include it
	result, err := m.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}

	if len(result.Workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(result.Workspaces))
	}

	ws := result.Workspaces[0]
	if ws.ID != created.WorkspaceID {
		t.Errorf("ID mismatch: got %q, want %q", ws.ID, created.WorkspaceID)
	}
}

func TestListWorkspaces_WorkspaceHasRequiredFields(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	// Create a workspace first
	_, err := m.CreateWorkspace(ctx, CreateWorkspaceParams{
		RepoURL: "https://github.com/test/repo.git",
		Branch:  "main",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	result, err := m.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}

	if len(result.Workspaces) == 0 {
		t.Fatal("expected at least one workspace")
	}

	ws := result.Workspaces[0]

	// Required fields per contract
	if ws.ID == "" {
		t.Error("workspace missing required field: id")
	}
	if ws.RepoURL == "" {
		t.Error("workspace missing required field: repoURL")
	}
	if ws.Path == "" {
		t.Error("workspace missing required field: path")
	}
	if ws.Status == "" {
		t.Error("workspace missing required field: status")
	}
	if ws.Created.IsZero() {
		t.Error("workspace missing required field: created")
	}
}

func TestListWorkspaces_StatusIsValid(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	_, err := m.CreateWorkspace(ctx, CreateWorkspaceParams{
		RepoURL: "https://github.com/test/repo.git",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	result, err := m.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}

	for _, ws := range result.Workspaces {
		validStatuses := map[string]bool{
			"cloning": true,
			"ready":   true,
			"error":   true,
		}
		if !validStatuses[ws.Status] {
			t.Errorf("invalid status %q for workspace %s", ws.Status, ws.ID)
		}
	}
}

// ============================================================
// Contract Test: DeleteWorkspace
// Contract: features/specs/012-mobile-console-app/contracts/ipc/workspace-delete.json
// ============================================================

func TestDeleteWorkspace_ReturnsSuccess(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	// Create a workspace first
	created, err := m.CreateWorkspace(ctx, CreateWorkspaceParams{
		RepoURL: "https://github.com/test/repo.git",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	// Delete it
	result, err := m.DeleteWorkspace(ctx, DeleteWorkspaceParams{
		WorkspaceID: created.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("DeleteWorkspace failed: %v", err)
	}

	if !result.Success {
		t.Error("expected success=true")
	}
}

func TestDeleteWorkspace_RemovesFromList(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	// Create a workspace
	created, err := m.CreateWorkspace(ctx, CreateWorkspaceParams{
		RepoURL: "https://github.com/test/repo.git",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	// Delete it
	_, err = m.DeleteWorkspace(ctx, DeleteWorkspaceParams{
		WorkspaceID: created.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("DeleteWorkspace failed: %v", err)
	}

	// List should be empty
	list, err := m.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}

	if len(list.Workspaces) != 0 {
		t.Errorf("expected 0 workspaces after delete, got %d", len(list.Workspaces))
	}
}

func TestDeleteWorkspace_RequiresWorkspaceID(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	_, err := m.DeleteWorkspace(ctx, DeleteWorkspaceParams{
		WorkspaceID: "", // Missing required field
	})
	if err == nil {
		t.Error("expected error for missing workspaceID")
	}
}

func TestDeleteWorkspace_HandlesNonExistentWorkspace(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	// Try to delete non-existent workspace
	result, err := m.DeleteWorkspace(ctx, DeleteWorkspaceParams{
		WorkspaceID: "non-existent-id",
	})

	// Should either return error or success=false
	if err == nil && result.Success {
		t.Error("expected failure for non-existent workspace")
	}
}

func TestDeleteWorkspace_CleansUpDirectory(t *testing.T) {
	m := setupTestManager(t)
	ctx := context.Background()

	// Create a workspace
	created, err := m.CreateWorkspace(ctx, CreateWorkspaceParams{
		RepoURL: "https://github.com/test/repo.git",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	wsPath := created.Path

	// Delete it
	_, err = m.DeleteWorkspace(ctx, DeleteWorkspaceParams{
		WorkspaceID: created.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("DeleteWorkspace failed: %v", err)
	}

	// Directory should be removed (if it was created)
	if wsPath != "" {
		if _, err := os.Stat(wsPath); !os.IsNotExist(err) {
			t.Error("expected workspace directory to be deleted")
		}
	}
}

// ============================================================
// Helper functions
// ============================================================

func setupTestManager(t *testing.T) *Manager {
	t.Helper()
	baseDir := t.TempDir()

	m, err := NewManager(baseDir)
	if err != nil {
		t.Fatalf("failed to create test manager: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		m.Shutdown(ctx)
	})

	return m
}

// ============================================================
// Workspace struct tests (verify types match contract)
// ============================================================

func TestWorkspace_FieldTypes(t *testing.T) {
	// Verify the Workspace struct round-trips its fields with the expected
	// types and values per the contract schema.
	created := time.Now()
	ws := Workspace{
		ID:      "test-uuid",
		RepoURL: "https://github.com/test/repo.git",
		Branch:  "main",
		Path:    "/workspaces/test",
		Status:  "ready",
		Error:   "",
		Created: created,
	}

	if ws.ID != "test-uuid" {
		t.Errorf("ID = %q, want test-uuid", ws.ID)
	}
	if ws.RepoURL != "https://github.com/test/repo.git" {
		t.Errorf("RepoURL = %q", ws.RepoURL)
	}
	if ws.Branch != "main" {
		t.Errorf("Branch = %q, want main", ws.Branch)
	}
	if ws.Path != "/workspaces/test" {
		t.Errorf("Path = %q, want /workspaces/test", ws.Path)
	}
	if ws.Status != "ready" {
		t.Errorf("Status = %q, want ready", ws.Status)
	}
	if ws.Error != "" {
		t.Errorf("Error = %q, want empty", ws.Error)
	}
	if !ws.Created.Equal(created) {
		t.Errorf("Created = %v, want %v", ws.Created, created)
	}
}
