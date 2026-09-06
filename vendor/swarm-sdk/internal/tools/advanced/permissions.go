package advanced

import (
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ---------------------------------------------------------------------------
// Advanced Permission Model — three-state allow / deny / ask
// ---------------------------------------------------------------------------

// PermissionBehavior represents the three possible permission decisions.
type PermissionBehavior string

const (
	// PermissionAllow grants the request automatically.
	PermissionAllow PermissionBehavior = "allow"

	// PermissionDeny denies the request automatically.
	PermissionDeny PermissionBehavior = "deny"

	// PermissionAsk requires human/operator confirmation.
	PermissionAsk PermissionBehavior = "ask"
)

// PermissionDecision carries the full result of an advanced permission check
// including the behaviour, reasoning, optional input transformation, and
// suggestions for the model when a request is denied.
type PermissionDecision struct {
	// Behavior is the permission outcome: allow, deny, or ask.
	Behavior PermissionBehavior `json:"behavior"`

	// Message is a human-readable explanation of the decision.
	Message string `json:"message,omitempty"`

	// UpdatedInput is set when the permission layer wants to transform the
	// tool input (e.g., redacting sensitive parameters). If nil, the
	// original input is used.
	UpdatedInput map[string]any `json:"updated_input,omitempty"`

	// DecisionReason provides structured metadata about why the decision
	// was made (rule name, policy origin, etc.).
	DecisionReason *DecisionReason `json:"decision_reason,omitempty"`

	// Suggestions are hints for the model about alternative actions it
	// can take when a request is denied. For example:
	//   "Request elevated permissions", "Use read-only alternative".
	Suggestions []string `json:"suggestions,omitempty"`
}

// DecisionReason provides structured context for a PermissionDecision.
type DecisionReason struct {
	// Type classifies the reason (e.g. "rule", "policy", "user_override").
	Type string `json:"type"`

	// RuleName identifies the specific rule that matched (if any).
	RuleName string `json:"rule_name,omitempty"`

	// Details carries arbitrary metadata about the decision.
	Details map[string]any `json:"details,omitempty"`
}

// ---------------------------------------------------------------------------
// AdvancedPermissionChecker
// ---------------------------------------------------------------------------

// PermissionRule defines a single access control rule.
type PermissionRule struct {
	// Name is a unique identifier for this rule.
	Name string

	// ToolPattern is a glob pattern matching tool names (e.g. "file_*").
	// "*" matches everything.
	ToolPattern string

	// Permissions are the SDK permissions this rule applies to.
	// Empty means the rule applies regardless of permission type.
	Permissions []tools.Permission

	// Behavior is the decision when this rule matches.
	Behavior PermissionBehavior

	// Message is an optional explanation.
	Message string

	// Suggestions are hints provided when Behavior is deny or ask.
	Suggestions []string

	// Priority determines evaluation order. Higher priority rules are
	// checked first. Default is 0.
	Priority int
}

// AdvancedPermissionChecker evaluates access control using a rule-based
// system that supports the three-state allow/deny/ask model. It does NOT
// replace the existing tools.PermissionChecker — it layers on top,
// providing richer decision metadata.
type AdvancedPermissionChecker struct {
	rules []PermissionRule
}

// NewAdvancedPermissionChecker creates a checker with no rules. Without
// rules, all requests default to PermissionAllow.
func NewAdvancedPermissionChecker() *AdvancedPermissionChecker {
	return &AdvancedPermissionChecker{
		rules: make([]PermissionRule, 0),
	}
}

// AddRule appends a permission rule. Rules are evaluated in priority order
// (highest first); the first matching rule wins.
func (apc *AdvancedPermissionChecker) AddRule(rule PermissionRule) {
	apc.rules = append(apc.rules, rule)
	// Keep sorted by priority descending
	for i := len(apc.rules) - 1; i > 0; i-- {
		if apc.rules[i].Priority > apc.rules[i-1].Priority {
			apc.rules[i], apc.rules[i-1] = apc.rules[i-1], apc.rules[i]
		}
	}
}

// RemoveRule removes a rule by name.
func (apc *AdvancedPermissionChecker) RemoveRule(name string) {
	for i, r := range apc.rules {
		if r.Name == name {
			apc.rules = append(apc.rules[:i], apc.rules[i+1:]...)
			return
		}
	}
}

// CheckPermission evaluates the rules for a given tool name and required
// permissions. Returns the decision from the first matching rule, or a
// default allow decision if no rules match.
func (apc *AdvancedPermissionChecker) CheckPermission(
	toolName string,
	required []tools.Permission,
	input map[string]any,
) PermissionDecision {
	for _, rule := range apc.rules {
		if !matchesToolPattern(toolName, rule.ToolPattern) {
			continue
		}
		if !matchesPermissions(required, rule.Permissions) {
			continue
		}

		decision := PermissionDecision{
			Behavior:    rule.Behavior,
			Message:     rule.Message,
			Suggestions: rule.Suggestions,
			DecisionReason: &DecisionReason{
				Type:     "rule",
				RuleName: rule.Name,
			},
		}

		// Generate default message if none set
		if decision.Message == "" {
			switch rule.Behavior {
			case PermissionAllow:
				decision.Message = fmt.Sprintf("Access to %s allowed by rule %s", toolName, rule.Name)
			case PermissionDeny:
				decision.Message = fmt.Sprintf("Access to %s denied by rule %s", toolName, rule.Name)
			case PermissionAsk:
				decision.Message = fmt.Sprintf("Permission required for %s (rule: %s)", toolName, rule.Name)
			}
		}

		return decision
	}

	// No rule matched — default allow
	return PermissionDecision{
		Behavior: PermissionAllow,
		Message:  fmt.Sprintf("Access to %s allowed (no matching rules)", toolName),
		DecisionReason: &DecisionReason{
			Type: "default",
		},
	}
}

// Rules returns a copy of all rules.
func (apc *AdvancedPermissionChecker) Rules() []PermissionRule {
	out := make([]PermissionRule, len(apc.rules))
	copy(out, apc.rules)
	return out
}

// ---------------------------------------------------------------------------
// Pattern matching helpers
// ---------------------------------------------------------------------------

// matchesToolPattern checks if a tool name matches a glob pattern.
// Supports: "*" (matches all), "prefix*", "*suffix", exact match.
func matchesToolPattern(toolName, pattern string) bool {
	if pattern == "*" || pattern == "" {
		return true
	}
	if pattern == toolName {
		return true
	}
	// Prefix glob: "file_*"
	if len(pattern) > 1 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(toolName) >= len(prefix) && toolName[:len(prefix)] == prefix
	}
	// Suffix glob: "*_read"
	if len(pattern) > 1 && pattern[0] == '*' {
		suffix := pattern[1:]
		return len(toolName) >= len(suffix) && toolName[len(toolName)-len(suffix):] == suffix
	}
	return false
}

// matchesPermissions checks if the required permissions intersect with the
// rule's permission set. If the rule's Permissions slice is empty, it
// matches all permission sets.
func matchesPermissions(required []tools.Permission, rulePerms []tools.Permission) bool {
	if len(rulePerms) == 0 {
		return true // empty rule perms → matches everything
	}
	ruleSet := make(map[tools.Permission]bool, len(rulePerms))
	for _, p := range rulePerms {
		ruleSet[p] = true
	}
	for _, p := range required {
		if ruleSet[p] {
			return true
		}
	}
	return false
}
