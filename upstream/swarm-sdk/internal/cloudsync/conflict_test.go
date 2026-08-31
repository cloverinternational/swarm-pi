package cloudsync

import (
	"testing"
	"time"
)

// ============================================================
// Tests for 021-cloud-settings-sync: Conflict Mode and Storage
// ============================================================

// --- ConflictMode Tests ---

func TestConflictMode_DefaultIsAuto(t *testing.T) {
	cm := NewConflictManager()

	if cm.Mode() != ConflictModeAuto {
		t.Errorf("expected default mode to be 'auto', got '%s'", cm.Mode())
	}
}

func TestConflictMode_CanSetManual(t *testing.T) {
	cm := NewConflictManager()

	err := cm.SetMode(ConflictModeManual)
	if err != nil {
		t.Fatalf("SetMode returned error: %v", err)
	}

	if cm.Mode() != ConflictModeManual {
		t.Errorf("expected mode='manual', got '%s'", cm.Mode())
	}
}

func TestConflictMode_CanSetAuto(t *testing.T) {
	cm := NewConflictManager()
	cm.SetMode(ConflictModeManual) // Start with manual

	err := cm.SetMode(ConflictModeAuto)
	if err != nil {
		t.Fatalf("SetMode returned error: %v", err)
	}

	if cm.Mode() != ConflictModeAuto {
		t.Errorf("expected mode='auto', got '%s'", cm.Mode())
	}
}

func TestConflictMode_RejectsInvalidMode(t *testing.T) {
	cm := NewConflictManager()

	err := cm.SetMode("invalid")
	if err == nil {
		t.Error("expected error for invalid mode")
	}
}

// --- Pending Conflict Storage Tests ---

func TestPendingConflict_CanAdd(t *testing.T) {
	cm := NewConflictManager()

	conflict := &PendingConflict{
		ID:             "settings:theme",
		DataType:       "settings",
		Key:            "theme",
		LocalValue:     "dark",
		CloudValue:     "light",
		LocalUpdatedAt: time.Now().Add(-5 * time.Minute),
		CloudUpdatedAt: time.Now().Add(-3 * time.Minute),
	}

	cm.AddConflict(conflict)

	conflicts := cm.PendingConflicts()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].ID != "settings:theme" {
		t.Errorf("expected conflict ID='settings:theme', got '%s'", conflicts[0].ID)
	}
}

func TestPendingConflict_CanGetByID(t *testing.T) {
	cm := NewConflictManager()

	conflict := &PendingConflict{
		ID:         "settings:theme",
		DataType:   "settings",
		Key:        "theme",
		LocalValue: "dark",
		CloudValue: "light",
	}
	cm.AddConflict(conflict)

	found := cm.GetConflict("settings:theme")
	if found == nil {
		t.Fatal("expected to find conflict")
	}
	if found.LocalValue != "dark" {
		t.Errorf("expected localValue='dark', got '%s'", found.LocalValue)
	}
}

func TestPendingConflict_GetReturnsNilForUnknown(t *testing.T) {
	cm := NewConflictManager()

	found := cm.GetConflict("unknown:id")
	if found != nil {
		t.Error("expected nil for unknown conflict ID")
	}
}

func TestPendingConflict_CanRemove(t *testing.T) {
	cm := NewConflictManager()

	conflict := &PendingConflict{
		ID:       "settings:theme",
		DataType: "settings",
		Key:      "theme",
	}
	cm.AddConflict(conflict)

	cm.RemoveConflict("settings:theme")

	if len(cm.PendingConflicts()) != 0 {
		t.Error("expected 0 conflicts after removal")
	}
}

func TestPendingConflict_RemoveNonexistentIsNoop(t *testing.T) {
	cm := NewConflictManager()
	cm.AddConflict(&PendingConflict{ID: "keep-me"})

	// Removing a nonexistent ID must not panic and must not disturb existing conflicts.
	cm.RemoveConflict("nonexistent")

	pending := cm.PendingConflicts()
	if len(pending) != 1 {
		t.Fatalf("removing nonexistent conflict altered state: got %d conflicts, want 1", len(pending))
	}
	if pending[0].ID != "keep-me" {
		t.Errorf("existing conflict changed: got ID %q, want keep-me", pending[0].ID)
	}
}

func TestPendingConflict_CanClearAll(t *testing.T) {
	cm := NewConflictManager()

	cm.AddConflict(&PendingConflict{ID: "conflict1"})
	cm.AddConflict(&PendingConflict{ID: "conflict2"})
	cm.AddConflict(&PendingConflict{ID: "conflict3"})

	cm.ClearConflicts()

	if len(cm.PendingConflicts()) != 0 {
		t.Error("expected 0 conflicts after clear")
	}
}

// --- Conflict Resolution Tests ---

func TestResolveConflict_LocalKeepsLocalValue(t *testing.T) {
	cm := NewConflictManager()
	cm.SetMode(ConflictModeManual)

	conflict := &PendingConflict{
		ID:         "settings:theme",
		DataType:   "settings",
		Key:        "theme",
		LocalValue: "dark",
		CloudValue: "light",
	}
	cm.AddConflict(conflict)

	resolution, err := cm.ResolveConflict("settings:theme", ResolutionLocal)
	if err != nil {
		t.Fatalf("ResolveConflict returned error: %v", err)
	}

	if resolution.Value != "dark" {
		t.Errorf("expected resolved value='dark', got '%s'", resolution.Value)
	}
	if resolution.Source != "local" {
		t.Errorf("expected source='local', got '%s'", resolution.Source)
	}

	// Conflict should be removed
	if cm.GetConflict("settings:theme") != nil {
		t.Error("expected conflict to be removed after resolution")
	}
}

func TestResolveConflict_CloudKeepsCloudValue(t *testing.T) {
	cm := NewConflictManager()
	cm.SetMode(ConflictModeManual)

	conflict := &PendingConflict{
		ID:         "settings:theme",
		DataType:   "settings",
		Key:        "theme",
		LocalValue: "dark",
		CloudValue: "light",
	}
	cm.AddConflict(conflict)

	resolution, err := cm.ResolveConflict("settings:theme", ResolutionCloud)
	if err != nil {
		t.Fatalf("ResolveConflict returned error: %v", err)
	}

	if resolution.Value != "light" {
		t.Errorf("expected resolved value='light', got '%s'", resolution.Value)
	}
	if resolution.Source != "cloud" {
		t.Errorf("expected source='cloud', got '%s'", resolution.Source)
	}
}

func TestResolveConflict_ReturnsErrorForUnknownConflict(t *testing.T) {
	cm := NewConflictManager()

	_, err := cm.ResolveConflict("unknown:id", ResolutionLocal)
	if err == nil {
		t.Error("expected error for unknown conflict")
	}
	if err != ErrConflictNotFound {
		t.Errorf("expected ErrConflictNotFound, got %v", err)
	}
}

func TestResolveConflict_ReturnsErrorForInvalidResolution(t *testing.T) {
	cm := NewConflictManager()
	cm.AddConflict(&PendingConflict{ID: "test"})

	_, err := cm.ResolveConflict("test", "invalid")
	if err == nil {
		t.Error("expected error for invalid resolution")
	}
}

// --- Auto Mode Behavior Tests ---

func TestAutoMode_ResolvesWithLastWriteWins(t *testing.T) {
	cm := NewConflictManager()
	// Default is auto mode

	localTime := time.Now().Add(-5 * time.Minute)
	cloudTime := time.Now().Add(-3 * time.Minute) // Cloud is newer

	conflict := &PendingConflict{
		ID:             "settings:theme",
		LocalValue:     "dark",
		CloudValue:     "light",
		LocalUpdatedAt: localTime,
		CloudUpdatedAt: cloudTime,
	}

	resolution := cm.AutoResolve(conflict)
	if resolution.Value != "light" {
		t.Errorf("expected auto-resolve to choose newer (cloud) value, got '%s'", resolution.Value)
	}
	if resolution.Source != "cloud" {
		t.Errorf("expected source='cloud', got '%s'", resolution.Source)
	}
}

func TestAutoMode_ChoosesLocalWhenNewer(t *testing.T) {
	cm := NewConflictManager()

	localTime := time.Now().Add(-1 * time.Minute) // Local is newer
	cloudTime := time.Now().Add(-10 * time.Minute)

	conflict := &PendingConflict{
		ID:             "settings:theme",
		LocalValue:     "dark",
		CloudValue:     "light",
		LocalUpdatedAt: localTime,
		CloudUpdatedAt: cloudTime,
	}

	resolution := cm.AutoResolve(conflict)
	if resolution.Value != "dark" {
		t.Errorf("expected auto-resolve to choose newer (local) value, got '%s'", resolution.Value)
	}
	if resolution.Source != "local" {
		t.Errorf("expected source='local', got '%s'", resolution.Source)
	}
}
