package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/protocol"
)

type attachedOutboundProbeConn struct {
	net.Conn
	deadlineCalls int
	writeCalls    int
}

func (c *attachedOutboundProbeConn) SetWriteDeadline(time.Time) error {
	c.deadlineCalls++
	return nil
}

func (c *attachedOutboundProbeConn) Write(p []byte) (int, error) {
	c.writeCalls++
	return len(p), nil
}

type attachedTestModel struct {
	content string
}

func (m *attachedTestModel) Init() tea.Cmd { return nil }

func (m *attachedTestModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }

func (m *attachedTestModel) View() tea.View { return tea.NewView(m.content) }

func TestAttachedSetModelPromotesLiveModel(t *testing.T) {
	bootstrap := &attachedTestModel{content: "bootstrap"}
	promoted := &attachedTestModel{content: "ready"}
	server := NewAttached(nil, bootstrap, 80, 24, nil, filepath.Join(t.TempDir(), "peer.ctrl"))

	server.SetModel(promoted)

	if got := server.currentModel(); got != promoted {
		t.Fatalf("current model = %p, want promoted model %p", got, promoted)
	}
	if got := server.currentModel().View().Content; got != "ready" {
		t.Fatalf("current model view = %q, want ready", got)
	}
}

func TestAttachedReadMessageRejectsMalformedLengthBeforePayload(t *testing.T) {
	tests := []struct {
		name   string
		length uint32
		want   string
	}{
		{name: "zero", length: 0, want: "malformed frame length"},
		{name: "over limit", length: maxAttachedFrameSize + 1, want: "frame too large"},
		{name: "maximum uint32", length: ^uint32(0), want: "frame too large"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serverConn, clientConn := net.Pipe()
			t.Cleanup(func() {
				_ = serverConn.Close()
				_ = clientConn.Close()
			})

			header := make([]byte, protocol.HeaderSize)
			binary.LittleEndian.PutUint32(header[:4], test.length)
			header[4] = byte(protocol.TypeSendText)
			writeDone := make(chan error, 1)
			go func() {
				_, err := clientConn.Write(header)
				writeDone <- err
			}()

			_, _, err := readMessageWithTimeout(serverConn, time.Second)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readMessageWithTimeout() error = %v, want containing %q", err, test.want)
			}
			if err := <-writeDone; err != nil {
				t.Fatalf("write header: %v", err)
			}
		})
	}
}

func TestAttachedConnectionDeadlines(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer serverConn.Close()
		defer clientConn.Close()

		started := time.Now()
		go func() { _, _ = clientConn.Write([]byte{1}) }()
		_, _, err := readMessageWithTimeout(serverConn, 20*time.Millisecond)
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("read error = %v, want timeout", err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("read deadline took %v", elapsed)
		}
	})

	t.Run("write", func(t *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer serverConn.Close()
		defer clientConn.Close()

		started := time.Now()
		err := writeMessageWithTimeout(serverConn, protocol.NewOK(), 20*time.Millisecond)
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("write error = %v, want timeout", err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("write deadline took %v", elapsed)
		}
	})
}

func TestAttachedWriteRejectsOversizedPayloadBeforeAnyConnectionSideEffect(t *testing.T) {
	conn := &attachedOutboundProbeConn{}
	msg := &protocol.Message{
		Type:    protocol.TypeEventData,
		Payload: make([]byte, maxAttachedFrameSize),
	}

	err := writeMessageWithTimeout(conn, msg, time.Second)
	if !errors.Is(err, ErrAttachedPayloadTooLarge) {
		t.Fatalf("write error = %v, want ErrAttachedPayloadTooLarge", err)
	}
	if conn.deadlineCalls != 0 {
		t.Fatalf("SetWriteDeadline called %d time(s), want zero", conn.deadlineCalls)
	}
	if conn.writeCalls != 0 {
		t.Fatalf("Write called %d time(s), want zero", conn.writeCalls)
	}
}

func TestAttachedWriterSendsOneBoundedOversizedErrorAndNoSecondFrame(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	writer := &attachedWriter{conn: serverConn}
	oversized := &protocol.Message{
		Type:    protocol.TypeEventData,
		Payload: make([]byte, maxAttachedFrameSize),
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writer.write(oversized)
	}()
	response, _, err := readMessageWithTimeout(clientConn, time.Second)
	if err != nil {
		t.Fatalf("read oversized error frame: %v", err)
	}
	if err := <-writeDone; !errors.Is(err, ErrAttachedPayloadTooLarge) {
		t.Fatalf("writer error = %v, want ErrAttachedPayloadTooLarge", err)
	}
	if response.Type != protocol.TypeError {
		t.Fatalf("response type = %v, want TypeError", response.Type)
	}
	var payload protocol.ErrorResponse
	if err := json.Unmarshal(response.Payload, &payload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if payload.Code != attachedPayloadTooLargeCode || payload.Message != attachedPayloadTooLargeCategory {
		t.Fatalf("error payload = %+v, want code=%d message=%q",
			payload, attachedPayloadTooLargeCode, attachedPayloadTooLargeCategory)
	}

	if err := writer.write(protocol.NewOK()); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second write error = %v, want io.ErrClosedPipe", err)
	}
	if err := clientConn.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, err := clientConn.Read(make([]byte, 1)); err == nil {
		t.Fatal("writer emitted a second frame after oversized error")
	}
}

func TestAttachedWriteAcceptsExactMaximumPayloadRoundTrip(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	payload := make([]byte, maxAttachedFrameSize-1)
	payload[0] = 0x5a
	payload[len(payload)-1] = 0xa5
	msg := &protocol.Message{Type: protocol.TypeEventData, Payload: payload}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writeMessageWithTimeout(serverConn, msg, 5*time.Second)
	}()
	got, _, err := readMessageWithTimeout(clientConn, 5*time.Second)
	if err != nil {
		t.Fatalf("read exact-boundary frame: %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write exact-boundary frame: %v", err)
	}
	if got.Type != msg.Type || len(got.Payload) != len(payload) {
		t.Fatalf("round trip type/length = %v/%d, want %v/%d",
			got.Type, len(got.Payload), msg.Type, len(payload))
	}
	if got.Payload[0] != payload[0] || got.Payload[len(got.Payload)-1] != payload[len(payload)-1] {
		t.Fatal("exact-boundary payload bytes changed during round trip")
	}
}

func TestAttachedReadDeadlineAllowsIdlePersistentClient(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	writeDone := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		writeDone <- writeMessageWithTimeout(clientConn, protocol.NewOK(), time.Second)
	}()
	message, _, err := readMessageWithTimeout(serverConn, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("idle persistent read: %v", err)
	}
	if message.Type != protocol.TypeOK {
		t.Fatalf("message type = %v, want OK", message.Type)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write after idle: %v", err)
	}
}

func TestAttachedWaitContentRejectsUnboundedTimeout(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	server := NewAttached(nil, nil, 80, 24, nil, filepath.Join(t.TempDir(), "peer.ctrl"))
	request := &protocol.Message{
		Type:    protocol.TypeWaitContent,
		Payload: []byte(`{"text":"never","timeout_ms":300001}`),
	}
	go server.dispatch(serverConn, request)
	response, _, err := readMessageWithTimeout(clientConn, time.Second)
	if err != nil {
		t.Fatalf("read validation response: %v", err)
	}
	if response.Type != protocol.TypeError {
		t.Fatalf("response type = %v, want error", response.Type)
	}
}

func TestAttachedListenRefusesLiveSocketWithoutRemovingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.ctrl")
	live, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen live socket: %v", err)
	}
	unixLive := live.(*net.UnixListener)
	unixLive.SetUnlinkOnClose(false)
	defer func() {
		_ = live.Close()
		_ = os.Remove(path)
	}()

	server := NewAttached(nil, nil, 80, 24, nil, path)
	if err := server.Listen(); err == nil || !strings.Contains(err.Error(), "live process") {
		t.Fatalf("Listen() error = %v, want live collision", err)
	}
	server.Stop()

	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("live socket was removed after collision: %v", err)
	}
	_ = conn.Close()
}

func TestAttachedListenReplacesStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.ctrl")
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen stale socket: %v", err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatalf("close stale socket: %v", err)
	}

	server := NewAttached(nil, nil, 80, 24, nil, path)
	if err := server.Listen(); err != nil {
		t.Fatalf("Listen() stale replacement: %v", err)
	}
	server.Stop()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains after Stop: %v", err)
	}
}

func TestAttachedListenUsesOwnerOnlyPermissions(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "peers")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	path := filepath.Join(parent, "peer.ctrl")
	server := NewAttached(nil, nil, 80, 24, nil, path)
	if err := server.Listen(); err != nil {
		t.Fatalf("Listen(): %v", err)
	}
	defer server.Stop()

	parentInfo, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("stat parent: %v", err)
	}
	if got := parentInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("parent permissions = %o, want 700", got)
	}
	socketInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if got := socketInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket permissions = %o, want 600", got)
	}
}

func TestAttachedListenRefusesNonSocketCollision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.ctrl")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write collision: %v", err)
	}
	server := NewAttached(nil, nil, 80, 24, nil, path)
	if err := server.Listen(); err == nil || !strings.Contains(err.Error(), "not a Unix socket") {
		t.Fatalf("Listen() error = %v, want non-socket collision", err)
	}
	server.Stop()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep" {
		t.Fatalf("collision file changed: data=%q err=%v", data, err)
	}
}

func TestAttachedStopDoesNotRemoveReplacementSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.ctrl")
	server := NewAttached(nil, nil, 80, 24, nil, path)
	if err := server.Listen(); err != nil {
		t.Fatalf("Listen(): %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("unlink original socket: %v", err)
	}
	replacement, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen replacement: %v", err)
	}
	replacement.(*net.UnixListener).SetUnlinkOnClose(false)
	defer func() {
		_ = replacement.Close()
		_ = os.Remove(path)
	}()

	server.Stop()
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("replacement socket was removed by Stop: %v", err)
	}
	_ = conn.Close()
}

func TestAttachedStopClosesEventSubscriber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.ctrl")
	events := make(chan []byte)
	server := NewAttached(nil, nil, 80, 24, events, path)
	if err := server.Listen(); err != nil {
		t.Fatalf("Listen(): %v", err)
	}
	startResult := make(chan error, 1)
	go func() { startResult <- server.Start(t.Context()) }()

	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := writeMessageWithTimeout(conn, &protocol.Message{Type: protocol.TypeSubscribeEvents}, time.Second); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	response, _, err := readMessageWithTimeout(conn, time.Second)
	if err != nil || response.Type != protocol.TypeOK {
		t.Fatalf("subscription response = %#v, %v", response, err)
	}

	server.Stop()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("subscriber connection remained open after Stop")
	}
	if err := <-startResult; err != nil {
		t.Fatalf("Start() after Stop = %v", err)
	}
}

// ─── malformed-frame tests ────────────────────────────────────────────────

// TestAttachedReadMessageRejectsTruncatedHeader proves a connection that
// closes mid-header (before the fixed 5-byte length+type prefix is fully
// received) surfaces an EOF-class error instead of hanging or panicking on
// a short header slice.
func TestAttachedReadMessageRejectsTruncatedHeader(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	writeDone := make(chan error, 1)
	go func() {
		_, err := clientConn.Write([]byte{0x01, 0x02, 0x03}) // 3 of 5 header bytes
		writeDone <- err
		_ = clientConn.Close()
	}()

	_, _, err := readMessageWithTimeout(serverConn, time.Second)
	if err == nil {
		t.Fatal("expected an error for a truncated header")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		t.Fatalf("readMessageWithTimeout() error = %v, want an EOF-class error", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write header: %v", err)
	}
}

// TestAttachedReadMessageRejectsTruncatedPayload proves a connection that
// closes after declaring a length but before delivering the full payload
// surfaces an EOF-class error rather than returning a short/corrupt
// payload silently.
func TestAttachedReadMessageRejectsTruncatedPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	header := make([]byte, protocol.HeaderSize)
	binary.LittleEndian.PutUint32(header[:4], 10) // declares 9 payload bytes
	header[4] = byte(protocol.TypeSendText)

	writeDone := make(chan error, 1)
	go func() {
		if _, err := clientConn.Write(header); err != nil {
			writeDone <- err
			return
		}
		_, err := clientConn.Write([]byte{1, 2, 3}) // only 3 of the declared 9 bytes
		writeDone <- err
		_ = clientConn.Close()
	}()

	_, _, err := readMessageWithTimeout(serverConn, time.Second)
	if err == nil {
		t.Fatal("expected an error for a truncated payload")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		t.Fatalf("readMessageWithTimeout() error = %v, want an EOF-class error", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write payload: %v", err)
	}
}

// ─── exact-boundary test ───────────────────────────────────────────────────

// TestAttachedReadMessageAcceptsExactBoundaryFrame proves the accept side of
// ADR-004/ADR-007's exact boundary: a declared length of exactly
// maxAttachedFrameSize (8 MiB) is accepted, and its payload -- length minus
// the one-byte message type -- is exactly maxAttachedFrameSize-1 bytes.
// Combined with TestAttachedReadMessageRejectsMalformedLengthBeforePayload's
// "over limit" case (maxAttachedFrameSize+1 rejected before any payload
// read), this pins both sides of the boundary.
func TestAttachedReadMessageAcceptsExactBoundaryFrame(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	payload := make([]byte, maxAttachedFrameSize-1)
	for i := range payload {
		payload[i] = byte(i)
	}
	header := make([]byte, protocol.HeaderSize)
	binary.LittleEndian.PutUint32(header[:4], uint32(maxAttachedFrameSize))
	header[4] = byte(protocol.TypeSendText)

	writeDone := make(chan error, 1)
	go func() {
		if _, err := clientConn.Write(header); err != nil {
			writeDone <- err
			return
		}
		_, err := clientConn.Write(payload)
		writeDone <- err
	}()

	msg, _, err := readMessageWithTimeout(serverConn, 5*time.Second)
	if err != nil {
		t.Fatalf("readMessageWithTimeout() at exact 8 MiB boundary: %v", err)
	}
	if len(msg.Payload) != maxAttachedFrameSize-1 {
		t.Fatalf("payload length = %d, want %d", len(msg.Payload), maxAttachedFrameSize-1)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write payload: %v", err)
	}
}

// ─── socket-collision test (idempotent Listen) ─────────────────────────────

// TestAttachedListenIdempotentWhenAlreadyBound proves a second Listen() call
// on an already-bound server is a harmless no-op rather than a spurious
// collision error against its own listener.
func TestAttachedListenIdempotentWhenAlreadyBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.ctrl")
	server := NewAttached(nil, nil, 80, 24, nil, path)
	if err := server.Listen(); err != nil {
		t.Fatalf("first Listen(): %v", err)
	}
	defer server.Stop()

	if err := server.Listen(); err != nil {
		t.Fatalf("second Listen() on an already-bound server: %v", err)
	}
}

// ─── permission / peer-identity test ───────────────────────────────────────

// TestAttachedHandleConnRejectsNonUnixPeerWithoutDispatch proves that when
// peer-credential validation fails, the server writes an explicit TypeError
// frame identifying the rejection (issue #249's root cause: a peer whose
// request bytes were never consumed and whose connection was then closed
// with those bytes still buffered got an opaque OS-level ECONNRESET/EPIPE —
// "connection reset by peer" — indistinguishable from a crashed peer or a
// dead socket) instead of silently closing with zero response.
func TestAttachedHandleConnRejectsNonUnixPeerWithoutDispatch(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	server := NewAttached(nil, &attachedTestModel{content: "hi"}, 80, 24, nil, filepath.Join(t.TempDir(), "peer.ctrl"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.handleConn(serverConn)
	}()
	frame := &protocol.Message{Type: protocol.TypeGetFrame}
	_ = writeMessageWithTimeout(clientConn, frame, time.Second)
	resp, _, err := readMessageWithTimeout(clientConn, 2*time.Second)
	if err != nil {
		t.Fatalf("expected an explicit rejection frame, got a read error (the exact opaque-reset symptom this fix removes): %v", err)
	}
	if resp.Type != protocol.TypeError {
		t.Fatalf("response type = 0x%02x, want TypeError", resp.Type)
	}
	var errResp protocol.ErrorResponse
	if err := resp.ParsePayload(&errResp); err != nil {
		t.Fatalf("parse error payload: %v", err)
	}
	if errResp.Code != attachedPeerUIDMismatchCode || !strings.Contains(errResp.Message, attachedPeerUIDMismatchCategory) {
		t.Fatalf("error response = %+v, want code %d and category %q", errResp, attachedPeerUIDMismatchCode, attachedPeerUIDMismatchCategory)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleConn did not return after rejecting a non-Unix peer")
	}
}

// ─── connection cap (bounded subscribers/resources) test ──────────────────

// TestAttachedHandleConnEnforcesConnectionCap proves maxAttachedConnections
// bounds simultaneous connections (and therefore simultaneous
// TypeSubscribeEvents subscribers): with the cap shrunk to 1, a second
// concurrent connection is closed immediately, before any peer-credential
// check or dispatch, while the first connection remains fully usable.
func TestAttachedHandleConnEnforcesConnectionCap(t *testing.T) {
	oldCap := maxAttachedConnections
	maxAttachedConnections = 1
	defer func() { maxAttachedConnections = oldCap }()

	path := filepath.Join(t.TempDir(), "peer.ctrl")
	server := NewAttached(nil, &attachedTestModel{content: "hi"}, 80, 24, nil, path)
	if err := server.Listen(); err != nil {
		t.Fatalf("Listen(): %v", err)
	}
	startResult := make(chan error, 1)
	go func() { startResult <- server.Start(t.Context()) }()
	defer func() {
		server.Stop()
		<-startResult
	}()

	first, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("dial first: %v", err)
	}
	defer first.Close()

	// Round-trip a request so we know the server fully registered the first
	// connection: registration happens before any message is read, so a
	// successful response proves handleConn already added it to s.conns.
	frame := &protocol.Message{Type: protocol.TypeGetFrame}
	if err := writeMessageWithTimeout(first, frame, time.Second); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if _, _, err := readMessageWithTimeout(first, time.Second); err != nil {
		t.Fatalf("read first response: %v", err)
	}

	second, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("dial second: %v", err)
	}
	defer second.Close()
	// The over-cap connection now receives an explicit rejection frame
	// (issue #249 fix) instead of being closed with zero response.
	resp, _, err := readMessageWithTimeout(second, 2*time.Second)
	if err != nil {
		t.Fatalf("expected an explicit over-cap rejection frame, got a read error: %v", err)
	}
	if resp.Type != protocol.TypeError {
		t.Fatalf("response type = 0x%02x, want TypeError", resp.Type)
	}
	var errResp protocol.ErrorResponse
	if err := resp.ParsePayload(&errResp); err != nil {
		t.Fatalf("parse error payload: %v", err)
	}
	if errResp.Code != attachedTooManyConnsCode || !strings.Contains(errResp.Message, attachedTooManyConnsCategory) {
		t.Fatalf("error response = %+v, want code %d and category %q", errResp, attachedTooManyConnsCode, attachedTooManyConnsCategory)
	}

	// The first (within-cap) connection must remain unaffected.
	if err := writeMessageWithTimeout(first, frame, time.Second); err != nil {
		t.Fatalf("write to first connection after cap rejection: %v", err)
	}
	if _, _, err := readMessageWithTimeout(first, time.Second); err != nil {
		t.Fatalf("read from first connection after cap rejection: %v", err)
	}
}

// ─── shutdown / cancellation test ───────────────────────────────────────────

// TestAttachedStartStopsOnContextCancel proves Start(ctx) honors external
// cancellation: it returns promptly (bounded, channel-based wait, not a
// sleep ratio) and the listener stops accepting new connections.
func TestAttachedStartStopsOnContextCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.ctrl")
	server := NewAttached(nil, nil, 80, 24, nil, path)
	if err := server.Listen(); err != nil {
		t.Fatalf("Listen(): %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	startResult := make(chan error, 1)
	go func() { startResult <- server.Start(ctx) }()

	cancel()

	select {
	case err := <-startResult:
		if err != nil {
			t.Fatalf("Start() after context cancel = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start() did not return after context cancellation")
	}

	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		conn.Close()
		t.Fatal("socket still accepting connections after context-cancelled shutdown")
	}
}
