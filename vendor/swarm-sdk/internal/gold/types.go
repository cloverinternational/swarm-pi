// Package gold implements the Gold tier of the bronze→silver→gold observability
// architecture. Gold is a statistical reasoning engine that analyzes Silver tree
// indices to produce actionable insights about agent behavior, efficiency, and
// cross-session patterns.
//
// Gold operates at three levels (from the SwarmOS Observability Report v2):
//
//  1. Within-project insights: "Bash failure rate is 12% — consider better error handling"
//  2. Cross-project patterns: "User corrects diagram quality in 3 of 5 projects"
//  3. Domain-transfer insights: "Iterative approach from LaTeX works for test-fix cycles"
//
// Architecture:
//
//	Silver (tree index) → Gold (reasoning retrieval) → Findings (feedback loop)
//	         ↑                                                    ↓
//	       Bronze ←←←←←←←← Steering ←←←←←←←←←←←← Dreams ←←←←←
package gold

import "time"

// InsightCategory classifies a Gold insight by its analytical focus.
type InsightCategory string

const (
	// CategoryToolEfficiency covers tool usage patterns and failure rates.
	CategoryToolEfficiency InsightCategory = "tool_efficiency"

	// CategoryCorrectionPattern covers user correction behavior and root causes.
	CategoryCorrectionPattern InsightCategory = "correction_pattern"

	// CategorySessionStructure covers how sessions are organized and paced.
	CategorySessionStructure InsightCategory = "session_structure"

	// CategorySteeringEffectiveness covers steering agent decision quality.
	CategorySteeringEffectiveness InsightCategory = "steering_effectiveness"

	// CategoryCrossProjectPattern covers patterns observed across multiple projects.
	CategoryCrossProjectPattern InsightCategory = "cross_project_pattern"

	// CategoryDomainTransfer covers insights applicable across different domains.
	CategoryDomainTransfer InsightCategory = "domain_transfer"

	// CategoryAnomalous covers unusual or unexpected behavior worth flagging.
	CategoryAnomalous InsightCategory = "anomalous"
)

// InsightSeverity indicates how actionable or important an insight is.
type InsightSeverity string

const (
	// SeverityInfo is purely informational, no action needed.
	SeverityInfo InsightSeverity = "info"

	// SeverityLow is a minor optimization opportunity.
	SeverityLow InsightSeverity = "low"

	// SeverityMedium is a meaningful improvement opportunity.
	SeverityMedium InsightSeverity = "medium"

	// SeverityHigh is a significant issue or efficiency gap worth addressing.
	SeverityHigh InsightSeverity = "high"

	// SeverityCritical is a blocking or seriously degrading pattern.
	SeverityCritical InsightSeverity = "critical"
)

// GoldInsight is a single insight produced by Gold analysis.
// Each insight is evidence-backed, actionable, and cross-referenced to its source data.
type GoldInsight struct {
	// ID is a unique identifier for this insight.
	ID string `json:"id"`

	// Title is a concise one-line description of the insight.
	Title string `json:"title"`

	// Description is a detailed explanation of what was observed.
	Description string `json:"description"`

	// Category classifies the type of insight.
	Category InsightCategory `json:"category"`

	// Severity indicates the importance/urgency of this insight.
	Severity InsightSeverity `json:"severity"`

	// Recommendation is an actionable suggestion based on this insight.
	Recommendation string `json:"recommendation"`

	// Evidence contains the specific data points that support this insight.
	Evidence []EvidencePoint `json:"evidence"`

	// ProjectHash identifies which project this insight came from.
	// Empty = cross-project insight.
	ProjectHash string `json:"project_hash,omitempty"`

	// ProjectHashes lists all projects for cross-project insights.
	ProjectHashes []string `json:"project_hashes,omitempty"`

	// SessionIDs lists the conversation IDs that contributed to this insight.
	SessionIDs []string `json:"session_ids,omitempty"`

	// TimeRange is the window of activity this insight covers.
	TimeRange InsightTimeRange `json:"time_range"`

	// Tags are searchable labels for filtering.
	Tags []string `json:"tags,omitempty"`

	// GeneratedAt is when this insight was produced.
	GeneratedAt time.Time `json:"generated_at"`

	// AnalysisRunID links back to the GoldAnalysisRun that produced this.
	AnalysisRunID string `json:"analysis_run_id"`
}

// EvidencePoint is a specific data reference that supports an insight.
type EvidencePoint struct {
	// Metric is the measured value (e.g., "failure_rate", "correction_count").
	Metric string `json:"metric"`

	// Value is the numeric or string value observed.
	Value string `json:"value"`

	// Source indicates where this evidence came from (e.g., "silver:tree_id:node_0001").
	Source string `json:"source"`

	// Context provides additional context for this evidence point.
	Context string `json:"context,omitempty"`
}

// InsightTimeRange defines the temporal scope of an insight.
type InsightTimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// GoldAnalysisRun records a single Gold analysis execution.
type GoldAnalysisRun struct {
	// ID is a unique run identifier.
	ID string `json:"id"`

	// ProjectHashes lists all projects analyzed in this run.
	ProjectHashes []string `json:"project_hashes"`

	// TreesAnalyzed is the number of Silver trees processed.
	TreesAnalyzed int `json:"trees_analyzed"`

	// InsightsGenerated is the count of insights produced.
	InsightsGenerated int `json:"insights_generated"`

	// DurationMs is how long the analysis took.
	DurationMs int64 `json:"duration_ms"`

	// StartedAt is when the run began.
	StartedAt time.Time `json:"started_at"`

	// CompletedAt is when the run finished.
	CompletedAt time.Time `json:"completed_at"`

	// Error is non-empty if the run failed.
	Error string `json:"error,omitempty"`
}

// AnalyzerConfig controls Gold analysis behavior.
type AnalyzerConfig struct {
	// SilverDirs maps project hashes to their silver tree directories.
	SilverDirs map[string]string `json:"silver_dirs"`

	// BronzeDirs maps project hashes to their bronze event directories.
	BronzeDirs map[string]string `json:"bronze_dirs"`

	// GoldDir is where Gold insights are persisted.
	GoldDir string `json:"gold_dir"`

	// LookbackDays is how many days of history to analyze. Default: 7.
	LookbackDays int `json:"lookback_days"`

	// MinSessionEvents is the minimum events for a session to be included.
	// Default: 10.
	MinSessionEvents int `json:"min_session_events"`

	// HighFailureRateThreshold triggers a tool efficiency insight.
	// Default: 0.05 (5%).
	HighFailureRateThreshold float64 `json:"high_failure_rate_threshold"`

	// HighCorrectionRateThreshold triggers a correction pattern insight.
	// Default: 0.10 (10% of user prompts are corrections).
	HighCorrectionRateThreshold float64 `json:"high_correction_rate_threshold"`

	// CrossProjectMinProjects requires this many projects for cross-project insights.
	// Default: 2.
	CrossProjectMinProjects int `json:"cross_project_min_projects"`
}

// DefaultAnalyzerConfig returns an AnalyzerConfig with sensible defaults.
func DefaultAnalyzerConfig() AnalyzerConfig {
	return AnalyzerConfig{
		SilverDirs:                  make(map[string]string),
		BronzeDirs:                  make(map[string]string),
		LookbackDays:                7,
		MinSessionEvents:            10,
		HighFailureRateThreshold:    0.05,
		HighCorrectionRateThreshold: 0.10,
		CrossProjectMinProjects:     2,
	}
}

// SessionStats is a computed statistics summary for a single Silver tree session.
type SessionStats struct {
	ProjectHash     string
	ConversationID  string
	Domain          string
	StartTime       time.Time
	EndTime         time.Time
	TotalEvents     int
	PhaseCount      int
	ErrorCount      int
	CorrectionCount int
	UserPromptCount int
	ToolFrequency   map[string]int
	DominantTools   []string

	// Derived metrics
	ErrorRate           float64
	CorrectionRate      float64
	EventsPerMinute     float64
	AvgPhaseSize        float64
	MostFailedTool      string
	MostFailedToolCount int
}
