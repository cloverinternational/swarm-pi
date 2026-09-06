package codex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

type testSpan struct{}

func (s *testSpan) End()                                       {}
func (s *testSpan) SetAttribute(string, any)                   {}
func (s *testSpan) SetAttributes(map[string]any)               {}
func (s *testSpan) SetStatus(observability.StatusCode, string) {}
func (s *testSpan) RecordError(error)                          {}
func (s *testSpan) SpanID() string                             { return "span" }
func (s *testSpan) TraceID() string                            { return "trace" }
func (s *testSpan) Context() context.Context                   { return context.Background() }

type testTracer struct{}

func (t *testTracer) StartSpan(ctx context.Context, name string) (context.Context, observability.Span) {
	return ctx, &testSpan{}
}

func (t *testTracer) StartSpanWithOptions(ctx context.Context, name string, opts observability.SpanOptions) (context.Context, observability.Span) {
	return ctx, &testSpan{}
}

func (t *testTracer) SpanFromContext(ctx context.Context) observability.Span {
	return &testSpan{}
}

func (t *testTracer) InjectContext(ctx context.Context, carrier map[string]string) error {
	return nil
}

func (t *testTracer) ExtractContext(carrier map[string]string) (context.Context, error) {
	return context.Background(), nil
}

func TestNew_EmptyAccountID_ReturnsError(t *testing.T) {
	cfg := Config{
		AccessToken: "test-access-token",
		AccountID:   "", // Empty AccountID should fail
	}

	provider, err := New(cfg)

	if err == nil {
		t.Fatal("expected error for empty AccountID, got nil")
	}
	if provider != nil {
		t.Fatal("expected nil provider when error returned")
	}
	if err.Error() != "codex account id is required" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestNew_EmptyAccessToken_ReturnsError(t *testing.T) {
	cfg := Config{
		AccessToken: "", // Empty AccessToken should fail
		AccountID:   "test-account-id",
	}

	provider, err := New(cfg)

	if err == nil {
		t.Fatal("expected error for empty AccessToken, got nil")
	}
	if provider != nil {
		t.Fatal("expected nil provider when error returned")
	}
	if err.Error() != "codex access token is required" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestNew_ValidCredentials_Succeeds(t *testing.T) {
	cfg := Config{
		AccessToken: "test-access-token",
		AccountID:   "test-account-id",
	}

	provider, err := New(cfg)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected non-nil provider")
	}
	if provider.Name() != "codex" {
		t.Errorf("expected provider name 'codex', got '%s'", provider.Name())
	}
}

func TestNew_DefaultBaseURL(t *testing.T) {
	cfg := Config{
		AccessToken: "test-access-token",
		AccountID:   "test-account-id",
		// BaseURL not set - should use default
	}

	provider, err := New(cfg)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.baseURL != "https://chatgpt.com/backend-api/codex" {
		t.Errorf("expected default baseURL, got '%s'", provider.baseURL)
	}
}

func TestNew_CustomBaseURL(t *testing.T) {
	cfg := Config{
		AccessToken: "test-access-token",
		AccountID:   "test-account-id",
		BaseURL:     "https://custom.example.com/api/",
	}

	provider, err := New(cfg)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// BaseURL should have trailing slash trimmed
	if provider.baseURL != "https://custom.example.com/api" {
		t.Errorf("expected baseURL with trimmed slash, got '%s'", provider.baseURL)
	}
}

func TestNew_ClonesPassthroughMaps(t *testing.T) {
	cfg := Config{
		AccessToken: "test-access-token",
		AccountID:   "test-account-id",
		ExtraQueryParams: map[string]string{
			"feature": "alpha",
		},
		ExtraHeaders: map[string]string{
			"x-test-header": "abc",
		},
	}

	prov, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mutate source config maps after provider construction.
	cfg.ExtraQueryParams["feature"] = "mutated"
	cfg.ExtraHeaders["x-test-header"] = "mutated"

	if got := prov.extraQuery["feature"]; got != "alpha" {
		t.Fatalf("expected cloned query params, got %q", got)
	}
	if got := prov.extraHeader["x-test-header"]; got != "abc" {
		t.Fatalf("expected cloned header values, got %q", got)
	}
}

func TestBuildRequest_WithReasoningEffort(t *testing.T) {
	payload, err := buildRequest(provider.ChatRequest{
		Model:           "gpt-5.2-codex",
		SystemPrompt:    "system",
		ReasoningEffort: "xhigh",
	})
	if err != nil {
		t.Fatalf("buildRequest failed: %v", err)
	}

	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("expected reasoning object in payload")
	}
	if reasoning["effort"] != "xhigh" {
		t.Fatalf("expected reasoning.effort=xhigh, got %v", reasoning["effort"])
	}
}

func TestBuildRequest_AutoEffortStillRequestsSummary(t *testing.T) {
	payload, err := buildRequest(provider.ChatRequest{
		Model:           "gpt-5.2-codex",
		SystemPrompt:    "system",
		ReasoningEffort: "auto",
	})
	if err != nil {
		t.Fatalf("buildRequest failed: %v", err)
	}

	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok || reasoning["summary"] != "auto" {
		t.Fatalf("expected summary-only reasoning payload for auto effort, got %#v", payload["reasoning"])
	}
	if _, exists := reasoning["effort"]; exists {
		t.Fatalf("auto effort must remain omitted, got %#v", reasoning)
	}
}

func TestBuildRequest_EncodesToolCallsAndToolOutputs(t *testing.T) {
	payload, err := buildRequest(provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "system",
		Messages: []*conversation.Message{
			{
				Role:    conversation.RoleSystem,
				Content: "must be ignored for codex input",
			},
			{
				Role:    conversation.RoleUser,
				Content: "create test.txt",
			},
			{
				Role: conversation.RoleAssistant,
				ToolCalls: []conversation.ToolCall{
					{
						ID:   "call_1",
						Name: "exec_command",
						Parameters: map[string]any{
							"cmd": "touch test.txt",
						},
					},
				},
			},
			{
				Role: conversation.RoleTool,
				ToolResults: []conversation.ToolResult{
					{
						CallID: "call_1",
						Name:   "exec_command",
						Output: "created",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildRequest failed: %v", err)
	}

	inputRaw, ok := payload["input"].([]map[string]any)
	if !ok {
		t.Fatalf("expected input payload as []map, got %T", payload["input"])
	}
	if len(inputRaw) != 3 {
		t.Fatalf("expected 3 codex input items, got %d", len(inputRaw))
	}

	if inputRaw[0]["type"] != "message" || inputRaw[0]["role"] != "user" {
		t.Fatalf("unexpected first input item: %+v", inputRaw[0])
	}
	if inputRaw[1]["type"] != "function_call" || inputRaw[1]["call_id"] != "call_1" || inputRaw[1]["name"] != "exec_command" {
		t.Fatalf("unexpected function_call input item: %+v", inputRaw[1])
	}
	if inputRaw[2]["type"] != "function_call_output" || inputRaw[2]["call_id"] != "call_1" || inputRaw[2]["output"] != "created" {
		t.Fatalf("unexpected function_call_output input item: %+v", inputRaw[2])
	}
}

func TestBuildRequest_UsesParametersSchemaForTools(t *testing.T) {
	payload, err := buildRequest(provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "system",
		Tools: []provider.Tool{
			{
				Name:        "bash",
				Description: "Run a shell command",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{
							"type": "string",
						},
					},
					"required":             []string{"command"},
					"additionalProperties": false,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildRequest failed: %v", err)
	}

	toolsRaw, ok := payload["tools"].([]map[string]any)
	if !ok {
		t.Fatalf("expected tools payload as []map, got %T", payload["tools"])
	}
	if len(toolsRaw) != 1 {
		t.Fatalf("expected one tool, got %d", len(toolsRaw))
	}
	tool := toolsRaw[0]
	if _, exists := tool["input_schema"]; exists {
		t.Fatalf("unexpected input_schema key in codex tool payload: %+v", tool)
	}
	if _, exists := tool["parameters"]; !exists {
		t.Fatalf("expected parameters key in codex tool payload: %+v", tool)
	}
	if strict, ok := tool["strict"].(bool); !ok || strict {
		t.Fatalf("expected strict=false for codex tool payload, got %+v", tool["strict"])
	}
}

func TestBuildRequest_NormalizesBareObjectToolSchemas(t *testing.T) {
	payload, err := buildRequest(provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "system",
		Tools: []provider.Tool{
			{
				Name:        "firebase_get_environment",
				Description: "Get Firebase environment details",
				Parameters: map[string]any{
					"type": "object",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildRequest failed: %v", err)
	}

	toolsRaw, ok := payload["tools"].([]map[string]any)
	if !ok {
		t.Fatalf("expected tools payload as []map, got %T", payload["tools"])
	}
	if len(toolsRaw) != 1 {
		t.Fatalf("expected one tool, got %d", len(toolsRaw))
	}

	params, ok := toolsRaw[0]["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("expected tool parameters as map, got %T", toolsRaw[0]["parameters"])
	}
	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected normalized properties map, got %T", params["properties"])
	}
	if len(properties) != 0 {
		t.Fatalf("expected empty properties map, got %+v", properties)
	}
}

func TestStream_AppliesExtraQueryAndHeaders(t *testing.T) {
	var gotQuery string
	var gotHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("exp")
		gotHeader = r.Header.Get("x-exp-header")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"unauthorized"}}`))
	}))
	defer srv.Close()

	prov, err := New(Config{
		AccessToken: "token",
		AccountID:   "acct",
		BaseURL:     srv.URL,
		Tracer:      &testTracer{},
		ExtraQueryParams: map[string]string{
			"exp": "1",
		},
		ExtraHeaders: map[string]string{
			"x-exp-header": "enabled",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Non-200s now surface synchronously from Stream (so the orchestrator's
	// retry machinery can see them) instead of as an in-channel error chunk.
	_, err = prov.Stream(context.Background(), provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "sys",
	})
	if err == nil {
		t.Fatalf("expected synchronous error from non-200 response")
	}

	if gotQuery != "1" {
		t.Fatalf("expected exp query param to be sent, got %q", gotQuery)
	}
	if gotHeader != "enabled" {
		t.Fatalf("expected x-exp-header to be sent, got %q", gotHeader)
	}
}

func TestChat_UsesCompletedOutputWhenDeltasArePartial(t *testing.T) {
	const fullText = "In a town that never put names on its streets..."

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"In\"}\n\n")
			flusher.Flush()
			fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":11},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}]}}\n\n", fullText)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	prov, err := New(Config{
		AccessToken: "token",
		AccountID:   "acct",
		BaseURL:     srv.URL,
		Tracer:      &testTracer{},
	})
	if err != nil {
		t.Fatalf("unexpected error creating provider: %v", err)
	}

	resp, err := prov.Chat(context.Background(), provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "sys",
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	if resp == nil || resp.Message == nil {
		t.Fatalf("expected non-nil response message")
	}
	if resp.Message.Content != fullText {
		t.Fatalf("expected full completed text, got %q", resp.Message.Content)
	}
	if resp.Usage == nil || resp.Usage.Input != 10 || resp.Usage.Output != 11 {
		t.Fatalf("expected usage from response.completed, got %+v", resp.Usage)
	}
}

func TestChat_UsesOutputItemDoneWhenCompletedLacksOutput(t *testing.T) {
	const fullText = "In a town that never put names on its streets..."

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"In\"}\n\n")
			flusher.Flush()
			fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}}\n\n", fullText)
			flusher.Flush()
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":7,\"output_tokens\":9}}}\n\n")
			flusher.Flush()
		}
	}))
	defer srv.Close()

	prov, err := New(Config{
		AccessToken: "token",
		AccountID:   "acct",
		BaseURL:     srv.URL,
		Tracer:      &testTracer{},
	})
	if err != nil {
		t.Fatalf("unexpected error creating provider: %v", err)
	}

	resp, err := prov.Chat(context.Background(), provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "sys",
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	if resp == nil || resp.Message == nil {
		t.Fatalf("expected non-nil response message")
	}
	if resp.Message.Content != fullText {
		t.Fatalf("expected full text from output_item.done, got %q", resp.Message.Content)
	}
}

func TestStream_HandlesFunctionCallAndResponseDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"exec_command\",\"arguments\":\"{\\\"cmd\\\":\\\"pwd\\\"}\"}}\n\n")
			flusher.Flush()
			fmt.Fprint(w, "data: {\"type\":\"response.done\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}\n\n")
			flusher.Flush()
		}
	}))
	defer srv.Close()

	prov, err := New(Config{
		AccessToken: "token",
		AccountID:   "acct",
		BaseURL:     srv.URL,
		Tracer:      &testTracer{},
	})
	if err != nil {
		t.Fatalf("unexpected error creating provider: %v", err)
	}

	stream, err := prov.Stream(context.Background(), provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "sys",
	})
	if err != nil {
		t.Fatalf("stream setup failed: %v", err)
	}

	var finalChunk provider.StreamChunk
	for chunk := range stream {
		finalChunk = chunk
	}
	if !finalChunk.Done {
		t.Fatalf("expected final done chunk")
	}
	if finalChunk.FinishReason != provider.FinishReasonToolCalls {
		t.Fatalf("expected finish reason %q, got %q", provider.FinishReasonToolCalls, finalChunk.FinishReason)
	}
	if len(finalChunk.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(finalChunk.ToolCalls))
	}
	if finalChunk.ToolCalls[0].ID != "call_1" || finalChunk.ToolCalls[0].Name != "exec_command" {
		t.Fatalf("unexpected parsed tool call: %+v", finalChunk.ToolCalls[0])
	}
	if finalChunk.ToolCalls[0].Parameters["cmd"] != "pwd" {
		t.Fatalf("expected parsed command argument, got %+v", finalChunk.ToolCalls[0].Parameters)
	}
	if finalChunk.Usage == nil || finalChunk.Usage.Input != 3 || finalChunk.Usage.Output != 4 {
		t.Fatalf("expected usage from response.done, got %+v", finalChunk.Usage)
	}
}

func TestChat_PropagatesFunctionCallsFromStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"exec_command\",\"arguments\":\"{\\\"cmd\\\":\\\"ls\\\"}\"}}\n\n")
			flusher.Flush()
			fmt.Fprint(w, "data: {\"type\":\"response.done\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n")
			flusher.Flush()
		}
	}))
	defer srv.Close()

	prov, err := New(Config{
		AccessToken: "token",
		AccountID:   "acct",
		BaseURL:     srv.URL,
		Tracer:      &testTracer{},
	})
	if err != nil {
		t.Fatalf("unexpected error creating provider: %v", err)
	}

	resp, err := prov.Chat(context.Background(), provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "sys",
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	if resp == nil || resp.Message == nil {
		t.Fatalf("expected non-nil response")
	}
	if resp.FinishReason != provider.FinishReasonToolCalls {
		t.Fatalf("expected finish reason %q, got %q", provider.FinishReasonToolCalls, resp.FinishReason)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].Parameters["cmd"] != "ls" {
		t.Fatalf("expected command argument ls, got %+v", resp.Message.ToolCalls[0].Parameters)
	}
}

func TestStream_ParsesFunctionCallInputFallbackAsCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"bash\",\"input\":\"pwd\"}}\n\n")
			flusher.Flush()
			fmt.Fprint(w, "data: {\"type\":\"response.done\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
			flusher.Flush()
		}
	}))
	defer srv.Close()

	prov, err := New(Config{
		AccessToken: "token",
		AccountID:   "acct",
		BaseURL:     srv.URL,
		Tracer:      &testTracer{},
	})
	if err != nil {
		t.Fatalf("unexpected error creating provider: %v", err)
	}

	stream, err := prov.Stream(context.Background(), provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "sys",
	})
	if err != nil {
		t.Fatalf("stream setup failed: %v", err)
	}

	var finalChunk provider.StreamChunk
	for chunk := range stream {
		finalChunk = chunk
	}
	if len(finalChunk.ToolCalls) != 1 {
		t.Fatalf("expected one parsed tool call, got %d", len(finalChunk.ToolCalls))
	}
	if got := finalChunk.ToolCalls[0].Parameters["command"]; got != "pwd" {
		t.Fatalf("expected bash command fallback to be parsed, got %+v", finalChunk.ToolCalls[0].Parameters)
	}
}

func TestStream_ParsesFunctionCallInputWhenArgumentsAreEmptyJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"bash\",\"arguments\":\"{}\",\"input\":\"pwd\"}}\n\n")
			flusher.Flush()
			fmt.Fprint(w, "data: {\"type\":\"response.done\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
			flusher.Flush()
		}
	}))
	defer srv.Close()

	prov, err := New(Config{
		AccessToken: "token",
		AccountID:   "acct",
		BaseURL:     srv.URL,
		Tracer:      &testTracer{},
	})
	if err != nil {
		t.Fatalf("unexpected error creating provider: %v", err)
	}

	stream, err := prov.Stream(context.Background(), provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "sys",
	})
	if err != nil {
		t.Fatalf("stream setup failed: %v", err)
	}

	var finalChunk provider.StreamChunk
	for chunk := range stream {
		finalChunk = chunk
	}
	if len(finalChunk.ToolCalls) != 1 {
		t.Fatalf("expected one parsed tool call, got %d", len(finalChunk.ToolCalls))
	}
	if got := finalChunk.ToolCalls[0].Parameters["command"]; got != "pwd" {
		t.Fatalf("expected command parsed from input fallback, got %+v", finalChunk.ToolCalls[0].Parameters)
	}
}

func TestStream_NormalizesCommonToolParameterAliases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_bash\",\"name\":\"bash\",\"arguments\":\"{\\\"cmd\\\":\\\"pwd\\\"}\"}}\n\n")
			flusher.Flush()
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_write\",\"name\":\"Write\",\"arguments\":\"{\\\"path\\\":\\\"notes.txt\\\",\\\"text\\\":\\\"hello\\\"}\"}}\n\n")
			flusher.Flush()
			fmt.Fprint(w, "data: {\"type\":\"response.done\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
			flusher.Flush()
		}
	}))
	defer srv.Close()

	prov, err := New(Config{
		AccessToken: "token",
		AccountID:   "acct",
		BaseURL:     srv.URL,
		Tracer:      &testTracer{},
	})
	if err != nil {
		t.Fatalf("unexpected error creating provider: %v", err)
	}

	stream, err := prov.Stream(context.Background(), provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "sys",
	})
	if err != nil {
		t.Fatalf("stream setup failed: %v", err)
	}

	var finalChunk provider.StreamChunk
	for chunk := range stream {
		finalChunk = chunk
	}
	if len(finalChunk.ToolCalls) != 2 {
		t.Fatalf("expected two tool calls, got %d", len(finalChunk.ToolCalls))
	}

	bashCall := finalChunk.ToolCalls[0]
	if bashCall.Name != "bash" || bashCall.Parameters["command"] != "pwd" {
		t.Fatalf("expected bash cmd alias normalization, got %+v", bashCall)
	}

	writeCall := finalChunk.ToolCalls[1]
	if writeCall.Name != "Write" || writeCall.Parameters["file_path"] != "notes.txt" || writeCall.Parameters["content"] != "hello" {
		t.Fatalf("expected write alias normalization, got %+v", writeCall)
	}
}

func TestStream_UpsertsToolCallArgsFromAddedToDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"bash\",\"arguments\":\"{}\"}}\n\n")
			flusher.Flush()
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"pwd\\\"}\"}}\n\n")
			flusher.Flush()
			fmt.Fprint(w, "data: {\"type\":\"response.done\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
			flusher.Flush()
		}
	}))
	defer srv.Close()

	prov, err := New(Config{
		AccessToken: "token",
		AccountID:   "acct",
		BaseURL:     srv.URL,
		Tracer:      &testTracer{},
	})
	if err != nil {
		t.Fatalf("unexpected error creating provider: %v", err)
	}

	stream, err := prov.Stream(context.Background(), provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "sys",
	})
	if err != nil {
		t.Fatalf("stream setup failed: %v", err)
	}

	var finalChunk provider.StreamChunk
	for chunk := range stream {
		finalChunk = chunk
	}
	if len(finalChunk.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(finalChunk.ToolCalls))
	}
	if got := finalChunk.ToolCalls[0].Parameters["command"]; got != "pwd" {
		t.Fatalf("expected args from output_item.done to overwrite placeholder args, got %+v", finalChunk.ToolCalls[0].Parameters)
	}
}

func TestBuildRequest_AdvertisesApplyPatchAsCustomTool(t *testing.T) {
	payload, err := buildRequest(provider.ChatRequest{
		Model: "gpt-5.2-codex",
		Tools: []provider.Tool{{
			Name:        "apply_patch",
			Description: "Edit files with V4A",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"input": map[string]any{"type": "string"}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	toolsRaw := payload["tools"].([]map[string]any)
	if len(toolsRaw) != 1 || toolsRaw[0]["type"] != "custom" || toolsRaw[0]["name"] != "apply_patch" {
		t.Fatalf("apply_patch was not encoded as a custom tool: %#v", toolsRaw)
	}
	if _, exists := toolsRaw[0]["parameters"]; exists {
		t.Fatalf("custom apply_patch must not carry a JSON schema: %#v", toolsRaw[0])
	}
	if toolsRaw[0]["description"] != "The `apply_patch` tool can be used to edit files. This is a FREEFORM tool, so do not wrap the patch in JSON." {
		t.Fatalf("unexpected apply_patch description: %#v", toolsRaw[0]["description"])
	}
	format, ok := toolsRaw[0]["format"].(map[string]any)
	if !ok || format["type"] != "grammar" || format["syntax"] != "lark" {
		t.Fatalf("apply_patch must carry the Codex grammar format: %#v", toolsRaw[0])
	}
	definition, _ := format["definition"].(string)
	for _, required := range []string{
		`start: begin_patch hunk+ end_patch`,
		`begin_patch: "*** Begin Patch" LF`,
		`update_hunk: "*** Update File: " filename LF change_move? change?`,
		`change_line: ("+" | "-" | " ") /(.*)/ LF`,
	} {
		if !strings.Contains(definition, required) {
			t.Fatalf("apply_patch grammar missing %q:\n%s", required, definition)
		}
	}
}

func TestBuildRequest_ReplaysApplyPatchCustomOutput(t *testing.T) {
	payload, err := buildRequest(provider.ChatRequest{
		Model: "gpt-5.2-codex",
		Messages: []*conversation.Message{
			{
				Role: conversation.RoleAssistant,
				ToolCalls: []conversation.ToolCall{{
					ID: "patch_1", Name: "apply_patch", Parameters: map[string]any{"input": "*** Begin Patch\n*** End Patch"},
				}},
			},
			{
				Role: conversation.RoleTool,
				ToolResults: []conversation.ToolResult{{
					CallID: "patch_1", // Legacy histories may omit Name.
					Output: "updated file.go",
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := payload["input"].([]map[string]any)
	if len(input) != 2 || input[0]["type"] != "custom_tool_call" || input[1]["type"] != "custom_tool_call_output" || input[1]["call_id"] != "patch_1" {
		t.Fatalf("unexpected custom tool call/output replay: %#v", input)
	}
}
