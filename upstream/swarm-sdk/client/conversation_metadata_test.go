package client

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

type metadataProviderResult struct {
	content string
	err     error
}

type scriptedMetadataProvider struct {
	mu       sync.Mutex
	results  []metadataProviderResult
	requests []provider.ChatRequest
}

func (p *scriptedMetadataProvider) Name() string { return "anthropic" }
func (p *scriptedMetadataProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{}
}
func (p *scriptedMetadataProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	if len(p.results) == 0 {
		return nil, errors.New("unexpected metadata provider call")
	}
	result := p.results[0]
	p.results = p.results[1:]
	if result.err != nil {
		return nil, result.err
	}
	return &provider.ChatResponse{
		Message: &conversation.Message{Role: conversation.RoleAssistant, Content: result.content},
	}, nil
}
func (p *scriptedMetadataProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("stream should not be used for metadata")
}
func (p *scriptedMetadataProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

type blockingMetadataProvider struct {
	started chan struct{}
	release chan struct{}
}

func (p *blockingMetadataProvider) Name() string { return "anthropic" }
func (p *blockingMetadataProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{}
}
func (p *blockingMetadataProvider) Chat(ctx context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
	close(p.started)
	select {
	case <-p.release:
		return &provider.ChatResponse{Message: &conversation.Message{
			Role:    conversation.RoleAssistant,
			Content: `{"title":"Stale Generated Title","summary":"This stale summary must not overwrite a newer turn."}`,
		}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (p *blockingMetadataProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("stream should not be used")
}

type lifecycleMetadataProvider struct {
	mu            sync.Mutex
	metadataCalls int
}

func (p *lifecycleMetadataProvider) Name() string { return "anthropic" }
func (p *lifecycleMetadataProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{}
}
func (p *lifecycleMetadataProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	content := "The managed agent completed the requested work."
	if strings.Contains(req.SystemPrompt, "navigation metadata") {
		p.metadataCalls++
		content = `{"title":"Managed Agent Metadata","summary":"The managed agent received a request and completed the requested work."}`
	}
	return &provider.ChatResponse{Message: &conversation.Message{
		Role:    conversation.RoleAssistant,
		Content: content,
	}}, nil
}
func (p *lifecycleMetadataProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("stream should not be used")
}

func newMetadataTestClient(t *testing.T, p provider.Provider) (*Client, *conversation.Conversation) {
	t.Helper()
	ctx := context.Background()
	workspace := t.TempDir()
	c, err := New(
		WithProviderString("anthropic", "test-model"),
		WithProviderInstance(p),
		WithWorkspace(workspace),
		WithStorageDir(t.TempDir()),
		WithoutAutoConfig(),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	conv, err := c.CreateConversationWithOptions(ctx, manager.CreateOptions{
		Mode:          "chat",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("CreateConversationWithOptions: %v", err)
	}
	return c, conv
}

func addMetadataMessage(t *testing.T, c *Client, convID string, role conversation.Role, content string) {
	t.Helper()
	if err := c.AddMessageToConversation(context.Background(), convID, &conversation.Message{
		Role:    role,
		Content: content,
	}); err != nil {
		t.Fatalf("AddMessageToConversation: %v", err)
	}
}

func TestBuildConversationMetadataViewIgnoresGeneratedCompactionMessages(t *testing.T) {
	messages := []*conversation.Message{
		{Role: conversation.RoleUser, Content: "real user request"},
		{Role: conversation.RoleAssistant, Content: "real assistant response"},
		{
			Role:    conversation.RoleUser,
			Content: "generated handoff",
			Metadata: map[string]any{
				conversation.CompactionGeneratedMetadataKey: true,
			},
		},
		{
			Role:    conversation.RoleAssistant,
			Content: "generated restoration",
			Metadata: map[string]any{
				conversation.CompactionGeneratedMetadataKey: true,
			},
		},
	}

	view := buildConversationMetadataView(messages)
	if view.firstUser != "real user request" {
		t.Fatalf("firstUser = %q, want real user request", view.firstUser)
	}
	if view.lastAssistant != "real assistant response" {
		t.Fatalf("lastAssistant = %q, want real assistant response", view.lastAssistant)
	}
	if strings.Contains(view.prompt, "generated handoff") || strings.Contains(view.prompt, "generated restoration") {
		t.Fatalf("metadata prompt contains generated compaction context: %q", view.prompt)
	}
}

func TestRefreshConversationMetadataGeneratesAndRefreshesEveryTurn(t *testing.T) {
	p := &scriptedMetadataProvider{results: []metadataProviderResult{
		{content: `{"title":"Repair Conversation Metadata","summary":"The user asked to repair title and recap generation. The agent traced the lifecycle and prepared the shared SDK fix."}`},
		{content: `{"title":"This Must Not Replace The Stable Title","summary":"The shared SDK metadata lifecycle was implemented and verified on a second turn."}`},
	}}
	c, conv := newMetadataTestClient(t, p)
	ctx := context.Background()

	addMetadataMessage(t, c, conv.ID, conversation.RoleUser, "Fix title and summary generation for every agent.")
	afterPrompt, err := c.LoadConversation(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterPrompt.Title == "" || afterPrompt.Title == "New Chat" {
		t.Fatal("substantive prompt did not synchronously receive a fallback title")
	}

	addMetadataMessage(t, c, conv.ID, conversation.RoleAssistant, "I traced the TUI-only poller and designed an SDK-owned lifecycle.")
	if err := c.RefreshConversationMetadata(ctx, conv.ID); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	first, _ := c.LoadConversation(ctx, conv.ID)
	if first.Title != "Repair Conversation Metadata" {
		t.Fatalf("title = %q", first.Title)
	}
	if first.Summary == nil || first.Summary.Recap == "" {
		t.Fatal("missing generated recap")
	}
	listed, err := c.ListConversations(ctx, ListOptions{WorkspacePath: first.WorkspacePath})
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(listed) != 1 || listed[0].Recap != first.Summary.Recap {
		t.Fatalf("generated recap missing from listing contract: %#v", listed)
	}
	addMetadataMessage(t, c, conv.ID, conversation.RoleTool, "post-turn bookkeeping")
	afterTool, _ := c.LoadConversation(ctx, conv.ID)
	if stateString(conversationMetadataState(afterTool), "status") != "generated" ||
		afterTool.Summary == nil || afterTool.Summary.Recap != first.Summary.Recap {
		t.Fatalf("non-substantive message downgraded generated metadata: %#v", afterTool.Summary)
	}

	addMetadataMessage(t, c, conv.ID, conversation.RoleUser, "Implement and verify it.")
	addMetadataMessage(t, c, conv.ID, conversation.RoleAssistant, "Implemented the shared lifecycle and all focused tests pass.")
	if err := c.RefreshConversationMetadata(ctx, conv.ID); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	second, _ := c.LoadConversation(ctx, conv.ID)
	if second.Title != first.Title {
		t.Fatalf("stable generated title changed from %q to %q", first.Title, second.Title)
	}
	if second.Summary == nil || second.Summary.Recap != "The shared SDK metadata lifecycle was implemented and verified on a second turn." {
		t.Fatalf("recap was not refreshed: %#v", second.Summary)
	}
	if got := p.callCount(); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}

	// The same message version is idempotent.
	if err := c.RefreshConversationMetadata(ctx, conv.ID); err != nil {
		t.Fatalf("idempotent refresh: %v", err)
	}
	if got := p.callCount(); got != 2 {
		t.Fatalf("idempotent refresh made provider call %d", got)
	}
}

func TestRefreshConversationMetadataFailurePersistsFallbackAndRetries(t *testing.T) {
	p := &scriptedMetadataProvider{results: []metadataProviderResult{
		{err: errors.New("provider unavailable")},
		{content: "```json\n{\"title\":\"Reliable Metadata Retry\",\"summary\":\"The first metadata call failed, retained a useful fallback, and the retry succeeded.\"}\n```"},
	}}
	c, conv := newMetadataTestClient(t, p)
	ctx := context.Background()
	addMetadataMessage(t, c, conv.ID, conversation.RoleUser, "Make metadata failures retryable.")
	addMetadataMessage(t, c, conv.ID, conversation.RoleAssistant, "The fallback remains useful when the provider fails.")

	if err := c.RefreshConversationMetadata(ctx, conv.ID); err == nil {
		t.Fatal("expected provider failure")
	}
	fallback, _ := c.LoadConversation(ctx, conv.ID)
	if fallback.Title == "" || fallback.Summary == nil || fallback.Summary.Recap == "" {
		t.Fatalf("fallback metadata missing: title=%q summary=%#v", fallback.Title, fallback.Summary)
	}
	state := conversationMetadataState(fallback)
	if stateString(state, "last_error") == "" || stateString(state, "status") != "fallback" {
		t.Fatalf("failure status not persisted: %#v", state)
	}

	if err := c.RefreshConversationMetadata(ctx, conv.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	retried, _ := c.LoadConversation(ctx, conv.ID)
	if retried.Title != "Reliable Metadata Retry" {
		t.Fatalf("retry title = %q", retried.Title)
	}
	if stateString(conversationMetadataState(retried), "status") != "generated" {
		t.Fatalf("retry status = %#v", conversationMetadataState(retried))
	}
}

func TestConversationMetadataSkipsMachineNoiseAndPreservesManualTitle(t *testing.T) {
	p := &scriptedMetadataProvider{results: []metadataProviderResult{
		{content: `{"title":"Model Tried Replacement","summary":"The real user request was summarized without injected task-reminder noise."}`},
	}}
	c, conv := newMetadataTestClient(t, p)
	ctx := context.Background()
	addMetadataMessage(t, c, conv.ID, conversation.RoleUser,
		`<system-reminder source="user_prompt_submit">[Task Nudge] Track your work with tasks.</system-reminder>`)
	machineOnly, _ := c.LoadConversation(ctx, conv.ID)
	if machineOnly.Title != "" {
		t.Fatalf("machine-only message produced title %q", machineOnly.Title)
	}
	addMetadataMessage(t, c, conv.ID, conversation.RoleUser,
		`<swarm_runtime_guidance>hidden runtime configuration</swarm_runtime_guidance>Improve conversation discovery.`)
	if err := c.SetConversationTitle(ctx, conv.ID, "My Deliberate Title"); err != nil {
		t.Fatal(err)
	}
	addMetadataMessage(t, c, conv.ID, conversation.RoleAssistant, "I improved the metadata lifecycle.")
	if err := c.RefreshConversationMetadata(ctx, conv.ID); err != nil {
		t.Fatal(err)
	}
	loaded, _ := c.LoadConversation(ctx, conv.ID)
	if loaded.Title != "My Deliberate Title" {
		t.Fatalf("manual title overwritten: %q", loaded.Title)
	}
	if loaded.Summary == nil || stringsContainsFold(loaded.Summary.Recap, "task nudge") {
		t.Fatalf("machine noise leaked into recap: %#v", loaded.Summary)
	}
}

func TestRefreshConversationMetadataDiscardsStaleProviderResult(t *testing.T) {
	p := &blockingMetadataProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	c, conv := newMetadataTestClient(t, p)
	ctx := context.Background()
	addMetadataMessage(t, c, conv.ID, conversation.RoleUser, "Summarize the first turn.")
	addMetadataMessage(t, c, conv.ID, conversation.RoleAssistant, "First turn result.")

	done := make(chan error, 1)
	go func() {
		done <- c.RefreshConversationMetadata(ctx, conv.ID)
	}()
	<-p.started

	// Message persistence must not wait for the metadata provider call.
	addMetadataMessage(t, c, conv.ID, conversation.RoleUser, "A newer turn arrived.")
	addMetadataMessage(t, c, conv.ID, conversation.RoleAssistant, "This is the newer result.")
	close(p.release)
	if err := <-done; err != nil {
		t.Fatalf("stale refresh: %v", err)
	}

	loaded, _ := c.LoadConversation(ctx, conv.ID)
	if loaded.Title == "Stale Generated Title" {
		t.Fatal("stale title overwrote newer fallback")
	}
	if loaded.Summary == nil || strings.Contains(loaded.Summary.Recap, "stale summary") {
		t.Fatalf("stale recap overwrote newer version: %#v", loaded.Summary)
	}
	if !strings.Contains(loaded.Summary.Recap, "newer result") {
		t.Fatalf("newer fallback recap missing: %q", loaded.Summary.Recap)
	}
}

func TestManagedSendMessageRunsMandatoryMetadataLifecycle(t *testing.T) {
	p := &lifecycleMetadataProvider{}
	workspace := t.TempDir()
	c, err := New(
		WithProviderString("anthropic", "test-model"),
		WithProviderInstance(p),
		WithWorkspace(workspace),
		WithStorageDir(t.TempDir()),
		WithClientType(ClientTypeManaged),
		WithoutAutoConfig(),
	)
	if err != nil {
		t.Fatalf("New managed client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if err := c.SendMessage(context.Background(), "", "Complete managed-agent work.", SendMessageOptions{}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	convID := c.ActiveConversation()
	loaded, err := c.LoadConversation(context.Background(), convID)
	if err != nil {
		t.Fatalf("LoadConversation: %v", err)
	}
	if loaded.Title != "Managed Agent Metadata" {
		t.Fatalf("managed title = %q", loaded.Title)
	}
	if loaded.Summary == nil || loaded.Summary.Recap == "" {
		t.Fatal("managed conversation did not receive recap")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.metadataCalls != 1 {
		t.Fatalf("managed metadata calls = %d, want 1", p.metadataCalls)
	}
}

func TestAgentInstanceRetainsProviderForMetadataGeneration(t *testing.T) {
	p := &scriptedMetadataProvider{results: []metadataProviderResult{
		{content: `{"title":"TUI Provider Retained","summary":"The prebuilt TUI agent retained its provider for metadata generation."}`},
	}}
	ignored := &scriptedMetadataProvider{results: []metadataProviderResult{
		{content: `{"title":"Wrong Provider","summary":"WithProviderInstance must be ignored when a prebuilt agent is supplied."}`},
	}}
	base, _ := newMetadataTestClient(t, p)
	workspace := t.TempDir()
	c, err := New(
		WithProviderString("anthropic", "test-model"),
		WithProviderInstance(ignored),
		WithAgentInstance(base.agent),
		WithWorkspace(workspace),
		WithStorageDir(t.TempDir()),
		WithoutAutoConfig(),
	)
	if err != nil {
		t.Fatalf("New agent-instance client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	conv, err := c.CreateConversationWithOptions(context.Background(), manager.CreateOptions{
		Mode:          "chat",
		WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	addMetadataMessage(t, c, conv.ID, conversation.RoleUser, "Keep the TUI provider available.")
	addMetadataMessage(t, c, conv.ID, conversation.RoleAssistant, "The provider remains available.")
	if err := c.RefreshConversationMetadata(context.Background(), conv.ID); err != nil {
		t.Fatalf("RefreshConversationMetadata: %v", err)
	}
	loaded, _ := c.LoadConversation(context.Background(), conv.ID)
	if loaded.Title != "TUI Provider Retained" {
		t.Fatalf("agent-instance metadata title = %q", loaded.Title)
	}
}

func TestPersistedGeneratingStateRetriesWithoutLocalRun(t *testing.T) {
	p := &scriptedMetadataProvider{results: []metadataProviderResult{
		{content: `{"title":"Recovered Metadata Run","summary":"A persisted generating marker from an interrupted process was retried."}`},
	}}
	c, conv := newMetadataTestClient(t, p)
	ctx := context.Background()
	addMetadataMessage(t, c, conv.ID, conversation.RoleUser, "Recover interrupted metadata generation.")
	addMetadataMessage(t, c, conv.ID, conversation.RoleAssistant, "The prior process stopped before completion.")
	loaded, _ := c.LoadConversation(ctx, conv.ID)
	view := buildConversationMetadataView(loaded.Messages)
	state := conversationMetadataState(loaded)
	state["version"] = view.version
	state["status"] = "generating"
	if err := c.SaveConversation(ctx, loaded); err != nil {
		t.Fatal(err)
	}

	if err := c.RefreshConversationMetadata(ctx, conv.ID); err != nil {
		t.Fatalf("retry persisted generating state: %v", err)
	}
	recovered, _ := c.LoadConversation(ctx, conv.ID)
	if recovered.Title != "Recovered Metadata Run" ||
		stateString(conversationMetadataState(recovered), "status") != "generated" {
		t.Fatalf("persisted generating state was not recovered: title=%q state=%#v",
			recovered.Title, conversationMetadataState(recovered))
	}
}

func TestMetadataUpdateEventCanReenterRefresh(t *testing.T) {
	p := &scriptedMetadataProvider{results: []metadataProviderResult{
		{content: `{"title":"Reentrant Event Safe","summary":"Conversation update handlers can safely re-enter metadata refresh."}`},
	}}
	c, conv := newMetadataTestClient(t, p)
	ctx := context.Background()
	addMetadataMessage(t, c, conv.ID, conversation.RoleUser, "Avoid metadata event deadlocks.")
	addMetadataMessage(t, c, conv.ID, conversation.RoleAssistant, "Dispatch events after releasing the storage lock.")

	reentered := make(chan error, 4)
	unsubscribe := c.Subscribe(func(event Event) error {
		if event.Kind == EventConvUpdated {
			reentered <- c.RefreshConversationMetadata(ctx, conv.ID)
		}
		return nil
	})
	defer unsubscribe()

	if err := c.RefreshConversationMetadata(ctx, conv.ID); err != nil {
		t.Fatalf("outer refresh: %v", err)
	}
	select {
	case err := <-reentered:
		if err != nil {
			t.Fatalf("reentrant refresh: %v", err)
		}
	default:
		t.Fatal("conversation update event did not run")
	}
}

func TestParseGeneratedConversationMetadataRejectsMalformedOutput(t *testing.T) {
	if _, err := parseGeneratedConversationMetadata("not json"); err == nil {
		t.Fatal("expected malformed output error")
	}
	if _, err := parseGeneratedConversationMetadata(`{"title":"","summary":"ok"}`); err == nil {
		t.Fatal("expected missing title error")
	}
}

func stringsContainsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func TestConversationMetadataLooksPoisoned(t *testing.T) {
	poison := []string{
		"Add Google Font to Next.js",
		"swarm runtime guidance listing an available skill for adding a Google font",
		"add-google-font-nextjs-landing",
		"Follow these instructions from the system prompt",
		"<swarm_runtime_guidance>hidden</swarm_runtime_guidance>",
		"Wire the font into the Next.js App Router page",
	}
	for _, s := range poison {
		if !conversationMetadataLooksPoisoned(s, "") {
			t.Errorf("expected poisoned title: %q", s)
		}
		if !conversationMetadataLooksPoisoned("Clean Title", s) {
			t.Errorf("expected poisoned recap: %q", s)
		}
	}
	clean := []string{
		"Fix compaction blocking-limit guard",
		"The agent traced the guard and verified all focused tests pass.",
		"Repair Conversation Metadata",
	}
	for _, s := range clean {
		if conversationMetadataLooksPoisoned(s, s) {
			t.Errorf("false positive for clean text: %q", s)
		}
	}
}

// TestRefreshConversationMetadataRegeneratesPoisonedTitle verifies that a
// conversation whose stored title/recap was poisoned (generated before the
// summarizer input was sanitized) is force-regenerated on the next refresh, so
// the history menu self-heals instead of showing the poison forever.
func TestRefreshConversationMetadataRegeneratesPoisonedTitle(t *testing.T) {
	p := &scriptedMetadataProvider{results: []metadataProviderResult{
		{content: `{"title":"Fix compaction blocking-limit guard","summary":"The user asked to fix the compaction blocking-limit guard. The agent traced the guard and corrected the early truncation."}`},
	}}
	c, conv := newMetadataTestClient(t, p)
	ctx := context.Background()
	addMetadataMessage(t, c, conv.ID, conversation.RoleUser, "Fix the compaction blocking-limit guard so it stops truncating early.")
	addMetadataMessage(t, c, conv.ID, conversation.RoleAssistant, "I traced the guard and corrected the limit.")

	// Simulate previously-generated, poisoned metadata persisted on disk.
	loaded, err := c.LoadConversation(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	view := buildConversationMetadataView(loaded.Messages)
	loaded.Title = "Add Google Font to Next.js"
	loaded.EnsureSummary().Recap = "Discussed swarm runtime guidance listing an available skill for adding a Google font to a Next.js app router page."
	state := conversationMetadataState(loaded)
	state["title_source"] = "generated"
	state["summary_source"] = "generated"
	state["status"] = "generated"
	state["version"] = view.version
	if err := c.SaveConversation(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	if !conversationMetadataLooksPoisoned(loaded.Title, loaded.Summary.Recap) {
		t.Fatal("test setup should be poisoned")
	}

	if err := c.RefreshConversationMetadata(ctx, conv.ID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	healed, _ := c.LoadConversation(ctx, conv.ID)
	if conversationMetadataLooksPoisoned(healed.Title, "") {
		t.Fatalf("title still poisoned after refresh: %q", healed.Title)
	}
	if healed.Title != "Fix compaction blocking-limit guard" {
		t.Fatalf("title = %q, want regenerated clean title", healed.Title)
	}
	if healed.Summary == nil || conversationMetadataLooksPoisoned("", healed.Summary.Recap) {
		t.Fatalf("recap still poisoned: %#v", healed.Summary)
	}
	if got := p.callCount(); got != 1 {
		t.Fatalf("provider calls = %d, want 1 (regeneration should have run)", got)
	}
}

// TestRefreshConversationMetadataPreservesManualFontTitle verifies that a
// deliberate user-set (manual) title mentioning fonts is never treated as poison
// and never overwritten, even though its text matches a poison marker.
func TestRefreshConversationMetadataPreservesManualFontTitle(t *testing.T) {
	p := &scriptedMetadataProvider{results: []metadataProviderResult{
		{content: `{"title":"Regenerated Clean Title","summary":"A regenerated clean summary of the discussion."}`},
	}}
	c, conv := newMetadataTestClient(t, p)
	ctx := context.Background()
	addMetadataMessage(t, c, conv.ID, conversation.RoleUser, "Help me pick a Google font for the marketing landing page.")
	addMetadataMessage(t, c, conv.ID, conversation.RoleAssistant, "I recommended a font pairing for the landing page.")

	loaded, err := c.LoadConversation(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Title = "Add Google Font to Landing Page"
	state := conversationMetadataState(loaded)
	state["title_source"] = "manual"
	if err := c.SaveConversation(ctx, loaded); err != nil {
		t.Fatal(err)
	}

	if err := c.RefreshConversationMetadata(ctx, conv.ID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	after, _ := c.LoadConversation(ctx, conv.ID)
	if after.Title != "Add Google Font to Landing Page" {
		t.Fatalf("manual title was overwritten: %q", after.Title)
	}
}
