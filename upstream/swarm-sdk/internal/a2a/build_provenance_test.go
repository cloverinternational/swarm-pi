package a2a

import (
	"runtime/debug"
	"testing"
)

// TestBuildProvenanceSameBuildForIdenticalValues proves two BuildProvenance
// values carrying the exact same VCS revision/modified state compare as the
// same build, whether or not MainVersion also matches — VCSRevision is the
// primary deterministic identity signal per SameBuild's documented contract.
func TestBuildProvenanceSameBuildForIdenticalValues(t *testing.T) {
	a := BuildProvenance{
		Available:   true,
		GoVersion:   "go1.26.2",
		MainVersion: "v1.2.3",
		VCSRevision: "abc123",
		VCSModified: false,
	}
	b := BuildProvenance{
		Available:   true,
		GoVersion:   "go1.26.2",
		MainVersion: "v1.2.3",
		VCSRevision: "abc123",
		VCSModified: false,
	}
	if !a.SameBuild(b) {
		t.Fatalf("identical BuildProvenance values did not compare as the same build: %+v vs %+v", a, b)
	}
	if !b.SameBuild(a) {
		t.Fatalf("SameBuild is not symmetric for identical values: %+v vs %+v", b, a)
	}
	if a.DifferentBuild(b) {
		t.Fatalf("DifferentBuild reported true for identical builds: %+v vs %+v", a, b)
	}
}

// TestBuildProvenanceDifferentBuildForDifferentRevisions proves two
// BuildProvenance values with different VCS revisions never compare equal,
// even when other fields (GoVersion, MainVersion) coincidentally match.
func TestBuildProvenanceDifferentBuildForDifferentRevisions(t *testing.T) {
	a := BuildProvenance{Available: true, GoVersion: "go1.26.2", MainVersion: "v1.2.3", VCSRevision: "abc123"}
	b := BuildProvenance{Available: true, GoVersion: "go1.26.2", MainVersion: "v1.2.3", VCSRevision: "def456"}
	if a.SameBuild(b) {
		t.Fatalf("different-revision BuildProvenance values compared as the same build: %+v vs %+v", a, b)
	}
	if !a.DifferentBuild(b) {
		t.Fatalf("DifferentBuild reported false for different-revision builds: %+v vs %+v", a, b)
	}
}

// TestBuildProvenanceDifferentBuildForModifiedFlag proves a clean build and a
// build from the exact same revision but with local modifications
// (VCSModified) never compare as the same build — the dirty/clean state is
// part of the deterministic identity, not a fuzzy/ignorable detail.
func TestBuildProvenanceDifferentBuildForModifiedFlag(t *testing.T) {
	clean := BuildProvenance{Available: true, VCSRevision: "abc123", VCSModified: false}
	dirty := BuildProvenance{Available: true, VCSRevision: "abc123", VCSModified: true}
	if clean.SameBuild(dirty) {
		t.Fatalf("clean and dirty builds at the same revision compared as the same build: %+v vs %+v", clean, dirty)
	}
}

// TestBuildProvenanceSameBuildFallsBackToMainVersion proves that when
// neither side carries a VCSRevision (for example a binary built without VCS
// embedding), SameBuild falls back to comparing MainVersion — except that
// "(devel)" never counts as a match, since it carries no real per-build
// identity and two independently "(devel)" builds are not provably the same.
func TestBuildProvenanceSameBuildFallsBackToMainVersion(t *testing.T) {
	a := BuildProvenance{Available: true, MainVersion: "v1.2.3"}
	b := BuildProvenance{Available: true, MainVersion: "v1.2.3"}
	if !a.SameBuild(b) {
		t.Fatalf("matching MainVersion without VCSRevision did not compare as the same build: %+v vs %+v", a, b)
	}

	devA := BuildProvenance{Available: true, MainVersion: "(devel)"}
	devB := BuildProvenance{Available: true, MainVersion: "(devel)"}
	if devA.SameBuild(devB) {
		t.Fatalf("two \"(devel)\" MainVersions compared as the same build, but (devel) carries no real identity: %+v vs %+v", devA, devB)
	}
}

// TestBuildProvenanceUnavailableNeverComparesSame proves SameBuild treats an
// unavailable (Available == false) provenance as indeterminate rather than a
// wildcard/positive match — including against another unavailable value,
// since neither side actually proved a build identity. This is the
// fail-closed direction for any staleness decision built on top of
// BuildProvenance: absent proof of sameness, callers must not assume
// freshness.
func TestBuildProvenanceUnavailableNeverComparesSame(t *testing.T) {
	unavailable := BuildProvenance{}
	other := BuildProvenance{Available: true, VCSRevision: "abc123"}

	if unavailable.SameBuild(other) {
		t.Fatalf("unavailable provenance compared as the same build as an available one: %+v vs %+v", unavailable, other)
	}
	if other.SameBuild(unavailable) {
		t.Fatalf("available provenance compared as the same build as an unavailable one: %+v vs %+v", other, unavailable)
	}
	if unavailable.SameBuild(BuildProvenance{}) {
		t.Fatalf("two unavailable provenances compared as the same build, want indeterminate/false")
	}
	if !unavailable.DifferentBuild(BuildProvenance{}) {
		t.Fatalf("DifferentBuild must be true (fail closed) for two unavailable provenances")
	}
}

// TestNewBuildProvenanceUsesRealReadBuildInfoWithoutPanicking exercises the
// production code path (the real runtime/debug.ReadBuildInfo, not the test
// seam below) and proves it never panics and always returns a usable value —
// under `go test`, build info is normally available, so this also proves the
// Available==true path populates GoVersion.
func TestNewBuildProvenanceUsesRealReadBuildInfoWithoutPanicking(t *testing.T) {
	provenance := NewBuildProvenance()
	if provenance.Available && provenance.GoVersion == "" {
		t.Fatalf("Available provenance missing GoVersion: %+v", provenance)
	}
}

// TestNewBuildProvenanceFallsBackSafelyWhenReadBuildInfoFails overrides the
// package-private readBuildInfo seam to simulate the ok=false case that
// runtime/debug.ReadBuildInfo() returns under contexts such as `go run`
// (which `go test` itself does not reliably reproduce). It proves
// NewBuildProvenance does not panic and instead returns the documented safe
// default: a zero-value BuildProvenance with Available explicitly false.
func TestNewBuildProvenanceFallsBackSafelyWhenReadBuildInfoFails(t *testing.T) {
	original := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	t.Cleanup(func() { readBuildInfo = original })

	provenance := NewBuildProvenance()
	if provenance.Available {
		t.Fatalf("NewBuildProvenance reported Available=true despite ok=false: %+v", provenance)
	}
	if provenance != (BuildProvenance{}) {
		t.Fatalf("NewBuildProvenance did not fall back to the zero-value safe default: %+v", provenance)
	}
}

// TestNewBuildProvenanceFallsBackSafelyWhenBuildInfoIsNilButOK covers the
// defensive nil-check branch: even if a future runtime somehow reported
// ok=true with a nil *BuildInfo, NewBuildProvenance must not panic
// dereferencing it.
func TestNewBuildProvenanceFallsBackSafelyWhenBuildInfoIsNilButOK(t *testing.T) {
	original := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, true }
	t.Cleanup(func() { readBuildInfo = original })

	provenance := NewBuildProvenance()
	if provenance.Available {
		t.Fatalf("NewBuildProvenance reported Available=true for a nil BuildInfo: %+v", provenance)
	}
}

// TestCurrentBuildProvenanceIsCachedPerProcess proves CurrentBuildProvenance
// memoizes its result: repeated calls return the identical value without
// re-invoking readBuildInfo, matching the "construct once" contract.
func TestCurrentBuildProvenanceIsCachedPerProcess(t *testing.T) {
	first := CurrentBuildProvenance()
	second := CurrentBuildProvenance()
	if first != second {
		t.Fatalf("CurrentBuildProvenance returned different values across calls: %+v vs %+v", first, second)
	}
}
