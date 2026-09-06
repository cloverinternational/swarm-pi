package analytics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestManualDispatcherDoesNotAutoFlush(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dispatcher, err := NewManualDispatcher(Config{
		CollectorURL:  server.URL,
		SpoolDir:      filepath.Join(t.TempDir(), "spool"),
		FlushInterval: time.Millisecond,
		BatchSize:     10,
	})
	if err != nil {
		t.Fatalf("NewManualDispatcher: %v", err)
	}
	if err := dispatcher.Enqueue(EventEnvelope{
		EventType:     "message.finalized",
		MachineIDHash: "machine",
		Payload:       map[string]any{"message_id": "msg-1"},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("manual dispatcher sent %d requests before explicit Flush", got)
	}
	stats, err := dispatcher.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Events.Files != 1 {
		t.Fatalf("spooled event files = %d, want 1", stats.Events.Files)
	}
	if err := dispatcher.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("requests after Flush = %d, want 1", got)
	}
	stats, err = dispatcher.Stats()
	if err != nil {
		t.Fatalf("Stats after Flush: %v", err)
	}
	if stats.Events.Files != 0 {
		t.Fatalf("spooled event files after Flush = %d, want 0", stats.Events.Files)
	}
}
