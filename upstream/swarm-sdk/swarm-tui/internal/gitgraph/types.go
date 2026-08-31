// Package gitgraph provides commit graph data structures and visualization.
// This is a Go port of the octogit/graph_data.py and octogit/graph_layout.py functionality.
package gitgraph

import (
	"encoding/json"
	"time"
)

// CommitType represents the type of commit in the graph.
type CommitType int

const (
	CommitNormal  CommitType = iota // Regular commit
	CommitMerge                     // Merge commit (2+ parents)
	CommitInitial                   // Initial commit (no parents)
	CommitHead                      // Current HEAD position
)

// String returns a string representation of the CommitType.
func (t CommitType) String() string {
	switch t {
	case CommitNormal:
		return "normal"
	case CommitMerge:
		return "merge"
	case CommitInitial:
		return "initial"
	case CommitHead:
		return "head"
	default:
		return "unknown"
	}
}

// RefType represents the type of git reference.
type RefType int

const (
	RefBranch RefType = iota
	RefTag
	RefRemoteBranch
	RefHead
)

// String returns a string representation of the RefType.
func (t RefType) String() string {
	switch t {
	case RefBranch:
		return "branch"
	case RefTag:
		return "tag"
	case RefRemoteBranch:
		return "remote"
	case RefHead:
		return "head"
	default:
		return "unknown"
	}
}

// MarshalJSON serializes RefType as a JSON string.
func (t RefType) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

// GitRef represents a Git reference (branch, tag, etc.).
type GitRef struct {
	Name      string  `json:"name"`      // Full reference name
	Type      RefType `json:"type"`      // Type of reference
	CommitSHA string  `json:"commitSHA"` // SHA of commit this ref points to
	IsCurrent bool    `json:"isCurrent"` // True if this is the current HEAD
}

// ShortName returns the shortened reference name.
func (r *GitRef) ShortName() string {
	name := r.Name
	prefixes := []string{"refs/heads/", "refs/tags/", "refs/remotes/"}
	for _, prefix := range prefixes {
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			return name[len(prefix):]
		}
	}
	return name
}

// DisplayName returns the display name with an appropriate icon prefix.
func (r *GitRef) DisplayName() string {
	short := r.ShortName()
	switch r.Type {
	case RefTag:
		return "🏷️  " + short
	case RefRemoteBranch:
		return "🌐 " + short
	case RefHead:
		return "➤ " + short
	default:
		return "🌿 " + short
	}
}

// CommitNode represents a single commit in the graph.
type CommitNode struct {
	SHA         string    `json:"sha"`         // Full commit SHA
	ShortSHA    string    `json:"shortSHA"`    // Abbreviated SHA (7-8 chars)
	Message     string    `json:"message"`     // Commit message (first line)
	FullMessage string    `json:"fullMessage"` // Full commit message
	Author      string    `json:"author"`      // Author name
	AuthorEmail string    `json:"authorEmail"` // Author email
	Committer   string    `json:"-"`           // Committer name (internal only)
	Date        time.Time `json:"date"`        // Commit date

	ParentSHAs []string `json:"parentSHAs"` // Parent commit SHAs
	ChildSHAs  []string `json:"-"`          // Child commit SHAs (internal only)

	Refs []GitRef `json:"refs"` // Branches/tags pointing here

	Type CommitType `json:"-"` // Type of commit (internal only)

	// Layout information (set by layout algorithm)
	Lane       int `json:"lane"`       // Visual column position
	Row        int `json:"row"`        // Visual row position
	ColorIndex int `json:"colorIndex"` // Color for this commit's branch

	// Enhanced layout for continuous branch visualization
	ActiveLanes      map[int]bool `json:"-"` // All lanes active at this commit (internal)
	MergeSourceLanes []int        `json:"-"` // Lanes merging into this commit (internal)
	ContinuesDown    bool         `json:"-"` // Whether this lane continues to children (internal)
}

// IsMerge returns true if this is a merge commit.
func (c *CommitNode) IsMerge() bool {
	return len(c.ParentSHAs) > 1
}

// IsInitial returns true if this is an initial commit.
func (c *CommitNode) IsInitial() bool {
	return len(c.ParentSHAs) == 0
}

// ShortMessage returns a truncated commit message.
func (c *CommitNode) ShortMessage(maxLen int) string {
	if len(c.Message) <= maxLen {
		return c.Message
	}
	if maxLen <= 3 {
		return c.Message[:maxLen]
	}
	return c.Message[:maxLen-3] + "..."
}

// HasRef checks if the commit has any refs (optionally filter by type).
func (c *CommitNode) HasRef(refType *RefType) bool {
	if refType == nil {
		return len(c.Refs) > 0
	}
	for _, ref := range c.Refs {
		if ref.Type == *refType {
			return true
		}
	}
	return false
}

// GraphEdge represents an edge between two commits in the graph.
type GraphEdge struct {
	FromSHA    string `json:"fromSHA"`    // Parent commit SHA
	ToSHA      string `json:"toSHA"`      // Child commit SHA
	FromLane   int    `json:"fromLane"`   // Starting lane
	ToLane     int    `json:"toLane"`     // Ending lane
	IsMerge    bool   `json:"isMerge"`    // True if this edge is part of a merge
	ColorIndex int    `json:"colorIndex"` // Color for this edge
}

// IsStraight returns true if the edge goes straight down (same lane).
func (e *GraphEdge) IsStraight() bool {
	return e.FromLane == e.ToLane
}

// IsCrossing returns true if the edge crosses lanes.
func (e *GraphEdge) IsCrossing() bool {
	return !e.IsStraight()
}

// CommitGraph is the complete commit graph data structure.
type CommitGraph struct {
	Commits map[string]*CommitNode `json:"commits"` // SHA -> CommitNode
	Edges   []GraphEdge            `json:"edges"`   // All edges in the graph
	Refs    map[string]*GitRef     `json:"-"`       // ref name -> GitRef (internal only)

	HeadSHA       string `json:"headSHA"`       // SHA of current HEAD
	CurrentBranch string `json:"currentBranch"` // Name of current branch

	// Layout metadata
	MaxLanes int `json:"maxLanes"` // Maximum number of lanes used
	MaxRows  int `json:"maxRows"`  // Total number of rows

	// Color palette for branches (indices map to colors)
	Colors []string `json:"colors"`
}

// NewCommitGraph creates a new empty commit graph.
func NewCommitGraph() *CommitGraph {
	return &CommitGraph{
		Commits: make(map[string]*CommitNode),
		Refs:    make(map[string]*GitRef),
		Colors: []string{
			"#bb9af7", // Purple
			"#9ece6a", // Green
			"#7dcfff", // Blue
			"#f7768e", // Red
			"#ff9e64", // Orange
			"#e0af68", // Yellow
			"#73daca", // Cyan
			"#c0caf5", // Light blue
		},
	}
}

// AddCommit adds a commit to the graph.
func (g *CommitGraph) AddCommit(commit *CommitNode) {
	g.Commits[commit.SHA] = commit
	if commit.Row+1 > g.MaxRows {
		g.MaxRows = commit.Row + 1
	}
	if commit.Lane+1 > g.MaxLanes {
		g.MaxLanes = commit.Lane + 1
	}
}

// AddEdge adds an edge to the graph.
func (g *CommitGraph) AddEdge(edge GraphEdge) {
	g.Edges = append(g.Edges, edge)
}

// AddRef adds a reference to the graph.
func (g *CommitGraph) AddRef(ref *GitRef) {
	g.Refs[ref.Name] = ref

	// Also add to the commit it points to
	if commit, ok := g.Commits[ref.CommitSHA]; ok {
		commit.Refs = append(commit.Refs, *ref)
	}
}

// GetCommit returns a commit by SHA (supports partial SHAs).
func (g *CommitGraph) GetCommit(sha string) *CommitNode {
	// Try exact match first
	if commit, ok := g.Commits[sha]; ok {
		return commit
	}

	// Try partial SHA match
	for fullSHA, commit := range g.Commits {
		if len(sha) <= len(fullSHA) && fullSHA[:len(sha)] == sha {
			return commit
		}
	}

	return nil
}

// GetCommitsInOrder returns commits sorted by row (topological order).
func (g *CommitGraph) GetCommitsInOrder() []*CommitNode {
	commits := make([]*CommitNode, 0, len(g.Commits))
	for _, commit := range g.Commits {
		commits = append(commits, commit)
	}

	// Sort by row
	for i := 0; i < len(commits)-1; i++ {
		for j := i + 1; j < len(commits); j++ {
			if commits[i].Row > commits[j].Row {
				commits[i], commits[j] = commits[j], commits[i]
			}
		}
	}

	return commits
}

// GetBranchesAtCommit returns all branches pointing to a commit.
func (g *CommitGraph) GetBranchesAtCommit(sha string) []GitRef {
	commit := g.GetCommit(sha)
	if commit == nil {
		return nil
	}

	var branches []GitRef
	for _, ref := range commit.Refs {
		if ref.Type == RefBranch {
			branches = append(branches, ref)
		}
	}
	return branches
}

// GetTagsAtCommit returns all tags pointing to a commit.
func (g *CommitGraph) GetTagsAtCommit(sha string) []GitRef {
	commit := g.GetCommit(sha)
	if commit == nil {
		return nil
	}

	var tags []GitRef
	for _, ref := range commit.Refs {
		if ref.Type == RefTag {
			tags = append(tags, ref)
		}
	}
	return tags
}

// GetColorForLane returns the color for a given lane.
func (g *CommitGraph) GetColorForLane(lane int) string {
	if len(g.Colors) == 0 {
		return "#ffffff"
	}
	return g.Colors[lane%len(g.Colors)]
}

// GraphFilter contains filtering options for the commit graph.
type GraphFilter struct {
	SearchText         string     // Search in commit messages
	AuthorFilter       string     // Filter by author
	BranchFilter       string     // Show only specific branch
	DateFrom           *time.Time // Filter by date range
	DateTo             *time.Time
	ShowMerges         bool // Show/hide merge commits
	ShowTags           bool // Show/hide tags
	ShowRemoteBranches bool // Show/hide remote branches
	MaxCommits         int  // Maximum commits to show
}

// DefaultFilter returns a default filter configuration.
func DefaultFilter() GraphFilter {
	return GraphFilter{
		ShowMerges:         true,
		ShowTags:           true,
		ShowRemoteBranches: true,
		MaxCommits:         100,
	}
}

// Matches checks if a commit matches the filter criteria.
func (f *GraphFilter) Matches(commit *CommitNode) bool {
	// Search text filter
	if f.SearchText != "" {
		searchLower := toLower(f.SearchText)
		if !contains(toLower(commit.Message), searchLower) &&
			!contains(toLower(commit.Author), searchLower) &&
			!contains(toLower(commit.SHA), searchLower) {
			return false
		}
	}

	// Author filter
	if f.AuthorFilter != "" {
		if !contains(toLower(commit.Author), toLower(f.AuthorFilter)) {
			return false
		}
	}

	// Date filter
	if f.DateFrom != nil && commit.Date.Before(*f.DateFrom) {
		return false
	}
	if f.DateTo != nil && commit.Date.After(*f.DateTo) {
		return false
	}

	// Merge filter
	if !f.ShowMerges && commit.IsMerge() {
		return false
	}

	return true
}

// Helper functions
func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		result[i] = c
	}
	return string(result)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || findSubstring(s, substr) >= 0)
}

func findSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
