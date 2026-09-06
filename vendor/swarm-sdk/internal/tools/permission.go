package tools

import "context"

// Permission represents a capability required to execute a tool
type Permission string

const (
	// PermissionFileRead allows reading files
	PermissionFileRead Permission = "file_read"

	// PermissionFileWrite allows writing files
	PermissionFileWrite Permission = "file_write"

	// PermissionFileDelete allows deleting files
	PermissionFileDelete Permission = "file_delete"

	// PermissionBashExecute allows executing bash commands
	PermissionBashExecute Permission = "bash_execute"

	// PermissionNetworkAccess allows making network requests
	PermissionNetworkAccess Permission = "network_access"

	// PermissionDatabaseRead allows reading from databases
	PermissionDatabaseRead Permission = "database_read"

	// PermissionDatabaseWrite allows writing to databases
	PermissionDatabaseWrite Permission = "database_write"

	// PermissionContainerAccess allows interacting with containers
	PermissionContainerAccess Permission = "container_access"

	// PermissionSecretAccess allows accessing secrets
	PermissionSecretAccess Permission = "secret_access"

	// PermissionHookManage allows creating or modifying hooks
	PermissionHookManage Permission = "hook_manage"
)

// PermissionChecker validates if an operation is allowed.
// This is Ring 0 - interface only, implementation in Ring 1.
type PermissionChecker interface {
	// Check validates if the given permissions are granted.
	// Returns true if all required permissions are granted.
	Check(ctx context.Context, required []Permission) bool

	// CheckWithContext validates permissions with additional context.
	// The context map contains information like agent_id, mode_id, resource path, etc.
	CheckWithContext(ctx context.Context, required []Permission, ctxData map[string]any) bool

	// RequestApproval requests runtime approval from the user.
	// Returns true if the user approves, false if denied.
	// This is used for interactive permission requests.
	RequestApproval(ctx context.Context, required []Permission, reason string) bool

	// Grant adds a permission grant.
	// Scope and scopeID define where the permission applies (global, project, mode, etc.).
	Grant(permission Permission, scope ToolScope, scopeID string) error

	// Revoke removes a permission grant.
	Revoke(permission Permission, scope ToolScope, scopeID string) error

	// IsGranted checks if a specific permission is granted in the given scope.
	IsGranted(permission Permission, scope ToolScope, scopeID string) bool
}

// PermissionContextProvider exposes permission context for tool execution.
// Registries that enforce permissions should implement this to support preflight checks.
type PermissionContextProvider interface {
	// GetPermissionChecker returns the configured permission checker.
	PermissionChecker() PermissionChecker

	// GetRegistration returns the tool registration metadata.
	Registration(name string) (*ToolRegistration, bool)
}

// PermissionPolicy defines how permission checks are enforced.
type PermissionPolicy string

const (
	// PolicyAllow grants the permission automatically.
	PolicyAllow PermissionPolicy = "allow"

	// PolicyDeny denies the permission automatically.
	PolicyDeny PermissionPolicy = "deny"

	// PolicyAsk asks the user for approval.
	PolicyAsk PermissionPolicy = "ask"

	// PolicySandbox allows with restrictions (e.g., limited file paths).
	PolicySandbox PermissionPolicy = "sandbox"

	// PolicyInteractive is a deprecated alias for PolicyAsk.
	PolicyInteractive PermissionPolicy = PolicyAsk
)

// PermissionGrant represents a permission that has been granted.
type PermissionGrant struct {
	// Permission is the permission that was granted.
	Permission Permission

	// Policy defines how the permission is enforced.
	Policy PermissionPolicy

	// Scope defines where this grant applies.
	Scope ToolScope

	// ScopeID identifies the scope (project ID, mode ID, etc.).
	ScopeID string

	// Constraints contains additional restrictions.
	// For example, file_write might be constrained to specific paths.
	Constraints map[string]any

	// Expiry indicates when this grant expires (optional).
	Expiry *int64
}

// PermissionDenial represents a denied permission request.
type PermissionDenial struct {
	// Permission is the permission that was denied.
	Permission Permission

	// Reason explains why the permission was denied.
	Reason string

	// Scope indicates where the denial occurred.
	Scope ToolScope

	// ScopeID identifies the scope.
	ScopeID string

	// Timestamp when the denial occurred (Unix timestamp).
	Timestamp int64
}

// PermissionRequest represents a request for permission approval.
type PermissionRequest struct {
	// Permissions are the permissions being requested.
	Permissions []Permission

	// Tool is the tool requesting permission.
	Tool string

	// Agent is the agent requesting permission.
	Agent string

	// Reason explains why the permission is needed.
	Reason string

	// Context contains additional information about the request.
	Context map[string]any
}
