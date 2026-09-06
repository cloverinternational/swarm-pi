package settings

import (
	"os"
	"testing"

	sdkprofiles "github.com/Swarm-Code/mono/swarm-sdk/internal/profiles"
)

// TestProfileManagerCRUD tests the complete CRUD lifecycle
func TestProfileManagerCRUD(t *testing.T) {
	// Setup: Create temp directory for test
	tempDir := t.TempDir()

	// Create manager backed by temp dir
	mgr := &ProfileManager{Manager: sdkprofiles.NewManager(tempDir)}
	testConfigPath := mgr.ConfigPath()

	// Test 1: Save initial config
	t.Run("Save", func(t *testing.T) {
		err := mgr.Save()
		if err != nil {
			t.Fatalf("Failed to save: %v", err)
		}

		// Verify file exists
		if _, err := os.Stat(testConfigPath); os.IsNotExist(err) {
			t.Fatal("Config file was not created")
		}
	})

	// Test 2: Load config
	t.Run("Load", func(t *testing.T) {
		loaded, err := mgr.LoadProfiles(testConfigPath)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		if len(loaded.Profiles) != 8 {
			t.Errorf("Expected 8 profiles, got %d", len(loaded.Profiles))
		}

		if loaded.DefaultProfile != "balanced" {
			t.Errorf("Expected default profile 'balanced', got '%s'", loaded.DefaultProfile)
		}
	})

	// Test 3: Create new profile
	t.Run("Create", func(t *testing.T) {
		profile, err := mgr.CreateProfile("Test Profile", "A test profile")
		if err != nil {
			t.Fatalf("Failed to create profile: %v", err)
		}

		if profile.Name != "Test Profile" {
			t.Errorf("Expected name 'Test Profile', got '%s'", profile.Name)
		}

		if len(mgr.GetConfig().Profiles) != 9 {
			t.Errorf("Expected 9 profiles after create, got %d", len(mgr.GetConfig().Profiles))
		}
	})

	// Test 4: Get profile
	t.Run("Get", func(t *testing.T) {
		profile, err := mgr.GetProfile("balanced")
		if err != nil {
			t.Fatalf("Failed to get profile: %v", err)
		}

		if profile.ID != "balanced" {
			t.Errorf("Expected ID 'balanced', got '%s'", profile.ID)
		}
	})

	// Test 5: Get active profile
	t.Run("GetActive", func(t *testing.T) {
		profile, err := mgr.GetActiveProfile()
		if err != nil {
			t.Fatalf("Failed to get active profile: %v", err)
		}

		if profile.ID != "balanced" {
			t.Errorf("Expected active profile 'balanced', got '%s'", profile.ID)
		}
	})

	// Test 6: Resolve alias
	t.Run("ResolveAlias", func(t *testing.T) {
		pointer, err := mgr.ResolveAlias(AliasMain)
		if err != nil {
			t.Fatalf("Failed to resolve alias: %v", err)
		}

		if pointer.Provider == "" {
			t.Error("Provider should not be empty")
		}

		if pointer.Model == "" {
			t.Error("Model should not be empty")
		}

		t.Logf("Resolved main → %s/%s", pointer.Provider, pointer.Model)
	})

	// Test 7: Update profile
	t.Run("Update", func(t *testing.T) {
		// Get existing profile
		profile, err := mgr.GetProfile("balanced")
		if err != nil {
			t.Fatalf("Failed to get profile: %v", err)
		}

		// Modify it
		profile.Description = "Updated description"

		// Update
		err = mgr.UpdateProfile("balanced", *profile)
		if err != nil {
			t.Fatalf("Failed to update profile: %v", err)
		}

		// Verify
		updated, _ := mgr.GetProfile("balanced")
		if updated.Description != "Updated description" {
			t.Errorf("Description not updated")
		}
	})

	// Test 8: Clone profile
	t.Run("Clone", func(t *testing.T) {
		clone, err := mgr.CloneProfile("balanced", "My Balanced")
		if err != nil {
			t.Fatalf("Failed to clone profile: %v", err)
		}

		if clone.Name != "My Balanced" {
			t.Errorf("Expected name 'My Balanced', got '%s'", clone.Name)
		}

		if clone.IsDefault {
			t.Error("Clone should not be default")
		}

		// Verify roles were copied (new format — Roles not Pointers)
		if len(clone.Roles) == 0 {
			t.Error("Roles were not copied to clone")
		}
	})

	// Test 9: Set default profile
	t.Run("SetDefault", func(t *testing.T) {
		err := mgr.SetDefaultProfile("quality")
		if err != nil {
			t.Fatalf("Failed to set default: %v", err)
		}

		if mgr.GetConfig().DefaultProfile != "quality" {
			t.Errorf("Default profile not changed")
		}

		// Verify active profile changed
		active, _ := mgr.GetActiveProfile()
		if active.ID != "quality" {
			t.Errorf("Active profile should be 'quality', got '%s'", active.ID)
		}
	})

	// Test 10: Delete profile (should fail for default)
	t.Run("DeleteDefault", func(t *testing.T) {
		err := mgr.DeleteProfile("quality")
		if err == nil {
			t.Error("Should not be able to delete default profile")
		}
	})

	// Test 11: Delete non-default profile
	t.Run("DeleteNonDefault", func(t *testing.T) {
		// First create a profile to delete
		profile, _ := mgr.CreateProfile("To Delete", "Will be deleted")

		// Delete it
		err := mgr.DeleteProfile(profile.ID)
		if err != nil {
			t.Fatalf("Failed to delete profile: %v", err)
		}

		// Verify it's gone
		_, err = mgr.GetProfile(profile.ID)
		if err == nil {
			t.Error("Profile should have been deleted")
		}
	})

	// Test 12: List profiles
	t.Run("List", func(t *testing.T) {
		profiles := mgr.ListProfiles()
		if len(profiles) == 0 {
			t.Error("Expected at least one profile")
		}

		t.Logf("Total profiles: %d", len(profiles))
	})
}

// TestBuiltinProfiles verifies the built-in profiles are valid
func TestBuiltinProfiles(t *testing.T) {
	profiles := GenerateBuiltinProfiles()

	if len(profiles) != 8 {
		t.Fatalf("Expected 8 built-in profiles, got %d", len(profiles))
	}

	expectedIDs := []string{
		"balanced",
		"quality",
		"performance",
		"cost-optimized",
		"claude-code",
		"gemini-code",
		"codex",
		"glm-zai",
	}
	for i, expectedID := range expectedIDs {
		if profiles[i].ID != expectedID {
			t.Errorf("Profile %d: expected ID '%s', got '%s'", i, expectedID, profiles[i].ID)
		}

		// Validate each profile
		if err := profiles[i].Validate(); err != nil {
			t.Errorf("Profile %s is invalid: %v", profiles[i].ID, err)
		}

		// Check all aliases are configured
		for _, alias := range AllAliases() {
			if !profiles[i].HasRole(alias) {
				t.Errorf("Profile %s missing alias %s", profiles[i].ID, alias)
			}
		}

		t.Logf("Profile %s: %s", profiles[i].ID, profiles[i].Name)
	}
}

// TestLoadNonexistentFile tests loading when file doesn't exist
func TestLoadNonexistentFile(t *testing.T) {
	mgr := &ProfileManager{}

	config, err := mgr.LoadProfiles("/nonexistent/path/profiles.json")
	if err != nil {
		t.Fatalf("Should not error on nonexistent file: %v", err)
	}

	if len(config.Profiles) == 0 {
		t.Error("Should return built-in defaults")
	}

	t.Logf("Loaded %d default profiles", len(config.Profiles))
}

// TestValidation tests validation logic
func TestValidation(t *testing.T) {
	t.Run("EmptyProvider", func(t *testing.T) {
		pointer := ModelPointer{
			Provider: "",
			Model:    "some-model",
		}
		err := pointer.Validate()
		if err != ErrEmptyProvider {
			t.Errorf("Expected ErrEmptyProvider, got %v", err)
		}
	})

	t.Run("EmptyModel", func(t *testing.T) {
		pointer := ModelPointer{
			Provider: "anthropic",
			Model:    "",
		}
		err := pointer.Validate()
		if err != ErrEmptyModel {
			t.Errorf("Expected ErrEmptyModel, got %v", err)
		}
	})

	t.Run("InvalidTemperature", func(t *testing.T) {
		caps := AgentCapabilities{
			Temperature: 3.0, // Invalid (> 2.0)
		}
		err := caps.Validate()
		if err != ErrInvalidTemperature {
			t.Errorf("Expected ErrInvalidTemperature, got %v", err)
		}
	})

	t.Run("ValidProfile", func(t *testing.T) {
		profile := AgentProfile{
			ID:   "test",
			Name: "Test",
			Pointers: map[ModelAlias]ModelPointer{
				AliasMain: {
					Provider: "anthropic",
					Model:    "opus",
				},
			},
		}
		err := profile.Validate()
		if err != nil {
			t.Errorf("Valid profile should not error: %v", err)
		}
	})
}

// TestAliasResolution tests the core alias → pointer resolution
func TestAliasResolution(t *testing.T) {
	mgr := &ProfileManager{Manager: sdkprofiles.NewManager(t.TempDir())}
	cfg := mgr.GetConfig()
	cfg.DefaultProfile = "test"
	cfg.Profiles = []AgentProfile{
		{
			ID:   "test",
			Name: "Test",
			Pointers: map[ModelAlias]ModelPointer{
				AliasMain: {
					Provider: "anthropic",
					Model:    "claude-opus-4",
				},
				AliasSteering: {
					Provider: "openai",
					Model:    "gpt-4o",
				},
			},
		},
	}

	t.Run("ValidAlias", func(t *testing.T) {
		pointer, err := mgr.ResolveAlias(AliasMain)
		if err != nil {
			t.Fatalf("Failed to resolve: %v", err)
		}

		if pointer.Provider != "anthropic" {
			t.Errorf("Expected anthropic, got %s", pointer.Provider)
		}
		if pointer.Model != "claude-opus-4" {
			t.Errorf("Expected claude-opus-4, got %s", pointer.Model)
		}
	})

	t.Run("UnconfiguredAlias", func(t *testing.T) {
		_, err := mgr.ResolveAlias(AliasBackground)
		if err == nil {
			t.Error("Should error on unconfigured alias")
		}
	})
}
