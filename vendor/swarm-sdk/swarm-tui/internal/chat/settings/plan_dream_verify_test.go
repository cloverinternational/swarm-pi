package settings

import (
	"strings"
	"testing"
)

func testPlanDreamTheme() Theme {
	return Theme{
		Primary:   "#00BCD4",
		Accent:    "#00BCD4",
		Text:      "#DDDDDD",
		TextDim:   "#999999",
		TextMuted: "#666666",
		BG:        "#1A1A1A",
		BGLight:   "#2A2A2A",
		BGLighter: "#3A3A3A",
		Border:    "#444444",
		Success:   "#4CAF50",
		Warning:   "#FF9800",
		Error:     "#F44336",
	}
}

// ─── PlanSettings ────────────────────────────────────────────────────────────

func TestPlanSettings_Render_HasHeader(t *testing.T) {
	p := NewPlanSettings(nil)
	state := NewState()
	state.Focus = FocusContent
	out := p.Render(80, 30, state, testPlanDreamTheme())
	if !strings.Contains(out, "Plan Mode") {
		t.Error("Plan Mode header missing from render")
	}
}

func TestPlanSettings_Render_HasBothToggles(t *testing.T) {
	p := NewPlanSettings(nil)
	state := NewState()
	state.Focus = FocusContent
	out := p.Render(80, 30, state, testPlanDreamTheme())
	if !strings.Contains(out, "Enable Plan Mode Tools") {
		t.Error("Enable toggle missing")
	}
	if !strings.Contains(out, "Auto-compact") {
		t.Error("AutoClear toggle missing")
	}
}

func TestPlanSettings_Render_HasHintBar(t *testing.T) {
	p := NewPlanSettings(nil)
	state := NewState()
	state.Focus = FocusContent
	out := p.Render(80, 30, state, testPlanDreamTheme())
	if !strings.Contains(out, "navigate") || !strings.Contains(out, "Space") {
		t.Errorf("Hint bar missing navigate/Space hints in output:\n%s", out)
	}
}

func TestPlanSettings_HandleKey_NavigatesDown(t *testing.T) {
	p := NewPlanSettings(nil)
	state := NewState()
	state.Focus = FocusContent
	state.SelectedItem = 0
	handled := p.HandleKey("down", state)
	if !handled {
		t.Error("down key should be handled")
	}
	if state.SelectedItem != 1 {
		t.Errorf("SelectedItem = %d, want 1", state.SelectedItem)
	}
}

func TestPlanSettings_HandleKey_NavigatesUp(t *testing.T) {
	p := NewPlanSettings(nil)
	state := NewState()
	state.Focus = FocusContent
	state.SelectedItem = 1
	handled := p.HandleKey("up", state)
	if !handled {
		t.Error("up key should be handled")
	}
	if state.SelectedItem != 0 {
		t.Errorf("SelectedItem = %d, want 0", state.SelectedItem)
	}
}

func TestPlanSettings_HandleKey_CannotGoAboveZero(t *testing.T) {
	p := NewPlanSettings(nil)
	state := NewState()
	state.Focus = FocusContent
	state.SelectedItem = 0
	handled := p.HandleKey("up", state)
	if handled {
		t.Error("up from item 0 should not be handled")
	}
	if state.SelectedItem != 0 {
		t.Errorf("SelectedItem should stay 0, got %d", state.SelectedItem)
	}
}

func TestPlanSettings_HandleKey_TogglesEnabled(t *testing.T) {
	p := NewPlanSettings(nil)
	before := p.IsEnabled()
	state := NewState()
	state.Focus = FocusContent
	state.SelectedItem = 0
	handled := p.HandleKey(" ", state)
	if !handled {
		t.Error("space should be handled on item 0")
	}
	if p.IsEnabled() == before {
		t.Error("space on item 0 should toggle Enabled")
	}
}

func TestPlanSettings_HandleKey_TogglesAutoClear(t *testing.T) {
	p := NewPlanSettings(nil)
	before := p.GetAutoClear()
	state := NewState()
	state.Focus = FocusContent
	state.SelectedItem = 1
	handled := p.HandleKey(" ", state)
	if !handled {
		t.Error("space should be handled on item 1")
	}
	if p.GetAutoClear() == before {
		t.Error("space on item 1 should toggle AutoClear")
	}
}

func TestPlanSettings_HandleKey_IgnoredWhenSidebarFocused(t *testing.T) {
	p := NewPlanSettings(nil)
	state := NewState()
	state.Focus = FocusSidebar
	handled := p.HandleKey("down", state)
	if handled {
		t.Error("keys should not be handled when sidebar has focus")
	}
}

func TestPlanSettings_HandleKey_EnterToggles(t *testing.T) {
	p := NewPlanSettings(nil)
	before := p.IsEnabled()
	state := NewState()
	state.Focus = FocusContent
	state.SelectedItem = 0
	handled := p.HandleKey("enter", state)
	if !handled {
		t.Error("enter should be handled on item 0")
	}
	if p.IsEnabled() == before {
		t.Error("enter on item 0 should toggle Enabled")
	}
}
