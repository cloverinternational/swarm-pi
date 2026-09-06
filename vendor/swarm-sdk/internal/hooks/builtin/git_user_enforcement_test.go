package builtin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// TestGitUserEnforcementHook_Name verifies the hook name.
func TestGitUserEnforcementHook_Name(t *testing.T) {
	h := NewGitUserEnforcementHookWithUser("alice@example.com")
	if h.Name() != "git-user-enforcement" {
		t.Errorf("expected name 'git-user-enforcement', got %s", h.Name())
	}
}

// TestGitUserEnforcementHook_Priority verifies the hook priority.
func TestGitUserEnforcementHook_Priority(t *testing.T) {
	h := NewGitUserEnforcementHookWithUser("alice@example.com")
	if h.Priority() != GitUserEnforcementPriority {
		t.Errorf("expected priority %d, got %d", GitUserEnforcementPriority, h.Priority())
	}
}

// TestGitUserEnforcementHook_Filter verifies the hook only filters tool-before-execute events.
func TestGitUserEnforcementHook_Filter(t *testing.T) {
	h := NewGitUserEnforcementHookWithUser("alice@example.com")

	tests := []struct {
		eventType  string
		shouldPass bool
	}{
		{hooks.EventToolBeforeExecute, true},
		{hooks.EventToolAfterExecute, false},
		{hooks.EventMessageAdded, false},
		{hooks.EventConversationCreated, false},
		{hooks.EventAgentStarted, false},
	}

	for _, tt := range tests {
		got := h.Filter(hooks.Event{Type: tt.eventType})
		if got != tt.shouldPass {
			t.Errorf("Filter(%s) = %v, want %v", tt.eventType, got, tt.shouldPass)
		}
	}
}

// TestGitUserEnforcementHook_AllowsOnNonMainBranch verifies that non-main branches are allowed.
func TestGitUserEnforcementHook_AllowsOnNonMainBranch(t *testing.T) {
	mockGit := func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "branch" && args[1] == "--show-current" {
			return "feature/new-thing\n", nil
		}
		if len(args) > 0 && args[0] == "remote" && args[1] == "get-url" && args[2] == "origin" {
			return "https://github.com/Swarm-Code/mono\n", nil
		}
		return "", errors.New("unexpected command")
	}

	h := NewGitUserEnforcementHookWithUser("alice@example.com")
	h.SetGitCommand(mockGit)

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		event := hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "Edit"}}
		result, err := h.OnEvent(ctx, event)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != hooks.ActionContinue {
			t.Errorf("call %d: expected ActionContinue on non-main branch, got %s", i+1, result.Action)
		}
		if result.Message != "" {
			t.Errorf("call %d: expected no message on non-main branch, got: %s", i+1, result.Message)
		}
	}
}

// TestGitUserEnforcementHook_AllowsWhenUserMatches verifies that matching user is allowed.
func TestGitUserEnforcementHook_AllowsWhenUserMatches(t *testing.T) {
	mockGit := func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "branch" && args[1] == "--show-current" {
			return "main\n", nil
		}
		if len(args) > 0 && args[0] == "config" && args[1] == "user.email" {
			return "alice@example.com\n", nil
		}
		if len(args) > 0 && args[0] == "remote" && args[1] == "get-url" && args[2] == "origin" {
			return "https://github.com/Swarm-Code/mono\n", nil
		}
		return "", errors.New("unexpected command")
	}

	h := NewGitUserEnforcementHookWithUser("alice@example.com")
	h.SetGitCommand(mockGit)

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		event := hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "Edit"}}
		result, err := h.OnEvent(ctx, event)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != hooks.ActionContinue {
			t.Errorf("call %d: expected ActionContinue when user matches, got %s", i+1, result.Action)
		}
		if result.Message != "" {
			t.Errorf("call %d: expected no message when user matches, got: %s", i+1, result.Message)
		}
	}
}

// TestGitUserEnforcementHook_GracePeriod verifies the 5-tool grace period.
func TestGitUserEnforcementHook_GracePeriod(t *testing.T) {
	mockGit := func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "branch" && args[1] == "--show-current" {
			return "main\n", nil
		}
		if len(args) > 0 && args[0] == "config" && args[1] == "user.email" {
			return "bob@example.com\n", nil
		}
		if len(args) > 0 && args[0] == "remote" && args[1] == "get-url" && args[2] == "origin" {
			return "https://github.com/Swarm-Code/mono\n", nil
		}
		return "", errors.New("unexpected command")
	}

	h := NewGitUserEnforcementHookWithUser("alice@example.com")
	h.SetGitCommand(mockGit)

	ctx := context.Background()

	// Calls 1-5: allowed with warning
	for i := 1; i <= GracePeriod; i++ {
		event := hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "Edit"}}
		result, err := h.OnEvent(ctx, event)
		if err != nil {
			t.Fatalf("grace call %d: unexpected error: %v", i, err)
		}
		if result.Action != hooks.ActionContinue {
			t.Errorf("grace call %d: expected ActionContinue, got %s", i, result.Action)
		}
		if result.Message == "" {
			t.Errorf("grace call %d: expected warning message, got none", i)
		}
		if !strings.Contains(result.Message, "bob@example.com") {
			t.Errorf("grace call %d: message should contain current user, got: %s", i, result.Message)
		}
		if !strings.Contains(result.Message, "alice@example.com") {
			t.Errorf("grace call %d: message should contain expected user, got: %s", i, result.Message)
		}
	}

	// Call 6: blocked
	event := hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "Edit"}}
	result, err := h.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("block call: unexpected error: %v", err)
	}
	if result.Action != hooks.ActionBlock {
		t.Errorf("block call: expected ActionBlock, got %s", result.Action)
	}
	if !strings.Contains(result.Message, "BLOCKED") {
		t.Errorf("block call: message should contain BLOCKED, got: %s", result.Message)
	}
}

// TestGitUserEnforcementHook_EmptyExpectedUserFailsOpen verifies empty expected user is allowed.
func TestGitUserEnforcementHook_EmptyExpectedUserFailsOpen(t *testing.T) {
	mockGit := func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "branch" && args[1] == "--show-current" {
			return "main\n", nil
		}
		if len(args) > 0 && args[0] == "config" && args[1] == "user.email" {
			return "bob@example.com\n", nil
		}
		if len(args) > 0 && args[0] == "remote" && args[1] == "get-url" && args[2] == "origin" {
			return "https://github.com/Swarm-Code/mono\n", nil
		}
		return "", errors.New("unexpected command")
	}

	h := NewGitUserEnforcementHookWithUser("")
	h.SetGitCommand(mockGit)

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		event := hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "Edit"}}
		result, err := h.OnEvent(ctx, event)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
		if result.Action != hooks.ActionContinue {
			t.Errorf("call %d: expected ActionContinue with empty expected user, got %s", i+1, result.Action)
		}
	}
}

// TestGitUserEnforcementHook_ReadOnlyToolsDontCount verifies read-only tools don't count.
func TestGitUserEnforcementHook_ReadOnlyToolsDontCount(t *testing.T) {
	mockGit := func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "branch" && args[1] == "--show-current" {
			return "main\n", nil
		}
		if len(args) > 0 && args[0] == "config" && args[1] == "user.email" {
			return "bob@example.com\n", nil
		}
		if len(args) > 0 && args[0] == "remote" && args[1] == "get-url" && args[2] == "origin" {
			return "https://github.com/Swarm-Code/mono\n", nil
		}
		return "", errors.New("unexpected command")
	}

	h := NewGitUserEnforcementHookWithUser("alice@example.com")
	h.SetGitCommand(mockGit)

	ctx := context.Background()

	// Read-only tools should not count against the grace period
	readOnlyTools := []string{"Read", "Grep", "Glob", "git_log", "git_status", "ls", "List", "task_list", "ask_user_question", "enter_plan_mode", "exit_plan_mode"}
	for i, tool := range readOnlyTools {
		event := hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": tool}}
		result, err := h.OnEvent(ctx, event)
		if err != nil {
			t.Fatalf("read-only tool %d (%s): unexpected error: %v", i+1, tool, err)
		}
		if result.Action != hooks.ActionContinue {
			t.Errorf("read-only tool %d (%s): expected ActionContinue, got %s", i+1, tool, result.Action)
		}
	}

	// Counter should still be 0
	if h.GetCounter() != 0 {
		t.Errorf("expected counter to be 0 after read-only tools, got %d", h.GetCounter())
	}

	// Now a non-read-only tool should trigger the grace period
	event := hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "Edit"}}
	result, err := h.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("edit tool: unexpected error: %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Errorf("edit tool: expected ActionContinue (first grace), got %s", result.Action)
	}
	if result.Message == "" {
		t.Errorf("edit tool: expected warning message, got none")
	}
	if h.GetCounter() != 1 {
		t.Errorf("expected counter to be 1 after first edit, got %d", h.GetCounter())
	}
}

// TestGitUserEnforcementHook_GitNotAvailableFailsOpen verifies git not available is allowed.
func TestGitUserEnforcementHook_GitNotAvailableFailsOpen(t *testing.T) {
	mockGit := func(args ...string) (string, error) {
		return "", errors.New("git not found")
	}

	h := NewGitUserEnforcementHookWithUser("alice@example.com")
	h.SetGitCommand(mockGit)

	ctx := context.Background()
	event := hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "Edit"}}
	result, err := h.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Errorf("expected ActionContinue when git not available, got %s", result.Action)
	}
	if result.Message != "" {
		t.Errorf("expected no message when git not available, got: %s", result.Message)
	}
}

// TestGitUserEnforcementHook_ResetCounter verifies counter reset works.
func TestGitUserEnforcementHook_ResetCounter(t *testing.T) {
	mockGit := func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "branch" && args[1] == "--show-current" {
			return "main\n", nil
		}
		if len(args) > 0 && args[0] == "config" && args[1] == "user.email" {
			return "bob@example.com\n", nil
		}
		if len(args) > 0 && args[0] == "remote" && args[1] == "get-url" && args[2] == "origin" {
			return "https://github.com/Swarm-Code/mono\n", nil
		}
		return "", errors.New("unexpected command")
	}

	h := NewGitUserEnforcementHookWithUser("alice@example.com")
	h.SetGitCommand(mockGit)

	ctx := context.Background()

	// Use up 3 grace calls
	for i := 0; i < 3; i++ {
		event := hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "Edit"}}
		h.OnEvent(ctx, event)
	}
	if h.GetCounter() != 3 {
		t.Fatalf("expected counter 3, got %d", h.GetCounter())
	}

	// Reset counter
	h.ResetCounter()
	if h.GetCounter() != 0 {
		t.Fatalf("expected counter 0 after reset, got %d", h.GetCounter())
	}

	// Next call should be allowed again (first grace call)
	event := hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "Edit"}}
	result, err := h.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("post-reset call: unexpected error: %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Errorf("post-reset call: expected ActionContinue, got %s", result.Action)
	}
	if result.Message == "" {
		t.Errorf("post-reset call: expected warning message, got none")
	}
	if h.GetCounter() != 1 {
		t.Errorf("expected counter 1 after post-reset call, got %d", h.GetCounter())
	}
}

// TestGitUserEnforcementHook_NonGitRepoFailsOpen verifies non-git repo is allowed.
func TestGitUserEnforcementHook_NonGitRepoFailsOpen(t *testing.T) {
	mockGit := func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "branch" && args[1] == "--show-current" {
			return "", errors.New("not a git repo")
		}
		return "", errors.New("unexpected command")
	}

	h := NewGitUserEnforcementHookWithUser("alice@example.com")
	h.SetGitCommand(mockGit)

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		event := hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "Edit"}}
		result, err := h.OnEvent(ctx, event)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
		if result.Action != hooks.ActionContinue {
			t.Errorf("call %d: expected ActionContinue in non-git repo, got %s", i+1, result.Action)
		}
	}
}

// TestGitUserEnforcementHook_Messages verifies warning and block messages contain required info.
func TestGitUserEnforcementHook_Messages(t *testing.T) {
	h := NewGitUserEnforcementHookWithUser("alice@example.com")

	warning := h.warningMessage("bob@example.com", 3)
	if !strings.Contains(warning, "bob@example.com") {
		t.Errorf("warning should contain current user")
	}
	if !strings.Contains(warning, "alice@example.com") {
		t.Errorf("warning should contain expected user")
	}
	if !strings.Contains(warning, "3/5") {
		t.Errorf("warning should contain counter")
	}

	block := h.blockMessage("bob@example.com")
	if !strings.Contains(block, "BLOCKED") {
		t.Errorf("block message should contain BLOCKED")
	}
	if !strings.Contains(block, "bob@example.com") {
		t.Errorf("block message should contain current user")
	}
	if !strings.Contains(block, "alice@example.com") {
		t.Errorf("block message should contain expected user")
	}
	if !strings.Contains(block, "5") {
		t.Errorf("block message should contain grace period")
	}
}

// TestGitUserEnforcementHook_AutoDetectUser verifies the auto-detect constructor.
func TestGitUserEnforcementHook_AutoDetectUser(t *testing.T) {
	// NewGitUserEnforcementHook auto-detects from git config
	// We can't easily mock the package-level detectGitUser, so we test
	// NewGitUserEnforcementHookWithUser directly
	h := NewGitUserEnforcementHookWithUser("auto@example.com")
	if h.GetExpectedUser() != "auto@example.com" {
		t.Errorf("expected user 'auto@example.com', got %s", h.GetExpectedUser())
	}
}
