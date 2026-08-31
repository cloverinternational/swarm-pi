package silver_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/silver"
)

func TestEndToEndRealData(t *testing.T) {
	// Find a project with bronze data.
	projects, err := silver.ListProjects()
	if err != nil || len(projects) == 0 {
		t.Skip("No projects with bronze data available")
	}

	// Use the project with the most data.
	var bestProject string
	var bestCount int
	for _, p := range projects {
		dir, _ := silver.FindBronzeDir(p)
		files, _ := silver.ListBronzeFiles(dir, time.Time{}, time.Time{})
		if len(files) > bestCount {
			bestCount = len(files)
			bestProject = p
		}
	}

	if bestProject == "" {
		t.Skip("No bronze files found")
	}

	bronzeDir, _ := silver.FindBronzeDir(bestProject)
	t.Logf("Using project %s with %d bronze files at %s", bestProject, bestCount, bronzeDir)

	// Step 1: Read all bronze events.
	files, err := silver.ListBronzeFiles(bronzeDir, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ListBronzeFiles: %v", err)
	}
	events, err := silver.ReadBronzeEvents(files)
	if err != nil {
		t.Fatalf("ReadBronzeEvents: %v", err)
	}
	t.Logf("Total events: %d", len(events))

	if len(events) < 20 {
		t.Skip("Not enough events for meaningful test")
	}

	// Step 2: Detect sessions.
	sessions := silver.DetectSessions(events, silver.SessionDetectorConfig{})
	sessions = silver.MergeSessions(sessions, 2*time.Minute)
	t.Logf("Detected sessions: %d", len(sessions))

	if len(sessions) == 0 {
		t.Fatal("No sessions detected")
	}

	// Step 3: Build tree for the largest session.
	var bestSession silver.SessionBoundary
	for _, s := range sessions {
		if s.EventCount > bestSession.EventCount {
			bestSession = s
		}
	}
	t.Logf("Largest session: %d events, conv=%s, %s - %s",
		bestSession.EventCount, bestSession.ConversationID,
		bestSession.StartTime.Format("15:04"), bestSession.EndTime.Format("15:04"))

	tree, err := silver.BuildTree(context.Background(), bestSession, silver.TreeBuilderConfig{})
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}

	t.Logf("Tree built: %d phases, domain=%s", len(tree.Structure), tree.DominantDomain)
	for _, node := range tree.Structure {
		t.Logf("  [%s] %s (%d events, %s)", node.NodeID, node.Title, node.EventCount, node.Domain)
		for _, child := range node.Nodes {
			t.Logf("    [%s] %s (%d events)", child.NodeID, child.Title, child.EventCount)
		}
	}

	// Step 4: Verify tree integrity.
	verification := silver.VerifyTree(tree, bestSession.Windows)
	t.Logf("Verification: accuracy=%.1f%%, valid=%v, issues=%d",
		verification.Accuracy*100, verification.Valid, len(verification.Issues))
	for _, issue := range verification.Issues {
		t.Logf("  [%s] %s: %s", issue.Severity, issue.Type, issue.Message)
	}

	// Step 5: Test retrieval tools.
	treeView := silver.GetSessionTree(tree)
	t.Logf("GetSessionTree output (%d chars):\n%s", len(treeView), treeView[:min(len(treeView), 1000)])

	if len(tree.Structure) > 0 {
		summary, err := silver.GetPhaseSummary(tree, tree.Structure[0].NodeID)
		if err != nil {
			t.Errorf("GetPhaseSummary: %v", err)
		} else {
			t.Logf("GetPhaseSummary [%s] (%d chars)", tree.Structure[0].NodeID, len(summary))
		}
	}

	// Step 6: Test persistence.
	tmpDir := filepath.Join(os.TempDir(), "silver_test_"+fmt.Sprint(time.Now().UnixNano()))
	defer os.RemoveAll(tmpDir)

	store, err := silver.NewSilverStore(tmpDir)
	if err != nil {
		t.Fatalf("NewSilverStore: %v", err)
	}

	tree.ProjectHash = bestProject
	path, err := store.SaveTree(tree)
	if err != nil {
		t.Fatalf("SaveTree: %v", err)
	}
	t.Logf("Saved tree to: %s", path)

	// Reload and verify.
	loaded, err := store.LoadTree(path)
	if err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	if loaded.TotalEvents != tree.TotalEvents {
		t.Errorf("Round-trip mismatch: got %d events, want %d", loaded.TotalEvents, tree.TotalEvents)
	}
	if len(loaded.Structure) != len(tree.Structure) {
		t.Errorf("Round-trip mismatch: got %d phases, want %d", len(loaded.Structure), len(tree.Structure))
	}

	t.Logf("Round-trip OK: %d events, %d phases", loaded.TotalEvents, len(loaded.Structure))

	// Step 7: Domain detection.
	domain := silver.ClassifyDomain(bestSession.Windows)
	t.Logf("Session domain: %s (confidence=%.0f%%)", domain.Domain, domain.Confidence*100)
}
