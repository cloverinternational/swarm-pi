// Package types defines core domain types for the chat UI.
//
// These types represent the data model for messages, conversations,
// and UI state without any rendering or framework dependencies.
package types

import (
	"sort"
	"time"
)

// BlockType identifies the type of content block in a message.
type BlockType int

const (
	// BlockThinking represents extended thinking content (Claude feature).
	BlockThinking BlockType = iota
	// BlockContent represents user-visible text content.
	BlockContent
	// BlockToolCall represents a tool invocation.
	BlockToolCall
	// BlockToolResult represents output from a tool execution.
	BlockToolResult
	// BlockHook represents a hook execution event.
	BlockHook
	// BlockSubAgent represents sub-agent activity.
	BlockSubAgent
)

// String returns the string representation of the block type.
func (b BlockType) String() string {
	switch b {
	case BlockThinking:
		return "thinking"
	case BlockContent:
		return "content"
	case BlockToolCall:
		return "tool_call"
	case BlockToolResult:
		return "tool_result"
	case BlockHook:
		return "hook"
	case BlockSubAgent:
		return "sub_agent"
	default:
		return "unknown"
	}
}

// Message represents a chat message with all display content.
type Message struct {
	// Role is the message sender ("user" or "assistant").
	Role string
	// Content is the main text content of the message.
	Content string
	// Timestamp is when the message was created.
	Timestamp time.Time

	// OrderedBlocks contains interleaved content for streaming rendering.
	// Blocks are ordered by sequence number to preserve arrival order.
	OrderedBlocks []MessageBlock

	// Thinking is extended thinking content (Claude feature).
	Thinking string

	// Attachments are files/images attached to the message.
	Attachments []Attachment

	// ToolCalls contains tool invocations in this message.
	ToolCalls []ToolCallDisplay
	// ToolResults contains tool outputs in this message.
	ToolResults []ToolResultDisplay

	// IsComplete indicates if the response is fully received.
	IsComplete bool
	// ElapsedTime is how long the agent worked on this response.
	ElapsedTime time.Duration
	// Model is the AI model used for this response.
	Model string

	// Metadata contains arbitrary key-value data.
	Metadata map[string]any

	// Pre-processed tool results for efficient rendering (cached).
	ReadResults  map[string]*ReadResult
	EditResults  map[string]*EditResult
	PatchResults map[string]*PatchResult
}

// GetOrderedBlocks returns message blocks in correct display order.
// This is the CONTRACTUAL way to access blocks - they are guaranteed to be sorted by sequence number.
//
// CONTRACT: UI code MUST use this method to access blocks for display.
// Direct access to OrderedBlocks may show blocks out of order.
func (m *Message) GetOrderedBlocks() []MessageBlock {
	if m == nil || len(m.OrderedBlocks) == 0 {
		return nil
	}

	// Fast path: blocks are already in sequence order (common case during streaming).
	alreadySorted := true
	for i := 1; i < len(m.OrderedBlocks); i++ {
		if m.OrderedBlocks[i].Sequence < m.OrderedBlocks[i-1].Sequence {
			alreadySorted = false
			break
		}
	}
	if alreadySorted {
		return m.OrderedBlocks
	}

	// Slow path: out-of-order delivery (rare).
	sorted := make([]MessageBlock, len(m.OrderedBlocks))
	copy(sorted, m.OrderedBlocks)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Sequence < sorted[j].Sequence
	})
	return sorted
}

// MessageBlock represents a renderable unit within a message.
// During streaming, blocks arrive in sequence and are rendered
// in arrival order to preserve the natural flow of content.
type MessageBlock struct {
	// Type identifies the kind of content in this block.
	Type BlockType
	// Sequence is the arrival order during streaming.
	Sequence int
	// Content is the text content for content blocks.
	Content string

	// Type-specific data (only one is set based on Type).
	ToolCall         *ToolCallDisplay
	ToolResult       *ToolResultDisplay
	HookExecution    *HookExecutionDisplay
	SubAgentActivity *SubAgentDisplay
}

// Attachment represents a file or image attachment in a message.
type Attachment struct {
	// FilePath is the full path to the attached file.
	FilePath string
	// FileName is the display name of the file.
	FileName string
	// MimeType is the MIME type of the file.
	MimeType string
	// Content is the raw file content.
	Content []byte
	// Size is the file size in bytes.
	Size int64
}

// ToolCallDisplay represents a tool invocation for UI rendering.
type ToolCallDisplay struct {
	// ID is the unique identifier for this tool call.
	ID string
	// Name is the tool name (e.g., "Read", "Bash", "Edit").
	Name string
	// Parameters contains the tool input parameters.
	Parameters map[string]any
}

// ToolResultDisplay represents tool output for UI rendering.
type ToolResultDisplay struct {
	// CallID links this result to its originating tool call.
	CallID string
	// Output is the successful result content.
	Output string
	// Error is the error message if the tool failed.
	Error string
	// ToolName is the name of the tool that produced this result.
	ToolName string
}

// HookExecutionDisplay represents a hook execution event.
type HookExecutionDisplay struct {
	// HookName is the name of the hook that was executed.
	HookName string
	// ToolName is the tool that triggered the hook.
	ToolName string
	// Phase is "before" or "after".
	Phase string
	// Success indicates if the hook executed successfully.
	Success bool
	// Output is the hook's output.
	Output string
	// Blocked indicates if the hook blocked the operation.
	Blocked bool
	// Error is the error message if the hook failed.
	Error string
}

// SubAgentDisplay represents aggregated sub-agent activity.
type SubAgentDisplay struct {
	// AgentID is the unique identifier from the SDK (distinguishes parallel agents).
	AgentID string
	// AgentName is the human-readable name of the sub-agent.
	AgentName string
	// TaskInstruction is the original task sent to the sub-agent.
	TaskInstruction string
	// Blocks contains nested activity for this sub-agent.
	Blocks []MessageBlock
	// StartTime is when this sub-agent block was first created.
	StartTime time.Time
	// LastHeartbeat tracks the most recent HeartbeatUpdate (zero = none received).
	LastHeartbeat time.Time
	// EventCount is the number of non-heartbeat updates received.
	EventCount int
	// TokenCount is a running estimate of tokens consumed.
	TokenCount int
	// SpinnerVerb is a present-tense activity verb, frozen at creation.
	SpinnerVerb string
	// CompletionVerb is a past-tense verb for the done state, frozen at creation.
	CompletionVerb string
	// IsComplete indicates the sub-agent has finished executing. When true,
	// the renderer collapses the in-progress display into a one-line summary.
	IsComplete bool
	// EndTime is when execution finished (zero until IsComplete).
	EndTime time.Time
	// TurnCount is the final number of provider turns the sub-agent took.
	TurnCount int
	// ToolUseCount is the final number of tool calls the sub-agent made.
	ToolUseCount int
}

// ReadResult contains pre-processed file read output.
type ReadResult struct {
	FilePath    string
	Content     string
	Language    string
	LineNumbers bool
	StartLine   int
	EndLine     int
}

// EditResult contains pre-processed file edit output.
type EditResult struct {
	FilePath  string
	OldLines  []string
	NewLines  []string
	DiffLines []string
}

// PatchResult contains pre-processed patch output.
type PatchResult struct {
	FilePath string
	Hunks    []PatchHunk
}

// PatchHunk represents a single diff hunk in a patch.
type PatchHunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []string
}
