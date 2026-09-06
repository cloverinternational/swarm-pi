// Package gitgraph provides commit graph data structures and visualization.
package gitgraph

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GraphLayoutEngine calculates visual positions for commits in the graph.
// This is a Go port of the octogit/graph_layout.py functionality.
type GraphLayoutEngine struct {
	graph       *CommitGraph
	laneManager *LaneManager
	filter      GraphFilter
}

// LaneManager manages lane allocation for the graph layout.
type LaneManager struct {
	activeLanes map[int]string // lane -> SHA of commit using this lane
	maxLane     int
}

// NewLaneManager creates a new lane manager.
func NewLaneManager() *LaneManager {
	return &LaneManager{
		activeLanes: make(map[int]string),
	}
}

// AllocateLane allocates a new lane for a commit.
func (lm *LaneManager) AllocateLane(sha string) int {
	// Find the first available lane
	for lane := 0; lane <= lm.maxLane+1; lane++ {
		if _, used := lm.activeLanes[lane]; !used {
			lm.activeLanes[lane] = sha
			if lane > lm.maxLane {
				lm.maxLane = lane
			}
			return lane
		}
	}
	// Should never reach here
	lane := lm.maxLane + 1
	lm.activeLanes[lane] = sha
	lm.maxLane = lane
	return lane
}

// ReleaseLane releases a lane when a commit's branch terminates.
func (lm *LaneManager) ReleaseLane(lane int) {
	delete(lm.activeLanes, lane)
}

// GetActiveLanes returns a copy of the currently active lanes.
func (lm *LaneManager) GetActiveLanes() map[int]bool {
	result := make(map[int]bool)
	for lane := range lm.activeLanes {
		result[lane] = true
	}
	return result
}

// NewGraphLayoutEngine creates a new graph layout engine.
func NewGraphLayoutEngine(filter GraphFilter) *GraphLayoutEngine {
	return &GraphLayoutEngine{
		graph:       NewCommitGraph(),
		laneManager: NewLaneManager(),
		filter:      filter,
	}
}

// BuildGraph builds a commit graph from a git repository.
func (e *GraphLayoutEngine) BuildGraph(repoPath string) (*CommitGraph, error) {
	// Get commit data
	commits, err := e.fetchCommits(repoPath)
	if err != nil {
		return nil, err
	}

	// Get refs (branches, tags)
	refs, err := e.fetchRefs(repoPath)
	if err != nil {
		// Non-fatal, continue without refs
		refs = nil
	}

	refMap := make(map[string][]GitRef)
	for _, ref := range refs {
		refMap[ref.CommitSHA] = append(refMap[ref.CommitSHA], ref)
	}

	// Get current HEAD
	headSHA, currentBranch, err := e.fetchHead(repoPath)
	if err != nil {
		// Non-fatal
		headSHA = ""
		currentBranch = ""
	}

	e.graph.HeadSHA = headSHA
	e.graph.CurrentBranch = currentBranch

	// Add refs to graph
	for _, ref := range refs {
		e.graph.AddRef(&ref)
	}

	// Build parent-child relationships and add commits
	childMap := make(map[string][]string) // parent SHA -> child SHAs
	for _, commit := range commits {
		for _, parentSHA := range commit.ParentSHAs {
			childMap[parentSHA] = append(childMap[parentSHA], commit.SHA)
		}
	}

	// Apply filter and set child SHAs
	var filteredCommits []*CommitNode
	for _, commit := range commits {
		if refsForCommit, ok := refMap[commit.SHA]; ok {
			commit.Refs = refsForCommit
		} else {
			commit.Refs = []GitRef{}
		}

		if e.filter.Matches(commit) {
			commit.ChildSHAs = childMap[commit.SHA]

			// Determine commit type
			if commit.SHA == headSHA {
				commit.Type = CommitHead
			} else if commit.IsInitial() {
				commit.Type = CommitInitial
			} else if commit.IsMerge() {
				commit.Type = CommitMerge
			} else {
				commit.Type = CommitNormal
			}

			filteredCommits = append(filteredCommits, commit)
		}
	}

	// Calculate layout
	e.calculateLayout(filteredCommits)

	return e.graph, nil
}

// fetchCommits fetches commits from the repository.
func (e *GraphLayoutEngine) fetchCommits(repoPath string) ([]*CommitNode, error) {
	format := "%H%x00%h%x00%s%x00%B%x00%an%x00%ae%x00%cn%x00%aI%x00%P%x00---END---"
	args := []string{"log", "--all", fmt.Sprintf("-n%d", e.filter.MaxCommits), fmt.Sprintf("--format=%s", format)}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get commits: %w", err)
	}

	var commits []*CommitNode
	entries := strings.SplitSeq(string(output), "---END---")

	for entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		fields := strings.Split(entry, "\x00")
		if len(fields) < 9 {
			continue
		}

		date, _ := time.Parse(time.RFC3339, fields[7])

		parents := []string{}
		if fields[8] != "" {
			parents = strings.Fields(fields[8])
		}

		commit := &CommitNode{
			SHA:         fields[0],
			ShortSHA:    fields[1],
			Message:     fields[2],
			FullMessage: fields[3],
			Author:      fields[4],
			AuthorEmail: fields[5],
			Committer:   fields[6],
			Date:        date,
			ParentSHAs:  parents,
			ActiveLanes: make(map[int]bool),
		}

		commits = append(commits, commit)
	}

	return commits, nil
}

// fetchRefs fetches all refs (branches, tags) from the repository.
func (e *GraphLayoutEngine) fetchRefs(repoPath string) ([]GitRef, error) {
	var refs []GitRef

	// Get branches
	cmd := exec.Command("git", "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/remotes")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		refType := RefBranch
		if strings.HasPrefix(parts[0], "refs/remotes/") {
			refType = RefRemoteBranch
		}

		refs = append(refs, GitRef{
			Name:      parts[0],
			CommitSHA: parts[1],
			Type:      refType,
		})
	}

	// Get tags
	cmd = exec.Command("git", "for-each-ref", "--format=%(refname) %(objectname)", "refs/tags")
	cmd.Dir = repoPath
	output, err = cmd.Output()
	if err == nil {
		for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}

			refs = append(refs, GitRef{
				Name:      parts[0],
				CommitSHA: parts[1],
				Type:      RefTag,
			})
		}
	}

	return refs, nil
}

// fetchHead fetches the current HEAD SHA and branch name.
func (e *GraphLayoutEngine) fetchHead(repoPath string) (string, string, error) {
	// Get HEAD SHA
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", "", err
	}
	headSHA := strings.TrimSpace(string(output))

	// Get current branch
	cmd = exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = repoPath
	output, err = cmd.Output()
	if err != nil {
		return headSHA, "", nil // Detached HEAD
	}
	currentBranch := strings.TrimSpace(string(output))

	return headSHA, currentBranch, nil
}

// calculateLayout assigns visual positions (lane, row) to commits.
func (e *GraphLayoutEngine) calculateLayout(commits []*CommitNode) {
	if len(commits) == 0 {
		return
	}

	// Create a map for quick lookup
	commitMap := make(map[string]*CommitNode)
	for _, c := range commits {
		commitMap[c.SHA] = c
	}

	// Track which commits we've processed
	processed := make(map[string]bool)
	// Track lane assignments for each branch
	branchLanes := make(map[string]int)

	// Process commits in topological order (they should already be sorted)
	for row, commit := range commits {
		commit.Row = row

		// Determine which lane to use
		lane := e.determineLane(commit, commitMap, branchLanes, processed)
		commit.Lane = lane
		commit.ColorIndex = lane

		// Record active lanes at this commit
		commit.ActiveLanes = e.laneManager.GetActiveLanes()

		// Handle merge source lanes
		if commit.IsMerge() {
			for i, parentSHA := range commit.ParentSHAs {
				if i > 0 { // Skip first parent (main line)
					if parent, ok := commitMap[parentSHA]; ok && processed[parentSHA] {
						commit.MergeSourceLanes = append(commit.MergeSourceLanes, parent.Lane)
					}
				}
			}
		}

		// Check if this lane continues down
		commit.ContinuesDown = len(commit.ParentSHAs) > 0

		// Add commit to graph
		e.graph.AddCommit(commit)
		processed[commit.SHA] = true

		// Release lanes for initial commits
		if commit.IsInitial() {
			e.laneManager.ReleaseLane(lane)
		}
	}

	// Second pass: add edges after all lanes are assigned.
	for _, commit := range commits {
		for i, parentSHA := range commit.ParentSHAs {
			if parent, ok := commitMap[parentSHA]; ok {
				edge := GraphEdge{
					FromSHA:    parentSHA,
					ToSHA:      commit.SHA,
					FromLane:   parent.Lane,
					ToLane:     commit.Lane,
					IsMerge:    i > 0,
					ColorIndex: commit.Lane,
				}
				e.graph.AddEdge(edge)
			}
		}
	}
}

// determineLane determines which lane a commit should use.
func (e *GraphLayoutEngine) determineLane(commit *CommitNode, commitMap map[string]*CommitNode, branchLanes map[string]int, processed map[string]bool) int {
	// Check if we should continue an existing lane from a child
	for _, childSHA := range commit.ChildSHAs {
		if child, ok := commitMap[childSHA]; ok && processed[childSHA] {
			// If this is the first parent of the child, continue its lane
			if len(child.ParentSHAs) > 0 && child.ParentSHAs[0] == commit.SHA {
				return child.Lane
			}
		}
	}

	// Check if any refs point to this commit and we have a lane for that branch
	for _, ref := range commit.Refs {
		if lane, ok := branchLanes[ref.Name]; ok {
			return lane
		}
	}

	// Allocate a new lane
	lane := e.laneManager.AllocateLane(commit.SHA)

	// Associate refs with this lane
	for _, ref := range commit.Refs {
		branchLanes[ref.Name] = lane
	}

	return lane
}

// BuildGraphFromRepo is a convenience function to build a graph from a repository path.
func BuildGraphFromRepo(repoPath string, filter GraphFilter) (*CommitGraph, error) {
	engine := NewGraphLayoutEngine(filter)
	return engine.BuildGraph(repoPath)
}

// GraphRenderer renders the commit graph as text.
type GraphRenderer struct {
	graph    *CommitGraph
	useColor bool
}

// NewGraphRenderer creates a new graph renderer.
func NewGraphRenderer(graph *CommitGraph, useColor bool) *GraphRenderer {
	return &GraphRenderer{
		graph:    graph,
		useColor: useColor,
	}
}

// RenderLine renders a single line of the graph.
func (r *GraphRenderer) RenderLine(commit *CommitNode) string {
	var sb strings.Builder

	// Render lane markers
	for lane := 0; lane < r.graph.MaxLanes; lane++ {
		if lane == commit.Lane {
			sb.WriteString("●")
		} else if commit.ActiveLanes[lane] {
			sb.WriteString("│")
		} else {
			sb.WriteString(" ")
		}
		sb.WriteString(" ")
	}

	// Render commit info
	sb.WriteString(" ")
	sb.WriteString(commit.ShortSHA)
	sb.WriteString(" ")
	sb.WriteString(commit.ShortMessage(50))

	// Render refs
	if len(commit.Refs) > 0 {
		sb.WriteString(" (")
		for i, ref := range commit.Refs {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(ref.ShortName())
		}
		sb.WriteString(")")
	}

	return sb.String()
}

// RenderGraph renders the entire graph as a string slice.
func (r *GraphRenderer) RenderGraph() []string {
	commits := r.graph.GetCommitsInOrder()
	lines := make([]string, 0, len(commits))

	for _, commit := range commits {
		lines = append(lines, r.RenderLine(commit))
	}

	return lines
}

// GraphSymbol represents symbols used in graph rendering.
type GraphSymbol struct {
	Commit         string
	MergeCommit    string
	VerticalLine   string
	HorizontalLine string
	TopLeft        string
	TopRight       string
	BottomLeft     string
	BottomRight    string
	Cross          string
	TeeDown        string
	TeeUp          string
	TeeLeft        string
	TeeRight       string
}

// DefaultSymbols returns the default Unicode symbols for graph rendering.
func DefaultSymbols() GraphSymbol {
	return GraphSymbol{
		Commit:         "●",
		MergeCommit:    "◉",
		VerticalLine:   "│",
		HorizontalLine: "─",
		TopLeft:        "┌",
		TopRight:       "┐",
		BottomLeft:     "└",
		BottomRight:    "┘",
		Cross:          "┼",
		TeeDown:        "┬",
		TeeUp:          "┴",
		TeeLeft:        "┤",
		TeeRight:       "├",
	}
}

// ASCIISymbols returns ASCII-only symbols for graph rendering.
func ASCIISymbols() GraphSymbol {
	return GraphSymbol{
		Commit:         "*",
		MergeCommit:    "@",
		VerticalLine:   "|",
		HorizontalLine: "-",
		TopLeft:        "+",
		TopRight:       "+",
		BottomLeft:     "+",
		BottomRight:    "+",
		Cross:          "+",
		TeeDown:        "+",
		TeeUp:          "+",
		TeeLeft:        "+",
		TeeRight:       "+",
	}
}

// GetCommitCount returns the number of commits matching a pattern.
func GetCommitCount(repoPath, pattern string) (int, error) {
	args := []string{"rev-list", "--count"}
	if pattern != "" {
		args = append(args, "--grep="+pattern)
	}
	args = append(args, "HEAD")

	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	count, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetBranchGraph returns a simplified branch graph for display.
func GetBranchGraph(repoPath string, maxCommits int) (string, error) {
	args := []string{"log", "--oneline", "--graph", "--all", fmt.Sprintf("-n%d", maxCommits)}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get branch graph: %w", err)
	}

	return string(output), nil
}

// GetSimplifiedGraph returns a simplified text representation using git's built-in graph.
func GetSimplifiedGraph(repoPath string, maxCommits int, format string) (string, error) {
	if format == "" {
		format = "%h %s"
	}

	args := []string{
		"log",
		"--graph",
		"--all",
		fmt.Sprintf("-n%d", maxCommits),
		fmt.Sprintf("--format=%s", format),
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get graph: %w", err)
	}

	return string(output), nil
}

// ParseGitLogGraph parses the output of git log --graph.
func ParseGitLogGraph(output string) []GraphLine {
	var lines []GraphLine
	lineRegex := regexp.MustCompile(`^([│├└┌─●\*\| \\\/]+)(.*)$`)

	for line := range strings.SplitSeq(output, "\n") {
		if line == "" {
			continue
		}

		matches := lineRegex.FindStringSubmatch(line)
		if matches != nil {
			lines = append(lines, GraphLine{
				GraphPart:   matches[1],
				ContentPart: matches[2],
			})
		} else {
			lines = append(lines, GraphLine{
				GraphPart:   "",
				ContentPart: line,
			})
		}
	}

	return lines
}

// GraphLine represents a line of git log --graph output.
type GraphLine struct {
	GraphPart   string // The graph visualization part
	ContentPart string // The commit info part
}
