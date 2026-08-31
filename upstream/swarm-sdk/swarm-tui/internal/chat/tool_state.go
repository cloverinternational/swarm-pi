package chat

import "time"

// CollapseLevel defines the display level for tool output
type CollapseLevel int

const (
	// CollapseLevelCollapsed shows only tool name + "▶ N lines"
	CollapseLevelCollapsed CollapseLevel = iota
	// CollapseLevelCompact shows tool name + first 3 lines + "▶ N more"
	CollapseLevelCompact
	// CollapseLevelFull shows everything
	CollapseLevelFull
)

// String returns a human-readable representation of the collapse level
func (c CollapseLevel) String() string {
	switch c {
	case CollapseLevelCollapsed:
		return "collapsed"
	case CollapseLevelCompact:
		return "compact"
	case CollapseLevelFull:
		return "full"
	default:
		return "unknown"
	}
}

// ToolCallState tracks collapse state for a tool call/result pair
type ToolCallState struct {
	// Identity
	CallID      string // Tool call ID (matches ToolCallDisplay.ID)
	ToolName    string // Display name (friendly, not API name)
	ToolAPIName string // API name (mcp_server_tool)

	// State
	CollapseLevel CollapseLevel // Current display level
	IsFocused     bool          // Whether this tool is keyboard-focused

	// Metadata for verbose mode
	ExecutionTime time.Duration // How long the tool took
	StartTime     time.Time     // When tool execution started
	EndTime       time.Time     // When tool execution completed
	OutputLines   int           // Total lines in output
	HasError      bool          // Whether tool result has error

	// Navigation
	BlockSequence int // Sequence number in OrderedBlocks
	MessageIndex  int // Index of the message containing this tool
}

// NextLevel cycles to the next collapse level (collapsed → compact → full → collapsed)
func (t *ToolCallState) NextLevel() {
	switch t.CollapseLevel {
	case CollapseLevelCollapsed:
		t.CollapseLevel = CollapseLevelCompact
	case CollapseLevelCompact:
		t.CollapseLevel = CollapseLevelFull
	case CollapseLevelFull:
		t.CollapseLevel = CollapseLevelCollapsed
	}
}

// PrevLevel cycles to the previous collapse level (collapsed ← compact ← full ← collapsed)
func (t *ToolCallState) PrevLevel() {
	switch t.CollapseLevel {
	case CollapseLevelCollapsed:
		t.CollapseLevel = CollapseLevelFull
	case CollapseLevelCompact:
		t.CollapseLevel = CollapseLevelCollapsed
	case CollapseLevelFull:
		t.CollapseLevel = CollapseLevelCompact
	}
}

// GetPreviewLines returns the number of lines to show at current level
func (t *ToolCallState) GetPreviewLines() int {
	switch t.CollapseLevel {
	case CollapseLevelCollapsed:
		return 0
	case CollapseLevelCompact:
		return 3
	case CollapseLevelFull:
		return t.OutputLines
	}
	return 0
}

// IsExpanded returns true if the tool is showing any output (compact or full)
func (t *ToolCallState) IsExpanded() bool {
	return t.CollapseLevel != CollapseLevelCollapsed
}

// IsFullyExpanded returns true if the tool is showing all output
func (t *ToolCallState) IsFullyExpanded() bool {
	return t.CollapseLevel == CollapseLevelFull
}

// Copy returns a deep copy of the state (for thread-safe operations)
func (t *ToolCallState) Copy() *ToolCallState {
	if t == nil {
		return nil
	}
	return &ToolCallState{
		CallID:        t.CallID,
		ToolName:      t.ToolName,
		ToolAPIName:   t.ToolAPIName,
		CollapseLevel: t.CollapseLevel,
		IsFocused:     t.IsFocused,
		ExecutionTime: t.ExecutionTime,
		StartTime:     t.StartTime,
		EndTime:       t.EndTime,
		OutputLines:   t.OutputLines,
		HasError:      t.HasError,
		BlockSequence: t.BlockSequence,
		MessageIndex:  t.MessageIndex,
	}
}
