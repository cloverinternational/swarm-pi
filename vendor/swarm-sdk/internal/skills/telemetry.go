package skills

import (
	"sync"
	"time"
)

// SkillTelemetryEvent represents a telemetry event for skill operations.
// Mirrors Claude Code's tengu_skill_loaded and skill usage tracking events.
//
// CONTRACT:
//   - Events are emitted via the global telemetry bus (AddTelemetryListener)
//   - Listeners must not block; they receive events asynchronously
//   - Event timestamps are UTC Unix milliseconds
type SkillTelemetryEvent struct {
	// Type is the event kind: "skill_loaded" or "skill_invoked".
	Type string `json:"type"`

	// SkillName is the name of the skill.
	SkillName string `json:"skill_name"`

	// Source indicates where the skill was discovered: "user", "project", "builtin", "mcp", "policy".
	Source string `json:"source,omitempty"`

	// LoadedFrom indicates the skill's load source (same as Source for backward compat).
	LoadedFrom string `json:"loaded_from,omitempty"`

	// DurationMS is the duration of the operation in milliseconds (for "skill_invoked").
	DurationMS int64 `json:"duration_ms,omitempty"`

	// Timestamp is the UTC Unix millisecond timestamp.
	Timestamp int64 `json:"ts"`

	// Error is set if the operation failed.
	Error string `json:"error,omitempty"`
}

// telemetryMu protects the listeners slice.
var telemetryMu sync.RWMutex

// telemetryListeners holds registered telemetry event listeners.
var telemetryListeners []func(SkillTelemetryEvent)

// AddTelemetryListener registers a callback for skill telemetry events.
// The callback is invoked asynchronously (in the caller's goroutine) for each
// skill_loaded and skill_invoked event. Listeners must not block.
//
// CONTRACT:
//   - The listener function must be safe to call from any goroutine
//   - Panics in listeners are recovered and silently dropped
//   - Listeners are called in registration order
func AddTelemetryListener(fn func(SkillTelemetryEvent)) {
	telemetryMu.Lock()
	defer telemetryMu.Unlock()
	telemetryListeners = append(telemetryListeners, fn)
}

// ResetTelemetryListeners removes all telemetry listeners.
// Primarily used in tests to avoid cross-test contamination.
func ResetTelemetryListeners() {
	telemetryMu.Lock()
	defer telemetryMu.Unlock()
	telemetryListeners = nil
}

// emitTelemetry sends an event to all registered listeners.
// Panics in individual listeners are recovered to prevent one bad
// listener from disrupting the rest.
func emitTelemetry(event SkillTelemetryEvent) {
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}

	telemetryMu.RLock()
	listeners := make([]func(SkillTelemetryEvent), len(telemetryListeners))
	copy(listeners, telemetryListeners)
	telemetryMu.RUnlock()

	for _, fn := range listeners {
		func() {
			defer func() { _ = recover() }() // Silently recover panics
			fn(event)
		}()
	}
}

// EmitSkillLoaded emits a "skill_loaded" telemetry event.
// Called at session start for each available skill, mirroring
// Claude Code's logSkillsLoaded (src/utils/telemetry/skillLoadedEvent.ts).
func EmitSkillLoaded(name, source, loadedFrom string) {
	emitTelemetry(SkillTelemetryEvent{
		Type:       "skill_loaded",
		SkillName:  name,
		Source:     source,
		LoadedFrom: loadedFrom,
	})
}

// EmitSkillInvoked emits a "skill_invoked" telemetry event.
// Called when a skill is invoked via the SkillTool, mirroring
// Claude Code's recordSkillUsage (src/utils/suggestions/skillUsageTracking.ts).
func EmitSkillInvoked(name, source string, durationMS int64, err error) {
	event := SkillTelemetryEvent{
		Type:       "skill_invoked",
		SkillName:  name,
		Source:     source,
		DurationMS: durationMS,
	}
	if err != nil {
		event.Error = err.Error()
	}
	emitTelemetry(event)
}
