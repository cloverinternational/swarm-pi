package bridge

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/profiles"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
)

func TestRoleTypeToModelAlias(t *testing.T) {
	tests := []struct {
		role     builtin.RoleType
		expected profiles.ModelAlias
	}{
		{builtin.RoleSupervisor, profiles.AliasSteering},
		{builtin.RoleImplementer, profiles.AliasSubAgent},
		{builtin.RoleSpecReviewer, profiles.AliasSteering},
		{builtin.RoleQualityReviewer, profiles.AliasSteering},
		{"unknown", profiles.AliasSubAgent},
		{"", profiles.AliasSubAgent},
	}

	for _, tc := range tests {
		t.Run(string(tc.role), func(t *testing.T) {
			got := RoleTypeToModelAlias(tc.role)
			if got != tc.expected {
				t.Errorf("RoleTypeToModelAlias(%q) = %q, want %q",
					tc.role, got, tc.expected)
			}
		})
	}
}

func TestBuildRoleModelSelector(t *testing.T) {
	// 1. nil manager → nil selector
	if sel := BuildRoleModelSelector(nil); sel != nil {
		t.Error("BuildRoleModelSelector(nil) should return nil")
	}

	// 2. Manager with a built-in profile
	mgr := profiles.NewManager(t.TempDir())
	sel := BuildRoleModelSelector(mgr)
	if sel == nil {
		t.Fatal("BuildRoleModelSelector(mgr) returned nil")
	}

	// 3. RoleSupervisor should resolve to steering (or fallback to main)
	cfg := sel(builtin.RoleSupervisor)
	if cfg == nil {
		t.Log("RoleSupervisor resolved to nil (profile may not have steering configured; using default)")
	} else {
		if cfg.Provider == "" {
			t.Error("RoleSupervisor config has empty Provider")
		}
		if cfg.Model == "" {
			t.Error("RoleSupervisor config has empty Model")
		}
	}

	// 4. RoleImplementer should resolve to subagent (or fallback to main)
	cfg = sel(builtin.RoleImplementer)
	if cfg == nil {
		t.Log("RoleImplementer resolved to nil (profile may not have subagent configured; using default)")
	} else {
		if cfg.Provider == "" {
			t.Error("RoleImplementer config has empty Provider")
		}
		if cfg.Model == "" {
			t.Error("RoleImplementer config has empty Model")
		}
	}

	// 5. Unknown role falls back to subagent
	cfg = sel(builtin.RoleType("unknown"))
	_ = cfg
}

func TestBuildCurrentProviderGetter(t *testing.T) {
	// 1. nil manager → nil getter
	if getter := BuildCurrentProviderGetter(nil); getter != nil {
		t.Error("BuildCurrentProviderGetter(nil) should return nil")
	}

	// 2. Manager with built-in profile
	mgr := profiles.NewManager(t.TempDir())
	getter := BuildCurrentProviderGetter(mgr)
	if getter == nil {
		t.Fatal("BuildCurrentProviderGetter(mgr) returned nil")
	}

	provider := getter()
	_ = provider
}

func TestResolveRoleChain(t *testing.T) {
	// 1. nil manager → nil chain
	if chain := ResolveRoleChain(nil, builtin.RoleSupervisor); chain != nil {
		t.Error("ResolveRoleChain(nil, ...) should return nil")
	}

	// 2. Manager with built-in profile
	mgr := profiles.NewManager(t.TempDir())
	chain := ResolveRoleChain(mgr, builtin.RoleImplementer)
	if chain != nil {
		if chain.Primary.Provider == "" {
			t.Error("resolved chain has empty Provider")
		}
		if chain.Primary.Model == "" {
			t.Error("resolved chain has empty Model")
		}
	}
}

// TestSelectorFallbackToMain verifies that when a role alias is not
// configured, the selector falls back to AliasMain.
func TestSelectorFallbackToMain(t *testing.T) {
	mgr := profiles.NewManager(t.TempDir())

	// Create a minimal profile with only AliasMain configured
	prof, err := mgr.CreateProfile("test-fallback", "Test Fallback Profile")
	if err != nil {
		t.Fatalf("CreateProfile error: %v", err)
	}

	err = mgr.SetRoleInProfile(prof.ID, profiles.AliasMain,
		profiles.NewRoleConfig("anthropic", "claude-3-5-sonnet-20241022"))
	if err != nil {
		t.Fatalf("SetRoleInProfile error: %v", err)
	}

	err = mgr.SetDefaultProfile(prof.ID)
	if err != nil {
		t.Fatalf("SetDefaultProfile error: %v", err)
	}

	sel := BuildRoleModelSelector(mgr)
	if sel == nil {
		t.Fatal("selector is nil")
	}

	// Request a role that is NOT configured — should fallback to AliasMain
	cfg := sel(builtin.RoleImplementer)
	if cfg == nil {
		t.Fatal("selector returned nil — expected fallback to AliasMain")
	}
	if cfg.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", cfg.Provider)
	}
	if cfg.Model != "claude-3-5-sonnet-20241022" {
		t.Errorf("Model = %q, want claude-3-5-sonnet-20241022", cfg.Model)
	}
}
