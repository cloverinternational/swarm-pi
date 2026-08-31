package gemini

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/google/uuid"
)

// SSEClient handles Server-Sent Events streaming from Gemini API.
type SSEClient struct {
	httpClient *http.Client
}

// NewSSEClient creates a new SSE client.
func NewSSEClient(httpClient *http.Client) *SSEClient {
	return &SSEClient{httpClient: httpClient}
}

// Stream sends a streaming request and returns a channel of responses.
func (c *SSEClient) Stream(ctx context.Context, url, accessToken string, body []byte, model ...string) (<-chan provider.StreamChunk, error) {
	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", url+"?alt=sse", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	m := ""
	if len(model) > 0 {
		m = model[0]
	}
	setGeminiHeaders(req, accessToken, m)
	req.Header.Set("Accept", "text/event-stream")

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, ParseGeminiError(resp.StatusCode, body)
	}

	// Create output channel
	chunks := make(chan provider.StreamChunk, 100)

	// Start goroutine to process SSE stream
	go func() {
		defer close(chunks)
		defer resp.Body.Close()

		// Monitor context cancellation and close the response body to
		// immediately unblock reader.ReadString() below.
		// Without this, cancellation is only detected between reads,
		// so the user must wait for the current API chunk to arrive.
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				resp.Body.Close()
			case <-done:
				// Stream finished normally, no need to force-close
			}
		}()
		defer close(done)

		reader := bufio.NewReader(resp.Body)
		var buffer strings.Builder
		// accumulated preserves provider-native ordering of content/thinking/
		// tool_call blocks across all SSE events for this assistant turn.
		// Attached to the final Done chunk so the agent can record the turn
		// with stable block order; see conversation.BlocksInDisplayOrder.
		var accumulated []conversation.MessageBlock

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					// Process any remaining buffered data
					if buffer.Len() > 0 {
						c.processSSEData(buffer.String(), chunks, &accumulated)
					}
					chunks <- provider.StreamChunk{Done: true, OrderedBlocks: accumulated}
					return
				}
				// If context was cancelled, the read error is from
				// force-closing the response body. Report context error.
				if ctx.Err() != nil {
					chunks <- provider.StreamChunk{
						Done:  true,
						Error: ctx.Err(),
					}
					return
				}
				chunks <- provider.StreamChunk{
					Done:  true,
					Error: fmt.Errorf("read error: %w", err),
				}
				return
			}

			line = strings.TrimSpace(line)

			// SSE format: "data: {...}" followed by empty line
			if after, ok := strings.CutPrefix(line, "data: "); ok {
				buffer.WriteString(after)
			} else if line == "" && buffer.Len() > 0 {
				// Empty line indicates end of event
				c.processSSEData(buffer.String(), chunks, &accumulated)
				buffer.Reset()
			}
			// Ignore other lines (comments, id, event type, etc.)
		}
	}()

	return chunks, nil
}

// processSSEData parses and sends a single SSE data chunk. The accumulated
// argument records provider-native ordering of content/thinking/tool_call
// blocks across events; on the final Done chunk it is snapshotted into
// OrderedBlocks so downstream consumers can reconstruct block order.
func (c *SSEClient) processSSEData(data string, chunks chan<- provider.StreamChunk, accumulated *[]conversation.MessageBlock) {
	var resp GeminiResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		// Log error but continue
		chunks <- provider.StreamChunk{
			Error: fmt.Errorf("failed to parse response: %w", err),
		}
		return
	}

	chunk := translateStreamResponse(&resp)
	if chunk == nil {
		return
	}
	// Walk this event's parts in stream order and append to accumulator.
	// Pair function_call parts with their freshly-generated ToolCall IDs
	// in chunk.ToolCalls (positional match — translateStreamResponse emits
	// them in the same order it visits parts).
	appendGeminiOrderedBlocks(accumulated, &resp, chunk.ToolCalls)

	// Only expose OrderedBlocks on the final chunk; intermediate chunks
	// leave the field nil to match Anthropic/OpenAI semantics.
	if chunk.Done {
		snapshot := make([]conversation.MessageBlock, len(*accumulated))
		copy(snapshot, *accumulated)
		chunk.OrderedBlocks = snapshot
	}
	chunks <- *chunk
}

// appendGeminiOrderedBlocks walks candidate.Content.Parts in stream order and
// appends one MessageBlock per part to the accumulator. Consecutive same-type
// text blocks are merged so incremental text deltas collapse into a single
// content/thinking block per run, keeping tool_call boundaries intact.
//
// eventToolCalls must be the chunk.ToolCalls slice produced by
// translateStreamResponse for this same event — the Nth function_call part
// maps to the Nth entry there (both are produced in the same iteration order
// over parts).
func appendGeminiOrderedBlocks(accumulated *[]conversation.MessageBlock, resp *GeminiResponse, eventToolCalls []conversation.ToolCall) {
	if resp == nil || resp.Response == nil || len(resp.Response.Candidates) == 0 {
		return
	}
	candidate := resp.Response.Candidates[0]
	if candidate.Content == nil {
		return
	}
	toolIdx := 0
	for _, part := range candidate.Content.Parts {
		if part == nil {
			continue
		}
		if part.Text != "" {
			blockType := conversation.BlockTypeContent
			if isThought(part.Thought) {
				blockType = conversation.BlockTypeThinking
			}
			// Merge with previous block if same type — coalesces streaming
			// text deltas into a single block while preserving tool_call
			// boundaries between runs of text.
			if n := len(*accumulated); n > 0 && (*accumulated)[n-1].Type == blockType {
				(*accumulated)[n-1].Content += part.Text
				continue
			}
			*accumulated = append(*accumulated, conversation.MessageBlock{
				Type:     blockType,
				Content:  part.Text,
				Sequence: len(*accumulated),
			})
			continue
		}
		if part.FunctionCall != nil && toolIdx < len(eventToolCalls) {
			tc := eventToolCalls[toolIdx]
			toolIdx++
			*accumulated = append(*accumulated, conversation.MessageBlock{
				Type:     conversation.BlockTypeToolCall,
				ToolCall: &tc,
				Sequence: len(*accumulated),
			})
		}
	}
}

// isThought checks if a Part's Thought field indicates thinking content.
// The Gemini API sends thought as a boolean, but it may arrive as various types.
func isThought(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t == "true" || t == "1"
	default:
		return v != nil
	}
}

// translateStreamResponse converts a Gemini streaming response to a StreamChunk.
func translateStreamResponse(resp *GeminiResponse) *provider.StreamChunk {
	if resp == nil || resp.Response == nil {
		return nil
	}

	chunk := &provider.StreamChunk{}

	// Thread the trace ID through Metadata so the bridge can populate
	// TokenCountPayload.MessageID (Gemini doesn't have a persistent completion ID;
	// traceId is the closest equivalent and is set when available).
	if resp.TraceID != "" {
		chunk.Metadata = map[string]any{
			"message_id": resp.TraceID,
		}
	}

	// Process candidates
	if len(resp.Response.Candidates) > 0 {
		candidate := resp.Response.Candidates[0]

		// Extract text delta, separating thinking from content
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					// If the part has thought=true, it's thinking content
					if isThought(part.Thought) {
						chunk.Thinking += part.Text
					} else {
						chunk.Delta += part.Text
					}
				}
				if part.FunctionCall != nil {
					// Generate a unique toolu_-prefixed ID for each tool call.
					// Gemini API does not return per-call IDs in its FunctionCall struct.
					// Using the function name as the ID causes two problems:
					//   1. Duplicate IDs when the same tool is called multiple times
					//   2. Invalid characters if the tool name contains chars outside [a-zA-Z0-9_-]
					// Both violate Anthropic's API constraints when the conversation is later
					// replayed to Anthropic (cross-provider fallback).
					toolCallID := "toolu_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:24]
					chunk.ToolCalls = append(chunk.ToolCalls, conversation.ToolCall{
						ID:               toolCallID,
						Name:             part.FunctionCall.Name,
						Parameters:       part.FunctionCall.Args,
						ThoughtSignature: part.ThoughtSignature, // Preserve for Gemini thinking models
					})
				}
			}
		}

		// Check for finish reason
		if candidate.FinishReason != "" {
			chunk.FinishReason = translateFinishReason(candidate.FinishReason)
			chunk.Done = true
		}
	}

	// Add usage metadata
	if resp.Response.UsageMetadata != nil {
		chunk.Usage = &conversation.TokenUsage{
			Input:  resp.Response.UsageMetadata.PromptTokenCount,
			Output: resp.Response.UsageMetadata.CandidatesTokenCount,
			Total:  resp.Response.UsageMetadata.TotalTokenCount,
		}
	}

	return chunk
}

// StreamAccumulator accumulates streaming chunks into a complete response.
type StreamAccumulator struct {
	Content      strings.Builder
	ToolCalls    []conversation.ToolCall
	FinishReason provider.FinishReason
	Usage        *conversation.TokenUsage
}

// NewStreamAccumulator creates a new accumulator.
func NewStreamAccumulator() *StreamAccumulator {
	return &StreamAccumulator{}
}

// Add adds a chunk to the accumulator.
func (a *StreamAccumulator) Add(chunk provider.StreamChunk) {
	if chunk.Delta != "" {
		a.Content.WriteString(chunk.Delta)
	}
	if len(chunk.ToolCalls) > 0 {
		a.ToolCalls = append(a.ToolCalls, chunk.ToolCalls...)
	}
	if chunk.FinishReason != "" {
		a.FinishReason = chunk.FinishReason
	}
	if chunk.Usage != nil {
		a.Usage = chunk.Usage
	}
}

// ToMessage converts the accumulated content to a Message.
func (a *StreamAccumulator) ToMessage() *conversation.Message {
	return &conversation.Message{
		Role:      conversation.RoleAssistant,
		Content:   a.Content.String(),
		ToolCalls: a.ToolCalls,
	}
}

// ToResponse converts the accumulated content to a ChatResponse.
func (a *StreamAccumulator) ToResponse() *provider.ChatResponse {
	finishReason := a.FinishReason

	// CRITICAL FIX: Override finish reason if there are tool calls.
	// Gemini API often returns "STOP" as the finish reason even when it wants to call tools.
	// This is different from OpenAI ("tool_calls") and Anthropic ("tool_use") which have
	// explicit finish reasons for tool calls. Without this fix, the agent loop sees
	// FinishReasonStop and terminates instead of executing the requested tools.
	if len(a.ToolCalls) > 0 {
		finishReason = provider.FinishReasonToolCalls
	}

	return &provider.ChatResponse{
		Message:      a.ToMessage(),
		FinishReason: finishReason,
		Usage:        a.Usage,
	}
}
