// Package silver implements the Silver tier of the bronze→silver→gold observability
// architecture. Silver builds hierarchical tree indices over raw bronze event data,
// inspired by the PageIndex approach (vectorless reasoning-based retrieval).
//
// The core insight from PageIndex: similarity ≠ relevance. Relevance requires
// reasoning. Silver provides the navigable structure that lets Gold and Steering
// agents reason about session activity without scanning raw JSONL files.
//
// Architecture:
//
//	Bronze (raw JSONL) → Silver (tree index) → Gold (reasoning retrieval)
//	                          ↓
//	                     Dreams (closet indices)
//
// Tree structure:
//
//	SilverTree (session-level)
//	├── SilverNode (phase: "Research Phase: Supply Chain Analysis")
//	│   ├── SilverNode (sub-phase: "Web Search Burst")
//	│   └── SilverNode (sub-phase: "Data Compilation")
//	└── SilverNode (phase: "Report Generation")
//	    ├── SilverNode (sub-phase: "LaTeX Compilation")
//	    └── SilverNode (sub-phase: "Diagram Fixes")
//
// Storage:
//
//	~/.swarm/projects/<hash>/silver/YYYY/MM/DD/tree_<conv_id>.json
package silver

import "time"

// SilverTree is the top-level session index. Each tree represents one session's
// worth of bronze events organized into a navigable hierarchy.
type SilverTree struct {
	// Version is the schema version for forward compatibility.
	Version int `json:"version"`

	// ProjectHash is the project identifier (directory hash).
	ProjectHash string `json:"project_hash"`

	// ConversationID is the session that produced this tree.
	ConversationID string `json:"conversation_id"`

	// SessionName is a human-readable label for the session.
	SessionName string `json:"session_name"`

	// SessionDescription is an LLM-generated summary of the session.
	SessionDescription string `json:"session_description"`

	// StartTime is the timestamp of the first event in the session.
	StartTime time.Time `json:"start_time"`

	// EndTime is the timestamp of the last event in the session.
	EndTime time.Time `json:"end_time"`

	// TotalEvents is the total number of bronze events covered by this tree.
	TotalEvents int `json:"total_events"`

	// DominantDomain is the primary domain detected across the session.
	DominantDomain string `json:"dominant_domain,omitempty"`

	// Structure is the hierarchical tree of session phases.
	Structure []SilverNode `json:"structure"`

	// Metadata contains additional session-level information.
	Metadata map[string]any `json:"metadata,omitempty"`

	// GeneratedAt is when this tree index was built.
	GeneratedAt time.Time `json:"generated_at"`
}

// SilverNode is a recursive tree node representing a session phase or sub-phase.
// Mirrors the PageIndex tree node schema adapted for temporal event data.
type SilverNode struct {
	// Title is an LLM-generated name for this phase (e.g., "Research Phase: Supply Chain Analysis").
	Title string `json:"title"`

	// NodeID is a unique identifier within the tree (e.g., "0001", "0001.01").
	NodeID string `json:"node_id"`

	// StartTime is the timestamp of the first event in this phase.
	StartTime time.Time `json:"start_time"`

	// EndTime is the timestamp of the last event in this phase.
	EndTime time.Time `json:"end_time"`

	// Summary is an LLM-generated description of what happened in this phase.
	Summary string `json:"summary"`

	// EventCount is the number of bronze events in this phase's time window.
	EventCount int `json:"event_count"`

	// DominantTools lists the most-used tools in this phase, ordered by frequency.
	DominantTools []string `json:"dominant_tools,omitempty"`

	// ToolFrequency maps tool names to call counts within this phase.
	ToolFrequency map[string]int `json:"tool_frequency,omitempty"`

	// ErrorCount is the number of tool failures in this phase.
	ErrorCount int `json:"error_count"`

	// UserPromptCount is the number of user.prompt_submit events in this phase.
	UserPromptCount int `json:"user_prompt_count"`

	// CorrectionCount is the number of user prompts that appear to be corrections
	// (detected via heuristics: negative sentiment, "stop", "no", "redo", etc.).
	CorrectionCount int `json:"correction_count"`

	// SteeringDecisions summarizes steering activity in this phase.
	SteeringDecisions *SteeringStats `json:"steering_decisions,omitempty"`

	// Domain is the detected activity domain for this phase.
	Domain string `json:"domain,omitempty"`

	// DomainSignals contains the evidence that led to domain classification.
	DomainSignals *DomainSignals `json:"domain_signals,omitempty"`

	// Nodes contains recursive child phases (sub-phases).
	// Empty for leaf nodes.
	Nodes []SilverNode `json:"nodes,omitempty"`
}

// SteeringStats summarizes steering agent activity within a phase.
type SteeringStats struct {
	ApproveCount int `json:"approve_count"`
	BlockCount   int `json:"block_count"`
	GuideCount   int `json:"guide_count"`
	FocusCount   int `json:"focus_count"`
	TotalCount   int `json:"total_count"`
	AvgLatencyMs int `json:"avg_latency_ms"`
}

// DomainSignals captures the evidence used for domain classification.
type DomainSignals struct {
	// Tools lists the characteristic tools that signal this domain.
	Tools []string `json:"tools,omitempty"`

	// Entities are detected domain-specific entities (tickers, project names, etc.).
	Entities []string `json:"entities,omitempty"`

	// Artifacts are output files or resources produced in this phase.
	Artifacts []string `json:"artifacts,omitempty"`

	// Keywords are domain-relevant terms found in user prompts or tool params.
	Keywords []string `json:"keywords,omitempty"`

	// Confidence is the domain classification confidence score (0.0 - 1.0).
	Confidence float64 `json:"confidence"`
}

// EventWindow represents a time-bucketed group of bronze events.
// These are the "pages" in the PageIndex analogy — the atomic units
// that the tree builder organizes into a hierarchy.
type EventWindow struct {
	// StartTime is the beginning of this window.
	StartTime time.Time `json:"start_time"`

	// EndTime is the end of this window.
	EndTime time.Time `json:"end_time"`

	// Events contains the raw bronze events in this window.
	Events []BronzeEvent `json:"events"`

	// EventCount is len(Events) — stored for convenience.
	EventCount int `json:"event_count"`

	// ToolCounts maps tool names to call counts within this window.
	ToolCounts map[string]int `json:"tool_counts"`

	// ErrorCount is the number of failures in this window.
	ErrorCount int `json:"error_count"`

	// UserPromptCount is the number of user prompts in this window.
	UserPromptCount int `json:"user_prompt_count"`
}

// BronzeEvent is a minimal representation of a bronze JSONL event,
// containing only the fields needed for Silver tree building.
// Full raw_data is NOT loaded — only the queryable payload fields.
type BronzeEvent struct {
	Timestamp      time.Time      `json:"ts"`
	Type           string         `json:"type"`
	AgentID        string         `json:"agent_id,omitempty"`
	ConversationID string         `json:"conv_id,omitempty"`
	TaskID         string         `json:"task_id,omitempty"`
	EventID        string         `json:"event_id,omitempty"`
	Payload        map[string]any `json:"payload"`
}

// SessionBoundary identifies a session within a stream of bronze events.
type SessionBoundary struct {
	// ConversationID is the primary conversation ID for this session.
	ConversationID string `json:"conversation_id"`

	// StartTime is the timestamp of the first event.
	StartTime time.Time `json:"start_time"`

	// EndTime is the timestamp of the last event.
	EndTime time.Time `json:"end_time"`

	// EventCount is the total events in this session.
	EventCount int `json:"event_count"`

	// Windows contains the time-bucketed event windows for this session.
	Windows []EventWindow `json:"windows"`
}

// DomainClassification is the result of classifying a set of events into
// an activity domain (software engineering, financial research, etc.).
type DomainClassification struct {
	// Domain is the identified domain name.
	Domain string `json:"domain"`

	// Confidence is the classification confidence (0.0 - 1.0).
	Confidence float64 `json:"confidence"`

	// Signals contains the evidence that led to this classification.
	Signals DomainSignals `json:"signals"`
}

// Known domain constants.
const (
	DomainSoftwareEngineering = "software_engineering"
	DomainFinancialResearch   = "financial_research"
	DomainScientificAnalysis  = "scientific_analysis"
	DomainBusinessOperations  = "business_operations"
	DomainUnknown             = "unknown"
)

// CurrentSchemaVersion is the current Silver tree schema version.
const CurrentSchemaVersion = 1

// DefaultWindowDuration is the default time window for grouping bronze events.
const DefaultWindowDuration = 5 * time.Minute

// MaxEventsPerPhaseBeforeSubdivision triggers recursive subdivision of a phase.
const MaxEventsPerPhaseBeforeSubdivision = 50
