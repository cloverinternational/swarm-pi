// Package workspace provides isolated execution contexts for agents.
package workspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// FilesystemWorkspace implements Workspace for local filesystem access.
// It provides sandboxed access to a directory tree with permission controls.
type FilesystemWorkspace struct {
	root        string
	permissions *Permissions
	logger      observability.Logger
	tracer      observability.Tracer

	// Metrics tracking
	mu      sync.RWMutex
	metrics WorkspaceMetrics

	// State
	closed bool
}

// FilesystemConfig configures a filesystem workspace.
type FilesystemConfig struct {
	// Root is the base directory for this workspace.
	Root string

	// Permissions define access controls.
	Permissions *Permissions

	// Logger for structured logging.
	Logger observability.Logger

	// Tracer for distributed tracing.
	Tracer observability.Tracer
}

// NewFilesystemWorkspace creates a new filesystem workspace.
func NewFilesystemWorkspace(config FilesystemConfig) (*FilesystemWorkspace, error) {
	// Validate root
	if config.Root == "" {
		return nil, sdkerr.Permanent("workspace.invalid_config", "root directory required")
	}

	// Resolve to absolute path
	absRoot, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, sdkerr.Permanent("workspace.invalid_root", fmt.Sprintf("invalid root path: %v", err))
	}

	// Check if root exists
	info, err := os.Stat(absRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, sdkerr.Permanent("workspace.root_not_found", fmt.Sprintf("root directory does not exist: %s", absRoot))
		}
		return nil, sdkerr.Wrap(err, "workspace.stat_failed")
	}

	if !info.IsDir() {
		return nil, sdkerr.Permanent("workspace.root_not_dir", fmt.Sprintf("root is not a directory: %s", absRoot))
	}

	// Default permissions if not provided
	perms := config.Permissions
	if perms == nil {
		perms = &Permissions{
			AllowedOperations: []Operation{OpRead, OpWrite, OpDelete, OpList, OpStat},
			AllowHiddenFiles:  false,
			AllowSymlinks:     false,
			ExecutionPolicy:   ExecutionInteractive,
		}
	}

	ws := &FilesystemWorkspace{
		root:        absRoot,
		permissions: perms,
		logger:      config.Logger,
		tracer:      config.Tracer,
	}

	if ws.logger != nil {
		ws.logger.Info(context.Background(), "workspace.created",
			observability.F("type", "filesystem"),
			observability.F("root", absRoot))
	}

	return ws, nil
}

// Type returns "filesystem".
func (ws *FilesystemWorkspace) Type() string {
	return "filesystem"
}

// Root returns the root directory path.
func (ws *FilesystemWorkspace) Root() string {
	return ws.root
}

// Resolve converts a relative path to an absolute path within the workspace.
func (ws *FilesystemWorkspace) Resolve(ctx context.Context, path string) (string, error) {
	ctx, span := ws.startSpan(ctx, "workspace.resolve")
	defer span.End()

	if err := ws.checkClosed(); err != nil {
		return "", err
	}

	// Clean the path to prevent directory traversal
	cleaned := filepath.Clean(path)

	// Join with root
	absPath := filepath.Join(ws.root, cleaned)

	// Verify it's still within workspace
	if !strings.HasPrefix(absPath, ws.root) {
		span.SetStatus(observability.StatusCodeError, "path escapes workspace")
		return "", sdkerr.Permanent("workspace.path_escape", fmt.Sprintf("path escapes workspace: %s", path))
	}

	span.SetAttribute("resolved_path", absPath)
	return absPath, nil
}

// List returns entries matching the pattern.
func (ws *FilesystemWorkspace) List(ctx context.Context, pattern string) ([]Entry, error) {
	ctx, span := ws.startSpan(ctx, "workspace.list")
	defer span.End()

	if err := ws.checkClosed(); err != nil {
		return nil, err
	}

	if err := ws.IsAllowed(ctx, OpList, pattern); err != nil {
		return nil, err
	}

	// Resolve pattern
	absPattern, err := ws.Resolve(ctx, pattern)
	if err != nil {
		return nil, err
	}

	// Use glob to match
	matches, err := filepath.Glob(absPattern)
	if err != nil {
		span.RecordError(err)
		return nil, sdkerr.Permanent("workspace.list_failed", fmt.Sprintf("glob failed: %v", err))
	}

	var entries []Entry
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			ws.logWarn(ctx, "workspace.list.stat_failed", observability.F("path", match))
			continue
		}

		// Convert to relative path
		relPath, err := filepath.Rel(ws.root, match)
		if err != nil {
			continue
		}

		entry := Entry{
			Path:    relPath,
			Name:    info.Name(),
			Type:    ws.entryType(info),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Permissions: EntryPermissions{
				Mode: uint32(info.Mode()),
			},
		}

		entries = append(entries, entry)
	}

	ws.incrementMetric("reads")
	span.SetAttribute("entry_count", len(entries))

	return entries, nil
}

// Read reads content from a file.
func (ws *FilesystemWorkspace) Read(ctx context.Context, path string) ([]byte, error) {
	ctx, span := ws.startSpan(ctx, "workspace.read")
	defer span.End()

	if err := ws.checkClosed(); err != nil {
		return nil, err
	}

	if err := ws.IsAllowed(ctx, OpRead, path); err != nil {
		return nil, err
	}

	absPath, err := ws.Resolve(ctx, path)
	if err != nil {
		return nil, err
	}

	// Check file size if limit is set
	if ws.permissions.MaxFileSize > 0 {
		info, err := os.Stat(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, sdkerr.Permanent("workspace.file_not_found", fmt.Sprintf("file not found: %s", path))
			}
			return nil, sdkerr.Wrap(err, "workspace.stat_failed")
		}

		if info.Size() > ws.permissions.MaxFileSize {
			return nil, sdkerr.Permanent("workspace.file_too_large",
				fmt.Sprintf("file size %d exceeds limit %d", info.Size(), ws.permissions.MaxFileSize))
		}
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			span.SetStatus(observability.StatusCodeError, "file not found")
			return nil, sdkerr.Permanent("workspace.file_not_found", fmt.Sprintf("file not found: %s", path))
		}
		span.RecordError(err)
		return nil, sdkerr.Wrap(err, "workspace.read_failed")
	}

	ws.incrementMetric("reads")
	ws.addBytes("read", int64(len(content)))
	span.SetAttribute("bytes_read", len(content))

	return content, nil
}

// ReadStream opens a streaming reader.
func (ws *FilesystemWorkspace) ReadStream(ctx context.Context, path string) (io.ReadCloser, error) {
	ctx, span := ws.startSpan(ctx, "workspace.read_stream")
	defer span.End()

	if err := ws.checkClosed(); err != nil {
		return nil, err
	}

	if err := ws.IsAllowed(ctx, OpRead, path); err != nil {
		return nil, err
	}

	absPath, err := ws.Resolve(ctx, path)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			span.SetStatus(observability.StatusCodeError, "file not found")
			return nil, sdkerr.Permanent("workspace.file_not_found", fmt.Sprintf("file not found: %s", path))
		}
		span.RecordError(err)
		return nil, sdkerr.Wrap(err, "workspace.open_failed")
	}

	ws.incrementMetric("reads")

	return file, nil
}

// Write writes content to a file.
func (ws *FilesystemWorkspace) Write(ctx context.Context, path string, content []byte) error {
	ctx, span := ws.startSpan(ctx, "workspace.write")
	defer span.End()

	if err := ws.checkClosed(); err != nil {
		return err
	}

	if ws.permissions.ReadOnly {
		return sdkerr.Permanent("workspace.read_only", "workspace is read-only")
	}

	if err := ws.IsAllowed(ctx, OpWrite, path); err != nil {
		return err
	}

	// Check file size limit
	if ws.permissions.MaxFileSize > 0 && int64(len(content)) > ws.permissions.MaxFileSize {
		return sdkerr.Permanent("workspace.file_too_large",
			fmt.Sprintf("content size %d exceeds limit %d", len(content), ws.permissions.MaxFileSize))
	}

	absPath, err := ws.Resolve(ctx, path)
	if err != nil {
		return err
	}

	// Create parent directories
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		span.RecordError(err)
		return sdkerr.Wrap(err, "workspace.mkdir_failed")
	}

	// Create a unique temp file in the destination directory so concurrent
	// writes to the same target do not collide on temp-file naming.
	tempFile, err := os.CreateTemp(dir, filepath.Base(absPath)+".tmp.*")
	if err != nil {
		span.RecordError(err)
		return sdkerr.Wrap(err, "workspace.write_failed")
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if _, err := tempFile.Write(content); err != nil {
		tempFile.Close()
		span.RecordError(err)
		return sdkerr.Wrap(err, "workspace.write_failed")
	}

	if err := tempFile.Chmod(0644); err != nil {
		tempFile.Close()
		span.RecordError(err)
		return sdkerr.Wrap(err, "workspace.write_failed")
	}

	if err := tempFile.Close(); err != nil {
		span.RecordError(err)
		return sdkerr.Wrap(err, "workspace.write_failed")
	}

	if err := os.Rename(tempPath, absPath); err != nil {
		span.RecordError(err)
		return sdkerr.Wrap(err, "workspace.rename_failed")
	}

	ws.incrementMetric("writes")
	ws.addBytes("written", int64(len(content)))
	span.SetAttribute("bytes_written", len(content))

	return nil
}

// WriteStream opens a streaming writer.
func (ws *FilesystemWorkspace) WriteStream(ctx context.Context, path string) (io.WriteCloser, error) {
	ctx, span := ws.startSpan(ctx, "workspace.write_stream")
	defer span.End()

	if err := ws.checkClosed(); err != nil {
		return nil, err
	}

	if ws.permissions.ReadOnly {
		return nil, sdkerr.Permanent("workspace.read_only", "workspace is read-only")
	}

	if err := ws.IsAllowed(ctx, OpWrite, path); err != nil {
		return nil, err
	}

	absPath, err := ws.Resolve(ctx, path)
	if err != nil {
		return nil, err
	}

	// Create parent directories
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		span.RecordError(err)
		return nil, sdkerr.Wrap(err, "workspace.mkdir_failed")
	}

	file, err := os.Create(absPath)
	if err != nil {
		span.RecordError(err)
		return nil, sdkerr.Wrap(err, "workspace.create_failed")
	}

	ws.incrementMetric("writes")

	return file, nil
}

// Delete removes a file or directory.
func (ws *FilesystemWorkspace) Delete(ctx context.Context, path string) error {
	ctx, span := ws.startSpan(ctx, "workspace.delete")
	defer span.End()

	if err := ws.checkClosed(); err != nil {
		return err
	}

	if ws.permissions.ReadOnly {
		return sdkerr.Permanent("workspace.read_only", "workspace is read-only")
	}

	if err := ws.IsAllowed(ctx, OpDelete, path); err != nil {
		return err
	}

	absPath, err := ws.Resolve(ctx, path)
	if err != nil {
		return err
	}

	// RemoveAll is idempotent (no error if doesn't exist)
	if err := os.RemoveAll(absPath); err != nil {
		span.RecordError(err)
		return sdkerr.Wrap(err, "workspace.delete_failed")
	}

	ws.incrementMetric("deletes")

	return nil
}

// Execute runs a command (not implemented for basic filesystem workspace).
func (ws *FilesystemWorkspace) Execute(ctx context.Context, command string, args []string, opts *ExecuteOptions) (*ExecuteResult, error) {
	return nil, sdkerr.Permanent("workspace.not_supported", "command execution not supported in basic filesystem workspace")
}

// Exists checks if a path exists.
func (ws *FilesystemWorkspace) Exists(ctx context.Context, path string) (bool, error) {
	ctx, span := ws.startSpan(ctx, "workspace.exists")
	defer span.End()

	if err := ws.checkClosed(); err != nil {
		return false, err
	}

	absPath, err := ws.Resolve(ctx, path)
	if err != nil {
		return false, err
	}

	_, err = os.Stat(absPath)
	if err == nil {
		return true, nil
	}

	if os.IsNotExist(err) {
		return false, nil
	}

	return false, sdkerr.Wrap(err, "workspace.stat_failed")
}

// Stat returns metadata about an entry.
func (ws *FilesystemWorkspace) Stat(ctx context.Context, path string) (*EntryInfo, error) {
	ctx, span := ws.startSpan(ctx, "workspace.stat")
	defer span.End()

	if err := ws.checkClosed(); err != nil {
		return nil, err
	}

	if err := ws.IsAllowed(ctx, OpStat, path); err != nil {
		return nil, err
	}

	absPath, err := ws.Resolve(ctx, path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			span.SetStatus(observability.StatusCodeError, "not found")
			return nil, sdkerr.Permanent("workspace.not_found", fmt.Sprintf("path not found: %s", path))
		}
		span.RecordError(err)
		return nil, sdkerr.Wrap(err, "workspace.stat_failed")
	}

	mode := info.Mode()
	entryInfo := &EntryInfo{
		Entry: Entry{
			Path:    path,
			Name:    info.Name(),
			Type:    ws.entryType(info),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Permissions: EntryPermissions{
				Read:    mode&0444 != 0,
				Write:   mode&0222 != 0,
				Execute: mode&0111 != 0,
				Mode:    uint32(mode),
			},
		},
		IsDir:        info.IsDir(),
		IsReadable:   mode&0444 != 0,
		IsWritable:   mode&0222 != 0,
		IsExecutable: mode&0111 != 0,
	}

	return entryInfo, nil
}

// IsAllowed checks if an operation is permitted.
func (ws *FilesystemWorkspace) IsAllowed(ctx context.Context, op Operation, target string) error {
	// Check if operation is allowed
	if len(ws.permissions.AllowedOperations) > 0 {
		allowed := slices.Contains(ws.permissions.AllowedOperations, op)
		if !allowed {
			ws.incrementMetric("permission_denials")
			return sdkerr.Permanent("workspace.operation_denied",
				fmt.Sprintf("operation %s not allowed", op))
		}
	}

	// Check denied paths first (takes precedence)
	for _, pattern := range ws.permissions.DeniedPaths {
		matched, err := filepath.Match(pattern, target)
		if err == nil && matched {
			ws.incrementMetric("permission_denials")
			return sdkerr.Permanent("workspace.path_denied",
				fmt.Sprintf("path %s matches denied pattern %s", target, pattern))
		}
	}

	// Check allowed paths
	if len(ws.permissions.AllowedPaths) > 0 {
		allowed := false
		for _, pattern := range ws.permissions.AllowedPaths {
			matched, err := filepath.Match(pattern, target)
			if err == nil && matched {
				allowed = true
				break
			}
		}
		if !allowed {
			ws.incrementMetric("permission_denials")
			return sdkerr.Permanent("workspace.path_denied",
				fmt.Sprintf("path %s not in allowed paths", target))
		}
	}

	// Check hidden files
	if !ws.permissions.AllowHiddenFiles && strings.HasPrefix(filepath.Base(target), ".") {
		ws.incrementMetric("permission_denials")
		return sdkerr.Permanent("workspace.hidden_file_denied",
			fmt.Sprintf("hidden file access denied: %s", target))
	}

	return nil
}

// SetPermissions updates workspace permissions.
func (ws *FilesystemWorkspace) SetPermissions(ctx context.Context, perms *Permissions) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	ws.permissions = perms
	return nil
}

// GetPermissions returns current permissions.
func (ws *FilesystemWorkspace) GetPermissions(ctx context.Context) (*Permissions, error) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	return ws.permissions, nil
}

// Watch monitors for changes (not implemented in basic version).
func (ws *FilesystemWorkspace) Watch(ctx context.Context, pattern string) (<-chan Event, error) {
	return nil, sdkerr.Permanent("workspace.not_supported", "file watching not implemented in basic filesystem workspace")
}

// Close releases resources.
func (ws *FilesystemWorkspace) Close() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	ws.closed = true

	if ws.logger != nil {
		ws.logger.Info(context.Background(), "workspace.closed",
			observability.F("type", "filesystem"),
			observability.F("root", ws.root))
	}

	return nil
}

// Capabilities returns what this workspace supports.
func (ws *FilesystemWorkspace) Capabilities() Capabilities {
	return Capabilities{
		SupportsRead:        true,
		SupportsWrite:       true,
		SupportsDelete:      true,
		SupportsExecute:     false,
		SupportsStreaming:   true,
		SupportsWatch:       false,
		SupportsSymlinks:    ws.permissions.AllowSymlinks,
		SupportsPermissions: true,
		MaxFileSize:         ws.permissions.MaxFileSize,
		MaxPathLength:       4096,
	}
}

// Metrics returns workspace usage statistics.
func (ws *FilesystemWorkspace) Metrics(ctx context.Context) (*WorkspaceMetrics, error) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	// Create a copy
	metrics := ws.metrics
	return &metrics, nil
}

// Helper methods

func (ws *FilesystemWorkspace) checkClosed() error {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	if ws.closed {
		return sdkerr.Permanent("workspace.closed", "workspace is closed")
	}
	return nil
}

func (ws *FilesystemWorkspace) entryType(info os.FileInfo) EntryType {
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return EntryTypeSymlink
	}
	if info.IsDir() {
		return EntryTypeDirectory
	}
	return EntryTypeFile
}

func (ws *FilesystemWorkspace) startSpan(ctx context.Context, name string) (context.Context, observability.Span) {
	if ws.tracer != nil {
		return ws.tracer.StartSpan(ctx, name)
	}
	return ctx, &noopSpan{}
}

func (ws *FilesystemWorkspace) logWarn(ctx context.Context, event string, fields ...observability.Field) {
	if ws.logger != nil {
		ws.logger.Warn(ctx, event, fields...)
	}
}

func (ws *FilesystemWorkspace) incrementMetric(name string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	switch name {
	case "reads":
		ws.metrics.TotalReads++
	case "writes":
		ws.metrics.TotalWrites++
	case "deletes":
		ws.metrics.TotalDeletes++
	case "executions":
		ws.metrics.TotalExecutions++
	case "permission_denials":
		ws.metrics.PermissionDenials++
	}
}

func (ws *FilesystemWorkspace) addBytes(op string, bytes int64) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	switch op {
	case "read":
		ws.metrics.BytesRead += bytes
	case "written":
		ws.metrics.BytesWritten += bytes
	}
}

// noopSpan is a no-op span for when tracer is nil
type noopSpan struct{}

func (s *noopSpan) End()                                                {}
func (s *noopSpan) SetAttribute(key string, value any)                  {}
func (s *noopSpan) SetAttributes(attrs map[string]any)                  {}
func (s *noopSpan) SetStatus(code observability.StatusCode, msg string) {}
func (s *noopSpan) RecordError(err error)                               {}
func (s *noopSpan) SpanID() string                                      { return "" }
func (s *noopSpan) TraceID() string                                     { return "" }
func (s *noopSpan) Context() context.Context                            { return context.Background() }
