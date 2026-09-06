// Package findings provides local knowledge persistence and search capabilities
// for the Swarm SDK. This package implements the AAR-style findings database
// pattern, enabling agents to capture, index, and search tool execution results.
package findings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// Finding represents a captured tool execution result with context and metadata.
// This is the core data structure stored in the local findings cache.
type Finding struct {
	// FindingID is a unique UUID for this finding
	FindingID string `json:"finding_id"`

	// ToolName identifies which tool was executed
	ToolName string `json:"tool_name"`

	// ToolInput contains the input parameters to the tool
	ToolInput map[string]any `json:"tool_input"`

	// ToolOutput contains the result returned by the tool
	ToolOutput map[string]any `json:"tool_output"`

	// Timestamp when the finding was captured
	Timestamp time.Time `json:"timestamp"`

	// AgentID identifies which agent created this finding
	AgentID string `json:"agent_id"`

	// ConversationID links this finding to a conversation
	ConversationID string `json:"conversation_id,omitempty"`

	// ContextSummary provides a brief description of the execution context
	ContextSummary string `json:"context_summary"`

	// Tags for categorization and filtering
	Tags []string `json:"tags,omitempty"`

	// Embedding is the vector representation for semantic search (populated by indexer)
	Embedding []float32 `json:"embedding,omitempty"`

	// ParentFindingID links to a previous related finding (for lineage tracking)
	ParentFindingID string `json:"parent_finding_id,omitempty"`

	// Metadata contains additional structured information
	Metadata FindingMetadata `json:"metadata"`
}

// FindingMetadata contains additional metadata about a finding
type FindingMetadata struct {
	// DurationMs records how long the tool execution took
	DurationMs int64 `json:"duration_ms,omitempty"`

	// TokensUsed records LLM token consumption if applicable
	TokensUsed int `json:"tokens_used,omitempty"`

	// Success indicates whether the tool execution succeeded
	Success bool `json:"success"`

	// ErrorMessage captures error details if execution failed
	ErrorMessage string `json:"error_message,omitempty"`

	// Priority indicates the importance of this finding (0-100, with 2 decimal precision)
	Priority float64 `json:"priority,omitempty"`

	// Source indicates where this finding originated (local, synced, imported)
	Source string `json:"source,omitempty"`

	// Custom allows arbitrary additional metadata
	Custom map[string]any `json:"custom,omitempty"`
}

// FindingQuery represents a search query against the findings database
type FindingQuery struct {
	// TextQuery for full-text search
	TextQuery string

	// SemanticQuery enables vector similarity search
	SemanticQuery string

	// Filters for structured field filtering
	Filters map[string]any

	// Tags to match
	Tags []string

	// ToolName to filter by specific tool
	ToolName string

	// AgentID to filter by specific agent
	AgentID string

	// TimeRange restricts results to a time window
	TimeRange *TimeRange

	// Limit on number of results
	Limit int

	// Offset for pagination
	Offset int
}

// TimeRange specifies a time window for queries
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// FindingResult wraps a finding with search relevance info
type FindingResult struct {
	Finding Finding

	// Score indicates relevance (higher = more relevant)
	Score float64

	// MatchType indicates how the result was matched (text, semantic, tag)
	MatchType string
}

// AnalysisResult represents the output of analyzing a tool result
type AnalysisResult struct {
	// ShouldCapture indicates whether to persist this as a finding
	ShouldCapture bool

	// ShouldCreateTask indicates whether to create a follow-up task
	ShouldCreateTask bool

	// TaskDescription provides details if ShouldCreateTask is true
	TaskDescription string

	// Tags to apply to the finding
	Tags []string

	// Priority for the finding (0-100, with 2 decimal precision for meaningful scores like 72.45)
	Priority float64

	// Insights extracted from the analysis
	Insights []string

	// RelatedFindingIDs link to prior similar findings
	RelatedFindingIDs []string
}

// ResultAnalyzer is the interface for analyzing tool execution results
type ResultAnalyzer interface {
	// Analyze evaluates a tool result and returns analysis
	Analyze(ctx context.Context, toolName string, input, output map[string]any) (AnalysisResult, error)

	// Name returns the analyzer's identifier
	Name() string
}

// Cache provides local storage for findings
type Cache interface {
	// Write persists a finding to local cache
	Write(ctx context.Context, finding Finding) error

	// Read retrieves a finding by ID
	Read(ctx context.Context, findingID string) (Finding, error)

	// Query searches the cache
	Query(ctx context.Context, query FindingQuery) ([]FindingResult, error)

	// Delete removes a finding
	Delete(ctx context.Context, findingID string) error

	// List returns all findings (paginated)
	List(ctx context.Context, limit, offset int) ([]Finding, error)

	// Close releases resources
	Close() error
}

// SemanticIndex provides vector similarity search
type SemanticIndex interface {
	// Index adds or updates a finding in the semantic index
	Index(ctx context.Context, finding Finding) error

	// SemanticSearch finds similar findings by vector query
	SemanticSearch(ctx context.Context, query string, limit int) ([]FindingResult, error)

	// Delete removes a finding from the index
	Delete(ctx context.Context, findingID string) error

	// Close releases resources
	Close() error
}

// SyncEngine handles bidirectional synchronization with remote servers
type SyncEngine interface {
	// Push uploads local findings to remote
	Push(ctx context.Context, findings []Finding) error

	// Pull downloads findings from remote
	Pull(ctx context.Context, since time.Time) ([]Finding, error)

	// Sync performs bidirectional sync
	Sync(ctx context.Context) error

	// GetStatus returns current sync status
	GetStatus() SyncStatus
}

// SyncStatus represents the current synchronization state
type SyncStatus struct {
	LastSync      time.Time
	PendingLocal  int
	PendingRemote int
	Connected     bool
	Error         error
}

// Config holds findings system configuration
type Config struct {
	// LocalCacheDir is the directory for local findings storage
	LocalCacheDir string

	// MaxCacheSizeMB limits the total cache size
	MaxCacheSizeMB int

	// RetentionDays specifies how long to keep findings
	RetentionDays int

	// EnableSemanticIndex enables vector similarity search
	EnableSemanticIndex bool

	// SyncEndpoint is the remote findings server URL
	SyncEndpoint string

	// SyncEnabled controls whether syncing is active
	SyncEnabled bool

	// AutoCaptureTools lists tools to auto-capture (empty = all)
	AutoCaptureTools []string
}

// DefaultConfig returns sensible default configuration.
// Findings are stored in ~/.swarm/projects/<workspace-hash>/findings/
// when workspaceDir is provided, otherwise falls back to ~/.swarm/findings.
func DefaultConfig() Config {
	return Config{
		LocalCacheDir:       getDefaultCacheDir(""),
		MaxCacheSizeMB:      1000,
		RetentionDays:       90,
		EnableSemanticIndex: true,
		SyncEnabled:         false,
		AutoCaptureTools:    []string{}, // Empty = all tools
	}
}

// DefaultConfigForWorkspace returns configuration for a specific workspace.
// Findings are stored in ~/.swarm/projects/<workspace-hash>/findings/.
func DefaultConfigForWorkspace(workspaceDir string) Config {
	return Config{
		LocalCacheDir:       getDefaultCacheDir(workspaceDir),
		MaxCacheSizeMB:      1000,
		RetentionDays:       90,
		EnableSemanticIndex: true,
		SyncEnabled:         false,
		AutoCaptureTools:    []string{}, // Empty = all tools
	}
}

// getDefaultCacheDir returns the default cache directory path.
// Uses ~/.swarm/projects/<hash>/findings when workspace is provided,
// otherwise falls back to ~/.swarm/findings.
func getDefaultCacheDir(workspaceDir string) string {
	if workspaceDir != "" {
		hash := workspaceHash(workspaceDir)
		return filepath.Join(paths.Root(), "projects", hash, "findings")
	}
	return filepath.Join(paths.Root(), "findings")
}

// workspaceHash returns a short hash of the workspace directory path.
func workspaceHash(workspaceDir string) string {
	h := sha256.New()
	h.Write([]byte(workspaceDir))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ExpandCacheDir expands the cache directory path, replacing ~ with home directory.
func ExpandCacheDir(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
