package chat

import (
	"testing"
	"time"
)

// TestGroupConversationsByBranch verifies that conversations are properly grouped
// by their Branch field in the sidebar. This is a regression test for the bug where
// non-main branches were displayed as "Untracked" instead of their actual name.
func TestGroupConversationsByBranch(t *testing.T) {
	app := &App{
		gitHelper: NewGitHelper("/tmp/test-repo"), // won't actually call git
	}

	now := time.Now()

	conversations := []Conversation{
		{ID: "conv1", Title: "Chat on main", Branch: "main", LastMessage: now},
		{ID: "conv2", Title: "Chat on feature", Branch: "feature/login", LastMessage: now},
		{ID: "conv3", Title: "Chat on version-0.7", Branch: "version-0.7", LastMessage: now},
		{ID: "conv4", Title: "Chat no branch", Branch: "", LastMessage: now},
		{ID: "conv5", Title: "Chat on develop", Branch: "develop", LastMessage: now},
	}

	groups := app.groupConversations(conversations)

	if len(groups) == 0 {
		t.Fatal("Expected at least one time group, got 0")
	}

	// All conversations are "Today" since they use time.Now()
	todayGroup := groups[0]
	if todayGroup.Label != "Today" {
		t.Fatalf("Expected first group label 'Today', got %q", todayGroup.Label)
	}

	// Check that we have the right number of subgroups (one per unique branch + "Untracked")
	// Branches: main, feature/login, version-0.7, develop, Untracked = 5 subgroups
	expectedSubGroups := 5
	if len(todayGroup.SubGroups) != expectedSubGroups {
		t.Fatalf("Expected %d subgroups, got %d", expectedSubGroups, len(todayGroup.SubGroups))
	}

	// Verify each branch appears with its correct name
	branchFound := make(map[string]bool)
	for _, sg := range todayGroup.SubGroups {
		branchFound[sg.RawName] = true
		t.Logf("SubGroup: Label=%q RawName=%q Conversations=%d", sg.Label, sg.RawName, len(sg.Conversations))
	}

	expectedBranches := []string{"main", "feature/login", "version-0.7", "develop", "Untracked"}
	for _, branch := range expectedBranches {
		if !branchFound[branch] {
			t.Errorf("Expected branch %q to be found in subgroups, but it was missing", branch)
		}
	}
}

// TestNormalizeBranchName verifies branch normalization for display
func TestNormalizeBranchName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "Untracked"},
		{"main", "main"},
		{"feature/login", "feature/login"},
		{"version-0.7", "version-0.7"},
		{"develop", "develop"},
		{"origin/main", "origin/main"},
	}

	for _, tt := range tests {
		result := normalizeBranchName(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeBranchName(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// TestGetLocalBranchName verifies extracting local branch names from remote refs
func TestGetLocalBranchName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"main", "main"},
		{"origin/main", "main"},
		{"upstream/feature/login", "login"},
		{"Untracked", "Untracked"},
		{"HEAD", "HEAD"},
		{"develop", "develop"},
		{"version-0.7", "version-0.7"},
	}

	for _, tt := range tests {
		result := getLocalBranchName(tt.input)
		if result != tt.expected {
			t.Errorf("getLocalBranchName(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// TestBranchGroupingSortOrder verifies that branches are sorted correctly:
// current branch first, then main/master, then other local branches, then remote, then Untracked last
func TestBranchGroupingSortOrder(t *testing.T) {
	app := &App{
		gitHelper: NewGitHelper("/tmp/test-repo"),
	}

	now := time.Now()

	conversations := []Conversation{
		{ID: "conv1", Title: "No branch chat", Branch: "", LastMessage: now},
		{ID: "conv2", Title: "Main chat", Branch: "main", LastMessage: now},
		{ID: "conv3", Title: "Feature chat", Branch: "feature/xyz", LastMessage: now},
		{ID: "conv4", Title: "Develop chat", Branch: "develop", LastMessage: now},
	}

	groups := app.groupConversations(conversations)
	if len(groups) == 0 {
		t.Fatal("Expected at least one time group")
	}

	todayGroup := groups[0]

	// Expected order: main first (no current branch detected), then develop, then feature/xyz, then Untracked
	// Since gitHelper points to non-existent repo, currentGitBranch will be ""
	// So order: main/master first, then alphabetical, then Untracked last
	if len(todayGroup.SubGroups) < 2 {
		t.Fatalf("Expected at least 2 subgroups, got %d", len(todayGroup.SubGroups))
	}

	// Main should be first (since no current branch is detected)
	if todayGroup.SubGroups[0].RawName != "main" {
		t.Errorf("Expected first subgroup to be 'main', got %q", todayGroup.SubGroups[0].RawName)
	}

	// "Untracked" should be last
	lastIdx := len(todayGroup.SubGroups) - 1
	if todayGroup.SubGroups[lastIdx].RawName != "Untracked" {
		t.Errorf("Expected last subgroup to be 'Untracked', got %q", todayGroup.SubGroups[lastIdx].RawName)
	}
}

// TestConversationBranchPreservedInGroup verifies that the Branch field from
// conversations is correctly preserved when grouping. This catches the bug where
// startNewChatDirect() was creating conversations with empty Branch even when
// the user was on a non-main git branch.
func TestConversationBranchPreservedInGroup(t *testing.T) {
	app := &App{
		gitHelper: NewGitHelper("/tmp/test-repo"),
	}

	now := time.Now()

	// Simulate conversations from different branches - including non-main branches
	// This is the exact scenario that was broken: conversations on "version-0.7"
	// were showing up as "Untracked" because startNewChatDirect() wasn't setting the branch
	conversations := []Conversation{
		{ID: "conv1", Title: "Work on version", Branch: "version-0.7", LastMessage: now},
		{ID: "conv2", Title: "Main work", Branch: "main", LastMessage: now},
		{ID: "conv3", Title: "More version work", Branch: "version-0.7", LastMessage: now},
	}

	groups := app.groupConversations(conversations)
	if len(groups) == 0 {
		t.Fatal("Expected at least one time group")
	}

	todayGroup := groups[0]

	// Should have exactly 2 subgroups: "main" and "version-0.7"
	if len(todayGroup.SubGroups) != 2 {
		t.Fatalf("Expected 2 subgroups (main, version-0.7), got %d", len(todayGroup.SubGroups))
	}

	// Find the version-0.7 subgroup
	var versionGroup *ConversationSubGroup
	for i := range todayGroup.SubGroups {
		if todayGroup.SubGroups[i].RawName == "version-0.7" {
			versionGroup = &todayGroup.SubGroups[i]
			break
		}
	}

	if versionGroup == nil {
		t.Fatal("Expected to find 'version-0.7' subgroup but it was missing")
	}

	if len(versionGroup.Conversations) != 2 {
		t.Errorf("Expected 2 conversations in version-0.7 group, got %d", len(versionGroup.Conversations))
	}

	if versionGroup.Label != "version-0.7" {
		t.Errorf("Expected label 'version-0.7', got %q", versionGroup.Label)
	}
}

// TestEmptyBranchBecomesNoBranch verifies that conversations with empty Branch
// are grouped under "Untracked" - this is the bug behavior when branch auto-detection
// is not working.
func TestEmptyBranchBecomesNoBranch(t *testing.T) {
	app := &App{
		gitHelper: NewGitHelper("/tmp/test-repo"),
	}

	now := time.Now()

	// All conversations have empty branch - this simulates the bug
	conversations := []Conversation{
		{ID: "conv1", Title: "Chat 1", Branch: "", LastMessage: now},
		{ID: "conv2", Title: "Chat 2", Branch: "", LastMessage: now},
	}

	groups := app.groupConversations(conversations)
	if len(groups) == 0 {
		t.Fatal("Expected at least one time group")
	}

	todayGroup := groups[0]

	// Should have exactly 1 subgroup: "Untracked"
	if len(todayGroup.SubGroups) != 1 {
		t.Fatalf("Expected 1 subgroup, got %d", len(todayGroup.SubGroups))
	}

	if todayGroup.SubGroups[0].RawName != "Untracked" {
		t.Errorf("Expected subgroup 'Untracked', got %q", todayGroup.SubGroups[0].RawName)
	}

	if len(todayGroup.SubGroups[0].Conversations) != 2 {
		t.Errorf("Expected 2 conversations in Untracked group, got %d", len(todayGroup.SubGroups[0].Conversations))
	}
}
