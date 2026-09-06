package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
)

func dockLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func dockStripped(s string) string {
	var b strings.Builder
	for _, l := range dockLines(s) {
		b.WriteString(ansi.Strip(l))
		b.WriteString("\n")
	}
	return b.String()
}

func runningEntry(id, cmd string, elapsed time.Duration) bashDockEntry {
	return bashDockEntry{TaskID: id, Command: cmd, State: bgprocess.StateRunning, Running: true, Elapsed: elapsed}
}

func doneEntry(id, cmd string, code int) bashDockEntry {
	return bashDockEntry{TaskID: id, Command: cmd, State: bgprocess.StateCompleted, Running: false, ExitCode: &code}
}

// Idle: no entries → empty render, zero lines reserved.
func TestBashDock_IdleEmpty(t *testing.T) {
	s, n := renderBashDock(nil, 0, false, 80, "#000000", nil)
	if s != "" || n != 0 {
		t.Errorf("expected empty idle dock, got s=%q n=%d", s, n)
	}
}

// Unfocused with commands: a single compact hint line.
func TestBashDock_UnfocusedHint(t *testing.T) {
	entries := []bashDockEntry{runningEntry("a", "npm run dev", 5*time.Second)}
	s, n := renderBashDock(entries, 0, false, 80, "#000000", nil)
	if n != 1 {
		t.Fatalf("expected 1 hint line, got %d", n)
	}
	text := ansi.Strip(s)
	if !strings.Contains(text, "task") || !strings.Contains(text, "↓") {
		t.Errorf("expected task hint with ↓, got: %q", text)
	}
}

// Focused: header + rows + tail + footer, and the selected row shows the tail.
func TestBashDock_FocusedListAndTail(t *testing.T) {
	entries := []bashDockEntry{
		runningEntry("a", "npm run dev", 42*time.Second),
		doneEntry("b", "make build", 0),
	}
	tailFn := func(id string, n int) []string {
		if id == "a" {
			return []string{"VITE ready", "listening on 5173"}
		}
		return nil
	}
	s, n := renderBashDock(entries, 0, true, 80, "#000000", tailFn)
	text := dockStripped(s)
	if n < 4 {
		t.Errorf("expected several lines when focused, got %d:\n%s", n, text)
	}
	for _, want := range []string{"background tasks", "npm run dev", "make build", "VITE ready", "scroll"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in focused dock:\n%s", want, text)
		}
	}
}

// Selecting the second entry shows ITS tail, not the first's.
func TestBashDock_TailFollowsSelection(t *testing.T) {
	entries := []bashDockEntry{
		runningEntry("a", "cmd-a", time.Second),
		runningEntry("b", "cmd-b", time.Second),
	}
	tailFn := func(id string, n int) []string { return []string{"tail-of-" + id} }
	s, _ := renderBashDock(entries, 1, true, 80, "#000000", tailFn)
	text := dockStripped(s)
	if !strings.Contains(text, "tail-of-b") {
		t.Errorf("expected selected entry b's tail, got:\n%s", text)
	}
}

// Every rendered dock line must be exactly the target width.
func TestBashDock_ExactWidth(t *testing.T) {
	entries := []bashDockEntry{
		runningEntry("a", strings.Repeat("x", 200), 42*time.Second),
		doneEntry("b", "make build", 1),
	}
	tailFn := func(id string, n int) []string { return []string{strings.Repeat("y", 300), "short"} }
	for _, w := range []int{40, 60, 80, 120} {
		for _, focused := range []bool{false, true} {
			s, _ := renderBashDock(entries, 0, focused, w, "#0E1118", tailFn)
			for i, l := range dockLines(s) {
				if got := ansi.StringWidth(ansi.Strip(l)); got != w {
					t.Errorf("w=%d focused=%v line %d width=%d: %q", w, focused, i, got, ansi.Strip(l))
				}
			}
		}
	}
}

// collectBashDockEntries excludes the foreground command; sortDockEntries then
// puts running first (deterministically, regardless of input order).
func TestCollectBashDockEntries_OrderAndForeground(t *testing.T) {
	now := time.Now()
	procs := []bgprocess.ProcessInfo{
		{Handle: bgprocess.NewProcessHandle("fg"), Command: "foreground", State: bgprocess.StateRunning, StartedAt: now},
		{Handle: bgprocess.NewProcessHandle("done1"), Command: "done-one", State: bgprocess.StateCompleted},
		{Handle: bgprocess.NewProcessHandle("run1"), Command: "run-one", State: bgprocess.StateRunning, StartedAt: now},
	}
	entries := collectBashDockEntries(procs, "fg")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (foreground excluded), got %d", len(entries))
	}
	for _, e := range entries {
		if e.TaskID == "fg" {
			t.Error("foreground command must be excluded")
		}
	}
	sortDockEntries(entries)
	if !entries[0].Running {
		t.Errorf("expected running entry first after sort, got %+v", entries[0])
	}
}

// sortDockEntries is deterministic and stable regardless of input order:
// running-first, then newest StartedAt, then stable ID.
func TestSortDockEntries_Deterministic(t *testing.T) {
	t0 := time.Now()
	mk := func(id string, running bool, start time.Time) bashDockEntry {
		st := bgprocess.StateCompleted
		if running {
			st = bgprocess.StateRunning
		}
		return bashDockEntry{TaskID: id, Running: running, StartedAt: start, State: st}
	}
	// Two different input orderings of the same set must sort identically.
	a := []bashDockEntry{
		mk("old-done", false, t0.Add(-10*time.Minute)),
		mk("new-run", true, t0.Add(-1*time.Minute)),
		mk("old-run", true, t0.Add(-5*time.Minute)),
	}
	b := []bashDockEntry{
		mk("old-run", true, t0.Add(-5*time.Minute)),
		mk("old-done", false, t0.Add(-10*time.Minute)),
		mk("new-run", true, t0.Add(-1*time.Minute)),
	}
	sortDockEntries(a)
	sortDockEntries(b)
	want := []string{"new-run", "old-run", "old-done"} // running newest-first, then done
	for i := range want {
		if a[i].TaskID != want[i] || b[i].TaskID != want[i] {
			t.Fatalf("order mismatch at %d: a=%s b=%s want=%s", i, a[i].TaskID, b[i].TaskID, want[i])
		}
	}
}

// Finished entries older than the linger window are aged out; running + recent
// finished entries are kept.
func TestCollectBashDockEntries_AgesOutOldFinished(t *testing.T) {
	now := time.Now()
	oldDone := now.Add(-dockFinishedLinger - time.Minute)
	recentDone := now.Add(-time.Second)
	procs := []bgprocess.ProcessInfo{
		{Handle: bgprocess.NewProcessHandle("run"), Command: "r", State: bgprocess.StateRunning, StartedAt: now},
		{Handle: bgprocess.NewProcessHandle("old"), Command: "o", State: bgprocess.StateCompleted, CompletedAt: &oldDone},
		{Handle: bgprocess.NewProcessHandle("recent"), Command: "n", State: bgprocess.StateCompleted, CompletedAt: &recentDone},
	}
	entries := collectBashDockEntries(procs, "")
	ids := map[string]bool{}
	for _, e := range entries {
		ids[e.TaskID] = true
	}
	if ids["old"] {
		t.Error("expected old finished entry to age out")
	}
	if !ids["run"] || !ids["recent"] {
		t.Errorf("expected running + recently-finished entries kept, got %v", ids)
	}
}

// ID-anchored selection survives a reshuffle: dockSelectedIndex re-resolves the
// same entry even when the underlying list order changes.
func TestDockSelectedIndex_AnchorsByID(t *testing.T) {
	a := &App{}
	entries := []bashDockEntry{
		{TaskID: "x", State: bgprocess.StateRunning, Running: true},
		{TaskID: "y", State: bgprocess.StateRunning, Running: true},
	}
	a.bashDockSelectedID = "bash:y"
	if got := a.dockSelectedIndex(entries); got != 1 {
		t.Fatalf("expected index 1 for y, got %d", got)
	}
	// Reshuffle: y now first. Selection must follow y, not the index.
	entries = []bashDockEntry{
		{TaskID: "y", State: bgprocess.StateRunning, Running: true},
		{TaskID: "x", State: bgprocess.StateRunning, Running: true},
	}
	if got := a.dockSelectedIndex(entries); got != 0 {
		t.Fatalf("expected index 0 for y after reshuffle, got %d", got)
	}
	// If the anchored entry disappears, fall back to the first entry.
	a.bashDockSelectedID = "bash:gone"
	if got := a.dockSelectedIndex(entries); got != 0 {
		t.Fatalf("expected fallback to 0 when anchor missing, got %d", got)
	}
	if a.bashDockSelectedID != "bash:y" {
		t.Errorf("expected anchor reset to first entry id, got %q", a.bashDockSelectedID)
	}
}

func TestRotatingBashGlyphAdvancesWithElapsed(t *testing.T) {
	first := rotatingBashGlyph(0)
	second := rotatingBashGlyph(120 * time.Millisecond)
	if first == second {
		t.Fatalf("expected rotating bash glyph to advance, got %q twice", first)
	}
	if dockGlyphText, _ := dockGlyph(runningEntry("run", "npm", 120*time.Millisecond)); dockGlyphText != second {
		t.Fatalf("dockGlyph should use rotating bash glyph, got %q want %q", dockGlyphText, second)
	}
}

func TestSpinnerDefaultsAvoidMatrixLetters(t *testing.T) {
	if got := NewDefaultRenderSettings().SpinnerType; got != "Braille" {
		t.Fatalf("default render spinner = %q, want Braille", got)
	}
	if got := GetSpinnerTypeFromString("not-a-real-spinner"); got != SpinnerTypeBraille {
		t.Fatalf("unknown spinner fallback = %v, want Braille", got)
	}
}
