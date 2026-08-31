package tools

import (
	"strings"
)

// PermissionEvaluationRequest contains data used for rule evaluation.
type PermissionEvaluationRequest struct {
	Permissions []Permission
	Tool        string
	Operations  []string
	Paths       []string
	Commands    []string
	URLs        []string
	SizeBytes   int64
	HasSize     bool
	Dangerous   bool
	AgentID     string
	Mode        string
	Scope       ToolScope
	ScopeID     string
}

// PermissionDecision represents the decision for a permission request.
type PermissionDecision struct {
	Policy        PermissionPolicy
	NeedsApproval bool
	Reason        string
	RuleID        string
	Source        string
}

// PermissionEngine evaluates permission requests using config rules.
type PermissionEngine struct {
	config PermissionConfig
}

// NewPermissionEngine creates a new permission engine.
func NewPermissionEngine(config PermissionConfig) *PermissionEngine {
	var normalized PermissionConfig = normalizePermissionConfig(config)
	return &PermissionEngine{config: normalized}
}

// Evaluate applies overrides, rules, and defaults to decide a policy.
func (e *PermissionEngine) Evaluate(request PermissionEvaluationRequest) PermissionDecision {
	var normalized PermissionEvaluationRequest = normalizeRequest(request)

	// Tool override
	var toolOverride OverridePolicy
	var ok bool
	toolOverride, ok = e.config.Overrides.Tools[normalized.Tool]
	if ok {
		return decisionFromOverride(toolOverride, "tool override", "override")
	}

	// Permission overrides
	var overridePolicy PermissionPolicy
	var hasOverride bool = false
	var permIndex int
	for permIndex = 0; permIndex < len(normalized.Permissions); permIndex++ {
		var perm Permission = normalized.Permissions[permIndex]
		var override OverridePolicy
		override, ok = e.config.Overrides.Permissions[perm]
		if !ok {
			continue
		}
		var policy PermissionPolicy = policyFromOverride(override)
		overridePolicy = mergePolicy(overridePolicy, policy, hasOverride)
		hasOverride = true
	}
	if hasOverride {
		return decisionFromPolicy(overridePolicy, "permission override", "override", "")
	}

	// Rules (first match wins, by priority desc then order)
	var matchedRule *PermissionRule = nil
	var sortedRules []PermissionRule = sortRulesByPriority(e.config.Rules)
	var ruleIndex int
	for ruleIndex = range sortedRules {
		if sortedRules[ruleIndex].When.Matches(normalized) {
			matchedRule = &sortedRules[ruleIndex]
			break
		}
	}
	if matchedRule != nil {
		return decisionFromPolicy(matchedRule.Then.Policy, matchedRule.Then.Reason, "rule", matchedRule.ID)
	}

	// Defaults
	var defaultPolicy PermissionPolicy = policyFromDefaults(normalized.Permissions, e.config.Defaults.Policies)
	defaultPolicy = applyLevel(defaultPolicy, normalized.Dangerous, e.config.Level)
	return decisionFromPolicy(defaultPolicy, "default policy", "default", "")
}

func normalizeRequest(request PermissionEvaluationRequest) PermissionEvaluationRequest {
	var normalized PermissionEvaluationRequest = request
	var tool string = strings.TrimSpace(normalized.Tool)
	if tool != "" {
		normalized.Tool = strings.ToLower(tool)
	}
	normalized.Operations = normalizeOperations(normalized.Operations)
	return normalized
}

func decisionFromOverride(override OverridePolicy, reason string, source string) PermissionDecision {
	var policy PermissionPolicy = policyFromOverride(override)
	return decisionFromPolicy(policy, reason, source, "")
}

func policyFromOverride(override OverridePolicy) PermissionPolicy {
	switch override {
	case OverrideAlwaysAllow:
		return PolicyAllow
	case OverrideAlwaysAsk:
		return PolicyAsk
	case OverrideAlwaysDeny:
		return PolicyDeny
	default:
		return PolicyDeny
	}
}

func decisionFromPolicy(policy PermissionPolicy, reason string, source string, ruleID string) PermissionDecision {
	var needsApproval bool = policy == PolicyAsk
	return PermissionDecision{
		Policy:        policy,
		NeedsApproval: needsApproval,
		Reason:        reason,
		RuleID:        ruleID,
		Source:        source,
	}
}

func mergePolicy(current PermissionPolicy, next PermissionPolicy, hasCurrent bool) PermissionPolicy {
	if !hasCurrent {
		return next
	}
	if policyRank(next) > policyRank(current) {
		return next
	}
	return current
}

func policyRank(policy PermissionPolicy) int {
	switch policy {
	case PolicyDeny:
		return 4
	case PolicyAsk:
		return 3
	case PolicySandbox:
		return 2
	case PolicyAllow:
		return 1
	default:
		return 0
	}
}

func policyFromDefaults(permissions []Permission, defaults map[Permission]PermissionPolicy) PermissionPolicy {
	if len(permissions) == 0 {
		return PolicyAllow
	}
	var combined PermissionPolicy
	var hasCombined bool = false
	var index int
	for index = range permissions {
		var perm Permission = permissions[index]
		var policy PermissionPolicy = PolicyAllow
		var ok bool
		policy, ok = defaults[perm]
		if !ok {
			policy = PolicyAllow
		}
		combined = mergePolicy(combined, policy, hasCombined)
		hasCombined = true
	}
	if !hasCombined {
		return PolicyAllow
	}
	return combined
}

func applyLevel(policy PermissionPolicy, dangerous bool, level PermissionLevel) PermissionPolicy {
	switch level {
	case LevelAlwaysAsk:
		// Ask before everything — both Allow and Deny are upgraded to Ask.
		// Explicit overrides (OverrideAlwaysDeny) bypass this via the override path
		// before applyLevel is reached, so hard blocks are still respected.
		if policy == PolicyAllow || policy == PolicyDeny {
			return PolicyAsk
		}
		return policy
	case LevelBalanced:
		// Allow stands; Deny on dangerous actions becomes Ask; Deny on safe actions
		// also becomes Ask (the default policies set Deny to mean "ask by default").
		if policy == PolicyDeny {
			return PolicyAsk
		}
		if policy == PolicyAllow && dangerous {
			return PolicyAsk
		}
		return policy
	case LevelPermissive:
		// Allow non-dangerous asks through; keep Ask on dangerous actions.
		if policy == PolicyAsk && !dangerous {
			return PolicyAllow
		}
		// Deny on non-dangerous actions becomes Allow.
		if policy == PolicyDeny && !dangerous {
			return PolicyAllow
		}
		return policy
	case LevelYOLO:
		return PolicyAllow
	default:
		return policy
	}
}

func sortRulesByPriority(rules []PermissionRule) []PermissionRule {
	var sorted []PermissionRule = make([]PermissionRule, len(rules))
	copy(sorted, rules)
	var swapped bool = true
	var length int = len(sorted)
	for swapped {
		swapped = false
		var index int
		for index = 0; index < length-1; index++ {
			var left PermissionRule = sorted[index]
			var right PermissionRule = sorted[index+1]
			if left.Priority < right.Priority {
				sorted[index] = right
				sorted[index+1] = left
				swapped = true
			}
		}
		length--
		if length <= 1 {
			break
		}
	}
	return sorted
}

// BuildPermissionEvaluationRequest constructs a PermissionEvaluationRequest from context data.
func BuildPermissionEvaluationRequest(required []Permission, ctxData map[string]any) PermissionEvaluationRequest {
	var request PermissionEvaluationRequest
	request.Permissions = required
	request.Operations = operationsFromPermissions(required)

	if ctxData == nil {
		return request
	}

	var toolValue any
	var ok bool
	toolValue, ok = ctxData["tool"]
	if ok {
		var tool string
		tool, ok = toolValue.(string)
		if ok {
			request.Tool = strings.ToLower(strings.TrimSpace(tool))
		}
	}
	if request.Tool == "" {
		toolValue, ok = ctxData["tool_name"]
		if ok {
			var tool string
			tool, ok = toolValue.(string)
			if ok {
				request.Tool = strings.ToLower(strings.TrimSpace(tool))
			}
		}
	}

	var scopeValue any
	scopeValue, ok = ctxData["scope"]
	if ok {
		request.Scope = parseToolScope(scopeValue)
	}

	var scopeIDValue any
	scopeIDValue, ok = ctxData["scope_id"]
	if ok {
		var scopeID string
		scopeID, ok = scopeIDValue.(string)
		if ok {
			request.ScopeID = scopeID
		}
	}

	var agentIDValue any
	agentIDValue, ok = ctxData["agent_id"]
	if ok {
		var agentID string
		agentID, ok = agentIDValue.(string)
		if ok {
			request.AgentID = agentID
		}
	}

	var modeValue any
	modeValue, ok = ctxData["mode"]
	if ok {
		var mode string
		mode, ok = modeValue.(string)
		if ok {
			request.Mode = mode
		}
	}

	var paramsValue any
	paramsValue, ok = ctxData["params"]
	if ok {
		var params map[string]any
		params, ok = paramsValue.(map[string]any)
		if ok {
			request = applyParamsToRequest(request, params)
		}
	}

	request = applyContextToRequest(request, ctxData)

	return request
}

func applyContextToRequest(request PermissionEvaluationRequest, ctxData map[string]any) PermissionEvaluationRequest {
	if ctxData == nil {
		return request
	}

	updated := request

	operation, ok := getStringContext(ctxData, "operation")
	if ok {
		updated.Operations = append(updated.Operations, operation)
	}
	operations, ok := getStringSliceContext(ctxData, "operations")
	if ok {
		updated.Operations = append(updated.Operations, operations...)
	}

	path, ok := getStringContext(ctxData, "path")
	if ok {
		updated.Paths = append(updated.Paths, path)
	}
	paths, ok := getStringSliceContext(ctxData, "paths")
	if ok {
		updated.Paths = append(updated.Paths, paths...)
	}

	command, ok := getStringContext(ctxData, "command")
	if ok {
		updated.Commands = append(updated.Commands, command)
	}
	commands, ok := getStringSliceContext(ctxData, "commands")
	if ok {
		updated.Commands = append(updated.Commands, commands...)
	}

	url, ok := getStringContext(ctxData, "url")
	if ok {
		updated.URLs = append(updated.URLs, url)
	}
	urls, ok := getStringSliceContext(ctxData, "urls")
	if ok {
		updated.URLs = append(updated.URLs, urls...)
	}

	if size, ok := getInt64Context(ctxData, "file_size", "size_bytes", "sizeBytes"); ok {
		if !updated.HasSize || size > updated.SizeBytes {
			updated.SizeBytes = size
			updated.HasSize = true
		}
	}

	updated.Operations = normalizeOperations(updated.Operations)

	return updated
}

func applyParamsToRequest(request PermissionEvaluationRequest, params map[string]any) PermissionEvaluationRequest {
	var updated PermissionEvaluationRequest = request

	var path string
	var ok bool
	path, ok = getStringParam(params, "path")
	if ok {
		updated.Paths = append(updated.Paths, path)
	}
	path, ok = getStringParam(params, "file_path")
	if ok {
		updated.Paths = append(updated.Paths, path)
	}
	var pathList []string
	pathList, ok = getStringSliceParam(params, "paths")
	if ok {
		updated.Paths = append(updated.Paths, pathList...)
	}
	var patch string
	patch, ok = getStringParam(params, "patch")
	if ok {
		var patchPaths []string = extractPathsFromPatch(patch)
		if len(patchPaths) > 0 {
			updated.Paths = append(updated.Paths, patchPaths...)
		}
	}

	var command string
	command, ok = getStringParam(params, "command")
	if ok {
		updated.Commands = append(updated.Commands, command)
	}
	var commandList []string
	commandList, ok = getStringSliceParam(params, "commands")
	if ok {
		updated.Commands = append(updated.Commands, commandList...)
	}

	var url string
	url, ok = getStringParam(params, "url")
	if ok {
		updated.URLs = append(updated.URLs, url)
	}
	var urlList []string
	urlList, ok = getStringSliceParam(params, "urls")
	if ok {
		updated.URLs = append(updated.URLs, urlList...)
	}

	var operation string
	operation, ok = getStringParam(params, "operation")
	if ok {
		updated.Operations = append(updated.Operations, operation)
	}
	updated.Operations = normalizeOperations(updated.Operations)

	// Vault-specific: extract credential name for approval display
	var credential string
	credential, ok = getStringParam(params, "credential")
	if ok && credential != "" {
		// Store credential in Commands for vault_exec to show in approval modal
		if updated.Tool == "vault_exec" || updated.Tool == "vault_list" {
			updated.Commands = append([]string{"credential: " + credential}, updated.Commands...)
		}
	}

	var size int64
	var hasSize bool
	size, hasSize = extractSizeBytes(params)
	if hasSize {
		updated.SizeBytes = size
		updated.HasSize = true
	}

	var tool string = updated.Tool
	if tool != "" {
		updated.Dangerous = IsDangerousAction(tool, params)
	}

	return updated
}

func operationsFromPermissions(permissions []Permission) []string {
	var operations []string = make([]string, 0, len(permissions))
	var index int
	for index = range permissions {
		var operation string = operationForPermission(permissions[index])
		if operation == "" {
			continue
		}
		operations = append(operations, operation)
	}
	return normalizeOperations(operations)
}

func operationForPermission(permission Permission) string {
	switch permission {
	case PermissionFileRead:
		return "read"
	case PermissionFileWrite:
		return "write"
	case PermissionFileDelete:
		return "delete"
	case PermissionBashExecute:
		return "execute"
	case PermissionNetworkAccess:
		return "network"
	case PermissionDatabaseRead:
		return "read"
	case PermissionDatabaseWrite:
		return "write"
	case PermissionContainerAccess:
		return "container"
	case PermissionSecretAccess:
		return "secret"
	case PermissionHookManage:
		return "hook"
	default:
		return ""
	}
}

func getStringParam(params map[string]any, key string) (string, bool) {
	var value any
	var ok bool
	value, ok = params[key]
	if !ok || value == nil {
		return "", false
	}
	var parsed string
	parsed, ok = value.(string)
	if !ok {
		return "", false
	}
	return parsed, true
}

func getStringContext(ctxData map[string]any, key string) (string, bool) {
	if ctxData == nil {
		return "", false
	}
	value, ok := ctxData[key]
	if !ok || value == nil {
		return "", false
	}
	parsed, ok := value.(string)
	if !ok {
		return "", false
	}
	return parsed, true
}

func getStringSliceContext(ctxData map[string]any, key string) ([]string, bool) {
	if ctxData == nil {
		return nil, false
	}
	value, ok := ctxData[key]
	if !ok || value == nil {
		return nil, false
	}
	if direct, ok := value.([]string); ok {
		return direct, true
	}
	if generic, ok := value.([]any); ok {
		result := make([]string, 0, len(generic))
		for _, item := range generic {
			if parsed, ok := item.(string); ok {
				result = append(result, parsed)
			}
		}
		if len(result) > 0 {
			return result, true
		}
	}
	return nil, false
}

func getInt64Context(ctxData map[string]any, keys ...string) (int64, bool) {
	if ctxData == nil {
		return 0, false
	}
	for _, key := range keys {
		value, ok := ctxData[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case int64:
			return typed, true
		case int:
			return int64(typed), true
		case float64:
			return int64(typed), true
		case float32:
			return int64(typed), true
		case uint64:
			return int64(typed), true
		case uint:
			return int64(typed), true
		}
	}
	return 0, false
}

func getStringSliceParam(params map[string]any, key string) ([]string, bool) {
	var value any
	var ok bool
	value, ok = params[key]
	if !ok || value == nil {
		return nil, false
	}
	var direct []string
	direct, ok = value.([]string)
	if ok {
		return direct, true
	}
	var generic []any
	generic, ok = value.([]any)
	if ok {
		var result []string = make([]string, 0, len(generic))
		var index int
		for index = 0; index < len(generic); index++ {
			var item any = generic[index]
			var parsed string
			parsed, ok = item.(string)
			if ok {
				result = append(result, parsed)
			}
		}
		if len(result) > 0 {
			return result, true
		}
	}
	return nil, false
}

func extractSizeBytes(params map[string]any) (int64, bool) {
	var maxSize int64 = 0
	var found bool = false
	var size int64
	var ok bool

	size, ok = sizeFromValue(params["content"])
	if ok {
		maxSize = maxSizeInt64(maxSize, size)
		found = true
	}
	size, ok = sizeFromValue(params["patch"])
	if ok {
		maxSize = maxSizeInt64(maxSize, size)
		found = true
	}
	size, ok = sizeFromValue(params["diff"])
	if ok {
		maxSize = maxSizeInt64(maxSize, size)
		found = true
	}

	return maxSize, found
}

func sizeFromValue(value any) (int64, bool) {
	if value == nil {
		return 0, false
	}
	var str string
	var ok bool
	str, ok = value.(string)
	if ok {
		return int64(len(str)), true
	}
	var bytes []byte
	bytes, ok = value.([]byte)
	if ok {
		return int64(len(bytes)), true
	}
	return 0, false
}

func maxSizeInt64(left int64, right int64) int64 {
	if right > left {
		return right
	}
	return left
}

func extractPathsFromPatch(patch string) []string {
	if patch == "" {
		return nil
	}
	var lines []string = strings.Split(patch, "\n")
	var paths []string = make([]string, 0, len(lines))
	var seen map[string]bool = make(map[string]bool, len(lines))
	var index int
	for index = range lines {
		var line string = strings.TrimSpace(lines[index])
		var path string = extractPatchPath(line)
		if path == "" {
			continue
		}
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths
}

func extractPatchPath(line string) string {
	var prefix string
	switch {
	case strings.HasPrefix(line, "*** Add File: "):
		prefix = "*** Add File: "
	case strings.HasPrefix(line, "*** Delete File: "):
		prefix = "*** Delete File: "
	case strings.HasPrefix(line, "*** Update File: "):
		prefix = "*** Update File: "
	case strings.HasPrefix(line, "*** Move to: "):
		prefix = "*** Move to: "
	default:
		return ""
	}
	var path string = strings.TrimSpace(strings.TrimPrefix(line, prefix))
	return path
}
