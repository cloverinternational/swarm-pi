package deepwiki

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// RequestLog stores a single LLM request with full timing and response data.
// These are written to ~/.swarm/deepwiki/requests/ for later analysis.
type RequestLog struct {
	// ID is a unique identifier for this request
	ID string `json:"id"`

	// WikiID links this request to a wiki generation job
	WikiID string `json:"wiki_id,omitempty"`

	// Timestamp when the request started
	Timestamp time.Time `json:"timestamp"`

	// Phase indicates the generation phase (planning, skeleton, sections, synthesis, reflector)
	Phase string `json:"phase"`

	// Stage indicates the pipeline stage (A, B, C, D) for V2 pipeline
	Stage string `json:"stage,omitempty"`

	// PageTitle is the page being generated (if applicable)
	PageTitle string `json:"page_title,omitempty"`

	// Provider is the LLM provider (anthropic, openai, ollama, etc.)
	Provider string `json:"provider"`

	// Model is the model name
	Model string `json:"model"`

	// Request contains the prompts sent to the LLM
	Request RequestPayload `json:"request"`

	// Response contains the LLM response
	Response ResponsePayload `json:"response"`

	// Timing contains detailed timing information
	Timing TimingInfo `json:"timing"`

	// Tokens contains token usage (if available)
	Tokens TokenInfo `json:"tokens"`

	// Result indicates success/failure
	Result ResultInfo `json:"result"`

	// Metadata contains additional context
	Metadata map[string]any `json:"metadata,omitempty"`
}

// RequestPayload contains the prompts sent to the LLM
type RequestPayload struct {
	System string `json:"system,omitempty"`
	User   string `json:"user,omitempty"`

	// InputTokens is the estimated input token count
	InputTokens int `json:"input_tokens,omitempty"`

	// ContextFiles are files included in the context
	ContextFiles []string `json:"context_files,omitempty"`

	// ContextEntities are code entities included in the context
	ContextEntities []string `json:"context_entities,omitempty"`
}

// ResponsePayload contains the LLM response
type ResponsePayload struct {
	// Raw is the raw response text
	Raw string `json:"raw,omitempty"`

	// Cleaned is the cleaned/processed response
	Cleaned string `json:"cleaned,omitempty"`

	// OutputTokens is the token count of the output
	OutputTokens int `json:"output_tokens,omitempty"`

	// Sections is the number of sections generated (for page generation)
	Sections int `json:"sections,omitempty"`
}

// TimingInfo contains detailed timing information
type TimingInfo struct {
	// Start is when the request started
	Start time.Time `json:"start"`

	// End is when the response was received
	End time.Time `json:"end"`

	// Duration is the total time taken
	Duration time.Duration `json:"duration"`

	// FirstToken is time to first token (for streaming)
	FirstToken time.Duration `json:"first_token,omitempty"`

	// TPS is tokens per second (calculated from output tokens / duration)
	TPS float64 `json:"tps,omitempty"`

	// Queued is time spent waiting in queue before execution
	Queued time.Duration `json:"queued,omitempty"`
}

// TokenInfo contains token usage information
type TokenInfo struct {
	Input         int `json:"input,omitempty"`
	Output        int `json:"output,omitempty"`
	CacheCreation int `json:"cache_creation,omitempty"`
	CacheRead     int `json:"cache_read,omitempty"`
	Total         int `json:"total,omitempty"`
}

// ResultInfo indicates the result of the request
type ResultInfo struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`

	// RetryCount is the number of retries attempted
	RetryCount int `json:"retry_count,omitempty"`

	// FallbackUsed indicates if a fallback LLM was used
	FallbackUsed bool `json:"fallback_used,omitempty"`

	// RateLimited indicates if the request hit a rate limit
	RateLimited bool `json:"rate_limited,omitempty"`
}

// RequestLogger manages logging of DeepWiki requests to disk
type RequestLogger struct {
	baseDir string
	wikiID  string
	enabled bool
}

// NewRequestLogger creates a new request logger
func NewRequestLogger(wikiID string) *RequestLogger {
	baseDir := paths.In("deepwiki", "requests")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		log.Printf("[deepwiki] Warning: Failed to create request log directory: %v", err)
		return &RequestLogger{enabled: false}
	}
	return &RequestLogger{
		baseDir: baseDir,
		wikiID:  wikiID,
		enabled: true,
	}
}

// Log writes a request log to disk (synchronous to ensure logs are written)
func (l *RequestLogger) Log(req *RequestLog) {
	log.Printf("[deepwiki/request_log] Log called: enabled=%v wikiID=%s", l.enabled, l.wikiID)
	if !l.enabled {
		log.Printf("[deepwiki/request_log] Logging disabled, skipping")
		return
	}

	if req.ID == "" {
		req.ID = fmt.Sprintf("%d-%s", time.Now().UnixNano(), req.Phase)
	}
	if req.WikiID == "" {
		req.WikiID = l.wikiID
	}
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now()
	}

	log.Printf("[deepwiki/request_log] Writing log: phase=%s stage=%s", req.Phase, req.Stage)
	// Synchronous write to ensure logs are captured
	l.writeLog(req)
}

func (l *RequestLogger) writeLog(req *RequestLog) {
	filename := fmt.Sprintf("%s.jsonl", req.Timestamp.Format("2006-01-02"))
	path := filepath.Join(l.baseDir, filename)
	log.Printf("[deepwiki/request_log] Writing to: %s", path)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("[deepwiki/request_log] Failed to open file: %v", err)
		return
	}
	defer f.Close()

	data, err := json.Marshal(req)
	if err != nil {
		log.Printf("[deepwiki/request_log] Failed to marshal: %v", err)
		return
	}
	n, err := f.Write(data)
	if err != nil {
		log.Printf("[deepwiki/request_log] Failed to write: %v", err)
	}
	f.Write([]byte("\n"))
	log.Printf("[deepwiki/request_log] Wrote %d bytes to %s", n, path)
}

// -----------------------------------------------------------------------------

// AnalysisResult contains the analysis of DeepWiki request logs
type AnalysisResult struct {
	// Summary contains overall statistics
	Summary AnalysisSummary `json:"summary"`

	// ByPhase contains per-phase statistics
	ByPhase map[string]PhaseStats `json:"by_phase"`

	// ByStage contains per-stage statistics (for V2 pipeline)
	ByStage map[string]StageStats `json:"by_stage"`

	// ByPage contains per-page statistics
	ByPage map[string]PageStats `json:"by_page"`

	// Timeline contains chronological events
	Timeline []TimelineEvent `json:"timeline"`

	// SlowRequests contains the slowest requests
	SlowRequests []RequestSummary `json:"slow_requests"`

	// Errors contains failed requests
	Errors []RequestSummary `json:"errors"`

	// Patterns contains detected patterns
	Patterns []DetectedPattern `json:"patterns"`
}

// AnalysisSummary contains overall statistics
type AnalysisSummary struct {
	TotalRequests   int           `json:"total_requests"`
	TotalDuration   time.Duration `json:"total_duration"`
	TotalTokens     TokenTotals   `json:"total_tokens"`
	AverageTPS      float64       `json:"average_tps"`
	SuccessRate     float64       `json:"success_rate"`
	RetryCount      int           `json:"retry_count"`
	FallbackCount   int           `json:"fallback_count"`
	RateLimitCount  int           `json:"rate_limit_count"`
	UniqueProviders []string      `json:"unique_providers"`
	UniqueModels    []string      `json:"unique_models"`
	TimeRange       TimeRange     `json:"time_range"`
}

// TokenTotals contains total token usage
type TokenTotals struct {
	Input         int64 `json:"input"`
	Output        int64 `json:"output"`
	CacheCreation int64 `json:"cache_creation"`
	CacheRead     int64 `json:"cache_read"`
	Total         int64 `json:"total"`
}

// TimeRange contains the time range of logs
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// PhaseStats contains per-phase statistics
type PhaseStats struct {
	Count         int           `json:"count"`
	TotalDuration time.Duration `json:"total_duration"`
	AvgDuration   time.Duration `json:"avg_duration"`
	AvgTPS        float64       `json:"avg_tps"`
	TotalTokens   TokenTotals   `json:"total_tokens"`
	SuccessRate   float64       `json:"success_rate"`
}

// StageStats contains per-stage statistics (V2 pipeline)
type StageStats struct {
	Stage         string        `json:"stage"`
	Name          string        `json:"name"`
	Count         int           `json:"count"`
	TotalDuration time.Duration `json:"total_duration"`
	AvgDuration   time.Duration `json:"avg_duration"`
	AvgTPS        float64       `json:"avg_tps"`
	TotalTokens   TokenTotals   `json:"total_tokens"`
	Pages         []string      `json:"pages,omitempty"`
}

// PageStats contains per-page statistics
type PageStats struct {
	Title         string        `json:"title"`
	StageCount    int           `json:"stage_count"`
	TotalDuration time.Duration `json:"total_duration"`
	Stages        []StageInfo   `json:"stages"`
}

// StageInfo contains info about a stage for a page
type StageInfo struct {
	Stage    string        `json:"stage"`
	Duration time.Duration `json:"duration"`
	Tokens   TokenTotals   `json:"tokens"`
}

// TimelineEvent represents a chronological event
type TimelineEvent struct {
	Time      time.Time `json:"time"`
	Phase     string    `json:"phase"`
	Stage     string    `json:"stage,omitempty"`
	PageTitle string    `json:"page_title,omitempty"`
	Duration  string    `json:"duration,omitempty"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
}

// RequestSummary is a summary of a request
type RequestSummary struct {
	ID        string        `json:"id"`
	Timestamp time.Time     `json:"timestamp"`
	Phase     string        `json:"phase"`
	Stage     string        `json:"stage,omitempty"`
	PageTitle string        `json:"page_title,omitempty"`
	Duration  time.Duration `json:"duration"`
	TPS       float64       `json:"tps"`
	Error     string        `json:"error,omitempty"`
}

// DetectedPattern represents a detected pattern in the logs
type DetectedPattern struct {
	Type        string   `json:"type"` // "slow_phase", "rate_limit_burst", "retry_pattern", etc.
	Description string   `json:"description"`
	Count       int      `json:"count"`
	Examples    []string `json:"examples,omitempty"`
	Severity    string   `json:"severity"` // "info", "warning", "critical"
}

// AnalyzeLogs analyzes request logs from the given directory
func AnalyzeLogs(logDir string, filter LogFilter) (*AnalysisResult, error) {
	if logDir == "" {
		logDir = paths.In("deepwiki", "requests")
	}

	files, err := filepath.Glob(filepath.Join(logDir, "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("glob error: %w", err)
	}

	var logs []RequestLog
	for _, f := range files {
		fileLogs, err := readLogsFromFile(f, filter)
		if err != nil {
			continue
		}
		logs = append(logs, fileLogs...)
	}

	if len(logs) == 0 {
		return nil, fmt.Errorf("no logs found in %s", logDir)
	}

	return analyzeLogs(logs), nil
}

// LogFilter filters which logs to analyze
type LogFilter struct {
	WikiID      string        `json:"wiki_id,omitempty"`
	StartDate   *time.Time    `json:"start_date,omitempty"`
	EndDate     *time.Time    `json:"end_date,omitempty"`
	Phase       string        `json:"phase,omitempty"`
	PageTitle   string        `json:"page_title,omitempty"`
	MinDuration time.Duration `json:"min_duration,omitempty"`
}

func readLogsFromFile(path string, filter LogFilter) ([]RequestLog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var logs []RequestLog
	lines := strings.SplitSeq(string(data), "\n")
	for line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var log RequestLog
		if err := json.Unmarshal([]byte(line), &log); err != nil {
			continue
		}
		if matchesFilter(log, filter) {
			logs = append(logs, log)
		}
	}
	return logs, nil
}

func matchesFilter(log RequestLog, filter LogFilter) bool {
	if filter.WikiID != "" && log.WikiID != filter.WikiID {
		return false
	}
	if filter.StartDate != nil && log.Timestamp.Before(*filter.StartDate) {
		return false
	}
	if filter.EndDate != nil && log.Timestamp.After(*filter.EndDate) {
		return false
	}
	if filter.Phase != "" && log.Phase != filter.Phase {
		return false
	}
	if filter.PageTitle != "" && log.PageTitle != filter.PageTitle {
		return false
	}
	if filter.MinDuration > 0 && log.Timing.Duration < filter.MinDuration {
		return false
	}
	return true
}

func analyzeLogs(logs []RequestLog) *AnalysisResult {
	result := &AnalysisResult{
		ByPhase: make(map[string]PhaseStats),
		ByStage: make(map[string]StageStats),
		ByPage:  make(map[string]PageStats),
	}

	// Sort by timestamp
	for i := 0; i < len(logs)-1; i++ {
		for j := i + 1; j < len(logs); j++ {
			if logs[i].Timestamp.After(logs[j].Timestamp) {
				logs[i], logs[j] = logs[j], logs[i]
			}
		}
	}

	providers := make(map[string]bool)
	models := make(map[string]bool)
	var totalTime time.Duration
	var totalTPS float64
	var tpsCount int
	var successCount int
	var totalInput, totalOutput, totalCache int64

	for _, log := range logs {
		providers[log.Provider] = true
		models[log.Model] = true
		totalTime += log.Timing.Duration

		if log.Timing.TPS > 0 {
			totalTPS += log.Timing.TPS
			tpsCount++
		}

		if log.Result.Success {
			successCount++
		}

		totalInput += int64(log.Tokens.Input)
		totalOutput += int64(log.Tokens.Output)
		totalCache += int64(log.Tokens.CacheCreation + log.Tokens.CacheRead)

		// By phase
		ps := result.ByPhase[log.Phase]
		ps.Count++
		ps.TotalDuration += log.Timing.Duration
		ps.TotalTokens.Input += int64(log.Tokens.Input)
		ps.TotalTokens.Output += int64(log.Tokens.Output)
		if log.Result.Success {
			ps.SuccessRate = (ps.SuccessRate*float64(ps.Count-1) + 1) / float64(ps.Count)
		} else {
			ps.SuccessRate = ps.SuccessRate * float64(ps.Count-1) / float64(ps.Count)
		}
		if log.Timing.TPS > 0 {
			ps.AvgTPS = (ps.AvgTPS*float64(ps.Count-1) + log.Timing.TPS) / float64(ps.Count)
		}
		result.ByPhase[log.Phase] = ps

		// By stage
		if log.Stage != "" {
			ss := result.ByStage[log.Stage]
			ss.Stage = log.Stage
			ss.Name = stageName(log.Stage)
			ss.Count++
			ss.TotalDuration += log.Timing.Duration
			ss.TotalTokens.Input += int64(log.Tokens.Input)
			ss.TotalTokens.Output += int64(log.Tokens.Output)
			if log.PageTitle != "" {
				ss.Pages = append(ss.Pages, log.PageTitle)
			}
			result.ByStage[log.Stage] = ss
		}

		// By page
		if log.PageTitle != "" {
			pageStats := result.ByPage[log.PageTitle]
			pageStats.Title = log.PageTitle
			pageStats.StageCount++
			pageStats.TotalDuration += log.Timing.Duration
			pageStats.Stages = append(pageStats.Stages, StageInfo{
				Stage:    log.Stage,
				Duration: log.Timing.Duration,
				Tokens:   TokenTotals{Input: int64(log.Tokens.Input), Output: int64(log.Tokens.Output)},
			})
			result.ByPage[log.PageTitle] = pageStats
		}

		// Timeline
		result.Timeline = append(result.Timeline, TimelineEvent{
			Time:      log.Timestamp,
			Phase:     log.Phase,
			Stage:     log.Stage,
			PageTitle: log.PageTitle,
			Duration:  log.Timing.Duration.String(),
			Success:   log.Result.Success,
			Error:     log.Result.Error,
		})

		// Slow requests
		if log.Timing.Duration > 30*time.Second {
			result.SlowRequests = append(result.SlowRequests, RequestSummary{
				ID:        log.ID,
				Timestamp: log.Timestamp,
				Phase:     log.Phase,
				Stage:     log.Stage,
				PageTitle: log.PageTitle,
				Duration:  log.Timing.Duration,
				TPS:       log.Timing.TPS,
			})
		}

		// Errors
		if !log.Result.Success {
			result.Errors = append(result.Errors, RequestSummary{
				ID:        log.ID,
				Timestamp: log.Timestamp,
				Phase:     log.Phase,
				Stage:     log.Stage,
				PageTitle: log.PageTitle,
				Error:     log.Result.Error,
			})
		}
	}

	// Calculate averages
	for phase, ps := range result.ByPhase {
		ps.AvgDuration = time.Duration(int64(ps.TotalDuration) / int64(ps.Count))
		result.ByPhase[phase] = ps
	}
	for stage, ss := range result.ByStage {
		ss.AvgDuration = time.Duration(int64(ss.TotalDuration) / int64(ss.Count))
		result.ByStage[stage] = ss
	}

	// Summary
	result.Summary = AnalysisSummary{
		TotalRequests: len(logs),
		TotalDuration: totalTime,
		TotalTokens: TokenTotals{
			Input:         totalInput,
			Output:        totalOutput,
			CacheCreation: totalCache,
			Total:         totalInput + totalOutput + totalCache,
		},
		AverageTPS:      safeDiv(totalTPS, tpsCount),
		SuccessRate:     safeDiv(float64(successCount), len(logs)),
		UniqueProviders: mapKeys(providers),
		UniqueModels:    mapKeys(models),
		TimeRange: TimeRange{
			Start: logs[0].Timestamp,
			End:   logs[len(logs)-1].Timestamp,
		},
	}

	// Detect patterns
	result.Patterns = detectPatterns(logs)

	return result
}

func stageName(stage string) string {
	switch stage {
	case "A":
		return "Skeleton"
	case "B":
		return "Sections"
	case "C":
		return "Synthesis"
	case "D":
		return "Reflector"
	default:
		return stage
	}
}

func safeDiv(a float64, b int) float64 {
	if b == 0 {
		return 0
	}
	return a / float64(b)
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func detectPatterns(logs []RequestLog) []DetectedPattern {
	var patterns []DetectedPattern

	// Detect rate limit bursts
	var rateLimits []RequestLog
	for _, log := range logs {
		if log.Result.RateLimited {
			rateLimits = append(rateLimits, log)
		}
	}
	if len(rateLimits) > 2 {
		patterns = append(patterns, DetectedPattern{
			Type:        "rate_limit_burst",
			Description: fmt.Sprintf("Multiple rate limits detected (%d occurrences)", len(rateLimits)),
			Count:       len(rateLimits),
			Severity:    "warning",
		})
	}

	// Detect slow sections stage
	var slowSections []RequestLog
	for _, log := range logs {
		if log.Stage == "B" && log.Timing.Duration > 60*time.Second {
			slowSections = append(slowSections, log)
		}
	}
	if len(slowSections) > 0 {
		patterns = append(patterns, DetectedPattern{
			Type:        "slow_sections",
			Description: fmt.Sprintf("%d section generations took >60s", len(slowSections)),
			Count:       len(slowSections),
			Severity:    "info",
		})
	}

	// Detect fallback usage
	var fallbacks []RequestLog
	for _, log := range logs {
		if log.Result.FallbackUsed {
			fallbacks = append(fallbacks, log)
		}
	}
	if len(fallbacks) > 0 {
		patterns = append(patterns, DetectedPattern{
			Type:        "fallback_usage",
			Description: fmt.Sprintf("%d requests used fallback LLM", len(fallbacks)),
			Count:       len(fallbacks),
			Severity:    "info",
		})
	}

	return patterns
}
