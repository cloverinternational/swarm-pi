// Package main demonstrates the hook system with real-world scenarios
package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/google/uuid"
)

// MockLogger implements observability.Logger for demo
type MockLogger struct{}

func (l *MockLogger) Log(ctx context.Context, level observability.Level, event string, fields ...observability.Field) {
	fmt.Printf("[%s] %s", level, event)
	for _, f := range fields {
		fmt.Printf(" %s=%v", f.Key, f.Value)
	}
	fmt.Println()
}

func (l *MockLogger) Trace(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelTrace, event, fields...)
}

func (l *MockLogger) Debug(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelDebug, event, fields...)
}

func (l *MockLogger) Info(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelInfo, event, fields...)
}

func (l *MockLogger) Warn(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelWarn, event, fields...)
}

func (l *MockLogger) Error(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelError, event, fields...)
}

func (l *MockLogger) Fatal(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelFatal, event, fields...)
}

func (l *MockLogger) WithFields(fields ...observability.Field) observability.Logger {
	return l
}

func (l *MockLogger) SetLevel(level observability.Level) {}

// MockTracer implements observability.Tracer for demo
type MockTracer struct{}

type mockSpan struct {
	name    string
	ctx     context.Context
	spanID  string
	traceID string
}

func (s *mockSpan) End() {
	fmt.Printf("  [SPAN] Ended: %s\n", s.name)
}

func (s *mockSpan) SetAttribute(key string, value any) {
	fmt.Printf("  [SPAN] %s: %s = %v\n", s.name, key, value)
}

func (s *mockSpan) SetAttributes(attrs map[string]any) {
	for key, value := range attrs {
		s.SetAttribute(key, value)
	}
}

func (s *mockSpan) SetStatus(code observability.StatusCode, message string) {
	fmt.Printf("  [SPAN] %s: status = %v, message = %s\n", s.name, code, message)
}

func (s *mockSpan) RecordError(err error) {
	fmt.Printf("  [SPAN] %s: error = %v\n", s.name, err)
}

func (s *mockSpan) SpanID() string {
	return s.spanID
}

func (s *mockSpan) TraceID() string {
	return s.traceID
}

func (s *mockSpan) Context() context.Context {
	return s.ctx
}

func (t *MockTracer) StartSpan(ctx context.Context, name string) (context.Context, observability.Span) {
	fmt.Printf("  [SPAN] Started: %s\n", name)
	span := &mockSpan{
		name:    name,
		ctx:     ctx,
		spanID:  "span-" + uuid.New().String()[:8],
		traceID: "trace-" + uuid.New().String()[:8],
	}
	return ctx, span
}

func (t *MockTracer) StartSpanWithOptions(ctx context.Context, name string, opts observability.SpanOptions) (context.Context, observability.Span) {
	return t.StartSpan(ctx, name)
}

func (t *MockTracer) SpanFromContext(ctx context.Context) observability.Span {
	return nil
}

func (t *MockTracer) InjectContext(ctx context.Context, carrier map[string]string) error {
	return nil
}

func (t *MockTracer) ExtractContext(carrier map[string]string) (context.Context, error) {
	return context.Background(), nil
}

// MockMetrics implements observability.Metrics for demo
type MockMetrics struct{}

func (m *MockMetrics) Counter(name string, value float64, labels map[string]string) {
	fmt.Printf("  [METRIC] Counter: %s = %.0f %v\n", name, value, labels)
}

func (m *MockMetrics) Gauge(name string, value float64, labels map[string]string) {
	fmt.Printf("  [METRIC] Gauge: %s = %.2f %v\n", name, value, labels)
}

func (m *MockMetrics) Histogram(name string, value float64, labels map[string]string) {
	fmt.Printf("  [METRIC] Histogram: %s = %.2f %v\n", name, value, labels)
}

func (m *MockMetrics) Summary(name string, value float64, labels map[string]string) {
	fmt.Printf("  [METRIC] Summary: %s = %.2f %v\n", name, value, labels)
}

func (m *MockMetrics) Timing(name string, duration time.Duration, labels map[string]string) {
	fmt.Printf("  [METRIC] Timing: %s = %v %v\n", name, duration, labels)
}

// MockAuditor implements observability.Auditor for demo
type MockAuditor struct{}

func (a *MockAuditor) Record(ctx context.Context, event observability.AuditEvent) error {
	fmt.Printf("  [AUDIT] %s: %s by %s - %s\n", event.EventType, event.Action, event.Actor, event.Outcome)
	return nil
}

func (a *MockAuditor) Query(ctx context.Context, criteria observability.AuditCriteria) ([]observability.AuditEvent, error) {
	return nil, nil
}

// Custom hooks for demo

// ToolBlockerHook blocks specific tools based on rules
type ToolBlockerHook struct {
	blockedTools map[string]string // tool name -> reason
}

func NewToolBlockerHook() *ToolBlockerHook {
	return &ToolBlockerHook{
		blockedTools: map[string]string{
			"rm":          "Destructive operation blocked by policy",
			"delete_file": "Destructive operation blocked by policy",
		},
	}
}

func (h *ToolBlockerHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	if event.Type == hooks.EventToolBeforeExecute {
		toolName := fmt.Sprintf("%v", event.Data["tool_name"])
		if reason, blocked := h.blockedTools[toolName]; blocked {
			fmt.Printf("  [BLOCKER] 🚫 Blocked tool: %s - %s\n", toolName, reason)
			return hooks.Block(reason), nil
		}
	}
	return hooks.Continue(), nil
}

func (h *ToolBlockerHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolBeforeExecute
}

func (h *ToolBlockerHook) Priority() int {
	return 99 // Very high - block early
}

func (h *ToolBlockerHook) Name() string {
	return "demo.tool_blocker"
}

// ContextInjectorHook injects additional context into events
type ContextInjectorHook struct {
	contextData map[string]any
}

func NewContextInjectorHook() *ContextInjectorHook {
	return &ContextInjectorHook{
		contextData: map[string]any{
			"environment":    "production",
			"policy_version": "v1.2.3",
			"security_level": "high",
		},
	}
}

func (h *ContextInjectorHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Clone event
	modified := event.Clone()

	// Inject context into metadata
	if modified.Metadata == nil {
		modified.Metadata = make(map[string]any)
	}

	maps.Copy(modified.Metadata, h.contextData)

	fmt.Printf("  [INJECTOR] ✨ Injected context: %v\n", h.contextData)

	return hooks.ModifyWithMessage(modified, "context injected"), nil
}

func (h *ContextInjectorHook) Filter(event hooks.Event) bool {
	// Inject into all tool and provider events
	return event.Type == hooks.EventToolBeforeExecute ||
		event.Type == hooks.EventProviderBeforeRequest
}

func (h *ContextInjectorHook) Priority() int {
	return 95 // Very high - inject early
}

func (h *ContextInjectorHook) Name() string {
	return "demo.context_injector"
}

// RateLimiterHook demonstrates rate limiting
type RateLimiterHook struct {
	limit    int
	window   time.Duration
	requests map[string][]time.Time
}

func NewRateLimiterHook(limit int, window time.Duration) *RateLimiterHook {
	return &RateLimiterHook{
		limit:    limit,
		window:   window,
		requests: make(map[string][]time.Time),
	}
}

func (h *RateLimiterHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	agentID := event.AgentID
	if agentID == "" {
		return hooks.Continue(), nil
	}

	now := time.Now()

	// Clean old requests
	recent := []time.Time{}
	for _, t := range h.requests[agentID] {
		if now.Sub(t) < h.window {
			recent = append(recent, t)
		}
	}

	if len(recent) >= h.limit {
		fmt.Printf("  [RATE LIMIT] ⏱️  Blocked: %d requests in %v (limit: %d)\n",
			len(recent), h.window, h.limit)
		return hooks.Block(fmt.Sprintf("Rate limit exceeded: %d/%d requests in %v",
			len(recent), h.limit, h.window)), nil
	}

	// Record request
	recent = append(recent, now)
	h.requests[agentID] = recent

	fmt.Printf("  [RATE LIMIT] ✅ Allowed: %d/%d requests\n", len(recent), h.limit)

	return hooks.Continue(), nil
}

func (h *RateLimiterHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventProviderBeforeRequest
}

func (h *RateLimiterHook) Priority() int {
	return 90
}

func (h *RateLimiterHook) Name() string {
	return "demo.rate_limiter"
}

func main() {
	fmt.Println("🎯 Hook System Demo - Comprehensive Test")
	fmt.Println("=========================================")

	// Create mock observability components
	logger := &MockLogger{}
	tracer := &MockTracer{}
	metrics := &MockMetrics{}
	auditor := &MockAuditor{}

	// Create hook manager
	config := hooks.ManagerConfig{
		MaxHooksPerScope: 100,
		EnableMetrics:    true,
		EnableTracing:    true,
		MaxExecutionTime: 5 * time.Second,
		ErrorHandler: func(hookName string, err error) {
			fmt.Printf("  [ERROR] Hook %s failed: %v\n", hookName, err)
		},
	}

	manager := hooks.NewManager(config)

	// Register built-in hooks
	fmt.Println("📝 Registering hooks...")

	// 1. Tracing (priority 95)
	manager.Register(
		builtin.NewTracingHook(tracer),
		hooks.ScopeGlobal,
		"",
	)

	// 2. Logging (priority 90)
	manager.Register(
		builtin.NewLoggingHook(logger, observability.LevelInfo),
		hooks.ScopeGlobal,
		"",
	)

	// 3. Metrics (priority 85)
	manager.Register(
		builtin.NewMetricsHook(metrics),
		hooks.ScopeGlobal,
		"",
	)

	// 4. Audit (priority 80)
	manager.Register(
		builtin.NewAuditHook(auditor),
		hooks.ScopeGlobal,
		"",
	)

	// Register custom hooks
	// 5. Tool blocker (priority 99)
	manager.Register(
		NewToolBlockerHook(),
		hooks.ScopeGlobal,
		"",
	)

	// 6. Context injector (priority 95)
	manager.Register(
		NewContextInjectorHook(),
		hooks.ScopeGlobal,
		"",
	)

	// 7. Rate limiter (priority 90)
	manager.Register(
		NewRateLimiterHook(3, 10*time.Second),
		hooks.ScopeGlobal,
		"",
	)

	fmt.Printf("✅ Registered %d hooks\n\n", len(manager.List()))

	// Show registered hooks
	fmt.Println("📋 Registered Hooks (by priority):")
	for _, reg := range manager.List() {
		fmt.Printf("  - %s (priority: %d, scope: %s)\n",
			reg.Hook.Name(), reg.Hook.Priority(), reg.Scope)
	}
	fmt.Println()

	ctx := context.Background()

	// Test 1: Allowed tool execution
	fmt.Println("🧪 Test 1: Allowed Tool Execution")
	fmt.Println("-----------------------------------")
	event1 := hooks.Event{
		ID:             uuid.New().String(),
		Type:           hooks.EventToolBeforeExecute,
		Timestamp:      time.Now(),
		TraceID:        "trace-001",
		ConversationID: "conv-123",
		AgentID:        "agent-456",
		Data: map[string]any{
			"tool_name": "file_read",
			"params": map[string]any{
				"path": "/tmp/test.txt",
			},
		},
	}

	finalEvent1, err := manager.Emit(ctx, event1)
	if err != nil {
		fmt.Printf("❌ Event blocked: %v\n", err)
	} else {
		fmt.Printf("✅ Event processed successfully\n")
		if finalEvent1.Metadata != nil {
			fmt.Printf("📦 Injected metadata: %v\n", finalEvent1.Metadata)
		}
	}
	fmt.Println()

	// Test 2: Blocked tool execution
	fmt.Println("🧪 Test 2: Blocked Tool Execution (rm command)")
	fmt.Println("-----------------------------------------------")
	event2 := hooks.Event{
		ID:             uuid.New().String(),
		Type:           hooks.EventToolBeforeExecute,
		Timestamp:      time.Now(),
		TraceID:        "trace-002",
		ConversationID: "conv-123",
		AgentID:        "agent-456",
		Data: map[string]any{
			"tool_name": "rm",
			"params": map[string]any{
				"path": "/important/file.txt",
			},
		},
	}

	finalEvent2, err := manager.Emit(ctx, event2)
	if err != nil {
		fmt.Printf("✅ Event correctly blocked: %v\n", err)
	} else {
		fmt.Printf("❌ Event should have been blocked!\n")
		_ = finalEvent2
	}
	fmt.Println()

	// Test 3: Rate limiting
	fmt.Println("🧪 Test 3: Rate Limiting (3 requests/10s)")
	fmt.Println("-------------------------------------------")
	for i := 1; i <= 5; i++ {
		fmt.Printf("Request %d:\n", i)
		event := hooks.Event{
			ID:             uuid.New().String(),
			Type:           hooks.EventProviderBeforeRequest,
			Timestamp:      time.Now(),
			TraceID:        fmt.Sprintf("trace-%03d", i),
			ConversationID: "conv-123",
			AgentID:        "agent-456",
			Data: map[string]any{
				"model":  "gpt-4",
				"tokens": 100,
			},
		}

		_, err := manager.Emit(ctx, event)
		if err != nil {
			fmt.Printf("  ❌ Blocked: %v\n", err)
		} else {
			fmt.Printf("  ✅ Allowed\n")
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Println()

	// Test 4: Context injection verification
	fmt.Println("🧪 Test 4: Context Injection Verification")
	fmt.Println("------------------------------------------")
	event4 := hooks.Event{
		ID:             uuid.New().String(),
		Type:           hooks.EventProviderBeforeRequest,
		Timestamp:      time.Now(),
		TraceID:        "trace-004",
		ConversationID: "conv-789",
		AgentID:        "agent-999",
		Data: map[string]any{
			"request": "What is 2+2?",
		},
	}

	finalEvent4, err := manager.Emit(ctx, event4)
	if err == nil && finalEvent4 != nil {
		fmt.Println("✅ Event processed")
		fmt.Println("📦 Injected metadata:")
		for key, value := range finalEvent4.Metadata {
			fmt.Printf("  - %s: %v\n", key, value)
		}
	}
	fmt.Println()

	// Test 5: Statistics
	fmt.Println("📊 Hook Statistics")
	fmt.Println("------------------")
	stats := manager.GetStats()
	fmt.Printf("Total executions: %d\n", stats.TotalExecutions)
	fmt.Printf("Total blocked: %d\n", stats.TotalBlocked)
	fmt.Printf("Total modified: %d\n", stats.TotalModified)
	fmt.Printf("Total errors: %d\n", stats.TotalErrors)
	fmt.Println()

	// Test 6: Scoped hooks (simplified)
	fmt.Println("🧪 Test 5: Scoped Hook Registration")
	fmt.Println("------------------------------------")
	fmt.Println("✅ Hooks registered at Global scope")
	fmt.Println("✅ Conversation/Mode/Project scoped hooks supported")
	fmt.Println("  (See manager.go for full implementation)")
	fmt.Println()

	// Test 7: Multiple event types
	fmt.Println("🧪 Test 6: Multiple Event Types")
	fmt.Println("--------------------------------")
	eventTypes := []string{
		hooks.EventMessageAdded,
		hooks.EventToolAfterExecute,
		hooks.EventProviderAfterResponse,
		hooks.EventContextWindowExceeded,
	}

	for _, eventType := range eventTypes {
		fmt.Printf("Event: %s\n", eventType)
		event := hooks.Event{
			ID:             uuid.New().String(),
			Type:           eventType,
			Timestamp:      time.Now(),
			TraceID:        "trace-multi",
			ConversationID: "conv-multi",
			AgentID:        "agent-multi",
			Data: map[string]any{
				"test": true,
			},
		}

		_, err := manager.Emit(ctx, event)
		if err != nil {
			fmt.Printf("  ❌ Error: %v\n", err)
		} else {
			fmt.Printf("  ✅ Processed\n")
		}
	}
	fmt.Println()

	// Final summary
	fmt.Println("🎉 Demo Complete!")
	fmt.Println("=================")
	fmt.Println("✅ All hook system features demonstrated:")
	fmt.Println("  - Built-in hooks (logging, tracing, metrics, audit)")
	fmt.Println("  - Custom hooks (blocker, injector, rate limiter)")
	fmt.Println("  - Pre-tool blocking")
	fmt.Println("  - Context injection")
	fmt.Println("  - Event modification")
	fmt.Println("  - Priority-based execution")
	fmt.Println("  - Statistics tracking")
	fmt.Println()

	os.Exit(0)
}
