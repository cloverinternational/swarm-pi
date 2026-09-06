// Package autogenskills implements the Hermes-style closed learning loop for
// the Swarm SDK skill system.
package autogenskills

import (
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// TriggerReason describes why a skill creation was initiated.
// CONTRACT: All reasons are mutually exclusive — a skill has exactly one trigger.
type TriggerReason string

const (
	// TriggerManual means the skill was created via an explicit CreateSkill API call.
	// The LLM was not nudged; the user or downstream consumer initiated directly.
	TriggerManual TriggerReason = "manual"

	// TriggerToolCallThreshold means the system hit the configured tool-call threshold
	// and nudged the LLM, which then voluntarily created a skill.
	TriggerToolCallThreshold TriggerReason = "tool_call_threshold"

	// TriggerErrorResolution means the system hit the configured error-resolution
	// threshold and nudged the LLM to create a skill from the successful fix.
	TriggerErrorResolution TriggerReason = "error_resolution"

	// TriggerLLMNudge means the LLM created a skill after receiving a nudge,
	// but not due to a specific threshold (e.g. the LLM proactively chose to).
	TriggerLLMNudge TriggerReason = "llm_nudge"
)

// String returns the human-readable description of the trigger reason.
func (r TriggerReason) String() string {
	switch r {
	case TriggerManual:
		return "manual API call"
	case TriggerToolCallThreshold:
		return "tool call threshold met"
	case TriggerErrorResolution:
		return "error resolution threshold met"
	case TriggerLLMNudge:
		return "LLM nudge response"
	default:
		return string(r)
	}
}

// IsValid returns true if the trigger reason is one of the known constants.
func (r TriggerReason) IsValid() bool {
	switch r {
	case TriggerManual, TriggerToolCallThreshold, TriggerErrorResolution, TriggerLLMNudge:
		return true
	}
	return false
}

// NudgeFragment is a strongly-typed string injected into the system prompt
// to nudge the LLM toward skill creation.
//
// CONTRACT:
//   - Zero value ("" or nil underlying) means "no nudge this turn".
//   - Non-empty means the fragment should be appended to the system prompt.
//   - Callers must check IsZero() before injection to avoid empty noise.
type NudgeFragment string

// IsZero returns true if this fragment should not be injected.
// The zero value (""), whitespace-only, and explicitly set "" all return true.
func (n NudgeFragment) IsZero() bool {
	return string(n) == ""
}

// String returns the raw fragment text for prompt assembly.
func (n NudgeFragment) String() string {
	return string(n)
}

// Validate returns an error if the fragment is too long or malformed.
// CONTRACT: Nudge fragments must be ≤ 2048 chars to fit within token budgets.
func (n NudgeFragment) Validate() error {
	if len(string(n)) > 2048 {
		return fmt.Errorf("autogenskills: nudge fragment %d chars exceeds maximum 2048", len(string(n)))
	}
	return nil
}

// CreateOptions defines the parameters for creating a new skill.
//
// CONTRACT:
//   - Name must be lowercase alphanumeric + hyphens (matches agentskills.io spec).
//   - Instruction length is enforced by the configured service/factory policy.
//   - TriggerReason must be valid (use TriggerManual for programmatic creation).
//   - Factory methods are the only way to construct CreateOptions (prevent direct struct literals).
type CreateOptions struct {
	// Name is the skill identifier. Required.
	// CONTRACT: Must match ^[a-z0-9]+(-[a-z0-9]+)*$, be at most 64
	// characters, and not be "archive" or a Windows device alias.
	Name string

	// Description explains what the skill does. Required.
	// CONTRACT: Max skills.MaxDescriptionLength (1500) chars. This is above the
	// agentskills.io limit of 1024 by design — see that constant for why.
	Description string

	// Instructions are the skill body content. Required.
	// A configured MinInstructionsLength may impose a minimum.
	Instructions string

	// Tags for discovery and categorization. Optional.
	Tags []string

	// Category for organization. Optional.
	Category string

	// TriggerReason records why this skill was created. Required.
	// CONTRACT: Must be a valid TriggerReason constant.
	TriggerReason TriggerReason

	// LLMProvider records which provider generated this skill. Optional.
	// Used for analytics and curator decisions (e.g. "anthropic", "openai").
	LLMProvider string
}

// Validate checks that the create options are valid.
// CONTRACT: Returns a descriptive error for each violation.
func (o CreateOptions) Validate() error {
	if o.Name == "" {
		return fmt.Errorf("autogenskills: create options: name is required")
	}
	if !isSafeSkillDirName(o.Name) {
		return fmt.Errorf("autogenskills: create options: name %q must match ^[a-z0-9]+(-[a-z0-9]+)*$, be at most 64 characters, and not be reserved", o.Name)
	}
	if o.Description == "" {
		return fmt.Errorf("autogenskills: create options: description is required")
	}
	if o.Instructions == "" {
		return fmt.Errorf("autogenskills: create options: instructions are required")
	}
	if !o.TriggerReason.IsValid() {
		return fmt.Errorf("autogenskills: create options: trigger_reason %q is not valid", o.TriggerReason)
	}
	return nil
}

// CreationResult represents the outcome of a skill creation operation.
type CreationResult struct {
	// Skill is the created skill metadata and content.
	// Nil if creation failed.
	Skill *skills.Skill

	// Path is the filesystem path where the skill was written.
	// Empty if creation failed.
	Path string

	// CreatedAt is the timestamp of creation.
	CreatedAt time.Time

	// Error is non-nil if creation failed.
	// When non-nil, Skill and Path are zero values.
	Error error
}

// IsSuccess returns true if the skill was created successfully.
func (r CreationResult) IsSuccess() bool {
	return r.Error == nil && r.Skill != nil
}

// PatchOptions specifies how a skill should be patched.
type PatchOptions struct {
	// Name is the skill to patch (required).
	Name string
	// Instructions is the new skill body (optional — if empty, only description/tags change).
	Instructions string
	// Description is the new description (optional — if empty, existing is kept).
	Description string
	// AppendInstructions appends to existing instructions instead of replacing.
	AppendInstructions bool
	// Tags to add (appended to existing tags, not replaced).
	Tags []string
	// TriggerReason for the patch (for metrics).
	TriggerReason TriggerReason
}

// PatchResult is the outcome of patching a skill.
type PatchResult struct {
	Skill           *skills.Skill
	Path            string
	PreviousVersion string
	NewVersion      string
	Error           error
}

// IsSuccess returns true if the patch succeeded.
func (r PatchResult) IsSuccess() bool {
	return r.Error == nil && r.Skill != nil
}

// NudgeContext holds runtime state used when deciding whether to nudge the LLM.
//
// CONTRACT:
//   - All counters are cumulative from session start.
//   - ExistingSkillNames prevents suggesting skills that already exist.
//   - Zero values are valid (new session with no activity).
type NudgeContext struct {
	// TurnCount is the total number of agent turns in the current session.
	TurnCount uint

	// ToolCallCount is the total number of tool calls made this session.
	ToolCallCount uint

	// ErrorCount is the number of tool errors encountered this session.
	ErrorCount uint

	// ErrorResolvedCount is the number of errors that were subsequently resolved.
	ErrorResolvedCount uint

	// ExistingSkillNames are the names of skills already in the registry.
	// Used to avoid suggesting duplicate skills.
	ExistingSkillNames []string

	// LastNudgeTurn is the turn number of the last nudge (0 = never).
	// Used to enforce NudgeInterval from TriggerConfig.
	LastNudgeTurn uint
}

// ShouldNudge returns true if the context meets the conditions for a nudge.
// This is a pure function — it does not mutate state or track history.
//
// CONTRACT:
//   - thresholds come from TriggerConfig (passed by caller).
//   - returns false if interval has not elapsed since last nudge.
func (ctx NudgeContext) ShouldNudge(trigger TriggerConfig) bool {
	// If we nudged recently, don't nudge again.
	if ctx.LastNudgeTurn > 0 && ctx.TurnCount-ctx.LastNudgeTurn < trigger.NudgeInterval {
		return false
	}

	// Check tool call threshold.
	if trigger.ToolCallThreshold > 0 && ctx.ToolCallCount >= trigger.ToolCallThreshold {
		return true
	}

	// Check error resolution threshold.
	if trigger.ErrorResolutionThreshold > 0 && ctx.ErrorResolvedCount >= trigger.ErrorResolutionThreshold {
		return true
	}

	return false
}
