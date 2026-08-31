package builtin

import (
	"strings"
	"testing"
	"time"
)

// TestBashTruncateOutput_SingleLineBlobIsTruncated covers the bug that was
// pushing multi-MB bash output into conversation history unchanged: when a
// command produced a single-line blob (no newlines), the line cap didn't
// fire and the byte cap was never enforced, so the whole payload went
// straight to the provider and blew the context window.
func TestBashTruncateOutput_SingleLineBlobIsTruncated(t *testing.T) {
	// 1 MB with no newlines
	blob := strings.Repeat("a", 1024*1024)
	out, path, truncated, _, _ := bashTruncateOutput(blob)
	if !truncated {
		t.Fatal("expected truncation for 1 MB single-line blob")
	}
	if path == "" {
		t.Error("expected full-output tempfile path to be set")
	}
	if len(out) >= len(blob) {
		t.Errorf("expected truncated output shorter than input, got %d (input %d)", len(out), len(blob))
	}
	if !strings.Contains(out, "tokens truncated") {
		t.Errorf("expected token-truncation notice, got tail %q", tail(out, 200))
	}
}

func TestBashTruncateOutput_SmallOutputUntouched(t *testing.T) {
	in := "hello\nworld\n"
	out, path, truncated, _, _ := bashTruncateOutput(in)
	if truncated {
		t.Error("small output should not be truncated")
	}
	if path != "" {
		t.Error("no tempfile should be written for small output")
	}
	if out != in {
		t.Errorf("output mutated: got %q want %q", out, in)
	}
}

func TestBashTruncateOutput_ManyLinesTruncated(t *testing.T) {
	// 3000 short lines trips the line cap (2000) without needing the byte cap.
	var b strings.Builder
	for range 3000 {
		b.WriteString("line\n")
	}
	out, _, truncated, _, _ := bashTruncateOutput(b.String())
	if !truncated {
		t.Fatal("expected truncation for 3000 lines")
	}
	if !strings.Contains(out, "lines truncated") {
		t.Errorf("expected line-truncation notice, got tail %q", tail(out, 200))
	}
}

// ─── P0.5 structured tool-output contract tests ─────────────────────────────

// TestBashTruncateOutput_NoTruncationMethodNone verifies the additive contract
// return values on the untouched path: short input must report truncated=false,
// method "none", origBytes equal to the input length, and no tempfile path.
func TestBashTruncateOutput_NoTruncationMethodNone(t *testing.T) {
	in := "hello\nworld\n"
	out, path, truncated, origBytes, method := bashTruncateOutput(in)
	if truncated {
		t.Error("short input should not be truncated")
	}
	if method != "none" {
		t.Errorf("method = %q, want %q", method, "none")
	}
	if origBytes != len(in) {
		t.Errorf("origBytes = %d, want %d", origBytes, len(in))
	}
	if path != "" {
		t.Errorf("outputPath = %q, want empty", path)
	}
	if out != in {
		t.Errorf("output mutated: got %q want %q", out, in)
	}
}

// TestBashTruncateOutput_LineCapMethod builds >2000 short lines so only the
// line cap fires; method must contain "lines", origBytes must equal the
// original size, and the full-output tempfile path must be set.
func TestBashTruncateOutput_LineCapMethod(t *testing.T) {
	var b strings.Builder
	for range 3000 {
		b.WriteString("x\n")
	}
	original := b.String()
	_, path, truncated, origBytes, method := bashTruncateOutput(original)
	if !truncated {
		t.Fatal("expected truncation for 3000 lines")
	}
	if !strings.Contains(method, "lines") {
		t.Errorf("method = %q, want it to contain %q", method, "lines")
	}
	if origBytes != len(original) {
		t.Errorf("origBytes = %d, want %d", origBytes, len(original))
	}
	if path == "" {
		t.Error("expected full-output tempfile path to be set")
	}
}

// TestBashTruncateOutput_TokenCapMethod builds a SINGLE very long line (no
// newlines) that exceeds the ~12500 token cap so only the token cap fires;
// method must contain "tokens".
func TestBashTruncateOutput_TokenCapMethod(t *testing.T) {
	// ~1 MB single-line blob: no newlines -> line cap cannot fire, token cap does.
	blob := strings.Repeat("a", 1024*1024)
	_, path, truncated, origBytes, method := bashTruncateOutput(blob)
	if !truncated {
		t.Fatal("expected truncation for oversized single-line blob")
	}
	if !strings.Contains(method, "tokens") {
		t.Errorf("method = %q, want it to contain %q", method, "tokens")
	}
	if strings.Contains(method, "lines") {
		t.Errorf("method = %q, single-line blob should not trip the line cap", method)
	}
	if origBytes != len(blob) {
		t.Errorf("origBytes = %d, want %d", origBytes, len(blob))
	}
	if path == "" {
		t.Error("expected full-output tempfile path to be set")
	}
}

// TestBuildResultOutputContract drives buildResult with a large stdout that
// triggers truncation and asserts the versioned output_contract metadata map
// is populated so downstream can attribute the truncation.
func TestBuildResultOutputContract(t *testing.T) {
	// Large single-line stdout to guarantee truncation.
	stdout := strings.Repeat("a", 1024*1024)
	result := buildResult(0, 5*time.Millisecond, 0, 0, stdout, "", "demo", "echo big", false)

	if got := result.Metadata["truncation_method"]; got == "none" {
		t.Errorf("truncation_method = %q, want non-\"none\"", got)
	}

	raw, ok := result.Metadata["output_contract"]
	if !ok {
		t.Fatal("output_contract metadata missing")
	}
	contract, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("output_contract is %T, want map[string]any", raw)
	}
	if contract["version"] != 1 {
		t.Errorf("contract version = %v, want 1", contract["version"])
	}
	if contract["truncated"] != true {
		t.Errorf("contract truncated = %v, want true", contract["truncated"])
	}
	orig, _ := contract["original_output_bytes"].(int)
	ret, _ := contract["returned_output_bytes"].(int)
	if !(orig > ret) {
		t.Errorf("expected original_output_bytes (%d) > returned_output_bytes (%d)", orig, ret)
	}
	if p, _ := contract["full_output_path"].(string); p == "" {
		t.Error("expected full_output_path to be non-empty on truncation")
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
