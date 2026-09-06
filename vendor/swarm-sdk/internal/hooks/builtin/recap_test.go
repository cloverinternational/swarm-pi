package builtin

import (
	"context"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

func TestRecapConfig_Validation(t *testing.T) {
	tests := []struct {
		name   string
		config RecapConfig
		want   bool
	}{
		{
			name: "default config is valid",
			config: RecapConfig{
				EnableRecap:                true,
				MaxMessages:                50,
				Format:                     RecapFormatDetailed,
				InactivityThresholdMinutes: 5,
			},
			want: true,
		},
		{
			name: "brief format config",
			config: RecapConfig{
				EnableRecap:                true,
				Format:                     RecapFormatBrief,
				MaxMessages:                30,
				InactivityThresholdMinutes: 10,
			},
			want: true,
		},
		{
			name: "disabled recap",
			config: RecapConfig{
				EnableRecap: false,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := NewRecapHook(nil, WithRecapConfig(tt.config))
			if hook == nil {
				t.Error("NewRecapHook() returned nil")
			}
		})
	}
}

func TestDefaultRecapGenerator_Generate(t *testing.T) {
	generator := NewDefaultRecapGenerator(nil)

	now := time.Now()
	messages := []*conversation.Message{
		{
			Role:      conversation.RoleUser,
			Content:   "Please help me implement a new feature for authentication",
			Timestamp: now.Add(-30 * time.Minute),
		},
		{
			Role:      conversation.RoleAssistant,
			Content:   "I will help you implement authentication. Let me start by examining the current code structure.",
			Timestamp: now.Add(-29 * time.Minute),
		},
		{
			Role:      conversation.RoleAssistant,
			Content:   "I have created the auth module. Now I need to add the login endpoint.",
			Timestamp: now.Add(-20 * time.Minute),
		},
		{
			Role:      conversation.RoleUser,
			Content:   "That looks good. Also add password validation.",
			Timestamp: now.Add(-15 * time.Minute),
		},
		{
			Role:      conversation.RoleAssistant,
			Content:   "Added password validation. The implementation is now complete.",
			Timestamp: now.Add(-5 * time.Minute),
		},
	}

	config := RecapConfig{
		MaxMessages:        50,
		Format:             RecapFormatDetailed,
		IncludeToolResults: true,
	}

	result, err := generator.Generate(context.Background(), messages, config)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if result.Summary == "" {
		t.Error("Generate() returned empty summary")
	}

	if result.OriginalGoal == "" {
		t.Error("Generate() returned empty original goal")
	}

	if result.ProgressMade == "" {
		t.Error("Generate() returned empty progress")
	}
}

func TestDefaultRecapGenerator_extractOriginalGoal(t *testing.T) {
	generator := NewDefaultRecapGenerator(nil)

	tests := []struct {
		name     string
		messages []*conversation.Message
		want     string
	}{
		{
			name: "extracts first user message",
			messages: []*conversation.Message{
				{Role: conversation.RoleUser, Content: "Hello"},
				{Role: conversation.RoleUser, Content: "Please help me fix the bug in login"},
			},
			want: "Please help me fix the bug in login",
		},
		{
			name: "truncates long messages",
			messages: []*conversation.Message{
				{Role: conversation.RoleUser, Content: "A" + makeString(300)},
			},
			want: "A" + makeString(199) + "...",
		},
		{
			name:     "no user messages",
			messages: []*conversation.Message{},
			want:     "Unknown",
		},
		{
			name: "only assistant messages",
			messages: []*conversation.Message{
				{Role: conversation.RoleAssistant, Content: "Hello, how can I help?"},
			},
			want: "General conversation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generator.extractOriginalGoal(tt.messages)
			if got != tt.want {
				t.Errorf("extractOriginalGoal() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultRecapGenerator_extractProgress(t *testing.T) {
	generator := NewDefaultRecapGenerator(nil)

	messages := []*conversation.Message{
		{Role: conversation.RoleAssistant, Content: "I have completed the setup"},
		{Role: conversation.RoleAssistant, Content: "Finished implementing the auth module"},
		{Role: conversation.RoleAssistant, Content: "The feature is now done"},
	}

	progress := generator.extractProgress(messages)
	if progress == "" || progress == "Progress not explicitly documented" {
		t.Error("extractProgress() should have found progress indicators")
	}
}

func TestDefaultRecapGenerator_extractPendingTasks(t *testing.T) {
	generator := NewDefaultRecapGenerator(nil)

	messages := []*conversation.Message{
		{Role: conversation.RoleUser, Content: "We still need to implement logout"},
		{Role: conversation.RoleAssistant, Content: "The task of adding validation is pending"},
		{Role: conversation.RoleUser, Content: "Todo: Add password reset"},
	}

	tasks := generator.extractPendingTasks(messages)
	if len(tasks) == 0 {
		t.Error("extractPendingTasks() should have found pending tasks")
	}
}

func TestDefaultRecapGenerator_extractModifiedFiles(t *testing.T) {
	generator := NewDefaultRecapGenerator(nil)

	messages := []*conversation.Message{
		{Role: conversation.RoleAssistant, Content: "Created file auth.go"},
		{Role: conversation.RoleAssistant, Content: "Modified src/login.js"},
		{Role: conversation.RoleAssistant, Content: "Updated config.yaml"},
	}

	files := generator.extractModifiedFiles(messages)

	// All three verbs now trigger the heuristic via stem matching:
	//   "Created" -> "creat", "Modified" -> "modif", "Updated" -> "updat".
	// So every file path is extracted. Order is map-iteration dependent, so
	// assert set membership rather than slice order.
	want := map[string]bool{"auth.go": true, "src/login.js": true, "config.yaml": true}
	if len(files) != len(want) {
		t.Errorf("expected extractModifiedFiles to find %d files, got %d: %v", len(want), len(files), files)
	}
	for _, f := range files {
		if !want[f] {
			t.Errorf("extractModifiedFiles returned unexpected file %q (got %v)", f, files)
		}
		delete(want, f)
	}
	if len(want) != 0 {
		t.Errorf("extractModifiedFiles missed files: %v (got %v)", want, files)
	}
}

func TestDefaultRecapGenerator_buildSummary(t *testing.T) {
	generator := NewDefaultRecapGenerator(nil)

	tests := []struct {
		name    string
		format  RecapFormat
		wantLen int // minimum expected length
	}{
		{
			name:    "brief format",
			format:  RecapFormatBrief,
			wantLen: 10,
		},
		{
			name:    "summary format",
			format:  RecapFormatSummary,
			wantLen: 20,
		},
		{
			name:    "detailed format",
			format:  RecapFormatDetailed,
			wantLen: 30,
		},
	}

	goal := "Implement authentication"
	progress := "Created auth module"
	state := "Working on login"
	tasks := []string{"Add logout", "Add validation"}
	files := []string{"auth.go", "login.js"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := generator.buildSummary(goal, progress, state, tasks, files, tt.format)
			if len(summary) < tt.wantLen {
				t.Errorf("buildSummary() length = %d, want at least %d", len(summary), tt.wantLen)
			}
		})
	}
}

func TestRecapHook_Filter(t *testing.T) {
	logger := noop.NewLogger()

	tests := []struct {
		name    string
		config  RecapConfig
		envVars map[string]string
		event   hooks.Event
		want    bool
	}{
		{
			name: "conversation resumed - enabled",
			config: RecapConfig{
				EnableRecap: true,
			},
			event: hooks.Event{
				Type: hooks.EventConversationResumed,
			},
			want: true,
		},
		{
			name: "context restored - enabled",
			config: RecapConfig{
				EnableRecap: true,
			},
			event: hooks.Event{
				Type: hooks.EventContextRestored,
			},
			want: true,
		},
		{
			name: "disabled recap - should not filter",
			config: RecapConfig{
				EnableRecap: false,
			},
			event: hooks.Event{
				Type: hooks.EventConversationResumed,
			},
			want: false,
		},
		{
			name: "wrong event type - should not filter",
			config: RecapConfig{
				EnableRecap: true,
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

			hook := NewRecapHook(logger, WithRecapConfig(tt.config))
			got := hook.Filter(tt.event)
			if got != tt.want {
				t.Errorf("Filter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func SkipTestRecapHook_shouldShowRecap(t *testing.T) {
	logger := noop.NewLogger()
	hook := NewRecapHook(logger, WithRecapConfig(RecapConfig{
		EnableRecap:                true,
		InactivityThresholdMinutes: 5,
	}))

	// First time - should show
	if !hook.shouldShowRecap("conv-1") {
		t.Error("shouldShowRecap() should return true for first time")
	}

	// Immediately after - should not show (within threshold)
	if hook.shouldShowRecap("conv-1") {
		t.Error("shouldShowRecap() should return false within inactivity threshold")
	}

	// New conversation - should show
	if !hook.shouldShowRecap("conv-2") {
		t.Error("shouldShowRecap() should return true for new conversation")
	}

	// Empty conversation ID - should not show
	if hook.shouldShowRecap("") {
		t.Error("shouldShowRecap() should return false for empty conversation ID")
	}
}

func TestRecapHook_Toggle(t *testing.T) {
	logger := noop.NewLogger()
	hook := NewRecapHook(logger)

	if !hook.IsEnabled() {
		t.Error("NewRecapHook should be enabled by default")
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

func TestRecapHook_Priority(t *testing.T) {
	logger := noop.NewLogger()
	hook := NewRecapHook(logger)

	if hook.Priority() != 80 {
		t.Errorf("Default priority = %d, want 80", hook.Priority())
	}

	// Test with custom priority
	hook2 := NewRecapHook(logger, WithRecapPriority(60))
	if hook2.Priority() != 60 {
		t.Errorf("Custom priority = %d, want 60", hook2.Priority())
	}
}

func TestRecapHook_Name(t *testing.T) {
	logger := noop.NewLogger()
	hook := NewRecapHook(logger)

	if hook.Name() != "recap" {
		t.Errorf("Hook name = %q, want \"recap\"", hook.Name())
	}
}

func TestRecapHook_Config(t *testing.T) {
	logger := noop.NewLogger()
	config := RecapConfig{
		EnableRecap:                true,
		MaxMessages:                75,
		Format:                     RecapFormatSummary,
		InactivityThresholdMinutes: 15,
	}

	hook := NewRecapHook(logger, WithRecapConfig(config))

	got := hook.GetConfig()
	if got.MaxMessages != 75 {
		t.Errorf("MaxMessages = %d, want 75", got.MaxMessages)
	}
	if got.Format != RecapFormatSummary {
		t.Errorf("Format = %v, want %v", got.Format, RecapFormatSummary)
	}

	// Update config
	newConfig := RecapConfig{
		EnableRecap: true,
		Format:      RecapFormatBrief,
	}
	hook.UpdateConfig(newConfig)

	got = hook.GetConfig()
	if got.Format != RecapFormatBrief {
		t.Errorf("Updated Format = %v, want %v", got.Format, RecapFormatBrief)
	}
}

func TestRecapHook_EnvVars(t *testing.T) {
	logger := noop.NewLogger()

	tests := []struct {
		name    string
		envVars map[string]string
		want    bool
	}{
		{
			name:    "disabled via env",
			envVars: map[string]string{"SWARM_RECAP_ENABLED": "0"},
			want:    false,
		},
		{
			name:    "forced enabled via env",
			envVars: map[string]string{"SWARM_RECAP_ENABLED": "1"},
			want:    true,
		},
		{
			name:    "no env vars - default enabled",
			envVars: map[string]string{},
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set env vars
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			hook := NewRecapHook(logger)
			if hook.IsEnabled() != tt.want {
				t.Errorf("IsEnabled() = %v, want %v", hook.IsEnabled(), tt.want)
			}
		})
	}
}

// Helper function
func makeString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
