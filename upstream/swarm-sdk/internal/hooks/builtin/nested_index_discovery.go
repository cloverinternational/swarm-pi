package builtin

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// FileReadRegistrar records directories where the agent has read files,
// so nested INDEX.md discovery can find repository indexes in those
// directories on the next context refresh. This mirrors Claude Code's
// nestedMemoryAttachmentTriggers pattern.
type FileReadRegistrar interface {
	RegisterFileRead(filePath string)
}

// NestedIndexDiscoveryHook listens for file-reading tool completions
// and registers the parent directory with a FileReadRegistrar.
//
// When the agent reads a file in a subdirectory (e.g., swarm-sdk/client/client.go),
// this hook records that directory (swarm-sdk/client/) so the context system
// can check for INDEX.md files there on the next refresh.
//
// This is the Swarm equivalent of Claude Code's nestedMemoryAttachmentTriggers:
//   - Claude Code: FileReadTool adds paths → getNestedMemoryAttachments discovers CLAUDE.md
//   - Swarm:       NestedIndexDiscoveryHook adds paths → ContextOrchestrator discovers INDEX.md
type NestedIndexDiscoveryHook struct {
	registrar    FileReadRegistrar
	workspaceDir string
	logger       observability.Logger
}

// NewNestedIndexDiscoveryHook creates a hook that registers file-reading
// directories with the given registrar.
func NewNestedIndexDiscoveryHook(registrar FileReadRegistrar, workspaceDir string) *NestedIndexDiscoveryHook {
	return &NestedIndexDiscoveryHook{
		registrar:    registrar,
		workspaceDir: workspaceDir,
	}
}

// SetLogger sets the logger for the hook.
func (h *NestedIndexDiscoveryHook) SetLogger(logger observability.Logger) {
	h.logger = logger
}

const (
	// NestedIndexDiscoveryPriority runs late (low priority number = runs first,
	// so we use a high number to run after more important hooks).
	NestedIndexDiscoveryPriority = 90

	// File-reading tool names that should trigger INDEX.md discovery.
	// These are the tool names used by the SDK's tool registry.
	fileReadTools = "file_read,read_file,Read,file_edit,file_write,Write,Edit"
)

// Name returns the hook name.
func (h *NestedIndexDiscoveryHook) Name() string { return "nested-index-discovery" }

// Priority returns the hook priority (runs late, observation-only).
func (h *NestedIndexDiscoveryHook) Priority() int { return NestedIndexDiscoveryPriority }

// Filter returns true for tool.after_execute events.
func (h *NestedIndexDiscoveryHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolAfterExecute
}

// OnEvent extracts file paths from file-reading tools and registers them.
func (h *NestedIndexDiscoveryHook) OnEvent(_ context.Context, event hooks.Event) (hooks.HookResult, error) {
	if h.registrar == nil {
		return hooks.Continue(), nil
	}

	toolName, _ := event.Data["tool_name"].(string)
	if !isFileReadTool(toolName) {
		return hooks.Continue(), nil
	}

	if h.logger != nil {
		h.logger.Debug(context.Background(), "nested_index_discovery.on_event",
			observability.F("tool", toolName),
		)
	}

	// Only process successful executions
	output, _ := event.Data["tool_output"].(map[string]any)
	if success, ok := output["success"].(bool); ok && !success {
		return hooks.Continue(), nil
	}

	// Extract file path from params
	params, _ := event.Data["params"].(map[string]any)
	if params == nil {
		params, _ = event.Data["tool_input"].(map[string]any)
	}
	if params == nil {
		return hooks.Continue(), nil
	}

	filePath := extractFilePath(params)
	if filePath == "" {
		return hooks.Continue(), nil
	}

	// Resolve to absolute path if relative
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(h.workspaceDir, filePath)
	}

	h.registrar.RegisterFileRead(filePath)
	if h.logger != nil {
		h.logger.Info(context.Background(), "nested_index_discovery.registered",
			observability.F("file_path", filePath),
			observability.F("tool", toolName),
		)
	}
	return hooks.Continue(), nil
}

// isFileReadTool checks if the tool name is a file-reading tool.
func isFileReadTool(name string) bool {
	for t := range strings.SplitSeq(fileReadTools, ",") {
		if name == strings.TrimSpace(t) {
			return true
		}
	}
	return false
}

// extractFilePath extracts a file path from tool parameters.
// Different tools use different parameter names for the file path.
func extractFilePath(params map[string]any) string {
	// Common parameter names across different tools
	pathKeys := []string{"file_path", "path", "filePath", "filename"}

	for _, key := range pathKeys {
		if v, ok := params[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
