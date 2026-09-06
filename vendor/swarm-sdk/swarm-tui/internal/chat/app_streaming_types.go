package chat

import (
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// ============================================================================
// COMPONENT LOCATOR INTEGRATION
// ============================================================================

// The global ComponentLocator (in locator.go) tracks all clickable components
// independently of the rendering pipeline.
//
// Usage:
//   - During viewConversations/viewChat: call Reset() then Register() components
//   - When user clicks: call FindAt(x, y) to get the component
//   - No padding math needed - coordinates are absolute screen positions

// ============================================================================
// STREAMING MESSAGES (Bubble Tea)
// ============================================================================

// streamChunkMsg carries a chunk of streaming content from the AI
type streamChunkMsg struct {
	content string
	usage   *conversation.TokenUsage
}

// streamDoneMsg signals the end of streaming
type streamDoneMsg struct {
	totalTokens  int
	inputTokens  int            // Current context size (full input sent to model)
	outputTokens int            // Tokens generated in this response
	cacheMetrics map[string]any // Cache hit/creation metrics from provider
	rawPayload   []byte         // Original provider JSON (Principle 3: State Preservation)
}

// streamErrorMsg signals an error during streaming
type streamErrorMsg struct {
	err error
}

// flushPendingUpdateMsg signals that any debounced viewport updates should be flushed
type flushPendingUpdateMsg struct{}

// tokenUpdateMsg carries real-time token usage updates
type tokenUpdateMsg struct {
	inputTokens          int
	outputTokens         int
	contextWindow        int
	effectiveWindow      int
	autoCompactThreshold int
	pctUsed              float64
	isFinal              bool
	source               a2aExecutionSource
	conversationID       string
}

// agentAutoCompactionStartedMsg signals that the SDK crossed the
// auto-compaction threshold and is beginning an in-loop compaction
// (agent.CompactionNeededUpdate).
type agentAutoCompactionStartedMsg struct {
	currentTokens  int
	threshold      int
	contextLimit   int
	source         a2aExecutionSource
	conversationID string
}

// agentAutoCompactionDoneMsg signals that an in-loop compaction succeeded
// (agent.CompactionDoneUpdate).
type agentAutoCompactionDoneMsg struct {
	tokensBefore   int
	tokensAfter    int
	source         a2aExecutionSource
	conversationID string
}

// agentAutoCompactionFailedMsg signals that an in-loop compaction failed
// (agent.CompactionFailedUpdate). fatal means the hard blocking limit was
// crossed and the turn is aborting.
type agentAutoCompactionFailedMsg struct {
	errMsg         string
	fatal          bool
	source         a2aExecutionSource
	conversationID string
}

// ============================================================================
// REAL-TIME AGENT EXECUTION MESSAGES
// ============================================================================

// agentResponseMsg carries the final response from Agent.Execute()
type agentResponseMsg struct {
	content        string
	err            error
	traceID        string
	conversationID string
	AllMessages    []*Message
}

// toolActivity tracks a single in-progress tool for the activity status display.
type toolActivity struct {
	callID      string
	toolName    string
	description string // e.g. "Searching for patterns", "Reading 3 files"
	startTime   time.Time
}

// agentToolCallMsg indicates a tool is about to be called (intermediate update)
type agentToolCallMsg struct {
	toolName       string
	parameters     map[string]any
	callID         string
	sequence       int // Order from LLM response
	source         a2aExecutionSource
	conversationID string
}

// agentToolResultMsg indicates a tool has completed execution (intermediate update)
type agentToolResultMsg struct {
	callID         string
	output         string
	err            error
	sequence       int            // Order from LLM response
	contentBlocks  []any          // Content blocks from tool result (images, etc.)
	metadata       map[string]any // Tool-specific metadata for rendering (e.g., diff data)
	source         a2aExecutionSource
	conversationID string
}

// agentThinkingMsg carries thinking/reasoning content as it's generated
type agentThinkingMsg struct {
	content        string
	append         bool // If true, append to existing thinking; if false, replace
	sequence       int  // Order from LLM response
	source         a2aExecutionSource
	conversationID string
}

// REMOVED: agentTokenEstimateMsg
// We now only use REAL token counts from API responses
// See: TOKEN_COUNT_FIX_PLAN.md

// agentContentUpdateMsg carries assistant content updates during execution
type agentContentUpdateMsg struct {
	content        string
	append         bool // If true, append to existing content; if false, replace
	sequence       int  // Order from LLM response
	source         a2aExecutionSource
	conversationID string
}

// agentAssistantMessageCompleteMsg is the terminal signal for a single assistant
// turn. It fires once per turn after all incremental Content/Thinking/Tool deltas
// have been emitted, so the handler can authoritatively finalize the current
// placeholder — including backfilling content that the provider delivered without
// incremental callbacks (e.g., tool-only final turns, non-streaming responses).
// Sourced from the SDK's AssistantMessageUpdate intermediate update.
type agentAssistantMessageCompleteMsg struct {
	content        string
	thinking       string
	finishReason   string
	turn           int
	inputTokens    int
	outputTokens   int
	sequence       int
	source         a2aExecutionSource
	conversationID string
}

// agentHookExecutionMsg carries hook execution results for UI display
type agentHookExecutionMsg struct {
	hookName       string
	toolName       string
	toolCallID     string // ID of the specific tool call this hook is for
	phase          string // "before" or "after"
	success        bool
	output         string
	blocked        bool
	errMsg         string
	sequence       int // Order from LLM response
	source         a2aExecutionSource
	conversationID string
}

// agentToolOutputChunkMsg carries incremental tool output (streaming)
type agentToolOutputChunkMsg struct {
	callID         string
	chunk          string
	stream         string // "stdout" or "stderr"
	sequence       int    // Order from LLM response
	source         a2aExecutionSource
	conversationID string
}

// agentSubAgentUpdateMsg carries wrapped sub-agent updates
type agentSubAgentUpdateMsg struct {
	agentID        string
	agentName      string
	update         agent.IntermediateUpdate
	sequence       int // Order from LLM response
	source         a2aExecutionSource
	conversationID string
}

// providerEventMsg carries provider-level events like retries and switching
type providerEventMsg struct {
	Type         string // "retry", "switch"
	ProviderName string
	ModelName    string
	Attempt      int
	MaxAttempts  int
	Error        string
	WaitDuration time.Duration
	FromProvider string // for switch
	ToProvider   string // for switch
	FromModel    string // for switch
	ToModel      string // for switch
	Reason       error
}

type a2aFocusConversationMsg struct {
	ConversationID string
}

type a2aPeerMessageMsg struct {
	ConversationID string
	Message        *Message
	PeerHandle     string
	AutoFocus      bool
}

type a2aInboundStartedMsg struct {
	ConversationID string
	PeerHandle     string
}

type a2aInboundUpdateMsg struct {
	ConversationID string
	PeerHandle     string
	Update         agent.IntermediateUpdate
}

type a2aInboundFinishedMsg struct {
	ConversationID string
	PeerHandle     string
	Err            error
}

// agentFallbackMsg carries fallback chain events from SDK to TUI
type agentFallbackMsg struct {
	provider       string
	model          string
	fromProvider   string
	fromModel      string
	attemptIndex   int
	status         string // "attempting", "failed", "success"
	errMsg         string
	duration       time.Duration
	sequence       int
	source         a2aExecutionSource
	conversationID string
}

// agentExhaustedMsg is sent when every model in the fallback chain has failed
// and the agent is now blocking, waiting for the user to pick a new profile.
// The TUI opens the profile switcher in "exhausted" mode; on selection it
// builds a new chain and sends a FallbackDecision on the Response channel.
type agentExhaustedMsg struct {
	provider string // last provider attempted
	model    string // last model attempted
	attempts int    // total attempt count
	errMsg   string // last error message
	// Response is the channel the agent is blocking on.
	// Send a FallbackDecision to unblock it.
	response       chan agent.FallbackDecision
	source         a2aExecutionSource
	conversationID string
}

// agentTickMsg is sent periodically during agent execution to drain the update queue
type agentTickMsg struct{}

// subAgentExhaustedMsg is sent when a sub-agent's fallback chain has failed
// and it's blocking waiting for the parent to pick a new profile.
// This mirrors agentExhaustedMsg but includes sub-agent identification.
type subAgentExhaustedMsg struct {
	agentID        string
	agentName      string
	provider       string                      // last provider attempted
	model          string                      // last model attempted
	attempts       int                         // total attempt count
	errMsg         string                      // last error message
	response       chan agent.FallbackDecision // channel to send decision back
	source         a2aExecutionSource
	conversationID string
}

// dreamStartedMsg is sent when a Dream consolidation run begins.
type dreamStartedMsg struct{}

// dreamCompleteMsg is sent when a Dream consolidation run completes.
type dreamCompleteMsg struct {
	// Ran is true if consolidation actually executed (conditions were met).
	Ran bool
	// Err holds any error that occurred.
	Err error
}

// ─── Plan Mode Messages ────────────────────────────────────────────────────────

// planApprovedMsg is sent by the plan approval modal when the user approves
// the plan. It carries the (potentially edited) plan and context-clear preference.
type planApprovedMsg struct {
	Plan         string // Final plan content (may differ from submitted plan if user edited)
	ClearContext bool   // True if user wants to compact context before implementing
}

// planRejectedMsg is sent by the plan approval modal when the user rejects
// the plan. It carries the user's feedback for the agent to act on.
type planRejectedMsg struct {
	Feedback string // User's rejection reason / revision instructions
}
