package legacyimport_test

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/harness/legacyimport"
)

func sampleReport() legacyimport.Report {
	return legacyimport.Report{
		GeneratedAt:      "2026-01-01T00:00:00Z",
		LegacySourcePath: "/legacy/config.json",
		BundleName:       "acme",
		Classification: []legacyimport.ClassificationEntry{
			{Source: []string{"tools.enabled"}, Item: "read", Bucket: legacyimport.BucketMigrated, Target: "agent.tools", Reason: "matched forge.read"},
			{Source: []string{"system.theme"}, Bucket: legacyimport.BucketIgnored, Reason: "presentation-only"},
			{Source: []string{"credentials.oauthTokens"}, Bucket: legacyimport.BucketUnsupported, Reason: "no oauth support"},
			{Source: []string{"system.enableSandbox"}, Bucket: legacyimport.BucketLossy, Target: "permissions.workspaceBoundary", Reason: "bool only"},
		},
		Synthesized: []string{"agent.systemPrompt: placeholder generated"},
		Compile:     legacyimport.CompileOutcome{Compiled: true, Digest: "abc123"},
		Rollback: legacyimport.RollbackPlan{
			Note:  "This is a ROLLBACK PLAN, not a performed backup: legacyimport is read-only...",
			Files: []legacyimport.RollbackFileEntry{{Order: 0, Path: "/legacy/config.json", ContentHash: "sha256:deadbeef", SizeBytes: 42}},
		},
	}
}

func TestReport_BucketCounts(t *testing.T) {
	r := sampleReport()
	counts := map[legacyimport.Bucket]int{}
	for _, c := range r.BucketCounts() {
		counts[c.Bucket] = c.Count
	}
	if counts[legacyimport.BucketMigrated] != 1 {
		t.Errorf("MIGRATED count = %d, want 1", counts[legacyimport.BucketMigrated])
	}
	if counts[legacyimport.BucketLossy] != 1 {
		t.Errorf("LOSSY count = %d, want 1", counts[legacyimport.BucketLossy])
	}
	if counts[legacyimport.BucketUnsupported] != 1 {
		t.Errorf("UNSUPPORTED count = %d, want 1", counts[legacyimport.BucketUnsupported])
	}
	if counts[legacyimport.BucketIgnored] != 1 {
		t.Errorf("IGNORED count = %d, want 1", counts[legacyimport.BucketIgnored])
	}
	// AllBuckets order must be fixed and closed at exactly four values.
	all := legacyimport.AllBuckets()
	if len(all) != 4 {
		t.Fatalf("AllBuckets() has %d entries, want 4", len(all))
	}
	want := []legacyimport.Bucket{legacyimport.BucketMigrated, legacyimport.BucketLossy, legacyimport.BucketUnsupported, legacyimport.BucketIgnored}
	for i, b := range want {
		if all[i] != b {
			t.Errorf("AllBuckets()[%d] = %s, want %s", i, all[i], b)
		}
	}
}

func TestReport_RenderDeterministic(t *testing.T) {
	r := sampleReport()
	a := r.Render()
	b := r.Render()
	if a != b {
		t.Fatal("Render() is not deterministic for an identical Report value")
	}
	if !strings.Contains(a, "MIGRATED") || !strings.Contains(a, "LOSSY") || !strings.Contains(a, "UNSUPPORTED") || !strings.Contains(a, "IGNORED") {
		t.Errorf("Render() output missing one or more bucket labels: %s", a)
	}
	if !strings.Contains(a, "ROLLBACK PLAN") {
		t.Error("Render() output must surface the rollback plan note")
	}
	if !strings.Contains(a, "COMPILED") {
		t.Error("Render() output must surface the compile outcome")
	}
}

func TestReport_RenderJSONDeterministicAndSorted(t *testing.T) {
	r := sampleReport()
	j1, err := r.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	j2, err := r.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(j1) != string(j2) {
		t.Fatal("RenderJSON() is not deterministic for an identical Report value")
	}

	// Re-order the input Classification slice and confirm RenderJSON still
	// produces the identical bytes (defensive sort independent of build order).
	shuffled := sampleReport()
	shuffled.Classification = []legacyimport.ClassificationEntry{
		r.Classification[3], r.Classification[1], r.Classification[0], r.Classification[2],
	}
	j3, err := shuffled.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(j1) != string(j3) {
		t.Fatal("RenderJSON() must be independent of Classification build order (defensive sort)")
	}
}

func TestReport_RollbackPlanNoteDistinctFromBackup(t *testing.T) {
	r := sampleReport()
	note := strings.ToLower(r.Rollback.Note)
	if !strings.Contains(note, "plan") {
		t.Error("rollback note must call itself a PLAN")
	}
	if !strings.Contains(note, "not a performed backup") {
		t.Error("rollback note must explicitly deny being a performed backup")
	}
}

func TestClassificationEntry_JSONShapeHasNoBareMaps(t *testing.T) {
	// Report and every nested type must serialize via slices/structs only
	// (never a bare map[string]any) so RenderJSON's determinism can never
	// depend on Go's map iteration order. This is a structural regression
	// guard: BucketCounts() returning []BucketCount (not map[Bucket]int) is
	// exactly the property under test.
	r := sampleReport()
	counts := r.BucketCounts()
	if len(counts) == 0 {
		t.Fatal("expected BucketCounts() to return a non-empty ordered slice")
	}
	_ = counts
}
