package tools

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"
)

// InteractivePermissionChecker implements PermissionChecker with interactive approval support.
type InteractivePermissionChecker struct {
	mu              sync.RWMutex
	broker          ApprovalBroker
	config          PermissionConfig
	projectConfig   PermissionConfig
	projectCallback func(PermissionConfig)
	sessionConfig   PermissionConfig
	modeConfigs     map[string]PermissionConfig
	agentConfigs    map[string]PermissionConfig
	grants          map[Permission]map[ToolScope]map[string]bool
	// workspaceRoot is the project workspace path. When set and the permission
	// level is YOLO, writes/deletes outside this directory are still presented
	// to the user interactively — YOLO never auto-approves out-of-workspace edits.
	workspaceRoot string
}

// NewInteractivePermissionChecker creates a new interactive permission checker.
func NewInteractivePermissionChecker(config PermissionConfig, broker ApprovalBroker) *InteractivePermissionChecker {
	var normalized PermissionConfig = normalizePermissionConfig(config)
	checker := &InteractivePermissionChecker{
		broker:       broker,
		config:       normalized,
		modeConfigs:  make(map[string]PermissionConfig),
		agentConfigs: make(map[string]PermissionConfig),
		grants:       make(map[Permission]map[ToolScope]map[string]bool),
	}
	if broker != nil {
		broker.SetConfig(normalized)
	}
	return checker
}

// Check validates if the given permissions are granted.
func (c *InteractivePermissionChecker) Check(ctx context.Context, required []Permission) bool {
	return c.CheckWithContext(ctx, required, nil)
}

// CheckWithContext validates permissions with additional context.
func (c *InteractivePermissionChecker) CheckWithContext(ctx context.Context, required []Permission, ctxData map[string]any) bool {
	scope, scopeID := scopeFromContext(ctxData)

	var pending []Permission = make([]Permission, 0, len(required))
	for _, perm := range required {
		if c.IsGranted(perm, scope, scopeID) {
			continue
		}
		pending = append(pending, perm)
	}

	if len(pending) == 0 {
		return true
	}

	// Read-only operations never block. Reading a file or grepping the tree is
	// non-destructive and safe to auto-allow regardless of approval policy —
	// gating them behind prompts (or a mis-scoped policy) only stalls the agent
	// on inspection it must do constantly. The out-of-workspace guard applies to
	// writes/deletes, not reads, so there's nothing to protect here.
	if allReadOnly(pending) {
		return true
	}

	request := BuildPermissionEvaluationRequest(pending, ctxData)
	layers := c.layersForRequest(request)
	decision, effective := EvaluatePermissionWithLayers(request, layers)

	switch decision.Policy {
	case PolicyAllow:
		return true
	case PolicySandbox:
		return true
	case PolicyAsk:
		reason := decision.Reason
		if reason == "" {
			reason = "runtime approval required"
		}
		return c.requestApproval(ctx, pending, reason, request, ctxData, scope, scopeID, effective)
	case PolicyDeny:
		return false
	default:
		return false
	}
}

// RequestApproval requests runtime approval from the user.
func (c *InteractivePermissionChecker) RequestApproval(ctx context.Context, required []Permission, reason string) bool {
	request := BuildPermissionEvaluationRequest(required, nil)
	layers := c.layersForRequest(request)
	_, effective := EvaluatePermissionWithLayers(request, layers)
	return c.requestApproval(ctx, required, reason, request, nil, ScopeGlobal, "", effective)
}

// allReadOnly reports whether every permission in the set is a non-destructive
// read (file or database read). Used to fast-path inspection tools like read and
// grep so they never prompt or get blocked.
func allReadOnly(perms []Permission) bool {
	if len(perms) == 0 {
		return false
	}
	for _, p := range perms {
		if p != PermissionFileRead && p != PermissionDatabaseRead {
			return false
		}
	}
	return true
}

// Grant adds a permission grant for the current session.
func (c *InteractivePermissionChecker) Grant(permission Permission, scope ToolScope, scopeID string) error {
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
func (c *InteractivePermissionChecker) Revoke(permission Permission, scope ToolScope, scopeID string) error {
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
func (c *InteractivePermissionChecker) IsGranted(permission Permission, scope ToolScope, scopeID string) bool {
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

// CheckWithDangerousDetection checks permissions with dangerous action detection.
// Deprecated: use CheckWithContext with full permission context instead.
func (c *InteractivePermissionChecker) CheckWithDangerousDetection(ctx context.Context, tool string, params map[string]any) bool {
	ctxData := map[string]any{
		"tool":   tool,
		"params": params,
	}
	request := BuildPermissionEvaluationRequest(nil, ctxData)
	layers := c.layersForRequest(request)
	decision, effective := EvaluatePermissionWithLayers(request, layers)
	switch decision.Policy {
	case PolicyAllow:
		return true
	case PolicySandbox:
		return true
	case PolicyAsk:
		reason := decision.Reason
		if reason == "" {
			reason = "runtime approval required"
		}
		return c.requestApproval(ctx, nil, reason, request, ctxData, ScopeGlobal, "", effective)
	case PolicyDeny:
		return false
	default:
		return false
	}
}

// CheckToolOverride checks if a tool has an override policy.
// Returns true if the tool is allowed, false if it needs approval or is denied.
func (c *InteractivePermissionChecker) CheckToolOverride(tool string, permission Permission) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.config.Overrides.Tools == nil {
		return false
	}
	override, exists := c.config.Overrides.Tools[strings.ToLower(tool)]
	if !exists {
		return false
	}
	return override == OverrideAlwaysAllow
}

// GrantSessionPermission grants a permission for the current session.
func (c *InteractivePermissionChecker) GrantSessionPermission(perm Permission) {
	_ = c.Grant(perm, ScopeGlobal, "")
}

// RevokeSessionPermission revokes a session permission.
func (c *InteractivePermissionChecker) RevokeSessionPermission(perm Permission) {
	_ = c.Revoke(perm, ScopeGlobal, "")
}

// HasSessionGrant checks if a permission has a session grant.
func (c *InteractivePermissionChecker) HasSessionGrant(perm Permission) bool {
	return c.IsGranted(perm, ScopeGlobal, "")
}

// AddToolOverride adds a per-tool override.
func (c *InteractivePermissionChecker) AddToolOverride(tool string, policy OverridePolicy) {
	normalized := strings.ToLower(strings.TrimSpace(tool))
	if normalized == "" {
		return
	}

	c.mu.Lock()
	if c.config.Overrides.Tools == nil {
		c.config.Overrides.Tools = make(map[string]OverridePolicy)
	}
	c.config.Overrides.Tools[normalized] = policy
	updated := c.config
	c.mu.Unlock()

	if c.broker != nil {
		c.broker.SetConfig(updated)
	}
}

// RemoveToolOverride removes a per-tool override.
func (c *InteractivePermissionChecker) RemoveToolOverride(tool string) {
	normalized := strings.ToLower(strings.TrimSpace(tool))
	if normalized == "" {
		return
	}

	c.mu.Lock()
	if c.config.Overrides.Tools != nil {
		delete(c.config.Overrides.Tools, normalized)
	}
	updated := c.config
	c.mu.Unlock()

	if c.broker != nil {
		c.broker.SetConfig(updated)
	}
}

// GetToolOverrides returns a copy of all tool overrides.
func (c *InteractivePermissionChecker) ToolOverrides() map[string]OverridePolicy {
	c.mu.RLock()
	defer c.mu.RUnlock()

	copyMap := make(map[string]OverridePolicy, len(c.config.Overrides.Tools))
	maps.Copy(copyMap, c.config.Overrides.Tools)
	return copyMap
}

// SetConfig updates the permission configuration.
func (c *InteractivePermissionChecker) SetConfig(config PermissionConfig) {
	c.mu.Lock()
	c.config = normalizePermissionConfig(config)
	updated := c.config
	c.mu.Unlock()

	if c.broker != nil {
		c.broker.SetConfig(updated)
	}
}

// GetConfig returns the current configuration.
func (c *InteractivePermissionChecker) Config() PermissionConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.config
}

// SetWorkspaceRoot registers the project workspace path so the checker can
// enforce the out-of-workspace write guard in YOLO mode.
// Must be called after construction if a workspace root is known.
func (c *InteractivePermissionChecker) SetWorkspaceRoot(root string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.workspaceRoot = root
}

// SetProjectConfig sets the project-level permission overrides.
func (c *InteractivePermissionChecker) SetProjectConfig(config PermissionConfig) {
	c.mu.Lock()
	c.projectConfig = config
	c.mu.Unlock()
}

// SetProjectConfigCallback registers a callback for project config updates.
func (c *InteractivePermissionChecker) SetProjectConfigCallback(callback func(PermissionConfig)) {
	c.mu.Lock()
	c.projectCallback = callback
	c.mu.Unlock()
}

// SetSessionConfig sets the session-level permission overrides.
func (c *InteractivePermissionChecker) SetSessionConfig(config PermissionConfig) {
	c.mu.Lock()
	c.sessionConfig = config
	c.mu.Unlock()
}

// SetModeConfig sets the mode-level permission overrides for a mode.
func (c *InteractivePermissionChecker) SetModeConfig(mode string, config PermissionConfig) {
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
func (c *InteractivePermissionChecker) SetAgentConfig(agentID string, config PermissionConfig) {
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

// EffectiveConfig returns the merged permission config for the provided context.
func (c *InteractivePermissionChecker) EffectiveConfig(ctxData map[string]any) PermissionConfig {
	request := BuildPermissionEvaluationRequest(nil, ctxData)
	layers := c.layersForRequest(request)
	return MergePermissionConfigs(DefaultPermissionConfig(), layers...)
}

func (c *InteractivePermissionChecker) requestApproval(
	ctx context.Context,
	required []Permission,
	reason string,
	request PermissionEvaluationRequest,
	ctxData map[string]any,
	scope ToolScope,
	scopeID string,
	config PermissionConfig,
) bool {
	if c.broker == nil {
		return false
	}

	timeout := config.TimeoutSeconds
	timeoutBehavior := config.TimeoutBehavior

	if timeout <= 0 {
		timeout = 300
	}

	permStr := joinPermissions(required)
	target := approvalTarget(request)
	preview := buildApprovalPreview(request, ctxData)
	approvalCtx := approvalContext(ctxData)
	conversationID := contextValue(ctxData, "conversation_id", "conversationId")
	batchID := contextValue(ctxData, "batch_id", "batchId")

	req := PermissionApprovalRequest{
		RequestID:      generateRequestID(),
		ConversationID: conversationID,
		Tool:           request.Tool,
		Permission:     permStr,
		Target:         target,
		Reason:         reason,
		Preview:        preview,
		Context:        approvalCtx,
		Timeout:        timeout,
		BatchID:        batchID,
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	resp, err := c.broker.Request(ctx, req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded && timeoutBehavior == "continue" {
			return true
		}
		return false
	}

	allowed := c.applyDecision(resp.Decision, required, request.Tool, scope, scopeID)
	if !allowed && resp.UserContext != "" {
		// Surface the broker's actionable denial reason to the caller (the tool
		// registry) so the model-visible tool-result can carry a recovery hint
		// instead of a generic "permission denied" message.
		SetDenialReason(ctx, resp.UserContext)
	}
	return allowed
}

func (c *InteractivePermissionChecker) layersForRequest(request PermissionEvaluationRequest) []PermissionConfig {
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

func (c *InteractivePermissionChecker) applyDecision(decision Decision, required []Permission, tool string, scope ToolScope, scopeID string) bool {
	switch decision {
	case DecisionApproveOnce:
		return true
	case DecisionApproveSession:
		for _, perm := range required {
			_ = c.Grant(perm, scope, scopeID)
		}
		return true
	case DecisionApproveAlways:
		normalizedTool := strings.ToLower(strings.TrimSpace(tool))
		if normalizedTool != "" {
			c.AddToolOverride(normalizedTool, OverrideAlwaysAllow)
			return true
		}
		for _, perm := range required {
			_ = c.Grant(perm, scope, scopeID)
		}
		return true
	case DecisionSaveProject:
		normalizedTool := strings.ToLower(strings.TrimSpace(tool))
		var updated PermissionConfig
		var callback func(PermissionConfig)
		var updatedConfig bool = false

		c.mu.Lock()
		projectConfig := c.projectConfig
		if projectConfig.Version == 0 {
			projectConfig.Version = 1
		}
		if normalizedTool != "" {
			if projectConfig.Overrides.Tools == nil {
				projectConfig.Overrides.Tools = make(map[string]OverridePolicy)
			}
			projectConfig.Overrides.Tools[normalizedTool] = OverrideAlwaysAllow
			updatedConfig = true
		} else if len(required) > 0 {
			if projectConfig.Overrides.Permissions == nil {
				projectConfig.Overrides.Permissions = make(map[Permission]OverridePolicy)
			}
			for _, perm := range required {
				if perm == "" {
					continue
				}
				projectConfig.Overrides.Permissions[perm] = OverrideAlwaysAllow
				updatedConfig = true
			}
		}
		if updatedConfig {
			c.projectConfig = projectConfig
			updated = projectConfig
			callback = c.projectCallback
		}
		c.mu.Unlock()

		if updatedConfig {
			if callback != nil {
				callback(updated)
			}
			return true
		}
		for _, perm := range required {
			_ = c.Grant(perm, scope, scopeID)
		}
		return true
	default:
		return false
	}
}

func scopeFromContext(ctxData map[string]any) (ToolScope, string) {
	if ctxData == nil {
		return ScopeGlobal, ""
	}
	var scope ToolScope = ScopeGlobal
	if value, ok := ctxData["scope"]; ok {
		scope = parseToolScope(value)
	}
	var scopeID string
	if value, ok := ctxData["scope_id"]; ok {
		if parsed, ok := value.(string); ok {
			scopeID = parsed
		}
	}
	return scope, scopeID
}

func joinPermissions(required []Permission) string {
	if len(required) == 0 {
		return ""
	}
	parts := make([]string, 0, len(required))
	for _, perm := range required {
		if perm == "" {
			continue
		}
		parts = append(parts, string(perm))
	}
	return strings.Join(parts, ",")
}

func approvalTarget(request PermissionEvaluationRequest) string {
	if len(request.Paths) > 0 {
		return request.Paths[0]
	}
	if len(request.Commands) > 0 {
		return request.Commands[0]
	}
	if len(request.URLs) > 0 {
		return request.URLs[0]
	}
	return ""
}

func buildApprovalPreview(request PermissionEvaluationRequest, ctxData map[string]any) *ApprovalPreview {
	var params map[string]any
	if ctxData != nil {
		if raw, ok := ctxData["params"]; ok {
			if parsed, ok := raw.(map[string]any); ok {
				params = parsed
			}
		}
	}

	// Vault-specific preview for vault_exec
	if params != nil {
		if cred, ok := getVaultCredentialParam(params); ok && cred != "" {
			var preview strings.Builder
			preview.WriteString("Credential: ")
			preview.WriteString(strconv.Quote(cred))
			if cmd, ok := getStringParam(params, "command"); ok && cmd != "" {
				preview.WriteString("\nCommand: ")
				preview.WriteString(strconv.Quote(cmd))
			}
			if args, ok := getStringSliceParam(params, "args"); ok && len(args) > 0 {
				preview.WriteString("\nArguments: ")
				for i, arg := range args {
					if i > 0 {
						preview.WriteByte(' ')
					}
					preview.WriteString(strconv.Quote(arg))
				}
			}
			if host, ok := getStringParam(params, "host"); ok && host != "" {
				preview.WriteString("\nHost: ")
				preview.WriteString(strconv.Quote(host))
			}
			workingDir, _ := getStringParam(params, "workingDir")
			if workingDir == "" {
				workingDir, _ = getStringParam(params, "working_dir")
			}
			if workingDir != "" {
				preview.WriteString("\nWorking directory: ")
				preview.WriteString(strconv.Quote(workingDir))
			}
			if reason, ok := getStringParam(params, "reason"); ok && reason != "" {
				preview.WriteString("\nReason: ")
				preview.WriteString(strconv.Quote(reason))
			}
			return &ApprovalPreview{Type: "text", Content: preview.String()}
		}
	}

	if params != nil {
		if patch, ok := getStringParam(params, "patch"); ok && patch != "" {
			return &ApprovalPreview{Type: "diff", Content: patch}
		}
		if diff, ok := getStringParam(params, "diff"); ok && diff != "" {
			return &ApprovalPreview{Type: "diff", Content: diff}
		}
	}

	if len(request.Commands) > 0 {
		return &ApprovalPreview{Type: "command", Content: strings.Join(request.Commands, "\n")}
	}

	if len(request.URLs) > 0 {
		return &ApprovalPreview{Type: "text", Content: strings.Join(request.URLs, "\n")}
	}

	if params != nil {
		if content, ok := getStringParam(params, "content"); ok && content != "" {
			return &ApprovalPreview{Type: "text", Content: content}
		}
	}

	return nil
}

func approvalContext(ctxData map[string]any) *ApprovalContext {
	if ctxData == nil {
		return nil
	}
	agentID := contextValue(ctxData, "agent_id", "agentId")
	taskDescription := contextValue(ctxData, "task_description", "taskDescription")
	if agentID == "" && taskDescription == "" {
		return nil
	}
	return &ApprovalContext{
		AgentID:         agentID,
		TaskDescription: taskDescription,
	}
}

func contextValue(ctxData map[string]any, keys ...string) string {
	if ctxData == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := ctxData[key]; ok {
			if parsed, ok := value.(string); ok {
				return parsed
			}
		}
	}
	return ""
}

// Simple ID generator using timestamp and counter
var requestCounter int64
var counterMu sync.Mutex

func generateRequestID() string {
	counterMu.Lock()
	requestCounter++
	count := requestCounter
	counterMu.Unlock()
	return fmt.Sprintf("%s-%d", time.Now().Format("20060102150405"), count)
}
