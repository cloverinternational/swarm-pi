package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolUseResultFromMetadata_NilMetadata(t *testing.T) {
	res := toolUseResultFromMetadata("hello", nil)
	if res.Stdout != "hello" {
		t.Fatalf("Stdout = %q, want %q", res.Stdout, "hello")
	}
	if res.ExitCode != nil {
		t.Errorf("ExitCode = %v, want nil", res.ExitCode)
	}
	if res.DurationMs != nil {
		t.Errorf("DurationMs = %v, want nil", res.DurationMs)
	}
	if res.OriginalOutputBytes != nil {
		t.Errorf("OriginalOutputBytes = %v, want nil", res.OriginalOutputBytes)
	}
	if res.ReturnedOutputBytes != nil {
		t.Errorf("ReturnedOutputBytes = %v, want nil", res.ReturnedOutputBytes)
	}
	if res.OutputContract != nil {
		t.Errorf("OutputContract = %v, want nil", res.OutputContract)
	}
	if res.Truncated {
		t.Errorf("Truncated = true, want false")
	}
	if res.TruncationMethod != "" {
		t.Errorf("TruncationMethod = %q, want empty", res.TruncationMethod)
	}
	if res.FullOutputPath != "" {
		t.Errorf("FullOutputPath = %q, want empty", res.FullOutputPath)
	}
}

func TestToolUseResultFromMetadata_FullMetadata(t *testing.T) {
	meta := map[string]any{
		"exit_code":             0,
		"duration_ms":           int64(12),
		"truncated":             true,
		"truncation_method":     "lines+tokens",
		"original_output_bytes": 1000,
		"returned_output_bytes": 200,
		"output_path":           "/tmp/x.txt",
		"output_contract":       map[string]any{"version": 1},
	}
	res := toolUseResultFromMetadata("out", meta)

	if res.Stdout != "out" {
		t.Fatalf("Stdout = %q, want %q", res.Stdout, "out")
	}
	if res.ExitCode == nil || *res.ExitCode != 0 {
		t.Errorf("ExitCode = %v, want 0", res.ExitCode)
	}
	if res.DurationMs == nil || *res.DurationMs != 12 {
		t.Errorf("DurationMs = %v, want 12", res.DurationMs)
	}
	if !res.Truncated {
		t.Errorf("Truncated = false, want true")
	}
	if res.TruncationMethod != "lines+tokens" {
		t.Errorf("TruncationMethod = %q, want %q", res.TruncationMethod, "lines+tokens")
	}
	if res.OriginalOutputBytes == nil || *res.OriginalOutputBytes != 1000 {
		t.Errorf("OriginalOutputBytes = %v, want 1000", res.OriginalOutputBytes)
	}
	if res.ReturnedOutputBytes == nil || *res.ReturnedOutputBytes != 200 {
		t.Errorf("ReturnedOutputBytes = %v, want 200", res.ReturnedOutputBytes)
	}
	if res.FullOutputPath != "/tmp/x.txt" {
		t.Errorf("FullOutputPath = %q, want %q", res.FullOutputPath, "/tmp/x.txt")
	}
	if res.OutputContract == nil {
		t.Fatalf("OutputContract = nil, want map")
	}
	if v, ok := res.OutputContract["version"].(int); !ok || v != 1 {
		t.Errorf("OutputContract[version] = %v, want 1", res.OutputContract["version"])
	}
}

func TestToolUseResultFromMetadata_Float64Coercion(t *testing.T) {
	// Numbers arriving via a JSON round-trip decode to float64.
	meta := map[string]any{
		"exit_code":   float64(2),
		"duration_ms": float64(50),
	}
	res := toolUseResultFromMetadata("out", meta)
	if res.ExitCode == nil || *res.ExitCode != 2 {
		t.Errorf("ExitCode = %v, want 2", res.ExitCode)
	}
	if res.DurationMs == nil || *res.DurationMs != 50 {
		t.Errorf("DurationMs = %v, want 50", res.DurationMs)
	}
}

func TestToolUseResultFromMetadata_JSONMarshal(t *testing.T) {
	// Full metadata: marshaled JSON contains the structured keys.
	meta := map[string]any{
		"exit_code":         0,
		"truncation_method": "lines+tokens",
		"output_contract":   map[string]any{"version": 1},
	}
	full := toolUseResultFromMetadata("out", meta)
	b, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("json.Marshal(full) error: %v", err)
	}
	js := string(b)
	if !strings.Contains(js, "output_contract") {
		t.Errorf("full JSON missing output_contract: %s", js)
	}
	if !strings.Contains(js, "truncation_method") {
		t.Errorf("full JSON missing truncation_method: %s", js)
	}

	// Nil metadata: omitempty drops the optional keys.
	empty := toolUseResultFromMetadata("out", nil)
	b2, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("json.Marshal(empty) error: %v", err)
	}
	js2 := string(b2)
	if strings.Contains(js2, "exit_code") {
		t.Errorf("nil-meta JSON unexpectedly contains exit_code: %s", js2)
	}
	if strings.Contains(js2, "output_contract") {
		t.Errorf("nil-meta JSON unexpectedly contains output_contract: %s", js2)
	}
}
