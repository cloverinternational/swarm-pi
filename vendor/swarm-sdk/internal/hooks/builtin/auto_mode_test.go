package builtin

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

func TestAutoModeConfig_Validation(t *testing.T) {
	tests := []struct {
		name   string
		config AutoModeConfig
		want   bool
	}{
		{
			name: "default config is valid",
			config: AutoModeConfig{
				SkipAutoPermissionPrompt: false,
				UseAutoModeDuringPlan:    true,
			},
			want: true,
		},
		{
			name: "opted in config",
			config: AutoModeConfig{
				SkipAutoPermissionPrompt: true,
				UseAutoModeDuringPlan:    true,
				AutoMode: AutoModeRules{
					Allow:    []string{"Read(*.go)"},
					SoftDeny: []string{"Bash(rm*)"},
				},
			},
			want: true,
		},
		{
			name: "disabled auto mode",
			config: AutoModeConfig{
				DisableAutoMode: "disable",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := NewAutoModeHook(nil, WithAutoModeConfig(tt.config))
			if hook == nil {
				t.Error("NewAutoModeHook() returned nil")
			}
		})
	}
}

func TestDefaultAutoModeClassifier_Classify(t *testing.T) {
	classifier := NewDefaultAutoModeClassifier(nil)

	tests := []struct {
		name      string
		toolName  string
		toolInput map[string]any
		rules     AutoModeRules
		want      string // expected recommendation
	}{
		{
			name:     "allow list match",
			toolName: "Read",
			toolInput: map[string]any{
				"file_path": "main.go",
			},
			rules: AutoModeRules{
				Allow: []string{"Read(*.go)"},
			},
			want: "approve",
		},
		{
			name:     "soft deny list match",
			toolName: "Bash",
			toolInput: map[string]any{
				"command": "rm -rf /",
			},
			rules: AutoModeRules{
				SoftDeny: []string{"Bash(rm*)"},
			},
			want: "prompt",
		},
		{
			name:     "no rules match - defaults to prompt",
			toolName: "Write",
			toolInput: map[string]any{
				"file_path": "test.txt",
			},
			rules: AutoModeRules{
				Allow:    []string{},
				SoftDeny: []string{},
			},
			want: "prompt",
		},
		{
			name:     "write tool without rules - defaults to prompt",
			toolName: "Write",
			toolInput: map[string]any{
				"file_path": "output.txt",
			},
			rules: AutoModeRules{},
			want:  "prompt",
		},
		{
			name:     "hard deny wins over allow",
			toolName: "Bash",
			toolInput: map[string]any{
				"command": "rm -rf /",
			},
			rules: AutoModeRules{
				Allow: []string{"Bash"},
				Deny:  []string{"Bash(rm -rf /*)"},
			},
			want: "deny",
		},
		{
			name:     "default allow list approves Read",
			toolName: "Read",
			toolInput: map[string]any{
				"file_path": "main.go",
			},
			rules: DefaultAutoModeRules(),
			want:  "approve",
		},
		{
			name:     "default soft-deny prompts Write",
			toolName: "Write",
			toolInput: map[string]any{
				"file_path": "main.go",
			},
			rules: DefaultAutoModeRules(),
			want:  "prompt",
		},
		{
			name:     "default hard-deny blocks rm -rf /",
			toolName: "Bash",
			toolInput: map[string]any{
				"command": "rm -rf /",
			},
			rules: DefaultAutoModeRules(),
			want:  "deny",
		},
		{
			name:     "default hard-deny blocks git push --force",
			toolName: "Bash",
			toolInput: map[string]any{
				"command": "git push --force origin main",
			},
			rules: DefaultAutoModeRules(),
			want:  "deny",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := classifier.Classify(context.Background(), tt.toolName, tt.toolInput, tt.rules)
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			if result.Recommendation != tt.want {
				t.Errorf("Classify() recommendation = %v, want %v", result.Recommendation, tt.want)
			}
		})
	}
}

func TestDefaultAutoModeClassifier_matchPattern(t *testing.T) {
	classifier := NewDefaultAutoModeClassifier(nil)

	tests := []struct {
		name      string
		toolName  string
		toolInput map[string]any
		pattern   string
		want      bool
	}{
		{
			name:     "exact match",
			toolName: "Read",
			toolInput: map[string]any{
				"file_path": "main.go",
			},
			pattern: "Read",
			want:    true,
		},
		{
			name:     "wildcard file pattern",
			toolName: "Read",
			toolInput: map[string]any{
				"file_path": "src/main.go",
			},
			pattern: "Read(*.go)",
			want:    true,
		},
		{
			name:     "wildcard recursive pattern",
			toolName: "Read",
			toolInput: map[string]any{
				"file_path": "src/components/Button.tsx",
			},
			pattern: "Read(**/*.tsx)",
			want:    true,
		},
		{
			name:     "bash command pattern",
			toolName: "Bash",
			toolInput: map[string]any{
				"command": "npm run build",
			},
			pattern: "Bash(npm run *)",
			want:    true,
		},
		{
			name:     "no match - wrong tool",
			toolName: "Write",
			toolInput: map[string]any{
				"file_path": "test.txt",
			},
			pattern: "Read(*.go)",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifier.matchPattern(tt.toolName, tt.toolInput, tt.pattern)
			if got != tt.want {
				t.Errorf("matchPattern() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAutoModeHook_Filter(t *testing.T) {
	logger := noop.NewLogger()

	tests := []struct {
		name    string
		config  AutoModeConfig
		envVars map[string]string
		event   hooks.Event
		want    bool
	}{
		{
			name: "tool execution event - opted in",
			config: AutoModeConfig{
				SkipAutoPermissionPrompt: true,
			},
			event: hooks.Event{
				Type: hooks.EventToolBeforeExecute,
			},
			want: true,
		},
		{
			name: "disabled auto mode - should not filter",
			config: AutoModeConfig{
				SkipAutoPermissionPrompt: true,
				DisableAutoMode:          "disable",
			},
			event: hooks.Event{
				Type: hooks.EventToolBeforeExecute,
			},
			want: false,
		},
		{
			name: "not opted in - should not filter",
			config: AutoModeConfig{
				SkipAutoPermissionPrompt: false,
			},
			event: hooks.Event{
				Type: hooks.EventToolBeforeExecute,
			},
			want: false,
		},
		{
			name: "wrong event type - should not filter",
			config: AutoModeConfig{
				SkipAutoPermissionPrompt: true,
			},
			event: hooks.Event{
				Type: hooks.EventMessageAdded,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set env vars
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			hook := NewAutoModeHook(logger, WithAutoModeConfig(tt.config))
			got := hook.Filter(tt.event)
			if got != tt.want {
				t.Errorf("Filter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAutoModeHook_OnEvent(t *testing.T) {
	logger := noop.NewLogger()
	config := AutoModeConfig{
		SkipAutoPermissionPrompt: true,
		AutoMode: AutoModeRules{
			Allow:    []string{"Read(*.go)"},
			SoftDeny: []string{"Bash(rm*)"},
		},
	}

	tests := []struct {
		name      string
		event     hooks.Event
		wantBlock bool
		wantMsg   string
	}{
		{
			name: "allow list - should continue",
			event: hooks.Event{
				Type: hooks.EventToolBeforeExecute,
				Data: map[string]any{
					"tool_name":  "Read",
					"tool_input": map[string]any{"file_path": "main.go"},
				},
			},
			wantBlock: false,
			wantMsg:   "Auto-approved",
		},
		{
			name: "soft deny list - should modify with metadata",
			event: hooks.Event{
				Type: hooks.EventToolBeforeExecute,
				Data: map[string]any{
					"tool_name":  "Bash",
					"tool_input": map[string]any{"command": "rm -rf tmp"},
				},
			},
			wantBlock: false,
			wantMsg:   "Requires approval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := NewAutoModeHook(logger, WithAutoModeConfig(config))
			result, err := hook.OnEvent(context.Background(), tt.event)
			if err != nil {
				t.Fatalf("OnEvent() error = %v", err)
			}

			if result.Action == hooks.ActionBlock && !tt.wantBlock {
				t.Errorf("OnEvent() blocked, want continue")
			}

			if tt.wantMsg != "" && !autoModeContainsSubstring(result.Message, tt.wantMsg) {
				t.Errorf("OnEvent() message = %q, want to contain %q", result.Message, tt.wantMsg)
			}
		})
	}
}

func TestIsWriteTool(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		want     bool
	}{
		{"write_file", "write_file", true},
		{"create_file", "create_file", true},
		{"delete_file", "delete_file", true},
		{"str_replace", "str_replace", true},
		{"bash", "bash", true},
		{"Read", "Read", false},
		{"Grep", "Grep", false},
		{"WebSearch", "WebSearch", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsWriteTool(tt.toolName)
			if got != tt.want {
				t.Errorf("IsWriteTool(%q) = %v, want %v", tt.toolName, got, tt.want)
			}
		})
	}
}

func TestAutoModeHook_Toggle(t *testing.T) {
	logger := noop.NewLogger()
	hook := NewAutoModeHook(logger)

	if !hook.IsEnabled() {
		t.Error("NewAutoModeHook should be enabled by default")
	}

	hook.SetEnabled(false)
	if hook.IsEnabled() {
		t.Error("SetEnabled(false) should disable hook")
	}

	hook.SetEnabled(true)
	if !hook.IsEnabled() {
		t.Error("SetEnabled(true) should enable hook")
	}
}

func TestAutoModeHook_HandleAutoModeTransition(t *testing.T) {
	logger := noop.NewLogger()
	hook := NewAutoModeHook(logger)

	// Enter auto mode
	hook.HandleAutoModeTransition(true)
	if !hook.NeedsExitAttachment() {
		t.Error("NeedsExitAttachment() should be true after entering auto mode")
	}

	// Exit auto mode
	hook.HandleAutoModeTransition(false)
	if hook.NeedsExitAttachment() {
		t.Error("NeedsExitAttachment() should be false after exiting auto mode")
	}
}

func TestAutoModeHook_Priority(t *testing.T) {
	logger := noop.NewLogger()
	hook := NewAutoModeHook(logger)

	if hook.Priority() != 90 {
		t.Errorf("Default priority = %d, want 90", hook.Priority())
	}

	// Test with custom priority
	hook2 := NewAutoModeHook(logger, WithAutoModePriority(50))
	if hook2.Priority() != 50 {
		t.Errorf("Custom priority = %d, want 50", hook2.Priority())
	}
}

func TestAutoModeHook_Name(t *testing.T) {
	logger := noop.NewLogger()
	hook := NewAutoModeHook(logger)

	if hook.Name() != "auto-mode" {
		t.Errorf("Hook name = %q, want \"auto-mode\"", hook.Name())
	}
}

// Helper function
func autoModeContainsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && autoModeContainsSubstringHelper(s, substr))
}

func autoModeContainsSubstringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
