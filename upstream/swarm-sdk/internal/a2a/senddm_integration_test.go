package a2a

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	a2apb "github.com/Swarm-Code/mono/swarm-sdk/internal/a2a/pb"
)

// Remote joins now fail closed on a non-opaque handle (JoinSwarm routes
// PeerTypeRemote through upsertRemotePeer), so these tests need real
// process-generated-shaped origin~alias handles instead of the old bare
// "receiver-peer"/"ghost-peer" strings.
var (
	sendDMReceiverHandle = strings.Repeat("a", originIDLen) + "~" + strings.Repeat("b", aliasIDLen)
	sendDMGhostHandle    = strings.Repeat("c", originIDLen) + "~" + strings.Repeat("d", aliasIDLen)
)

// TestSendDM_UnauthenticatedRemoteEndpointFailsClosed verifies that transport
// capabilities manually published by an unauthenticated remote presence are
// stripped and cannot trigger WebSocket delivery. The inbox remains the durable
// fallback for the queued message.
func TestSendDM_UnauthenticatedRemoteEndpointFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SWARM_HOME", tmpDir) // isolate ~/.swarm/swarms registry

	// Build a minimal WebSocket server that mimics what Runtime exposes.
	// We don't need a full Runtime on the receive side — just the WS server +
	// SendMessage delegate, which is the same delegate Runtime installs.
	srv := NewWebSocketServer(nil)

	var received atomic.Int32

	srv.SetSendMessageDelegate(func(ctx context.Context, req *a2apb.SendMessageRequest) (*a2apb.SendMessageResponse, *a2aError) {
		received.Add(1)
		return &a2apb.SendMessageResponse{}, nil
	})

	mux := http.NewServeMux()
	srv.RegisterOnMux(mux)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	httpSrv := &http.Server{Handler: mux}
	go func() { _ = httpSrv.Serve(ln) }()
	defer httpSrv.Shutdown(context.Background())

	endpointURL := "http://" + ln.Addr().String()

	// Register an unauthenticated remote presence with a forged transport
	// capability. Discovery may retain its display/liveness data, but must not
	// expose the endpoint as dialable.
	peer := PeerPresence{
		Handle:      sendDMReceiverHandle,
		EndpointURL: endpointURL,
		Type:        PeerTypeRemote,
		StartedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	}
	if err := writePeerFile(t, tmpDir, "default", peer); err != nil {
		t.Fatalf("write peer file: %v", err)
	}
	gotPeer, err := GetPeer("default", sendDMReceiverHandle)
	if err != nil {
		t.Fatalf("GetPeer: %v", err)
	}
	if gotPeer == nil {
		t.Fatal("GetPeer returned nil remote presence")
	}
	if gotPeer.EndpointURL != "" {
		t.Fatalf("unauthenticated remote endpoint was not stripped: %q", gotPeer.EndpointURL)
	}

	// Stand up the sender Runtime
	backend, err := NewSQLiteBackend(SQLiteConfig{Path: tmpDir + "/registry.db"})
	if err != nil {
		t.Fatalf("backend: %v", err)
	}
	defer backend.Close()

	rt, err := NewRuntime(Config{
		Handle:        "sender-peer",
		WorkspacePath: tmpDir,
		SwarmName:     "default",
		Descriptor: AgentDescriptor{
			Name: "sender-peer",
		},
	}, backend, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := rt.SendDM(ctx, sendDMReceiverHandle, "WS-INTEGRATION-PROBE")
	if err != nil {
		t.Fatalf("SendDM: %v", err)
	}
	if result.Status != "queued" {
		t.Fatalf("expected Status=queued, got %q (queued reason: %q)", result.Status, result.QueuedReason)
	}
	if result.QueuedReason == "" || len(result.QueuedReason) > 256 {
		t.Fatalf("expected a bounded queued reason, got %q (length %d)", result.QueuedReason, len(result.QueuedReason))
	}
	if strings.Contains(result.QueuedReason, endpointURL) {
		t.Fatalf("queued reason exposed stripped endpoint: %q", result.QueuedReason)
	}
	if got := received.Load(); got != 0 {
		t.Fatalf("WebSocket delegate invoked for unauthenticated remote endpoint (count=%d)", got)
	}

	// Verify inbox durability despite transport delivery failing closed.
	msgs, err := ReadInbox("default", sendDMReceiverHandle)
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	found := false
	for _, m := range msgs {
		if m.Text == "WS-INTEGRATION-PROBE" && m.From == "sender-peer" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("inbox did not contain the DM; got %d messages", len(msgs))
	}
}

// TestSendDM_FallbackToInbox_WhenPeerUnreachable verifies that if WS dial fails,
// SendDM returns Status="queued" and the inbox copy is still durably written.
func TestSendDM_FallbackToInbox_WhenPeerUnreachable(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Register a peer pointing at an unreachable endpoint.
	peer := PeerPresence{
		Handle:      sendDMGhostHandle,
		EndpointURL: "http://127.0.0.1:1", // tcpmux — refuses connections
		Type:        PeerTypeRemote,
		StartedAt:   time.Now(),
		LastSeenAt:  time.Now(),
	}
	if err := writePeerFile(t, tmpDir, "default", peer); err != nil {
		t.Fatalf("write peer file: %v", err)
	}

	backend, err := NewSQLiteBackend(SQLiteConfig{Path: tmpDir + "/registry.db"})
	if err != nil {
		t.Fatalf("backend: %v", err)
	}
	defer backend.Close()

	rt, err := NewRuntime(Config{
		Handle:        "sender-peer",
		WorkspacePath: tmpDir,
		SwarmName:     "default",
		Descriptor: AgentDescriptor{
			Name: "sender-peer",
		},
	}, backend, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := rt.SendDM(ctx, sendDMGhostHandle, "FALLBACK-TOKEN")
	if err != nil {
		t.Fatalf("SendDM: %v", err)
	}
	if result.Status != "queued" {
		t.Fatalf("expected Status=queued (peer unreachable), got %q", result.Status)
	}
	if !strings.Contains(result.QueuedReason, "peer unreachable") {
		t.Fatalf("expected QueuedReason to mention 'peer unreachable', got %q", result.QueuedReason)
	}

	// Inbox must still have the message for offline delivery.
	msgs, err := ReadInbox("default", sendDMGhostHandle)
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(msgs) == 0 || msgs[len(msgs)-1].Text != "FALLBACK-TOKEN" {
		t.Fatalf("inbox did not durably store fallback message; got: %+v", msgs)
	}
}

// writePeerFile is a minimal test helper that writes a peer presence file
// directly to the filesystem-based discovery registry. We bypass JoinSwarm's
// PID-liveness check by setting Type=PeerTypeRemote.
func writePeerFile(t *testing.T, _ string, swarmName string, peer PeerPresence) error {
	t.Helper()
	if err := JoinSwarm(swarmName, peer); err != nil {
		return err
	}
	return TouchPeer(swarmName, peer.Handle)
}

// Marshal-shape sanity: the request we send across the wire must round-trip
// through protojson cleanly.
func TestSendDM_PayloadShape(t *testing.T) {
	req := &a2apb.SendMessageRequest{
		Message: &a2apb.Message{
			MessageId: "shape-1",
			Role:      a2apb.Role_ROLE_USER,
			Parts: []*a2apb.Part{
				{Content: &a2apb.Part_Text{Text: "hello"}},
			},
		},
	}

	payload, err := protojson.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got a2apb.SendMessageRequest
	if err := protojson.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.GetMessage().GetMessageId() != "shape-1" {
		t.Fatalf("round-trip lost message id")
	}
	parts := got.GetMessage().GetParts()
	if len(parts) != 1 || parts[0].GetText() != "hello" {
		t.Fatalf("round-trip lost payload text")
	}
}
