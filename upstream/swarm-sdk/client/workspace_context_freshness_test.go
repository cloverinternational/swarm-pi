package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeIndexWithAge writes an INDEX.md at dir and backdates its mtime by age.
func writeIndexWithAge(t *testing.T, dir, content string, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, "INDEX.md")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-age)
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestFreshness_FreshIndex_NoNotice covers the clean/no-op case (SWA-43):
// a recently modified INDEX.md must NOT trigger the freshness notice.
func TestFreshness_FreshIndex_NoNotice(t *testing.T) {
	dir := t.TempDir()
	writeIndexWithAge(t, dir, "# INDEX.md\nFresh component index.", 1*time.Hour)

	result := loadIndexMdWalk(dir)
	if result == "" {
		t.Fatal("expected non-empty result for a fresh INDEX.md")
	}
	if strings.Contains(result, "INDEX FRESHNESS NOTICE") {
		t.Errorf("fresh index must NOT produce a freshness notice, got:\n%s", result)
	}
	t.Logf("✅ Fresh index (1h old): no notice")
}

// TestFreshness_StaleIndex_Notice covers the stale-index scenario (SWA-43):
// an INDEX.md older than the threshold must trigger the freshness notice
// and name the stale path.
func TestFreshness_StaleIndex_Notice(t *testing.T) {
	dir := t.TempDir()
	stalePath := writeIndexWithAge(t, dir, "# INDEX.md\nStale component index.", indexMdStaleAfter+24*time.Hour)

	result := loadIndexMdWalk(dir)
	if !strings.Contains(result, "INDEX FRESHNESS NOTICE") {
		t.Fatalf("stale index must produce a freshness notice, got:\n%s", result)
	}
	if !strings.Contains(result, stalePath) {
		t.Errorf("freshness notice must name the stale path %q, got:\n%s", stalePath, result)
	}
	// The original content must still be present — the notice augments, not replaces.
	if !strings.Contains(result, "Stale component index.") {
		t.Errorf("stale index content should still be injected, got:\n%s", result)
	}
	t.Logf("✅ Stale index (>14d old): notice present and names path")
}

// TestFreshness_BoundaryJustUnderThreshold covers the meaningful boundary:
// an index just under the threshold is still considered fresh.
func TestFreshness_BoundaryJustUnderThreshold(t *testing.T) {
	dir := t.TempDir()
	writeIndexWithAge(t, dir, "# INDEX.md\nBoundary index.", indexMdStaleAfter-1*time.Hour)

	result := loadIndexMdWalk(dir)
	if strings.Contains(result, "INDEX FRESHNESS NOTICE") {
		t.Errorf("index just under threshold must be fresh, got:\n%s", result)
	}
	t.Logf("✅ Boundary index (just under 14d): no notice")
}

// TestFreshness_MixedHierarchy_OnlyStaleNamed covers a mixed tree: one fresh,
// one stale INDEX.md. The notice must appear and name ONLY the stale one.
func TestFreshness_MixedHierarchy_OnlyStaleNamed(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "component")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	stalePath := writeIndexWithAge(t, root, "# INDEX.md root\nStale root index.", indexMdStaleAfter+48*time.Hour)
	freshPath := writeIndexWithAge(t, child, "# INDEX.md child\nFresh child index.", 2*time.Hour)

	result := loadIndexMdWalk(child)
	if !strings.Contains(result, "INDEX FRESHNESS NOTICE") {
		t.Fatalf("mixed tree with a stale index must produce a notice, got:\n%s", result)
	}
	if !strings.Contains(result, stalePath) {
		t.Errorf("notice must name the stale root path %q", stalePath)
	}
	// The fresh path should not be listed as stale. Check it does not appear in
	// the notice section (after the marker).
	idx := strings.Index(result, "INDEX FRESHNESS NOTICE")
	notice := result[idx:]
	if strings.Contains(notice, freshPath) {
		t.Errorf("fresh path %q must NOT be listed in the freshness notice", freshPath)
	}
	t.Logf("✅ Mixed hierarchy: only stale path named in notice")
}

// TestFreshness_NoIndex_NoNotice covers the missing/no-op case: no INDEX.md
// means empty result and no notice.
func TestFreshness_NoIndex_NoNotice(t *testing.T) {
	dir := t.TempDir()
	result := loadIndexMdWalk(dir)
	if result != "" {
		t.Errorf("expected empty result when no INDEX.md exists, got:\n%s", result)
	}
	t.Logf("✅ No index: empty result, no notice")
}
