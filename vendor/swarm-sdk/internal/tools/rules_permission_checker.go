package tools

import (
	"context"
	"strings"
	"sync"
)

// RulesPermissionChecker enforces permissions using a PermissionEngine.
type RulesPermissionChecker struct {
	mu            sync.RWMutex
	config        PermissionConfig
	projectConfig PermissionConfig
	sessionConfig PermissionConfig
	modeConfigs   map[string]PermissionConfig
	agentConfigs  map[string]PermissionConfig
	grants        map[Permission]map[ToolScope]map[string]bool
}

// NewRulesPermissionChecker creates a new checker backed by a PermissionEngine.
func NewRulesPermissionChecker(config PermissionConfig) *RulesPermissionChecker {
	var normalized PermissionConfig = normalizePermissionConfig(config)
	return &RulesPermissionChecker{
		config:       normalized,
		modeConfigs:  make(map[string]PermissionConfig),
		agentConfigs: make(map[string]PermissionConfig),
		grants:       make(map[Permission]map[ToolScope]map[string]bool),
	}
}

// SetConfig updates the permission configuration.
func (c *RulesPermissionChecker) SetConfig(config PermissionConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var normalized PermissionConfig = normalizePermissionConfig(config)
	c.config = normalized
}

// GetConfig returns the current permission configuration.
func (c *RulesPermissionChecker) Config() PermissionConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.config
}

// Check validates if the given permissions are granted.
func (c *RulesPermissionChecker) Check(ctx context.Context, required []Permission) bool {
	return c.CheckWithContext(ctx, required, nil)
}

// CheckWithContext validates permissions with additional context.
func (c *RulesPermissionChecker) CheckWithContext(ctx context.Context, required []Permission, ctxData map[string]any) bool {
	var scope ToolScope = ScopeGlobal
	var scopeID string = ""
	if ctxData != nil {
		var scopeValue any
		var ok bool
		scopeValue, ok = ctxData["scope"]
		if ok {
			scope = parseToolScope(scopeValue)
		}
		var scopeIDValue any
		scopeIDValue, ok = ctxData["scope_id"]
		if ok {
			var parsed string
			parsed, ok = scopeIDValue.(string)
			if ok {
				scopeID = parsed
			}
		}
	}

	var pending []Permission = make([]Permission, 0, len(required))
	var index int
	for index = range required {
		var perm Permission = required[index]
		if c.IsGranted(perm, scope, scopeID) {
			continue
		}
		pending = append(pending, perm)
	}

	if len(pending) == 0 {
		return true
	}

	var request PermissionEvaluationRequest = BuildPermissionEvaluationRequest(pending, ctxData)
	layers := c.layersForRequest(request)
	var decision PermissionDecision
	decision, _ = EvaluatePermissionWithLayers(request, layers)

	switch decision.Policy {
	case PolicyAllow:
		return true
	case PolicySandbox:
		return true
	case PolicyAsk:
		return c.RequestApproval(ctx, pending, decision.Reason)
	case PolicyDeny:
		return false
	default:
		return false
	}
}

// RequestApproval requests runtime approval from the user.
// Default implementation denies (non-interactive environments should configure grants).
func (c *RulesPermissionChecker) RequestApproval(ctx context.Context, required []Permission, reason string) bool {
	return false
}

// SetProjectConfig sets the project-level permission overrides.
func (c *RulesPermissionChecker) SetProjectConfig(config PermissionConfig) {
	c.mu.Lock()
	c.projectConfig = config
	c.mu.Unlock()
}

// SetSessionConfig sets the session-level permission overrides.
func (c *RulesPermissionChecker) SetSessionConfig(config PermissionConfig) {
	c.mu.Lock()
	c.sessionConfig = config
	c.mu.Unlock()
}

// SetModeConfig sets the mode-level permission overrides for a mode.
func (c *RulesPermissionChecker) SetModeConfig(mode string, config PermissionConfig) {
	trimmed := strings.TrimSpace(mode)
	if trimmed == "" {
		return
	}
	c.mu.Lock()
	if c.modeConfigs == nil {
		c.modeConfigs = make(map[string]PermissionConfig)
	}
	c.modeConfigs[trimmed] = config
	c.mu.Unlock()
}

// SetAgentConfig sets the agent-level permission overrides for an agent.
func (c *RulesPermissionChecker) SetAgentConfig(agentID string, config PermissionConfig) {
	trimmed := strings.TrimSpace(agentID)
	if trimmed == "" {
		return
	}
	c.mu.Lock()
	if c.agentConfigs == nil {
		c.agentConfigs = make(map[string]PermissionConfig)
	}
	c.agentConfigs[trimmed] = config
	c.mu.Unlock()
}

// Grant adds a permission grant.
func (c *RulesPermissionChecker) Grant(permission Permission, scope ToolScope, scopeID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var normalizedScope ToolScope = scope
	if normalizedScope == "" {
		normalizedScope = ScopeGlobal
	}
	if _, ok := c.grants[permission]; !ok {
		c.grants[permission] = make(map[ToolScope]map[string]bool)
	}
	if _, ok := c.grants[permission][normalizedScope]; !ok {
		c.grants[permission][normalizedScope] = make(map[string]bool)
	}
	c.grants[permission][normalizedScope][scopeID] = true
	return nil
}

// Revoke removes a permission grant.
func (c *RulesPermissionChecker) Revoke(permission Permission, scope ToolScope, scopeID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var normalizedScope ToolScope = scope
	if normalizedScope == "" {
		normalizedScope = ScopeGlobal
	}
	if _, ok := c.grants[permission]; !ok {
		return nil
	}
	if _, ok := c.grants[permission][normalizedScope]; !ok {
		return nil
	}
	delete(c.grants[permission][normalizedScope], scopeID)
	if len(c.grants[permission][normalizedScope]) == 0 {
		delete(c.grants[permission], normalizedScope)
	}
	return nil
}

// IsGranted checks if a specific permission is granted in the given scope.
func (c *RulesPermissionChecker) IsGranted(permission Permission, scope ToolScope, scopeID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var scopeGrants map[ToolScope]map[string]bool
	var ok bool
	scopeGrants, ok = c.grants[permission]
	if !ok {
		return false
	}
	var normalizedScope ToolScope = scope
	if normalizedScope == "" {
		normalizedScope = ScopeGlobal
	}
	var grants map[string]bool
	grants, ok = scopeGrants[normalizedScope]
	if ok {
		if grants[scopeID] {
			return true
		}
	}
	grants, ok = scopeGrants[ScopeGlobal]
	if ok {
		if grants[""] {
			return true
		}
	}
	return false
}

func (c *RulesPermissionChecker) layersForRequest(request PermissionEvaluationRequest) []PermissionConfig {
	c.mu.RLock()
	globalConfig := c.config
	projectConfig := c.projectConfig
	sessionConfig := c.sessionConfig
	var modeConfig PermissionConfig
	if request.Mode != "" && c.modeConfigs != nil {
		if cfg, ok := c.modeConfigs[request.Mode]; ok {
			modeConfig = cfg
		}
	}
	var agentConfig PermissionConfig
	if request.AgentID != "" && c.agentConfigs != nil {
		if cfg, ok := c.agentConfigs[request.AgentID]; ok {
			agentConfig = cfg
		}
	}
	c.mu.RUnlock()

	return []PermissionConfig{
		globalConfig,
		projectConfig,
		modeConfig,
		agentConfig,
		sessionConfig,
	}
}

func parseToolScope(value any) ToolScope {
	if value == nil {
		return ScopeGlobal
	}
	var scope ToolScope
	var ok bool
	scope, ok = value.(ToolScope)
	if ok {
		return scope
	}
	var scopeString string
	scopeString, ok = value.(string)
	if ok {
		var trimmed string = strings.TrimSpace(scopeString)
		if trimmed != "" {
			return ToolScope(trimmed)
		}
	}
	return ScopeGlobal
}

func normalizePermissionConfig(config PermissionConfig) PermissionConfig {
	var normalized PermissionConfig = config
	if normalized.Version == 0 {
		normalized.Version = 1
	}
	if normalized.Level == "" {
		normalized.Level = LevelBalanced
	}
	if normalized.TimeoutSeconds == 0 {
		normalized.TimeoutSeconds = 300
	}
	if normalized.TimeoutBehavior == "" {
		normalized.TimeoutBehavior = "stop"
	}
	if normalized.Defaults.Policies == nil {
		normalized.Defaults.Policies = DefaultPermissionPolicies()
	}
	if normalized.Overrides.Tools == nil {
		normalized.Overrides.Tools = make(map[string]OverridePolicy)
	}
	if normalized.Overrides.Permissions == nil {
		normalized.Overrides.Permissions = make(map[Permission]OverridePolicy)
	}
	if normalized.Rules == nil {
		normalized.Rules = make([]PermissionRule, 0)
	}
	return normalized
}
