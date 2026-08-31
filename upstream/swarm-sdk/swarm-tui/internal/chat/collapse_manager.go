package chat

import (
	"fmt"
	"sync"
	"time"
)

// CollapseManager coordinates tool call collapse states across all messages
type CollapseManager struct {
	mu     sync.RWMutex
	states map[string]*ToolCallState // key: "msgIdx:callID"

	// Navigation state
	focusedKey string   // Currently focused tool key
	keyOrder   []string // Ordered list of keys (for navigation)

	// Settings
	defaultLevel CollapseLevel // Default level for new tools
}

// NewCollapseManager creates a new manager with default settings
func NewCollapseManager() *CollapseManager {
	return &CollapseManager{
		states:       make(map[string]*ToolCallState),
		keyOrder:     []string{},
		defaultLevel: CollapseLevelCollapsed, // Start collapsed by default
	}
}

// makeKey creates a unique key for a tool call
func makeKey(msgIdx int, callID string) string {
	return fmt.Sprintf("%d:%s", msgIdx, callID)
}

// parseKey extracts message index from a key (not needed for current impl)
// func parseKey(key string) (int, string) { ... }

// RegisterToolCall adds a new tool call to tracking
func (m *CollapseManager) RegisterToolCall(msgIdx int, callID, toolName, apiName string, sequence int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := makeKey(msgIdx, callID)
	if _, exists := m.states[key]; exists {
		return // Already registered
	}

	state := &ToolCallState{
		CallID:        callID,
		ToolName:      toolName,
		ToolAPIName:   apiName,
		CollapseLevel: m.defaultLevel,
		IsFocused:     false,
		BlockSequence: sequence,
		MessageIndex:  msgIdx,
	}

	m.states[key] = state
	m.keyOrder = append(m.keyOrder, key)
}

// UpdateToolResult updates metadata when a tool result arrives
func (m *CollapseManager) UpdateToolResult(msgIdx int, callID string, outputLines int, hasError bool, startTime, endTime time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := makeKey(msgIdx, callID)
	if state, ok := m.states[key]; ok {
		state.OutputLines = outputLines
		state.HasError = hasError
		state.StartTime = startTime
		state.EndTime = endTime
		if !startTime.IsZero() && !endTime.IsZero() {
			state.ExecutionTime = endTime.Sub(startTime)
		}
	}
}

// ToggleLevel cycles the collapse level for a tool call (collapsed → compact → full)
func (m *CollapseManager) ToggleLevel(msgIdx int, callID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := makeKey(msgIdx, callID)
	if state, ok := m.states[key]; ok {
		state.NextLevel()
		return true
	}
	return false
}

// SetLevel sets a specific collapse level for a tool call
func (m *CollapseManager) SetLevel(msgIdx int, callID string, level CollapseLevel) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := makeKey(msgIdx, callID)
	if state, ok := m.states[key]; ok {
		state.CollapseLevel = level
	}
}

// GetState returns a copy of the state for a tool call (thread-safe)
func (m *CollapseManager) GetState(msgIdx int, callID string) *ToolCallState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := makeKey(msgIdx, callID)
	if state, ok := m.states[key]; ok {
		return state.Copy()
	}
	return nil
}

// GetStateByKey returns a copy of the state by key
func (m *CollapseManager) GetStateByKey(key string) *ToolCallState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if state, ok := m.states[key]; ok {
		return state.Copy()
	}
	return nil
}

// FocusNext moves focus to the next tool call, returns the focused key
func (m *CollapseManager) FocusNext() string {
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

// FocusPrev moves focus to the previous tool call, returns the focused key
func (m *CollapseManager) FocusPrev() string {
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
	currentIdx := len(m.keyOrder) // Start past the end so -1 gives last element
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

// ToggleFocused toggles the collapse level of the focused tool call
func (m *CollapseManager) ToggleFocused() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.focusedKey != "" {
		if state, ok := m.states[m.focusedKey]; ok {
			state.NextLevel()
			return m.focusedKey
		}
	}
	return ""
}

// GetFocusedState returns a copy of the focused tool's state
func (m *CollapseManager) GetFocusedState() *ToolCallState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.focusedKey != "" {
		if state, ok := m.states[m.focusedKey]; ok {
			return state.Copy()
		}
	}
	return nil
}

// GetFocusedKey returns the currently focused key
func (m *CollapseManager) GetFocusedKey() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.focusedKey
}

// ClearFocus removes focus from all tools
func (m *CollapseManager) ClearFocus() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.focusedKey != "" {
		if state, ok := m.states[m.focusedKey]; ok {
			state.IsFocused = false
		}
		m.focusedKey = ""
	}
}

// ExpandAll expands all tool calls to full view
func (m *CollapseManager) ExpandAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, state := range m.states {
		state.CollapseLevel = CollapseLevelFull
	}
}

// CollapseAll collapses all tool calls to minimal view
func (m *CollapseManager) CollapseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, state := range m.states {
		state.CollapseLevel = CollapseLevelCollapsed
	}
}

// SetAllToCompact sets all tool calls to compact view (3-line preview)
func (m *CollapseManager) SetAllToCompact() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, state := range m.states {
		state.CollapseLevel = CollapseLevelCompact
	}
}

// SetDefaultLevel sets the default collapse level for new tools
func (m *CollapseManager) SetDefaultLevel(level CollapseLevel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultLevel = level
}

// GetDefaultLevel returns the default collapse level
func (m *CollapseManager) GetDefaultLevel() CollapseLevel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultLevel
}

// Reset clears all state (for new conversations)
func (m *CollapseManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.states = make(map[string]*ToolCallState)
	m.keyOrder = []string{}
	m.focusedKey = ""
}

// Count returns the number of tracked tool calls
func (m *CollapseManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.states)
}

// GetAllStates returns copies of all states (for debugging/export)
func (m *CollapseManager) GetAllStates() []*ToolCallState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*ToolCallState, 0, len(m.keyOrder))
	for _, key := range m.keyOrder {
		if state, ok := m.states[key]; ok {
			result = append(result, state.Copy())
		}
	}
	return result
}
