// Package manager provides conversation management and coordination.
// This is Ring 1 - orchestrates storage, context strategies, and observability.
package manager

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// Manager coordinates conversation persistence and exports.
// Thread-safe for concurrent operations.
// NOTE: Context trimming has been disabled. Full conversation history is preserved.
type Manager struct {
	storage storage.Storage
	logger  observability.Logger
	tracer  observability.Tracer
	mu      sync.RWMutex
}

// Config provides configuration for the Manager.
type Config struct {
	Storage storage.Storage
	Logger  observability.Logger
	Tracer  observability.Tracer
}

// CreateOptions specifies options for creating a new conversation.
type CreateOptions struct {
	Mode             string
	WorkspacePath    string
	Metadata         *conversation.ConversationMetadata
	TraceID          string
	BaseSystemPrompt string // Stable system prompt for this conversation (diagnostic/audit)

	// ── Join key (PLAN.md gap G1) ────────────────────────────────────────
	//
	// These are stamped into Metadata.Custom by Create and are what lets the
	// hook/event stream be joined to the conversation store. All are optional;
	// an empty field is simply not written. None of them change the persisted
	// Conversation struct — they land as keys inside the pre-existing
	// metadata.custom bag. See internal/conversation/joinkey.go.

	// Origin classifies how the conversation was started: one of
	// conversation.OriginInteractive, OriginSubagent, OriginHeadless. Callers
	// that know their own front end should always set this — leaving it empty
	// is what produced the asymmetric tagging described in PLAN.md gap G6.
	Origin string

	// AgentID is the owning agent definition ID (agent.Definition.ID). The
	// client fills this in from its active definition when the caller leaves it
	// empty, so most callers need not set it.
	AgentID string

	// SessionID overrides the process session identity. Leave empty to use
	// conversation.ProcessSessionID(), which is what both front ends seed and
	// what the hook stream reports; set it only when the caller genuinely owns a
	// different session identity.
	SessionID string

	// ParentConversationID / ParentAgentID attribute a child run to its parent.
	// Nothing sets these today: sub-agent, delegate and background runs do not
	// currently get their own conversation file (see joinkey.go). The plumbing
	// exists so that when they do, the link is one field assignment away.
	ParentConversationID string
	ParentAgentID        string
}

// GetMessagesOptions specifies filters for retrieving messages.
type GetMessagesOptions struct {
	Role   conversation.Role
	Before *time.Time
	After  *time.Time
	Limit  int
	Offset int
}

// ExportFormat specifies the export format.
type ExportFormat string

const (
	ExportFormatJSON       ExportFormat = "json"
	ExportFormatMarkdown   ExportFormat = "markdown"
	ExportFormatHTML       ExportFormat = "html"
	ExportFormatJSONL      ExportFormat = "jsonl"
	ExportFormatClaudeCode ExportFormat = "claude"
)

// NewManager creates a new conversation manager.
// NOTE: Context trimming has been disabled. Full conversation history is preserved.
func NewManager(config Config) (*Manager, error) {
	if config.Storage == nil {
		return nil, sdkerr.Permanent("manager.invalid_config", "Storage is required")
	}

	if config.Logger == nil {
		return nil, sdkerr.Permanent("manager.invalid_config", "Logger is required")
	}

	if config.Tracer == nil {
		return nil, sdkerr.Permanent("manager.invalid_config", "Tracer is required")
	}

	return &Manager{
		storage: config.Storage,
		logger:  config.Logger,
		tracer:  config.Tracer,
	}, nil
}

// Create creates a new conversation.
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (*conversation.Conversation, error) {
	ctx, span := m.tracer.StartSpan(ctx, "manager.create")
	defer span.End()

	if opts.Mode == "" {
		m.logger.Error(ctx, "manager.create.invalid_mode",
			observability.F("error", "mode is required"))
		return nil, sdkerr.Permanent("manager.invalid_options", "Mode is required")
	}

	now := time.Now()
	metadata := conversation.ConversationMetadata{}
	if opts.Metadata != nil {
		metadata = *opts.Metadata
	}

	// Stamp the join key exactly once, here, at the single chokepoint every
	// conversation-creating path funnels through (client.NewConversation and
	// client.CreateConversationWithOptions both land on this method). Doing it
	// here rather than at each call site is what makes coverage total instead of
	// dependent on which helper the caller happened to use.
	//
	// Purely additive: StampJoinKeys writes only absent-or-empty keys into the
	// existing metadata.custom bag, so a caller that pre-seeded a value (the
	// headless path seeds origin) keeps it, and no existing key is touched.
	//
	// Cost: one map write per key, plus — on the first conversation created for
	// a given workspace in this process — a memoised read of .git/HEAD. No
	// subprocess, and nothing here runs per message.
	sessionID := opts.SessionID
	if sessionID == "" {
		sessionID = conversation.ProcessSessionID()
	}
	conversation.StampJoinKeys(&metadata, conversation.JoinKeys{
		SessionID:            sessionID,
		AgentID:              opts.AgentID,
		Origin:               opts.Origin,
		ParentConversationID: opts.ParentConversationID,
		ParentAgentID:        opts.ParentAgentID,
		GitSHA:               conversation.GitSHA(opts.WorkspacePath),
	})

	conv := &conversation.Conversation{
		ID:               generateID(),
		CreatedAt:        now,
		UpdatedAt:        now,
		Mode:             opts.Mode,
		ModeHistory:      []string{opts.Mode},
		Status:           conversation.StatusActive,
		Messages:         make([]*conversation.Message, 0),
		Metadata:         metadata,
		WorkspacePath:    opts.WorkspacePath,
		TotalTokens:      0,
		TotalCostUSD:     0,
		TraceID:          opts.TraceID,
		BaseSystemPrompt: opts.BaseSystemPrompt,
	}

	err := m.storage.Save(ctx, conv)
	if err != nil {
		m.logger.Error(ctx, "manager.create.storage_error",
			observability.F("conversation_id", conv.ID),
			observability.F("error", err.Error()))
		return nil, sdkerr.Wrap(err, "manager.storage_failed")
	}

	span.SetAttribute("conversation_id", conv.ID)
	span.SetAttribute("mode", opts.Mode)

	m.logger.Info(ctx, "manager.conversation_created",
		observability.F("conversation_id", conv.ID),
		observability.F("mode", opts.Mode))

	return conv, nil
}

// Resume resumes an existing conversation from storage.
func (m *Manager) Resume(ctx context.Context, id string) (*conversation.Conversation, error) {
	ctx, span := m.tracer.StartSpan(ctx, "manager.resume")
	defer span.End()

	conv, err := m.storage.Load(ctx, id)
	if err != nil {
		m.logger.Warn(ctx, "manager.resume.not_found",
			observability.F("conversation_id", id))
		return nil, sdkerr.Permanent("manager.conversation_not_found",
			fmt.Sprintf("conversation %s not found", id))
	}

	span.SetAttribute("conversation_id", id)
	span.SetAttribute("message_count", len(conv.Messages))

	return conv, nil
}

// AddMessage adds a message to a conversation.
func (m *Manager) AddMessage(ctx context.Context, convID string, msg *conversation.Message) error {
	ctx, span := m.tracer.StartSpan(ctx, "manager.add_message")
	defer span.End()

	m.mu.Lock()
	defer m.mu.Unlock()

	conv, err := m.storage.Load(ctx, convID)
	if err != nil {
		m.logger.Error(ctx, "manager.add_message.load_failed",
			observability.F("conversation_id", convID),
			observability.F("error", err.Error()))
		return sdkerr.Permanent("manager.conversation_not_found",
			fmt.Sprintf("conversation %s not found", convID))
	}

	conv.AddMessage(msg)

	// NOTE: Context trimming has been disabled. Full conversation history is preserved.
	// The provider will handle context limits and the agent will use compaction when needed.

	err = m.storage.Save(ctx, conv)
	if err != nil {
		m.logger.Error(ctx, "manager.add_message.save_failed",
			observability.F("conversation_id", convID),
			observability.F("error", err.Error()))
		return sdkerr.Wrap(err, "manager.storage_failed")
	}

	span.SetAttribute("conversation_id", convID)
	span.SetAttribute("message_role", string(msg.Role))
	span.SetAttribute("total_tokens", conv.TotalTokens)

	// Enhanced logging for debugging tool call issues
	toolCallIDs := make([]string, 0)
	for _, tc := range msg.ToolCalls {
		toolCallIDs = append(toolCallIDs, tc.ID)
	}
	toolResultIDs := make([]string, 0)
	for _, tr := range msg.ToolResults {
		toolResultIDs = append(toolResultIDs, tr.CallID)
	}

	m.logger.Info(ctx, "manager.message_added",
		observability.F("conversation_id", convID),
		observability.F("message_id", msg.ID),
		observability.F("role", string(msg.Role)),
		observability.F("content_length", len(msg.Content)),
		observability.F("tool_calls_count", len(msg.ToolCalls)),
		observability.F("tool_call_ids", toolCallIDs),
		observability.F("tool_results_count", len(msg.ToolResults)),
		observability.F("tool_result_ids", toolResultIDs),
		observability.F("total_messages", len(conv.Messages)),
	)

	return nil
}

// Save saves a conversation to storage.
// This is useful for updating conversation metadata (like CurrentContextSize) without adding a message.
func (m *Manager) Save(ctx context.Context, conv *conversation.Conversation) error {
	ctx, span := m.tracer.StartSpan(ctx, "manager.save")
	defer span.End()

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.storage.Save(ctx, conv); err != nil {
		m.logger.Error(ctx, "manager.save.failed",
			observability.F("conversation_id", conv.ID),
			observability.F("error", err.Error()))
		return sdkerr.Wrap(err, "manager.storage_failed")
	}

	m.logger.Debug(ctx, "manager.save.completed",
		observability.F("conversation_id", conv.ID),
		observability.F("current_context_size", conv.CurrentContextSize))

	return nil
}

// GetMessages retrieves messages from a conversation with optional filtering.
func (m *Manager) GetMessages(ctx context.Context, convID string, opts GetMessagesOptions) ([]*conversation.Message, error) {
	ctx, span := m.tracer.StartSpan(ctx, "manager.get_messages")
	defer span.End()

	conv, err := m.storage.Load(ctx, convID)
	if err != nil {
		return nil, sdkerr.Permanent("manager.conversation_not_found",
			fmt.Sprintf("conversation %s not found", convID))
	}

	historySummary := conversation.SummarizeMessages(conv.Messages)
	m.logger.Info(ctx, "manager.get_messages.loaded",
		observability.F("conversation_id", convID),
		observability.F("raw_message_count", historySummary.MessageCount),
		observability.F("nil_message_count", historySummary.NilCount),
		observability.F("role_histogram", historySummary.Roles),
		observability.F("tool_call_count", historySummary.ToolCalls),
		observability.F("tool_result_count", historySummary.ToolResults),
		observability.F("message_id_sequence_hash", historySummary.IDSequenceHash),
	)

	messages := make([]*conversation.Message, 0)

	for _, msg := range conv.Messages {
		if opts.Role != "" && msg.Role != opts.Role {
			continue
		}

		if opts.Before != nil && msg.Timestamp.After(*opts.Before) {
			continue
		}

		if opts.After != nil && msg.Timestamp.Before(*opts.After) {
			continue
		}

		messages = append(messages, msg)
	}

	if opts.Offset > 0 {
		if opts.Offset >= len(messages) {
			messages = make([]*conversation.Message, 0)
		} else {
			messages = messages[opts.Offset:]
		}
	}

	if opts.Limit > 0 && len(messages) > opts.Limit {
		messages = messages[:opts.Limit]
	}

	span.SetAttribute("conversation_id", convID)
	span.SetAttribute("message_count", len(messages))

	m.logger.Info(ctx, "manager.get_messages.filtered",
		observability.F("conversation_id", convID),
		observability.F("filtered_message_count", len(messages)),
	)

	return messages, nil
}

// Complete marks a conversation as completed.
func (m *Manager) Complete(ctx context.Context, convID string) error {
	return m.setStatus(ctx, convID, conversation.StatusCompleted)
}

// Archive marks a conversation as archived.
func (m *Manager) Archive(ctx context.Context, convID string) error {
	return m.setStatus(ctx, convID, conversation.StatusArchived)
}

// Delete removes a conversation from storage.
func (m *Manager) Delete(ctx context.Context, convID string) error {
	ctx, span := m.tracer.StartSpan(ctx, "manager.delete")
	defer span.End()

	m.mu.Lock()
	defer m.mu.Unlock()

	err := m.storage.Delete(ctx, convID)
	if err != nil {
		return sdkerr.Wrap(err, "manager.storage_failed")
	}

	span.SetAttribute("conversation_id", convID)

	m.logger.Info(ctx, "manager.conversation_deleted",
		observability.F("conversation_id", convID))

	return nil
}

// Export exports a conversation in the specified format.
func (m *Manager) Export(ctx context.Context, convID string, format ExportFormat) ([]byte, error) {
	ctx, span := m.tracer.StartSpan(ctx, "manager.export")
	defer span.End()

	conv, err := m.storage.Load(ctx, convID)
	if err != nil {
		return nil, sdkerr.Permanent("manager.conversation_not_found",
			fmt.Sprintf("conversation %s not found", convID))
	}

	span.SetAttribute("conversation_id", convID)
	span.SetAttribute("format", string(format))

	switch format {
	case ExportFormatJSON:
		return ExportToJSON(conv, true)
	case ExportFormatMarkdown:
		return ExportToMarkdown(conv)
	case ExportFormatHTML:
		return ExportToHTML(conv)
	case ExportFormatJSONL:
		return ExportToJSONL(conv)
	case ExportFormatClaudeCode:
		return ExportToClaudeCode(conv)
	default:
		return nil, sdkerr.Permanent("manager.invalid_format",
			fmt.Sprintf("unsupported export format: %s", format))
	}
}

// Import imports a conversation from exported data.
func (m *Manager) Import(ctx context.Context, data []byte, format ExportFormat) (*conversation.Conversation, error) {
	ctx, span := m.tracer.StartSpan(ctx, "manager.import")
	defer span.End()

	var conv *conversation.Conversation
	var err error

	switch format {
	case ExportFormatJSON:
		conv, err = ImportFromJSON(data)
	case ExportFormatJSONL:
		conv, err = ImportFromJSONL(data)
	case ExportFormatClaudeCode:
		conv, err = ImportFromClaudeCode(data)
	default:
		return nil, sdkerr.Permanent("manager.invalid_format",
			fmt.Sprintf("unsupported import format: %s", format))
	}

	if err != nil {
		m.logger.Error(ctx, "manager.import_failed",
			observability.F("format", string(format)),
			observability.F("error", err.Error()))
		return nil, err
	}

	err = m.storage.Save(ctx, conv)
	if err != nil {
		m.logger.Error(ctx, "manager.import.save_failed",
			observability.F("conversation_id", conv.ID),
			observability.F("error", err.Error()))
		return nil, sdkerr.Wrap(err, "manager.storage_failed")
	}

	span.SetAttribute("conversation_id", conv.ID)

	return conv, nil
}

// Helper methods

func (m *Manager) setStatus(ctx context.Context, convID string, status conversation.Status) error {
	ctx, span := m.tracer.StartSpan(ctx, "manager.set_status")
	defer span.End()

	conv, err := m.storage.Load(ctx, convID)
	if err != nil {
		return sdkerr.Permanent("manager.conversation_not_found",
			fmt.Sprintf("conversation %s not found", convID))
	}

	conv.Status = status
	conv.UpdatedAt = time.Now()

	err = m.storage.Save(ctx, conv)
	if err != nil {
		return sdkerr.Wrap(err, "manager.storage_failed")
	}

	span.SetAttribute("conversation_id", convID)
	span.SetAttribute("status", string(status))

	return nil
}

// generateID generates a unique, human-readable, time-sortable conversation ID.
// Format: YYYYMMDD-HHMMSS-XXXXXX where X is a random alphanumeric suffix.
// Example: 20260303-142317-a8f2k1
//
// The time prefix makes IDs naturally sortable by creation time.
// The random suffix provides collision resistance.
func generateID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	suffix := make([]byte, 6)
	for i := range suffix {
		suffix[i] = alphabet[rand.Intn(len(alphabet))]
	}
	return time.Now().Format("20060102-150405") + "-" + string(suffix)
}
