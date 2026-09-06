// Package agenttest provides test doubles for the Swarm SDK agent package.
//
// Use [MockProvider] to test agent behavior without calling any real LLM API.
// Responses are scripted in advance and consumed in order on each Chat call.
//
// Example — basic scripted responses:
//
//	func TestMyAgent(t *testing.T) {
//	    ag := agenttest.NewAgent(t, "The answer is 42.")
//	    result, err := ag.Run(context.Background(), "What is 6×7?")
//	    if err != nil { t.Fatal(err) }
//	    if !strings.Contains(result.Message, "42") {
//	        t.Errorf("unexpected response: %s", result.Message)
//	    }
//	}
//
// Example — scripted tool call then final response:
//
//	mock := agenttest.NewMockProvider().
//	    RespondWith("I'll read the file.").       // first turn: plain text
//	    ThenCallTool("read_file", map[string]any{"path": "/main.go"}).
//	    ThenRespondWith("The file has 42 lines.") // after tool result
//
//	ag, _ := agent.New(agent.Config{
//	    Definition: &agent.Definition{ID: "t", Provider: "mock", Model: "mock"},
//	    Provider:   mock,
//	    Tools:      []tools.Tool{myReadTool},
//	})
package agenttest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// step is one scripted response in the mock sequence.
type step struct {
	kind       string         // "text" or "tool_call"
	text       string         // for kind=="text": the assistant response
	toolName   string         // for kind=="tool_call": tool to invoke
	toolParams map[string]any // for kind=="tool_call": parameters
}

// MockProvider is a [provider.Provider] that returns scripted responses in order.
// It is safe for concurrent use.
type MockProvider struct {
	mu        sync.Mutex
	steps     []step
	callIndex int
	callLog   []string
	toolCalls map[string]int // name → count
}

// NewMockProvider returns a new empty MockProvider.
// Add scripted responses with [MockProvider.RespondWith], [MockProvider.ThenCallTool],
// and [MockProvider.ThenRespondWith].
func NewMockProvider() *MockProvider {
	return &MockProvider{
		toolCalls: make(map[string]int),
	}
}

// RespondWith appends a plain-text assistant response to the script.
// This is typically used as the first (or only) step.
func (m *MockProvider) RespondWith(text string) *MockProvider {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps = append(m.steps, step{kind: "text", text: text})
	return m
}

// ThenCallTool appends a tool-call step to the script.
// The mock will return a FinishReasonToolCalls response on the next Chat call,
// causing the agent to execute the named tool with the given params.
//
// Follow with [ThenRespondWith] to script the assistant's reply after the tool result.
func (m *MockProvider) ThenCallTool(name string, params map[string]any) *MockProvider {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps = append(m.steps, step{kind: "tool_call", toolName: name, toolParams: params})
	return m
}

// ThenRespondWith appends another plain-text response step.
// Equivalent to [RespondWith] but reads more naturally in a chain after [ThenCallTool].
func (m *MockProvider) ThenRespondWith(text string) *MockProvider {
	return m.RespondWith(text)
}

// ToolCallCount returns the number of times the mock triggered a call to toolName.
func (m *MockProvider) ToolCallCount(toolName string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.toolCalls[toolName]
}

// CallLog returns a chronological slice of all Chat calls received by the mock.
// Each entry is a human-readable summary of the request.
func (m *MockProvider) CallLog() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.callLog))
	copy(out, m.callLog)
	return out
}

// TotalCalls returns the total number of Chat calls received.
func (m *MockProvider) TotalCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callIndex
}

// ── provider.Provider implementation ─────────────────────────────────────────

func (m *MockProvider) Name() string { return "mock" }

func (m *MockProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:            false, // force Chat() path in agent executeLoop
		FunctionCalling:      true,
		SupportsSystemPrompt: true,
		SupportsTemperature:  true,
		SupportedModels:      []string{"mock"},
		MaxContextWindow:     128_000,
		MaxOutputTokens:      4096,
	}
}

// Chat returns the next scripted response.
// If all scripted responses are exhausted it returns an empty "stop" response
// so the agent terminates naturally rather than erroring.
func (m *MockProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := m.callIndex
	m.callIndex++
	m.callLog = append(m.callLog, fmt.Sprintf("call[%d] model=%s messages=%d", idx, req.Model, len(req.Messages)))

	// All scripted steps consumed → return a natural stop.
	if idx >= len(m.steps) {
		return &provider.ChatResponse{
			Message:      &conversation.Message{Role: conversation.RoleAssistant, Content: ""},
			FinishReason: provider.FinishReasonStop,
			Usage:        &conversation.TokenUsage{Input: 10, Output: 5},
		}, nil
	}

	s := m.steps[idx]

	switch s.kind {
	case "tool_call":
		m.toolCalls[s.toolName]++
		callID := fmt.Sprintf("mock_call_%d_%d", idx, time.Now().UnixNano())
		return &provider.ChatResponse{
			Message: &conversation.Message{
				Role: conversation.RoleAssistant,
				ToolCalls: []conversation.ToolCall{
					{
						ID:         callID,
						Name:       s.toolName,
						Parameters: s.toolParams,
					},
				},
			},
			FinishReason: provider.FinishReasonToolCalls,
			Usage:        &conversation.TokenUsage{Input: 20, Output: 10},
		}, nil

	default: // "text"
		return &provider.ChatResponse{
			Message:      &conversation.Message{Role: conversation.RoleAssistant, Content: s.text},
			FinishReason: provider.FinishReasonStop,
			Usage:        &conversation.TokenUsage{Input: 20, Output: int(len(s.text) / 4)},
		}, nil
	}
}

// Stream is implemented for interface compliance. It delegates to Chat and
// wraps the response in a single-chunk stream.
func (m *MockProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	resp, err := m.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	ch := make(chan provider.StreamChunk, 2)
	ch <- provider.StreamChunk{
		Delta:        resp.Message.Content,
		ToolCalls:    resp.Message.ToolCalls,
		FinishReason: resp.FinishReason,
		Usage:        resp.Usage,
		Done:         true,
	}
	close(ch)
	return ch, nil
}

// ── Test helper ───────────────────────────────────────────────────────────────

// NewAgent creates a fully configured [agent.Agent] backed by a MockProvider
// that returns the given text responses in order.
//
// It is a convenience wrapper for the common test pattern of "give me an agent
// that just returns these strings". For tool-call scenarios use
// [NewMockProvider] directly and build the agent with [agent.New].
//
//	ag := agenttest.NewAgent(t, "Hello!", "Done.")
//	result, _ := ag.Run(ctx, "Hi")
//	// result.Message == "Hello!"
func NewAgent(t testing.TB, responses ...string) *agent.Agent {
	t.Helper()

	mock := NewMockProvider()
	for _, r := range responses {
		mock.RespondWith(r)
	}

	ag, err := agent.New(agent.Config{
		Definition: &agent.Definition{
			ID:       "agenttest-agent",
			Provider: "mock",
			Model:    "mock",
		},
		Provider: mock,
	})
	if err != nil {
		t.Fatalf("agenttest.NewAgent: failed to create agent: %v", err)
	}
	return ag
}
