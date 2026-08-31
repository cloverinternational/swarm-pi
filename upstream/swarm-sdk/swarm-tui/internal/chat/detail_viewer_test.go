package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
)

// DetailViewer renders a header (title + status) and the body, and Refresh
// re-runs the closure.
func TestDetailViewer_ViewAndRefresh(t *testing.T) {
	n := 0
	body := "line one\nline two"
	v := NewDetailViewer("my title", func() string { return "running" }, func() string {
		n++
		return body
	})
	v.SetSize(80, 20)

	out := ansi.Strip(v.View())
	if !strings.Contains(out, "my title") {
		t.Errorf("expected title in header:\n%s", out)
	}
	if !strings.Contains(out, "running") {
		t.Errorf("expected status in header:\n%s", out)
	}
	if !strings.Contains(out, "line one") {
		t.Errorf("expected body content:\n%s", out)
	}
	if !strings.Contains(out, "esc back") {
		t.Errorf("expected esc hint:\n%s", out)
	}

	// Refresh re-runs the closure.
	before := n
	body = "updated body"
	v.Refresh()
	if n <= before {
		t.Error("expected refresh to re-run the body closure")
	}
	if !strings.Contains(ansi.Strip(v.View()), "updated body") {
		t.Error("expected refreshed content to appear")
	}
}

// SetSize clamps to sane minimums and does not panic on tiny sizes.
func TestDetailViewer_SetSizeClamps(t *testing.T) {
	v := NewDetailViewer("t", nil, func() string { return "x" })
	v.SetSize(1, 1) // should clamp internally
	_ = v.View()    // must not panic
}

// openBashDetail builds a takeover viewer for a real spawned process.
func TestOpenBashDetail_FromSpawnedProcess(t *testing.T) {
	mgr := bgprocess.NewManager(bgprocess.DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())
	sdk := &SDKIntegration{bgProcessManager: mgr}
	a := &App{sdk: sdk, width: 80, height: 24}

	h, err := mgr.Spawn(context.Background(), bgprocess.SpawnRequest{
		Command: "printf 'alpha\\nbravo\\n'; sleep 0.2",
		Owner:   bgprocess.OwnerInfo{AgentID: "t"},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	time.Sleep(250 * time.Millisecond)

	a.openBashDetail(h.ID())
	if a.detailViewer == nil {
		t.Fatal("expected detailViewer to be set")
	}
	out := ansi.Strip(a.detailViewer.View())
	for _, want := range []string{"command:", "status:", "alpha", "bravo"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in bash detail:\n%s", want, out)
		}
	}
}

// openBashDetail on an unknown task still produces a viewer with a graceful
// "no longer available" message (never nil / never panics).
func TestOpenBashDetail_UnknownTask(t *testing.T) {
	mgr := bgprocess.NewManager(bgprocess.DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())
	a := &App{sdk: &SDKIntegration{bgProcessManager: mgr}, width: 80, height: 24}
	a.openBashDetail("nope")
	if a.detailViewer == nil {
		t.Fatal("expected a viewer even for unknown task")
	}
	if !strings.Contains(ansi.Strip(a.detailViewer.View()), "no longer available") {
		t.Errorf("expected graceful message for unknown task")
	}
}

// openDockEntryDetail dispatches to the sub-agent opener for agent entries.
func TestOpenDockEntryDetail_AgentDispatch(t *testing.T) {
	a := &App{sdk: &SDKIntegration{}, width: 80, height: 24}
	// Agent entry with no live agent → graceful "no longer available".
	a.openDockEntryDetail(bashDockEntry{IsAgent: true, AgentID: "ghost"})
	if a.detailViewer == nil {
		t.Fatal("expected a viewer for agent entry")
	}
	if !strings.Contains(ansi.Strip(a.detailViewer.View()), "no longer available") {
		t.Errorf("expected graceful sub-agent message")
	}
}
