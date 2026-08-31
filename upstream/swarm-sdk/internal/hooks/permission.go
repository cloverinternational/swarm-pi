package hooks

// HookPermissionPolicy defines whether a hook is allowed to execute.
type HookPermissionPolicy string

const (
	// HookPermissionAllow allows the hook to execute.
	HookPermissionAllow HookPermissionPolicy = "allow"

	// HookPermissionDeny prevents the hook from executing.
	HookPermissionDeny HookPermissionPolicy = "deny"
)

// PermissionedHook exposes a per-hook permission policy.
type PermissionedHook interface {
	HookPermissionPolicy() HookPermissionPolicy
}

// NormalizeHookPermissionPolicy coerces unknown values to allow.
func NormalizeHookPermissionPolicy(policy HookPermissionPolicy) HookPermissionPolicy {
	if policy == HookPermissionDeny {
		return HookPermissionDeny
	}
	return HookPermissionAllow
}
