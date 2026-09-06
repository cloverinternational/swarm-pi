package gold

import (
	"fmt"
	"strings"
	"time"
)

// analyzeCrossProject generates insights by comparing patterns across multiple projects.
// This is the Gold tier's unique contribution: patterns that only emerge when you look
// across the full portfolio of projects.
func (a *Analyzer) analyzeCrossProject(runID string, projectStats map[string][]SessionStats) []GoldInsight {
	if len(projectStats) < a.cfg.CrossProjectMinProjects {
		return nil
	}

	var insights []GoldInsight

	// 1. Find tools with high failure rates in multiple projects
	insights = append(insights, a.crossProjectToolFailures(runID, projectStats)...)

	// 2. Find correction patterns that recur across projects (universal user preferences)
	insights = append(insights, a.crossProjectCorrectionPatterns(runID, projectStats)...)

	// 3. Find domain-transfer insights (approaches that work in one domain applicable elsewhere)
	insights = append(insights, a.crossProjectDomainTransfer(runID, projectStats)...)

	return insights
}

// crossProjectToolFailures identifies tools that fail in multiple projects simultaneously.
// These are systemic issues rather than project-specific ones.
func (a *Analyzer) crossProjectToolFailures(runID string, projectStats map[string][]SessionStats) []GoldInsight {
	// Count how many projects each tool fails in
	toolProjectFailures := make(map[string][]string)        // tool → []projectHash with failures
	toolProjectRates := make(map[string]map[string]float64) // tool → projectHash → failRate

	var globalStart, globalEnd time.Time

	for projectHash, stats := range projectStats {
		toolErrors := make(map[string]int)
		toolTotal := make(map[string]int)

		for _, s := range stats {
			for tool, count := range s.ToolFrequency {
				toolTotal[tool] += count
			}
			if s.MostFailedTool != "" {
				toolErrors[s.MostFailedTool] += s.MostFailedToolCount
			}
			if globalStart.IsZero() || s.StartTime.Before(globalStart) {
				globalStart = s.StartTime
			}
			if s.EndTime.After(globalEnd) {
				globalEnd = s.EndTime
			}
		}

		for tool, errCount := range toolErrors {
			total := toolTotal[tool]
			if total == 0 {
				continue
			}
			failRate := float64(errCount) / float64(total)
			if failRate >= a.cfg.HighFailureRateThreshold {
				toolProjectFailures[tool] = append(toolProjectFailures[tool], projectHash)
				if toolProjectRates[tool] == nil {
					toolProjectRates[tool] = make(map[string]float64)
				}
				toolProjectRates[tool][projectHash] = failRate
			}
		}
	}

	var insights []GoldInsight
	timeRange := InsightTimeRange{Start: globalStart, End: globalEnd}

	for tool, projects := range toolProjectFailures {
		if len(projects) < 2 {
			continue // Only flag if failing in 2+ projects
		}

		// Compute average failure rate
		avgRate := 0.0
		for _, rate := range toolProjectRates[tool] {
			avgRate += rate
		}
		avgRate /= float64(len(projects))

		severity := SeverityMedium
		if len(projects) >= 3 || avgRate >= 0.15 {
			severity = SeverityHigh
		}

		evidence := []EvidencePoint{
			{
				Metric: "projects_affected",
				Value:  fmt.Sprintf("%d", len(projects)),
				Source: "gold:cross-project",
			},
			{
				Metric: "avg_failure_rate",
				Value:  fmt.Sprintf("%.1f%%", avgRate*100),
				Source: "gold:cross-project",
			},
		}
		for _, ph := range projects {
			evidence = append(evidence, EvidencePoint{
				Metric:  "project_failure_rate",
				Value:   fmt.Sprintf("%.1f%%", toolProjectRates[tool][ph]*100),
				Source:  fmt.Sprintf("gold:project:%s", ph),
				Context: ph,
			})
		}

		insights = append(insights, GoldInsight{
			ID:    fmt.Sprintf("%s-cross-tool-failure-%s", runID, strings.ToLower(tool)),
			Title: fmt.Sprintf("%s fails in %d projects (systemic issue)", tool, len(projects)),
			Description: fmt.Sprintf(
				"The %s tool has elevated failure rates across %d different projects (avg: %.1f%%). "+
					"This is a systemic issue, not project-specific. Affected projects: %s.",
				tool, len(projects), avgRate*100, strings.Join(projects, ", ")),
			Category:       CategoryCrossProjectPattern,
			Severity:       severity,
			Recommendation: fmt.Sprintf("Audit %s tool implementation for systematic failures. Add retry logic or pre-flight checks that apply globally.", tool),
			Evidence:       evidence,
			ProjectHashes:  projects,
			TimeRange:      timeRange,
			Tags:           []string{"cross-project", "tool-failure", strings.ToLower(tool)},
			GeneratedAt:    time.Now(),
			AnalysisRunID:  runID,
		})
	}

	return insights
}

// crossProjectCorrectionPatterns identifies correction behaviors that appear in multiple projects.
// These represent universal user preferences worth storing in the _universal MemPalace wing.
func (a *Analyzer) crossProjectCorrectionPatterns(runID string, projectStats map[string][]SessionStats) []GoldInsight {
	// Count projects with elevated correction rates
	var highCorrectionProjects []string
	projectCorrectionRates := make(map[string]float64)
	var globalStart, globalEnd time.Time

	for projectHash, stats := range projectStats {
		totalCorrections := 0
		totalUserPrompts := 0
		for _, s := range stats {
			totalCorrections += s.CorrectionCount
			totalUserPrompts += s.UserPromptCount
			if globalStart.IsZero() || s.StartTime.Before(globalStart) {
				globalStart = s.StartTime
			}
			if s.EndTime.After(globalEnd) {
				globalEnd = s.EndTime
			}
		}
		if totalUserPrompts > 0 {
			rate := float64(totalCorrections) / float64(totalUserPrompts)
			projectCorrectionRates[projectHash] = rate
			if rate >= a.cfg.HighCorrectionRateThreshold {
				highCorrectionProjects = append(highCorrectionProjects, projectHash)
			}
		}
	}

	if len(highCorrectionProjects) < 2 {
		return nil
	}

	// Compute average correction rate across affected projects
	avgRate := 0.0
	for _, ph := range highCorrectionProjects {
		avgRate += projectCorrectionRates[ph]
	}
	avgRate /= float64(len(highCorrectionProjects))

	timeRange := InsightTimeRange{Start: globalStart, End: globalEnd}

	evidence := []EvidencePoint{
		{Metric: "projects_with_high_corrections", Value: fmt.Sprintf("%d/%d", len(highCorrectionProjects), len(projectStats)), Source: "gold:cross-project"},
		{Metric: "avg_correction_rate", Value: fmt.Sprintf("%.1f%%", avgRate*100), Source: "gold:cross-project"},
	}

	severity := SeverityMedium
	if avgRate >= 0.25 || len(highCorrectionProjects) >= 3 {
		severity = SeverityHigh
	}

	return []GoldInsight{{
		ID:    fmt.Sprintf("%s-cross-correction-pattern", runID),
		Title: fmt.Sprintf("User correction pattern found in %d projects (universal preference signal)", len(highCorrectionProjects)),
		Description: fmt.Sprintf(
			"High correction rates (≥%.0f%%) observed in %d of %d projects (avg rate: %.0f%%). "+
				"This indicates universal user preferences not yet captured in the memory palace. "+
				"Analyze verbatim corrections in Bronze data to extract concrete rules for the _universal wing.",
			a.cfg.HighCorrectionRateThreshold*100,
			len(highCorrectionProjects), len(projectStats), avgRate*100),
		Category: CategoryCrossProjectPattern,
		Severity: severity,
		Recommendation: "Mine Bronze user.prompt_submit events across all projects for common correction patterns. " +
			"Add the most frequent patterns as drawers in the _universal MemPalace wing so they apply to all future sessions.",
		Evidence:      evidence,
		ProjectHashes: highCorrectionProjects,
		TimeRange:     timeRange,
		Tags:          []string{"cross-project", "correction", "universal-preference"},
		GeneratedAt:   time.Now(),
		AnalysisRunID: runID,
	}}
}

// crossProjectDomainTransfer identifies successful patterns from one domain that could benefit another.
func (a *Analyzer) crossProjectDomainTransfer(runID string, projectStats map[string][]SessionStats) []GoldInsight {
	// Map each project to its dominant domain and average correction rate
	projectDomains := make(map[string]string)     // project → domain
	projectEfficiency := make(map[string]float64) // project → correction rate (lower = better)
	var globalStart, globalEnd time.Time

	for projectHash, stats := range projectStats {
		domainCount := make(map[string]int)
		totalCorrections := 0
		totalUserPrompts := 0
		for _, s := range stats {
			if s.Domain != "" {
				domainCount[s.Domain] += s.TotalEvents
			}
			totalCorrections += s.CorrectionCount
			totalUserPrompts += s.UserPromptCount
			if globalStart.IsZero() || s.StartTime.Before(globalStart) {
				globalStart = s.StartTime
			}
			if s.EndTime.After(globalEnd) {
				globalEnd = s.EndTime
			}
		}

		// Pick most active domain
		bestDomain := ""
		bestCount := 0
		for d, c := range domainCount {
			if c > bestCount {
				bestCount = c
				bestDomain = d
			}
		}
		if bestDomain != "" {
			projectDomains[projectHash] = bestDomain
		}

		if totalUserPrompts > 0 {
			projectEfficiency[projectHash] = float64(totalCorrections) / float64(totalUserPrompts)
		}
	}

	// Find domain pairs where one domain has significantly better efficiency
	domainEfficiency := make(map[string][]float64) // domain → []correction rates
	for ph, domain := range projectDomains {
		if rate, ok := projectEfficiency[ph]; ok {
			domainEfficiency[domain] = append(domainEfficiency[domain], rate)
		}
	}

	// Compute avg efficiency per domain
	domainAvgRate := make(map[string]float64)
	for domain, rates := range domainEfficiency {
		sum := 0.0
		for _, r := range rates {
			sum += r
		}
		domainAvgRate[domain] = sum / float64(len(rates))
	}

	if len(domainAvgRate) < 2 {
		return nil
	}

	// Find best (lowest correction rate) and worst domain
	bestDomain, worstDomain := "", ""
	bestRate, worstRate := 1.0, 0.0
	for d, rate := range domainAvgRate {
		if rate < bestRate {
			bestRate = rate
			bestDomain = d
		}
		if rate > worstRate {
			worstRate = rate
			worstDomain = d
		}
	}

	// Only surface if there's a meaningful difference (≥10 percentage points)
	if bestDomain == worstDomain || worstRate-bestRate < 0.10 {
		return nil
	}

	timeRange := InsightTimeRange{Start: globalStart, End: globalEnd}

	return []GoldInsight{{
		ID:    fmt.Sprintf("%s-domain-transfer", runID),
		Title: fmt.Sprintf("Domain transfer opportunity: %s techniques (%.0f%% correction) → %s (%.0f%% correction)", bestDomain, bestRate*100, worstDomain, worstRate*100),
		Description: fmt.Sprintf(
			"The %s domain achieves %.0f%% correction rate vs %s at %.0f%%. "+
				"Workflow approaches that work well in %s may improve %s sessions. "+
				"For example: iterative output review, clearer sub-task decomposition, or more frequent user check-ins.",
			bestDomain, bestRate*100, worstDomain, worstRate*100, bestDomain, worstDomain),
		Category: CategoryDomainTransfer,
		Severity: SeverityLow,
		Recommendation: fmt.Sprintf(
			"Review the approach patterns used in successful %s sessions and consider applying them to %s sessions. "+
				"Specifically: iterative output generation, shorter loops, and earlier validation checkpoints.",
			bestDomain, worstDomain),
		Evidence: []EvidencePoint{
			{Metric: "best_domain", Value: bestDomain, Source: "gold:cross-project"},
			{Metric: "best_correction_rate", Value: fmt.Sprintf("%.1f%%", bestRate*100), Source: "gold:cross-project"},
			{Metric: "worst_domain", Value: worstDomain, Source: "gold:cross-project"},
			{Metric: "worst_correction_rate", Value: fmt.Sprintf("%.1f%%", worstRate*100), Source: "gold:cross-project"},
		},
		TimeRange:     timeRange,
		Tags:          []string{"domain-transfer", bestDomain, worstDomain},
		GeneratedAt:   time.Now(),
		AnalysisRunID: runID,
	}}
}
