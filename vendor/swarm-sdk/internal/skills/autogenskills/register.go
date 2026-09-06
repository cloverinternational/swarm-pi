// Package autogenskills implements the Hermes-style closed learning loop for
// the Swarm SDK skill system.
package autogenskills

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// RegisterWithAgent wires autogenskills into an existing agent.
//
// This is the single-call integration point for downstream consumers
// (TUI, headless servers, custom agents). It handles:
//  1. Creates Service with the given config
//  2. Registers SkillManageTool in the agent's tool registry
//
// CONTRACT:
//   - cfg must be validated before calling.
//   - ag must have a non-nil ToolRegistry.
//   - skillReg is used for factory skill registration and discovery.
//   - existingEphFn is DEPRECATED and ignored. Autogenskills no longer uses
//     ephemeral system prompt injection; guidance is delivered via hooks
//     (BudgetEnforcementHook) that return persistent user messages.
//   - If cfg.Mode is ModeNever, this is a no-op (returns nil service, nil error).
//   - Thread-safe: should only be called during agent setup.
//   - The caller must separately register hooks (LifecycleHook + BudgetEnforcementHook)
//     with their hooks system (SDK hooks.HookSystem or agent HooksManager).
//
// Usage (TUI / headless):
//
//	svc, err := autogenskills.RegisterWithAgent(ag, cfg, skillReg, nil)
func RegisterWithAgent(ag *agent.Agent, cfg Config, skillReg *skills.Registry, existingEphFn func([]*conversation.Message) string) (*Service, error) {
	if !cfg.IsEnabled() {
		return nil, nil // no-op for disabled mode
	}

	if ag == nil {
		return nil, fmt.Errorf("autogenskills: RegisterWithAgent requires non-nil agent")
	}
	if ag.ToolRegistry() == nil {
		return nil, fmt.Errorf("autogenskills: agent must have a ToolRegistry")
	}
	if skillReg == nil {
		return nil, fmt.Errorf("autogenskills: RegisterWithAgent requires non-nil skills registry")
	}

	// Create metrics + service
	metrics := &Metrics{}
	svc, err := NewService(cfg, skillReg, metrics)
	if err != nil {
		return nil, fmt.Errorf("autogenskills: create service: %w", err)
	}

	// Register SkillManage tool
	manageTool, err := NewSkillManageTool(svc)
	if err != nil {
		return nil, fmt.Errorf("autogenskills: create SkillManage tool: %w", err)
	}
	if err := ag.ToolRegistry().Register(manageTool); err != nil {
		return nil, fmt.Errorf("autogenskills: register SkillManage tool: %w", err)
	}

	// Ephemeral nudge removed. Autogenskills relies on hooks (LifecycleHook
	// for metrics, BudgetEnforcementHook for blocking) and the SkillManage
	// tool. No hidden system prompt injection.

	// Inject skill guidance and index into the system prompt so the
	// agent knows about the SkillManage tool and existing skills.
	var promptAdditions []string
	if guidance := svc.GetSKILLSGuidance(); guidance != "" {
		promptAdditions = append(promptAdditions, guidance)
	}
	if index := svc.GetSkillIndex(); index != "" {
		promptAdditions = append(promptAdditions, index)
	}
	if len(promptAdditions) > 0 {
		currentPrompt := ag.SystemPrompt()
		updatedPrompt := currentPrompt + "\n\n" + strings.Join(promptAdditions, "\n\n")
		ag.SetSystemPrompt(updatedPrompt)
	}

	return svc, nil
}

// joinEphemeralParts combines multiple ephemeral text fragments.
func joinEphemeralParts(parts []string) string {
	var result strings.Builder
	for i, p := range parts {
		if i > 0 {
			result.WriteString("\n\n")
		}
		result.WriteString(p)
	}
	return result.String()
}
