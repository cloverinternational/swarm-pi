// Checkpoint/snapshot tool for saving the state of work done by agents.
// Ported from ii-agent's save_checkpoint.py
package ii

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

const (
	// SaveCheckpointToolName is the unique identifier for this tool
	SaveCheckpointToolName = "save_checkpoint"

	// SaveCheckpointToolDisplayName is the human-readable name
	SaveCheckpointToolDisplayName = "Save checkpoint"

	// DefaultCommitMessage is used when no commit message is provided
	DefaultCommitMessage = "Checkpoint"

	// DefaultGitUserEmail is the email used for git commits
	DefaultGitUserEmail = "bot@example.com"

	// DefaultGitUserName is the name used for git commits
	DefaultGitUserName = "II Agent"
)

// saveCheckpointDescription provides detailed documentation for the tool
const saveCheckpointDescription = `Save a checkpoint of the work the agents have done. This must be called after the user's task is done, or after a major change has been implemented.

Always call this tool when you have done testing and ensure the required functionalities are implemented.

This tool performs the following steps:
1. Runs the build process (bun run build:local)
2. Cleans up build artifacts (.next-build)
3. Initializes git repository if not present
4. Stages all changes (git add -A)
5. Creates a commit with the provided message
6. Returns the commit revision`

// CommandResult contains the output of a command execution
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// SaveCheckpointTool implements checkpoint/snapshot functionality
type SaveCheckpointTool struct {
	workspaceManager *WorkspaceManager
	gitUserEmail     string
	gitUserName      string
}

// NewSaveCheckpointTool creates a new SaveCheckpointTool
func NewSaveCheckpointTool(wm *WorkspaceManager) *SaveCheckpointTool {
	return &SaveCheckpointTool{
		workspaceManager: wm,
		gitUserEmail:     DefaultGitUserEmail,
		gitUserName:      DefaultGitUserName,
	}
}

// NewSaveCheckpointToolWithConfig creates a SaveCheckpointTool with custom git config
func NewSaveCheckpointToolWithConfig(wm *WorkspaceManager, email, name string) *SaveCheckpointTool {
	if email == "" {
		email = DefaultGitUserEmail
	}
	if name == "" {
		name = DefaultGitUserName
	}
	return &SaveCheckpointTool{
		workspaceManager: wm,
		gitUserEmail:     email,
		gitUserName:      name,
	}
}

// Name returns the tool name
func (t *SaveCheckpointTool) Name() string {
	return SaveCheckpointToolName
}

// DisplayName returns the human-readable display name
func (t *SaveCheckpointTool) DisplayName() string {
	return SaveCheckpointToolDisplayName
}

// Description returns the tool description
func (t *SaveCheckpointTool) Description() string {
	return saveCheckpointDescription
}

// Parameters returns the JSON schema for tool parameters
func (t *SaveCheckpointTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"project_directory": map[string]any{
				"type":        "string",
				"description": "Absolute or workspace-relative path to the project root.",
			},
			"commit_message": map[string]any{
				"type":        "string",
				"description": "Git commit message (default: 'Checkpoint').",
			},
		},
		"required": []string{"project_directory", "commit_message"},
	}
}

// runCommand executes a command and returns the result
func (t *SaveCheckpointTool) runCommand(ctx context.Context, command []string, cwd string) (*CommandResult, error) {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := &CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitError.ExitCode()
		} else {
			// Command not found or other execution error
			return nil, fmt.Errorf("command '%s' is not available: %w", strings.Join(command, " "), err)
		}
	}

	return result, nil
}

// formatFailureMessage creates an error message from command output
func (t *SaveCheckpointTool) formatFailureMessage(prefix, stdout, stderr string) string {
	stderr = strings.TrimSpace(stderr)
	stdout = strings.TrimSpace(stdout)
	if stderr != "" {
		return fmt.Sprintf("%s. stderr: %s", prefix, stderr)
	}
	if stdout != "" {
		return fmt.Sprintf("%s. stdout: %s", prefix, stdout)
	}
	return prefix + "."
}

// ensureGitConfig ensures git user configuration is set
func (t *SaveCheckpointTool) ensureGitConfig(ctx context.Context, projectDir string) (*tools.ToolResult, error) {
	commands := []struct {
		cmd   []string
		stage string
		label string
	}{
		{
			cmd:   []string{"git", "config", "--global", "user.email", t.gitUserEmail},
			stage: "git_config_email",
			label: "git config --global user.email",
		},
		{
			cmd:   []string{"git", "config", "--global", "user.name", t.gitUserName},
			stage: "git_config_name",
			label: "git config --global user.name",
		},
	}

	for _, c := range commands {
		result, err := t.runCommand(ctx, c.cmd, projectDir)
		if err != nil {
			return nil, err
		}

		if result.ExitCode != 0 {
			return tools.NewToolResult(t.formatFailureMessage(c.label+" failed", result.Stdout, result.Stderr)).
				WithMetadata("stage", c.stage).
				WithMetadata("exit_code", result.ExitCode), nil
		}
	}

	return nil, nil
}

// resolveDirectory resolves and validates the project directory path
func (t *SaveCheckpointTool) resolveDirectory(rawPath string) (string, error) {
	path := strings.TrimSpace(rawPath)

	// If workspace manager is available, resolve relative paths
	if t.workspaceManager != nil {
		if !filepath.IsAbs(path) {
			path = filepath.Join(t.workspaceManager.WorkspacePath(), path)
		}
		if err := t.workspaceManager.ValidateExistingDirectoryPath(path); err != nil {
			return "", err
		}
	} else {
		// Without workspace manager, just check if directory exists
		absPath, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("failed to resolve path: %w", err)
		}
		path = absPath

		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("directory does not exist: %s", path)
			}
			return "", fmt.Errorf("failed to stat directory: %w", err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("path is not a directory: %s", path)
		}
	}

	return path, nil
}

// Execute saves a checkpoint of the project
func (t *SaveCheckpointTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "save_checkpoint.context_cancelled")
	default:
	}

	// Extract parameters
	rawPath, ok := params["project_directory"].(string)
	if !ok || rawPath == "" {
		return nil, sdkerr.Permanent("save_checkpoint.missing_project_directory", "project_directory is required")
	}

	commitMessage, ok := params["commit_message"].(string)
	if !ok || commitMessage == "" {
		commitMessage = DefaultCommitMessage
	}

	// Resolve and validate directory
	projectDir, err := t.resolveDirectory(rawPath)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: %s", err.Error())), nil
	}

	// Step 1: Run build
	buildResult, err := t.runCommand(ctx, []string{"bun", "run", "build:local"}, projectDir)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: %s", err.Error())), nil
	}

	if buildResult.ExitCode != 0 {
		return tools.NewToolResult(t.formatFailureMessage("Build failed", buildResult.Stdout, buildResult.Stderr)).
			WithMetadata("stage", "build").
			WithMetadata("exit_code", buildResult.ExitCode), nil
	}

	// Step 2: Clean up build artifacts
	cleanupResult, err := t.runCommand(ctx, []string{"rm", "-rf", ".next-build"}, projectDir)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: %s", err.Error())), nil
	}

	if cleanupResult.ExitCode != 0 {
		return tools.NewToolResult(t.formatFailureMessage("Cleanup failed", cleanupResult.Stdout, cleanupResult.Stderr)).
			WithMetadata("stage", "cleanup").
			WithMetadata("exit_code", cleanupResult.ExitCode), nil
	}

	// Step 3: Ensure git config
	configResult, err := t.ensureGitConfig(ctx, projectDir)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: %s", err.Error())), nil
	}
	if configResult != nil {
		return configResult, nil
	}

	// Step 4: Initialize git if needed
	gitDir := filepath.Join(projectDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		initResult, err := t.runCommand(ctx, []string{"git", "init"}, projectDir)
		if err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: %s", err.Error())), nil
		}

		if initResult.ExitCode != 0 {
			return tools.NewToolResult(t.formatFailureMessage("git init failed", initResult.Stdout, initResult.Stderr)).
				WithMetadata("stage", "git_init").
				WithMetadata("exit_code", initResult.ExitCode), nil
		}
	}

	// Step 5: Stage all changes
	addResult, err := t.runCommand(ctx, []string{"git", "add", "-A"}, projectDir)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: %s", err.Error())), nil
	}

	if addResult.ExitCode != 0 {
		return tools.NewToolResult(t.formatFailureMessage("git add failed", addResult.Stdout, addResult.Stderr)).
			WithMetadata("stage", "git_add").
			WithMetadata("exit_code", addResult.ExitCode), nil
	}

	// Step 6: Create commit
	commitResult, err := t.runCommand(ctx, []string{"git", "commit", "-m", commitMessage}, projectDir)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: %s", err.Error())), nil
	}

	if commitResult.ExitCode != 0 {
		return tools.NewToolResult(t.formatFailureMessage("git commit failed", commitResult.Stdout, commitResult.Stderr)).
			WithMetadata("stage", "git_commit").
			WithMetadata("exit_code", commitResult.ExitCode), nil
	}

	// Step 7: Get revision
	revResult, err := t.runCommand(ctx, []string{"git", "rev-parse", "HEAD"}, projectDir)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: %s", err.Error())), nil
	}

	if revResult.ExitCode != 0 {
		return tools.NewToolResult(t.formatFailureMessage("Could not read git revision", revResult.Stdout, revResult.Stderr)).
			WithMetadata("stage", "git_rev_parse").
			WithMetadata("exit_code", revResult.ExitCode), nil
	}

	revision := strings.TrimSpace(revResult.Stdout)

	// Create success result
	result := tools.NewToolResult(fmt.Sprintf("Checkpoint created at %s", revision))
	result.WithMetadata("project_directory", projectDir)
	result.WithMetadata("revision", revision)
	result.WithMetadata("build_stdout", buildResult.Stdout)
	result.WithMetadata("build_stderr", buildResult.Stderr)
	result.WithMetadata("cleanup_stdout", cleanupResult.Stdout)
	result.WithMetadata("cleanup_stderr", cleanupResult.Stderr)

	return result, nil
}

// Validate checks if the given parameters are valid
func (t *SaveCheckpointTool) Validate(params map[string]any) error {
	rawPath, ok := params["project_directory"].(string)
	if !ok || strings.TrimSpace(rawPath) == "" {
		return fmt.Errorf("project_directory is required")
	}

	// commit_message is optional with a default
	return nil
}

// IsIdempotent returns false as creating checkpoints modifies git state
func (t *SaveCheckpointTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool creates git commits
func (t *SaveCheckpointTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions
func (t *SaveCheckpointTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionFileWrite, tools.PermissionBashExecute}
}

// SupportedContentTypes returns the content types this tool can produce
func (t *SaveCheckpointTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints provides guidance for efficient tool use
func (t *SaveCheckpointTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns confirmation details for checkpoint creation
func (t *SaveCheckpointTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	projectDir, _ := params["project_directory"].(string)
	commitMessage, _ := params["commit_message"].(string)
	if commitMessage == "" {
		commitMessage = DefaultCommitMessage
	}
	return &ConfirmationDetails{
		Type:    ConfirmationTypeBash,
		Message: fmt.Sprintf("Build and create checkpoint in %s with message: '%s'", projectDir, commitMessage),
	}
}

// Metadata returns the tool metadata
func (t *SaveCheckpointTool) Metadata() map[string]any {
	return map[string]any{
		"category": "development",
		"tags":     []string{"checkpoint", "git", "build", "snapshot"},
	}
}

// SetGitConfig allows changing the git user configuration
func (t *SaveCheckpointTool) SetGitConfig(email, name string) {
	if email != "" {
		t.gitUserEmail = email
	}
	if name != "" {
		t.gitUserName = name
	}
}
