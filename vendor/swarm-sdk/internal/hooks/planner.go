// Package hooks implements the event hook system.
// This file provides the HookPlanner for creating execution plans.
package hooks

// HookPlanner creates execution plans for hook events.
type HookPlanner struct {
	configHierarchy *ConfigHierarchy
}

// NewHookPlanner creates a new hook planner.
func NewHookPlanner(hierarchy *ConfigHierarchy) *HookPlanner {
	return &HookPlanner{
		configHierarchy: hierarchy,
	}
}

// ExecutionPlan represents a plan for executing hooks.
type ExecutionPlan struct {
	EventName  HookEventName
	Hooks      []CommandHookConfig
	Sequential bool
	Matcher    string
}

// EventContext provides context for matching hooks.
type EventContext struct {
	ToolName string // For tool events
	Trigger  string // For session/compress events (source/reason)
}

// CreateExecutionPlan creates an execution plan for the given event.
func (p *HookPlanner) CreateExecutionPlan(eventName HookEventName, ctx *EventContext) (*ExecutionPlan, error) {
	config := p.configHierarchy.GetMergedConfig()
	if config == nil {
		return nil, nil
	}

	definitions := config.GetDefinitionsForEvent(eventName)
	if len(definitions) == 0 {
		return nil, nil
	}

	// Get match target
	matchTarget := p.getMatchTarget(eventName, ctx)

	// Filter matching definitions and collect hooks
	var hooks []CommandHookConfig
	sequential := false
	seen := make(map[string]bool)

	for _, def := range definitions {
		if !def.Matches(matchTarget) {
			continue
		}

		// If any definition is sequential, run all sequentially
		if def.Sequential {
			sequential = true
		}

		// Deduplicate hooks
		for _, hook := range def.Hooks {
			key := hook.GetKey()
			if seen[key] {
				continue
			}

			// Check if hook is disabled
			if hook.Name != "" && p.configHierarchy.IsHookDisabled(hook.Name) {
				continue
			}

			seen[key] = true
			hooks = append(hooks, hook)
		}
	}

	if len(hooks) == 0 {
		return nil, nil
	}

	return &ExecutionPlan{
		EventName:  eventName,
		Hooks:      hooks,
		Sequential: sequential,
		Matcher:    matchTarget,
	}, nil
}

// getMatchTarget returns the target string to match against hook matchers.
func (p *HookPlanner) getMatchTarget(eventName HookEventName, ctx *EventContext) string {
	if ctx == nil {
		return "*"
	}

	switch eventName {
	case EventBeforeTool, EventAfterTool:
		if ctx.ToolName != "" {
			return ctx.ToolName
		}
	case EventSessionStart, EventSessionEnd, EventPreCompress:
		if ctx.Trigger != "" {
			return ctx.Trigger
		}
	}

	return "*"
}

// GetAllHooks returns all registered hooks with their metadata.
func (p *HookPlanner) GetAllHooks() []HookRegistryEntry {
	config := p.configHierarchy.GetMergedConfig()
	if config == nil {
		return nil
	}

	var entries []HookRegistryEntry

	collectFromEvent := func(eventName HookEventName, defs []HookDefinition) {
		for _, def := range defs {
			for _, hook := range def.Hooks {
				entries = append(entries, HookRegistryEntry{
					Config:     hook,
					EventName:  eventName,
					Matcher:    def.Matcher,
					Sequential: def.Sequential,
					Enabled:    !p.configHierarchy.IsHookDisabled(hook.Name),
				})
			}
		}
	}

	collectFromEvent(EventSessionStart, config.SessionStart)
	collectFromEvent(EventSessionEnd, config.SessionEnd)
	collectFromEvent(EventBeforeAgent, config.BeforeAgent)
	collectFromEvent(EventAfterAgent, config.AfterAgent)
	collectFromEvent(EventSubagentStop, config.SubagentStop)
	collectFromEvent(EventBeforeModel, config.BeforeModel)
	collectFromEvent(EventAfterModel, config.AfterModel)
	collectFromEvent(EventBeforeToolSelection, config.BeforeToolSelection)
	collectFromEvent(EventBeforeTool, config.BeforeTool)
	collectFromEvent(EventAfterTool, config.AfterTool)
	collectFromEvent(EventPreCompress, config.PreCompress)
	collectFromEvent(EventNotification, config.Notification)

	return entries
}

// HookRegistryEntry represents a registered hook with metadata.
type HookRegistryEntry struct {
	Config     CommandHookConfig
	EventName  HookEventName
	Matcher    string
	Sequential bool
	Enabled    bool
}
