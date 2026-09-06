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

// TestConnectionPool_FireAndForget exercises the full WebSocket DM round-trip
// without going through Runtime: it stands up a WebSocketServer with a stub
// SendMessage delegate, sends a fire-and-forget request from a ConnectionPool,
// and verifies the server received the unmarshaled SendMessageRequest.
func TestConnectionPool_FireAndForget(t *testing.T) {
	srv := NewWebSocketServer(nil)

	var received atomic.Int32
	var receivedText atomic.Value // string

	srv.SetSendMessageDelegate(func(ctx context.Context, req *a2apb.SendMessageRequest) (*a2apb.SendMessageResponse, *a2aError) {
		received.Add(1)
		if msg := req.GetMessage(); msg != nil {
			for _, part := range msg.GetParts() {
				if t := part.GetText(); t != "" {
					receivedText.Store(t)
				}
			}
		}
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

	pool := NewConnectionPool(nil)
	defer pool.Close()

	// Build a SendMessageRequest exactly as Runtime.SendDM does.
	req := &a2apb.SendMessageRequest{
		Message: &a2apb.Message{
			MessageId: "test-dm-1",
			Role:      a2apb.Role_ROLE_USER,
			Parts: []*a2apb.Part{
				{Content: &a2apb.Part_Text{Text: "hello over websocket"}},
			},
		},
	}
	payload, err := protojson.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	wsMsg := WebSocketMessage{
		Type:      "request",
		ID:        "test-dm-1",
		Method:    "SendMessage",
		Payload:   payload,
		Timestamp: time.Now(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pool.SendFireAndForget(ctx, "peer-a", endpointURL, wsMsg); err != nil {
		t.Fatalf("SendFireAndForget: %v", err)
	}

	// Server processes inbound requests asynchronously — give it a moment.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if received.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if got := received.Load(); got != 1 {
		t.Fatalf("expected delegate to receive 1 message, got %d", got)
	}
	if v, _ := receivedText.Load().(string); v != "hello over websocket" {
		t.Fatalf("delegate received wrong text: %q", v)
	}

	if !pool.HasConnection("peer-a") {
		t.Fatal("pool should report active connection after successful send")
	}
}

// TestConnectionPool_DialFailure verifies that a send to a non-listening
// endpoint returns an error promptly (not after PoolMaxReconnectAttempts).
func TestConnectionPool_DialFailure(t *testing.T) {
	pool := NewConnectionPool(nil)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 127.0.0.1:1 is reserved (tcpmux), reliably refuses connections.
	err := pool.SendFireAndForget(ctx, "ghost", "http://127.0.0.1:1", WebSocketMessage{
		Type:      "request",
		ID:        "x",
		Method:    "SendMessage",
		Payload:   []byte(`{}`),
		Timestamp: time.Now(),
	})
	if err == nil {
		t.Fatal("expected dial failure error, got nil")
	}
	if !strings.Contains(err.Error(), "dial") {
		t.Fatalf("expected error to mention dial, got: %v", err)
	}
}

// TestConnectionPool_RequestResponse verifies that SendRequest receives a real
// response (proves response-correlation via respWaiters works end-to-end).
func TestConnectionPool_RequestResponse(t *testing.T) {
	srv := NewWebSocketServer(nil)

	srv.SetSendMessageDelegate(func(ctx context.Context, req *a2apb.SendMessageRequest) (*a2apb.SendMessageResponse, *a2aError) {
		return &a2apb.SendMessageResponse{
			Payload: &a2apb.SendMessageResponse_Message{
				Message: &a2apb.Message{
					MessageId: "reply-1",
					Role:      a2apb.Role_ROLE_AGENT,
					Parts: []*a2apb.Part{
						{Content: &a2apb.Part_Text{Text: "ack"}},
					},
				},
			},
		}, nil
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

	pool := NewConnectionPool(nil)
	defer pool.Close()

	payload, _ := protojson.Marshal(&a2apb.SendMessageRequest{
		Message: &a2apb.Message{
			MessageId: "req-1",
			Role:      a2apb.Role_ROLE_USER,
			Parts: []*a2apb.Part{
				{Content: &a2apb.Part_Text{Text: "ping"}},
			},
		},
	})

	wsMsg := WebSocketMessage{
		Type:      "request",
		ID:        "req-1",
		Method:    "SendMessage",
		Payload:   payload,
		Timestamp: time.Now(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := pool.SendRequest(ctx, "peer-b", endpointURL, wsMsg)
	if err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Type != "response" {
		t.Fatalf("expected response type, got %q", resp.Type)
	}

	var smr a2apb.SendMessageResponse
	if err := protojson.Unmarshal(resp.Payload, &smr); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	gotMsg := smr.GetMessage()
	if gotMsg == nil {
		t.Fatal("response did not contain a message payload")
	}
	if len(gotMsg.GetParts()) == 0 || gotMsg.GetParts()[0].GetText() != "ack" {
		t.Fatalf("unexpected response text: %+v", gotMsg.GetParts())
	}
}
