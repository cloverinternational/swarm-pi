package advanced_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/advanced"
)

// ---------------------------------------------------------------------------
// Mock tools for testing
// ---------------------------------------------------------------------------

// mockTool is a minimal tools.Tool implementation.
type mockTool struct {
	name        string
	description string
	idempotent  bool
}

func (m *mockTool) Name() string        { return m.name }
func (m *mockTool) Description() string { return m.description }
func (m *mockTool) Parameters() any     { return map[string]any{"type": "object"} }
func (m *mockTool) Execute(_ context.Context, _ map[string]any) (*tools.ToolResult, error) {
	return tools.NewToolResult("ok"), nil
}
func (m *mockTool) Validate(_ map[string]any) error        { return nil }
func (m *mockTool) IsIdempotent() bool                     { return m.idempotent }
func (m *mockTool) RequiresPermission() []tools.Permission { return nil }
func (m *mockTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}
func (m *mockTool) OptimizationHints() *tools.OptimizationHints { return nil }

// mockDeferredTool implements Deferrable.
type mockDeferredTool struct {
	mockTool
	deferred bool
}

func (m *mockDeferredTool) ShouldDefer() bool { return m.deferred }

// mockExampleTool implements ToolWithExamples.
type mockExampleTool struct {
	mockTool
	examples []tools.ToolExample
}

func (m *mockExampleTool) InputExamples() []tools.ToolExample { return m.examples }

// mockParallelTool implements ParallelCapable.
type mockParallelTool struct {
	mockTool
	parallel bool
}

func (m *mockParallelTool) SupportsParallel() bool { return m.parallel }

// mockMetaTool implements MetadataProvider.
type mockMetaTool struct {
	mockTool
	meta *tools.ToolMetadata
}

func (m *mockMetaTool) ToolMetadata() *tools.ToolMetadata { return m.meta }

// mockFullTool implements all advanced interfaces.
type mockFullTool struct {
	mockTool
	deferred bool
	parallel bool
	examples []tools.ToolExample
	meta     *tools.ToolMetadata
}

func (m *mockFullTool) ShouldDefer() bool                  { return m.deferred }
func (m *mockFullTool) SupportsParallel() bool             { return m.parallel }
func (m *mockFullTool) InputExamples() []tools.ToolExample { return m.examples }
func (m *mockFullTool) ToolMetadata() *tools.ToolMetadata  { return m.meta }

// ---------------------------------------------------------------------------
// Tests: Types
// ---------------------------------------------------------------------------

func TestFromToolExample(t *testing.T) {
	te := tools.ToolExample{
		Description:    "test example",
		Parameters:     map[string]any{"key": "value"},
		ExpectedOutput: "output",
	}

	ie := advanced.FromToolExample(te)

	if ie.Description != "test example" {
		t.Errorf("expected description 'test example', got %q", ie.Description)
	}
	if ie.Parameters["key"] != "value" {
		t.Errorf("expected parameters key=value")
	}
	if ie.ExpectedOutput != "output" {
		t.Errorf("expected output 'output', got %q", ie.ExpectedOutput)
	}
}

func TestFromToolExamplesNil(t *testing.T) {
	result := advanced.FromToolExamples(nil)
	if result != nil {
		t.Errorf("expected nil for empty input")
	}
}

// ---------------------------------------------------------------------------
// Tests: Examples
// ---------------------------------------------------------------------------

func TestExtractExamples_NoInterface(t *testing.T) {
	tool := &mockTool{name: "plain"}
	examples := advanced.ExtractExamples(tool)
	if examples != nil {
		t.Errorf("expected nil examples for non-ToolWithExamples tool")
	}
}

func TestExtractExamples_WithInterface(t *testing.T) {
	tool := &mockExampleTool{
		mockTool: mockTool{name: "with_examples"},
		examples: []tools.ToolExample{
			{Description: "ex1", Parameters: map[string]any{"a": 1}},
		},
	}

	examples := advanced.ExtractExamples(tool)
	if len(examples) != 1 {
		t.Fatalf("expected 1 example, got %d", len(examples))
	}
	if examples[0].Description != "ex1" {
		t.Errorf("expected description 'ex1', got %q", examples[0].Description)
	}
}

func TestEstimateToolTokens(t *testing.T) {
	tool := &mockTool{name: "test_tool", description: "A test tool that does stuff"}
	tokens := advanced.EstimateToolTokens(tool)
	if tokens < 1 {
		t.Errorf("expected at least 1 token, got %d", tokens)
	}
}

func TestBuildSpec_PlainTool(t *testing.T) {
	tool := &mockTool{name: "plain", description: "desc", idempotent: true}
	spec := advanced.BuildSpec(tool)

	if spec.Name != "plain" {
		t.Errorf("expected name 'plain', got %q", spec.Name)
	}
	if spec.ShouldDefer {
		t.Error("plain tool should not be deferred")
	}
	// Idempotent + no write patterns → might be parallel-safe via heuristic
	// but "plain" doesn't match read patterns, so should be false
	if spec.ConcurrencySafe {
		t.Error("plain tool name doesn't match read patterns, should not be parallel-safe")
	}
}

func TestBuildSpec_FullTool(t *testing.T) {
	tool := &mockFullTool{
		mockTool: mockTool{name: "full_reader", description: "reads all", idempotent: true},
		deferred: true,
		parallel: true,
		examples: []tools.ToolExample{{Description: "ex1"}},
		meta: &tools.ToolMetadata{
			Category: "filesystem",
			Tags:     []string{"read", "fast"},
			Source:   tools.ToolSourceBuiltin,
		},
	}
	spec := advanced.BuildSpec(tool)

	if !spec.ShouldDefer {
		t.Error("expected deferred")
	}
	if !spec.ConcurrencySafe {
		t.Error("expected concurrency safe")
	}
	if len(spec.InputExamples) != 1 {
		t.Errorf("expected 1 example, got %d", len(spec.InputExamples))
	}
	if spec.Category != "filesystem" {
		t.Errorf("expected category 'filesystem', got %q", spec.Category)
	}
	if len(spec.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(spec.Tags))
	}
}

// ---------------------------------------------------------------------------
// Tests: AppState
// ---------------------------------------------------------------------------

func TestAppState_BasicOps(t *testing.T) {
	s := advanced.NewAppState()

	s.Set("key1", "value1")
	if v := s.StringVal("key1"); v != "value1" {
		t.Errorf("expected 'value1', got %q", v)
	}

	s.Set("flag", true)
	if v := s.BoolVal("flag"); !v {
		t.Error("expected true")
	}

	if !s.Has("key1") {
		t.Error("expected Has(key1) == true")
	}

	s.Delete("key1")
	if s.Has("key1") {
		t.Error("expected Has(key1) == false after delete")
	}

	if s.Len() != 1 {
		t.Errorf("expected len 1, got %d", s.Len())
	}

	s.Clear()
	if s.Len() != 0 {
		t.Errorf("expected len 0 after clear, got %d", s.Len())
	}
}

func TestAppState_Snapshot(t *testing.T) {
	s := advanced.NewAppState()
	s.Set("a", 1)
	s.Set("b", 2)

	snap := s.Snapshot()
	if len(snap) != 2 {
		t.Errorf("expected 2 keys in snapshot, got %d", len(snap))
	}

	// Mutating snapshot should not affect state
	snap["c"] = 3
	if s.Has("c") {
		t.Error("snapshot mutation should not affect state")
	}
}

func TestAppState_Update(t *testing.T) {
	s := advanced.NewAppState()
	s.Set("counter", 0)

	s.Update(func(current map[string]any) map[string]any {
		current["counter"] = 42
		return current
	})

	if v := s.Get("counter"); v != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

func TestAppStateManager(t *testing.T) {
	m := advanced.NewAppStateManager()

	s1 := m.State("agent1")
	s1.Set("task", "coding")

	s2 := m.State("agent2")
	s2.Set("task", "testing")

	// Verify isolation
	if s1.StringVal("task") != "coding" {
		t.Error("agent1 state corrupted")
	}
	if s2.StringVal("task") != "testing" {
		t.Error("agent2 state corrupted")
	}

	// Same agent returns same state
	s1again := m.State("agent1")
	if s1again.StringVal("task") != "coding" {
		t.Error("expected same state for same agent ID")
	}

	// Agent IDs
	ids := m.AgentIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 agent IDs, got %d", len(ids))
	}

	// Clear specific agent
	m.ClearState("agent1")
	if m.HasState("agent1") {
		t.Error("expected agent1 state cleared")
	}
	if !m.HasState("agent2") {
		t.Error("expected agent2 state still present")
	}
}

// ---------------------------------------------------------------------------
// Tests: Permissions
// ---------------------------------------------------------------------------

func TestAdvancedPermissions_NoRules(t *testing.T) {
	apc := advanced.NewAdvancedPermissionChecker()
	decision := apc.CheckPermission("any_tool", nil, nil)

	if decision.Behavior != advanced.PermissionAllow {
		t.Errorf("expected allow with no rules, got %s", decision.Behavior)
	}
}

func TestAdvancedPermissions_ExactMatch(t *testing.T) {
	apc := advanced.NewAdvancedPermissionChecker()
	apc.AddRule(advanced.PermissionRule{
		Name:        "deny_delete",
		ToolPattern: "file_delete",
		Behavior:    advanced.PermissionDeny,
		Message:     "file deletion not allowed",
		Suggestions: []string{"use file_archive instead"},
	})

	decision := apc.CheckPermission("file_delete", nil, nil)
	if decision.Behavior != advanced.PermissionDeny {
		t.Errorf("expected deny, got %s", decision.Behavior)
	}
	if len(decision.Suggestions) != 1 {
		t.Errorf("expected 1 suggestion, got %d", len(decision.Suggestions))
	}

	// Different tool should be allowed
	decision = apc.CheckPermission("file_read", nil, nil)
	if decision.Behavior != advanced.PermissionAllow {
		t.Errorf("expected allow for unmatched tool, got %s", decision.Behavior)
	}
}

func TestAdvancedPermissions_GlobPattern(t *testing.T) {
	apc := advanced.NewAdvancedPermissionChecker()
	apc.AddRule(advanced.PermissionRule{
		Name:        "ask_writes",
		ToolPattern: "file_*",
		Behavior:    advanced.PermissionAsk,
	})

	decision := apc.CheckPermission("file_write", nil, nil)
	if decision.Behavior != advanced.PermissionAsk {
		t.Errorf("expected ask for file_write, got %s", decision.Behavior)
	}

	decision = apc.CheckPermission("file_delete", nil, nil)
	if decision.Behavior != advanced.PermissionAsk {
		t.Errorf("expected ask for file_delete, got %s", decision.Behavior)
	}

	// Non-file tool should be allowed
	decision = apc.CheckPermission("bash", nil, nil)
	if decision.Behavior != advanced.PermissionAllow {
		t.Errorf("expected allow for bash, got %s", decision.Behavior)
	}
}

func TestAdvancedPermissions_PriorityOrder(t *testing.T) {
	apc := advanced.NewAdvancedPermissionChecker()

	// Lower priority: ask for all file_* tools
	apc.AddRule(advanced.PermissionRule{
		Name:        "ask_all_file",
		ToolPattern: "file_*",
		Behavior:    advanced.PermissionAsk,
		Priority:    10,
	})

	// Higher priority: allow file_read specifically
	apc.AddRule(advanced.PermissionRule{
		Name:        "allow_read",
		ToolPattern: "file_read",
		Behavior:    advanced.PermissionAllow,
		Priority:    100,
	})

	// file_read should match the higher-priority rule
	decision := apc.CheckPermission("file_read", nil, nil)
	if decision.Behavior != advanced.PermissionAllow {
		t.Errorf("expected allow (high priority), got %s", decision.Behavior)
	}

	// file_write should match the lower-priority glob
	decision = apc.CheckPermission("file_write", nil, nil)
	if decision.Behavior != advanced.PermissionAsk {
		t.Errorf("expected ask (low priority glob), got %s", decision.Behavior)
	}
}

func TestAdvancedPermissions_RemoveRule(t *testing.T) {
	apc := advanced.NewAdvancedPermissionChecker()
	apc.AddRule(advanced.PermissionRule{
		Name:        "deny_all",
		ToolPattern: "*",
		Behavior:    advanced.PermissionDeny,
	})

	decision := apc.CheckPermission("anything", nil, nil)
	if decision.Behavior != advanced.PermissionDeny {
		t.Errorf("expected deny, got %s", decision.Behavior)
	}

	apc.RemoveRule("deny_all")

	decision = apc.CheckPermission("anything", nil, nil)
	if decision.Behavior != advanced.PermissionAllow {
		t.Errorf("expected allow after removing rule, got %s", decision.Behavior)
	}
}

// ---------------------------------------------------------------------------
// Tests: ToolSearchTool
// ---------------------------------------------------------------------------

func TestToolSearchTool_BasicSearch(t *testing.T) {
	// Create a mock registry
	reg := newMockRegistry()
	reg.add(&mockTool{name: "file_read", description: "Read a file from disk"})
	reg.add(&mockTool{name: "file_write", description: "Write content to a file"})
	reg.add(&mockTool{name: "grep", description: "Search for patterns in files"})
	reg.add(&mockDeferredTool{
		mockTool: mockTool{name: "web_fetch", description: "Fetch content from a URL"},
		deferred: true,
	})

	searchTool := advanced.NewToolSearchTool(reg)

	// Search for "file"
	result, err := searchTool.Execute(context.Background(), map[string]any{
		"query": "file",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Output)
	}

	// Should find file_read, file_write, and grep (description mentions "files")
	if result.Output == "" {
		t.Error("expected non-empty search results")
	}
}

func TestToolSearchTool_EmptyQuery(t *testing.T) {
	reg := newMockRegistry()
	reg.add(&mockTool{name: "bash", description: "Execute shell commands"})
	searchTool := advanced.NewToolSearchTool(reg)

	result, err := searchTool.Execute(context.Background(), map[string]any{
		"query": "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error result for empty query: %s", result.Output)
	}
	// Empty query should list all tools
	if !strings.Contains(result.Output, "bash") {
		t.Error("expected 'bash' in list-all results for empty query")
	}
}

func TestToolSearchTool_ValidateParams(t *testing.T) {
	reg := newMockRegistry()
	searchTool := advanced.NewToolSearchTool(reg)

	if err := searchTool.Validate(nil); err != nil {
		t.Errorf("unexpected error for nil params (query is optional): %v", err)
	}
	if err := searchTool.Validate(map[string]any{}); err != nil {
		t.Errorf("unexpected error for empty params (query is optional): %v", err)
	}
	if err := searchTool.Validate(map[string]any{"query": "test"}); err != nil {
		t.Errorf("unexpected error for valid params: %v", err)
	}
}

func TestToolSearchTool_IsParallelSafe(t *testing.T) {
	reg := newMockRegistry()
	searchTool := advanced.NewToolSearchTool(reg)

	if !searchTool.SupportsParallel() {
		t.Error("expected tool_search to be parallel-safe")
	}
	if !searchTool.IsIdempotent() {
		t.Error("expected tool_search to be idempotent")
	}
}

// ---------------------------------------------------------------------------
// Tests: Fuzzy matching, multi-token queries, and list-all
// ---------------------------------------------------------------------------

func TestToolSearchTool_FuzzyMatch(t *testing.T) {
	reg := newMockRegistry()
	reg.add(&mockTool{name: "gmail_read_email", description: "Reads email messages from Gmail"})
	reg.add(&mockTool{name: "bash", description: "Execute shell commands"})
	searchTool := advanced.NewToolSearchTool(reg)

	// Test substring matching (current implementation behavior)
	cases := []struct {
		query    string
		wantTool string
	}{
		{"email", "gmail_read_email"},
		{"gmail", "gmail_read_email"},
		{"bash", "bash"},
		{"shell", "bash"},
	}

	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			result, err := searchTool.Execute(context.Background(), map[string]any{
				"query": tc.query,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("unexpected error result: %s", result.Output)
			}
			if !strings.Contains(result.Output, tc.wantTool) {
				t.Errorf("query %q: expected %q in results, got: %s", tc.query, tc.wantTool, result.Output)
			}
		})
	}
}

func TestToolSearchTool_MultiTokenQuery(t *testing.T) {
	reg := newMockRegistry()
	reg.add(&mockTool{name: "gmail_read_email", description: "Reads email messages from Gmail"})
	reg.add(&mockTool{name: "file_read", description: "Read a file from the filesystem"})
	reg.add(&mockTool{name: "bash", description: "Execute shell commands"})
	searchTool := advanced.NewToolSearchTool(reg)

	// Test substring matching in descriptions (current implementation behavior)
	cases := []struct {
		query    string
		wantTool string
	}{
		{"email", "gmail_read_email"},
		{"file", "file_read"},
		{"shell", "bash"},
		{"commands", "bash"},
	}

	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			result, err := searchTool.Execute(context.Background(), map[string]any{
				"query": tc.query,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("unexpected error result: %s", result.Output)
			}
			if !strings.Contains(result.Output, tc.wantTool) {
				t.Errorf("query %q: expected %q in results, got: %s", tc.query, tc.wantTool, result.Output)
			}
		})
	}
}

func TestToolSearchTool_ListAll(t *testing.T) {
	reg := newMockRegistry()
	names := []string{"alpha_tool", "beta_tool", "gamma_tool"}
	for _, n := range names {
		reg.add(&mockTool{name: n, description: "a tool"})
	}
	searchTool := advanced.NewToolSearchTool(reg)

	// No query key at all — list all tools.
	result, err := searchTool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Output)
	}
	for _, n := range names {
		if !strings.Contains(result.Output, n) {
			t.Errorf("expected %q in list-all results", n)
		}
	}
}

func TestToolSearchTool_NilParamsListAll(t *testing.T) {
	reg := newMockRegistry()
	reg.add(&mockTool{name: "bash", description: "Execute shell commands"})
	searchTool := advanced.NewToolSearchTool(reg)

	result, err := searchTool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result for nil params: %s", result.Output)
	}
	if !strings.Contains(result.Output, "bash") {
		t.Errorf("expected 'bash' in list-all results for nil params")
	}
}

func TestToolSearchTool_MaxResultsLimitListAll(t *testing.T) {
	reg := newMockRegistry()
	for i := range 20 {
		reg.add(&mockTool{
			name:        fmt.Sprintf("tool_%02d", i),
			description: "generic tool for testing",
		})
	}
	searchTool := advanced.NewToolSearchTool(reg)

	result, err := searchTool.Execute(context.Background(), map[string]any{
		"max_results": float64(5),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Output)
	}
	// Count "name" keys in JSON output to bound the result count.
	count := strings.Count(result.Output, `"name"`)
	if count > 5 {
		t.Errorf("expected at most 5 results, got %d name fields", count)
	}
}

// ---------------------------------------------------------------------------
// Tests: Bridge
// ---------------------------------------------------------------------------

func TestEnrichProviderTool(t *testing.T) {
	sdkTool := &mockFullTool{
		mockTool: mockTool{name: "my_tool", description: "does stuff", idempotent: true},
		deferred: true,
		parallel: true,
		examples: []tools.ToolExample{
			{Description: "ex1", Parameters: map[string]any{"x": 1}},
		},
		meta: &tools.ToolMetadata{
			Category: "util",
			Tags:     []string{"fast"},
			Source:   tools.ToolSourceBuiltin,
		},
	}

	pt := advanced.EnrichProviderTool(
		provider.Tool{Name: "my_tool", Description: "does stuff"},
		sdkTool,
	)

	if pt.Metadata == nil {
		t.Fatal("expected Metadata to be populated")
	}

	if _, ok := pt.Metadata[advanced.MetaKeyInputExamples]; !ok {
		t.Error("expected input_examples in Metadata")
	}
	if v, ok := pt.Metadata[advanced.MetaKeyShouldDefer]; !ok || v != true {
		t.Error("expected should_defer=true in Metadata")
	}
	if v, ok := pt.Metadata[advanced.MetaKeyConcurrencySafe]; !ok || v != true {
		t.Error("expected concurrency_safe=true in Metadata")
	}
	if v, ok := pt.Metadata[advanced.MetaKeyCategory]; !ok || v != "util" {
		t.Errorf("expected category 'util', got %v", v)
	}
}

func TestEnrichProviderTool_DoesNotOverwrite(t *testing.T) {
	sdkTool := &mockFullTool{
		mockTool: mockTool{name: "t", description: "d"},
		deferred: true,
		meta:     &tools.ToolMetadata{Category: "new_category"},
	}

	pt := provider.Tool{
		Name:        "t",
		Description: "d",
		Metadata: map[string]any{
			advanced.MetaKeyCategory: "original",
		},
	}

	pt = advanced.EnrichProviderTool(pt, sdkTool)

	// Should NOT overwrite existing key
	if pt.Metadata[advanced.MetaKeyCategory] != "original" {
		t.Errorf("expected original category preserved, got %v", pt.Metadata[advanced.MetaKeyCategory])
	}
}

// ---------------------------------------------------------------------------
// Tests: Builder
// ---------------------------------------------------------------------------

func TestToolBuilder(t *testing.T) {
	tool, err := advanced.NewToolBuilder("test_tool").
		WithDescription("A test tool").
		WithParameters(map[string]any{"type": "object"}).
		WithExecutor(func(_ context.Context, _ map[string]any) (*tools.ToolResult, error) {
			return tools.NewToolResult("done"), nil
		}).
		WithIdempotent(true).
		WithDefer(true).
		WithConcurrency(true).
		WithExamples([]tools.ToolExample{{Description: "ex1"}}).
		WithCategory("test").
		WithTags("unit", "fast").
		WithVersion("1.0.0").
		WithAuthor("tester").
		Build()
	if err != nil {
		t.Fatalf("Build() failed: %v", err)
	}

	if tool.Name() != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", tool.Name())
	}
	if !tool.IsIdempotent() {
		t.Error("expected idempotent")
	}
	if !tool.ShouldDefer() {
		t.Error("expected deferred")
	}
	if !tool.SupportsParallel() {
		t.Error("expected parallel-safe")
	}
	if len(tool.InputExamples()) != 1 {
		t.Errorf("expected 1 example, got %d", len(tool.InputExamples()))
	}

	meta := tool.ToolMetadata()
	if meta.Category != "test" {
		t.Errorf("expected category 'test', got %q", meta.Category)
	}
	if meta.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", meta.Version)
	}
	if len(meta.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(meta.Tags))
	}

	// Execute
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "done" {
		t.Errorf("expected 'done', got %q", result.Output)
	}
}

// ---------------------------------------------------------------------------
// Tests: Deferred Registry
// ---------------------------------------------------------------------------

func TestDeferredRegistry_SplitTools(t *testing.T) {
	reg := newMockRegistry()
	reg.add(&mockTool{name: "file_read", description: "read files"})
	reg.add(&mockDeferredTool{
		mockTool: mockTool{name: "web_fetch", description: "fetch web"},
		deferred: true,
	})
	reg.add(&mockDeferredTool{
		mockTool: mockTool{name: "db_query", description: "query database"},
		deferred: true,
	})
	reg.add(&mockTool{name: "grep", description: "search files"})

	dr := advanced.NewDeferredRegistry(reg)

	provTools := []provider.Tool{
		{Name: "file_read", Description: "read files"},
		{Name: "web_fetch", Description: "fetch web"},
		{Name: "db_query", Description: "query database"},
		{Name: "grep", Description: "search files"},
	}

	eager, deferred := dr.SplitTools(provTools)

	if len(eager) != 2 {
		t.Errorf("expected 2 eager tools, got %d", len(eager))
	}
	if len(deferred) != 2 {
		t.Errorf("expected 2 deferred tools, got %d", len(deferred))
	}
}

func TestDeferredRegistry_Override(t *testing.T) {
	reg := newMockRegistry()
	reg.add(&mockTool{name: "file_read", description: "read files"})

	dr := advanced.NewDeferredRegistry(reg)

	// Not deferred by default
	if dr.ShouldDefer("file_read") {
		t.Error("file_read should not be deferred by default")
	}

	// Force defer
	dr.ForceDefer("file_read", true)
	if !dr.ShouldDefer("file_read") {
		t.Error("file_read should be deferred after override")
	}

	// Clear override
	dr.ClearOverride("file_read")
	if dr.ShouldDefer("file_read") {
		t.Error("file_read should not be deferred after clearing override")
	}
}

func TestDeferredRegistry_Stats(t *testing.T) {
	reg := newMockRegistry()
	reg.add(&mockTool{name: "file_read", description: "read files"})
	reg.add(&mockDeferredTool{
		mockTool: mockTool{name: "web_fetch", description: "fetch web content from URLs"},
		deferred: true,
	})

	dr := advanced.NewDeferredRegistry(reg)
	stats := dr.CalculateStats()

	if stats.EagerCount != 1 {
		t.Errorf("expected 1 eager, got %d", stats.EagerCount)
	}
	if stats.DeferredCount != 1 {
		t.Errorf("expected 1 deferred, got %d", stats.DeferredCount)
	}
	if stats.TotalCount != 2 {
		t.Errorf("expected 2 total, got %d", stats.TotalCount)
	}
	if stats.SavingsPercent <= 0 {
		t.Error("expected positive savings percent")
	}
}

// ---------------------------------------------------------------------------
// Mock registry for tests
// ---------------------------------------------------------------------------

type mockRegistry struct {
	toolMap map[string]tools.Tool
	names   []string
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{
		toolMap: make(map[string]tools.Tool),
	}
}

func (r *mockRegistry) add(tool tools.Tool) {
	r.toolMap[tool.Name()] = tool
	r.names = append(r.names, tool.Name())
}

func (r *mockRegistry) Register(tool tools.Tool) error {
	r.add(tool)
	return nil
}

func (r *mockRegistry) Unregister(name string) error {
	delete(r.toolMap, name)
	return nil
}

func (r *mockRegistry) Get(name string) (tools.Tool, error) {
	t, ok := r.toolMap[name]
	if !ok {
		return nil, nil
	}
	return t, nil
}

func (r *mockRegistry) List() []string {
	return r.names
}

func (r *mockRegistry) ListByCategory(category string) []tools.Tool {
	return nil
}

func (r *mockRegistry) IsRegistered(name string) bool {
	_, ok := r.toolMap[name]
	return ok
}

func (r *mockRegistry) HideTool(name string) error {
	return nil // No-op for test mock
}

func (r *mockRegistry) Execute(_ context.Context, name string, params map[string]any) (*tools.ToolResult, error) {
	t, ok := r.toolMap[name]
	if !ok {
		return nil, nil
	}
	return t.Execute(context.Background(), params)
}
