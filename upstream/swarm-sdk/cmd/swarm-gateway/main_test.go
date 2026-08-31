package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/gateway"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lan"
)

type inertListener struct{}

func (inertListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (inertListener) Close() error              { return nil }
func (inertListener) Addr() net.Addr            { return &net.TCPAddr{} }

func validCommandAuthority(t *testing.T, now time.Time) *lan.CredentialAuthority {
	t.Helper()
	a, err := lan.NewMemoryCredentialAuthority([]lan.CredentialRecord{{
		ID: "client", Secret: "secret", Revision: 1, ExpiresAt: now.Add(time.Hour),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestParseConfigRejectsReusableSecretArgv(t *testing.T) {
	if _, err := parseConfig([]string{"-token", "must-not-enter-argv"}); err == nil {
		t.Fatal("legacy reusable -token argv input was accepted")
	}
	cfg, err := parseConfig([]string{"-credential-store", "/owner-only/credentials.json"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.credentialStore != "/owner-only/credentials.json" {
		t.Fatalf("credential store = %q", cfg.credentialStore)
	}
}

func TestInvalidPreflightMakesZeroBindBeaconAndServeCalls(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	base := gatewayConfig{
		addr:            "0.0.0.0:8787",
		advertiseHost:   "192.0.2.10",
		swarm:           "test",
		credentialStore: "/secure/credentials.json",
		tlsCert:         "/secure/listener.crt",
		tlsKey:          "/secure/listener.key",
	}
	for _, tc := range []struct {
		name      string
		authority func(string, time.Time) (*lan.CredentialAuthority, error)
		tls       func(string, string, string, time.Time) (*lan.ListenerTLSEvidence, error)
	}{
		{
			name: "invalid credential storage",
			authority: func(string, time.Time) (*lan.CredentialAuthority, error) {
				return nil, lan.ErrCredentialStorage
			},
		},
		{
			name: "expired credential state",
			authority: func(string, time.Time) (*lan.CredentialAuthority, error) {
				a, err := lan.NewMemoryCredentialAuthority([]lan.CredentialRecord{{
					ID: "client", Secret: "secret", Revision: 1, ExpiresAt: now.Add(-time.Second),
				}})
				return a, err
			},
			tls: func(string, string, string, time.Time) (*lan.ListenerTLSEvidence, error) {
				return nil, errors.New("must not need TLS after credential denial")
			},
		},
		{
			name:      "invalid certificate",
			authority: func(string, time.Time) (*lan.CredentialAuthority, error) { return validCommandAuthority(t, now), nil },
			tls: func(string, string, string, time.Time) (*lan.ListenerTLSEvidence, error) {
				return nil, lan.ErrListenerTLS
			},
		},
		{
			name:      "mismatched private key",
			authority: func(string, time.Time) (*lan.CredentialAuthority, error) { return validCommandAuthority(t, now), nil },
			tls: func(string, string, string, time.Time) (*lan.ListenerTLSEvidence, error) {
				return nil, errors.New("key mismatch")
			},
		},
		{
			name:      "wrong listener SAN",
			authority: func(string, time.Time) (*lan.CredentialAuthority, error) { return validCommandAuthority(t, now), nil },
			tls: func(string, string, string, time.Time) (*lan.ListenerTLSEvidence, error) {
				return nil, errors.New("SAN mismatch")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var binds, beacons, serves atomic.Int32
			deps := runtimeDeps{
				now:           func() time.Time { return now },
				loadAuthority: tc.authority,
				loadTLS:       tc.tls,
				listen: func(string, string) (net.Listener, error) {
					binds.Add(1)
					return inertListener{}, nil
				},
				startBeacon: func(context.Context, gateway.BeaconConfig) { beacons.Add(1) },
				serve: func(*http.Server, net.Listener, *lan.ListenerTLSEvidence) error {
					serves.Add(1)
					return nil
				},
			}
			if deps.loadTLS == nil {
				deps.loadTLS = func(string, string, string, time.Time) (*lan.ListenerTLSEvidence, error) {
					return nil, errors.New("unexpected TLS load")
				}
			}
			err := runGateway(context.Background(), base, deps)
			if err == nil {
				t.Fatal("invalid preflight unexpectedly succeeded")
			}
			if binds.Load() != 0 || beacons.Load() != 0 || serves.Load() != 0 {
				t.Fatalf("side effects after denial: binds=%d beacons=%d serves=%d err=%v",
					binds.Load(), beacons.Load(), serves.Load(), err)
			}
		})
	}
}

func TestAdvertisedListenerRequiresConcreteIdentity(t *testing.T) {
	got, err := advertisedListener("0.0.0.0:8787", "gateway.example")
	if err != nil {
		t.Fatal(err)
	}
	if got != "gateway.example:8787" {
		t.Fatalf("advertised listener = %q", got)
	}
	if _, err := advertisedListener("not-an-address", "gateway.example"); err == nil ||
		!strings.Contains(err.Error(), "invalid listen address") {
		t.Fatalf("malformed listen address err=%v", err)
	}
}

// deadlineRecordingResponseWriter wraps httptest.ResponseRecorder and
// additionally implements the SetWriteDeadline(time.Time) error method
// http.ResponseController looks for, proving serve.WithResponseWriteBounds
// actually attempted to install a deadline before dispatch -- not merely
// that the response still carries the right status/body.
type deadlineRecordingResponseWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
	calls    int
}

func (w *deadlineRecordingResponseWriter) SetWriteDeadline(t time.Time) error {
	w.calls++
	w.deadline = t
	return nil
}

// TestRunGateway_HandlerIsWrappedWithResponseWriteBounds proves the
// standalone command's own http.Server.Handler assignment (the "standalone
// command mux" the shared response-bound contract calls out separately from
// the embedded gateway.Server.Handler()) is wrapped with
// serve.WithResponseWriteBounds: it captures the *http.Server the injected
// serve dependency receives and confirms dispatching a request through its
// Handler installs a bounded write deadline before producing the normal,
// unauthenticated-and-public /api/version response.
func TestRunGateway_HandlerIsWrappedWithResponseWriteBounds(t *testing.T) {
	t.Setenv("SWARM_LAN_REGISTRY", "0") // avoid real LAN gossip side effects in this unit test.
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	var captured http.Handler
	deps := runtimeDeps{
		now: func() time.Time { return now },
		listen: func(string, string) (net.Listener, error) {
			return inertListener{}, nil
		},
		startBeacon: func(context.Context, gateway.BeaconConfig) {},
		serve: func(s *http.Server, _ net.Listener, _ *lan.ListenerTLSEvidence) error {
			captured = s.Handler
			return nil
		},
	}
	cfg := gatewayConfig{addr: "127.0.0.1:8787", swarm: "test"} // loopback: no credential/TLS preflight needed.
	if err := runGateway(context.Background(), cfg, deps); err != nil {
		t.Fatalf("runGateway: %v", err)
	}
	if captured == nil {
		t.Fatal("serve callback never observed a Handler")
	}

	w := &deadlineRecordingResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	r := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	captured.ServeHTTP(w, r)

	if w.calls == 0 {
		t.Fatal("expected the standalone command's http.Server.Handler to install a write deadline before dispatch")
	}
	if until := time.Until(w.deadline); until <= 0 || until > 15*time.Second {
		t.Fatalf("deadline = %v (%.2fs from now), want a bound roughly 10s in the future", w.deadline, until.Seconds())
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", w.Code, w.Body.String())
	}
}

// TestLanRegistryEnabled_ExplicitOptInOnly is the Phase 01 CONTRACT.md R5
// table test for the standalone gateway's LAN registry wiring gate: only the
// exact string "1" enables it (unset, empty, "0", and any other value must
// leave it disabled). This exercises the pure predicate directly — no
// os.Setenv/os.Getenv/t.Setenv anywhere in this test — so it mutates no
// process-global environment state and is safe alongside every other test
// in this package, parallel or not.
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
		{name: "unrelated value yes", raw: "yes", want: false},
		{name: "unrelated numeric value 2", raw: "2", want: false},
		{name: "whitespace-padded one is not exactly \"1\"", raw: " 1", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lanRegistryEnabled(tc.raw); got != tc.want {
				t.Errorf("lanRegistryEnabled(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
