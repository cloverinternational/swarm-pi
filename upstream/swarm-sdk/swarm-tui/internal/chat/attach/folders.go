package attach

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FolderNode is the dashboard hierarchy: a workspace bucket containing
// attachable sessions. ParentID is intentionally not used here; it remains
// session metadata, not the operator's primary navigation hierarchy.
type FolderNode struct {
	Key      string
	Name     string
	Path     string
	Machine  string
	Targets  []Target
	Badge    StateBadge
	Counts   FolderCounts
	Expanded bool
}

type FolderCounts struct {
	Total        int
	Running      int
	Questions    int
	Approvals    int
	Finished     int
	Failed       int
	Disconnected int
}

type FolderRow struct {
	Folder *FolderNode
	Target *Target
	Depth  int
}

func FolderKey(target Target) string {
	machine := strings.TrimSpace(target.Machine)
	if machine == "" {
		machine = "local"
	}
	workspace := strings.TrimSpace(target.Workspace)
	if workspace == "" {
		workspace = strings.TrimSpace(target.Snapshot.Workspace)
	}
	if workspace == "" {
		workspace = "(unassigned)"
	}
	return machine + "\x00" + filepath.Clean(workspace)
}

func FolderDisplay(target Target) (name, path, machine string) {
	machine = strings.TrimSpace(target.Machine)
	path = strings.TrimSpace(target.Workspace)
	if path == "" {
		path = strings.TrimSpace(target.Snapshot.Workspace)
	}
	if path == "" {
		return "Unassigned workspace", "(no workspace)", machine
	}
	name = filepath.Base(filepath.Clean(path))
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = path
	}
	return name, path, machine
}

func BuildFolders(targets []Target, expanded map[string]bool, mode SortMode) []*FolderNode {
	byKey := make(map[string]*FolderNode)
	for i := range targets {
		target := targets[i]
		key := FolderKey(target)
		folder := byKey[key]
		if folder == nil {
			name, path, machine := FolderDisplay(target)
			folder = &FolderNode{
				Key:      key,
				Name:     name,
				Path:     path,
				Machine:  machine,
				Expanded: true,
			}
			if expanded != nil {
				if value, ok := expanded[key]; ok {
					folder.Expanded = !value
				}
			}
			byKey[key] = folder
		}
		folder.Targets = append(folder.Targets, target)
	}

	folders := make([]*FolderNode, 0, len(byKey))
	for _, folder := range byKey {
		SortTargets(folder.Targets, mode)
		folder.Counts = countFolder(folder.Targets)
		folder.Badge = aggregateBadge(folder.Targets)
		folders = append(folders, folder)
	}
	sort.SliceStable(folders, func(i, j int) bool {
		if folderRank(folders[i]) != folderRank(folders[j]) {
			return folderRank(folders[i]) < folderRank(folders[j])
		}
		if mode == SortActivity {
			if !folderTime(folders[i]).Equal(folderTime(folders[j])) {
				return folderTime(folders[i]).After(folderTime(folders[j]))
			}
		}
		if folders[i].Machine != folders[j].Machine {
			return folders[i].Machine < folders[j].Machine
		}
		if folders[i].Path != folders[j].Path {
			return folders[i].Path < folders[j].Path
		}
		return folders[i].Key < folders[j].Key
	})
	return folders
}

func FlattenFolders(folders []*FolderNode) []FolderRow {
	rows := make([]FolderRow, 0)
	for _, folder := range folders {
		rows = append(rows, FolderRow{Folder: folder})
		if folder.Expanded {
			for i := range folder.Targets {
				rows = append(rows, FolderRow{Folder: folder, Target: &folder.Targets[i], Depth: 1})
			}
		}
	}
	return rows
}

func SortTargets(targets []Target, mode SortMode) {
	sort.SliceStable(targets, func(i, j int) bool {
		a, b := targets[i], targets[j]
		switch mode {
		case SortAttention:
			if attentionRank(a) != attentionRank(b) {
				return attentionRank(a) < attentionRank(b)
			}
		case SortActivity:
			if !targetTime(a).Equal(targetTime(b)) {
				return targetTime(a).After(targetTime(b))
			}
		case SortStatus:
			if statusRank(a) != statusRank(b) {
				return statusRank(a) < statusRank(b)
			}
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
}

func TargetSummary(target Target) string {
	candidates := []string{target.Summary, target.Snapshot.Summary, target.Snapshot.Preview}
	for _, candidate := range candidates {
		candidate = compactSummary(candidate)
		if candidate != "" {
			return candidate
		}
	}
	for i := len(target.Snapshot.Messages) - 1; i >= 0; i-- {
		message := target.Snapshot.Messages[i]
		if strings.EqualFold(message.Role, "system") {
			continue
		}
		if summary := compactSummary(message.Content); summary != "" {
			return summary
		}
		for _, block := range message.Blocks {
			if summary := compactSummary(block.Content); summary != "" {
				return summary
			}
		}
	}
	fallbacks := []string{target.LastTool}
	if target.Kind == TargetKindSubAgent {
		fallbacks = append(fallbacks, target.Name)
	}
	for _, candidate := range fallbacks {
		if summary := compactSummary(candidate); summary != "" {
			return summary
		}
	}
	return "No conversation summary yet"
}

func compactSummary(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len([]rune(value)) > 96 {
		value = string([]rune(value)[:93]) + "..."
	}
	return value
}

func countFolder(targets []Target) FolderCounts {
	counts := FolderCounts{Total: len(targets)}
	for _, target := range targets {
		switch target.Snapshot.Badge(target.Status) {
		case StateBadgeQuestion:
			counts.Questions++
		case StateBadgeApproval, StateBadgePlan:
			counts.Approvals++
		case StateBadgeDone:
			counts.Finished++
		case StateBadgeFailed:
			counts.Failed++
		case StateBadgeStale:
			counts.Disconnected++
		case StateBadgeRunning, StateBadgePlanning:
			counts.Running++
		}
	}
	return counts
}

func aggregateBadge(targets []Target) StateBadge {
	best := StateBadgeIdle
	bestRank := badgeRank(best)
	for _, target := range targets {
		badge := target.Snapshot.Badge(target.Status)
		if rank := badgeRank(badge); rank < bestRank {
			best, bestRank = badge, rank
		}
	}
	return best
}

func badgeRank(badge StateBadge) int {
	switch badge {
	case StateBadgeQuestion:
		return 0
	case StateBadgeApproval, StateBadgePlan:
		return 1
	case StateBadgeFailed:
		return 2
	case StateBadgeRunning, StateBadgePlanning:
		return 3
	case StateBadgeStale:
		return 4
	case StateBadgeDone:
		return 5
	default:
		return 6
	}
}

func folderRank(folder *FolderNode) int { return badgeRank(folder.Badge) }

func targetTime(target Target) time.Time {
	if !target.Snapshot.UpdatedAt.IsZero() {
		return target.Snapshot.UpdatedAt
	}
	return time.Time{}
}

func folderTime(folder *FolderNode) time.Time {
	var latest time.Time
	for _, target := range folder.Targets {
		if current := targetTime(target); current.After(latest) {
			latest = current
		}
	}
	return latest
}
