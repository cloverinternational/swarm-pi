package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
)

func TestBashClampSelection(t *testing.T) {
	cases := []struct{ idx, n, want int }{
		{0, 0, -1},
		{5, 0, -1},
		{-3, 4, 0},
		{2, 4, 2},
		{9, 4, 3},
	}
	for _, c := range cases {
		if got := bashClampSelection(c.idx, c.n); got != c.want {
			t.Errorf("bashClampSelection(%d,%d)=%d want %d", c.idx, c.n, got, c.want)
		}
	}
}

func TestBashMoveSelection(t *testing.T) {
	if got := bashMoveSelection(0, -1, 3); got != 0 {
		t.Errorf("moving up at top should stay 0, got %d", got)
	}
	if got := bashMoveSelection(2, 1, 3); got != 2 {
		t.Errorf("moving down at bottom should stay 2, got %d", got)
	}
	if got := bashMoveSelection(1, 1, 3); got != 2 {
		t.Errorf("moving down should go to 2, got %d", got)
	}
}

func TestBashKillTargetID(t *testing.T) {
	rows := []bashRow{
		{id: "run1", state: bgprocess.StateRunning},
		{id: "done1", state: bgprocess.StateCompleted},
		{id: "run2", state: bgprocess.StateRunning},
	}
	if got := bashKillTargetID(rows, 0); got != "run1" {
		t.Errorf("index 0 should target run1, got %q", got)
	}
	if got := bashKillTargetID(rows, 1); got != "" {
		t.Errorf("completed row is not killable, got %q", got)
	}
	if got := bashKillTargetID(rows, 2); got != "run2" {
		t.Errorf("index 2 should target run2, got %q", got)
	}
	if got := bashKillTargetID(rows, 5); got != "" {
		t.Errorf("out-of-range should be empty, got %q", got)
	}
}

func TestBashVisibleWindow(t *testing.T) {
	rows := []bashRow{{id: "a"}, {id: "b"}, {id: "c"}, {id: "d"}, {id: "e"}, {id: "f"}, {id: "g"}}

	window, start := bashVisibleWindow(rows, 5, 3)
	if start != 4 || len(window) != 3 || window[0].id != "e" || window[1].id != "f" || window[2].id != "g" {
		t.Fatalf("selected-centered window = start %d, ids %v; want start 4, [e f g]", start, []string{window[0].id, window[1].id, window[2].id})
	}

	window, start = bashVisibleWindow(rows, 0, 3)
	if start != 0 || window[0].id != "a" || window[2].id != "c" {
		t.Errorf("top window = start %d, first/last %q/%q; want 0, a/c", start, window[0].id, window[2].id)
	}
}

func TestBashSelectionResolvesAcrossReorderAndRemoval(t *testing.T) {
	rows := []bashRow{{id: "a"}, {id: "b"}, {id: "c"}}
	idx, id := bashResolveSelection(rows, 1, "b")
	if idx != 1 || id != "b" {
		t.Fatalf("initial selection = %d/%q, want 1/b", idx, id)
	}

	rows = []bashRow{{id: "c"}, {id: "a"}, {id: "b"}}
	idx, id = bashResolveSelection(rows, idx, id)
	if idx != 2 || id != "b" {
		t.Fatalf("selection after reorder = %d/%q, want 2/b", idx, id)
	}

	rows = rows[:2]
	idx, id = bashResolveSelection(rows, idx, id)
	if idx != 1 || id != "a" {
		t.Fatalf("selection after removal = %d/%q, want 1/a", idx, id)
	}
}

func TestBashPanelNavigationRenderEnterAndKillAgreePastCap(t *testing.T) {
	manager := bgprocess.NewManager(bgprocess.DefaultManagerConfig())
	defer manager.Shutdown(context.Background())

	for i := 0; i < 7; i++ {
		_, err := manager.Spawn(context.Background(), bgprocess.SpawnRequest{
			Command: "sleep 5 # activity-" + string(rune('a'+i)),
			Owner:   bgprocess.OwnerInfo{AgentID: "bash-activity-test"},
			Timeout: 10 * time.Second,
		})
		if err != nil {
			t.Fatalf("spawn process %d: %v", i, err)
		}
	}

	app := &App{
		sdk:              &SDKIntegration{bgProcessManager: manager},
		bashPanelFocused: true,
		width:            80,
		height:           24,
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows := app.currentBashRows()
		allRunning := len(rows) == 7
		for _, row := range rows {
			allRunning = allRunning && row.state == bgprocess.StateRunning
		}
		if allRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("processes did not all reach running state: %+v", rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for i := 0; i < 6; i++ {
		handled, _ := app.handleBashPanelKey("down")
		if !handled {
			t.Fatal("down should be handled while BASH panel is focused")
		}
	}
	rows := app.currentBashRows()
	app.bashSelectedIdx, app.bashSelectedID = bashResolveSelection(rows, app.bashSelectedIdx, app.bashSelectedID)
	if app.bashSelectedIdx != 6 {
		t.Fatalf("selected index = %d, want 6", app.bashSelectedIdx)
	}
	selected := rows[app.bashSelectedIdx]

	rendered := ansi.Strip(strings.Join(renderBashActivity(bashActivityInput{
		rows: rows, width: 60, focused: true, selectedIdx: app.bashSelectedIdx, theme: bashTestTheme(),
	}), "\n"))
	if !strings.Contains(rendered, selected.command) {
		t.Fatalf("selected command %q is not in the visible window:\n%s", selected.command, rendered)
	}

	handled, _ := app.handleBashPanelKey("enter")
	if !handled || app.detailViewer == nil {
		t.Fatal("enter should open the selected command detail viewer")
	}
	if detail := ansi.Strip(app.detailViewer.View()); !strings.Contains(detail, selected.command) {
		t.Fatalf("detail viewer does not show selected command %q:\n%s", selected.command, detail)
	}

	if got := bashKillTargetID(rows, app.bashSelectedIdx); got != selected.id {
		t.Fatalf("kill target = %q, want selected visible id %q", got, selected.id)
	}
	app.handleBashPanelKey("k")
	info, err := manager.GetInfoByID(context.Background(), selected.id)
	if err != nil {
		t.Fatalf("get killed process: %v", err)
	}
	if info.State == bgprocess.StateRunning {
		t.Fatalf("selected process %q is still running after kill", selected.id)
	}
}
