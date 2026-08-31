package client

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills/autogenskills"
)

// TestOperatingRulesBlockInjectedWhenSkillBudgetActive verifies that the fixed
// operating-rules block (task-first + skill-budget) is injected into the system
// prompt when autogenskills budget enforcement is active, and that the agent
// metadata block carries the active profile name.
func TestOperatingRulesBlockInjectedWhenSkillBudgetActive(t *testing.T) {
	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
		WithActiveProfile("my-profile"),
		WithAutogenSkills(&AutogenSkillsConfig{
			Mode:       "manual",
			Trigger:    autogenskills.TriggerConfig{ToolCallBudget: 5, WorkingBudget: 90, NudgeInterval: 5, MaxNudgeIgnores: 3, ErrorResolutionThreshold: 1},
			AutogenDir: t.TempDir(),
		}),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	prompt := c.agent.SystemPrompt()

	if !strings.Contains(prompt, `<context name="operatingRules">`) {
		t.Errorf("operatingRules block missing from system prompt when skill budget active.\nPrompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Create a task FIRST") {
		t.Error("operatingRules block missing the task-first rule")
	}
	if !strings.Contains(prompt, "working budget") {
		t.Error("operatingRules block missing the skill-budget rule")
	}
	// Profile must be carried in the agent metadata block.
	if !strings.Contains(prompt, "profile: my-profile") {
		t.Errorf("agentMetadata missing the active profile.\nPrompt:\n%s", prompt)
	}
}

// TestOperatingRulesBlockAbsentWithoutSkillBudget verifies the operating-rules
// block is NOT injected when autogenskills (and therefore the budget) is off.
func TestOperatingRulesBlockAbsentWithoutSkillBudget(t *testing.T) {
	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	prompt := c.agent.SystemPrompt()
	if strings.Contains(prompt, `<context name="operatingRules">`) {
		t.Errorf("operatingRules block should be absent when skill budget is disabled.\nPrompt:\n%s", prompt)
	}
}
