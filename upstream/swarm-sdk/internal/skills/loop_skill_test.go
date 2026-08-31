package skills

import (
	"testing"
)

func TestParseLoopInput(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantInterval string
		wantPrompt   string
	}{
		{
			name:         "leading interval with slash command",
			input:        "5m /babysit-prs",
			wantInterval: "5m",
			wantPrompt:   "/babysit-prs",
		},
		{
			name:         "trailing every with minutes",
			input:        "check the deploy every 20m",
			wantInterval: "20m",
			wantPrompt:   "check the deploy",
		},
		{
			name:         "trailing every with word minutes",
			input:        "run tests every 5 minutes",
			wantInterval: "5m",
			wantPrompt:   "run tests",
		},
		{
			name:         "every PR not time expression",
			input:        "check every PR",
			wantInterval: "10m",
			wantPrompt:   "check every PR",
		},
		{
			name:         "no interval default",
			input:        "check the deploy",
			wantInterval: "10m",
			wantPrompt:   "check the deploy",
		},
		{
			name:         "just interval no prompt",
			input:        "5m",
			wantInterval: "5m",
			wantPrompt:   "",
		},
		{
			name:         "empty input",
			input:        "",
			wantInterval: "10m",
			wantPrompt:   "",
		},
		{
			name:         "trailing every with hours",
			input:        "backup data every 2 hours",
			wantInterval: "2h",
			wantPrompt:   "backup data",
		},
		{
			name:         "trailing every with single hour",
			input:        "sync every 1 hour",
			wantInterval: "1h",
			wantPrompt:   "sync",
		},
		{
			name:         "trailing every with days",
			input:        "generate report every 7 days",
			wantInterval: "7d",
			wantPrompt:   "generate report",
		},
		{
			name:         "trailing every with seconds",
			input:        "ping every 30 seconds",
			wantInterval: "30s",
			wantPrompt:   "ping",
		},
		{
			name:         "trailing every with spaced unit",
			input:        "check status every 5 m",
			wantInterval: "5m",
			wantPrompt:   "check status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotInterval, gotPrompt := ParseLoopInput(tt.input)
			if gotInterval != tt.wantInterval {
				t.Errorf("ParseLoopInput() interval = %v, want %v", gotInterval, tt.wantInterval)
			}
			if gotPrompt != tt.wantPrompt {
				t.Errorf("ParseLoopInput() prompt = %v, want %v", gotPrompt, tt.wantPrompt)
			}
		})
	}
}

func TestIntervalToCron(t *testing.T) {
	tests := []struct {
		name     string
		interval string
		want     string
		wantErr  bool
	}{
		{
			name:     "5 minutes",
			interval: "5m",
			want:     "*/5 * * * *",
			wantErr:  false,
		},
		{
			name:     "30 minutes",
			interval: "30m",
			want:     "*/30 * * * *",
			wantErr:  false,
		},
		{
			name:     "2 hours",
			interval: "2h",
			want:     "0 */2 * * *",
			wantErr:  false,
		},
		{
			name:     "1 day",
			interval: "1d",
			want:     "0 0 */1 * *",
			wantErr:  false,
		},
		{
			name:     "90 seconds rounds to 2 minutes",
			interval: "90s",
			want:     "*/2 * * * *",
			wantErr:  false,
		},
		{
			name:     "45 seconds rounds to 1 minute",
			interval: "45s",
			want:     "*/1 * * * *",
			wantErr:  false,
		},
		{
			name:     "60 minutes converts to hourly",
			interval: "60m",
			want:     "0 */1 * * *",
			wantErr:  false,
		},
		{
			name:     "120 minutes converts to 2 hours",
			interval: "120m",
			want:     "0 */2 * * *",
			wantErr:  false,
		},
		{
			name:     "90 minutes doesn't divide evenly",
			interval: "90m",
			want:     "",
			wantErr:  true,
		},
		{
			name:     "25 hours too large",
			interval: "25h",
			want:     "",
			wantErr:  true,
		},
		{
			name:     "invalid format",
			interval: "5x",
			want:     "",
			wantErr:  true,
		},
		{
			name:     "no unit",
			interval: "5",
			want:     "",
			wantErr:  true,
		},
		{
			name:     "1 second rounds to 1 minute",
			interval: "1s",
			want:     "*/1 * * * *",
			wantErr:  false,
		},
		{
			name:     "59 seconds rounds to 1 minute",
			interval: "59s",
			want:     "*/1 * * * *",
			wantErr:  false,
		},
		{
			name:     "60 seconds rounds to 1 minute",
			interval: "60s",
			want:     "*/1 * * * *",
			wantErr:  false,
		},
		{
			name:     "3600 seconds (1 hour) becomes 60 minutes error",
			interval: "3600s",
			want:     "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IntervalToCron(tt.interval)
			if (err != nil) != tt.wantErr {
				t.Errorf("IntervalToCron() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("IntervalToCron() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoopSkillMetadata(t *testing.T) {
	skill := loopSkill()

	if skill.Metadata.Name != "loop" {
		t.Errorf("skill name = %v, want loop", skill.Metadata.Name)
	}

	if !skill.Metadata.UserInvocable {
		t.Error("skill should be user invocable")
	}

	if skill.Instructions == "" {
		t.Error("skill instructions should not be empty")
	}

	if skill.Path != "builtin:loop" {
		t.Errorf("skill path = %v, want builtin:loop", skill.Path)
	}

	if skill.Source != "builtin" {
		t.Errorf("skill source = %v, want builtin", skill.Source)
	}

	if !skill.ContentLoaded {
		t.Error("skill content should be loaded")
	}
}
