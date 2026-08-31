package presentationcontrol

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/attachcontract"
)

// errReadTimeout is returned by readLineWithTimeout when no line arrives
// within the given deadline.
var errReadTimeout = errors.New("presentationcontrol: test timed out waiting for a response line")

// testCapabilities returns a small, deterministic Capabilities value used
// across dispatch tests.
func testCapabilities() Capabilities {
	return Capabilities{
		SupportedRanges: []attachcontract.VersionRange{
			{Major: 1, MinMinor: 0, MaxMinor: 3},
		},
		Capabilities: []string{"presentation.ping", "presentation.capabilities"},
	}
}

// TestDispatchPingAndCapabilitiesEndToEnd proves both built-in verbs work
// end-to-end over a real net.Pipe(): OpPing echoes the payload back, and
// OpCapabilities returns exactly the Capabilities the Dispatcher was
// constructed with.
func TestDispatchPingAndCapabilitiesEndToEnd(t *testing.T) {
	caps := testCapabilities()
	d := NewDispatcher(caps)

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- d.Serve(serverConn)
	}()

	clientReader := bufio.NewReader(clientConn)

	// --- presentation.ping ---
	type pingPayload struct {
		Value int `json:"value"`
	}
	pingReq, err := NewRequest(OpPing, pingPayload{Value: 42})
	if err != nil {
		t.Fatalf("NewRequest(OpPing) failed: %v", err)
	}
	if err := writeEnvelope(clientConn, pingReq); err != nil {
		t.Fatalf("writing ping request failed: %v", err)
	}

	pingRespLine, err := readLineWithTimeout(clientReader, 2*time.Second)
	if err != nil {
		t.Fatalf("reading ping response failed: %v", err)
	}
	var pingResp Envelope
	if err := json.Unmarshal(pingRespLine, &pingResp); err != nil {
		t.Fatalf("unmarshaling ping response failed: %v (line=%q)", err, pingRespLine)
	}
	if pingResp.Op != OpPing {
		t.Fatalf("pingResp.Op = %q, want %q", pingResp.Op, OpPing)
	}
	if pingResp.CorrelationID != pingReq.CorrelationID {
		t.Fatalf("pingResp.CorrelationID = %q, want %q", pingResp.CorrelationID, pingReq.CorrelationID)
	}
	if pingResp.Error != nil {
		t.Fatalf("pingResp.Error = %+v, want nil", pingResp.Error)
	}
	var echoed pingPayload
	if err := json.Unmarshal(pingResp.Payload, &echoed); err != nil {
		t.Fatalf("unmarshaling echoed payload failed: %v", err)
	}
	if echoed.Value != 42 {
		t.Fatalf("echoed.Value = %d, want 42", echoed.Value)
	}

	// --- presentation.capabilities ---
	capsReq, err := NewRequest(OpCapabilities, nil)
	if err != nil {
		t.Fatalf("NewRequest(OpCapabilities) failed: %v", err)
	}
	if err := writeEnvelope(clientConn, capsReq); err != nil {
		t.Fatalf("writing capabilities request failed: %v", err)
	}

	capsRespLine, err := readLineWithTimeout(clientReader, 2*time.Second)
	if err != nil {
		t.Fatalf("reading capabilities response failed: %v", err)
	}
	var capsResp Envelope
	if err := json.Unmarshal(capsRespLine, &capsResp); err != nil {
		t.Fatalf("unmarshaling capabilities response failed: %v (line=%q)", err, capsRespLine)
	}
	if capsResp.Error != nil {
		t.Fatalf("capsResp.Error = %+v, want nil", capsResp.Error)
	}
	var gotCaps Capabilities
	if err := json.Unmarshal(capsResp.Payload, &gotCaps); err != nil {
		t.Fatalf("unmarshaling capabilities payload failed: %v", err)
	}
	if len(gotCaps.SupportedRanges) != 1 || gotCaps.SupportedRanges[0] != caps.SupportedRanges[0] {
		t.Fatalf("gotCaps.SupportedRanges = %+v, want %+v", gotCaps.SupportedRanges, caps.SupportedRanges)
	}
	if !gotCaps.HasCapability("presentation.ping") || !gotCaps.HasCapability("presentation.capabilities") {
		t.Fatalf("gotCaps.Capabilities = %+v, missing expected entries", gotCaps.Capabilities)
	}

	// Closing the client conn should make Serve return cleanly (nil).
	_ = clientConn.Close()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve returned error after clean close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for Serve to return after client closed the connection")
	}
}

// TestDispatchUnknownOperation proves an unknown (but well-formed) Op
// produces a CategoryUnknownOperation error response WITHOUT closing the
// connection, so a client can keep issuing further well-formed requests.
func TestDispatchUnknownOperation(t *testing.T) {
	d := NewDispatcher(testCapabilities())
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	go func() { _ = d.Serve(serverConn) }()

	clientReader := bufio.NewReader(clientConn)

	req, err := NewRequest("presentation.does_not_exist", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	if err := writeEnvelope(clientConn, req); err != nil {
		t.Fatalf("writing request failed: %v", err)
	}

	line, err := readLineWithTimeout(clientReader, 2*time.Second)
	if err != nil {
		t.Fatalf("reading response failed: %v", err)
	}
	var resp Envelope
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshaling response failed: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("resp.Error is nil, want a CategoryUnknownOperation error")
	}
	if resp.Error.Category != CategoryUnknownOperation {
		t.Fatalf("resp.Error.Category = %q, want %q", resp.Error.Category, CategoryUnknownOperation)
	}

	// Connection must still be usable: send a valid ping afterward.
	pingReq, err := NewRequest(OpPing, nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	if err := writeEnvelope(clientConn, pingReq); err != nil {
		t.Fatalf("writing follow-up ping failed: %v", err)
	}
	pingLine, err := readLineWithTimeout(clientReader, 2*time.Second)
	if err != nil {
		t.Fatalf("reading follow-up ping response failed: %v", err)
	}
	var pingResp Envelope
	if err := json.Unmarshal(pingLine, &pingResp); err != nil {
		t.Fatalf("unmarshaling follow-up ping response failed: %v", err)
	}
	if pingResp.Op != OpPing || pingResp.Error != nil {
		t.Fatalf("follow-up ping response = %+v, want a successful OpPing response", pingResp)
	}
}

// TestDispatchMalformedEnvelopeClosesConnection proves that sending bytes
// which are not valid JSON produces a well-formed CategoryMalformedEnvelope
// error response, after which Serve closes the connection and returns
// (rather than hanging or panicking).
func TestDispatchMalformedEnvelopeClosesConnection(t *testing.T) {
	d := NewDispatcher(testCapabilities())
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- d.Serve(serverConn)
	}()

	clientReader := bufio.NewReader(clientConn)

	if _, err := clientConn.Write([]byte("this is not { valid json\n")); err != nil {
		t.Fatalf("writing malformed input failed: %v", err)
	}

	line, err := readLineWithTimeout(clientReader, 2*time.Second)
	if err != nil {
		t.Fatalf("reading malformed-envelope error response failed: %v", err)
	}
	var resp Envelope
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("the malformed-envelope error response itself must be well-formed JSON: %v (line=%q)", err, line)
	}
	if resp.Error == nil {
		t.Fatalf("resp.Error is nil, want a CategoryMalformedEnvelope error")
	}
	if resp.Error.Category != CategoryMalformedEnvelope {
		t.Fatalf("resp.Error.Category = %q, want %q", resp.Error.Category, CategoryMalformedEnvelope)
	}

	select {
	case err := <-serveDone:
		if err == nil {
			t.Fatalf("Serve returned nil error, want a non-nil error after a malformed envelope")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for Serve to return after a malformed envelope (it must close, not hang)")
	}

	// The connection must actually be closed server-side: a further client
	// write/read should observe closure rather than hang forever. net.Pipe
	// surfaces this as io.ErrClosedPipe (or similar) on the next read.
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := clientConn.Read(buf); err == nil {
		t.Fatalf("expected an error reading from clientConn after server closed its side, got nil")
	}
}

// TestDispatchOversizedEnvelopeRejected constructs a REAL oversized
// payload (larger than MaxEnvelopeSize) and proves Serve rejects it with a
// CategoryMalformedEnvelope error and closes the connection, rather than
// buffering it indefinitely or exhausting memory.
func TestDispatchOversizedEnvelopeRejected(t *testing.T) {
	d := NewDispatcher(testCapabilities())
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- d.Serve(serverConn)
	}()

	clientReader := bufio.NewReader(clientConn)

	// Build a genuinely oversized single line: a large run of bytes with
	// NO newline, so the server cannot finish the line before the size
	// bound trips.
	oversizedPayload := make([]byte, MaxEnvelopeSize+4096)
	for i := range oversizedPayload {
		oversizedPayload[i] = 'a'
	}

	writeDone := make(chan error, 1)
	go func() {
		// Write a JSON-looking prefix followed by a large run of bytes and
		// no terminating newline, simulating a broken/hostile peer that
		// never completes a line.
		_, err := clientConn.Write(append([]byte(`{"op":"presentation.ping","payload":"`), oversizedPayload...))
		writeDone <- err
	}()

	line, err := readLineWithTimeout(clientReader, 5*time.Second)
	if err != nil {
		t.Fatalf("reading oversized-envelope error response failed: %v", err)
	}
	var resp Envelope
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("the oversized-envelope error response itself must be well-formed JSON: %v", err)
	}
	if resp.Error == nil || resp.Error.Category != CategoryMalformedEnvelope {
		t.Fatalf("resp.Error = %+v, want a CategoryMalformedEnvelope error", resp.Error)
	}

	select {
	case err := <-serveDone:
		if err == nil {
			t.Fatalf("Serve returned nil error, want a non-nil error after an oversized envelope")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for Serve to return after an oversized envelope (it must reject, not hang or exhaust memory)")
	}

	// Best-effort: don't require the write goroutine to finish (net.Pipe's
	// synchronous nature plus the server closing early may leave it
	// blocked/erroring), but drain it so the test doesn't leak a goroutine
	// warning locally.
	select {
	case <-writeDone:
	case <-time.After(1 * time.Second):
	}
}

// readLineWithTimeout reads one '\n'-terminated line from r, failing with
// an error if none arrives within timeout. It exists so dispatch tests
// fail fast and loudly instead of hanging forever if Serve misbehaves.
func readLineWithTimeout(r *bufio.Reader, timeout time.Duration) ([]byte, error) {
	type result struct {
		line []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := r.ReadBytes('\n')
		ch <- result{line: line, err: err}
	}()
	select {
	case res := <-ch:
		return res.line, res.err
	case <-time.After(timeout):
		return nil, errReadTimeout
	}
}
