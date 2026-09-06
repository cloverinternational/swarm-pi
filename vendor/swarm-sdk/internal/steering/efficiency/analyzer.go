package efficiency

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// PatternType defines the type of efficiency pattern
type PatternType string

const (
	PatternRedundancy PatternType = "redundancy"
	PatternDocBloat   PatternType = "doc_bloat"
	PatternPriority   PatternType = "priority"
	PatternStalling   PatternType = "stalling"
)

// Pattern represents a detected inefficiency
type Pattern struct {
	Type        PatternType
	Severity    string // "low", "medium", "high", "critical"
	Description string
	File        string
	Count       int
	Timestamp   time.Time
}

// AnalysisResult contains efficiency analysis output
type AnalysisResult struct {
	Score           float64 // 1-5 scale, 5 is optimal
	Patterns        []Pattern
	Recommendations []string
	Timestamp       time.Time
}

// Analyzer detects inefficiency patterns in tool calls
type Analyzer struct {
	maxFileReads int
	mu           sync.RWMutex

	// Tracking for pattern detection
	fileReads   map[string]int
	fileWrites  map[string]int
	toolHistory []ToolCallInfo
}

// ToolCallInfo stores info about a tool call for analysis
type ToolCallInfo struct {
	Name      string
	Input     map[string]any
	Timestamp time.Time
}

// NewAnalyzer creates a new efficiency analyzer
func NewAnalyzer(maxFileReads int) *Analyzer {
	if maxFileReads <= 0 {
		maxFileReads = 3
	}
	return &Analyzer{
		maxFileReads: maxFileReads,
		fileReads:    make(map[string]int),
		fileWrites:   make(map[string]int),
		toolHistory:  make([]ToolCallInfo, 0),
	}
}

// AnalyzeToolCall analyzes a single tool call for patterns
func (a *Analyzer) AnalyzeToolCall(ctx context.Context, name string, input map[string]any) *AnalysisResult {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := &AnalysisResult{
		Score:           5.0,
		Patterns:        make([]Pattern, 0),
		Recommendations: make([]string, 0),
		Timestamp:       time.Now(),
	}

	// Track tool call
	a.toolHistory = append(a.toolHistory, ToolCallInfo{
		Name:      name,
		Input:     input,
		Timestamp: time.Now(),
	})

	// Detect patterns
	a.detectRedundancy(name, input, result)
	a.detectDocBloat(name, input, result)
	a.detectStalling(result)

	// Ensure score stays in range
	if result.Score < 1 {
		result.Score = 1
	}
	if result.Score > 5 {
		result.Score = 5
	}

	return result
}

func (a *Analyzer) detectRedundancy(name string, input map[string]any, result *AnalysisResult) {
	// Track file reads
	if name == "read_file" || name == "file_read" {
		if path, ok := input["path"].(string); ok {
			a.fileReads[path]++
			if a.fileReads[path] > a.maxFileReads {
				result.Patterns = append(result.Patterns, Pattern{
					Type:        PatternRedundancy,
					Severity:    "medium",
					Description: fmt.Sprintf("File %s read %d times", path, a.fileReads[path]),
					File:        path,
					Count:       a.fileReads[path],
					Timestamp:   time.Now(),
				})
				result.Score -= 0.5
				result.Recommendations = append(result.Recommendations,
					fmt.Sprintf("Consider caching file %s to avoid repeated reads", path))
			}
		}
	}

	// Track file writes
	if name == "write_file" || name == "str_replace" {
		if path, ok := input["path"].(string); ok {
			a.fileWrites[path]++
			if a.fileWrites[path] > 5 {
				result.Patterns = append(result.Patterns, Pattern{
					Type:        PatternRedundancy,
					Severity:    "low",
					Description: fmt.Sprintf("File %s written %d times", path, a.fileWrites[path]),
					File:        path,
					Count:       a.fileWrites[path],
					Timestamp:   time.Now(),
				})
				result.Score -= 0.2
			}
		}
	}
}

func (a *Analyzer) detectDocBloat(name string, input map[string]any, result *AnalysisResult) {
	// Check if documentation is being created before implementation
	if name == "write_file" || name == "create_file" {
		if path, ok := input["path"].(string); ok {
			// Check if it's a .md file
			if len(path) > 3 && path[len(path)-3:] == ".md" {
				// Check if any .go files have been written
				goFilesWritten := 0
				for p := range a.fileWrites {
					if len(p) > 3 && p[len(p)-3:] == ".go" {
						goFilesWritten++
					}
				}

				if goFilesWritten == 0 && len(a.fileWrites) < 3 {
					result.Patterns = append(result.Patterns, Pattern{
						Type:        PatternDocBloat,
						Severity:    "low",
						Description: "Documentation created before implementation",
						File:        path,
						Timestamp:   time.Now(),
					})
					result.Recommendations = append(result.Recommendations,
						"Consider implementing code before documentation")
				}
			}
		}
	}
}

func (a *Analyzer) detectStalling(result *AnalysisResult) {
	// Detect stalling: many tool calls without file writes
	if len(a.toolHistory) > 10 {
		recentWrites := 0
		for i := len(a.toolHistory) - 10; i < len(a.toolHistory); i++ {
			if a.toolHistory[i].Name == "write_file" || a.toolHistory[i].Name == "str_replace" {
				recentWrites++
			}
		}

		if recentWrites == 0 {
			result.Patterns = append(result.Patterns, Pattern{
				Type:        PatternStalling,
				Severity:    "low",
				Description: "Many tool calls without writes - possible stalling",
				Timestamp:   time.Now(),
			})
			result.Score -= 0.3
		}
	}
}

// GetScore returns the current efficiency score
func (a *Analyzer) GetScore() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Calculate based on recent history
	if len(a.toolHistory) == 0 {
		return 5.0
	}

	score := 5.0
	for _, count := range a.fileReads {
		if count > a.maxFileReads {
			score -= 0.5
		}
	}

	if score < 1 {
		score = 1
	}
	return score
}

// Reset clears the analyzer state
func (a *Analyzer) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.fileReads = make(map[string]int)
	a.fileWrites = make(map[string]int)
	a.toolHistory = make([]ToolCallInfo, 0)
}
