// Package workspace defines the interface for isolated execution contexts.
// This is Ring 0 - pure interface definitions with no implementations.
//
// Workspaces provide sandboxed environments where agents can operate safely.
// They abstract over different backend types (filesystem, remote, container, etc.)
// while maintaining consistent permission controls and observability.
package workspace

import (
	"context"
	"io"
	"time"
)

// Workspace defines the interface for isolated execution contexts.
// All workspace operations are permission-checked and fully traced.
type Workspace interface {
	// Type returns the workspace type identifier.
	// Examples: "filesystem", "remote", "container", "git", "s3", "database", "api", "memory"
	Type() string

	// Root returns the root path/identifier for this workspace.
	// Format depends on type:
	// - filesystem: "/path/to/project"
	// - remote: "ssh://user@host/path"
	// - container: "docker://container-id/path"
	// - git: "git://repo-url#branch"
	// - s3: "s3://bucket/prefix"
	// - database: "postgres://host/database"
	// - api: "https://api.example.com"
	// - memory: "memory://workspace-id"
	Root() string

	// Resolve converts a relative path to an absolute path within this workspace.
	// Returns error if path escapes workspace boundaries.
	Resolve(ctx context.Context, path string) (string, error)

	// List returns entries matching the pattern.
	// Pattern syntax depends on workspace type (glob, SQL, API query, etc.)
	List(ctx context.Context, pattern string) ([]Entry, error)

	// Read reads content from the given path.
	// Returns error if path doesn't exist or is not readable.
	Read(ctx context.Context, path string) ([]byte, error)

	// ReadStream opens a streaming reader for large content.
	// Caller must close the reader when done.
	ReadStream(ctx context.Context, path string) (io.ReadCloser, error)

	// Write writes content to the given path.
	// Creates parent directories if needed (for filesystem workspaces).
	// Returns error if operation is not allowed.
	Write(ctx context.Context, path string, content []byte) error

	// WriteStream opens a streaming writer for large content.
	// Caller must close the writer when done.
	WriteStream(ctx context.Context, path string) (io.WriteCloser, error)

	// Delete removes the entry at the given path.
	// No error if path doesn't exist (idempotent).
	Delete(ctx context.Context, path string) error

	// Execute runs a command in this workspace.
	// Returns stdout, stderr, and exit code.
	// Only supported by workspaces that allow command execution (filesystem, remote, container).
	Execute(ctx context.Context, command string, args []string, opts *ExecuteOptions) (*ExecuteResult, error)

	// Exists checks if a path exists in this workspace.
	Exists(ctx context.Context, path string) (bool, error)

	// Stat returns metadata about an entry.
	Stat(ctx context.Context, path string) (*EntryInfo, error)

	// IsAllowed checks if an operation is permitted on the given target.
	// This is called before every operation to enforce permissions.
	IsAllowed(ctx context.Context, op Operation, target string) error

	// SetPermissions updates permissions for this workspace.
	// Permissions are enforced for all operations.
	SetPermissions(ctx context.Context, perms *Permissions) error

	// GetPermissions returns current permissions.
	GetPermissions(ctx context.Context) (*Permissions, error)

	// Watch monitors the workspace for changes.
	// Returns a channel of events. Close context to stop watching.
	// Not all workspace types support watching.
	Watch(ctx context.Context, pattern string) (<-chan Event, error)

	// Close releases resources associated with this workspace.
	Close() error

	// Capabilities returns what this workspace supports.
	Capabilities() Capabilities
}

// Entry represents a single entry in a workspace (file, directory, object, row, etc.)
type Entry struct {
	// Path is the relative path within the workspace.
	Path string

	// Name is the entry name (last component of path).
	Name string

	// Type is the entry type.
	Type EntryType

	// Size in bytes (0 for directories).
	Size int64

	// ModTime is when the entry was last modified.
	ModTime time.Time

	// Permissions are the entry's access permissions.
	Permissions EntryPermissions

	// Metadata contains workspace-specific additional information.
	Metadata map[string]any
}

// EntryType identifies the kind of entry.
type EntryType string

const (
	// EntryTypeFile is a regular file.
	EntryTypeFile EntryType = "file"

	// EntryTypeDirectory is a directory.
	EntryTypeDirectory EntryType = "directory"

	// EntryTypeSymlink is a symbolic link.
	EntryTypeSymlink EntryType = "symlink"

	// EntryTypeObject is a cloud storage object (S3, etc.)
	EntryTypeObject EntryType = "object"

	// EntryTypeTable is a database table.
	EntryTypeTable EntryType = "table"

	// EntryTypeEndpoint is an API endpoint.
	EntryTypeEndpoint EntryType = "endpoint"
)

// EntryInfo contains detailed metadata about an entry.
type EntryInfo struct {
	Entry

	// IsDir is true if this is a directory.
	IsDir bool

	// IsReadable is true if the entry can be read.
	IsReadable bool

	// IsWritable is true if the entry can be written.
	IsWritable bool

	// IsExecutable is true if the entry can be executed.
	IsExecutable bool

	// Owner is the entry owner (user/group/account).
	Owner string

	// ContentType is the MIME type (for files/objects).
	ContentType string

	// Checksum is a hash of the content (for verification).
	Checksum string

	// CreatedAt is when the entry was created.
	CreatedAt time.Time

	// AccessedAt is when the entry was last accessed.
	AccessedAt time.Time
}

// EntryPermissions represents access permissions for an entry.
type EntryPermissions struct {
	// Read permission.
	Read bool

	// Write permission.
	Write bool

	// Execute permission.
	Execute bool

	// Delete permission.
	Delete bool

	// Mode is the numeric permission mode (Unix-style, e.g., 0755).
	// Only applicable to filesystem workspaces.
	Mode uint32
}

// ExecuteOptions configures command execution.
type ExecuteOptions struct {
	// WorkingDir is the directory to run the command in.
	WorkingDir string

	// Env contains environment variables (key=value pairs).
	Env map[string]string

	// Stdin provides input to the command.
	Stdin io.Reader

	// Timeout limits execution time.
	Timeout time.Duration

	// User to run as (for remote/container workspaces).
	User string

	// Shell to use (if not specified, uses default).
	Shell string

	// CaptureOutput determines if stdout/stderr are captured.
	CaptureOutput bool
}

// ExecuteResult contains the result of command execution.
type ExecuteResult struct {
	// Stdout is the standard output.
	Stdout string

	// Stderr is the standard error output.
	Stderr string

	// ExitCode is the command exit code.
	ExitCode int

	// Duration is how long execution took.
	Duration time.Duration

	// Error is any execution error (separate from command errors).
	Error error
}

// Operation represents an operation type for permission checking.
type Operation string

const (
	// OpRead reads content.
	OpRead Operation = "read"

	// OpWrite writes content.
	OpWrite Operation = "write"

	// OpDelete deletes entries.
	OpDelete Operation = "delete"

	// OpExecute runs commands.
	OpExecute Operation = "execute"

	// OpList lists entries.
	OpList Operation = "list"

	// OpStat gets entry metadata.
	OpStat Operation = "stat"
)

// Permissions defines access control for a workspace.
type Permissions struct {
	// ReadOnly makes the workspace read-only.
	ReadOnly bool

	// AllowedOperations lists permitted operations.
	// Empty means all operations allowed (subject to ReadOnly).
	AllowedOperations []Operation

	// AllowedPaths restricts operations to specific paths (glob patterns).
	// Empty means all paths allowed.
	AllowedPaths []string

	// DeniedPaths explicitly denies access to paths (glob patterns).
	// Denials take precedence over allows.
	DeniedPaths []string

	// MaxFileSize limits individual file read/write size in bytes.
	// 0 means no limit.
	MaxFileSize int64

	// MaxTotalSize limits total workspace size in bytes.
	// 0 means no limit.
	MaxTotalSize int64

	// AllowSymlinks permits following symbolic links.
	AllowSymlinks bool

	// AllowHiddenFiles permits access to hidden files (starting with .).
	AllowHiddenFiles bool

	// ExecutionPolicy controls command execution.
	ExecutionPolicy ExecutionPolicy

	// RateLimits restricts operation rates.
	RateLimits *RateLimits
}

// ExecutionPolicy controls how commands can be executed.
type ExecutionPolicy string

const (
	// ExecutionDenied disallows all command execution.
	ExecutionDenied ExecutionPolicy = "denied"

	// ExecutionInteractive requires user approval for each command.
	ExecutionInteractive ExecutionPolicy = "interactive"

	// ExecutionSandboxed allows execution but with sandboxing.
	ExecutionSandboxed ExecutionPolicy = "sandboxed"

	// ExecutionUnrestricted allows free execution (use with caution).
	ExecutionUnrestricted ExecutionPolicy = "unrestricted"
)

// RateLimits restricts operation frequencies.
type RateLimits struct {
	// MaxReadsPerSecond limits read operations.
	MaxReadsPerSecond int

	// MaxWritesPerSecond limits write operations.
	MaxWritesPerSecond int

	// MaxExecutionsPerMinute limits command executions.
	MaxExecutionsPerMinute int
}

// Event represents a change in the workspace.
type Event struct {
	// Type is the event type.
	Type EventType

	// Path is the affected path.
	Path string

	// Timestamp is when the event occurred.
	Timestamp time.Time

	// Metadata contains event-specific information.
	Metadata map[string]any
}

// EventType identifies the kind of workspace event.
type EventType string

const (
	// EventCreate indicates an entry was created.
	EventCreate EventType = "create"

	// EventModify indicates an entry was modified.
	EventModify EventType = "modify"

	// EventDelete indicates an entry was deleted.
	EventDelete EventType = "delete"

	// EventMove indicates an entry was moved/renamed.
	EventMove EventType = "move"
)

// Capabilities describes what a workspace supports.
type Capabilities struct {
	// SupportsRead indicates read operations are supported.
	SupportsRead bool

	// SupportsWrite indicates write operations are supported.
	SupportsWrite bool

	// SupportsDelete indicates delete operations are supported.
	SupportsDelete bool

	// SupportsExecute indicates command execution is supported.
	SupportsExecute bool

	// SupportsStreaming indicates streaming read/write is supported.
	SupportsStreaming bool

	// SupportsWatch indicates change watching is supported.
	SupportsWatch bool

	// SupportsSymlinks indicates symbolic links are supported.
	SupportsSymlinks bool

	// SupportsPermissions indicates fine-grained permissions are supported.
	SupportsPermissions bool

	// MaxFileSize is the maximum supported file size (0 = unlimited).
	MaxFileSize int64

	// MaxPathLength is the maximum path length (0 = unlimited).
	MaxPathLength int
}

// WorkspaceConfig provides configuration for creating workspaces.
type WorkspaceConfig struct {
	// Type is the workspace type to create.
	Type string

	// Root is the root path/identifier.
	Root string

	// Permissions are initial permissions.
	Permissions *Permissions

	// Options contains workspace-specific configuration.
	// For filesystem: {"base_dir": "/path"}
	// For remote: {"host": "example.com", "user": "user", "key_path": "~/.ssh/id_rsa"}
	// For container: {"image": "node:20", "auto_cleanup": true}
	// For git: {"branch": "main", "depth": 1}
	// For s3: {"region": "us-east-1", "access_key": "...", "secret_key": "..."}
	// For database: {"connection_string": "postgres://..."}
	// For api: {"base_url": "https://...", "auth": {...}}
	Options map[string]any
}

// WorkspaceMetrics provides statistics about workspace usage.
type WorkspaceMetrics struct {
	// TotalReads is the total number of read operations.
	TotalReads int64

	// TotalWrites is the total number of write operations.
	TotalWrites int64

	// TotalDeletes is the total number of delete operations.
	TotalDeletes int64

	// TotalExecutions is the total number of command executions.
	TotalExecutions int64

	// BytesRead is the total bytes read.
	BytesRead int64

	// BytesWritten is the total bytes written.
	BytesWritten int64

	// CurrentSize is the current workspace size in bytes.
	CurrentSize int64

	// EntryCount is the number of entries in the workspace.
	EntryCount int64

	// PermissionDenials is the count of permission denied errors.
	PermissionDenials int64

	// AverageOperationDuration is the average operation time in milliseconds.
	AverageOperationDuration float64
}

// MetricsProvider is an optional interface for workspaces that provide metrics.
type MetricsProvider interface {
	Metrics(ctx context.Context) (*WorkspaceMetrics, error)
}

// Snapshotter is an optional interface for workspaces that support snapshots.
type Snapshotter interface {
	// Snapshot creates a point-in-time snapshot of the workspace.
	Snapshot(ctx context.Context, name string) error

	// Restore restores the workspace to a snapshot.
	Restore(ctx context.Context, name string) error

	// ListSnapshots returns available snapshots.
	ListSnapshots(ctx context.Context) ([]SnapshotInfo, error)

	// DeleteSnapshot removes a snapshot.
	DeleteSnapshot(ctx context.Context, name string) error
}

// SnapshotInfo describes a workspace snapshot.
type SnapshotInfo struct {
	// Name is the snapshot identifier.
	Name string

	// CreatedAt is when the snapshot was created.
	CreatedAt time.Time

	// Size is the snapshot size in bytes.
	Size int64

	// Description is an optional snapshot description.
	Description string
}

// Transactional is an optional interface for workspaces that support transactions.
// Useful for database and API workspaces.
type Transactional interface {
	// Begin starts a transaction.
	Begin(ctx context.Context) (Transaction, error)
}

// Transaction represents an atomic set of operations.
type Transaction interface {
	// Commit commits the transaction.
	Commit(ctx context.Context) error

	// Rollback rolls back the transaction.
	Rollback(ctx context.Context) error

	// Workspace returns a workspace scoped to this transaction.
	Workspace() Workspace
}
