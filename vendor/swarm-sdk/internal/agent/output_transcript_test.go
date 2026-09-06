package agent

import (
	"strings"
	"testing"
)

// TestFormatRecordsAsTranscript_CoalescesStreamingChunks verifies that the many
// streaming "append" content records (one per token) are coalesced back into a
// single readable block, and that the rendered output contains NO raw NDJSON.
func TestFormatRecordsAsTranscript_CoalescesStreamingChunks(t *testing.T) {
	// Simulate token streaming: "I'll explore the file." split across chunks.
	tokens := []string{"I'll", " explore", " the", " file", "."}
	var records []OutputRecord
	for i, tok := range tokens {
		records = append(records, OutputRecord{
			Type:    "content",
			Content: tok,
			Append:  i > 0, // first chunk starts the block, rest append
		})
	}
	records = append(records,
		OutputRecord{Type: "tool_call", Name: "Read"},
		OutputRecord{Type: "tool_result", Output: "package main"},
		OutputRecord{Type: "final", Content: "Done analyzing."},
	)

	out := FormatRecordsAsTranscript(records, false)

	// Must contain the reconstructed sentence and tool/final markers.
	if !strings.Contains(out, "I'll explore the file.") {
		t.Errorf("expected coalesced content, got:\n%s", out)
	}
	if !strings.Contains(out, "→ Read") {
		t.Errorf("expected tool-call line, got:\n%s", out)
	}
	if !strings.Contains(out, "Done analyzing.") {
		t.Errorf("expected final content, got:\n%s", out)
	}

	// Must NOT contain raw NDJSON noise.
	for _, bad := range []string{`"type":"content"`, `"append":true`, `"ts":`} {
		if strings.Contains(out, bad) {
			t.Errorf("transcript leaked raw JSON %q:\n%s", bad, out)
		}
	}
}

// TestFormatRecordsAsTranscript_ShrinksOutput is the core regression guard for
// the 382K–514K char blowup: the rendered transcript must be dramatically
// smaller than the raw NDJSON it was built from.
func TestFormatRecordsAsTranscript_ShrinksOutput(t *testing.T) {
	// 5000 single-character streaming chunks → huge NDJSON, tiny real text.
	var records []OutputRecord
	var rawSize int
	for i := 0; i < 5000; i++ {
		rec := OutputRecord{Type: "content", Content: "x", Append: i > 0, TS: 1780070585514}
		records = append(records, rec)
		// Approximate the raw NDJSON line size for this record.
		rawSize += len(`{"type":"content","ts":1780070585514,"content":"x","append":true}` + "\n")
	}

	out := FormatRecordsAsTranscript(records, false)

	// The real content is 5000 'x' chars; transcript should be ~that, not the
	// ~325KB of raw NDJSON.
	if len(out) > rawSize/10 {
		t.Errorf("transcript not shrunk enough: transcript=%d bytes, raw≈%d bytes", len(out), rawSize)
	}
	if !strings.Contains(out, strings.Repeat("x", 5000)) {
		t.Errorf("expected 5000 coalesced 'x' chars in transcript (len=%d)", len(out))
	}
}

// TestFormatRecordsAsTranscript_Errors surfaces tool and final errors.
func TestFormatRecordsAsTranscript_Errors(t *testing.T) {
	records := []OutputRecord{
		{Type: "tool_call", Name: "Bash"},
		{Type: "tool_result", Error: "command not found: bad-cmd"},
		{Type: "final", Error: "task failed"},
	}
	out := FormatRecordsAsTranscript(records, false)
	if !strings.Contains(out, "command not found: bad-cmd") {
		t.Errorf("expected tool error in transcript:\n%s", out)
	}
	if !strings.Contains(out, "task failed") {
		t.Errorf("expected final error in transcript:\n%s", out)
	}
}

// TestFormatRecordsAsTranscript_Empty returns empty string for no records.
func TestFormatRecordsAsTranscript_Empty(t *testing.T) {
	if got := FormatRecordsAsTranscript(nil, false); got != "" {
		t.Errorf("expected empty transcript for nil records, got %q", got)
	}
}

// TestFormatRecordsAsTranscript_ThinkingGate verifies thinking blocks are
// excluded by default and included when requested.
func TestFormatRecordsAsTranscript_ThinkingGate(t *testing.T) {
	records := []OutputRecord{
		{Type: "thinking", Content: "secret reasoning"},
		{Type: "content", Content: "visible answer"},
	}
	if out := FormatRecordsAsTranscript(records, false); strings.Contains(out, "secret reasoning") {
		t.Errorf("thinking should be excluded by default:\n%s", out)
	}
	if out := FormatRecordsAsTranscript(records, true); !strings.Contains(out, "secret reasoning") {
		t.Errorf("thinking should be included when requested:\n%s", out)
	}
}

// TestFormatFinalOrTranscript_ReturnsFinalOnly verifies that when a terminal
// "final" record exists, only the final message is returned — not the
// intermediate content/tool play-by-play (Claude Code finalizeAgentTool parity).
func TestFormatFinalOrTranscript_ReturnsFinalOnly(t *testing.T) {
	records := []OutputRecord{
		{Type: "content", Content: "Let me look around..."},
		{Type: "tool_call", Name: "Read"},
		{Type: "tool_result", Output: "package main"},
		{Type: "content", Content: "intermediate musing"},
		{Type: "final", Content: "Result: the bug is at foo.go:42."},
	}
	out := FormatFinalOrTranscript(records, false)

	if !strings.Contains(out, "Result: the bug is at foo.go:42.") {
		t.Errorf("expected final message, got:\n%s", out)
	}
	// Intermediate noise must NOT appear.
	for _, bad := range []string{"Let me look around", "→ Read", "intermediate musing", "package main"} {
		if strings.Contains(out, bad) {
			t.Errorf("final-only output leaked intermediate content %q:\n%s", bad, out)
		}
	}
}

// TestFormatFinalOrTranscript_FallsBackWhenNoFinal verifies that without a
// final record (agent still running / killed), the coalesced transcript is
// returned so partial progress is preserved.
func TestFormatFinalOrTranscript_FallsBackWhenNoFinal(t *testing.T) {
	records := []OutputRecord{
		{Type: "content", Content: "partial progress so far"},
		{Type: "tool_call", Name: "Bash"},
	}
	out := FormatFinalOrTranscript(records, false)
	if !strings.Contains(out, "partial progress so far") {
		t.Errorf("expected transcript fallback to include partial content:\n%s", out)
	}
	if !strings.Contains(out, "→ Bash") {
		t.Errorf("expected transcript fallback to include tool line:\n%s", out)
	}
}

// TestFormatFinalOrTranscript_FinalError surfaces a final error.
func TestFormatFinalOrTranscript_FinalError(t *testing.T) {
	records := []OutputRecord{
		{Type: "content", Content: "trying..."},
		{Type: "final", Error: "task failed: timeout"},
	}
	out := FormatFinalOrTranscript(records, false)
	if !strings.Contains(out, "task failed: timeout") {
		t.Errorf("expected final error, got:\n%s", out)
	}
}
