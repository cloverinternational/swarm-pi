package chat

import (
	"regexp"
	"strings"
	"time"
)

// DebugInspectProvider provides access to app internals for agent-based debugging.
// This interface allows the DebugInspect tool to query logs, messages, requests,
// tool executions, and application state programmatically.
type DebugInspectProvider interface {
	// GetLogs returns log entries matching the given criteria
	GetLogs(pattern string, categories []string, level string, limit, offset int) []DebugLogEntry

	// GetMessages returns message info, optionally for a specific message index
	GetMessages(messageID *int) []DebugMessageInfo

	// GetMessageRender returns detailed render information for a specific message
	GetMessageRender(messageID int) *DebugRenderInfo

	// GetRequests returns API request/response info matching the pattern
	GetRequests(pattern string, limit, offset int) []DebugRequestInfo

	// GetToolExecutions returns tool execution traces matching the pattern
	GetToolExecutions(pattern string, limit, offset int) []DebugToolExecution

	// GetAppState returns current application state
	GetAppState() *DebugAppState
}

// DebugLogEntry represents a single log entry for the debug tool
type DebugLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Category  string    `json:"category"`
	Message   string    `json:"message"`
	Raw       string    `json:"raw"`
}

// DebugMessageInfo represents a chat message with metadata for debugging
type DebugMessageInfo struct {
	Index          int                     `json:"index"`
	Role           string                  `json:"role"`
	ContentPreview string                  `json:"content_preview"`
	ContentLength  int                     `json:"content_length"`
	Timestamp      time.Time               `json:"timestamp"`
	ToolCalls      []DebugToolCallInfo     `json:"tool_calls,omitempty"`
	ToolResults    []DebugToolResultInfo   `json:"tool_results,omitempty"`
	Thinking       string                  `json:"thinking,omitempty"`
	ThinkingLength int                     `json:"thinking_length,omitempty"`
	Attachments    []DebugAttachmentInfo   `json:"attachments,omitempty"`
	Blocks         []DebugBlockInfo        `json:"blocks,omitempty"`
	RenderInfo     *DebugMessageRenderInfo `json:"render_info,omitempty"`
	IsComplete     bool                    `json:"is_complete,omitempty"`
	ElapsedTime    string                  `json:"elapsed_time,omitempty"`
	Model          string                  `json:"model,omitempty"`
}

// DebugToolCallInfo represents a tool call for debugging
type DebugToolCallInfo struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Parameters map[string]any `json:"parameters"`
}

// DebugToolResultInfo represents a tool result for debugging
type DebugToolResultInfo struct {
	CallID        string `json:"call_id"`
	OutputPreview string `json:"output_preview"`
	OutputLength  int    `json:"output_length"`
	Error         string `json:"error,omitempty"`
}

// DebugAttachmentInfo represents an attachment for debugging
type DebugAttachmentInfo struct {
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

// DebugBlockInfo represents a message block for debugging
type DebugBlockInfo struct {
	Type     string `json:"type"`
	Sequence int    `json:"sequence"`
	Tool     string `json:"tool,omitempty"`
	Preview  string `json:"preview,omitempty"`
}

// DebugMessageRenderInfo contains rendering metadata for a message
type DebugMessageRenderInfo struct {
	LineStart int  `json:"line_start"`
	LineEnd   int  `json:"line_end"`
	IsFocused bool `json:"is_focused"`
}

// DebugRenderInfo contains detailed render information for a message
type DebugRenderInfo struct {
	MessageID      int                `json:"message_id"`
	Role           string             `json:"role"`
	RenderedLines  []DebugRenderLine  `json:"rendered_lines"`
	TotalLines     int                `json:"total_lines"`
	StylesUsed     []string           `json:"styles_used"`
	BlocksRendered []DebugBlockRender `json:"blocks_rendered"`
}

// DebugRenderLine represents a single rendered line
type DebugRenderLine struct {
	Line    int    `json:"line"`
	Content string `json:"content"`
	Style   string `json:"style"`
}

// DebugBlockRender represents a rendered block
type DebugBlockRender struct {
	Type  string `json:"type"`
	Lines []int  `json:"lines"`
	Tool  string `json:"tool,omitempty"`
}

// DebugRequestInfo represents an API request for debugging
type DebugRequestInfo struct {
	ID           string    `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	Method       string    `json:"method"`
	URL          string    `json:"url"`
	Status       int       `json:"status"`
	DurationMs   int64     `json:"duration_ms"`
	TokensIn     int       `json:"tokens_in"`
	TokensOut    int       `json:"tokens_out"`
	Model        string    `json:"model"`
	MessageCount int       `json:"message_count"`
	Error        string    `json:"error,omitempty"`
}

// DebugToolExecution represents a tool execution trace
type DebugToolExecution struct {
	CallID        string               `json:"call_id"`
	ToolName      string               `json:"tool_name"`
	Parameters    map[string]any       `json:"parameters"`
	OutputPreview string               `json:"output_preview"`
	OutputLength  int                  `json:"output_length"`
	Error         string               `json:"error,omitempty"`
	DurationMs    int64                `json:"duration_ms,omitempty"`
	Hooks         []DebugHookExecution `json:"hooks,omitempty"`
}

// DebugHookExecution represents a hook execution
type DebugHookExecution struct {
	Name    string `json:"name"`
	Phase   string `json:"phase"`
	Blocked bool   `json:"blocked"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

// DebugAppState represents the current application state
type DebugAppState struct {
	Provider        string               `json:"provider"`
	Model           string               `json:"model"`
	ContextWindow   int                  `json:"context_window"`
	TokenCount      int                  `json:"token_count"`
	OperatingMode   string               `json:"operating_mode"`
	ConversationID  string               `json:"conversation_id"`
	MessageCount    int                  `json:"message_count"`
	Streaming       bool                 `json:"streaming"`
	ThinkingEnabled bool                 `json:"thinking_enabled"`
	ThinkingBudget  int                  `json:"thinking_budget,omitempty"`
	CachingEnabled  bool                 `json:"caching_enabled"`
	RenderSettings  *DebugRenderSettings `json:"render_settings,omitempty"`
	Viewport        *DebugViewportState  `json:"viewport,omitempty"`
	QueueState      *DebugQueueState     `json:"queue_state,omitempty"`
}

// DebugRenderSettings contains render configuration
type DebugRenderSettings struct {
	ShowFullOutput bool              `json:"show_full_output"`
	ShowThinking   bool              `json:"show_thinking"`
	ToolColors     map[string]string `json:"tool_colors,omitempty"`
}

// DebugViewportState contains viewport information
type DebugViewportState struct {
	Width        int `json:"width"`
	Height       int `json:"height"`
	ScrollOffset int `json:"scroll_offset"`
	TotalLines   int `json:"total_lines"`
}

// DebugQueueState contains agent update queue information
type DebugQueueState struct {
	Capacity        int `json:"capacity"`
	CurrentDepth    int `json:"current_depth"`
	GlobalUpdateSeq int `json:"global_update_sequence"`
	MessageSeq      int `json:"message_sequence"`
}

// DebugInspectResult is the response from the DebugInspect tool
type DebugInspectResult struct {
	Target string `json:"target"`

	// For logs target
	Logs      []DebugLogEntry `json:"logs,omitempty"`
	LogsTotal int             `json:"logs_total,omitempty"`

	// For messages target
	Messages      []DebugMessageInfo `json:"messages,omitempty"`
	MessagesTotal int                `json:"messages_total,omitempty"`

	// For render target
	Render *DebugRenderInfo `json:"render,omitempty"`

	// For requests target
	Requests      []DebugRequestInfo `json:"requests,omitempty"`
	RequestsTotal int                `json:"requests_total,omitempty"`

	// For tools target
	ToolExecutions      []DebugToolExecution `json:"tool_executions,omitempty"`
	ToolExecutionsTotal int                  `json:"tool_executions_total,omitempty"`

	// For state target
	State *DebugAppState `json:"state,omitempty"`

	// Viewport context (always included)
	Viewport *DebugViewportState `json:"viewport,omitempty"`
}

// Helper functions for implementing the provider

// truncateString truncates a string to maxLen and adds "..." if truncated
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// matchesPattern checks if text matches a regex pattern (case-insensitive)
func matchesPattern(text, pattern string) bool {
	if pattern == "" {
		return true
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		// Fall back to simple contains
		return strings.Contains(strings.ToLower(text), strings.ToLower(pattern))
	}
	return re.MatchString(text)
}

// logLevelToInt converts log level string to int for comparison
func logLevelToInt(level string) int {
	switch strings.ToLower(level) {
	case "trace":
		return 0
	case "debug":
		return 1
	case "info":
		return 2
	case "warn", "warning":
		return 3
	case "error":
		return 4
	case "fatal":
		return 5
	default:
		return -1 // Unknown levels pass through
	}
}

// meetsMinLevel checks if entryLevel meets or exceeds minLevel
func meetsMinLevel(entryLevel, minLevel string) bool {
	if minLevel == "" {
		return true
	}
	entryInt := logLevelToInt(entryLevel)
	minInt := logLevelToInt(minLevel)
	if entryInt < 0 || minInt < 0 {
		return true // Unknown levels pass through
	}
	return entryInt >= minInt
}
