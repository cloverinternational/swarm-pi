// Package inspection provides bounded, read-only repository text inspection.
package inspection

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

const (
	defaultLimit           = 200
	maxListLimit           = 1000
	maxMatchLimit          = 1000
	maxReadLines           = 500
	maxFileBytes           = 1 << 20
	maxSearchFiles         = 2000
	maxSearchScanBytes     = 16 << 20
	maxSearchResponseBytes = 256 << 10
	maxMatchTextBytes      = 4 << 10
)

// Tool performs bounded list, search, and read operations beneath one workspace.
// It has no mutation, process execution, or network primitives.
type Tool struct {
	tools.BaseTool
	root         string
	rootHandle   *os.Root
	rootFS       fs.FS
	afterResolve func(path string)
}

// New constructs a repository inspection tool confined to workspaceRoot.
func New(workspaceRoot string) (*Tool, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return nil, sdkerr.Permanent("repository_inspect.missing_workspace", "workspace root is required")
	}
	absolute, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, sdkerr.Permanent("repository_inspect.invalid_workspace", fmt.Sprintf("resolve workspace root: %v", err))
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, sdkerr.Permanent("repository_inspect.invalid_workspace", fmt.Sprintf("resolve workspace root symlinks: %v", err))
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return nil, sdkerr.Permanent("repository_inspect.invalid_workspace", "workspace root must be an existing directory")
	}
	rootHandle, err := os.OpenRoot(resolved)
	if err != nil {
		return nil, sdkerr.Permanent("repository_inspect.invalid_workspace", fmt.Sprintf("open workspace root: %v", err))
	}
	return &Tool{
		root:       filepath.Clean(resolved),
		rootHandle: rootHandle,
		rootFS:     rootHandle.FS(),
	}, nil
}

// Name returns the stable tool identifier.
func (*Tool) Name() string { return "repository_inspect" }

// Description explains the bounded read-only inspection surface.
func (*Tool) Description() string {
	return "Safely inspects repository text without shell or network access. Use operation=list to enumerate bounded paths, operation=search to find bounded literal or regular-expression matches, and operation=read to read a bounded line range. Every path is confined to the workspace, including after symlink resolution; binary and oversized files are not returned."
}

// Parameters returns the JSON schema accepted by Execute.
func (*Tool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{"type": "string", "enum": []string{"list", "search", "read"}},
			"path":      map[string]any{"type": "string", "description": "Workspace-relative file or directory path. Defaults to the workspace root."},
			"query":     map[string]any{"type": "string", "description": "Required search text or RE2 regular expression."},
			"regex":     map[string]any{"type": "boolean", "description": "Interpret query as an RE2 regular expression."},
			"line":      map[string]any{"type": "integer", "minimum": 1, "description": "First 1-based line for read. Defaults to 1."},
			"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": maxListLimit, "description": "Maximum entries, matches, or lines returned."},
		},
		"required": []string{"operation"},
	}
}

// IsIdempotent reports that inspection never mutates repository state.
func (*Tool) IsIdempotent() bool { return true }

// IsSafeRepositoryInspector marks this tool as the safe inspection capability.
func (*Tool) IsSafeRepositoryInspector() bool { return true }

// Execute performs one bounded repository inspection operation.
func (tool *Tool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	operation, _ := params["operation"].(string)
	path, _ := params["path"].(string)
	target, err := tool.resolve(path)
	if err != nil {
		return nil, err
	}
	if tool.afterResolve != nil {
		tool.afterResolve(target)
	}
	switch operation {
	case "list":
		limit, err := parseLimit(params, defaultLimit, maxListLimit)
		if err != nil {
			return nil, err
		}
		return tool.list(ctx, target, limit)
	case "search":
		limit, err := parseLimit(params, defaultLimit, maxMatchLimit)
		if err != nil {
			return nil, err
		}
		query, _ := params["query"].(string)
		if query == "" {
			return nil, sdkerr.Permanent("repository_inspect.missing_query", "query is required for search")
		}
		useRegex, _ := params["regex"].(bool)
		return tool.search(ctx, target, query, useRegex, limit)
	case "read":
		limit, err := parseLimit(params, defaultLimit, maxReadLines)
		if err != nil {
			return nil, err
		}
		line := intParam(params, "line", 1)
		if line < 1 {
			return nil, sdkerr.Permanent("repository_inspect.invalid_line", "line must be at least 1")
		}
		return tool.read(target, line, limit)
	default:
		return nil, sdkerr.Permanent("repository_inspect.invalid_operation", "operation must be list, search, or read")
	}
}

func (tool *Tool) resolve(path string) (string, error) {
	if path == "" {
		path = "."
	}
	if filepath.IsAbs(path) {
		return "", sdkerr.Permanent("repository_inspect.path_outside_workspace", "path must be workspace-relative")
	}
	candidate := filepath.Clean(filepath.Join(tool.root, path))
	if !within(tool.root, candidate) {
		return "", sdkerr.Permanent("repository_inspect.path_outside_workspace", "path escapes workspace")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", sdkerr.Permanent("repository_inspect.path_invalid", fmt.Sprintf("resolve path: %v", err))
	}
	if !within(tool.root, resolved) {
		return "", sdkerr.Permanent("repository_inspect.path_outside_workspace", "path escapes workspace through a symlink")
	}
	relative, err := filepath.Rel(tool.root, resolved)
	if err != nil {
		return "", sdkerr.Permanent("repository_inspect.path_invalid", fmt.Sprintf("make path workspace-relative: %v", err))
	}
	return filepath.ToSlash(relative), nil
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

type listEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

func (tool *Tool) list(ctx context.Context, target string, limit int) (*tools.ToolResult, error) {
	info, err := fs.Stat(tool.rootFS, target)
	if err != nil {
		return nil, sdkerr.Permanent("repository_inspect.list_failed", err.Error())
	}
	entries := make([]listEntry, 0, limit)
	truncated := false
	if info.IsDir() {
		err = fs.WalkDir(tool.rootFS, target, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if path == target {
				return nil
			}
			if len(entries) == limit {
				truncated = true
				return fs.SkipAll
			}
			entries = append(entries, listEntry{Path: filepath.ToSlash(path), Type: entryType(entry)})
			return nil
		})
	} else {
		entries = append(entries, listEntry{Path: filepath.ToSlash(target), Type: "file"})
	}
	if err != nil {
		return nil, sdkerr.Permanent("repository_inspect.list_failed", err.Error())
	}
	return jsonResult(map[string]any{"operation": "list", "entries": entries, "truncated": truncated})
}

type searchMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

func (tool *Tool) search(ctx context.Context, target, query string, useRegex bool, limit int) (*tools.ToolResult, error) {
	var expression *regexp.Regexp
	var err error
	if useRegex {
		expression, err = regexp.Compile(query)
		if err != nil {
			return nil, sdkerr.Permanent("repository_inspect.invalid_regex", err.Error())
		}
	}
	matches := make([]searchMatch, 0, limit)
	truncated := false
	scannedFiles := 0
	scannedBytes := int64(0)
	responseBytes := 0
	searchFile := func(path string) error {
		info, statErr := fs.Stat(tool.rootFS, path)
		if statErr != nil || !info.Mode().IsRegular() {
			return nil
		}
		if scannedFiles >= maxSearchFiles ||
			scannedBytes+info.Size() > maxSearchScanBytes {
			truncated = true
			return fs.SkipAll
		}
		scannedFiles++
		scannedBytes += info.Size()
		data, readErr := tool.readText(path)
		if readErr != nil {
			return nil
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		scanner.Buffer(make([]byte, 64*1024), maxFileBytes)
		for line := 1; scanner.Scan(); line++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			text := scanner.Text()
			found := strings.Contains(text, query)
			if expression != nil {
				found = expression.MatchString(text)
			}
			if !found {
				continue
			}
			if len(matches) == limit {
				truncated = true
				return fs.SkipAll
			}
			match := searchMatch{
				Path: filepath.ToSlash(path),
				Line: line,
				Text: truncateUTF8(text, maxMatchTextBytes),
			}
			encoded, marshalErr := json.Marshal(match)
			if marshalErr != nil {
				return marshalErr
			}
			if responseBytes+len(encoded)+1 > maxSearchResponseBytes-1024 {
				truncated = true
				return fs.SkipAll
			}
			responseBytes += len(encoded) + 1
			matches = append(matches, match)
		}
		return scanner.Err()
	}
	info, err := fs.Stat(tool.rootFS, target)
	if err != nil {
		return nil, sdkerr.Permanent("repository_inspect.search_failed", err.Error())
	}
	if info.IsDir() {
		err = fs.WalkDir(tool.rootFS, target, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			return searchFile(path)
		})
	} else {
		err = searchFile(target)
	}
	if err != nil && err != fs.SkipAll {
		return nil, sdkerr.Permanent("repository_inspect.search_failed", err.Error())
	}
	result, err := jsonResult(map[string]any{
		"operation":     "search",
		"matches":       matches,
		"truncated":     truncated,
		"scanned_files": scannedFiles,
		"scanned_bytes": scannedBytes,
	})
	if err != nil {
		return nil, err
	}
	if len(result.Output) > maxSearchResponseBytes {
		return nil, sdkerr.Permanent("repository_inspect.response_too_large", "bounded search response exceeded internal limit")
	}
	return result, nil
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

func (tool *Tool) read(target string, line, limit int) (*tools.ToolResult, error) {
	data, err := tool.readText(target)
	if err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	start := line - 1
	if start > len(lines) {
		start = len(lines)
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}
	return jsonResult(map[string]any{
		"operation": "read",
		"path":      filepath.ToSlash(target),
		"line":      line,
		"content":   strings.Join(lines[start:end], ""),
		"truncated": start > 0 || end < len(lines),
	})
}

func (tool *Tool) readText(path string) ([]byte, error) {
	file, err := tool.rootHandle.Open(path)
	if err != nil {
		return nil, sdkerr.Permanent("repository_inspect.read_failed", err.Error())
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, sdkerr.Permanent("repository_inspect.read_failed", err.Error())
	}
	if !info.Mode().IsRegular() {
		return nil, sdkerr.Permanent("repository_inspect.not_file", "path is not a regular file")
	}
	if info.Size() > maxFileBytes {
		return nil, sdkerr.Permanent("repository_inspect.file_too_large", fmt.Sprintf("file exceeds %d bytes", maxFileBytes))
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return nil, sdkerr.Permanent("repository_inspect.read_failed", err.Error())
	}
	if len(data) > maxFileBytes {
		return nil, sdkerr.Permanent("repository_inspect.file_too_large", fmt.Sprintf("file exceeds %d bytes", maxFileBytes))
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return nil, sdkerr.Permanent("repository_inspect.not_text", "file is not UTF-8 repository text")
	}
	return data, nil
}

func parseLimit(params map[string]any, fallback, maximum int) (int, error) {
	limit := intParam(params, "limit", fallback)
	if limit < 1 || limit > maximum {
		return 0, sdkerr.Permanent("repository_inspect.invalid_limit", fmt.Sprintf("limit must be between 1 and %d", maximum))
	}
	return limit, nil
}

func intParam(params map[string]any, key string, fallback int) int {
	value, ok := params[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func entryType(entry fs.DirEntry) string {
	if entry.IsDir() {
		return "directory"
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return "symlink"
	}
	return "file"
}

func jsonResult(value any) (*tools.ToolResult, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, sdkerr.Permanent("repository_inspect.encode_failed", err.Error())
	}
	return tools.NewToolResult(string(data)), nil
}

var _ tools.SafeRepositoryInspector = (*Tool)(nil)
