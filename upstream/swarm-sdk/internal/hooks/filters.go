package hooks

import "slices"

import "strings"

// EventFilter provides fast event filtering with pattern matching
type EventFilter struct {
	// Type patterns (supports wildcards)
	typePatterns []string

	// Scope filters
	scopes []HookScope

	// Context filters
	conversationIDs []string
	agentIDs        []string
	modeIDs         []string
	groupIDs        []string
}

// NewEventFilter creates a new event filter
func NewEventFilter() *EventFilter {
	return &EventFilter{}
}

// WithTypePatterns adds type patterns to filter
func (f *EventFilter) WithTypePatterns(patterns ...string) *EventFilter {
	f.typePatterns = append(f.typePatterns, patterns...)
	return f
}

// WithScopes adds scope filters
func (f *EventFilter) WithScopes(scopes ...HookScope) *EventFilter {
	f.scopes = append(f.scopes, scopes...)
	return f
}

// WithConversations adds conversation ID filters
func (f *EventFilter) WithConversations(ids ...string) *EventFilter {
	f.conversationIDs = append(f.conversationIDs, ids...)
	return f
}

// WithAgents adds agent ID filters
func (f *EventFilter) WithAgents(ids ...string) *EventFilter {
	f.agentIDs = append(f.agentIDs, ids...)
	return f
}

// WithModes adds mode ID filters
func (f *EventFilter) WithModes(ids ...string) *EventFilter {
	f.modeIDs = append(f.modeIDs, ids...)
	return f
}

// WithGroups adds group ID filters
func (f *EventFilter) WithGroups(ids ...string) *EventFilter {
	f.groupIDs = append(f.groupIDs, ids...)
	return f
}

// Matches checks if an event matches the filter
func (f *EventFilter) Matches(event Event) bool {
	// Check type patterns
	if len(f.typePatterns) > 0 {
		matched := false
		for _, pattern := range f.typePatterns {
			if matchPattern(pattern, event.Type) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Check conversation filter
	if len(f.conversationIDs) > 0 {
		matched := slices.Contains(f.conversationIDs, event.ConversationID)
		if !matched {
			return false
		}
	}

	// Check agent filter
	if len(f.agentIDs) > 0 {
		matched := slices.Contains(f.agentIDs, event.AgentID)
		if !matched {
			return false
		}
	}

	// Check mode filter
	if len(f.modeIDs) > 0 {
		matched := slices.Contains(f.modeIDs, event.ModeID)
		if !matched {
			return false
		}
	}

	// Check group filter
	if len(f.groupIDs) > 0 {
		matched := slices.Contains(f.groupIDs, event.GroupID)
		if !matched {
			return false
		}
	}

	return true
}

// matchPattern supports wildcards: "message.*", "*.created", "*"
func matchPattern(pattern, value string) bool {
	// Match all
	if pattern == "*" {
		return true
	}

	// Exact match
	if pattern == value {
		return true
	}

	// Prefix match: "message.*" matches "message.created"
	if before, ok := strings.CutSuffix(pattern, ".*"); ok {
		prefix := before
		return strings.HasPrefix(value, prefix+".")
	}

	// Suffix match: "*.created" matches "message.created"
	if after, ok := strings.CutPrefix(pattern, "*."); ok {
		suffix := after
		return strings.HasSuffix(value, "."+suffix)
	}

	// Middle match: "message.*.completed" matches "message.agent.completed"
	if strings.Contains(pattern, ".*") {
		parts := strings.Split(pattern, ".*")
		if len(parts) != 2 {
			return false
		}
		return strings.HasPrefix(value, parts[0]+".") && strings.HasSuffix(value, "."+parts[1])
	}

	return false
}

// CompositeFilter combines multiple filters with AND logic
type CompositeFilter struct {
	filters []*EventFilter
}

// NewCompositeFilter creates a composite filter
func NewCompositeFilter(filters ...*EventFilter) *CompositeFilter {
	return &CompositeFilter{
		filters: filters,
	}
}

// Matches checks if event matches all filters
func (f *CompositeFilter) Matches(event Event) bool {
	for _, filter := range f.filters {
		if !filter.Matches(event) {
			return false
		}
	}
	return true
}

// AddFilter adds a filter to the composite
func (f *CompositeFilter) AddFilter(filter *EventFilter) {
	f.filters = append(f.filters, filter)
}

// OrFilter combines multiple filters with OR logic
type OrFilter struct {
	filters []*EventFilter
}

// NewOrFilter creates an OR filter
func NewOrFilter(filters ...*EventFilter) *OrFilter {
	return &OrFilter{
		filters: filters,
	}
}

// Matches checks if event matches any filter
func (f *OrFilter) Matches(event Event) bool {
	if len(f.filters) == 0 {
		return true
	}

	for _, filter := range f.filters {
		if filter.Matches(event) {
			return true
		}
	}
	return false
}

// AddFilter adds a filter to the OR
func (f *OrFilter) AddFilter(filter *EventFilter) {
	f.filters = append(f.filters, filter)
}

// NotFilter inverts a filter
type NotFilter struct {
	filter *EventFilter
}

// NewNotFilter creates a NOT filter
func NewNotFilter(filter *EventFilter) *NotFilter {
	return &NotFilter{
		filter: filter,
	}
}

// Matches returns true if event does NOT match the filter
func (f *NotFilter) Matches(event Event) bool {
	return !f.filter.Matches(event)
}

// Predefined filter builders

// MatchAll creates a filter that matches all events
func MatchAll() *EventFilter {
	return NewEventFilter()
}

// MatchNone creates a filter that matches no events
func MatchNone() *EventFilter {
	// Return filter with impossible pattern
	return NewEventFilter().WithTypePatterns("__NEVER_MATCH__")
}

// MatchMessageEvents creates a filter for message events
func MatchMessageEvents() *EventFilter {
	return NewEventFilter().WithTypePatterns("message.*")
}

// MatchToolEvents creates a filter for tool events
func MatchToolEvents() *EventFilter {
	return NewEventFilter().WithTypePatterns("tool.*")
}

// MatchProviderEvents creates a filter for provider events
func MatchProviderEvents() *EventFilter {
	return NewEventFilter().WithTypePatterns("provider.*")
}

// MatchLifecycleEvents creates a filter for lifecycle events
func MatchLifecycleEvents() *EventFilter {
	return NewEventFilter().WithTypePatterns(
		"conversation.*",
		"agent.*",
		"mode.*",
		"group.*",
	)
}

// MatchSteeringEvents creates a filter for steering events
func MatchSteeringEvents() *EventFilter {
	return NewEventFilter().WithTypePatterns("steering.*")
}

// MatchErrorEvents creates a filter for error-related events
func MatchErrorEvents() *EventFilter {
	return NewEventFilter().WithTypePatterns(
		"tool.execution_failed",
		"tool.permission_denied",
		"provider.error",
		"group.consensus_failed",
	)
}
