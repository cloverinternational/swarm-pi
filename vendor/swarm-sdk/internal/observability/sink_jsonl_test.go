package observability

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestJSONLSinkConcurrentWritesStableSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace-events.jsonl")
	sink, err := NewJSONLSink(path)
	if err != nil {
		t.Fatalf("NewJSONLSink() error = %v", err)
	}

	const total = 200
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range total {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = sink.WriteEvent(ctx, TraceEvent{
				EventType: EventTypeSpanStart,
				Timestamp: time.Now().UTC(),
				TraceID:   "trace-1",
				SpanID:    fmt.Sprintf("span-%d", i),
				Operation: "tool.exec",
			})
		}(i)
	}
	wg.Wait()

	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("os.Open() error = %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	sequenceSeen := make(map[int64]struct{}, total)
	count := 0
	for scanner.Scan() {
		count++
		var event TraceEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if event.Sequence <= 0 || event.Sequence > total {
			t.Fatalf("unexpected sequence value %d", event.Sequence)
		}
		sequenceSeen[event.Sequence] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error = %v", err)
	}

	if count != total {
		t.Fatalf("expected %d events, got %d", total, count)
	}
	if len(sequenceSeen) != total {
		t.Fatalf("expected %d unique sequences, got %d", total, len(sequenceSeen))
	}
}
