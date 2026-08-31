package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// newTestMux returns a Mux backed by a bare *client.Client suitable for
// tests that exercise dispatch + transport plumbing without full provider
// configuration.  Many handlers (snapshot, activeAgent, mode get, etc.) work
// directly on a zero Client because the session-state surface lives entirely
// on the struct.
func newTestMux() *Mux {
	return NewMux(&client.Client{})
}

type blockingJSONValue struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (v blockingJSONValue) MarshalJSON() ([]byte, error) {
	v.entered <- struct{}{}
	<-v.release
	return []byte(`"released"`), nil
}

type deadlineAwareBlockingWriter struct {
	header            http.Header
	deadlineInstalled chan time.Time
	writeEntered      chan struct{}
	writeUnblocked    chan struct{}
	writeOnce         sync.Once
	unblockOnce       sync.Once
}

func newDeadlineAwareBlockingWriter() *deadlineAwareBlockingWriter {
	return &deadlineAwareBlockingWriter{
		header:            make(http.Header),
		deadlineInstalled: make(chan time.Time, 1),
		writeEntered:      make(chan struct{}),
		writeUnblocked:    make(chan struct{}),
	}
}

func (w *deadlineAwareBlockingWriter) Header() http.Header {
	return w.header
}

func (w *deadlineAwareBlockingWriter) WriteHeader(int) {}

// Flush is a no-op so this type also satisfies http.Flusher, letting the
// same deterministic blocked-write-bounded-by-deadline fixture exercise
// SSEHandler (which requires a Flusher) as well as HTTPHandler.
func (w *deadlineAwareBlockingWriter) Flush() {}

func (w *deadlineAwareBlockingWriter) Write([]byte) (int, error) {
	w.writeOnce.Do(func() { close(w.writeEntered) })
	<-w.writeUnblocked
	return 0, context.DeadlineExceeded
}

func (w *deadlineAwareBlockingWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlineInstalled <- deadline
	go func() {
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		<-timer.C
		w.unblockOnce.Do(func() { close(w.writeUnblocked) })
	}()
	return nil
}

// writeDeadlineRecorder adapts *httptest.ResponseRecorder — which does not
// itself implement SetWriteDeadline — into an explicit deadline-capable
// http.ResponseWriter. CONTRACT.md requires tests to use exactly this kind
// of explicit adapter for the normal (deadline-supported) path rather than
// relying on any generic fail-open behavior; WithResponseWriteBounds itself
// fails closed for a plain recorder (see
// TestWithResponseWriteBounds_UnsupportedWriterFailsClosedWithoutWriting).
type writeDeadlineRecorder struct {
	*httptest.ResponseRecorder
	mu        sync.Mutex
	deadlines []time.Time
}

func newWriteDeadlineRecorder() *writeDeadlineRecorder {
	return &writeDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (w *writeDeadlineRecorder) SetWriteDeadline(t time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deadlines = append(w.deadlines, t)
	return nil
}

func (w *writeDeadlineRecorder) Unwrap() http.ResponseWriter { return w.ResponseRecorder }

func (w *writeDeadlineRecorder) deadlineCalls() []time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]time.Time, len(w.deadlines))
	copy(out, w.deadlines)
	return out
}

// panicOnWriteResponseWriter never supports a write deadline and panics if
// anything ever calls Write/WriteHeader on it — used to prove
// WithResponseWriteBounds fails closed without ever giving the wrapped
// handler a chance to write.
type panicOnWriteResponseWriter struct {
	header http.Header
}

func (w *panicOnWriteResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *panicOnWriteResponseWriter) WriteHeader(int) {
	panic("WriteHeader must never be called through an unsupported writer")
}

func (w *panicOnWriteResponseWriter) Write([]byte) (int, error) {
	panic("Write must never be called through an unsupported writer")
}

// unwrapOnlyResponseWriter exposes no SetWriteDeadline of its own — only
// Unwrap() — so http.ResponseController (and therefore
// WithResponseWriteBounds) must see through it to the embedded writer to
// find the capability.
type unwrapOnlyResponseWriter struct {
	http.ResponseWriter
}

func (u *unwrapOnlyResponseWriter) Unwrap() http.ResponseWriter { return u.ResponseWriter }

// drainDispatchLimiter blocks until every dispatch child that has ever
// acquired a slot on limiter has released it again, by re-acquiring
// limiter's full configured capacity itself before returning.
//
// Both http.go's and websocket.go's per-request/per-message dispatch child
// goroutines are deliberately untracked by their connection/request
// supervisor's own wait group (so an uncooperative method handler cannot
// block shutdown) — see http.go's HTTPHandler and websocket.go's
// handleWSRequest doc comments. Each such child's `defer limiter.release()`
// runs strictly after that same child's own bounded-encode call
// (boundedEncodeResponse/httpBoundedResponseEnvelope), in the same
// goroutine and in program order, so successfully re-filling limiter back
// to its full capacity is a genuine channel-synchronized happens-before
// edge: a blocking channel send can only complete once a peer's receive
// frees space, and unlike polling len(chan) (verified empirically to
// create no such edge — a concurrent unsynchronized access after observing
// len()==0 is still reported as a race by go test -race), this blocking
// send/receive pair is recognized by the race detector as real
// synchronization. Any test whose leaked/untracked dispatch child reads a
// package-level bounded-encoding tuning variable (maxBoundedJSONBytes,
// maxBoundedJSONNodes, ...) must call this — with a private,
// test-dedicated *dispatchLimiter matching exactly the children it
// spawns — before returning, or a later test that mutates those variables
// can race it under go test -race.
//
// Callers must ensure no other goroutine is still legitimately competing
// to acquire the same limiter when this is called (for example, by first
// confirming — as every caller here does — that every request/message this
// test will ever send has already been written), or this could block
// forever or steal a slot a genuine pending acquirer is still waiting on.
func drainDispatchLimiter(t *testing.T, limiter *dispatchLimiter, timeout time.Duration) {
	t.Helper()
	capacity := cap(limiter.slots)
	done := make(chan struct{})
	go func() {
		for i := 0; i < capacity; i++ {
			limiter.acquire(context.Background())
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("dispatch limiter never fully drained — a child may still be running past this test's return")
	}
}

// sseDeadlineRecorder records every SetWriteDeadline call SSEHandler makes
// on it (value and order), so tests can assert the exact deterministic
// sequence — non-zero before each write/flush, zero (cleared) while idle —
// instead of inferring it from timing.
type sseDeadlineRecorder struct {
	header http.Header
	calls  chan time.Time
}

func newSSEDeadlineRecorder() *sseDeadlineRecorder {
	return &sseDeadlineRecorder{header: make(http.Header), calls: make(chan time.Time, 64)}
}

func (w *sseDeadlineRecorder) Header() http.Header         { return w.header }
func (w *sseDeadlineRecorder) WriteHeader(int)             {}
func (w *sseDeadlineRecorder) Write(p []byte) (int, error) { return len(p), nil }
func (w *sseDeadlineRecorder) Flush()                      {}
func (w *sseDeadlineRecorder) SetWriteDeadline(t time.Time) error {
	w.calls <- t
	return nil
}

// ─── Dispatch / registration tests ────────────────────────────────────

func TestMux_Methods_RegistersBuiltins(t *testing.T) {
	m := newTestMux()
	names := m.Methods()
	if len(names) < 20 {
		t.Fatalf("expected >=20 builtin methods, got %d: %v", len(names), names)
	}
	want := []string{
		"client.snapshot",
		"client.sendMessage",
		"client.activeAgent",
		"client.setMode",
		"client.toolRegistry",
		"client.searchHistory",
	}
	have := make(map[string]bool, len(names))
	for _, n := range names {
		have[n] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("missing builtin method %q", w)
		}
	}
}

func TestMux_Dispatch_UnknownMethod(t *testing.T) {
	m := newTestMux()
	_, err := m.Dispatch(context.Background(), "client.nope", nil)
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
	rpcErr, ok := err.(*ErrorObject)
	if !ok {
		t.Fatalf("expected *ErrorObject, got %T: %v", err, err)
	}
	if rpcErr.Code != ErrMethodNotFound {
		t.Errorf("code: got %d want %d", rpcErr.Code, ErrMethodNotFound)
	}
}

func TestMux_Dispatch_Snapshot(t *testing.T) {
	m := newTestMux()
	res, err := m.Dispatch(context.Background(), "client.snapshot", nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	st, ok := res.(client.State)
	if !ok {
		t.Fatalf("expected client.State, got %T", res)
	}
	_ = st // zero value is acceptable for bare client
}

func TestMux_Dispatch_SnapshotWireIsTransportSafe(t *testing.T) {
	m := newTestMux()
	res, err := m.Dispatch(context.Background(), "client.snapshotWire", nil)
	if err != nil {
		t.Fatalf("dispatch snapshotWire: %v", err)
	}
	wire, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map snapshot projection, got %T", res)
	}
	if _, err := EncodeBoundedJSON(context.Background(), wire); err != nil {
		t.Fatalf("snapshotWire must be bounded-encodable: %v", err)
	}
	if _, hasTime := wire["updatedAt"]; hasTime {
		t.Fatal("snapshotWire must not expose time-bearing fields")
	}
}

func TestMux_Dispatch_ActiveMode_AfterSet(t *testing.T) {
	m := newTestMux()
	// Set mode via dispatch.
	body := json.RawMessage(`{"mode":"plan"}`)
	if _, err := m.Dispatch(context.Background(), "client.setMode", body); err != nil {
		t.Fatalf("setMode: %v", err)
	}
	res, err := m.Dispatch(context.Background(), "client.activeMode", nil)
	if err != nil {
		t.Fatalf("activeMode: %v", err)
	}
	if res.(string) != "plan" {
		t.Errorf("active mode: got %q want %q", res, "plan")
	}
}

func TestMux_Register_AddsCustomMethod(t *testing.T) {
	m := newTestMux()
	m.Register(Method{
		Name: "custom.echo",
		Handler: func(_ context.Context, _ *client.Client, p json.RawMessage) (any, error) {
			return string(p), nil
		},
	})
	res, err := m.Dispatch(context.Background(), "custom.echo", json.RawMessage(`"hi"`))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.(string) != `"hi"` {
		t.Errorf("got %q", res)
	}
}

func TestMux_Register_IgnoresEmptyName(t *testing.T) {
	m := newTestMux()
	before := len(m.Methods())
	m.Register(Method{Name: "", Handler: func(_ context.Context, _ *client.Client, _ json.RawMessage) (any, error) { return nil, nil }})
	m.Register(Method{Name: "x.y", Handler: nil})
	after := len(m.Methods())
	if after != before {
		t.Errorf("method count changed: got %d want %d", after, before)
	}
}

// ─── HTTP transport tests ─────────────────────────────────────────────

func TestMux_HTTPHandler_PostJSON(t *testing.T) {
	srv := httptest.NewServer(newTestMux().HTTPHandler())
	defer srv.Close()

	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"client.activeMode"}`)
	resp, err := http.Post(srv.URL, "application/json", body)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Jsonrpc != "2.0" {
		t.Errorf("jsonrpc: %q", out.Jsonrpc)
	}
	if string(out.ID) != "1" {
		t.Errorf("id: %s", string(out.ID))
	}
	if out.Error != nil {
		t.Errorf("unexpected error: %+v", out.Error)
	}
	// active mode on a bare client is the empty string.
	if out.Result != "" {
		t.Errorf("result: got %v want empty", out.Result)
	}
}

func TestMux_HTTPHandler_RejectsGET(t *testing.T) {
	srv := httptest.NewServer(newTestMux().HTTPHandler())
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: %d want 405", resp.StatusCode)
	}
}

func TestMux_HTTPHandler_ParseError(t *testing.T) {
	srv := httptest.NewServer(newTestMux().HTTPHandler())
	defer srv.Close()
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`not-json`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var out Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Error == nil || out.Error.Code != ErrParseError {
		t.Errorf("expected parse error, got %+v", out.Error)
	}
}

func TestMux_HTTPHandler_UnknownMethod(t *testing.T) {
	srv := httptest.NewServer(newTestMux().HTTPHandler())
	defer srv.Close()
	resp, err := http.Post(srv.URL, "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"client.nope"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var out Response
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Error == nil || out.Error.Code != ErrMethodNotFound {
		t.Errorf("expected method-not-found, got %+v", out.Error)
	}
}

func TestMux_HTTPHandler_Notification_NoResponse(t *testing.T) {
	srv := httptest.NewServer(newTestMux().HTTPHandler())
	defer srv.Close()
	// No id field → notification.
	resp, err := http.Post(srv.URL, "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","method":"client.activeMode"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status: %d want 204", resp.StatusCode)
	}
}

// ─── Auth middleware ──────────────────────────────────────────────────

func TestMux_BearerAuth_Required(t *testing.T) {
	m := newTestMux().WithAuth(BearerAuth(func(token string) (string, error) {
		if token == "good" {
			return "user-1", nil
		}
		return "", errors.New("bad token")
	}))
	srv := httptest.NewServer(m.HTTPHandler())
	defer srv.Close()

	// Missing auth.
	resp, _ := http.Post(srv.URL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"client.activeMode"}`))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing auth: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Bad token.
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"client.activeMode"}`))
	req.Header.Set("Authorization", "Bearer wrong")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad auth: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Good token.
	req, _ = http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"client.activeMode"}`))
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("good auth: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("good auth: status %d, body=%s", resp.StatusCode, string(body))
	}
	resp.Body.Close()
}

// ─── SSE transport ───────────────────────────────────────────────────

func TestMux_SSEHandler_StreamsEvents(t *testing.T) {
	m := newTestMux()
	srv := httptest.NewServer(m.SSEHandler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	// Trigger an event after the stream is established.  SetMode emits
	// EventModeChanged synchronously, so a small delay then poll suffices.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = m.client.SetMode(context.Background(), "act")
	}()

	// Read until we see at least one event.
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = resp.Request.Body
		n, err := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if bytes.Contains(buf, []byte("event: mode_changed")) {
				return
			}
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("did not see mode_changed event; got: %q", string(buf))
}

// ─── WebSocket transport ─────────────────────────────────────────────

func TestMux_WebSocketHandler_DispatchAndPushEvents(t *testing.T) {
	m := newTestMux()
	srv := httptest.NewServer(m.WebSocketHandler())
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	// Send a request; expect a response.
	if err := c.WriteMessage(websocket.TextMessage,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"client.activeMode"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read response (skip any push notifications first).
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	gotResp := false
	for !gotResp {
		_, msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var generic map[string]json.RawMessage
		_ = json.Unmarshal(msg, &generic)
		if _, ok := generic["id"]; ok {
			// Response.
			if string(generic["id"]) != "1" {
				t.Errorf("id: %s", string(generic["id"]))
			}
			gotResp = true
			break
		}
		// Otherwise it's a notification — keep reading.
	}

	// Now trigger a push event by mutating client state and confirm we
	// receive it as a "client.event" notification.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = m.client.SetMode(context.Background(), "auto")
	}()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("read push: %v", err)
		}
		var generic map[string]json.RawMessage
		_ = json.Unmarshal(msg, &generic)
		if method, ok := generic["method"]; ok && string(method) == `"client.event"` {
			return // success
		}
	}
}

func TestMux_WebSocketHandler_UnknownMethod(t *testing.T) {
	m := newTestMux()
	srv := httptest.NewServer(m.WebSocketHandler())
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if err := c.WriteMessage(websocket.TextMessage,
		[]byte(`{"jsonrpc":"2.0","id":42,"method":"client.no-such"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var resp Response
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue
		}
		if string(resp.ID) != "42" {
			continue // notification or different correlation
		}
		if resp.Error == nil || resp.Error.Code != ErrMethodNotFound {
			t.Errorf("expected method-not-found, got %+v", resp.Error)
		}
		return
	}
}

// ─── Subscribe-style fan-out smoke test ──────────────────────────────

func TestMux_Subscribe_FansOutToMultipleConnections(t *testing.T) {
	m := newTestMux()
	srv := httptest.NewServer(m.WebSocketHandler())
	defer srv.Close()

	dial := func() *websocket.Conn {
		wsURL, _ := url.Parse(srv.URL)
		wsURL.Scheme = "ws"
		c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return c
	}

	c1 := dial()
	defer c1.Close()
	c2 := dial()
	defer c2.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	for _, c := range []*websocket.Conn{c1, c2} {
		go func() {
			defer wg.Done()
			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			for {
				_, msg, err := c.ReadMessage()
				if err != nil {
					t.Errorf("read: %v", err)
					return
				}
				var generic map[string]json.RawMessage
				_ = json.Unmarshal(msg, &generic)
				if method, ok := generic["method"]; ok && string(method) == `"client.event"` {
					return
				}
			}
		}()
	}

	// Give subscribers a moment to register, then trigger.
	time.Sleep(100 * time.Millisecond)
	_ = m.client.SetMode(context.Background(), "plan")

	wg.Wait()
}

// ─── HTTP resource-safety tests ───────────────────────────────────────

// TestMux_HTTPHandler_RejectsOversizedBodyBeforeAllocation proves an
// oversized JSON-RPC HTTP body is rejected with the stable
// requestTooLargeData category, and that the cap is enforced by
// http.MaxBytesReader rather than a successful full read followed by a
// size check (a read of maxHTTPBodyBytes+1 bytes must fail).
func TestMux_HTTPHandler_RejectsOversizedBodyBeforeAllocation(t *testing.T) {
	old := maxHTTPBodyBytes
	maxHTTPBodyBytes = 64
	defer func() { maxHTTPBodyBytes = old }()

	srv := httptest.NewServer(newTestMux().HTTPHandler())
	defer srv.Close()

	body := strings.NewReader(strings.Repeat("a", int(maxHTTPBodyBytes)+1))
	resp, err := http.Post(srv.URL, "application/json", body)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	var out Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Error == nil {
		t.Fatal("expected an error response for an oversized body")
	}
	if out.Error.Data != requestTooLargeData {
		t.Fatalf("error data = %v, want stable %q category", out.Error.Data, requestTooLargeData)
	}
}

// TestMux_HTTPHandler_AcceptsBodyExactlyAtCap proves the boundary is
// inclusive: a body of exactly maxHTTPBodyBytes bytes is accepted and fully
// read, matching ADR-004/ADR-007's exact-boundary semantics for framed and
// bounded protocols throughout this codebase.
func TestMux_HTTPHandler_AcceptsBodyExactlyAtCap(t *testing.T) {
	old := maxHTTPBodyBytes
	maxHTTPBodyBytes = 256
	defer func() { maxHTTPBodyBytes = old }()

	srv := httptest.NewServer(newTestMux().HTTPHandler())
	defer srv.Close()

	const prefix = `{"jsonrpc":"2.0","id":1,"method":"client.activeMode","pad":"`
	const suffix = `"}`
	padLen := int(maxHTTPBodyBytes) - len(prefix) - len(suffix)
	if padLen < 0 {
		t.Fatalf("cap %d too small for fixture overhead", maxHTTPBodyBytes)
	}
	payload := prefix + strings.Repeat("x", padLen) + suffix
	if int64(len(payload)) != maxHTTPBodyBytes {
		t.Fatalf("fixture length = %d, want exactly %d", len(payload), maxHTTPBodyBytes)
	}

	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	var out Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Error != nil {
		t.Fatalf("unexpected error at exact boundary: %+v", out.Error)
	}
}

// TestMux_HTTPHandler_CancellationPropagatesToHandler proves the context
// Dispatch receives is cancelled once httpRequestTimeout elapses, and that
// a handler cooperating with ctx.Done() observes it — i.e. bounded
// execution time actually cancels, not merely "eventually returns because
// the handler happened to finish".
func TestMux_HTTPHandler_CancellationPropagatesToHandler(t *testing.T) {
	oldTimeout := httpRequestTimeout
	httpRequestTimeout = 30 * time.Millisecond
	defer func() { httpRequestTimeout = oldTimeout }()

	m := newTestMux()
	cancelled := make(chan struct{})
	m.Register(Method{
		Name: "test.awaitCancel",
		Handler: func(ctx context.Context, _ *client.Client, _ json.RawMessage) (any, error) {
			<-ctx.Done()
			close(cancelled)
			return nil, ctx.Err()
		},
	})
	srv := httptest.NewServer(m.HTTPHandler())
	defer srv.Close()

	resp, err := http.Post(srv.URL, "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"test.awaitCancel"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never observed context cancellation within the safety bound")
	}
}

func TestMux_HTTPHandler_NonCooperativeDispatchReturnsAndDiscardsLateResult(t *testing.T) {
	oldTimeout := httpRequestTimeout
	httpRequestTimeout = 30 * time.Millisecond
	defer func() { httpRequestTimeout = oldTimeout }()

	m := newTestMux()
	// A private, single-slot dispatch limiter lets this test conclusively
	// drain its one intentionally-uncooperative dispatch child (see
	// drainDispatchLimiter) instead of leaving it to race a later test's
	// bounded-encoding tuning var mutations under go test -race.
	m.dispatchLimit = newDispatchLimiter(1)
	entered := make(chan struct{})
	release := make(chan struct{})
	childReturned := make(chan struct{})
	m.Register(Method{
		Name: "test.ignoreContext",
		Handler: func(context.Context, *client.Client, json.RawMessage) (any, error) {
			close(entered)
			<-release
			close(childReturned)
			return "late", nil
		},
	})

	// setResponseWriteDeadline fails closed for an unsupported writer, so a
	// direct ServeHTTP test that expects a real write must use the explicit
	// deadline-capable adapter rather than a bare httptest.ResponseRecorder
	// (see writeDeadlineRecorder's doc comment and CONTRACT.md).
	rec := newWriteDeadlineRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rpc",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"test.ignoreContext"}`))
	handlerDone := make(chan struct{})
	go func() {
		m.HTTPHandler().ServeHTTP(rec, req)
		close(handlerDone)
	}()

	<-entered
	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP request waited for context-ignoring method code")
	}

	var response Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode timeout response: %v", err)
	}
	if response.Error == nil || response.Error.Data != requestTimeoutData {
		t.Fatalf("response error = %+v, want stable %q category", response.Error, requestTimeoutData)
	}
	before := rec.Body.String()

	close(release)
	<-childReturned
	if after := rec.Body.String(); after != before {
		t.Fatalf("late child mutated ResponseWriter: before=%q after=%q", before, after)
	}

	// childReturned only proves the Handler itself returned, not that the
	// dispatch child's own subsequent bounded-encode step has finished —
	// see drainDispatchLimiter's doc comment.
	drainDispatchLimiter(t, m.dispatchLimit, 5*time.Second)
}

func TestMux_HTTPHandler_BoundsBlockedDispatchChildren(t *testing.T) {
	oldTimeout := httpRequestTimeout
	httpRequestTimeout = 40 * time.Millisecond
	defer func() { httpRequestTimeout = oldTimeout }()

	m := newTestMux()
	m.dispatchLimit = newDispatchLimiter(2)
	release := make(chan struct{})
	entered := make(chan struct{}, cap(m.dispatchLimit.slots))
	m.Register(Method{
		Name: "test.blockedChild",
		Handler: func(context.Context, *client.Client, json.RawMessage) (any, error) {
			entered <- struct{}{}
			<-release
			return "late", nil
		},
	})

	var requests sync.WaitGroup
	requests.Add(3)
	for i := 0; i < 3; i++ {
		go func(id int) {
			defer requests.Done()
			// See the deadline-capable adapter note above.
			rec := newWriteDeadlineRecorder()
			req := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(
				fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"test.blockedChild"}`, id)))
			m.HTTPHandler().ServeHTTP(rec, req)
		}(i)
	}

	for i := 0; i < cap(m.dispatchLimit.slots); i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("blocked HTTP children did not reach configured cap")
		}
	}
	requests.Wait()
	select {
	case <-entered:
		t.Fatal("method children exceeded the configured HTTP dispatch cap")
	default:
	}
	close(release)

	// See drainDispatchLimiter's doc comment: both blocked children's own
	// bounded-encode steps must finish before this test returns, or a
	// later test's tuning var mutations can race them under go test -race.
	// (The third request's dispatch child never acquires a slot at all —
	// it observes its own request's ctx.Done() from httpRequestTimeout
	// first — so exactly two real acquirers ever exist here, matching this
	// limiter's configured capacity with no other competitor left to
	// interfere with the drain.)
	drainDispatchLimiter(t, m.dispatchLimit, 5*time.Second)
}

// TestMux_HTTPHandler_CustomMarshalerNeverInvokedReturnsEncodingError proves
// the bounded encoder rejects a json.Marshaler-implementing result WITHOUT
// ever calling its MarshalJSON — the blocking channel in blockingJSONValue
// would hang this test forever if it were invoked, so the request
// completing at all (let alone quickly, with no goroutine/timeout dance
// needed) is itself part of the proof. This replaces the previous R3-era
// test, which assumed encoding/json's Marshal (and therefore an arbitrary
// blocking MarshalJSON) was still on this path; bounded_json.go's contract
// is exactly the opposite — no reachable Marshaler/TextMarshaler is ever
// invoked (see CONTRACT.md's bounded-encoding contract).
func TestMux_HTTPHandler_CustomMarshalerNeverInvokedReturnsEncodingError(t *testing.T) {
	m := newTestMux()
	marshalEntered := make(chan struct{}, 1)
	m.Register(Method{
		Name: "test.blockedMarshal",
		Handler: func(context.Context, *client.Client, json.RawMessage) (any, error) {
			// release is never closed; if MarshalJSON is ever called this
			// method hangs forever waiting on it, so a test that completes
			// at all is already proof MarshalJSON was not invoked.
			return blockingJSONValue{entered: marshalEntered, release: make(chan struct{})}, nil
		},
	})

	// See the deadline-capable adapter note above.
	rec := newWriteDeadlineRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rpc",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"test.blockedMarshal"}`))
	m.HTTPHandler().ServeHTTP(rec, req)

	select {
	case <-marshalEntered:
		t.Fatal("bounded encoder invoked a custom MarshalJSON implementation")
	default:
	}

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil || resp.Error.Data != responseEncodingErrorData {
		t.Fatalf("response error = %+v, want stable %q category", resp.Error, responseEncodingErrorData)
	}
}

// TestMux_HTTPHandler_LateChildRetainsGlobalSlotUntilItActuallyFinishes
// proves a dispatch child that keeps running past its request's own timeout
// (the supervisor already returned a response_timeout to the client) still
// occupies its global dispatch-limiter slot for its entire real lifetime —
// including the bounded-encode step, which now happens inside the same
// child (see http.go) — and only frees that capacity once it genuinely
// finishes, never early just because its supervisor moved on.
func TestMux_HTTPHandler_LateChildRetainsGlobalSlotUntilItActuallyFinishes(t *testing.T) {
	oldTimeout := httpRequestTimeout
	httpRequestTimeout = 30 * time.Millisecond
	defer func() { httpRequestTimeout = oldTimeout }()

	limiter := newDispatchLimiter(1)
	m := newTestMux()
	m.dispatchLimit = limiter

	entered := make(chan struct{})
	release := make(chan struct{})
	m.Register(Method{
		Name: "test.lateChild",
		Handler: func(context.Context, *client.Client, json.RawMessage) (any, error) {
			close(entered)
			<-release
			return "late", nil
		},
	})
	quickEntered := make(chan struct{}, 1)
	m.Register(Method{
		Name: "test.quick",
		Handler: func(context.Context, *client.Client, json.RawMessage) (any, error) {
			quickEntered <- struct{}{}
			return "ok", nil
		},
	})

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		// See the deadline-capable adapter note above.
		rec := newWriteDeadlineRecorder()
		req := httptest.NewRequest(http.MethodPost, "/rpc",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"test.lateChild"}`))
		m.HTTPHandler().ServeHTTP(rec, req)
	}()

	<-entered
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not return once its request timeout elapsed")
	}
	if got := len(limiter.slots); got != 1 {
		t.Fatalf("global slot occupied = %d, want 1 while the late child is still running", got)
	}

	// The first request's short httpRequestTimeout only needs to apply to
	// the first request (so its supervisor returns early while the child
	// keeps running). Restore a generous timeout before issuing the second
	// request so it can sit waiting for global capacity for as long as this
	// test needs, instead of racing its own request timeout against
	// close(release) below.
	httpRequestTimeout = 5 * time.Second

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		// See the deadline-capable adapter note above.
		rec := newWriteDeadlineRecorder()
		req := httptest.NewRequest(http.MethodPost, "/rpc",
			strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"test.quick"}`))
		m.HTTPHandler().ServeHTTP(rec, req)
	}()
	select {
	case <-quickEntered:
		t.Fatal("a second request acquired global capacity while the late child still holds it")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-quickEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("global capacity was not released once the late child actually finished")
	}
	<-secondDone
}

func TestMux_HTTPHandler_ResponseWriteDeadlineBoundsBlockingWriter(t *testing.T) {
	oldWriteTimeout := httpResponseWriteTimeout
	httpResponseWriteTimeout = 30 * time.Millisecond
	defer func() { httpResponseWriteTimeout = oldWriteTimeout }()

	w := newDeadlineAwareBlockingWriter()
	req := httptest.NewRequest(http.MethodPost, "/rpc",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"client.activeMode"}`))
	done := make(chan struct{})
	go func() {
		newTestMux().HTTPHandler().ServeHTTP(w, req)
		close(done)
	}()

	select {
	case <-w.deadlineInstalled:
	case <-time.After(5 * time.Second):
		t.Fatal("response writer never observed a write deadline")
	}
	select {
	case <-w.writeEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never attempted the bounded response write")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking response write did not return after its installed deadline")
	}
}

// TestMux_HTTPHandler_DeadlineCapableRecorderReceivesNormalResponse proves
// the normal (deadline-supported) HTTPHandler write path still behaves
// exactly as before once wrapped in an explicit deadline-capable adapter —
// see writeDeadlineRecorder's doc comment: CONTRACT.md requires tests to
// use exactly this kind of explicit adapter for the write-expected path
// rather than relying on any generic fail-open behavior from a bare
// httptest.ResponseRecorder, which does not itself implement
// SetWriteDeadline.
func TestMux_HTTPHandler_DeadlineCapableRecorderReceivesNormalResponse(t *testing.T) {
	rec := newWriteDeadlineRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rpc",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"client.activeMode"}`))

	newTestMux().HTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var response Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode recorder response: %v", err)
	}
	if response.Error != nil || string(response.ID) != "1" {
		t.Fatalf("unexpected recorder response: %+v", response)
	}
	if calls := rec.deadlineCalls(); len(calls) == 0 {
		t.Fatal("HTTPHandler never installed a write deadline on the deadline-capable recorder")
	}
}

// TestMux_HTTPHandler_UnsupportedWriterFailsClosedWithoutWriting proves
// setResponseWriteDeadline's fail-closed contract end to end through
// HTTPHandler: a writer that cannot support a write deadline at all (no
// SetWriteDeadline, no Unwrap to something that has one — the same
// panic-on-write sentinel response_write.go's WithResponseWriteBounds test
// uses) must never receive a Write or WriteHeader call for any of
// HTTPHandler's write sites — the normal success path, the pre-dispatch
// error paths (writeJSON), and the notification/status path (writeStatus).
// If any of those sites failed to check setResponseWriteDeadline's return
// value before writing, this test would panic instead of merely observing
// an empty response.
func TestMux_HTTPHandler_UnsupportedWriterFailsClosedWithoutWriting(t *testing.T) {
	t.Run("normal_response", func(t *testing.T) {
		w := &panicOnWriteResponseWriter{}
		req := httptest.NewRequest(http.MethodPost, "/rpc",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"client.activeMode"}`))
		newTestMux().HTTPHandler().ServeHTTP(w, req) // must not panic
	})
	t.Run("parse_error", func(t *testing.T) {
		w := &panicOnWriteResponseWriter{}
		req := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(`not-json`))
		newTestMux().HTTPHandler().ServeHTTP(w, req) // must not panic
	})
	t.Run("notification", func(t *testing.T) {
		w := &panicOnWriteResponseWriter{}
		req := httptest.NewRequest(http.MethodPost, "/rpc",
			strings.NewReader(`{"jsonrpc":"2.0","method":"client.activeMode"}`))
		newTestMux().HTTPHandler().ServeHTTP(w, req) // must not panic
	})
	t.Run("method_not_allowed", func(t *testing.T) {
		w := &panicOnWriteResponseWriter{}
		req := httptest.NewRequest(http.MethodGet, "/rpc", nil)
		newTestMux().HTTPHandler().ServeHTTP(w, req) // must not panic
	})
}

// setResponseWriteDeadline unit tests: prove the fail-closed contract at
// the smallest possible scope, independent of any handler plumbing above
// it.

func TestSetResponseWriteDeadline_SupportedWriterSucceeds(t *testing.T) {
	rec := newWriteDeadlineRecorder()
	if !setResponseWriteDeadline(rec) {
		t.Fatal("setResponseWriteDeadline = false for a writer that supports SetWriteDeadline")
	}
	if calls := rec.deadlineCalls(); len(calls) != 1 {
		t.Fatalf("deadline calls = %d, want exactly 1", len(calls))
	}
}

func TestSetResponseWriteDeadline_UnsupportedWriterFailsClosed(t *testing.T) {
	w := &panicOnWriteResponseWriter{}
	if setResponseWriteDeadline(w) {
		t.Fatal("setResponseWriteDeadline = true for a writer with no SetWriteDeadline/Unwrap support, want fail-closed false")
	}
}

func TestSetResponseWriteDeadline_UnwrapOnlyWriterStillSucceeds(t *testing.T) {
	// An unwrap-only wrapper around a real *httptest.ResponseRecorder still
	// has no SetWriteDeadline anywhere in its chain, so this must also fail
	// closed — it is not enough to merely support Unwrap.
	w := &unwrapOnlyResponseWriter{ResponseWriter: httptest.NewRecorder()}
	if setResponseWriteDeadline(w) {
		t.Fatal("setResponseWriteDeadline = true through an Unwrap chain with no real SetWriteDeadline support, want fail-closed false")
	}
}

// TestMux_HTTPHandler_SlowBodyIsBoundedByDeadline proves a client that
// trickles a request body in and then stalls indefinitely does not pin the
// handler forever: the per-request read deadline (derived from
// httpRequestTimeout via http.ResponseController) must cut the connection
// loose within a bounded wait.
func TestMux_HTTPHandler_SlowBodyIsBoundedByDeadline(t *testing.T) {
	oldTimeout := httpRequestTimeout
	httpRequestTimeout = 50 * time.Millisecond
	defer func() { httpRequestTimeout = oldTimeout }()

	srv := httptest.NewServer(newTestMux().HTTPHandler())
	defer srv.Close()

	pr, pw := io.Pipe()
	stop := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		_, _ = pw.Write([]byte(`{"jsonrpc"`))
		<-stop
		_ = pw.Close()
	}()
	defer func() {
		close(stop)
		<-writerDone
	}()

	req, err := http.NewRequest(http.MethodPost, srv.URL, pr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.ContentLength = -1 // force chunked transfer; body is never "complete"

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()

	select {
	case <-done:
		// Either outcome (client-visible error, or a response once the
		// server-side deadline forced the read to fail) proves the slow
		// client did not block the handler forever.
	case <-time.After(10 * time.Second):
		t.Fatal("slow-client request was not bounded by httpRequestTimeout")
	}
}

// ─── WebSocket resource-safety tests ───────────────────────────────────

// wsTestServer wraps h so the returned drain function blocks until every
// WebSocketHandler invocation served by srv has fully returned, including
// its internal housekeeping goroutines (the shutdown watcher and ping
// ticker), which are tracked by the handler's own sync.WaitGroup and only
// unblock once the connection is closed/cancelled.
//
// This exists because httptest.Server.Close() does NOT wait for a hijacked
// connection: net/http/httptest's internal ConnState hook calls wg.Done()
// the moment a connection transitions to StateHijacked (which happens
// inside websocket.Upgrader.Upgrade), so a WebSocket connection is
// completely untracked by srv.Close() from that point on. Without an
// explicit drain, a still-unwinding handler goroutine from one test can
// race the *next* test's mutation of a shared package-level tuning
// variable (maxWSMessageBytes, wsPongWait, maxWSInFlightPerConn) — a real
// bug this drain exists to make impossible, not just a test artifact.
//
// Callers must ensure the connection is already closed (or the server has
// already decided to close it) before calling drain; otherwise drain blocks
// until the connection eventually ends.
func wsTestServer(h http.Handler) (srv *httptest.Server, drain func()) {
	var wg sync.WaitGroup
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wg.Add(1)
		defer wg.Done()
		h.ServeHTTP(w, r)
	})
	srv = httptest.NewServer(wrapped)
	return srv, wg.Wait
}

// TestMux_WebSocketHandler_RejectsMismatchedOrigin proves the default
// upgrader fails closed for a browser-presented cross-origin handshake.
func TestMux_WebSocketHandler_RejectsMismatchedOrigin(t *testing.T) {
	m := newTestMux()
	srv, drain := wsTestServer(m.WebSocketHandler())
	defer srv.Close()
	defer drain()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	header := http.Header{"Origin": []string{"http://evil.example"}}
	c, resp, err := websocket.DefaultDialer.Dial(wsURL.String(), header)
	if err == nil {
		c.Close()
		t.Fatal("expected handshake to fail for a mismatched Origin")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		status := -1
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("status = %d, want %d", status, http.StatusForbidden)
	}
	// A rejected handshake never reaches WebSocketHandler's upgraded body
	// (Upgrade itself fails before wg.Add would ever run in that path), so
	// drain (deferred above) returns immediately; no connection to close.
}

// TestMux_WebSocketHandler_AcceptsMatchingOrigin proves a same-origin
// browser handshake still succeeds under the fail-closed default.
func TestMux_WebSocketHandler_AcceptsMatchingOrigin(t *testing.T) {
	m := newTestMux()
	srv, drain := wsTestServer(m.WebSocketHandler())
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	header := http.Header{"Origin": []string{"http://" + wsURL.Host}}
	c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), header)
	if err != nil {
		t.Fatalf("expected handshake to succeed for a matching Origin: %v", err)
	}
	c.Close()
	drain()
}

// TestMux_WebSocketHandler_AcceptsAbsentOriginForNonBrowserClients proves
// the bearer-capable non-browser compatibility path (no Origin header, e.g.
// a CLI or service-to-service caller) is unaffected by the fail-closed
// browser-Origin check. Every other WS test in this file also dials without
// an Origin header; this test asserts it explicitly for regression
// protection against accidentally making the check unconditional.
func TestMux_WebSocketHandler_AcceptsAbsentOriginForNonBrowserClients(t *testing.T) {
	m := newTestMux()
	srv, drain := wsTestServer(m.WebSocketHandler())
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("expected handshake to succeed without an Origin header: %v", err)
	}
	c.Close()
	drain()
}

// TestMux_WebSocketHandler_RejectsOversizedMessage proves an inbound
// message larger than maxWSMessageBytes is rejected by the read-limit
// check (which fails the read and closes the connection) rather than being
// fully buffered and dispatched.
func TestMux_WebSocketHandler_RejectsOversizedMessage(t *testing.T) {
	old := maxWSMessageBytes
	maxWSMessageBytes = 1024
	defer func() { maxWSMessageBytes = old }()

	m := newTestMux()
	srv, drain := wsTestServer(m.WebSocketHandler())
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	oversized := make([]byte, maxWSMessageBytes+1)
	if err := c.WriteMessage(websocket.BinaryMessage, oversized); err != nil {
		// The server may already have closed the connection fast enough
		// that even the write fails; that still proves the oversized
		// message never reached dispatch.
		drain()
		return
	}

	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("expected the connection to close after an oversized message")
	}
	// The server already closed its side to have produced that read error;
	// drain waits for its handler goroutine (and internal wg) to finish
	// unwinding before this test returns and the deferred restore of
	// maxWSMessageBytes runs.
	drain()
}

// TestMux_WebSocketHandler_ClosesUnresponsiveClient proves a client that
// stops answering Pong control frames (indistinguishable, from the
// connection's perspective, from a hung or crashed peer) is closed within a
// bounded wait rather than pinning the connection and its goroutines
// forever. wsPongWait is shrunk so the test does not need a real-world-sized
// wait; the assertion itself blocks on a channel-equivalent (ReadMessage)
// with a generous safety bound, not a timing ratio.
func TestMux_WebSocketHandler_ClosesUnresponsiveClient(t *testing.T) {
	oldWait := wsPongWait
	wsPongWait = 100 * time.Millisecond
	defer func() { wsPongWait = oldWait }()

	m := newTestMux()
	srv, drain := wsTestServer(m.WebSocketHandler())
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	// Disable the client's automatic Pong response so the connection looks
	// unresponsive to the server despite the TCP link itself staying open.
	c.SetPingHandler(func(string) error { return nil })

	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("expected the server to close an unresponsive connection")
	}
	// The server already closed its side (that is what produced the read
	// error above); drain waits for its handler goroutine to fully unwind
	// before this test returns and the deferred restore of wsPongWait runs.
	drain()
}

// TestMux_WebSocketHandler_BoundsInFlightRequestsPerConnection proves the
// number of concurrently executing method dispatches for one connection
// never exceeds maxWSInFlightPerConn: it sends more requests than the cap
// to a handler that blocks on a shared gate, and asserts (via a channel
// barrier, not a sleep-based ratio) that exactly the capped number of
// handlers become active before any of them can complete.
func TestMux_WebSocketHandler_BoundsInFlightRequestsPerConnection(t *testing.T) {
	oldCap := maxWSInFlightPerConn
	maxWSInFlightPerConn = 2
	defer func() { maxWSInFlightPerConn = oldCap }()
	inFlightCap := maxWSInFlightPerConn

	m := newTestMux()
	gate := make(chan struct{})
	var active int32
	reachedCap := make(chan struct{})
	var once sync.Once
	m.Register(Method{
		Name: "test.blockUntilGate",
		Handler: func(_ context.Context, _ *client.Client, _ json.RawMessage) (any, error) {
			if atomic.AddInt32(&active, 1) == int32(inFlightCap) {
				once.Do(func() { close(reachedCap) })
			}
			<-gate
			atomic.AddInt32(&active, -1)
			return "ok", nil
		},
	})
	srv, drain := wsTestServer(m.WebSocketHandler())
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	const send = 5 // deliberately more than cap
	for i := 0; i < send; i++ {
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"test.blockUntilGate"}`, i)
		if err := c.WriteMessage(websocket.TextMessage, []byte(req)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	select {
	case <-reachedCap:
	case <-time.After(5 * time.Second):
		t.Fatal("active handler count never reached the configured cap")
	}
	if got := atomic.LoadInt32(&active); got != int32(inFlightCap) {
		t.Fatalf("active in-flight handlers = %d, want exactly the cap %d (bound not enforced)", got, inFlightCap)
	}

	close(gate)

	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	seen := make(map[string]bool, send)
	for len(seen) < send {
		_, msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var resp Response
		if err := json.Unmarshal(msg, &resp); err != nil || len(resp.ID) == 0 {
			continue
		}
		seen[string(resp.ID)] = true
	}

	// Close the client side and wait for the server's handler goroutine
	// (and its internal wg-tracked housekeeping goroutines) to fully
	// unwind before this test returns and the deferred restore of
	// maxWSInFlightPerConn runs.
	c.Close()
	drain()
}

// TestMux_WebSocketHandler_ConcurrentWritesDoNotRace proves multiple
// goroutines racing to write responses on the same connection (forced via a
// barrier so their releases genuinely overlap) never corrupt a frame or
// trigger a data race under go test -race — i.e. exactly one writer owns
// the connection at a time.
func TestMux_WebSocketHandler_ConcurrentWritesDoNotRace(t *testing.T) {
	const n = 20

	m := newTestMux()
	gate := make(chan struct{})
	var entered int32
	reachedAll := make(chan struct{})
	var once sync.Once
	m.Register(Method{
		Name: "test.concurrentWrite",
		Handler: func(_ context.Context, _ *client.Client, _ json.RawMessage) (any, error) {
			if atomic.AddInt32(&entered, 1) == int32(n) {
				once.Do(func() { close(reachedAll) })
			}
			<-gate
			return "ok", nil
		},
	})
	srv, drain := wsTestServer(m.WebSocketHandler())
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	for i := 0; i < n; i++ {
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"test.concurrentWrite"}`, i)
		if err := c.WriteMessage(websocket.TextMessage, []byte(req)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	select {
	case <-reachedAll:
	case <-time.After(5 * time.Second):
		t.Fatal("handlers never reached full concurrency before release")
	}
	close(gate) // release all n handlers simultaneously -> genuinely concurrent writes

	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	seen := make(map[string]bool, n)
	for len(seen) < n {
		_, msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var resp Response
		if err := json.Unmarshal(msg, &resp); err != nil || len(resp.ID) == 0 {
			continue // a client.event notification or partial decode; ignore
		}
		seen[string(resp.ID)] = true
	}

	c.Close()
	drain()
}

// TestMux_WebSocketHandler_ContextCancellationClosesConnection proves the
// shutdown watcher forces a blocked conn.ReadMessage() to unblock when the
// handler's context is cancelled from outside (e.g. server shutdown), so a
// connection is drained instead of leaking its read loop and goroutines
// until the peer eventually disconnects on its own.
func TestMux_WebSocketHandler_ContextCancellationClosesConnection(t *testing.T) {
	m := newTestMux()
	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	defer cancelShutdown()

	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.WebSocketHandler().ServeHTTP(w, r.WithContext(shutdownCtx))
	})
	srv, drain := wsTestServer(wrapped)
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	// Confirm the connection is alive and dispatching before cancelling.
	if err := c.WriteMessage(websocket.TextMessage,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"client.activeMode"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := c.ReadMessage(); err != nil {
		t.Fatalf("read initial response: %v", err)
	}

	cancelShutdown()

	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("expected the connection to close after context cancellation")
	}
	// The watcher goroutine forced the close that produced the read error
	// above; drain waits for the handler's full unwind (including that
	// watcher and the ping-ticker goroutine) before this test returns.
	drain()
}

func TestMux_WebSocketHandler_GlobalDispatchLimitSurvivesDisconnectAndReconnect(t *testing.T) {
	attempted := make(chan struct{}, 3)
	limiter := newDispatchLimiter(2)
	limiter.beforeAcquire = func() { attempted <- struct{}{} }

	m := newTestMux()
	m.dispatchLimit = limiter
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	var active int32
	var peak int32
	m.Register(Method{
		Name: "test.blockAcrossConnections",
		Handler: func(context.Context, *client.Client, json.RawMessage) (any, error) {
			current := atomic.AddInt32(&active, 1)
			for {
				oldPeak := atomic.LoadInt32(&peak)
				if current <= oldPeak || atomic.CompareAndSwapInt32(&peak, oldPeak, current) {
					break
				}
			}
			entered <- struct{}{}
			<-release
			atomic.AddInt32(&active, -1)
			return "released", nil
		},
	})

	srv, drain := wsTestServer(m.WebSocketHandler())
	defer srv.Close()
	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	dial := func() *websocket.Conn {
		c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return c
	}

	first := dial()
	for i := 0; i < 2; i++ {
		request := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"test.blockAcrossConnections"}`, i)
		if err := first.WriteMessage(websocket.TextMessage, []byte(request)); err != nil {
			t.Fatalf("first client write %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		select {
		case <-attempted:
		case <-time.After(5 * time.Second):
			t.Fatal("first client child never attempted global acquisition")
		}
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("first client child never entered blocked dispatch")
		}
	}
	if got := len(limiter.slots); got != 2 {
		t.Fatalf("occupied global slots = %d, want configured cap 2", got)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("close first client: %v", err)
	}

	second := dial()
	if err := second.WriteMessage(websocket.TextMessage,
		[]byte(`{"jsonrpc":"2.0","id":3,"method":"test.blockAcrossConnections"}`)); err != nil {
		t.Fatalf("reconnected client write: %v", err)
	}
	select {
	case <-attempted:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnected client child never reached global limiter")
	}
	select {
	case <-entered:
		t.Fatal("reconnected client exceeded global capacity held by disconnected children")
	default:
	}
	if got := len(limiter.slots); got != 2 {
		t.Fatalf("disconnect released child-owned slots early: occupied = %d, want 2", got)
	}

	close(release)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("capacity did not return after original blocked children exited")
	}
	if got := atomic.LoadInt32(&peak); got > 2 {
		t.Fatalf("peak globally active dispatch children = %d, want at most 2", got)
	}

	// The single <-entered above only proves one of the three dispatch
	// children (the two disconnected originals plus the reconnected
	// client) acquired a freed slot — not that every child's own
	// bounded-encode step has finished. No further request is ever sent
	// after this point, so nothing else will legitimately compete for
	// limiter's capacity; see drainDispatchLimiter's doc comment.
	drainDispatchLimiter(t, limiter, 5*time.Second)

	_ = second.Close()
	drained := make(chan struct{})
	go func() {
		drain()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("WebSocket handlers did not drain after clients closed")
	}
}

func TestMux_WebSocketHandler_NonCooperativeChildDoesNotBlockShutdownOrWriteLateResult(t *testing.T) {
	oldCap := maxWSInFlightPerConn
	maxWSInFlightPerConn = 1
	defer func() { maxWSInFlightPerConn = oldCap }()

	m := newTestMux()
	// A private, single-slot dispatch limiter lets this test conclusively
	// drain its one intentionally context-ignoring dispatch child (see
	// drainDispatchLimiter) instead of leaving it to race a later test's
	// bounded-encoding tuning var mutations under go test -race.
	m.dispatchLimit = newDispatchLimiter(1)
	entered := make(chan struct{})
	release := make(chan struct{})
	childReturned := make(chan struct{})
	m.Register(Method{
		Name: "test.ignoreWSCancel",
		Handler: func(context.Context, *client.Client, json.RawMessage) (any, error) {
			close(entered)
			<-release
			close(childReturned)
			return "late", nil
		},
	})

	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.WebSocketHandler().ServeHTTP(w, r.WithContext(shutdownCtx))
	})
	srv, drain := wsTestServer(wrapped)
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if err := c.WriteMessage(websocket.TextMessage,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"test.ignoreWSCancel"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	<-entered

	cancelShutdown()
	drained := make(chan struct{})
	go func() {
		drain()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("WebSocket shutdown waited for context-ignoring method child")
	}

	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("connection remained open after cancellation")
	}
	close(release)
	<-childReturned
	// The result-only child has no connection reference. Its late completion
	// cannot produce a second frame; the already-closed connection remains closed.
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("late method result wrote a frame after connection shutdown")
	}

	// childReturned only proves the Handler itself returned, not that the
	// dispatch child's own subsequent bounded-encode step has finished —
	// see drainDispatchLimiter's doc comment.
	drainDispatchLimiter(t, m.dispatchLimit, 5*time.Second)
}

func TestMux_WebSocketHandler_BlockedChildrenRemainAtPerConnectionCapDuringShutdown(t *testing.T) {
	oldCap := maxWSInFlightPerConn
	maxWSInFlightPerConn = 2
	defer func() { maxWSInFlightPerConn = oldCap }()

	m := newTestMux()
	// A private, per-test dispatch limiter (exactly maxWSInFlightPerConn
	// capacity, matching the per-connection sem this test already relies
	// on) replaces the shared process-wide default so this test can prove,
	// via a genuine channel happens-before edge rather than a sleep, that
	// its two intentionally-blocked-past-shutdown dispatch children have
	// fully finished — including their boundedEncodeResponse call — before
	// the test function returns. Without this, those untracked children
	// (handleWSRequest's result-only child is deliberately not tracked by
	// WebSocketHandler's connection wait group; see websocket.go) keep
	// running after close(release) below and race the next test's
	// package-level bounded-encoding tuning var mutations
	// (maxBoundedJSONBytes/maxBoundedJSONNodes) under go test -race.
	limiter := newDispatchLimiter(maxWSInFlightPerConn)
	m.dispatchLimit = limiter
	release := make(chan struct{})
	entered := make(chan struct{}, maxWSInFlightPerConn)
	m.Register(Method{
		Name: "test.permanentlyBlocked",
		Handler: func(context.Context, *client.Client, json.RawMessage) (any, error) {
			entered <- struct{}{}
			<-release
			return "late", nil
		},
	})
	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.WebSocketHandler().ServeHTTP(w, r.WithContext(shutdownCtx))
	})
	srv, drain := wsTestServer(wrapped)
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	for i := 0; i < maxWSInFlightPerConn+3; i++ {
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"test.permanentlyBlocked"}`, i)
		if err := c.WriteMessage(websocket.TextMessage, []byte(req)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	for i := 0; i < maxWSInFlightPerConn; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("blocked WS children did not reach per-connection cap")
		}
	}

	cancelShutdown()
	_ = c.Close()
	drained := make(chan struct{})
	go func() {
		drain()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("connection shutdown waited for blocked method children")
	}
	select {
	case <-entered:
		t.Fatal("blocked WS children exceeded per-connection cap")
	default:
	}
	close(release)

	// Each blocked child's `defer limiter.release()` (see handleWSRequest
	// in websocket.go) runs strictly after its own boundedEncodeResponse
	// call, in the same goroutine. Re-acquiring both of this limiter's
	// slots therefore cannot succeed until both children have completed
	// that call — a real synchronization edge, not a timing assumption —
	// so this test cannot return while either child might still be reading
	// package-level bounded-encoding tuning variables.
	drainedChildren := make(chan struct{})
	go func() {
		for i := 0; i < maxWSInFlightPerConn; i++ {
			limiter.acquire(context.Background())
		}
		close(drainedChildren)
	}()
	select {
	case <-drainedChildren:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked WS dispatch children never released their dispatch capacity — a child may still be running past this test's return")
	}
}

// ─── bounded_json.go: structural encoder ────────────────────────────────

func TestBoundedJSON_ExactCapAndCapPlusOne(t *testing.T) {
	oldMax := maxBoundedJSONBytes
	defer func() { maxBoundedJSONBytes = oldMax }()
	maxBoundedJSONBytes = 32

	// A JSON string literal with no bytes needing escaping costs len+2
	// (the surrounding quotes).
	fits := strings.Repeat("a", maxBoundedJSONBytes-2)
	b, err := encodeBoundedJSON(context.Background(), fits)
	if err != nil {
		t.Fatalf("value at exact cap must encode, got %v", err)
	}
	if len(b) != maxBoundedJSONBytes {
		t.Fatalf("encoded length = %d, want exactly %d", len(b), maxBoundedJSONBytes)
	}

	tooBig := strings.Repeat("a", maxBoundedJSONBytes-1)
	if _, err := encodeBoundedJSON(context.Background(), tooBig); classifyBoundedError(err) != boundedEncodeOversize {
		t.Fatalf("value one byte over cap: err = %v, want oversize", err)
	}
}

func TestBoundedJSON_EscapingExpansionCountsAgainstCap(t *testing.T) {
	oldMax := maxBoundedJSONBytes
	defer func() { maxBoundedJSONBytes = oldMax }()

	// Each control byte expands to a 6-byte \u00XX escape. A budget sized
	// for the raw (unescaped) length must still reject it.
	raw := strings.Repeat("\x01", 10)
	maxBoundedJSONBytes = len(raw) + 2
	if _, err := encodeBoundedJSON(context.Background(), raw); classifyBoundedError(err) != boundedEncodeOversize {
		t.Fatalf("escaped control-character string under a raw-sized budget: err = %v, want oversize", err)
	}

	maxBoundedJSONBytes = len(raw)*6 + 2 // its true escaped size
	b, err := encodeBoundedJSON(context.Background(), raw)
	if err != nil {
		t.Fatalf("escaped string at its true escaped cap must encode, got %v", err)
	}
	if len(b) != maxBoundedJSONBytes {
		t.Fatalf("encoded length = %d, want %d", len(b), maxBoundedJSONBytes)
	}
}

func TestBoundedJSON_LargeStringsSlicesMaps(t *testing.T) {
	s := strings.Repeat("x", 200000)
	b, err := encodeBoundedJSON(context.Background(), s)
	if err != nil {
		t.Fatalf("large string: %v", err)
	}
	var gotStr string
	if err := json.Unmarshal(b, &gotStr); err != nil || gotStr != s {
		t.Fatalf("large string round-trip failed: err=%v", err)
	}

	sl := make([]int, 50000)
	for i := range sl {
		sl[i] = i
	}
	b, err = encodeBoundedJSON(context.Background(), sl)
	if err != nil {
		t.Fatalf("large slice: %v", err)
	}
	var gotSlice []int
	if err := json.Unmarshal(b, &gotSlice); err != nil || len(gotSlice) != len(sl) {
		t.Fatalf("large slice round-trip failed: err=%v len=%d", err, len(gotSlice))
	}

	mp := make(map[string]int, 5000)
	for i := 0; i < 5000; i++ {
		mp[fmt.Sprintf("key-%05d", i)] = i
	}
	b, err = encodeBoundedJSON(context.Background(), mp)
	if err != nil {
		t.Fatalf("large map: %v", err)
	}
	var gotMap map[string]int
	if err := json.Unmarshal(b, &gotMap); err != nil || len(gotMap) != len(mp) {
		t.Fatalf("large map round-trip failed: err=%v len=%d", err, len(gotMap))
	}
}

func TestBoundedJSON_BytesBase64(t *testing.T) {
	raw := make([]byte, 10000)
	for i := range raw {
		raw[i] = byte(i)
	}
	b, err := encodeBoundedJSON(context.Background(), raw)
	if err != nil {
		t.Fatalf("byte slice: %v", err)
	}
	var got []byte
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode base64 string: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("base64 round-trip mismatch")
	}
}

type deepChainNode struct {
	Next *deepChainNode `json:"next,omitempty"`
}

func TestBoundedJSON_DeepValueExceedsDepthLimit(t *testing.T) {
	oldDepth := maxBoundedJSONDepth
	defer func() { maxBoundedJSONDepth = oldDepth }()
	maxBoundedJSONDepth = 8

	var head *deepChainNode
	for i := 0; i < 20; i++ {
		head = &deepChainNode{Next: head}
	}
	if _, err := encodeBoundedJSON(context.Background(), head); classifyBoundedError(err) != boundedEncodeInvalid {
		t.Fatalf("deep chain: err = %v, want invalid (depth exceeded)", err)
	}
}

type selfRefNode struct {
	Self any `json:"self,omitempty"`
}

func TestBoundedJSON_CyclicValueDetected(t *testing.T) {
	n := &selfRefNode{}
	n.Self = n
	if _, err := encodeBoundedJSON(context.Background(), n); classifyBoundedError(err) != boundedEncodeInvalid {
		t.Fatalf("cyclic struct via pointer: err = %v, want invalid (cycle detected)", err)
	}

	m := map[string]any{}
	m["self"] = m
	if _, err := encodeBoundedJSON(context.Background(), m); classifyBoundedError(err) != boundedEncodeInvalid {
		t.Fatalf("cyclic map: err = %v, want invalid (cycle detected)", err)
	}
}

func TestBoundedJSON_RawMessageValidation(t *testing.T) {
	valid := json.RawMessage(`{"a":1,"b":[1,2,3]}`)
	b, err := encodeBoundedJSON(context.Background(), valid)
	if err != nil {
		t.Fatalf("valid RawMessage: %v", err)
	}
	if string(b) != string(valid) {
		t.Fatalf("RawMessage must be copied verbatim: got %s want %s", b, valid)
	}

	invalid := json.RawMessage(`{not json`)
	if _, err := encodeBoundedJSON(context.Background(), invalid); classifyBoundedError(err) != boundedEncodeInvalid {
		t.Fatalf("invalid RawMessage: err = %v, want invalid", err)
	}

	var nilRaw json.RawMessage
	b, err = encodeBoundedJSON(context.Background(), nilRaw)
	if err != nil || string(b) != "null" {
		t.Fatalf("nil RawMessage must encode as null, got %s, err %v", b, err)
	}
}

// TestBoundedJSON_TimeEncodedNotRejected guards against a regression where
// time.Time — reachable in nearly every response type in this codebase via
// an UpdatedAt/CreatedAt/etc. field (client.State, ConversationSummary, ...)
// — was unconditionally rejected as response_encoding_error because it
// implements json.Marshaler/encoding.TextMarshaler, breaking those RPC
// methods entirely rather than protecting against anything adversarial. See
// bounded_json.go's timeType/encodeTime.
func TestBoundedJSON_TimeEncodedNotRejected(t *testing.T) {
	ts := time.Date(2026, 8, 14, 20, 15, 26, 936185034, time.UTC)
	b, err := encodeBoundedJSON(context.Background(), ts)
	if err != nil {
		t.Fatalf("time.Time must encode, got err: %v", err)
	}
	want := `"` + ts.Format(time.RFC3339Nano) + `"`
	if string(b) != want {
		t.Fatalf("time.Time encoded = %s, want %s", b, want)
	}

	// The same guarantee must hold nested inside a struct field, mirroring
	// how client.State.UpdatedAt / ConversationSummary.UpdatedAt are
	// actually reached in real RPC responses.
	type withTime struct {
		UpdatedAt time.Time `json:"updatedAt"`
	}
	b, err = encodeBoundedJSON(context.Background(), withTime{UpdatedAt: ts})
	if err != nil {
		t.Fatalf("struct with time.Time field must encode, got err: %v", err)
	}
	wantStruct := `{"updatedAt":` + want + `}`
	if string(b) != wantStruct {
		t.Fatalf("struct with time.Time field encoded = %s, want %s", b, wantStruct)
	}

	// Zero value must also encode (not treated as an error/edge case).
	if _, err := encodeBoundedJSON(context.Background(), time.Time{}); err != nil {
		t.Fatalf("zero time.Time must encode, got err: %v", err)
	}
}

type invokeTrackingJSONMarshaler struct {
	invoked *int32
}

func (m invokeTrackingJSONMarshaler) MarshalJSON() ([]byte, error) {
	atomic.AddInt32(m.invoked, 1)
	return []byte(`"should never appear"`), nil
}

type invokeTrackingTextMarshaler struct {
	invoked *int32
}

func (m invokeTrackingTextMarshaler) MarshalText() ([]byte, error) {
	atomic.AddInt32(m.invoked, 1)
	return []byte("should never appear"), nil
}

func TestBoundedJSON_CustomMarshalersNeverInvoked(t *testing.T) {
	var jsonCalls, textCalls int32

	if _, err := encodeBoundedJSON(context.Background(), invokeTrackingJSONMarshaler{invoked: &jsonCalls}); classifyBoundedError(err) != boundedEncodeInvalid {
		t.Fatalf("json.Marshaler value: err = %v, want invalid", err)
	}
	if atomic.LoadInt32(&jsonCalls) != 0 {
		t.Fatal("MarshalJSON was invoked")
	}

	if _, err := encodeBoundedJSON(context.Background(), invokeTrackingTextMarshaler{invoked: &textCalls}); classifyBoundedError(err) != boundedEncodeInvalid {
		t.Fatalf("encoding.TextMarshaler value: err = %v, want invalid", err)
	}
	if atomic.LoadInt32(&textCalls) != 0 {
		t.Fatal("MarshalText was invoked")
	}

	// Reached indirectly through an `any` field (as an arbitrary method
	// result would be), the same guarantee must hold: the encoder resolves
	// the concrete type before rejecting it, never the static interface
	// type, and still never invokes the method.
	var wrapped any = invokeTrackingJSONMarshaler{invoked: &jsonCalls}
	if _, err := encodeBoundedJSON(context.Background(), wrapped); classifyBoundedError(err) != boundedEncodeInvalid {
		t.Fatalf("wrapped marshaler value: err = %v, want invalid", err)
	}
	if atomic.LoadInt32(&jsonCalls) != 0 {
		t.Fatal("MarshalJSON was invoked when reached through an interface")
	}
}

func TestBoundedJSON_NodeCountLimit(t *testing.T) {
	oldNodes := maxBoundedJSONNodes
	defer func() { maxBoundedJSONNodes = oldNodes }()
	maxBoundedJSONNodes = 10

	sl := make([]int, 100)
	if _, err := encodeBoundedJSON(context.Background(), sl); classifyBoundedError(err) != boundedEncodeInvalid {
		t.Fatalf("value exceeding node budget: err = %v, want invalid", err)
	}
}

func TestBoundedJSON_UnsupportedKindRejected(t *testing.T) {
	ch := make(chan int)
	if _, err := encodeBoundedJSON(context.Background(), ch); classifyBoundedError(err) != boundedEncodeInvalid {
		t.Fatalf("chan value: err = %v, want invalid", err)
	}
}

func TestBoundedJSON_NonStringKeyedMapRejected(t *testing.T) {
	m := map[int]string{1: "a"}
	if _, err := encodeBoundedJSON(context.Background(), m); classifyBoundedError(err) != boundedEncodeInvalid {
		t.Fatalf("int-keyed map: err = %v, want invalid", err)
	}
}

func TestBoundedJSON_CancellationObservedBeforeEncoding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := encodeBoundedJSON(ctx, map[string]any{"a": 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("encodeBoundedJSON with an already-canceled context: err = %v, want context.Canceled", err)
	}
}

// ─── jsonrpc.go / bounded_json.go: response envelope fallbacks ─────────

type trackingUnknownError struct {
	invoked *int32
}

func (e *trackingUnknownError) Error() string {
	if e.invoked != nil {
		atomic.AddInt32(e.invoked, 1)
	}
	return "must never appear in a response"
}

func TestMakeErrorResponse_UnknownErrorNeverFormatted(t *testing.T) {
	var invoked int32
	resp := makeErrorResponse(json.RawMessage(`1`), &trackingUnknownError{invoked: &invoked})
	if atomic.LoadInt32(&invoked) != 0 {
		t.Fatal("makeErrorResponse invoked Error() on an unknown error type")
	}
	if resp.Error == nil || resp.Error.Message != "internal error" || resp.Error.Data != nil {
		t.Fatalf("unexpected error response for unknown error: %+v", resp.Error)
	}

	// A genuine *ErrorObject is preserved verbatim, never re-wrapped.
	obj := NewError(ErrInvalidParams, "bad param", "field")
	resp = makeErrorResponse(json.RawMessage(`1`), obj)
	if resp.Error != obj {
		t.Fatalf("makeErrorResponse must preserve a genuine *ErrorObject verbatim")
	}
}

func TestBoundedEncodeResponse_OversizedErrorObjectDataFallsBackToResponseTooLarge(t *testing.T) {
	oldMax := maxBoundedJSONBytes
	defer func() { maxBoundedJSONBytes = oldMax }()
	maxBoundedJSONBytes = 256

	resp := makeErrorResponse(json.RawMessage(`7`), NewError(ErrInvalidParams, "bad", strings.Repeat("x", 10000)))
	b := boundedEncodeResponse(context.Background(), resp)

	var decoded Response
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("fallback response must be valid JSON: %v (body=%s)", err, b)
	}
	if decoded.Error == nil || decoded.Error.Data != responseTooLargeData {
		t.Fatalf("decoded error = %+v, want stable %q category", decoded.Error, responseTooLargeData)
	}
	if decoded.Error.Message != responseTooLargeMessage {
		t.Fatalf("decoded message = %q, want %q", decoded.Error.Message, responseTooLargeMessage)
	}
	if string(decoded.ID) != "7" {
		t.Fatalf("id = %s, want preserved 7", decoded.ID)
	}
	if len(b) > maxBoundedJSONBytes {
		t.Fatalf("fallback itself exceeded the cap: %d > %d", len(b), maxBoundedJSONBytes)
	}
}

func TestBoundedEncodeResponse_IDDroppedToNullWhenEvenFallbackDoesNotFit(t *testing.T) {
	oldMax := maxBoundedJSONBytes
	defer func() { maxBoundedJSONBytes = oldMax }()
	maxBoundedJSONBytes = 64 // smaller than a huge id plus the fixed fallback error shape

	hugeID := json.RawMessage(strings.Repeat("1", 1000))
	resp := makeErrorResponse(hugeID, NewError(ErrInvalidParams, "bad", strings.Repeat("x", 10000)))
	b := boundedEncodeResponse(context.Background(), resp)

	var decoded Response
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("fallback response must be valid JSON: %v (body=%s)", err, b)
	}
	if string(decoded.ID) != "null" {
		t.Fatalf("id = %s, want null once even the fallback with the real id does not fit", decoded.ID)
	}
}

func TestBoundedEncodeResponse_CancellationFallsBackToEncodingError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp := makeResponse(json.RawMessage(`1`), map[string]any{"a": 1})
	b := boundedEncodeResponse(ctx, resp)
	var decoded Response
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("fallback must be valid JSON: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Data != responseEncodingErrorData {
		t.Fatalf("decoded = %+v, want response_encoding_error fallback for a canceled context", decoded.Error)
	}
}

// ─── bounded_json.go: map encoding repair (R5) ──────────────────────────

// TestBoundedJSON_MapEncodingExactDeterministicOrdering proves encodeMap's
// MapRange-into-one-bounded-entry-slice-then-sort replacement for the old
// MapKeys()+byName-map trio still produces byte-exact, deterministic key
// ordering — plain lexical sort of the string keys, matching sort.Strings.
func TestBoundedJSON_MapEncodingExactDeterministicOrdering(t *testing.T) {
	m := map[string]int{"banana": 2, "apple": 1, "cherry": 3, "": 0}
	b, err := encodeBoundedJSON(context.Background(), m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	const want = `{"":0,"apple":1,"banana":2,"cherry":3}`
	if string(b) != want {
		t.Fatalf("encoded map = %s, want exact deterministic ordering %s", b, want)
	}
}

// TestBoundedJSON_MapCardinalityPreflightRejectsOnNodeBudget proves a map
// whose cardinality alone exceeds the remaining node budget is rejected by
// mapCardinalityPreflight before v.MapKeys/MapRange or any ordering
// allocation ever runs — reached here indirectly through encodeBoundedJSON,
// exactly like every other public-facing entry point.
func TestBoundedJSON_MapCardinalityPreflightRejectsOnNodeBudget(t *testing.T) {
	oldNodes := maxBoundedJSONNodes
	defer func() { maxBoundedJSONNodes = oldNodes }()
	maxBoundedJSONNodes = 4 // 1 consumed by the map node itself; 3 remain

	m := map[string]int{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5} // 5 entries > 3 remaining
	if _, err := encodeBoundedJSON(context.Background(), m); classifyBoundedError(err) != boundedEncodeInvalid {
		t.Fatalf("map cardinality exceeding remaining node budget: err = %v, want invalid", err)
	}
}

// TestBoundedJSON_MapCardinalityPreflightRejectsOnMinimumOutputBudget proves
// the overflow-safe minimum-output byte preflight independently of the node
// budget: a cardinality whose minimum possible output (minJSONMapEntryBytes
// per entry) already exceeds the remaining byte budget must be rejected as
// oversize before any per-entry allocation.
func TestBoundedJSON_MapCardinalityPreflightRejectsOnMinimumOutputBudget(t *testing.T) {
	oldMax := maxBoundedJSONBytes
	defer func() { maxBoundedJSONBytes = oldMax }()
	maxBoundedJSONBytes = 10 // '{'+'}' leaves 8 remaining bytes; minJSONMapEntryBytes=4 → at most 2 entries could ever fit

	m := map[string]int{"a": 1, "b": 2, "c": 3} // 3 entries: 3*4=12 > 8 remaining
	if _, err := encodeBoundedJSON(context.Background(), m); classifyBoundedError(err) != boundedEncodeOversize {
		t.Fatalf("map cardinality exceeding the minimum-output byte budget: err = %v, want oversize", err)
	}
}

// TestBoundedJSON_MapCardinalityRejectionAllocationsDoNotScaleWithSourceMapSize
// is the allocation-shaped proof CONTRACT.md requires: two source maps of
// wildly different cardinality are built OUTSIDE testing.AllocsPerRun's
// measured closure (map construction itself is intentionally excluded from
// the measurement), and the measured operation — encodeBoundedJSON hitting
// mapCardinalityPreflight's node-budget rejection — must allocate
// essentially the same, small, constant number of times for both, proving
// the rejection path never allocates anything proportional to the source
// map's cardinality (no MapKeys result, no byName map, no ordering/entry
// slice sized by the attacker-controlled length).
func TestBoundedJSON_MapCardinalityRejectionAllocationsDoNotScaleWithSourceMapSize(t *testing.T) {
	oldNodes := maxBoundedJSONNodes
	defer func() { maxBoundedJSONNodes = oldNodes }()
	maxBoundedJSONNodes = 4 // 1 consumed by the map node itself; 3 remain — both maps below vastly exceed that.

	small := make(map[string]int, 100)
	for i := 0; i < 100; i++ {
		small[fmt.Sprintf("k%d", i)] = i
	}
	large := make(map[string]int, 200000)
	for i := 0; i < 200000; i++ {
		large[fmt.Sprintf("k%d", i)] = i
	}

	measure := func(m map[string]int) float64 {
		return testing.AllocsPerRun(20, func() {
			if _, err := encodeBoundedJSON(context.Background(), m); classifyBoundedError(err) != boundedEncodeInvalid {
				t.Fatalf("map cardinality preflight rejection: err = %v, want invalid", err)
			}
		})
	}

	smallAllocs := measure(small)
	largeAllocs := measure(large)

	if largeAllocs > smallAllocs+1 {
		t.Fatalf("rejection allocations scaled with source map cardinality: small(n=100)=%.1f allocs, large(n=200000)=%.1f allocs — want the large map's rejection to cost essentially the same as the small map's", smallAllocs, largeAllocs)
	}
}

// ─── bounded_json.go: exported API and errors.Is sentinels (R5) ─────────

// TestEncodeBoundedJSON_ExportedMatchesInternalEncoding proves the exported
// EncodeBoundedJSON wrapper CONTRACT.md requires ("Worker 1 exports
// serve.EncodeBoundedJSON(ctx, value) ([]byte, error)") produces byte-exact
// output identical to the package-internal encodeBoundedJSON it wraps, at
// the same full maxBoundedJSONBytes cap with no trailing newline.
func TestEncodeBoundedJSON_ExportedMatchesInternalEncoding(t *testing.T) {
	v := map[string]any{"a": 1, "b": "two", "c": []int{1, 2, 3}}
	got, err := EncodeBoundedJSON(context.Background(), v)
	if err != nil {
		t.Fatalf("EncodeBoundedJSON: %v", err)
	}
	want, err := encodeBoundedJSON(context.Background(), v)
	if err != nil {
		t.Fatalf("encodeBoundedJSON: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("EncodeBoundedJSON output = %s, want exact match with internal encodeBoundedJSON output %s", got, want)
	}
	if len(got) > 0 && got[len(got)-1] == '\n' {
		t.Fatal("EncodeBoundedJSON must return compact JSON with no trailing newline")
	}
}

// TestEncodeBoundedJSON_ErrorsIsSentinels proves ErrResponseTooLarge and
// ErrResponseEncoding are errors.Is-compatible (via *boundedEncodeError's
// Unwrap), distinct from one another, and returned via the exported
// EncodeBoundedJSON entry point Worker 2's /api/peers handler consumes.
func TestEncodeBoundedJSON_ErrorsIsSentinels(t *testing.T) {
	oldMax := maxBoundedJSONBytes
	maxBoundedJSONBytes = 4
	_, oversizeErr := EncodeBoundedJSON(context.Background(), "way too long for four bytes")
	maxBoundedJSONBytes = oldMax

	if !errors.Is(oversizeErr, ErrResponseTooLarge) {
		t.Fatalf("oversize value: err = %v, want errors.Is match against ErrResponseTooLarge", oversizeErr)
	}
	if errors.Is(oversizeErr, ErrResponseEncoding) {
		t.Fatalf("oversize value: err = %v, must not also match ErrResponseEncoding", oversizeErr)
	}

	ch := make(chan int)
	_, invalidErr := EncodeBoundedJSON(context.Background(), ch)
	if !errors.Is(invalidErr, ErrResponseEncoding) {
		t.Fatalf("unsupported value: err = %v, want errors.Is match against ErrResponseEncoding", invalidErr)
	}
	if errors.Is(invalidErr, ErrResponseTooLarge) {
		t.Fatalf("unsupported value: err = %v, must not also match ErrResponseTooLarge", invalidErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, cancelErr := EncodeBoundedJSON(ctx, map[string]any{"a": 1})
	if !errors.Is(cancelErr, context.Canceled) {
		t.Fatalf("canceled context: err = %v, want errors.Is match against context.Canceled", cancelErr)
	}
	if errors.Is(cancelErr, ErrResponseTooLarge) || errors.Is(cancelErr, ErrResponseEncoding) {
		t.Fatalf("canceled context: err = %v, must remain distinguishable from both bounded-encode sentinels", cancelErr)
	}
}

// ─── http.go: explicit-limit HTTP JSON-RPC response envelope (R5) ───────

// TestHTTPBoundedResponseEnvelope_ExactCapAndCapPlusOne proves
// httpBoundedResponseEnvelope's one-byte newline reservation is exact: a
// Response whose structural encoding lands exactly at the reserved
// (maxBoundedJSONBytes-1) limit produces a final payload — trailing newline
// included — of exactly maxBoundedJSONBytes, and a Response one byte larger
// still produces a payload (fixed response_too_large fallback plus newline)
// that never exceeds maxBoundedJSONBytes.
func TestHTTPBoundedResponseEnvelope_ExactCapAndCapPlusOne(t *testing.T) {
	oldMax := maxBoundedJSONBytes
	defer func() { maxBoundedJSONBytes = oldMax }()
	// Large enough that the fixed response_too_large fallback error object
	// (jsonrpc/id/error/code/message/data scaffolding) comfortably fits
	// under the reserved limit too — the cap+1 case below must exercise the
	// oversize-falls-back-and-still-fits path, not the much rarer
	// "even the fallback doesn't fit" last-resort literal path already
	// covered by TestBoundedEncodeResponse_IDDroppedToNullWhenEvenFallbackDoesNotFit.
	maxBoundedJSONBytes = 256

	// Compute the fixed envelope overhead (jsonrpc/id/result field
	// scaffolding around an empty result string) once, so the result
	// string can be sized to land the structural encode exactly at the
	// reserved limit.
	base := Response{Jsonrpc: jsonrpcVersion, ID: json.RawMessage(`1`), Result: ""}
	baseBytes, err := encodeBoundedJSONCapped(context.Background(), base, maxBoundedJSONBytes)
	if err != nil {
		t.Fatalf("baseline structural encode: %v", err)
	}
	overhead := len(baseBytes)
	limit := maxBoundedJSONBytes - 1 // one byte reserved for the trailing '\n'
	if limit < overhead {
		t.Fatalf("test fixture misconfigured: overhead %d exceeds reserved limit %d", overhead, limit)
	}

	fits := Response{Jsonrpc: jsonrpcVersion, ID: json.RawMessage(`1`), Result: strings.Repeat("a", limit-overhead)}
	payload := httpBoundedResponseEnvelope(context.Background(), fits)
	if len(payload) != maxBoundedJSONBytes {
		t.Fatalf("payload at the exact reserved structural limit: length = %d, want exactly %d (cap including the trailing newline)", len(payload), maxBoundedJSONBytes)
	}
	if payload[len(payload)-1] != '\n' {
		t.Fatalf("payload must end with the trailing newline, got %q", payload)
	}
	var decodedFits Response
	if err := json.Unmarshal(payload[:len(payload)-1], &decodedFits); err != nil {
		t.Fatalf("payload at exact cap must decode as valid JSON before the newline: %v", err)
	}
	if decodedFits.Error != nil {
		t.Fatalf("payload at exact cap must be the real (non-fallback) response: %+v", decodedFits.Error)
	}

	tooBig := Response{Jsonrpc: jsonrpcVersion, ID: json.RawMessage(`1`), Result: strings.Repeat("a", limit-overhead+1)}
	payloadOver := httpBoundedResponseEnvelope(context.Background(), tooBig)
	if len(payloadOver) > maxBoundedJSONBytes {
		t.Fatalf("cap+1 result: payload length = %d, must never exceed %d including the trailing newline", len(payloadOver), maxBoundedJSONBytes)
	}
	if payloadOver[len(payloadOver)-1] != '\n' {
		t.Fatalf("cap+1 fallback payload must still end with the trailing newline, got %q", payloadOver)
	}
	var decodedOver Response
	if err := json.Unmarshal(payloadOver[:len(payloadOver)-1], &decodedOver); err != nil {
		t.Fatalf("cap+1 fallback must still be valid JSON before the trailing newline: %v", err)
	}
	if decodedOver.Error == nil || decodedOver.Error.Data != responseTooLargeData {
		t.Fatalf("cap+1 result: decoded = %+v, want response_too_large fallback", decodedOver.Error)
	}
}

// ─── response_write.go: WithResponseWriteBounds ─────────────────────────

func TestWithResponseWriteBounds_UnsupportedWriterFailsClosedWithoutWriting(t *testing.T) {
	w := &panicOnWriteResponseWriter{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	called := false
	h := WithResponseWriteBounds(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	h.ServeHTTP(w, req) // must not panic
	if called {
		t.Fatal("next handler ran despite an unsupported (no write-deadline) ResponseWriter")
	}
}

func TestWithResponseWriteBounds_NormalRecorderCompatibleThroughExplicitAdapter(t *testing.T) {
	rec := newWriteDeadlineRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h := WithResponseWriteBounds(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("ok"))
	}))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "ok")
	}
	calls := rec.deadlineCalls()
	if len(calls) == 0 || calls[0].IsZero() {
		t.Fatal("WithResponseWriteBounds did not install a deadline on the adapter")
	}
}

func TestWithResponseWriteBounds_SeesThroughUnwrapCapableWrapper(t *testing.T) {
	rec := newWriteDeadlineRecorder()
	wrapped := &unwrapOnlyResponseWriter{ResponseWriter: rec}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	called := false
	h := WithResponseWriteBounds(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	h.ServeHTTP(wrapped, req)
	if !called {
		t.Fatal("WithResponseWriteBounds failed closed even though the wrapper's Unwrap() exposed a deadline-capable writer")
	}
	if len(rec.deadlineCalls()) == 0 {
		t.Fatal("deadline was not installed through the Unwrap() chain")
	}
}

// ─── sse.go: write-bound deadline handling ──────────────────────────────

// TestMux_SSEHandler_BlockedFlushIsBoundedByDeadline proves SSEHandler
// installs a write deadline before its very first (handshake) write/flush,
// exactly like every other write path in this package: a writer whose Write
// blocks forever unblocks only once its installed deadline fires, and the
// handler returns instead of hanging.
func TestMux_SSEHandler_BlockedFlushIsBoundedByDeadline(t *testing.T) {
	oldWriteTimeout := httpResponseWriteTimeout
	httpResponseWriteTimeout = 30 * time.Millisecond
	defer func() { httpResponseWriteTimeout = oldWriteTimeout }()

	w := newDeadlineAwareBlockingWriter()
	req := httptest.NewRequest(http.MethodGet, "/sse", nil)
	done := make(chan struct{})
	go func() {
		newTestMux().SSEHandler().ServeHTTP(w, req)
		close(done)
	}()

	select {
	case <-w.deadlineInstalled:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE handler never installed a write deadline before its handshake write")
	}
	select {
	case <-w.writeEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE handler never attempted its bounded handshake write")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked SSE write did not return after its installed deadline")
	}
}

// TestMux_SSEHandler_RefreshesDeadlinePerWriteAndClearsWhileIdle proves the
// exact deterministic sequence CONTRACT.md requires: a non-zero deadline
// before the handshake write/flush, cleared (zero time) while idle
// afterward, a fresh non-zero deadline installed again before the next
// event's write/flush, and cleared again once idle. It asserts on recorded
// call values from a channel, not on timing.
func TestMux_SSEHandler_RefreshesDeadlinePerWriteAndClearsWhileIdle(t *testing.T) {
	m := newTestMux()
	w := newSSEDeadlineRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/sse", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		m.SSEHandler().ServeHTTP(w, req)
		close(done)
	}()

	mustRecv := func(label string) time.Time {
		t.Helper()
		select {
		case v := <-w.calls:
			return v
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %s SetWriteDeadline call", label)
			return time.Time{}
		}
	}

	handshake := mustRecv("handshake write")
	if handshake.IsZero() {
		t.Fatal("handshake write deadline must be non-zero")
	}
	idle1 := mustRecv("post-handshake idle clear")
	if !idle1.IsZero() {
		t.Fatalf("expected deadline cleared while idle after handshake, got %v", idle1)
	}

	// Trigger one event. SetMode emits EventModeChanged synchronously to
	// subscribers already registered by the time it is called, but
	// registration races this goroutine, so retry briefly instead of
	// relying on a single fixed sleep.
	eventDeadline := make(chan time.Time, 1)
	go func() {
		select {
		case v := <-w.calls:
			eventDeadline <- v
		case <-time.After(5 * time.Second):
		}
	}()
	giveUp := time.Now().Add(2 * time.Second)
	for time.Now().Before(giveUp) {
		_ = m.client.SetMode(context.Background(), "act")
		select {
		case v := <-eventDeadline:
			if v.IsZero() {
				t.Fatal("event write deadline must be non-zero")
			}
			idle2 := mustRecv("post-event idle clear")
			if !idle2.IsZero() {
				t.Fatalf("expected deadline cleared while idle after event write, got %v", idle2)
			}
			cancel()
			<-done
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
	t.Fatal("SSE handler never refreshed its write deadline for a pushed event")
}
