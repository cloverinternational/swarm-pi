// Package ii provides tool implementations ported from ii-agent.
// These tools follow the ii-agent patterns and naming conventions.
package ii

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ConfirmationType represents the type of confirmation needed
type ConfirmationType string

const (
	ConfirmationTypeEdit ConfirmationType = "edit"
	ConfirmationTypeBash ConfirmationType = "bash"
	ConfirmationTypeMCP  ConfirmationType = "mcp"
)

// ConfirmationDetails contains details for tool execution confirmation
type ConfirmationDetails struct {
	Type    ConfirmationType
	Message string
}

// FileEditResultContent represents the result of a file edit operation
type FileEditResultContent struct {
	Type       string `json:"type"`
	OldContent string `json:"old_content"`
	NewContent string `json:"new_content"`
}

// NewFileEditResultContent creates a new FileEditResultContent
func NewFileEditResultContent(oldContent, newContent string) *FileEditResultContent {
	return &FileEditResultContent{
		Type:       "file_edit",
		OldContent: oldContent,
		NewContent: newContent,
	}
}

// WorkspaceManager manages file system operations within a designated workspace directory
type WorkspaceManager struct {
	workspacePath string
}

// NewWorkspaceManager creates a new WorkspaceManager
func NewWorkspaceManager(workspacePath string) (*WorkspaceManager, error) {
	absPath, err := filepath.Abs(workspacePath)
	if err != nil {
		return nil, &WorkspaceError{Message: "failed to resolve workspace path: " + err.Error()}
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &WorkspaceError{Message: "workspace path does not exist: " + absPath}
		}
		return nil, &WorkspaceError{Message: "failed to stat workspace path: " + err.Error()}
	}

	if !info.IsDir() {
		return nil, &WorkspaceError{Message: "workspace path is not a directory: " + absPath}
	}

	return &WorkspaceManager{workspacePath: absPath}, nil
}

// GetWorkspacePath returns the absolute path to the workspace directory
func (w *WorkspaceManager) WorkspacePath() string {
	return w.workspacePath
}

// ValidateBoundary checks if a given path is within the workspace directory or other allowed paths
func (w *WorkspaceManager) ValidateBoundary(path string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	// Clean and compare paths
	cleanPath := filepath.Clean(absPath)
	cleanWorkspace := filepath.Clean(w.workspacePath)

	// Check if path is workspace or inside it
	if cleanPath == cleanWorkspace || strings.HasPrefix(cleanPath, cleanWorkspace+string(os.PathSeparator)) {
		return true
	}

	// Also allow /tmp directory for temporary file access
	tmpDir := os.TempDir()
	cleanTmp := filepath.Clean(tmpDir)
	if cleanPath == cleanTmp || strings.HasPrefix(cleanPath, cleanTmp+string(os.PathSeparator)) {
		return true
	}

	// Allow the SwarmOS root (~/.swarm) — conversation history, plan files,
	// skills, session state, agent config, images, etc.
	swarmRoot := filepath.Clean(paths.Root())
	if cleanPath == swarmRoot || strings.HasPrefix(cleanPath, swarmRoot+string(os.PathSeparator)) {
		return true
	}

	return false
}

// ValidatePath validates that path is absolute and within workspace boundary
func (w *WorkspaceManager) ValidatePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return &FileSystemValidationError{Message: "path cannot be empty"}
	}

	if !filepath.IsAbs(path) {
		return &FileSystemValidationError{Message: "path is not absolute: " + path}
	}

	if !w.ValidateBoundary(path) {
		return &FileSystemValidationError{Message: "path is not within workspace boundary: " + path}
	}

	return nil
}

// ValidateExistingFilePath validates that file_path exists and is a file
func (w *WorkspaceManager) ValidateExistingFilePath(filePath string) error {
	if err := w.ValidatePath(filePath); err != nil {
		return err
	}

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &FileSystemValidationError{Message: "file does not exist: " + filePath}
		}
		return &FileSystemValidationError{Message: "failed to stat file: " + err.Error()}
	}

	if info.IsDir() {
		return &FileSystemValidationError{Message: "path exists but is not a file: " + filePath}
	}

	return nil
}

// ValidateExistingDirectoryPath validates that directory_path exists and is a directory
func (w *WorkspaceManager) ValidateExistingDirectoryPath(dirPath string) error {
	if err := w.ValidatePath(dirPath); err != nil {
		return err
	}

	info, err := os.Stat(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &FileSystemValidationError{Message: "directory does not exist: " + dirPath}
		}
		return &FileSystemValidationError{Message: "failed to stat directory: " + err.Error()}
	}

	if !info.IsDir() {
		return &FileSystemValidationError{Message: "path exists but is not a directory: " + dirPath}
	}

	return nil
}

// WorkspaceError represents a workspace-related error
type WorkspaceError struct {
	Message string
}

func (e *WorkspaceError) Error() string {
	return e.Message
}

// FileSystemValidationError represents a file system validation error
type FileSystemValidationError struct {
	Message string
}

func (e *FileSystemValidationError) Error() string {
	return e.Message
}

// IITool extends the base Tool interface with ii-agent specific methods
type IITool interface {
	tools.Tool

	// DisplayName returns the human-readable display name
	DisplayName() string

	// IsReadOnly returns whether this tool only reads data
	IsReadOnly() bool

	// ShouldConfirmExecute determines if execution should be confirmed
	ShouldConfirmExecute(params map[string]any) *ConfirmationDetails

	// Metadata returns optional metadata for the tool
	Metadata() map[string]any
}

// DefaultAllowedPaths returns the default allowed paths for tool operations
func DefaultAllowedPaths() []string {
	paths := []string{}

	// Add current working directory
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths, cwd)
	}

	// Add home directory
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, home)
	}

	// Add /tmp for temporary files
	paths = append(paths, os.TempDir())

	return paths
}

// CheckAllowedPath verifies a path is within allowed paths
func CheckAllowedPath(absPath string, allowedPaths []string) error {
	if len(allowedPaths) == 0 {
		return nil // No restrictions
	}

	cleanPath := filepath.Clean(absPath)
	for _, allowed := range allowedPaths {
		cleanAllowed := filepath.Clean(allowed)
		if cleanPath == cleanAllowed || strings.HasPrefix(cleanPath, cleanAllowed+string(os.PathSeparator)) {
			return nil
		}
	}

	return &FileSystemValidationError{Message: "path not in allowed paths: " + absPath}
}

// ToolContext provides context for tool execution
type ToolContext struct {
	Context          context.Context
	WorkspaceManager *WorkspaceManager
}

// NewToolContext creates a new ToolContext
func NewToolContext(ctx context.Context, workspacePath string) (*ToolContext, error) {
	wm, err := NewWorkspaceManager(workspacePath)
	if err != nil {
		return nil, err
	}
	return &ToolContext{
		Context:          ctx,
		WorkspaceManager: wm,
	}, nil
}
