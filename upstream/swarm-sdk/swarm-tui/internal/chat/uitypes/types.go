package uitypes

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// Attachment represents a file/image attachment in a message
type Attachment struct {
	FilePath string
	FileName string
	MimeType string
	Content  []byte
	Size     int64
}

// MessageBlock represents a block in the message (content, tool call, result, or hook)
type MessageBlock struct {
	Type             string // "content", "tool_call", "tool_result", "thinking", "hook_execution", "sub_agent_activity"
	Content          string
	ToolCall         *ToolCallDisplay
	ToolResult       *ToolResultDisplay
	HookExecution    *HookExecutionDisplay
	SubAgentActivity *SubAgentDisplay
	Sequence         int              // Order of appearance during streaming
	contentBuilder   *strings.Builder // Used during streaming to avoid repeated allocations
}

// AppendContent appends content efficiently using a builder during streaming
func (b *MessageBlock) AppendContent(content string) {
	if b.contentBuilder == nil {
		b.contentBuilder = &strings.Builder{}
		b.contentBuilder.WriteString(b.Content) // Include any existing content
	}
	b.contentBuilder.WriteString(content)
}

// GetContent returns the accumulated content, checking builder first
func (b *MessageBlock) GetContent() string {
	if b.contentBuilder != nil {
		return b.contentBuilder.String()
	}
	return b.Content
}

// FinalizeContent converts the builder to string and clears the builder
func (b *MessageBlock) FinalizeContent() {
	if b.contentBuilder != nil {
		b.Content = b.contentBuilder.String()
		b.contentBuilder = nil
	}
}

// ResetContentBuilder clears the content builder without finalizing
func (b *MessageBlock) ResetContentBuilder() {
	b.contentBuilder = nil
}

// GetDisplayPriority returns the rendering priority for a message block type.
// Lower numbers render first. This ensures logical display order regardless of
// streaming arrival order (where tool_call metadata arrives before content text).
//
// The scale uses 100-based increments to leave room for insertions.
// Hook blocks use Phase to determine their position relative to the tool call:
//   - "before" hooks render BEFORE the tool call  (priority 150, between content and tool_call)
//   - "after" hooks render AFTER the tool result   (priority 350, between tool_result and sub_agent)
func (b *MessageBlock) GetDisplayPriority() int {
	switch b.Type {
	case "thinking":
		return 0 // Always first (if shown)
	case "content":
		return 100 // User-visible text comes first
	case "hook_execution":
		if b.HookExecution != nil && b.HookExecution.Phase == "before" {
			return 150 // Pre-tool hooks render before the tool_call (200)
		}
		return 350 // Post-tool hooks render after the tool_result (300)
	case "tool_call":
		return 200 // Tool invocation comes after content and pre-hooks
	case "tool_result":
		return 300 // Tool output follows tool call
	case "sub_agent_activity":
		return 500 // Sub-agent activity last
	default:
		return 999 // Unknown types render last
	}
}

// SubAgentDisplay represents aggregated sub-agent activity for UI display
type SubAgentDisplay struct {
	AgentID         string // Unique ID from SDK (distinguishes parallel agents)
	AgentName       string
	TaskInstruction string         // Original task sent to subagent
	Blocks          []MessageBlock // Nested blocks for the sub-agent's activity

	// Time tracking — set once at block creation, never mutated by the renderer
	StartTime time.Time // When this sub-agent block was first created

	// Liveness tracking — used by the renderer to distinguish three states:
	//   1. callback never wired   → LastHeartbeat zero AND EventCount == 0
	//   2. wired but currently idle → LastHeartbeat recent, EventCount may be 0
	//   3. actively producing      → EventCount > 0
	// Without this, every silent stretch looked like state (1), and the user
	// would stare at a frozen-looking spinner for hours before realizing the
	// streaming wiring was broken.
	LastHeartbeat time.Time // Most recent HeartbeatUpdate (zero = none received)
	EventCount    int       // Count of non-heartbeat updates received

	// Stats
	TokenCount int // Running estimate of tokens consumed (updated externally if available)

	// Verb sampling — sampled once at creation and frozen (like Claude Code's useState approach)
	SpinnerVerb    string // Present-tense activity verb, e.g. "Searching…"
	CompletionVerb string // Past-tense verb for "done" state, e.g. "Worked"

	// Completion state — when true the renderer shows a one-line summary
	// instead of in-progress status. Mirrors claude-code's renderToolResult
	// "Done (N tool uses   M tokens   Xs)" format.
	IsComplete   bool
	EndTime      time.Time // When execution finished (zero until IsComplete)
	TurnCount    int       // Final number of provider turns
	ToolUseCount int       // Final number of tool calls made by the sub-agent
}

// ToolCallDisplay represents a tool call for UI display
type ToolCallDisplay struct {
	ID         string
	Name       string
	Parameters map[string]any
}

// ToolResultDisplay represents a tool result for UI display
type ToolResultDisplay struct {
	CallID        string
	Output        string
	Error         string
	ToolName      string         // Name of the tool for result type determination
	Attachments   []Attachment   // Images/PDFs extracted from tool output
	IsStreaming   bool           // True while tool is still streaming output (not yet final)
	Metadata      map[string]any // Tool-specific metadata for rendering (e.g., diff data)
	outputBuilder *strings.Builder
}

// AppendOutput appends a chunk to the output efficiently using a builder
func (t *ToolResultDisplay) AppendOutput(chunk string) {
	if t.outputBuilder == nil {
		t.outputBuilder = &strings.Builder{}
		t.outputBuilder.WriteString(t.Output) // Include any existing output
	}
	t.outputBuilder.WriteString(chunk)
}

// GetOutput returns the accumulated output, finalizing if necessary
func (t *ToolResultDisplay) GetOutput() string {
	if t.outputBuilder != nil {
		return t.outputBuilder.String()
	}
	return t.Output
}

// SetOutput replaces the current output and clears any streaming builder state.
func (t *ToolResultDisplay) SetOutput(output string) {
	t.Output = output
	t.outputBuilder = nil
}

// FinalizeOutput converts the builder to string and clears the builder
func (t *ToolResultDisplay) FinalizeOutput() {
	if t.outputBuilder != nil {
		t.Output = t.outputBuilder.String()
		t.outputBuilder = nil
	}
}

// HookExecutionDisplay represents a hook execution for UI display
type HookExecutionDisplay struct {
	HookName   string
	ToolName   string
	ToolCallID string // ID of the specific tool call this hook belongs to
	Phase      string // "before" or "after"
	Success    bool
	Output     string
	Blocked    bool
	Error      string
}

// SubAgentRenderStyles holds pre-allocated lipgloss styles for sub-agent activity rendering.
// These are created once at app init and reused on every frame, eliminating allocation overhead.
type SubAgentRenderStyles struct {
	Agent     lipgloss.Style // Agent name header
	Meta      lipgloss.Style // Metadata (tool count, etc.)
	Tool      lipgloss.Style // Tool names
	Content   lipgloss.Style // Content text
	Error     lipgloss.Style // Error messages
	Connector lipgloss.Style // Tree connectors
}
