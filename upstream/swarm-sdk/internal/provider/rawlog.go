// Package provider — RawLogger is a diagnostic sink for raw HTTP bodies.
// Providers call it when a logger is attached to the context via WithRawLogger;
// callers (e.g. compaction) implement it to capture full wire-level detail.
package provider

import "context"

// RawLogger receives raw HTTP request/response bodies from provider calls.
// All methods are no-ops when the receiver is nil.
type RawLogger interface {
	// LogHTTPRequest is called just before the provider sends an HTTP request.
	LogHTTPRequest(provider, method string, body []byte)
	// LogHTTPResponse is called when a 2xx response is received.
	LogHTTPResponse(provider, method string, statusCode int, body []byte)
	// LogHTTPError is called when a non-2xx status or network error occurs.
	LogHTTPError(provider, method string, statusCode int, body []byte, err error)
	// LogParseError is called when the response body cannot be decoded.
	LogParseError(provider string, body []byte, err error)
}

type rawLogKey struct{}

// WithRawLogger attaches a RawLogger to the context.
func WithRawLogger(ctx context.Context, l RawLogger) context.Context {
	return context.WithValue(ctx, rawLogKey{}, l)
}

// RawLogFromCtx returns the RawLogger attached to ctx, or nil.
func RawLogFromCtx(ctx context.Context) RawLogger {
	l, _ := ctx.Value(rawLogKey{}).(RawLogger)
	return l
}
