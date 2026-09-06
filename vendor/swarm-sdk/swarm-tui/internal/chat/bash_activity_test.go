package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
)

func bashTestTheme() bashTheme {
	return bashTheme{
		Success: "#00ff00", Warning: "#ffff00", Error: "#ff0000",
		Accent: "#00ffff", Text: "#ffffff", TextDim: "#888888",
		TextMute: "#aaaaaa", BG: "#111827",
	}
}

func intp(i int) *int { return &i }

func plainLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, stripANSI(l))
	}
	return out
}

func TestRenderBashActivityEmpty(t *testing.T) {
	if got := renderBashActivity(bashActivityInput{theme: bashTestTheme(), width: 30}); got != nil {
		t.Fatalf("empty rows should render nil, got %v", got)
	}
}

func TestRenderBashActivityStructure(t *testing.T) {
	rows := []bashRow{
		{id: "a", command: "npm run dev", state: bgprocess.StateRunning, elapsed: 65 * time.Second, tail: []string{"listening on :3000"}},
		{id: "b", command: "go build ./...", state: bgprocess.StateCompleted, elapsed: 12 * time.Second, exitCode: intp(0)},
		{id: "c", command: "make test", state: bgprocess.StateFailed, elapsed: 30 * time.Second, exitCode: intp(2)},
	}
	out := renderBashActivity(bashActivityInput{rows: rows, width: 34, theme: bashTestTheme()})
	if len(out) == 0 {
		t.Fatal("expected non-empty output")
	}
	head := stripANSI(out[0])
	if !strings.Contains(head, "running") || !strings.Contains(head, "1 failed") {
		t.Fatalf("header should summarize statuses, got %q", head)
	}
	joined := strings.Join(plainLines(out), "\n")
	for _, want := range []string{"npm run dev", "go build", "make test", "listening on :3000", "✘2", "✔"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected rendered output to contain %q\n---\n%s", want, joined)
		}
	}
}

func TestRenderBashActivityCapAndOverflow(t *testing.T) {
	rows := make([]bashRow, 0, 8)
	for i := 0; i < 8; i++ {
		rows = append(rows, bashRow{id: string(rune('a' + i)), command: "cmd-" + string(rune('a'+i)), state: bgprocess.StateRunning})
	}
	out := renderBashActivity(bashActivityInput{rows: rows, width: 30, cap: 3, focused: true, selectedIdx: 6, theme: bashTestTheme()})
	joined := strings.Join(plainLines(out), "\n")
	for _, want := range []string{"cmd-f", "cmd-g", "cmd-h", "↑ 5 more"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected windowed output to contain %q, got:\n%s", want, joined)
		}
	}
	for _, hidden := range []string{"cmd-a", "cmd-e"} {
		if strings.Contains(joined, hidden) {
			t.Errorf("expected %q outside the selected window, got:\n%s", hidden, joined)
		}
	}
	selectedLine := ""
	for _, line := range plainLines(out) {
		if strings.Contains(line, "cmd-g") {
			selectedLine = line
			break
		}
	}
	if !strings.Contains(selectedLine, "❯") {
		t.Errorf("selected absolute row should remain highlighted, got %q", selectedLine)
	}
}

func TestRenderBashActivityFocusHints(t *testing.T) {
	rows := []bashRow{{id: "a", command: "sleep 100", state: bgprocess.StateRunning}}
	out := renderBashActivity(bashActivityInput{rows: rows, width: 40, focused: true, selectedIdx: 0, theme: bashTestTheme()})
	joined := strings.Join(plainLines(out), "\n")
	if !strings.Contains(joined, "kill") {
		t.Errorf("focused panel should show kill hint, got:\n%s", joined)
	}
	if !strings.Contains(joined, "sleep 100") {
		t.Errorf("selected row should show its command, got:\n%s", joined)
	}
	// unfocused should advertise the alt+b affordance instead of the nav hint
	un := renderBashActivity(bashActivityInput{rows: rows, width: 40, focused: false, theme: bashTestTheme()})
	uj := strings.Join(plainLines(un), "\n")
	if !strings.Contains(uj, "alt+b") {
		t.Errorf("unfocused panel should hint alt+b, got:\n%s", uj)
	}
	if strings.Contains(uj, "kill") {
		t.Errorf("unfocused panel should not show kill hint, got:\n%s", uj)
	}
}

func TestBuildBashRowsOrderAndTail(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	done := now.Add(-time.Minute)
	procs := []bgprocess.ProcessInfo{
		{Handle: bgprocess.NewProcessHandle("done"), Command: "done", State: bgprocess.StateCompleted, CompletedAt: &done, StartedAt: now.Add(-2 * time.Minute)},
		{Handle: bgprocess.NewProcessHandle("run"), Command: "run", State: bgprocess.StateRunning, StartedAt: now.Add(-30 * time.Second)},
	}
	tailFn := func(id string, n int) []string {
		if id == "run" {
			return []string{"tail-line"}
		}
		return nil
	}
	rows := buildBashRows(procs, tailFn, now)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].state != bgprocess.StateRunning {
		t.Errorf("running row should sort first, got %s", rows[0].state)
	}
	if len(rows[0].tail) != 1 || rows[0].tail[0] != "tail-line" {
		t.Errorf("running row should carry its tail, got %v", rows[0].tail)
	}
	if rows[0].elapsed != 30*time.Second {
		t.Errorf("running elapsed should be 30s, got %s", rows[0].elapsed)
	}
	if rows[1].elapsed != time.Minute {
		t.Errorf("terminal duration should be 1m, got %s", rows[1].elapsed)
	}
}

func TestBuildBashRowsDeterministicNewestFirst(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	sameTime := now.Add(-time.Minute)
	procs := []bgprocess.ProcessInfo{
		{Handle: bgprocess.NewProcessHandle("terminal"), State: bgprocess.StateCompleted, StartedAt: now},
		{Handle: bgprocess.NewProcessHandle("b"), State: bgprocess.StateRunning, StartedAt: sameTime},
		{Handle: bgprocess.NewProcessHandle("newest"), State: bgprocess.StateRunning, StartedAt: now.Add(-time.Second)},
		{Handle: bgprocess.NewProcessHandle("a"), State: bgprocess.StateRunning, StartedAt: sameTime},
	}

	rows := buildBashRows(procs, nil, now)
	got := []string{rows[0].id, rows[1].id, rows[2].id, rows[3].id}
	want := []string{"newest", "a", "b", "terminal"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("deterministic row order = %v, want %v", got, want)
		}
	}
}
