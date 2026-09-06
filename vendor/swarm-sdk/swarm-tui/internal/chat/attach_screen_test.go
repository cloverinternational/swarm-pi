package chat

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/attach"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/client"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/state"
)

type fakeAttachSession struct {
	frame    string
	frames   chan *client.Frame
	errors   chan error
	snap     attach.StateSnapshot
	detached bool
	keys     []string
}

type fakeSubagentInterjector struct {
	id   string
	text string
}

func (f *fakeSubagentInterjector) Interject(id, text string) error {
	f.id, f.text = id, text
	return nil
}

func (f *fakeAttachSession) Frames() <-chan *client.Frame { return f.frames }
func (f *fakeAttachSession) Errors() <-chan error         { return f.errors }
func (f *fakeAttachSession) Snapshot() attach.StateSnapshot {
	return f.snap
}
func (f *fakeAttachSession) SendKey(k string) error  { f.keys = append(f.keys, k); return nil }
func (f *fakeAttachSession) SendText(k string) error { f.keys = append(f.keys, k); return nil }
func (f *fakeAttachSession) Detach() error           { f.detached = true; return nil }

func key(k string) tea.KeyMsg {
	if k == "enter" {
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	return tea.KeyPressMsg{Text: k}
}

func TestAttachScreenListAndPeerLifecycle(t *testing.T) {
	targets := []attach.Target{
		{Kind: attach.TargetKindPeer, ID: "p", Name: "peer", Machine: "host", Status: "busy", Steerable: true},
		{Kind: attach.TargetKindSubAgent, ID: "s", Name: "worker", Status: "running"},
	}
	fake := &fakeAttachSession{frames: make(chan *client.Frame, 4), errors: make(chan error, 1)}
	fake.frames <- &client.Frame{Content: "first frame"}
	s := NewAttachScreen(func() ([]attach.Target, error) { return targets, nil }, func(string) (liveSession, error) { return fake, nil }, nil)
	view := s.View()
	if !contains(view, "WORKSPACES") || !contains(view, "worker") || !contains(view, "peer") {
		t.Fatalf("folder/session rows missing: %q", view)
	}
	if s.Selected() != 0 {
		t.Fatalf("expected subagent first")
	}
	s.Update(key("down"))
	s.Update(key("down"))
	s.Update(key("enter"))
	s.Update(key("o"))
	if !s.Attached() || !contains(s.View(), "first frame") {
		t.Fatalf("peer detail not shown")
	}
	fake.frames <- &client.Frame{Content: "updated frame"}
	s.Refresh()
	if !contains(s.View(), "updated frame") {
		t.Fatalf("frame was not refreshed")
	}
	s.Update(key("esc"))
	if s.Attached() || !fake.detached {
		t.Fatalf("esc should detach, not close screen")
	}
	if ok, _ := s.Update(key("esc")); ok {
		t.Fatalf("second esc should close screen")
	}
}

func TestAttachScreenAutoPreviewsPeerOnSelection(t *testing.T) {
	fake := &fakeAttachSession{frames: make(chan *client.Frame, 1), errors: make(chan error, 1)}
	s := NewAttachScreen(func() ([]attach.Target, error) {
		return []attach.Target{{Kind: attach.TargetKindPeer, ID: "peer", Name: "peer"}}, nil
	}, func(string) (liveSession, error) { return fake, nil }, nil)

	s.Update(key("down"))
	if !s.Attached() {
		t.Fatal("moving onto peer should open its live preview without Enter")
	}
}

func TestAttachScreenEnterRetriesSelectedPeerAfterAttachFailure(t *testing.T) {
	targets := []attach.Target{
		{Kind: attach.TargetKindPeer, ID: "peer", Name: "peer", ControlSocket: "/tmp/peer.ctrl"},
	}
	fake := &fakeAttachSession{frames: make(chan *client.Frame, 1), errors: make(chan error, 1)}
	attempts := 0
	s := NewAttachScreen(
		func() ([]attach.Target, error) { return targets, nil },
		func(string) (liveSession, error) {
			attempts++
			if attempts == 1 {
				return nil, errors.New("peer not ready")
			}
			return fake, nil
		},
		nil,
	)

	// Enter on the initial folder selects the peer and makes the first
	// attach attempt. The failed attempt leaves the cursor on that peer.
	s.Update(key("enter"))
	if s.Attached() || !contains(s.View(), "SESSION UNAVAILABLE") {
		t.Fatalf("first attach should fail visibly: attached=%v view=%q", s.Attached(), s.View())
	}

	// A second Enter on the same selected target must retry rather than being
	// swallowed by setSelected's same-index fast path.
	s.Update(key("enter"))
	if !s.Attached() || attempts != 2 {
		t.Fatalf("retry Enter did not attach: attached=%v attempts=%d view=%q", s.Attached(), attempts, s.View())
	}
}

func TestAttachScreenNavigationSkipsFoldersAndCyclesSessions(t *testing.T) {
	targets := []attach.Target{
		{Kind: attach.TargetKindSubAgent, ID: "a", Name: "alpha", Workspace: "/tmp/demo", Status: "running"},
		{Kind: attach.TargetKindSubAgent, ID: "b", Name: "beta", Workspace: "/tmp/demo", Status: "running"},
	}
	s := NewAttachScreen(func() ([]attach.Target, error) { return targets, nil }, nil, nil)

	// Tab moves between actionable sessions and skips workspace headers.
	s.Update(key("tab"))
	if s.rows[s.Selected()].Target == nil || s.rows[s.Selected()].Target.ID != "a" {
		t.Fatalf("tab selected row %d, want first session", s.Selected())
	}
	s.Update(key("tab"))
	if s.rows[s.Selected()].Target == nil || s.rows[s.Selected()].Target.ID != "b" {
		t.Fatalf("second tab selected row %d, want second session", s.Selected())
	}
	s.Update(key("shift+tab"))
	if s.rows[s.Selected()].Target == nil || s.rows[s.Selected()].Target.ID != "a" {
		t.Fatalf("shift+tab selected row %d, want first session", s.Selected())
	}
}

func TestAttachScreenRefreshPreservesSelectedFolder(t *testing.T) {
	targets := []attach.Target{
		{Kind: attach.TargetKindPeer, ID: "alpha", Name: "alpha", Workspace: "/tmp/alpha"},
		{Kind: attach.TargetKindPeer, ID: "mono", Name: "mono", Workspace: "/home/swarm/Work/mono"},
	}
	s := NewAttachScreen(func() ([]attach.Target, error) { return targets, nil }, nil, nil)
	s.Update(key("down"))
	if s.rows[s.Selected()].Folder == nil || s.rows[s.Selected()].Folder.Name != "mono" {
		t.Fatalf("selected row before refresh = %#v, want mono folder", s.rows[s.Selected()])
	}

	s.Refresh()
	if s.rows[s.Selected()].Folder == nil || s.rows[s.Selected()].Folder.Name != "mono" {
		t.Fatalf("selected row after refresh = %#v, want mono folder", s.rows[s.Selected()])
	}
}

func TestAttachScreenRendersStructuredConversationState(t *testing.T) {
	fake := &fakeAttachSession{
		frames: make(chan *client.Frame, 1),
		errors: make(chan error, 1),
		snap: attach.StateSnapshot{
			Messages: []state.MessageState{
				{Role: "user", Content: "inspect the peer"},
				{Role: "assistant", Blocks: []state.BlockState{
					{Type: "thinking", Content: "checking state"},
					{Type: "tool_call", Content: "Read: msg-1"},
					{Type: "tool_result", Content: "conversation loaded"},
				}},
			},
			Streaming: true,
		},
	}
	s := NewAttachScreen(func() ([]attach.Target, error) {
		return []attach.Target{{Kind: attach.TargetKindPeer, ID: "peer", Name: "peer"}}, nil
	}, func(string) (liveSession, error) { return fake, nil }, nil)
	s.SetSize(100, 30)
	s.Update(key("enter"))
	view := s.View()
	for _, want := range []string{"you", "inspect the peer", "thinking", "⚙ Read: msg-1", "↳ conversation loaded", "still working"} {
		if !contains(view, want) {
			t.Fatalf("structured transcript missing %q:\n%s", want, view)
		}
	}
	if contains(view, "Structured dashboard") {
		t.Fatal("attach pane still renders metadata-only dashboard")
	}
}

func TestAttachScreenShowsActivityPreviewBeforeAttach(t *testing.T) {
	targets := []attach.Target{{
		Kind:   attach.TargetKindSubAgent,
		ID:     "worker",
		Name:   "worker",
		Status: "running",
		Snapshot: attach.StateSnapshot{
			Streaming: true,
			Preview:   "Reading the workspace and checking the failing test",
			Summary:   "Investigating attach behavior",
			Messages: []state.MessageState{
				{Role: "assistant", Content: "I found the control socket."},
			},
		},
	}}
	s := NewAttachScreen(func() ([]attach.Target, error) { return targets, nil }, nil, nil)
	s.Update(key("down"))
	view := s.View()
	for _, want := range []string{"ACTIVITY", "WORKING NOW", "Reading the workspace", "Investigating attach behavior", "RECENT TRANSCRIPT"} {
		if !contains(view, want) {
			t.Fatalf("activity preview missing %q:\n%s", want, view)
		}
	}
}

func TestAttachScreenDiscoveryError(t *testing.T) {
	s := NewAttachScreen(func() ([]attach.Target, error) { return nil, errors.New("offline") }, nil, nil)
	if !contains(s.View(), "offline") {
		t.Fatal("expected discovery error")
	}
}

func TestAppCtrlQOpensAndRoutesAttachScreen(t *testing.T) {
	app := &App{
		width:       80,
		height:      24,
		debugScreen: NewDebugScreen(),
		attachScreenFactory: func() *AttachScreen {
			return NewAttachScreen(func() ([]attach.Target, error) {
				return []attach.Target{
					{Kind: attach.TargetKindSubAgent, Name: "worker"},
					{Kind: attach.TargetKindSubAgent, Name: "worker-2"},
				}, nil
			}, nil, nil)
		},
	}

	app.handleKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if app.attachScreen == nil {
		t.Fatal("Ctrl+Q did not open the attach screen")
	}
	if got := app.attachScreen.Selected(); got != 0 {
		t.Fatalf("attach screen selected index = %d, want 0", got)
	}

	// Once open, keys go to the takeover rather than the chat screen.
	app.handleKey(key("down"))
	if got := app.attachScreen.Selected(); got != 1 {
		t.Fatalf("attach screen did not receive navigation: selected %d", got)
	}
	app.handleKey(key("esc"))
	app.handleKey(key("esc"))
	if app.attachScreen != nil {
		t.Fatal("Esc did not close the attach screen")
	}
}

func TestAppAttachMonitorTickRefreshesLiveFrame(t *testing.T) {
	fake := &fakeAttachSession{
		frames: make(chan *client.Frame, 1),
		errors: make(chan error, 1),
	}
	fake.frames <- &client.Frame{Content: "live peer frame"}
	screen := NewAttachScreen(func() ([]attach.Target, error) {
		return []attach.Target{{Kind: attach.TargetKindPeer, ID: "peer", Name: "peer"}}, nil
	}, func(string) (liveSession, error) {
		return fake, nil
	}, nil)
	screen.Update(key("enter"))
	screen.Update(key("o"))

	app := &App{attachScreen: screen, width: 80, height: 24}
	app.Update(attachMonitorTickMsg{})

	if !contains(screen.View(), "live peer frame") {
		t.Fatalf("attach monitor tick did not refresh live frame: %q", screen.View())
	}
	if !app.viewNeedsRefresh {
		t.Fatal("attach monitor tick did not invalidate the app view")
	}
}

func TestAttachScreenReleasesSessionAfterPollError(t *testing.T) {
	fake := &fakeAttachSession{frames: make(chan *client.Frame, 1), errors: make(chan error, 1)}
	s := NewAttachScreen(func() ([]attach.Target, error) {
		return []attach.Target{{Kind: attach.TargetKindPeer, ID: "peer", Name: "peer"}}, nil
	}, func(string) (liveSession, error) { return fake, nil }, nil)

	s.Update(key("down"))
	if !s.Attached() {
		t.Fatal("peer selection should attach")
	}
	fake.errors <- errors.New("broken pipe")
	s.Refresh()
	if s.Attached() {
		t.Fatal("poll error must release the live session")
	}
	if !fake.detached {
		t.Fatal("poll error must detach the underlying session")
	}
	if !contains(s.View(), "SESSION UNAVAILABLE") {
		t.Fatalf("missing unavailable state: %q", s.View())
	}
}

func contains(s, want string) bool {
	for i := 0; i+len(want) <= len(s); i++ {
		if s[i:i+len(want)] == want {
			return true
		}
	}
	return false
}

func TestAttachScreenSubagentDetailTracksStatusAndEscapes(t *testing.T) {
	targets := []attach.Target{{Kind: attach.TargetKindSubAgent, ID: "s", Name: "worker", Status: "running"}}
	s := NewAttachScreen(func() ([]attach.Target, error) { return targets, nil }, nil, nil)
	if ok, _ := s.Update(key("enter")); !ok || !contains(s.View(), "RUNNING") {
		t.Fatalf("enter should show subagent detail: %q", s.View())
	}
	targets[0].Status = "online"
	s.Refresh()
	if !contains(s.View(), "RUNNING") {
		t.Fatalf("detail did not refresh latest target state: %q", s.View())
	}
	if ok, _ := s.Update(key("esc")); !ok || !contains(s.View(), "WORKSPACES") {
		t.Fatalf("first esc should return to list: ok=%v view=%q", ok, s.View())
	}
	if ok, _ := s.Update(key("esc")); ok {
		t.Fatal("second esc should close screen")
	}
}

func TestAttachScreenInterjectsSelectedSubagentByID(t *testing.T) {
	interjector := &fakeSubagentInterjector{}
	s := NewAttachScreen(func() ([]attach.Target, error) {
		return []attach.Target{{Kind: attach.TargetKindSubAgent, ID: "stable-agent-id", Name: "worker", Status: "running", Steerable: true}}, nil
	}, nil, interjector)

	if ok, _ := s.Update(key("enter")); !ok {
		t.Fatal("enter should show subagent detail")
	}
	s.Update(key("raw text"))
	if interjector.id != "stable-agent-id" || interjector.text != "raw text" {
		t.Fatalf("Interject() = (%q, %q), want (%q, %q)", interjector.id, interjector.text, "stable-agent-id", "raw text")
	}
}

func TestAttachScreenEmptyState(t *testing.T) {
	s := NewAttachScreen(func() ([]attach.Target, error) { return nil, nil }, nil, nil)
	if view := s.View(); !contains(view, "No live sessions") {
		t.Fatalf("empty state missing: %q", view)
	}
	s.SetSize(1, 1)
}

func TestAttachViewportPreservesRelativeScrollAcrossContentRefresh(t *testing.T) {
	s := NewAttachScreen(nil, nil, nil)
	s.SetSize(80, 12)
	s.viewport.SetContent(strings.Repeat("line\n", 80))
	s.viewport.SetYOffset(20)
	before := s.viewport.ScrollPercent()

	// Simulate the wrapped render path changing the coordinate space before a
	// live refresh. The old attach code copied YOffset directly here, which
	// made the refreshed view jump to an unrelated line.
	lines, hash := s.viewport.LinesForPrewrap()
	mapping := make([]int, len(lines)+1)
	wrapped := make([]string, 0, len(lines)*2)
	for i, line := range lines {
		mapping[i] = len(wrapped)
		wrapped = append(wrapped, line, line)
	}
	mapping[len(lines)] = len(wrapped)
	s.viewport.SetPreWrappedLines(wrapped, mapping, 80, hash)

	s.viewport.SetContent(strings.Repeat("updated\n", 100))
	s.viewport.SetScrollPercent(before)

	after := s.viewport.ScrollPercent()
	if after < before-0.03 || after > before+0.03 {
		t.Fatalf("scroll percent changed across refresh: before=%.3f after=%.3f", before, after)
	}
}

func TestAttachViewportKeepsBottomPinnedAcrossRefresh(t *testing.T) {
	s := NewAttachScreen(nil, nil, nil)
	s.SetSize(80, 12)
	s.snapshot.Messages = make([]state.MessageState, 20)
	for i := range s.snapshot.Messages {
		s.snapshot.Messages[i] = state.MessageState{Role: "assistant", Content: "line"}
	}
	s.updateStructuredViewport()
	s.viewport.GotoBottom()
	s.snapshot.Messages = make([]state.MessageState, 80)
	for i := range s.snapshot.Messages {
		s.snapshot.Messages[i] = state.MessageState{Role: "assistant", Content: "new line"}
	}
	s.updateStructuredViewport()
	if !s.viewport.AtBottom() {
		t.Fatalf("bottom-pinned viewport moved away from bottom after refresh")
	}
}

func TestCompactTargetLabelUsesConversationAndCurrentTask(t *testing.T) {
	target := attach.Target{
		ID:             "peer-handle",
		Name:           "mono",
		Workspace:      "/home/swarm/Work/mono",
		ConversationID: "conversation-123456789",
		Snapshot:       attach.StateSnapshot{Summary: "Reviewing the attach tree"},
	}
	got := compactTargetLabel(target)
	if !strings.Contains(got, "conver…6789") {
		t.Fatalf("label = %q, want shortened conversation ID", got)
	}
	if !strings.Contains(got, "Reviewing the attach") {
		t.Fatalf("label = %q, want current task", got)
	}
	if strings.Contains(got, "mono") {
		t.Fatalf("label = %q still uses workspace name", got)
	}
}

func TestCompactTargetLabelFallsBackToActivityAndHandle(t *testing.T) {
	target := attach.Target{
		ID: "peer-handle",
		Snapshot: attach.StateSnapshot{
			ActivityLabel: "Waiting for approval",
		},
	}
	if got := compactTargetLabel(target); got != "peer-handle · Waiting for approval" {
		t.Fatalf("label = %q, want activity fallback", got)
	}
}
