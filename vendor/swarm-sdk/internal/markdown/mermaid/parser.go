// Package mermaid provides source-level parsing helpers for Mermaid diagram
// code blocks. These are renderer-agnostic: the TUI uses them for ANSI-art
// rendering, and the visual picker server uses them for HTML rendering.
package mermaid

import (
	"regexp"
	"strings"
)

// KnownTypes lists the mermaid diagram kinds the parser recognizes.
// Order matters: longer prefixes first so stateDiagram-v2 wins over stateDiagram.
var KnownTypes = []string{
	"sequenceDiagram", "flowchart", "graph",
	"xychart-beta", "xychart",
	"pie", "gantt", "classDiagram",
	"stateDiagram-v2", "stateDiagram",
	"erDiagram", "mindmap", "timeline", "journey", "quadrantChart",
}

// Regexes for ParseTitle to avoid recompilation on each call.
var (
	inlineRe = regexp.MustCompile(`(?i)^\s*\S+\s+title\s+"?([^"]+)"?\s*$`)
	titleRe  = regexp.MustCompile(`(?i)^\s*title\s+"?([^"]+)"?\s*$`)
)

// DetectType returns the diagram type string (e.g. "flowchart"), the raw
// first-line text if the type is unknown, or "unknown" for an empty input.
func DetectType(lines []string) string {
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		lower := strings.ToLower(t)
		for _, kt := range KnownTypes {
			if strings.HasPrefix(lower, strings.ToLower(kt)) {
				return kt
			}
		}
		return t
	}
	return "unknown"
}

// ParseTitle extracts a title from mermaid source. Handles two forms:
//   - Inline: `pie title "My Pie"` (same line as the type)
//   - Standalone: `title "Flow Title"` (separate line)
func ParseTitle(lines []string) string {
	for _, line := range lines {
		if m := inlineRe.FindStringSubmatch(line); m != nil {
			return strings.TrimSpace(m[1])
		}
		if m := titleRe.FindStringSubmatch(line); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}
