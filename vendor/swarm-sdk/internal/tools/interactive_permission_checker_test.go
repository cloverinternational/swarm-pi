package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPermissionConfig(level PermissionLevel) PermissionConfig {
	config := DefaultPermissionConfig()
	config.Level = level
	config.TimeoutSeconds = 300
	config.TimeoutBehavior = "stop"
	if config.Overrides.Tools == nil {
		config.Overrides.Tools = make(map[string]OverridePolicy)
	}
	return config
}

func TestBuildApprovalPreview_VaultExecCredentialID(t *testing.T) {
	const secret = "must-not-appear"
	preview := buildApprovalPreview(PermissionEvaluationRequest{}, map[string]any{
		"params": map[string]any{
			"credentialId": "github-token\nReason: spoofed",
			"command":      "gh",
			"args":         []string{"repo", "view\nHost: evil.example"},
			"host":         "github.com",
			"workingDir":   "/tmp/repo",
			"reason":       "inspect \x1b[31mrepository metadata",
			"secret":       secret,
		},
	})
	if preview == nil {
		t.Fatal("vault_exec preview is nil")
	}
	for _, want := range []string{
		`Credential: "github-token\nReason: spoofed"`,
		`Command: "gh"`,
		`Arguments: "repo" "view\nHost: evil.example"`,
		`Host: "github.com"`,
		`Working directory: "/tmp/repo"`,
		`Reason: "inspect \x1b[31mrepository metadata"`,
	} {
		if !strings.Contains(preview.Content, want) {
			t.Errorf("preview %q does not contain %q", preview.Content, want)
		}
	}
	if strings.Contains(preview.Content, "\nReason: spoofed") ||
		strings.Contains(preview.Content, "\nHost: evil.example") ||
		strings.Contains(preview.Content, "\x1b") {
		t.Fatalf("preview contains unescaped model-controlled control text: %q", preview.Content)
	}
	if strings.Contains(preview.Content, secret) {
		t.Fatalf("preview leaked an unrelated secret field: %q", preview.Content)
	}
}

func TestInteractivePermissionChecker_VaultExecUsesConfiguredLevel(t *testing.T) {
	params := map[string]any{
		"credentialId": "github-token",
		"command":      "gh",
		"args":         []string{"repo", "view"},
		"host":         "github.com",
	}
	ctxData := BuildPermissionContext(
		"vault_exec",
		params,
		[]Permission{PermissionSecretAccess},
		ScopeGlobal,
		"",
		"",
		"",
		"",
	)

	balancedBroker := &mockApprovalBroker{defaultDecision: DecisionApproveOnce}
	balanced := NewInteractivePermissionChecker(
		testPermissionConfig(LevelBalanced),
		balancedBroker,
	)
	if !balanced.CheckWithContext(context.Background(), []Permission{PermissionSecretAccess}, ctxData) {
		t.Fatal("Balanced vault_exec was not allowed after broker approval")
	}
	if len(balancedBroker.requests) != 1 {
		t.Fatalf("Balanced broker requests = %d, want 1", len(balancedBroker.requests))
	}
	if preview := balancedBroker.requests[0].Preview; preview == nil ||
		!strings.Contains(preview.Content, `Credential: "github-token"`) {
		t.Fatalf("Balanced vault preview = %#v", preview)
	}

	yoloBroker := &mockApprovalBroker{defaultDecision: DecisionDeny}
	yolo := NewInteractivePermissionChecker(
		testPermissionConfig(LevelYOLO),
		yoloBroker,
	)
	if !yolo.CheckWithContext(context.Background(), []Permission{PermissionSecretAccess}, ctxData) {
		t.Fatal("YOLO vault_exec was denied")
	}
	if len(yoloBroker.requests) != 0 {
		t.Fatalf("YOLO broker requests = %d, want 0", len(yoloBroker.requests))
	}
}

func TestInteractivePermissionChecker_AllowDecision(t *testing.T) {
	broker := &mockApprovalBroker{}
	config := testPermissionConfig(LevelBalanced)

	checker := NewInteractivePermissionChecker(config, broker)
	ctx := context.Background()

	allowed := checker.CheckWithContext(ctx, []Permission{PermissionFileRead}, map[string]any{
		"tool": "file_read",
		"params": map[string]any{
			"path": "/tmp/config.json",
		},
	})

	if !allowed {
		t.Error("Expected allow decision for file_read")
	}

	if len(broker.requests) != 0 {
		t.Errorf("Expected no approval request for allow decision, got %d", len(broker.requests))
	}
}

// Read-only permissions (file_read, database_read, and combos thereof) must
// NEVER prompt or block — even under the strictest LevelAlwaysAsk config and
// even when the broker would deny. This guards the fast-path that keeps grep/read
// from stalling the agent on approvals.
func TestInteractivePermissionChecker_ReadOnlyNeverPrompts(t *testing.T) {
	cases := [][]Permission{
		{PermissionFileRead},
		{PermissionDatabaseRead},
		{PermissionFileRead, PermissionDatabaseRead},
	}
	for _, perms := range cases {
		// Broker would DENY if consulted; a bypassed read must still be allowed.
		broker := &mockApprovalBroker{defaultDecision: DecisionDeny}
		config := testPermissionConfig(LevelAlwaysAsk)
		checker := NewInteractivePermissionChecker(config, broker)

		allowed := checker.CheckWithContext(context.Background(), perms, map[string]any{
			"tool": "grep",
		})

		if !allowed {
			t.Errorf("read-only perms %v must be allowed without prompting", perms)
		}
		if len(broker.requests) != 0 {
			t.Errorf("read-only perms %v must not consult the broker, got %d requests", perms, len(broker.requests))
		}
	}
}

func TestInteractivePermissionChecker_DenyDecision(t *testing.T) {
	broker := &mockApprovalBroker{}
	config := testPermissionConfig(LevelBalanced)
	// Add an explicit deny rule for file_write to ensure it's denied without asking
	// (LevelBalanced upgrades PolicyDeny to PolicyAsk by default)
	config.Rules = []PermissionRule{
		{
			ID: "deny-file-write",
			When: PermissionRuleMatch{
				Tools: []string{"file_write"},
			},
			Then: PermissionRuleAction{
				Policy: PolicyDeny,
			},
		},
	}

	checker := NewInteractivePermissionChecker(config, broker)
	ctx := context.Background()

	allowed := checker.CheckWithContext(ctx, []Permission{PermissionFileWrite}, map[string]any{
		"tool": "file_write",
		"params": map[string]any{
			"path": "/tmp/output.txt",
		},
	})

	if allowed {
		t.Error("Expected deny decision for file_write")
	}

	if len(broker.requests) != 0 {
		t.Errorf("Expected no approval request for deny decision, got %d", len(broker.requests))
	}
}

func TestInteractivePermissionChecker_AskDecisionApproveOnce(t *testing.T) {
	broker := &mockApprovalBroker{defaultDecision: DecisionApproveOnce}
	config := testPermissionConfig(LevelAlwaysAsk)

	checker := NewInteractivePermissionChecker(config, broker)
	ctx := context.Background()

	allowed := checker.CheckWithContext(ctx, []Permission{PermissionFileWrite}, map[string]any{
		"tool": "file_write",
		"params": map[string]any{
			"path": "/tmp/data.txt",
		},
	})

	if !allowed {
		t.Error("Expected approval to allow request")
	}

	if len(broker.requests) != 1 {
		t.Errorf("Expected 1 approval request, got %d", len(broker.requests))
	}
}

func TestInteractivePermissionChecker_SessionGrant(t *testing.T) {
	broker := &mockApprovalBroker{defaultDecision: DecisionApproveSession}
	config := testPermissionConfig(LevelAlwaysAsk)

	checker := NewInteractivePermissionChecker(config, broker)
	ctx := context.Background()

	allowed := checker.CheckWithContext(ctx, []Permission{PermissionFileWrite}, map[string]any{
		"tool": "file_write",
		"params": map[string]any{
			"path": "/tmp/once.txt",
		},
	})

	if !allowed {
		t.Error("Expected session approval to allow request")
	}

	if len(broker.requests) != 1 {
		t.Errorf("Expected 1 approval request, got %d", len(broker.requests))
	}

	broker.requests = nil
	broker.defaultDecision = DecisionDeny

	allowed = checker.CheckWithContext(ctx, []Permission{PermissionFileWrite}, map[string]any{
		"tool": "file_write",
		"params": map[string]any{
			"path": "/tmp/again.txt",
		},
	})

	if !allowed {
		t.Error("Expected session grant to allow request without approval")
	}

	if len(broker.requests) != 0 {
		t.Errorf("Expected no approval requests after session grant, got %d", len(broker.requests))
	}
}

func TestInteractivePermissionChecker_AlwaysAllowPersist(t *testing.T) {
	broker := &mockApprovalBroker{defaultDecision: DecisionApproveAlways}
	config := testPermissionConfig(LevelBalanced)
	config.Defaults.Policies[PermissionFileWrite] = PolicyAsk

	checker := NewInteractivePermissionChecker(config, broker)
	ctx := context.Background()

	allowed := checker.CheckWithContext(ctx, []Permission{PermissionFileWrite}, map[string]any{
		"tool": "file_write",
		"params": map[string]any{
			"path":    "/tmp/output.txt",
			"content": "hello",
		},
	})

	if !allowed {
		t.Error("Expected always approval to allow request")
	}

	overrides := checker.ToolOverrides()
	if overrides["file_write"] != OverrideAlwaysAllow {
		t.Errorf("Expected file_write override to be always_allow, got %v", overrides["file_write"])
	}

	broker.requests = nil
	broker.defaultDecision = DecisionDeny

	allowed = checker.CheckWithContext(ctx, []Permission{PermissionFileWrite}, map[string]any{
		"tool": "file_write",
		"params": map[string]any{
			"path":    "/tmp/output.txt",
			"content": "hello again",
		},
	})

	if !allowed {
		t.Error("Expected persisted override to allow request without approval")
	}

	if len(broker.requests) != 0 {
		t.Errorf("Expected no approval requests after always allow, got %d", len(broker.requests))
	}
}

func TestInteractivePermissionChecker_SaveProjectPersist(t *testing.T) {
	broker := &mockApprovalBroker{defaultDecision: DecisionSaveProject}
	config := testPermissionConfig(LevelBalanced)
	config.Defaults.Policies[PermissionFileWrite] = PolicyAsk

	checker := NewInteractivePermissionChecker(config, broker)
	checker.SetProjectConfig(PermissionConfig{Version: 1})

	var saved PermissionConfig
	var callbackCalled bool = false
	checker.SetProjectConfigCallback(func(cfg PermissionConfig) {
		saved = cfg
		callbackCalled = true
	})

	ctx := context.Background()
	allowed := checker.CheckWithContext(ctx, []Permission{PermissionFileWrite}, map[string]any{
		"tool": "file_write",
		"params": map[string]any{
			"path":    "/tmp/output.txt",
			"content": "hello",
		},
	})

	if !allowed {
		t.Error("Expected project save approval to allow request")
	}
	if !callbackCalled {
		t.Error("Expected project config callback to be called")
	}
	if saved.Overrides.Tools["file_write"] != OverrideAlwaysAllow {
		t.Errorf("Expected project override to be always_allow, got %v", saved.Overrides.Tools["file_write"])
	}
}

func TestInteractivePermissionChecker_Timeout(t *testing.T) {
	broker := &mockApprovalBroker{
		defaultDecision: DecisionApproveOnce,
		delayResponse:   2 * time.Second,
	}

	config := testPermissionConfig(LevelAlwaysAsk)
	config.TimeoutSeconds = 1

	checker := NewInteractivePermissionChecker(config, broker)
	ctx := context.Background()

	start := time.Now()
	allowed := checker.CheckWithContext(ctx, []Permission{PermissionFileWrite}, map[string]any{
		"tool": "file_write",
		"params": map[string]any{
			"path": "/tmp/slow.txt",
		},
	})
	elapsed := time.Since(start)

	if allowed {
		t.Error("Expected timeout to deny request")
	}

	if elapsed > 1500*time.Millisecond {
		t.Errorf("Expected timeout around 1s, waited %v", elapsed)
	}
}

type mockApprovalBroker struct {
	requests           []PermissionApprovalRequest
	responses          map[string]ApprovalResponse
	defaultDecision    Decision
	defaultUserContext string
	delayResponse      time.Duration
	lastConfig         PermissionConfig
}

func (m *mockApprovalBroker) Request(ctx context.Context, req PermissionApprovalRequest) (ApprovalResponse, error) {
	m.requests = append(m.requests, req)

	if m.delayResponse > 0 {
		select {
		case <-time.After(m.delayResponse):
		case <-ctx.Done():
			return ApprovalResponse{Decision: DecisionDeny}, ctx.Err()
		}
	}

	if resp, ok := m.responses[req.RequestID]; ok {
		return resp, nil
	}

	if m.defaultDecision != "" {
		return ApprovalResponse{Decision: m.defaultDecision, UserContext: m.defaultUserContext}, nil
	}

	return ApprovalResponse{Decision: DecisionDeny}, nil
}

func (m *mockApprovalBroker) Respond(requestID string, decision Decision) error {
	return nil
}

func (m *mockApprovalBroker) SetConfig(config PermissionConfig) {
	m.lastConfig = config
}

func (m *mockApprovalBroker) GetPendingRequests() []PermissionApprovalRequest {
	return nil
}

// ---------------------------------------------------------------------------
// YOLO out-of-workspace write guard tests
// ---------------------------------------------------------------------------

// TestYOLO_InWorkspace_NoPrompt verifies that in YOLO mode writes inside the
// workspace are still auto-approved without calling the broker.
func TestYOLO_InWorkspace_NoPrompt(t *testing.T) {
	dir := t.TempDir()
	broker := &mockApprovalBroker{defaultDecision: DecisionApproveOnce}

	cfg := DefaultPermissionConfig()
	cfg.Level = LevelYOLO

	checker := NewInteractivePermissionChecker(cfg, broker)
	checker.SetWorkspaceRoot(dir)

	// Write inside workspace — should auto-approve, broker never called.
	targetFile := filepath.Join(dir, "src", "main.go")
	ctxData := map[string]any{
		"tool":   "write",
		"params": map[string]any{"file_path": targetFile},
	}
	result := checker.CheckWithContext(context.Background(), []Permission{PermissionFileWrite}, ctxData)
	if !result {
		t.Fatal("expected YOLO to auto-approve in-workspace write, got denied")
	}
	if len(broker.requests) != 0 {
		t.Fatalf("expected 0 broker calls for in-workspace write, got %d", len(broker.requests))
	}
}

// TestYOLO_OutsideWorkspace_AutoApproves verifies that in YOLO mode a write
// outside the workspace is auto-approved without invoking the broker. The
// previous out-of-workspace guard was removed so YOLO bypasses all prompts.
func TestYOLO_OutsideWorkspace_AutoApproves(t *testing.T) {
	dir := t.TempDir()
	broker := &mockApprovalBroker{defaultDecision: DecisionApproveOnce}

	cfg := DefaultPermissionConfig()
	cfg.Level = LevelYOLO

	checker := NewInteractivePermissionChecker(cfg, broker)
	checker.SetWorkspaceRoot(dir)

	// Write to a path outside the workspace (e.g. /etc/hosts or home dir)
	outsidePath := filepath.Join(filepath.Dir(dir), "sensitive_file.txt")
	ctxData := map[string]any{
		"tool":   "write",
		"params": map[string]any{"file_path": outsidePath},
	}
	result := checker.CheckWithContext(context.Background(), []Permission{PermissionFileWrite}, ctxData)
	if !result {
		t.Fatal("expected YOLO to auto-approve out-of-workspace write")
	}
	if len(broker.requests) != 0 {
		t.Fatalf("expected 0 broker calls for out-of-workspace write in YOLO mode, got %d", len(broker.requests))
	}
}

// TestYOLO_OutsideWorkspace_Delete_AutoApproves ensures delete outside workspace
// is also auto-approved in YOLO mode without prompting.
func TestYOLO_OutsideWorkspace_Delete_AutoApproves(t *testing.T) {
	dir := t.TempDir()
	broker := &mockApprovalBroker{defaultDecision: DecisionApproveOnce}

	cfg := DefaultPermissionConfig()
	cfg.Level = LevelYOLO

	checker := NewInteractivePermissionChecker(cfg, broker)
	checker.SetWorkspaceRoot(dir)

	outsidePath := filepath.Join(filepath.Dir(dir), "do_not_delete.txt")
	ctxData := map[string]any{
		"tool":   "delete",
		"params": map[string]any{"path": outsidePath},
	}
	result := checker.CheckWithContext(context.Background(), []Permission{PermissionFileDelete}, ctxData)
	if !result {
		t.Fatal("expected YOLO to auto-approve out-of-workspace delete")
	}
	if len(broker.requests) != 0 {
		t.Fatalf("expected 0 broker calls for out-of-workspace delete in YOLO mode, got %d", len(broker.requests))
	}
}

// TestYOLO_NoWorkspaceRoot_NoChange ensures that when workspaceRoot is not set,
// YOLO still auto-approves everything (backwards-compatible behaviour).
func TestYOLO_NoWorkspaceRoot_NoChange(t *testing.T) {
	broker := &mockApprovalBroker{defaultDecision: DecisionApproveOnce}

	cfg := DefaultPermissionConfig()
	cfg.Level = LevelYOLO

	checker := NewInteractivePermissionChecker(cfg, broker)
	// No SetWorkspaceRoot call

	ctxData := map[string]any{
		"tool":   "write",
		"params": map[string]any{"file_path": "/etc/hosts"},
	}
	result := checker.CheckWithContext(context.Background(), []Permission{PermissionFileWrite}, ctxData)
	if !result {
		t.Fatal("expected auto-approve when workspace root is unset")
	}
	if len(broker.requests) != 0 {
		t.Fatalf("expected 0 broker calls with no workspace root, got %d", len(broker.requests))
	}
}

// TestYOLO_ReadOutsideWorkspace_NoPrompt ensures that reads outside workspace
// are still auto-approved in YOLO mode (only writes are guarded).
func TestYOLO_ReadOutsideWorkspace_NoPrompt(t *testing.T) {
	dir := t.TempDir()
	broker := &mockApprovalBroker{defaultDecision: DecisionApproveOnce}

	cfg := DefaultPermissionConfig()
	cfg.Level = LevelYOLO

	checker := NewInteractivePermissionChecker(cfg, broker)
	checker.SetWorkspaceRoot(dir)

	outsidePath := "/home/user/.swarm/conversations/some_conv/plan.md"
	ctxData := map[string]any{
		"tool":   "read",
		"params": map[string]any{"file_path": outsidePath},
	}
	result := checker.CheckWithContext(context.Background(), []Permission{PermissionFileRead}, ctxData)
	if !result {
		t.Fatal("expected YOLO to auto-approve out-of-workspace read")
	}
	if len(broker.requests) != 0 {
		t.Fatalf("expected 0 broker calls for out-of-workspace read, got %d", len(broker.requests))
	}
}

// TestDenialReasonReachesSink verifies that when a broker denies a request with
// an actionable UserContext, the InteractivePermissionChecker records that
// reason into the context denial-reason sink so the tool registry can surface it
// to the model (review H1 end-to-end wiring).
func TestDenialReasonReachesSink(t *testing.T) {
	broker := &mockApprovalBroker{
		defaultDecision:    DecisionDeny,
		defaultUserContext: "this is a read-only run — do not retry mutating operations",
	}
	config := testPermissionConfig(LevelAlwaysAsk)
	checker := NewInteractivePermissionChecker(config, broker)

	ctx, reason := WithDenialReasonSink(context.Background())
	allowed := checker.CheckWithContext(ctx, []Permission{PermissionFileWrite}, map[string]any{
		"tool":   "Write",
		"params": map[string]any{"path": "/tmp/x"},
	})
	if allowed {
		t.Fatal("expected file_write to be denied")
	}
	if reason == nil || *reason == "" {
		t.Fatal("expected the denial reason to be recorded into the sink")
	}
	if *reason != broker.defaultUserContext {
		t.Fatalf("sink reason = %q, want %q", *reason, broker.defaultUserContext)
	}
}

// TestSetDenialReason_NoSinkIsSafe: SetDenialReason must be a safe no-op when no
// sink is installed in the context.
func TestSetDenialReason_NoSinkIsSafe(t *testing.T) {
	SetDenialReason(context.Background(), "should not panic")
}
