package bash

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
)

// stripAll joins rendered lines with visible text stripped of ANSI codes.
func stripAll(lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(ansi.Strip(l))
		b.WriteString("\n")
	}
	return b.String()
}

// ── Metadata-first detection: explicit background=true ────────────────────────

func TestBackground_MetadataExplicit(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   `{"status":"backgrounded","task_id":"bg-123","message":"..."}`,
		Params:   map[string]any{"command": "npm run dev"},
		Metadata: map[string]any{
			"background":        true,
			"task_id":           "bg-123",
			"background_reason": "explicit",
		},
		Width:   80,
		BgColor: "#000000",
	}
	lines := New().Render(ctx, nil)
	text := stripAll(lines)
	if !strings.Contains(text, "Backgrounded") {
		t.Errorf("expected 'Backgrounded' label, got:\n%s", text)
	}
	if !strings.Contains(text, "requested") {
		t.Errorf("expected 'requested' reason label, got:\n%s", text)
	}
	if !strings.Contains(text, "bg-123") {
		t.Errorf("expected task_id bg-123, got:\n%s", text)
	}
}

// ── Metadata-first detection: idle auto-background ────────────────────────────

func TestBackground_MetadataIdle(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   `{"backgrounded":true,"task_id":"bg-idle","status":"running"}`,
		Params:   map[string]any{"command": "go build ./..."},
		Metadata: map[string]any{
			"background":        true,
			"task_id":           "bg-idle",
			"background_reason": "idle",
		},
		Width:   80,
		BgColor: "#000000",
	}
	text := stripAll(New().Render(ctx, nil))
	if !strings.Contains(text, "idle timeout") {
		t.Errorf("expected 'idle timeout' reason, got:\n%s", text)
	}
	if !strings.Contains(text, "bg-idle") {
		t.Errorf("expected task_id from metadata, got:\n%s", text)
	}
}

// ── Metadata-first detection: Ctrl+B user-triggered ───────────────────────────

func TestBackground_MetadataUser(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   `{"backgrounded":true,"task_id":"bg-u","status":"running","user_triggered":true}`,
		Params:   map[string]any{"command": "tail -f log"},
		Metadata: map[string]any{
			"background":        true,
			"task_id":           "bg-u",
			"background_reason": "user",
		},
		Width:   80,
		BgColor: "#000000",
	}
	text := stripAll(New().Render(ctx, nil))
	if !strings.Contains(text, "Ctrl+B") {
		t.Errorf("expected 'Ctrl+B' reason, got:\n%s", text)
	}
}

// ── Legacy fallback: no metadata, JSON body only ──────────────────────────────

func TestBackground_LegacyJSONFallback(t *testing.T) {
	// A pre-metadata conversation: only the JSON body is present.
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   `{"backgrounded":true,"task_id":"legacy-9","status":"running","message":"idle"}`,
		Params:   map[string]any{"command": "sleep 100"},
		Metadata: nil,
		Width:    80,
		BgColor:  "#000000",
	}
	text := stripAll(New().Render(ctx, nil))
	if !strings.Contains(text, "Backgrounded") {
		t.Errorf("legacy JSON should still be detected as backgrounded, got:\n%s", text)
	}
	if !strings.Contains(text, "legacy-9") {
		t.Errorf("expected task_id parsed from JSON body, got:\n%s", text)
	}
}

// ── Non-backgrounded output must NOT be treated as backgrounded ────────────────

func TestBackground_NormalOutputNotMisdetected(t *testing.T) {
	// Output that merely mentions the word "background" must not trip detection.
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Output:   "starting background worker process\ndone",
		Params:   map[string]any{"command": "echo hi"},
		Metadata: map[string]any{"exit_code": float64(0)},
		Width:    80,
		BgColor:  "#000000",
	}
	text := stripAll(New().Render(ctx, nil))
	if strings.Contains(text, "task_id") {
		t.Errorf("normal output should not render as backgrounded, got:\n%s", text)
	}
}

// ── Detection helpers ─────────────────────────────────────────────────────────

func TestIsBackgroundedOutput_ParsesJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"explicit flag", `{"backgrounded":true,"task_id":"x"}`, true},
		{"status+id", `{"status":"running","task_id":"x"}`, true},
		{"status backgrounded", `{"status":"backgrounded","task_id":"x"}`, true},
		{"plain text mentioning background", "running in the background now", false},
		{"json without id", `{"status":"running"}`, false},
		{"normal command output", "hello world", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isBackgroundedOutput(c.in); got != c.want {
				t.Errorf("isBackgroundedOutput(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestIsBackgroundedMeta(t *testing.T) {
	if !isBackgroundedMeta(map[string]any{"background": true}) {
		t.Error("expected true for background=true")
	}
	if !isBackgroundedMeta(map[string]any{"background": "true"}) {
		t.Error("expected true for background=\"true\"")
	}
	if isBackgroundedMeta(map[string]any{"background": false}) {
		t.Error("expected false for background=false")
	}
	if isBackgroundedMeta(nil) {
		t.Error("expected false for nil metadata")
	}
}
