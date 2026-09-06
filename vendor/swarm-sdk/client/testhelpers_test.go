package client

import (
	"context"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// slowProvider blocks Chat for a configurable delay, returning the reply only
// if ctx isn't cancelled first. Used to exercise context cancellation paths.
type slowProvider struct {
	delay time.Duration
	reply string
}

func (p *slowProvider) Name() string { return "slow" }

func (p *slowProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Streaming: false, FunctionCalling: false}
}

func (p *slowProvider) Chat(ctx context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
	select {
	case <-time.After(p.delay):
		return &provider.ChatResponse{
			Message:      &conversation.Message{Role: conversation.RoleAssistant, Content: p.reply},
			FinishReason: provider.FinishReasonStop,
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *slowProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, context.Canceled
}

// nilResponseProvider returns (nil, nil) from Chat so Generate has to handle
// the nil-message case gracefully.
type nilResponseProvider struct{}

func (*nilResponseProvider) Name() string                        { return "nil-response" }
func (*nilResponseProvider) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (*nilResponseProvider) Chat(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, nil
}
func (*nilResponseProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, nil
}
