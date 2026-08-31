package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// debugLogFile is the path for raw WebSocket message logging (debug only)
const debugLogFile = "/tmp/voice_ws_debug.log"

// voiceDebugEnabled controls raw WebSocket message logging to /tmp/voice_ws_debug.log.
// Always on for now to diagnose transcript issues; disable once transcription works.
var voiceDebugEnabled = true

// WSConnection wraps gorilla websocket with voice-specific handling
type WSConnection struct {
	conn   *websocket.Conn
	config *Config
	mu     sync.Mutex
	done   chan struct{}
}

// NewWSConnection creates a new WebSocket connection wrapper
func NewWSConnection(cfg *Config) *WSConnection {
	return &WSConnection{
		config: cfg,
	}
}

// Connect establishes WebSocket connection with voice stream parameters
func (w *WSConnection) Connect(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Build URL with query parameters
	u, err := url.Parse(w.config.BaseURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Add query parameters matching Claude Code's implementation
	q := u.Query()
	q.Set("encoding", string(w.config.Encoding))
	q.Set("sample_rate", fmt.Sprintf("%d", w.config.SampleRate))
	q.Set("channels", fmt.Sprintf("%d", w.config.Channels))
	q.Set("endpointing_ms", fmt.Sprintf("%d", w.config.EndpointingMs))
	q.Set("utterance_end_ms", fmt.Sprintf("%d", w.config.UtteranceMs))
	q.Set("language", w.config.Language)

	// Add optional parameters
	if len(w.config.Keyterms) > 0 {
		keytermsJSON, err := json.Marshal(w.config.Keyterms)
		if err != nil {
			return fmt.Errorf("failed to marshal keyterms: %w", err)
		}
		q.Set("keyterms", string(keytermsJSON))
	}

	if w.config.Provider != "" {
		q.Set("stt_provider", w.config.Provider)
	}

	if w.config.UseConversation {
		q.Set("use_conversation_engine", "true")
	}

	u.RawQuery = q.Encode()

	// Setup headers
	header := http.Header{}
	if w.config.AuthToken != "" {
		header.Set("Authorization", "Bearer "+w.config.AuthToken)
	}
	if w.config.UserAgent != "" {
		header.Set("User-Agent", w.config.UserAgent)
	}
	if w.config.AppID != "" {
		header.Set("x-app", w.config.AppID)
	}

	// Dial with timeout
	dialer := websocket.Dialer{
		HandshakeTimeout: w.config.ConnectTimeout,
	}

	conn, _, err := dialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return fmt.Errorf("websocket dial failed: %w", err)
	}

	w.conn = conn
	w.done = make(chan struct{})

	return nil
}

// StartKeepAlive starts the keepalive goroutine
func (w *WSConnection) StartKeepAlive() {
	go w.keepAlive()
}

// keepAlive sends KeepAlive messages periodically to maintain connection
func (w *WSConnection) keepAlive() {
	ticker := time.NewTicker(w.config.KeepAliveInt)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.mu.Lock()
			if w.conn == nil {
				w.mu.Unlock()
				return
			}
			err := w.conn.WriteJSON(struct {
				Type string `json:"type"`
			}{Type: "KeepAlive"})
			w.mu.Unlock()

			if err != nil {
				return
			}
		case <-w.done:
			return
		}
	}
}

// SendAudio sends an audio chunk over the WebSocket
func (w *WSConnection) SendAudio(chunk []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conn == nil {
		return ErrWebSocketClosed
	}

	return w.conn.WriteMessage(websocket.BinaryMessage, chunk)
}

// CloseStream sends CloseStream message to finalize transcription
func (w *WSConnection) CloseStream() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conn == nil {
		return ErrWebSocketClosed
	}

	return w.conn.WriteJSON(struct {
		Type string `json:"type"`
	}{Type: "CloseStream"})
}

// ReadMessage reads the next WebSocket message and parses it.
// Returns (nil, nil) for unrecognized but non-fatal message types so the
// caller can simply continue the receive loop without treating them as errors.
func (w *WSConnection) ReadMessage() (Message, error) {
	if w.conn == nil {
		return nil, ErrWebSocketClosed
	}

	messageType, data, err := w.conn.ReadMessage()
	if err != nil {
		// A normal WebSocket close from the server is not an error — it signals
		// the stream is done. Return CloseStreamMsg so the receive loop exits cleanly.
		if websocket.IsCloseError(err,
			websocket.CloseNormalClosure,
			websocket.CloseGoingAway,
			websocket.CloseNoStatusReceived,
		) {
			return CloseStreamMsg{Type: "CloseStream"}, nil
		}
		return nil, fmt.Errorf("read error: %w", err)
	}

	if voiceDebugEnabled {
		if f, err := os.OpenFile(debugLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			fmt.Fprintf(f, "RAW[type=%d]: %s\n", messageType, string(data))
			f.Close()
		}
	}

	if messageType == websocket.TextMessage {
		return parseTextMessage(data)
	}

	// Binary frames are audio data echoes or other non-transcript frames; ignore.
	return nil, nil
}

// parseTextMessage parses a text message into the appropriate type
func parseTextMessage(data []byte) (Message, error) {
	// First, determine the message type
	var base struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, fmt.Errorf("failed to parse message type: %w", err)
	}

	switch base.Type {
	case "TranscriptText":
		var msg TranscriptTextMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("failed to parse TranscriptText: %w", err)
		}
		msg.Type = "TranscriptText"
		return msg, nil

	case "TranscriptEndpoint":
		var msg TranscriptEndpointMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("failed to parse TranscriptEndpoint: %w", err)
		}
		msg.Type = "TranscriptEndpoint"
		return msg, nil

	case "TranscriptError":
		var msg TranscriptErrorMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("failed to parse TranscriptError: %w", err)
		}
		msg.Type = "TranscriptError"
		return msg, nil

	case "error":
		var msg ServerErrorMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("failed to parse error: %w", err)
		}
		msg.Type = "error"
		return msg, nil

	case "KeepAlive":
		return KeepAliveMsg{Type: "KeepAlive"}, nil

	case "CloseStream":
		return CloseStreamMsg{Type: "CloseStream"}, nil

	default:
		// Unknown types are not fatal — log and return nil so the receive loop continues.
		if voiceDebugEnabled {
			if f, err := os.OpenFile(debugLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
				fmt.Fprintf(f, "UNKNOWN_TYPE: %s\n", base.Type)
				f.Close()
			}
		}
		return nil, nil
	}
}

// Close closes the WebSocket connection
func (w *WSConnection) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.done != nil {
		close(w.done)
		w.done = nil
	}

	if w.conn != nil {
		// Send close message
		err := w.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		if err != nil {
			// Ignore close message errors
		}

		err = w.conn.Close()
		w.conn = nil
		return err
	}

	return nil
}

// IsConnected returns whether the connection is active
func (w *WSConnection) IsConnected() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn != nil
}

// SetReadTimeout sets a read deadline on the connection
func (w *WSConnection) SetReadTimeout(d time.Duration) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conn == nil {
		return ErrWebSocketClosed
	}

	return w.conn.SetReadDeadline(time.Now().Add(d))
}

// SetWriteTimeout sets a write deadline on the connection
func (w *WSConnection) SetWriteTimeout(d time.Duration) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conn == nil {
		return ErrWebSocketClosed
	}

	return w.conn.SetWriteDeadline(time.Now().Add(d))
}
