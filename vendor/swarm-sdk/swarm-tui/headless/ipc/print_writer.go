// Package ipc provides print writer and event types for headless mode output.
// This package mirrors the event pipeline in headless/ipc/server_print.go
// and produces Claude-compatible NDJSON output.
package ipc

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// PrintWriter writes structured NDJSON events for headless mode.
type PrintWriter struct {
	w   io.Writer
	mu  sync.Mutex
	enc *json.Encoder
}

// NewPrintWriter creates a new PrintWriter that writes to w.
func NewPrintWriter(w io.Writer) *PrintWriter {
	return &PrintWriter{
		w:   w,
		enc: json.NewEncoder(w),
	}
}

// WriteInit writes a system init event.
func (pw *PrintWriter) WriteInit(init PrintSystemInit) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	pw.enc.Encode(init)
}

// WriteAssistant writes an assistant message event.
func (pw *PrintWriter) WriteAssistant(msg PrintAssistantMessage) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	pw.enc.Encode(msg)
}

// WriteUser writes a user message event.
func (pw *PrintWriter) WriteUser(msg PrintUserMessage) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	pw.enc.Encode(msg)
}

// WriteResult writes a result event.
func (pw *PrintWriter) WriteResult(result PrintResultEvent) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	pw.enc.Encode(result)
}

// WriteText writes plain text output (for text mode).
func (pw *PrintWriter) WriteText(text string) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	fmt.Fprintln(pw.w, text)
}

// WriteHookStarted writes a hook started event.
func (pw *PrintWriter) WriteHookStarted(evt PrintHookStartedEvent) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	pw.enc.Encode(evt)
}

// WriteHookProgress writes a hook progress event.
func (pw *PrintWriter) WriteHookProgress(evt PrintHookProgressEvent) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	pw.enc.Encode(evt)
}

// WriteHookResponse writes a hook response event.
func (pw *PrintWriter) WriteHookResponse(evt PrintHookResponseEvent) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	pw.enc.Encode(evt)
}

// PrintSystemInit represents the initial system event.
type PrintSystemInit struct {
	Type           string                 `json:"type"`
	Subtype        string                 `json:"subtype"`
	Timestamp      int64                  `json:"timestamp"`
	Version        string                 `json:"version"`
	SessionID      string                 `json:"session_id"`
	CWD            string                 `json:"cwd"`
	Tools          []string               `json:"tools"`
	MCPServers     []PrintMCPServerStatus `json:"mcp_servers"`
	Model          string                 `json:"model"`
	PermissionMode string                 `json:"permission_mode"`
	SlashCommands  []string               `json:"slash_commands"`
	APIKeySource   string                 `json:"api_key_source"`
	SwarmVersion   string                 `json:"swarm_version"`
	OutputStyle    string                 `json:"output_style"`
	Agents         []string               `json:"agents"`
	Skills         []string               `json:"skills"`
	Plugins        []PrintPlugin          `json:"plugins"`
	UUID           string                 `json:"uuid"`
	FastModeState  string                 `json:"fast_mode_state"`
}

// PrintMCPServerStatus represents MCP server status.
type PrintMCPServerStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// PrintPlugin represents a plugin.
type PrintPlugin struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// PrintAssistantMessage represents an assistant message event.
type PrintAssistantMessage struct {
	Type            string       `json:"type"`
	Message         PrintMessage `json:"message"`
	ParentToolUseID *string      `json:"parent_tool_use_id,omitempty"`
	SessionID       string       `json:"session_id"`
	UUID            string       `json:"uuid"`
}

// PrintMessage represents a message.
type PrintMessage struct {
	Type         string              `json:"type"`
	Role         string              `json:"role"`
	Model        string              `json:"model"`
	ID           string              `json:"id"`
	Content      []PrintContentBlock `json:"content"`
	StopReason   *string             `json:"stop_reason,omitempty"`
	StopSequence *string             `json:"stop_sequence,omitempty"`
	Usage        PrintMessageUsage   `json:"usage"`
	ContextMgmt  any                 `json:"context_mgmt,omitempty"`
}

// PrintContentBlock represents a content block in a message.
type PrintContentBlock struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	Thinking string           `json:"thinking,omitempty"`
	ID       string           `json:"id,omitempty"`
	Name     string           `json:"name,omitempty"`
	Input    map[string]any   `json:"input,omitempty"`
	Caller   *PrintToolCaller `json:"caller,omitempty"`
}

// PrintToolCaller represents the caller of a tool.
type PrintToolCaller struct {
	Type string `json:"type"`
}

// PrintMessageUsage represents token usage.
type PrintMessageUsage struct {
	InputTokens              int                `json:"input_tokens"`
	CacheCreationInputTokens int                `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int                `json:"cache_read_input_tokens"`
	CacheCreation            PrintCacheCreation `json:"cache_creation"`
	OutputTokens             int                `json:"output_tokens"`
	ServiceTier              string             `json:"service_tier"`
	InferenceGeo             string             `json:"inference_geo"`
}

// PrintCacheCreation represents cache creation info.
type PrintCacheCreation struct {
	Tokens int `json:"tokens"`
}

// PrintUserMessage represents a user message event.
type PrintUserMessage struct {
	Type            string             `json:"type"`
	Message         PrintUserContent   `json:"message"`
	ParentToolUseID *string            `json:"parent_tool_use_id,omitempty"`
	SessionID       string             `json:"session_id"`
	UUID            string             `json:"uuid"`
	ToolUseResult   PrintToolUseResult `json:"tool_use_result"`
}

// PrintUserContent represents user message content.
type PrintUserContent struct {
	Role    string                   `json:"role"`
	Content []PrintToolResultContent `json:"content"`
}

// PrintToolResultContent represents tool result content.
type PrintToolResultContent struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
	Output    string `json:"output"`
	System    string `json:"system,omitempty"`
	Error     string `json:"error,omitempty"`
	Title     string `json:"title,omitempty"`
}

// PrintToolUseResult represents the result of a tool use.
type PrintToolUseResult struct {
	Stdout           string `json:"stdout"`
	Interrupted      bool   `json:"interrupted"`
	IsImage          bool   `json:"is_image"`
	NoOutputExpected bool   `json:"no_output_expected"`

	// Structured bash tool-output contract fields (P0.5). All optional and
	// omitempty so text/back-compat callers are unaffected.
	ExitCode            *int           `json:"exit_code,omitempty"`
	DurationMs          *int64         `json:"duration_ms,omitempty"`
	Truncated           bool           `json:"truncated,omitempty"`
	TruncationMethod    string         `json:"truncation_method,omitempty"`
	OriginalOutputBytes *int           `json:"original_output_bytes,omitempty"`
	ReturnedOutputBytes *int           `json:"returned_output_bytes,omitempty"`
	FullOutputPath      string         `json:"full_output_path,omitempty"`
	OutputContract      map[string]any `json:"output_contract,omitempty"`
}

// PrintResultEvent represents a result event.
type PrintResultEvent struct {
	Type                string           `json:"type"`
	Subtype             string           `json:"subtype"`
	Timestamp           int64            `json:"timestamp"`
	Version             string           `json:"version"`
	SessionID           string           `json:"session_id"`
	IsError             bool             `json:"is_error"`
	DurationMs          int64            `json:"duration_ms"`
	DurationApiMs       int64            `json:"duration_api_ms"`
	NumTurns            int              `json:"num_turns"`
	Result              string           `json:"result"`
	StopReason          *string          `json:"stop_reason,omitempty"`
	WallDeadlineSeconds int              `json:"wall_deadline_seconds,omitempty"`
	TotalCostUSD        float64          `json:"total_cost_usd"`
	Usage               PrintResultUsage `json:"usage"`
	// ToolCallCount is the total number of tool_use blocks the assistant
	// emitted across the whole run. ToolCallsByName breaks that total down per
	// tool name. Both are always populated (0 / empty object when no tools were
	// called) so the analysis pipeline can read tool usage directly from the
	// result event instead of walking the stream.
	ToolCallCount     int                             `json:"tool_call_count"`
	ToolCallsByName   map[string]int                  `json:"tool_calls_by_name"`
	ModelUsage        map[string]PrintModelUsageEntry `json:"model_usage"`
	PermissionDenials []any                           `json:"permission_denials"`
	FastModeState     string                          `json:"fast_mode_state"`
	UUID              string                          `json:"uuid"`
}

// PrintResultUsage represents usage stats in result event.
type PrintResultUsage struct {
	InputTokens              int                `json:"input_tokens"`
	CacheCreationInputTokens int                `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int                `json:"cache_read_input_tokens"`
	OutputTokens             int                `json:"output_tokens"`
	ServerToolUse            PrintServerToolUse `json:"server_tool_use"`
	ServiceTier              string             `json:"service_tier"`
	CacheCreation            PrintCacheCreation `json:"cache_creation"`
	InferenceGeo             string             `json:"inference_geo"`
	Iterations               []any              `json:"iterations"`
	Speed                    string             `json:"speed"`
}

// PrintServerToolUse represents server tool use.
type PrintServerToolUse struct {
	Total    int            `json:"total"`
	Commands map[string]int `json:"commands"`
}

// PrintModelUsageEntry represents per-model usage.
type PrintModelUsageEntry struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// PrintHookStartedEvent represents a hook started event.
type PrintHookStartedEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	HookID    string `json:"hook_id"`
	HookName  string `json:"hook_name"`
	HookEvent string `json:"hook_event"`
	UUID      string `json:"uuid"`
	SessionID string `json:"session_id"`
}

// PrintHookProgressEvent represents a hook progress event.
type PrintHookProgressEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	HookID    string `json:"hook_id"`
	HookName  string `json:"hook_name"`
	HookEvent string `json:"hook_event"`
	Status    string `json:"status"`
	Chunk     string `json:"chunk"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Output    string `json:"output"`
	UUID      string `json:"uuid"`
	SessionID string `json:"session_id"`
}

// PrintHookResponseEvent represents a hook response event.
type PrintHookResponseEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	HookID    string `json:"hook_id"`
	HookName  string `json:"hook_name"`
	HookEvent string `json:"hook_event"`
	Status    string `json:"status"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
	Outcome   string `json:"outcome"`
	UUID      string `json:"uuid"`
	SessionID string `json:"session_id"`
}

// NewEventUUID generates a new event UUID.
func NewEventUUID() string {
	return fmt.Sprintf("evt_%d_%d", time.Now().UnixNano(), time.Now().Unix())
}
