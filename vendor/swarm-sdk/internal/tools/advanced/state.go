package advanced

import (
	"maps"
	"sync"
)

// ---------------------------------------------------------------------------
// AppState — thread-safe shared state for programmatic tool calling
// ---------------------------------------------------------------------------

// AppState provides a thread-safe key-value store that tools can read and
// mutate during execution. It is scoped per agent (identified by AgentID)
// so multiple agents running concurrently do not interfere with each other.
//
// This enables the programmatic tool calling pattern where intermediate
// results are stored in state rather than passed back through the LLM
// context, dramatically reducing token consumption for multi-step workflows.
//
// Modelled after the getAppState/setAppState pattern found in Claude Code.
type AppState struct {
	mu   sync.RWMutex
	data map[string]any
}

// NewAppState creates a new empty AppState.
func NewAppState() *AppState {
	return &AppState{
		data: make(map[string]any),
	}
}

// Get retrieves a value by key. Returns nil if the key does not exist.
func (s *AppState) Get(key string) any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data[key]
}

// GetString retrieves a string value. Returns "" if the key does not exist
// or is not a string.
func (s *AppState) StringVal(key string) string {
	v := s.Get(key)
	if str, ok := v.(string); ok {
		return str
	}
	return ""
}

// GetBool retrieves a bool value. Returns false if the key does not exist
// or is not a bool.
func (s *AppState) BoolVal(key string) bool {
	v := s.Get(key)
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// Set stores a key-value pair atomically.
func (s *AppState) Set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

// Delete removes a key.
func (s *AppState) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
}

// Has returns true if the key exists.
func (s *AppState) Has(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.data[key]
	return ok
}

// Keys returns all keys.
func (s *AppState) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}

// Snapshot returns a shallow copy of all data. Mutations to the returned
// map do not affect the AppState.
func (s *AppState) Snapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := make(map[string]any, len(s.data))
	maps.Copy(snap, s.data)
	return snap
}

// Update atomically applies an updater function to the state. The updater
// receives the current snapshot and returns the full replacement. This is
// the recommended way to perform read-modify-write operations.
//
// The updater must NOT retain or mutate the passed map after returning.
func (s *AppState) Update(updater func(current map[string]any) map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	newData := updater(s.data)
	// Protect against nil updater result
	if newData == nil {
		newData = make(map[string]any)
	}
	s.data = newData
}

// Clear removes all keys.
func (s *AppState) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = make(map[string]any)
}

// Len returns the number of keys.
func (s *AppState) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// ---------------------------------------------------------------------------
// AppStateManager — per-agent state isolation
// ---------------------------------------------------------------------------

// AppStateManager manages per-agent AppState instances. Each agent gets its
// own isolated state identified by agent ID. State is created lazily on
// first access.
type AppStateManager struct {
	mu     sync.RWMutex
	agents map[string]*AppState
}

// NewAppStateManager creates a new manager.
func NewAppStateManager() *AppStateManager {
	return &AppStateManager{
		agents: make(map[string]*AppState),
	}
}

// GetState returns the AppState for the given agent. If no state exists, a
// new empty AppState is created and returned.
func (m *AppStateManager) State(agentID string) *AppState {
	m.mu.RLock()
	state, ok := m.agents[agentID]
	m.mu.RUnlock()

	if ok {
		return state
	}

	// Create lazily
	m.mu.Lock()
	defer m.mu.Unlock()
	// Double-check after lock upgrade
	if state, ok := m.agents[agentID]; ok {
		return state
	}
	state = NewAppState()
	m.agents[agentID] = state
	return state
}

// HasState returns true if the agent already has state.
func (m *AppStateManager) HasState(agentID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.agents[agentID]
	return ok
}

// ClearState removes all state for the given agent.
func (m *AppStateManager) ClearState(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.agents, agentID)
}

// ClearAll removes all agent states.
func (m *AppStateManager) ClearAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agents = make(map[string]*AppState)
}

// AgentIDs returns all agent IDs that have state.
func (m *AppStateManager) AgentIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.agents))
	for id := range m.agents {
		ids = append(ids, id)
	}
	return ids
}
