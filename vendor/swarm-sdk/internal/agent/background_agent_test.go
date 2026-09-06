package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

type interjectionProbeProvider struct {
	mu          sync.Mutex
	firstTurn   chan struct{}
	releaseTurn chan struct{}
	requests    [][]*conversation.Message
}

func (p *interjectionProbeProvider) Name() string { return "interjection-probe" }

func (p *interjectionProbeProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{FunctionCalling: true, MaxContextWindow: 128_000}
}

func (p *interjectionProbeProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.mu.Lock()
	call := len(p.requests)
	copied := make([]*conversation.Message, len(req.Messages))
	for i, msg := range req.Messages {
		copied[i] = msg.Clone()
	}
	p.requests = append(p.requests, copied)
	p.mu.Unlock()

	if call == 0 {
		close(p.firstTurn)
		<-p.releaseTurn
		return &provider.ChatResponse{
			Message: &conversation.Message{
				Role: conversation.RoleAssistant,
				ToolCalls: []conversation.ToolCall{{
					ID:   "probe-call",
					Name: "probe_tool",
				}},
			},
			FinishReason: provider.FinishReasonToolCalls,
		}, nil
	}
	return &provider.ChatResponse{
		Message:      &conversation.Message{Role: conversation.RoleAssistant, Content: "done"},
		FinishReason: provider.FinishReasonStop,
	}, nil
}

func (p *interjectionProbeProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Delta: resp.Message.Content, ToolCalls: resp.Message.ToolCalls, FinishReason: resp.FinishReason, Done: true}
	close(ch)
	return ch, nil
}

func (p *interjectionProbeProvider) secondRequest() []*conversation.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) < 2 {
		return nil
	}
	return p.requests[1]
}

func TestBackgroundAgentInterjectDeliveredAtNextTurn(t *testing.T) {
	probe := &interjectionProbeProvider{
		firstTurn:   make(chan struct{}),
		releaseTurn: make(chan struct{}),
	}
	toolRegistry := tools.NewSimpleRegistry(noop.NewLogger(), noop.NewTracer())
	if err := toolRegistry.Register(tools.Func[struct{}]("probe_tool", "probe", func(context.Context, struct{}) (*tools.ToolResult, error) {
		return tools.NewToolResult("ok"), nil
	})); err != nil {
		t.Fatalf("register probe tool: %v", err)
	}
	ag, err := New(Config{
		Definition:   &Definition{ID: "interject", Provider: "interjection-probe", Model: "probe"},
		Provider:     probe,
		ToolRegistry: toolRegistry,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b, err := NewBackgroundAgent(BackgroundAgentConfig{
		Agent:  ag,
		Logger: noop.NewLogger(),
		Tracer: noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("NewBackgroundAgent: %v", err)
	}
	if err := b.Start(context.Background(), "initial"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-probe.firstTurn
	if err := b.Interject("steer now"); err != nil {
		t.Fatalf("Interject: %v", err)
	}
	close(probe.releaseTurn)
	<-b.Done()

	var sawInterjection bool
	for _, msg := range probe.secondRequest() {
		if msg.Role == conversation.RoleUser && msg.Content == "steer now" {
			sawInterjection = true
		}
	}
	if !sawInterjection {
		t.Fatalf("second provider request did not contain queued interjection: %#v", probe.secondRequest())
	}
}

func TestBackgroundAgentInterjectQueueFull(t *testing.T) {
	b := &BackgroundAgent{status: StatusRunning}
	for i := 0; i < interjectQueueCapacity; i++ {
		if err := b.Interject("message"); err != nil {
			t.Fatalf("Interject %d: %v", i, err)
		}
	}
	if err := b.Interject("overflow"); !errors.Is(err, ErrInterjectQueueFull) {
		t.Fatalf("overflow error = %v, want ErrInterjectQueueFull", err)
	}
}

func TestBackgroundAgentInterjectNotRunning(t *testing.T) {
	b := &BackgroundAgent{}
	if err := b.Interject("message"); !errors.Is(err, ErrAgentNotRunning) {
		t.Fatalf("not-running error = %v, want ErrAgentNotRunning", err)
	}
}
