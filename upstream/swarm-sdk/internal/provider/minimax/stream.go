package minimax

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	httplib "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/http"
)

// stream sends a chat request and streams the response in chunks.
func (p *Provider) stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	// Translate request to MiniMax format
	minimaxReq, providerJSON, err := translateRequest(req, p.logger)
	if err != nil {
		return nil, err
	}

	// Enable streaming
	minimaxReq.Stream = true

	// Store for debugging
	if rawJSON, err := json.Marshal(providerJSON); err == nil {
		p.lastProviderJSON = rawJSON
	}

	// Log request
	if p.logger != nil {
		p.logger.Debug(ctx, "minimax.stream.request",
			observability.F("model", minimaxReq.Model),
			observability.F("message_count", len(minimaxReq.Messages)),
		)
	}

	// Build and execute request using fluent API
	reqBuilder := p.client.BuildRequest(ctx).
		Method("POST").
		URL(p.config.BaseURL + "/v1/messages").
		Body(minimaxReq)

	resp, err := p.client.Do(ctx, reqBuilder)
	if err != nil {
		return nil, err
	}

	// Check for HTTP errors
	if resp.IsError() {
		defer resp.Close()
		body, _ := resp.Body()
		return nil, p.handleAPIError(resp.StatusCode(), body)
	}

	// Create output channel
	chunkChan := make(chan provider.StreamChunk, 100)

	// Start streaming goroutine
	go p.processStream(ctx, resp, chunkChan)

	return chunkChan, nil
}

// processStream processes the SSE stream from MiniMax.
func (p *Provider) processStream(ctx context.Context, resp *httplib.Response, chunkChan chan<- provider.StreamChunk) {
	defer close(chunkChan)
	defer resp.Close()

	body, _ := resp.Body()
	reader := strings.NewReader(string(body))
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var (
		textBuffer     strings.Builder
		thinkingBuffer strings.Builder
		stopReason     provider.FinishReason
		// orderedBlocks preserves provider-native ordering of content and
		// thinking blocks across the stream so downstream consumers can
		// reconstruct display order. Minimax does not surface tool_use
		// blocks through this SSE endpoint; only text + thinking apply.
		orderedBlocks []conversation.MessageBlock
	)

	appendBlock := func(typ conversation.BlockType, text string) {
		if text == "" {
			return
		}
		if n := len(orderedBlocks); n > 0 && orderedBlocks[n-1].Type == typ {
			orderedBlocks[n-1].Content += text
			return
		}
		orderedBlocks = append(orderedBlocks, conversation.MessageBlock{
			Type:     typ,
			Content:  text,
			Sequence: len(orderedBlocks),
		})
	}

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines
		if line == "" {
			continue
		}

		// Skip non-data lines
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		// Extract data
		data := strings.TrimPrefix(line, "data: ")

		// Check for stream end
		if data == "[DONE]" {
			break
		}

		// Parse SSE event
		var event StreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			if p.logger != nil {
				p.logger.Warn(ctx, "minimax.stream.parse_error",
					observability.F("error", err.Error()),
				)
			}
			continue
		}

		// Handle raw event callback
		if p.rawEventCallback != nil {
			p.rawEventCallback.OnRawEvent(event.Type, data)
		}

		// Process event based on type
		switch event.Type {
		case "content_block_delta":
			if event.Delta != nil {
				if event.Delta.Text != "" {
					textBuffer.WriteString(event.Delta.Text)
					appendBlock(conversation.BlockTypeContent, event.Delta.Text)
					chunkChan <- provider.StreamChunk{
						Delta: event.Delta.Text,
					}
				}
				if event.Delta.Thinking != "" {
					thinkingBuffer.WriteString(event.Delta.Thinking)
					appendBlock(conversation.BlockTypeThinking, event.Delta.Thinking)
					chunkChan <- provider.StreamChunk{
						Thinking: event.Delta.Thinking,
					}
				}
			}

		case "message_delta":
			if event.Delta != nil && event.Delta.StopReason != "" {
				stopReason = translateStopReason(event.Delta.StopReason)
			}

		case "message_stop":
			// Send final chunk with provider-native block order attached.
			snapshot := make([]conversation.MessageBlock, len(orderedBlocks))
			copy(snapshot, orderedBlocks)
			chunkChan <- provider.StreamChunk{
				Done:          true,
				FinishReason:  stopReason,
				OrderedBlocks: snapshot,
			}
		}
	}

	if err := scanner.Err(); err != nil && p.logger != nil {
		p.logger.Error(ctx, "minimax.stream.scanner_error",
			observability.F("error", err.Error()),
		)
	}
}

// StreamEvent represents a streaming event from MiniMax.
type StreamEvent struct {
	Type         string              `json:"type"`
	Index        []int               `json:"index,omitempty"`
	ContentBlock *StreamContentBlock `json:"content_block,omitempty"`
	Delta        *StreamDelta        `json:"delta,omitempty"`
	Message      *StreamMessage      `json:"message,omitempty"`
	Usage        *StreamUsage        `json:"usage,omitempty"`
	Error        *StreamError        `json:"error,omitempty"`
}

// StreamContentBlock represents a content block in streaming.
type StreamContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Input    any    `json:"input,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

// StreamDelta represents a delta in streaming.
type StreamDelta struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	Thinking   string `json:"thinking,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

// StreamMessage represents a message in streaming.
type StreamMessage struct {
	ID    string      `json:"id"`
	Type  string      `json:"type"`
	Role  string      `json:"role"`
	Usage StreamUsage `json:"usage"`
}

// StreamUsage represents usage in streaming.
type StreamUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// StreamError represents an error in streaming.
type StreamError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
