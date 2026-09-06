package gitops

import "testing"

func TestParseDiffDetailed_EmptyDiffHasEmptyHunks(t *testing.T) {
	diff, err := parseDiffDetailed("", "file.txt")
	if err != nil {
		t.Fatalf("parseDiffDetailed error: %v", err)
	}
	if diff.Hunks == nil {
		t.Fatal("expected hunks to be an empty slice, got nil")
	}
	if len(diff.Hunks) != 0 {
		t.Fatalf("expected 0 hunks, got %d", len(diff.Hunks))
	}
}

func TestParseDiffDetailed_BinaryDiffHasEmptyHunks(t *testing.T) {
	diff, err := parseDiffDetailed("Binary files a/foo and b/foo differ", "foo")
	if err != nil {
		t.Fatalf("parseDiffDetailed error: %v", err)
	}
	if !diff.Binary {
		t.Fatal("expected binary diff")
	}
	if diff.Hunks == nil {
		t.Fatal("expected hunks to be an empty slice, got nil")
	}
	if len(diff.Hunks) != 0 {
		t.Fatalf("expected 0 hunks, got %d", len(diff.Hunks))
	}
}

func TestParseDiff_EmptyDiffHasEmptyHunks(t *testing.T) {
	diff, err := parseDiff("", "file.txt")
	if err != nil {
		t.Fatalf("parseDiff error: %v", err)
	}
	if diff.Hunks == nil {
		t.Fatal("expected hunks to be an empty slice, got nil")
	}
	if len(diff.Hunks) != 0 {
		t.Fatalf("expected 0 hunks, got %d", len(diff.Hunks))
	}
}
