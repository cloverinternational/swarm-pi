package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatcherFlushesSpooledEvents(t *testing.T) {
	tmpDir := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		var batch BatchRequest
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Fatalf("decode batch: %v", err)
		}
		if len(batch.Events) != 1 {
			t.Fatalf("expected one event, got %d", len(batch.Events))
		}
		event := batch.Events[0]
		if event.DeviceIDHash != "device123" || event.WorkspaceHash != "workspace123" || event.WorkspaceLabel != "project" {
			t.Fatalf("unexpected identity fields: device=%q workspace=%q label=%q", event.DeviceIDHash, event.WorkspaceHash, event.WorkspaceLabel)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	dispatcher, err := NewDispatcher(Config{
		CollectorURL:   server.URL,
		AuthToken:      "token",
		Source:         "swarm-tui",
		AppVersion:     "test",
		BatchSize:      10,
		FlushInterval:  time.Hour,
		SpoolDir:       tmpDir,
		MaxSpoolBytes:  1 << 20,
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	defer dispatcher.Close()

	err = dispatcher.Enqueue(EventEnvelope{
		EventType:      "session.started",
		MachineIDHash:  "abc123",
		DeviceIDHash:   "device123",
		WorkspaceHash:  "workspace123",
		WorkspaceLabel: "project",
		Payload:        map[string]any{"path": "/Users/test/.ssh/id_rsa"},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if err := dispatcher.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected one request, got %d", requests)
	}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".json" {
			t.Fatalf("expected spool to be empty, found %s", entry.Name())
		}
	}
}

func TestDispatcherFlushesSpooledArtifacts(t *testing.T) {
	tmpDir := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/artifacts" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var batch ArtifactBatchRequest
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Fatalf("decode artifact batch: %v", err)
		}
		if len(batch.Artifacts) != 1 {
			t.Fatalf("expected one artifact, got %d", len(batch.Artifacts))
		}
		artifact := batch.Artifacts[0]
		if artifact.ArtifactType != "finding" || artifact.ArtifactID != "finding/1" {
			t.Fatalf("unexpected artifact identity: %#v", artifact)
		}
		if got := artifact.Payload["api_key"]; got != "[REDACTED:secret]" {
			t.Fatalf("expected redacted payload, got %#v", got)
		}
		if got := artifact.ArtifactPath; got != "[REDACTED:path]" {
			t.Fatalf("expected redacted artifact path, got %#v", got)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	dispatcher, err := NewDispatcher(Config{
		CollectorURL:   server.URL,
		AuthToken:      "token",
		Source:         "swarm-tui",
		AppVersion:     "test",
		BatchSize:      10,
		FlushInterval:  time.Hour,
		SpoolDir:       tmpDir,
		MaxSpoolBytes:  1 << 20,
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	defer dispatcher.Close()

	if err := dispatcher.EnqueueArtifact(ArtifactEnvelope{
		ArtifactID:    "finding/1",
		ArtifactType:  "finding",
		MachineIDHash: "abc123",
		ArtifactPath:  "/Users/test/.ssh/id_rsa",
		Payload:       map[string]any{"api_key": "secret"},
	}); err != nil {
		t.Fatalf("EnqueueArtifact: %v", err)
	}
	if err := dispatcher.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected one request, got %d", requests)
	}
}

func TestDispatcherCloseDrainsPendingEventsAndArtifacts(t *testing.T) {
	tmpDir := t.TempDir()
	var eventCount atomic.Int64
	var artifactCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/collect":
			var batch BatchRequest
			if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
				t.Errorf("decode event batch: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			eventCount.Add(int64(len(batch.Events)))
		case "/v1/artifacts":
			var batch ArtifactBatchRequest
			if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
				t.Errorf("decode artifact batch: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			artifactCount.Add(int64(len(batch.Artifacts)))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	dispatcher, err := NewDispatcher(Config{
		CollectorURL:         server.URL,
		AuthToken:            "token",
		Source:               "swarm-tui",
		AppVersion:           "test",
		BatchSize:            10,
		FlushInterval:        time.Hour,
		SpoolDir:             tmpDir,
		MaxSpoolBytes:        1 << 20,
		RequestTimeout:       2 * time.Second,
		ShutdownFlushTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}

	for i := range 73 {
		if err := dispatcher.Enqueue(EventEnvelope{
			EventID:       NewEventID(),
			EventType:     "message.created",
			MachineIDHash: "abc123",
			Payload:       map[string]any{"index": i},
		}); err != nil {
			t.Fatalf("Enqueue(%d): %v", i, err)
		}
	}
	for i := range 41 {
		if err := dispatcher.EnqueueArtifact(ArtifactEnvelope{
			ArtifactID:    NewEventID(),
			ArtifactType:  "finding",
			MachineIDHash: "abc123",
			Payload:       map[string]any{"index": i},
		}); err != nil {
			t.Fatalf("EnqueueArtifact(%d): %v", i, err)
		}
	}

	if err := dispatcher.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := eventCount.Load(); got != 73 {
		t.Fatalf("sent events = %d, want 73", got)
	}
	if got := artifactCount.Load(); got != 41 {
		t.Fatalf("sent artifacts = %d, want 41", got)
	}
	if got := countSpooledJSON(t, tmpDir); got != 0 {
		t.Fatalf("spooled files after Close = %d, want 0", got)
	}
}

func TestDispatcherCloseReturnsFlushErrorAndKeepsSpool(t *testing.T) {
	tmpDir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "collector unavailable", http.StatusInternalServerError)
	}))
	defer server.Close()

	dispatcher, err := NewDispatcher(Config{
		CollectorURL:         server.URL,
		AuthToken:            "token",
		Source:               "swarm-tui",
		AppVersion:           "test",
		BatchSize:            10,
		FlushInterval:        time.Hour,
		SpoolDir:             tmpDir,
		MaxSpoolBytes:        1 << 20,
		RequestTimeout:       2 * time.Second,
		ShutdownFlushTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	if err := dispatcher.Enqueue(EventEnvelope{
		EventType:     "message.created",
		MachineIDHash: "abc123",
		Payload:       map[string]any{"content": "hello"},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	err = dispatcher.Close()
	if err == nil {
		t.Fatalf("Close succeeded, want collector error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("Close error = %v, want HTTP 500 context", err)
	}
	if got := countSpooledJSON(t, tmpDir); got == 0 {
		t.Fatalf("expected failed event to remain spooled")
	}
}

func TestDispatcherRedactsBeforeSpooling(t *testing.T) {
	tmpDir := t.TempDir()
	dispatcher, err := NewDispatcher(Config{
		CollectorURL:         "://invalid",
		AuthToken:            "token",
		Source:               "swarm-tui",
		AppVersion:           "test",
		BatchSize:            10,
		FlushInterval:        time.Hour,
		SpoolDir:             tmpDir,
		MaxSpoolBytes:        1 << 20,
		RequestTimeout:       100 * time.Millisecond,
		ShutdownFlushTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	if err := dispatcher.Enqueue(EventEnvelope{
		EventType:     "message.created",
		MachineIDHash: "abc123",
		Payload: map[string]any{
			"prompt":        "use Bearer abcdefghijklmnop for the API",
			"credentialDir": "/Users/test/.aws/credentials",
		},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := dispatcher.EnqueueArtifact(ArtifactEnvelope{
		ArtifactType:  "finding",
		MachineIDHash: "abc123",
		ArtifactPath:  "/Users/test/.swarm/config/oauth/anthropic.json",
		Payload:       map[string]any{"api_key": "secret"},
	}); err != nil {
		t.Fatalf("EnqueueArtifact: %v", err)
	}
	_ = dispatcher.Close()

	events, err := dispatcher.spool.LoadBatch(10)
	if err != nil {
		t.Fatalf("LoadBatch events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("spooled events = %d, want 1", len(events))
	}
	payload := events[0].Event.Payload
	if payload["prompt"] != "[REDACTED:secret]" {
		t.Fatalf("spooled prompt = %#v, want redacted secret", payload["prompt"])
	}
	if payload["credentialDir"] != "[REDACTED:secret]" {
		t.Fatalf("spooled credentialDir = %#v, want sensitive key redaction", payload["credentialDir"])
	}

	artifacts, err := dispatcher.artifactSpool.LoadBatch(10)
	if err != nil {
		t.Fatalf("LoadBatch artifacts: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("spooled artifacts = %d, want 1", len(artifacts))
	}
	if artifacts[0].Artifact.ArtifactPath != "[REDACTED:path]" {
		t.Fatalf("artifact path = %#v, want redacted path", artifacts[0].Artifact.ArtifactPath)
	}
	if artifacts[0].Artifact.Payload["api_key"] != "[REDACTED:secret]" {
		t.Fatalf("artifact api_key = %#v, want redacted secret", artifacts[0].Artifact.Payload["api_key"])
	}
}

func TestDeviceIDHashUsesExplicitEnv(t *testing.T) {
	t.Setenv("SWARM_ANALYTICS_DEVICE_ID", "device-1")

	got, err := DeviceIDHash("test.namespace")
	if err != nil {
		t.Fatalf("DeviceIDHash: %v", err)
	}
	if want := HashValue("test.namespace", "device-1"); got != want {
		t.Fatalf("DeviceIDHash = %q, want %q", got, want)
	}
}

func TestWorkspaceHashAndLabel(t *testing.T) {
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "project")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got, want := WorkspaceHash("workspace.namespace", workspace), HashValue("workspace.namespace", resolvedWorkspace); got != want {
		t.Fatalf("WorkspaceHash = %q, want %q", got, want)
	}
	if got := WorkspaceLabel(workspace); got != "project" {
		t.Fatalf("WorkspaceLabel = %q, want project", got)
	}
	if got := WorkspaceHash("workspace.namespace", ""); got != "" {
		t.Fatalf("WorkspaceHash(empty) = %q, want empty", got)
	}
}

func countSpooledJSON(t *testing.T, dir string) int {
	t.Helper()
	count := 0
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			count++
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	return count
}
