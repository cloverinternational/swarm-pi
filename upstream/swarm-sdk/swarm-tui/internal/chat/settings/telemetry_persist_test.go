package settings

import (
	"testing"
)

// TestTelemetrySettingDefaultsOff verifies telemetry opt-in is off by default
// and consent is unacknowledged on a fresh config.
func TestTelemetrySettingDefaultsOff(t *testing.T) {
	cm := testConfigManager(t)
	g := NewGeneralSettings(cm)
	if g.GetTelemetryEnabled() {
		t.Fatal("telemetry must default to OFF")
	}
	if g.TelemetryConsentAcknowledged() {
		t.Fatal("telemetry consent must default to unacknowledged")
	}
}

// TestTelemetrySetEnabledPersists verifies SetTelemetryEnabled(true) persists
// both the opt-in flag and the consent-acknowledged flag across a reload.
func TestTelemetrySetEnabledPersists(t *testing.T) {
	cm := testConfigManager(t)
	g := NewGeneralSettings(cm)

	g.SetTelemetryEnabled(true)
	if !g.GetTelemetryEnabled() {
		t.Fatal("expected telemetry enabled after SetTelemetryEnabled(true)")
	}
	if !g.TelemetryConsentAcknowledged() {
		t.Fatal("expected consent acknowledged after SetTelemetryEnabled")
	}

	reloaded := NewGeneralSettings(cm)
	if !reloaded.GetTelemetryEnabled() {
		t.Error("telemetry enabled did NOT persist to disk")
	}
	if !reloaded.TelemetryConsentAcknowledged() {
		t.Error("telemetry consent ack did NOT persist to disk")
	}
}

// TestTelemetrySetDisabledPersists verifies turning telemetry off persists.
func TestTelemetrySetDisabledPersists(t *testing.T) {
	cm := testConfigManager(t)
	g := NewGeneralSettings(cm)

	g.SetTelemetryEnabled(true)
	g.SetTelemetryEnabled(false)
	if g.GetTelemetryEnabled() {
		t.Fatal("expected telemetry disabled after SetTelemetryEnabled(false)")
	}

	reloaded := NewGeneralSettings(cm)
	if reloaded.GetTelemetryEnabled() {
		t.Error("telemetry disable did NOT persist to disk")
	}
	if !reloaded.TelemetryConsentAcknowledged() {
		t.Error("consent ack should remain true after answering (declining counts)")
	}
}
