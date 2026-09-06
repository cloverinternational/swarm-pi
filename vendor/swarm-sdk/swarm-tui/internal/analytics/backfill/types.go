package backfill

import (
	"path/filepath"
	"time"

	sdkanalytics "github.com/Swarm-Code/mono/swarm-sdk/internal/analytics"
)

const (
	IncludeAll           = "all"
	IncludeConversations = "conversations"
	IncludeArtifacts     = "artifacts"
	ScopeAll             = "all"
	ScopeWorkspace       = "workspace"
)

type Options struct {
	ConversationsDir  string
	SwarmDir          string
	LegacyFindingsDir string
	LedgerPath        string
	Since             time.Time
	Until             time.Time
	Workspace         string
	Scope             string
	Include           string
	Limit             int
	DryRun            bool
	NoFlush           bool
	Requeue           bool
	FlushTimeout      time.Duration
}

type Result struct {
	StartedAt            time.Time                    `json:"started_at"`
	FinishedAt           time.Time                    `json:"finished_at"`
	DryRun               bool                         `json:"dry_run"`
	AnalyticsEnabled     bool                         `json:"analytics_enabled"`
	CollectorURL         string                       `json:"collector_url"`
	LedgerPath           string                       `json:"ledger_path"`
	ConversationsScanned int                          `json:"conversations_scanned"`
	EventsConsidered     int                          `json:"events_considered"`
	EventsEnqueued       int                          `json:"events_enqueued"`
	ArtifactsConsidered  int                          `json:"artifacts_considered"`
	ArtifactsEnqueued    int                          `json:"artifacts_enqueued"`
	SkippedLedger        int                          `json:"skipped_ledger"`
	SkippedFilter        int                          `json:"skipped_filter"`
	Errors               []string                     `json:"errors,omitempty"`
	SpoolBefore          sdkanalytics.DispatcherStats `json:"spool_before"`
	SpoolAfter           sdkanalytics.DispatcherStats `json:"spool_after"`
}

type StatusResult struct {
	AnalyticsEnabled bool                         `json:"analytics_enabled"`
	CollectorURL     string                       `json:"collector_url"`
	LedgerPath       string                       `json:"ledger_path"`
	Ledger           LedgerStats                  `json:"ledger"`
	Spool            sdkanalytics.DispatcherStats `json:"spool"`
}

type FlushResult struct {
	AnalyticsEnabled bool                         `json:"analytics_enabled"`
	CollectorURL     string                       `json:"collector_url"`
	SpoolBefore      sdkanalytics.DispatcherStats `json:"spool_before"`
	SpoolAfter       sdkanalytics.DispatcherStats `json:"spool_after"`
}

type LedgerStats struct {
	Entries   int `json:"entries"`
	Events    int `json:"events"`
	Artifacts int `json:"artifacts"`
}

func DefaultOptions(homeDir string) Options {
	return Options{
		ConversationsDir:  filepath.Join(homeDir, ".swarmos", "conversations"),
		SwarmDir:          filepath.Join(homeDir, ".swarm"),
		LegacyFindingsDir: filepath.Join(homeDir, ".swarmos", "findings"),
		LedgerPath:        filepath.Join(homeDir, ".swarmos", "analytics-backfill-ledger.jsonl"),
		Scope:             ScopeAll,
		Include:           IncludeAll,
		FlushTimeout:      60 * time.Second,
	}
}

func (r Result) limitReached(limit int) bool {
	return limit > 0 && r.EventsEnqueued+r.ArtifactsEnqueued >= limit
}
