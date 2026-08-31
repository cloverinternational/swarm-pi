package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type beaconConn struct {
	writes atomic.Int32
	data   chan []byte
}

func (c *beaconConn) Read([]byte) (int, error)         { return 0, errors.New("unused") }
func (c *beaconConn) Close() error                     { return nil }
func (c *beaconConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (c *beaconConn) RemoteAddr() net.Addr             { return &net.UDPAddr{} }
func (c *beaconConn) SetDeadline(time.Time) error      { return nil }
func (c *beaconConn) SetReadDeadline(time.Time) error  { return nil }
func (c *beaconConn) SetWriteDeadline(time.Time) error { return nil }
func (c *beaconConn) Write(p []byte) (int, error) {
	c.writes.Add(1)
	c.data <- append([]byte(nil), p...)
	return len(p), nil
}

func TestAuthenticatedBeaconUsesHTTPSAndWSS(t *testing.T) {
	now := time.Now()
	evidence := testTLSEvidence(t, now, "192.0.2.10")
	payload, err := authenticatedBeaconPayload(BeaconConfig{
		Host: "192.0.2.10", Port: "8787", Swarm: "test", TLSEvidence: evidence,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	var got beacon
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.URL, "https://") || !strings.HasPrefix(got.WSURL, "wss://") {
		t.Fatalf("beacon URL=%q wsURL=%q, want HTTPS/WSS", got.URL, got.WSURL)
	}
	if !got.TokenRequired {
		t.Fatal("authenticated beacon must advertise credential requirement")
	}
}

func TestAuthenticatedBeaconInvalidEvidenceMakesZeroNetworkCalls(t *testing.T) {
	original := beaconDial
	defer func() { beaconDial = original }()
	var dials atomic.Int32
	beaconDial = func(*net.UDPAddr) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("must not dial")
	}
	err := StartAuthenticatedBeacon(context.Background(), BeaconConfig{
		Host: "192.0.2.10", Port: "8787", Swarm: "test",
	})
	if !errors.Is(err, errBeaconTLS) {
		t.Fatalf("err=%v, want TLS evidence denial", err)
	}
	if got := dials.Load(); got != 0 {
		t.Fatalf("invalid evidence opened beacon network %d times", got)
	}
}

func TestPlaintextBeaconUsesHTTPAndWS(t *testing.T) {
	payload, err := plaintextBeaconPayload("192.0.2.10", "8787", "test", false, time.Unix(123, 0))
	if err != nil {
		t.Fatal(err)
	}
	var got beacon
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.URL, "http://") || !strings.HasPrefix(got.WSURL, "ws://") {
		t.Fatalf("beacon URL=%q wsURL=%q, want HTTP/WS", got.URL, got.WSURL)
	}
	if got.TokenRequired {
		t.Fatal("open plaintext beacon must not require a token")
	}
	if got.Sent != 123 {
		t.Fatalf("beacon sent=%d, want 123", got.Sent)
	}
}

func TestStartAuthenticatedBeaconWritesValidatedPayload(t *testing.T) {
	original := beaconDial
	defer func() { beaconDial = original }()
	conn := &beaconConn{data: make(chan []byte, 1)}
	beaconDial = func(*net.UDPAddr) (net.Conn, error) { return conn, nil }
	now := time.Now()
	evidence := testTLSEvidence(t, now, "192.0.2.10")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- StartAuthenticatedBeacon(ctx, BeaconConfig{
			Host: "192.0.2.10", Port: "8787", Swarm: "test", TLSEvidence: evidence,
		})
	}()
	payload := <-conn.data
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"http://`) || strings.Contains(string(payload), `"ws://`) {
		t.Fatalf("authenticated beacon contains plaintext scheme: %s", payload)
	}
}
