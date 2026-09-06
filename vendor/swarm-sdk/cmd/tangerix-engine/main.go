// Package main provides the Tangerix Engine — an HTTP microservice that wraps
// the swarm-sdk Go client for B2B marketplace PO extraction and buyer/seller
// matching. Designed to be called from Cloudflare Workers via fetch().
//
// Endpoints:
//
//	POST /extract  — Extract structured PO fields from text
//	POST /match    — Match a PO against seller listings
//	POST /chat     — Multi-turn chat with the Tangerix AI persona
//	GET  /health   — Health check
//
// Environment variables:
//
//	PORT              — Listen port (default 8081)
//	WORKSPACE         — Workspace dir for swarm-sdk (default ".")
//	PROVIDER          — LLM provider name (default auto-detect from ~/.swarm)
//	MODEL             — Model name (default auto-detect)
//	API_KEY           — Provider API key (default auto-detect)
//	BASE_URL          — Provider base URL override
//	ALLOWED_ORIGINS   — Comma-separated CORS origins (default "*")
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	clientpkg "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// ─── Types ──────────────────────────────────────────────────────────────────

// Engine wraps the swarm-sdk client for Tangerix-specific operations.
type Engine struct {
	client *clientpkg.Client
	logger *log.Logger
}

// ExtractRequest is the payload for POST /extract.
type ExtractRequest struct {
	Text           string `json:"text"`            // Raw PO text (from PDF, CSV, pasted, OCR)
	ConversationID string `json:"conversation_id"` // Optional: resume a conversation
}

// ExtractResponse is the structured output from PO extraction.
type ExtractResponse struct {
	Raw            string  `json:"raw"`             // Raw LLM output (JSON + summary)
	ConversationID string  `json:"conversation_id"` // ID for follow-up turns
	Confidence     float64 `json:"confidence"`      // Placeholder for parsed confidence
	TokensIn       int     `json:"tokens_in"`
	TokensOut      int     `json:"tokens_out"`
	Duration       float64 `json:"duration_ms"`
}

// MatchRequest is the payload for POST /match.
type MatchRequest struct {
	PO        json.RawMessage `json:"po"`        // Structured PO fields (from extraction)
	Listings  json.RawMessage `json:"listings"`  // Array of seller listings to match against
	Direction string          `json:"direction"` // "buyer_to_seller" or "seller_to_buyer"
}

// MatchResponse is the structured output from matching.
type MatchResponse struct {
	Raw        string  `json:"raw"` // Raw LLM output (JSON + matches + summary)
	Confidence float64 `json:"confidence"`
	TokensIn   int     `json:"tokens_in"`
	TokensOut  int     `json:"tokens_out"`
	Duration   float64 `json:"duration_ms"`
}

// ChatRequest is the payload for POST /chat.
type ChatRequest struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversation_id,omitempty"`
}

// ChatResponse is the chat output.
type ChatResponse struct {
	ConversationID string  `json:"conversation_id"`
	Response       string  `json:"response"`
	TokensIn       int     `json:"tokens_in"`
	TokensOut      int     `json:"tokens_out"`
	Duration       float64 `json:"duration_ms"`
}

// HealthResponse is the health check output.
type HealthResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Version   string `json:"version"`
	Workspace string `json:"workspace"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
}

const version = "1.0.0"

// ─── Engine construction ────────────────────────────────────────────────────

// NewEngine creates the swarm-sdk client with the Tangerix system prompt.
func NewEngine() (*Engine, error) {
	var opts []clientpkg.Option

	workspace := envOrDefault("WORKSPACE", ".")
	opts = append(opts,
		clientpkg.WithWorkspace(workspace),
		clientpkg.WithSystemPrompt(TangerixSystemPrompt),
		clientpkg.WithoutIndexMd(),         // No INDEX.md scanning needed
		clientpkg.WithApprovalMode("yolo"), // Auto-approve for server use
	)

	// If an explicit provider is configured via env, disable autoconfig so
	// the engine is fully self-contained and doesn't read ~/.swarm.
	// Without PROVIDER set, autoconfig runs and picks up the local
	// ~/.swarm config (useful for dev/testing).
	providerStr := os.Getenv("PROVIDER")
	if providerStr != "" {
		p, err := clientpkg.ParseProvider(providerStr)
		if err != nil {
			return nil, fmt.Errorf("invalid PROVIDER %q: %w", providerStr, err)
		}
		model := os.Getenv("MODEL")
		opts = append(opts,
			clientpkg.WithoutAutoConfig(),
			clientpkg.WithProvider(p, model),
		)
	}
	if apiKey := os.Getenv("API_KEY"); apiKey != "" {
		opts = append(opts, clientpkg.WithAPIKey(apiKey))
	}
	if baseURL := os.Getenv("BASE_URL"); baseURL != "" {
		opts = append(opts, clientpkg.WithBaseURL(baseURL))
	}

	c, err := clientpkg.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create swarm-sdk client: %w", err)
	}

	return &Engine{
		client: c,
		logger: log.New(os.Stdout, "[tangerix-engine] ", log.LstdFlags),
	}, nil
}

// Close shuts down the engine client.
func (e *Engine) Close() error {
	return e.client.Close()
}

// ─── HTTP Handlers ──────────────────────────────────────────────────────────

// handleHealth returns engine health.
func (e *Engine) handleHealth(w http.ResponseWriter, r *http.Request) {
	in, out := e.client.TokenUsage()
	_ = in
	_ = out
	resp := HealthResponse{
		Status:    "healthy",
		Service:   "tangerix-engine",
		Version:   version,
		Workspace: e.client.WorkspaceDir(),
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleExtract handles POST /extract — PO field extraction.
func (e *Engine) handleExtract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ExtractRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	if strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	// Build a stateless extraction call with the Tangerix system prompt.
	// We use Execute with DisableTools so it runs a single text completion
	// (no tool loop) but still returns authoritative token/cost accounting
	// on the ExecuteResponse — unlike GenerateMessages which bypasses the
	// token-tracking fanout and would always report 0 tokens.
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(
		"Extract all purchase order fields from the following input. "+
			"Return structured JSON first, then a short summary, confidence, ambiguities, and next steps.\n\n"+
			"INPUT:\n%s", req.Text)

	start := time.Now()
	resp, err := e.client.Execute(ctx, agent.ExecuteRequest{
		Message:              prompt,
		SystemPromptOverride: TangerixSystemPrompt,
		DisableTools:         true,
		DisableHooks:         true,
	})
	duration := time.Since(start)

	if err != nil {
		e.logger.Printf("extract failed: %v", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("extraction failed: %v", err))
		return
	}

	out := ExtractResponse{
		Raw:       resp.Message,
		TokensIn:  resp.InputTokens,
		TokensOut: resp.OutputTokens,
		Duration:  float64(duration.Milliseconds()),
	}
	e.logger.Printf("extract OK: %d in / %d out tokens, $%.4f, %.0fms",
		resp.InputTokens, resp.OutputTokens, resp.CostUSD, float64(duration.Milliseconds()))
	writeJSON(w, http.StatusOK, out)
}

// handleMatch handles POST /match — buyer/seller matching.
func (e *Engine) handleMatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req MatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	if len(req.PO) == 0 {
		writeError(w, http.StatusBadRequest, "po is required")
		return
	}
	if len(req.Listings) == 0 {
		writeError(w, http.StatusBadRequest, "listings is required")
		return
	}

	direction := req.Direction
	if direction == "" {
		direction = "buyer_to_seller"
	}

	prompt := fmt.Sprintf(
		"Match the following purchase order against the seller listings. "+
			"Direction: %s. "+
			"Return structured JSON with the top 5 matches, each including match_score, matched_attributes, mismatches, confidence, and explanation. "+
			"Then provide a short summary.\n\n"+
			"PURCHASE ORDER:\n%s\n\n"+
			"SELLER LISTINGS:\n%s",
		direction, string(req.PO), string(req.Listings))

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	start := time.Now()
	resp, err := e.client.Execute(ctx, agent.ExecuteRequest{
		Message:              prompt,
		SystemPromptOverride: TangerixSystemPrompt,
		DisableTools:         true,
		DisableHooks:         true,
	})
	duration := time.Since(start)

	if err != nil {
		e.logger.Printf("match failed: %v", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("matching failed: %v", err))
		return
	}

	out := MatchResponse{
		Raw:       resp.Message,
		TokensIn:  resp.InputTokens,
		TokensOut: resp.OutputTokens,
		Duration:  float64(duration.Milliseconds()),
	}
	e.logger.Printf("match OK: %d in / %d out tokens, $%.4f, %.0fms",
		resp.InputTokens, resp.OutputTokens, resp.CostUSD, float64(duration.Milliseconds()))
	writeJSON(w, http.StatusOK, out)
}

// handleChat handles POST /chat — multi-turn chat with the Tangerix persona.
func (e *Engine) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	if strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	// ChatCtx with an empty convID runs statelessly and does NOT create a
	// conversation (only SendMessage does). To get a usable, persistent
	// conversation ID that round-trips for follow-up turns, create the
	// conversation explicitly when the caller didn't supply one.
	convID := req.ConversationID
	if convID == "" {
		conv, cerr := e.client.NewConversation(ctx)
		if cerr != nil {
			e.logger.Printf("chat: new conversation failed: %v", cerr)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create conversation: %v", cerr))
			return
		}
		convID = conv.ID
	}

	start := time.Now()
	response, err := e.client.ChatCtx(ctx, convID, req.Message)
	duration := time.Since(start)

	if err != nil {
		e.logger.Printf("chat failed: %v", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("chat failed: %v", err))
		return
	}

	in, out := e.client.TokenUsage()

	resp := ChatResponse{
		ConversationID: convID,
		Response:       response,
		TokensIn:       in,
		TokensOut:      out,
		Duration:       float64(duration.Milliseconds()),
	}
	e.logger.Printf("chat OK: conv=%s, %d in / %d out tokens, %.0fms", convID, in, out, float64(duration.Milliseconds()))
	writeJSON(w, http.StatusOK, resp)
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// enableCORS adds CORS headers. In production, restrict origins via ALLOWED_ORIGINS.
func enableCORS(next http.Handler) http.Handler {
	allowedOrigins := envOrDefault("ALLOWED_ORIGINS", "*")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowedOrigins == "*" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" {
			for _, allowed := range strings.Split(allowedOrigins, ",") {
				if strings.TrimSpace(allowed) == origin {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					break
				}
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─── Main ───────────────────────────────────────────────────────────────────

func main() {
	port := envOrDefault("PORT", "8081")

	engine, err := NewEngine()
	if err != nil {
		log.Fatalf("Failed to create engine: %v", err)
	}
	defer engine.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", engine.handleHealth)
	mux.HandleFunc("/extract", engine.handleExtract)
	mux.HandleFunc("/match", engine.handleMatch)
	mux.HandleFunc("/chat", engine.handleChat)

	handler := enableCORS(mux)

	addr := fmt.Sprintf(":%s", port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second, // Long for LLM generations
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		engine.logger.Println("Shutting down engine...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			engine.logger.Printf("Shutdown error: %v", err)
		}
	}()

	engine.logger.Printf("Tangerix Engine v%s running on http://localhost%s", version, addr)
	engine.logger.Printf("Endpoints: /health, /extract, /match, /chat")

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
