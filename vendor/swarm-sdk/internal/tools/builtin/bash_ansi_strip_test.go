package builtin

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// TestBashStripsANSIFromOutput proves that SGR/color escape sequences never
// reach the model in a tool result. rg/ls/git/grep auto-detect a terminal and
// emit color even though the caller never asked for it whenever the command
// runs under a PTY (the `script` buffering fallback used when stdbuf is
// unavailable — see bash_cmd_builder.go). Without stripping, the matched
// substring is wrapped in literal ESC[...m bytes that read as noise or, worse,
// get misparsed as if the wrapped text were missing (issue #303).
func TestBashStripsANSIFromOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell commands are Unix-only")
	}
	// Emit the exact SGR wrapping ripgrep produces around a match, plus a
	// non-SGR CSI cursor-movement sequence for good measure — both must be
	// gone from the final result, but the wrapped text itself must survive
	// byte-for-byte.
	cmd := `printf '\033[0m\033[1m\033[31mLAN peer registry\033[0m broadcasts on.\n'; printf 'plain line\033[2Kwith cursor erase\n'`
	result, err := newBashForTest().Run(context.Background(), BashParams{Command: cmd})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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

// TestBashStripsANSIFromFailureError proves the same for the error path
// (bashCommandFailedError), which is built independently from buildResult.
func TestBashStripsANSIFromFailureError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell commands are Unix-only")
	}
	cmd := `printf '\033[31mred-stdout\033[0m\n'; printf '\033[32mgreen-stderr\033[0m\n' >&2; exit 5`
	_, err := newBashForTest().Run(context.Background(), BashParams{Command: cmd})
	if err == nil {
		t.Fatal("expected non-zero exit to return an error")
	}
	if strings.ContainsRune(err.Error(), '\x1b') {
		t.Fatalf("error still contains raw ESC bytes: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "red-stdout") || !strings.Contains(err.Error(), "green-stderr") {
		t.Errorf("expected marker text to survive intact, got: %q", err.Error())
	}
}

// TestStripANSI_PreservesNonSGRText is a narrow unit test on stripANSI itself:
// only ESC[...m sequences are removed, and no adjacent text is consumed.
func TestStripANSI_PreservesNonSGRText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no escapes", "hello world", "hello world"},
		{"single SGR", "\x1b[31mred\x1b[0m", "red"},
		{"nested SGR", "\x1b[0m\x1b[1m\x1b[31mLAN peer registry\x1b[0m", "LAN peer registry"},
		{"empty params m", "\x1b[mtext", "text"},
		{"bare ESC at EOF", "trailing\x1b", "trailing\x1b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripANSI(tc.in)
			if got != tc.want {
				t.Errorf("stripANSI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
