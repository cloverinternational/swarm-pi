package main

// Phase 01 security review repair R5 — deterministic coverage for the two
// blockers this worker (`.swarmflow/swarm-attach-architecture/
// p01-security-lifecycle-repair-r5/CONTRACT.md`) owns in the daemon binary:
//
//  1. both daemon production http.Server constructions (the loopback
//     client-interface/"attach" server and the LAN gateway server) must
//     carry a positive 10-second WriteTimeout backstop, in addition to the
//     per-handler response deadlines the serve package installs; and
//  2. the LAN peer registry must be wired only on explicit opt-in
//     (SWARM_LAN_REGISTRY=1) — unset, empty, "0", and any other value must
//     leave it disabled.
//
// These tests exercise the small, extracted constructors/predicate directly
// (no listener bound, no goroutine started, no environment mutated) so they
// are fast, deterministic, and safe to run under -race and in parallel with
// every other package test.

import (
	"net/http"
	"testing"
	"time"
)

// TestNewDaemonClientServer_HasPositiveTenSecondWriteTimeout proves the
// loopback client-interface ("attach") server — the one runDaemon binds and
// serves /rpc, /sse, /ws, and /healthz on — always carries the Phase 01 R5
// write-timeout backstop, and that the handler passed in is wired through
// unchanged (not silently dropped or replaced).
func TestNewDaemonClientServer_HasPositiveTenSecondWriteTimeout(t *testing.T) {
	handler := http.NewServeMux()
	srv := newDaemonClientServer(handler)
	if srv == nil {
		t.Fatal("newDaemonClientServer returned nil")
	}
	if srv.WriteTimeout != 10*time.Second {
		t.Fatalf("WriteTimeout = %v, want exactly 10s", srv.WriteTimeout)
	}
	if srv.WriteTimeout <= 0 {
		t.Fatalf("WriteTimeout = %v, want a positive backstop", srv.WriteTimeout)
	}
	if srv.Handler == nil {
		t.Fatal("Handler not wired through")
	}
	got, ok := srv.Handler.(*http.ServeMux)
	if !ok || got != handler {
		t.Fatalf("Handler = %#v, want the exact *http.ServeMux passed in", srv.Handler)
	}
}

// TestNewDaemonGatewayServer_HasPositiveTenSecondWriteTimeout is the LAN
// gateway server's counterpart to the test above — the server
// serveDaemonGateway binds and serves gw.Handler() on.
func TestNewDaemonGatewayServer_HasPositiveTenSecondWriteTimeout(t *testing.T) {
	handler := http.NewServeMux()
	srv := newDaemonGatewayServer(handler)
	if srv == nil {
		t.Fatal("newDaemonGatewayServer returned nil")
	}
	if srv.WriteTimeout != 10*time.Second {
		t.Fatalf("WriteTimeout = %v, want exactly 10s", srv.WriteTimeout)
	}
	if srv.WriteTimeout <= 0 {
		t.Fatalf("WriteTimeout = %v, want a positive backstop", srv.WriteTimeout)
	}
	if srv.Handler == nil {
		t.Fatal("Handler not wired through")
	}
	got, ok := srv.Handler.(*http.ServeMux)
	if !ok || got != handler {
		t.Fatalf("Handler = %#v, want the exact *http.ServeMux passed in", srv.Handler)
	}
}

// TestNewDaemonClientServer_And_NewDaemonGatewayServer_AreIndependentInstances
// guards against a refactor accidentally sharing one *http.Server (and
// therefore one WriteTimeout, one listener, one Shutdown lifecycle) between
// the two logically distinct daemon servers.
func TestNewDaemonClientServer_And_NewDaemonGatewayServer_AreIndependentInstances(t *testing.T) {
	h := http.NewServeMux()
	clientSrv := newDaemonClientServer(h)
	gatewaySrv := newDaemonGatewayServer(h)
	if clientSrv == gatewaySrv {
		t.Fatal("expected two independent *http.Server instances, got the same pointer")
	}
}

// TestLanRegistryEnabled_ExplicitOptInOnly is the Phase 01 R5 table test for
// the daemon's LAN registry wiring gate: only the exact string "1" enables
// it. This exercises the pure predicate directly (no os.Setenv/os.Getenv
// anywhere in this test), so it needs no t.Setenv, mutates no process-global
// state, and is safe to run with t.Parallel() (deliberately not used here to
// keep this file's tests trivially reviewable as a flat table, but nothing
// about the predicate would make parallel execution unsafe).
func TestLanRegistryEnabled_ExplicitOptInOnly(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "unset (empty string, as os.Getenv returns for an unset var)", raw: "", want: false},
		{name: "empty string explicitly", raw: "", want: false},
		{name: "zero disables", raw: "0", want: false},
		{name: "one enables", raw: "1", want: true},
		{name: "unrelated value true", raw: "true", want: false},
		{name: "unrelated value TRUE (case)", raw: "TRUE", want: false},
		{name: "unrelated value yes", raw: "yes", want: false},
		{name: "unrelated value on", raw: "on", want: false},
		{name: "unrelated numeric value 2", raw: "2", want: false},
		{name: "whitespace-padded one is not exactly \"1\"", raw: " 1", want: false},
		{name: "trailing-whitespace one is not exactly \"1\"", raw: "1 ", want: false},
		{name: "leading zero", raw: "01", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lanRegistryEnabled(tc.raw); got != tc.want {
				t.Errorf("lanRegistryEnabled(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
