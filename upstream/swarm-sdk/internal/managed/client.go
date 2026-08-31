// Package managed provides a client for connecting to Swarm Cloud managed agents.
// This allows agents to run remotely with isolated compute environments.
package managed

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Client is a client for the Swarm Cloud managed agents API.
type Client struct {
	endpoint   string
	apiKey     string
	httpClient *http.Client
	logger     *slog.Logger

	mu           sync.RWMutex
	sessionCache map[string]*SessionInfo
}

// Config configures the managed client.
type Config struct {
	// Endpoint is the URL of the Swarm Cloud API.
	// Example: "https://cloud.swarmcode.ai" or "http://149.28.63.81:8080"
	Endpoint string

	// APIKey is the authentication key for the service.
	APIKey string

	// HTTPClient is an optional custom HTTP client.
	HTTPClient *http.Client

	// Logger for structured logging.
	Logger *slog.Logger
}

// SessionInfo contains information about a managed session.
type SessionInfo struct {
	SessionID string    `json:"session_id"`
	ProjectID string    `json:"project_id"`
	Status    string    `json:"status"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"started_at"`
	SocketURI string    `json:"socket_uri"`
}

// NewClient creates a new managed client.
func NewClient(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Client{
		endpoint:     cfg.Endpoint,
		apiKey:       cfg.APIKey,
		httpClient:   httpClient,
		logger:       logger,
		sessionCache: make(map[string]*SessionInfo),
	}, nil
}

// doRequest performs an HTTP request to the managed API.
func (c *Client) doRequest(ctx context.Context, method, path string, body any, result any) error {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	url := c.endpoint + path
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			return fmt.Errorf("request failed with status %d", resp.StatusCode)
		}
		return fmt.Errorf("API error: %s", errResp.Error)
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// HealthCheck checks if the managed service is healthy.
func (c *Client) HealthCheck(ctx context.Context) error {
	resp, err := c.httpClient.Get(c.endpoint + "/healthz")
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}

	return nil
}

// InitProject initializes a project for managed execution.
func (c *Client) InitProject(ctx context.Context, projectID string) error {
	c.logger.Info("initializing project", "project_id", projectID)

	req := projectInitRequest{
		NodeID:    "sdk-client",
		ProjectID: projectID,
	}

	var resp projectInitResponse
	if err := c.doRequest(ctx, "POST", "/v1/projects/init", req, &resp); err != nil {
		return fmt.Errorf("failed to init project: %w", err)
	}

	c.logger.Info("project initialized", "project_id", projectID, "volume_id", resp.VolumeID)
	return nil
}

// BootSession boots a new managed session.
func (c *Client) BootSession(ctx context.Context, sessionID, projectID string) (*SessionInfo, error) {
	c.logger.Info("booting session", "session_id", sessionID, "project_id", projectID)

	req := bootSessionRequest{
		NodeID:    "sdk-client",
		SessionID: sessionID,
		ProjectID: projectID,
	}

	var resp bootSessionResponse
	if err := c.doRequest(ctx, "POST", "/v1/sessions/boot", req, &resp); err != nil {
		return nil, fmt.Errorf("failed to boot session: %w", err)
	}

	info := &SessionInfo{
		SessionID: resp.SessionID,
		ProjectID: resp.ProjectID,
		Status:    resp.Status,
		Port:      resp.Port,
		SocketURI: resp.SocketURI,
		StartedAt: time.Now(),
	}

	c.mu.Lock()
	c.sessionCache[sessionID] = info
	c.mu.Unlock()

	c.logger.Info("session booted",
		"session_id", sessionID,
		"status", resp.Status,
		"port", resp.Port,
	)

	return info, nil
}

// StopSession stops a managed session.
func (c *Client) StopSession(ctx context.Context, sessionID string) error {
	c.logger.Info("stopping session", "session_id", sessionID)

	var resp stopSessionResponse
	if err := c.doRequest(ctx, "POST", "/v1/sessions/"+sessionID+"/stop", nil, &resp); err != nil {
		return fmt.Errorf("failed to stop session: %w", err)
	}

	c.mu.Lock()
	delete(c.sessionCache, sessionID)
	c.mu.Unlock()

	c.logger.Info("session stopped", "session_id", sessionID)
	return nil
}

// GetSession gets information about a session.
func (c *Client) GetSession(ctx context.Context, sessionID string) (*SessionInfo, error) {
	// Check cache first
	c.mu.RLock()
	if info, ok := c.sessionCache[sessionID]; ok {
		c.mu.RUnlock()
		return info, nil
	}
	c.mu.RUnlock()

	var resp sessionInfoResponse
	if err := c.doRequest(ctx, "GET", "/v1/sessions/"+sessionID, nil, &resp); err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	info := &SessionInfo{
		SessionID: resp.SessionID,
		ProjectID: resp.ProjectID,
		Status:    resp.Status,
		Port:      resp.Port,
		StartedAt: resp.StartedAt,
	}

	c.mu.Lock()
	c.sessionCache[sessionID] = info
	c.mu.Unlock()

	return info, nil
}

// IsSessionHealthy checks if a session is still running and responsive.
// This is used to determine if an existing session can be reused.
func (c *Client) IsSessionHealthy(ctx context.Context, sessionID string) (bool, error) {
	// Check cache first for port
	c.mu.RLock()
	info, ok := c.sessionCache[sessionID]
	c.mu.RUnlock()

	if !ok {
		return false, fmt.Errorf("session not in cache")
	}

	// Try to hit the health endpoint on the session container
	// The manager proxies /v1/sessions/{id}/health to the container
	healthURL := fmt.Sprintf("%s/v1/sessions/%s/health", c.endpoint, sessionID)

	req, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
	if err != nil {
		return false, err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Debug("session health check failed", "error", err, "session_id", sessionID)
		return false, err
	}
	defer resp.Body.Close()

	healthy := resp.StatusCode == http.StatusOK

	if healthy {
		c.logger.Debug("session is healthy", "session_id", sessionID, "port", info.Port)
	} else {
		c.logger.Debug("session not healthy", "session_id", sessionID, "status", resp.StatusCode)
	}

	return healthy, nil
}

// SendMessage sends a message to a session.
func (c *Client) SendMessage(ctx context.Context, sessionID string, role, content string) error {
	req := sendMessageRequest{
		Role:    role,
		Content: content,
	}

	if err := c.doRequest(ctx, "POST", "/v1/sessions/"+sessionID+"/message", req, nil); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}

// StreamEvents streams events from a session using Server-Sent Events.
func (c *Client) StreamEvents(ctx context.Context, sessionID string) (<-chan Event, error) {
	url := c.endpoint + "/v1/sessions/" + sessionID + "/stream"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("stream request returned status %d", resp.StatusCode)
	}

	eventCh := make(chan Event, 100)

	go func() {
		defer close(eventCh)
		defer resp.Body.Close()

		decoder := newSSEDecoder(resp.Body)
		for {
			event, err := decoder.Decode()
			if err != nil {
				if err != io.EOF {
					c.logger.Warn("stream decode error", "error", err)
				}
				return
			}

			select {
			case eventCh <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	return eventCh, nil
}

// StreamWithMessage sends a message to a session and streams the response events.
// This is the primary method for executing agents - it POSTs the message to /v1/stream
// and returns the SSE event channel.
func (c *Client) StreamWithMessage(ctx context.Context, sessionID, model, message string) (<-chan Event, error) {
	url := c.endpoint + "/v1/sessions/" + sessionID + "/stream"

	// Build the request body
	body := map[string]any{
		"session_id": sessionID,
		"model":      model,
		"message":    message,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("stream request returned status %d: %s", resp.StatusCode, string(respBody))
	}

	eventCh := make(chan Event, 100)

	go func() {
		defer close(eventCh)
		defer resp.Body.Close()

		decoder := newSSEDecoder(resp.Body)
		for {
			event, err := decoder.Decode()
			if err != nil {
				if err != io.EOF {
					c.logger.Warn("stream decode error", "error", err)
				}
				return
			}

			select {
			case eventCh <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	return eventCh, nil
}

// Event represents a server-sent event.
type Event struct {
	Type    string         `json:"type"`
	Content string         `json:"content,omitempty"`
	Data    map[string]any `json:"-"`
}

// Request/Response types

type projectInitRequest struct {
	NodeID    string `json:"node_id"`
	ProjectID string `json:"project_id"`
}

type projectInitResponse struct {
	NodeID    string `json:"node_id"`
	ProjectID string `json:"project_id"`
	VolumeID  string `json:"volume_id"`
	Status    string `json:"status"`
}

type bootSessionRequest struct {
	NodeID    string `json:"node_id"`
	SessionID string `json:"session_id"`
	ProjectID string `json:"project_id"`
}

type bootSessionResponse struct {
	NodeID    string `json:"node_id"`
	SessionID string `json:"session_id"`
	ProjectID string `json:"project_id"`
	VMID      string `json:"vm_id"`
	Status    string `json:"status"`
	Port      int    `json:"port"`
	SocketURI string `json:"socket_uri"`
}

type stopSessionResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

type sessionInfoResponse struct {
	SessionID    string    `json:"session_id"`
	ProjectID    string    `json:"project_id"`
	Status       string    `json:"status"`
	Port         int       `json:"port"`
	StartedAt    time.Time `json:"started_at"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
}

type sendMessageRequest struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// SSEDecoder decodes Server-Sent Events.
type SSEDecoder struct {
	reader *bufio.Reader
}

func newSSEDecoder(r io.Reader) *SSEDecoder {
	return &SSEDecoder{
		reader: bufio.NewReader(r),
	}
}

func (d *SSEDecoder) Decode() (Event, error) {
	var event Event
	var eventType string

	for {
		line, err := d.reader.ReadBytes('\n')
		if err != nil {
			return event, err
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			// Empty line signals end of event
			if eventType != "" {
				event.Type = eventType
				return event, nil
			}
			continue
		}

		if after, ok := bytes.CutPrefix(line, []byte("event: ")); ok {
			eventType = string(after)
			continue
		}

		if after, ok := bytes.CutPrefix(line, []byte("data: ")); ok {
			data := after
			// Parse the JSON data into a map
			var dataMap map[string]any
			if err := json.Unmarshal(data, &dataMap); err != nil {
				// If it's not a JSON object, try to parse as a simple value
				event.Content = string(data)
			} else {
				event.Data = dataMap
				// Extract content if present
				if content, ok := dataMap["content"].(string); ok {
					event.Content = content
				}
				// Fallback: allow the type to travel inside the JSON payload
				// when the server didn't emit a separate `event:` prefix line.
				// Real Anthropic-style streams use `event:` lines; some upstream
				// mocks and simpler servers just embed `type` in the data blob.
				if eventType == "" {
					if t, ok := dataMap["type"].(string); ok {
						eventType = t
					}
				}
			}
			continue
		}
	}
}
