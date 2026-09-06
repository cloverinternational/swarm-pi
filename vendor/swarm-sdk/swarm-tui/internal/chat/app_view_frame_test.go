package chat

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestNormalizeFrameForTerminalPadsBlankTailLines(t *testing.T) {
	got := normalizeFrameForTerminal("top\n", 6, 4)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("line count = %d, want 4; frame=%q", len(lines), got)
	}
	for i := 1; i < len(lines); i++ {
		if lipgloss.Width(lines[i]) != 6 {
			t.Fatalf("tail line %d width = %d, want 6; line=%q frame=%q", i, lipgloss.Width(lines[i]), lines[i], got)
		}
	}
}

func TestNormalizeFrameForTerminalPadsShortContentLines(t *testing.T) {
	got := normalizeFrameForTerminal("/do", 8, 1)
	if lipgloss.Width(got) != 8 {
		t.Fatalf("frame width = %d, want 8; frame=%q", lipgloss.Width(got), got)
	}
	if strings.Contains(got, "/domain") {
		t.Fatalf("new frame unexpectedly contains stale longer slash command: %q", got)
	}
}

func TestNormalizeFrameForTerminalTrimsOverflow(t *testing.T) {
	got := normalizeFrameForTerminal("one\ntwo\nthree", 8, 2)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("line count = %d, want 2; frame=%q", len(lines), got)
	}
	if strings.TrimRight(lines[0], " ") != "one" || strings.TrimRight(lines[1], " ") != "two" {
		t.Fatalf("unexpected trimmed frame: %q", got)
	}
}
