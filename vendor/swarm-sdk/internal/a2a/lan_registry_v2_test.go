package a2a

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/lan/gossip"
)

// TestStartLANRegistryV2_DisabledStartsNoListener proves the v2 loop never
// starts when EnableSignedGossipV2 is false — not by reading the flag back,
// but by independently binding the SAME UDP port StartLANRegistryV2 would
// have used and asserting that bind succeeds (i.e. nothing is listening
// there). If the loop had started despite the flag being false, this bind
// would fail with "address already in use".
func TestStartLANRegistryV2_DisabledStartsNoListener(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const port = 58790 // fixed-but-test-scoped port, distinct from production PeerSyncV2Port
	stop, err := StartLANRegistryV2(context.Background(), LANRegistryV2Config{
		EnableSignedGossipV2: false,
		Port:                 port,
	})
	if err != nil {
		t.Fatalf("StartLANRegistryV2 with flag=false returned an error: %v", err)
	}
	defer stop()

	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		t.Fatalf("expected port %d to be free (no v2 listener bound) since the flag was false, but bind failed: %v", port, err)
	}
	defer probe.Close()
}

// TestStartLANRegistryV2_DisabledReturnsNoOpStop proves the returned stop
// function is safe to call unconditionally (it does nothing, does not
// panic, and is idempotent) even though nothing was started.
func TestStartLANRegistryV2_DisabledReturnsNoOpStop(t *testing.T) {
	stop, err := StartLANRegistryV2(context.Background(), LANRegistryV2Config{EnableSignedGossipV2: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stop()
	stop() // idempotent
}

// TestStartLANRegistryV2_EndToEnd_VerifiedFrameUpsertsRemotePeer sends a
// real, validly-signed v2 frame over a real UDP loopback socket to a real
// StartLANRegistryV2 listener and asserts the shared on-disk presence
// registry ends up with exactly one persisted remote peer whose fields
// were derived from the verified frame — proving the enabled loop actually
// verifies inbound frames and reuses the EXISTING upsertRemotePeer write
// path, not merely that some internal function was unit-called.
func TestStartLANRegistryV2_EndToEnd_VerifiedFrameUpsertsRemotePeer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const port = 58791 // fixed-but-test-scoped port, distinct from production PeerSyncV2Port
	stop, err := StartLANRegistryV2(context.Background(), LANRegistryV2Config{
		Swarm:                DefaultSwarmName,
		EnableSignedGossipV2: true,
		Port:                 port,
	})
	if err != nil {
		t.Fatalf("StartLANRegistryV2: %v", err)
	}
	defer stop()

	signer, err := gossip.NewSigner()
	if err != nil {
		t.Fatalf("gossip.NewSigner: %v", err)
	}
	now := time.Now()
	frame, err := gossip.Sign(signer, "peer-e2e", "daemon-e2e", "eth-e2e", now, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("gossip.Sign: %v", err)
	}
	wire, err := frame.Bytes()
	if err != nil {
		t.Fatalf("frame.Bytes: %v", err)
	}

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatalf("dial loopback v2 listener: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write(wire); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	wantHandle := truncatedHexHash("peer-e2e") + "~" + truncatedHexHash("daemon-e2e")

	deadline := time.Now().Add(5 * time.Second)
	var peers []PeerPresence
	for time.Now().Before(deadline) {
		peers, err = ListPeers(DefaultSwarmName)
		if err != nil {
			t.Fatalf("ListPeers: %v", err)
		}
		if len(peers) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if len(peers) != 1 {
		t.Fatalf("expected exactly 1 persisted remote peer after one verified v2 frame, got %d: %+v", len(peers), peers)
	}
	got := peers[0]
	if got.Handle != wantHandle {
		t.Errorf("persisted handle = %q, want %q (derived from verified PeerIdentity/DaemonInstance)", got.Handle, wantHandle)
	}
	if got.Type != PeerTypeRemote {
		t.Errorf("persisted type = %q, want %q", got.Type, PeerTypeRemote)
	}
}

// TestStartLANRegistryV2_EndToEnd_UnverifiableFrameIsDropped sends a
// structurally-well-formed but badly-signed frame (wrong key) over the
// same real UDP path and asserts NOTHING is persisted — proving rejected
// frames never reach upsertRemotePeer.
func TestStartLANRegistryV2_EndToEnd_UnverifiableFrameIsDropped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const port = 58792 // fixed-but-test-scoped port, distinct from production PeerSyncV2Port
	stop, err := StartLANRegistryV2(context.Background(), LANRegistryV2Config{
		Swarm:                DefaultSwarmName,
		EnableSignedGossipV2: true,
		Port:                 port,
	})
	if err != nil {
		t.Fatalf("StartLANRegistryV2: %v", err)
	}
	defer stop()

	signerA, err := gossip.NewSigner()
	if err != nil {
		t.Fatalf("gossip.NewSigner: %v", err)
	}
	signerB, err := gossip.NewSigner()
	if err != nil {
		t.Fatalf("gossip.NewSigner: %v", err)
	}
	now := time.Now()
	frame, err := gossip.Sign(signerA, "peer-bad", "daemon-bad", "eth-bad", now, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("gossip.Sign: %v", err)
	}
	frame.Signer = signerB.PublicKeyHex() // mismatched key -> signature verification must fail
	wire, err := frame.Bytes()
	if err != nil {
		t.Fatalf("frame.Bytes: %v", err)
	}

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatalf("dial loopback v2 listener: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write(wire); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	// Give the ingest loop a real chance to (wrongly) process the frame
	// before asserting nothing was persisted.
	time.Sleep(300 * time.Millisecond)

	peers, err := ListPeers(DefaultSwarmName)
	if err != nil {
		t.Fatalf("ListPeers: %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("expected 0 persisted peers for an unverifiable frame, got %d: %+v", len(peers), peers)
	}
}
