package subagent

import "time"

// SubAgentCollapseLevel defines display level for sub-agent blocks
type SubAgentCollapseLevel int

const (
	SubAgentLevelCollapsed SubAgentCollapseLevel = iota // Header only
	SubAgentLevelExpanded                               // Full content
)

// String returns a human-readable name for the collapse level
func (l SubAgentCollapseLevel) String() string {
	switch l {
	case SubAgentLevelCollapsed:
		return "collapsed"
	case SubAgentLevelExpanded:
		return "expanded"
	default:
		return "unknown"
	}
}

// SubAgentState tracks collapse state for a sub-agent block
type SubAgentState struct {
	// Identity
	Key        string // Unique key: "sa:msgIdx:agentName:seq"
	AgentName  string
	MessageIdx int
	BlockSeq   int

	// State
	CollapseLevel SubAgentCollapseLevel
	IsFocused     bool

	// Metadata
	ToolCount  int           // Number of tool calls inside
	TotalLines int           // Total output lines (for summary)
	HasErrors  bool          // Whether any tool had an error
	StartTime  time.Time     // When sub-agent started
	Duration   time.Duration // Total execution time

	// Navigation context
	ParentToolID  string   // If spawned by delegate_task
	NestedToolIDs []string // Tool call IDs inside this sub-agent

	// Streaming state
	IsStreaming     bool      // Currently receiving content
	StreamingStatus string    // Status text (e.g., "Working", "Running tools")
	LastUpdate      time.Time // For timeout/stale detection
	ShowBorder      bool      // Whether to show box border around content
}

// Toggle switches between collapsed and expanded
func (s *SubAgentState) Toggle() {
	if s.CollapseLevel == SubAgentLevelCollapsed {
		s.CollapseLevel = SubAgentLevelExpanded
	} else {
		s.CollapseLevel = SubAgentLevelCollapsed
	}
}

// IsExpanded returns true if sub-agent content is visible
func (s *SubAgentState) IsExpanded() bool {
	return s.CollapseLevel == SubAgentLevelExpanded
}

// Copy returns a thread-safe copy
func (s *SubAgentState) Copy() *SubAgentState {
	if s == nil {
		return nil
	}
	cp := *s
	cp.NestedToolIDs = make([]string, len(s.NestedToolIDs))
	copy(cp.NestedToolIDs, s.NestedToolIDs)
	return &cp
}

// SetStreaming updates the streaming state
func (s *SubAgentState) SetStreaming(streaming bool, status string) {
	s.IsStreaming = streaming
	s.StreamingStatus = status
	s.LastUpdate = time.Now()
}

// IsStreamingActive returns true if actively streaming (not stale)
func (s *SubAgentState) IsStreamingActive() bool {
	if !s.IsStreaming {
		return false
	}
	// Consider stale after 5 seconds of no updates
	return time.Since(s.LastUpdate) < 5*time.Second
}

// ToggleBorder toggles the border display state
func (s *SubAgentState) ToggleBorder() {
	s.ShowBorder = !s.ShowBorder
}
