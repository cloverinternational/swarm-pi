package sdkerr

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"time"
)

// Error is the canonical SDK error model with causal lineage metadata.
type Error struct {
	ErrorID       string         // Stable ID for this error instance.
	Code          string         // Stable dot-notation error code.
	Operation     string         // Operation where error occurred.
	Component     string         // Component that produced the error.
	TraceID       string         // Trace ID for distributed/local tracing.
	SpanID        string         // Span ID where this error occurred.
	ParentErrorID string         // Parent error ID for causal trees.
	OccurredAt    time.Time      // When the error occurred (UTC).
	Attributes    map[string]any // Arbitrary diagnostic attributes.
	Cause         error          // Underlying cause.

	// Behavioural fields set at construction time.
	Category   Category      // Error category for retry/handling decisions.
	Message    string        // Human-readable message.
	Retryable  bool          // True if retry is allowed.
	RetryAfter time.Duration // Suggested retry delay.

	// Optional context identifiers.
	ConversationID string
	AgentID        string

	// Source location captured automatically at construction time.
	File string // Source file where the error was created (e.g., "agent/agent_execute.go").
	Line int    // Line number in the source file.
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := e.Message
	if msg == "" {
		switch {
		case e.Cause != nil:
			msg = e.Cause.Error()
		case e.Code != "":
			msg = e.Code
		default:
			msg = "sdk error"
		}
	}
	if e.ErrorID == "" {
		return msg
	}
	return fmt.Sprintf("%s (error_id=%s)", msg, e.ErrorID)
}

// Unwrap exposes the direct cause for errors.Is/errors.As traversal.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is supports errors.Is matching by ErrorID (preferred) or Code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok || e == nil {
		return false
	}
	if t.ErrorID != "" {
		return e.ErrorID == t.ErrorID
	}
	if t.Code != "" {
		return e.Code == t.Code
	}
	return false
}

// Clone returns a deep copy suitable for additional wrapping/annotation.
func (e *Error) Clone() *Error {
	if e == nil {
		return nil
	}
	cloned := *e
	cloned.Attributes = copyMap(e.Attributes)
	return &cloned
}

func (e *Error) ensureAttributes() map[string]any {
	if e.Attributes == nil {
		e.Attributes = make(map[string]any)
	}
	return e.Attributes
}

func copyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	maps.Copy(out, input)
	return out
}

func newErrorID() string {
	var buf [10]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("err_%d", time.Now().UnixNano())
	}
	return "err_" + hex.EncodeToString(buf[:])
}
