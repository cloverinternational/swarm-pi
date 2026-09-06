// Package cursor implements the Cursor (Anysphere) LLM provider.
//
// Cursor's backend at api2.cursor.sh speaks Connect-RPC (NOT OpenAI-compatible
// /v1/chat/completions). The chat streaming endpoint is
// /aiserver.v1.ChatService/StreamUnifiedChatWithToolsSSE which uses Connect-RPC
// envelope framing for the request and SSE-style streaming for the response.
//
// This provider translates the SDK's canonical ChatRequest to Cursor's
// StreamUnifiedChatRequest and parses the streaming Connect-RPC response back
// into StreamChunks.
package cursor

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

const (
	// cursorAPIBaseURLDefault is the Cursor control-plane base URL.
	cursorAPIBaseURLDefault = "https://api2.cursor.sh"

	// cursorClientVersion mirrors the version string sent by the official
	// cursor-agent CLI. The backend uses this to gate features and reject
	// outdated clients.
	cursorClientVersion = "cli-2026.06.26-7079533"
	cursorClientType    = "cursor-agent-cli"

	// cursorChatService is the Connect-RPC service name for chat.
	// NOTE: The old AiService/StreamChat is DEPRECATED; the new methods are
	// on ChatService.
	cursorChatService = "aiserver.v1.ChatService"

	// cursorStreamMethod is the Connect-RPC method for streaming chat.
	// StreamUnifiedChatWithToolsSSE is ServerStreaming (one request, streamed
	// response). The non-SSE variant (StreamUnifiedChatWithTools) is
	// BiDiStreaming which requires a bidirectional stream.
	cursorStreamMethod = "StreamUnifiedChatWithToolsSSE"

	// cursorModelsRPC is the control-plane method to list available models.
	cursorModelsRPC = "/aiserver.v1.AiService/AvailableModels"

	// Connect-RPC envelope flags.
	flagEnvelopeStart = 0x00
	flagEnvelopeEnd   = 0x02
)

// Config provides configuration for the Cursor provider.
type Config struct {
	// APIKey is the Cursor access token (from OAuth login or API key exchange).
	APIKey string
	// BaseURL is the API endpoint (defaults to https://api2.cursor.sh).
	BaseURL string
	// Name is the provider name (defaults to "cursor").
	Name string
	// Logger for structured logging.
	Logger observability.Logger
	// Tracer for distributed tracing.
	Tracer observability.Tracer
	// RawDebugWriter, if set, enables raw request/response logging.
	RawDebugWriter io.Writer
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	if c.APIKey == "" {
		return fmt.Errorf("cursor: APIKey (access token) is required")
	}
	return nil
}

// Provider implements provider.Provider for Cursor's Connect-RPC backend.
type Provider struct {
	apiKey  string
	baseURL string
	name    string
	logger  observability.Logger
	tracer  observability.Tracer
	client  *http.Client
}

// New creates a new Cursor provider.
func New(config Config) (*Provider, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = cursorAPIBaseURLDefault
	}
	name := config.Name
	if name == "" {
		name = "cursor"
	}
	transport := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
	}
	if config.RawDebugWriter != nil {
		transport = &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 5,
			IdleConnTimeout:     90 * time.Second,
		}
	}
	return &Provider{
		apiKey:  config.APIKey,
		baseURL: baseURL,
		name:    name,
		logger:  config.Logger,
		tracer:  config.Tracer,
		client: &http.Client{
			Transport: transport,
			Timeout:   0, // No timeout for streaming
		},
	}, nil
}

// Name implements provider.Provider.
func (p *Provider) Name() string {
	return p.name
}

// Capabilities implements provider.Provider.
func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:            true,
		FunctionCalling:      true,
		Vision:               false,
		MaxContextWindow:     300000,
		MaxOutputTokens:      16384,
		SupportsSystemPrompt: false, // Cursor assembles system prompt server-side
		SupportsTemperature:  false,
		PromptCaching:        false,
		SupportsJSON:         false,
	}
}

// Chat implements provider.Provider — synchronous (collects all chunks).
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	chunks, err := p.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	var content strings.Builder
	var toolCalls []conversation.ToolCall
	var finishReason provider.FinishReason
	var usage *conversation.TokenUsage
	for chunk := range chunks {
		if chunk.Error != nil {
			return nil, chunk.Error
		}
		content.WriteString(chunk.Delta)
		if len(chunk.ToolCalls) > 0 {
			toolCalls = append(toolCalls, chunk.ToolCalls...)
		}
		if chunk.Done {
			finishReason = chunk.FinishReason
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
		}
	}
	msg := &conversation.Message{
		Role:    conversation.RoleAssistant,
		Content: content.String(),
	}
	if len(toolCalls) > 0 {
		msg.OrderedBlocks = make([]conversation.MessageBlock, 0, len(toolCalls))
		for _, tc := range toolCalls {
			msg.OrderedBlocks = append(msg.OrderedBlocks, conversation.MessageBlock{
				Type:     conversation.BlockTypeToolCall,
				ToolCall: &tc,
			})
		}
	}
	return &provider.ChatResponse{
		Message:         msg,
		FinishReason:    finishReason,
		Usage:           usage,
		StreamedContent: true,
	}, nil
}

// Stream implements provider.Provider — streams Connect-RPC response chunks.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	// Build the Connect-RPC request body.
	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("cursor: failed to build request: %w", err)
	}

	// Wrap in Connect-RPC unary envelope (flags + 4-byte big-endian length + JSON).
	envelope := makeConnectEnvelope(body)

	// Build HTTP request.
	url := fmt.Sprintf("%s/%s/%s", p.baseURL, cursorChatService, cursorStreamMethod)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(envelope))
	if err != nil {
		return nil, fmt.Errorf("cursor: failed to create request: %w", err)
	}
	p.setHeaders(httpReq)

	// Send request.
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("cursor: request failed: %w", err)
	}

	// Check for non-200 status (non-streaming error).
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("cursor: API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Stream response chunks.
	ch := make(chan provider.StreamChunk, 32)
	go p.parseStreamResponse(resp.Body, ch)
	return ch, nil
}

// buildRequestBody translates a canonical ChatRequest to Cursor's
// StreamUnifiedChatRequest JSON format.
func (p *Provider) buildRequestBody(req provider.ChatRequest) ([]byte, error) {
	// Convert canonical messages to Cursor ConversationMessage format.
	convMsgs := make([]map[string]any, 0, len(req.Messages))
	for _, msg := range req.Messages {
		var msgType int
		switch msg.Role {
		case conversation.RoleUser:
			msgType = 2 // MESSAGE_TYPE_HUMAN
		case conversation.RoleAssistant:
			msgType = 3 // MESSAGE_TYPE_AI
		case conversation.RoleSystem:
			msgType = 1 // MESSAGE_TYPE_SYSTEM
		default:
			msgType = 2
		}
		// Extract text content from the message.
		text := extractTextContent(msg)
		if text == "" {
			continue
		}
		entry := map[string]any{
			"text": text,
			"type": msgType,
		}
		convMsgs = append(convMsgs, entry)
	}

	request := map[string]any{
		"conversation":            convMsgs,
		"conversation_id":         fmt.Sprintf("swarm-%d", time.Now().UnixNano()),
		"is_chat":                 true,
		"is_agentic":              false,
		"is_headless":             true,
		"use_unified_chat_prompt": true,
		"model_details": map[string]any{
			"model_name":        req.Model,
			"enable_ghost_mode": false,
		},
	}

	// Wrap in StreamUnifiedChatRequestWithTools (the SSE endpoint expects this
	// wrapper with a stream_unified_chat_request field).
	wrapped := map[string]any{
		"stream_unified_chat_request": request,
	}

	return json.Marshal(wrapped)
}

// setHeaders sets the required Cursor Connect-RPC headers.
func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/connect+json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("x-cursor-client-version", cursorClientVersion)
	req.Header.Set("x-cursor-client-type", cursorClientType)
	req.Header.Set("x-ghost-mode", "false")
}

// parseStreamResponse reads Connect-RPC streaming envelope frames from the
// response body and emits StreamChunks. The response is a sequence of
// 5-byte envelope headers (1 byte flags + 4 bytes big-endian length)
// followed by JSON message bodies.
func (p *Provider) parseStreamResponse(body io.ReadCloser, ch chan<- provider.StreamChunk) {
	defer close(ch)
	defer body.Close()

	var accumulated strings.Builder

	for {
		// Read 5-byte envelope header.
		header := make([]byte, 5)
		if _, err := io.ReadFull(body, header); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				// Stream ended — emit final chunk if we have content.
				if accumulated.Len() > 0 {
					ch <- provider.StreamChunk{
						Done:         true,
						FinishReason: provider.FinishReasonStop,
					}
				}
				return
			}
			ch <- provider.StreamChunk{Error: fmt.Errorf("cursor: read envelope header: %w", err)}
			return
		}

		flags := header[0]
		length := binary.BigEndian.Uint32(header[1:5])

		// Read the message body.
		msgBuf := make([]byte, length)
		if _, err := io.ReadFull(body, msgBuf); err != nil {
			ch <- provider.StreamChunk{Error: fmt.Errorf("cursor: read message body: %w", err)}
			return
		}

		// Check for end-of-stream frame.
		if flags&flagEnvelopeEnd != 0 {
			// This is the end-of-stream trailer frame. Parse as JSON for
			// any error information.
			var trailer map[string]any
			if json.Unmarshal(msgBuf, &trailer) == nil {
				if errMsg, ok := trailer["error"].(map[string]any); ok {
					if msg, ok := errMsg["message"].(string); ok {
						ch <- provider.StreamChunk{
							Error: fmt.Errorf("cursor: %s", msg),
							Done:  true,
						}
						return
					}
				}
			}
			// Normal end of stream.
			ch <- provider.StreamChunk{
				Done:         true,
				FinishReason: provider.FinishReasonStop,
			}
			return
		}

		// Parse the JSON message.
		var msg map[string]any
		if err := json.Unmarshal(msgBuf, &msg); err != nil {
			// Not JSON — might be raw text. Emit as content.
			text := string(msgBuf)
			if text != "" {
				accumulated.WriteString(text)
				ch <- provider.StreamChunk{Delta: text}
			}
			continue
		}

		// Extract text content from the message.
		if text, ok := msg["text"].(string); ok && text != "" {
			accumulated.WriteString(text)
			ch <- provider.StreamChunk{Delta: text}
		}

		// Check for error messages.
		if errMsg, ok := msg["error"].(map[string]any); ok {
			if msg, ok := errMsg["message"].(string); ok {
				ch <- provider.StreamChunk{
					Error: fmt.Errorf("cursor: %s", msg),
					Done:  true,
				}
				return
			}
		}
	}
}

// makeConnectEnvelope wraps a JSON body in a Connect-RPC unary envelope:
// 1 byte flags (0x00) + 4 bytes big-endian length + JSON body.
func makeConnectEnvelope(body []byte) []byte {
	envelope := make([]byte, 5+len(body))
	envelope[0] = flagEnvelopeStart
	binary.BigEndian.PutUint32(envelope[1:5], uint32(len(body)))
	copy(envelope[5:], body)
	return envelope
}

// extractTextContent extracts text from a conversation.Message, handling
// both simple string content and block-based content.
func extractTextContent(msg *conversation.Message) string {
	if msg == nil {
		return ""
	}
	// If the message has content blocks, extract text from them.
	if len(msg.OrderedBlocks) > 0 {
		var text strings.Builder
		for _, block := range msg.OrderedBlocks {
			if block.Type == conversation.BlockTypeContent && block.Content != "" {
				text.WriteString(block.Content)
			}
		}
		return text.String()
	}
	// Fall back to simple content field.
	return msg.Content
}
