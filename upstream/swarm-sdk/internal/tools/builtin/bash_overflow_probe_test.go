package builtin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestProbe_BashLargeOutputSavedToTempFile proves end-to-end that when a bash
// command produces more output than the truncation threshold, the full output
// is persisted to a temp file and the agent is told exactly where to find it.
//
// This is the "probe" — it runs real commands through the BashTool, checks the
// returned ToolResult for both the truncation notice in the output text AND
// the output_path in metadata, and then reads the temp file to verify the full
// content is recoverable.
func TestProbe_BashLargeOutputSavedToTempFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	tool := newBashForTest()
	ctx := context.Background()

	params := BashParams{
		Command:        "for i in $(seq 3000); do echo \"line-$i\"; done",
		TimeoutSeconds: 60,
		Description:    "generate large output",
	}

	result, err := tool.Run(ctx, params)
	if err != nil {
		t.Fatalf("BashTool.Run error: %v", err)
	}

	// ── Check 1: output text must mention truncation and the temp file path ──
	output := result.Output
	if !strings.Contains(output, "truncated") {
		t.Errorf("expected truncation notice in output, got tail: %q", tail(output, 300))
	}
	if !strings.Contains(output, "Full output saved to:") {
		t.Errorf("expected 'Full output saved to:' in output, got tail: %q", tail(output, 300))
	}

	// ── Check 2: metadata must have output_path and truncated ──
	outputPath, _ := result.Metadata["output_path"].(string)
	if outputPath == "" {
		t.Fatal("expected metadata['output_path'] to be set when output is truncated")
	}
	if truncated, ok := result.Metadata["truncated"].(bool); !ok || !truncated {
		t.Error("expected metadata['truncated'] = true")
	}

	// ── Check 3: the temp file must exist and contain the full output ──
	fullBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("cannot read full-output temp file at %s: %v", outputPath, err)
	}
	fullContent := string(fullBytes)

	// The full file must have all 3000 lines
	lineCount := strings.Count(fullContent, "\n")
	if lineCount < 3000 {
		t.Errorf("expected full file to have at least 3000 lines, got %d", lineCount)
	}
	// Verify the first and last lines are present
	if !strings.Contains(fullContent, "line-1\n") {
		t.Error("expected full file to contain 'line-1'")
	}
	if !strings.Contains(fullContent, "line-3000") {
		t.Error("expected full file to contain 'line-3000'")
	}

	t.Logf("✓ probe passed: full output saved to %s (%d bytes, %d lines)",
		outputPath, len(fullBytes), lineCount)
}

// TestProbe_BashMetadataNotBloatedWhenTruncated proves that when output is
// truncated, the metadata["stdout"] and metadata["stderr"] strings are NOT
// the full multi-KB/multi-MB blobs — they're replaced with a pointer to the
// temp file. This prevents the metadata from blowing out serialization.
func TestProbe_BashMetadataNotBloatedWhenTruncated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	tool := newBashForTest()
	ctx := context.Background()

	params := BashParams{
		Command:        "for i in $(seq 3000); do echo \"line-$i\"; done",
		TimeoutSeconds: 60,
	}

	result, err := tool.Run(ctx, params)
	if err != nil {
		t.Fatalf("BashTool.Run error: %v", err)
	}

	// When truncated, metadata["stdout"] must NOT contain the raw full output.
	stdoutMeta, _ := result.Metadata["stdout"].(string)
	if strings.HasPrefix(stdoutMeta, "line-") {
		t.Errorf("metadata['stdout'] should be a pointer, not raw output; got %d chars: %q",
			len(stdoutMeta), tail(stdoutMeta, 80))
	}
	if !strings.Contains(stdoutMeta, "saved to file") {
		t.Errorf("expected metadata['stdout'] to reference file, got: %q", stdoutMeta)
	}

	t.Logf("✓ probe passed: metadata['stdout'] = %q (no bloat)", stdoutMeta)
}

// TestProbe_BashSmallOutputNotTruncated proves that small command output
// passes through unchanged — no temp file, no truncation, and metadata
// contains the actual stdout/stderr strings.
func TestProbe_BashSmallOutputNotTruncated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	tool := newBashForTest()
	ctx := context.Background()

	params := BashParams{
		Command:        "echo 'hello world'",
		TimeoutSeconds: 60,
	}

	result, err := tool.Run(ctx, params)
	if err != nil {
		t.Fatalf("BashTool.Run error: %v", err)
	}

	// Small output should NOT be truncated
	if truncated, ok := result.Metadata["truncated"].(bool); ok && truncated {
		t.Error("small output should not be marked as truncated")
	}
	if _, hasOutputPath := result.Metadata["output_path"]; hasOutputPath {
		t.Error("small output should not have output_path in metadata")
	}

	// metadata["stdout"] should contain the actual content
	stdoutMeta, _ := result.Metadata["stdout"].(string)
	if !strings.Contains(stdoutMeta, "hello world") {
		t.Errorf("expected metadata['stdout'] to contain actual output, got: %q", stdoutMeta)
	}

	t.Logf("✓ probe passed: small output not truncated, stdout in metadata = %q", stdoutMeta)
}

// TestProbe_BashStreamingLargeOutputSavedToTempFile proves the streaming path
// also saves overflow to a temp file when the in-memory cap is exceeded.
func TestProbe_BashStreamingLargeOutputSavedToTempFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	tool := newBashForTest()
	ctx := context.Background()

	var streamChunks []string
	params := BashParams{
		Command:        "for i in $(seq 3000); do echo \"stream-line-$i\"; done",
		TimeoutSeconds: 60,
	}

	result, err := tool.RunStreaming(ctx, params, func(chunk, stream string) {
		streamChunks = append(streamChunks, chunk)
	})
	if err != nil {
		t.Fatalf("RunStreaming error: %v", err)
	}

	// ── Check 1: streaming chunks must have been delivered ──
	if len(streamChunks) == 0 {
		t.Fatal("expected streaming chunks to be delivered")
	}

	// ── Check 2: result output must mention truncation ──
	output := result.Output
	if !strings.Contains(output, "truncated") {
		t.Errorf("expected truncation notice in streaming result, got tail: %q", tail(output, 300))
	}

	// ── Check 3: output_path must be in metadata ──
	outputPath, _ := result.Metadata["output_path"].(string)
	if outputPath == "" {
		t.Fatal("expected metadata['output_path'] to be set in streaming result")
	}

	// ── Check 4: the temp file must exist and have the full content ──
	fullBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("cannot read full-output temp file at %s: %v", outputPath, err)
	}
	fullContent := string(fullBytes)
	if !strings.Contains(fullContent, "stream-line-1") {
		t.Error("expected full file to contain 'stream-line-1'")
	}
	if !strings.Contains(fullContent, "stream-line-3000") {
		t.Error("expected full file to contain 'stream-line-3000'")
	}

	lineCount := strings.Count(fullContent, "\n")
	t.Logf("✓ probe passed: streaming full output saved to %s (%d bytes, %d lines, %d chunks)",
		outputPath, len(fullBytes), lineCount, len(streamChunks))
}

// TestProbe_BashTruncationMessageReferencesSwarmTools proves the truncation
// notice tells the agent to use actual Swarm tool names (file_read, grep)
// rather than generic/incorrect names.
func TestProbe_BashTruncationMessageReferencesSwarmTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	tool := newBashForTest()
	ctx := context.Background()

	params := BashParams{
		Command:        "for i in $(seq 3000); do echo \"line-$i\"; done",
		TimeoutSeconds: 60,
	}

	result, err := tool.Run(ctx, params)
	if err != nil {
		t.Fatalf("BashTool.Run error: %v", err)
	}

	output := result.Output

	// The overflow escape hatch must point at SHELL idioms. The Read/grep tools
	// it used to name were removed, so naming them again would hand the model a
	// dangling instruction at exactly the moment it is already drowning in output.
	for _, want := range []string{"sed -n", "rg "} {
		if !strings.Contains(output, want) {
			t.Errorf("truncation notice should mention %q, got tail: %q", want, tail(output, 400))
		}
	}
	// Must NOT resurrect any removed tool name.
	for _, bad := range []string{"file_read", "Use Grep", "Read with offset", "mode='search'"} {
		if strings.Contains(output, bad) {
			t.Errorf("truncation notice references removed tool %q: %q", bad, tail(output, 400))
		}
	}
	t.Logf("✓ probe passed: truncation message points at shell idioms, not removed tools")
}

// TestProbe_BashFullOutputTempFileIsReadableWithFileRead proves the temp file
// is in a format that the file_read tool can read — line-by-line with proper
// newlines, no binary data, and the file is in the expected directory.
func TestProbe_BashFullOutputTempFileIsReadableWithFileRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	tool := newBashForTest()
	ctx := context.Background()

	params := BashParams{
		Command:        "for i in $(seq 2500); do echo \"data-$i\"; done",
		TimeoutSeconds: 60,
	}

	result, err := tool.Run(ctx, params)
	if err != nil {
		t.Fatalf("BashTool.Run error: %v", err)
	}

	outputPath, _ := result.Metadata["output_path"].(string)
	if outputPath == "" {
		t.Fatal("expected output_path in metadata")
	}

	// ── Check 1: file must be in the standard swarm-tool-output directory ──
	expectedDir := filepath.Join(os.TempDir(), "swarm-tool-output")
	if !strings.HasPrefix(outputPath, expectedDir) {
		t.Errorf("expected temp file under %s, got %s", expectedDir, outputPath)
	}

	// ── Check 2: file name should match pattern bash-full-*.txt ──
	base := filepath.Base(outputPath)
	if !strings.HasPrefix(base, "bash-full-") || !strings.HasSuffix(base, ".txt") {
		t.Errorf("expected file name pattern 'bash-full-*.txt', got %s", base)
	}

	// ── Check 3: file content is valid UTF-8 text with clean line endings ──
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("cannot read temp file: %v", err)
	}

	// No carriage returns (not Windows-style)
	if strings.Contains(string(content), "\r") {
		t.Error("temp file should not contain carriage returns")
	}

	// Lines should end with \n
	lines := strings.Split(string(content), "\n")
	nonEmptyLines := 0
	for _, l := range lines {
		if l != "" {
			nonEmptyLines++
		}
	}
	if nonEmptyLines < 2500 {
		t.Errorf("expected at least 2500 non-empty lines, got %d", nonEmptyLines)
	}

	t.Logf("✓ probe passed: temp file is well-formed at %s (%d lines, pattern: %s)",
		outputPath, nonEmptyLines, base)
}
