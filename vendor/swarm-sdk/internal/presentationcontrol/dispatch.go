package presentationcontrol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
)

// MaxEnvelopeSize bounds the largest single NDJSON-framed Envelope line
// Serve will accept from a peer (1 MiB), so a malicious or broken peer that
// never sends a newline cannot force unbounded buffering. See
// readBoundedLine.
const MaxEnvelopeSize = 1 << 20 // 1 MiB

// Presentation-plane verbs this package's default Dispatcher understands.
// Every verb is namespaced "presentation.*" per ADR-003's plane.verb rule
// ("bare or overloaded verbs such as `send`, `status`, `get`... are
// prohibited at a plane boundary").
const (
	// OpPing is a liveness/echo verb: the response Payload is an exact
	// copy of the request Payload.
	OpPing = "presentation.ping"
	// OpCapabilities returns this dispatcher's Capabilities (as supplied
	// to NewDispatcher) as the response Payload.
	OpCapabilities = "presentation.capabilities"
)

// Category strings used in EnvelopeError.Category by the default
// Dispatcher. These are stable and safe for callers to switch on.
const (
	// CategoryMalformedEnvelope means the peer sent bytes that could not
	// be parsed as a well-formed Envelope (invalid JSON, oversized line,
	// etc). Serve closes the connection after sending this response.
	CategoryMalformedEnvelope = "malformed_envelope"
	// CategoryUnknownOperation means the Envelope parsed correctly but
	// named an Op this Dispatcher does not implement. The connection is
	// NOT closed in this case; the loop continues.
	CategoryUnknownOperation = "unknown_operation"
	// CategoryInternalError means this Dispatcher failed to construct a
	// response for an otherwise-valid request (e.g. a marshal failure).
	CategoryInternalError = "internal_error"
)

// errEnvelopeTooLarge is returned internally by readBoundedLine when a
// single line exceeds MaxEnvelopeSize before a newline is found.
var errEnvelopeTooLarge = errors.New("presentationcontrol: envelope exceeds maximum size")

// Dispatcher handles one negotiated presentation-plane connection after
// ReadPreface has confirmed the Preface and the caller has handed off the
// resulting wrapped conn (with the preface bytes already stripped).
// Implementations own their own verb registry; the default implementation
// constructed by NewDispatcher supports a minimal verb set (OpPing,
// OpCapabilities) -- it does not reimplement every existing legacy
// attached-server verb (see INDEX.md's "Scope" section).
type Dispatcher interface {
	// Serve reads Envelopes from conn, dispatches each by Op, and writes
	// an Envelope response for each request, looping until conn is closed
	// (returns nil) or a malformed envelope is received (writes an error
	// Envelope, closes conn, and returns a non-nil error). Serve never
	// panics on malformed input.
	Serve(conn net.Conn) error
}

// dispatcher is the default Dispatcher implementation returned by
// NewDispatcher.
type dispatcher struct {
	caps Capabilities
}

// NewDispatcher constructs the default Dispatcher implementation. caps is
// returned verbatim as the response Payload to an OpCapabilities request.
func NewDispatcher(caps Capabilities) Dispatcher {
	return &dispatcher{caps: caps}
}

// Serve implements Dispatcher. Framing is newline-delimited JSON (NDJSON):
// exactly one JSON-encoded Envelope per line, matching this program's
// existing NDJSON precedent (Phase 05's journal) -- see INDEX.md for the
// rationale over length-prefixing. Serve reads and dispatches Envelopes
// until conn is closed cleanly (returns nil) or a malformed/oversized
// envelope is received, in which case Serve writes a best-effort
// NewErrorResponse, closes conn itself, and returns a non-nil error.
func (d *dispatcher) Serve(conn net.Conn) error {
	reader := bufio.NewReaderSize(conn, 4096)
	for {
		line, rerr := readBoundedLine(reader, MaxEnvelopeSize)
		if errors.Is(rerr, errEnvelopeTooLarge) {
			resp := NewErrorResponse("", CategoryMalformedEnvelope,
				fmt.Sprintf("envelope exceeds maximum size of %d bytes", MaxEnvelopeSize))
			_ = writeEnvelope(conn, resp)
			_ = conn.Close()
			return fmt.Errorf("presentationcontrol: rejected oversized envelope (max %d bytes): %w", MaxEnvelopeSize, rerr)
		}

		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			var env Envelope
			if jsonErr := json.Unmarshal(trimmed, &env); jsonErr != nil {
				resp := NewErrorResponse("", CategoryMalformedEnvelope, "invalid JSON envelope: "+jsonErr.Error())
				_ = writeEnvelope(conn, resp)
				_ = conn.Close()
				return fmt.Errorf("presentationcontrol: malformed envelope: %w", jsonErr)
			}

			respEnv := d.dispatch(env)
			if writeErr := writeEnvelope(conn, respEnv); writeErr != nil {
				return fmt.Errorf("presentationcontrol: write response envelope: %w", writeErr)
			}
		}

		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return nil
			}
			return fmt.Errorf("presentationcontrol: read envelope: %w", rerr)
		}
	}
}

// dispatch routes a single parsed request Envelope to this dispatcher's
// verb implementation and returns the response Envelope. It never panics:
// an unknown Op yields a CategoryUnknownOperation error response rather
// than a runtime failure.
func (d *dispatcher) dispatch(req Envelope) Envelope {
	switch req.Op {
	case OpPing:
		return Envelope{
			SchemaVersion: SchemaVersion,
			Plane:         Plane,
			Op:            OpPing,
			CorrelationID: req.CorrelationID,
			Payload:       req.Payload,
		}
	case OpCapabilities:
		raw, err := json.Marshal(d.caps)
		if err != nil {
			return NewErrorResponse(req.CorrelationID, CategoryInternalError, "failed to marshal capabilities: "+err.Error())
		}
		return Envelope{
			SchemaVersion: SchemaVersion,
			Plane:         Plane,
			Op:            OpCapabilities,
			CorrelationID: req.CorrelationID,
			Payload:       raw,
		}
	default:
		return NewErrorResponse(req.CorrelationID, CategoryUnknownOperation,
			fmt.Sprintf("unknown presentation-plane operation %q", req.Op))
	}
}

// writeEnvelope marshals env and writes it to w as one NDJSON line
// (trailing '\n').
func writeEnvelope(w io.Writer, env Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("presentationcontrol: marshal response envelope: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// readBoundedLine reads one '\n'-terminated line from r, returning the line
// WITHOUT its trailing newline. It bounds total accumulated memory to at
// most max bytes plus at most one internal bufio buffer's worth of slack:
// bufio.Reader.ReadString/ReadBytes do not bound memory on their own (they
// keep accumulating fragments across arbitrarily many buffer refills until
// a delimiter is found), so this function checks the accumulated length
// after every underlying ReadSlice call and bails out with
// errEnvelopeTooLarge the moment it exceeds max, before requesting any
// further data from r. This is the mechanism that keeps a malicious or
// broken peer that never sends '\n' from exhausting memory.
//
// On a clean end-of-stream with no partial line pending, it returns
// (nil, io.EOF). On end-of-stream with an unterminated trailing line (a
// peer that writes an envelope then closes without a final '\n'), it
// returns the partial line data alongside io.EOF so the caller can still
// process that last envelope.
func readBoundedLine(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > max {
			return buf, errEnvelopeTooLarge
		}
		if err == nil {
			// Found the delimiter; trim it before returning.
			return buf[:len(buf)-1], nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			// The line continues beyond the current internal buffer
			// contents; loop to pull more without losing what's been
			// accumulated so far. The length check above still applies on
			// every iteration, bounding total growth to at most one
			// extra internal-buffer's worth past max.
			continue
		}
		// EOF or a genuine I/O error: return whatever was accumulated
		// alongside the error so the caller can decide whether a trailing
		// unterminated line should still be processed.
		return buf, err
	}
}
