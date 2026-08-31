// Package ii provides the DebugLogsTool for searching and filtering session debug logs.
// This tool is only available when the agent is started with --debug flag.
package ii

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

const (
	debugLogsToolName        = "debug_logs"
	debugLogsToolDescription = `Access and search through the current session's debug logs.

Use this tool to:
- Grep through logs to find errors or specific events
- Filter logs by category (APP, TOOL, SDK, etc.)
- View recent log entries to understand what happened

This tool is only available when the agent is started with --debug flag.

Examples:
- Search for errors: pattern="error"
- Find tool executions: pattern="TOOL"
- View recent activity: lines=50
- Filter by category: category="SDK"`

	defaultLogLines = 100
	maxLogLines     = 1000
)

// DebugLogsTool provides access to session debug logs.
type DebugLogsTool struct {
	logPath string
}

// NewDebugLogsTool creates a new debug logs tool.
// logPath is the path to the debug log file (default: "swarmos_debug.log")
func NewDebugLogsTool(logPath string) *DebugLogsTool {
	if logPath == "" {
		logPath = "swarmos_debug.log"
	}
	return &DebugLogsTool{
		logPath: logPath,
	}
}

func (t *DebugLogsTool) Name() string {
	return debugLogsToolName
}

func (t *DebugLogsTool) Description() string {
	return debugLogsToolDescription
}

func (t *DebugLogsTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Search pattern (case-insensitive). Supports regex or simple substring match.",
			},
			"category": map[string]any{
				"type":        "string",
				"description": "Filter by log category (e.g., APP, TOOL, SDK, TOKENS, CONV). Case-insensitive.",
			},
			"lines": map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("Maximum number of log lines to return. Default: %d, Max: %d", defaultLogLines, maxLogLines),
			},
		},
		"required": []string{},
	}
}

func (t *DebugLogsTool) Validate(params map[string]any) error {
	if lines, ok := params["lines"]; ok {
		if linesFloat, ok := lines.(float64); ok {
			if linesFloat < 1 || linesFloat > maxLogLines {
				return fmt.Errorf("lines must be between 1 and %d", maxLogLines)
			}
		}
	}
	return nil
}

func (t *DebugLogsTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Extract parameters
	pattern := ""
	if p, ok := params["pattern"].(string); ok {
		pattern = p
	}

	category := ""
	if c, ok := params["category"].(string); ok {
		category = strings.ToUpper(c)
	}

	lines := defaultLogLines
	if l, ok := params["lines"].(float64); ok {
		lines = max(min(int(l), maxLogLines), 1)
	}

	// Read and filter logs
	content, err := t.readLogs(pattern, category, lines)
	if err != nil {
		return &tools.ToolResult{
			Content: []tools.ContentBlock{
				{Type: tools.ContentTypeText, Text: fmt.Sprintf("Error reading debug logs: %v", err)},
			},
			IsError: true,
		}, nil
	}

	return &tools.ToolResult{
		Content: []tools.ContentBlock{
			{Type: tools.ContentTypeText, Text: content},
		},
	}, nil
}

func (t *DebugLogsTool) readLogs(pattern, category string, maxLines int) (string, error) {
	file, err := os.Open(t.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "[No debug log file found. Make sure the app was started with debug logging enabled.]", nil
		}
		return "", err
	}
	defer file.Close()

	// Compile regex pattern if provided
	var re *regexp.Regexp
	if pattern != "" {
		re, err = regexp.Compile("(?i)" + pattern)
		if err != nil {
			// Fall back to simple substring match
			re = nil
		}
	}

	// Read all lines first (we need to get the last N)
	var allLines []string
	scanner := bufio.NewScanner(file)
	// Increase buffer size for long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Filter by category if specified
		if category != "" {
			// Log format: [time] [CATEGORY] file:line | message
			if !strings.Contains(strings.ToUpper(line), "["+category+"]") {
				continue
			}
		}

		// Filter by pattern if specified
		if pattern != "" {
			if re != nil {
				if !re.MatchString(line) {
					continue
				}
			} else {
				if !strings.Contains(strings.ToLower(line), strings.ToLower(pattern)) {
					continue
				}
			}
		}

		allLines = append(allLines, line)
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading log file: %w", err)
	}

	if len(allLines) == 0 {
		filters := []string{}
		if pattern != "" {
			filters = append(filters, fmt.Sprintf("pattern='%s'", pattern))
		}
		if category != "" {
			filters = append(filters, fmt.Sprintf("category='%s'", category))
		}
		filterStr := "none"
		if len(filters) > 0 {
			filterStr = strings.Join(filters, ", ")
		}
		return fmt.Sprintf("[No logs matching filters: %s]", filterStr), nil
	}

	// Get last N lines
	startIdx := 0
	if len(allLines) > maxLines {
		startIdx = len(allLines) - maxLines
	}
	resultLines := allLines[startIdx:]

	// Build result
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Showing %d of %d matching logs", len(resultLines), len(allLines)))
	if len(allLines) > maxLines {
		sb.WriteString(fmt.Sprintf(" (truncated from %d)", len(allLines)))
	}
	sb.WriteString("]\n")
	sb.WriteString(strings.Join(resultLines, "\n"))

	return sb.String(), nil
}

func (t *DebugLogsTool) IsIdempotent() bool {
	return true // Reading logs is idempotent
}

func (t *DebugLogsTool) RequiresPermission() []tools.Permission {
	return nil // No special permissions required
}

func (t *DebugLogsTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

func (t *DebugLogsTool) OptimizationHints() *tools.OptimizationHints {
	return &tools.OptimizationHints{
		PreferSequential:  false,
		EstimatedDuration: 50 * time.Millisecond,
		CanBatch:          true,
		BatchSize:         5,
		Priority:          70,
		MinimalLatency:    true,
		Cacheable:         false, // Don't cache - logs change constantly
		CacheTTL:          0,
	}
}
