package agent_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	agent "github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

type capturedLog struct {
	level observability.Level
	event string
}

type captureLogger struct {
	mu   sync.Mutex
	logs []capturedLog
}

func (l *captureLogger) Log(_ context.Context, level observability.Level, event string, _ ...observability.Field) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, capturedLog{level: level, event: event})
}
func (l *captureLogger) Trace(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelTrace, event, fields...)
}
func (l *captureLogger) Debug(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelDebug, event, fields...)
}
func (l *captureLogger) Info(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelInfo, event, fields...)
}
func (l *captureLogger) Warn(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelWarn, event, fields...)
}
func (l *captureLogger) Error(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelError, event, fields...)
}

func TestMessagePersistenceFailureLogsAtError(t *testing.T) {
	p := &completionConfirmProvider{reason: provider.FinishReasonStop}
	logger := &captureLogger{}
	ag, err := agent.New(agent.Config{
		Definition: &agent.Definition{
			ID:       "persistence-logging-test",
			Name:     "Persistence Logging Test",
			Provider: p.Name(),
			Model:    "mock-model",
		},
		Provider: p,
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	ag.SetMessageCallback(func(context.Context, *conversation.Message) error {
		return errors.New("disk full")
	})

	if _, err := ag.Execute(context.Background(), agent.ExecuteRequest{
		Message:  "persist this response",
		MaxTurns: 1,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	logger.mu.Lock()
	defer logger.mu.Unlock()
	for _, entry := range logger.logs {
		if entry.event == "agent.message_callback_failed" {
			if entry.level != observability.LevelError {
				t.Fatalf("persistence failure level = %v, want error", entry.level)
			}
			return
		}
	}
	t.Fatalf("missing loud persistence failure log; entries: %#v", logger.logs)
}
