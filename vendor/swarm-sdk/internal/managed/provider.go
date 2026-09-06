// Package managed provides the ManagedProvider for remote LLM execution via Swarm Cloud.
package managed

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// ManagedProvider implements provider.Provider for remote execution via Swarm Cloud.
// It routes Chat and Stream calls through the managed client to the Swarm Cloud service.
type ManagedProvider struct {
	client       *Client
	sessionID    string
	projectID    string
	model        string
	capabilities provider.Capabilities

	mu                sync.RWMutex
	sessionStarted    bool
	messagesSent      int
	totalInputTokens  int
	totalOutputTokens int

	logger *slog.Logger
}

// ManagedProviderConfig configures the managed provider.
type ManagedProviderConfig struct {
	// Client is the managed API client (required).
	Client *Client

	// SessionID is the unique session identifier (required).
	// If empty, one will be generated on first use.
	SessionID string

	// ProjectID is the project for workspace isolation (required).
	ProjectID string

	// Model is the model to use on the remote service (required).
	Model string

	// Capabilities are the capabilities of the remote service.
	// If not set, defaults are used.
	Capabilities provider.Capabilities

	// Logger for structured logging.
	Logger *slog.Logger
}

// NewManagedProvider creates a new managed provider.
func NewManagedProvider(cfg ManagedProviderConfig) (*ManagedProvider, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("client is required")
	}
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("project ID is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("model is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	// Default capabilities if not specified
	caps := cfg.Capabilities
	if caps.MaxContextWindow == 0 {
		caps.MaxContextWindow = 200000 // 200K default for managed
	}
	if caps.MaxOutputTokens == 0 {
		caps.MaxOutputTokens = 8192
	}

	// Generate session ID if not provided
	sessionID := cfg.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("session-%d", time.Now().UnixNano())
	}

	return &ManagedProvider{
		client:       cfg.Client,
		sessionID:    sessionID,
		projectID:    cfg.ProjectID,
		model:        cfg.Model,
		capabilities: caps,
		logger:       logger,
	}, nil
}

// Name returns the provider name.
func (p *ManagedProvider) Name() string {
	return "managed"
}

// Chat sends a synchronous request to the managed service.
func (p *ManagedProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.logger.Debug("managed_provider.chat_start",
		"session_id", p.sessionID,
		"message_count", len(req.Messages))

	// Ensure session is started
	if err := p.ensureSession(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure session: %w", err)
	}

	// Convert the last user message to a send message call
	// For full conversation support, we'd need a different API design
	// For now, we'll use streaming and collect the result
	return p.chatViaStream(ctx, req)
}

// chatViaStream implements Chat using Stream for simplicity.
func (p *ManagedProvider) chatViaStream(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	// Send the last user message
	if len(req.Messages) > 0 {
		lastMsg := req.Messages[len(req.Messages)-1]
		if lastMsg.Role == conversation.RoleUser {
			content := extractContent(lastMsg)
			if err := p.client.SendMessage(ctx, p.sessionID, "user", content); err != nil {
				return nil, fmt.Errorf("failed to send message: %w", err)
			}
			p.mu.Lock()
			p.messagesSent++
			p.mu.Unlock()
		}
	}

	// Stream the response and collect
	streamCh, err := p.Stream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to start stream: %w", err)
	}

	var content strings.Builder
	var thinking strings.Builder
	var usage *conversation.TokenUsage
	var toolCalls []conversation.ToolCall
	var finishReason provider.FinishReason

	for chunk := range streamCh {
		if chunk.Error != nil {
			return nil, chunk.Error
		}
		content.WriteString(chunk.Delta)
		thinking.WriteString(chunk.Thinking)
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if len(chunk.ToolCalls) > 0 {
			toolCalls = append(toolCalls, chunk.ToolCalls...)
		}
		if chunk.Done {
			finishReason = chunk.FinishReason
		}
	}

	// Build response
	resp := &provider.ChatResponse{
		Message: &conversation.Message{
			Role:      conversation.RoleAssistant,
			Content:   content.String(),
			Thinking:  thinking.String(),
			ToolCalls: toolCalls,
		},
		FinishReason:    finishReason,
		Usage:           usage,
		StreamedContent: true,
	}

	if finishReason == "" {
		resp.FinishReason = provider.FinishReasonStop
	}

	// Update stats
	if usage != nil {
		p.mu.Lock()
		p.totalInputTokens += usage.Input
		p.totalOutputTokens += usage.Output
		p.mu.Unlock()
	}

	return resp, nil
}

// Stream sends a streaming request to the managed service.
func (p *ManagedProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.logger.Debug("managed_provider.stream_start",
		"session_id", p.sessionID,
		"message_count", len(req.Messages))

	// Ensure session is started
	if err := p.ensureSession(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure session: %w", err)
	}

	// Get the SSE stream from the managed client
	eventCh, err := p.client.StreamEvents(ctx, p.sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to start event stream: %w", err)
	}

	// Convert managed events to provider stream chunks
	chunkCh := make(chan provider.StreamChunk, 100)

	go func() {
		defer close(chunkCh)

		for event := range eventCh {
			chunk := p.eventToChunk(event)
			select {
			case chunkCh <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()

	return chunkCh, nil
}

// Capabilities returns the provider capabilities.
func (p *ManagedProvider) Capabilities() provider.Capabilities {
	return p.capabilities
}

// ensureSession ensures the managed session is started.
func (p *ManagedProvider) ensureSession(ctx context.Context) error {
	p.mu.RLock()
	started := p.sessionStarted
	p.mu.RUnlock()

	if started {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if p.sessionStarted {
		return nil
	}

	// Initialize project
	if err := p.client.InitProject(ctx, p.projectID); err != nil {
		return fmt.Errorf("failed to init project: %w", err)
	}

	// Boot session
	_, err := p.client.BootSession(ctx, p.sessionID, p.projectID)
	if err != nil {
		return fmt.Errorf("failed to boot session: %w", err)
	}

	p.sessionStarted = true
	p.logger.Info("managed_provider.session_started",
		"session_id", p.sessionID,
		"project_id", p.projectID)

	return nil
}

// eventToChunk converts a managed Event to a provider StreamChunk.
func (p *ManagedProvider) eventToChunk(event Event) provider.StreamChunk {
	chunk := provider.StreamChunk{}

	switch event.Type {
	case "thinking":
		// Extended thinking content
		if content, ok := event.Data["content"].(string); ok {
			chunk.Thinking = content
		} else if event.Content != "" {
			chunk.Thinking = event.Content
		}

	case "content", "text":
		// Regular content
		if content, ok := event.Data["content"].(string); ok {
			chunk.Delta = content
		} else if event.Content != "" {
			chunk.Delta = event.Content
		}

	case "tool_call":
		// Tool call
		chunk.ToolCalls = p.parseToolCalls(event)

	case "tool_result":
		// Tool result - skip, handled elsewhere

	case "done", "complete":
		// Final chunk
		chunk.Done = true
		chunk.FinishReason = provider.FinishReasonStop
		chunk.Usage = p.parseUsage(event)

	case "error":
		// Error
		chunk.Done = true
		chunk.FinishReason = provider.FinishReasonError
		if errMsg, ok := event.Data["error"].(string); ok {
			chunk.Error = fmt.Errorf("%s", errMsg)
		} else {
			chunk.Error = fmt.Errorf("unknown error from managed service")
		}

	default:
		// Unknown event type - try to extract content
		if content, ok := event.Data["content"].(string); ok {
			chunk.Delta = content
		}
	}

	return chunk
}

// parseUsage extracts token usage from an event.
func (p *ManagedProvider) parseUsage(event Event) *conversation.TokenUsage {
	usage := &conversation.TokenUsage{}

	if input, ok := event.Data["input_tokens"].(int); ok {
		usage.Input = input
	} else if input, ok := event.Data["input_tokens"].(float64); ok {
		usage.Input = int(input)
	} else if input, ok := event.Data["input"].(int); ok {
		usage.Input = input
	}

	if output, ok := event.Data["output_tokens"].(int); ok {
		usage.Output = output
	} else if output, ok := event.Data["output_tokens"].(float64); ok {
		usage.Output = int(output)
	} else if output, ok := event.Data["output"].(int); ok {
		usage.Output = output
	}

	return usage
}

// parseToolCalls extracts tool calls from an event.
func (p *ManagedProvider) parseToolCalls(event Event) []conversation.ToolCall {
	var calls []conversation.ToolCall

	// Try single tool call
	if name, ok := event.Data["name"].(string); ok {
		call := conversation.ToolCall{
			Name: name,
		}
		if id, ok := event.Data["id"].(string); ok {
			call.ID = id
		}
		if params, ok := event.Data["parameters"].(map[string]any); ok {
			call.Parameters = params
		}
		calls = append(calls, call)
	}

	// Try array of tool calls
	if toolCalls, ok := event.Data["tool_calls"].([]any); ok {
		for _, tc := range toolCalls {
			if tcMap, ok := tc.(map[string]any); ok {
				call := conversation.ToolCall{}
				if id, ok := tcMap["id"].(string); ok {
					call.ID = id
				}
				if name, ok := tcMap["name"].(string); ok {
					call.Name = name
				}
				if params, ok := tcMap["parameters"].(map[string]any); ok {
					call.Parameters = params
				}
				calls = append(calls, call)
			}
		}
	}

	return calls
}

// SessionID returns the current session ID.
func (p *ManagedProvider) SessionID() string {
	return p.sessionID
}

// Close stops the managed session.
func (p *ManagedProvider) Close(ctx context.Context) error {
	p.mu.RLock()
	started := p.sessionStarted
	p.mu.RUnlock()

	if !started {
		return nil
	}

	if err := p.client.StopSession(ctx, p.sessionID); err != nil {
		return fmt.Errorf("failed to stop session: %w", err)
	}

	p.mu.Lock()
	p.sessionStarted = false
	p.mu.Unlock()

	p.logger.Info("managed_provider.session_stopped", "session_id", p.sessionID)
	return nil
}

// Stats returns usage statistics.
func (p *ManagedProvider) Stats() ManagedProviderStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return ManagedProviderStats{
		SessionID:     p.sessionID,
		ProjectID:     p.projectID,
		MessagesSent:  p.messagesSent,
		InputTokens:   p.totalInputTokens,
		OutputTokens:  p.totalOutputTokens,
		SessionActive: p.sessionStarted,
	}
}

// ManagedProviderStats contains usage statistics.
type ManagedProviderStats struct {
	SessionID     string
	ProjectID     string
	MessagesSent  int
	InputTokens   int
	OutputTokens  int
	SessionActive bool
}

// Helper functions

func extractContent(msg *conversation.Message) string {
	// Message.Content is a string
	return msg.Content
}
