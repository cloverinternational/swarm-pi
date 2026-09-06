package silver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ListBronzeFiles returns all bronze JSONL files for a project within the given
// time range, sorted chronologically. If start/end are zero, all files are returned.
func ListBronzeFiles(bronzeDir string, start, end time.Time) ([]string, error) {
	var files []string

	err := filepath.Walk(bronzeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}

		// Parse date from directory structure: bronze/YYYY/MM/DD/events_HH.jsonl
		rel, _ := filepath.Rel(bronzeDir, path)
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) < 4 {
			return nil
		}

		dateStr := parts[0] + "-" + parts[1] + "-" + parts[2]
		hourStr := strings.TrimPrefix(strings.TrimSuffix(parts[3], ".jsonl"), "events_")
		tsStr := dateStr + "T" + hourStr + ":00:00Z"

		fileTime, parseErr := time.Parse("2006-01-02T15:04:05Z", tsStr)
		if parseErr != nil {
			return nil // skip unparseable files
		}

		// Filter by time range if specified.
		if !start.IsZero() && fileTime.Before(start.Add(-1*time.Hour)) {
			return nil
		}
		if !end.IsZero() && fileTime.After(end.Add(1*time.Hour)) {
			return nil
		}

		files = append(files, path)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("silver: walk bronze dir %q: %w", bronzeDir, err)
	}

	sort.Strings(files)
	return files, nil
}

// ReadBronzeFile stream-parses a single bronze JSONL file into BronzeEvent structs.
// It reads line-by-line to avoid loading entire files into memory.
// Events with parse errors are silently skipped.
func ReadBronzeFile(path string) ([]BronzeEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("silver: open bronze file %q: %w", path, err)
	}
	defer f.Close()

	var events []BronzeEvent
	scanner := bufio.NewScanner(f)
	// Increase buffer for large events (some tool results can be very large).
	scanner.Buffer(make([]byte, 0, 512*1024), 2*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var evt BronzeEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			continue // skip malformed lines
		}
		events = append(events, evt)
	}

	if err := scanner.Err(); err != nil {
		return events, fmt.Errorf("silver: scan bronze file %q: %w", path, err)
	}

	return events, nil
}

// ReadBronzeEvents reads all bronze events from the given files, in order.
func ReadBronzeEvents(files []string) ([]BronzeEvent, error) {
	var all []BronzeEvent
	for _, f := range files {
		events, err := ReadBronzeFile(f)
		if err != nil {
			return all, err
		}
		all = append(all, events...)
	}

	// Sort by timestamp to ensure chronological order across files.
	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp.Before(all[j].Timestamp)
	})

	return all, nil
}

// WindowEvents groups a sorted slice of bronze events into fixed-duration time windows.
// windowDuration defaults to DefaultWindowDuration (5 minutes) if zero.
func WindowEvents(events []BronzeEvent, windowDuration time.Duration) []EventWindow {
	if len(events) == 0 {
		return nil
	}
	if windowDuration <= 0 {
		windowDuration = DefaultWindowDuration
	}

	var windows []EventWindow
	var current *EventWindow

	for _, evt := range events {
		// Start a new window if needed.
		if current == nil || evt.Timestamp.After(current.EndTime) || evt.Timestamp.Equal(current.EndTime) {
			// Close current window.
			if current != nil {
				current.EventCount = len(current.Events)
				windows = append(windows, *current)
			}

			// Compute window boundaries aligned to windowDuration.
			windowStart := evt.Timestamp.Truncate(windowDuration)
			current = &EventWindow{
				StartTime:  windowStart,
				EndTime:    windowStart.Add(windowDuration),
				Events:     nil,
				ToolCounts: make(map[string]int),
			}
		}

		current.Events = append(current.Events, evt)

		// Accumulate stats.
		if toolName, ok := evt.Payload["tool_name"].(string); ok {
			current.ToolCounts[toolName]++
		}
		if evt.Type == "tool.execution_failed" {
			current.ErrorCount++
		} else if evt.Type == "tool.after_execute" {
			// Check for error in payload
			if errVal, ok := evt.Payload["error"]; ok && errVal != nil && errVal != "" {
				current.ErrorCount++
			}
		}
		if evt.Type == "user.prompt_submit" {
			current.UserPromptCount++
		}
	}

	// Don't forget the last window.
	if current != nil && len(current.Events) > 0 {
		current.EventCount = len(current.Events)
		windows = append(windows, *current)
	}

	return windows
}

// FindBronzeDir returns the bronze data directory for a project.
// Checks both the standard locations:
//   - ~/.swarm/projects/<hash>/findings/bronze/
//   - ~/.swarm/projects/<hash>/bronze/
func FindBronzeDir(projectHash string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("silver: get home dir: %w", err)
	}

	projectBase := filepath.Join(homeDir, ".swarm", "projects", projectHash)

	// Check findings/bronze first (current production layout).
	dir := filepath.Join(projectBase, "findings", "bronze")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir, nil
	}

	// Fall back to direct bronze directory.
	dir = filepath.Join(projectBase, "bronze")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir, nil
	}

	return "", fmt.Errorf("silver: no bronze directory found for project %s", projectHash)
}

// ListProjects returns all project hashes that have bronze data.
func ListProjects() ([]string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("silver: get home dir: %w", err)
	}

	projectsDir := filepath.Join(homeDir, ".swarm", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, fmt.Errorf("silver: read projects dir: %w", err)
	}

	var projects []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		hash := entry.Name()

		// Check if this project has bronze data.
		if _, err := FindBronzeDir(hash); err == nil {
			projects = append(projects, hash)
		}
	}

	return projects, nil
}
