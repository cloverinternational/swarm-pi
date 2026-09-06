package chat

import (
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
)

// TestFilterSidePanelBashProcesses verifies the side panel only surfaces Bash
// commands that are currently running or that were explicitly backgrounded and
// finished recently. Completed foreground command history must be omitted.
func TestFilterSidePanelBashProcesses(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { tm := now.Add(-d); return &tm }

	proc := func(id string, state bgprocess.ProcessState, completed *time.Time) bgprocess.ProcessInfo {
		return bgprocess.ProcessInfo{
			Handle:      bgprocess.NewProcessHandle(id),
			Command:     id,
			State:       state,
			CompletedAt: completed,
		}
	}

	backgrounded := map[string]bool{
		"bg-running":       true,
		"bg-done-recent":   true,
		"bg-done-stale":    true,
		"bg-failed-recent": true,
	}
	isBackgrounded := func(id string) bool { return backgrounded[id] }

	input := []bgprocess.ProcessInfo{
		proc("fg-running", bgprocess.StateRunning, nil),
		proc("bg-running", bgprocess.StateRunning, nil),
		proc("fg-done", bgprocess.StateCompleted, ago(1*time.Minute)),
		proc("bg-done-recent", bgprocess.StateCompleted, ago(1*time.Minute)),
		proc("bg-failed-recent", bgprocess.StateFailed, ago(2*time.Minute)),
		proc("bg-done-stale", bgprocess.StateCompleted, ago(10*time.Minute)),
		proc("fg-recent", bgprocess.StateCompleted, ago(30*time.Second)),
	}

	got := filterSidePanelBashProcesses(input, isBackgrounded, now)

	want := map[string]bool{
		"fg-running":       true,
		"bg-running":       true,
		"bg-done-recent":   true,
		"bg-failed-recent": true,
	}

	if len(got) != len(want) {
		t.Fatalf("expected %d visible processes, got %d: %v", len(want), len(got), sidePanelIDs(got))
	}
	for _, p := range got {
		if !want[p.Handle.ID()] {
			t.Errorf("unexpected process shown in side panel: %s (state=%s)", p.Handle.ID(), p.State)
		}
	}
}

// TestFilterSidePanelBashProcessesNilGuards ensures a nil predicate hides all
// non-running processes and empty input yields no visible commands.
func TestFilterSidePanelBashProcessesNilGuards(t *testing.T) {
	now := time.Now()
	done := now.Add(-time.Minute)
	input := []bgprocess.ProcessInfo{
		{Handle: bgprocess.NewProcessHandle("run"), State: bgprocess.StateRunning},
		{Handle: bgprocess.NewProcessHandle("done"), State: bgprocess.StateCompleted, CompletedAt: &done},
	}
	got := filterSidePanelBashProcesses(input, nil, now)
	if len(got) != 1 || got[0].Handle.ID() != "run" {
		t.Fatalf("nil predicate should show only running processes, got %v", sidePanelIDs(got))
	}
	if res := filterSidePanelBashProcesses(nil, nil, now); len(res) != 0 {
		t.Fatalf("empty input should yield no processes, got %v", sidePanelIDs(res))
	}
}

func sidePanelIDs(procs []bgprocess.ProcessInfo) []string {
	out := make([]string, 0, len(procs))
	for _, p := range procs {
		out = append(out, p.Handle.ID())
	}
	return out
}
