package tools

import (
	"context"
	"maps"
	"sync"
)

// DefaultPermissionPolicies returns the default policy for all known permissions.
// Default-deny is applied to dangerous permissions; everything else defaults to allow.
// Note: hook_manage defaults to allow so hooks work out of the box.
func DefaultPermissionPolicies() map[Permission]PermissionPolicy {
	policies := map[Permission]PermissionPolicy{
		PermissionBashExecute:     PolicyDeny,
		PermissionFileWrite:       PolicyDeny,
		PermissionFileDelete:      PolicyDeny,
		PermissionNetworkAccess:   PolicyDeny,
		PermissionDatabaseWrite:   PolicyDeny,
		PermissionSecretAccess:    PolicyDeny,
		PermissionContainerAccess: PolicyDeny,
		// PermissionHookManage defaults to allow (not in this list)
	}

	for _, perm := range AllPermissions() {
		if _, exists := policies[perm]; !exists {
			policies[perm] = PolicyAllow
		}
	}

	return policies
}

// AllPermissions returns a stable list of known permissions.
func AllPermissions() []Permission {
	return []Permission{
		PermissionFileRead,
		PermissionFileWrite,
		PermissionFileDelete,
		PermissionBashExecute,
		PermissionNetworkAccess,
		PermissionDatabaseRead,
		PermissionDatabaseWrite,
		PermissionContainerAccess,
		PermissionSecretAccess,
		PermissionHookManage,
	}
}

// SimplePermissionChecker is an in-memory permission checker with configurable defaults.
type SimplePermissionChecker struct {
	mu       sync.RWMutex
	policies map[Permission]PermissionPolicy
	grants   map[Permission]map[ToolScope]map[string]bool
}

// NewSimplePermissionChecker creates a new checker with default policies.
func NewSimplePermissionChecker(defaultPolicies map[Permission]PermissionPolicy) *SimplePermissionChecker {
	policies := DefaultPermissionPolicies()
	if defaultPolicies != nil {
		policies = make(map[Permission]PermissionPolicy, len(defaultPolicies))
		maps.Copy(policies, defaultPolicies)
		for _, perm := range AllPermissions() {
			if _, exists := policies[perm]; !exists {
				policies[perm] = PolicyAllow
			}
		}
	}

	return &SimplePermissionChecker{
		policies: policies,
		grants:   make(map[Permission]map[ToolScope]map[string]bool),
	}
}

// SetPolicy updates the default policy for a permission.
func (c *SimplePermissionChecker) SetPolicy(permission Permission, policy PermissionPolicy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.policies[permission] = policy
}

// GetPolicy returns the default policy for a permission.
func (c *SimplePermissionChecker) Policy(permission Permission) PermissionPolicy {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if policy, ok := c.policies[permission]; ok {
		return policy
	}
	return PolicyAllow
}

// Policies returns a copy of all default policies.
func (c *SimplePermissionChecker) Policies() map[Permission]PermissionPolicy {
	c.mu.RLock()
	defer c.mu.RUnlock()
	policies := make(map[Permission]PermissionPolicy, len(c.policies))
	maps.Copy(policies, c.policies)
	return policies
}

// Check validates if the given permissions are granted.
func (c *SimplePermissionChecker) Check(ctx context.Context, required []Permission) bool {
	return c.CheckWithContext(ctx, required, nil)
}

// CheckWithContext validates permissions with additional context.
func (c *SimplePermissionChecker) CheckWithContext(ctx context.Context, required []Permission, ctxData map[string]any) bool {
	scope, scopeID := ScopeGlobal, ""
	if ctxData != nil {
		if v, ok := ctxData["scope"].(ToolScope); ok {
			scope = v
		} else if v, ok := ctxData["scope"].(string); ok && v != "" {
			scope = ToolScope(v)
		}
		if v, ok := ctxData["scope_id"].(string); ok {
			scopeID = v
		}
	}

	for _, perm := range required {
		if c.IsGranted(perm, scope, scopeID) {
			continue
		}

		switch c.Policy(perm) {
		case PolicyAllow:
			continue
		case PolicySandbox:
			continue
		case PolicyAsk:
			if c.RequestApproval(ctx, []Permission{perm}, "runtime approval required") {
				continue
			}
			return false
		default:
			return false
		}
	}

	return true
}

// RequestApproval requests runtime approval from the user.
// Default implementation denies (non-interactive environments should configure grants).
func (c *SimplePermissionChecker) RequestApproval(ctx context.Context, required []Permission, reason string) bool {
	return false
}

// Grant adds a permission grant.
func (c *SimplePermissionChecker) Grant(permission Permission, scope ToolScope, scopeID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if scope == "" {
		scope = ScopeGlobal
	}
	if _, ok := c.grants[permission]; !ok {
		c.grants[permission] = make(map[ToolScope]map[string]bool)
	}
	if _, ok := c.grants[permission][scope]; !ok {
		c.grants[permission][scope] = make(map[string]bool)
	}
	c.grants[permission][scope][scopeID] = true
	return nil
}

// Revoke removes a permission grant.
func (c *SimplePermissionChecker) Revoke(permission Permission, scope ToolScope, scopeID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if scope == "" {
		scope = ScopeGlobal
	}
	if _, ok := c.grants[permission]; !ok {
		return nil
	}
	if _, ok := c.grants[permission][scope]; !ok {
		return nil
	}
	delete(c.grants[permission][scope], scopeID)
	if len(c.grants[permission][scope]) == 0 {
		delete(c.grants[permission], scope)
	}
	return nil
}

// IsGranted checks if a specific permission is granted in the given scope.
func (c *SimplePermissionChecker) IsGranted(permission Permission, scope ToolScope, scopeID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	scopeGrants, ok := c.grants[permission]
	if !ok {
		return false
	}
	if scope == "" {
		scope = ScopeGlobal
	}
	if grants, ok := scopeGrants[scope]; ok {
		if grants[scopeID] {
			return true
		}
	}
	if grants, ok := scopeGrants[ScopeGlobal]; ok {
		if grants[""] {
			return true
		}
	}
	return false
}
