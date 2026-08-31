package serve

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// Bounds and stable error data shared by the JSON-RPC HTTP transport. Tests in
// this package may shrink them to exercise cap and timeout paths
// deterministically without allocating megabytes of fixture data or waiting
// for production timeouts.
var (
	// maxHTTPBodyBytes caps a single JSON-RPC HTTP request body. It is
	// enforced by http.MaxBytesReader *before* any body byte is read into
	// memory, so an attacker cannot force an allocation proportional to a
	// declared Content-Length, a chunked body, or any other unbounded
	// stream — the reader itself refuses to hand back more than this many
	// bytes.
	maxHTTPBodyBytes int64 = 4 << 20 // 4 MiB

	// httpRequestTimeout bounds how long a single JSON-RPC HTTP request may
	// run end-to-end (bounded body read + Dispatch) from the moment
	// authentication succeeds. The derived, cancellable context is threaded
	// into Dispatch so a slow or stuck handler observes cancellation
	// instead of pinning the request goroutine indefinitely; the same
	// deadline also bounds the read side of the connection via
	// http.ResponseController so a slow client cannot stall the handler
	// before Dispatch is even reached. Writes use their own fresh deadline.
	httpRequestTimeout = 30 * time.Second

	// httpResponseWriteTimeout is a fresh bound installed immediately before
	// each response write. It is intentionally separate from the request
	// deadline: a response produced at the end of the request budget still
	// gets a bounded opportunity to reach the client.
	httpResponseWriteTimeout = 10 * time.Second
)

// requestTooLargeData is the stable, machine-checkable Error.Data value
// returned when a request body exceeds maxHTTPBodyBytes. It never changes
// across releases so callers can branch on it instead of parsing free-form
// error text.
const requestTooLargeData = "request_too_large"

// requestTimeoutData is the stable Error.Data category returned when the
// request deadline wins before dispatch produces a result.
const requestTimeoutData = "request_timeout"

// dispatchResult carries a fully bounded-encoded response payload from a
// dispatch child (which owns global limiter capacity — see dispatchLimiter
// in mux.go) back to its request/connection supervisor. The child performs
// both arbitrary method dispatch and bounded encoding before publishing
// here, so the supervisor never touches an un-encoded arbitrary result and
// the child's limiter slot is held until encoding genuinely completes.
type dispatchResult struct {
	payload []byte
}

// HTTPHandler returns an http.Handler that speaks JSON-RPC 2.0 over POST
// requests.  Single-request mode only — batch requests are not supported in
// this first cut and would surface as a parse error.
//
// The handler is request/response: streaming methods like sendMessage emit
// their intermediate updates via Subscribe (which the caller can fan out
// over a separate /sse endpoint).  Within a single HTTP call only the final
// result is returned.
func (m *Mux) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			if !setResponseWriteDeadline(w) {
				return
			}
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}

		ctx, ok := m.applyAuth(w, r)
		if !ok {
			return
		}

		// Bound total request execution (body read + dispatch) so a slow
		// client or a stuck handler cannot hold the connection/goroutine
		// open indefinitely; Dispatch (and any cooperating handler) observes
		// ctx.Done() for cancellation.
		ctx, cancel := context.WithTimeout(ctx, httpRequestTimeout)
		defer cancel()

		// Bound the read side of the underlying connection to the same
		// deadline. Without this, a client that trickles the request body in
		// one byte at a time could stall io.ReadAll well past
		// httpRequestTimeout, since io.ReadAll itself has no notion of ctx.
		// The write side is deliberately left unbound here: coupling it to
		// the same absolute deadline would race the handler's own response
		// write (issued right around when the deadline fires) against that
		// same deadline, intermittently failing a perfectly normal,
		// on-time response.
		if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
			_ = http.NewResponseController(w).SetReadDeadline(deadline)
		}

		// Cap the body before any byte is read. MaxBytesReader closes the
		// underlying body and returns *http.MaxBytesError once the cap is
		// exceeded, so io.ReadAll cannot accumulate an unbounded buffer for
		// an oversized or endlessly streamed request.
		r.Body = http.MaxBytesReader(w, r.Body, maxHTTPBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeJSON(w, makeErrorResponse(nil, NewError(ErrInvalidRequest, "request body exceeds maximum size", requestTooLargeData)))
				return
			}
			writeJSON(w, makeErrorResponse(nil, NewError(ErrParseError, "read body: "+err.Error(), nil)))
			return
		}

		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, makeErrorResponse(nil, NewError(ErrParseError, err.Error(), nil)))
			return
		}
		if req.Jsonrpc != "" && req.Jsonrpc != "2.0" {
			writeJSON(w, makeErrorResponse(req.ID, NewError(ErrInvalidRequest, "jsonrpc must be 2.0", req.Jsonrpc)))
			return
		}

		// The child itself acquires and owns shared process-wide capacity.
		// It performs both arbitrary method code and arbitrary result
		// marshaling, then publishes immutable bytes through a buffered
		// channel. The request supervisor remains the sole ResponseWriter
		// owner and can return even if either operation ignores cancellation.
		resultCh := make(chan dispatchResult, 1)
		go func() {
			limiter := m.dispatchLimiter()
			if !limiter.acquire(ctx) {
				return
			}
			defer limiter.release()

			result, dispatchErr := m.Dispatch(ctx, req.Method, req.Params)
			if req.IsNotification() {
				resultCh <- dispatchResult{}
				return
			}

			var response Response
			if dispatchErr != nil {
				response = makeErrorResponse(req.ID, dispatchErr)
			} else {
				response = makeResponse(req.ID, result)
			}
			// Bounded encoding happens here, inside the limiter-held child,
			// so an expensive-to-encode result (deep/huge/many-node) is
			// bounded by the same global dispatch capacity as method code
			// itself — the child does not release its slot until the bytes
			// it publishes are final. See bounded_json.go: this never calls
			// json.Marshal, a reachable Marshaler/TextMarshaler, or Error()
			// on an arbitrary value; oversize/unsupported results fall back
			// to the fixed response_too_large/response_encoding_error
			// envelopes instead. httpBoundedResponseEnvelope reserves one
			// byte of the shared cap for the trailing newline it appends
			// below (preserving json.Encoder's established newline
			// convention), so the published payload — newline included — is
			// always at most maxBoundedJSONBytes.
			resultCh <- dispatchResult{payload: httpBoundedResponseEnvelope(ctx, response)}
		}()

		var outcome dispatchResult
		select {
		case outcome = <-resultCh:
		case <-ctx.Done():
			// The method child may ignore cancellation forever. It owns no
			// ResponseWriter and its result channel is buffered, so this
			// request can return now and any late result is discarded.
			if req.IsNotification() {
				writeStatus(w, http.StatusNoContent)
				return
			}
			writeJSON(w, makeErrorResponse(req.ID, NewError(ErrInternalError, "request timed out", requestTimeoutData)))
			return
		}

		// Notifications: no response.
		if req.IsNotification() {
			writeStatus(w, http.StatusNoContent)
			return
		}

		writeJSONBytes(w, outcome.payload)
	})
}

// writeJSON bounded-encodes resp (see bounded_json.go) and writes it as JSON
// with a 200 status. Used only for the pre-dispatch error paths (parse
// error, bad jsonrpc version) where resp is always a small, fixed-shape
// Response built from a trusted local error, never arbitrary method output.
func writeJSON(w http.ResponseWriter, resp Response) {
	writeJSONBytes(w, httpBoundedResponseEnvelope(context.Background(), resp))
}

// httpBoundedResponseEnvelope bounded-encodes resp for the HTTP JSON-RPC
// transport specifically. It is the "internal explicit-limit response path
// for HTTP" from CONTRACT.md: it reserves exactly one byte of the shared
// maxBoundedJSONBytes structural cap for the trailing '\n' this transport
// always appends (matching the previously-established json.Encoder
// convention), so every normal or fallback HTTP payload — the newline
// included — is guaranteed to be at most maxBoundedJSONBytes end to end.
// WebSocket/SSE/stream paths append no trailing newline and therefore keep
// the full cap via boundedEncodeResponse/encodeBoundedJSON directly (see
// bounded_json.go and websocket.go/sse.go/stream.go).
func httpBoundedResponseEnvelope(ctx context.Context, resp Response) []byte {
	limit := maxBoundedJSONBytes - 1
	if limit < 0 {
		limit = 0
	}
	payload := boundedEncodeResponseCapped(ctx, resp, limit)
	return append(payload, '\n')
}

func writeJSONBytes(w http.ResponseWriter, payload []byte) {
	if !setResponseWriteDeadline(w) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}

func writeStatus(w http.ResponseWriter, status int) {
	if !setResponseWriteDeadline(w) {
		return
	}
	w.WriteHeader(status)
}

// setResponseWriteDeadline installs a fresh httpResponseWriteTimeout write
// deadline on w and reports whether it succeeded. It fails closed: any
// non-nil error — including the documented http.ErrNotSupported sentinel
// for a writer that genuinely cannot support a deadline — is treated as a
// refusal, exactly like WithResponseWriteBounds (response_write.go). Every
// call site in this package already checks this return value and returns
// immediately without invoking any write method when it is false, so no
// writer this package could not bound ever receives a Write, WriteHeader,
// Flush, http.Error, or upgrader call.
func setResponseWriteDeadline(w http.ResponseWriter) bool {
	return http.NewResponseController(w).SetWriteDeadline(time.Now().Add(httpResponseWriteTimeout)) == nil
}

// clearResponseWriteDeadline removes any previously installed write
// deadline by installing the zero time (the documented "no deadline" value
// for both net.Conn and http.ResponseController). Used by SSEHandler to go
// idle between events: without this, a long-lived-but-healthy stream with
// infrequent events would eventually trip the per-write deadline installed
// for its *last* write even though nothing was in flight. Best-effort like
// setResponseWriteDeadline — an unsupported writer has nothing to clear.
func clearResponseWriteDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
}
