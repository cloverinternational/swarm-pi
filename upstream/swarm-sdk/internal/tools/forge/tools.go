// Package forge provides 1:1 implementations of Forge's tools.
// These are simple, predictable tools without hash-based verification.
package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/filetracker"
	sdkpaths "github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vision"
)

// ============================================================================
// Helper Functions
// ============================================================================

// requireStringWithContext validates a required string parameter with full context.
// Returns detailed error showing all required fields, what was provided, and correct usage examples.
func requireStringWithContext(params map[string]any, key string, requiredFields []string) (string, error) {
	v, ok := params[key]
	if !ok {
		// Try common aliases for backwards compatibility
		aliases := map[string][]string{
			"file_path": {"file", "path", "filename"},
			"content":   {"contents", "text", "body"},
		}
		if als, hasAls := aliases[key]; hasAls {
			for _, alias := range als {
				if v, ok = params[alias]; ok {
					break
				}
			}
		}
		if !ok || v == nil {
			// Build comprehensive error showing all required fields
			return "", buildMissingParamError(params, requiredFields, key)
		}
	}
	s, ok := v.(string)
	if !ok {
		return "", sdkerr.Permanent(
			fmt.Sprintf("%s.invalid_type", key),
			fmt.Sprintf("%s must be a string, got %T", key, v))
	}
	return s, nil
}

// buildMissingParamError creates a detailed error message showing what's missing and how to fix it.
func buildMissingParamError(params map[string]any, requiredFields []string, missingField string) error {
	// Determine which required fields are present vs missing
	var presentFields []string
	var missingFields []string

	for _, field := range requiredFields {
		if _, ok := params[field]; ok {
			presentFields = append(presentFields, field)
		} else {
			// Check aliases too
			found := false
			if field == "file_path" {
				for _, alias := range []string{"file", "path", "filename"} {
					if _, ok := params[alias]; ok {
						found = true
						break
					}
				}
			} else if field == "content" {
				for _, alias := range []string{"contents", "text", "body"} {
					if _, ok := params[alias]; ok {
						found = true
						break
					}
				}
			}
			if !found {
				missingFields = append(missingFields, field)
			}
		}
	}

	// Build error message
	var sb strings.Builder
	sb.WriteString("Write tool call missing required parameters.\n\n")
	sb.WriteString("REQUIRED FIELDS:\n")
	for _, field := range requiredFields {
		if field == missingField || contains(missingFields, field) {
			sb.WriteString(fmt.Sprintf("  ✗ %s (MISSING)\n", field))
		} else {
			sb.WriteString(fmt.Sprintf("  ✓ %s (provided)\n", field))
		}
	}

	sb.WriteString("\nCORRECT USAGE:\n")
	sb.WriteString(`  Write({
    "file_path": "/absolute/path/to/file.txt",
    "content": "file content here",
    "overwrite": true  // optional, defaults to false
  })`)
	sb.WriteString("\n\nIMPORTANT:\n")
	sb.WriteString("  • file_path MUST be an absolute path (not relative)\n")
	sb.WriteString("  • content is the full text to write\n")
	sb.WriteString("  • Both file_path and content are required\n")

	if len(params) > 0 {
		sb.WriteString("\nYOU PROVIDED:\n")
		sb.WriteString(fmt.Sprintf("  Write(%+v)\n", params))
		sb.WriteString("\nThe tool received these parameters but is missing required fields.")
	} else {
		sb.WriteString("\nYOU PROVIDED:\n")
		sb.WriteString("  Write({})\n")
		sb.WriteString("\nThe tool received an empty parameter object.")
	}

	return sdkerr.Permanent(
		fmt.Sprintf("%s.missing", missingField),
		sb.String())
}

// contains checks if a string slice contains a value
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func requireString(params map[string]any, key string) (string, error) {
	v, ok := params[key]
	if !ok {
		// Try common aliases for backwards compatibility
		aliases := map[string][]string{
			"file_path": {"file", "path", "filename"},
			"content":   {"contents", "text", "body"},
		}
		if als, hasAls := aliases[key]; hasAls {
			for _, alias := range als {
				if v, ok = params[alias]; ok {
					break
				}
			}
			if v == nil {
				return "", sdkerr.Permanent(fmt.Sprintf("%s.missing", key), fmt.Sprintf("%s is required", key))
			}
		} else {
			return "", sdkerr.Permanent(fmt.Sprintf("%s.missing", key), fmt.Sprintf("%s is required", key))
		}
	}
	s, ok := v.(string)
	if !ok {
		return "", sdkerr.Permanent(fmt.Sprintf("%s.invalid_type", key), fmt.Sprintf("%s must be a string", key))
	}
	return s, nil
}

func getString(params map[string]any, key string, defaultVal string) string {
	if v, ok := params[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return defaultVal
}

func getInt(params map[string]any, key string, defaultVal int) int {
	if v, ok := params[key]; ok {
		switch n := v.(type) {
		case int:
			return n
		case int64:
			return int(n)
		case float64:
			return int(n)
		case json.Number:
			if i, err := n.Int64(); err == nil {
				return int(i)
			}
		}
	}
	return defaultVal
}

func getBool(params map[string]any, key string, defaultVal bool) bool {
	if v, ok := params[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return defaultVal
}

func getStringSlice(params map[string]any, key string) []string {
	if v, ok := params[key]; ok {
		if arr, ok := v.([]any); ok {
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		}
		if arr, ok := v.([]string); ok {
			return arr
		}
	}
	return nil
}

// ============================================================================
// File-type detection helpers (shared by FSRead)
// ============================================================================

// supportedReadImageExts are image extensions FSRead can return as vision content.
var supportedReadImageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
}

// fsReadImageExts is the broader set used for "is this an image?" detection.

// ============================================================================
// FSRead - File reading tool (1:1 with Forge)
// ============================================================================

// FSRead reads a file with optional line numbers.
type FSRead struct {
	workspacePath string
	maxFileSize   int64 // Maximum file size in bytes (0 = default 2MB)
}

// NewFSRead creates a new FSRead tool.
func NewFSRead(workspacePath string) *FSRead {
	return &FSRead{workspacePath: workspacePath, maxFileSize: 2 * 1024 * 1024} // 2MB default
}

// NewFSReadWithLimit creates a new FSRead tool with a custom size limit.
func NewFSReadWithLimit(workspacePath string, maxFileSize int64) *FSRead {
	return &FSRead{workspacePath: workspacePath, maxFileSize: maxFileSize}
}

// Name returns the tool name.
func (t *FSRead) Name() string { return "Read" }

// Description returns the tool description.
func (t *FSRead) Description() string {
	return `Views an image file so you can actually see it.

This tool handles IMAGES ONLY (.jpg, .jpeg, .png, .gif, .webp). It returns the
visual content directly, which the shell cannot do.

For every other kind of file, use the shell instead:
  - whole file:   cat FILE
  - a slice:      sed -n 'START,ENDp' FILE
  - with numbers: nl -ba FILE | sed -n 'START,ENDp'
  - first/last:   head -n N FILE  /  tail -n N FILE
  - search:       rg PATTERN        (add -n for line numbers, -t go to filter)
  - find files:   rg --files -g 'PATTERN'`
}

// Parameters returns the JSON schema for the tool parameters.
func (t *FSRead) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the image file (.jpg, .jpeg, .png, .gif, .webp).",
			},
		},
		"required": []string{"file_path"},
	}
}

// makeWhitespaceReal converts the visible whitespace markers used by the
// show_whitespace rendering back into real tabs and spaces.
func makeWhitespaceReal(s string) string {
	s = strings.ReplaceAll(s, "\u27f9", "\t")
	s = strings.ReplaceAll(s, "\u00b7", " ")
	return s
}

// supportedReadImageExtList renders the supported extensions for error text.
func supportedReadImageExtList() string {
	exts := make([]string, 0, len(supportedReadImageExts))
	for ext := range supportedReadImageExts {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	return strings.Join(exts, ", ")
}

// Execute reads an image file and returns it as vision content.
//
// Text reading was deliberately removed: the shell (cat/sed/head/tail/rg) does
// it faster, composes better, and its output is capped so it cannot flood the
// context window. Images are the one case the shell cannot serve, because it
// has no way to return an image content block.
func (t *FSRead) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "fs_read.cancelled")
	default:
	}

	filePath, err := requireString(params, "file_path")
	if err != nil {
		return nil, err
	}

	absPath, err := t.validatePath(ctx, filePath)
	if err != nil {
		return nil, err
	}

	ext := strings.ToLower(filepath.Ext(absPath))
	if !supportedReadImageExts[ext] {
		return tools.NewToolResult(fmt.Sprintf(
			"ERROR: Read handles image files only (%s). For text use the shell, e.g. "+
				"`sed -n '1,200p' %s` to view a slice or `rg PATTERN %s` to search it.",
			supportedReadImageExtList(), absPath, absPath)), nil
	}

	// vision.EncodeImageFile enforces the 5 MB limit and base64-encodes.
	imageData, mediaType, vErr := vision.EncodeImageFile(absPath)
	if vErr != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to read image: %s", vErr.Error())), nil
	}

	fileInfo, stErr := os.Stat(absPath)
	if stErr != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to stat image: %s", stErr.Error())), nil
	}

	imageBlock := tools.ImageContentBase64(imageData, mediaType).
		WithAnnotation("filePath", absPath)
	result := tools.NewContentResult(imageBlock)
	result.AddText(fmt.Sprintf("Image file: %s (%s, %.2f KB)",
		filepath.Base(absPath), mediaType, float64(fileInfo.Size())/1024))

	filetracker.RecordAccess(ctx, absPath, false, filetracker.EstimateTokens(int(fileInfo.Size())))
	return result, nil
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

func (t *FSRead) validatePath(ctx context.Context, path string) (string, error) {
	absPath, err := resolveWorkspacePath(t.workspacePath, path)
	if err != nil {
		return "", sdkerr.Permanent("fs_read.invalid_path", fmt.Sprintf("cannot resolve path: %v", err))
	}
	if t.workspacePath != "" && !strings.HasPrefix(absPath, t.workspacePath) {
		// Also allow reading from ~/.swarm/ — agents need access to their own
		// conversation history, plan files, skills, and other session state.
		if isSwarmosPath(absPath) {
			return absPath, nil
		}
		// Allow paths that were explicitly approved by the user via the
		// interactive permission system. The registry injects approved paths
		// into the context after CheckWithContext returns true.
		if tools.IsPathApproved(ctx, absPath) {
			return absPath, nil
		}
		return "", sdkerr.Permanent("fs_read.path_outside_workspace", "path must be within workspace or ~/.swarm/")
	}
	return absPath, nil
}

// resolveWorkspacePath turns a user-supplied path into an absolute path, anchored
// on the workspace root rather than the process working directory.
//
// Two real-world failure modes are handled here:
//
//  1. Relative paths. A sub-agent's process CWD is not guaranteed to be the
//     workspace root. Resolving "agent/agent_execute.go" with filepath.Abs
//     would anchor on CWD and silently point at the wrong file. We anchor
//     relative paths on workspaceRoot instead.
//
//  2. Duplicated leading segment. Models frequently emit a path that already
//     includes the workspace's final segment, e.g. passing
//     "swarm-sdk/agent/agent_execute.go" when the workspace root is
//     ".../mono/swarm-sdk". Naive joining yields
//     ".../mono/swarm-sdk/swarm-sdk/agent/agent_execute.go" — a path that still
//     lives under the workspace (so the boundary check passes) but does not
//     exist (so the open fails with a confusing "file not found"). When the
//     resolved path is missing but stripping the duplicated segment yields a
//     path that exists, we self-heal to that path.
//
// The returned path is always absolute and cleaned. Existence is NOT required
// (callers may be creating a new file); the self-heal only triggers when it
// produces an existing target.
func resolveWorkspacePath(workspaceRoot, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}

	var absPath string
	if filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	} else if workspaceRoot != "" {
		// Anchor relative paths on the workspace root, not the process CWD.
		absPath = filepath.Clean(filepath.Join(workspaceRoot, path))
	} else {
		resolved, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		absPath = resolved
	}

	// Self-heal a duplicated leading workspace segment when the resolved path
	// doesn't exist but the de-duplicated one does.
	if workspaceRoot != "" {
		if _, err := os.Lstat(absPath); err != nil && os.IsNotExist(err) {
			if healed, ok := dedupWorkspaceSegment(workspaceRoot, absPath); ok {
				absPath = healed
			}
		}
	}

	return absPath, nil
}

// dedupWorkspaceSegment detects a path of the form <root>/<base>/<base>/rest
// (where <base> is filepath.Base(root)) and returns <root>/rest if that target
// exists. Returns ok=false when no de-duplication applies or the target is
// still missing.
func dedupWorkspaceSegment(workspaceRoot, absPath string) (string, bool) {
	root := filepath.Clean(workspaceRoot)
	base := filepath.Base(root)
	if base == "" || base == "." || base == string(os.PathSeparator) {
		return "", false
	}
	prefix := root + string(os.PathSeparator) + base + string(os.PathSeparator)
	if !strings.HasPrefix(absPath, prefix) {
		return "", false
	}
	rest := absPath[len(prefix):]
	candidate := filepath.Join(root, rest)
	if _, err := os.Lstat(candidate); err == nil {
		return candidate, true
	}
	return "", false
}

// isSwarmosPath returns true when absPath is inside the user's ~/.swarm/ directory.
// This gives read tools access to conversation history, plan files, skills, etc.
// without opening the entire home directory.
func isSwarmosPath(absPath string) bool {
	swarmDir := filepath.Clean(sdkpaths.Root())
	return absPath == swarmDir || strings.HasPrefix(absPath, swarmDir+string(os.PathSeparator))
}

// Validate checks parameters.
func (t *FSRead) Validate(params map[string]any) error {
	_, err := requireString(params, "file_path")
	return err
}

// IsIdempotent returns true.
func (t *FSRead) IsIdempotent() bool { return true }

// RequiresPermission returns required permissions.
func (t *FSRead) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionFileRead}
}

// SupportedContentTypes returns supported content types.
func (t *FSRead) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints returns nil.
func (t *FSRead) OptimizationHints() *tools.OptimizationHints { return nil }

// ============================================================================
// FSWrite - File writing tool (1:1 with Forge)
// ============================================================================

// FSWrite writes content to a file.
type FSWrite struct {
	workspacePath string
}

// NewFSWrite creates a new FSWrite tool.
func NewFSWrite(workspacePath string) *FSWrite {
	return &FSWrite{workspacePath: workspacePath}
}

// Name returns the tool name.
func (t *FSWrite) Name() string { return "Write" }

// Description returns the tool description.
func (t *FSWrite) Description() string {
	return `Writes content to a file.

Usage:
- file_path must be an absolute path (not relative)
- content is the text to write to the file
- If the file exists and overwrite is false (default), an error will be returned
- If overwrite is true, existing files will be overwritten

Parameters:
- file_path: The absolute path to the file to write
- content: The content to write to the file
- overwrite: If true, overwrite existing files (default: false)

Examples:
  Write({
    "file_path": "/home/user/project/file.txt",
    "content": "Hello world"
  })

  Write({
    "file_path": "/home/user/project/file.txt",
    "content": "New content",
    "overwrite": true
  })

IMPORTANT: Both file_path and content are required parameters and must be provided
in every Write call. file_path MUST be an absolute path (starting with / on Unix or
C:\ on Windows).`
}

// Parameters returns the JSON schema.
func (t *FSWrite) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to write (e.g., /home/user/project/file.txt) - MUST be absolute, not relative",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to write to the file - this is the full text content",
			},
			"overwrite": map[string]any{
				"type":        "boolean",
				"description": "If true, overwrite existing files (default: false)",
			},
		},
		"required": []string{"file_path", "content"},
		"examples": []map[string]any{
			{
				"file_path": "/home/user/project/config.json",
				"content":   "{\"key\": \"value\"}",
			},
			{
				"file_path": "/home/user/project/README.md",
				"content":   "# My Project\\n\\nDescription here",
				"overwrite": true,
			},
		},
	}
}

// Execute writes to the file.
func (t *FSWrite) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "fs_write.cancelled")
	default:
	}

	filePath, err := requireString(params, "file_path")
	if err != nil {
		return nil, err
	}
	content, err := requireString(params, "content")
	if err != nil {
		return nil, err
	}
	overwrite := getBool(params, "overwrite", false)

	// Hidden compatibility adapter: all writes use the canonical transaction
	// engine so snapshots, rollback, path safety, and file tracking stay uniform.
	return NewApplyPatchTool(t.workspacePath).executeLegacyWrite(ctx, filePath, content, overwrite)
}

// Validate checks parameters.
func (t *FSWrite) Validate(params map[string]any) error {
	// Use enriched validation that provides comprehensive error messages
	requiredFields := []string{"file_path", "content"}

	if _, err := requireStringWithContext(params, "file_path", requiredFields); err != nil {
		return err
	}
	_, err := requireStringWithContext(params, "content", requiredFields)
	return err
}

// IsIdempotent returns false.
func (t *FSWrite) IsIdempotent() bool { return false }

// RequiresPermission returns required permissions.
func (t *FSWrite) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionFileWrite}
}

// SupportedContentTypes returns supported content types.
func (t *FSWrite) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints returns nil.
func (t *FSWrite) OptimizationHints() *tools.OptimizationHints { return nil }

// ============================================================================
// FSPatch - String replacement tool (1:1 with Forge)
// ============================================================================

// FSPatch performs string replacement in a file.
type FSPatch struct {
	workspacePath string
}

// NewFSPatch creates a new FSPatch tool.
func NewFSPatch(workspacePath string) *FSPatch {
	return &FSPatch{workspacePath: workspacePath}
}

// Name returns the tool name.
func (t *FSPatch) Name() string { return "Edit" }

// Description returns the tool description.
func (t *FSPatch) Description() string {
	return `Performs exact string replacement in a file.

Usage:
- file_path must be an absolute path
- old_string is the exact text to replace (must match exactly)
- new_string is the text to replace it with (must be different from old_string)
- replace_all: if true, replace all occurrences (default: false)

Parameters:
- file_path: The absolute path to the file to modify
- old_string: The text to replace
- new_string: The text to replace it with
- replace_all: Replace all occurrences (default: false)`
}

// Parameters returns the JSON schema.
func (t *FSPatch) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to modify",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "The text to replace",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "The text to replace it with",
			},
			"replace_all": map[string]any{
				"type":        "boolean",
				"description": "Replace all occurrences (default: false)",
			},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	}
}

// Execute performs the string replacement.
func (t *FSPatch) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "fs_patch.cancelled")
	default:
	}

	filePath, err := requireString(params, "file_path")
	if err != nil {
		return nil, err
	}
	oldString, err := requireString(params, "old_string")
	if err != nil {
		return nil, err
	}
	newString, err := requireString(params, "new_string")
	if err != nil {
		return nil, err
	}
	replaceAll := getBool(params, "replace_all", false)

	// Convert visible whitespace markers back to actual whitespace
	// This allows agents to copy from read output and use in edits
	oldString = makeWhitespaceReal(oldString)
	newString = makeWhitespaceReal(newString)

	// Validate
	if oldString == newString {
		return nil, sdkerr.Permanent("fs_patch.same_strings", "old_string and new_string must be different")
	}
	if oldString == "" {
		return nil, sdkerr.Permanent("fs_patch.empty_old", "old_string cannot be empty")
	}

	// Hidden compatibility adapter: exact replacements share the same preflight,
	// snapshot, rollback, and file-tracking engine as apply_patch.
	return NewApplyPatchTool(t.workspacePath).executeLegacyReplace(ctx, filePath, oldString, newString, replaceAll)
}

// Validate checks parameters.
func (t *FSPatch) Validate(params map[string]any) error {
	if _, err := requireString(params, "file_path"); err != nil {
		return err
	}
	if _, err := requireString(params, "old_string"); err != nil {
		return err
	}
	_, err := requireString(params, "new_string")
	return err
}

// IsIdempotent returns false.
func (t *FSPatch) IsIdempotent() bool { return false }

// RequiresPermission returns required permissions.
func (t *FSPatch) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionFileWrite}
}

// SupportedContentTypes returns supported content types.
func (t *FSPatch) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints returns nil.
func (t *FSPatch) OptimizationHints() *tools.OptimizationHints { return nil }

// ============================================================================
// FSSearch - Grep/search tool (1:1 with Forge)
// ============================================================================

// OutputMode controls how search results are displayed.
type OutputMode string

const (
	OutputModeContent          OutputMode = "content"
	OutputModeFilesWithMatches OutputMode = "files_with_matches"
	OutputModeCount            OutputMode = "count"
)

// FSSearch searches for patterns in files using ripgrep.
type FSSearch struct {
	workspacePath string
}

// NewFSSearch creates a new FSSearch tool.
func NewFSSearch(workspacePath string) *FSSearch {
	return &FSSearch{workspacePath: workspacePath}
}

// Name returns the tool name.
func (t *FSSearch) Name() string { return "Grep" }

// Description returns the tool description.
func (t *FSSearch) Description() string {
	return `Searches for patterns in files using ripgrep.

Usage:
- pattern: The regular expression pattern to search for
- path: File or directory to search in (defaults to current working directory)
- glob: Glob pattern to filter files (e.g., "*.js", "*.{ts,tsx}")
- output_mode: "content" (show lines), "files_with_matches" (show paths), "count" (show counts)
- -B: Lines before match (context)
- -A: Lines after match (context)
- -C: Lines before and after match (context)
- -n: Show line numbers (default: true for content mode)
- -i: Case insensitive search
- type: File type to search (e.g., "js", "py", "rust", "go")
- head_limit: Limit output to first N lines/entries
- offset: Skip first N lines/entries
- multiline: Enable multiline mode (. matches newlines)`
}

// Parameters returns the JSON schema.
func (t *FSSearch) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "The regular expression pattern to search for",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "File or directory to search in",
			},
			"glob": map[string]any{
				"type":        "string",
				"description": "Glob pattern to filter files (e.g., *.js, *.{ts,tsx})",
			},
			"output_mode": map[string]any{
				"type":        "string",
				"enum":        []string{"content", "files_with_matches", "count"},
				"description": "Output mode (default: files_with_matches)",
			},
			"-B": map[string]any{
				"type":        "integer",
				"description": "Lines before match (context)",
			},
			"-A": map[string]any{
				"type":        "integer",
				"description": "Lines after match (context)",
			},
			"-C": map[string]any{
				"type":        "integer",
				"description": "Lines before and after match (context)",
			},
			"-n": map[string]any{
				"type":        "boolean",
				"description": "Show line numbers (default: true for content)",
			},
			"-i": map[string]any{
				"type":        "boolean",
				"description": "Case insensitive search",
			},
			"type": map[string]any{
				"type":        "string",
				"description": "File type (js, py, rust, go, etc.)",
			},
			"head_limit": map[string]any{
				"type":        "integer",
				"description": "Limit output to first N results",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Skip first N results",
			},
			"multiline": map[string]any{
				"type":        "boolean",
				"description": "Enable multiline mode",
			},
		},
		"required": []string{"pattern"},
	}
}

// buildSearchArgs assembles the ripgrep argv for a search.
//
// Two hyphen hazards are handled here, and they are the reason this is a
// separate, unit-testable function rather than inline argv fiddling:
//
//  1. The PATTERN must never be parsed as a flag. Searching for a CSS custom
//     property (`--ops-shell-muted`) or a CLI flag (`--dry-run`) is completely
//     ordinary, and positional-pattern rg answers those with
//     "unrecognized flag --dry-run", or worse, silently reinterprets
//     `-infinity|infinity` as a PATH and reports "No such file or directory".
//     Passing the pattern as the value of `-e` makes it pattern data no matter
//     what it starts with. `-e` is preferred over a bare `--` separator
//     because a search PATH follows the pattern here; `--` would only protect
//     whichever argument came first.
//  2. The PATH still follows, so an explicit `--` is emitted before it. The
//     path is always an absolute, scope-validated path today, but the
//     separator keeps that a local invariant rather than a distant one.
//
// The pattern is passed through verbatim — never escaped or rewritten — so an
// actually invalid regex still surfaces ripgrep's own regex parse error.
func buildSearchArgs(params map[string]any, pattern, resolvedPath string) []string {
	args := []string{}

	// Output mode
	outputMode := getString(params, "output_mode", "files_with_matches")
	switch outputMode {
	case "content":
		args = append(args, "--line-number")
	case "files_with_matches":
		args = append(args, "--files-with-matches")
	case "count":
		args = append(args, "--count")
	}

	// Context lines
	if v := getInt(params, "-B", 0); v > 0 {
		args = append(args, "-B", strconv.Itoa(v))
	}
	if v := getInt(params, "-A", 0); v > 0 {
		args = append(args, "-A", strconv.Itoa(v))
	}
	if v := getInt(params, "-C", 0); v > 0 {
		args = append(args, "-C", strconv.Itoa(v))
	}

	// Other options
	if getBool(params, "-i", false) {
		args = append(args, "-i")
	}
	if getBool(params, "multiline", false) {
		args = append(args, "-U", "--multiline-dotall")
	}
	if v := getInt(params, "head_limit", 0); v > 0 {
		args = append(args, "-m", strconv.Itoa(v))
	}

	// Glob and type
	if glob := getString(params, "glob", ""); glob != "" {
		args = append(args, "--glob", glob)
	}
	if fileType := getString(params, "type", ""); fileType != "" {
		args = append(args, "--type", fileType)
	}

	// Pattern — always as the value of -e, never positional.
	args = append(args, "-e", pattern)

	// End of options, then the search path.
	args = append(args, "--", resolvedPath)

	return args
}

// Execute performs the search.
func (t *FSSearch) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "fs_search.cancelled")
	default:
	}

	pattern, err := requireString(params, "pattern")
	if err != nil {
		return nil, err
	}

	// Path — must live inside the configured workspace. Without this guard,
	// agents asked to "search the repo" but given a default-cwd workspace
	// have in the wild walked /nix/store, /sys, and /tmp/nix-shell.*, which
	// hangs rg for minutes and trashes the context window.
	searchPath := getString(params, "path", t.workspacePath)
	resolvedPath, err := validateScope(searchPath, t.workspacePath, "fs_search.invalid_path")
	if err != nil {
		return nil, err
	}

	args := buildSearchArgs(params, pattern, resolvedPath)

	// Execute ripgrep
	cmd := exec.CommandContext(ctx, "rg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// rg returns exit code 1 when no matches found
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 1 {
			return tools.NewToolResult("No matches found"), nil
		}
		return nil, sdkerr.Wrap(fmt.Errorf("%s", string(output)), "fs_search.failed")
	}

	return tools.NewToolResult(string(output)), nil
}

func (t *FSSearch) Validate(params map[string]any) error {
	_, err := requireString(params, "pattern")
	return err
}

// IsIdempotent returns true.
func (t *FSSearch) IsIdempotent() bool { return true }

// RequiresPermission returns required permissions.
func (t *FSSearch) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionFileRead}
}

// SupportedContentTypes returns supported content types.
func (t *FSSearch) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints returns nil.
func (t *FSSearch) OptimizationHints() *tools.OptimizationHints { return nil }

// ============================================================================
// FSUndo - Undo last edit (1:1 with Forge)
// ============================================================================

// FSUndo reverts a file to its previous state.
type FSUndo struct {
	workspacePath string
	snapshotDir   string
}

// NewFSUndo creates a new FSUndo tool.
func NewFSUndo(workspacePath, snapshotDir string) *FSUndo {
	return &FSUndo{workspacePath: workspacePath, snapshotDir: snapshotDir}
}

// Name returns the tool name.
func (t *FSUndo) Name() string { return "Undo" }

// Description returns the tool description.
func (t *FSUndo) Description() string {
	return `Reverts a file to its previous state.

Usage:
- path: The absolute path of the file to revert

This tool reverts a file to its previous state using a snapshot.
Snapshots are automatically created before edits.`
}

// Parameters returns the JSON schema.
func (t *FSUndo) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The absolute path of the file to revert",
			},
		},
		"required": []string{"path"},
	}
}

// Execute performs the undo.
func (t *FSUndo) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "fs_undo.cancelled")
	default:
	}

	filePath, err := requireString(params, "path")
	if err != nil {
		return nil, err
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, sdkerr.Permanent("fs_undo.invalid_path", fmt.Sprintf("cannot resolve path: %v", err))
	}

	// Find latest snapshot
	if t.snapshotDir == "" {
		return nil, sdkerr.Permanent("fs_undo.no_snapshots", "snapshot directory not configured")
	}

	entries, err := os.ReadDir(t.snapshotDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, sdkerr.Permanent("fs_undo.no_snapshot",
				fmt.Sprintf("no snapshot found for %s (no edits have been tracked yet)", absPath))
		}
		return nil, sdkerr.Permanent("fs_undo.no_snapshots",
			fmt.Sprintf("cannot read snapshot directory: %v", err))
	}

	baseName := filepath.Base(absPath)
	var latestSnap string
	var latestTime int64

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), baseName+"-") && strings.HasSuffix(entry.Name(), ".bak") {
			// Extract timestamp from name: filename-1234567890.bak
			name := entry.Name()
			tsStr := strings.TrimSuffix(strings.TrimPrefix(name, baseName+"-"), ".bak")
			if ts, err := strconv.ParseInt(tsStr, 10, 64); err == nil {
				if ts > latestTime {
					latestTime = ts
					latestSnap = entry.Name()
				}
			}
		}
	}

	if latestSnap == "" {
		return nil, sdkerr.Permanent("fs_undo.no_snapshot", fmt.Sprintf("no snapshot found for %s", absPath))
	}

	// If the snapshot was taken for a newly created file, delete it.
	bakPath := filepath.Join(t.snapshotDir, latestSnap)
	if isNewFileSnapshot(bakPath) {
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			return nil, sdkerr.Wrap(err, "fs_undo.delete_failed")
		}
		msg := formatUndoMessage(absPath, latestSnap, t.snapshotDir)
		consumeSnapshot(bakPath)
		return tools.NewToolResult(msg), nil
	}

	// Read snapshot
	content, err := os.ReadFile(bakPath)
	if err != nil {
		return nil, sdkerr.Wrap(err, "fs_undo.read_failed")
	}

	// Restore file
	if err := os.WriteFile(absPath, content, 0644); err != nil {
		return nil, sdkerr.Wrap(err, "fs_undo.write_failed")
	}

	msg := formatUndoMessage(absPath, latestSnap, t.snapshotDir)
	// Consume the snapshot that was just applied. Undo previously picked the
	// same "latest by embedded timestamp" .bak file on every call without
	// ever retiring it, so a second call restored byte-identical content
	// from the SAME snapshot instead of walking one step further back in
	// history -- reported (with git diff staying unchanged across two
	// "Successfully reverted" responses citing the identical snapshot
	// timestamp) as issue #238. Removing the just-applied snapshot here
	// means the NEXT Undo call naturally finds the next-most-recent
	// remaining snapshot for this path, or correctly reports
	// fs_undo.no_snapshot once the history for this file is exhausted --
	// exactly the two acceptance tests in #238.
	consumeSnapshot(bakPath)
	return tools.NewToolResult(msg), nil
}

// consumeSnapshot removes a .bak snapshot and its companion .ctx.json after
// FSUndo has successfully applied it, so it is never restored a second time.
// Best-effort: a snapshot that is already gone (concurrent Undo, manual
// cleanup) is not treated as a failure.
func consumeSnapshot(bakPath string) {
	_ = os.Remove(bakPath)
	_ = os.Remove(snapshotContextPath(bakPath))
}

// Validate checks parameters.
func (t *FSUndo) Validate(params map[string]any) error {
	_, err := requireString(params, "path")
	return err
}

// IsIdempotent returns false.
func (t *FSUndo) IsIdempotent() bool { return false }

// RequiresPermission returns required permissions.
func (t *FSUndo) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionFileWrite}
}

// SupportedContentTypes returns supported content types.
func (t *FSUndo) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints returns nil.
func (t *FSUndo) OptimizationHints() *tools.OptimizationHints { return nil }

// ============================================================================
// Shell - Command execution tool (1:1 with Forge)
// ============================================================================

// Shell executes shell commands.
type Shell struct {
	workspacePath string
}

// NewShell creates a new Shell tool.
func NewShell(workspacePath string) *Shell {
	return &Shell{workspacePath: workspacePath}
}

// Name returns the tool name.
func (t *Shell) Name() string { return "Shell" }

// Description returns the tool description.
func (t *Shell) Description() string {
	return `Executes a shell command.

Usage:
- command: The shell command to execute
- cwd: Working directory (defaults to current directory)
- keep_ansi: Whether to preserve ANSI codes in output (default: false)
- env: Environment variable names to pass (e.g., ["PATH", "HOME"])
- description: Brief description of what the command does (5-10 words)

The output will include the exit code and duration.`
}

// Parameters returns the JSON schema.
func (t *Shell) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute",
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Working directory for command execution",
			},
			"keep_ansi": map[string]any{
				"type":        "boolean",
				"description": "Preserve ANSI codes in output (default: false)",
			},
			"env": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Environment variable names to pass",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Brief description of what the command does",
			},
		},
		"required": []string{"command"},
	}
}

// Execute runs the command.
func (t *Shell) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "shell.cancelled")
	default:
	}

	command, err := requireString(params, "command")
	if err != nil {
		return nil, err
	}

	// Build command
	cmd := exec.CommandContext(ctx, "sh", "-c", command)

	// Working directory — same scope rules as FSSearch. A shell session
	// that cds into /nix/store or /sys can drag an agent into a pathological
	// loop just as easily as a rogue rg.
	cwd := getString(params, "cwd", t.workspacePath)
	resolvedCwd, err := validateScope(cwd, t.workspacePath, "shell.invalid_cwd")
	if err != nil {
		return nil, err
	}
	cmd.Dir = resolvedCwd

	// Environment
	envNames := getStringSlice(params, "env")
	if len(envNames) > 0 {
		cmd.Env = os.Environ()
		for _, name := range envNames {
			if val, ok := os.LookupEnv(name); ok {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", name, val))
			}
		}
	}

	// Execute
	start := time.Now()
	output, execErr := cmd.CombinedOutput()
	duration := time.Since(start)

	// Build result
	var result strings.Builder
	exitCode := 0
	if execErr != nil {
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	result.WriteString(fmt.Sprintf("[exit_code=%d | %s]\n", exitCode, duration.Truncate(time.Millisecond)))
	result.Write(output)

	return tools.NewToolResult(result.String()), nil
}

// Validate checks parameters.
func (t *Shell) Validate(params map[string]any) error {
	_, err := requireString(params, "command")
	return err
}

// IsIdempotent returns false.
func (t *Shell) IsIdempotent() bool { return false }

// RequiresPermission returns required permissions.
func (t *Shell) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionBashExecute}
}

// SupportedContentTypes returns supported content types.
func (t *Shell) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints returns nil.
func (t *Shell) OptimizationHints() *tools.OptimizationHints { return nil }
