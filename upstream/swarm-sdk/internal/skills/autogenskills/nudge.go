// Package autogenskills implements the Hermes-style closed learning loop for
// the Swarm SDK skill system.
package autogenskills

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// NudgeBuilder generates NudgeFragment strings for injection into the system prompt.
//
// CONTRACT:
//   - All methods are pure functions (no side effects, no state mutation).
//   - Returns zero NudgeFragment when no nudge is warranted.
//   - Never generates nudges for empty skill name lists.
//   - All fragments include a clear call-to-action for the LLM.
type NudgeBuilder struct{}

// NewNudgeBuilder creates a NudgeBuilder.
func NewNudgeBuilder() *NudgeBuilder {
	return &NudgeBuilder{}
}

// BuildNudge generates a nudge fragment based on the current context and config.
// Returns a zero NudgeFragment if no nudge should be sent this turn.
//
// CONTRACT:
//   - Uses NudgeContext.ShouldNudge() internally to check thresholds.
//   - If a nudge is warranted, generates a concise, actionable fragment.
//   - Includes existing skill names to avoid duplicates.
func (b *NudgeBuilder) BuildNudge(ctx NudgeContext, cfg Config) NudgeFragment {
	trigger := cfg.Trigger.WithDefaults()
	if !ctx.ShouldNudge(trigger) {
		return NudgeFragment("")
	}

	return b.buildFragment(ctx, cfg)
}

// buildFragment creates the actual nudge text. Internal method — callers
// should use BuildNudge which does the threshold check first.
func (b *NudgeBuilder) buildFragment(ctx NudgeContext, cfg Config) NudgeFragment {
	var parts []string

	parts = append(parts, "You have been working for a while. Consider creating a skill if you notice a recurring pattern.")

	if len(ctx.ExistingSkillNames) > 0 {
		parts = append(parts, fmt.Sprintf("Existing skills: %s.", strings.Join(ctx.ExistingSkillNames, ", ")))
	} else {
		parts = append(parts, "No skills exist yet.")
	}

	parts = append(parts, "Review recent learning class-first: patch a loaded skill, patch an umbrella, or add a support file before creating anything.")
	parts = append(parts, "If nothing generalizes beyond this task, record a no-mutation review instead of manufacturing a skill.")

	fragment := strings.Join(parts, " ")
	return NudgeFragment(fragment)
}

// BuildThresholdNudge is a specialized nudge when a specific threshold was just crossed.
// More targeted than the generic nudge — mentions exactly which threshold triggered.
func (b *NudgeBuilder) BuildThresholdNudge(ctx NudgeContext, cfg Config, thresholdName string) NudgeFragment {
	trigger := cfg.Trigger.WithDefaults()
	if !ctx.ShouldNudge(trigger) {
		return NudgeFragment("")
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("Threshold '%s' reached (%d tool calls this session).", thresholdName, ctx.ToolCallCount))
	parts = append(parts, "Review whether recent learning belongs in a loaded skill, an umbrella, or a support file.")

	if len(ctx.ExistingSkillNames) > 0 {
		parts = append(parts, fmt.Sprintf("Existing skills: %s.", strings.Join(ctx.ExistingSkillNames, ", ")))
	}

	parts = append(parts, "Create only as a last resort; a no-mutation review is valid when nothing generalizes.")

	return NudgeFragment(strings.Join(parts, " "))
}

// BuildPostErrorNudge nudges after an error was successfully resolved.
// Focuses on capturing the fix as a skill for future reference.
func (b *NudgeBuilder) BuildPostErrorNudge(ctx NudgeContext, cfg Config, errorPattern string) NudgeFragment {
	trigger := cfg.Trigger.WithDefaults()
	if trigger.ErrorResolutionThreshold == 0 || ctx.ErrorResolvedCount < trigger.ErrorResolutionThreshold {
		return NudgeFragment("")
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("You just resolved an error pattern: %q.", errorPattern))
	parts = append(parts, "Consider capturing this fix as a skill so it can be reused automatically.")

	if len(ctx.ExistingSkillNames) > 0 {
		parts = append(parts, fmt.Sprintf("Existing skills: %s.", strings.Join(ctx.ExistingSkillNames, ", ")))
	}

	return NudgeFragment(strings.Join(parts, " "))
}

// BuildNudgeFn returns a closure that produces a nudge string.
//
// DEPRECATED: Ephemeral system prompt injection is invisible to the model,
// not persistent in conversation history, and breaks prompt caching.
// The autogenskills system now relies on hooks (BudgetEnforcementHook for
// hard blocks, LifecycleHook for metrics) and the SkillManage tool.
// Soft reminders should use hooks that return hooks.ContinueWithMessage().
//
// CONTRACT:
//   - Returns "" when no nudge is warranted.
//   - skillNameGetter is optional; when nil, no existing skills are listed.
func BuildNudgeFn(svc *Service, skillNameGetter func() []string) func([]*conversation.Message) string {
	return func(_ []*conversation.Message) string {
		var existing []string
		if skillNameGetter != nil {
			existing = skillNameGetter()
		}
		frag := svc.GetNudgeFragment(existing)
		if frag.IsZero() {
			return ""
		}
		// Wrap in system-reminder tags so the Anthropic translator treats
		// it as an ephemeral block (placed in the uncached third system block).
		return fmt.Sprintf("<system-reminder>\n%s\n</system-reminder>", frag.String())
	}
}

// BuildNudgeFnWithRegistry is a convenience variant that lists skill names
// from a skills.Registry.
func BuildNudgeFnWithRegistry(svc *Service, reg *skills.Registry) func([]*conversation.Message) string {
	getter := func() []string {
		if reg == nil {
			return nil
		}
		var names []string
		for _, s := range reg.List() {
			names = append(names, s.Metadata.Name)
		}
		return names
	}
	return BuildNudgeFn(svc, getter)
}
