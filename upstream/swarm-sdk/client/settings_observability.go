// Package client provides built-in observability for settings operations.
//
// # Observability System
//
// The observability system tracks settings lifecycle events to help trace
// errors and verify state consistency across Swarm applications.
//
// Features:
//   - Operation tracing (Load, Save, Validate)
//   - State change detection with diff reporting
//   - Persistence verification (did write match intent?)
//   - Hook system for custom logging/metrics
//   - Zero overhead when disabled
//
// Basic usage:
//
//	// Create observable settings manager
//	manager := client.NewObservableSettingsManager(
//	    "~/.swarm/config/config.yaml",
//	    client.WithSettingsObserver(func(evt client.SettingsEvent) {
//	        log.Printf("[Settings] %s: %s", evt.Type, evt.Description)
//	    }),
//
// )
//
//	// Load with automatic tracing
//	cfg, snapshot, err := manager.LoadObserved()
//	if err != nil {
//	    // Check snapshot for operation details
//	    log.Printf("Load failed after %v", snapshot.Duration)
//	}
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"time"
)

// SettingsEventType categorizes settings lifecycle events.
type SettingsEventType string

const (
	// EventLoadStart - settings load initiated
	EventLoadStart SettingsEventType = "load:start"
	// EventLoadSuccess - settings loaded successfully
	EventLoadSuccess SettingsEventType = "load:success"
	// EventLoadError - settings load failed
	EventLoadError SettingsEventType = "load:error"
	// EventLoadDefaults - returned defaults (file not found)
	EventLoadDefaults SettingsEventType = "load:defaults"

	// EventSaveStart - settings save initiated
	EventSaveStart SettingsEventType = "save:start"
	// EventSaveSuccess - settings saved successfully
	EventSaveSuccess SettingsEventType = "save:success"
	// EventSaveError - settings save failed
	EventSaveError SettingsEventType = "save:error"

	// EventValidateStart - validation initiated
	EventValidateStart SettingsEventType = "validate:start"
	// EventValidateSuccess - validation passed
	EventValidateSuccess SettingsEventType = "validate:success"
	// EventValidateError - validation failed
	EventValidateError SettingsEventType = "validate:error"

	// EventStateChange - configuration changed
	EventStateChange SettingsEventType = "state:change"
	// EventVerifySuccess - persistence verified (written == intent)
	EventVerifySuccess SettingsEventType = "verify:success"
	// EventVerifyError - persistence mismatch (written != intent)
	EventVerifyError SettingsEventType = "verify:error"
)

// SettingsEvent represents a settings lifecycle event.
type SettingsEvent struct {
	// Type is the event category
	Type SettingsEventType `json:"type"`
	// Timestamp is when the event occurred
	Timestamp time.Time `json:"timestamp"`
	// Description is a human-readable summary
	Description string `json:"description"`
	// Duration is how long the operation took (if applicable)
	Duration time.Duration `json:"duration,omitempty"`
	// Error is the error that occurred (if any)
	Error error `json:"-"`
	// ErrorString is the string representation of the error
	ErrorString string `json:"error,omitempty"`
	// Fields contains additional context (field names changed, etc.)
	Fields map[string]any `json:"fields,omitempty"`
}

// OperationSnapshot captures the complete state of a settings operation.
type OperationSnapshot struct {
	// Operation is the type of operation performed
	Operation string `json:"operation"`
	// StartTime is when the operation began
	StartTime time.Time `json:"start_time"`
	// EndTime is when the operation completed
	EndTime time.Time `json:"end_time"`
	// Duration is the total operation time
	Duration time.Duration `json:"duration,omitempty"`
	// Success indicates if the operation succeeded
	Success bool `json:"success"`
	// Error is any error that occurred
	Error error `json:"-"`
	// ConfigBefore is the configuration before the operation
	ConfigBefore *CoreConfig `json:"config_before,omitempty"`
	// ConfigAfter is the configuration after the operation
	ConfigAfter *CoreConfig `json:"config_after,omitempty"`
	// ChangedFields lists fields that were modified
	ChangedFields []string `json:"changed_fields,omitempty"`
	// Events is the chain of events during this operation
	Events []SettingsEvent `json:"events"`
}

// SettingsObserver is called for each settings event.
// Return false to stop further processing of this event chain.
type SettingsObserver func(SettingsEvent)

// ObservableSettingsManager extends SettingsManager with observability features.
type ObservableSettingsManager interface {
	SettingsManager

	// LoadObserved loads settings and returns a snapshot of the operation
	LoadObserved() (*CoreConfig, *OperationSnapshot, error)

	// SaveObserved saves settings and returns a snapshot of the operation
	SaveObserved(*CoreConfig) (*OperationSnapshot, error)

	// ValidateObserved validates config and returns a snapshot
	ValidateObserved(*CoreConfig) (*OperationSnapshot, error)

	// AddObserver adds an observer to receive events
	AddObserver(SettingsObserver)

	// RemoveObserver removes a previously added observer
	RemoveObserver(SettingsObserver)

	// SetVerifyPersistence enables/disables post-save verification
	SetVerifyPersistence(bool)
}

// ObservableManagerOptions configures an ObservableSettingsManager.
type ObservableManagerOptions struct {
	observers         []SettingsObserver
	verifyPersistence bool
	context           context.Context
}

// ObservableManagerOption is a functional option for configuring observability.
type ObservableManagerOption func(*ObservableManagerOptions)

// WithSettingsObserver adds an observer to receive settings events.
func WithSettingsObserver(observer SettingsObserver) ObservableManagerOption {
	return func(o *ObservableManagerOptions) {
		o.observers = append(o.observers, observer)
	}
}

// WithVerifyPersistence enables post-save verification that written state matches intent.
func WithVerifyPersistence(enable bool) ObservableManagerOption {
	return func(o *ObservableManagerOptions) {
		o.verifyPersistence = enable
	}
}

// WithObservabilityContext sets the context for observability operations.
func WithObservabilityContext(ctx context.Context) ObservableManagerOption {
	return func(o *ObservableManagerOptions) {
		o.context = ctx
	}
}

// observableSettingsManager implements ObservableSettingsManager.
type observableSettingsManager struct {
	base              SettingsManager
	observers         []SettingsObserver
	verifyPersistence bool
}

// NewObservableSettingsManager creates an observable settings manager.
//
// Example:
//
//	manager := client.NewObservableSettingsManager(
//	    "~/.swarm/config/config.yaml",
//	    client.WithSettingsObserver(func(evt client.SettingsEvent) {
//	        if evt.Error != nil {
//	            log.Printf("[Settings Error] %s: %v", evt.Type, evt.Error)
//	        }
//	    }),
//	    client.WithVerifyPersistence(true),
//
// )
func NewObservableSettingsManager(configPath string, opts ...ObservableManagerOption) ObservableSettingsManager {
	options := &ObservableManagerOptions{
		verifyPersistence: true, // enabled by default
	}
	for _, opt := range opts {
		opt(options)
	}

	return &observableSettingsManager{
		base:              NewSettingsManager(configPath),
		observers:         options.observers,
		verifyPersistence: options.verifyPersistence,
	}
}

// LoadObserved loads settings with full observability.
func (o *observableSettingsManager) LoadObserved() (*CoreConfig, *OperationSnapshot, error) {
	snapshot := &OperationSnapshot{
		Operation: "load",
		StartTime: time.Now(),
		Events:    make([]SettingsEvent, 0),
		Success:   false,
	}

	o.emitEvent(SettingsEvent{
		Type:        EventLoadStart,
		Timestamp:   time.Now(),
		Description: fmt.Sprintf("Loading settings from %s", o.base.Path()),
	})

	cfg, err := o.base.Load()

	snapshot.EndTime = time.Now()
	snapshot.Duration = snapshot.EndTime.Sub(snapshot.StartTime)
	snapshot.ConfigAfter = cfg

	if err != nil {
		snapshot.Error = err
		o.emitEvent(SettingsEvent{
			Type:        EventLoadError,
			Timestamp:   time.Now(),
			Description: "Settings load failed",
			Error:       err,
			ErrorString: err.Error(),
		})
		return cfg, snapshot, err
	}

	// Detect if we returned defaults (file didn't exist)
	_, statErr := statFile(o.base.Path())
	if statErr != nil {
		o.emitEvent(SettingsEvent{
			Type:        EventLoadDefaults,
			Timestamp:   time.Now(),
			Description: "Config file not found, returning defaults",
		})
	}

	snapshot.Success = true
	o.emitEvent(SettingsEvent{
		Type:        EventLoadSuccess,
		Timestamp:   time.Now(),
		Description: fmt.Sprintf("Settings loaded in %v", snapshot.Duration),
		Duration:    snapshot.Duration,
	})

	return cfg, snapshot, nil
}

// SaveObserved saves settings with full observability and optional verification.
func (o *observableSettingsManager) SaveObserved(cfg *CoreConfig) (*OperationSnapshot, error) {
	snapshot := &OperationSnapshot{
		Operation:   "save",
		StartTime:   time.Now(),
		Events:      make([]SettingsEvent, 0),
		Success:     false,
		ConfigAfter: cfg,
	}

	// Load previous config to detect changes
	prevCfg, _ := o.base.Load()
	snapshot.ConfigBefore = prevCfg

	if prevCfg != nil && cfg != nil {
		snapshot.ChangedFields = detectChanges(prevCfg, cfg)
	}

	o.emitEvent(SettingsEvent{
		Type:        EventSaveStart,
		Timestamp:   time.Now(),
		Description: fmt.Sprintf("Saving settings to %s", o.base.Path()),
		Fields: map[string]any{
			"changed_fields": snapshot.ChangedFields,
		},
	})

	if len(snapshot.ChangedFields) > 0 {
		o.emitEvent(SettingsEvent{
			Type:        EventStateChange,
			Timestamp:   time.Now(),
			Description: fmt.Sprintf("Configuration changed: %v", snapshot.ChangedFields),
			Fields: map[string]any{
				"fields": snapshot.ChangedFields,
			},
		})
	}

	err := o.base.Save(cfg)

	snapshot.EndTime = time.Now()
	snapshot.Duration = snapshot.EndTime.Sub(snapshot.StartTime)

	if err != nil {
		snapshot.Error = err
		o.emitEvent(SettingsEvent{
			Type:        EventSaveError,
			Timestamp:   time.Now(),
			Description: "Settings save failed",
			Error:       err,
			ErrorString: err.Error(),
		})
		return snapshot, err
	}

	snapshot.Success = true

	// Verify persistence if enabled
	if o.verifyPersistence {
		if verifyErr := o.verifyWrittenState(cfg); verifyErr != nil {
			o.emitEvent(SettingsEvent{
				Type:        EventVerifyError,
				Timestamp:   time.Now(),
				Description: "Persistence verification failed - written state does not match intent",
				Error:       verifyErr,
				ErrorString: verifyErr.Error(),
			})
		} else {
			o.emitEvent(SettingsEvent{
				Type:        EventVerifySuccess,
				Timestamp:   time.Now(),
				Description: "Persistence verified - written state matches intent",
			})
		}
	}

	o.emitEvent(SettingsEvent{
		Type:        EventSaveSuccess,
		Timestamp:   time.Now(),
		Description: fmt.Sprintf("Settings saved in %v", snapshot.Duration),
		Duration:    snapshot.Duration,
	})

	return snapshot, nil
}

// ValidateObserved validates config with observability.
func (o *observableSettingsManager) ValidateObserved(cfg *CoreConfig) (*OperationSnapshot, error) {
	snapshot := &OperationSnapshot{
		Operation:   "validate",
		StartTime:   time.Now(),
		Events:      make([]SettingsEvent, 0),
		Success:     false,
		ConfigAfter: cfg,
	}

	o.emitEvent(SettingsEvent{
		Type:        EventValidateStart,
		Timestamp:   time.Now(),
		Description: "Validating configuration",
	})

	err := cfg.Validate()

	snapshot.EndTime = time.Now()
	snapshot.Duration = snapshot.EndTime.Sub(snapshot.StartTime)

	if err != nil {
		snapshot.Error = err
		o.emitEvent(SettingsEvent{
			Type:        EventValidateError,
			Timestamp:   time.Now(),
			Description: "Configuration validation failed",
			Error:       err,
			ErrorString: err.Error(),
		})
		return snapshot, err
	}

	snapshot.Success = true
	o.emitEvent(SettingsEvent{
		Type:        EventValidateSuccess,
		Timestamp:   time.Now(),
		Description: fmt.Sprintf("Validation passed in %v", snapshot.Duration),
		Duration:    snapshot.Duration,
	})

	return snapshot, nil
}

// Load delegates to base manager (satisfies SettingsManager interface).
func (o *observableSettingsManager) Load() (*CoreConfig, error) {
	return o.base.Load()
}

// Save delegates to base manager (satisfies SettingsManager interface).
func (o *observableSettingsManager) Save(cfg *CoreConfig) error {
	return o.base.Save(cfg)
}

// Path returns the configuration path.
func (o *observableSettingsManager) Path() string {
	return o.base.Path()
}

// AddObserver adds an observer to receive events.
func (o *observableSettingsManager) AddObserver(observer SettingsObserver) {
	o.observers = append(o.observers, observer)
}

// RemoveObserver removes a previously added observer.
func (o *observableSettingsManager) RemoveObserver(observer SettingsObserver) {
	// Since we can't compare functions directly, we use a placeholder approach
	// In practice, observers should be managed by reference equality at call site
	// For now, this is a no-op - observers are typically short-lived
}

// SetVerifyPersistence enables/disables post-save verification.
func (o *observableSettingsManager) SetVerifyPersistence(enable bool) {
	o.verifyPersistence = enable
}

// emitEvent sends an event to all registered observers.
func (o *observableSettingsManager) emitEvent(evt SettingsEvent) {
	for _, observer := range o.observers {
		if observer != nil {
			observer(evt)
		}
	}
}

// verifyWrittenState reads back the saved config and verifies it matches intent.
func (o *observableSettingsManager) verifyWrittenState(intended *CoreConfig) error {
	written, err := o.base.Load()
	if err != nil {
		return fmt.Errorf("verify: failed to read back saved config: %w", err)
	}

	// Compare intended vs written
	if !configsEqual(intended, written) {
		intendedJSON, _ := json.MarshalIndent(intended, "", "  ")
		writtenJSON, _ := json.MarshalIndent(written, "", "  ")
		return fmt.Errorf(
			"verify: written config does not match intent\n"+
				"Intended:\n%s\n"+
				"Written:\n%s",
			intendedJSON, writtenJSON,
		)
	}

	return nil
}

// configsEqual compares two CoreConfig structs for equality.
func configsEqual(a, b *CoreConfig) bool {
	if a == nil || b == nil {
		return a == b // both nil or one nil
	}

	// Use JSON serialization for deep equality check
	aJSON, err1 := json.Marshal(a)
	bJSON, err2 := json.Marshal(b)

	if err1 != nil || err2 != nil {
		return false
	}

	return string(aJSON) == string(bJSON)
}

// detectChanges returns a list of field names that differ between two configs.
func detectChanges(before, after *CoreConfig) []string {
	if before == nil || after == nil {
		return []string{"all"} // full change if either is nil
	}

	changes := []string{}

	v1 := reflect.ValueOf(before).Elem()
	v2 := reflect.ValueOf(after).Elem()
	t := v1.Type()

	for i := 0; i < v1.NumField(); i++ {
		field := t.Field(i)
		val1 := v1.Field(i)
		val2 := v2.Field(i)

		// Skip unexported fields
		if !val1.CanInterface() {
			continue
		}

		if !reflect.DeepEqual(val1.Interface(), val2.Interface()) {
			changes = append(changes, field.Name)
		}
	}

	return changes
}

// statFile is a helper to check if a file exists (avoids import cycle with os usage).
func statFile(path string) (any, error) {
	return osStat(path)
}

// osStat wraps os.Stat for testability.
var osStat = os.Stat
