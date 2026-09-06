package bash

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
)

// ── Width correctness across all viewport sizes ─────────────────────────────

// verifyAllLinesWidth checks every rendered line does not EXCEED targetWidth visual chars.
// Flat rendering (no box padding) produces lines that may be shorter — that is fine.
// Returns all failures as a slice of error strings.
func verifyAllLinesWidth(t *testing.T, lines []string, targetWidth int, label string) {
	t.Helper()
	for i, l := range lines {
		stripped := shared.StripANSI(l)
		w := shared.PrintableWidth(stripped)
		if w > targetWidth {
			t.Errorf("[%s] line %d: visual width %d > %d (overflow)\n  stripped: %q", label, i, w, targetWidth, stripped)
		}
	}
}

func TestStress_WidthSweep_WithDuration(t *testing.T) {
	// Sweep widths from minimum (40) to very wide (300)
	for _, width := range []int{40, 41, 42, 50, 60, 70, 80, 100, 120, 150, 200, 300} {
		ctx := &toolrender.RenderContext{
			ToolName: "Bash",
			Output:   "hello world\nsecond line",
			Params:   map[string]any{"command": "echo hello"},
			Metadata: map[string]any{
				"exit_code":   float64(0),
				"duration_ms": float64(2300),
			},
			Width:   width,
			BgColor: "#000000",
		}
		lines := renderTerminal("echo hello", "hello world\nsecond line", ctx, width, "#000000")
		verifyAllLinesWidth(t, lines, width, fmt.Sprintf("w=%d+dur", width))
	}
}

func TestStress_WidthSweep_NoDuration(t *testing.T) {
	for _, width := range []int{40, 50, 60, 80, 100, 200} {
		ctx := &toolrender.RenderContext{
			ToolName: "Bash",
			Output:   "ok",
			Params:   map[string]any{"command": "echo ok"},
			Metadata: map[string]any{"exit_code": float64(0)},
			Width:    width,
			BgColor:  "#000000",
		}
		lines := renderTerminal("echo ok", "ok", ctx, width, "#000000")
		verifyAllLinesWidth(t, lines, width, fmt.Sprintf("w=%d+nodur", width))
	}
}

func TestStress_WidthSweep_Running(t *testing.T) {
	for _, width := range []int{40, 50, 60, 80, 100, 200} {
		ctx := &toolrender.RenderContext{
			ToolName: "Bash",
			Output:   "partial output",
			Params:   map[string]any{"command": "make build"},
			Width:    width,
			BgColor:  "#000000",
			IsActive: true,
		}
		lines := renderTerminal("make build", "partial output", ctx, width, "#000000")
		verifyAllLinesWidth(t, lines, width, fmt.Sprintf("w=%d+running", width))
	}
}

// ── Width below minimum ─────────────────────────────────────────────────────

func TestStress_VeryNarrowWidth(t *testing.T) {
	// Widths below 40 should be clamped to 40
	for _, width := range []int{10, 20, 30, 39} {
		ctx := &toolrender.RenderContext{
			ToolName: "Bash",
			Output:   "ok",
			Params:   map[string]any{"command": "echo ok"},
			Metadata: map[string]any{"exit_code": float64(0)},
			Width:    width,
			BgColor:  "#000000",
		}
		lines := renderTerminal("echo ok", "ok", ctx, width, "#000000")
		// Should clamp to 40
		verifyAllLinesWidth(t, lines, 40, fmt.Sprintf("w=%d+clamped", width))
	}
}

// ── Long duration string vs narrow width ────────────────────────────────────

func TestStress_LongDurationNarrowWidth(t *testing.T) {
	// Duration "12m 34s" + title "Shell" on a 40-wide box — tight fit
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "ok",
		Params:   map[string]any{"command": "sleep 754"},
		Metadata: map[string]any{
			"exit_code":   float64(0),
			"duration_ms": float64(754000), // 12m 34s
		},
		Width:   40,
		BgColor: "#000000",
	}
	lines := renderTerminal("sleep 754", "ok", ctx, 40, "#000000")
	verifyAllLinesWidth(t, lines, 40, "longdur+narrow")
}

// ── Long command truncation ─────────────────────────────────────────────────

func TestStress_VeryLongCommand(t *testing.T) {
	longCmd := "find / -name '*.go' -exec grep -l 'interface{}' {} \\; | sort | uniq -c | sort -rn | head -20 | awk '{print $2}' | xargs wc -l"
	for _, width := range []int{40, 60, 80, 120} {
		ctx := &toolrender.RenderContext{
			ToolName: "Bash",
			Output:   "result",
			Params:   map[string]any{"command": longCmd},
			Metadata: map[string]any{"exit_code": float64(0)},
			Width:    width,
			BgColor:  "#000000",
		}
		lines := renderTerminal(longCmd, "result", ctx, width, "#000000")
		verifyAllLinesWidth(t, lines, width, fmt.Sprintf("longcmd+w=%d", width))
	}
}

// ── Multi-line command (newlines in command string) ──────────────────────────

func TestStress_MultilineCommand(t *testing.T) {
	cmd := "if true; then\n  echo hello\nfi"
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "hello",
		Params:   map[string]any{"command": cmd},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal(cmd, "hello", ctx, 80, "#000000")
	verifyAllLinesWidth(t, lines, 80, "multiline-cmd")

	// The command belongs to the tool header and must not be duplicated in the
	// result block. The result still preserves command output.
	foundOutput := false
	for _, l := range lines {
		stripped := shared.StripANSI(l)
		if strings.Contains(stripped, "if true") || strings.Contains(stripped, "echo hello") {
			t.Errorf("result block should not repeat multiline command: %q", stripped)
		}
		if strings.Contains(stripped, "hello") {
			foundOutput = true
		}
	}
	if !foundOutput {
		t.Error("expected command output to be rendered")
	}
}

// ── Long output lines (should clip, not wrap) ───────────────────────────────

func TestStress_LongOutputLines(t *testing.T) {
	// Lines of varying lengths, some way over the box width
	longLines := []string{
		strings.Repeat("A", 50),
		strings.Repeat("B", 100),
		strings.Repeat("C", 200),
		strings.Repeat("D", 500),
		"short",
	}
	output := strings.Join(longLines, "\n")
	for _, width := range []int{40, 60, 80, 120} {
		ctx := &toolrender.RenderContext{
			ToolName: "Bash",
			Output:   output,
			Params:   map[string]any{"command": "cat bigfile"},
			Metadata: map[string]any{"exit_code": float64(0)},
			Width:    width,
			BgColor:  "#000000",
		}
		lines := renderTerminal("cat bigfile", output, ctx, width, "#000000")
		verifyAllLinesWidth(t, lines, width, fmt.Sprintf("longlines+w=%d", width))
	}
}

// ── Output with ANSI color codes ────────────────────────────────────────────

func TestStress_ANSIColoredOutput(t *testing.T) {
	// Simulate git diff / grep --color output
	ansiOutput := "\x1b[32m+added line\x1b[0m\n\x1b[31m-removed line\x1b[0m\n\x1b[1;33mwarning:\x1b[0m something"
	for _, width := range []int{40, 60, 80, 120} {
		ctx := &toolrender.RenderContext{
			ToolName: "Bash",
			Output:   ansiOutput,
			Params:   map[string]any{"command": "git diff"},
			Metadata: map[string]any{"exit_code": float64(0)},
			Width:    width,
			BgColor:  "#000000",
		}
		lines := renderTerminal("git diff", ansiOutput, ctx, width, "#000000")
		verifyAllLinesWidth(t, lines, width, fmt.Sprintf("ansi+w=%d", width))
	}
}

func TestStress_LongANSIColoredLine(t *testing.T) {
	// ANSI line that's wider than the box — must clip correctly
	longAnsi := "\x1b[32m" + strings.Repeat("X", 200) + "\x1b[0m"
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   longAnsi,
		Params:   map[string]any{"command": "grep test"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal("grep test", longAnsi, ctx, 80, "#000000")
	verifyAllLinesWidth(t, lines, 80, "long-ansi-line")
}

// ── Tabs in output ──────────────────────────────────────────────────────────

func TestStress_TabsAtVariousWidths(t *testing.T) {
	tabOutput := "file.go:10:\t\tif err != nil {\n\t\t\treturn err\n\t\t}"
	for _, width := range []int{40, 60, 80, 120} {
		ctx := &toolrender.RenderContext{
			ToolName: "Bash",
			Output:   tabOutput,
			Params:   map[string]any{"command": "grep -rn err ."},
			Metadata: map[string]any{"exit_code": float64(0)},
			Width:    width,
			BgColor:  "#000000",
		}
		lines := renderTerminal("grep -rn err .", tabOutput, ctx, width, "#000000")
		verifyAllLinesWidth(t, lines, width, fmt.Sprintf("tabs+w=%d", width))

		// No rendered line should contain literal tab
		for i, l := range lines {
			if strings.Contains(l, "\t") {
				t.Errorf("[tabs+w=%d] line %d contains literal tab", width, i)
			}
		}
	}
}

// ── CJK / wide characters ──────────────────────────────────────────────────

func TestStress_CJKOutput(t *testing.T) {
	// CJK characters are 2-wide. Clipping must not split them.
	cjk := strings.Repeat("漢", 60) // 60 CJK chars = 120 visual width
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   cjk,
		Params:   map[string]any{"command": "cat chinese.txt"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal("cat chinese.txt", cjk, ctx, 80, "#000000")
	for i, l := range lines {
		stripped := shared.StripANSI(l)
		w := shared.PrintableWidth(stripped)
		// CJK clipping can leave 1 char slack due to 2-wide boundary
		if w > 80 {
			t.Errorf("[CJK] line %d: visual width %d > 80 (overflow)\n  stripped: %q", i, w, stripped)
		}
	}
}

// ── Emoji output ────────────────────────────────────────────────────────────

func TestStress_EmojiOutput(t *testing.T) {
	emoji := "✅ Build passed\n❌ Test failed\n⚠️ Warning\n🚀 Deployed"
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   emoji,
		Params:   map[string]any{"command": "status"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal("status", emoji, ctx, 80, "#000000")
	for i, l := range lines {
		stripped := shared.StripANSI(l)
		w := shared.PrintableWidth(stripped)
		// Emoji widths vary; allow ±1
		if w > 80 {
			t.Errorf("[emoji] line %d: visual width %d > 80 (overflow)\n  stripped: %q", i, w, stripped)
		}
	}
}

// ── Empty and edge-case outputs ─────────────────────────────────────────────

func TestStress_EmptyOutput(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "",
		Params:   map[string]any{"command": "true"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal("true", "", ctx, 80, "#000000")
	verifyAllLinesWidth(t, lines, 80, "empty-output")
	if len(lines) < 1 {
		t.Errorf("empty output should still produce the command line, got %d lines", len(lines))
	}
}

func TestStress_OnlyNewlines(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "\n\n\n\n\n",
		Params:   map[string]any{"command": "echo"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal("echo", "\n\n\n\n\n", ctx, 80, "#000000")
	verifyAllLinesWidth(t, lines, 80, "only-newlines")
}

func TestStress_OnlySpaces(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "     ",
		Params:   map[string]any{"command": "echo '     '"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal("echo '     '", "     ", ctx, 80, "#000000")
	verifyAllLinesWidth(t, lines, 80, "only-spaces")
}

func TestStress_EmptyCommand(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "something",
		Params:   map[string]any{"command": ""},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal("", "something", ctx, 80, "#000000")
	verifyAllLinesWidth(t, lines, 80, "empty-cmd")
}

func TestStress_NilParams(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "output",
		Params:   nil,
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal("", "output", ctx, 80, "#000000")
	verifyAllLinesWidth(t, lines, 80, "nil-params")
}

func TestStress_NilMetadata(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "output",
		Params:   map[string]any{"command": "test"},
		Metadata: nil,
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal("test", "output", ctx, 80, "#000000")
	verifyAllLinesWidth(t, lines, 80, "nil-meta")
}

func TestStress_NilContext(t *testing.T) {
	// ctx=nil should not panic
	lines := renderTerminal("echo hi", "hi", nil, 80, "#000000")
	verifyAllLinesWidth(t, lines, 80, "nil-ctx")
}

// ── Truncation metadata ────────────────────────────────────────────────────

func TestStress_TruncationHintWidth(t *testing.T) {
	for _, width := range []int{40, 60, 80, 120} {
		ctx := &toolrender.RenderContext{
			ToolName: "Bash",
			Output:   "partial output...",
			Params:   map[string]any{"command": "cat huge"},
			Metadata: map[string]any{
				"exit_code":   float64(0),
				"truncated":   true,
				"output_path": "/tmp/swarm-tool-output/bash-full-12345.txt",
			},
			Width:   width,
			BgColor: "#000000",
		}
		lines := renderTerminal("cat huge", "partial output...", ctx, width, "#000000")
		verifyAllLinesWidth(t, lines, width, fmt.Sprintf("trunc-hint+w=%d", width))
	}
}

func TestStress_TruncationHintVeryLongPath(t *testing.T) {
	longPath := "/tmp/swarm-tool-output/" + strings.Repeat("a", 200) + ".txt"
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "partial",
		Params:   map[string]any{"command": "cat huge"},
		Metadata: map[string]any{
			"exit_code":   float64(0),
			"truncated":   true,
			"output_path": longPath,
		},
		Width:   80,
		BgColor: "#000000",
	}
	lines := renderTerminal("cat huge", "partial", ctx, 80, "#000000")
	verifyAllLinesWidth(t, lines, 80, "trunc-long-path")
}

// ── Many lines (truncation to 10) ──────────────────────────────────────────

func TestStress_100Lines(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 100; i++ {
		sb.WriteString(fmt.Sprintf("line %03d: %s\n", i, strings.Repeat(".", i%40)))
	}
	for _, width := range []int{40, 80, 120} {
		ctx := &toolrender.RenderContext{
			ToolName: "Bash",
			Output:   sb.String(),
			Params:   map[string]any{"command": "seq 100"},
			Metadata: map[string]any{"exit_code": float64(0)},
			Width:    width,
			BgColor:  "#000000",
		}
		lines := renderTerminal("seq 100", sb.String(), ctx, width, "#000000")
		verifyAllLinesWidth(t, lines, width, fmt.Sprintf("100lines+w=%d", width))

		// Middle truncation keeps a counted omission. The exact count can vary
		// with width because wrapping happens before the screen-line budget.
		foundHint := false
		for _, l := range lines {
			stripped := shared.StripANSI(l)
			if strings.Contains(stripped, "… +") && strings.Contains(stripped, "lines") {
				foundHint = true
			}
		}
		if !foundHint {
			t.Errorf("[100lines+w=%d] expected counted middle omission", width)
		}
	}
}

func TestStress_1000Lines(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 1000; i++ {
		sb.WriteString(fmt.Sprintf("line %04d\n", i))
	}
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   sb.String(),
		Params:   map[string]any{"command": "seq 1000"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal("seq 1000", sb.String(), ctx, 80, "#000000")
	verifyAllLinesWidth(t, lines, 80, "1000lines")

	// Total rendered lines should be bounded — header + blank + cmd + blank + 20 output + hint + bottom
	// = ~26 lines max. Give headroom for edge cases.
	if len(lines) > 30 {
		t.Errorf("1000-line output should render bounded lines, got %d", len(lines))
	}
}

// ── Error-only output (no stdout) ───────────────────────────────────────────

func TestStress_ErrorOnlyWidth(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "",
		Error:    "command not found: foobar",
		Params:   map[string]any{"command": "foobar"},
		Metadata: map[string]any{"exit_code": float64(127)},
		Width:    80,
		BgColor:  "#000000",
	}
	// When error is passed but no output, renderTerminal gets output=""
	// The error is shown in a separate section
	lines := renderTerminal("foobar", "", ctx, 80, "#000000")
	verifyAllLinesWidth(t, lines, 80, "error-only")
}

// ── Background color edge cases ─────────────────────────────────────────────

func TestStress_EmptyBgColor(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "output",
		Params:   map[string]any{"command": "echo test"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "",
	}
	lines := renderTerminal("echo test", "output", ctx, 80, "")
	// Should not crash; defaultBgColor is used
	if len(lines) == 0 {
		t.Error("empty bgcolor should still produce output")
	}
}

// ── Header structure tests ──────────────────────────────────────────────────

func TestStress_ResultTreeAlignment(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "hello",
		Params:   map[string]any{"command": "echo hello"},
		Metadata: map[string]any{
			"exit_code":   float64(0),
			"duration_ms": float64(500),
		},
		Width:   80,
		BgColor: "#000000",
	}
	lines := renderTerminal("echo hello", "hello", ctx, 80, "#000000")

	if len(lines) < 2 {
		t.Fatal("too few lines rendered")
	}

	// The header owns the command; result output starts with a quiet tree edge.
	firstLine := shared.StripANSI(lines[0])
	if !strings.Contains(firstLine, "└ hello") {
		t.Errorf("first result line should use a tree connector, got: %q", firstLine)
	}
	if strings.Contains(firstLine, "echo hello") {
		t.Errorf("result line should not repeat the command, got: %q", firstLine)
	}

	// No line should overflow the viewport
	for i, l := range lines {
		stripped := shared.StripANSI(l)
		w := shared.PrintableWidth(stripped)
		if w > 80 {
			t.Errorf("line %d: visual width %d > 80 (overflow)\n  stripped: %q", i, w, stripped)
		}
	}
}

// ── Mixed content stress ────────────────────────────────────────────────────

func TestStress_RealWorldGrepOutput(t *testing.T) {
	// Simulate realistic grep output with paths, line numbers, tabs, long lines
	output := strings.Join([]string{
		"src/main.go:15:\tfunc main() {",
		"src/main.go:16:\t\tfmt.Println(\"Hello, World!\")",
		"src/main.go:17:\t}",
		"src/server/handler.go:142:\t\t\tif err := db.QueryRow(ctx, \"SELECT * FROM users WHERE id = $1\", userID).Scan(&user); err != nil {",
		"src/server/handler.go:143:\t\t\t\treturn fmt.Errorf(\"failed to fetch user %d: %w\", userID, err)",
		"src/server/handler.go:144:\t\t\t}",
		"internal/config/config.go:89:\t// DefaultTimeout is the default timeout for HTTP requests to the external API gateway service endpoint",
		"internal/config/config.go:90:\tDefaultTimeout = 30 * time.Second // 30 seconds should be enough for most operations including database queries and external service calls",
		"vendor/github.com/some/very/long/package/path/that/goes/on/forever/internal/helper.go:1:\tpackage helper",
		"Makefile:42:\t@echo \"Building all targets with GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) LDFLAGS=$(LDFLAGS)\"",
	}, "\n")
	for _, width := range []int{40, 60, 80, 100, 120} {
		ctx := &toolrender.RenderContext{
			ToolName: "Bash",
			Output:   output,
			Params:   map[string]any{"command": "grep -rn 'func' ."},
			Metadata: map[string]any{
				"exit_code":   float64(0),
				"duration_ms": float64(450),
			},
			Width:   width,
			BgColor: "#0E1118",
		}
		lines := renderTerminal("grep -rn 'func' .", output, ctx, width, "#0E1118")
		verifyAllLinesWidth(t, lines, width, fmt.Sprintf("grep-real+w=%d", width))
	}
}

// ── Render via public API (Render method) ───────────────────────────────────

func TestStress_PublicAPI_Render(t *testing.T) {
	r := New()

	// Test via public Render method to ensure no discrepancy
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "hello\nworld",
		Error:    "warning: something",
		Params:   map[string]any{"command": "test-cmd"},
		Metadata: map[string]any{
			"exit_code":   float64(0),
			"duration_ms": float64(100),
		},
		Width:   80,
		BgColor: "#000000",
	}

	lines := r.Render(ctx, nil)
	verifyAllLinesWidth(t, lines, 80, "public-api")
}

func TestStress_PublicAPI_CanRender(t *testing.T) {
	r := New()

	tests := []struct {
		name   string
		expect bool
	}{
		{"Bash", true},
		{"bash", true},
		{"shell", true},
		{"computer", true},
		{"Read", false},
		{"Edit", false},
		{"mcp__terminal__run_command", true},
		{"mcp__server__execute", true},
		{"mcp__server__list_files", false},
	}

	for _, tt := range tests {
		ctx := &toolrender.RenderContext{ToolName: tt.name}
		got := r.CanRender(ctx)
		if got != tt.expect {
			t.Errorf("CanRender(%q) = %v, want %v", tt.name, got, tt.expect)
		}
	}
}

// ── Exit code edge cases ────────────────────────────────────────────────────

// ── ShowFull (Ctrl+O) bypasses internal truncation ──────────────────────────

func TestStress_ShowFull_ShowsAllLines(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 50; i++ {
		sb.WriteString(fmt.Sprintf("line %d\n", i))
	}
	output := sb.String()

	// Without ShowFull: should truncate to 10 lines + hint
	ctxTrunc := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   output,
		Params:   map[string]any{"command": "seq 50"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
		ShowFull: false,
	}
	linesTrunc := renderTerminal("seq 50", output, ctxTrunc, 80, "#000000")

	// With ShowFull: should show ALL 50 lines, no hint
	ctxFull := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   output,
		Params:   map[string]any{"command": "seq 50"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
		ShowFull: true,
	}
	linesFull := renderTerminal("seq 50", output, ctxFull, 80, "#000000")

	// Truncated output should have a counted middle omission.
	foundHint := false
	for _, l := range linesTrunc {
		if strings.Contains(shared.StripANSI(l), "… +") {
			foundHint = true
		}
	}
	if !foundHint {
		t.Error("truncated mode should show a counted middle omission")
	}

	// Full should NOT have any hint
	for _, l := range linesFull {
		if strings.Contains(shared.StripANSI(l), "… +") {
			t.Error("ShowFull mode should NOT show an omission hint")
		}
	}

	// Full should have more rendered lines than truncated
	if len(linesFull) <= len(linesTrunc) {
		t.Errorf("ShowFull should produce more lines (%d) than truncated (%d)", len(linesFull), len(linesTrunc))
	}

	// All lines should still be exactly width
	verifyAllLinesWidth(t, linesFull, 80, "showfull-all")
}

func TestStress_ExitCode_HighValue(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "killed",
		Params:   map[string]any{"command": "timeout 1 sleep 10"},
		Metadata: map[string]any{"exit_code": float64(137)},
		Width:    80,
		BgColor:  "#000000",
		ShowFull: true, // Required to show exit code
	}
	lines := renderTerminal("timeout 1 sleep 10", "killed", ctx, 80, "#000000")
	verifyAllLinesWidth(t, lines, 80, "exit-137")

	found := false
	for _, l := range lines {
		if strings.Contains(shared.StripANSI(l), "exit 137") {
			found = true
		}
	}
	if !found {
		t.Error("expected 'exit 137' in output")
	}
}
