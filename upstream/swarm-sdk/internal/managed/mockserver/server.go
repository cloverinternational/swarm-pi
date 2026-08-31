// Package mockserver provides a mock Swarm Cloud server for testing managed agents.
// This allows developers to test managed execution mode locally without needing
// an actual VPS deployment.
package mockserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MockServer simulates the Swarm Cloud API for testing.
type MockServer struct {
	mu       sync.RWMutex
	server   *http.Server
	addr     string
	sessions map[string]*MockSession
	projects map[string]*MockProject
	apiKeys  map[string]bool
	logger   *slog.Logger

	// Callbacks for testing
	OnBootSession func(sessionID, projectID string) error
	OnStopSession func(sessionID string) error
}

// MockSession represents a mock agent session.
type MockSession struct {
	ID         string
	ProjectID  string
	Status     string
	Port       int
	StartedAt  time.Time
	Messages   []MockMessage
	TokenUsage MockTokenUsage
}

// MockProject represents a mock project.
type MockProject struct {
	ID        string
	CreatedAt time.Time
	VolumeID  string
}

// MockMessage represents a message in a session.
type MockMessage struct {
	Role    string
	Content string
}

// MockTokenUsage tracks token usage in a session.
type MockTokenUsage struct {
	InputTokens  int
	OutputTokens int
}

// Config configures the mock server.
type Config struct {
	// Addr is the address to listen on (e.g., "127.0.0.1:18080").
	// If empty, a random port will be assigned.
	Addr string
	// APIKeys is a set of valid API keys.
	APIKeys []string
	// Logger for structured logging.
	Logger *slog.Logger
	// Callbacks for testing
	OnBootSession func(sessionID, projectID string) error
	OnStopSession func(sessionID string) error
}

// New creates a new mock server.
func New(cfg Config) (*MockServer, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	addr := cfg.Addr
	if addr == "" {
		// Find an available port
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("failed to find available port: %w", err)
		}
		addr = ln.Addr().String()
		ln.Close()
	}

	apiKeys := make(map[string]bool)
	for _, key := range cfg.APIKeys {
		apiKeys[key] = true
	}
	// Add a default test key if none provided
	if len(apiKeys) == 0 {
		apiKeys["test-api-key"] = true
	}

	s := &MockServer{
		addr:          addr,
		sessions:      make(map[string]*MockSession),
		projects:      make(map[string]*MockProject),
		apiKeys:       apiKeys,
		logger:        cfg.Logger,
		OnBootSession: cfg.OnBootSession,
		OnStopSession: cfg.OnStopSession,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/projects/init", s.handleProjectInit)
	mux.HandleFunc("/v1/sessions/boot", s.handleSessionBoot)
	mux.HandleFunc("/v1/sessions/", s.handleSessionAction)

	s.server = &http.Server{
		Addr:    addr,
		Handler: s.authMiddleware(mux),
	}

	return s, nil
}

// Addr returns the server address.
func (s *MockServer) Addr() string {
	return s.addr
}

// URL returns the base URL for API calls.
func (s *MockServer) URL() string {
	return "http://" + s.addr
}

// Start starts the mock server.
func (s *MockServer) Start(ctx context.Context) error {
	s.logger.Info("starting mock server", "addr", s.addr)

	go func() {
		if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
			s.logger.Error("server error", "error", err)
		}
	}()

	// Wait for server to be ready
	for range 10 {
		conn, err := net.DialTimeout("tcp", s.addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	return fmt.Errorf("server did not start within timeout")
}

// Stop stops the mock server.
func (s *MockServer) Stop(ctx context.Context) error {
	s.logger.Info("stopping mock server")
	return s.server.Shutdown(ctx)
}

// authMiddleware adds API key authentication.
func (s *MockServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow health endpoint without auth
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		// Check for API key
		authHeader := r.Header.Get("Authorization")
		var apiKey string
		if after, ok := strings.CutPrefix(authHeader, "Bearer "); ok {
			apiKey = strings.TrimSpace(after)
		} else {
			apiKey = r.Header.Get("X-API-Key")
		}

		if apiKey == "" || !s.apiKeys[apiKey] {
			writeError(w, http.StatusUnauthorized, "invalid API key")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *MockServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *MockServer) handleProjectInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ProjectInitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if project exists
	if proj, exists := s.projects[req.ProjectID]; exists {
		writeJSON(w, http.StatusOK, ProjectInitResponse{
			NodeID:    req.NodeID,
			ProjectID: req.ProjectID,
			VolumeID:  proj.VolumeID,
			Status:    "initialized",
		})
		return
	}

	// Create new project
	proj := &MockProject{
		ID:        req.ProjectID,
		CreatedAt: time.Now(),
		VolumeID:  fmt.Sprintf("vol-%s", req.ProjectID),
	}
	s.projects[req.ProjectID] = proj

	writeJSON(w, http.StatusOK, ProjectInitResponse{
		NodeID:    req.NodeID,
		ProjectID: req.ProjectID,
		VolumeID:  proj.VolumeID,
		Status:    "initialized",
	})
}

func (s *MockServer) handleSessionBoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req BootSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if session exists
	if session, exists := s.sessions[req.SessionID]; exists {
		writeJSON(w, http.StatusOK, BootSessionResponse{
			NodeID:    req.NodeID,
			SessionID: req.SessionID,
			ProjectID: req.ProjectID,
			VMID:      fmt.Sprintf("vm-%s", req.SessionID[:8]),
			Status:    session.Status,
			Port:      session.Port,
			SocketURI: fmt.Sprintf("ws://localhost:%d/session/%s", session.Port, req.SessionID),
		})
		return
	}

	// Call callback if set
	if s.OnBootSession != nil {
		if err := s.OnBootSession(req.SessionID, req.ProjectID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	// Create new session
	port := 9000 + len(s.sessions)
	session := &MockSession{
		ID:        req.SessionID,
		ProjectID: req.ProjectID,
		Status:    "running",
		Port:      port,
		StartedAt: time.Now(),
	}
	s.sessions[req.SessionID] = session

	s.logger.Info("booted mock session",
		"session_id", req.SessionID,
		"project_id", req.ProjectID,
		"port", port,
	)

	writeJSON(w, http.StatusOK, BootSessionResponse{
		NodeID:    req.NodeID,
		SessionID: req.SessionID,
		ProjectID: req.ProjectID,
		VMID:      fmt.Sprintf("vm-%s", req.SessionID[:8]),
		Status:    "running",
		Port:      port,
		SocketURI: fmt.Sprintf("ws://localhost:%d/session/%s", port, req.SessionID),
	})
}

func (s *MockServer) handleSessionAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/sessions/"), "/")
	if len(parts) == 0 {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	sessionID := parts[0]

	// Check for sub-actions
	if len(parts) > 1 {
		switch parts[1] {
		case "stop":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			s.handleStopSession(w, r, sessionID)
			return
		case "message":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			s.handleSendMessage(w, r, sessionID)
			return
		case "stream":
			// Client flow: GET for pure stream subscription, POST for
			// send-message-and-stream (the single-call path used by
			// managed.Client.StreamWithMessage). Accept both.
			if r.Method != http.MethodGet && r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			s.handleStream(w, r, sessionID)
			return
		default:
			writeError(w, http.StatusNotFound, "unknown action")
			return
		}
	}

	// No sub-action - handle session itself
	switch r.Method {
	case http.MethodGet:
		s.handleGetSession(w, r, sessionID)
	case http.MethodPost:
		s.handleStopSession(w, r, sessionID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *MockServer) handleGetSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	s.mu.RLock()
	session, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":    session.ID,
		"project_id":    session.ProjectID,
		"status":        session.Status,
		"port":          session.Port,
		"started_at":    session.StartedAt,
		"input_tokens":  session.TokenUsage.InputTokens,
		"output_tokens": session.TokenUsage.OutputTokens,
	})
}

func (s *MockServer) handleStopSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, exists := s.sessions[sessionID]
	if !exists {
		writeJSON(w, http.StatusOK, StopSessionResponse{
			SessionID: sessionID,
			Status:    "not_found",
		})
		return
	}

	// Call callback if set
	if s.OnStopSession != nil {
		if err := s.OnStopSession(sessionID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	session.Status = "stopped"
	s.logger.Info("stopped mock session", "session_id", sessionID)

	writeJSON(w, http.StatusOK, StopSessionResponse{
		SessionID: sessionID,
		Status:    "stopped",
	})
}

func (s *MockServer) handleSendMessage(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req SendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	s.mu.Lock()
	session, exists := s.sessions[sessionID]
	if !exists {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	// Add message to session
	session.Messages = append(session.Messages, MockMessage{
		Role:    req.Role,
		Content: req.Content,
	})
	session.TokenUsage.InputTokens += len(strings.Fields(req.Content))
	s.mu.Unlock()

	// Simulate processing and generate a mock response
	go func() {
		time.Sleep(100 * time.Millisecond)

		s.mu.Lock()
		if session, ok := s.sessions[sessionID]; ok {
			session.Messages = append(session.Messages, MockMessage{
				Role:    "assistant",
				Content: fmt.Sprintf("Mock response to: %s", req.Content[:min(50, len(req.Content))]),
			})
			session.TokenUsage.OutputTokens += 50
		}
		s.mu.Unlock()
	}()

	writeJSON(w, http.StatusOK, map[string]string{
		"status":     "accepted",
		"session_id": sessionID,
	})
}

func (s *MockServer) handleStream(w http.ResponseWriter, r *http.Request, sessionID string) {
	s.mu.RLock()
	_, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Send mock events
	events := []map[string]any{
		{"type": "thinking", "content": "Processing request..."},
		{"type": "content", "content": "This is a mock response from the managed agent."},
		{"type": "done", "input_tokens": 100, "output_tokens": 50},
	}

	for _, event := range events {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		time.Sleep(50 * time.Millisecond)
	}
}

// GetSessions returns all sessions for testing assertions.
func (s *MockServer) GetSessions() map[string]*MockSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]*MockSession, len(s.sessions))
	maps.Copy(result, s.sessions)
	return result
}

// GetProjects returns all projects for testing assertions.
func (s *MockServer) GetProjects() map[string]*MockProject {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]*MockProject, len(s.projects))
	maps.Copy(result, s.projects)
	return result
}

// Helper functions and types

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}

// Request/Response types (shared with compute package)

type ProjectInitRequest struct {
	NodeID    string `json:"node_id"`
	ProjectID string `json:"project_id"`
}

type ProjectInitResponse struct {
	NodeID    string `json:"node_id"`
	ProjectID string `json:"project_id"`
	VolumeID  string `json:"volume_id"`
	Status    string `json:"status"`
}

type BootSessionRequest struct {
	NodeID    string `json:"node_id"`
	SessionID string `json:"session_id"`
	ProjectID string `json:"project_id"`
}

type BootSessionResponse struct {
	NodeID    string `json:"node_id"`
	SessionID string `json:"session_id"`
	ProjectID string `json:"project_id"`
	VMID      string `json:"vm_id"`
	Status    string `json:"status"`
	Port      int    `json:"port"`
	SocketURI string `json:"socket_uri"`
}

type StopSessionResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

type SendMessageRequest struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
