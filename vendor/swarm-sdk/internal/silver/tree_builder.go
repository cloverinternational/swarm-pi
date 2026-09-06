package silver

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// TreeBuilderConfig controls how the tree hierarchy is generated.
type TreeBuilderConfig struct {
	// WindowDuration for grouping events. Default: 5 minutes.
	WindowDuration time.Duration

	// MaxEventsBeforeSubdivide triggers recursive subdivision of a phase.
	// Default: MaxEventsPerPhaseBeforeSubdivision (50).
	MaxEventsBeforeSubdivide int

	// LLMEnhancer, if set, is called to improve phase titles and summaries.
	// When nil, the builder uses purely statistical heuristics.
	LLMEnhancer TreeEnhancer
}

// TreeEnhancer is an optional callback that uses an LLM to improve the
// statistically-generated tree with better titles, summaries, and possibly
// refined phase boundaries. The silver package never imports agent/provider
// packages — the caller wires the LLM connection.
type TreeEnhancer interface {
	// EnhanceTree receives a statistically-built tree and returns an improved
	// version with better titles and summaries. The context allows cancellation.
	EnhanceTree(ctx context.Context, tree SilverTree, rawWindows []EventWindow) (SilverTree, error)
}

// BuildTree constructs a SilverTree from a session's event windows.
// This is the core PageIndex-inspired algorithm:
//
//  1. Detect phase boundaries from tool usage pattern changes
//  2. Group windows into phases
//  3. Generate titles and summaries (statistical, optionally LLM-enhanced)
//  4. Recursively subdivide large phases
//  5. Compute per-node statistics
//  6. Classify domains per phase
//
// The algorithm works without any LLM calls, producing a useful tree from
// pure heuristics. When cfg.LLMEnhancer is set, titles and summaries are
// improved by an LLM pass.
func BuildTree(ctx context.Context, session SessionBoundary, cfg TreeBuilderConfig) (SilverTree, error) {
	if cfg.MaxEventsBeforeSubdivide <= 0 {
		cfg.MaxEventsBeforeSubdivide = MaxEventsPerPhaseBeforeSubdivision
	}

	windows := session.Windows
	if len(windows) == 0 {
		return SilverTree{}, fmt.Errorf("silver/tree_builder: no event windows to build from")
	}

	// Step 1-2: Detect phase boundaries and group windows.
	phases := detectPhases(windows)

	// Step 3-5: Build nodes from phases.
	var nodes []SilverNode
	for i, phase := range phases {
		nodeID := fmt.Sprintf("%04d", i+1)
		node := buildNodeFromWindows(phase, nodeID, cfg.MaxEventsBeforeSubdivide)
		nodes = append(nodes, node)
	}

	// Step 6: Session-level domain classification.
	sessionDomain := ClassifyDomain(windows)

	tree := SilverTree{
		Version:            CurrentSchemaVersion,
		ConversationID:     session.ConversationID,
		SessionName:        generateSessionName(windows, sessionDomain),
		SessionDescription: generateSessionDescription(nodes, sessionDomain),
		StartTime:          windows[0].StartTime,
		EndTime:            windows[len(windows)-1].EndTime,
		TotalEvents:        session.EventCount,
		DominantDomain:     sessionDomain.Domain,
		Structure:          nodes,
		GeneratedAt:        time.Now().UTC(),
	}

	// Optional LLM enhancement pass.
	if cfg.LLMEnhancer != nil {
		enhanced, err := cfg.LLMEnhancer.EnhanceTree(ctx, tree, windows)
		if err == nil {
			tree = enhanced
		}
		// On error, fall back to the statistical tree silently.
	}

	return tree, nil
}

// phaseGroup is an intermediate grouping of windows that belong to the same phase.
type phaseGroup struct {
	windows []EventWindow
}

// detectPhases identifies phase boundaries by analyzing tool usage pattern
// shifts between adjacent windows. A new phase starts when:
//
//  1. The dominant tool category changes significantly (e.g., research → implementation)
//  2. A user correction/prompt cluster appears (signals task pivot)
//  3. A large time gap exists between windows (>15 min)
//  4. The dominant tools change by more than 50% (Jaccard distance)
func detectPhases(windows []EventWindow) []phaseGroup {
	if len(windows) <= 1 {
		return []phaseGroup{{windows: windows}}
	}

	var phases []phaseGroup
	currentPhase := phaseGroup{windows: []EventWindow{windows[0]}}

	for i := 1; i < len(windows); i++ {
		prev := windows[i-1]
		curr := windows[i]

		isBoundary := false

		// Check 1: Time gap > 15 minutes between window ends/starts.
		gap := curr.StartTime.Sub(prev.EndTime)
		if gap > 15*time.Minute {
			isBoundary = true
		}

		// Check 2: Dominant tool category shift.
		if !isBoundary && toolPatternShift(prev.ToolCounts, curr.ToolCounts) > 0.6 {
			isBoundary = true
		}

		// Check 3: User correction burst in current window.
		if !isBoundary && curr.UserPromptCount >= 2 && prev.UserPromptCount == 0 {
			isBoundary = true
		}

		if isBoundary {
			phases = append(phases, currentPhase)
			currentPhase = phaseGroup{windows: []EventWindow{curr}}
		} else {
			currentPhase.windows = append(currentPhase.windows, curr)
		}
	}
	phases = append(phases, currentPhase)

	return phases
}

// toolPatternShift computes the dissimilarity between two tool frequency maps.
// Returns 0.0 (identical) to 1.0 (completely different) using Jaccard distance.
func toolPatternShift(a, b map[string]int) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}

	// Collect all tool names.
	allTools := make(map[string]bool)
	for t := range a {
		allTools[t] = true
	}
	for t := range b {
		allTools[t] = true
	}

	if len(allTools) == 0 {
		return 0
	}

	// Compute Jaccard distance based on tool presence.
	intersection := 0
	for t := range allTools {
		if a[t] > 0 && b[t] > 0 {
			intersection++
		}
	}

	return 1.0 - float64(intersection)/float64(len(allTools))
}

// buildNodeFromWindows constructs a SilverNode from a group of windows,
// recursively subdividing if the event count exceeds the threshold.
func buildNodeFromWindows(phase phaseGroup, nodeID string, maxEvents int) SilverNode {
	return buildNodeFromWindowsWithDepth(phase, nodeID, maxEvents, 0)
}

// maxTreeDepth prevents infinite recursion in subdivision.
const maxTreeDepth = 4

// buildNodeFromWindowsWithDepth is the recursive implementation with depth tracking.
func buildNodeFromWindowsWithDepth(phase phaseGroup, nodeID string, maxEvents int, depth int) SilverNode {
	windows := phase.windows
	if len(windows) == 0 {
		return SilverNode{NodeID: nodeID, Title: "Empty phase"}
	}

	// Aggregate statistics across all windows in this phase.
	totalEvents := 0
	totalErrors := 0
	totalPrompts := 0
	toolCounts := make(map[string]int)
	var steeringApprove, steeringBlock, steeringGuide, steeringFocus int

	for _, w := range windows {
		totalEvents += w.EventCount
		totalErrors += w.ErrorCount
		totalPrompts += w.UserPromptCount
		for tool, count := range w.ToolCounts {
			toolCounts[tool] += count
		}

		// Count steering decisions from events.
		for _, evt := range w.Events {
			if evt.Type == "steering.decision_made" {
				if decision, ok := evt.Payload["decision"].(string); ok {
					switch decision {
					case "approve":
						steeringApprove++
					case "block":
						steeringBlock++
					case "guide":
						steeringGuide++
					case "focus":
						steeringFocus++
					}
				}
			}
		}
	}

	// Count user corrections (heuristic: prompts containing correction signals).
	corrections := countCorrections(windows)

	// Domain classification for this phase.
	domain := ClassifyDomain(windows)

	// Build dominant tools list (sorted by frequency).
	dominantTools := topNTools(toolCounts, 5)

	node := SilverNode{
		Title:           generatePhaseTitle(windows, dominantTools, domain),
		NodeID:          nodeID,
		StartTime:       windows[0].StartTime,
		EndTime:         windows[len(windows)-1].EndTime,
		Summary:         generatePhaseSummary(windows, totalEvents, totalErrors, corrections, dominantTools, domain),
		EventCount:      totalEvents,
		DominantTools:   dominantTools,
		ToolFrequency:   toolCounts,
		ErrorCount:      totalErrors,
		UserPromptCount: totalPrompts,
		CorrectionCount: corrections,
		Domain:          domain.Domain,
		DomainSignals:   &domain.Signals,
	}

	steeringTotal := steeringApprove + steeringBlock + steeringGuide + steeringFocus
	if steeringTotal > 0 {
		node.SteeringDecisions = &SteeringStats{
			ApproveCount: steeringApprove,
			BlockCount:   steeringBlock,
			GuideCount:   steeringGuide,
			FocusCount:   steeringFocus,
			TotalCount:   steeringTotal,
		}
	}

	// Recursive subdivision: if this phase has too many events and enough
	// windows, split into sub-phases. Depth-limited to prevent infinite recursion.
	if totalEvents > maxEvents && len(windows) > 1 && depth < maxTreeDepth {
		subPhases := subdividePhase(windows)
		if len(subPhases) > 1 { // only subdivide if we actually split
			for j, subPhase := range subPhases {
				childID := fmt.Sprintf("%s.%02d", nodeID, j+1)
				childNode := buildNodeFromWindowsWithDepth(subPhase, childID, maxEvents, depth+1)
				node.Nodes = append(node.Nodes, childNode)
			}
		}
	}

	return node
}

// subdividePhase splits a phase's windows roughly in half based on the
// most significant tool pattern shift within the phase.
func subdividePhase(windows []EventWindow) []phaseGroup {
	if len(windows) <= 2 {
		// Can't meaningfully subdivide further.
		return []phaseGroup{{windows: windows}}
	}

	// Find the best split point (highest tool pattern shift).
	bestShift := 0.0
	bestIdx := len(windows) / 2 // default to midpoint

	for i := 1; i < len(windows); i++ {
		shift := toolPatternShift(windows[i-1].ToolCounts, windows[i].ToolCounts)
		if shift > bestShift {
			bestShift = shift
			bestIdx = i
		}
	}

	// Ensure bestIdx is at least 1 and at most len-1 to avoid empty groups.
	if bestIdx <= 0 {
		bestIdx = 1
	}
	if bestIdx >= len(windows) {
		bestIdx = len(windows) - 1
	}

	return []phaseGroup{
		{windows: windows[:bestIdx]},
		{windows: windows[bestIdx:]},
	}
}

// generatePhaseTitle creates a human-readable title for a phase based on
// its dominant tools and domain.
func generatePhaseTitle(windows []EventWindow, tools []string, domain DomainClassification) string {
	if len(tools) == 0 {
		return "Activity Phase"
	}

	// Determine activity type from dominant tool.
	primaryTool := tools[0]
	var activity string

	switch {
	case primaryTool == "websearch" || primaryTool == "agent_browser" || primaryTool == "web_search":
		activity = "Research"
	case primaryTool == "Edit" || primaryTool == "Write" || primaryTool == "MultiEdit":
		activity = "Implementation"
	case primaryTool == "Read" || primaryTool == "grep" || primaryTool == "semantic_grep":
		activity = "Exploration"
	case primaryTool == "Bash" || primaryTool == "Shell":
		activity = "Execution"
	case primaryTool == "TaskManage" || primaryTool == "task_manage" ||
		primaryTool == "TaskCreate" || primaryTool == "TaskUpdate":
		activity = "Planning"
	default:
		activity = "Activity"
	}

	// Add domain context if available.
	if domain.Domain != DomainUnknown && domain.Confidence > 0.3 {
		domainLabel := strings.Replace(domain.Domain, "_", " ", -1)
		return fmt.Sprintf("%s Phase: %s", activity, strings.Title(domainLabel))
	}

	return fmt.Sprintf("%s Phase (%s)", activity, strings.Join(tools[:min(len(tools), 3)], ", "))
}

// generatePhaseSummary creates a brief statistical summary of a phase.
func generatePhaseSummary(windows []EventWindow, events, errors, corrections int, tools []string, domain DomainClassification) string {
	parts := []string{
		fmt.Sprintf("%d events across %d time windows", events, len(windows)),
	}

	if len(tools) > 0 {
		parts = append(parts, fmt.Sprintf("dominant: %s", strings.Join(tools[:min(len(tools), 3)], "/")))
	}
	if errors > 0 {
		parts = append(parts, fmt.Sprintf("%d errors", errors))
	}
	if corrections > 0 {
		parts = append(parts, fmt.Sprintf("%d user corrections", corrections))
	}

	return strings.Join(parts, "; ")
}

// generateSessionName creates a session-level name.
func generateSessionName(windows []EventWindow, domain DomainClassification) string {
	if len(windows) == 0 {
		return "Empty Session"
	}

	dateStr := windows[0].StartTime.Format("2006-01-02")
	timeStr := windows[0].StartTime.Format("15:04")

	if domain.Domain != DomainUnknown && domain.Confidence > 0.3 {
		domainLabel := strings.Replace(domain.Domain, "_", " ", -1)
		return fmt.Sprintf("%s session %s %s", strings.Title(domainLabel), dateStr, timeStr)
	}

	return fmt.Sprintf("Agent session %s %s", dateStr, timeStr)
}

// generateSessionDescription creates a session-level summary.
func generateSessionDescription(nodes []SilverNode, domain DomainClassification) string {
	totalEvents := 0
	phaseNames := make([]string, 0, len(nodes))
	for _, n := range nodes {
		totalEvents += n.EventCount
		phaseNames = append(phaseNames, n.Title)
	}

	desc := fmt.Sprintf("%d events across %d phases", totalEvents, len(nodes))
	if len(phaseNames) <= 4 {
		desc += ": " + strings.Join(phaseNames, " → ")
	}
	return desc
}

// countCorrections counts user prompts that appear to be corrections.
func countCorrections(windows []EventWindow) int {
	corrections := 0
	correctionSignals := []string{
		"stop", "no ", "dont", "don't", "wrong", "redo", "fix",
		"shit", "bad", "broken", "not what", "try again",
		"instead", "listen", "im not", "thats not",
	}

	for _, w := range windows {
		for _, evt := range w.Events {
			if evt.Type != "user.prompt_submit" {
				continue
			}
			// Check payload for prompt text.
			prompt := ""
			if p, ok := evt.Payload["prompt"].(string); ok {
				prompt = strings.ToLower(p)
			} else if p, ok := evt.Payload["content"].(string); ok {
				prompt = strings.ToLower(p)
			}
			if prompt == "" {
				continue
			}
			for _, signal := range correctionSignals {
				if strings.Contains(prompt, signal) {
					corrections++
					break
				}
			}
		}
	}
	return corrections
}

// topNTools returns the top N tools by frequency.
func topNTools(toolCounts map[string]int, n int) []string {
	type tc struct {
		name  string
		count int
	}
	var tools []tc
	for name, count := range toolCounts {
		tools = append(tools, tc{name, count})
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].count > tools[j].count
	})

	result := make([]string, 0, n)
	for i, t := range tools {
		if i >= n {
			break
		}
		result = append(result, t.name)
	}
	return result
}
