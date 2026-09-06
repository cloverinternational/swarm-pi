// Package gitpanel provides Bubbletea TUI components for Git operations.
package gitpanel

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitgraph"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
)

// Use DefaultTheme from theme package instead of custom testTheme
func getTestTheme() theme.Theme {
	return theme.DefaultTheme()
}

// mockKeyMsg implements tea.KeyMsg for testing
type mockKeyMsg struct {
	key string
}

func (m mockKeyMsg) String() string { return m.key }
func (m mockKeyMsg) Key() tea.Key   { return tea.Key{Text: m.key, Code: rune(m.key[0])} }

// createTestRepo creates a temporary git repository for testing
func createTestRepo(t *testing.T) string {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "gitpanel-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to init git repo: %v", err)
	}

	// Configure git user (required for commits)
	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to configure git email: %v", err)
	}

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to configure git user: %v", err)
	}

	// Create initial commit
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial content\n"), 0644); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to write test file: %v", err)
	}

	cmd = exec.Command("git", "add", "-A")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to stage files: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to create initial commit: %v", err)
	}

	return tmpDir
}

// TestNew tests creating a new gitpanel model
func TestNew(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	th := getTestTheme()
	model := New(th, tmpDir)

	if model.repo == nil {
		t.Error("Expected repo to be initialized")
	}

	if model.activeTab != TabStatus {
		t.Errorf("Expected default tab to be TabStatus, got %v", model.activeTab)
	}

	if model.statusView == nil {
		t.Error("Expected statusView to be initialized")
	}

	if model.graphView == nil {
		t.Error("Expected graphView to be initialized")
	}

	if model.historyView == nil {
		t.Error("Expected historyView to be initialized")
	}
}

// TestNew_InvalidPath tests creating a gitpanel with invalid path
func TestNew_InvalidPath(t *testing.T) {
	th := getTestTheme()
	model := New(th, "/nonexistent/path/to/repo")

	if model.repo != nil {
		t.Error("Expected repo to be nil for invalid path")
	}

	if model.lastError == nil {
		t.Error("Expected lastError to be set for invalid path")
	}
}

// TestInit tests the Init command returns a refresh status command
func TestInit(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	th := getTestTheme()
	model := New(th, tmpDir)

	cmd := model.Init()
	if cmd == nil {
		t.Fatal("Expected Init() to return a command")
	}

	// Execute the command and verify it returns a StatusRefreshedMsg
	msg := cmd()
	statusMsg, ok := msg.(StatusRefreshedMsg)
	if !ok {
		t.Fatalf("Expected StatusRefreshedMsg, got %T", msg)
	}

	if statusMsg.Error != nil {
		t.Errorf("Unexpected error in StatusRefreshedMsg: %v", statusMsg.Error)
	}

	if statusMsg.Status == nil {
		t.Error("Expected Status to be non-nil")
	}

	if statusMsg.Status != nil && statusMsg.Status.CurrentBranch == "" {
		t.Error("Expected CurrentBranch to be set")
	}
}

// TestInit_WithCommitHistory tests Init loads commit history
func TestInit_WithCommitHistory(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Create additional commits
	for i := range 5 {
		testFile := filepath.Join(tmpDir, "test.txt")
		if err := os.WriteFile(testFile, []byte("content "+string(rune('0'+i))+"\n"), 0644); err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}

		cmd := exec.Command("git", "add", "-A")
		cmd.Dir = tmpDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("Failed to stage: %v", err)
		}

		cmd = exec.Command("git", "commit", "-m", "Commit "+string(rune('0'+i)))
		cmd.Dir = tmpDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("Failed to commit: %v", err)
		}
	}

	th := getTestTheme()
	model := New(th, tmpDir)

	cmd := model.Init()
	if cmd == nil {
		t.Fatal("Expected Init() to return a command")
	}

	msg := cmd()
	statusMsg, ok := msg.(StatusRefreshedMsg)
	if !ok {
		t.Fatalf("Expected StatusRefreshedMsg, got %T", msg)
	}

	if statusMsg.History == nil || len(statusMsg.History) == 0 {
		t.Error("Expected History to be populated")
	}

	// Should have at least 5 commits (initial + 5 new - but initial isn't included in log sometimes)
	if len(statusMsg.History) < 5 {
		t.Errorf("Expected at least 5 commits, got %d", len(statusMsg.History))
	}
}

// TestUpdate_StatusRefreshedMsg tests handling StatusRefreshedMsg
func TestUpdate_StatusRefreshedMsg(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	th := getTestTheme()
	model := New(th, tmpDir)
	model.loading = true

	// Create mock status
	mockStatus := &gitops.RepoStatus{
		CurrentBranch: "main",
		HeadSHA:       "abc123",
	}

	mockGraph := &gitgraph.CommitGraph{}

	mockHistory := []gitops.CommitInfo{
		{SHA: "abc123", ShortSHA: "abc123", Message: "Test commit"},
	}

	msg := StatusRefreshedMsg{
		Status:  mockStatus,
		Graph:   mockGraph,
		History: mockHistory,
	}

	updatedModel, _ := model.Update(msg)

	if updatedModel.loading {
		t.Error("Expected loading to be false after StatusRefreshedMsg")
	}

	if updatedModel.status == nil {
		t.Error("Expected status to be set")
	}

	if updatedModel.status.CurrentBranch != "main" {
		t.Errorf("Expected branch 'main', got '%s'", updatedModel.status.CurrentBranch)
	}

	if updatedModel.lastError != nil {
		t.Errorf("Expected lastError to be nil, got %v", updatedModel.lastError)
	}
}

// TestUpdate_StatusRefreshedMsg_WithError tests handling error in StatusRefreshedMsg
func TestUpdate_StatusRefreshedMsg_WithError(t *testing.T) {
	th := getTestTheme()
	model := New(th, "/nonexistent")

	msg := StatusRefreshedMsg{
		Error: os.ErrNotExist,
	}

	updatedModel, _ := model.Update(msg)

	if updatedModel.lastError == nil {
		t.Error("Expected lastError to be set")
	}
}

// TestUpdate_KeyMsg_TabSwitching tests tab switching with number keys
func TestUpdate_KeyMsg_TabSwitching(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	th := getTestTheme()
	model := New(th, tmpDir)

	tests := []struct {
		key         string
		expectedTab Tab
	}{
		{"1", TabStatus},
		{"2", TabFiles},
		{"3", TabUnstaged},
		{"4", TabStaged},
		{"5", TabCommit},
		{"6", TabGraph},
		{"7", TabHistory},
	}

	for _, tt := range tests {
		keyMsg := mockKeyMsg{key: tt.key}
		updatedModel, _ := model.Update(keyMsg)

		if updatedModel.activeTab != tt.expectedTab {
			t.Errorf("Key '%s': expected tab %v, got %v", tt.key, tt.expectedTab, updatedModel.activeTab)
		}
	}
}

// TestUpdate_KeyMsg_Navigation tests navigation with j/k keys
func TestUpdate_KeyMsg_Navigation(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	th := getTestTheme()
	model := New(th, tmpDir)

	// Add some unstaged files for selection
	model.status = &gitops.RepoStatus{
		Unstaged: []gitops.FileEntry{
			{Path: "file1.txt"},
			{Path: "file2.txt"},
			{Path: "file3.txt"},
		},
	}
	model.activeTab = TabUnstaged
	model.selectedIndex = 0

	// Test moving down
	keyMsg := mockKeyMsg{key: "j"}
	model, _ = model.Update(keyMsg)

	if model.selectedIndex != 1 {
		t.Errorf("Expected selectedIndex 1 after 'j', got %d", model.selectedIndex)
	}

	// Test moving up
	keyMsg = mockKeyMsg{key: "k"}
	model, _ = model.Update(keyMsg)

	if model.selectedIndex != 0 {
		t.Errorf("Expected selectedIndex 0 after 'k', got %d", model.selectedIndex)
	}
}

// TestUpdate_KeyMsg_Refresh tests refresh command
func TestUpdate_KeyMsg_Refresh(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	th := getTestTheme()
	model := New(th, tmpDir)

	keyMsg := mockKeyMsg{key: "r"}
	updatedModel, cmd := model.Update(keyMsg)

	if !updatedModel.loading {
		t.Error("Expected loading to be true after refresh")
	}

	if cmd == nil {
		t.Error("Expected refresh command to be returned")
	}
}

// TestView_RendersTabs tests that View renders tabs
func TestView_RendersTabs(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	th := getTestTheme()
	model := New(th, tmpDir)
	model.width = 100
	model.height = 30
	model.status = &gitops.RepoStatus{
		CurrentBranch: "main",
		HeadSHA:       "abc123def456",
	}

	view := model.View()

	// Should contain tab names
	if !strings.Contains(view, "Status") {
		t.Error("Expected view to contain 'Status' tab")
	}
	if !strings.Contains(view, "Graph") {
		t.Error("Expected view to contain 'Graph' tab")
	}
	if !strings.Contains(view, "History") {
		t.Error("Expected view to contain 'History' tab")
	}
}

// TestView_NoRepo tests View when repo is nil
func TestView_NoRepo(t *testing.T) {
	th := getTestTheme()
	model := New(th, "/nonexistent")
	model.width = 100
	model.height = 30

	view := model.View()

	if !strings.Contains(view, "Error") && !strings.Contains(view, "repository") {
		t.Error("Expected error message about repository")
	}
}

// TestView_Loading tests View when loading
func TestView_Loading(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	th := getTestTheme()
	model := New(th, tmpDir)
	model.width = 100
	model.height = 30
	model.loading = true

	view := model.View()

	if !strings.Contains(view, "Loading") {
		t.Error("Expected view to show loading state")
	}
}

// TestSetSize tests setting panel dimensions
func TestSetSize(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	th := getTestTheme()
	model := New(th, tmpDir)

	model.SetSize(120, 40)

	if model.width != 120 {
		t.Errorf("Expected width 120, got %d", model.width)
	}

	if model.height != 40 {
		t.Errorf("Expected height 40, got %d", model.height)
	}
}

// TestGetActiveTab tests GetActiveTab
func TestGetActiveTab(t *testing.T) {
	th := getTestTheme()
	model := Model{
		theme:     th,
		activeTab: TabGraph,
	}

	if model.GetActiveTab() != TabGraph {
		t.Errorf("Expected TabGraph, got %v", model.GetActiveTab())
	}
}

// TestSetActiveTab tests SetActiveTab
func TestSetActiveTab(t *testing.T) {
	th := getTestTheme()
	model := Model{
		theme:         th,
		activeTab:     TabStatus,
		selectedIndex: 5,
	}

	model.SetActiveTab(TabHistory)

	if model.activeTab != TabHistory {
		t.Errorf("Expected TabHistory, got %v", model.activeTab)
	}

	if model.selectedIndex != 0 {
		t.Errorf("Expected selectedIndex to reset to 0, got %d", model.selectedIndex)
	}
}

// TestTab_String tests Tab.String() method
func TestTab_String(t *testing.T) {
	tests := []struct {
		tab      Tab
		expected string
	}{
		{TabStatus, "Status"},
		{TabFiles, "Files"},
		{TabUnstaged, "Unstaged"},
		{TabStaged, "Staged"},
		{TabCommit, "Commit"},
		{TabGraph, "Graph"},
		{TabHistory, "History"},
		{Tab(99), "Unknown"},
	}

	for _, tt := range tests {
		if tt.tab.String() != tt.expected {
			t.Errorf("Tab(%d).String() = %q, want %q", tt.tab, tt.tab.String(), tt.expected)
		}
	}
}

// TestStageOperations tests staging/unstaging file operations
func TestStageOperations(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Create a modified file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("modified content\n"), 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	th := getTestTheme()
	model := New(th, tmpDir)

	// Initialize status
	cmd := model.Init()
	msg := cmd()
	model, _ = model.Update(msg)

	// Verify we have unstaged changes
	if model.status == nil || len(model.status.Unstaged) == 0 {
		t.Skip("No unstaged changes detected")
	}

	// Test stage all
	model.activeTab = TabUnstaged
	keyMsg := mockKeyMsg{key: "a"}
	model, stageCmd := model.Update(keyMsg)

	if stageCmd == nil {
		t.Error("Expected stage command to be returned")
	}

	if !model.loading {
		t.Error("Expected loading to be true during stage operation")
	}

	// Execute the stage command
	stageMsg := stageCmd()
	opMsg, ok := stageMsg.(OperationCompleteMsg)
	if !ok {
		t.Fatalf("Expected OperationCompleteMsg, got %T", stageMsg)
	}

	if opMsg.Error != nil {
		t.Errorf("Unexpected error during stage: %v", opMsg.Error)
	}

	if opMsg.Operation != "stage all" {
		t.Errorf("Expected operation 'stage all', got %q", opMsg.Operation)
	}
}

// TestGraphView tests the graph view component
func TestGraphView(t *testing.T) {
	th := getTestTheme()
	gv := NewGraphView(th)

	gv.SetSize(80, 20)

	if gv.width != 80 {
		t.Errorf("Expected width 80, got %d", gv.width)
	}

	// Test empty state
	view := gv.View()
	if !strings.Contains(view, "No commit history") {
		t.Error("Expected empty state message")
	}

	// Create mock graph
	commits := []*gitgraph.CommitNode{
		{SHA: "aaa111", ShortSHA: "aaa111", Message: "First commit", Lane: 0},
		{SHA: "bbb222", ShortSHA: "bbb222", Message: "Second commit", Lane: 0},
	}

	graph := &gitgraph.CommitGraph{
		Commits:  make(map[string]*gitgraph.CommitNode),
		MaxLanes: 1,
	}
	for _, c := range commits {
		graph.Commits[c.SHA] = c
	}

	gv.SetGraph(graph)

	if gv.selectedIndex != 0 {
		t.Errorf("Expected selectedIndex 0 after SetGraph, got %d", gv.selectedIndex)
	}

	// Test navigation
	gv.SelectNext()
	if gv.selectedIndex != 1 {
		t.Errorf("Expected selectedIndex 1 after SelectNext, got %d", gv.selectedIndex)
	}

	gv.SelectPrev()
	if gv.selectedIndex != 0 {
		t.Errorf("Expected selectedIndex 0 after SelectPrev, got %d", gv.selectedIndex)
	}
}

// TestHistoryView tests the history view component
func TestHistoryView(t *testing.T) {
	th := getTestTheme()
	hv := NewHistoryView(th)

	hv.SetSize(80, 20)

	// Test empty state
	view := hv.View()
	if !strings.Contains(view, "No commit history") {
		t.Error("Expected empty state message")
	}

	// Set mock commits
	commits := []gitops.CommitInfo{
		{SHA: "aaa111", ShortSHA: "aaa111", Message: "First commit"},
		{SHA: "bbb222", ShortSHA: "bbb222", Message: "Second commit"},
		{SHA: "ccc333", ShortSHA: "ccc333", Message: "Third commit"},
	}

	hv.SetCommits(commits)

	if hv.selectedIndex != 0 {
		t.Errorf("Expected selectedIndex 0 after SetCommits, got %d", hv.selectedIndex)
	}

	// Test navigation
	hv.SelectNext()
	if hv.selectedIndex != 1 {
		t.Errorf("Expected selectedIndex 1 after SelectNext, got %d", hv.selectedIndex)
	}

	// Test SelectedCommit
	selected := hv.SelectedCommit()
	if selected == nil || selected.SHA != "bbb222" {
		t.Error("Expected selected commit to be bbb222")
	}

	hv.SelectPrev()
	if hv.selectedIndex != 0 {
		t.Errorf("Expected selectedIndex 0 after SelectPrev, got %d", hv.selectedIndex)
	}

	// Test view renders
	view = hv.View()
	if !strings.Contains(view, "Commit History") {
		t.Error("Expected view to contain 'Commit History'")
	}
}

// TestStatusView tests the status view component
func TestStatusView(t *testing.T) {
	th := getTestTheme()
	sv := NewStatusView(th)

	sv.SetSize(80, 30)

	// Test loading state
	view := sv.View()
	if !strings.Contains(view, "Loading") {
		t.Error("Expected loading message when status is nil")
	}

	// Set mock status
	status := &gitops.RepoStatus{
		CurrentBranch: "main",
		HeadSHA:       "abc123def456789012345678901234567890",
		Remote:        "origin",
		RemoteURL:     "https://github.com/test/repo.git",
		Staged: []gitops.FileEntry{
			{Path: "file1.txt", Status: gitops.FileModified},
		},
		Unstaged: []gitops.FileEntry{
			{Path: "file2.txt", Status: gitops.FileModified},
		},
		Untracked: []gitops.FileEntry{
			{Path: "file3.txt", Status: gitops.FileUntracked},
		},
		Ahead:  2,
		Behind: 1,
	}

	sv.SetStatus(status)

	view = sv.View()

	// Check various sections are rendered
	if !strings.Contains(view, "main") {
		t.Error("Expected view to contain branch name 'main'")
	}

	if !strings.Contains(view, "Repository Status") {
		t.Error("Expected view to contain 'Repository Status'")
	}

	if !strings.Contains(view, "Changes") {
		t.Error("Expected view to contain 'Changes'")
	}

	if !strings.Contains(view, "Quick Actions") {
		t.Error("Expected view to contain 'Quick Actions'")
	}
}

// TestDiffViewer tests the diff viewer component
func TestDiffViewer(t *testing.T) {
	th := getTestTheme()
	dv := NewDiffViewer(th)

	dv.SetSize(80, 30)

	// Test empty state
	view := dv.View()
	if !strings.Contains(view, "No diff to display") {
		t.Error("Expected empty state message")
	}

	// Test binary file
	dv.SetDiff("binary.png", &gitops.FileDiff{Binary: true})
	view = dv.View()
	if !strings.Contains(view, "Binary file") {
		t.Error("Expected binary file message")
	}

	// Test actual diff - SetDiff properly
	diff := &gitops.FileDiff{
		Path: "test.go",
		Hunks: []gitops.Hunk{
			{
				Header:    "@@ -1,3 +1,4 @@",
				StartLine: 1,
				NewStart:  1,
				Lines:     []string{" line1", "-removed", "+added", " line3"},
			},
		},
	}

	dv.SetDiff("test.go", diff)
	view = dv.View()

	// The file path should be in the view
	if !strings.Contains(view, "test.go") {
		t.Error("Expected view to contain file path")
	}

	// Test hunk navigation with multiple hunks
	diff2 := &gitops.FileDiff{
		Path: "test2.go",
		Hunks: []gitops.Hunk{
			{Header: "@@ -1,3 +1,4 @@", Lines: []string{" line1"}},
			{Header: "@@ -10,3 +10,4 @@", Lines: []string{" line10"}},
		},
	}

	dv.SetDiff("test2.go", diff2)

	dv.NextHunk()
	if dv.selectedHunk != 1 {
		t.Errorf("Expected selectedHunk 1, got %d", dv.selectedHunk)
	}

	dv.PrevHunk()
	if dv.selectedHunk != 0 {
		t.Errorf("Expected selectedHunk 0, got %d", dv.selectedHunk)
	}
}

// BenchmarkView benchmarks the View rendering
func BenchmarkView(b *testing.B) {
	tmpDir, _ := os.MkdirTemp("", "gitpanel-bench-*")
	defer os.RemoveAll(tmpDir)

	exec.Command("git", "init").Run()
	exec.Command("git", "config", "user.email", "test@test.com").Run()
	exec.Command("git", "config", "user.name", "Test").Run()

	th := getTestTheme()
	model := New(th, tmpDir)
	model.width = 120
	model.height = 40
	model.status = &gitops.RepoStatus{
		CurrentBranch: "main",
		HeadSHA:       "abc123",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = model.View()
	}
}

// TestScrolling tests the scroll functionality for unstaged files
func TestScrolling(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	// Create multiple files to test scrolling
	for i := range 20 {
		filename := filepath.Join(tmpDir, "file"+string(rune('A'+i))+".txt")
		content := []byte("test content " + string(rune('A'+i)) + "\n")
		if err := os.WriteFile(filename, content, 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	th := getTestTheme()
	model := New(th, tmpDir)
	model.SetSize(80, 15)

	refreshMsg := model.refreshStatus()()
	statusMsg := refreshMsg.(StatusRefreshedMsg)
	model.status = statusMsg.Status

	model.activeTab = TabUnstaged
	model.selectedIndex = 0
	model.scrollOffset = 0

	files := append(model.status.Unstaged, model.status.Untracked...)
	if len(files) < 10 {
		t.Skip("Not enough files to test scrolling")
	}

	for range 10 {
		keyMsg := mockKeyMsg{key: "j"}
		model, _ = model.Update(keyMsg)
	}

	if model.selectedIndex != 10 {
		t.Errorf("Expected selectedIndex to be 10, got %d", model.selectedIndex)
	}

	if model.scrollOffset == 0 {
		t.Error("Expected scrollOffset > 0")
	}

	for range 5 {
		keyMsg := mockKeyMsg{key: "k"}
		model, _ = model.Update(keyMsg)
	}

	if model.selectedIndex != 5 {
		t.Errorf("Expected selectedIndex to be 5, got %d", model.selectedIndex)
	}

	keyMsg := mockKeyMsg{key: "g"}
	model, _ = model.Update(keyMsg)

	if model.selectedIndex != 0 {
		t.Errorf("Expected selectedIndex to be 0, got %d", model.selectedIndex)
	}

	if model.scrollOffset != 0 {
		t.Errorf("Expected scrollOffset to be 0, got %d", model.scrollOffset)
	}
}

// TestDiffScrolling tests scrolling within diff viewer
func TestDiffScrolling(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.txt")
	var content strings.Builder
	for i := range 100 {
		content.WriteString("Line ")
		content.WriteString(string(rune('0' + (i % 10))))
		content.WriteString(" of test content\n")
	}
	if err := os.WriteFile(testFile, []byte(content.String()), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	th := getTestTheme()
	model := New(th, tmpDir)
	model.SetSize(80, 20)

	refreshMsg := model.refreshStatus()()
	statusMsg := refreshMsg.(StatusRefreshedMsg)
	model.status = statusMsg.Status

	model.activeTab = TabUnstaged
	model.selectedIndex = 0
	model, cmd := model.viewDiffSelected()

	if !model.viewingDiff {
		t.Error("Expected viewingDiff to be true")
	}

	if cmd == nil {
		t.Error("Expected diff loading command")
	}

	// Simulate diff loaded
	if cmd != nil {
		msg := cmd()
		model, _ = model.Update(msg)
	}

	initialOffset := model.diffViewer.scrollOffset
	keyMsg := mockKeyMsg{key: "j"}
	model, _ = model.Update(keyMsg)

	if model.diffViewer.scrollOffset <= initialOffset {
		t.Error("Expected diff scroll offset to increase")
	}

	keyMsg = mockKeyMsg{key: "q"}
	model, _ = model.Update(keyMsg)

	if model.viewingDiff {
		t.Error("Expected viewingDiff to be false after q")
	}
}

// TestPageUpDown tests page up/down navigation
func TestPageUpDown(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	for i := range 50 {
		filename := filepath.Join(tmpDir, "file"+string(rune('A'+(i%26)))+string(rune('0'+(i/26)))+".txt")
		if err := os.WriteFile(filename, []byte("content\n"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	th := getTestTheme()
	model := New(th, tmpDir)
	model.SetSize(80, 20)

	refreshMsg := model.refreshStatus()()
	statusMsg := refreshMsg.(StatusRefreshedMsg)
	model.status = statusMsg.Status

	model.activeTab = TabUnstaged
	model.selectedIndex = 0

	keyMsg := mockKeyMsg{key: "ctrl+d"}
	model, _ = model.Update(keyMsg)

	if model.selectedIndex == 0 {
		t.Error("Expected selectedIndex to increase")
	}

	firstPageDown := model.selectedIndex

	keyMsg = mockKeyMsg{key: "ctrl+u"}
	model, _ = model.Update(keyMsg)

	if model.selectedIndex >= firstPageDown {
		t.Error("Expected selectedIndex to decrease")
	}
}

// TestEnsureVisible tests the viewport scrolling logic
func TestEnsureVisible(t *testing.T) {
	th := getTestTheme()
	model := New(th, "/tmp")
	model.height = 15

	unstagedFiles := make([]gitops.FileEntry, 30)
	for i := range 30 {
		unstagedFiles[i] = gitops.FileEntry{
			Path:   "file" + string(rune('A'+(i%26))) + ".txt",
			Status: gitops.FileModified,
		}
	}
	model.status = &gitops.RepoStatus{
		Unstaged: unstagedFiles,
	}

	model.activeTab = TabUnstaged
	model.selectedIndex = 0
	model.scrollOffset = 0

	model.selectedIndex = 20
	model = model.ensureVisible()

	if model.scrollOffset == 0 {
		t.Error("Expected scrollOffset to adjust")
	}

	if model.selectedIndex < model.scrollOffset {
		t.Error("Selected item should not be above scroll")
	}

	model.selectedIndex = 0
	model = model.ensureVisible()

	if model.scrollOffset != 0 {
		t.Error("Expected scrollOffset to be 0")
	}
}

// TestStagedTabScrolling tests scrolling in staged files tab
func TestStagedTabScrolling(t *testing.T) {
	tmpDir := createTestRepo(t)
	defer os.RemoveAll(tmpDir)

	for i := range 20 {
		filename := filepath.Join(tmpDir, "staged"+string(rune('A'+i))+".txt")
		if err := os.WriteFile(filename, []byte("staged content\n"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to stage files: %v", err)
	}

	th := getTestTheme()
	model := New(th, tmpDir)
	model.SetSize(80, 15)

	refreshMsg := model.refreshStatus()()
	statusMsg := refreshMsg.(StatusRefreshedMsg)
	model.status = statusMsg.Status

	model.activeTab = TabStaged
	model.selectedIndex = 0

	if len(model.status.Staged) < 10 {
		t.Skip("Not enough staged files")
	}

	for range 10 {
		keyMsg := mockKeyMsg{key: "down"}
		model, _ = model.Update(keyMsg)
	}

	if model.selectedIndex != 10 {
		t.Errorf("Expected selectedIndex 10, got %d", model.selectedIndex)
	}

	if model.scrollOffset == 0 {
		t.Error("Expected scrollOffset > 0")
	}
}
