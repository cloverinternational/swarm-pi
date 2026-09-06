package attach

import (
	"fmt"
	"sync"
	"time"

	automation "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/client"
	automationstate "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/state"
)

const livePeerPollInterval = 100 * time.Millisecond

// LivePeerSession polls a live AttachedServer and exposes changed rendered
// content plus a structured state snapshot to a TUI consumer. The automation
// client is serialized because the wire protocol has no request IDs.
type LivePeerSession struct {
	client *automation.Client

	mu       sync.Mutex
	stateMu  sync.RWMutex
	detached bool
	last     string
	snapshot StateSnapshot
	frames   chan *automation.Frame
	errors   chan error
	done     chan struct{}
	once     sync.Once
}

// NewLivePeerSession connects to socketPath and starts polling the peer.
func NewLivePeerSession(socketPath string) (*LivePeerSession, error) {
	c := automation.New("unix://" + socketPath)
	if err := c.Connect(); err != nil {
		return nil, fmt.Errorf("attach live peer: %w", err)
	}
	s := &LivePeerSession{
		client: c,
		frames: make(chan *automation.Frame, 8),
		errors: make(chan error, 8),
		done:   make(chan struct{}),
	}

	// Prime the session synchronously. AttachScreen renders immediately after
	// this constructor returns; starting the poller first left the first detail
	// pane with an empty transcript/activity snapshot and made a successful
	// attach look blank until a later refresh tick. The frame request is the
	// minimum viable proof that this is a usable live peer. State is best effort
	// so older peers can still provide their raw terminal frame.
	frame, err := c.GetFrame()
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("attach live peer: initial frame: %w", err)
	}
	if frame != nil {
		s.last = frame.Content
		select {
		case s.frames <- frame:
		default:
			// The channel is only a hand-off buffer; the first frame is also
			// available from the normal poll path if a consumer is delayed.
		}
	}
	if st, stateErr := c.GetState(); stateErr == nil {
		s.snapshot = snapshotFromState(st)
	} else {
		s.snapshot = StateSnapshot{Stale: true}
	}

	go s.poll()
	return s, nil
}

// Attach is an alias for NewLivePeerSession.
func Attach(socketPath string) (*LivePeerSession, error) {
	return NewLivePeerSession(socketPath)
}

func (s *LivePeerSession) Frames() <-chan *automation.Frame { return s.frames }
func (s *LivePeerSession) Errors() <-chan error             { return s.errors }

func (s *LivePeerSession) poll() {
	ticker := time.NewTicker(livePeerPollInterval)
	defer ticker.Stop()
	defer close(s.frames)
	defer close(s.errors)
	for {
		select {
		case <-s.done:
			return
		default:
		}

		s.mu.Lock()
		if s.detached {
			s.mu.Unlock()
			return
		}
		frame, frameErr := s.client.GetFrame()
		var st *automationstate.FullState
		var stateErr error
		if frameErr == nil {
			st, stateErr = s.client.GetState()
		}
		s.mu.Unlock()

		if frameErr != nil {
			s.publishError(frameErr)
		} else if frame != nil && s.changed(frame.Content) {
			select {
			case s.frames <- frame:
			case <-s.done:
				return
			}
		}
		if stateErr != nil {
			s.stateMu.Lock()
			s.snapshot.Stale = true
			s.stateMu.Unlock()
		} else {
			s.stateMu.Lock()
			s.snapshot = snapshotFromState(st)
			s.stateMu.Unlock()
		}

		select {
		case <-ticker.C:
		case <-s.done:
			return
		}
	}
}

func (s *LivePeerSession) changed(content string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.detached || content == s.last {
		return false
	}
	s.last = content
	return true
}

func (s *LivePeerSession) publishError(err error) {
	select {
	case s.errors <- err:
	case <-s.done:
	}
}

// Snapshot returns the last structured state observed from the peer.
func (s *LivePeerSession) Snapshot() StateSnapshot {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.snapshot
}

func snapshotFromState(st *automationstate.FullState) StateSnapshot {
	if st == nil {
		return StateSnapshot{Stale: true}
	}
	snapshot := StateSnapshot{
		Screen:         st.Screen,
		ConversationID: st.ConversationID,
		OperatingMode:  st.OperatingMode,
		Workspace:      st.Workspace,
		Branch:         st.Branch,
		Streaming:      st.Streaming,
		ActivityPhase:  st.ActivityPhase,
		ActivityLabel:  st.ActivityLabel,
		ActivityStatus: st.ActivityStatus,
		ActivityActive: st.ActivityActive,
		UpdatedAt:      st.Timestamp,
	}
	snapshot.Messages = append([]automationstate.MessageState(nil), st.Messages...)
	if st.Modal != nil {
		snapshot.ModalOpen = st.Modal.Open
		snapshot.ModalID = st.Modal.ID
	}
	for i := len(st.Messages) - 1; i >= 0; i-- {
		if st.Messages[i].Role == "system" || st.Messages[i].Content == "" {
			continue
		}
		snapshot.Preview = st.Messages[i].Content
		if len(snapshot.Preview) > 240 {
			snapshot.Preview = snapshot.Preview[:240] + "…"
		}
		snapshot.Summary = snapshot.Preview
		break
	}
	return snapshot
}

func (s *LivePeerSession) SendKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.detached {
		return fmt.Errorf("attach live peer: detached")
	}
	return s.client.SendKey(key)
}

func (s *LivePeerSession) SendText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.detached {
		return fmt.Errorf("attach live peer: detached")
	}
	return s.client.SendText(text)
}

func (s *LivePeerSession) Click(x, y int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.detached {
		return fmt.Errorf("attach live peer: detached")
	}
	return s.client.Click(x, y)
}

func (s *LivePeerSession) Scroll(x, y int, up bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.detached {
		return fmt.Errorf("attach live peer: detached")
	}
	return s.client.Scroll(x, y, up)
}

// Detach is idempotent and safe to call concurrently. It prevents further
// frames before closing the underlying socket and terminating the poller.
func (s *LivePeerSession) Detach() error {
	var err error
	s.once.Do(func() {
		s.mu.Lock()
		s.detached = true
		s.mu.Unlock()
		close(s.done)
		err = s.client.Close()
	})
	return err
}
