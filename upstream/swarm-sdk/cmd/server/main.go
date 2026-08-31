// Package main provides an HTTP server that exposes the Swarm SDK Client API.
// This allows TypeScript/front-end applications to use the Swarm engine.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// Server wraps the Swarm SDK client and exposes it via HTTP
type Server struct {
	client        *client.Client
	conversations map[string]*conversation.Conversation
	terminalMgr   *TerminalManager
	logger        *log.Logger
}

// NewServer creates a new HTTP server with initialized Swarm SDK client
func NewServer(opts ...client.Option) (*Server, error) {
	c, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	return &Server{
		client:        c,
		conversations: make(map[string]*conversation.Conversation),
		terminalMgr:   NewTerminalManager(),
		logger:        log.New(os.Stdout, "[swarm-server] ", log.LstdFlags),
	}, nil
}

// Close shuts down the server and client
func (s *Server) Close() error {
	return s.client.Close()
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Status    string `json:"status"`
	Version   string `json:"version"`
	Workspace string `json:"workspace"`
}

// ChatRequest represents a chat message request
type ChatRequest struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversation_id,omitempty"`
	SystemPrompt   string `json:"system_prompt,omitempty"`
}

// ChatResponse represents a chat message response
type ChatResponse struct {
	ConversationID string `json:"conversation_id"`
	Response       string `json:"response"`
	Done           bool   `json:"done"`
	Error          string `json:"error,omitempty"`
}

// ConversationResponse represents a conversation
type ConversationResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Title     string    `json:"title,omitempty"`
}

// handleHealth returns server health status
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:    "healthy",
		Version:   "0.1.0",
		Workspace: s.client.WorkspaceDir(),
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleChat handles chat requests
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	ctx := r.Context()
	var convID string

	// Use existing conversation or create new one
	if req.ConversationID != "" {
		convID = req.ConversationID
	} else {
		// Create new conversation
		conv, err := s.client.NewConversation(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create conversation: %v", err))
			return
		}
		convID = conv.ID
		s.conversations[convID] = conv
	}

	// Send message using ChatInConversation
	response, err := s.client.ChatInConversation(convID, req.Message)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("chat failed: %v", err))
		return
	}

	resp := ChatResponse{
		ConversationID: convID,
		Response:       response,
		Done:           true,
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGenerate handles direct generation requests (no conversation state)
func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Prompt       string `json:"prompt"`
		SystemPrompt string `json:"system_prompt,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	ctx := r.Context()
	response, err := s.client.Generate(ctx, req.Prompt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("generation failed: %v", err))
		return
	}

	resp := ChatResponse{
		Response: response,
		Done:     true,
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleNewConversation creates a new conversation
func (s *Server) handleNewConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := r.Context()
	conv, err := s.client.NewConversation(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create conversation: %v", err))
		return
	}

	s.conversations[conv.ID] = conv

	resp := ConversationResponse{
		ID:        conv.ID,
		CreatedAt: conv.CreatedAt,
		UpdatedAt: conv.UpdatedAt,
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleListConversations lists all conversations
func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := r.Context()
	convs, err := s.client.ListAllConversations(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list conversations: %v", err))
		return
	}

	var resp []ConversationResponse
	for _, conv := range convs {
		resp = append(resp, ConversationResponse{
			ID:        conv.ID,
			CreatedAt: conv.CreatedAt,
			UpdatedAt: conv.UpdatedAt,
			Title:     conv.Title,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGetConversation gets a specific conversation
func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := r.URL.Path[len("/api/conversations/"):]
	if id == "" {
		writeError(w, http.StatusBadRequest, "conversation ID required")
		return
	}

	ctx := r.Context()
	conv, err := s.client.LoadConversation(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("conversation not found: %v", err))
		return
	}

	resp := ConversationResponse{
		ID:        conv.ID,
		CreatedAt: conv.CreatedAt,
		UpdatedAt: conv.UpdatedAt,
		Title:     conv.Title,
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleDeleteConversation deletes a conversation
func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := r.URL.Path[len("/api/conversations/"):]
	if id == "" {
		writeError(w, http.StatusBadRequest, "conversation ID required")
		return
	}

	ctx := r.Context()
	if err := s.client.DeleteConversation(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete conversation: %v", err))
		return
	}

	delete(s.conversations, id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleExecute handles agent execution with tools
func (s *Server) handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req agent.ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	ctx := r.Context()
	resp, err := s.client.Execute(ctx, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("execution failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleConfig returns client configuration
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := s.client.Config()
	writeJSON(w, http.StatusOK, cfg)
}

// Helper functions
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// enableCORS enables CORS for all origins (development only)
func enableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func main() {
	var (
		port      = os.Getenv("PORT")
		workspace = os.Getenv("WORKSPACE")
	)

	if port == "" {
		port = "8080"
	}
	if workspace == "" {
		workspace = "."
	}

	// Create server with default configuration
	server, err := NewServer(
		client.WithWorkspace(workspace),
		client.WithAllTools(),           // Enable all tools
		client.WithDefaultHooks(),       // Enable steering, task enforcement
		client.WithApprovalMode("yolo"), // Auto-approve for server use
	)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Setup routes
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("/health", server.handleHealth)

	// Chat/Generation
	mux.HandleFunc("/api/chat", server.handleChat)
	mux.HandleFunc("/api/generate", server.handleGenerate)

	// Conversations
	mux.HandleFunc("/api/conversations", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			server.handleListConversations(w, r)
		case http.MethodPost:
			server.handleNewConversation(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
	mux.HandleFunc("/api/conversations/", server.handleGetConversation)
	mux.HandleFunc("/api/conversations/delete/", server.handleDeleteConversation)

	// Execution
	mux.HandleFunc("/api/execute", server.handleExecute)

	// Terminal WebSocket
	server.terminalMgr.SetupTerminalRoutes(mux)

	// Config
	mux.HandleFunc("/api/config", server.handleConfig)

	// Wrap with CORS
	handler := enableCORS(mux)

	// Start server
	addr := fmt.Sprintf(":%s", port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		log.Println("Shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("Shutdown error: %v", err)
		}
	}()

	log.Printf("Swarm SDK Server running on http://localhost%s", addr)
	log.Printf("Workspace: %s", workspace)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
