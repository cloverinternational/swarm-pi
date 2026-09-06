// Package mock provides a mock provider for testing.
package mock

import (
	"context"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// Provider implements provider.Provider for testing.
type Provider struct {
	name      string
	models    []string
	requests  []*provider.ChatRequest
	responses []*provider.ChatResponse
	mu        sync.Mutex
}

// NewProvider creates a new mock provider.
func NewProvider() *Provider {
	return &Provider{
		name:   "mock",
		models: []string{"mock-model"},
	}
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return p.name
}

// Chat sends a request and returns a response.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Record the request
	p.requests = append(p.requests, &req)

	// If we have canned responses, use them
	if len(p.responses) > 0 {
		resp := p.responses[0]
		p.responses = p.responses[1:]
		return resp, nil
	}

	// Default response
	return &provider.ChatResponse{
		Message: &conversation.Message{
			Role:    conversation.RoleAssistant,
			Content: "mock response",
		},
		Usage: &conversation.TokenUsage{
			Input:  100,
			Output: 50,
		},
		FinishReason: provider.FinishReasonStop,
	}, nil
}

// Stream processes a request with streaming. For the mock, it just returns the full response.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	p.requests = append(p.requests, &req)
	p.mu.Unlock()

	const body = "mock stream response"
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{
			Delta: body,
		}
		ch <- provider.StreamChunk{
			Done: true,
			Usage: &conversation.TokenUsage{
				Input:  100,
				Output: 50,
			},
			FinishReason: provider.FinishReasonStop,
			OrderedBlocks: []conversation.MessageBlock{{
				Type:     conversation.BlockTypeContent,
				Content:  body,
				Sequence: 0,
			}},
		}
	}()
	return ch, nil
}

// Capabilities returns the provider capabilities.
func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Vision:               true,
		Streaming:            true,
		FunctionCalling:      true,
		SupportsSystemPrompt: true,
	}
}

// AddResponse adds a canned response for testing.
func (p *Provider) AddResponse(content string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses = append(p.responses, &provider.ChatResponse{
		Message: &conversation.Message{
			Role:    conversation.RoleAssistant,
			Content: content,
		},
		FinishReason: provider.FinishReasonStop,
	})
}

// GetRequests returns all recorded requests.
func (p *Provider) GetRequests() []*provider.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests
}

// Reset clears all recorded requests and responses.
func (p *Provider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = nil
	p.responses = nil
}
