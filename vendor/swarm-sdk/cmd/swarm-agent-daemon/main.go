// swarm-agent-daemon — autonomous agent daemon with attach/detach.
//
// Starts an agent working toward a goal, exposes an HTTP server so
// that TUI clients (or any SSE/WS consumer) can attach, watch the
// agent work, send steering messages, and detach — like tmux for AI.
//
// Usage:
//
//	go run ./cmd/swarm-agent-daemon -goal "refactor auth module"
//	go run ./cmd/swarm-agent-daemon -goal "fix all failing tests" -addr :9090
//
// Endpoints:
//
//	GET  /sse       — SSE event stream (live agent updates)
//	GET  /sse?after=123  — replay from seq 123, then live
//	GET  /ws        — WebSocket (bidirectional: events + send messages)
//	POST /rpc       — JSON-RPC 2.0 (full client surface)
//	GET  /health    — daemon health + stats
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/serve"
)

func main() {
	goal := flag.String("goal", "", "goal prompt for the autonomous agent (required)")
	addr := flag.String("addr", ":8090", "HTTP listen address")
	model := flag.String("model", "claude-sonnet-4-5", "LLM model to use")
	provider := flag.String("provider", string(client.ProviderAnthropic), "LLM provider")
	logCap := flag.Int("logcap", 2048, "event log ring buffer capacity")
	continuePrompt := flag.String("continue-prompt",
		"Continue working toward the original goal. If you've completed the goal, say so explicitly. Otherwise, keep going.",
		"prompt sent when the agent finishes a turn without tool calls")
	flag.Parse()

	if *goal == "" {
		fmt.Fprintln(os.Stderr, "error: -goal is required")
		flag.Usage()
		os.Exit(1)
	}

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[daemon] ")
	log.Printf("starting autonomous agent daemon")
	log.Printf("  goal:    %q", *goal)
	log.Printf("  addr:    %s", *addr)
	log.Printf("  model:   %s/%s", *provider, *model)

	// ── Create client ─────────────────────────────────────────────
	c, err := client.New(
		client.WithProvider(client.Provider(*provider), *model),
		client.WithFullAgent(),
		client.WithApprovalMode("yolo"),
	)
	if err != nil {
		log.Fatalf("client.New: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := c.Start(ctx); err != nil {
		log.Fatalf("client.Start: %v", err)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		_ = c.Stop(shutCtx)
		_ = c.Close()
	}()

	// ── Event log for replay ─────────────────────────────────────
	elog := serve.NewEventLog(*logCap)
	elog.Attach(c)
	defer elog.Detach()

	// ── Wire serve.Mux ───────────────────────────────────────────
	mux := serve.NewMux(c)

	// ── Autonomous agent loop ─────────────────────────────────────
	go runAgentLoop(ctx, c, *goal, *continuePrompt)

	// ── HTTP server ───────────────────────────────────────────────
	httpMux := http.NewServeMux()

	// SSE with optional replay via ?after=seq
	httpMux.Handle("/sse", sseWithReplay(mux, elog))

	// WebSocket (bidirectional — attach + steer)
	httpMux.Handle("/ws", mux.WebSocketHandler())

	// JSON-RPC over HTTP
	httpMux.Handle("/rpc", mux.HTTPHandler())

	// Health / status
	httpMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":    "running",
			"workspace": c.WorkspaceDir(),
			"goal":      *goal,
			"model":     *model,
			"provider":  *provider,
			"eventSeq":  elog.Latest(),
		})
	})

	srv := &http.Server{
		Addr:         *addr,
		Handler:      httpMux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // unlimited for SSE
	}

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		log.Printf("shutting down...")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("HTTP server listening on %s", *addr)
	log.Printf("  SSE:       http://localhost%s/sse", *addr)
	log.Printf("  WebSocket: http://localhost%s/ws", *addr)
	log.Printf("  RPC:       http://localhost%s/rpc", *addr)
	log.Printf("  Health:    http://localhost%s/health", *addr)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
	log.Printf("daemon exited")
}

// runAgentLoop sends the goal prompt, then watches for stream-end
// events. When a turn finishes without tool calls, it re-prompts to
// keep the agent working. This creates the "run for hours" behavior.
func runAgentLoop(ctx context.Context, c *client.Client, goal, continueMsg string) {
	// Send the initial goal.
	log.Printf("sending goal prompt...")
	if err := c.SendMessage(ctx, "", goal, client.SendMessageOptions{}); err != nil {
		log.Printf("goal send error: %v", err)
		return
	}
	log.Printf("initial turn complete")

	// Now loop: when the agent finishes a turn, re-prompt.
	// We detect "turn finished" by watching for EventStreamEnd.
	turnDone := make(chan struct{}, 1)
	c.Subscribe(func(ev client.Event) error {
		if ev.Kind == client.EventStreamEnd {
			select {
			case turnDone <- struct{}{}:
			default:
			}
		}
		return nil
	})

	for {
		select {
		case <-ctx.Done():
			log.Printf("agent loop: context cancelled")
			return
		case <-turnDone:
			// Small pause between turns to avoid hammering the API.
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}

			log.Printf("agent turn ended — re-prompting to continue")
			if err := c.SendMessage(ctx, "", continueMsg, client.SendMessageOptions{}); err != nil {
				log.Printf("continue send error: %v", err)
				// Back off on error.
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Second):
				}
			}
		}
	}
}

// sseWithReplay wraps the serve.Mux SSE handler with optional event
// log replay. If the request includes ?after=N, the handler first
// replays all events with seq > N from the event log, then switches
// to the live SSE stream.
func sseWithReplay(mux *serve.Mux, elog *serve.EventLog) http.Handler {
	inner := mux.SSEHandler()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		afterStr := r.URL.Query().Get("after")
		if afterStr == "" {
			// No replay requested — just use the standard SSE handler.
			inner.ServeHTTP(w, r)
			return
		}

		after, err := strconv.ParseUint(afterStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid after parameter", http.StatusBadRequest)
			return
		}

		// Replay phase: send historical events.
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, ": replay-start\n\n")
		flusher.Flush()

		events, latest := elog.Events(after)
		for _, ev := range events {
			fmt.Fprintf(w, "event: replay\ndata: %s\n\n", ev.Data)
			flusher.Flush()
		}
		fmt.Fprintf(w, "event: replay-end\ndata: {\"latest\":%d}\n\n", latest)
		flusher.Flush()

		// Now fall through to live SSE — but we can't just call
		// inner.ServeHTTP because it writes its own headers. Instead,
		// we rely on the fact that the SSE handler's Subscribe will
		// only send events that happen *after* subscription, which
		// is exactly what we want after replay.
		//
		// Create a fake request without the ?after param so the inner
		// handler doesn't try to replay again.
		cleanURL := *r.URL
		cleanURL.RawQuery = ""
		cleanReq := r.Clone(r.Context())
		cleanReq.URL = &cleanURL

		inner.ServeHTTP(w, cleanReq)
	})
}
