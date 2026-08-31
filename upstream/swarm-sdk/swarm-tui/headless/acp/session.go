package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

// promptResult carries the stop reason once a session/prompt turn completes.
type promptResult struct {
	stopReason string
	err        error
}

// session holds state for a single ACP session.
type session struct {
	id     string
	convID string
	cwd    string
	// cancel the context for the session (used to abort pending prompt turns).
	cancelFn context.CancelFunc
	// promptDone receives the result once a prompt turn ends.
	// It is nil when no prompt is in flight.
	mu         sync.Mutex
	promptDone chan promptResult

	// Per-session config overrides (empty string means "use global default").
	provider string
	model    string
	agentID  string
	modeID   string
}

// newSession creates a new session with a random ID.
func newSession(convID, cwd string) *session {
	return &session{
		id:     generateSessionID(),
		convID: convID,
		cwd:    cwd,
	}
}

// startPrompt marks a prompt turn as in-flight and returns the result channel.
// Returns an error if a prompt is already in flight.
func (s *session) startPrompt(cancel context.CancelFunc) (chan promptResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.promptDone != nil {
		return nil, fmt.Errorf("a prompt turn is already in flight for session %s", s.id)
	}
	s.cancelFn = cancel
	s.promptDone = make(chan promptResult, 1)
	return s.promptDone, nil
}

// finishPrompt signals the result to the waiting prompt handler and clears the slot.
func (s *session) finishPrompt(result promptResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.promptDone != nil {
		s.promptDone <- result
		s.promptDone = nil
	}
}

// cancel aborts any in-flight prompt turn.
func (s *session) cancel() {
	s.mu.Lock()
	fn := s.cancelFn
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// applyConfig applies a map of key→value updates to the session configuration.
// Returns the list of field names that actually changed.
func (s *session) applyConfig(updates map[string]string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var changed []string
	for k, v := range updates {
		switch k {
		case "provider":
			if s.provider != v {
				s.provider = v
				changed = append(changed, "provider")
			}
		case "model":
			if s.model != v {
				s.model = v
				changed = append(changed, "model")
			}
		case "agent":
			if s.agentID != v {
				s.agentID = v
				changed = append(changed, "agent")
			}
		case "mode":
			if s.modeID != v {
				s.modeID = v
				changed = append(changed, "mode")
			}
		}
	}
	return changed
}

// getConfig returns a snapshot of the session's current per-session config.
func (s *session) getConfig() (provider, model, agentID, modeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.provider, s.model, s.agentID, s.modeID
}

// sessionRegistry stores active sessions keyed by session ID.
type sessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*session
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{sessions: make(map[string]*session)}
}

// add registers a new session.
func (r *sessionRegistry) add(s *session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.id] = s
}

// get looks up a session by ID.
func (r *sessionRegistry) get(id string) (*session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[id]
	return s, ok
}

// remove deletes a session from the registry.
func (r *sessionRegistry) remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
}

// generateSessionID returns a random hex session ID prefixed with "sess_".
func generateSessionID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "sess_" + hex.EncodeToString(b)
}
