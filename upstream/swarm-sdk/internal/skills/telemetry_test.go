package skills

import (
	"sync/atomic"
	"testing"
)

func TestEmitSkillLoaded(t *testing.T) {
	ResetTelemetryListeners()

	var received atomic.Int32
	AddTelemetryListener(func(event SkillTelemetryEvent) {
		if event.Type != "skill_loaded" {
			t.Errorf("expected type skill_loaded, got %s", event.Type)
		}
		if event.SkillName != "test-skill" {
			t.Errorf("expected name test-skill, got %s", event.SkillName)
		}
		if event.Source != "project" {
			t.Errorf("expected source project, got %s", event.Source)
		}
		if event.Timestamp == 0 {
			t.Error("expected non-zero timestamp")
		}
		received.Add(1)
	})

	EmitSkillLoaded("test-skill", "project", "project")

	if received.Load() != 1 {
		t.Errorf("expected 1 event, got %d", received.Load())
	}
}

func TestEmitSkillInvoked(t *testing.T) {
	ResetTelemetryListeners()

	var received atomic.Int32
	AddTelemetryListener(func(event SkillTelemetryEvent) {
		if event.Type != "skill_invoked" {
			t.Errorf("expected type skill_invoked, got %s", event.Type)
		}
		if event.SkillName != "my-skill" {
			t.Errorf("expected name my-skill, got %s", event.SkillName)
		}
		if event.DurationMS != 150 {
			t.Errorf("expected duration 150, got %d", event.DurationMS)
		}
		if event.Error != "" {
			t.Errorf("expected no error, got %s", event.Error)
		}
		received.Add(1)
	})

	EmitSkillInvoked("my-skill", "user", 150, nil)

	if received.Load() != 1 {
		t.Errorf("expected 1 event, got %d", received.Load())
	}
}

func TestEmitSkillInvoked_WithError(t *testing.T) {
	ResetTelemetryListeners()

	AddTelemetryListener(func(event SkillTelemetryEvent) {
		if event.Error == "" {
			t.Error("expected error message in event")
		}
		if event.Type != "skill_invoked" {
			t.Errorf("expected type skill_invoked, got %s", event.Type)
		}
	})

	EmitSkillInvoked("fail-skill", "user", 0, &testErr{"execution failed"})
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

func TestTelemetryListener_PanicRecovery(t *testing.T) {
	ResetTelemetryListeners()

	var secondCalled atomic.Int32

	// First listener panics — should not prevent second from running
	AddTelemetryListener(func(event SkillTelemetryEvent) {
		panic("boom")
	})
	AddTelemetryListener(func(event SkillTelemetryEvent) {
		secondCalled.Add(1)
	})

	// Should not panic
	EmitSkillLoaded("recover-skill", "builtin", "builtin")

	if secondCalled.Load() != 1 {
		t.Error("second listener should still be called after first panics")
	}
}

func TestTelemetryListener_Multiple(t *testing.T) {
	ResetTelemetryListeners()

	var count atomic.Int32
	AddTelemetryListener(func(event SkillTelemetryEvent) { count.Add(1) })
	AddTelemetryListener(func(event SkillTelemetryEvent) { count.Add(1) })
	AddTelemetryListener(func(event SkillTelemetryEvent) { count.Add(1) })

	EmitSkillLoaded("multi-skill", "user", "user")

	if count.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", count.Load())
	}
}

func TestTelemetryListener_NoListeners(t *testing.T) {
	// Register a listener, then reset to remove all listeners.
	var count atomic.Int32
	AddTelemetryListener(func(event SkillTelemetryEvent) { count.Add(1) })
	ResetTelemetryListeners()

	// Emitting with no listeners must not panic and must not reach the
	// previously-registered (now removed) listener.
	EmitSkillLoaded("no-listeners", "user", "user")
	EmitSkillInvoked("no-listeners", "user", 100, nil)

	if got := count.Load(); got != 0 {
		t.Errorf("removed listener received %d events; expected 0 after reset", got)
	}
}

func TestResetTelemetryListeners(t *testing.T) {
	ResetTelemetryListeners()

	var count atomic.Int32
	AddTelemetryListener(func(event SkillTelemetryEvent) { count.Add(1) })

	ResetTelemetryListeners()

	EmitSkillLoaded("after-reset", "user", "user")

	if count.Load() != 0 {
		t.Errorf("expected 0 calls after reset, got %d", count.Load())
	}
}
