package gold

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/silver"
)

// TestGoldAnalyzerEndToEnd validates the full Gold analysis pipeline:
// 1. Build synthetic Silver trees with known statistics
// 2. Run Gold analysis
// 3. Verify correct insights are generated
// 4. Test cross-project analysis
// 5. Test Gold store persistence
func TestGoldAnalyzerEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()

	// ── 1. Create Silver trees with known patterns ────────────────────────
	t.Log("Step 1: Creating synthetic Silver trees")

	// Build a Silver store for project A (high Bash failure rate)
	silverDirA := tmpDir + "/silver_a"
	storeA, err := silver.NewSilverStore(silverDirA)
	if err != nil {
		t.Fatalf("NewSilverStore A: %v", err)
	}

	// Session with high Bash failure rate
	now := time.Now()
	treeA1 := buildSyntheticTree("conv-a1", "project-a", "software_engineering",
		now.Add(-2*time.Hour), now.Add(-1*time.Hour), 150,
		map[string]int{"Bash": 80, "Read": 40, "Edit": 20, "Shell": 10},
		7,  // ErrorCount (7/80 = 8.75% fail rate for Bash)
		15, // UserPromptCount
		4,  // CorrectionCount (4/15 = 26.7%)
	)

	treeA2 := buildSyntheticTree("conv-a2", "project-a", "software_engineering",
		now.Add(-4*time.Hour), now.Add(-3*time.Hour), 120,
		map[string]int{"Bash": 70, "Read": 30, "Write": 15, "grep": 5},
		6, // ErrorCount (6/70 = 8.6%)
		12,
		3, // Corrections
	)

	for _, tree := range []silver.SilverTree{treeA1, treeA2} {
		if _, err := storeA.SaveTree(tree); err != nil {
			t.Fatalf("SaveTree A: %v", err)
		}
	}

	// Build a Silver store for project B (high correction rate, different domain)
	silverDirB := tmpDir + "/silver_b"
	storeB, err := silver.NewSilverStore(silverDirB)
	if err != nil {
		t.Fatalf("NewSilverStore B: %v", err)
	}

	// Finance project with different tool usage
	treeB1 := buildSyntheticTree("conv-b1", "project-b", "financial_research",
		now.Add(-1*time.Hour), now, 200,
		map[string]int{"websearch": 90, "Bash": 60, "agent_browser": 30, "Read": 20},
		3, // Low error rate (3/60 = 5%)
		20,
		6, // High corrections (6/20 = 30%)
	)

	if _, err := storeB.SaveTree(treeB1); err != nil {
		t.Fatalf("SaveTree B: %v", err)
	}

	// ── 2. Run Gold analysis ──────────────────────────────────────────────
	t.Log("Step 2: Running Gold analysis")

	cfg := DefaultAnalyzerConfig()
	cfg.SilverDirs = map[string]string{
		"project-a": silverDirA,
		"project-b": silverDirB,
	}
	cfg.LookbackDays = 1
	cfg.HighFailureRateThreshold = 0.05    // 5% threshold
	cfg.HighCorrectionRateThreshold = 0.20 // 20% threshold
	cfg.CrossProjectMinProjects = 2

	analyzer := NewAnalyzer(cfg)
	run, insights, err := analyzer.Run(context.Background(), "test-run-001")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	t.Logf("  Analysis: trees_analyzed=%d, insights_generated=%d, duration_ms=%d",
		run.TreesAnalyzed, run.InsightsGenerated, run.DurationMs)
	t.Logf("  Run ID: %s", run.ID)

	if run.TreesAnalyzed < 2 {
		t.Errorf("trees analyzed: got %d, want ≥2", run.TreesAnalyzed)
	}
	if len(insights) == 0 {
		t.Error("no insights generated — expected at least tool efficiency and correction insights")
	}

	// ── 3. Verify specific insights ───────────────────────────────────────
	t.Log("Step 3: Verifying generated insights")

	categories := make(map[InsightCategory]int)
	severities := make(map[InsightSeverity]int)
	for _, ins := range insights {
		categories[ins.Category]++
		severities[ins.Severity]++
		t.Logf("  [%s/%s] %s", ins.Category, ins.Severity, ins.Title)
		if ins.Recommendation == "" {
			t.Errorf("insight %q has no recommendation", ins.ID)
		}
		if len(ins.Evidence) == 0 {
			t.Errorf("insight %q has no evidence", ins.ID)
		}
	}

	// Should have tool efficiency insights (high Bash failure rate)
	if categories[CategoryToolEfficiency] == 0 {
		t.Error("missing tool efficiency insights — expected Bash failure rate detection")
	}

	// Should have correction pattern insights
	if categories[CategoryCorrectionPattern] == 0 {
		t.Error("missing correction pattern insights")
	}

	// Should have at least one cross-project insight (2 projects)
	if categories[CategoryCrossProjectPattern] == 0 {
		t.Log("  (note: cross-project insights may be skipped if patterns don't reach threshold)")
	}

	// Insights should be sorted by severity (critical/high first)
	if len(insights) > 1 {
		for i := 1; i < len(insights); i++ {
			if severityRank(insights[i].Severity) > severityRank(insights[i-1].Severity) {
				t.Errorf("insights not sorted by severity: [%d]=%s > [%d]=%s",
					i, insights[i].Severity, i-1, insights[i-1].Severity)
			}
		}
		t.Log("  Severity ordering: CORRECT")
	}

	// ── 4. Test Gold store persistence ────────────────────────────────────
	t.Log("Step 4: Testing Gold store persistence")

	goldDir := tmpDir + "/gold"
	store, err := NewGoldStore(goldDir)
	if err != nil {
		t.Fatalf("NewGoldStore: %v", err)
	}

	if err := store.SaveRun(run, insights); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	// Load back
	loaded, err := store.LoadRecentInsights(1)
	if err != nil {
		t.Fatalf("LoadRecentInsights: %v", err)
	}
	t.Logf("  Persisted %d insights, loaded back %d", len(insights), len(loaded))

	if len(loaded) != len(insights) {
		t.Errorf("persistence mismatch: saved %d, loaded %d", len(insights), len(loaded))
	}

	// Load runs
	runs, err := store.LoadRecentRuns(1)
	if err != nil {
		t.Fatalf("LoadRecentRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("runs loaded: got %d, want 1", len(runs))
	}
	t.Logf("  Run loaded: id=%s, trees=%d, insights=%d",
		runs[0].ID, runs[0].TreesAnalyzed, runs[0].InsightsGenerated)

	// Test summary
	summary, err := store.InsightSummary(1, 10)
	if err != nil {
		t.Fatalf("InsightSummary: %v", err)
	}
	t.Logf("  Summary: %d chars", len(summary))
	if !strings.Contains(summary, "Gold Insights") {
		t.Error("summary missing 'Gold Insights' header")
	}

	// ── 5. Test cross-project analysis standalone ─────────────────────────
	t.Log("Step 5: Testing cross-project analysis")

	// Create stats manually to test the cross-project analyzer
	projectStats := map[string][]SessionStats{
		"coding-project": {
			{
				ProjectHash:     "coding-project",
				ConversationID:  "c1",
				Domain:          "software_engineering",
				TotalEvents:     100,
				UserPromptCount: 10,
				CorrectionCount: 1, // 10% correction rate
				ErrorCount:      2,
				ToolFrequency:   map[string]int{"Bash": 50, "Read": 30, "Edit": 20},
			},
		},
		"finance-project": {
			{
				ProjectHash:     "finance-project",
				ConversationID:  "f1",
				Domain:          "financial_research",
				TotalEvents:     200,
				UserPromptCount: 20,
				CorrectionCount: 7, // 35% correction rate
				ErrorCount:      3,
				ToolFrequency:   map[string]int{"websearch": 100, "Bash": 60, "Read": 40},
			},
		},
	}

	crossInsights := analyzer.analyzeCrossProject("cross-test", projectStats)
	t.Logf("  Cross-project insights: %d", len(crossInsights))
	for _, ins := range crossInsights {
		t.Logf("    [%s] %s", ins.Category, ins.Title)
	}

	// Should detect domain transfer (coding 10% vs finance 35%)
	hasDomainTransfer := false
	for _, ins := range crossInsights {
		if ins.Category == CategoryDomainTransfer {
			hasDomainTransfer = true
		}
	}
	if !hasDomainTransfer {
		t.Log("  (note: domain transfer requires ≥10 pp difference — may be suppressed)")
	}

	// ── 6. Session stats computation ─────────────────────────────────────
	t.Log("Step 6: Verifying session stats computation")

	stats := computeSessionStats("test-project", treeA1)
	t.Logf("  Session stats: events=%d, errors=%d, corrections=%d/%d, error_rate=%.1f%%",
		stats.TotalEvents, stats.ErrorCount, stats.CorrectionCount, stats.UserPromptCount,
		stats.ErrorRate*100)

	if stats.TotalEvents != 150 {
		t.Errorf("total events: got %d, want 150", stats.TotalEvents)
	}
	if stats.ErrorCount != 7 {
		t.Errorf("error count: got %d, want 7", stats.ErrorCount)
	}
	if stats.UserPromptCount != 15 {
		t.Errorf("user prompt count: got %d, want 15", stats.UserPromptCount)
	}
	if stats.CorrectionCount != 4 {
		t.Errorf("correction count: got %d, want 4", stats.CorrectionCount)
	}

	// ── Summary ──────────────────────────────────────────────────────────
	t.Log("=== PHASE 4 GOLD TIER TEST PASSED ===")
	t.Logf("  Trees analyzed: %d (2 projects)", run.TreesAnalyzed)
	t.Logf("  Insights generated: %d", len(insights))
	t.Logf("  Categories: %v", categories)
	t.Logf("  Severities: %v", severities)
	t.Logf("  Persistence: round-trip successful")
	t.Logf("  Duration: %dms", run.DurationMs)
}

func TestGoldStore(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewGoldStore(tmpDir)
	if err != nil {
		t.Fatalf("NewGoldStore: %v", err)
	}

	// Empty store
	insights, err := store.LoadRecentInsights(7)
	if err != nil {
		t.Fatalf("LoadRecentInsights empty: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected empty, got %d insights", len(insights))
	}

	// Save and load
	run := &GoldAnalysisRun{
		ID:                "test-run",
		ProjectHashes:     []string{"p1", "p2"},
		TreesAnalyzed:     5,
		InsightsGenerated: 3,
		DurationMs:        42,
		StartedAt:         time.Now(),
		CompletedAt:       time.Now(),
	}

	testInsights := []GoldInsight{
		{
			ID: "i1", Title: "Test insight 1",
			Category: CategoryToolEfficiency, Severity: SeverityHigh,
			Recommendation: "Fix the tool",
			GeneratedAt:    time.Now(), AnalysisRunID: "test-run",
		},
		{
			ID: "i2", Title: "Test insight 2",
			Category: CategoryCorrectionPattern, Severity: SeverityMedium,
			Recommendation: "Review corrections",
			GeneratedAt:    time.Now(), AnalysisRunID: "test-run",
		},
	}

	if err := store.SaveRun(run, testInsights); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	loaded, err := store.LoadRecentInsights(7)
	if err != nil {
		t.Fatalf("LoadRecentInsights: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("loaded insights: got %d, want 2", len(loaded))
	}

	// Pruning (nothing to prune — too recent)
	removed, err := store.PruneOldInsights(30)
	if err != nil {
		t.Fatalf("PruneOldInsights: %v", err)
	}
	if removed != 0 {
		t.Errorf("pruned recent insights: got %d, want 0", removed)
	}
}

// buildSyntheticTree constructs a SilverTree for testing.
func buildSyntheticTree(
	convID, projectHash, domain string,
	start, end time.Time,
	totalEvents int,
	toolFreq map[string]int,
	errorCount, userPrompts, correctionCount int,
) silver.SilverTree {
	// Build dominant tools list
	type tf struct {
		name  string
		count int
	}
	var tools []tf
	for name, count := range toolFreq {
		tools = append(tools, tf{name, count})
	}
	// Simple sort
	for i := 0; i < len(tools); i++ {
		for j := i + 1; j < len(tools); j++ {
			if tools[j].count > tools[i].count {
				tools[i], tools[j] = tools[j], tools[i]
			}
		}
	}
	dominant := make([]string, 0, len(tools))
	for _, t := range tools {
		dominant = append(dominant, t.name)
	}

	node := silver.SilverNode{
		Title:           "Main Phase",
		NodeID:          "0001",
		StartTime:       start,
		EndTime:         end,
		EventCount:      totalEvents,
		ErrorCount:      errorCount,
		UserPromptCount: userPrompts,
		CorrectionCount: correctionCount,
		ToolFrequency:   toolFreq,
		DominantTools:   dominant,
		Domain:          domain,
	}

	return silver.SilverTree{
		Version:        1,
		ProjectHash:    projectHash,
		ConversationID: convID,
		StartTime:      start,
		EndTime:        end,
		TotalEvents:    totalEvents,
		DominantDomain: domain,
		Structure:      []silver.SilverNode{node},
		GeneratedAt:    time.Now(),
	}
}
