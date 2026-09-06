package chat

import (
	"errors"
	"strings"
	"testing"
	"time"

	voiceruntime "github.com/Swarm-Code/mono/swarm-sdk/internal/voice/runtime"
)

func TestVoiceRuntimeReadyUsesInspectionForManagedModelReadiness(t *testing.T) {
	mismatch := voiceRuntimeResultMsg{
		health: voiceruntime.Health{Healthy: true, Status: "ok"},
		inspection: voiceruntime.Inspection{
			State: voiceruntime.State{Status: voiceruntime.StatusUnhealthy, Mode: voiceruntime.ModeManaged},
		},
	}
	if voiceRuntimeReady(mismatch) {
		t.Fatal("health-only response bypassed managed configured-model readiness")
	}

	mismatch.inspection.State.Status = voiceruntime.StatusRunning
	if !voiceRuntimeReady(mismatch) {
		t.Fatal("running inspection was not accepted as ready")
	}
}

func TestVoiceErrorNotificationLocalConnectionRefused(t *testing.T) {
	kind, text, dedupe := voiceErrorNotification(errors.New("Post \"http://127.0.0.1:8001/v1/audio/transcriptions\": dial tcp 127.0.0.1:8001: connect: connection refused"))
	if kind != "warning" {
		t.Fatalf("kind=%q want warning", kind)
	}
	if !strings.Contains(text, "local transcription server is not running") {
		t.Fatalf("text=%q missing local server guidance", text)
	}
	if dedupe < time.Minute {
		t.Fatalf("dedupe=%s want at least 1m", dedupe)
	}
}

func TestVoiceErrorNotificationGenericError(t *testing.T) {
	kind, text, dedupe := voiceErrorNotification(errors.New("remote service failed"))
	if kind != "error" {
		t.Fatalf("kind=%q want error", kind)
	}
	if !strings.Contains(text, "Voice error: remote service failed") {
		t.Fatalf("text=%q", text)
	}
	if dedupe != 10*time.Second {
		t.Fatalf("dedupe=%s want 10s", dedupe)
	}
}
