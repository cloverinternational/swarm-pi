package lifecycle

import (
	"context"
	"testing"
	"time"
)

func TestHookMatcher(t *testing.T) {
	tests := []struct {
		name    string
		matcher string
		input   string
		want    bool
	}{
		{"wildcard matches all", "*", "anything", true},
		{"exact match", "Write", "Write", true},
		{"regex match", "Write|Edit", "Write", true},
		{"regex match alt", "Write|Edit", "Edit", true},
		{"no match", "Write", "Read", false},
		{"partial regex", "Bash.*", "BashCommand", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := HookMatcher{Matcher: tt.matcher}
			if err := m.Compile(); err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			if got := m.Matches(tt.input); got != tt.want {
				t.Errorf("Matches(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestHookDecision(t *testing.T) {
	tests := []struct {
		name        string
		decision    HookDecision
		wantAllowed bool
		wantBlocked bool
		wantConfirm bool
	}{
		{
			name:        "allow decision",
			decision:    HookDecision{Decision: "allow"},
			wantAllowed: true,
		},
		{
			name:        "approve decision",
			decision:    HookDecision{Decision: "approve"},
			wantAllowed: true,
		},
		{
			name:        "deny decision",
			decision:    HookDecision{Decision: "deny"},
			wantBlocked: true,
		},
		{
			name:        "block decision",
			decision:    HookDecision{Decision: "block"},
			wantBlocked: true,
		},
		{
			name:        "ask decision",
			decision:    HookDecision{Decision: "ask"},
			wantConfirm: true,
		},
		{
			name:        "permission deny",
			decision:    HookDecision{PermissionDecision: "deny"},
			wantBlocked: true,
		},
		{
			name:        "empty is allowed",
			decision:    HookDecision{},
			wantAllowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.decision.IsAllowed(); got != tt.wantAllowed {
				t.Errorf("IsAllowed() = %v, want %v", got, tt.wantAllowed)
			}
			if got := tt.decision.IsBlocked(); got != tt.wantBlocked {
				t.Errorf("IsBlocked() = %v, want %v", got, tt.wantBlocked)
			}
			if got := tt.decision.NeedsConfirmation(); got != tt.wantConfirm {
				t.Errorf("NeedsConfirmation() = %v, want %v", got, tt.wantConfirm)
			}
		})
	}
}

func TestExecutor(t *testing.T) {
	config := LifecycleHooksConfig{
		PreToolUse: []HookMatcher{
			{
				Matcher: "Write|Edit",
				Hooks: []HookConfig{
					{
						Type:    HookTypeCommand,
						Command: "echo allow",
						Timeout: 5,
					},
				},
			},
		},
	}

	executor := NewExecutor(config)

	ctx := context.Background()
	hookCtx := &HookContext{
		Event:      EventPreToolUse,
		ToolName:   "Write",
		WorkingDir: "/tmp",
		Timestamp:  time.Now(),
	}

	decisions, err := executor.Execute(ctx, hookCtx)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(decisions) == 0 {
		t.Fatal("Expected at least one decision")
	}

	if !decisions[0].IsAllowed() {
		t.Errorf("Expected allowed decision, got: %+v", decisions[0])
	}
}

func TestExecutorNoMatch(t *testing.T) {
	config := LifecycleHooksConfig{
		PreToolUse: []HookMatcher{
			{
				Matcher: "Write",
				Hooks: []HookConfig{
					{Type: HookTypeCommand, Command: "echo test"},
				},
			},
		},
	}

	executor := NewExecutor(config)

	ctx := context.Background()
	hookCtx := &HookContext{
		Event:    EventPreToolUse,
		ToolName: "Read", // Does not match "Write"
	}

	decisions, err := executor.Execute(ctx, hookCtx)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(decisions) != 0 {
		t.Errorf("Expected no decisions for non-matching tool, got %d", len(decisions))
	}
}

func TestVariableExpansion(t *testing.T) {
	executor := &Executor{}

	hookCtx := &HookContext{
		PluginRoot:     "/path/to/plugin",
		ToolName:       "TestTool",
		SessionID:      "session-123",
		ConversationID: "conv-456",
		WorkingDir:     "/workdir",
	}

	tests := []struct {
		input string
		want  string
	}{
		{"${CLAUDE_PLUGIN_ROOT}/scripts/test.sh", "/path/to/plugin/scripts/test.sh"},
		{"${SWARM_PLUGIN_ROOT}/hooks", "/path/to/plugin/hooks"},
		{"Tool: ${TOOL_NAME}", "Tool: TestTool"},
		{"Session: ${SESSION_ID}", "Session: session-123"},
		{"Conversation: ${CONVERSATION_ID}", "Conversation: conv-456"},
		{"Dir: ${WORKING_DIR}", "Dir: /workdir"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := executor.expandVariables(tt.input, hookCtx)
			if got != tt.want {
				t.Errorf("expandVariables(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseConfig(t *testing.T) {
	jsonConfig := `{
		"PreToolUse": [
			{
				"matcher": "Write|Edit",
				"hooks": [
					{"type": "command", "command": "echo test", "timeout": 30}
				]
			}
		],
		"Stop": [
			{
				"matcher": "*",
				"hooks": [
					{"type": "prompt", "prompt": "Verify task completion"}
				]
			}
		]
	}`

	config, err := ParseConfig([]byte(jsonConfig), ".json")
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	if len(config.PreToolUse) != 1 {
		t.Errorf("Expected 1 PreToolUse matcher, got %d", len(config.PreToolUse))
	}

	if len(config.Stop) != 1 {
		t.Errorf("Expected 1 Stop matcher, got %d", len(config.Stop))
	}

	if config.PreToolUse[0].Hooks[0].Timeout != 30 {
		t.Errorf("Expected timeout 30, got %d", config.PreToolUse[0].Hooks[0].Timeout)
	}
}
