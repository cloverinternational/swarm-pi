package ws

import (
	"net/http"
)

// ServerOption configures a ServerUpgrader.
type ServerOption func(*ServerUpgrader)

// WithCheckOrigin sets the CheckOrigin function for the upgrader.
// The CheckOrigin function should return true if the request Origin header
// is acceptable. If CheckOrigin is nil, the upgrader uses a safe default:
// it rejects requests with Origin headers that don't match the Host header.
//
// For development, you may want to allow all origins:
//
//	ws.WithCheckOrigin(func(_ *http.Request) bool { return true })
//
// For production, validate the origin:
//
//	ws.WithCheckOrigin(func(r *http.Request) bool {
//	    return r.Header.Get("Origin") == "https://trusted-domain.com"
//	})
func WithCheckOrigin(fn func(r *http.Request) bool) ServerOption {
	return func(s *ServerUpgrader) {
		s.upgrader.CheckOrigin = fn
	}
}

// WithBufferSizes sets the read and write buffer sizes for the upgrader.
// Larger buffers can improve performance for high-throughput connections
// at the cost of increased memory usage per connection.
//
// Default sizes are 4096 bytes for both read and write buffers.
//
// Example for high-throughput scenarios:
//
//	ws.WithBufferSizes(8192, 8192)
func WithBufferSizes(read, write int) ServerOption {
	return func(s *ServerUpgrader) {
		s.upgrader.ReadBufferSize = read
		s.upgrader.WriteBufferSize = write
	}
}

// WithSubprotocols sets the supported WebSocket subprotocols.
// The server negotiates a subprotocol by finding the first match in
// this list with a protocol requested by the client.
//
// Example:
//
//	ws.WithSubprotocols("jsonrpc", "jsonrpc-2.0")
func WithSubprotocols(protocols ...string) ServerOption {
	return func(s *ServerUpgrader) {
		s.upgrader.Subprotocols = protocols
	}
}

// WithCompression enables or disables compression for the upgrader.
// When enabled, the server will negotiate compression with clients
// that support it (permessage-deflate extension).
//
// Example:
//
//	ws.WithCompression(true)
func WithCompression(enabled bool) ServerOption {
	return func(s *ServerUpgrader) {
		s.upgrader.EnableCompression = enabled
	}
}

// WithErrorLog sets the error log function for the upgrader.
// The error log function is called when the upgrader encounters an error
// during the upgrade process. If nil, errors are logged to the standard logger.
//
// The function receives the HTTP response writer, request, HTTP status code,
// and reason for the failure.
func WithErrorLog(fn func(w http.ResponseWriter, r *http.Request, status int, reason error)) ServerOption {
	return func(s *ServerUpgrader) {
		s.upgrader.Error = fn
	}
}
