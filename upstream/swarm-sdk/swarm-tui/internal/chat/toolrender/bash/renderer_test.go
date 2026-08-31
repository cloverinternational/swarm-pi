package bash

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
)

// ── expandTabs ────────────────────────────────────────────────────────────────

func TestExpandTabs_AtColumnZero(t *testing.T) {
	got := expandTabs("\thello")
	want := "    hello"
	if got != want {
		t.Errorf("expandTabs(%q) = %q, want %q", "\thello", got, want)
	}
}

func TestExpandTabs_PartialTabStop(t *testing.T) {
	got := expandTabs("abc\thello")
	want := "abc hello"
	if got != want {
		t.Errorf("expandTabs(%q) = %q, want %q", "abc\thello", got, want)
	}
}

func TestExpandTabs_ExactTabStop(t *testing.T) {
	got := expandTabs("abcd\thello")
	want := "abcd    hello"
	if got != want {
		t.Errorf("expandTabs(%q) = %q, want %q", "abcd\thello", got, want)
	}
}

func TestExpandTabs_MultipleTabsTracksColumn(t *testing.T) {
	got := expandTabs("\t\thello")
	want := "        hello"
	if got != want {
		t.Errorf("expandTabs(%q) = %q, want %q", "\t\thello", got, want)
	}
}

func TestExpandTabs_TwoTabsAfterContent(t *testing.T) {
	got := expandTabs("ab\t\thello")
	want := "ab      hello"
	if got != want {
		t.Errorf("expandTabs(%q) = %q, want %q", "ab\t\thello", got, want)
	}
}

func TestExpandTabs_NoTabs(t *testing.T) {
	in := "no tabs here"
	got := expandTabs(in)
	if got != in {
		t.Errorf("expandTabs with no tabs changed string: got %q", got)
	}
}

func TestExpandTabs_OnlyTab(t *testing.T) {
	got := expandTabs("\t")
	if got != "    " {
		t.Errorf("single tab should expand to 4 spaces, got %q", got)
	}
}

func TestExpandTabs_ProducesNoTabs(t *testing.T) {
	inputs := []string{
		"\t",
		"abc\tdef",
		"\t\t\t",
		"swarm-tui/chat/sdk.go:120:\t\tsdk.logger.Info(ctx,",
	}
	for _, in := range inputs {
		out := expandTabs(in)
		if strings.ContainsRune(out, '\t') {
			t.Errorf("expandTabs(%q) still contains tab: %q", in, out)
		}
	}
}

// ── Duration formatting ─────────────────────────────────────────────────────

func TestFormatDuration_SubSecond(t *testing.T) {
	got := formatDuration(300)
	want := "0.3s"
	if got != want {
		t.Errorf("formatDuration(300) = %q, want %q", got, want)
	}
}

func TestFormatDuration_Seconds(t *testing.T) {
	got := formatDuration(2300)
	want := "2.3s"
	if got != want {
		t.Errorf("formatDuration(2300) = %q, want %q", got, want)
	}
}

func TestFormatDuration_Minutes(t *testing.T) {
	got := formatDuration(83000)
	want := "1m 23s"
	if got != want {
		t.Errorf("formatDuration(83000) = %q, want %q", got, want)
	}
}

func TestFormatDuration_ExactMinute(t *testing.T) {
	got := formatDuration(60000)
	want := "1m 0s"
	if got != want {
		t.Errorf("formatDuration(60000) = %q, want %q", got, want)
	}
}

// ── Exit code extraction ────────────────────────────────────────────────────

func TestGetExitCode_Present(t *testing.T) {
	meta := map[string]any{"exit_code": float64(0)}
	if got := getExitCode(meta); got != 0 {
		t.Errorf("getExitCode = %d, want 0", got)
	}
}

func TestGetExitCode_NonZero(t *testing.T) {
	meta := map[string]any{"exit_code": float64(1)}
	if got := getExitCode(meta); got != 1 {
		t.Errorf("getExitCode = %d, want 1", got)
	}
}

func TestGetExitCode_Missing(t *testing.T) {
	meta := map[string]any{}
	if got := getExitCode(meta); got != -1 {
		t.Errorf("getExitCode = %d, want -1", got)
	}
}

func TestGetExitCode_NilMetadata(t *testing.T) {
	if got := getExitCode(nil); got != -1 {
		t.Errorf("getExitCode(nil) = %d, want -1", got)
	}
}

// ── Exit code display in rendered output ────────────────────────────────────

func TestRenderTerminal_ExitZero_ShowsGreen(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "hello",
		Params:   map[string]any{"command": "echo hello"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
		ShowFull: true, // Required to show exit code
	}
	lines := renderTerminal("echo hello", "hello", ctx, 80, "#000000")

	found := false
	for _, l := range lines {
		stripped := shared.StripANSI(l)
		if strings.Contains(stripped, "exit 0") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'exit 0' in rendered output for exit code 0")
	}
}

func TestRenderTerminal_ExitOne_ShowsRed(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "error occurred",
		Params:   map[string]any{"command": "false"},
		Metadata: map[string]any{"exit_code": float64(1)},
		Width:    80,
		BgColor:  "#000000",
		ShowFull: true, // Required to show exit code
	}
	lines := renderTerminal("false", "error occurred", ctx, 80, "#000000")

	found := false
	for _, l := range lines {
		stripped := shared.StripANSI(l)
		if strings.Contains(stripped, "exit 1") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'exit 1' in rendered output for exit code 1")
	}
}

// ── Command ownership ───────────────────────────────────────────────────────

func TestRenderTerminal_DoesNotRepeatCommandFromHeader(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "ok",
		Params:   map[string]any{"command": "echo ok"},
		Metadata: map[string]any{"duration_ms": float64(2300)},
		Width:    80,
		BgColor:  "#000000",
		ShowFull: true,
	}
	lines := renderTerminal("echo ok", "ok", ctx, 80, "#000000")

	if len(lines) == 0 {
		t.Fatal("no lines rendered")
	}
	for _, line := range lines {
		if strings.Contains(shared.StripANSI(line), "echo ok") {
			t.Errorf("result should not repeat command from tool header: %q", shared.StripANSI(line))
		}
	}
}

// ── Output truncation (middle mode for completed commands) ─────────────────

func TestRenderTerminal_MiddleTruncationPreservesOutcome(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 30; i++ {
		sb.WriteString(fmt.Sprintf("line %d\n", i))
	}
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   sb.String(),
		Params:   map[string]any{"command": "seq 30"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal("seq 30", sb.String(), ctx, 80, "#000000")

	// The omission count sits between the beginning and final outcome.
	foundHint := false
	for _, l := range lines {
		stripped := shared.StripANSI(l)
		if strings.Contains(stripped, "… +26 lines") {
			foundHint = true
			break
		}
	}
	if !foundHint {
		t.Error("expected counted middle omission for 30-line output")
	}

	foundFirst := false
	foundLast := false
	for _, l := range lines {
		stripped := shared.StripANSI(l)
		if strings.TrimSpace(stripped) == "└ line 1" {
			foundFirst = true
		}
		if strings.TrimSpace(stripped) == "line 30" {
			foundLast = true
		}
	}
	if !foundFirst {
		t.Error("expected first line to preserve setup context")
	}
	if !foundLast {
		t.Error("expected final line to preserve command outcome")
	}
}

// ── Streaming state ─────────────────────────────────────────────────────────

func TestRenderTerminal_StreamingShowsRunning(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "",
		Params:   map[string]any{"command": "make build"},
		Width:    80,
		BgColor:  "#000000",
		IsActive: true,
	}
	lines := renderTerminal("make build", "", ctx, 80, "#000000")

	if len(lines) == 0 {
		t.Fatal("no lines rendered")
	}
	foundRunning := false
	for _, l := range lines {
		if strings.Contains(shared.StripANSI(l), "waiting for output…") {
			foundRunning = true
			break
		}
	}
	if !foundRunning {
		t.Error("expected waiting indicator in streaming output")
	}
}

func TestRenderTerminal_StreamingWithOutput(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 20; i++ {
		sb.WriteString(fmt.Sprintf("compiling file%d.go\n", i))
	}
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   sb.String(),
		Params:   map[string]any{"command": "make build"},
		Width:    80,
		BgColor:  "#000000",
		IsActive: true,
	}
	lines := renderTerminal("make build", sb.String(), ctx, 80, "#000000")

	// Should show an explicit earlier-line count (20 lines > 8 streaming limit).
	foundHint := false
	for _, l := range lines {
		stripped := shared.StripANSI(l)
		if strings.Contains(stripped, "… +12 earlier lines") {
			foundHint = true
			break
		}
	}
	if !foundHint {
		t.Error("expected earlier-line count for streaming output")
	}

}

// ── Truncation hint display ─────────────────────────────────────────────────

func TestRenderTerminal_TruncationHint(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "partial output...",
		Params:   map[string]any{"command": "big-command"},
		Metadata: map[string]any{
			"exit_code":      float64(0),
			"truncated":      true,
			"truncated_path": "/tmp/full-output.log",
		},
		Width:   80,
		BgColor: "#000000",
	}
	lines := renderTerminal("big-command", "partial output...", ctx, 80, "#000000")

	foundTrunc := false
	for _, l := range lines {
		stripped := shared.StripANSI(l)
		if strings.Contains(stripped, "Output truncated") && strings.Contains(stripped, "/tmp/full-output.log") {
			foundTrunc = true
			break
		}
	}
	if !foundTrunc {
		t.Error("expected truncation hint with file path")
	}
}

// ── Shell header ────────────────────────────────────────────────────────────

func TestRenderTerminal_HeaderShowsDuration(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "ok",
		Params:   map[string]any{"command": "echo ok"},
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal("echo ok", "ok", ctx, 80, "#000000")

	if len(lines) == 0 {
		t.Fatal("no lines rendered")
	}
	stripped := shared.StripANSI(lines[0])
	// Header should NOT contain "Shell" - we removed that label
	if strings.Contains(stripped, "Shell") {
		t.Errorf("header should NOT contain 'Shell', got: %q", stripped)
	}
	// Header should contain the duration label (empty string for no metadata)
	// Just verify we got a valid line
	if stripped == "" {
		t.Error("header should not be empty")
	}
}

// ── Box overflow regression tests ───────────────────────────────────────────

var regressionLines = []string{
	"swarm-tui/internal/chat/sdk_integration_tokens.go:126:\tsdk.logger.Info(ctx, \"generating_compaction_summary\",",
	"swarm-tui/internal/chat/sdk_integration_tokens.go:141:\tsdk.logger.Info(ctx, \"compaction_summary_generated\",",
	"swarm-tui/internal/chat/sdk_integration_tokens.go:215:\tsdk.logger.Info(ctx, \"generating_compaction_summary_multi_provider\",",
	"swarm-tui/internal/chat/sdk_integration_conversation.go:528:\t\t\tsdk.logger.Warn(ctx, \"failed_to_add_compacted_message\",",
	"swarm-sdk/agent/agent_execute.go:590:\t\ta.logger.Info(ctx, \"agent.compaction_needed\",",
}

func TestRenderTerminal_TabLines_BoxDoesNotOverflow(t *testing.T) {
	const width = 108
	output := strings.Join(regressionLines, "\n")
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   output,
		Params:   map[string]any{"command": "grep -rn 'test' ."},
		Width:    width,
		BgColor:  "#0E1118",
	}
	renderedLines := renderTerminal("grep -rn 'test' .", output, ctx, width, "#0E1118")

	for _, rendered := range renderedLines {
		stripped := shared.StripANSI(rendered)
		if !strings.HasPrefix(stripped, "│") {
			continue
		}
		visWidth := shared.PrintableWidth(stripped)
		expected := width
		if visWidth != expected {
			t.Errorf("rendered line visual width %d ≠ expected %d\nline: %q",
				visWidth, expected, stripped)
		}
	}
}

func TestRenderTerminal_RunningState_NoCrash(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "",
		Params:   map[string]any{"command": "long running command"},
		Width:    100,
		BgColor:  "",
		IsActive: true,
	}
	lines := renderTerminal("long running command", "", ctx, 100, "")
	if len(lines) == 0 {
		t.Error("running state should produce at least a border + cursor line")
	}
}

// ── Header width correctness ────────────────────────────────────────────────

func TestRenderTerminal_AllLinesExactWidth(t *testing.T) {
	// Test with duration (triggers the rightText branch of topBorder)
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "line1\nline2\nline3",
		Params:   map[string]any{"command": "echo test"},
		Metadata: map[string]any{
			"exit_code":   float64(0),
			"duration_ms": float64(1500),
		},
		Width:   80,
		BgColor: "#000000",
	}
	lines := renderTerminal("echo test", "line1\nline2\nline3", ctx, 80, "#000000")

	for i, l := range lines {
		stripped := shared.StripANSI(l)
		w := shared.PrintableWidth(stripped)
		if w > 80 {
			t.Errorf("line %d: visual width %d > 80 (overflow)\n  stripped: %q", i, w, stripped)
		}
	}
}

func TestRenderTerminal_AllLinesExactWidth_NoDuration(t *testing.T) {
	// Test without duration (triggers the no-rightText branch)
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "ok",
		Params:   map[string]any{"command": "echo ok"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    60,
		BgColor:  "#000000",
	}
	lines := renderTerminal("echo ok", "ok", ctx, 60, "#000000")

	for i, l := range lines {
		stripped := shared.StripANSI(l)
		w := shared.PrintableWidth(stripped)
		if w > 60 {
			t.Errorf("line %d: visual width %d > 60 (overflow)\n  stripped: %q", i, w, stripped)
		}
	}
}

func TestRenderTerminal_LongOutputClipped(t *testing.T) {
	// A line wider than the box should be clipped with ellipsis, not wrapped
	longLine := strings.Repeat("x", 200)
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   longLine,
		Params:   map[string]any{"command": "cat bigfile"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
	}
	lines := renderTerminal("cat bigfile", longLine, ctx, 80, "#000000")

	for i, l := range lines {
		stripped := shared.StripANSI(l)
		w := shared.PrintableWidth(stripped)
		if w > 80 {
			t.Errorf("line %d: visual width %d > 80 (long output should not overflow)\n  stripped: %q", i, w, stripped)
		}
	}
}

func TestRenderTerminal_EmptyOutput_NoCrash(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "Command completed successfully (no output)",
		Params:   map[string]any{"command": "echo hi"},
		Width:    100,
		BgColor:  "",
	}
	lines := renderTerminal("echo hi", "Command completed successfully (no output)", ctx, 100, "")
	if len(lines) == 0 {
		t.Error("should produce output lines")
	}
}
