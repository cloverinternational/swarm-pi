package chat

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerror "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	tuiobs "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/observability"
	tmocks "github.com/Swarm-Code/mono/swarm-sdk/tests/mocks"
)

type testChatProvider struct {
	name         string
	chatCalls    []provider.ChatRequest
	chatFn       func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error)
	capabilities provider.Capabilities
}

func (p *testChatProvider) Name() string {
	return p.name
}

func (p *testChatProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.chatCalls = append(p.chatCalls, req)
	if p.chatFn != nil {
		return p.chatFn(ctx, req)
	}
	return &provider.ChatResponse{}, nil
}

func (p *testChatProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

func (p *testChatProvider) Capabilities() provider.Capabilities {
	if p.capabilities.MaxContextWindow == 0 {
		return provider.Capabilities{MaxContextWindow: 200000}
	}
	return p.capabilities
}

func newTestAgent(t *testing.T, prov provider.Provider, providerName, model string) *agent.Agent {
	t.Helper()
	reg := tools.NewSimpleRegistry(nil, nil)
	logger := tuiobs.NewTUILogger()
	tracer := tuiobs.NewTUITracer(logger)
	provReg := provider.NewSimpleRegistry(logger)
	agt, err := agent.New(agent.Config{
		Definition: &agent.Definition{
			ID:       "test-agent",
			Name:     "test-agent",
			Provider: providerName,
			Model:    model,
		},
		Provider:         prov,
		ProviderRegistry: provReg,
		ToolRegistry:     reg,
		Logger:           logger,
		Tracer:           tracer,
		Auditor:          &tmocks.MockAuditor{},
	})
	if err != nil {
		t.Fatalf("failed to create test agent: %v", err)
	}
	return agt
}

func TestRetryDisabledUsesDirectProviderPath(t *testing.T) {
	primary := &testChatProvider{
		name: "openai",
		chatFn: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
			return &provider.ChatResponse{}, nil
		},
	}

	fallbackBuildCalls := 0
	stack, err := buildReliabilityProviderStack(
		primary,
		"OpenAI",
		"gpt-5.1",
		commands.RetrySettingsConfig{
			Enabled:               false,
			MaxRetriesPerProvider: 3,
			RetryAfterFallbackMs:  1000,
		},
		&fallback.Chain{
			Primary: fallback.ModelRef{Provider: "OpenAI", Model: "gpt-5.1"},
			Fallbacks: []fallback.ModelRef{
				{Provider: "Anthropic", Model: "claude-sonnet-4-20250514"},
			},
		},
		func(providerName, model string) (provider.Provider, error) {
			fallbackBuildCalls++
			return nil, fmt.Errorf("unexpected fallback build for %s/%s", providerName, model)
		},
		nil,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("buildReliabilityProviderStack failed: %v", err)
	}

	if _, ok := stack.(*provider.Orchestrator); ok {
		t.Fatalf("expected direct provider path when retry is disabled")
	}
	if fallbackBuildCalls != 0 {
		t.Fatalf("fallback builders should not be called when retry is disabled")
	}
}

func TestRetryEnabledBuildsOrchestratorWithFallbackOrder(t *testing.T) {
	primary := &testChatProvider{
		name: "openai",
		chatFn: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
			if req.Model != "gpt-5.1" {
				return nil, fmt.Errorf("primary got wrong model: %s", req.Model)
			}
			return nil, sdkerror.Transient("provider.timeout", "temporary outage")
		},
	}
	fallback1 := &testChatProvider{
		name: "anthropic",
		chatFn: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
			if req.Model != "claude-sonnet-4-20250514" {
				return nil, fmt.Errorf("fallback1 got wrong model: %s", req.Model)
			}
			return &provider.ChatResponse{}, nil
		},
	}
	fallback2 := &testChatProvider{
		name: "gemini",
		chatFn: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
			return nil, fmt.Errorf("fallback2 should not be called, got model=%s", req.Model)
		},
	}

	var buildOrder []string
	stack, err := buildReliabilityProviderStack(
		primary,
		"OpenAI",
		"gpt-5.1",
		commands.RetrySettingsConfig{
			Enabled:               true,
			MaxRetriesPerProvider: 1,
			RetryAfterFallbackMs:  1,
		},
		&fallback.Chain{
			Primary: fallback.ModelRef{Provider: "OpenAI", Model: "gpt-5.1"},
			Fallbacks: []fallback.ModelRef{
				{Provider: "Anthropic", Model: "claude-sonnet-4-20250514"},
				{Provider: "Gemini", Model: "gemini-2.5-pro"},
			},
		},
		func(providerName, model string) (provider.Provider, error) {
			key := providerName + "::" + model
			buildOrder = append(buildOrder, key)
			switch key {
			case "Anthropic::claude-sonnet-4-20250514":
				return fallback1, nil
			case "Gemini::gemini-2.5-pro":
				return fallback2, nil
			default:
				return nil, fmt.Errorf("unexpected fallback provider request: %s", key)
			}
		},
		nil,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("buildReliabilityProviderStack failed: %v", err)
	}

	if _, ok := stack.(*provider.Orchestrator); !ok {
		t.Fatalf("expected orchestrator provider when retry is enabled")
	}

	_, err = stack.Chat(context.Background(), provider.ChatRequest{Model: "this-should-be-overridden"})
	if err != nil {
		t.Fatalf("orchestrated chat failed: %v", err)
	}

	wantBuildOrder := []string{
		"Anthropic::claude-sonnet-4-20250514",
		"Gemini::gemini-2.5-pro",
	}
	if !slices.Equal(buildOrder, wantBuildOrder) {
		t.Fatalf("unexpected fallback build order\ngot:  %v\nwant: %v", buildOrder, wantBuildOrder)
	}
	if len(primary.chatCalls) == 0 || len(fallback1.chatCalls) == 0 {
		t.Fatalf("expected primary and first fallback providers to be called")
	}
}

func TestReliabilityFallbacksShareProviderContextTimeline(t *testing.T) {
	store := newProviderContextCaptureStore()
	primary := &testChatProvider{
		name: "openai",
		chatFn: func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
			return nil, sdkerror.Transient("provider.timeout", "primary failed")
		},
	}
	fallbackProvider := &testChatProvider{
		name: "anthropic",
		chatFn: func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
			return &provider.ChatResponse{Usage: &conversation.TokenUsage{Input: 42, Output: 3, Total: 45}}, nil
		},
	}

	stack, err := buildReliabilityProviderStack(
		primary,
		"OpenAI",
		"gpt-5.1",
		commands.RetrySettingsConfig{
			Enabled:               true,
			MaxRetriesPerProvider: 1,
			RetryAfterFallbackMs:  1,
		},
		&fallback.Chain{
			Primary: fallback.ModelRef{Provider: "OpenAI", Model: "gpt-5.1"},
			Fallbacks: []fallback.ModelRef{
				{Provider: "Anthropic", Model: "claude-sonnet"},
			},
		},
		func(providerName, model string) (provider.Provider, error) {
			if providerName != "Anthropic" || model != "claude-sonnet" {
				return nil, fmt.Errorf("unexpected fallback: %s/%s", providerName, model)
			}
			return fallbackProvider, nil
		},
		func(p provider.Provider) provider.Provider {
			return newContextInjectingProviderWithCapture(p, nil, store)
		},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("buildReliabilityProviderStack: %v", err)
	}

	_, err = stack.Chat(context.Background(), contextCaptureRequest("run-fallback", "ignored", "payload"))
	if err != nil {
		t.Fatalf("fallback Chat: %v", err)
	}
	calls := store.take("run-fallback")
	if len(calls) < 2 {
		t.Fatalf("timeline calls = %d, want primary failure(s) plus fallback: %+v", len(calls), calls)
	}
	if calls[0].Provider != "openai" || calls[0].Outcome != contextCallOutcomeError {
		t.Fatalf("first call = %+v, want failed primary", calls[0])
	}
	last := calls[len(calls)-1]
	if last.Provider != "anthropic" || last.Outcome != contextCallOutcomeDone || last.Usage == nil || last.Usage.InputTokens != 42 {
		t.Fatalf("last call = %+v, want paired fallback success", last)
	}
}

func TestFallbackEntriesInvalidOrDuplicateHandledSafely(t *testing.T) {
	primary := &testChatProvider{
		name: "openai",
		chatFn: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
			if req.Model != "gpt-5.1" {
				return nil, fmt.Errorf("primary got wrong model: %s", req.Model)
			}
			return &provider.ChatResponse{}, nil
		},
	}

	var buildOrder []string
	stack, err := buildReliabilityProviderStack(
		primary,
		"OpenAI",
		"gpt-5.1",
		commands.RetrySettingsConfig{
			Enabled:               true,
			MaxRetriesPerProvider: 2,
			RetryAfterFallbackMs:  1500,
		},
		&fallback.Chain{
			Primary: fallback.ModelRef{Provider: "OpenAI", Model: "gpt-5.1"},
			Fallbacks: []fallback.ModelRef{
				{Provider: "", Model: "missing-provider"},
				{Provider: "OpenAI", Model: "gpt-5.1"}, // duplicate of primary
				{Provider: "Anthropic", Model: ""},     // missing model
				{Provider: "Anthropic", Model: "claude-sonnet-4-20250514"},
			},
		},
		func(providerName, model string) (provider.Provider, error) {
			buildOrder = append(buildOrder, providerName+"::"+model)
			return nil, fmt.Errorf("simulate failed fallback provider build")
		},
		nil,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("buildReliabilityProviderStack failed: %v", err)
	}

	_, err = stack.Chat(context.Background(), provider.ChatRequest{Model: "ignored"})
	if err != nil {
		t.Fatalf("expected primary to succeed despite invalid fallback entries: %v", err)
	}

	if len(buildOrder) != 1 || buildOrder[0] != "Anthropic::claude-sonnet-4-20250514" {
		t.Fatalf("expected only one valid fallback build attempt, got %v", buildOrder)
	}
}

func TestSDKIntegrationSwitchProviderModelChangeReloadsWithReliability(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	initial := &testChatProvider{name: "openai"}
	logger := tuiobs.NewTUILogger()
	sdk := &SDKIntegration{
		provider:     initial,
		providerName: "openai",
		currentModel: "gpt-5.1",
		logger:       logger,
		tracer:       tuiobs.NewTUITracer(logger),
		agent:        newTestAgent(t, initial, "openai", "gpt-5.1"),
		providerBuilder: func(providerName, model string, _ providerBuildDeps) (provider.Provider, string, string, bool, error) {
			return &testChatProvider{name: provider.NormalizeProviderName(providerName)}, "system", "token", false, nil
		},
		reliabilityConfigLoader: func() (*commands.SwarmOSConfig, error) {
			return &commands.SwarmOSConfig{
				RetrySettings: &commands.RetrySettingsConfig{
					Enabled:               false,
					MaxRetriesPerProvider: 3,
					RetryAfterFallbackMs:  1000,
				},
			}, nil
		},
	}
	sdk.agent.SetConfiguredContextWindow(400_000)

	if err := sdk.SwitchProvider("openai", "gpt-5.2"); err != nil {
		t.Fatalf("SwitchProvider failed: %v", err)
	}

	if sdk.currentModel != "gpt-5.2" {
		t.Fatalf("expected model update to persist, got %s", sdk.currentModel)
	}
	if sdk.provider == initial {
		t.Fatalf("expected provider to be rebuilt when model changes")
	}
	if got := sdk.agent.ConfiguredContextWindow(); got != 0 {
		t.Fatalf("stale configured context survived model switch: got %d, want 0", got)
	}
	if cfg := sdk.agent.AutoCompactionConfig(); !cfg.EnableAutoCompaction {
		t.Fatal("auto-compaction was not re-resolved after model switch")
	}
}
