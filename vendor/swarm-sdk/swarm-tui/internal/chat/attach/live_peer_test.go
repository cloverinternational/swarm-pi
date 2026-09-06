package attach

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/server"
	automationstate "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/state"
)

type livePeerModel struct {
	mu      sync.RWMutex
	content string
	state   *automationstate.AppState
}

func (m *livePeerModel) Init() tea.Cmd { return nil }
func (m *livePeerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, nil
}
func (m *livePeerModel) View() tea.View {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return tea.NewView(m.content)
}

func (m *livePeerModel) GetScreen() string { return automationstate.ScreenChat }

func (m *livePeerModel) GetAppState() *automationstate.AppState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

func (m *livePeerModel) setContent(content string) {
	m.mu.Lock()
	m.content = content
	m.mu.Unlock()
}

func TestLivePeerSessionAttachedServer(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "peer.sock")
	model := &livePeerModel{content: "first"}
	program := tea.NewProgram(model)
	go func() { _, _ = program.Run() }()
	defer program.Quit()
	ctx := t.Context()
	srv := server.NewAttached(program, model, 80, 24, nil, socket)
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	go func() { _ = srv.Start(ctx) }()

	session, err := NewLivePeerSession(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Detach()

	select {
	case got := <-session.Frames():
		if got.Content != "first" {
			t.Fatalf("synchronous first frame = %q", got.Content)
		}
	default:
		t.Fatal("NewLivePeerSession returned without priming the first frame")
	}

	model.setContent("second")
	select {
	case got := <-session.Frames():
		if got.Content != "second" {
			t.Fatalf("changed frame = %q", got.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for changed frame")
	}
	if err := session.SendKey("x"); err != nil {
		t.Fatal(err)
	}
	if err := session.SendText("hello"); err != nil {
		t.Fatal(err)
	}
	session.Detach()
	select {
	case got, ok := <-session.Frames():
		if ok {
			t.Fatalf("frame after detach: %q", got.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("frames channel did not close after detach")
	}
	_ = os.Remove(socket)
}

func TestLivePeerSessionPublishesCanonicalActivity(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "activity.sock")
	model := &livePeerModel{
		content: "activity",
		state: &automationstate.AppState{
			ActivityPhase:  "tool_use",
			ActivityLabel:  "Reading attach_screen.go",
			ActivityStatus: "thinking",
			ActivityActive: true,
		},
	}
	program := tea.NewProgram(model)
	go func() { _, _ = program.Run() }()
	defer program.Quit()
	ctx := t.Context()
	srv := server.NewAttached(program, model, 80, 24, nil, socket)
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	go func() { _ = srv.Start(ctx) }()

	session, err := NewLivePeerSession(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Detach()

	deadline := time.After(2 * time.Second)
	for {
		snapshot := session.Snapshot()
		if snapshot.ActivityPhase == "tool_use" {
			if snapshot.ActivityLabel != "Reading attach_screen.go" ||
				snapshot.ActivityStatus != "thinking" ||
				!snapshot.ActivityActive {
				t.Fatalf("activity snapshot = %#v", snapshot)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for activity snapshot: %#v", snapshot)
		case <-time.After(20 * time.Millisecond):
		}
	}
}
