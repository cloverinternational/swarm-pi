package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestBuildRequest_CodexWireParity(t *testing.T) {
	payload, err := buildRequestWithOptions(provider.ChatRequest{
		Model:        "gpt-5.5",
		SystemPrompt: "system prompt",
		Tools: []provider.Tool{
			{Name: "bash", Description: "run", Parameters: map[string]any{"type": "object"}},
		},
	}, requestOptions{promptCacheKey: "conv-42", parallelToolCalls: true})
	if err != nil {
		t.Fatalf("buildRequestWithOptions: %v", err)
	}

	include, ok := payload["include"].([]string)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Errorf("include = %v, want [reasoning.encrypted_content]", payload["include"])
	}
	if payload["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want auto", payload["tool_choice"])
	}
	if payload["parallel_tool_calls"] != true {
		t.Errorf("parallel_tool_calls = %v, want true when catalog flag set", payload["parallel_tool_calls"])
	}
	if payload["prompt_cache_key"] != "conv-42" {
		t.Errorf("prompt_cache_key = %v", payload["prompt_cache_key"])
	}
	if payload["instructions"] != "system prompt" {
		t.Errorf("instructions = %v", payload["instructions"])
	}
	if _, hasTools := payload["tools"]; !hasTools {
		t.Error("non-lite request must carry top-level tools")
	}
	if payload["store"] != false {
		t.Errorf("store = %v, want false", payload["store"])
	}
}

func TestBuildRequest_ParallelToolCallsDefaultsFalse(t *testing.T) {
	// Unknown models must not request parallel tool calls (codex-rs
	// fallback metadata parity).
	payload, err := buildRequestWithOptions(provider.ChatRequest{Model: "gpt-unknown"}, requestOptions{})
	if err != nil {
		t.Fatalf("buildRequestWithOptions: %v", err)
	}
	if payload["parallel_tool_calls"] != false {
		t.Errorf("parallel_tool_calls = %v, want false for unknown model", payload["parallel_tool_calls"])
	}
}

func TestBuildRequest_MaxEffortClampedWithoutSupport(t *testing.T) {
	payload, err := buildRequestWithOptions(provider.ChatRequest{
		Model:           "gpt-5.5",
		ReasoningEffort: "max",
	}, requestOptions{supportsMaxEffort: false})
	if err != nil {
		t.Fatalf("buildRequestWithOptions: %v", err)
	}
	reasoning := payload["reasoning"].(map[string]any)
	if reasoning["effort"] != "xhigh" {
		t.Errorf("effort = %v, want xhigh (max clamped on non-supporting model)", reasoning["effort"])
	}
}

func TestBuildRequest_ResponsesLiteMode(t *testing.T) {
	payload, err := buildRequestWithOptions(provider.ChatRequest{
		Model:        "gpt-5.6-terra",
		SystemPrompt: "system prompt",
		Tools: []provider.Tool{
			{Name: "bash", Description: "run", Parameters: map[string]any{"type": "object"}},
		},
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "hi"},
		},
		ReasoningEffort: "max",
	}, requestOptions{responsesLite: true, supportsMaxEffort: true, promptCacheKey: "conv-7"})
	if err != nil {
		t.Fatalf("buildRequestWithOptions: %v", err)
	}

	if _, hasInstructions := payload["instructions"]; hasInstructions {
		t.Error("lite request must not carry top-level instructions")
	}
	if _, hasTools := payload["tools"]; hasTools {
		t.Error("lite request must not carry top-level tools")
	}
	if payload["parallel_tool_calls"] != false {
		t.Errorf("parallel_tool_calls = %v, want false in lite mode", payload["parallel_tool_calls"])
	}

	input, ok := payload["input"].([]map[string]any)
	if !ok {
		t.Fatalf("input type %T", payload["input"])
	}
	if len(input) != 3 {
		t.Fatalf("len(input) = %d, want 3 (additional_tools, developer message, user message)", len(input))
	}
	if input[0]["type"] != "additional_tools" || input[0]["role"] != "developer" {
		t.Errorf("input[0] = %+v, want additional_tools item", input[0])
	}
	if input[1]["type"] != "message" || input[1]["role"] != "developer" {
		t.Errorf("input[1] = %+v, want developer instructions message", input[1])
	}
	content, ok := input[1]["content"].([]map[string]any)
	if !ok || len(content) != 1 || content[0]["type"] != "input_text" || content[0]["text"] != "system prompt" {
		t.Errorf("developer message content = %+v", input[1]["content"])
	}
	if input[2]["type"] != "message" || input[2]["role"] != "user" {
		t.Errorf("input[2] = %+v, want user message", input[2])
	}

	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok {
		t.Fatal("lite request must carry reasoning object")
	}
	if reasoning["context"] != "all_turns" {
		t.Errorf("reasoning.context = %v, want all_turns", reasoning["context"])
	}
	if reasoning["effort"] != "max" {
		t.Errorf("reasoning.effort = %v, want max", reasoning["effort"])
	}
}

func TestBuildRequest_SerializesImageMetadata(t *testing.T) {
	payload, err := buildRequestWithOptions(provider.ChatRequest{
		Model: "gpt-5.6-sol",
		Messages: []*conversation.Message{
			{
				Role:    conversation.RoleUser,
				Content: "describe this image",
				Metadata: map[string]any{
					"images": []map[string]string{
						{
							"type":       "base64",
							"media_type": "image/png",
							"data":       "aGVsbG8=",
						},
					},
				},
			},
		},
	}, requestOptions{responsesLite: true})
	if err != nil {
		t.Fatalf("buildRequestWithOptions: %v", err)
	}

	input := payload["input"].([]map[string]any)
	userMessage := input[len(input)-1]
	content, ok := userMessage["content"].([]map[string]any)
	if !ok {
		t.Fatalf("user message content type %T, want []map[string]any", userMessage["content"])
	}
	if len(content) != 2 {
		t.Fatalf("len(content) = %d, want text and image parts", len(content))
	}
	if content[0]["type"] != "input_text" || content[0]["text"] != "describe this image" {
		t.Errorf("text part = %+v", content[0])
	}
	if content[1]["type"] != "input_image" || content[1]["image_url"] != "data:image/png;base64,aGVsbG8=" {
		t.Errorf("image part = %+v", content[1])
	}
}

func TestBuildRequest_ReplaysOutputItemsInStreamOrder(t *testing.T) {
	// A turn that interleaved reasoning and function calls must replay in
	// the exact original order with server ids and status stripped
	// (Responses API rejects reordered reasoning and unstored ids).
	rawItems := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"blob-1","status":"completed"}`),
		json.RawMessage(`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"bash","arguments":"{\"command\":\"ls\"}","status":"completed"}`),
		json.RawMessage(`{"type":"reasoning","id":"rs_2","summary":[],"encrypted_content":"blob-2"}`),
		json.RawMessage(`{"type":"function_call","id":"fc_2","call_id":"call_2","name":"bash","arguments":"{}"}`),
	}
	payload, err := buildRequest(provider.ChatRequest{
		Model:        "gpt-5.5",
		SystemPrompt: "s",
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "do it"},
			{
				Role: conversation.RoleAssistant,
				Metadata: map[string]any{
					codexOutputItemsMetadataKey: rawItems,
				},
				ToolCalls: []conversation.ToolCall{
					{ID: "call_1", Name: "bash", Parameters: map[string]any{"command": "ls"}},
					{ID: "call_2", Name: "bash", Parameters: map[string]any{}},
				},
			},
			{
				Role: conversation.RoleTool,
				ToolResults: []conversation.ToolResult{
					{CallID: "call_1", Name: "bash", Output: "ok"},
					{CallID: "call_2", Name: "bash", Output: "ok"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	input, ok := payload["input"].([]map[string]any)
	if !ok {
		t.Fatalf("input type %T", payload["input"])
	}
	// user, r1, fc1, r2, fc2, output1, output2
	if len(input) != 7 {
		t.Fatalf("len(input) = %d, want 7: %+v", len(input), input)
	}
	wantOrder := []struct{ typ, key, val string }{
		{"message", "role", "user"},
		{"reasoning", "encrypted_content", "blob-1"},
		{"function_call", "call_id", "call_1"},
		{"reasoning", "encrypted_content", "blob-2"},
		{"function_call", "call_id", "call_2"},
		{"function_call_output", "call_id", "call_1"},
		{"function_call_output", "call_id", "call_2"},
	}
	for i, want := range wantOrder {
		if input[i]["type"] != want.typ || input[i][want.key] != want.val {
			t.Errorf("input[%d] = %+v, want type=%s %s=%s", i, input[i], want.typ, want.key, want.val)
		}
	}
	// Server ids and status must be stripped from every replayed item.
	for i := 1; i <= 4; i++ {
		if _, hasID := input[i]["id"]; hasID {
			t.Errorf("input[%d] retains server id: %+v", i, input[i])
		}
		if _, hasStatus := input[i]["status"]; hasStatus {
			t.Errorf("input[%d] retains status: %+v", i, input[i])
		}
	}
	// The reconstructed function_call/tool-call path must NOT duplicate the
	// replayed items.
	fcCount := 0
	for _, item := range input {
		if item["type"] == "function_call" {
			fcCount++
		}
	}
	if fcCount != 2 {
		t.Errorf("function_call count = %d, want 2 (no duplicates)", fcCount)
	}
}

func TestBuildRequest_ReplaysOutputItemsFromRoundTrippedMetadata(t *testing.T) {
	// Simulates metadata loaded from persisted JSON: []any of map[string]any.
	payload, err := buildRequest(provider.ChatRequest{
		Model: "gpt-5.5",
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "go"},
			{
				Role:    conversation.RoleAssistant,
				Content: "done",
				Metadata: map[string]any{
					codexOutputItemsMetadataKey: []any{
						map[string]any{"type": "reasoning", "id": "rs_2", "encrypted_content": "blob2"},
						map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "done"}}},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	input := payload["input"].([]map[string]any)
	found := false
	for _, item := range input {
		if item["type"] == "reasoning" && item["encrypted_content"] == "blob2" {
			if _, hasID := item["id"]; hasID {
				t.Error("round-tripped reasoning item retains server id")
			}
			found = true
		}
	}
	if !found {
		t.Errorf("round-tripped reasoning item not replayed: %+v", input)
	}
}

func TestStream_CodexHeaderParity(t *testing.T) {
	headerCh := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerCh <- r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	prov, err := New(Config{
		AccessToken: "tok",
		AccountID:   "acct",
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	stream, err := prov.Stream(context.Background(), provider.ChatRequest{
		Model:    "gpt-5.6-terra",
		Metadata: map[string]any{"conversation_id": "conv-abc"},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream {
	}

	headers := <-headerCh
	// session-id is conversation-scoped and matches prompt_cache_key.
	if got := headers.Get("session-id"); got != "conv-abc" {
		t.Errorf("session-id = %q, want conv-abc", got)
	}
	if got := headers.Get("thread-id"); got != "conv-abc" {
		t.Errorf("thread-id = %q, want conv-abc", got)
	}
	if got := headers.Get("x-client-request-id"); got != "conv-abc" {
		t.Errorf("x-client-request-id = %q", got)
	}
	if got := headers.Get("version"); got != codexClientVersion {
		t.Errorf("version = %q, want %q", got, codexClientVersion)
	}
	if got := headers.Get("x-openai-internal-codex-responses-lite"); got != "true" {
		t.Errorf("lite header = %q, want true for gpt-5.6-terra", got)
	}
	if headers.Get("x-openai-conversation-id") != "" || headers.Get("x-openai-session-id") != "" {
		t.Error("legacy random per-request headers must not be sent")
	}
}

func TestStream_CapturesOrderedOutputItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		wtr := bufio.NewWriter(w)
		wtr.WriteString("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"id\":\"rs_9\",\"summary\":[],\"encrypted_content\":\"secret\"}}\n\n")
		wtr.WriteString("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_9\",\"call_id\":\"call_9\",\"name\":\"bash\",\"arguments\":\"{}\"}}\n\n")
		wtr.WriteString("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		wtr.Flush()
	}))
	defer server.Close()

	prov, err := New(Config{AccessToken: "tok", AccountID: "acct", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := prov.Stream(context.Background(), provider.ChatRequest{Model: "gpt-5.5"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var finalMetadata map[string]any
	for chunk := range stream {
		if chunk.Error != nil {
			t.Fatalf("chunk error: %v", chunk.Error)
		}
		if chunk.Done {
			finalMetadata = chunk.Metadata
		}
	}

	items, ok := finalMetadata[codexOutputItemsMetadataKey].([]json.RawMessage)
	if !ok || len(items) != 2 {
		t.Fatalf("expected 2 captured output items, got %v", finalMetadata)
	}
	var first, second map[string]any
	if err := json.Unmarshal(items[0], &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(items[1], &second); err != nil {
		t.Fatal(err)
	}
	if first["type"] != "reasoning" || first["encrypted_content"] != "secret" {
		t.Errorf("first item = %v", first)
	}
	if second["type"] != "function_call" || second["call_id"] != "call_9" {
		t.Errorf("second item = %v", second)
	}
}

func TestStream_CapturesOutputItemsWithoutReasoning(t *testing.T) {
	// Mixed text + custom-tool turns must retain their original item order even
	// when the response contains no reasoning item.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		wtr := bufio.NewWriter(w)
		wtr.WriteString("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hi\"}]}}\n\n")
		wtr.WriteString("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"custom_tool_call\",\"call_id\":\"patch_1\",\"name\":\"apply_patch\",\"input\":\"*** Begin Patch\\n*** End Patch\"}}\n\n")
		wtr.WriteString("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		wtr.Flush()
	}))
	defer server.Close()

	prov, err := New(Config{AccessToken: "tok", AccountID: "acct", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := prov.Stream(context.Background(), provider.ChatRequest{Model: "gpt-5.5"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var metadata map[string]any
	for chunk := range stream {
		if chunk.Done {
			metadata = chunk.Metadata
		}
	}
	items, ok := metadata[codexOutputItemsMetadataKey].([]json.RawMessage)
	if !ok || len(items) != 2 {
		t.Fatalf("expected ordered message + custom tool items, got %v", metadata)
	}
	var first, second map[string]any
	if err := json.Unmarshal(items[0], &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(items[1], &second); err != nil {
		t.Fatal(err)
	}
	if first["type"] != "message" || second["type"] != "custom_tool_call" || second["call_id"] != "patch_1" {
		t.Fatalf("output item order was not preserved: first=%v second=%v", first, second)
	}
}
