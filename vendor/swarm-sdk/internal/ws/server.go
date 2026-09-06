package ws

import (
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// ServerUpgrader wraps websocket.Upgrader with a simplified interface
// for upgrading HTTP connections to WebSocket connections.
type ServerUpgrader struct {
	upgrader websocket.Upgrader
}

// NewServerUpgrader creates a new ServerUpgrader with the provided options.
// Default configuration uses 4096 byte buffers and allows all origins.
//
// Example:
//
//	upgrader := ws.NewServerUpgrader(
//	    ws.WithCheckOrigin(func(r *http.Request) bool {
//	        return r.Header.Get("Origin") == "https://example.com"
//	    }),
//	    ws.WithBufferSizes(8192, 8192),
//	)
func NewServerUpgrader(options ...ServerOption) *ServerUpgrader {
	s := &ServerUpgrader{
		upgrader: websocket.Upgrader{
			ReadBufferSize:  DefaultReadBufferSize,
			WriteBufferSize: DefaultWriteBufferSize,
			CheckOrigin:     func(_ *http.Request) bool { return true },
		},
	}

	for _, opt := range options {
		opt(s)
	}

	return s
}

// Upgrade upgrades the HTTP server connection to the WebSocket protocol.
// It wraps the raw websocket.Conn in a thread-safe Conn wrapper.
//
// The responseHeader is included in the response to the client's upgrade request.
// It may be used to set cookies, etc. Setting responseHeader is optional.
func (s *ServerUpgrader) Upgrade(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (*Conn, error) {
	raw, err := s.upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		return nil, err
	}
	return &Conn{conn: raw}, nil
}

// Conn wraps a gorilla/websocket.Conn with thread-safe write operations.
// All write methods are protected by a mutex to prevent interleaving frames
// when multiple goroutines send concurrently.
type Conn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

// WriteJSON writes a JSON-encoded message to the connection.
// This method is thread-safe and can be called concurrently from
// multiple goroutines.
func (c *Conn) WriteJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(v)
}

// ReadJSON reads a JSON-encoded message from the connection and stores
// it in the value pointed to by v.
func (c *Conn) ReadJSON(v any) error {
	return c.conn.ReadJSON(v)
}

// WriteMessage writes a message with the given type and payload.
// This method is thread-safe.
func (c *Conn) WriteMessage(messageType int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(messageType, data)
}

// ReadMessage reads a message from the connection.
// It returns the message type and payload.
func (c *Conn) ReadMessage() (messageType int, p []byte, err error) {
	return c.conn.ReadMessage()
}

// Close closes the WebSocket connection.
func (c *Conn) Close() error {
	return c.conn.Close()
}

// Underlying returns the underlying gorilla/websocket.Conn.
// This is exposed for advanced use cases that need direct access
// to the raw connection. Most users should use the wrapper methods.
func (c *Conn) Underlying() *websocket.Conn {
	return c.conn
}
