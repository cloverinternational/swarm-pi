package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// auditTestObserver implements Observer for audit testing
type auditTestObserver struct{}

func (a *auditTestObserver) Observe(ctx context.Context, transcript string) error {
	// No-op observer - just returns nil
	return nil
}

// TestStreamingSteeringDriverAuditLog verifies that audit logging is properly
// integrated into the streaming steering driver.
func TestStreamingSteeringDriverAuditLog(t *testing.T) {
	// Create a temp directory for the audit log
	tmpDir := t.TempDir()
	auditPath := filepath.Join(tmpDir, "audit.jsonl")

	// Create a fake observer that just returns nil
	fakeObserver := &auditTestObserver{}

	// Create driver with audit log enabled
	cfg := SteeringDriverConfig{
		Mode:            SteeringModeStream,
		EventBufferSize: 16,
		FlushCount:      2,
		FlushAge:        100 * time.Millisecond,
		AuditLogPath:    auditPath,
		DriftEnabled:    true,
		Target:          NewDefaultSteeringTarget(),
		Observer:        fakeObserver, // Need observer for flush to run
	}

	driver := NewStreamingSteeringDriver(cfg)

	// Start the driver
	ctx := context.Background()
	if err := driver.Start(ctx); err != nil {
		t.Fatalf("Failed to start driver: %v", err)
	}
	defer driver.Stop()

	// Enqueue some events
	ev1 := hooks.Event{
		Type:      "tool.before_execute",
		Timestamp: time.Now(),
		Data: map[string]any{
			"tool_name": "Read",
			"params":    map[string]any{"file_path": "test.go"},
		},
	}
	ev2 := hooks.Event{
		Type:      "tool.after_execute",
		Timestamp: time.Now(),
		Data: map[string]any{
			"tool_name": "Read",
			"output":    "file contents",
		},
	}

	driver.Enqueue(ev1)
	driver.Enqueue(ev2)

	// Wait for flush to happen (either by count or age)
	time.Sleep(200 * time.Millisecond)

	// Verify flush happened
	if driver.FlushedTotal() == 0 {
		t.Error("Expected at least one flush")
	}

	// Stop driver to ensure audit log is closed
	if err := driver.Stop(); err != nil {
		t.Errorf("Failed to stop driver: %v", err)
	}

	// Verify audit log file exists and contains expected records
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("Failed to read audit log: %v", err)
	}

	t.Logf("Audit log contents: %q", string(data))

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		t.Fatal("Audit log is empty")
	}

	t.Logf("Found %d lines in audit log", len(lines))

	// Parse and verify at least one flush record
	foundFlush := false
	for _, line := range lines {
		if line == "" {
			continue
		}

		var rec AuditRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("Failed to parse audit record: %v", err)
			continue
		}

		// Debug: print what we found
		t.Logf("Found record: kind=%s, flushed=%d, dropped=%d", rec.Kind, rec.Flushed, rec.Dropped)

		// Check for required fields
		if rec.Timestamp == "" {
			t.Error("Audit record missing timestamp")
		}
		if rec.Kind == "" {
			t.Error("Audit record missing kind")
		}

		if rec.Kind == "flush" {
			foundFlush = true
			if rec.Flushed == 0 {
				t.Error("Flush record has zero flushed count")
			}
		}
	}

	if !foundFlush {
		t.Error("No flush record found in audit log")
	}
}

// TestStreamingSteeringDriverNoAuditLog verifies the driver works correctly
// when audit logging is disabled (empty path).
func TestStreamingSteeringDriverNoAuditLog(t *testing.T) {
	cfg := SteeringDriverConfig{
		Mode:            SteeringModeStream,
		EventBufferSize: 16,
		FlushCount:      2,
		FlushAge:        100 * time.Millisecond,
		AuditLogPath:    "", // No audit log
		Target:          NewDefaultSteeringTarget(),
	}

	driver := NewStreamingSteeringDriver(cfg)

	ctx := context.Background()
	if err := driver.Start(ctx); err != nil {
		t.Fatalf("Failed to start driver: %v", err)
	}
	defer driver.Stop()

	// Enqueue events
	ev := hooks.Event{
		Type:      "tool.before_execute",
		Timestamp: time.Now(),
		Data: map[string]any{
			"tool_name": "Bash",
		},
	}

	driver.Enqueue(ev)
	driver.Enqueue(ev)

	// Wait for flush
	time.Sleep(200 * time.Millisecond)

	// Verify driver still works without audit log
	if driver.FlushedTotal() == 0 {
		t.Error("Expected at least one flush")
	}
}
