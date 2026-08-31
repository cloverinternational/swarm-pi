package builtin

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// AutoModeConfig defines the configuration for auto mode behavior
type AutoModeConfig struct {
	// User has accepted auto mode opt-in dialog
	SkipAutoPermissionPrompt bool `json:"skipAutoPermissionPrompt"`

	// Whether plan mode uses auto mode semantics (default: true)
	UseAutoModeDuringPlan bool `json:"useAutoModeDuringPlan"`

	// Auto mode classifier customization
	AutoMode AutoModeRules `json:"autoMode"`

	// Disable auto mode entirely
	DisableAutoMode string `json:"disableAutoMode"`
}

// AutoModeRules defines the classification rules
type AutoModeRules struct {
	Allow       []string `json:"allow"`       // Rules for auto-approval
	SoftDeny    []string `json:"soft_deny"`   // Rules requiring review
	Deny        []string `json:"deny"`        // Hard deny (blocks immediately)
	Environment []string `json:"environment"` // Context for classifier
}

// DefaultAutoModeRules returns a Claude-Code-equivalent starting rule set:
//   - Allow: read-only / inspection tools (filesystem, notebook, todo, search)
//   - SoftDeny: edit / execute / network-write tools (prompt with risk banner)
//   - Deny: destructive Bash patterns (rm -rf /, git push --force, reset --hard, disk wipe)
//
// Users can override by passing a custom AutoModeConfig via WithAutoModeConfig.
// Tool names are matched case-insensitively; arg patterns use simple glob
// semantics (see matchArgPattern).
func DefaultAutoModeRules() AutoModeRules {
	return AutoModeRules{
		Allow: []string{
			// Filesystem inspection
			"Read", "Glob", "Grep", "LS", "NotebookRead",
			// Task inspection AND mutation. Task tools must be auto-allowed:
			// the task-enforcement and task-nudge hooks both demand task
			// creation as the precondition for ANY other tool, so requiring
			// approval here creates a hard contradiction (system asks for
			// tasks, then gates the tool that creates them). Survey 2026-04-27
			// confirmed this as the most-fired friction across all sessions.
			"TodoRead", "TodoList", "TodoWrite",
			"TaskManage", "task_manage",
			"TaskCreate", "TaskUpdate", "TaskGet", "TaskList", "TaskOutput", "TaskRead",
			"task_create", "task_update", "task_get", "task_list", "task_read",
			// Web search is read-only; WebFetch stays on SoftDeny since it
			// can exfiltrate context via the URL.
			"WebSearch",
			// Sub-agent dispatch: the parent session still gets the sub-agent's
			// own permission prompts, so dispatching itself is safe.
			"Task", "Agent",
			// Internal bookkeeping
			"ExitPlanMode", "EnterPlanMode", "ScheduleWakeup",
			// User-facing UI primitives — asking the user is never destructive.
			"AskUserQuestion", "ask_user_question",
		},
		SoftDeny: []string{
			// Edits and writes
			"Edit", "MultiEdit", "Write", "NotebookEdit",
			// Shell / exec
			"Bash",
			// Network fetch (URL arg can leak context, response can steer model)
			"WebFetch",
		},
		Deny: []string{
			// Recursive rm targeting root or home
			"Bash(rm -rf /)",
			"Bash(rm -rf /*)",
			"Bash(rm -rf ~)",
			"Bash(rm -rf ~/*)",
			"Bash(rm -rf $HOME*)",
			// Force pushes and history rewrites on protected branches
			"Bash(git push --force*)",
			"Bash(git push -f*)",
			"Bash(git push*--force-with-lease*)",
			"Bash(git reset --hard*)",
			// Disk-level destruction
			"Bash(dd if=*of=/dev/*)",
			"Bash(mkfs*)",
			"Bash(fdisk*)",
			"Bash(shutdown*)",
			"Bash(reboot*)",
			"Bash(:(){ :|:& };:*)", // classic fork-bomb
		},
	}
}

// ClassificationResult represents the outcome of safety classification
type ClassificationResult struct {
	Risk           string   `json:"risk"`           // "low", "medium", "high"
	Confidence     float64  `json:"confidence"`     // 0.0 - 1.0
	Reason         string   `json:"reason"`         // Explanation of risk assessment
	Recommendation string   `json:"recommendation"` // "approve", "prompt", "deny"
	Categories     []string `json:"categories,omitempty"`
}

// AutoModeClassifier defines the interface for safety classification
type AutoModeClassifier interface {
	Classify(ctx context.Context, toolName string, toolInput map[string]any, rules AutoModeRules) (ClassificationResult, error)
}

// DefaultAutoModeClassifier implements rule-based classification
type DefaultAutoModeClassifier struct {
	logger observability.Logger
}

// NewDefaultAutoModeClassifier creates a new rule-based classifier
func NewDefaultAutoModeClassifier(logger observability.Logger) *DefaultAutoModeClassifier {
	if logger == nil {
		logger = noop.NewLogger()
	}
	return &DefaultAutoModeClassifier{logger: logger}
}

// Classify evaluates tool safety using rules.
//
// Resolution order (first match wins):
//  1. Hard Deny — destructive patterns that should never run silently
//  2. Allow — read-only / safe tools that auto-approve
//  3. SoftDeny — write / exec tools that must prompt
//  4. Fallback — unknown tools prompt the user
func (c *DefaultAutoModeClassifier) Classify(ctx context.Context, toolName string, toolInput map[string]any, rules AutoModeRules) (ClassificationResult, error) {
	// Hard deny always wins so destructive patterns cannot be re-enabled by
	// a looser Allow entry above.
	if c.matchesDenyList(toolName, toolInput, rules.Deny) {
		c.logger.Info(ctx, "auto_mode.deny_list_match",
			observability.F("tool", toolName))
		return ClassificationResult{
			Risk:           "high",
			Confidence:     1.0,
			Reason:         fmt.Sprintf("Tool %q matches hard deny rule", toolName),
			Recommendation: "deny",
		}, nil
	}

	// Fast path: check allow list
	if c.matchesAllowList(toolName, toolInput, rules.Allow) {
		c.logger.Debug(ctx, "auto_mode.allow_list_match",
			observability.F("tool", toolName))
		return ClassificationResult{
			Risk:           "low",
			Confidence:     1.0,
			Reason:         fmt.Sprintf("Tool %q matches allow list", toolName),
			Recommendation: "approve",
		}, nil
	}

	// Check soft deny list
	if c.matchesSoftDenyList(toolName, toolInput, rules.SoftDeny) {
		c.logger.Debug(ctx, "auto_mode.soft_deny_match",
			observability.F("tool", toolName))
		return ClassificationResult{
			Risk:           "medium",
			Confidence:     0.9,
			Reason:         fmt.Sprintf("Tool %q matches soft deny list - requires user review", toolName),
			Recommendation: "prompt",
		}, nil
	}

	// Default: medium risk, prompt user
	c.logger.Debug(ctx, "auto_mode.no_rule_match",
		observability.F("tool", toolName))
	return ClassificationResult{
		Risk:           "medium",
		Confidence:     0.5,
		Reason:         fmt.Sprintf("Tool %q has no matching rules - requires user review", toolName),
		Recommendation: "prompt",
	}, nil
}

// matchesDenyList mirrors matchesAllowList / matchesSoftDenyList for the
// hard-deny set. Split out (rather than inlined) to keep the three lookups
// symmetric and to preserve the "first match wins" contract.
func (c *DefaultAutoModeClassifier) matchesDenyList(toolName string, toolInput map[string]any, patterns []string) bool {
	for _, pattern := range patterns {
		if c.matchPattern(toolName, toolInput, pattern) {
			return true
		}
	}
	return false
}

// matchesAllowList checks if tool matches any allow pattern
func (c *DefaultAutoModeClassifier) matchesAllowList(toolName string, toolInput map[string]any, patterns []string) bool {
	for _, pattern := range patterns {
		if c.matchPattern(toolName, toolInput, pattern) {
			return true
		}
	}
	return false
}

// matchesSoftDenyList checks if tool matches any soft deny pattern
func (c *DefaultAutoModeClassifier) matchesSoftDenyList(toolName string, toolInput map[string]any, patterns []string) bool {
	for _, pattern := range patterns {
		if c.matchPattern(toolName, toolInput, pattern) {
			return true
		}
	}
	return false
}

// matchPattern matches a tool action against a pattern
// Pattern syntax: ToolName(args) or ToolName(**/pattern)
func (c *DefaultAutoModeClassifier) matchPattern(toolName string, toolInput map[string]any, pattern string) bool {
	pattern = strings.TrimSpace(pattern)

	// Parse pattern: ToolName(args) or just ToolName
	var patternTool, patternArgs string
	if idx := strings.Index(pattern, "("); idx > 0 && strings.HasSuffix(pattern, ")") {
		patternTool = pattern[:idx]
		patternArgs = pattern[idx+1 : len(pattern)-1]
	} else {
		patternTool = pattern
	}

	// Check tool name match (case-insensitive)
	if !strings.EqualFold(patternTool, toolName) && !c.isWildcardMatch(patternTool, toolName) {
		return false
	}

	// If no args pattern, just tool name match is enough
	if patternArgs == "" {
		return true
	}

	// Check args pattern against relevant input fields
	// Common fields: command, path, file_path, url, etc.
	checkFields := []string{"command", "path", "file_path", "url", "pattern", "query"}
	for _, field := range checkFields {
		if val, ok := toolInput[field].(string); ok {
			if c.matchArgPattern(val, patternArgs) {
				return true
			}
		}
	}

	return false
}

// isWildcardMatch checks if pattern with wildcards matches value
func (c *DefaultAutoModeClassifier) isWildcardMatch(pattern, value string) bool {
	// Simple wildcard matching: * matches anything, ? matches single char
	// Convert to regex
	regexPattern := "^" + regexp.QuoteMeta(pattern)
	regexPattern = strings.ReplaceAll(regexPattern, "\\*", ".*")
	regexPattern = strings.ReplaceAll(regexPattern, "\\?", ".")
	regexPattern += "$"

	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return false
	}
	return re.MatchString(strings.ToLower(value))
}

// matchArgPattern matches an argument against a pattern
func (c *DefaultAutoModeClassifier) matchArgPattern(arg, pattern string) bool {
	pattern = strings.ToLower(pattern)
	arg = strings.ToLower(arg)

	// Handle glob patterns like **/*.ts
	if strings.Contains(pattern, "*") {
		return c.isWildcardMatch(pattern, arg)
	}

	// Handle command patterns like "npm run *"
	if before, ok := strings.CutSuffix(pattern, "*"); ok {
		prefix := before
		return strings.HasPrefix(arg, prefix)
	}

	// Exact match
	return arg == pattern
}

// AutoModeHook implements automatic safety classification for tool execution
type AutoModeHook struct {
	config     AutoModeConfig
	classifier AutoModeClassifier
	logger     observability.Logger
	priority   int
	enabled    bool
	state      *autoModeState
}

// autoModeState tracks runtime state
type autoModeState struct {
	needsExitAttachment bool
	lastActive          time.Time
}

// AutoModeOption configures the auto mode hook
type AutoModeOption func(*AutoModeHook)

// WithAutoModeConfig sets the auto mode configuration
func WithAutoModeConfig(config AutoModeConfig) AutoModeOption {
	return func(h *AutoModeHook) {
		h.config = config
	}
}

// WithAutoModeClassifier sets a custom classifier
func WithAutoModeClassifier(classifier AutoModeClassifier) AutoModeOption {
	return func(h *AutoModeHook) {
		h.classifier = classifier
	}
}

// WithAutoModePriority sets the hook priority
func WithAutoModePriority(priority int) AutoModeOption {
	return func(h *AutoModeHook) {
		h.priority = priority
	}
}

// WithAutoModeEnabled sets whether auto mode is enabled
func WithAutoModeEnabled(enabled bool) AutoModeOption {
	return func(h *AutoModeHook) {
		h.enabled = enabled
	}
}

// NewAutoModeHook creates a new auto mode hook
func NewAutoModeHook(logger observability.Logger, opts ...AutoModeOption) *AutoModeHook {
	if logger == nil {
		logger = noop.NewLogger()
	}

	h := &AutoModeHook{
		config: AutoModeConfig{
			SkipAutoPermissionPrompt: false,
			UseAutoModeDuringPlan:    true,
			// Ship with Claude-Code-equivalent defaults so the hook is
			// immediately useful. Override via WithAutoModeConfig.
			AutoMode: DefaultAutoModeRules(),
		},
		classifier: NewDefaultAutoModeClassifier(logger),
		logger:     logger,
		priority:   90, // High priority - run before most hooks but after security
		enabled:    true,
		state: &autoModeState{
			lastActive: time.Now(),
		},
	}

	// Apply options
	for _, opt := range opts {
		opt(h)
	}

	// Check environment variable for opt-in override
	if os.Getenv("SWARM_AUTO_MODE_OPT_IN") == "1" {
		h.config.SkipAutoPermissionPrompt = true
	}

	return h
}

// Name implements hooks.Hook
func (h *AutoModeHook) Name() string { return "auto-mode" }

// Priority implements hooks.Hook
func (h *AutoModeHook) Priority() int { return h.priority }

// Filter implements hooks.Hook
// Only processes tool.before_execute events when auto mode is enabled
func (h *AutoModeHook) Filter(event hooks.Event) bool {
	if !h.enabled || h.config.DisableAutoMode == "disable" {
		return false
	}
	if !h.hasAutoModeOptIn() {
		return false
	}

	return event.Type == hooks.EventToolBeforeExecute
}

// hasAutoModeOptIn checks if user has opted into auto mode
func (h *AutoModeHook) hasAutoModeOptIn() bool {
	// Check environment variable
	if os.Getenv("SWARM_AUTO_MODE_OPT_IN") == "1" {
		return true
	}

	// Check config
	return h.config.SkipAutoPermissionPrompt
}

// OnEvent implements hooks.Hook
// Evaluates tool safety and decides whether to allow, block, or prompt
func (h *AutoModeHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	toolName, _ := event.Data["tool_name"].(string)
	toolInput, _ := event.Data["tool_input"].(map[string]any)

	h.logger.Debug(ctx, "auto_mode.evaluating",
		observability.F("tool", toolName),
		observability.F("conversation_id", event.ConversationID))

	// Get classification rules from config
	rules := h.config.AutoMode

	// Perform classification
	result, err := h.classifier.Classify(ctx, toolName, toolInput, rules)
	if err != nil {
		// Fail open: log error but allow execution
		h.logger.Warn(ctx, "auto_mode.classification_failed",
			observability.F("error", err.Error()),
			observability.F("tool", toolName))
		return hooks.ContinueWithMessage(fmt.Sprintf("Auto-mode classification error: %v", err)), nil
	}

	// Handle classification result
	switch result.Recommendation {
	case "approve":
		h.logger.Info(ctx, "auto_mode.approved",
			observability.F("tool", toolName),
			observability.F("risk", result.Risk),
			observability.F("confidence", result.Confidence))
		return hooks.ContinueWithMessage(
			fmt.Sprintf("Auto-approved (risk: %s, confidence: %.2f)", result.Risk, result.Confidence)), nil

	case "deny":
		h.logger.Info(ctx, "auto_mode.denied",
			observability.F("tool", toolName),
			observability.F("reason", result.Reason))
		return hooks.Block(fmt.Sprintf("Auto-denied: %s", result.Reason)), nil

	case "prompt":
		fallthrough
	default:
		// Require user approval - add metadata but don't block
		modified := event.Clone()
		if modified.Metadata == nil {
			modified.Metadata = make(map[string]any)
		}
		modified.Metadata["auto_mode_recommendation"] = "prompt"
		modified.Metadata["auto_mode_risk"] = result.Risk
		modified.Metadata["auto_mode_reason"] = result.Reason
		modified.Metadata["auto_mode_confidence"] = result.Confidence

		h.logger.Info(ctx, "auto_mode.prompt_required",
			observability.F("tool", toolName),
			observability.F("risk", result.Risk),
			observability.F("reason", result.Reason))

		// Return modified event with classification metadata
		return hooks.ModifyWithMessage(modified,
			fmt.Sprintf("Requires approval (risk: %s, reason: %s)", result.Risk, result.Reason)), nil
	}
}

// SetEnabled enables or disables the auto mode hook
func (h *AutoModeHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// IsEnabled returns whether auto mode is enabled
func (h *AutoModeHook) IsEnabled() bool {
	return h.enabled && h.config.DisableAutoMode != "disable"
}

// GetConfig returns the current auto mode configuration
func (h *AutoModeHook) GetConfig() AutoModeConfig {
	return h.config
}

// UpdateConfig updates the auto mode configuration
func (h *AutoModeHook) UpdateConfig(config AutoModeConfig) {
	h.config = config
}

// HandleAutoModeTransition manages state when entering/exiting auto mode
func (h *AutoModeHook) HandleAutoModeTransition(entering bool) {
	h.state.needsExitAttachment = entering

	if entering {
		h.logger.Info(context.Background(), "auto_mode.entered")
	} else {
		h.logger.Info(context.Background(), "auto_mode.exited")
	}
}

// NeedsExitAttachment checks if exit attachment is needed
func (h *AutoModeHook) NeedsExitAttachment() bool {
	return h.state.needsExitAttachment
}
