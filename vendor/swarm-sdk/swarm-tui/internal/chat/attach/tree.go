package attach

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type SortMode string

const (
	SortLineage   SortMode = "lineage"
	SortAttention SortMode = "attention"
	SortActivity  SortMode = "activity"
	SortStatus    SortMode = "status"
)

// TreeNode is a target plus its authoritative children.
type TreeNode struct {
	Target   Target
	Children []*TreeNode
}

// BuildTree builds a deterministic forest from target ParentID references.
// Missing parents and cycles become roots so malformed metadata never hides a
// target from the dashboard.
func BuildTree(targets []Target) []*TreeNode {
	nodes := make(map[string]*TreeNode, len(targets))
	order := make([]string, 0, len(targets))
	for i, target := range targets {
		key := target.ID
		if key == "" {
			key = fmt.Sprintf("__anonymous-%d", i)
		}
		if _, exists := nodes[key]; exists {
			continue
		}
		nodes[key] = &TreeNode{Target: target}
		order = append(order, key)
	}

	roots := make([]*TreeNode, 0)
	for _, id := range order {
		node := nodes[id]
		if node.Target.ParentID == "" || node.Target.ParentID == node.Target.ID {
			roots = append(roots, node)
			continue
		}
		parent, exists := nodes[node.Target.ParentID]
		if !exists || createsCycle(node, parent, nodes) {
			roots = append(roots, node)
			continue
		}
		parent.Children = append(parent.Children, node)
	}
	sortTree(roots)
	return roots
}

// SortTree applies an explicit, stable ordering without changing parent
// relationships. Lineage is the deterministic default; the other modes are
// intentional operator views rather than implicit refresh-time reordering.
func SortTree(roots []*TreeNode, mode SortMode) {
	sortTreeMode(roots, mode)
}

func sortTreeMode(nodes []*TreeNode, mode SortMode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i].Target, nodes[j].Target
		switch mode {
		case SortAttention:
			if attentionRank(a) != attentionRank(b) {
				return attentionRank(a) < attentionRank(b)
			}
		case SortActivity:
			if !sameTime(a.Snapshot.UpdatedAt, b.Snapshot.UpdatedAt) {
				return a.Snapshot.UpdatedAt.After(b.Snapshot.UpdatedAt)
			}
		case SortStatus:
			if statusRank(a) != statusRank(b) {
				return statusRank(a) < statusRank(b)
			}
		}
		return lineageLess(a, b)
	})
	for _, node := range nodes {
		sortTreeMode(node.Children, mode)
	}
}

func lineageLess(a, b Target) bool {
	if a.Kind != b.Kind {
		return a.Kind == TargetKindSubAgent
	}
	if a.Machine != b.Machine {
		return a.Machine < b.Machine
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.ID < b.ID
}

func attentionRank(t Target) int {
	modal := strings.ToLower(t.Snapshot.ModalID)
	switch {
	case strings.Contains(modal, "question"):
		return 0
	case strings.Contains(modal, "approval"), strings.Contains(modal, "permission"), strings.Contains(modal, "plan"):
		return 1
	case statusRank(t) == 0:
		return 2
	default:
		return 3
	}
}

func statusRank(t Target) int {
	switch strings.ToLower(strings.TrimSpace(t.Status)) {
	case "failed", "crashed", "error":
		return 0
	case "running", "streaming", "busy", "active":
		return 1
	case "waiting", "pending", "paused":
		return 2
	case "done", "completed", "cancelled":
		return 4
	default:
		if t.Snapshot.Stale {
			return 5
		}
		return 3
	}
}

func sameTime(a, b time.Time) bool { return a.Equal(b) }

func createsCycle(node, parent *TreeNode, nodes map[string]*TreeNode) bool {
	seen := map[string]bool{node.Target.ID: true}
	for current := parent; current != nil; {
		if seen[current.Target.ID] {
			return true
		}
		seen[current.Target.ID] = true
		if current.Target.ParentID == "" {
			return false
		}
		current = nodes[current.Target.ParentID]
	}
	return false
}

func sortTree(nodes []*TreeNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Target.Kind != nodes[j].Target.Kind {
			return nodes[i].Target.Kind == TargetKindSubAgent
		}
		if nodes[i].Target.Machine != nodes[j].Target.Machine {
			return nodes[i].Target.Machine < nodes[j].Target.Machine
		}
		if nodes[i].Target.Name != nodes[j].Target.Name {
			return nodes[i].Target.Name < nodes[j].Target.Name
		}
		return nodes[i].Target.ID < nodes[j].Target.ID
	})
	for _, node := range nodes {
		sortTree(node.Children)
	}
}
