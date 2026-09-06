package silver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SilverStore manages persistence of Silver tree indices on disk.
//
// Storage layout:
//
//	<baseDir>/YYYY/MM/DD/tree_<conv_id>.json
//
// where baseDir is typically ~/.swarm/projects/<hash>/silver/
type SilverStore struct {
	baseDir string
}

// NewSilverStore creates a SilverStore rooted at baseDir.
// The directory is created if it does not exist.
func NewSilverStore(baseDir string) (*SilverStore, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("silver/store: create dir %q: %w", baseDir, err)
	}
	return &SilverStore{baseDir: baseDir}, nil
}

// Dir returns the storage base directory.
func (s *SilverStore) Dir() string { return s.baseDir }

// SaveTree persists a SilverTree to disk. The file path is derived from the
// tree's StartTime and ConversationID:
//
//	<baseDir>/YYYY/MM/DD/tree_<conv_id>.json
func (s *SilverStore) SaveTree(tree SilverTree) (string, error) {
	// Ensure version is set.
	if tree.Version == 0 {
		tree.Version = CurrentSchemaVersion
	}
	if tree.GeneratedAt.IsZero() {
		tree.GeneratedAt = time.Now().UTC()
	}

	// Build path from tree's start time.
	dateDir := filepath.Join(s.baseDir,
		tree.StartTime.Format("2006"),
		tree.StartTime.Format("01"),
		tree.StartTime.Format("02"))

	if err := os.MkdirAll(dateDir, 0755); err != nil {
		return "", fmt.Errorf("silver/store: create date dir: %w", err)
	}

	// Sanitize conversation ID for filename safety.
	safeConvID := sanitizeForFilename(tree.ConversationID)
	if safeConvID == "" {
		safeConvID = tree.StartTime.Format("150405")
	}
	filename := fmt.Sprintf("tree_%s.json", safeConvID)
	path := filepath.Join(dateDir, filename)

	data, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		return "", fmt.Errorf("silver/store: marshal tree: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("silver/store: write %q: %w", path, err)
	}

	return path, nil
}

// LoadTree reads a SilverTree from a JSON file.
func (s *SilverStore) LoadTree(path string) (SilverTree, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SilverTree{}, fmt.Errorf("silver/store: read %q: %w", path, err)
	}

	var tree SilverTree
	if err := json.Unmarshal(data, &tree); err != nil {
		return SilverTree{}, fmt.Errorf("silver/store: parse %q: %w", path, err)
	}

	return tree, nil
}

// ListTrees returns paths to all Silver tree files within a date range,
// sorted chronologically. If start/end are zero, all trees are returned.
func (s *SilverStore) ListTrees(start, end time.Time) ([]string, error) {
	var files []string

	err := filepath.Walk(s.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".json") || !strings.HasPrefix(info.Name(), "tree_") {
			return nil
		}

		// Parse date from directory path: silver/YYYY/MM/DD/tree_xxx.json
		rel, _ := filepath.Rel(s.baseDir, path)
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) < 4 {
			return nil
		}

		dateStr := parts[0] + "-" + parts[1] + "-" + parts[2]
		fileDate, parseErr := time.Parse("2006-01-02", dateStr)
		if parseErr != nil {
			return nil
		}

		if !start.IsZero() && fileDate.Before(start.Truncate(24*time.Hour)) {
			return nil
		}
		if !end.IsZero() && fileDate.After(end.Truncate(24*time.Hour)) {
			return nil
		}

		files = append(files, path)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("silver/store: walk %q: %w", s.baseDir, err)
	}

	sort.Strings(files)
	return files, nil
}

// ListTreesSince loads all Silver trees with start time on or after cutoff,
// sorted chronologically. This is the primary entry point for Gold analysis.
func (s *SilverStore) ListTreesSince(cutoff time.Time) ([]SilverTree, error) {
	paths, err := s.ListTrees(cutoff, time.Time{})
	if err != nil {
		return nil, err
	}

	var trees []SilverTree
	for _, p := range paths {
		tree, loadErr := s.LoadTree(p)
		if loadErr != nil {
			continue
		}
		trees = append(trees, tree)
	}

	sort.Slice(trees, func(i, j int) bool {
		return trees[i].StartTime.Before(trees[j].StartTime)
	})
	return trees, nil
}

// LoadAllTrees loads all Silver trees for this project, sorted by start time.
func (s *SilverStore) LoadAllTrees() ([]SilverTree, error) {
	paths, err := s.ListTrees(time.Time{}, time.Time{})
	if err != nil {
		return nil, err
	}

	var trees []SilverTree
	for _, p := range paths {
		tree, loadErr := s.LoadTree(p)
		if loadErr != nil {
			continue // skip corrupt files
		}
		trees = append(trees, tree)
	}

	sort.Slice(trees, func(i, j int) bool {
		return trees[i].StartTime.Before(trees[j].StartTime)
	})

	return trees, nil
}

// PruneOldTrees deletes tree files older than retentionDays.
// Returns the number of files removed.
func (s *SilverStore) PruneOldTrees(retentionDays int) (int, error) {
	if retentionDays <= 0 {
		retentionDays = 30 // Silver retains longer than Bronze's 7 days
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	paths, err := s.ListTrees(time.Time{}, cutoff)
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, p := range paths {
		if err := os.Remove(p); err == nil {
			removed++
		}
	}

	// Clean up empty date directories.
	s.cleanEmptyDirs()

	return removed, nil
}

// cleanEmptyDirs removes empty date directories left after pruning.
func (s *SilverStore) cleanEmptyDirs() {
	_ = filepath.Walk(s.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || path == s.baseDir {
			return nil
		}
		entries, _ := os.ReadDir(path)
		if len(entries) == 0 {
			_ = os.Remove(path)
		}
		return nil
	})
}

// FindSilverDir returns the Silver storage directory for a project.
func FindSilverDir(projectHash string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("silver/store: get home dir: %w", err)
	}
	return filepath.Join(homeDir, ".swarm", "projects", projectHash, "silver"), nil
}

// sanitizeForFilename replaces characters that are unsafe in filenames.
func sanitizeForFilename(s string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		" ", "_",
		"..", "_",
	)
	result := replacer.Replace(s)
	// Trim to reasonable length.
	if len(result) > 80 {
		result = result[:80]
	}
	return result
}
