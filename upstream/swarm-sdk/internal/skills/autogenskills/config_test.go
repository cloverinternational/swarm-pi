package autogenskills

import (
	"testing"
)

func TestCreationMode_IsEnabled(t *testing.T) {
	tests := []struct {
		mode     CreationMode
		expected bool
	}{
		{ModeManual, true},
		{ModeAuto, true},
		{ModeNever, false},
		{CreationMode("unknown"), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			got := tt.mode.IsEnabled()
			if got != tt.expected {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTriggerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  TriggerConfig
		wantErr bool
	}{
		{
			name:    "valid default",
			config:  TriggerConfig{},
			wantErr: false,
		},
		{
			name:    "valid with threshold",
			config:  TriggerConfig{ToolCallThreshold: 5},
			wantErr: false,
		},
		{
			name:    "threshold too high",
			config:  TriggerConfig{ToolCallThreshold: 91},
			wantErr: true,
		},
		{
			name:    "min instructions too low",
			config:  TriggerConfig{MinInstructionsLength: 100},
			wantErr: true,
		},
		{
			name:    "min instructions valid",
			config:  TriggerConfig{MinInstructionsLength: 200},
			wantErr: false,
		},
		{
			name:    "boundary 90",
			config:  TriggerConfig{ToolCallThreshold: 90},
			wantErr: false,
		},
		{
			name:    "working budget too high",
			config:  TriggerConfig{WorkingBudget: 201},
			wantErr: true,
		},
		{
			name:    "working budget boundary 200",
			config:  TriggerConfig{WorkingBudget: 200},
			wantErr: false,
		},
		{
			name:    "negative max nudge ignores",
			config:  TriggerConfig{MaxNudgeIgnores: -1},
			wantErr: true,
		},
		{
			name:    "zero max nudge ignores",
			config:  TriggerConfig{MaxNudgeIgnores: 0},
			wantErr: false, // 0 means "use default"
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr && err == nil {
				t.Errorf("Validate() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Validate() unexpected error: %v", err)
			}
		})
	}
}

func TestTriggerConfig_WithDefaults(t *testing.T) {
	c := TriggerConfig{}
	out := c.WithDefaults()
	if out.NudgeInterval != 15 {
		t.Errorf("WithDefaults() NudgeInterval = %d, want 15", out.NudgeInterval)
	}
	if out.ToolCallBudget != 15 {
		t.Errorf("WithDefaults() ToolCallBudget = %d, want 15", out.ToolCallBudget)
	}
	if out.WorkingBudget != 90 {
		t.Errorf("WithDefaults() WorkingBudget = %d, want 90", out.WorkingBudget)
	}
	if out.MaxNudgeIgnores != 3 {
		t.Errorf("WithDefaults() MaxNudgeIgnores = %d, want 3", out.MaxNudgeIgnores)
	}
	if out.ErrorResolutionThreshold != 1 {
		t.Errorf("WithDefaults() ErrorResolutionThreshold = %d, want 1", out.ErrorResolutionThreshold)
	}

	// Should not override explicitly set values
	c2 := TriggerConfig{NudgeInterval: 10, WorkingBudget: 50, MaxNudgeIgnores: 5, ErrorResolutionThreshold: 3}
	out2 := c2.WithDefaults()
	if out2.NudgeInterval != 10 {
		t.Errorf("WithDefaults() should preserve NudgeInterval = 10, got %d", out2.NudgeInterval)
	}
	if out2.WorkingBudget != 50 {
		t.Errorf("WithDefaults() should preserve WorkingBudget = 50, got %d", out2.WorkingBudget)
	}
	if out2.MaxNudgeIgnores != 5 {
		t.Errorf("WithDefaults() should preserve MaxNudgeIgnores = 5, got %d", out2.MaxNudgeIgnores)
	}
	if out2.ErrorResolutionThreshold != 3 {
		t.Errorf("WithDefaults() should preserve ErrorResolutionThreshold = 3, got %d", out2.ErrorResolutionThreshold)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Mode != ModeNever {
		t.Errorf("DefaultConfig() Mode = %q, want %q", cfg.Mode, ModeNever)
	}
	if cfg.Trigger.NudgeInterval != 15 {
		t.Errorf("DefaultConfig() NudgeInterval = %d, want 15", cfg.Trigger.NudgeInterval)
	}
	if cfg.Trigger.ToolCallBudget != 15 {
		t.Errorf("DefaultConfig() ToolCallBudget = %d, want 15", cfg.Trigger.ToolCallBudget)
	}
	if cfg.Trigger.WorkingBudget != 90 {
		t.Errorf("DefaultConfig() WorkingBudget = %d, want 90", cfg.Trigger.WorkingBudget)
	}
	if cfg.Trigger.MaxNudgeIgnores != 3 {
		t.Errorf("DefaultConfig() MaxNudgeIgnores = %d, want 3", cfg.Trigger.MaxNudgeIgnores)
	}
	if cfg.Trigger.ErrorResolutionThreshold != 1 {
		t.Errorf("DefaultConfig() ErrorResolutionThreshold = %d, want 1", cfg.Trigger.ErrorResolutionThreshold)
	}
	if cfg.Curator.Interval != "1h" {
		t.Errorf("DefaultConfig() Interval = %q, want 1h", cfg.Curator.Interval)
	}
	if cfg.Curator.ArchiveAfterDays != 90 {
		t.Errorf("DefaultConfig() ArchiveAfterDays = %d, want 90", cfg.Curator.ArchiveAfterDays)
	}
	if cfg.Curator.StaleAfterDays != 30 {
		t.Errorf("DefaultConfig() StaleAfterDays = %d, want 30", cfg.Curator.StaleAfterDays)
	}
	if cfg.Curator.PatchAfterDays != 60 {
		t.Errorf("DefaultConfig() PatchAfterDays = %d, want 60", cfg.Curator.PatchAfterDays)
	}
	if cfg.Curator.ConsolidateTagOverlap != 0.5 {
		t.Errorf("DefaultConfig() ConsolidateTagOverlap = %f, want 0.5", cfg.Curator.ConsolidateTagOverlap)
	}
	if cfg.Curator.MaxTurns != 9999 {
		t.Errorf("DefaultConfig() curator MaxTurns = %d, want 9999", cfg.Curator.MaxTurns)
	}
	if cfg.Curator.Timeout != "1h" {
		t.Errorf("DefaultConfig() curator Timeout = %q, want 1h", cfg.Curator.Timeout)
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name:    "default never mode",
			config:  DefaultConfig(),
			wantErr: false,
		},
		{
			name: "auto mode without autogen dir",
			config: Config{
				Mode:    ModeAuto,
				Trigger: TriggerConfig{ToolCallThreshold: 5},
			},
			wantErr: true,
		},
		{
			name: "auto mode with all fields",
			config: Config{
				Mode:       ModeAuto,
				Trigger:    TriggerConfig{ToolCallThreshold: 5},
				AutogenDir: "~/.swarm/skills/autogen",
			},
			wantErr: false,
		},
		{
			name: "manual mode without autogen dir",
			config: Config{
				Mode:    ModeManual,
				Trigger: TriggerConfig{},
			},
			wantErr: true, // manual mode still needs autogen_dir for programmatic creation
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr && err == nil {
				t.Errorf("Validate() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Validate() unexpected error: %v", err)
			}
		})
	}
}

func TestConfig_IsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		expected bool
	}{
		{"never", DefaultConfig(), false},
		{"manual", Config{Mode: ModeManual}, true},
		{"auto", Config{Mode: ModeAuto}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.IsEnabled()
			if got != tt.expected {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.expected)
			}
		})
	}
}
