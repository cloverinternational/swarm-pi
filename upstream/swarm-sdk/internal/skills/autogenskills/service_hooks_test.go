package autogenskills

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// TestService_GetHooks_disabled returns nil for disabled service.
func TestService_GetHooks_disabled(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := DefaultConfig() // ModeNever
	svc, _ := NewService(cfg, reg, nil)

	hookList, err := svc.GetHooks()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hookList != nil {
		t.Errorf("expected nil hooks for disabled service, got %d", len(hookList))
	}
}

// TestService_GetHooks_auto returns both lifecycle and budget hooks.
func TestService_GetHooks_auto(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeAuto, AutogenDir: t.TempDir(), Trigger: TriggerConfig{ToolCallBudget: 5}}
	m := &Metrics{}
	svc, err := NewService(cfg, reg, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hookList, err := svc.GetHooks()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hookList) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(hookList))
	}

	// Check hook names
	expectedNames := map[string]bool{
		"autogenskills":                    false,
		"autogenskills-budget-enforcement": false,
	}
	for _, h := range hookList {
		expectedNames[h.Name()] = true
	}
	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected hook %q not found in GetHooks result", name)
		}
	}
}

// TestService_GetHooks_autoNoBudget returns only lifecycle hook when budget=0.
func TestService_GetHooks_autoNoBudget(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeAuto, AutogenDir: t.TempDir(), Trigger: TriggerConfig{ToolCallBudget: 0}}
	m := &Metrics{}
	svc, err := NewService(cfg, reg, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hookList, err := svc.GetHooks()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hookList) != 1 {
		t.Fatalf("expected 1 hook (budget disabled), got %d", len(hookList))
	}
	if hookList[0].Name() != "autogenskills" {
		t.Errorf("expected lifecycle hook, got %q", hookList[0].Name())
	}
}

// TestService_RegisterHooksWithManager registers hooks in a real manager.
func TestService_RegisterHooksWithManager(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeAuto, AutogenDir: t.TempDir(), Trigger: TriggerConfig{ToolCallBudget: 5}}
	m := &Metrics{}
	svc, err := NewService(cfg, reg, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mgr := hooks.NewManager(hooks.ManagerConfig{})
	if err := svc.RegisterHooksWithManager(mgr); err != nil {
		t.Fatalf("RegisterHooksWithManager failed: %v", err)
	}

	// Check both hooks are registered
	registrations := mgr.List()
	if len(registrations) != 2 {
		t.Errorf("expected 2 registered hooks, got %d", len(registrations))
	}
}

// TestService_RegisterHooksWithManager_disabled is a no-op for disabled service.
func TestService_RegisterHooksWithManager_disabled(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := DefaultConfig() // ModeNever
	svc, _ := NewService(cfg, reg, nil)

	mgr := hooks.NewManager(hooks.ManagerConfig{})
	if err := svc.RegisterHooksWithManager(mgr); err != nil {
		t.Fatalf("RegisterHooksWithManager should not error for disabled: %v", err)
	}

	registrations := mgr.List()
	if len(registrations) != 0 {
		t.Errorf("expected 0 registered hooks for disabled, got %d", len(registrations))
	}
}
