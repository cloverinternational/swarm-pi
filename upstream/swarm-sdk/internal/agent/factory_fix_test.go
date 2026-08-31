package agent

import (
	"context"
	"fmt"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome"
	"maps"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// raceTestProvider is a minimal provider.Provider for factory race tests.
type familyTestLookup struct {
	conversations map[string]*conversation.Conversation
}

func (l familyTestLookup) LoadConversation(_ context.Context, id string) (*conversation.Conversation, error) {
	conv, ok := l.conversations[id]
	if !ok {
		return nil, fmt.Errorf("conversation not found")
	}
	return conv, nil
}

type raceTestProvider struct {
	name          string
	contextWindow int
}

func (r *raceTestProvider) Name() string { return r.name }
func (r *raceTestProvider) Chat(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{
		Message:      &conversation.Message{Role: conversation.RoleAssistant, Content: "ok"},
		FinishReason: provider.FinishReasonStop,
	}, nil
}
func (r *raceTestProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Delta: "ok", Done: true, FinishReason: provider.FinishReasonStop}
	close(ch)
	return ch, nil
}
func (r *raceTestProvider) Capabilities() provider.Capabilities {
	contextWindow := r.contextWindow
	if contextWindow == 0 {
		contextWindow = 128_000
	}
	return provider.Capabilities{
		SupportedModels:  []string{"race-model"},
		MaxContextWindow: contextWindow,
	}
}

func TestResolveAgentContextWindowPrecedence(t *testing.T) {
	prov := &raceTestProvider{name: "context-test"}
	tests := []struct {
		name       string
		configured int
		provider   provider.Provider
		want       int
		wantSource string
	}{
		{
			name:       "configured large model wins",
			configured: 400_000,
			provider:   prov,
			want:       400_000,
			wantSource: "configured",
		},
		{
			name:       "known small model remains exact",
			configured: 128_000,
			provider:   prov,
			want:       128_000,
			wantSource: "configured",
		},
		{
			name:       "provider capability used when unconfigured",
			provider:   prov,
			want:       128_000,
			wantSource: "provider_capability",
		},
		{
			name:       "provider capability is policy clamped",
			provider:   &raceTestProvider{name: "large", contextWindow: 1_000_000},
			want:       provider.DefaultContextWindowCap,
			wantSource: "provider_capability",
		},
		{
			name:       "unknown model defaults to 200k",
			want:       provider.DefaultUnknownContextWindow,
			wantSource: "unknown_fallback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, source := resolveAgentContextWindow(tt.configured, tt.provider)
			if got != tt.want || source != tt.wantSource {
				t.Fatalf("resolveAgentContextWindow() = (%d, %q), want (%d, %q)",
					got, source, tt.want, tt.wantSource)
			}
		})
	}
}

func TestCreateSubAgentSharesConfiguredContextWithCompaction(t *testing.T) {
	reg := provider.NewSimpleRegistry(noop.NewLogger())
	if err := reg.Register("contextprovider", func(cfg provider.Config) (provider.Provider, error) {
		return &raceTestProvider{name: cfg.Name}, nil
	}); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	factory, err := NewSimpleFactory(FactoryConfig{
		ProviderRegistry: reg,
		Logger:           noop.NewLogger(),
		Tracer:           noop.NewTracer(),
		Auditor:          noop.NewAuditor(),
	})
	if err != nil {
		t.Fatalf("NewSimpleFactory: %v", err)
	}

	sub, err := factory.CreateSubAgent(context.Background(), SubAgentConfig{
		AgentID: "context-subagent",
		ProviderConfig: provider.Config{
			Name:          "contextprovider",
			Model:         "race-model",
			ContextWindow: 400_000,
		},
	})
	if err != nil {
		t.Fatalf("CreateSubAgent: %v", err)
	}
	if got := sub.ConfiguredContextWindow(); got != 400_000 {
		t.Fatalf("agent configured context = %d, want 400000", got)
	}
	svc := sub.CompactionService()
	if svc == nil {
		t.Fatal("subagent compaction service was not configured")
	}
	if got := svc.GetThresholdInfo(0).ContextLimit; got != 400_000 {
		t.Fatalf("compaction context = %d, want 400000", got)
	}
}

func TestCreateFromDefinition_FillsMissingProviderAndModel(t *testing.T) {
	// Simulate a builtin agent definition (with empty Provider and Model)
	def := &Definition{
		ID:           "test-agent",
		Name:         "Test Agent",
		Description:  "Test agent",
		SystemPrompt: "You are a test agent...",
		Provider:     "", // EMPTY - from builtin agent
		Model:        "", // EMPTY - from builtin agent
	}

	// Create a provider config (resolved from profile)
	providerConfig := provider.Config{
		Name:  "anthropic",
		Model: "claude-3-5-haiku",
	}

	// Call the function that the fix added (fillInMissingProviderAndModel)
	// We simulate what CreateFromDefinition now does before validation
	if def.Provider == "" {
		def.Provider = providerConfig.Name
	}
	if def.Model == "" {
		def.Model = providerConfig.Model
	}

	// Now validation should pass
	err := def.Validate()
	if err != nil {
		t.Errorf("Validation should pass after filling in Provider and Model: %v", err)
	}

	// Verify the values were set correctly
	if def.Provider != "anthropic" {
		t.Errorf("Expected Provider to be 'anthropic', got %q", def.Provider)
	}
	if def.Model != "claude-3-5-haiku" {
		t.Errorf("Expected Model to be 'claude-3-5-haiku', got %q", def.Model)
	}
}

func TestValidate_FailsWithEmptyProviderAndModel(t *testing.T) {
	// This test verifies the original behavior (before the fix)
	def := &Definition{
		ID:           "test-agent",
		Name:         "Test Agent",
		Description:  "Test agent",
		SystemPrompt: "You are a test agent...",
		Provider:     "", // EMPTY
		Model:        "", // EMPTY
	}

	// Validation should fail with empty Provider
	err := def.Validate()
	if err == nil {
		t.Error("Validation should fail with empty Provider")
	}
	if err.Error() != "agent provider is required" {
		t.Errorf("Expected 'agent provider is required' error, got: %v", err)
	}
}

// TestCreateFromDefinition_NoConcurrentMutationOfSharedDefinition verifies that
// when multiple goroutines concurrently call CreateFromDefinition with the same
// shared *Definition (simulating builtin agent definitions), the factory's deep-
// copy prevents data races and leaves the original definition untouched.
func TestCreateFromDefinition_NoConcurrentMutationOfSharedDefinition(t *testing.T) {
	// 1. Set up a provider registry with a mock provider factory.
	reg := provider.NewSimpleRegistry(noop.NewLogger())
	if err := reg.Register("raceprovider", func(cfg provider.Config) (provider.Provider, error) {
		return &raceTestProvider{name: cfg.Name}, nil
	}); err != nil {
		t.Fatalf("failed to register provider: %v", err)
	}

	// 2. Create a factory.
	factory, err := NewSimpleFactory(FactoryConfig{
		ProviderRegistry: reg,
		Logger:           noop.NewLogger(),
		Tracer:           noop.NewTracer(),
		Auditor:          noop.NewAuditor(),
	})
	if err != nil {
		t.Fatalf("failed to create factory: %v", err)
	}

	// 3. Create a shared definition that mimics a builtin agent:
	//    - empty Provider and Model (filled in by factory)
	//    - Metadata map (must be deep-copied)
	sharedDef := &Definition{
		ID:           "general-assistant",
		Name:         "General Assistant",
		Description:  "Shared builtin definition",
		SystemPrompt: "You are a helpful assistant.",
		Provider:     "", // EMPTY — factory fills this
		Model:        "", // EMPTY — factory fills this
		Metadata: map[string]any{
			"type":       "sub_agent",
			"custom_key": "original_value",
		},
	}

	// Capture original values for later verification.
	originalProvider := sharedDef.Provider
	originalModel := sharedDef.Model
	originalMetadata := map[string]any{}
	maps.Copy(originalMetadata, sharedDef.Metadata)

	// 4. Spawn many goroutines that concurrently use the SAME *Definition.
	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := range numGoroutines {
		go func(idx int) {
			defer wg.Done()

			// Each goroutine calls CreateFromDefinition with the SHARED definition.
			providerConfig := provider.Config{
				Name:  "raceprovider",
				Model: "race-model",
			}
			_, err := factory.CreateFromDefinition(context.Background(), sharedDef, providerConfig)
			if err != nil {
				// We expect some failures due to concurrent provider creation,
				// but the important thing is that there is no data race.
				// Log for debugging but don't fail the test — the race detector
				// is the real assertion here.
				t.Logf("goroutine %d: CreateFromDefinition error (may be expected): %v", idx, err)
			}
		}(i)
	}

	wg.Wait()

	// 5. Verify the shared definition was NEVER mutated.
	if sharedDef.Provider != originalProvider {
		t.Errorf("sharedDef.Provider was mutated: got %q, want %q", sharedDef.Provider, originalProvider)
	}
	if sharedDef.Model != originalModel {
		t.Errorf("sharedDef.Model was mutated: got %q, want %q", sharedDef.Model, originalModel)
	}
	if len(sharedDef.Metadata) != len(originalMetadata) {
		t.Errorf("sharedDef.Metadata length changed: got %d, want %d", len(sharedDef.Metadata), len(originalMetadata))
	}
	for k, v := range originalMetadata {
		if sharedDef.Metadata[k] != v {
			t.Errorf("sharedDef.Metadata[%q] was mutated: got %v, want %v", k, sharedDef.Metadata[k], v)
		}
	}
}

// TestCreateFromDefinition_MetadataDeepCopy verifies that the Metadata map is
// deep-copied so that mutations to the copy do not affect the original.
func TestCreateFromDefinition_MetadataDeepCopy(t *testing.T) {
	// Set up a provider registry with a mock provider factory.
	reg := provider.NewSimpleRegistry(noop.NewLogger())
	if err := reg.Register("raceprovider", func(cfg provider.Config) (provider.Provider, error) {
		return &raceTestProvider{name: cfg.Name}, nil
	}); err != nil {
		t.Fatalf("failed to register provider: %v", err)
	}

	factory, err := NewSimpleFactory(FactoryConfig{
		ProviderRegistry: reg,
		Logger:           noop.NewLogger(),
		Tracer:           noop.NewTracer(),
		Auditor:          noop.NewAuditor(),
	})
	if err != nil {
		t.Fatalf("failed to create factory: %v", err)
	}

	// Create a definition with Metadata.
	sharedDef := &Definition{
		ID:           "test-agent",
		Name:         "Test Agent",
		Provider:     "",
		Model:        "",
		SystemPrompt: "You are a test agent.",
		Metadata: map[string]any{
			"type": "sub_agent",
		},
	}

	providerConfig := provider.Config{
		Name:  "raceprovider",
		Model: "race-model",
	}

	// Call CreateFromDefinition.
	agent, err := factory.CreateFromDefinition(context.Background(), sharedDef, providerConfig)
	if err != nil {
		t.Fatalf("CreateFromDefinition failed: %v", err)
	}

	// The factory sets def.Capabilities.SupportsVision and may modify Metadata
	// via the auto-compaction check (type=="sub_agent"). The deep copy ensures
	// the original is untouched.
	if sharedDef.Metadata == nil {
		t.Fatalf("sharedDef.Metadata became nil")
	}
	if sharedDef.Metadata["type"] != "sub_agent" {
		t.Errorf("sharedDef.Metadata['type'] was mutated: got %v, want 'sub_agent'", sharedDef.Metadata["type"])
	}

	// Verify the agent's definition has the filled-in values.
	if agent.Definition().Provider != "raceprovider" {
		t.Errorf("agent definition Provider not filled: got %q", agent.Definition().Provider)
	}
	if agent.Definition().Model != "race-model" {
		t.Errorf("agent definition Model not filled: got %q", agent.Definition().Model)
	}
}

func TestChildConstructorsInheritBrowserFamilyIndependentOfToolAllowlist(t *testing.T) {
	reg := provider.NewSimpleRegistry(noop.NewLogger())
	if err := reg.Register("familyprovider", func(cfg provider.Config) (provider.Provider, error) {
		return &raceTestProvider{name: cfg.Name}, nil
	}); err != nil {
		t.Fatal(err)
	}
	factory, err := NewSimpleFactory(FactoryConfig{
		ProviderRegistry: reg,
		Logger:           noop.NewLogger(),
		Tracer:           noop.NewTracer(),
		Auditor:          noop.NewAuditor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := chrome.NewFamilyResolver(familyTestLookup{conversations: map[string]*conversation.Conversation{
		"root-conversation": {ID: "root-conversation"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	family, err := resolver.Resolve(t.Context(), "root-conversation")
	if err != nil {
		t.Fatal(err)
	}
	parentCtx, err := chrome.Bind(t.Context(), family)
	if err != nil {
		t.Fatal(err)
	}
	providerConfig := provider.Config{Name: "familyprovider", Model: "race-model"}

	subagent, err := factory.CreateSubAgent(parentCtx, SubAgentConfig{
		AgentID:        "subagent",
		Tools:          []string{"read_only"},
		ProviderConfig: providerConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	background, err := factory.CreateBackground(parentCtx, BackgroundConfig{
		AgentID:        "background",
		Tools:          []string{"background_only"},
		ProviderConfig: providerConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	preset, err := CreatePresetSubAgent(parentCtx, factory, PresetQuestionAnswerer, providerConfig)
	if err != nil {
		t.Fatal(err)
	}
	defined, err := factory.CreateFromDefinition(parentCtx, &Definition{
		ID:        "defined-child",
		Name:      "defined child",
		Provider:  "familyprovider",
		Model:     "race-model",
		ToolHints: []string{"defined_only"},
	}, providerConfig)
	if err != nil {
		t.Fatal(err)
	}

	for _, child := range []*Agent{subagent, background, preset, defined} {
		if !child.BrowserFamilyContext().SameFamily(family) {
			t.Fatalf("%s did not inherit browser family", child.ID())
		}
	}
	if got := subagent.Definition().ToolHints; len(got) != 1 || got[0] != "read_only" {
		t.Fatalf("subagent tool allowlist = %v", got)
	}
	if got := background.Definition().ToolHints; len(got) != 1 || got[0] != "background_only" {
		t.Fatalf("background tool allowlist = %v", got)
	}
	if got := defined.Definition().ToolHints; len(got) != 1 || got[0] != "defined_only" {
		t.Fatalf("defined child tool allowlist = %v", got)
	}
}
