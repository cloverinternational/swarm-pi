package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionRecordRoundTripUsesPrivateAtomicManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	record := TUISessionRecord{
		Version:      1,
		ID:           "tui-round-trip",
		State:        sessionStateRunning,
		Intent:       sessionIntentRunning,
		Workspace:    "/workspace/project",
		Executable:   "/usr/local/bin/swarmos",
		Conversation: "conv-42",
		Args:         []string{"--workspace", "/workspace/project", "--conversation-id", "conv-42"},
		StartedAt:    time.Now().UTC(),
	}
	if err := saveSessionRecord(record); err != nil {
		t.Fatal(err)
	}
	got, err := loadSessionRecord(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Conversation != record.Conversation || got.Workspace != record.Workspace {
		t.Fatalf("round trip = %#v", got)
	}
	path, err := sessionPath(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode = %o, want 600", info.Mode().Perm())
	}
}

func TestReconcileMarksMissingProcessInterrupted(t *testing.T) {
	original := processEvidenceFunc
	t.Cleanup(func() { processEvidenceFunc = original })
	processEvidenceFunc = func(int) (string, string, string, error) {
		return "", "", "", errors.New("process missing")
	}
	record := TUISessionRecord{
		ID:           "tui-interrupted",
		State:        sessionStateRunning,
		Intent:       sessionIntentRunning,
		PID:          123,
		ProcessStart: "start-1",
		Executable:   filepath.Join(string(filepath.Separator), "swarmos"),
	}
	reconcileSessionRecord(&record)
	if record.State != sessionStateInterrupted {
		t.Fatalf("state = %q, want interrupted", record.State)
	}
	if record.Intent != sessionIntentRunning {
		t.Fatalf("intent = %q, want running for explicit recovery", record.Intent)
	}
}

func TestResumeRefusesMatchingLiveProcess(t *testing.T) {
	original := processEvidenceFunc
	t.Cleanup(func() { processEvidenceFunc = original })
	processEvidenceFunc = func(int) (string, string, string, error) {
		return "start-1", "/usr/local/bin/swarmos", "1000", nil
	}
	record := TUISessionRecord{
		ID:           "tui-live",
		State:        sessionStateRunning,
		Intent:       sessionIntentRunning,
		PID:          123,
		ProcessStart: "start-1",
		Executable:   "/usr/local/bin/swarmos",
		Workspace:    t.TempDir(),
	}
	if err := resumeSession(record); err == nil {
		t.Fatal("resume succeeded for a matching live process")
	}
}
