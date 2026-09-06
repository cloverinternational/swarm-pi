package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// HTTPStreamTransport implements HTTP Streamable MCP communication
type HTTPStreamTransport struct {
	config     *TransportConfig
	httpClient *http.Client
	sessionID  string

	// SSE stream management
	sseConn     *http.Response
	sseScanner  *bufio.Scanner
	lastEventID string

	// Reconnection strategy
	reconnect *ReconnectStrategy

	// Thread safety
	mu        sync.RWMutex
	connected bool

	// Observability
	logger observability.Logger
	tracer observability.Tracer
}

// ReconnectStrategy handles connection resilience with exponential backoff
type ReconnectStrategy struct {
	baseDelay   time.Duration
	maxDelay    time.Duration
	maxAttempts int
	jitterFunc  func(time.Duration) time.Duration
	attempt     int
}

// NewHTTPStreamTransport creates a new HTTP Streamable transport
func NewHTTPStreamTransport(
	config *TransportConfig,
	logger observability.Logger,
	tracer observability.Tracer,
) *HTTPStreamTransport {
	return &HTTPStreamTransport{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
		reconnect: &ReconnectStrategy{
			baseDelay:   100 * time.Millisecond,
			maxDelay:    30 * time.Second,
			maxAttempts: 10,
			jitterFunc:  defaultJitter,
		},
		logger: logger,
		tracer: tracer,
	}
}

func (t *HTTPStreamTransport) Connect(ctx context.Context) error {
	ctx, span := t.tracer.StartSpan(ctx, "mcp.http.connect")
	defer span.End()

	t.mu.Lock()
	if t.connected {
		t.mu.Unlock()
		return fmt.Errorf("already connected")
	}
	t.mu.Unlock()

	// Send initialize request
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      "init",
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities": map[string]any{
				"roots": map[string]any{
					"listChanged": true,
				},
				"sampling": map[string]any{},
			},
			"clientInfo": map[string]any{
				"name":    "swarmos-sdk",
				"version": "1.0.0",
			},
		},
	}

	resp, err := t.sendPOST(ctx, req)
	if err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}
	defer resp.Body.Close()

	// Extract session ID from headers
	if sessionID := resp.Header.Get("Mcp-Session-Id"); sessionID != "" {
		t.mu.Lock()
		t.sessionID = sessionID
		t.mu.Unlock()
		t.logger.Info(ctx, "MCP session established",
			observability.F("session_id", sessionID))
	}

	// Parse initialize response
	var initResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&initResp); err != nil {
		return fmt.Errorf("failed to decode init response: %w", err)
	}

	if initResp.Error != nil {
		return fmt.Errorf("initialization error: %s", initResp.Error.Message)
	}

	// Send initialized notification
	notif := &JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}

	notifResp, err := t.sendPOST(ctx, notif)
	if err != nil {
		return fmt.Errorf("failed to send initialized notification: %w", err)
	}
	notifResp.Body.Close()

	t.mu.Lock()
	t.connected = true
	sessionID := t.sessionID
	t.mu.Unlock()

	t.logger.Info(ctx, "HTTP Streamable transport connected",
		observability.F("url", t.config.URL),
		observability.F("session_id", sessionID))

	return nil
}

func (t *HTTPStreamTransport) Send(ctx context.Context, message any) error {
	ctx, span := t.tracer.StartSpan(ctx, "mcp.http.send")
	defer span.End()

	t.mu.RLock()
	if !t.connected {
		t.mu.RUnlock()
		return fmt.Errorf("not connected")
	}
	t.mu.RUnlock()

	resp, err := t.sendPOST(ctx, message)
	if err != nil {
		return err
	}

	// Handle SSE stream response
	if resp.Header.Get("Content-Type") == "text/event-stream" {
		t.mu.Lock()
		t.sseConn = resp
		t.sseScanner = bufio.NewScanner(resp.Body)
		// Increase buffer for large events
		buf := make([]byte, 0, 64*1024)
		t.sseScanner.Buffer(buf, 1024*1024)
		t.mu.Unlock()

		t.logger.Debug(ctx, "SSE stream opened")
		// Don't close - we keep the stream open
	} else {
		// Normal response - close body
		resp.Body.Close()
	}

	return nil
}

func (t *HTTPStreamTransport) sendPOST(ctx context.Context, message any) (*http.Response, error) {
	data, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.config.URL, strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	// Add session ID if present
	t.mu.RLock()
	sessionID := t.sessionID
	lastEventID := t.lastEventID
	t.mu.RUnlock()

	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	// Add Last-Event-ID for resumption
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}

	// Add custom headers from config
	for k, v := range t.config.Headers {
		req.Header.Set(k, v)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	// Handle HTTP errors
	if resp.StatusCode == http.StatusNotFound && sessionID != "" {
		// Session expired, need to reconnect
		t.mu.Lock()
		t.sessionID = ""
		t.connected = false
		t.mu.Unlock()
		return nil, fmt.Errorf("session expired (HTTP 404)")
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		// Rate limited, extract retry-after
		retryAfter := resp.Header.Get("Retry-After")
		return nil, fmt.Errorf("rate limited (HTTP 429), retry after: %s", retryAfter)
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return resp, nil
}

func (t *HTTPStreamTransport) Receive(ctx context.Context) (any, error) {
	t.mu.RLock()
	scanner := t.sseScanner
	t.mu.RUnlock()

	if scanner == nil {
		return nil, fmt.Errorf("no active SSE stream")
	}

	// Parse SSE event
	event, err := t.parseSSEEvent(ctx, scanner)
	if err != nil {
		// Check if we should reconnect
		if err == io.EOF || strings.Contains(err.Error(), "connection") {
			if err := t.reconnectSSE(ctx); err != nil {
				return nil, fmt.Errorf("reconnection failed: %w", err)
			}
			// Retry receive after reconnection
			return t.Receive(ctx)
		}
		return nil, err
	}

	return event, nil
}

func (t *HTTPStreamTransport) parseSSEEvent(ctx context.Context, scanner *bufio.Scanner) (any, error) {
	var eventID string
	var data strings.Builder

	// Read SSE event (multi-line format)
	for scanner.Scan() {
		line := scanner.Text()

		// Empty line marks end of event
		if line == "" {
			break
		}

		// Skip comments
		if strings.HasPrefix(line, ":") {
			continue
		}

		// Parse field
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		field := parts[0]
		value := strings.TrimPrefix(parts[1], " ")

		switch field {
		case "id":
			eventID = value
		case "event":
			// Event type is optional, we don't use it for routing
			_ = value
		case "data":
			if data.Len() > 0 {
				data.WriteString("\n")
			}
			data.WriteString(value)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner error: %w", err)
	}

	// Update last event ID for resumption
	if eventID != "" {
		t.mu.Lock()
		t.lastEventID = eventID
		t.mu.Unlock()
	}

	// Parse JSON data
	dataStr := data.String()
	if dataStr == "" {
		return nil, fmt.Errorf("empty event data")
	}

	// Try to determine message type
	var peek struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Method  string `json:"method"`
	}

	if err := json.Unmarshal([]byte(dataStr), &peek); err != nil {
		return nil, fmt.Errorf("failed to parse event data: %w", err)
	}

	// Parse based on type
	if peek.Method != "" && peek.ID != nil {
		var req JSONRPCRequest
		if err := json.Unmarshal([]byte(dataStr), &req); err != nil {
			return nil, err
		}
		t.logger.Debug(ctx, "Received SSE request",
			observability.F("method", req.Method),
			observability.F("event_id", eventID))
		return &req, nil
	} else if peek.Method != "" {
		var notif JSONRPCNotification
		if err := json.Unmarshal([]byte(dataStr), &notif); err != nil {
			return nil, err
		}
		t.logger.Debug(ctx, "Received SSE notification",
			observability.F("method", notif.Method))
		return &notif, nil
	} else {
		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(dataStr), &resp); err != nil {
			return nil, err
		}
		t.logger.Debug(ctx, "Received SSE response",
			observability.F("id", resp.ID))
		return &resp, nil
	}
}

func (t *HTTPStreamTransport) reconnectSSE(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.reconnect.attempt >= t.reconnect.maxAttempts {
		return fmt.Errorf("max reconnection attempts exceeded")
	}

	t.reconnect.attempt++
	delay := t.calculateBackoff()

	t.logger.Warn(ctx, "SSE stream disconnected, reconnecting",
		observability.F("attempt", t.reconnect.attempt),
		observability.F("delay", delay.String()))

	// Wait with backoff
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return ctx.Err()
	}

	// Open new SSE stream via GET
	req, err := http.NewRequestWithContext(ctx, "GET", t.config.URL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "text/event-stream")
	if t.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	if t.lastEventID != "" {
		req.Header.Set("Last-Event-ID", t.lastEventID)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("reconnection failed: HTTP %d", resp.StatusCode)
	}

	// Update stream
	if t.sseConn != nil {
		t.sseConn.Body.Close()
	}

	t.sseConn = resp
	t.sseScanner = bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	t.sseScanner.Buffer(buf, 1024*1024)

	// Reset attempt counter on successful reconnection
	t.reconnect.attempt = 0

	t.logger.Info(ctx, "SSE stream reconnected")

	return nil
}

func (t *HTTPStreamTransport) calculateBackoff() time.Duration {
	// Exponential backoff: baseDelay * 2^attempt
	delay := min(
		// Cap at max delay
		time.Duration(float64(t.reconnect.baseDelay)*math.Pow(2, float64(t.reconnect.attempt-1))), t.reconnect.maxDelay)

	// Add jitter
	if t.reconnect.jitterFunc != nil {
		delay = t.reconnect.jitterFunc(delay)
	}

	return delay
}

func defaultJitter(d time.Duration) time.Duration {
	// Add ±20% jitter
	jitter := time.Duration(float64(d) * 0.2 * (2.0*float64(time.Now().UnixNano()%100)/100.0 - 1.0))
	return d + jitter
}

func (t *HTTPStreamTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected {
		return nil
	}

	t.connected = false

	// Close SSE connection
	if t.sseConn != nil {
		t.sseConn.Body.Close()
		t.sseConn = nil
	}

	// Send DELETE to terminate session
	if t.sessionID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "DELETE", t.config.URL, nil)
		if err == nil {
			req.Header.Set("Mcp-Session-Id", t.sessionID)
			resp, err := t.httpClient.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}
	}

	ctx := context.Background()
	t.logger.Info(ctx, "HTTP Streamable transport closed")

	return nil
}

func (t *HTTPStreamTransport) IsConnected() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.connected
}

func (t *HTTPStreamTransport) TransportType() TransportType {
	return TransportHTTPStream
}
