package context

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
)

type stubConfigLoader struct {
	config ContextConfig
}

func (s *stubConfigLoader) LoadContext(config ContextConfig, workDir string) (*LoadedContext, error) {
	return &LoadedContext{}, nil
}

func (s *stubConfigLoader) GetConfig() ContextConfig {
	return s.config
}

func (s *stubConfigLoader) SetConfig(config ContextConfig) error {
	s.config = config
	return nil
}

type stubMCPProvider struct {
	mu     sync.Mutex
	counts map[string]int
}

func (s *stubMCPProvider) ReadResource(ctx context.Context, serverName, uri string) (*mcp.ResourceContents, error) {
	key := serverName + "|" + uri
	s.mu.Lock()
	if s.counts == nil {
		s.counts = make(map[string]int)
	}
	s.counts[key]++
	count := s.counts[key]
	s.mu.Unlock()
	return &mcp.ResourceContents{Text: fmt.Sprintf("%s:%d", uri, count)}, nil
}

func (s *stubMCPProvider) GetPrompt(ctx context.Context, serverName, promptName string, args map[string]string) (*mcp.PromptResult, error) {
	return &mcp.PromptResult{}, nil
}

func (s *stubMCPProvider) Count(serverName, uri string) int {
	key := serverName + "|" + uri
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[key]
}

func TestOrchestratorPerSourceTTL(t *testing.T) {
	provider := &stubMCPProvider{}
	base := time.Date(2026, 2, 5, 0, 0, 0, 0, time.UTC)
	now := base
	nowFn := func() time.Time { return now }

	ttlSeconds := 10
	cfg := ContextConfig{
		Sources: []ContextSourceConfig{
			{
				ID:          "mcp_a",
				Kind:        SourceKindMCPResource,
				Enabled:     true,
				RefreshMode: RefreshTTL,
				TTLSeconds:  &ttlSeconds,
				CachePolicy: CacheEphemeral,
				ServerName:  "srv",
				URI:         "resource://a",
			},
			{
				ID:          "mcp_b",
				Kind:        SourceKindMCPResource,
				Enabled:     true,
				RefreshMode: RefreshEveryTurn,
				CachePolicy: CacheEphemeral,
				ServerName:  "srv",
				URI:         "resource://b",
			},
		},
	}

	loader := &stubConfigLoader{config: cfg}
	orch := NewContextOrchestrator(cfg, ".", loader, provider, nil)

	opts := ContextBlockOptions{RunID: "run-1", Now: nowFn}
	_ = orch.GetContextBlock(context.Background(), opts)

	now = base.Add(5 * time.Second)
	_ = orch.GetContextBlock(context.Background(), opts)

	if provider.Count("srv", "resource://a") != 1 {
		t.Fatalf("expected ttl source to refresh once within ttl")
	}
	if provider.Count("srv", "resource://b") != 2 {
		t.Fatalf("expected every_turn source to refresh twice, got %d", provider.Count("srv", "resource://b"))
	}

	now = base.Add(11 * time.Second)
	_ = orch.GetContextBlock(context.Background(), opts)

	if provider.Count("srv", "resource://a") != 2 {
		t.Fatalf("expected ttl source to refresh after ttl, got %d", provider.Count("srv", "resource://a"))
	}
}

func TestOrchestratorEveryMessagePerSource(t *testing.T) {
	provider := &stubMCPProvider{}
	cfg := ContextConfig{
		Sources: []ContextSourceConfig{
			{
				ID:          "mcp_run",
				Kind:        SourceKindMCPResource,
				Enabled:     true,
				RefreshMode: RefreshEveryMessage,
				CachePolicy: CacheEphemeral,
				ServerName:  "srv",
				URI:         "resource://run",
			},
		},
	}
	loader := &stubConfigLoader{config: cfg}
	orch := NewContextOrchestrator(cfg, ".", loader, provider, nil)

	opts := ContextBlockOptions{RunID: "run-1"}
	_ = orch.GetContextBlock(context.Background(), opts)
	_ = orch.GetContextBlock(context.Background(), opts)
	if provider.Count("srv", "resource://run") != 1 {
		t.Fatalf("expected every_message source to refresh once per run_id")
	}

	opts.RunID = "run-2"
	_ = orch.GetContextBlock(context.Background(), opts)
	if provider.Count("srv", "resource://run") != 2 {
		t.Fatalf("expected every_message source to refresh on new run_id")
	}
}

func TestOrchestratorOnChangeFileSource(t *testing.T) {
	dir := t.TempDir()
	swarmDir := filepath.Join(dir, ".swarm")
	if err := os.MkdirAll(swarmDir, 0755); err != nil {
		t.Fatalf("failed to create .swarm dir: %v", err)
	}

	filePath := filepath.Join(swarmDir, "SWARM.md")
	if err := os.WriteFile(filePath, []byte("one"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	cfg := ContextConfig{
		Sources: []ContextSourceConfig{
			{
				ID:          SourceIDProjectSwarmMd,
				Enabled:     true,
				RefreshMode: RefreshOnChange,
				CachePolicy: CacheCached,
			},
		},
	}
	loader := &stubConfigLoader{config: cfg}
	orch := NewContextOrchestrator(cfg, dir, loader, nil, nil)

	first := orch.GetContextBlock(context.Background(), ContextBlockOptions{})
	if !strings.Contains(first, "one") {
		t.Fatalf("expected initial content to be loaded")
	}

	if err := os.Chmod(filePath, 0000); err != nil {
		t.Fatalf("failed to chmod file: %v", err)
	}
	second := orch.GetContextBlock(context.Background(), ContextBlockOptions{})
	if !strings.Contains(second, "one") {
		t.Fatalf("expected cached content when file unchanged")
	}

	if err := os.Chmod(filePath, 0644); err != nil {
		t.Fatalf("failed to reset chmod: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("two"), 0644); err != nil {
		t.Fatalf("failed to update file: %v", err)
	}
	third := orch.GetContextBlock(context.Background(), ContextBlockOptions{})
	if !strings.Contains(third, "two") {
		t.Fatalf("expected updated content after change")
	}

	if err := os.Remove(filePath); err != nil {
		t.Fatalf("failed to remove file: %v", err)
	}
	fourth := orch.GetContextBlock(context.Background(), ContextBlockOptions{})
	if strings.Contains(fourth, "two") || strings.Contains(fourth, "one") {
		t.Fatalf("expected content to be removed when file missing")
	}
}

func TestOrchestratorCachePolicySplit(t *testing.T) {
	previousNow := nowFunc
	nowFunc = func() time.Time {
		return time.Date(2026, 2, 5, 12, 0, 0, 0, time.UTC)
	}
	defer func() { nowFunc = previousNow }()

	cfg := ContextConfig{
		Sources: []ContextSourceConfig{
			{
				ID:          SourceIDProjectName,
				Enabled:     true,
				CachePolicy: CacheCached,
			},
			{
				ID:          SourceIDCurrentDate,
				Enabled:     true,
				CachePolicy: CacheEphemeral,
			},
		},
	}
	loader := &stubConfigLoader{config: cfg}
	orch := NewContextOrchestrator(cfg, "/tmp/swarm-dot-dev", loader, nil, nil)

	block := orch.GetContextBlock(context.Background(), ContextBlockOptions{})
	cached := extractTagBlock(block, CachedContextStartTag, CachedContextEndTag)
	ephemeral := extractTagBlock(block, ContextStartTag, ContextEndTag)

	if cached == "" || ephemeral == "" {
		t.Fatalf("expected both cached and ephemeral blocks")
	}
	if !strings.Contains(cached, "projectName") {
		t.Fatalf("expected cached block to include project name")
	}
	if !strings.Contains(ephemeral, "currentDate") {
		t.Fatalf("expected ephemeral block to include current date")
	}
}

func extractTagBlock(text, startTag, endTag string) string {
	_, after, ok := strings.Cut(text, startTag)
	if !ok {
		return ""
	}
	remaining := after
	before, _, ok := strings.Cut(remaining, endTag)
	if !ok {
		return ""
	}
	return before
}
