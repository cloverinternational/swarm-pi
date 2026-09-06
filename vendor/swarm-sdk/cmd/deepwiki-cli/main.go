package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "generate":
		generateCmd(os.Args[2:])
	case "analyze":
		analyzeCmd(os.Args[2:])
	case "list":
		listCmd(os.Args[2:])
	case "show":
		showCmd(os.Args[2:])
	case "timeline":
		timelineCmd(os.Args[2:])
	case "stats":
		statsCmd(os.Args[2:])
	case "export":
		exportCmd(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`DeepWiki CLI - Generate wikis via deepwiki-ui bridge and analyze request logs

Usage:
  deepwiki-cli <command> [options]

Commands:
  generate <dir>         Generate wiki for a directory (calls deepwiki-ui API)
  analyze [dir]          Analyze request logs and show summary
  list [dir]             List all request log files
  show <file>            Show detailed view of a specific log file
  timeline [dir]         Show chronological timeline of requests
  stats [dir]            Show detailed statistics
  export [dir] [format]  Export analysis as JSON or markdown

Generate Options:
  --provider <name>      LLM provider (anthropic, openai, ollama, zhipu)
  --model <name>         Model name
  --max-pages <n>        Maximum pages to generate (default: auto)
  --workers <n>          Parallel workers (default: 8)
  --language <lang>      Output language (default: English)
  --exclude <dirs>       Comma-separated dirs to exclude
  --api-url <url>        DeepWiki UI API URL (default: http://localhost:8090)

Examples:
  # Generate wiki for a folder
  deepwiki-cli generate ./my-repo --provider zhipu --model glm-4-plus

  # Analyze logs from last generation
  deepwiki-cli analyze`)
}

// -----------------------------------------------------------------------------
// Generate Command - Calls deepwiki-ui bridge API
// -----------------------------------------------------------------------------

type GenerateRequest struct {
	RepoPath         string   `json:"repo_path"`
	Provider         string   `json:"provider"`
	Model            string   `json:"model"`
	Language         string   `json:"language"`
	MaxPages         int      `json:"max_pages"`
	Workers          int      `json:"workers"`
	ExcludeDirs      []string `json:"exclude_dirs,omitempty"`
	FastProvider     string   `json:"fast_provider,omitempty"`
	FastModel        string   `json:"fast_model,omitempty"`
	FallbackProvider string   `json:"fallback_provider,omitempty"`
	FallbackModel    string   `json:"fallback_model,omitempty"`
	Throttle         bool     `json:"throttle,omitempty"`
	MaxConcurrency   int      `json:"max_concurrency,omitempty"`
}

type SSEEvent struct {
	Type    string          `json:"type"`
	WikiID  string          `json:"wiki_id,omitempty"`
	Phase   string          `json:"phase,omitempty"`
	Step    string          `json:"step,omitempty"`
	Pct     int             `json:"pct,omitempty"`
	Current int             `json:"current,omitempty"`
	Total   int             `json:"total,omitempty"`
	Title   string          `json:"title,omitempty"`
	Page    json.RawMessage `json:"page,omitempty"`
	Error   string          `json:"error,omitempty"`
	Plan    json.RawMessage `json:"plan,omitempty"`
}

func generateCmd(args []string) {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	provider := fs.String("provider", "z.ai", "LLM provider")
	model := fs.String("model", "", "Model name")
	maxPages := fs.Int("max-pages", 0, "Maximum pages (0 = auto)")
	workers := fs.Int("workers", 8, "Parallel workers")
	language := fs.String("language", "English", "Output language")
	exclude := fs.String("exclude", "", "Comma-separated dirs to exclude")
	apiURL := fs.String("api-url", "http://localhost:8090", "DeepWiki UI API URL")
	fastProvider := fs.String("fast-provider", "", "Fast LLM provider")
	fastModel := fs.String("fast-model", "", "Fast LLM model")
	throttle := fs.Bool("throttle", false, "Enable throttling for rate-limited providers (z.ai)")
	maxConcurrency := fs.Int("max-concurrency", 0, "Max concurrent requests (0 = auto: 2 for z.ai, 8 otherwise)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: deepwiki-cli generate <dir> [options]\n\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nNote: For rate-limited providers like z.ai, use --throttle to reduce concurrency.\n")
	}

	fs.Parse(args)

	// Get repo path from remaining args
	var repoPath string
	remaining := fs.Args()
	if len(remaining) > 0 {
		repoPath = remaining[0]
	}
	if repoPath == "" {
		fmt.Fprintln(os.Stderr, "Error: directory path required")
		fs.Usage()
		os.Exit(1)
	}

	// Resolve absolute path
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		os.Exit(1)
	}

	// Default model based on provider
	modelName := *model
	if modelName == "" {
		switch strings.ToLower(*provider) {
		case "z.ai":
			modelName = "glm-4.7"
		case "zhipu":
			modelName = "glm-4-plus"
		case "anthropic":
			modelName = "claude-3-5-sonnet-20241022"
		case "openai":
			modelName = "gpt-4o"
		case "ollama":
			modelName = "qwen3:1.7b"
		default:
			fmt.Fprintf(os.Stderr, "Error: --model required for provider %s\n", *provider)
			os.Exit(1)
		}
	}

	fmt.Println("╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    DEEPWIKI GENERATION                           ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

	// Auto-throttle for rate-limited providers
	effectiveWorkers := *workers
	throttleEnabled := *throttle
	concurrencyLimit := *maxConcurrency

	// Auto-detect rate-limited providers
	isRateLimited := strings.ToLower(*provider) == "z.ai" || strings.ToLower(*provider) == "zhipu"
	if isRateLimited && !throttleEnabled {
		fmt.Println("\n  ⚠ Warning: z.ai provider has strict rate limits.")
		fmt.Println("    Consider using --throttle for better reliability.")
	}
	if isRateLimited && concurrencyLimit == 0 {
		concurrencyLimit = 2 // Safe default for rate-limited providers
	}
	if throttleEnabled && concurrencyLimit == 0 {
		concurrencyLimit = 2
	}
	if concurrencyLimit > 0 && concurrencyLimit < effectiveWorkers {
		effectiveWorkers = concurrencyLimit
	}

	fmt.Printf("\n  Repository:    %s\n", absPath)
	fmt.Printf("  Provider:      %s\n", *provider)
	fmt.Printf("  Model:         %s\n", modelName)
	if *fastProvider != "" {
		fmt.Printf("  Fast LLM:      %s/%s\n", *fastProvider, *fastModel)
	}
	fmt.Printf("  Workers:       %d", effectiveWorkers)
	if throttleEnabled || (isRateLimited && concurrencyLimit > 0) {
		fmt.Printf(" (throttled, max %d concurrent)", concurrencyLimit)
	}
	fmt.Println()
	fmt.Printf("  Language:      %s\n", *language)
	fmt.Printf("  API URL:       %s\n", *apiURL)
	if *exclude != "" {
		fmt.Printf("  Exclude:       %s\n", *exclude)
	}
	fmt.Println()

	// Build request
	var excludeDirs []string
	if *exclude != "" {
		excludeDirs = strings.Split(*exclude, ",")
		for i, d := range excludeDirs {
			excludeDirs[i] = strings.TrimSpace(d)
		}
	}

	req := GenerateRequest{
		RepoPath:       absPath,
		Provider:       *provider,
		Model:          modelName,
		Language:       *language,
		MaxPages:       *maxPages,
		Workers:        effectiveWorkers,
		ExcludeDirs:    excludeDirs,
		FastProvider:   *fastProvider,
		FastModel:      *fastModel,
		Throttle:       throttleEnabled,
		MaxConcurrency: concurrencyLimit,
	}

	body, err := json.Marshal(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling request: %v\n", err)
		os.Exit(1)
	}

	// Call the API
	fmt.Println("┌─ GENERATING ─────────────────────────────────────────────────────┐")

	startTime := time.Now()
	httpReq, err := http.NewRequest("POST", *apiURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
		os.Exit(1)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0} // No timeout for SSE
	resp, err := client.Do(httpReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n  ✗ Request failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "\n  Is the deepwiki-ui server running? Start it with:\n")
		fmt.Fprintf(os.Stderr, "    cd deepwiki-ui/server && go run .\n")
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "\n  ✗ API error (%d): %s\n", resp.StatusCode, string(bodyBytes))
		os.Exit(1)
	}

	// Parse SSE stream
	var wikiID string
	var totalPages, currentPage int

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines
		if line == "" {
			continue
		}

		// Parse SSE data
		if after, ok := strings.CutPrefix(line, "data: "); ok {
			data := after
			var event SSEEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			switch event.Type {
			case "job_start":
				wikiID = event.WikiID
				fmt.Printf("  Job started: %s\n", wikiID)

			case "progress":
				elapsed := time.Since(startTime).Round(time.Second)
				title := event.Title
				if len(title) > 35 {
					title = title[:32] + "..."
				}
				if event.Current > 0 && event.Total > 0 {
					currentPage = event.Current
					totalPages = event.Total
					pct := float64(currentPage) / float64(totalPages) * 100
					fmt.Printf("\r  [%d/%d] (%.0f%%) %s [%s]    ",
						currentPage, totalPages, pct, title, elapsed)
				} else {
					fmt.Printf("\r  %s [%s]    ", event.Step, elapsed)
				}

			case "plan":
				fmt.Printf("\n  ✓ Wiki structure planned\n")

			case "page":
				elapsed := time.Since(startTime).Round(time.Second)
				if event.Current > 0 && event.Total > 0 {
					currentPage = event.Current
					totalPages = event.Total
					pct := float64(currentPage) / float64(totalPages) * 100
					title := event.Title
					if len(title) > 35 {
						title = title[:32] + "..."
					}
					fmt.Printf("\r  [%d/%d] (%.0f%%) %s [%s]    ",
						currentPage, totalPages, pct, title, elapsed)
				}

			case "done":
				elapsed := time.Since(startTime).Round(time.Second)
				fmt.Printf("\n  ✓ Generation complete in %s\n", elapsed)
				wikiID = event.WikiID

			case "error":
				fmt.Printf("\n  ✗ Error: %s\n", event.Error)
				os.Exit(1)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "\n  ✗ Stream error: %v\n", err)
		os.Exit(1)
	}

	// Summary
	fmt.Println("\n┌─ SUMMARY ────────────────────────────────────────────────────────┐")
	fmt.Printf("  Wiki ID:       %s\n", wikiID)
	fmt.Printf("  Repository:    %s\n", filepath.Base(absPath))
	fmt.Printf("  Pages:         %d\n", totalPages)
	fmt.Printf("  Elapsed:       %s\n", time.Since(startTime).Round(time.Second))
	fmt.Println("\n╚══════════════════════════════════════════════════════════════════╝")
	fmt.Println("\n  View in browser: http://localhost:5173")
	fmt.Println("\n  Analyze logs: deepwiki-cli analyze --wiki-id " + wikiID)
}

// -----------------------------------------------------------------------------
// Analyze Command
// -----------------------------------------------------------------------------

func analyzeCmd(args []string) {
	filter, dir := parseFilter(args)

	result, err := analyzeLogs(dir, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	printAnalysis(result)
}

type LogFilter struct {
	WikiID      string
	StartDate   *time.Time
	EndDate     *time.Time
	Phase       string
	PageTitle   string
	MinDuration time.Duration
}

func parseFilter(args []string) (LogFilter, string) {
	var filter LogFilter
	var dir string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--wiki-id":
			if i+1 < len(args) {
				filter.WikiID = args[i+1]
				i++
			}
		case "--phase":
			if i+1 < len(args) {
				filter.Phase = args[i+1]
				i++
			}
		case "--page":
			if i+1 < len(args) {
				filter.PageTitle = args[i+1]
				i++
			}
		case "--from":
			if i+1 < len(args) {
				t, err := time.Parse("2006-01-02", args[i+1])
				if err == nil {
					filter.StartDate = &t
				}
				i++
			}
		case "--to":
			if i+1 < len(args) {
				t, err := time.Parse("2006-01-02", args[i+1])
				if err == nil {
					filter.EndDate = &t
				}
				i++
			}
		case "--min-duration":
			if i+1 < len(args) {
				d, err := time.ParseDuration(args[i+1])
				if err == nil {
					filter.MinDuration = d
				}
				i++
			}
		default:
			if !strings.HasPrefix(args[i], "--") && dir == "" {
				dir = args[i]
			}
		}
	}
	return filter, dir
}

// Simple analysis functions (copied from SDK for standalone CLI)
type RequestLog struct {
	ID        string          `json:"id"`
	WikiID    string          `json:"wiki_id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Phase     string          `json:"phase"`
	Stage     string          `json:"stage,omitempty"`
	PageTitle string          `json:"page_title,omitempty"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Request   RequestPayload  `json:"request"`
	Response  ResponsePayload `json:"response"`
	Timing    TimingInfo      `json:"timing"`
	Tokens    TokenInfo       `json:"tokens"`
	Result    ResultInfo      `json:"result"`
}

type RequestPayload struct {
	System string `json:"system,omitempty"`
	User   string `json:"user,omitempty"`
}

type ResponsePayload struct {
	Raw string `json:"raw,omitempty"`
}

type TimingInfo struct {
	Start    time.Time     `json:"start"`
	End      time.Time     `json:"end"`
	Duration time.Duration `json:"duration"`
	TPS      float64       `json:"tps,omitempty"`
}

type TokenInfo struct {
	Input  int `json:"input,omitempty"`
	Output int `json:"output,omitempty"`
}

type ResultInfo struct {
	Success     bool   `json:"success"`
	Error       string `json:"error,omitempty"`
	RateLimited bool   `json:"rate_limited,omitempty"`
}

type AnalysisResult struct {
	TotalRequests int
	TotalDuration time.Duration
	SuccessRate   float64
	AverageTPS    float64
	TotalTokens   int64
	ByPhase       map[string]PhaseStats
	ByStage       map[string]StageStats
	SlowRequests  []RequestLog
	Errors        []RequestLog
	// Enhanced metrics
	WallClockTime     time.Duration
	EffectiveParallel float64
	IdealTime         time.Duration
	SuccessfulCount   int
	FailedCount       int
	SuccessfulTime    time.Duration
	FailedTime        time.Duration
	AvgSuccessDur     time.Duration
	AvgFailDur        time.Duration
	StartTime         time.Time
	EndTime           time.Time
}

type PhaseStats struct {
	Count         int
	TotalDuration time.Duration
	AvgTPS        float64
}

type StageStats struct {
	Count         int
	TotalDuration time.Duration
	AvgDuration   time.Duration
}

func analyzeLogs(logDir string, filter LogFilter) (*AnalysisResult, error) {
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

	return computeAnalysis(logs), nil
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

func computeAnalysis(logs []RequestLog) *AnalysisResult {
	result := &AnalysisResult{
		ByPhase: make(map[string]PhaseStats),
		ByStage: make(map[string]StageStats),
	}

	var totalTPS float64
	var tpsCount int
	var successCount int

	// Find time range for wall-clock calculation
	if len(logs) > 0 {
		result.StartTime = logs[0].Timestamp
		result.EndTime = logs[0].Timestamp
		for _, log := range logs {
			if log.Timestamp.Before(result.StartTime) {
				result.StartTime = log.Timestamp
			}
			if log.Timestamp.After(result.EndTime) {
				result.EndTime = log.Timestamp
			}
		}
		result.WallClockTime = result.EndTime.Sub(result.StartTime)
	}

	for _, log := range logs {
		result.TotalRequests++
		result.TotalDuration += log.Timing.Duration
		result.TotalTokens += int64(log.Tokens.Input + log.Tokens.Output)

		if log.Timing.TPS > 0 {
			totalTPS += log.Timing.TPS
			tpsCount++
		}

		// Determine success based on response presence (not just result.success)
		// because the logging code may not always set result.success correctly
		hasResponse := log.Response.Raw != ""
		isSuccess := hasResponse || log.Result.Success

		if isSuccess {
			successCount++
			result.SuccessfulCount++
			result.SuccessfulTime += log.Timing.Duration
		} else {
			result.FailedCount++
			result.FailedTime += log.Timing.Duration
			result.Errors = append(result.Errors, log)
		}

		// By phase
		ps := result.ByPhase[log.Phase]
		ps.Count++
		ps.TotalDuration += log.Timing.Duration
		if log.Timing.TPS > 0 {
			ps.AvgTPS = (ps.AvgTPS*float64(ps.Count-1) + log.Timing.TPS) / float64(ps.Count)
		}
		result.ByPhase[log.Phase] = ps

		// By stage
		if log.Stage != "" {
			ss := result.ByStage[log.Stage]
			ss.Count++
			ss.TotalDuration += log.Timing.Duration
			result.ByStage[log.Stage] = ss
		}

		// Slow requests
		if log.Timing.Duration > 30*time.Second {
			result.SlowRequests = append(result.SlowRequests, log)
		}
	}

	// Calculate derived metrics
	result.SuccessRate = float64(successCount) / float64(len(logs))
	if tpsCount > 0 {
		result.AverageTPS = totalTPS / float64(tpsCount)
	}

	// Calculate effective parallelism
	if result.WallClockTime > 0 {
		result.EffectiveParallel = float64(result.TotalDuration) / float64(result.WallClockTime)
	}

	// Calculate ideal time (time if no failures, same parallelism)
	if result.SuccessfulCount > 0 {
		result.AvgSuccessDur = time.Duration(int64(result.SuccessfulTime) / int64(result.SuccessfulCount))
	}
	if result.FailedCount > 0 {
		result.AvgFailDur = time.Duration(int64(result.FailedTime) / int64(result.FailedCount))
	}

	// Ideal time: if we ran with same parallelism but 100% success
	// We'd only need the successful work time, scaled by parallelism
	if result.EffectiveParallel > 0 && result.SuccessfulTime > 0 {
		result.IdealTime = time.Duration(float64(result.SuccessfulTime) / result.EffectiveParallel)
	}

	return result
}

func printAnalysis(r *AnalysisResult) {
	fmt.Println("\n╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    DEEPWIKI ANALYSIS SUMMARY                     ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

	fmt.Println("\n┌─ OVERVIEW ──────────────────────────────────────────────────────┐")
	fmt.Printf("  Total Requests:    %d (%d success, %d failed)\n",
		r.TotalRequests, r.SuccessfulCount, r.FailedCount)
	fmt.Printf("  Success Rate:      %.1f%%\n", r.SuccessRate*100)
	if r.AverageTPS > 0 {
		fmt.Printf("  Average TPS:       %.1f tokens/sec\n", r.AverageTPS)
	}
	if r.TotalTokens > 0 {
		fmt.Printf("  Total Tokens:      %s\n", formatNumber(r.TotalTokens))
	}

	// Time breakdown
	fmt.Println("\n┌─ TIMING ────────────────────────────────────────────────────────┐")
	if r.WallClockTime > 0 {
		fmt.Printf("  Wall-Clock Time:   %s (actual elapsed)\n", r.WallClockTime.Round(time.Second))
		fmt.Printf("  Total LLM Time:    %s (sum of all requests)\n", r.TotalDuration.Round(time.Second))
		fmt.Printf("  Effective Parallel: %.1fx\n", r.EffectiveParallel)
		fmt.Printf("  Time Range:        %s to %s\n",
			r.StartTime.Format("15:04:05"), r.EndTime.Format("15:04:05"))
	}

	// Performance analysis
	fmt.Println("\n┌─ PERFORMANCE ANALYSIS ───────────────────────────────────────────┐")
	fmt.Printf("  Successful Work:   %s (%d reqs × %s avg)\n",
		r.SuccessfulTime.Round(time.Second), r.SuccessfulCount, r.AvgSuccessDur.Round(time.Second))
	if r.FailedCount > 0 {
		fmt.Printf("  Failed Attempts:   %s (%d reqs × %s avg)\n",
			r.FailedTime.Round(time.Second), r.FailedCount, r.AvgFailDur.Round(time.Second))
		fmt.Printf("  Time Wasted:       %s (%.1f%% of total)\n",
			r.FailedTime.Round(time.Second),
			float64(r.FailedTime)/float64(r.TotalDuration)*100)
	}

	if r.IdealTime > 0 && r.FailedCount > 0 {
		fmt.Println("\n  ┌─ IDEAL SCENARIO (100%% success) ─────────────────────────────┐")
		fmt.Printf("  │ Ideal Time:        %s (with same parallelism)    │\n", r.IdealTime.Round(time.Second))
		timeSaved := r.WallClockTime - r.IdealTime
		pctSaved := float64(timeSaved) / float64(r.WallClockTime) * 100
		fmt.Printf("  │ Time Saved:        %s (%.1f%% improvement)         │\n",
			timeSaved.Round(time.Second), pctSaved)
		fmt.Println("  └──────────────────────────────────────────────────────────────┘")
	}

	if len(r.ByPhase) > 0 {
		fmt.Println("\n┌─ BY PHASE ──────────────────────────────────────────────────────┐")
		for phase, ps := range r.ByPhase {
			avg := time.Duration(int64(ps.TotalDuration) / int64(ps.Count))
			fmt.Printf("  %-12s  %3d calls  %8s total  %6s avg  %.0f TPS\n",
				phase, ps.Count, ps.TotalDuration.Round(time.Second), avg.Round(time.Millisecond), ps.AvgTPS)
		}
	}

	if len(r.ByStage) > 0 {
		fmt.Println("\n┌─ BY STAGE (V2 Pipeline) ─────────────────────────────────────────┐")
		stageOrder := []string{"A", "B", "C", "D"}
		stageNames := map[string]string{"A": "Skeleton", "B": "Sections", "C": "Synthesis", "D": "Reflector"}
		for _, stage := range stageOrder {
			if ss, ok := r.ByStage[stage]; ok {
				avg := time.Duration(int64(ss.TotalDuration) / int64(ss.Count))
				name := stageNames[stage]
				fmt.Printf("  Stage %s %-10s  %3d calls  %8s total  %6s avg\n",
					stage, name, ss.Count, ss.TotalDuration.Round(time.Second), avg.Round(time.Millisecond))
			}
		}
	}

	if len(r.SlowRequests) > 0 {
		fmt.Println("\n┌─ SLOW REQUESTS (>30s) ───────────────────────────────────────────┐")
		for i, req := range r.SlowRequests {
			if i >= 5 {
				fmt.Printf("  ... and %d more\n", len(r.SlowRequests)-5)
				break
			}
			title := req.PageTitle
			if len(title) > 25 {
				title = title[:22] + "..."
			}
			fmt.Printf("  %-12s %-3s  %-25s  %s\n",
				req.Phase, req.Stage, title, req.Timing.Duration.Round(time.Second))
		}
	}

	if len(r.Errors) > 0 && len(r.Errors) <= 10 {
		fmt.Println("\n┌─ FAILED REQUESTS ────────────────────────────────────────────────┐")
		for i, req := range r.Errors {
			if i >= 5 {
				fmt.Printf("  ... and %d more\n", len(r.Errors)-5)
				break
			}
			fmt.Printf("  %s/%s: %s (%s)\n", req.Phase, req.Stage,
				req.Timing.Duration.Round(time.Millisecond),
				shortError(req.Result.Error))
		}
	}

	fmt.Println("\n╚══════════════════════════════════════════════════════════════════╝")
}

func shortError(err string) string {
	if err == "" {
		return "rate limited or timeout"
	}
	if len(err) > 40 {
		return err[:37] + "..."
	}
	return err
}

func formatNumber(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// -----------------------------------------------------------------------------
// Other Commands
// -----------------------------------------------------------------------------

func listCmd(args []string) {
	_, dir := parseFilter(args)
	if dir == "" {
		dir = paths.In("deepwiki", "requests")
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Println("No request log files found.")
		return
	}

	fmt.Println("Request Log Files:")
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		fmt.Printf("  %s  (%s)  %s\n", filepath.Base(f), formatBytes(info.Size()), info.ModTime().Format("2006-01-02 15:04"))
	}
}

func formatBytes(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1f MB", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1f KB", float64(n)/1_000)
	}
	return fmt.Sprintf("%d B", n)
}

func showCmd(args []string) {
	if len(args) < 1 || strings.HasPrefix(args[0], "--") {
		fmt.Fprintln(os.Stderr, "Usage: deepwiki-cli show <file>")
		os.Exit(1)
	}

	file := args[0]
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	lines := strings.SplitSeq(string(data), "\n")
	for line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var log RequestLog
		if err := json.Unmarshal([]byte(line), &log); err != nil {
			continue
		}
		fmt.Printf("────────────────────────────────────────────────────────────\n")
		fmt.Printf("ID:        %s\n", log.ID)
		fmt.Printf("Timestamp: %s\n", log.Timestamp.Format(time.RFC3339))
		fmt.Printf("Phase:     %s", log.Phase)
		if log.Stage != "" {
			fmt.Printf(" (Stage %s)", log.Stage)
		}
		fmt.Println()
		if log.PageTitle != "" {
			fmt.Printf("Page:      %s\n", log.PageTitle)
		}
		fmt.Printf("Provider:  %s / %s\n", log.Provider, log.Model)
		fmt.Printf("Duration:  %s (%.1f TPS)\n", log.Timing.Duration, log.Timing.TPS)
		fmt.Printf("Tokens:    %d in, %d out\n", log.Tokens.Input, log.Tokens.Output)
		fmt.Printf("Success:   %v\n", log.Result.Success)
		if log.Result.Error != "" {
			fmt.Printf("Error:     %s\n", log.Result.Error)
		}
	}
}

func timelineCmd(args []string) {
	filter, dir := parseFilter(args)

	logDir := dir
	if logDir == "" {
		logDir = paths.In("deepwiki", "requests")
	}

	files, err := filepath.Glob(filepath.Join(logDir, "*.jsonl"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
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
		fmt.Println("No logs found.")
		return
	}

	fmt.Println("\n╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                        REQUEST TIMELINE                          ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

	for _, log := range logs {
		status := "✓"
		if !log.Result.Success {
			status = "✗"
		}
		title := log.PageTitle
		if len(title) > 30 {
			title = title[:27] + "..."
		}
		fmt.Printf("%s  %-8s %-3s  %-30s  %s  %s\n",
			log.Timestamp.Format("15:04:05"),
			log.Phase,
			log.Stage,
			title,
			log.Timing.Duration.Round(time.Millisecond),
			status)
	}
}

func statsCmd(args []string) {
	filter, dir := parseFilter(args)

	result, err := analyzeLogs(dir, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}

func exportCmd(args []string) {
	filter, dir := parseFilter(args)
	format := "json"

	for i, arg := range args {
		if !strings.HasPrefix(arg, "--") && i > 0 && args[i-1] != dir {
			format = arg
		}
	}

	result, err := analyzeLogs(dir, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	switch format {
	case "json":
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
	case "md", "markdown":
		printMarkdownReport(result)
	default:
		fmt.Fprintf(os.Stderr, "Unknown format: %s (use: json, md)\n", format)
		os.Exit(1)
	}
}

func printMarkdownReport(r *AnalysisResult) {
	fmt.Println("# DeepWiki Analysis Report")
	fmt.Println()
	fmt.Printf("**Generated:** %s\n\n", time.Now().Format(time.RFC3339))

	fmt.Println("## Summary")
	fmt.Printf("- **Total Requests:** %d\n", r.TotalRequests)
	fmt.Printf("- **Total Duration:** %s\n", r.TotalDuration.Round(time.Second))
	fmt.Printf("- **Success Rate:** %.1f%%\n", r.SuccessRate*100)
	fmt.Printf("- **Average TPS:** %.1f tokens/sec\n", r.AverageTPS)
	fmt.Printf("- **Total Tokens:** %s\n", formatNumber(r.TotalTokens))
}

var _ = io.EOF // silence unused import warning
