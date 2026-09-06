package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

type capturedHistoryEvent struct {
	level  observability.Level
	event  string
	fields []observability.Field
}

type historyCaptureLogger struct {
	events []capturedHistoryEvent
}

func (logger *historyCaptureLogger) Log(_ context.Context, level observability.Level, event string, fields ...observability.Field) {
	logger.events = append(logger.events, capturedHistoryEvent{level: level, event: event, fields: fields})
}

func (logger *historyCaptureLogger) Trace(ctx context.Context, event string, fields ...observability.Field) {
	logger.Log(ctx, observability.LevelTrace, event, fields...)
}

func (logger *historyCaptureLogger) Debug(ctx context.Context, event string, fields ...observability.Field) {
	logger.Log(ctx, observability.LevelDebug, event, fields...)
}

func (logger *historyCaptureLogger) Info(ctx context.Context, event string, fields ...observability.Field) {
	logger.Log(ctx, observability.LevelInfo, event, fields...)
}

func (logger *historyCaptureLogger) Warn(ctx context.Context, event string, fields ...observability.Field) {
	logger.Log(ctx, observability.LevelWarn, event, fields...)
}

func (logger *historyCaptureLogger) Error(ctx context.Context, event string, fields ...observability.Field) {
	logger.Log(ctx, observability.LevelError, event, fields...)
}

func TestTranslateMessagesHistoryLoggingIsBounded(t *testing.T) {
	run := func(t *testing.T, count int) int {
		t.Helper()
		messages := make([]*conversation.Message, count)
		for i := range messages {
			role := conversation.RoleUser
			if i%2 == 1 {
				role = conversation.RoleAssistant
			}
			messages[i] = &conversation.Message{
				ID:      fmt.Sprintf("message-%08d-with-an-intentionally-long-identifier", i),
				Role:    role,
				Content: "content",
			}
		}

		logger := &historyCaptureLogger{}
		if _, err := translateMessagesCore(context.Background(), messages, false, false, logger, true); err != nil {
			t.Fatalf("translateMessagesCore(%d): %v", count, err)
		}

		summaryEvents := 0
		summaryBytes := 0
		for _, event := range logger.events {
			switch event.event {
			case "translate.messages.input_message", "translate.messages.output_message":
				t.Fatalf("found removed per-message event %q", event.event)
			case "translate.messages.summary":
				summaryEvents++
				fields := make(map[string]any, len(event.fields))
				for _, field := range event.fields {
					fields[field.Key] = field.Value
				}
				encoded, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				summaryBytes += len(encoded)
			}
		}
		if summaryEvents != 1 {
			t.Fatalf("summary event count for %d messages = %d, want 1", count, summaryEvents)
		}
		return summaryBytes
	}

	smallBytes := run(t, 1)
	largeBytes := run(t, 10_000)
	if largeBytes > 512 {
		t.Fatalf("10k-message translation summary is %d bytes, want <= 512", largeBytes)
	}
	if largeBytes-smallBytes > 32 {
		t.Fatalf("translation summary grew by %d bytes, want <= 32", largeBytes-smallBytes)
	}
}
