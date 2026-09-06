package conversation

import (
	"reflect"
	"sort"
	"time"
)

// Message represents a single message in a conversation.
// This is the canonical message format that all providers translate to/from.
type Message struct {
	// ID is the unique identifier for this message.
	ID string `json:"id"`

	// Timestamp when the message was created (RFC3339 format).
	Timestamp time.Time `json:"timestamp"`

	// Role identifies who created this message.
	Role Role `json:"role"`

	// Content is the text content of the message.
	Content string `json:"content"`

	// AgentID identifies which agent created this message (if assistant role).
	AgentID string `json:"agent_id,omitempty"`

	// GroupID identifies which agent group this message belongs to.
	GroupID string `json:"group_id,omitempty"`

	// ModeID identifies which mode was active when this message was created.
	ModeID string `json:"mode_id,omitempty"`

	// Provider identifies the LLM provider used (if assistant role).
	Provider string `json:"provider,omitempty"`

	// Model identifies the LLM model used (if assistant role).
	Model string `json:"model,omitempty"`

	// Tokens tracks token usage for this message.
	Tokens *TokenUsage `json:"tokens,omitempty"`

	// ToolCalls contains tool calls requested by the assistant.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// ToolResults contains results from tool executions.
	ToolResults []ToolResult `json:"tool_results,omitempty"`

	// Thinking contains extended thinking content (e.g., from Anthropic's extended thinking feature).
	Thinking string `json:"thinking,omitempty"`

	// EditHistory tracks all edits made to this message.
	EditHistory []Edit `json:"edit_history,omitempty"`

	// PromptIDs is the append-only ledger of prompt identifiers minted
	// for this message's content over its lifetime. Index 0 is the id
	// minted at first persist; the last entry is the *canonical* id used
	// when tagging newly-created tasks. Edits append; they never mutate.
	//
	// This is the audit-trail anchor: every Task.OriginPromptID resolves
	// to exactly one entry here (or to a PromptArchiveEntry in
	// CompactionState.PromptArchive after the message is compacted).
	//
	// Populated only on user-role messages; assistant/tool messages leave
	// it empty.
	PromptIDs []string `json:"prompt_ids,omitempty"`

	// Metadata contains additional custom fields.
	Metadata map[string]any `json:"metadata,omitempty"`
	// A2A contains A2A-native metadata when this message was projected from
	// another top-level agent session.
	A2A *A2AMetadata `json:"a2a,omitempty"`
	// OrderedBlocks tracks the sequence of content, thinking, tool calls, results, and sub-agent activity
	// in the order they were received/streamed. This preserves the full execution trace.
	// Used for multi-level rendering and streaming reconstruction.
	OrderedBlocks []MessageBlock `json:"ordered_blocks,omitempty"`
	// SubAgentActivity contains aggregated sub-agent execution information for display.
	// This is populated when the message contains sub-agent tool delegations.
	SubAgentActivity []*SubAgentActivity `json:"sub_agent_activity,omitempty"`
}

// GetOrderedBlocks returns message blocks in correct display order.
// This is the CONTRACTUAL way to access blocks - they are guaranteed to be sorted by sequence number.
//
// CONTRACT: Agents MUST use this method to access blocks for display.
// Direct access to OrderedBlocks may show blocks out of order.
//
// Example:
//
//	blocks := msg.GetOrderedBlocks()
//	for _, block := range blocks {
//	    // Blocks are guaranteed to be in correct order
//	}
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

// TokenUsage tracks token consumption for a message.
type TokenUsage struct {
	// Input is the number of input tokens.
	// This is the raw input_tokens field from the provider — it does NOT include
	// cache tokens. Use InputContextSize() to get the full input context size.
	Input int `json:"input"`

	// Output is the number of output tokens.
	Output int `json:"output"`

	// Total is the canonical sum: Input + CacheCreation + CacheRead + Output.
	// Matches the qp6() formula used by Claude Code for compaction threshold decisions.
	Total int `json:"total"`

	// CacheCreation is the number of tokens written to the prompt cache this turn.
	// Only populated for providers that support prompt caching (Anthropic).
	CacheCreation int `json:"cache_creation,omitempty"`

	// CacheRead is the number of tokens read from the prompt cache this turn.
	// Only populated for providers that support prompt caching (Anthropic).
	CacheRead int `json:"cache_read,omitempty"`
	// CacheCreation5m and CacheCreation1h split CacheCreation by the TTL that
	// was actually written, mirroring Anthropic's usage.cache_creation object
	// (ephemeral_5m_input_tokens / ephemeral_1h_input_tokens).
	//
	// These live on TokenUsage rather than on message metadata deliberately.
	// The provider also publishes the same numbers under
	// metadata["cache_metrics"], but that map does not survive persistence: a
	// scan of 400 recent conversations found 6,386 messages carrying cache
	// tokens and zero carrying metadata.cache_metrics, while
	// tokens.cache_creation persists reliably. Cache-break attribution needs to
	// know which TTL was in force (see usageindex/detect.go), so the value has
	// to travel on the channel that actually reaches disk.
	//
	// Both are zero for providers without prompt caching, and may be zero for
	// Anthropic responses that omit the breakdown. Callers must treat "no
	// breakdown" as unknown rather than assuming 5m.
	CacheCreation5m int `json:"cache_creation_5m,omitempty"`
	CacheCreation1h int `json:"cache_creation_1h,omitempty"`
}

// InputContextSize returns the full input-side context size: Input + CacheCreation + CacheRead.
// This is what should be compared against the context window limit for compaction decisions.
// For providers without caching, this equals Input.
func (u *TokenUsage) InputContextSize() int {
	if u == nil {
		return 0
	}
	return u.Input + u.CacheCreation + u.CacheRead
}

// FullContextSize returns the canonical total: Input + CacheCreation + CacheRead + Output.
// Equivalent to Claude Code's qp6() function. Use for compaction threshold comparisons
// when the output budget must also be counted.
func (u *TokenUsage) FullContextSize() int {
	if u == nil {
		return 0
	}
	return u.Input + u.CacheCreation + u.CacheRead + u.Output
}

// ToolCall represents a request to execute a tool.
type ToolCall struct {
	// ID is the unique identifier for this tool call.
	ID string `json:"id"`

	// Name is the name of the tool to execute.
	Name string `json:"name"`

	// Parameters are the arguments to pass to the tool.
	Parameters map[string]any `json:"parameters"`

	// ThoughtSignature is used by Gemini thinking models to maintain reasoning context.
	// When present, it must be preserved and sent back with conversation history.
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	// CallID links this result to the corresponding ToolCall.
	CallID string `json:"call_id"`

	// Name is the name of the tool that was executed.
	// This is needed for providers like Gemini that require
	// the function name in the response.
	Name string `json:"name,omitempty"`

	// Output is the result returned by the tool.
	Output string `json:"output"`

	// Content contains rich content blocks (images, audio, PDF) returned by the tool.
	// This allows tools to return multimodal content that the LLM can process.
	Content []ContentBlock `json:"content,omitempty"`

	// Error contains error information if the tool execution failed.
	Error *ToolError `json:"error,omitempty"`
}

// ContentBlock represents a rich content block in a tool result.
// This mirrors tools.ContentBlock but is defined here to avoid import cycles.
type ContentBlock struct {
	// Type indicates the content type (text, image, etc.)
	Type string `json:"type"`

	// For text content
	Text string `json:"text,omitempty"`

	// For binary content (images, audio, PDF, video)
	Data     []byte `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`

	// For resource links
	URI         string `json:"uri,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Size        *int64 `json:"size,omitempty"`

	// Annotations for provider-specific metadata
	Annotations map[string]any `json:"annotations,omitempty"`
}

// ToolError represents an error from tool execution.
type ToolError struct {
	// Type is the error type (e.g., "tool.permission_denied").
	Type string `json:"type"`

	// Message is the error message.
	Message string `json:"message"`
}

// Reference is a structured reference attached to peer messages.
type Reference struct {
	Type     string         `json:"type"`
	Value    string         `json:"value"`
	Label    string         `json:"label,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

const (
	// A2APersistedMetadataKey marks A2A-projected peer messages that were already persisted
	// before they were injected into the live execute loop.
	A2APersistedMetadataKey = "a2a_persisted"

	// CompactionGeneratedMetadataKey marks provider-visible handoff messages
	// generated by compaction. They remain in the durable active generation but
	// are not rendered as user-authored turns by interactive clients.
	CompactionGeneratedMetadataKey = "compaction_generated"
)

// A2AMetadata stores projection context for A2A-authored peer traffic.
type A2AMetadata struct {
	RemoteAgentHandle  string      `json:"remote_agent_handle,omitempty"`
	RemoteAgentSession string      `json:"remote_agent_session,omitempty"`
	RemoteAgentStatus  string      `json:"remote_agent_status,omitempty"` // Status of the sender (idle, working, busy, away)
	RemoteAgentTask    string      `json:"remote_agent_task,omitempty"`   // Current task of the sender
	EndpointURL        string      `json:"endpoint_url,omitempty"`
	CardURL            string      `json:"card_url,omitempty"`
	MessageID          string      `json:"message_id,omitempty"`
	ContextID          string      `json:"context_id,omitempty"`
	TaskID             string      `json:"task_id,omitempty"`
	ReferenceTaskIDs   []string    `json:"reference_task_ids,omitempty"`
	TaskState          string      `json:"task_state,omitempty"`
	ArtifactID         string      `json:"artifact_id,omitempty"`
	ArtifactName       string      `json:"artifact_name,omitempty"`
	ArtifactAppend     bool        `json:"artifact_append,omitempty"`
	ArtifactLastChunk  bool        `json:"artifact_last_chunk,omitempty"`
	StreamingEventType string      `json:"streaming_event_type,omitempty"`
	References         []Reference `json:"references,omitempty"`
}

// Edit represents a single edit to a message.
type Edit struct {
	// Timestamp when the edit occurred.
	Timestamp time.Time `json:"timestamp"`

	// Editor identifies who made the edit (user, agent, steering, etc.).
	Editor string `json:"editor"`

	// PreviousContent is the content before this edit.
	PreviousContent string `json:"previous_content"`

	// Reason explains why the edit was made.
	Reason string `json:"reason,omitempty"`

	// PreviousPromptID is the prompt id that was canonical before this
	// edit. For user-role message edits this should equal whatever was at
	// the tail of Message.PromptIDs at the moment AddEdit was called.
	// Empty on edits to non-user messages.
	PreviousPromptID string `json:"previous_prompt_id,omitempty"`

	// NewPromptID is the prompt id minted from the post-edit content.
	// This MUST match the value appended to Message.PromptIDs by the
	// edit operation; the duplication is intentional so an audit
	// reviewer can correlate Edit entries with PromptIDs entries
	// without joining on position.
	NewPromptID string `json:"new_prompt_id,omitempty"`
}

// Clone creates a deep copy of the message.
func (m *Message) Clone() *Message {
	clone := &Message{
		ID:        m.ID,
		Timestamp: m.Timestamp,
		Role:      m.Role,
		Content:   m.Content,
		AgentID:   m.AgentID,
		GroupID:   m.GroupID,
		ModeID:    m.ModeID,
		Provider:  m.Provider,
		Model:     m.Model,
		Thinking:  m.Thinking,
	}

	if m.A2A != nil {
		clone.A2A = &A2AMetadata{
			RemoteAgentHandle:  m.A2A.RemoteAgentHandle,
			RemoteAgentSession: m.A2A.RemoteAgentSession,
			RemoteAgentStatus:  m.A2A.RemoteAgentStatus,
			RemoteAgentTask:    m.A2A.RemoteAgentTask,
			EndpointURL:        m.A2A.EndpointURL,
			CardURL:            m.A2A.CardURL,
			MessageID:          m.A2A.MessageID,
			ContextID:          m.A2A.ContextID,
			TaskID:             m.A2A.TaskID,
			TaskState:          m.A2A.TaskState,
			ArtifactID:         m.A2A.ArtifactID,
			ArtifactName:       m.A2A.ArtifactName,
			ArtifactAppend:     m.A2A.ArtifactAppend,
			ArtifactLastChunk:  m.A2A.ArtifactLastChunk,
			StreamingEventType: m.A2A.StreamingEventType,
		}
		if len(m.A2A.ReferenceTaskIDs) > 0 {
			clone.A2A.ReferenceTaskIDs = append([]string(nil), m.A2A.ReferenceTaskIDs...)
		}
		if len(m.A2A.References) > 0 {
			clone.A2A.References = make([]Reference, len(m.A2A.References))
			for i, reference := range m.A2A.References {
				clone.A2A.References[i] = reference
				clone.A2A.References[i].Metadata = cloneMessageMap(reference.Metadata)
			}
		}
	}

	if m.Tokens != nil {
		clone.Tokens = &TokenUsage{
			Input:           m.Tokens.Input,
			Output:          m.Tokens.Output,
			Total:           m.Tokens.Total,
			CacheCreation:   m.Tokens.CacheCreation,
			CacheRead:       m.Tokens.CacheRead,
			CacheCreation5m: m.Tokens.CacheCreation5m,
			CacheCreation1h: m.Tokens.CacheCreation1h,
		}
	}

	if len(m.ToolCalls) > 0 {
		clone.ToolCalls = make([]ToolCall, len(m.ToolCalls))
		for i, toolCall := range m.ToolCalls {
			clone.ToolCalls[i] = cloneToolCall(toolCall)
		}
	}

	if len(m.ToolResults) > 0 {
		clone.ToolResults = make([]ToolResult, len(m.ToolResults))
		for i, tr := range m.ToolResults {
			clone.ToolResults[i] = cloneToolResult(tr)
		}
	}

	if len(m.EditHistory) > 0 {
		clone.EditHistory = make([]Edit, len(m.EditHistory))
		copy(clone.EditHistory, m.EditHistory)
	}

	if len(m.PromptIDs) > 0 {
		clone.PromptIDs = make([]string, len(m.PromptIDs))
		copy(clone.PromptIDs, m.PromptIDs)
	}

	if m.Metadata != nil {
		clone.Metadata = cloneMessageMap(m.Metadata)
	}

	if len(m.OrderedBlocks) > 0 {
		clone.OrderedBlocks = cloneMessageBlocks(m.OrderedBlocks)
	}

	if len(m.SubAgentActivity) > 0 {
		clone.SubAgentActivity = make([]*SubAgentActivity, len(m.SubAgentActivity))
		for i, activity := range m.SubAgentActivity {
			clone.SubAgentActivity[i] = cloneSubAgentActivity(activity)
		}
	}

	return clone
}

func cloneToolCall(toolCall ToolCall) ToolCall {
	cloned := toolCall
	cloned.Parameters = cloneMessageMap(toolCall.Parameters)
	return cloned
}

func cloneToolResult(toolResult ToolResult) ToolResult {
	cloned := toolResult
	if toolResult.Error != nil {
		errCopy := *toolResult.Error
		cloned.Error = &errCopy
	}
	if len(toolResult.Content) > 0 {
		cloned.Content = make([]ContentBlock, len(toolResult.Content))
		for i, block := range toolResult.Content {
			cloned.Content[i] = block
			cloned.Content[i].Data = append([]byte(nil), block.Data...)
			cloned.Content[i].Annotations = cloneMessageMap(block.Annotations)
			if block.Size != nil {
				size := *block.Size
				cloned.Content[i].Size = &size
			}
		}
	}
	return cloned
}

func cloneMessageBlocks(blocks []MessageBlock) []MessageBlock {
	if blocks == nil {
		return nil
	}
	cloned := make([]MessageBlock, len(blocks))
	for i, block := range blocks {
		cloned[i] = block
		if block.ToolCall != nil {
			toolCall := cloneToolCall(*block.ToolCall)
			cloned[i].ToolCall = &toolCall
		}
		if block.ToolResult != nil {
			toolResult := cloneToolResult(*block.ToolResult)
			cloned[i].ToolResult = &toolResult
		}
		if block.SubAgentActivity != nil {
			cloned[i].SubAgentActivity = cloneSubAgentActivity(block.SubAgentActivity)
		}
	}
	return cloned
}

func cloneSubAgentActivity(activity *SubAgentActivity) *SubAgentActivity {
	if activity == nil {
		return nil
	}
	cloned := *activity
	cloned.Blocks = cloneMessageBlocks(activity.Blocks)
	return &cloned
}

func cloneMessageMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	return cloneMessageReflect(reflect.ValueOf(source), make(map[cloneVisit]reflect.Value)).Interface().(map[string]any)
}

func cloneMessageValue(value any) any {
	cloned := cloneMessageReflect(reflect.ValueOf(value), make(map[cloneVisit]reflect.Value))
	if !cloned.IsValid() {
		return nil
	}
	return cloned.Interface()
}

type cloneVisit struct {
	kind reflect.Kind
	typ  reflect.Type
	ptr  uintptr
}

func cloneMessageReflect(value reflect.Value, visited map[cloneVisit]reflect.Value) reflect.Value {
	if !value.IsValid() {
		return reflect.Value{}
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		clonedValue := cloneMessageReflect(value.Elem(), visited)
		cloned := reflect.New(value.Type()).Elem()
		cloned.Set(clonedValue)
		return cloned
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		visit := cloneVisit{kind: value.Kind(), typ: value.Type(), ptr: value.Pointer()}
		if cloned, ok := visited[visit]; ok {
			return cloned
		}
		cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
		visited[visit] = cloned
		iter := value.MapRange()
		for iter.Next() {
			cloned.SetMapIndex(
				cloneMessageReflect(iter.Key(), visited),
				cloneMessageReflect(iter.Value(), visited),
			)
		}
		return cloned
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		visit := cloneVisit{kind: value.Kind(), typ: value.Type(), ptr: value.Pointer()}
		if cloned, ok := visited[visit]; ok {
			return cloned
		}
		cloned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		visited[visit] = cloned
		for i := 0; i < value.Len(); i++ {
			cloned.Index(i).Set(cloneMessageReflect(value.Index(i), visited))
		}
		return cloned
	case reflect.Array:
		cloned := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			cloned.Index(i).Set(cloneMessageReflect(value.Index(i), visited))
		}
		return cloned
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		visit := cloneVisit{kind: value.Kind(), typ: value.Type(), ptr: value.Pointer()}
		if cloned, ok := visited[visit]; ok {
			return cloned
		}
		cloned := reflect.New(value.Type().Elem())
		visited[visit] = cloned
		cloned.Elem().Set(cloneMessageReflect(value.Elem(), visited))
		return cloned
	case reflect.Struct:
		cloned := reflect.New(value.Type()).Elem()
		cloned.Set(value)
		for i := 0; i < value.NumField(); i++ {
			field := cloned.Field(i)
			if field.CanSet() && value.Type().Field(i).PkgPath == "" {
				field.Set(cloneMessageReflect(value.Field(i), visited))
			}
		}
		return cloned
	default:
		return value
	}
}

// AddEdit records an edit to the message.
func (m *Message) AddEdit(editor, reason string) {
	m.EditHistory = append(m.EditHistory, Edit{
		Timestamp:       time.Now(),
		Editor:          editor,
		PreviousContent: m.Content,
		Reason:          reason,
	})
}

// AddEditWithPromptID records an edit to the message AND links the edit
// to the prompt-id transition. Unlike AddEdit, the caller is required to
// pass the previous content explicitly because the typical call sequence
// is "swap m.Content, mint a new prompt id, then record the edit" — by
// the time this method runs, m.Content already holds the post-edit text.
//
// previousContent is what m.Content held before the swap.
// newPromptID should be minted by the caller off the post-edit content
// (see swarm-sdk/tasks.MintPromptID).
//
// This helper preserves the audit chain by:
//   - snapshotting previousContent into Edit.PreviousContent,
//   - stamping Edit.PreviousPromptID with the current canonical id (the
//     tail of m.PromptIDs, if any),
//   - stamping Edit.NewPromptID with newPromptID,
//   - appending newPromptID to m.PromptIDs (append-only).
//
// When newPromptID equals the current canonical id (e.g. canonicalisation
// produced the same hash, or the caller passed the existing id), the
// PromptIDs ledger is left untouched but the Edit is still recorded.
func (m *Message) AddEditWithPromptID(editor, reason, previousContent, newPromptID string) {
	prev := ""
	if n := len(m.PromptIDs); n > 0 {
		prev = m.PromptIDs[n-1]
	}

	m.EditHistory = append(m.EditHistory, Edit{
		Timestamp:        time.Now(),
		Editor:           editor,
		PreviousContent:  previousContent,
		Reason:           reason,
		PreviousPromptID: prev,
		NewPromptID:      newPromptID,
	})

	if newPromptID != "" && newPromptID != prev {
		m.PromptIDs = append(m.PromptIDs, newPromptID)
	}
}

// CanonicalPromptID returns the prompt id currently in effect for this
// message — the last entry in PromptIDs, or "" if the ledger is empty
// (the message was never minted, e.g. assistant or tool messages).
func (m *Message) CanonicalPromptID() string {
	if n := len(m.PromptIDs); n > 0 {
		return m.PromptIDs[n-1]
	}
	return ""
}

// Edited reports whether the message has been modified after creation.
func (m *Message) Edited() bool {
	return len(m.EditHistory) > 0
} // BlockType identifies the kind of content in a MessageBlock.
type BlockType string

const (
	BlockTypeContent          BlockType = "content"
	BlockTypeToolCall         BlockType = "tool_call"
	BlockTypeToolResult       BlockType = "tool_result"
	BlockTypeThinking         BlockType = "thinking"
	BlockTypeHookExecution    BlockType = "hook_execution"
	BlockTypeSubAgentActivity BlockType = "sub_agent_activity"
)

// MessageBlock represents a single block within a message.
type MessageBlock struct {
	Type             BlockType         `json:"type"`
	Content          string            `json:"content,omitempty"`
	ToolCall         *ToolCall         `json:"tool_call,omitempty"`
	ToolResult       *ToolResult       `json:"tool_result,omitempty"`
	SubAgentActivity *SubAgentActivity `json:"sub_agent_activity,omitempty"`
	Sequence         int               `json:"sequence,omitempty"`
}

// SubAgentActivity represents execution information from a delegated sub-agent task
// This captures the full lifecycle of sub-agent execution including task, tools used, and outputs.
type SubAgentActivity struct {
	AgentID         string         `json:"agent_id"`
	AgentName       string         `json:"agent_name"`
	TaskInstruction string         `json:"task_instruction"`
	Blocks          []MessageBlock `json:"blocks,omitempty"`
	StartTime       time.Time      `json:"start_time"`
	EndTime         time.Time      `json:"end_time"`
	Status          string         `json:"status,omitempty"` // "running", "complete", "error"
	Error           string         `json:"error,omitempty"`
}
