package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	sdkprofiles "github.com/Swarm-Code/mono/swarm-sdk/internal/profiles"
)

func TestJSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent_profiles.json")

	// 1. Generate built-in profiles and marshal
	profiles := GenerateBuiltinProfiles()
	config := ProfilesConfig{
		DefaultProfile: "balanced",
		Profiles:       profiles,
	}

	if err := config.Validate(); err != nil {
		t.Fatalf("built-in profiles invalid before save: %v", err)
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// 2. Re-read and unmarshal
	data2, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var loaded ProfilesConfig
	if err := json.Unmarshal(data2, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if err := loaded.Validate(); err != nil {
		t.Fatalf("validate after load: %v", err)
	}

	// 3. Check structure intact
	if len(loaded.Profiles) != 8 {
		t.Fatalf("expected 8 profiles, got %d", len(loaded.Profiles))
	}

	for _, p := range loaded.Profiles {
		if len(p.Roles) == 0 {
			t.Errorf("profile %s has zero roles after round-trip", p.ID)
		}
		for alias, rc := range p.Roles {
			if rc.Chain == nil {
				t.Errorf("profile %s role %s has nil chain", p.ID, alias)
				continue
			}
			ref := rc.PrimaryRef()
			if ref.Provider == "" || ref.Model == "" {
				t.Errorf("profile %s role %s has empty primary ref", p.ID, alias)
			}
			if !rc.Enabled {
				t.Errorf("profile %s role %s should be enabled by default", p.ID, alias)
			}
			t.Logf("  %s / %s: %s/%s  +%d fallbacks", p.ID, alias, ref.Provider, ref.Model, len(rc.AllRefs())-1)
		}
	}

	// 4. Verify retry policy persists
	balanced := loaded.Profiles[0]
	if balanced.RetryPolicy == nil {
		t.Error("balanced profile retry policy is nil after round-trip")
	} else {
		if !balanced.RetryPolicy.RotateOnRateLimit {
			t.Error("RotateOnRateLimit should be true in balanced profile")
		}
		if !balanced.RetryPolicy.RotateOnPayment {
			t.Error("RotateOnPayment should be true in balanced profile")
		}
		t.Logf("  retry policy: cooldown=%ds", balanced.RetryPolicy.CooldownSeconds)
	}

	// 5. Verify ResolveChain works through ProfileManager
	mgr := &ProfileManager{Manager: sdkprofiles.NewManager(filepath.Dir(path))}
	*mgr.GetConfig() = loaded

	chain, err := mgr.ResolveChain(AliasMain)
	if err != nil {
		t.Fatalf("ResolveChain(main): %v", err)
	}
	if chain.IsEmpty() {
		t.Error("resolved chain for main is empty")
	}
	t.Logf("  main chain: %d models total", chain.Len())

	// 6. Test old Pointers format migration
	oldJSON := `{
		"default_profile": "test",
		"profiles": [{
			"id": "test",
			"name": "Test",
			"pointers": {
				"main": { "provider": "anthropic", "model": "claude-opus-4-20250514" },
				"thinking": { "provider": "cerebras", "model": "llama-3.3-70b" }
			},
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-01T00:00:00Z"
		}]
	}`

	var oldConfig ProfilesConfig
	if err := json.Unmarshal([]byte(oldJSON), &oldConfig); err != nil {
		t.Fatalf("unmarshal old format: %v", err)
	}

	// Migrate
	for i := range oldConfig.Profiles {
		oldConfig.Profiles[i].MigratePointersToRoles()
	}

	if len(oldConfig.Profiles[0].Roles) == 0 {
		t.Error("migration from Pointers to Roles produced zero roles")
	}

	// "thinking" alias should have been remapped to "inference"
	if _, ok := oldConfig.Profiles[0].Roles[AliasInference]; !ok {
		t.Error("'thinking' alias was not migrated to AliasInference")
	}

	// Pointers should be cleared after migration
	if len(oldConfig.Profiles[0].Pointers) != 0 {
		t.Error("Pointers should be cleared after migration")
	}

	t.Logf("  migration: %d roles from old format", len(oldConfig.Profiles[0].Roles))
}
