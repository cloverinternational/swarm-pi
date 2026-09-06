package builtin

import (
	"context"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// ComplexityDetectionHook analyzes the user's first message to detect work scope.
// This helps surface planning as a suggestion for complex work without being punitive.
//
// Complexity signals:
// - Keywords: refactor, design, architecture, migrate, implement feature, rewrite
// - Ambiguity: question marks, "figure out", "not sure"
// - Scope: mentions of multiple files, cross-cutting, system-wide
//
// This is purely informational - it sets a session flag that other hooks can read.
type ComplexityDetectionHook struct {
	firstMessageProcessed bool
	isComplexWork         bool
}

// NewComplexityDetectionHook creates a new complexity detection hook
func NewComplexityDetectionHook() *ComplexityDetectionHook {
	return &ComplexityDetectionHook{}
}

// Name returns the hook name
func (h *ComplexityDetectionHook) Name() string {
	return "complexity-detection"
}

// Priority runs very early to detect scope before other hooks
func (h *ComplexityDetectionHook) Priority() int {
	return 99 // Run before session-start (priority 10)
}

// Filter listens for the first user message
func (h *ComplexityDetectionHook) Filter(event hooks.Event) bool {
	if h.firstMessageProcessed {
		return false // Only process once
	}
	return event.Type == hooks.EventMessageAfterReceive
}

// OnEvent analyzes the message for complexity signals
func (h *ComplexityDetectionHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	h.firstMessageProcessed = true

	role, _ := event.Data["role"].(string)
	if role != "user" {
		return hooks.Continue(), nil
	}

	content, _ := event.Data["content"].(string)
	if content == "" {
		return hooks.Continue(), nil
	}

	h.isComplexWork = detectComplexity(content)

	// If complex work detected, surface planning as the suggested first action
	// This is NOT a warning or block - just a helpful suggestion about approach
	if h.isComplexWork {
		msg := `[SESSION INTELLIGENCE]

I notice you're working on something that involves multiple components or architectural decisions.

**Suggested approach:**
Before diving into implementation, spending 2-3 minutes in plan mode will save time downstream.

→ Try: enter_plan_mode()

Then:
1. Sketch out your approach
2. Create a plan artifact
3. Exit plan mode and create tasks
4. Execute with confidence

[This is just a suggestion — you can ignore this and proceed directly if you prefer]`

		return hooks.ContinueWithMessage(msg), nil
	}

	// Simple work - just proceed
	return hooks.Continue(), nil
}

// IsComplexWork returns whether the first message indicates complex work
func (h *ComplexityDetectionHook) IsComplexWork() bool {
	return h.isComplexWork
}

// detectComplexity analyzes content for complexity signals
func detectComplexity(content string) bool {
	lower := strings.ToLower(content)

	// Complexity keywords
	complexKeywords := []string{
		"refactor",
		"design",
		"architecture",
		"migrate",
		"implement feature",
		"new feature",
		"rewrite",
		"rebuild",
		"restructure",
		"cross-cutting",
		"system-wide",
		"major change",
		"overhaul",
		"redesign",
	}

	for _, keyword := range complexKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}

	// Ambiguity signals - these indicate planning is needed
	ambiguitySignals := []string{
		"how do i",
		"how should i",
		"what's the best way",
		"figure out",
		"not sure",
		"uncertain",
		"approach",
		"strategy",
		"plan",
		"design the",
	}

	for _, signal := range ambiguitySignals {
		if strings.Contains(lower, signal) {
			return true
		}
	}

	// Scope signals - multiple files or components
	scopeSignals := []string{
		"multiple",
		"several",
		"multiple files",
		"across",
		"throughout",
		"everywhere",
		"all the",
		"various",
	}

	for _, signal := range scopeSignals {
		if strings.Contains(lower, signal) {
			return true
		}
	}

	// Question mark = likely needs planning
	if strings.Count(lower, "?") >= 2 {
		return true
	}

	return false
}
