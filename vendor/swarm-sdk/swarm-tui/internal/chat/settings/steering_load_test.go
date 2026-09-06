package settings

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	swarmcore "github.com/Swarm-Code/mono/swarm-core/core"
)

// TestSteeringLoadsOnCallbackRegistration asserts that once both steering
// callbacks are wired via SetSteeringCallbacks, the SteeringSettings
// in-memory state is populated from the loadConfig callback. This is the
// regression gate for the "steering UI shows defaults even though disk
// has values" bug.
func TestSteeringLoadsOnCallbackRegistration(t *testing.T) {
	setTempHome(t)

	mgr := NewManager(
		"ClaudeCode",
		"claude-opus-4-20250514",
		true, false, false, false, false,
		"dot",
		"chat",
		nil,
	)

	want := &swarmcore.SteeringConfig{
		Enabled:             true,
		RuntimeEnabled:      true,
		AutoApproveSimple:   true,
		MaxRetries:          7,
		EnableEfficiency:    true,
		MaxFileReads:        9,
		WorkflowEnabled:     true,
		ShowDecisionsInChat: true,
		LogDecisions:        true,
	}

	loadCalls := 0
	loadConfig := func(scope string) (*swarmcore.SteeringConfig, error) {
		loadCalls++
		return want, nil
	}
	saveConfig := func(scope string, cfg *swarmcore.SteeringConfig) tea.Cmd {
		return nil
	}

	mgr.SetSteeringCallbacks(loadConfig, saveConfig)

	if loadCalls != 1 {
		t.Fatalf("expected loadConfig to be invoked exactly once after SetSteeringCallbacks, got %d", loadCalls)
	}

	got := mgr.GetSteeringSettings()
	if got == nil {
		t.Fatal("SteeringSettings nil after SetSteeringCallbacks")
	}

	if !got.enabled {
		t.Error("enabled: want true, got false")
	}
	if !got.runtimeEnabled {
		t.Error("runtimeEnabled: want true, got false")
	}
	if !got.autoApproveSimple {
		t.Error("autoApproveSimple: want true, got false")
	}
	if got.maxRetries != 7 {
		t.Errorf("maxRetries: want 7, got %d", got.maxRetries)
	}
	if !got.enableEfficiency {
		t.Error("enableEfficiency: want true, got false")
	}
	if got.maxFileReads != 9 {
		t.Errorf("maxFileReads: want 9, got %d", got.maxFileReads)
	}
	if !got.workflowEnabled {
		t.Error("workflowEnabled: want true, got false")
	}
	if !got.showDecisions {
		t.Error("showDecisions: want true, got false")
	}
	if !got.logDecisions {
		t.Error("logDecisions: want true, got false")
	}
}

// TestSidebarNavReloadsSteering asserts that arrow-key sidebar navigation
// into the Steering section invokes Load(). This keeps the load-on-enter
// contract intact for any future changes that depend on navigation-time
// side effects (steering reload, auth refresh, etc.).
func TestSidebarNavReloadsSteering(t *testing.T) {
	setTempHome(t)

	mgr := NewManager(
		"ClaudeCode",
		"claude-opus-4-20250514",
		true, false, false, false, false,
		"dot",
		"chat",
		nil,
	)

	loadCalls := 0
	loadConfig := func(scope string) (*swarmcore.SteeringConfig, error) {
		loadCalls++
		return &swarmcore.SteeringConfig{Enabled: true, MaxRetries: 7}, nil
	}
	saveConfig := func(scope string, cfg *swarmcore.SteeringConfig) tea.Cmd { return nil }

	mgr.SetSteeringCallbacks(loadConfig, saveConfig)

	// Reset counter: we want to measure loads triggered by navigation,
	// not by the eager load inside SetSteeringCallbacks.
	loadsBeforeNav := loadCalls

	mgr.SetFocus(FocusSidebar)

	// Navigate down through sections until we land on Steering.
	for i := 0; i < len(Sections); i++ {
		if mgr.GetState().SelectedSection == SectionSteering {
			break
		}
		_ = mgr.HandleKey("down", nil)
	}

	if mgr.GetState().SelectedSection != SectionSteering {
		t.Fatalf("failed to navigate to steering section; got %v", mgr.GetState().SelectedSection)
	}

	loadsAfterNav := loadCalls
	if loadsAfterNav <= loadsBeforeNav {
		t.Fatalf("expected Load() to be invoked while navigating to steering section; before=%d after=%d", loadsBeforeNav, loadsAfterNav)
	}
}
