package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

type familyCheckingProvider struct {
	want  chrome.FamilyContext
	calls atomic.Int32
}

func (p *familyCheckingProvider) Name() string { return "family-check" }
func (p *familyCheckingProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{SupportedModels: []string{"family-model"}}
}
func (p *familyCheckingProvider) Chat(ctx context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls.Add(1)
	got, err := chrome.Require(ctx)
	if err != nil {
		return nil, err
	}
	if !got.SameFamily(p.want) {
		return nil, errors.New("provider received wrong browser family")
	}
	return &provider.ChatResponse{
		Message:      &conversation.Message{Role: conversation.RoleAssistant, Content: "ok"},
		FinishReason: provider.FinishReasonStop,
	}, nil
}
func (p *familyCheckingProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("unexpected stream")
}

func resolvedTestFamily(t *testing.T, id string) chrome.FamilyContext {
	t.Helper()
	resolver, err := chrome.NewFamilyResolver(familyTestLookup{conversations: map[string]*conversation.Conversation{
		id: {ID: id},
	}})
	if err != nil {
		t.Fatal(err)
	}
	family, err := resolver.Resolve(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return family
}

func newFamilyTestAgent(t *testing.T, p provider.Provider, family chrome.FamilyContext) *Agent {
	t.Helper()
	agent, err := New(Config{
		Definition: &Definition{
			ID:       "family-agent",
			Name:     "family agent",
			Provider: p.Name(),
			Model:    "family-model",
		},
		Provider:      p,
		BrowserFamily: family,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.Initialize(); err != nil {
		t.Fatal(err)
	}
	return agent
}

func TestAgentExecutionCarriesInheritedBrowserFamily(t *testing.T) {
	family := resolvedTestFamily(t, "root")
	p := &familyCheckingProvider{want: family}
	agent := newFamilyTestAgent(t, p, family)

	if _, err := agent.Execute(t.Context(), ExecuteRequest{Message: "test"}); err != nil {
		t.Fatal(err)
	}
	if p.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", p.calls.Load())
	}
}

func TestAgentExecutionRejectsContradictoryBrowserFamily(t *testing.T) {
	inherited := resolvedTestFamily(t, "root-a")
	other := resolvedTestFamily(t, "root-b")
	p := &familyCheckingProvider{want: inherited}
	agent := newFamilyTestAgent(t, p, inherited)
	ctx, err := chrome.Bind(t.Context(), other)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := agent.Execute(ctx, ExecuteRequest{Message: "test"}); !errors.Is(err, chrome.ErrBrowserFamilyAuthority) {
		t.Fatalf("Execute error = %v, want authority failure", err)
	}
	if p.calls.Load() != 0 {
		t.Fatalf("provider called %d times after authority conflict", p.calls.Load())
	}
}
