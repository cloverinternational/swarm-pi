package gold

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/silver"
)

// Analyzer is the Gold tier statistical reasoning engine.
// It analyzes Silver tree indices to produce GoldInsight records
// without requiring an LLM — insights emerge from statistical patterns.
type Analyzer struct {
	cfg AnalyzerConfig
}

// NewAnalyzer creates a new Gold analyzer with the given configuration.
func NewAnalyzer(cfg AnalyzerConfig) *Analyzer {
	if cfg.LookbackDays <= 0 {
		cfg.LookbackDays = 7
	}
	if cfg.MinSessionEvents <= 0 {
		cfg.MinSessionEvents = 10
	}
	if cfg.HighFailureRateThreshold <= 0 {
		cfg.HighFailureRateThreshold = 0.05
	}
	if cfg.HighCorrectionRateThreshold <= 0 {
		cfg.HighCorrectionRateThreshold = 0.10
	}
	if cfg.CrossProjectMinProjects <= 0 {
		cfg.CrossProjectMinProjects = 2
	}
	return &Analyzer{cfg: cfg}
}

// Run executes a full Gold analysis pass over all configured Silver trees.
// It loads recent trees, computes session statistics, and generates insights.
func (a *Analyzer) Run(ctx context.Context, runID string) (*GoldAnalysisRun, []GoldInsight, error) {
	startedAt := time.Now()
	run := &GoldAnalysisRun{
		ID:            runID,
		StartedAt:     startedAt,
		ProjectHashes: make([]string, 0, len(a.cfg.SilverDirs)),
	}

	// Collect session stats from all configured projects
	cutoff := time.Now().AddDate(0, 0, -a.cfg.LookbackDays)
	var allStats []SessionStats

	for projectHash, silverDir := range a.cfg.SilverDirs {
		run.ProjectHashes = append(run.ProjectHashes, projectHash)

		store, err := silver.NewSilverStore(silverDir)
		if err != nil {
			continue
		}

		trees, err := store.ListTreesSince(cutoff)
		if err != nil {
			continue
		}

		for _, tree := range trees {
			if tree.TotalEvents < a.cfg.MinSessionEvents {
				continue
			}
			stats := computeSessionStats(projectHash, tree)
			allStats = append(allStats, stats)
			run.TreesAnalyzed++
		}
	}

	if len(allStats) == 0 {
		run.CompletedAt = time.Now()
		run.DurationMs = time.Since(startedAt).Milliseconds()
		return run, nil, nil
	}

	// Generate insights from statistics
	var insights []GoldInsight

	// Per-project analyses
	projectStats := groupByProject(allStats)
	for projectHash, stats := range projectStats {
		insights = append(insights, a.analyzeToolEfficiency(runID, projectHash, stats)...)
		insights = append(insights, a.analyzeCorrectionPatterns(runID, projectHash, stats)...)
		insights = append(insights, a.analyzeSessionStructure(runID, projectHash, stats)...)
	}

	// Cross-project analysis
	if len(projectStats) >= a.cfg.CrossProjectMinProjects {
		insights = append(insights, a.analyzeCrossProject(runID, projectStats)...)
	}

	// Sort by severity (critical first)
	sort.Slice(insights, func(i, j int) bool {
		return severityRank(insights[i].Severity) > severityRank(insights[j].Severity)
	})

	run.InsightsGenerated = len(insights)
	run.CompletedAt = time.Now()
	run.DurationMs = time.Since(startedAt).Milliseconds()

	return run, insights, nil
}

// analyzeToolEfficiency detects tools with high failure rates or unusual usage patterns.
func (a *Analyzer) analyzeToolEfficiency(runID, projectHash string, stats []SessionStats) []GoldInsight {
	if len(stats) == 0 {
		return nil
	}

	// Aggregate tool usage across all sessions
	toolTotal := make(map[string]int)
	toolErrors := make(map[string]int)
	var timeRange InsightTimeRange

	for _, s := range stats {
		for tool, count := range s.ToolFrequency {
			toolTotal[tool] += count
		}
		if s.MostFailedTool != "" {
			toolErrors[s.MostFailedTool] += s.MostFailedToolCount
		}
		if timeRange.Start.IsZero() || s.StartTime.Before(timeRange.Start) {
			timeRange.Start = s.StartTime
		}
		if s.EndTime.After(timeRange.End) {
			timeRange.End = s.EndTime
		}
	}

	var insights []GoldInsight

	// Detect high-failure tools
	for tool, errorCount := range toolErrors {
		total := toolTotal[tool]
		if total == 0 {
			continue
		}
		failRate := float64(errorCount) / float64(total)
		if failRate >= a.cfg.HighFailureRateThreshold {
			severity := SeverityLow
			if failRate >= 0.15 {
				severity = SeverityHigh
			} else if failRate >= 0.08 {
				severity = SeverityMedium
			}

			insights = append(insights, GoldInsight{
				ID:             fmt.Sprintf("%s-tool-failure-%s", runID, strings.ToLower(tool)),
				Title:          fmt.Sprintf("%s has elevated failure rate: %.0f%%", tool, failRate*100),
				Description:    fmt.Sprintf("The %s tool failed %d times out of %d calls (%.1f%%) across %d sessions. This is above the %.0f%% threshold.", tool, errorCount, total, failRate*100, len(stats), a.cfg.HighFailureRateThreshold*100),
				Category:       CategoryToolEfficiency,
				Severity:       severity,
				Recommendation: fmt.Sprintf("Investigate why %s is failing frequently. Consider adding retry logic, better error handling, or pre-flight validation before calling %s.", tool, tool),
				Evidence: []EvidencePoint{
					{Metric: "failure_rate", Value: fmt.Sprintf("%.1f%%", failRate*100), Source: fmt.Sprintf("gold:project:%s", projectHash)},
					{Metric: "failure_count", Value: fmt.Sprintf("%d", errorCount), Source: fmt.Sprintf("gold:project:%s", projectHash)},
					{Metric: "total_calls", Value: fmt.Sprintf("%d", total), Source: fmt.Sprintf("gold:project:%s", projectHash)},
					{Metric: "sessions_analyzed", Value: fmt.Sprintf("%d", len(stats)), Source: fmt.Sprintf("gold:project:%s", projectHash)},
				},
				ProjectHash:   projectHash,
				TimeRange:     timeRange,
				Tags:          []string{"tool-failure", strings.ToLower(tool)},
				GeneratedAt:   time.Now(),
				AnalysisRunID: runID,
			})
		}
	}

	// Detect dominant tool overuse (one tool > 50% of calls)
	totalCalls := 0
	for _, count := range toolTotal {
		totalCalls += count
	}
	for tool, count := range toolTotal {
		if totalCalls > 0 && float64(count)/float64(totalCalls) > 0.5 {
			insights = append(insights, GoldInsight{
				ID:             fmt.Sprintf("%s-tool-dominant-%s", runID, strings.ToLower(tool)),
				Title:          fmt.Sprintf("%s dominates tool usage at %.0f%%", tool, float64(count)/float64(totalCalls)*100),
				Description:    fmt.Sprintf("In %d sessions, %s accounts for %.0f%% of all tool calls (%d/%d). Heavy reliance on a single tool may indicate workflow inefficiency.", len(stats), tool, float64(count)/float64(totalCalls)*100, count, totalCalls),
				Category:       CategoryToolEfficiency,
				Severity:       SeverityInfo,
				Recommendation: fmt.Sprintf("Review whether %s is being used optimally or if alternative tools could reduce load.", tool),
				Evidence: []EvidencePoint{
					{Metric: "tool_share", Value: fmt.Sprintf("%.0f%%", float64(count)/float64(totalCalls)*100), Source: fmt.Sprintf("gold:project:%s", projectHash)},
				},
				ProjectHash:   projectHash,
				TimeRange:     timeRange,
				Tags:          []string{"tool-dominance", strings.ToLower(tool)},
				GeneratedAt:   time.Now(),
				AnalysisRunID: runID,
			})
		}
	}

	return insights
}

// analyzeCorrectionPatterns detects sessions with high user correction rates.
func (a *Analyzer) analyzeCorrectionPatterns(runID, projectHash string, stats []SessionStats) []GoldInsight {
	if len(stats) == 0 {
		return nil
	}

	var highCorrectionSessions []SessionStats
	totalCorrections := 0
	totalUserPrompts := 0
	var timeRange InsightTimeRange

	for _, s := range stats {
		totalCorrections += s.CorrectionCount
		totalUserPrompts += s.UserPromptCount
		if s.CorrectionRate >= a.cfg.HighCorrectionRateThreshold {
			highCorrectionSessions = append(highCorrectionSessions, s)
		}
		if timeRange.Start.IsZero() || s.StartTime.Before(timeRange.Start) {
			timeRange.Start = s.StartTime
		}
		if s.EndTime.After(timeRange.End) {
			timeRange.End = s.EndTime
		}
	}

	var insights []GoldInsight

	if len(highCorrectionSessions) == 0 || totalUserPrompts == 0 {
		return insights
	}

	overallCorrectionRate := float64(totalCorrections) / float64(totalUserPrompts)
	sessionIDs := make([]string, 0, len(highCorrectionSessions))
	for _, s := range highCorrectionSessions {
		sessionIDs = append(sessionIDs, s.ConversationID)
	}

	severity := SeverityLow
	if overallCorrectionRate >= 0.25 {
		severity = SeverityCritical
	} else if overallCorrectionRate >= 0.20 {
		severity = SeverityHigh
	} else if overallCorrectionRate >= 0.15 {
		severity = SeverityMedium
	}

	// Find the most common domain in high-correction sessions
	domainCount := make(map[string]int)
	for _, s := range highCorrectionSessions {
		if s.Domain != "" {
			domainCount[s.Domain]++
		}
	}
	topDomain := topKey(domainCount)

	insights = append(insights, GoldInsight{
		ID:    fmt.Sprintf("%s-correction-rate", runID),
		Title: fmt.Sprintf("User correction rate is %.0f%% across %d sessions", overallCorrectionRate*100, len(stats)),
		Description: fmt.Sprintf(
			"%d of %d sessions had elevated correction rates (≥%.0f%%). Overall: %d corrections out of %d user prompts (%.0f%%)%s.",
			len(highCorrectionSessions), len(stats),
			a.cfg.HighCorrectionRateThreshold*100,
			totalCorrections, totalUserPrompts, overallCorrectionRate*100,
			domainInsert(topDomain),
		),
		Category: CategoryCorrectionPattern,
		Severity: severity,
		Recommendation: "Analyse verbatim correction prompts in Bronze data to identify systematic misalignments. " +
			"High correction rates indicate the agent is not meeting user expectations — " +
			"review steering context and dream palace entries for this project.",
		Evidence: []EvidencePoint{
			{Metric: "correction_rate", Value: fmt.Sprintf("%.0f%%", overallCorrectionRate*100), Source: fmt.Sprintf("gold:project:%s", projectHash)},
			{Metric: "sessions_with_corrections", Value: fmt.Sprintf("%d/%d", len(highCorrectionSessions), len(stats)), Source: fmt.Sprintf("gold:project:%s", projectHash)},
			{Metric: "total_corrections", Value: fmt.Sprintf("%d", totalCorrections), Source: fmt.Sprintf("gold:project:%s", projectHash)},
		},
		ProjectHash:   projectHash,
		SessionIDs:    sessionIDs,
		TimeRange:     timeRange,
		Tags:          []string{"correction", "user-feedback"},
		GeneratedAt:   time.Now(),
		AnalysisRunID: runID,
	})

	return insights
}

// analyzeSessionStructure detects unusual session patterns (very long, very short, burst activity).
func (a *Analyzer) analyzeSessionStructure(runID, projectHash string, stats []SessionStats) []GoldInsight {
	if len(stats) < 2 {
		return nil
	}

	var insights []GoldInsight
	var timeRange InsightTimeRange

	totalEvents := 0
	for _, s := range stats {
		totalEvents += s.TotalEvents
		if timeRange.Start.IsZero() || s.StartTime.Before(timeRange.Start) {
			timeRange.Start = s.StartTime
		}
		if s.EndTime.After(timeRange.End) {
			timeRange.End = s.EndTime
		}
	}
	avgEvents := float64(totalEvents) / float64(len(stats))

	// Find outlier sessions (> 3x avg events)
	var heavySessions []SessionStats
	for _, s := range stats {
		if float64(s.TotalEvents) > avgEvents*3 {
			heavySessions = append(heavySessions, s)
		}
	}

	if len(heavySessions) > 0 {
		sessionIDs := make([]string, 0, len(heavySessions))
		for _, s := range heavySessions {
			sessionIDs = append(sessionIDs, s.ConversationID)
		}

		insights = append(insights, GoldInsight{
			ID:          fmt.Sprintf("%s-heavy-sessions", runID),
			Title:       fmt.Sprintf("%d sessions significantly exceed average event count (avg=%.0f)", len(heavySessions), avgEvents),
			Description: fmt.Sprintf("%d of %d sessions have >3x the average event count (%.0f). Heavy sessions may indicate runaway loops or overly complex tasks.", len(heavySessions), len(stats), avgEvents),
			Category:    CategorySessionStructure,
			Severity:    SeverityMedium,
			Recommendation: "Review heavy sessions in Bronze data to check for repetitive tool loops or inefficient retry patterns. " +
				"Consider adding steering guides for when to stop and ask.",
			Evidence: []EvidencePoint{
				{Metric: "heavy_session_count", Value: fmt.Sprintf("%d", len(heavySessions)), Source: fmt.Sprintf("gold:project:%s", projectHash)},
				{Metric: "avg_events_per_session", Value: fmt.Sprintf("%.0f", avgEvents), Source: fmt.Sprintf("gold:project:%s", projectHash)},
			},
			ProjectHash:   projectHash,
			SessionIDs:    sessionIDs,
			TimeRange:     timeRange,
			Tags:          []string{"session-length", "efficiency"},
			GeneratedAt:   time.Now(),
			AnalysisRunID: runID,
		})
	}

	return insights
}

// --- Internal helpers ---

// computeSessionStats extracts statistical metrics from a SilverTree.
func computeSessionStats(projectHash string, tree silver.SilverTree) SessionStats {
	stats := SessionStats{
		ProjectHash:    projectHash,
		ConversationID: tree.ConversationID,
		Domain:         tree.DominantDomain,
		StartTime:      tree.StartTime,
		EndTime:        tree.EndTime,
		TotalEvents:    tree.TotalEvents,
		PhaseCount:     len(tree.Structure),
		ToolFrequency:  make(map[string]int),
	}

	// Aggregate from all nodes
	for _, node := range tree.Structure {
		aggregateNodeStats(&stats, node)
	}

	// Compute derived metrics
	if stats.UserPromptCount > 0 {
		stats.CorrectionRate = float64(stats.CorrectionCount) / float64(stats.UserPromptCount)
	}
	duration := tree.EndTime.Sub(tree.StartTime).Minutes()
	if duration > 0 {
		stats.EventsPerMinute = float64(stats.TotalEvents) / duration
	}
	if stats.PhaseCount > 0 {
		stats.AvgPhaseSize = float64(stats.TotalEvents) / float64(stats.PhaseCount)
	}

	// Compute error rate and find most failed tool
	totalCalls := 0
	for _, count := range stats.ToolFrequency {
		totalCalls += count
	}
	if totalCalls > 0 {
		stats.ErrorRate = float64(stats.ErrorCount) / float64(totalCalls)
	}

	// DominantTools
	type toolFreq struct {
		name  string
		count int
	}
	var tools []toolFreq
	for name, count := range stats.ToolFrequency {
		tools = append(tools, toolFreq{name, count})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].count > tools[j].count })
	for i := 0; i < 5 && i < len(tools); i++ {
		stats.DominantTools = append(stats.DominantTools, tools[i].name)
	}

	return stats
}

// aggregateNodeStats recursively accumulates stats from a SilverNode.
func aggregateNodeStats(stats *SessionStats, node silver.SilverNode) {
	stats.ErrorCount += node.ErrorCount
	stats.UserPromptCount += node.UserPromptCount
	stats.CorrectionCount += node.CorrectionCount

	for tool, count := range node.ToolFrequency {
		stats.ToolFrequency[tool] += count
	}

	if node.ErrorCount > 0 && len(node.DominantTools) > 0 {
		if node.ErrorCount > stats.MostFailedToolCount {
			stats.MostFailedToolCount = node.ErrorCount
			stats.MostFailedTool = node.DominantTools[0]
		}
	}

	for _, child := range node.Nodes {
		aggregateNodeStats(stats, child)
	}
}

// groupByProject groups SessionStats by project hash.
func groupByProject(stats []SessionStats) map[string][]SessionStats {
	result := make(map[string][]SessionStats)
	for _, s := range stats {
		result[s.ProjectHash] = append(result[s.ProjectHash], s)
	}
	return result
}

// severityRank maps severity to a numeric rank for sorting.
func severityRank(s InsightSeverity) int {
	switch s {
	case SeverityCritical:
		return 5
	case SeverityHigh:
		return 4
	case SeverityMedium:
		return 3
	case SeverityLow:
		return 2
	default:
		return 1
	}
}

// topKey returns the key with the highest count in a frequency map.
func topKey(m map[string]int) string {
	best := ""
	bestCount := 0
	for k, v := range m {
		if v > bestCount {
			bestCount = v
			best = k
		}
	}
	return best
}

// domainInsert returns a formatted domain string for insight descriptions.
func domainInsert(domain string) string {
	if domain == "" {
		return ""
	}
	return fmt.Sprintf(" (dominant domain: %s)", domain)
}
