// Package gitgraph provides commit graph data structures and visualization.
package gitgraph

import (
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
)

// BuildWorktreeGraph builds a combined CommitGraph from the repository,
// fetching all branches (--all via BuildGraphFromRepo), then assigning
// per-worktree ColorIndex to commits based on which worktree's branch
// introduced them.
//
// mainRepoPath is any worktree path -- all worktrees share the same git
// object store, so BuildGraphFromRepo sees every branch.
//
// worktrees is the list from gitops.GetWorktrees() used for color assignment.
// When nil or empty, no color overrides are applied.
func BuildWorktreeGraph(mainRepoPath string, worktrees []gitops.WorktreeInfo, filter GraphFilter) (*CommitGraph, error) {
	graph, err := BuildGraphFromRepo(mainRepoPath, filter)
	if err != nil {
		return nil, err
	}

	if len(worktrees) == 0 || len(graph.Colors) == 0 {
		return graph, nil
	}

	// Build branch name -> color index map from worktrees.
	branchColor := make(map[string]int, len(worktrees))
	for i, wt := range worktrees {
		if wt.Branch != "" {
			branchColor[wt.Branch] = i % len(graph.Colors)
		}
	}

	// For each commit, if any of its branch refs match a worktree branch,
	// override ColorIndex. Only RefBranch refs are considered (not tags or
	// remote tracking branches).
	for _, commit := range graph.Commits {
		for _, ref := range commit.Refs {
			if ref.Type == RefBranch {
				if idx, ok := branchColor[ref.ShortName()]; ok {
					commit.ColorIndex = idx
					break
				}
			}
		}
	}

	return graph, nil
}
