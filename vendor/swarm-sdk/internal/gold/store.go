package gold

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GoldStore persists Gold insights and analysis runs to disk.
//
// Storage layout:
//
//	<goldDir>/
//	├── runs/
//	│   └── YYYY/MM/DD/run_<id>.json     # GoldAnalysisRun records
//	└── insights/
//	    └── YYYY/MM/DD/insights_<id>.json # []GoldInsight for that run
type GoldStore struct {
	baseDir string
}

// NewGoldStore creates a GoldStore rooted at baseDir.
func NewGoldStore(baseDir string) (*GoldStore, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("gold/store: create dir %q: %w", baseDir, err)
	}
	return &GoldStore{baseDir: baseDir}, nil
}

// Dir returns the Gold storage root directory.
func (s *GoldStore) Dir() string { return s.baseDir }

// SaveRun persists a GoldAnalysisRun and its insights.
func (s *GoldStore) SaveRun(run *GoldAnalysisRun, insights []GoldInsight) error {
	if run == nil {
		return fmt.Errorf("gold/store: run must not be nil")
	}

	dateDir := run.StartedAt.Format("2006/01/02")
	runsDir := filepath.Join(s.baseDir, "runs", dateDir)
	insightsDir := filepath.Join(s.baseDir, "insights", dateDir)

	for _, dir := range []string{runsDir, insightsDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("gold/store: create dir %q: %w", dir, err)
		}
	}

	// Save run record
	runPath := filepath.Join(runsDir, fmt.Sprintf("run_%s.json", sanitizeID(run.ID)))
	if err := writeGoldJSON(runPath, run); err != nil {
		return fmt.Errorf("gold/store: save run: %w", err)
	}

	// Save insights
	if len(insights) > 0 {
		insightsPath := filepath.Join(insightsDir, fmt.Sprintf("insights_%s.json", sanitizeID(run.ID)))
		if err := writeGoldJSON(insightsPath, insights); err != nil {
			return fmt.Errorf("gold/store: save insights: %w", err)
		}
	}

	return nil
}

// LoadRecentInsights loads all insights from the last N days.
func (s *GoldStore) LoadRecentInsights(days int) ([]GoldInsight, error) {
	if days <= 0 {
		days = 7
	}
	cutoff := time.Now().AddDate(0, 0, -days)

	insightsBase := filepath.Join(s.baseDir, "insights")
	var allInsights []GoldInsight

	err := filepath.Walk(insightsBase, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}

		// Parse date from path: insights/YYYY/MM/DD/insights_xxx.json
		rel, _ := filepath.Rel(insightsBase, path)
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) < 4 {
			return nil
		}
		dateStr := parts[0] + "-" + parts[1] + "-" + parts[2]
		fileDate, parseErr := time.Parse("2006-01-02", dateStr)
		if parseErr != nil {
			return nil
		}
		if fileDate.Before(cutoff.Truncate(24 * time.Hour)) {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		var batch []GoldInsight
		if jsonErr := json.Unmarshal(data, &batch); jsonErr != nil {
			return nil
		}
		allInsights = append(allInsights, batch...)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("gold/store: walk insights: %w", err)
	}

	// Sort by severity then generated_at
	sort.Slice(allInsights, func(i, j int) bool {
		ri, rj := severityRank(allInsights[i].Severity), severityRank(allInsights[j].Severity)
		if ri != rj {
			return ri > rj
		}
		return allInsights[i].GeneratedAt.After(allInsights[j].GeneratedAt)
	})

	return allInsights, nil
}

// LoadRecentRuns loads all GoldAnalysisRun records from the last N days.
func (s *GoldStore) LoadRecentRuns(days int) ([]GoldAnalysisRun, error) {
	if days <= 0 {
		days = 7
	}
	cutoff := time.Now().AddDate(0, 0, -days)

	runsBase := filepath.Join(s.baseDir, "runs")
	var runs []GoldAnalysisRun

	err := filepath.Walk(runsBase, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}

		rel, _ := filepath.Rel(runsBase, path)
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) < 4 {
			return nil
		}
		dateStr := parts[0] + "-" + parts[1] + "-" + parts[2]
		fileDate, parseErr := time.Parse("2006-01-02", dateStr)
		if parseErr != nil || fileDate.Before(cutoff.Truncate(24*time.Hour)) {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		var run GoldAnalysisRun
		if jsonErr := json.Unmarshal(data, &run); jsonErr != nil {
			return nil
		}
		runs = append(runs, run)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("gold/store: walk runs: %w", err)
	}

	sort.Slice(runs, func(i, j int) bool {
		return runs[i].StartedAt.After(runs[j].StartedAt)
	})
	return runs, nil
}

// InsightSummary returns a compact formatted summary of recent insights
// suitable for inclusion in a system prompt or report.
func (s *GoldStore) InsightSummary(days int, maxInsights int) (string, error) {
	insights, err := s.LoadRecentInsights(days)
	if err != nil {
		return "", err
	}
	if len(insights) == 0 {
		return "", nil
	}

	if maxInsights > 0 && len(insights) > maxInsights {
		insights = insights[:maxInsights]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Gold Insights (%d findings from last %d days)\n\n", len(insights), days))

	for i, ins := range insights {
		sb.WriteString(fmt.Sprintf("%d. **[%s]** %s\n", i+1, ins.Severity, ins.Title))
		if ins.Recommendation != "" {
			sb.WriteString(fmt.Sprintf("   → %s\n", truncateString(ins.Recommendation, 120)))
		}
	}
	sb.WriteString("\n")

	return sb.String(), nil
}

// PruneOldInsights deletes insight and run files older than retentionDays.
func (s *GoldStore) PruneOldInsights(retentionDays int) (int, error) {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	removed := 0
	for _, subDir := range []string{"runs", "insights"} {
		dir := filepath.Join(s.baseDir, subDir)
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if info.ModTime().Before(cutoff) {
				if removeErr := os.Remove(path); removeErr == nil {
					removed++
				}
			}
			return nil
		})
	}
	return removed, nil
}

// FindGoldDir returns the standard Gold storage directory for a project.
func FindGoldDir(projectHash string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("gold/store: get home dir: %w", err)
	}
	return filepath.Join(homeDir, ".swarm", "projects", projectHash, "gold"), nil
}

// GlobalGoldDir returns the global Gold directory for cross-project insights.
func GlobalGoldDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("gold/store: get home dir: %w", err)
	}
	return filepath.Join(homeDir, ".swarm", "gold"), nil
}

// --- helpers ---

func writeGoldJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

func sanitizeID(id string) string {
	s := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, id)
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
