package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/lifecycle"
)

// This file covers /healthz — pure LIVENESS (ADR-005 "Readiness,
// publication, and adapters": "is the serve loop alive"), unchanged in
// wire meaning by the Phase 03 readiness split. /readyz — READINESS,
// requiring a successful self-probe before it ever reports true — is
// covered separately in daemon_health_test.go.
//
// A freshly constructed daemonState now starts in `lifecycle.StateStarting`
// (ADR-005: never `ready` before a self-probe — see newDaemonState in
// daemon_cli.go), so these tests set state.status directly to `ready`
// before exercising setWorking/setIdle's now-validated `ready -> working`
// and `working -> ready` transitions. That direct field write is test
// scaffolding only (same package, unexported field) standing in for a
// real self-probe, which daemon_health_test.go exercises separately via
// selfProbeReady; it is not a use of the production write path.
func TestDaemonHealthzHandler(t *testing.T) {
	state := newDaemonState("test-daemon", "test-model", "/tmp/ws")
	state.instanceToken = "instance-token"
	state.processStart = "process-start"
	state.executable = "/test/swarmos"
	state.status = lifecycle.StateReady
	state.setWorking("doing things")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	daemonHealthzHandler(state)(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["ok"] != true {
		t.Error("ok field missing or false")
	}
	if body["handle"] != "test-daemon" {
		t.Errorf("handle = %v", body["handle"])
	}
	// The literal wire value stays "working" — ADR-005's canonical lifecycle
	// state for a daemon with an accepted active execution — but it must now
	// be sourced from lifecycle.StateWorking.String() rather than a magic
	// string duplicated here, so a future vocabulary change fails this test
	// at compile/definition time instead of silently drifting.
	if body["status"] != lifecycle.StateWorking.String() {
		t.Errorf("status = %v, want %v", body["status"], lifecycle.StateWorking.String())
	}
	if body["current_task"] != "doing things" {
		t.Errorf("current_task = %v", body["current_task"])
	}
	if _, ok := body["version"].(string); !ok {
		t.Error("version missing")
	}
	if body["instance_token"] != state.instanceToken {
		t.Errorf("instance_token = %v", body["instance_token"])
	}
	if body["process_start"] != state.processStart {
		t.Errorf("process_start = %v", body["process_start"])
	}
	if body["executable"] != state.executable {
		t.Errorf("executable = %v", body["executable"])
	}
}

// TestDaemonHealthzHandlerIdleEmitsCanonicalReady proves ADR-005's
// "ready" — not legacy "idle" — is the canonical wire value once a daemon's
// active execution completes. "idle" remains a legacy-read/derived-display
// concept only; the current writer must never emit it as canonical state.
func TestDaemonHealthzHandlerIdleEmitsCanonicalReady(t *testing.T) {
	state := newDaemonState("test-daemon-2", "test-model", "/tmp/ws")
	state.status = lifecycle.StateReady
	state.setWorking("doing things")
	state.setIdle(nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	daemonHealthzHandler(state)(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["status"] != lifecycle.StateReady.String() {
		t.Errorf("status = %v, want %v (never legacy %q)", body["status"], lifecycle.StateReady.String(), "idle")
	}
}
