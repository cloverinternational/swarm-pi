package judge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunPackage_Layer1Only(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "go.mod", "module bt\n\ngo 1.21\n")
	mustWrite(t, dir, "lib.go", "package bt\n\nfunc Double(n int) int { return n * 2 }\n")
	mustWrite(t, dir, "lib_test.go", `package bt

import "testing"

func TestReal(t *testing.T) {
	if Double(2) != 4 { t.Fatal("bad") }
}
func TestFake(t *testing.T) {}
`)
	recs, err := RunPackage(context.Background(), dir, Options{Judge: NoopJudge{}, Mutate: false})
	if err != nil {
		t.Fatalf("RunPackage: %v", err)
	}
	got := map[string]Verdict{}
	for _, r := range recs {
		got[r.TestName] = r.Verdict
	}
	if got["TestReal"] != VerdictReal {
		t.Errorf("TestReal=%q want real", got["TestReal"])
	}
	if got["TestFake"] != VerdictFake {
		t.Errorf("TestFake=%q want fake", got["TestFake"])
	}
}

func TestSummarizeAndBaselineDiff(t *testing.T) {
	recs := []JudgeRecord{
		{Package: "p", TestName: "A", Verdict: VerdictReal},
		{Package: "p", TestName: "B", Verdict: VerdictFake},
		{Package: "p", TestName: "C", Verdict: VerdictFake},
		{Package: "p", TestName: "H", Verdict: VerdictHelper},
	}
	s := Summarize(recs)
	if s.Total != 4 || s.Real != 1 || s.Fake != 2 || s.Helpers != 1 {
		t.Fatalf("summary wrong: %+v", s)
	}

	// Baseline grandfathers p.B; only p.C is a NEW fake.
	b := &Baseline{Fakes: []string{"p.B"}}
	nf := b.NewFakes(recs)
	if len(nf) != 1 || nf[0].TestName != "C" {
		t.Fatalf("expected only p.C as new fake, got %+v", nf)
	}
}

func TestSaveLoadBaseline_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")
	recs := []JudgeRecord{
		{Package: "p", TestName: "B", Verdict: VerdictFake},
		{Package: "p", TestName: "A", Verdict: VerdictReal},
	}
	if err := SaveBaseline(path, recs); err != nil {
		t.Fatalf("SaveBaseline: %v", err)
	}
	b, err := LoadBaseline(path)
	if err != nil {
		t.Fatalf("LoadBaseline: %v", err)
	}
	if len(b.Fakes) != 1 || b.Fakes[0] != "p.B" {
		t.Fatalf("round-trip wrong: %+v", b.Fakes)
	}
}

func TestLoadBaseline_MissingFileIsEmpty(t *testing.T) {
	b, err := LoadBaseline(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing baseline should not error: %v", err)
	}
	if len(b.Fakes) != 0 {
		t.Fatalf("missing baseline should be empty, got %+v", b.Fakes)
	}
}

func TestTestDirs_FindsAndSkipsVendor(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) {
		full := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte("package x\n"), 0o644)
	}
	mk("a/a_test.go")
	mk("b/c/c_test.go")
	mk("vendor/v/v_test.go")
	mk("b/plain.go")

	dirs, err := testDirs(root)
	if err != nil {
		t.Fatalf("testDirs: %v", err)
	}
	has := func(suffix string) bool {
		for _, d := range dirs {
			if filepath.Base(d) == suffix {
				return true
			}
		}
		return false
	}
	if !has("a") || !has("c") {
		t.Errorf("expected dirs a and c, got %v", dirs)
	}
	if has("v") {
		t.Errorf("vendor dir should be skipped, got %v", dirs)
	}
}
