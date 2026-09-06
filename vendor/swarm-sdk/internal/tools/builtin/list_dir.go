package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// Constants for list_dir
const (
	ListDirDefaultOffset = 1
	ListDirDefaultLimit  = 100
	ListDirDefaultDepth  = 2
	ListDirMaxEntryLen   = 500
	ListDirIndentSpaces  = 2
)

// ListDirParams are the typed parameters for the list_dir tool.
type ListDirParams struct {
	DirPath string `json:"dir_path" description:"Absolute path to the directory to list" required:"true"`
	Offset  int    `json:"offset"   description:"1-indexed entry to start from (default: 1)"`
	Limit   int    `json:"limit"    description:"Max entries to return (default: 100)"`
	Depth   int    `json:"depth"    description:"Max recursion depth (default: 2)"`
	Pattern string `json:"pattern"  description:"Glob filter for filenames e.g. '*.go', '*.{ts,tsx}'"`
}

// ListDirTool implements recursive directory listing with pagination
type ListDirTool struct {
	tools.BaseTool

	allowedPaths []string
}

// ListDirToolConfig configures ListDirTool behavior
type ListDirToolConfig struct {
	AllowedPaths []string
}

// DefaultListDirConfig returns default configuration
func DefaultListDirConfig() ListDirToolConfig {
	return ListDirToolConfig{
		AllowedPaths: defaultAllowedPaths(),
	}
}

// NewListDirTool creates a ListDirTool with default configuration, wrapped as a typed Tool.
func NewListDirTool() tools.Tool {
	return NewListDirToolWithConfig(DefaultListDirConfig())
}

// NewListDirToolWithConfig creates a ListDirTool with custom configuration, wrapped as a typed Tool.
func NewListDirToolWithConfig(config ListDirToolConfig) tools.Tool {
	t := &ListDirTool{allowedPaths: config.AllowedPaths}
	return tools.Typed[ListDirParams](t)
}

// Name returns the tool name
func (t *ListDirTool) Name() string {
	return "list_dir"
}

// Description returns the tool description
func (t *ListDirTool) Description() string {
	return `List a directory recursively with pagination and glob filtering. Entries are alphabetical; directories end with / and symlinks with @.`
}

// Parameters returns the JSON schema for tool parameters.
func (t *ListDirTool) Parameters() any {
	return tools.SchemaFor[ListDirParams]()
}

// Run executes the list_dir tool with typed parameters.
func (t *ListDirTool) Run(ctx context.Context, params ListDirParams) (*tools.ToolResult, error) {
	// Check context first
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "list_dir.context_cancelled")
	default:
	}

	// Build args with defaults applied for zero values
	args := &listDirArgs{
		dirPath: params.DirPath,
		pattern: params.Pattern,
		offset:  params.Offset,
		limit:   params.Limit,
		depth:   params.Depth,
	}
	if args.offset == 0 {
		args.offset = ListDirDefaultOffset
	}
	if args.limit == 0 {
		args.limit = ListDirDefaultLimit
	}
	if args.depth == 0 {
		args.depth = ListDirDefaultDepth
	}

	// Validate path is absolute
	if !filepath.IsAbs(args.dirPath) {
		return nil, sdkerr.Permanent("list_dir.relative_path",
			"dir_path must be an absolute path")
	}

	// Check path exists and is a directory
	info, err := os.Stat(args.dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, sdkerr.Permanent("list_dir.path_not_found",
				fmt.Sprintf("path not found: %s", args.dirPath))
		}
		return nil, sdkerr.Permanent("list_dir.path_error",
			fmt.Sprintf("unable to access `%s`: %v", args.dirPath, err))
	}

	if !info.IsDir() {
		return nil, sdkerr.Permanent("list_dir.not_directory",
			fmt.Sprintf("`%s` is not a directory", args.dirPath))
	}

	// Check path restrictions
	if err := t.checkPathAllowed(args.dirPath); err != nil {
		return nil, err
	}

	// Collect directory entries
	entries, err := t.collectEntries(ctx, args.dirPath, args.depth, args.pattern)
	if err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return tools.NewXMLResult(tools.NewXML("result").
			Attr("path", args.dirPath).
			AttrInt("entry_count", 0).
			AttrBool("truncated", false)), nil
	}

	// Apply offset and limit
	startIndex := args.offset - 1 // Convert to 0-indexed
	if startIndex >= len(entries) {
		return nil, sdkerr.Permanent("list_dir.offset_exceeded",
			"offset exceeds directory entry count")
	}

	remainingEntries := len(entries) - startIndex
	cappedLimit := min(args.limit, remainingEntries)
	endIndex := startIndex + cappedLimit

	selectedEntries := entries[startIndex:endIndex]

	// Sort selected entries by name
	sort.Slice(selectedEntries, func(i, j int) bool {
		return selectedEntries[i].sortKey < selectedEntries[j].sortKey
	})

	// Build XML output
	truncated := endIndex < len(entries)
	b := tools.NewXML("result").
		Attr("path", args.dirPath).
		AttrInt("entry_count", int64(len(selectedEntries))).
		AttrBool("truncated", truncated)

	for _, entry := range selectedEntries {
		entryType := "file"
		switch entry.kind {
		case entryKindDirectory:
			entryType = "dir"
		case entryKindSymlink:
			entryType = "sym"
		case entryKindOther:
			entryType = "other"
		}
		b.SelfClose("entry",
			fmt.Sprintf(`type=%q`, entryType),
			fmt.Sprintf(`name=%q`, entry.displayName),
			fmt.Sprintf(`depth="%d"`, entry.depth),
		)
	}

	return tools.NewXMLResult(b), nil
}

// listDirArgs holds parsed parameters
// listDirArgs holds parsed parameters
type listDirArgs struct {
	dirPath string
	offset  int
	limit   int
	depth   int
	pattern string // optional glob filter
}

// dirEntry represents a directory entry for listing
type dirEntry struct {
	sortKey     string // Full relative path for sorting
	displayName string // Name to display (file/dir name only)
	depth       int    // Nesting level (0 = root)
	kind        dirEntryKind
}

// dirEntryKind represents the type of directory entry
type dirEntryKind int

const (
	entryKindFile dirEntryKind = iota
	entryKindDirectory
	entryKindSymlink
	entryKindOther
)

// collectEntries performs BFS traversal to collect directory entries
func (t *ListDirTool) collectEntries(ctx context.Context, rootPath string, maxDepth int, pattern string) ([]dirEntry, error) {
	var entries []dirEntry

	type queueItem struct {
		absPath        string
		relativePrefix string
		remainingDepth int
	}

	queue := []queueItem{{rootPath, "", maxDepth}}

	for len(queue) > 0 {
		// Check context
		select {
		case <-ctx.Done():
			return nil, sdkerr.Wrap(ctx.Err(), "list_dir.context_cancelled")
		default:
		}

		// Dequeue first item
		current := queue[0]
		queue = queue[1:]

		// Read directory contents
		dirEntries, err := os.ReadDir(current.absPath)
		if err != nil {
			return nil, sdkerr.Permanent("list_dir.read_error",
				fmt.Sprintf("failed to read directory: %v", err))
		}

		// Process entries in sorted order
		var processedEntries []struct {
			absPath string
			relPath string
			kind    dirEntryKind
			entry   dirEntry
		}

		for _, de := range dirEntries {
			name := de.Name()
			var relPath string
			if current.relativePrefix == "" {
				relPath = name
			} else {
				relPath = current.relativePrefix + "/" + name
			}

			displayName := truncateString(name, ListDirMaxEntryLen)
			displayDepth := strings.Count(current.relativePrefix, "/")
			if current.relativePrefix != "" {
				displayDepth++
			}
			sortKey := truncateString(relPath, ListDirMaxEntryLen)

			kind := getEntryKind(de)

			// Apply glob filter: directories always show, files must match pattern.
			if pattern != "" && kind == entryKindFile {
				matched, _ := filepath.Match(pattern, name)
				if !matched {
					continue
				}
			}

			processedEntries = append(processedEntries, struct {
				absPath string
				relPath string
				kind    dirEntryKind
				entry   dirEntry
			}{
				absPath: filepath.Join(current.absPath, name),
				relPath: relPath,
				kind:    kind,
				entry: dirEntry{
					sortKey:     sortKey,
					displayName: displayName,
					depth:       displayDepth,
					kind:        kind,
				},
			})
		}

		// Sort by sort key
		sort.Slice(processedEntries, func(i, j int) bool {
			return processedEntries[i].entry.sortKey < processedEntries[j].entry.sortKey
		})

		// Add entries and queue subdirectories
		for _, pe := range processedEntries {
			if pe.kind == entryKindDirectory && current.remainingDepth > 1 {
				queue = append(queue, queueItem{
					absPath:        pe.absPath,
					relativePrefix: pe.relPath,
					remainingDepth: current.remainingDepth - 1,
				})
			}
			entries = append(entries, pe.entry)
		}
	}

	return entries, nil
}

// getEntryKind determines the kind of a directory entry
func getEntryKind(entry os.DirEntry) dirEntryKind {
	info, err := entry.Info()
	if err != nil {
		return entryKindOther
	}

	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return entryKindSymlink
	}
	if mode.IsDir() {
		return entryKindDirectory
	}
	if mode.IsRegular() {
		return entryKindFile
	}
	return entryKindOther
}

// truncateString truncates a string to maxLen, respecting UTF-8 boundaries
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	// Find a safe truncation point
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}

// checkPathAllowed verifies the path is within allowed paths (if configured)
func (t *ListDirTool) checkPathAllowed(absPath string) error {
	return checkAllowedPath(absPath, t.allowedPaths, "list_dir.path_not_allowed")
}

// IsIdempotent returns true as list_dir doesn't modify state
func (t *ListDirTool) IsIdempotent() bool {
	return true
}

// RequiresPermission returns the required permissions for this tool
func (t *ListDirTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionFileRead}
}

// SupportedContentTypes returns the content types this tool can produce
func (t *ListDirTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints provides guidance for efficient tool use
func (t *ListDirTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// SupportsParallel returns true because directory listings are safe to execute concurrently.
// Listing directories is a read-only operation with no side effects or shared mutable state.
func (t *ListDirTool) SupportsParallel() bool {
	return true
}
