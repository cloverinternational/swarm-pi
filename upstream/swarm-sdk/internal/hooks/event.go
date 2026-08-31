// Package hooks provides event interception capabilities.
// This is Ring 0 - pure interface definitions with no implementations.
package hooks

import "maps"

import "time"

import "github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"

// Event represents something that happened in the system.
// Events are immutable facts that hooks can observe and react to.
type Event struct {
	// ID is the unique identifier for this event.
	ID string

	// Type is the event type in dot-notation.
	// Examples: "message.before_send", "tool.executed", "provider.response"
	Type string

	// Timestamp when the event occurred (RFC3339 format).
	Timestamp time.Time

	// TraceID links this event to a distributed trace.
	TraceID string

	// ConversationID identifies the conversation this event belongs to —
	// EXCEPT for hooks.EventAgentStarted (SessionStart), where no specific
	// conversation necessarily exists yet at process boot: the TUI/headless
	// front ends populate it with the process-level session id
	// (conversation.ProcessSessionID(), the same value they seed
	// rootSessionID/printer-session from) as the best available identity at
	// that moment. Every OTHER event type carries a real conversation id
	// here.
	//
	// This is a real, load-bearing distinction, not a formatting nuance: a
	// joiner that treats ConversationID as uniformly "the conversation" will
	// silently mis-join SessionStart rows. See internal/bench/effect.go's
	// SessionID field doc and internal/conversation/joinkey.go's
	// ProcessSessionID doc for the other end of this — process session id
	// and conversation id are DIFFERENT values that happen to both get
	// called "session" somewhere in this pipeline (Claude-Code-compatible
	// hook JSON/env also spells conversation id as "session_id", by design,
	// matching Claude Code's own session==conversation semantics — see
	// shell_hook.go:buildClaudeCodeInputImpl).
	ConversationID string

	// AgentID identifies the agent.
	AgentID string

	// ModeID identifies the active mode.
	ModeID string

	// GroupID identifies the agent group.
	GroupID string

	// Data contains event-specific payload.
	Data map[string]any

	// Metadata contains additional context.
	Metadata map[string]any

	// ToolOutcome carries the typed, structured outcome of a tool execution
	// on tool.after_execute (AfterTool) events: the real exit code, a
	// success/failure verdict, and the paths a mutating tool actually
	// touched. It is nil on every other event type, and nil on tool events
	// whose tool does not yet report a structured outcome — consumers must
	// treat nil as "no evidence", never as success.
	//
	// It is a typed pointer rather than another Data key so consumers get
	// compile-time checking instead of the map[string]any archaeology that
	// made tool outcomes unrecoverable in the first place. The value is a
	// deep copy owned by this event; mutating it cannot affect the tool
	// result the agent returns.
	//
	// The type lives in the dependency-free internal/toolout leaf package
	// because internal/tools already depends transitively on this package,
	// so this package can never import internal/tools.
	ToolOutcome *toolout.Outcome
}

// Clone creates a deep copy of the event.
// Used when hooks need to modify events.
func (e *Event) Clone() *Event {
	clone := &Event{
		ID:             e.ID,
		Type:           e.Type,
		Timestamp:      e.Timestamp,
		TraceID:        e.TraceID,
		ConversationID: e.ConversationID,
		AgentID:        e.AgentID,
		ModeID:         e.ModeID,
		GroupID:        e.GroupID,
		ToolOutcome:    e.ToolOutcome.Clone(),
	}

	if e.Data != nil {
		clone.Data = make(map[string]any)
		maps.Copy(clone.Data, e.Data)
	}

	if e.Metadata != nil {
		clone.Metadata = make(map[string]any)
		maps.Copy(clone.Metadata, e.Metadata)
	}

	return clone
}

// Standard event types used throughout the SDK.
const (
	// Lifecycle events
	EventConversationCreated   = "conversation.created"
	EventConversationResumed   = "conversation.resumed"
	EventConversationCompleted = "conversation.completed"
	EventConversationArchived  = "conversation.archived"
	EventAgentInitialized      = "agent.initialized"
	EventAgentStarted          = "agent.started"
	EventAgentStopped          = "agent.stopped"
	EventAgentDestroyed        = "agent.destroyed"
	// System telemetry events
	EventSystemMetrics = "system.metrics" // Periodic + session-boundary Go runtime + OS resource snapshot

	// Message events
	EventMessageAdded        = "message.added"
	EventMessageEdited       = "message.edited"
	EventMessageDeleted      = "message.deleted"
	EventMessageBeforeSend   = "message.before_send"
	EventMessageAfterReceive = "message.after_receive"

	// Tool events
	EventToolRegistered          = "tool.registered"
	EventToolUnregistered        = "tool.unregistered"
	EventToolEnabled             = "tool.enabled"
	EventToolDisabled            = "tool.disabled"
	EventToolAvailabilityChanged = "tool.availability_changed" // Batch notification of tool changes
	EventToolBeforeExecute       = "tool.before_execute"
	EventToolAfterExecute        = "tool.after_execute"
	EventToolExecutionFailed     = "tool.execution_failed"
	EventToolPermissionDenied    = "tool.permission_denied"

	// Provider events
	EventProviderBeforeRequest = "provider.before_request"
	EventProviderAfterResponse = "provider.after_response"
	EventProviderStreamChunk   = "provider.stream_chunk"
	EventProviderRateLimited   = "provider.rate_limited"
	EventProviderError         = "provider.error"

	// Context events
	EventContextWindowExceeded = "context.window_exceeded"
	EventContextTrimmed        = "context.trimmed"
	EventContextSummarized     = "context.summarized"
	EventContextRestored       = "context.restored"

	// Mode events
	EventModeEntered             = "mode.entered"
	EventModeExited              = "mode.exited"
	EventModeTransitionRequested = "mode.transition_requested"
	EventModeTransitionBlocked   = "mode.transition_blocked"
	EventModeTransitionCompleted = "mode.transition_completed"

	// Group events
	EventGroupStarted          = "group.started"
	EventGroupAgentCompleted   = "group.agent_completed"
	EventGroupConsensusReached = "group.consensus_reached"
	EventGroupConsensusFailed  = "group.consensus_failed"
	EventGroupCompleted        = "group.completed"
	EventGroupRetrying         = "group.retrying"

	// Steering events
	EventSteeringDecisionRequested = "steering.decision_requested"
	EventSteeringDecisionMade      = "steering.decision_made"
	EventSteeringIntervention      = "steering.intervention"
	EventSteeringApprovalNeeded    = "steering.approval_needed"
	EventSteeringOverride          = "steering.override"

	// A2A events
	EventA2APeerAnnounced    = "a2a.peer_announced"
	EventA2APeerExpired      = "a2a.peer_expired"
	EventA2ATaskCreated      = "a2a.task_created"
	EventA2ATaskUpdated      = "a2a.task_updated"
	EventA2AMessageProjected = "a2a.message_projected"

	// Dream events
	EventDreamConsolidationStarted  = "dream.consolidation_started"
	EventDreamConsolidationComplete = "dream.consolidation_complete"
	EventDreamConsolidationFailed   = "dream.consolidation_failed"
	EventDreamMemoryCandidateStaged = "dream.memory_candidate_staged"
	EventDreamMemorySnapshot        = "dream.memory_snapshot"

	// Findings events
	EventFindingsCaptured  = "findings.captured"
	EventFindingsEvaluated = "findings.evaluated"
	EventFindingsPromoted  = "findings.promoted"

	// Silver events
	EventSilverTreeBuilt = "silver.tree_built"

	// Gold events
	EventGoldAnalysisStarted  = "gold.analysis_started"
	EventGoldAnalysisComplete = "gold.analysis_complete"
	EventGoldAnalysisFailed   = "gold.analysis_failed"
	EventGoldInsightCreated   = "gold.insight_created"

	// Local artifact mirror events
	EventBronzeLocalEventCaptured = "bronze.local_event_captured"
)
