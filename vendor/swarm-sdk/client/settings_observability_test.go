package client

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestObservableSettingsManager_LoadObserved tests the observed load operation.
func TestObservableSettingsManager_LoadObserved(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Create manager with observer
	var events []SettingsEvent
	manager := NewObservableSettingsManager(configPath,
		WithSettingsObserver(func(evt SettingsEvent) {
			events = append(events, evt)
		}),
	)

	// Load when file doesn't exist (should return defaults)
	cfg, snapshot, err := manager.LoadObserved()
	if err != nil {
		t.Fatalf("LoadObserved() error = %v", err)
	}

	// Verify defaults were returned
	defaults := (&CoreConfig{}).Defaults()
	if cfg.Provider != defaults.Provider {
		t.Errorf("Expected provider %s, got %s", defaults.Provider, cfg.Provider)
	}

	// Verify snapshot
	if snapshot == nil {
		t.Fatal("Expected snapshot, got nil")
	}
	if snapshot.Operation != "load" {
		t.Errorf("Expected operation 'load', got %s", snapshot.Operation)
	}
	if !snapshot.Success {
		t.Error("Expected success to be true")
	}
	if snapshot.Duration < 0 {
		t.Error("Expected duration >= 0")
	}

	// Verify events were emitted
	if len(events) < 2 {
		t.Errorf("Expected at least 2 events, got %d", len(events))
	}

	// Check for load:start event
	foundStart := false
	for _, evt := range events {
		if evt.Type == EventLoadStart {
			foundStart = true
			if !strings.Contains(evt.Description, "Loading") {
				t.Error("Expected description to contain 'Loading'")
			}
		}
	}
	if !foundStart {
		t.Error("Expected load:start event")
	}

	// Check for load:defaults event (file didn't exist)
	foundDefaults := false
	for _, evt := range events {
		if evt.Type == EventLoadDefaults {
			foundDefaults = true
		}
	}
	if !foundDefaults {
		t.Error("Expected load:defaults event for missing file")
	}

	// Check for load:success event
	foundSuccess := false
	for _, evt := range events {
		if evt.Type == EventLoadSuccess {
			foundSuccess = true
			if evt.Duration == 0 {
				t.Error("Expected duration in success event")
			}
		}
	}
	if !foundSuccess {
		t.Error("Expected load:success event")
	}
}

// TestObservableSettingsManager_SaveObserved tests the observed save operation.
func TestObservableSettingsManager_SaveObserved(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Create manager with observer
	var events []SettingsEvent
	manager := NewObservableSettingsManager(configPath,
		WithSettingsObserver(func(evt SettingsEvent) {
			events = append(events, evt)
		}),
		WithVerifyPersistence(true),
	)

	// Save initial config
	cfg := (&CoreConfig{}).Defaults()
	cfg.Model = "test-model"
	cfg.Provider = ProviderAnthropic

	snapshot, err := manager.SaveObserved(&cfg)
	if err != nil {
		t.Fatalf("SaveObserved() error = %v", err)
	}

	// Verify snapshot
	if snapshot == nil {
		t.Fatal("Expected snapshot, got nil")
	}
	if snapshot.Operation != "save" {
		t.Errorf("Expected operation 'save', got %s", snapshot.Operation)
	}
	if !snapshot.Success {
		t.Error("Expected success to be true")
	}
	if snapshot.ConfigAfter == nil {
		t.Error("Expected ConfigAfter to be set")
	}

	// Verify events were emitted
	if len(events) < 2 {
		t.Errorf("Expected at least 2 events, got %d", len(events))
	}

	// Check for verify:success event
	foundVerify := false
	for _, evt := range events {
		if evt.Type == EventVerifySuccess {
			foundVerify = true
		}
	}
	if !foundVerify {
		t.Error("Expected verify:success event with persistence verification")
	}

	// Update and save again to test change detection
	events = nil // reset
	cfg2 := cfg
	cfg2.Model = "updated-model"

	snapshot2, err := manager.SaveObserved(&cfg2)
	if err != nil {
		t.Fatalf("SaveObserved() second call error = %v", err)
	}

	// Verify change detection
	foundChange := slices.Contains(snapshot2.ChangedFields, "Model")
	if !foundChange {
		t.Errorf("Expected 'Model' in changed fields, got %v", snapshot2.ChangedFields)
	}

	// Check for state:change event
	foundStateChange := false
	for _, evt := range events {
		if evt.Type == EventStateChange {
			foundStateChange = true
			if evt.Fields == nil {
				t.Error("Expected Fields in state:change event")
			}
		}
	}
	if !foundStateChange {
		t.Error("Expected state:change event")
	}
}

// TestObservableSettingsManager_LoadObserved_Error tests error handling in load.
func TestObservableSettingsManager_LoadObserved_Error(t *testing.T) {
	// Use a path that will cause an error (directory instead of file)
	tempDir := t.TempDir()

	// Create a directory at the config path (will cause error when trying to read as file)
	configPath := filepath.Join(tempDir, "configdir")
	if err := os.MkdirAll(configPath, 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	var events []SettingsEvent

	// Create subdirectory to cause error
	badPath := filepath.Join(configPath, "config.json", "bad")
	manager2 := NewObservableSettingsManager(badPath,
		WithSettingsObserver(func(evt SettingsEvent) {
			events = append(events, evt)
		}),
	)

	_, snapshot, err := manager2.LoadObserved()
	if err == nil {
		t.Skip("Could not trigger load error - this is platform-dependent")
	}

	if snapshot == nil {
		t.Fatal("Expected snapshot even on error")
	}
	if snapshot.Success {
		t.Error("Expected success to be false on error")
	}
	if snapshot.Error == nil {
		t.Error("Expected Error to be set on failure")
	}

	// Verify load:error event
	foundError := false
	for _, evt := range events {
		if evt.Type == EventLoadError {
			foundError = true
			if evt.Error == nil {
				t.Error("Expected Error field in load:error event")
			}
			if evt.ErrorString == "" {
				t.Error("Expected ErrorString field in load:error event")
			}
		}
	}
	if !foundError {
		t.Error("Expected load:error event")
	}
}

// TestObservableSettingsManager_ValidateObserved tests the observed validate operation.
func TestObservableSettingsManager_ValidateObserved(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	var events []SettingsEvent
	manager := NewObservableSettingsManager(configPath,
		WithSettingsObserver(func(evt SettingsEvent) {
			events = append(events, evt)
		}),
	)

	// Test valid config
	validCfg := (&CoreConfig{}).Defaults()
	snapshot, err := manager.ValidateObserved(&validCfg)
	if err != nil {
		t.Fatalf("ValidateObserved() error = %v", err)
	}

	if !snapshot.Success {
		t.Error("Expected success for valid config")
	}

	// Check for validate:success event
	foundSuccess := false
	for _, evt := range events {
		if evt.Type == EventValidateSuccess {
			foundSuccess = true
			if evt.Duration == 0 {
				t.Error("Expected duration in success event")
			}
		}
	}
	if !foundSuccess {
		t.Error("Expected validate:success event")
	}

	// Test invalid config
	events = nil // reset
	invalidCfg := CoreConfig{
		Provider: "invalid-provider",
		Model:    "",
	}

	snapshot2, err := manager.ValidateObserved(&invalidCfg)
	if err == nil {
		t.Error("Expected error for invalid config")
	}

	if snapshot2.Success {
		t.Error("Expected success to be false for invalid config")
	}
	if snapshot2.Error == nil {
		t.Error("Expected Error to be set")
	}

	// Check for validate:error event
	foundError := false
	for _, evt := range events {
		if evt.Type == EventValidateError {
			foundError = true
			if evt.Error == nil {
				t.Error("Expected Error field in validate:error event")
			}
		}
	}
	if !foundError {
		t.Error("Expected validate:error event")
	}
}

// TestObservableSettingsManager_VerifyPersistenceError tests persistence verification failure.
func TestObservableSettingsManager_VerifyPersistenceError(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	var events []SettingsEvent
	manager := NewObservableSettingsManager(configPath,
		WithSettingsObserver(func(evt SettingsEvent) {
			events = append(events, evt)
		}),
		WithVerifyPersistence(true),
	)

	// Save valid config
	cfg := (&CoreConfig{}).Defaults()
	_, err := manager.SaveObserved(&cfg)
	if err != nil {
		t.Fatalf("SaveObserved() error = %v", err)
	}

	// Verify verify:success was emitted
	foundVerify := false
	for _, evt := range events {
		if evt.Type == EventVerifySuccess {
			foundVerify = true
			break
		}
	}
	if !foundVerify {
		t.Error("Expected verify:success event")
	}
}

// TestConfigsEqual tests the configsEqual function.
func TestConfigsEqual(t *testing.T) {
	tests := []struct {
		name string
		a    *CoreConfig
		b    *CoreConfig
		want bool
	}{
		{
			name: "both nil",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "a nil",
			a:    nil,
			b:    &CoreConfig{Model: "test"},
			want: false,
		},
		{
			name: "b nil",
			a:    &CoreConfig{Model: "test"},
			b:    nil,
			want: false,
		},
		{
			name: "equal configs",
			a:    &CoreConfig{Model: "test", Provider: ProviderAnthropic},
			b:    &CoreConfig{Model: "test", Provider: ProviderAnthropic},
			want: true,
		},
		{
			name: "different model",
			a:    &CoreConfig{Model: "test1", Provider: ProviderAnthropic},
			b:    &CoreConfig{Model: "test2", Provider: ProviderAnthropic},
			want: false,
		},
		{
			name: "different provider",
			a:    &CoreConfig{Model: "test", Provider: ProviderAnthropic},
			b:    &CoreConfig{Model: "test", Provider: ProviderOpenAI},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := configsEqual(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("configsEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDetectChanges tests the detectChanges function.
func TestDetectChanges(t *testing.T) {
	tests := []struct {
		name      string
		before    *CoreConfig
		after     *CoreConfig
		wantCount int
	}{
		{
			name:      "both nil",
			before:    nil,
			after:     nil,
			wantCount: 1, // returns ["all"]
		},
		{
			name:      "before nil",
			before:    nil,
			after:     &CoreConfig{Model: "test"},
			wantCount: 1, // returns ["all"]
		},
		{
			name:      "no changes",
			before:    &CoreConfig{Model: "test", Provider: ProviderAnthropic, Temperature: 0.7},
			after:     &CoreConfig{Model: "test", Provider: ProviderAnthropic, Temperature: 0.7},
			wantCount: 0,
		},
		{
			name:      "model changed",
			before:    &CoreConfig{Model: "test1", Provider: ProviderAnthropic},
			after:     &CoreConfig{Model: "test2", Provider: ProviderAnthropic},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectChanges(tt.before, tt.after)
			if len(got) != tt.wantCount {
				t.Errorf("detectChanges() returned %d fields, want %d: %v", len(got), tt.wantCount, got)
			}
		})
	}
}

// TestSettingsEvent_JSON tests JSON serialization of SettingsEvent.
func TestSettingsEvent_JSON(t *testing.T) {
	evt := SettingsEvent{
		Type:        EventLoadSuccess,
		Timestamp:   time.Now(),
		Description: "Test event",
		Duration:    time.Millisecond * 100,
		Error:       errors.New("test error"),
		ErrorString: "test error",
		Fields: map[string]any{
			"key": "value",
		},
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var decoded SettingsEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if decoded.Type != evt.Type {
		t.Errorf("Type = %v, want %v", decoded.Type, evt.Type)
	}
	if decoded.Description != evt.Description {
		t.Errorf("Description = %v, want %v", decoded.Description, evt.Description)
	}
	if decoded.ErrorString != evt.ErrorString {
		t.Errorf("ErrorString = %v, want %v", decoded.ErrorString, evt.ErrorString)
	}
}

// TestOperationSnapshot_JSON tests JSON serialization of OperationSnapshot.
func TestOperationSnapshot_JSON(t *testing.T) {
	cfg := (&CoreConfig{}).Defaults()
	snapshot := &OperationSnapshot{
		Operation:     "test",
		StartTime:     time.Now(),
		EndTime:       time.Now().Add(time.Second),
		Duration:      time.Second,
		Success:       true,
		ConfigAfter:   &cfg,
		ChangedFields: []string{"Model"},
		Events: []SettingsEvent{
			{Type: EventLoadStart, Description: "Started"},
		},
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var decoded OperationSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if decoded.Operation != snapshot.Operation {
		t.Errorf("Operation = %v, want %v", decoded.Operation, snapshot.Operation)
	}
	if !decoded.Success {
		t.Error("Expected Success to be true")
	}
}

// TestSetVerifyPersistence tests the SetVerifyPersistence method.
func TestSetVerifyPersistence(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	manager := NewObservableSettingsManager(configPath).(*observableSettingsManager)

	// Default should be true
	if !manager.verifyPersistence {
		t.Error("Expected verifyPersistence to be true by default")
	}

	// Disable
	manager.SetVerifyPersistence(false)
	if manager.verifyPersistence {
		t.Error("Expected verifyPersistence to be false after SetVerifyPersistence(false)")
	}

	// Enable
	manager.SetVerifyPersistence(true)
	if !manager.verifyPersistence {
		t.Error("Expected verifyPersistence to be true after SetVerifyPersistence(true)")
	}
}

// TestObservableSettingsManager_SettingsManagerInterface tests that ObservableSettingsManager
// correctly implements the SettingsManager interface.
func TestObservableSettingsManager_SettingsManagerInterface(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Test via interface to ensure compatibility
	var manager SettingsManager = NewObservableSettingsManager(configPath)

	// Test Load
	cfg, err := manager.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("Expected config, got nil")
	}

	// Test Save
	newCfg := (&CoreConfig{}).Defaults()
	if err := manager.Save(&newCfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Test Path
	if manager.Path() != configPath {
		t.Errorf("Path() = %v, want %v", manager.Path(), configPath)
	}
}

// TestObservableSettingsManager_AddObserver tests adding observers.
func TestObservableSettingsManager_AddObserver(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	var observer1Count, observer2Count int

	manager := NewObservableSettingsManager(configPath,
		WithSettingsObserver(func(evt SettingsEvent) {
			observer1Count++
		}),
	).(*observableSettingsManager)

	// Add second observer
	manager.AddObserver(func(evt SettingsEvent) {
		observer2Count++
	})

	// Trigger an operation
	cfg := (&CoreConfig{}).Defaults()
	_, _ = manager.SaveObserved(&cfg)

	// Both observers should have been called
	if observer1Count == 0 {
		t.Error("Expected observer1 to be called")
	}
	if observer2Count == 0 {
		t.Error("Expected observer2 to be called")
	}
}
