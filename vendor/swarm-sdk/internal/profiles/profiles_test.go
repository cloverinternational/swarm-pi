package profiles_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/profiles"
)

// TestBuiltinProfiles verifies GenerateBuiltinProfiles produces 8 valid profiles.
func TestBuiltinProfiles(t *testing.T) {
	all := profiles.GenerateBuiltinProfiles()

	expectedIDs := []string{
		"balanced", "quality", "performance", "cost-optimized",
		"claude-code", "gemini-code", "codex", "glm-zai",
	}

	if len(all) != len(expectedIDs) {
		t.Fatalf("expected %d profiles, got %d", len(expectedIDs), len(all))
	}

	for i, p := range all {
		if p.ID != expectedIDs[i] {
			t.Errorf("profile[%d]: expected ID %q, got %q", i, expectedIDs[i], p.ID)
		}
		if err := p.Validate(); err != nil {
			t.Errorf("profile %q failed validation: %v", p.ID, err)
		}
		// Every builtin must have all 7 canonical roles
		for _, alias := range profiles.AllAliases() {
			if !p.HasRole(alias) {
				t.Errorf("profile %q missing role %q", p.ID, alias)
			}
		}
	}
}

// TestDefaultProfileIsBalanced checks that exactly one profile has IsDefault=true
// and it's "balanced".
func TestDefaultProfileIsBalanced(t *testing.T) {
	all := profiles.GenerateBuiltinProfiles()
	defaults := 0
	for _, p := range all {
		if p.IsDefault {
			defaults++
			if p.ID != "balanced" {
				t.Errorf("default profile should be 'balanced', got %q", p.ID)
			}
		}
	}
	if defaults != 1 {
		t.Errorf("expected exactly 1 default profile, got %d", defaults)
	}
}

// TestJSONRoundTrip verifies profiles survive a JSON marshal/unmarshal cycle.
func TestJSONRoundTrip(t *testing.T) {
	cfg := profiles.ProfilesConfig{
		DefaultProfile: "balanced",
		Profiles:       profiles.GenerateBuiltinProfiles(),
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var cfg2 profiles.ProfilesConfig
	if err := json.Unmarshal(data, &cfg2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if err := cfg2.Validate(); err != nil {
		t.Fatalf("validate after round-trip: %v", err)
	}

	if len(cfg2.Profiles) != len(cfg.Profiles) {
		t.Errorf("profile count mismatch: got %d, want %d", len(cfg2.Profiles), len(cfg.Profiles))
	}
}

// TestManagerCRUD exercises Create, Clone, SetDefault, Delete via a temp dir.
func TestManagerCRUD(t *testing.T) {
	dir := t.TempDir()
	mgr := profiles.NewManager(dir)

	all := mgr.ListProfiles()
	if len(all) != 8 {
		t.Fatalf("expected 8 builtin profiles, got %d", len(all))
	}

	// Create
	p, err := mgr.CreateProfile("Test Profile", "created in test")
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if len(mgr.ListProfiles()) != 9 {
		t.Errorf("expected 9 profiles after create")
	}

	// Verify on disk
	data, err := os.ReadFile(filepath.Join(dir, "agent_profiles.json"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if len(data) == 0 {
		t.Error("agent_profiles.json is empty after create")
	}

	// Clone
	cloned, err := mgr.CloneProfile("balanced", "Balanced Clone")
	if err != nil {
		t.Fatalf("CloneProfile: %v", err)
	}
	if cloned.Name != "Balanced Clone" {
		t.Errorf("cloned name: got %q", cloned.Name)
	}
	if len(mgr.ListProfiles()) != 10 {
		t.Errorf("expected 10 profiles after clone")
	}

	// SetDefault
	if err := mgr.SetDefaultProfile("quality"); err != nil {
		t.Fatalf("SetDefaultProfile: %v", err)
	}
	if mgr.GetActiveProfileID() != "quality" {
		t.Errorf("active profile should be 'quality', got %q", mgr.GetActiveProfileID())
	}

	// Delete (the test profile we created, not default)
	if err := mgr.DeleteProfile(p.ID); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	if len(mgr.ListProfiles()) != 9 {
		t.Errorf("expected 9 profiles after delete")
	}

	// Cannot delete default
	if err := mgr.DeleteProfile("quality"); err == nil {
		t.Error("expected error deleting default profile")
	}
}

// TestResolveChain verifies chain resolution against the active profile.
func TestResolveChain(t *testing.T) {
	dir := t.TempDir()
	mgr := profiles.NewManager(dir)

	// Default is "balanced"
	chain, err := mgr.ResolveChain(profiles.AliasMain)
	if err != nil {
		t.Fatalf("ResolveChain main: %v", err)
	}
	if chain.IsEmpty() {
		t.Error("resolved chain is empty")
	}
	if chain.Primary.Provider != "anthropic" {
		t.Errorf("balanced main primary provider: got %q, want anthropic", chain.Primary.Provider)
	}

	// Switch to claude-code and verify Opus is steering primary
	if err := mgr.SetDefaultProfile("claude-code"); err != nil {
		t.Fatalf("SetDefaultProfile claude-code: %v", err)
	}
	chain, err = mgr.ResolveChain(profiles.AliasSteering)
	if err != nil {
		t.Fatalf("ResolveChain steering: %v", err)
	}
	if chain.Primary.Model != "claude-opus-4-7" {
		t.Errorf("claude-code steering primary model: got %q, want claude-opus-4-7", chain.Primary.Model)
	}
}

// TestVendorProfiles checks all 4 new vendor profiles have the right primary providers.
func TestVendorProfiles(t *testing.T) {
	all := profiles.GenerateBuiltinProfiles()
	byID := make(map[string]profiles.AgentProfile)
	for _, p := range all {
		byID[p.ID] = p
	}

	checks := []struct {
		profileID string
		alias     profiles.ModelAlias
		provider  string
	}{
		{"claude-code", profiles.AliasMain, "anthropic"},
		{"claude-code", profiles.AliasSteering, "anthropic"},
		{"gemini-code", profiles.AliasMain, "google"},
		{"gemini-code", profiles.AliasLongContext, "google"},
		{"codex", profiles.AliasMain, "openai"},
		{"codex", profiles.AliasSteering, "openai"},
		{"glm-zai", profiles.AliasMain, "cerebras"},
		{"glm-zai", profiles.AliasInference, "cerebras"},
	}

	for _, c := range checks {
		p, ok := byID[c.profileID]
		if !ok {
			t.Errorf("profile %q not found", c.profileID)
			continue
		}
		rc, ok := p.GetRole(c.alias)
		if !ok {
			t.Errorf("profile %q missing role %q", c.profileID, c.alias)
			continue
		}
		if rc.Chain.Primary.Provider != c.provider {
			t.Errorf("profile %q role %q: got provider %q, want %q",
				c.profileID, c.alias, rc.Chain.Primary.Provider, c.provider)
		}
	}
}

// TestCodexProfileModelMapping verifies the codex profile's role → model
// assignments track the current gpt-5.6 catalog family.
func TestCodexProfileModelMapping(t *testing.T) {
	var codex *profiles.AgentProfile
	for _, p := range profiles.GenerateBuiltinProfiles() {
		if p.ID == "codex" {
			pc := p
			codex = &pc
			break
		}
	}
	if codex == nil {
		t.Fatal("codex profile not found")
	}

	wantModels := map[profiles.ModelAlias]string{
		profiles.AliasMain:        "gpt-5.6-terra",
		profiles.AliasVision:      "gpt-5.6-terra",
		profiles.AliasSteering:    "gpt-5.6-sol",
		profiles.AliasLongContext: "gpt-5.6-sol",
		profiles.AliasSubAgent:    "gpt-5.6-luna",
		profiles.AliasInference:   "gpt-5.6-luna",
		profiles.AliasCompaction:  "gpt-5.6-luna",
		profiles.AliasBackground:  "gpt-5.6-luna",
	}
	for alias, wantModel := range wantModels {
		rc, ok := codex.GetRole(alias)
		if !ok {
			t.Errorf("codex profile missing role %q", alias)
			continue
		}
		if rc.Chain.Primary.Model != wantModel {
			t.Errorf("codex role %q: got model %q, want %q", alias, rc.Chain.Primary.Model, wantModel)
		}
	}
}

// TestMigrationFromPointers verifies old Pointers format is auto-migrated.
func TestMigrationFromPointers(t *testing.T) {
	old := profiles.AgentProfile{
		ID:   "legacy",
		Name: "Legacy",
		Pointers: map[profiles.ModelAlias]profiles.ModelPointer{
			profiles.AliasMain: {Provider: "anthropic", Model: "claude-3-haiku-20240307"},
		},
	}

	old.MigratePointersToRoles()

	if len(old.Roles) != 1 {
		t.Fatalf("expected 1 role after migration, got %d", len(old.Roles))
	}
	rc, ok := old.GetRole(profiles.AliasMain)
	if !ok {
		t.Fatal("main role not found after migration")
	}
	if rc.Chain.Primary.Provider != "anthropic" {
		t.Errorf("migrated provider: got %q", rc.Chain.Primary.Provider)
	}
	if old.Pointers != nil {
		t.Error("Pointers should be nil after migration")
	}
}
