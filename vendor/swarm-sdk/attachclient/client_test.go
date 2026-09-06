package attachclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── fakeRPCTransport: a minimal in-memory Transport for client-level tests
// that don't need real HTTP (negotiation matrix, isolation, correlation,
// detach-does-not-stop). ──────────────────────────────────────────────────

type rpcCallRecord struct {
	base   string
	method string
	params any
}

type fakeRPCTransport struct {
	mu    sync.Mutex
	calls []rpcCallRecord

	rpcFunc    func(method string, params any) (json.RawMessage, error)
	eventsFunc func(from Cursor) (RawEventStream, error)

	capsFunc func(base string) (Capabilities, error) // set to enable CapabilityProvider
}

func (t *fakeRPCTransport) RPC(ctx context.Context, base, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	t.calls = append(t.calls, rpcCallRecord{base: base, method: method, params: params})
	t.mu.Unlock()
	if t.rpcFunc != nil {
		return t.rpcFunc(method, params)
	}
	return json.RawMessage(`{}`), nil
}

func (t *fakeRPCTransport) OpenEvents(ctx context.Context, base string, from Cursor) (RawEventStream, error) {
	if t.eventsFunc != nil {
		return t.eventsFunc(from)
	}
	return &fakeRawStream{}, nil
}

func (t *fakeRPCTransport) callCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.calls)
}

func (t *fakeRPCTransport) lastCall() (rpcCallRecord, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.calls) == 0 {
		return rpcCallRecord{}, false
	}
	return t.calls[len(t.calls)-1], true
}

// capTransport wraps fakeRPCTransport and additionally implements
// CapabilityProvider, so Connect performs negotiation.
type capTransport struct {
	*fakeRPCTransport
}

func (t capTransport) Capabilities(ctx context.Context, base string) (Capabilities, error) {
	return t.capsFunc(base)
}

// ── contract negotiation matrix, exercised through Connect ────────────────

func TestConnect_NegotiationMatrix_Compatible(t *testing.T) {
	tr := capTransport{&fakeRPCTransport{capsFunc: func(base string) (Capabilities, error) {
		return Capabilities{InstanceID: "daemon-1", Ranges: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 3}}}, nil
	}}}
	tr.fakeRPCTransport.capsFunc = tr.capsFunc

	c := New(Deps{Transport: tr, SupportedVersions: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 5}}})
	sess, err := c.Connect(context.Background(), Target{BaseURL: "http://daemon"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess.NegotiatedVersion != (Version{Major: 1, Minor: 3}) {
		t.Fatalf("got negotiated %v, want 1.3", sess.NegotiatedVersion)
	}
	if sess.DaemonInstanceID != "daemon-1" {
		t.Fatalf("got DaemonInstanceID %q, want daemon-1", sess.DaemonInstanceID)
	}
}

func TestConnect_NegotiationMatrix_IncompatibleMajorFails(t *testing.T) {
	tr := capTransport{&fakeRPCTransport{}}
	tr.fakeRPCTransport.capsFunc = func(base string) (Capabilities, error) {
		return Capabilities{Ranges: []VersionRange{{Major: 9, MinMinor: 0, MaxMinor: 0}}}, nil
	}

	c := New(Deps{Transport: tr, SupportedVersions: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}}})
	_, err := c.Connect(context.Background(), Target{BaseURL: "http://daemon"})
	var ce *CompatibilityError
	if err == nil {
		t.Fatal("expected a *CompatibilityError, got nil")
	}
	if !asCompatibilityError(err, &ce) {
		t.Fatalf("expected *CompatibilityError, got %v (%T)", err, err)
	}
	if ce.Category != CompatibilityUnknownMajor {
		t.Fatalf("got category %v, want %v", ce.Category, CompatibilityUnknownMajor)
	}
}

func TestConnect_LegacyTransportSkipsNegotiation(t *testing.T) {
	// A Transport that does NOT implement CapabilityProvider is a legacy
	// peer per ADR-004 Compatibility: Connect must still succeed.
	tr := &fakeRPCTransport{}
	c := New(Deps{Transport: tr})
	sess, err := c.Connect(context.Background(), Target{BaseURL: "http://daemon"})
	if err != nil {
		t.Fatalf("unexpected error connecting to a legacy (non-negotiating) peer: %v", err)
	}
	if sess.NegotiatedVersion != (Version{}) {
		t.Fatalf("got NegotiatedVersion %v, want zero value for a legacy peer", sess.NegotiatedVersion)
	}
}

// small helper: errors.As inline (kept local to avoid importing errors twice
// across files with different aliasing needs)
func asCompatibilityError(err error, target **CompatibilityError) bool {
	ce, ok := err.(*CompatibilityError)
	if ok {
		*target = ce
	}
	return ok
}

// ── production-constructor harmless RPC smoke test ─────────────────────────

func TestConstructor_HarmlessRPCSmoke(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rpc" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "client.snapshot":
			fmt.Fprint(w, `{"result":{"Provider":"anthropic","Model":"claude","OperatingMode":"agent","ActiveConvID":"c1"}}`)
		default:
			fmt.Fprint(w, `{"result":{}}`)
		}
	}))
	defer srv.Close()

	// New(Deps{}) with everything defaulted — the "production constructor" —
	// must be able to perform one harmless negotiated-or-legacy exchange.
	c := New(Deps{})
	if _, err := c.Connect(context.Background(), Target{BaseURL: srv.URL}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Provider != "anthropic" || status.Model != "claude" || status.ActiveConversationID != "c1" {
		t.Fatalf("got %+v, want provider=anthropic model=claude activeConv=c1", status)
	}
}

// ── two-client workspace/conversation isolation test ───────────────────────

func TestTwoClients_SelectionIsolation(t *testing.T) {
	trA := &fakeRPCTransport{}
	trB := &fakeRPCTransport{}

	a := New(Deps{Transport: trA})
	b := New(Deps{Transport: trB})
	if _, err := a.Connect(context.Background(), Target{BaseURL: "http://daemon"}); err != nil {
		t.Fatalf("connect a: %v", err)
	}
	if _, err := b.Connect(context.Background(), Target{BaseURL: "http://daemon"}); err != nil {
		t.Fatalf("connect b: %v", err)
	}

	if err := a.Select(context.Background(), Selection{ConversationID: "conv-A"}); err != nil {
		t.Fatalf("select a: %v", err)
	}
	if err := b.Select(context.Background(), Selection{ConversationID: "conv-B"}); err != nil {
		t.Fatalf("select b: %v", err)
	}

	if got := a.Selection().ConversationID; got != "conv-A" {
		t.Fatalf("client A selection leaked: got %q, want conv-A", got)
	}
	if got := b.Selection().ConversationID; got != "conv-B" {
		t.Fatalf("client B selection leaked: got %q, want conv-B", got)
	}
	if a.SessionInfo().ID == b.SessionInfo().ID {
		t.Fatalf("two independently Connect-ed clients must not share a session ID")
	}
}

// ── approval and cancel correlation test ────────────────────────────────────

func TestApproveCorrelation_PassesCallIDAndDecision(t *testing.T) {
	tr := &fakeRPCTransport{}
	c := New(Deps{Transport: tr})
	if _, err := c.Connect(context.Background(), Target{BaseURL: "http://daemon"}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if err := c.Approve(context.Background(), ApprovalDecision{CallID: "call-42", Allow: true}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	last, ok := tr.lastCall()
	if !ok || last.method != "client.respondApproval" {
		t.Fatalf("got last call %+v, want client.respondApproval", last)
	}
	params, ok := last.params.(map[string]any)
	if !ok || params["callID"] != "call-42" || params["allow"] != true {
		t.Fatalf("got params %+v, want callID=call-42 allow=true", last.params)
	}
}

func TestCancelCorrelation_UnknownExecutionIDNeverReachesDaemon(t *testing.T) {
	tr := &fakeRPCTransport{}
	c := New(Deps{Transport: tr})
	if _, err := c.Connect(context.Background(), Target{BaseURL: "http://daemon"}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	before := tr.callCount()
	if err := c.Cancel(context.Background(), ExecutionID("bogus")); err == nil {
		t.Fatal("expected an error cancelling an unknown execution ID")
	}
	if got := tr.callCount(); got != before {
		t.Fatalf("cancelling an unknown execution ID must never reach the daemon: got %d calls (was %d)", got, before)
	}
}

func TestSubmitThenCancelCorrelation_KnownIDReachesDaemonExactlyOnce(t *testing.T) {
	tr := &fakeRPCTransport{}
	c := New(Deps{Transport: tr})
	if _, err := c.Connect(context.Background(), Target{BaseURL: "http://daemon"}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	exec, err := c.Submit(context.Background(), Work{Method: "client.sendMessage", Params: map[string]any{"message": "hi"}})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if exec.ID == "" {
		t.Fatal("expected a non-empty ExecutionID correlating this work item")
	}

	if err := c.Cancel(context.Background(), exec.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	last, ok := tr.lastCall()
	if !ok || last.method != "client.cancel" {
		t.Fatalf("got last call %+v, want client.cancel", last)
	}
	params, ok := last.params.(map[string]any)
	if !ok || params["clientExecutionID"] != string(exec.ID) {
		t.Fatalf("got params %+v, want clientExecutionID=%s", last.params, exec.ID)
	}

	// Cancelling the same ID twice must fail the second time (already
	// completed/removed from the in-flight correlation map) and must not
	// re-issue client.cancel to the daemon.
	before := tr.callCount()
	if err := c.Cancel(context.Background(), exec.ID); err == nil {
		t.Fatal("expected an error double-cancelling the same execution ID")
	}
	if got := tr.callCount(); got != before {
		t.Fatalf("double-cancel must not reach the daemon again: got %d calls (was %d)", got, before)
	}
}

// blockingRawStream never yields an event until ctx is cancelled — used to
// simulate a steady-state open connection in tests that need Detach/Close
// to be the only thing that unblocks the reconnect loop.
type blockingRawStream struct{}

func (blockingRawStream) Next(ctx context.Context) (RawEvent, error) {
	<-ctx.Done()
	return RawEvent{}, ctx.Err()
}

func (blockingRawStream) Close() error { return nil }

// ── detach-does-not-stop test ───────────────────────────────────────────────

func TestDetach_ReleasesOnlyClientResources(t *testing.T) {
	tr := &fakeRPCTransport{}
	var openCalls int
	var openMu sync.Mutex
	tr.eventsFunc = func(from Cursor) (RawEventStream, error) {
		openMu.Lock()
		openCalls++
		n := openCalls
		openMu.Unlock()
		if n == 1 {
			return &fakeRawStream{events: []RawEvent{
				{Kind: "stream_start", Cursor: Cursor{StreamID: "s1", Sequence: 1}, Payload: agentEventPayload("stream_start")},
			}}, nil
		}
		// Every reconnect after the first scripted event just holds the
		// connection open (steady state) until ctx (Detach) cancels it.
		return blockingRawStream{}, nil
	}

	c := New(Deps{Transport: tr})
	if _, err := c.Connect(context.Background(), Target{BaseURL: "http://daemon"}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	stream, err := c.Events(context.Background(), Cursor{})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	// Drain the one scripted event so the stream is in steady state.
	select {
	case <-stream.Events:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for scripted event")
	}

	before := tr.callCount()
	if err := c.Detach(context.Background()); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if got := tr.callCount(); got != before {
		t.Fatalf("Detach must issue zero daemon RPC calls (it releases client resources only): got %d calls (was %d)", got, before)
	}

	// The event stream must be torn down (Events channel closes).
	select {
	case _, ok := <-stream.Events:
		if ok {
			t.Fatal("expected Events channel to close after Detach")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Events channel to close after Detach")
	}

	// Every subsequent Client method must fail locally with ErrDetached and
	// must not touch the network either.
	if _, err := c.Status(context.Background()); err != ErrDetached {
		t.Fatalf("Status after Detach: got %v, want ErrDetached", err)
	}
	if err := c.Select(context.Background(), Selection{ConversationID: "x"}); err != ErrDetached {
		t.Fatalf("Select after Detach: got %v, want ErrDetached", err)
	}
	if _, err := c.Submit(context.Background(), Work{Method: "client.sendMessage"}); err != ErrDetached {
		t.Fatalf("Submit after Detach: got %v, want ErrDetached", err)
	}
	if got := tr.callCount(); got != before {
		t.Fatalf("post-Detach calls must never reach the daemon: got %d calls (was %d)", got, before)
	}

	// Detach is idempotent.
	if err := c.Detach(context.Background()); err != nil {
		t.Fatalf("second Detach: got %v, want nil (idempotent)", err)
	}
}

// ── timeout, auth-error, and malformed-event (over real HTTP) tests ───────

func TestHTTPTransport_RPCTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"result":{}}`))
	}))
	defer srv.Close()

	tr := NewHTTPTransport(20 * time.Millisecond)
	_, err := tr.RPC(context.Background(), srv.URL, "client.snapshot", map[string]any{})
	if err == nil {
		t.Fatal("expected a deadline-exceeded error for a slow RPC, got nil (rpcCall used to have NO deadline at all)")
	}
}

func TestHTTPTransport_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("nope"))
	}))
	defer srv.Close()

	tr := NewHTTPTransport(0)
	_, err := tr.RPC(context.Background(), srv.URL, "client.snapshot", map[string]any{})
	var ae *AuthError
	if err == nil {
		t.Fatal("expected an *AuthError, got nil")
	}
	if a, ok := err.(*AuthError); ok {
		ae = a
	} else {
		t.Fatalf("got %v (%T), want *AuthError", err, err)
	}
	if ae.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", ae.StatusCode)
	}
}

func TestHTTPTransport_RPCErrorSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":{"message":"boom"}}`)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(0)
	_, err := tr.RPC(context.Background(), srv.URL, "client.sendMessage", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v, want an error mentioning \"boom\"", err)
	}
}

func TestHTTPTransport_MalformedRPCResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(0)
	_, err := tr.RPC(context.Background(), srv.URL, "client.snapshot", map[string]any{})
	if err == nil {
		t.Fatal("expected a malformed-response error, got nil")
	}
}

func TestHTTPTransport_SSE_RoundTripAndMalformedEventIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"kind\":\"stream_start\"}\n\n")
		fmt.Fprint(w, "data: {not valid json\n\n")
		fmt.Fprint(w, "data: {\"kind\":\"stream_end\"}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	tr := NewHTTPTransport(0)
	rs, err := tr.OpenEvents(context.Background(), srv.URL, Cursor{})
	if err != nil {
		t.Fatalf("OpenEvents: %v", err)
	}
	defer rs.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var kinds []string
	for i := 0; i < 3; i++ {
		raw, err := rs.Next(ctx)
		if err != nil {
			t.Fatalf("Next(%d): %v", i, err)
		}
		ev, gap := decodeEvent(raw)
		if gap != nil {
			t.Fatalf("unexpected gap for a malformed (non-gap) event: %+v", gap)
		}
		kinds = append(kinds, ev.Kind)
	}
	if kinds[0] != "stream_start" || kinds[2] != "stream_end" {
		t.Fatalf("got kinds %v, want [stream_start ? stream_end] with the malformed middle one tolerated, not panicking", kinds)
	}
}
