package profiling

import (
	"time"
)

// CacheBreakType categorizes the type of cache break detected
type CacheBreakType string

const (
	BreakSystemPromptChange  CacheBreakType = "SYSTEM_PROMPT_CHANGE"
	BreakMessageFormatChange CacheBreakType = "MESSAGE_FORMAT_CHANGE"
	BreakMarkerDisappeared   CacheBreakType = "MARKER_DISAPPEARED"
	BreakContentMutated      CacheBreakType = "CONTENT_MUTATED"
	BreakBlockReordered      CacheBreakType = "BLOCK_REORDERED"
	BreakUnknown             CacheBreakType = "UNKNOWN"
)

// BreakSeverity indicates impact of cache break
type BreakSeverity string

const (
	SeverityHigh   BreakSeverity = "HIGH"   // Breaks cache prefix
	SeverityMedium BreakSeverity = "MEDIUM" // May impact future turns
	SeverityLow    BreakSeverity = "LOW"    // Minor impact
)

// DiffType indicates the nature of JSON difference
type DiffType string

const (
	DiffAdded     DiffType = "ADDED"
	DiffRemoved   DiffType = "REMOVED"
	DiffModified  DiffType = "MODIFIED"
	DiffReordered DiffType = "REORDERED"
)

// MutationType indicates how the mutation occurred
type MutationType string

const (
	MutationDirectAssign MutationType = "DIRECT_ASSIGN"
	MutationMethodCall   MutationType = "METHOD_CALL"
	MutationExternal     MutationType = "EXTERNAL"
)

// StackFrame represents a single frame in a stack trace
type StackFrame struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// CacheBreakLocation represents where in code a break occurred
type CacheBreakLocation struct {
	File     string `json:"file"`
	Function string `json:"function"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

// CacheBreakEvent represents a single detected cache break
type CacheBreakEvent struct {
	// Identification
	ID             string    `json:"id"`
	Timestamp      time.Time `json:"ts"`
	ConversationID string    `json:"conversation_id"`
	TurnNumber     int       `json:"turn_number"`

	// Event classification
	EventType   string `json:"event_type"` // "CACHE_BREAK_DETECTED", "CACHE_HIT", "CACHE_MISS"
	BreakType   string `json:"break_type,omitempty"`
	Severity    string `json:"severity,omitempty"`
	Description string `json:"description,omitempty"`

	// Localization in code
	Location   *CacheBreakLocation `json:"location,omitempty"`
	StackTrace []StackFrame        `json:"stack_trace,omitempty"`

	// Message identification
	MessageIndex int `json:"message_index,omitempty"`

	// Hash comparison
	BeforeHash      string `json:"before_hash,omitempty"`
	AfterHash       string `json:"after_hash,omitempty"`
	BeforeBytesSnip string `json:"before_bytes_snip,omitempty"`
	AfterBytesSnip  string `json:"after_bytes_snip,omitempty"`

	// Diff information
	DiffPath []string `json:"diff_path,omitempty"`
	DiffType string   `json:"diff_type,omitempty"`
	DiffSize int64    `json:"diff_size,omitempty"`

	// Mutation details
	MutationType    string         `json:"mutation_type,omitempty"`
	MutationPath    string         `json:"mutation_path,omitempty"`
	MutationDetails map[string]any `json:"mutation_details,omitempty"`

	// Correlation
	SpanID    string `json:"span_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// CacheMetrics aggregates cache statistics
type CacheMetrics struct {
	// Temporal
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`

	// Conversation metrics
	ConversationID string `json:"conversation_id"`
	TotalTurns     int    `json:"total_turns"`
	TotalAPICalls  int    `json:"total_api_calls"`

	// Cache performance
	CacheHits       int     `json:"cache_hits"`
	CacheMisses     int     `json:"cache_misses"`
	CacheBreaks     int     `json:"cache_breaks"`
	CacheHitPercent float64 `json:"cache_hit_percent"`

	// Message stability
	MessageMutations int            `json:"message_mutations"`
	BreaksByType     map[string]int `json:"breaks_by_type"`
	BreaksBySeverity map[string]int `json:"breaks_by_severity"`

	// System prompt stability
	SystemPromptChanges int `json:"system_prompt_changes"`
	SystemBlockChanges  int `json:"system_block_changes"`

	// Token usage
	TotalInputTokens    int64   `json:"total_input_tokens"`
	TotalOutputTokens   int64   `json:"total_output_tokens"`
	CacheCreatedTokens  int64   `json:"cache_created_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheSavingsPercent float64 `json:"cache_savings_percent"`

	// Break analysis
	TopBreakLocations []LocationBreakStats `json:"top_break_locations"`
	TopMutationPaths  []MutationPathStats  `json:"top_mutation_paths"`
	TimelineEvents    []TimelineEvent      `json:"timeline_events"`
}

// LocationBreakStats tracks breaks by code location
type LocationBreakStats struct {
	Location string `json:"location"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Breaks   int    `json:"breaks"`
}

// MutationPathStats tracks breaks by mutation path
type MutationPathStats struct {
	Path   string `json:"path"`
	Breaks int    `json:"breaks"`
}

// TimelineEvent represents an event in the timeline
type TimelineEvent struct {
	Timestamp   time.Time      `json:"timestamp"`
	TurnNumber  int            `json:"turn_number"`
	EventType   string         `json:"event_type"` // "API_CALL", "CACHE_BREAK", "MUTATION", "CACHE_HIT"
	Description string         `json:"description"`
	Details     map[string]any `json:"details,omitempty"`
}

// CacheBreakReport represents a comprehensive analysis report
type CacheBreakReport struct {
	Summary         *CacheMetrics      `json:"summary"`
	BreakClusters   []BreakCluster     `json:"break_clusters"`
	Hotspots        []HotspotAnalysis  `json:"hotspots"`
	Efficiency      *EfficiencyMetrics `json:"efficiency"`
	Recommendations []Recommendation   `json:"recommendations"`
	GeneratedAt     time.Time          `json:"generated_at"`
}

// BreakCluster represents grouped breaks of same type
type BreakCluster struct {
	Type   string            `json:"type"`
	Count  int               `json:"count"`
	Events []CacheBreakEvent `json:"events"`
}

// HotspotAnalysis identifies problem areas
type HotspotAnalysis struct {
	Location    string  `json:"location"`
	BreakCount  int     `json:"break_count"`
	Severity    string  `json:"severity"`
	Confidence  float64 `json:"confidence"` // 0.0-1.0
	Description string  `json:"description"`
}

// EfficiencyMetrics represents cache efficiency
type EfficiencyMetrics struct {
	CacheHitRate   float64 `json:"cache_hit_rate"`  // 0.0-1.0
	CacheSavings   float64 `json:"cache_savings"`   // 0.0-1.0
	BreakFrequency float64 `json:"break_frequency"` // breaks per turn
	BreakImpact    string  `json:"break_impact"`    // "NONE", "LOW", "MEDIUM", "HIGH"
}

// Recommendation suggests how to fix cache breaks
type Recommendation struct {
	Priority    string `json:"priority"` // "CRITICAL", "HIGH", "MEDIUM", "LOW"
	Category    string `json:"category"` // "CODE_CHANGE", "CONFIG", "INVESTIGATION"
	Title       string `json:"title"`
	Description string `json:"description"`
	Location    string `json:"location,omitempty"`
	Evidence    string `json:"evidence,omitempty"`
}

// MessageHashEntry stores a message hash with metadata
type MessageHashEntry struct {
	TurnNumber   int       `json:"turn_number"`
	Timestamp    time.Time `json:"timestamp"`
	MessageCount int       `json:"message_count"`
	Hash         string    `json:"hash"`
	Message0Hash string    `json:"message_0_hash"` // Hash of first message specifically
	JSONByteSize int       `json:"json_byte_size"`
	SystemPrompt string    `json:"system_prompt"` // First 200 chars for change detection
}

// APIMetrics captures metrics from a single API call
type APIMetrics struct {
	Timestamp           time.Time
	Provider            string
	Model               string
	RequestTokens       int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	CacheHitDetected    bool
	MessageCount        int
	SystemPromptLength  int
	ResponseTimeMS      int64
}
