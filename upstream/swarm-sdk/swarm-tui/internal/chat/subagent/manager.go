package subagent

import (
	"fmt"
	"sync"
)

// SubAgentManager coordinates sub-agent collapse states and navigation
type SubAgentManager struct {
	mu     sync.RWMutex
	states map[string]*SubAgentState

	// Navigation
	focusedKey string
	keyOrder   []string

	// Default state
	defaultCollapsed bool
}

// NewSubAgentManager creates a new manager with sub-agents collapsed by default
func NewSubAgentManager() *SubAgentManager {
	return &SubAgentManager{
		states:           make(map[string]*SubAgentState),
		keyOrder:         []string{},
		defaultCollapsed: true, // Collapsed by default per requirements
	}
}

// makeSubAgentKey creates unique key for a sub-agent
func makeSubAgentKey(msgIdx int, agentName string, blockSeq int) string {
	return fmt.Sprintf("sa:%d:%s:%d", msgIdx, agentName, blockSeq)
}

// Register adds a new sub-agent to tracking
func (m *SubAgentManager) Register(msgIdx int, agentName string, blockSeq int, toolCount int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := makeSubAgentKey(msgIdx, agentName, blockSeq)
	if _, exists := m.states[key]; exists {
		return
	}

	level := SubAgentLevelCollapsed
	if !m.defaultCollapsed {
		level = SubAgentLevelExpanded
	}

	state := &SubAgentState{
		Key:           key,
		AgentName:     agentName,
		MessageIdx:    msgIdx,
		BlockSeq:      blockSeq,
		CollapseLevel: level,
		ToolCount:     toolCount,
	}

	m.states[key] = state
	m.keyOrder = append(m.keyOrder, key)
}

// GetState returns a copy of sub-agent state
func (m *SubAgentManager) GetState(msgIdx int, agentName string, blockSeq int) *SubAgentState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := makeSubAgentKey(msgIdx, agentName, blockSeq)
	if state, ok := m.states[key]; ok {
		return state.Copy()
	}
	return nil
}

// Toggle toggles collapse state for a sub-agent
func (m *SubAgentManager) Toggle(msgIdx int, agentName string, blockSeq int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := makeSubAgentKey(msgIdx, agentName, blockSeq)
	if state, ok := m.states[key]; ok {
		state.Toggle()
		return true
	}
	return false
}

// ToggleFocused toggles the currently focused sub-agent
func (m *SubAgentManager) ToggleFocused() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.focusedKey != "" {
		if state, ok := m.states[m.focusedKey]; ok {
			state.Toggle()
			return m.focusedKey
		}
	}
	return ""
}

// FocusNext moves focus to next sub-agent
func (m *SubAgentManager) FocusNext() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.keyOrder) == 0 {
		return ""
	}

	// Clear current focus
	if m.focusedKey != "" {
		if state, ok := m.states[m.focusedKey]; ok {
			state.IsFocused = false
		}
	}

	// Find current index
	currentIdx := -1
	for i, key := range m.keyOrder {
		if key == m.focusedKey {
			currentIdx = i
			break
		}
	}

	// Move to next (wrap around)
	nextIdx := (currentIdx + 1) % len(m.keyOrder)
	nextKey := m.keyOrder[nextIdx]

	if state, ok := m.states[nextKey]; ok {
		state.IsFocused = true
		m.focusedKey = nextKey
	}

	return nextKey
}

// FocusPrev moves focus to previous sub-agent
func (m *SubAgentManager) FocusPrev() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.keyOrder) == 0 {
		return ""
	}

	// Clear current focus
	if m.focusedKey != "" {
		if state, ok := m.states[m.focusedKey]; ok {
			state.IsFocused = false
		}
	}

	// Find current index (start past end so -1 gives last element)
	currentIdx := len(m.keyOrder)
	for i, key := range m.keyOrder {
		if key == m.focusedKey {
			currentIdx = i
			break
		}
	}

	// Move to previous (wrap around)
	prevIdx := (currentIdx - 1 + len(m.keyOrder)) % len(m.keyOrder)
	prevKey := m.keyOrder[prevIdx]

	if state, ok := m.states[prevKey]; ok {
		state.IsFocused = true
		m.focusedKey = prevKey
	}

	return prevKey
}

// ClearFocus removes focus from all sub-agents
func (m *SubAgentManager) ClearFocus() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.focusedKey != "" {
		if state, ok := m.states[m.focusedKey]; ok {
			state.IsFocused = false
		}
		m.focusedKey = ""
	}
}

// ExpandAll expands all sub-agents
func (m *SubAgentManager) ExpandAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, state := range m.states {
		state.CollapseLevel = SubAgentLevelExpanded
	}
}

// CollapseAll collapses all sub-agents
func (m *SubAgentManager) CollapseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, state := range m.states {
		state.CollapseLevel = SubAgentLevelCollapsed
	}
}

// GetFocusedKey returns the currently focused key
func (m *SubAgentManager) GetFocusedKey() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.focusedKey
}

// HasFocus returns true if any sub-agent is focused
func (m *SubAgentManager) HasFocus() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.focusedKey != ""
}

// Reset clears all state (for new conversations)
func (m *SubAgentManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.states = make(map[string]*SubAgentState)
	m.keyOrder = []string{}
	m.focusedKey = ""
}

// Count returns number of tracked sub-agents
func (m *SubAgentManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.states)
}

// UpdateMetadata updates sub-agent metadata (call after processing blocks)
func (m *SubAgentManager) UpdateMetadata(msgIdx int, agentName string, blockSeq int,
	toolCount int, totalLines int, hasErrors bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := makeSubAgentKey(msgIdx, agentName, blockSeq)
	if state, ok := m.states[key]; ok {
		state.ToolCount = toolCount
		state.TotalLines = totalLines
		state.HasErrors = hasErrors
	}
}

// UpdateStreaming updates the streaming state for a sub-agent
func (m *SubAgentManager) UpdateStreaming(msgIdx int, agentName string, blockSeq int,
	isStreaming bool, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := makeSubAgentKey(msgIdx, agentName, blockSeq)
	if state, ok := m.states[key]; ok {
		state.SetStreaming(isStreaming, status)
	}
}

// SetShowBorder updates the border display state for a sub-agent
func (m *SubAgentManager) SetShowBorder(msgIdx int, agentName string, blockSeq int, show bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := makeSubAgentKey(msgIdx, agentName, blockSeq)
	if state, ok := m.states[key]; ok {
		state.ShowBorder = show
	}
}

// ToggleFocusedBorder toggles the border on the currently focused sub-agent
func (m *SubAgentManager) ToggleFocusedBorder() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.focusedKey != "" {
		if state, ok := m.states[m.focusedKey]; ok {
			state.ToggleBorder()
			return m.focusedKey
		}
	}
	return ""
}

// EnableAllBorders enables borders on all sub-agents
func (m *SubAgentManager) EnableAllBorders() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, state := range m.states {
		state.ShowBorder = true
	}
}

// DisableAllBorders disables borders on all sub-agents
func (m *SubAgentManager) DisableAllBorders() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, state := range m.states {
		state.ShowBorder = false
	}
}

// SetDefaultCollapsed sets whether new sub-agents start collapsed
func (m *SubAgentManager) SetDefaultCollapsed(collapsed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultCollapsed = collapsed
}

// GetAllStates returns copies of all states (for debugging/export)
func (m *SubAgentManager) GetAllStates() []*SubAgentState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*SubAgentState, 0, len(m.keyOrder))
	for _, key := range m.keyOrder {
		if state, ok := m.states[key]; ok {
			result = append(result, state.Copy())
		}
	}
	return result
}
