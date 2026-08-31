package sdkerr

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"runtime"
	"time"
)

// Option configures an SDK error.
type Option func(*Error)

// WithRetryAfter sets retry delay for transient failures.
func WithRetryAfter(d time.Duration) Option {
	return func(e *Error) {
		e.RetryAfter = d
	}
}

// WithTraceID sets the trace ID.
func WithTraceID(traceID string) Option {
	return func(e *Error) {
		e.TraceID = traceID
	}
}

// WithSpanID sets the span ID.
func WithSpanID(spanID string) Option {
	return func(e *Error) {
		e.SpanID = spanID
	}
}

// WithTraceFromContext copies trace/span IDs from context.
func WithTraceFromContext(ctx context.Context) Option {
	traceID, spanID := TraceFromContext(ctx)
	return func(e *Error) {
		if traceID != "" {
			e.TraceID = traceID
		}
		if spanID != "" {
			e.SpanID = spanID
		}
	}
}

// WithConversationID sets conversation identifier.
func WithConversationID(conversationID string) Option {
	return func(e *Error) {
		e.ConversationID = conversationID
	}
}

// WithAgentID sets agent identifier.
func WithAgentID(agentID string) Option {
	return func(e *Error) {
		e.AgentID = agentID
	}
}

// WithOperation sets operation context.
func WithOperation(operation string) Option {
	return func(e *Error) {
		e.Operation = operation
	}
}

// WithComponent sets component context.
func WithComponent(component string) Option {
	return func(e *Error) {
		e.Component = component
	}
}

// WithParentErrorID explicitly sets a parent error link.
func WithParentErrorID(parentErrorID string) Option {
	return func(e *Error) {
		e.ParentErrorID = parentErrorID
	}
}

// WithAttr adds a diagnostic attribute.
func WithAttr(key string, value any) Option {
	return func(e *Error) {
		if key == "" {
			return
		}
		e.ensureAttributes()[key] = value
	}
}

// WithContext is a backwards-compatible alias for WithAttr.
func WithContext(key string, value any) Option {
	return WithAttr(key, value)
}

// WithAttributes adds multiple diagnostic attributes.
func WithAttributes(attrs map[string]any) Option {
	return func(e *Error) {
		if len(attrs) == 0 {
			return
		}
		out := e.ensureAttributes()
		maps.Copy(out, attrs)
	}
}

// Transient creates a retryable transient error with a message.
// To wrap an existing error, use Wrap instead.
func Transient(code string, msg string, opts ...Option) *Error {
	return newCausalError(CategoryTransient, code, msg, nil, opts...)
}

// Permanent creates a non-retryable permanent error.
func Permanent(code string, message string, opts ...Option) *Error {
	return newCausalError(CategoryPermanent, code, message, nil, opts...)
}

// Steering creates an error requiring steering policy to decide.
func Steering(code string, message string, opts ...Option) *Error {
	return newCausalError(CategorySteering, code, message, nil, opts...)
}

// Critical creates a critical error requiring immediate escalation.
func Critical(code string, message string, opts ...Option) *Error {
	return newCausalError(CategoryCritical, code, message, nil, opts...)
}

// Wrap creates a new causal error around any source error, inheriting its category.
func Wrap(err error, code string, opts ...Option) *Error {
	if err == nil {
		return nil
	}
	return newCausalError(GetCategory(err), code, "", err, opts...)
}

// Capture converts an arbitrary error into canonical SDK error form.
// Existing SDK errors are cloned and augmented with the provided options.
func Capture(err error, opts ...Option) *Error {
	if err == nil {
		return nil
	}
	if existing := asError(err); existing != nil {
		cloned := existing.Clone()
		for _, opt := range opts {
			opt(cloned)
		}
		return cloned
	}
	return newCausalError(CategoryPermanent, "sdk.captured_error", "", err, opts...)
}

func newCausalError(category Category, code, message string, cause error, opts ...Option) *Error {
	if code == "" {
		if parent := asError(cause); parent != nil {
			code = parent.Code
		}
	}
	if code == "" {
		code = "sdk.error"
	}
	if message == "" {
		if cause != nil {
			message = cause.Error()
		} else {
			message = code
		}
	}

	e := &Error{
		ErrorID:    newErrorID(),
		Code:       code,
		Category:   category,
		Message:    message,
		Retryable:  category == CategoryTransient,
		OccurredAt: time.Now().UTC(),
		Attributes: make(map[string]any),
	}

	// Capture source location: skip newCausalError + the public constructor
	// (Wrap/Permanent/Transient/etc.), so the recorded site is the caller
	// of the constructor — i.e. the SDK code that created the error.
	if _, file, line, ok := runtime.Caller(2); ok {
		e.File = file
		e.Line = line
	}

	if cause != nil {
		e.Cause = cause
		if parent := asError(cause); parent != nil {
			e.ParentErrorID = parent.ErrorID
			if e.TraceID == "" {
				e.TraceID = parent.TraceID
			}
			if e.SpanID == "" {
				e.SpanID = parent.SpanID
			}
			if e.RetryAfter == 0 {
				e.RetryAfter = parent.RetryAfter
			}
			if e.ConversationID == "" {
				e.ConversationID = parent.ConversationID
			}
			if e.AgentID == "" {
				e.AgentID = parent.AgentID
			}
		}
	}

	for _, opt := range opts {
		opt(e)
	}
	return e
}

func asError(err error) *Error {
	if err == nil {
		return nil
	}
	var sdkErr *Error
	if errors.As(err, &sdkErr) {
		return sdkErr
	}
	return nil
}

// FormatCode builds stable dot-notation error codes.
func FormatCode(component, operation, reason string) string {
	if component == "" && operation == "" {
		return reason
	}
	if reason == "" {
		reason = "failed"
	}
	switch {
	case component != "" && operation != "":
		return fmt.Sprintf("%s.%s.%s", component, operation, reason)
	case component != "":
		return fmt.Sprintf("%s.%s", component, reason)
	default:
		return fmt.Sprintf("%s.%s", operation, reason)
	}
}
