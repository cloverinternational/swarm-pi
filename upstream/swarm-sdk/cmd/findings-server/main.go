package main

// Local Findings Server
// REST API server for findings synchronization and search
// Usage: go run local_server.go [--port 8080] [--db postgres://...]

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/findings"
)

var (
	port       = flag.Int("port", 8080, "Server port")
	modelPath  = flag.String("model", "", "Path to embedding model (optional)")
	enableAuth = flag.Bool("auth", false, "Enable API key authentication")
)

type Server struct {
	cache      findings.Cache
	index      findings.SemanticIndex
	enableAuth bool
	apiKeys    map[string]bool
}

func main() {
	flag.Parse()

	log.Println("╔══════════════════════════════════════════════════════════╗")
	log.Println("║  Swarm Findings Server - Local                           ║")
	log.Println("╚══════════════════════════════════════════════════════════╝")

	// Initialize PostgreSQL cache
	log.Println("Connecting to PostgreSQL...")
	config := findings.PostgresConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "swarm_findings",
		User:     "swarm",
		Password: "swarm",
		SSLMode:  "disable",
	}
	cache, err := findings.NewPostgresCache(config)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer cache.Close()

	log.Println("Database connected successfully")

	// Initialize semantic index (optional)
	var index findings.SemanticIndex
	if *modelPath != "" {
		log.Println("Initializing semantic index with model:", *modelPath)
		embedder := &findings.LocalEmbedder{}
		index, err = findings.NewFaissSemanticIndex("/tmp/findings-index", embedder)
		if err != nil {
			log.Printf("Warning: Failed to initialize semantic index: %v", err)
			index = findings.NewNoOpIndex()
		}
	} else {
		log.Println("No embedding model specified, semantic search disabled")
		index = findings.NewNoOpIndex()
	}

	// Create server
	server := &Server{
		cache:      cache,
		index:      index,
		enableAuth: *enableAuth,
		apiKeys:    map[string]bool{"test-key-123": true},
	}

	// Setup routes
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", server.handleHealth)

	// API endpoints
	mux.HandleFunc("/api/v1/findings/push", server.withAuth(server.handlePush))
	mux.HandleFunc("/api/v1/findings/pull", server.withAuth(server.handlePull))
	mux.HandleFunc("/api/v1/findings/search", server.withAuth(server.handleSearch))
	mux.HandleFunc("/api/v1/findings/", server.withAuth(server.handleGetFinding))
	mux.HandleFunc("/api/v1/stats", server.withAuth(server.handleStats))

	// Start server
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: mux,
	}

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("\nShutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpServer.Shutdown(ctx)
	}()

	log.Printf("Server starting on http://localhost:%d", *port)
	log.Println("Endpoints:")
	log.Println("  POST /api/v1/findings/push    - Upload findings")
	log.Println("  POST /api/v1/findings/pull    - Download findings")
	log.Println("  GET  /api/v1/findings/search  - Search findings")
	log.Println("  GET  /api/v1/findings/{id}    - Get specific finding")
	log.Println("  GET  /api/v1/stats            - Server statistics")
	log.Println("  GET  /health                  - Health check")
	log.Println("")

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}

	log.Println("Server stopped")
}

// withAuth wraps handlers with authentication
func (s *Server) withAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.enableAuth {
			apiKey := r.Header.Get("Authorization")
			if apiKey == "" || !s.apiKeys[apiKey] {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		handler(w, r)
	}
}

// handleHealth returns health status
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := map[string]any{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handlePush receives findings from agents
func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Findings []findings.Finding `json:"findings"`
		DeviceID string             `json:"device_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	count := 0

	for _, f := range req.Findings {
		f.Metadata.Source = "synced"

		if err := s.cache.Write(ctx, f); err != nil {
			log.Printf("Warning: Failed to store finding %s: %v", f.FindingID, err)
			continue
		}

		if s.index != nil {
			go s.index.Index(ctx, f)
		}

		count++
	}

	log.Printf("Pushed %d findings from %s", count, req.DeviceID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":   "ok",
		"received": len(req.Findings),
		"stored":   count,
	})
}

// handlePull sends findings to agents
func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Since    time.Time `json:"since"`
		DeviceID string    `json:"device_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	results, err := s.cache.Query(ctx, findings.FindingQuery{
		TimeRange: &findings.TimeRange{
			Start: req.Since,
			End:   time.Now().Add(time.Hour),
		},
		Limit: 1000,
	})

	if err != nil {
		http.Error(w, fmt.Sprintf("Query failed: %v", err), http.StatusInternalServerError)
		return
	}

	fList := make([]findings.Finding, len(results))
	for i, r := range results {
		fList[i] = r.Finding
	}

	log.Printf("Pulled %d findings for %s", len(fList), req.DeviceID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"findings": fList,
		"has_more": false,
	})
}

// handleSearch searches findings
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := findings.FindingQuery{
		ToolName: r.URL.Query().Get("tool"),
		AgentID:  r.URL.Query().Get("agent"),
		Limit:    100,
	}

	if limit := r.URL.Query().Get("limit"); limit != "" {
		fmt.Sscanf(limit, "%d", &query.Limit)
	}

	if tags := r.URL.Query().Get("tags"); tags != "" {
		query.Tags = strings.Split(tags, ",")
	}

	ctx := r.Context()

	var results []findings.FindingResult
	var err error

	if semantic := r.URL.Query().Get("semantic"); semantic != "" && s.index != nil {
		results, err = s.index.SemanticSearch(ctx, semantic, query.Limit)
	} else {
		results, err = s.cache.Query(ctx, query)
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("Search failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"results": results,
		"count":   len(results),
	})
}

// handleGetFinding returns a specific finding
func (s *Server) handleGetFinding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Path
	prefix := "/api/v1/findings/"
	if len(path) <= len(prefix) {
		http.Error(w, "Finding ID required", http.StatusBadRequest)
		return
	}

	findingID := path[len(prefix):]
	if findingID == "" {
		http.Error(w, "Finding ID required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	f, err := s.cache.Read(ctx, findingID)
	if err != nil {
		http.Error(w, "Finding not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(f)
}

// handleStats returns server statistics
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats := map[string]any{
		"server_time": time.Now().UTC().Format(time.RFC3339),
	}

	if pc, ok := s.cache.(*findings.PostgresCache); ok {
		if dbStats, err := pc.Stats(r.Context()); err == nil {
			maps.Copy(stats, dbStats)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
