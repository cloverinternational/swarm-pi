package bgprocess

import (
	"strings"
	"testing"
	"time"
)

// TestCompletedProcessResultStripsANSI proves that SGR/cursor-control escape
// codes captured from a subprocess (e.g. ripgrep auto-color-detecting a PTY
// allocated by the stdbuf-unavailable `script` fallback) never reach the
// model-facing ToolResult text, while the text they wrap survives intact
// (issue #303).
func TestCompletedProcessResultStripsANSI(t *testing.T) {
	raw := "\x1b[0m\x1b[1m\x1b[31mLAN peer registry\x1b[0m broadcasts on.\n" +
		"plain line\x1b[2Kwith cursor erase\n"
	result := completedProcessResult(&ProcessResult{
		ExitCode: 0,
		Output:   []byte(raw),
		Duration: 5 * time.Millisecond,
	})
	if result == nil {
		t.Fatal("expected a ToolResult")
	}
	if strings.ContainsRune(result.Output, '\x1b') {
		t.Fatalf("result still contains raw ESC bytes: %q", result.Output)
	}
	if !strings.Contains(result.Output, "LAN peer registry broadcasts on.") {
		t.Errorf("expected the matched substring to survive intact, got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "plain linewith cursor erase") {
		t.Errorf("expected text around a non-SGR CSI sequence to survive intact, got: %q", result.Output)
	}
}

// TestCompletedProcessResultEmptyANSIOnlyOutput proves that output which is
// *purely* escape codes (stripping to empty) still gets the normal
// "no output" placeholder rather than surfacing an empty CDATA blob.
func TestCompletedProcessResultEmptyANSIOnlyOutput(t *testing.T) {
	result := completedProcessResult(&ProcessResult{
		ExitCode: 0,
		Output:   []byte("\x1b[0m\x1b[K"),
	})
	if result.Output != "Command completed successfully (no output)" {
		t.Errorf("expected the no-output placeholder, got: %q", result.Output)
	}
}
