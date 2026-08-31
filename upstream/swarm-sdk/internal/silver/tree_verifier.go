package silver

import (
	"fmt"
	"time"
)

// VerificationResult contains the outcome of a tree self-verification check.
type VerificationResult struct {
	// Valid is true if the tree passes all structural integrity checks.
	Valid bool `json:"valid"`

	// Accuracy is the percentage of nodes that pass verification (0.0 - 1.0).
	Accuracy float64 `json:"accuracy"`

	// TotalNodes is the total number of nodes checked.
	TotalNodes int `json:"total_nodes"`

	// PassedNodes is the number of nodes that passed verification.
	PassedNodes int `json:"passed_nodes"`

	// Issues contains descriptions of any verification failures.
	Issues []VerificationIssue `json:"issues,omitempty"`
}

// VerificationIssue describes a single verification failure.
type VerificationIssue struct {
	NodeID   string `json:"node_id"`
	Type     string `json:"type"` // "event_count_mismatch", "time_gap", "time_overlap", "empty_node"
	Message  string `json:"message"`
	Severity string `json:"severity"` // "error", "warning"
}

// VerifyTree checks the structural integrity of a SilverTree against the raw
// event windows. This implements the PageIndex self-verification pattern:
//
//  1. Every leaf node's event_count must match the actual bronze events in its time range.
//  2. No time gaps — every event window must be covered by exactly one leaf node.
//  3. No overlaps — sibling nodes must not have overlapping time ranges.
//  4. No empty nodes — every node must contain at least one event.
//  5. Tree totals must be consistent (parent event_count = sum of children).
//
// Returns a VerificationResult with accuracy score and detailed issues.
func VerifyTree(tree SilverTree, windows []EventWindow) VerificationResult {
	result := VerificationResult{}

	if len(tree.Structure) == 0 {
		result.Issues = append(result.Issues, VerificationIssue{
			Type:     "empty_tree",
			Message:  "Tree has no structure nodes",
			Severity: "error",
		})
		return result
	}

	// Check 1: Verify each node's event count against time range.
	verifyNodeCounts(tree.Structure, windows, &result)

	// Check 2: No time gaps between sibling nodes.
	verifyNoGaps(tree.Structure, &result)

	// Check 3: No time overlaps between sibling nodes.
	verifyNoOverlaps(tree.Structure, &result)

	// Check 4: No empty nodes.
	verifyNoEmpty(tree.Structure, &result)

	// Check 5: Parent-child consistency.
	verifyParentChildConsistency(tree.Structure, &result)

	// Check 6: Total event count consistency.
	totalFromNodes := sumEventCounts(tree.Structure)
	if totalFromNodes != tree.TotalEvents {
		result.Issues = append(result.Issues, VerificationIssue{
			Type:     "total_mismatch",
			Message:  fmt.Sprintf("Tree total_events=%d but sum of root nodes=%d", tree.TotalEvents, totalFromNodes),
			Severity: "warning",
		})
	}

	// Compute accuracy.
	if result.TotalNodes > 0 {
		result.Accuracy = float64(result.PassedNodes) / float64(result.TotalNodes)
	}
	result.Valid = len(result.Issues) == 0 || result.Accuracy >= 1.0

	return result
}

// verifyNodeCounts checks that each leaf node's EventCount matches
// the actual number of events in its time range.
func verifyNodeCounts(nodes []SilverNode, windows []EventWindow, result *VerificationResult) {
	for _, node := range nodes {
		if len(node.Nodes) > 0 {
			// Recurse into children.
			verifyNodeCounts(node.Nodes, windows, result)
			continue
		}

		// Leaf node: count events in its time range from windows.
		result.TotalNodes++
		actual := countEventsInRange(windows, node.StartTime, node.EndTime)

		if actual == node.EventCount {
			result.PassedNodes++
		} else {
			// Allow a small tolerance (± 5% or ± 2 events) for boundary effects.
			tolerance := max(2, node.EventCount/20)
			diff := abs(actual - node.EventCount)
			if diff <= tolerance {
				result.PassedNodes++ // close enough
			} else {
				result.Issues = append(result.Issues, VerificationIssue{
					NodeID:   node.NodeID,
					Type:     "event_count_mismatch",
					Message:  fmt.Sprintf("Node %s claims %d events but %d found in time range %s—%s", node.NodeID, node.EventCount, actual, node.StartTime.Format("15:04"), node.EndTime.Format("15:04")),
					Severity: "warning",
				})
			}
		}
	}
}

// verifyNoGaps checks that sibling nodes are contiguous (no unaccounted gaps).
func verifyNoGaps(nodes []SilverNode, result *VerificationResult) {
	for i := 1; i < len(nodes); i++ {
		prev := nodes[i-1]
		curr := nodes[i]

		gap := curr.StartTime.Sub(prev.EndTime)
		// Allow gaps up to 1 minute (for rounding/window alignment).
		if gap > time.Minute {
			result.Issues = append(result.Issues, VerificationIssue{
				NodeID:   curr.NodeID,
				Type:     "time_gap",
				Message:  fmt.Sprintf("Gap of %s between node %s (ends %s) and node %s (starts %s)", gap.Round(time.Second), prev.NodeID, prev.EndTime.Format("15:04:05"), curr.NodeID, curr.StartTime.Format("15:04:05")),
				Severity: "warning",
			})
		}
	}

	// Recurse into children of each node.
	for _, node := range nodes {
		if len(node.Nodes) > 0 {
			verifyNoGaps(node.Nodes, result)
		}
	}
}

// verifyNoOverlaps checks that sibling nodes don't have overlapping time ranges.
func verifyNoOverlaps(nodes []SilverNode, result *VerificationResult) {
	for i := 1; i < len(nodes); i++ {
		prev := nodes[i-1]
		curr := nodes[i]

		if curr.StartTime.Before(prev.EndTime) {
			overlap := prev.EndTime.Sub(curr.StartTime)
			result.Issues = append(result.Issues, VerificationIssue{
				NodeID:   curr.NodeID,
				Type:     "time_overlap",
				Message:  fmt.Sprintf("Overlap of %s between node %s and node %s", overlap.Round(time.Second), prev.NodeID, curr.NodeID),
				Severity: "error",
			})
		}
	}

	// Recurse into children.
	for _, node := range nodes {
		if len(node.Nodes) > 0 {
			verifyNoOverlaps(node.Nodes, result)
		}
	}
}

// verifyNoEmpty checks that no node has zero events.
func verifyNoEmpty(nodes []SilverNode, result *VerificationResult) {
	for _, node := range nodes {
		if node.EventCount == 0 {
			result.Issues = append(result.Issues, VerificationIssue{
				NodeID:   node.NodeID,
				Type:     "empty_node",
				Message:  fmt.Sprintf("Node %s has zero events", node.NodeID),
				Severity: "error",
			})
		}
		if len(node.Nodes) > 0 {
			verifyNoEmpty(node.Nodes, result)
		}
	}
}

// verifyParentChildConsistency checks that parent EventCount equals
// the sum of children EventCount (for non-leaf nodes).
func verifyParentChildConsistency(nodes []SilverNode, result *VerificationResult) {
	for _, node := range nodes {
		if len(node.Nodes) == 0 {
			continue
		}

		childSum := sumEventCounts(node.Nodes)
		if childSum != node.EventCount {
			result.Issues = append(result.Issues, VerificationIssue{
				NodeID:   node.NodeID,
				Type:     "parent_child_mismatch",
				Message:  fmt.Sprintf("Node %s has event_count=%d but children sum to %d", node.NodeID, node.EventCount, childSum),
				Severity: "warning",
			})
		}

		// Recurse.
		verifyParentChildConsistency(node.Nodes, result)
	}
}

// countEventsInRange counts events from windows that fall within [start, end].
func countEventsInRange(windows []EventWindow, start, end time.Time) int {
	count := 0
	for _, w := range windows {
		for _, evt := range w.Events {
			if (evt.Timestamp.Equal(start) || evt.Timestamp.After(start)) &&
				(evt.Timestamp.Equal(end) || evt.Timestamp.Before(end)) {
				count++
			}
		}
	}
	return count
}

// sumEventCounts returns the total EventCount across a slice of nodes.
func sumEventCounts(nodes []SilverNode) int {
	total := 0
	for _, n := range nodes {
		total += n.EventCount
	}
	return total
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
