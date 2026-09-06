package vocab

import (
	"os"
	"path/filepath"
	"testing"
)

func testArtifact() Artifact {
	a := Artifact{
		Schema:         1,
		TrainedThrough: "2026-08-05",
		MinimumSupport: 20,
		Channel:        "prose",
		Elevated: []Marker{
			{Phrase: "narrow", Lift: 17.8, DF: 226, Echo: 0},
			{Phrase: "too large", Lift: 12.7, DF: 378, Echo: 0.96},
			{Phrase: "let me read the exact", Lift: 35.2, DF: 258, Echo: 0},
			{Phrase: "belowsupport", Lift: 99, DF: 2, Echo: 0},
		},
		Suppressed: []Marker{
			{Phrase: "now", Lift: 0.13, DF: 340, Echo: 0},
			{Phrase: "verify", Lift: 0.32, DF: 120, Echo: 0},
		},
	}
	a.Corpus.FailureDocs = 4508
	a.Corpus.ControlDocs = 64999
	return a
}

func TestBelowSupportMarkersAreNotLoaded(t *testing.T) {
	s := New(testArtifact())
	if got := s.Score("belowsupport everywhere").ElevatedHits; got != 0 {
		t.Fatalf("marker under MinimumSupport must not load, hits=%d", got)
	}
}

func TestElevatedAndMultiWordPhrases(t *testing.T) {
	s := New(testArtifact())
	sc := s.Score("The result is too large. Let me read the exact current lines and narrow the search.")
	if sc.ElevatedHits != 3 {
		t.Fatalf("want 3 elevated hits, got %d (%v)", sc.ElevatedHits, sc.ElevatedMatched)
	}
	// "too large" is 96% echo, so it contributes only 0.04 while the two
	// zero-echo markers contribute 1.0 each.
	if sc.EchoWeighted < 2.0 || sc.EchoWeighted > 2.05 {
		t.Fatalf("echo weighting wrong: %v", sc.EchoWeighted)
	}
}

// The load-bearing case: suppression. A post-failure message lacks the success
// register, so its suppression score must be high; a post-success message
// contains it and must score low.
func TestSuppressionScoreSeparatesConditions(t *testing.T) {
	s := New(testArtifact())
	failure := s.Score("The context did not match. Let me narrow the search.")
	success := s.Score("Now let me verify the change and move on.")
	if failure.SuppressionScore != 1.0 {
		t.Fatalf("failure text should suppress all markers, got %v", failure.SuppressionScore)
	}
	if success.SuppressionScore != 0.0 {
		t.Fatalf("success text contains both markers, got %v", success.SuppressionScore)
	}
	if failure.SuppressionScore <= success.SuppressionScore {
		t.Fatal("suppression must be higher after failure")
	}
}

func TestWordBoundariesAndInertPhrases(t *testing.T) {
	s := New(testArtifact())
	// "narrowing" must not match the marker "narrow".
	if got := s.Score("narrowing the field").ElevatedHits; got != 0 {
		t.Fatalf("substring must not match, hits=%d", got)
	}
	// A marker containing regex metacharacters must be treated as literal data.
	art := testArtifact()
	art.Elevated = []Marker{{Phrase: "a.c", DF: 50}}
	if got := New(art).Score("abc").ElevatedHits; got != 0 {
		t.Fatalf("artifact content must be inert, hits=%d", got)
	}
}

func TestNilScorerAndEmptyInputDegrade(t *testing.T) {
	var s *Scorer
	if got := s.Score("anything"); got.Words != 0 || got.ElevatedHits != 0 {
		t.Fatalf("nil scorer must yield zero score, got %+v", got)
	}
	if got := New(testArtifact()).Score("   "); got.Words != 0 {
		t.Fatalf("empty body must yield zero score, got %+v", got)
	}
}

func TestLoadRoundTripAndSchemaGate(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "prose.json")
	writeArtifact(t, good, `{"schema":1,"trained_through":"2026-08-05","minimum_support":1,
	  "channel":"prose","elevated":[{"phrase":"narrow","lift":17.8,"df":226}],
	  "suppressed":[{"phrase":"now","lift":0.13,"df":340}]}`)
	s, err := Load(good)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.TrainedThrough() != "2026-08-05" || s.Channel() != "prose" {
		t.Fatalf("provenance lost: %q %q", s.TrainedThrough(), s.Channel())
	}
	if s.Score("let me narrow it").ElevatedHits != 1 {
		t.Fatal("loaded artifact did not score")
	}

	bad := filepath.Join(dir, "bad.json")
	writeArtifact(t, bad, `{"schema":99}`)
	if _, err := Load(bad); err == nil {
		t.Fatal("unsupported schema must be rejected")
	}
	if _, err := Load(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing artifact must error")
	}
}

func writeArtifact(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDefaultRegistry(t *testing.T) {
	if Default("nope") != nil {
		t.Fatal("unset channel must be nil")
	}
	SetDefault("prose", New(testArtifact()))
	defer SetDefault("prose", nil)
	if Default("prose") == nil {
		t.Fatal("default not installed")
	}
}
