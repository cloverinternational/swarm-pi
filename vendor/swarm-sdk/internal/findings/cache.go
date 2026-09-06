package findings

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// FileCache implements Cache using JSON files on disk.
// This is the default implementation used by LocalFindingsHook.
type FileCache struct {
	baseDir   string
	config    Config
	mu        sync.RWMutex
	writeChan chan Finding
	done      chan struct{}
	closeOnce sync.Once
}

// NewFileCache creates a new file-based cache.
func NewFileCache(config Config) (*FileCache, error) {
	// Expand home directory if needed
	baseDir := ExpandCacheDir(config.LocalCacheDir)

	// Ensure directory structure exists
	dirs := []string{
		baseDir,
		filepath.Join(baseDir, "cache"),
		filepath.Join(baseDir, "cache", time.Now().Format("2006")),
		filepath.Join(baseDir, "cache", time.Now().Format("2006"), time.Now().Format("01")),
		filepath.Join(baseDir, "cache", time.Now().Format("2006"), time.Now().Format("01"), time.Now().Format("02")),
		filepath.Join(baseDir, "index"),
		filepath.Join(baseDir, "synced"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	fc := &FileCache{
		baseDir:   baseDir,
		config:    config,
		writeChan: make(chan Finding, 100),
		done:      make(chan struct{}),
	}

	// Start background writer
	go fc.backgroundWriter()

	return fc, nil
}

// Write persists a finding to the cache.
// This writes immediately to the current day's file.
func (fc *FileCache) Write(ctx context.Context, finding Finding) error {
	// Ensure finding has an ID
	if finding.FindingID == "" {
		finding.FindingID = uuid.New().String()
	}

	// Set timestamp if not present
	if finding.Timestamp.IsZero() {
		finding.Timestamp = time.Now()
	}

	// Queue for background write (non-blocking with buffer)
	select {
	case fc.writeChan <- finding:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Channel full, write synchronously
		return fc.writeSync(finding)
	}
}

// writeSync performs synchronous file write
func (fc *FileCache) writeSync(finding Finding) error {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	// Determine file path: base/cache/YYYY/MM/DD/findings_<timestamp>.json
	dateDir := filepath.Join(fc.baseDir, "cache",
		finding.Timestamp.Format("2006"),
		finding.Timestamp.Format("01"),
		finding.Timestamp.Format("02"))

	if err := os.MkdirAll(dateDir, 0755); err != nil {
		return fmt.Errorf("failed to create date directory: %w", err)
	}

	// Marshal finding as compact JSON (one line per finding).
	data, err := json.Marshal(finding)
	if err != nil {
		return fmt.Errorf("failed to marshal finding: %w", err)
	}

	// Append to daily JSONL file — single write path for efficiency.
	dailyFile := filepath.Join(dateDir, "findings.jsonl")

	f, err := os.OpenFile(dailyFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open daily file: %w", err)
	}
	defer f.Close()

	line := append(data, '\n')
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("failed to append to daily file: %w", err)
	}

	return nil
}

// backgroundWriter handles async writes
func (fc *FileCache) backgroundWriter() {
	for {
		select {
		case finding := <-fc.writeChan:
			if err := fc.writeSync(finding); err != nil {
				// Log error but continue
				fmt.Fprintf(os.Stderr, "[findings] background write error: %v\n", err)
			}
		case <-fc.done:
			return
		}
	}
}

// Read retrieves a finding by ID.
// Searches JSONL files across all date directories.
func (fc *FileCache) Read(ctx context.Context, findingID string) (Finding, error) {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	var result Finding
	found := false

	err := filepath.Walk(filepath.Join(fc.baseDir, "cache"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		dec := json.NewDecoder(f)
		for dec.More() {
			var finding Finding
			if err := dec.Decode(&finding); err != nil {
				continue
			}
			if finding.FindingID == findingID {
				result = finding
				found = true
				return filepath.SkipAll
			}
		}
		return nil
	})

	if err != nil {
		return Finding{}, fmt.Errorf("failed to search for finding: %w", err)
	}

	if !found {
		return Finding{}, fmt.Errorf("finding not found: %s", findingID)
	}

	return result, nil
}

// Query performs a simple search over findings.
// Note: This is a basic implementation. Full-text and semantic search
// should use the SemanticIndex interface.
func (fc *FileCache) Query(ctx context.Context, query FindingQuery) ([]FindingResult, error) {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	var results []FindingResult

	// Walk cache directory, scanning JSONL files
	err := filepath.Walk(filepath.Join(fc.baseDir, "cache"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		dec := json.NewDecoder(f)
		for dec.More() {
			var finding Finding
			if err := dec.Decode(&finding); err != nil {
				continue
			}

			if !matchesQuery(finding, query) {
				continue
			}

			results = append(results, FindingResult{
				Finding:   finding,
				Score:     calculateScore(finding, query),
				MatchType: "filter",
			})
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to query findings: %w", err)
	}

	// Sort by score
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Apply offset
	if query.Offset > 0 && query.Offset < len(results) {
		results = results[query.Offset:]
	} else if query.Offset >= len(results) {
		return nil, nil
	}

	// Apply limit
	if query.Limit > 0 && len(results) > query.Limit {
		results = results[:query.Limit]
	}

	return results, nil
}

// matchesQuery checks if a finding matches the query criteria
func matchesQuery(f Finding, q FindingQuery) bool {
	// Tool name filter
	if q.ToolName != "" && f.ToolName != q.ToolName {
		return false
	}

	// Agent ID filter
	if q.AgentID != "" && f.AgentID != q.AgentID {
		return false
	}

	// Tags filter (must have all specified tags)
	if len(q.Tags) > 0 {
		tagSet := make(map[string]bool)
		for _, t := range f.Tags {
			tagSet[t] = true
		}
		for _, required := range q.Tags {
			if !tagSet[required] {
				return false
			}
		}
	}

	// Time range filter
	if q.TimeRange != nil {
		if f.Timestamp.Before(q.TimeRange.Start) || f.Timestamp.After(q.TimeRange.End) {
			return false
		}
	}

	// Text search (simple substring match)
	if q.TextQuery != "" {
		text := strings.ToLower(q.TextQuery)
		content := strings.ToLower(f.ContextSummary + " " + f.ToolName)
		if !strings.Contains(content, text) {
			return false
		}
	}

	return true
}

// calculateScore gives a relevance score for sorting
func calculateScore(f Finding, q FindingQuery) float64 {
	score := 0.0

	// More recent = higher score
	age := time.Since(f.Timestamp).Hours()
	score += 1.0 / (1.0 + age/24.0) // Decay over days

	// Higher priority = higher score
	score += float64(f.Metadata.Priority) / 100.0

	// Successful findings get a small boost
	if f.Metadata.Success {
		score += 0.1
	}

	return score
}

// List returns all findings with pagination
func (fc *FileCache) List(ctx context.Context, limit, offset int) ([]Finding, error) {
	query := FindingQuery{
		Limit:  limit,
		Offset: offset,
	}

	results, err := fc.Query(ctx, query)
	if err != nil {
		return nil, err
	}

	findings := make([]Finding, len(results))
	for i, r := range results {
		findings[i] = r.Finding
	}

	return findings, nil
}

// Delete removes a finding by rewriting the JSONL file without the matching entry.
func (fc *FileCache) Delete(ctx context.Context, findingID string) error {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	return filepath.Walk(filepath.Join(fc.baseDir, "cache"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		var kept []string
		found := false
		for _, line := range lines {
			if line == "" {
				continue
			}
			if strings.Contains(line, findingID) {
				var f Finding
				if json.Unmarshal([]byte(line), &f) == nil && f.FindingID == findingID {
					found = true
					continue
				}
			}
			kept = append(kept, line)
		}

		if found {
			content := strings.Join(kept, "\n")
			if content != "" {
				content += "\n"
			}
			return os.WriteFile(path, []byte(content), 0644)
		}
		return nil
	})
}

// Close shuts down the cache. Safe to call multiple times.
func (fc *FileCache) Close() error {
	fc.closeOnce.Do(func() {
		close(fc.done)
	})
	return nil
}

// CleanupOlderThan removes findings JSONL files older than the given duration.
// Returns the number of files removed and total bytes reclaimed.
func (fc *FileCache) CleanupOlderThan(retention time.Duration) (filesRemoved int, bytesReclaimed int64, err error) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	cutoff := time.Now().Add(-retention)
	cacheDir := filepath.Join(fc.baseDir, "cache")

	err = filepath.Walk(cacheDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			size := info.Size()
			if rmErr := os.Remove(path); rmErr == nil {
				filesRemoved++
				bytesReclaimed += size
			}
		}
		return nil
	})

	return filesRemoved, bytesReclaimed, err
}
