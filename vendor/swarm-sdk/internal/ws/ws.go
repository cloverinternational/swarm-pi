// Package ws provides standardized WebSocket utilities for the Swarm SDK.
//
// This is an INTERNAL SDK package - external consumers should use the
// official swarm-sdk/client package for WebSocket connections.
//
// The ws package wraps gorilla/websocket to provide:
//   - Thread-safe connection wrapper
//   - Standardized server upgrader with options
//   - Consistent JSON read/write methods
//
// Usage:
//
//	upgrader := ws.NewServerUpgrader(
//	    ws.WithCheckOrigin(func(r *http.Request) bool { return true }),
//	    ws.WithBufferSizes(4096, 4096),
//	)
//	conn, err := upgrader.Upgrade(w, r)
//	if err != nil {
//	    return err
//	}
//	defer conn.Close()
//
//	// Read JSON messages
//	var msg Message
//	if err := conn.ReadJSON(&msg); err != nil {
//	    return err
//	}
//
//	// Write JSON responses
//	if err := conn.WriteJSON(response); err != nil {
//	    return err
//	}
package ws

import (
	"github.com/gorilla/websocket"
)

// ReadBufferSize and WriteBufferSize define default buffer sizes for connections.
const (
	DefaultReadBufferSize  = 4096
	DefaultWriteBufferSize = 4096
)

// MessageType represents the type of WebSocket message.
type MessageType int

const (
	// TextMessage denotes a text data message.
	TextMessage = websocket.TextMessage
	// BinaryMessage denotes a binary data message.
	BinaryMessage = websocket.BinaryMessage
	// CloseMessage denotes a close control message.
	CloseMessage = websocket.CloseMessage
	// PingMessage denotes a ping control message.
	PingMessage = websocket.PingMessage
	// PongMessage denotes a pong control message.
	PongMessage = websocket.PongMessage
)
