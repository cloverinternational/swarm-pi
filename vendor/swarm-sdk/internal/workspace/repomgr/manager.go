package repomgr

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Manager manages workspace lifecycle - creation, listing, and deletion.
type Manager struct {
	baseDir      string
	workspaces   map[string]*Workspace
	cloneCancels map[string]context.CancelFunc
	cloneDone    map[string]chan struct{}
	mu           sync.RWMutex
}

// NewManager creates a new workspace manager with the given base directory.
func NewManager(baseDir string) (*Manager, error) {
	// Create base directory if it doesn't exist
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}

	return &Manager{
		baseDir:      baseDir,
		workspaces:   make(map[string]*Workspace),
		cloneCancels: make(map[string]context.CancelFunc),
		cloneDone:    make(map[string]chan struct{}),
	}, nil
}

// CreateWorkspace clones a repository into a new workspace.
func (m *Manager) CreateWorkspace(ctx context.Context, params CreateWorkspaceParams) (*CreateWorkspaceResult, error) {
	// Validate required fields
	if params.RepoURL == "" {
		return nil, errors.New("repoURL is required")
	}

	// Validate URL format - must have scheme or be a git SSH URL
	parsedURL, err := url.Parse(params.RepoURL)
	if err != nil || (parsedURL.Scheme == "" && !isGitSSHURL(params.RepoURL)) {
		return &CreateWorkspaceResult{
			WorkspaceID: "",
			Path:        "",
			Status:      "error",
			Error:       "invalid repository URL",
		}, nil
	}

	// Generate workspace ID
	id := uuid.New().String()

	// Create workspace directory
	wsPath := filepath.Join(m.baseDir, fmt.Sprintf("ws-%s", id))
	if err := os.MkdirAll(wsPath, 0755); err != nil {
		return &CreateWorkspaceResult{
			WorkspaceID: id,
			Path:        wsPath,
			Status:      "error",
			Error:       fmt.Sprintf("failed to create workspace directory: %v", err),
		}, nil
	}

	// Set default branch
	branch := params.Branch
	if branch == "" {
		branch = "main"
	}

	// Create workspace record with cloning status
	ws := &Workspace{
		ID:      id,
		RepoURL: params.RepoURL,
		Branch:  branch,
		Path:    wsPath,
		Status:  "cloning",
		Created: time.Now(),
	}

	// Store workspace
	cloneCtx, cloneCancel := context.WithCancel(context.Background())
	cloneDone := make(chan struct{})
	m.mu.Lock()
	m.workspaces[id] = ws
	m.cloneCancels[id] = cloneCancel
	m.cloneDone[id] = cloneDone
	m.mu.Unlock()

	// Start async git clone
	go func() {
		defer close(cloneDone)
		m.cloneRepository(cloneCtx, ws)
	}()

	return &CreateWorkspaceResult{
		WorkspaceID: id,
		Path:        wsPath,
		Status:      ws.Status,
	}, nil
}

// cloneRepository performs the git clone operation asynchronously and updates workspace status.
func (m *Manager) cloneRepository(ctx context.Context, ws *Workspace) {
	err := CloneRepo(ctx, CloneParams{
		URL:       ws.RepoURL,
		Branch:    ws.Branch,
		TargetDir: ws.Path,
	})

	m.mu.Lock()
	defer m.mu.Unlock()

	if err != nil {
		ws.Status = "error"
		ws.Error = err.Error()
	} else {
		ws.Status = "ready"
	}
}

// ListWorkspaces returns all workspaces.
func (m *Manager) ListWorkspaces(ctx context.Context) (*ListWorkspacesResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	workspaces := make([]Workspace, 0, len(m.workspaces))
	for _, ws := range m.workspaces {
		workspaces = append(workspaces, *ws)
	}

	return &ListWorkspacesResult{
		Workspaces: workspaces,
	}, nil
}

// DeleteWorkspace removes a workspace and cleans up its files.
func (m *Manager) DeleteWorkspace(ctx context.Context, params DeleteWorkspaceParams) (*DeleteWorkspaceResult, error) {
	// Validate required fields
	if params.WorkspaceID == "" {
		return nil, errors.New("workspaceID is required")
	}

	m.mu.Lock()
	ws, exists := m.workspaces[params.WorkspaceID]
	if !exists {
		m.mu.Unlock()
		return &DeleteWorkspaceResult{Success: false}, nil
	}

	cloneCancel := m.cloneCancels[params.WorkspaceID]
	cloneDone := m.cloneDone[params.WorkspaceID]

	// Remove from map
	delete(m.workspaces, params.WorkspaceID)
	delete(m.cloneCancels, params.WorkspaceID)
	delete(m.cloneDone, params.WorkspaceID)
	m.mu.Unlock()

	if cloneCancel != nil {
		cloneCancel()
	}
	if cloneDone != nil {
		select {
		case <-cloneDone:
		case <-time.After(2 * time.Second):
		}
	}

	// Delete the directory
	if ws.Path != "" {
		if err := os.RemoveAll(ws.Path); err != nil {
			// Log error but still return success since we removed from tracking
			// In production, we might want different behavior
		}
	}

	return &DeleteWorkspaceResult{Success: true}, nil
}

// Shutdown cancels any in-flight clone operations and waits briefly for cleanup.
func (m *Manager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.cloneCancels))
	doneChans := make([]chan struct{}, 0, len(m.cloneDone))
	for _, cancel := range m.cloneCancels {
		cancels = append(cancels, cancel)
	}
	for _, done := range m.cloneDone {
		doneChans = append(doneChans, done)
	}
	m.cloneCancels = make(map[string]context.CancelFunc)
	m.cloneDone = make(map[string]chan struct{})
	m.mu.Unlock()

	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}

	for _, done := range doneChans {
		if done == nil {
			continue
		}
		select {
		case <-done:
		case <-ctx.Done():
			return
		}
	}
}

// isGitSSHURL checks if a URL is a valid git SSH URL (e.g., git@github.com:user/repo.git)
func isGitSSHURL(u string) bool {
	// Git SSH URLs have format: git@host:path or user@host:path
	// They must contain @ and : in the right order
	atIndex := -1
	colonIndex := -1
	for i, c := range u {
		if c == '@' && atIndex == -1 {
			atIndex = i
		}
		if c == ':' && colonIndex == -1 {
			colonIndex = i
		}
	}
	return atIndex > 0 && colonIndex > atIndex
}
