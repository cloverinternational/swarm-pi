package silver

import (
	"fmt"
	"strings"
	"time"
)

// GetSessionTree returns a compact representation of a SilverTree suitable
// for in-context reasoning by Gold/Steering agents. It includes titles,
// summaries, time ranges, event counts, and domains — but NOT raw events.
// This mirrors PageIndex's get_document_structure() tool.
//
// The output fits in ~2-5K tokens depending on tree depth, making it efficient
// for LLM reasoning without drowning in raw data.
func GetSessionTree(tree SilverTree) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# Session: %s\n", tree.SessionName)
	if tree.SessionDescription != "" {
		fmt.Fprintf(&sb, "%s\n", tree.SessionDescription)
	}
	fmt.Fprintf(&sb, "Time: %s — %s\n",
		tree.StartTime.Format("2006-01-02 15:04"),
		tree.EndTime.Format("15:04"))
	fmt.Fprintf(&sb, "Total events: %d | Domain: %s\n\n",
		tree.TotalEvents, tree.DominantDomain)

	for _, node := range tree.Structure {
		renderNode(&sb, node, 0)
	}

	return sb.String()
}

// renderNode recursively renders a SilverNode into a human-readable tree format.
func renderNode(sb *strings.Builder, node SilverNode, depth int) {
	indent := strings.Repeat("  ", depth)
	bullet := "##"
	if depth == 1 {
		bullet = "###"
	} else if depth >= 2 {
		bullet = "####"
	}

	fmt.Fprintf(sb, "%s%s [%s] %s\n", indent, bullet, node.NodeID, node.Title)
	fmt.Fprintf(sb, "%sTime: %s — %s | Events: %d",
		indent, node.StartTime.Format("15:04"), node.EndTime.Format("15:04"), node.EventCount)

	if node.ErrorCount > 0 {
		fmt.Fprintf(sb, " | Errors: %d", node.ErrorCount)
	}
	if node.CorrectionCount > 0 {
		fmt.Fprintf(sb, " | User corrections: %d", node.CorrectionCount)
	}
	sb.WriteString("\n")

	if len(node.DominantTools) > 0 {
		fmt.Fprintf(sb, "%sTools: %s\n", indent, strings.Join(node.DominantTools, ", "))
	}
	if node.Domain != "" {
		fmt.Fprintf(sb, "%sDomain: %s\n", indent, node.Domain)
	}
	if node.Summary != "" {
		fmt.Fprintf(sb, "%sSummary: %s\n", indent, node.Summary)
	}
	if node.SteeringDecisions != nil && node.SteeringDecisions.TotalCount > 0 {
		sd := node.SteeringDecisions
		fmt.Fprintf(sb, "%sSteering: %d decisions (approve=%d block=%d guide=%d focus=%d)\n",
			indent, sd.TotalCount, sd.ApproveCount, sd.BlockCount, sd.GuideCount, sd.FocusCount)
	}
	sb.WriteString("\n")

	for _, child := range node.Nodes {
		renderNode(sb, child, depth+1)
	}
}

// GetEvents retrieves raw bronze events for a specific time window.
// This mirrors PageIndex's get_page_content() tool — the agent reasons
// about the tree structure first, then fetches only the specific events
// it needs for detailed analysis.
func GetEvents(bronzeDir string, start, end time.Time) ([]BronzeEvent, error) {
	files, err := ListBronzeFiles(bronzeDir, start, end)
	if err != nil {
		return nil, fmt.Errorf("silver/retrieval: list files: %w", err)
	}

	var result []BronzeEvent
	for _, f := range files {
		events, readErr := ReadBronzeFile(f)
		if readErr != nil {
			continue
		}
		for _, evt := range events {
			if (evt.Timestamp.Equal(start) || evt.Timestamp.After(start)) &&
				(evt.Timestamp.Equal(end) || evt.Timestamp.Before(end)) {
				result = append(result, evt)
			}
		}
	}

	return result, nil
}

// GetPhaseSummary returns a detailed statistical summary for a specific node
// in the Silver tree. This is more detailed than the compact tree view but
// doesn't include raw events.
func GetPhaseSummary(tree SilverTree, nodeID string) (string, error) {
	node := findNode(tree.Structure, nodeID)
	if node == nil {
		return "", fmt.Errorf("silver/retrieval: node %q not found in tree", nodeID)
	}

	var sb strings.Builder

	fmt.Fprintf(&sb, "# Phase: %s\n", node.Title)
	fmt.Fprintf(&sb, "Node ID: %s\n", node.NodeID)
	fmt.Fprintf(&sb, "Time range: %s — %s\n",
		node.StartTime.Format("2006-01-02 15:04:05"),
		node.EndTime.Format("15:04:05"))
	fmt.Fprintf(&sb, "Duration: %s\n", node.EndTime.Sub(node.StartTime).Round(time.Second))
	fmt.Fprintf(&sb, "Total events: %d\n", node.EventCount)
	fmt.Fprintf(&sb, "Errors: %d\n", node.ErrorCount)
	fmt.Fprintf(&sb, "User prompts: %d\n", node.UserPromptCount)
	fmt.Fprintf(&sb, "User corrections: %d\n", node.CorrectionCount)

	if node.Domain != "" {
		fmt.Fprintf(&sb, "\n## Domain\n%s", node.Domain)
		if node.DomainSignals != nil {
			fmt.Fprintf(&sb, " (confidence: %.0f%%)\n", node.DomainSignals.Confidence*100)
			if len(node.DomainSignals.Tools) > 0 {
				fmt.Fprintf(&sb, "Signal tools: %s\n", strings.Join(node.DomainSignals.Tools, ", "))
			}
			if len(node.DomainSignals.Entities) > 0 {
				fmt.Fprintf(&sb, "Entities: %s\n", strings.Join(node.DomainSignals.Entities, ", "))
			}
			if len(node.DomainSignals.Artifacts) > 0 {
				fmt.Fprintf(&sb, "Artifacts: %s\n", strings.Join(node.DomainSignals.Artifacts, ", "))
			}
		} else {
			sb.WriteString("\n")
		}
	}

	if len(node.ToolFrequency) > 0 {
		fmt.Fprintf(&sb, "\n## Tool Usage\n")
		// Sort by frequency descending.
		type toolCount struct {
			name  string
			count int
		}
		var tools []toolCount
		for name, count := range node.ToolFrequency {
			tools = append(tools, toolCount{name, count})
		}
		for i := 0; i < len(tools); i++ {
			for j := i + 1; j < len(tools); j++ {
				if tools[j].count > tools[i].count {
					tools[i], tools[j] = tools[j], tools[i]
				}
			}
		}
		for _, t := range tools {
			pct := float64(t.count) / float64(node.EventCount) * 100
			fmt.Fprintf(&sb, "  %-20s %4d (%5.1f%%)\n", t.name, t.count, pct)
		}
	}

	if node.SteeringDecisions != nil && node.SteeringDecisions.TotalCount > 0 {
		sd := node.SteeringDecisions
		fmt.Fprintf(&sb, "\n## Steering Activity\n")
		fmt.Fprintf(&sb, "Total decisions: %d\n", sd.TotalCount)
		fmt.Fprintf(&sb, "  Approve: %d\n", sd.ApproveCount)
		fmt.Fprintf(&sb, "  Block:   %d\n", sd.BlockCount)
		fmt.Fprintf(&sb, "  Guide:   %d\n", sd.GuideCount)
		fmt.Fprintf(&sb, "  Focus:   %d\n", sd.FocusCount)
		if sd.AvgLatencyMs > 0 {
			fmt.Fprintf(&sb, "  Avg latency: %dms\n", sd.AvgLatencyMs)
		}
	}

	if node.Summary != "" {
		fmt.Fprintf(&sb, "\n## Summary\n%s\n", node.Summary)
	}

	if len(node.Nodes) > 0 {
		fmt.Fprintf(&sb, "\n## Sub-phases (%d)\n", len(node.Nodes))
		for _, child := range node.Nodes {
			fmt.Fprintf(&sb, "  - [%s] %s (%d events, %s—%s)\n",
				child.NodeID, child.Title, child.EventCount,
				child.StartTime.Format("15:04"), child.EndTime.Format("15:04"))
		}
	}

	return sb.String(), nil
}

// findNode recursively searches for a node by ID in a tree structure.
func findNode(nodes []SilverNode, nodeID string) *SilverNode {
	for i := range nodes {
		if nodes[i].NodeID == nodeID {
			return &nodes[i]
		}
		if found := findNode(nodes[i].Nodes, nodeID); found != nil {
			return found
		}
	}
	return nil
}

// GetMultiProjectTree returns a combined tree view across multiple projects.
// This enables Gold cross-project analysis.
func GetMultiProjectTree(projectTrees map[string][]SilverTree) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# Multi-Project Session Overview\n\n")

	for projectHash, trees := range projectTrees {
		fmt.Fprintf(&sb, "## Project: %s\n", projectHash)
		fmt.Fprintf(&sb, "Sessions: %d\n\n", len(trees))

		for _, tree := range trees {
			fmt.Fprintf(&sb, "### %s\n", tree.SessionName)
			fmt.Fprintf(&sb, "Time: %s — %s | Events: %d | Domain: %s\n",
				tree.StartTime.Format("2006-01-02 15:04"),
				tree.EndTime.Format("15:04"),
				tree.TotalEvents,
				tree.DominantDomain)

			// Show top-level phases only for cross-project view.
			for _, node := range tree.Structure {
				fmt.Fprintf(&sb, "  - [%s] %s (%d events, %s)\n",
					node.NodeID, node.Title, node.EventCount, node.Domain)
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}
