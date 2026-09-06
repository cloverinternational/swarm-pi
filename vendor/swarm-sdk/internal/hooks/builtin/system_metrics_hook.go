package builtin

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// SystemMetricsHook captures Go runtime and OS process metrics as system.metrics
// bronze events following the medallion architecture: raw data lands in bronze_events,
// silver/gold transforms surface it later.
//
// It fires:
//   - Once on agent.started (startup baseline snapshot)
//   - Periodically (every interval, default 60s) while the agent is running
//   - Once on conversation.completed (end-of-conversation snapshot)
//   - Once on agent.stopped (final snapshot, stops the ticker)
type SystemMetricsHook struct {
	interval  time.Duration
	startTime time.Time
	logger    observability.Logger

	mu          sync.Mutex
	emitter     SteeringEventEmitter
	lastConvID  string
	lastAgentID string
	stopCh      chan struct{}
	running     bool
}

// NewSystemMetricsHook creates a new system metrics capture hook.
// interval controls how often periodic snapshots are taken (default: 60s).
func NewSystemMetricsHook(interval time.Duration, logger observability.Logger) *SystemMetricsHook {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &SystemMetricsHook{
		interval:  interval,
		startTime: time.Now(),
		logger:    logger,
		stopCh:    make(chan struct{}),
	}
}

// Name implements hooks.Hook.
func (h *SystemMetricsHook) Name() string { return "system-metrics" }

// Priority implements hooks.Hook — runs after most other hooks.
func (h *SystemMetricsHook) Priority() int { return 0 }

// SetEventEmitter wires the emitter so the hook can fire system.metrics events
// back into the pipeline (where BronzeEventHook will capture them).
func (h *SystemMetricsHook) SetEventEmitter(emitter SteeringEventEmitter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.emitter = emitter
}

// Filter returns true for agent lifecycle and conversation completion events.
func (h *SystemMetricsHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventAgentStarted ||
		event.Type == hooks.EventAgentStopped ||
		event.Type == hooks.EventConversationCompleted
}

// OnEvent handles agent lifecycle events.
func (h *SystemMetricsHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Track the latest conversation/agent IDs for the periodic ticker.
	h.mu.Lock()
	if event.ConversationID != "" {
		h.lastConvID = event.ConversationID
	}
	if event.AgentID != "" {
		h.lastAgentID = event.AgentID
	}
	h.mu.Unlock()

	switch event.Type {
	case hooks.EventAgentStarted:
		h.mu.Lock()
		if !h.running {
			h.running = true
			h.stopCh = make(chan struct{})
			go h.runTicker(ctx)
		}
		h.mu.Unlock()
		// Startup baseline snapshot.
		h.emitMetrics(ctx, event.ConversationID, event.AgentID, "agent_started")

	case hooks.EventConversationCompleted:
		// End-of-conversation snapshot — captures peak usage for the session.
		h.emitMetrics(ctx, event.ConversationID, event.AgentID, "conversation_end")

	case hooks.EventAgentStopped:
		h.mu.Lock()
		if h.running {
			h.running = false
			close(h.stopCh)
		}
		h.mu.Unlock()
		// Final snapshot before shutdown.
		h.emitMetrics(ctx, event.ConversationID, event.AgentID, "agent_stopped")
	}

	return hooks.Continue(), nil
}

// runTicker fires periodic system.metrics events on the configured interval.
func (h *SystemMetricsHook) runTicker(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.mu.Lock()
			convID := h.lastConvID
			agentID := h.lastAgentID
			h.mu.Unlock()
			h.emitMetrics(ctx, convID, agentID, "periodic")
		case <-h.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// emitMetrics collects current system metrics and fires a system.metrics event.
func (h *SystemMetricsHook) emitMetrics(ctx context.Context, convID, agentID, trigger string) {
	h.mu.Lock()
	emitter := h.emitter
	h.mu.Unlock()
	if emitter == nil {
		return
	}

	data := collectSystemMetrics(trigger, time.Since(h.startTime))
	evt := hooks.Event{
		Type:           hooks.EventSystemMetrics,
		Timestamp:      time.Now(),
		ConversationID: convID,
		AgentID:        agentID,
		Data:           data,
	}
	if _, err := emitter.Emit(ctx, evt); err != nil && h.logger != nil {
		h.logger.Warn(ctx, "system_metrics.emit_failed",
			observability.F("error", err.Error()),
			observability.F("trigger", trigger))
	}
}

// collectSystemMetrics reads Go runtime stats and process RSS (Linux only).
func collectSystemMetrics(trigger string, uptime time.Duration) map[string]any {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	data := map[string]any{
		"trigger":       trigger,
		"heap_alloc_mb": float32(ms.HeapAlloc) / 1024 / 1024,
		"heap_sys_mb":   float32(ms.HeapSys) / 1024 / 1024,
		"goroutines":    runtime.NumGoroutine(),
		"gc_count":      ms.NumGC,
		"num_cpu":       runtime.NumCPU(),
		"go_version":    runtime.Version(),
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
		"uptime_s":      int64(uptime.Seconds()),
	}

	// RSS memory is only available on Linux via /proc/self/status.
	if runtime.GOOS == "linux" {
		if rssKB, err := readLinuxRSSKB(); err == nil {
			data["rss_mb"] = float32(rssKB) / 1024
		}
	}

	return data
}

// readLinuxRSSKB reads the resident set size from /proc/self/status.
// Returns the value in kilobytes.
func readLinuxRSSKB() (int64, error) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VmRSS:") {
			// Format: "VmRSS:\t  340 kB"
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return strconv.ParseInt(fields[1], 10, 64)
			}
		}
	}
	return 0, fmt.Errorf("VmRSS not found in /proc/self/status")
}
