package chat

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
)

// createTestProfileManager creates a ProfileManager with builtin profiles for testing.
// This avoids dependency on filesystem state and ensures consistent test behavior.
// It sets HOME to a temp directory so NewProfileManager uses builtin defaults.
func createTestProfileManager(t *testing.T) *settings.ProfileManager {
	t.Helper()

	// Use t.Setenv which auto-restores after test
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)

	// Create manager - will use builtin defaults since no config file exists
	mgr := settings.NewProfileManager()
	return mgr
}

// TestBuildProfileRoleModelSelector_UsesProfileManager verifies that the selector
// returns configuration from the ProfileManager when called with a valid role.
func TestBuildProfileRoleModelSelector_UsesProfileManager(t *testing.T) {
	mgr := createTestProfileManager(t)
	selector := buildProfileRoleModelSelector(mgr)

	if selector == nil {
		t.Fatal("Expected selector to be non-nil")
	}

	result := selector("supervisor")
	if result == nil {
		t.Error("Expected non-nil result for supervisor role")
	}
}

func TestBuildProfileRoleModelSelector_SupervisorMapsSteering(t *testing.T) {
	mgr := createTestProfileManager(t)
	selector := buildProfileRoleModelSelector(mgr)

	if selector == nil {
		t.Fatal("Expected selector to be non-nil")
	}

	profile, err := mgr.GetActiveProfile()
	if err != nil {
		t.Fatalf("Failed to get active profile: %v", err)
	}

	steeringRole, ok := profile.GetRole(settings.AliasSteering)
	if !ok {
		t.Fatalf("Failed to get steering role")
	}
	steeringPointer := steeringRole.ToModelPointer()

	result := selector("supervisor")

	if result == nil {
		t.Fatal("Expected non-nil result for supervisor role")
	}

	if result.Provider != steeringPointer.Provider {
		t.Errorf("Expected provider name %s, got %s", steeringPointer.Provider, result.Provider)
	}
	if result.Model != steeringPointer.Model {
		t.Errorf("Expected model %s, got %s", steeringPointer.Model, result.Model)
	}
}

func TestBuildProfileRoleModelSelector_ImplementerMapsSubagent(t *testing.T) {
	mgr := createTestProfileManager(t)
	selector := buildProfileRoleModelSelector(mgr)

	if selector == nil {
		t.Fatal("Expected selector to be non-nil")
	}

	profile, err := mgr.GetActiveProfile()
	if err != nil {
		t.Fatalf("Failed to get active profile: %v", err)
	}

	subagentRole, ok := profile.GetRole(settings.AliasSubAgent)
	if !ok {
		t.Fatalf("Failed to get subagent role")
	}
	subagentPointer := subagentRole.ToModelPointer()

	result := selector("implementer")

	if result == nil {
		t.Fatal("Expected non-nil result for implementer role")
	}

	if result.Provider != subagentPointer.Provider {
		t.Errorf("Expected provider name %s, got %s", subagentPointer.Provider, result.Provider)
	}
	if result.Model != subagentPointer.Model {
		t.Errorf("Expected model %s, got %s", subagentPointer.Model, result.Model)
	}
}

func TestBuildProfileRoleModelSelector_ReviewersMapSteering(t *testing.T) {
	mgr := createTestProfileManager(t)
	selector := buildProfileRoleModelSelector(mgr)

	if selector == nil {
		t.Fatal("Expected selector to be non-nil")
	}

	profile, err := mgr.GetActiveProfile()
	if err != nil {
		t.Fatalf("Failed to get active profile: %v", err)
	}

	steeringRole, ok := profile.GetRole(settings.AliasSteering)
	if !ok {
		t.Fatalf("Failed to get steering role")
	}
	steeringPointer := steeringRole.ToModelPointer()

	result1 := selector("specReviewer")
	if result1 == nil {
		t.Error("Expected non-nil result for specReviewer role")
	}
	if result1.Provider != steeringPointer.Provider {
		t.Errorf("specReviewer: Expected provider name %s, got %s", steeringPointer.Provider, result1.Provider)
	}
	if result1.Model != steeringPointer.Model {
		t.Errorf("specReviewer: Expected model %s, got %s", steeringPointer.Model, result1.Model)
	}

	result2 := selector("qualityReviewer")
	if result2 == nil {
		t.Error("Expected non-nil result for qualityReviewer role")
	}
	if result2.Provider != steeringPointer.Provider {
		t.Errorf("qualityReviewer: Expected provider name %s, got %s", steeringPointer.Provider, result2.Provider)
	}
	if result2.Model != steeringPointer.Model {
		t.Errorf("qualityReviewer: Expected model %s, got %s", steeringPointer.Model, result2.Model)
	}
}

func TestBuildProfileRoleModelSelector_UnknownRoleDefaultsSubagent(t *testing.T) {
	mgr := createTestProfileManager(t)
	selector := buildProfileRoleModelSelector(mgr)

	if selector == nil {
		t.Fatal("Expected selector to be non-nil")
	}

	profile, err := mgr.GetActiveProfile()
	if err != nil {
		t.Fatalf("Failed to get active profile: %v", err)
	}

	subagentRole, ok := profile.GetRole(settings.AliasSubAgent)
	if !ok {
		t.Fatalf("Failed to get subagent role")
	}
	subagentPointer := subagentRole.ToModelPointer()

	result := selector("unknownRole")

	if result == nil {
		t.Fatal("Expected non-nil result for unknown role (should default to subagent)")
	}

	if result.Provider != subagentPointer.Provider {
		t.Errorf("Expected provider name %s, got %s", subagentPointer.Provider, result.Provider)
	}
	if result.Model != subagentPointer.Model {
		t.Errorf("Expected model %s, got %s", subagentPointer.Model, result.Model)
	}
}

func TestBuildProfileRoleModelSelector_ReturnsNilWhenAliasNotConfigured(t *testing.T) {
	mgr := createTestProfileManager(t)

	profile, err := mgr.CreateProfile("test-profile", "Profile with missing aliases")
	if err != nil {
		t.Fatalf("Failed to create test profile: %v", err)
	}

	err = mgr.SetDefaultProfile(profile.ID)
	if err != nil {
		t.Fatalf("Failed to set default profile: %v", err)
	}

	selector := buildProfileRoleModelSelector(mgr)

	if selector == nil {
		t.Fatal("Expected selector to be non-nil")
	}

	result := selector("supervisor")

	if result != nil {
		t.Error("Expected nil result when alias is not configured in profile")
	}
}
