package autogenskills

import (
	"fmt"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// ═══════════════════════════════════════════════════════════════════════════
// TriggerReason Tests
// ═══════════════════════════════════════════════════════════════════════════

func TestTriggerReason_String(t *testing.T) {
	tests := []struct {
		reason TriggerReason
		want   string
	}{
		{TriggerManual, "manual API call"},
		{TriggerToolCallThreshold, "tool call threshold met"},
		{TriggerErrorResolution, "error resolution threshold met"},
		{TriggerLLMNudge, "LLM nudge response"},
		{TriggerReason("unknown"), "unknown"},
	}
	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			if got := tt.reason.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTriggerReason_IsValid(t *testing.T) {
	valid := []TriggerReason{TriggerManual, TriggerToolCallThreshold, TriggerErrorResolution, TriggerLLMNudge}
	for _, r := range valid {
		if !r.IsValid() {
			t.Errorf("%q should be valid", r)
		}
	}

	invalid := []TriggerReason{"", "bogus", "manual_api"}
	for _, r := range invalid {
		if r.IsValid() {
			t.Errorf("%q should be invalid", r)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// NudgeFragment Tests
// ═══════════════════════════════════════════════════════════════════════════

func TestNudgeFragment_IsZero(t *testing.T) {
	if !(NudgeFragment("")).IsZero() {
		t.Error("empty fragment should be zero")
	}
	if (NudgeFragment("consider creating a skill")).IsZero() {
		t.Error("non-empty fragment should not be zero")
	}
}

func TestNudgeFragment_String(t *testing.T) {
	f := NudgeFragment("test fragment")
	if f.String() != "test fragment" {
		t.Errorf("String() = %q, want %q", f.String(), "test fragment")
	}
}

func TestNudgeFragment_Validate(t *testing.T) {
	// Valid — under limit
	short := NudgeFragment("short nudge")
	if err := short.Validate(); err != nil {
		t.Errorf("short fragment should be valid: %v", err)
	}

	// Valid — exactly at limit
	atLimit := NudgeFragment(make([]byte, 2048))
	if err := atLimit.Validate(); err != nil {
		t.Errorf("2048-char fragment should be valid: %v", err)
	}

	// Invalid — over limit
	overLimit := NudgeFragment(make([]byte, 2049))
	if err := overLimit.Validate(); err == nil {
		t.Error("2049-char fragment should be invalid")
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// CreateOptions Tests
// ═══════════════════════════════════════════════════════════════════════════

func TestCreateOptions_Validate(t *testing.T) {
	t.Run("valid manual", func(t *testing.T) {
		o := CreateOptions{
			Name:          "test-skill",
			Description:   "A test skill",
			Instructions:  "Do something useful with at least two hundred characters so it passes validation checks without any issues whatsoever and continues to be valid for testing",
			TriggerReason: TriggerManual,
		}
		if err := o.Validate(); err != nil {
			t.Errorf("expected valid, got: %v", err)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		o := CreateOptions{Description: "d", Instructions: "i", TriggerReason: TriggerManual}
		if err := o.Validate(); err == nil {
			t.Error("expected error for missing name")
		}
	})

	t.Run("missing description", func(t *testing.T) {
		o := CreateOptions{Name: "n", Instructions: "i", TriggerReason: TriggerManual}
		if err := o.Validate(); err == nil {
			t.Error("expected error for missing description")
		}
	})

	t.Run("missing instructions", func(t *testing.T) {
		o := CreateOptions{Name: "n", Description: "d", TriggerReason: TriggerManual}
		if err := o.Validate(); err == nil {
			t.Error("expected error for missing instructions")
		}
	})

	t.Run("invalid trigger", func(t *testing.T) {
		o := CreateOptions{
			Name:          "n",
			Description:   "d",
			Instructions:  "i",
			TriggerReason: TriggerReason("bogus"),
		}
		if err := o.Validate(); err == nil {
			t.Error("expected error for invalid trigger reason")
		}
	})

	t.Run("zero trigger is invalid", func(t *testing.T) {
		o := CreateOptions{
			Name:          "n",
			Description:   "d",
			Instructions:  "i",
			TriggerReason: TriggerReason(""),
		}
		if err := o.Validate(); err == nil {
			t.Error("expected error for zero trigger reason")
		}
	})
}

// ═══════════════════════════════════════════════════════════════════════════
// CreationResult Tests
// ═══════════════════════════════════════════════════════════════════════════

func TestCreationResult_IsSuccess(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		r := CreationResult{
			Skill:     &skills.Skill{},
			Path:      "/tmp/test",
			CreatedAt: time.Now(),
		}
		if !r.IsSuccess() {
			t.Error("expected success")
		}
	})

	t.Run("error", func(t *testing.T) {
		r := CreationResult{Error: fmt.Errorf("fail")}
		if r.IsSuccess() {
			t.Error("expected failure")
		}
	})

	t.Run("nil skill", func(t *testing.T) {
		r := CreationResult{Path: "/tmp/test"}
		if r.IsSuccess() {
			t.Error("nil skill should be failure")
		}
	})
}

// ═══════════════════════════════════════════════════════════════════════════
// NudgeContext Tests
// ═══════════════════════════════════════════════════════════════════════════

func TestNudgeContext_ShouldNudge(t *testing.T) {
	t.Run("tool threshold met", func(t *testing.T) {
		ctx := NudgeContext{ToolCallCount: 5, TurnCount: 10}
		trigger := TriggerConfig{ToolCallThreshold: 5, NudgeInterval: 1}
		if !ctx.ShouldNudge(trigger) {
			t.Error("expected nudge at tool threshold")
		}
	})

	t.Run("error threshold met", func(t *testing.T) {
		ctx := NudgeContext{ErrorResolvedCount: 3, TurnCount: 10}
		trigger := TriggerConfig{ErrorResolutionThreshold: 3, NudgeInterval: 1}
		if !ctx.ShouldNudge(trigger) {
			t.Error("expected nudge at error threshold")
		}
	})

	t.Run("interval blocks", func(t *testing.T) {
		ctx := NudgeContext{ToolCallCount: 5, TurnCount: 10, LastNudgeTurn: 9}
		trigger := TriggerConfig{ToolCallThreshold: 5, NudgeInterval: 5}
		if ctx.ShouldNudge(trigger) {
			t.Error("should not nudge — interval not elapsed")
		}
	})

	t.Run("interval allows", func(t *testing.T) {
		ctx := NudgeContext{ToolCallCount: 5, TurnCount: 15, LastNudgeTurn: 9}
		trigger := TriggerConfig{ToolCallThreshold: 5, NudgeInterval: 5}
		if !ctx.ShouldNudge(trigger) {
			t.Error("expected nudge — interval elapsed")
		}
	})

	t.Run("no thresholds configured", func(t *testing.T) {
		ctx := NudgeContext{ToolCallCount: 100, TurnCount: 100}
		trigger := TriggerConfig{NudgeInterval: 1}
		if ctx.ShouldNudge(trigger) {
			t.Error("should not nudge without thresholds")
		}
	})
}
