package judge

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests exercise the real parser against real fixture source written to a
// temp dir. No mocks: AnalyzePackageDir reads actual files and we assert the
// extracted signals. They would fail if the AST analysis regressed.

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}

func findTest(tests []AnalyzedTest, name string) *AnalyzedTest {
	for i := range tests {
		if tests[i].TestName == name {
			return &tests[i]
		}
	}
	return nil
}

func TestAnalyze_ClassifiesSignals(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "fix_test.go", `package fix

import "testing"

// TestEmpty has no body.
func TestEmpty(t *testing.T) {}

// TestNoAssert calls real code but never asserts.
func TestNoAssert(t *testing.T) {
	_ = Add(1, 2)
}

// TestSkipped bails out unconditionally.
func TestSkipped(t *testing.T) {
	t.Skip("not ready")
}

// TestTautology asserts a constant against a constant.
func TestTautology(t *testing.T) {
	assert.Equal(t, 1, 1)
}

// TestReal asserts the result of real code.
func TestReal(t *testing.T) {
	got := Add(2, 3)
	if got != 5 {
		t.Fatalf("Add(2,3)=%d want 5", got)
	}
}

// helper is not a test.
func helper(x int) int { return x }
`)

	pkgIdents := map[string]bool{"Add": true}
	tests, err := AnalyzePackageDir(dir, pkgIdents)
	if err != nil {
		t.Fatalf("AnalyzePackageDir: %v", err)
	}

	if e := findTest(tests, "TestEmpty"); e == nil || !e.Signals.EmptyBody {
		t.Errorf("TestEmpty: expected EmptyBody=true, got %+v", e)
	}
	if na := findTest(tests, "TestNoAssert"); na == nil || na.Signals.Assertions != 0 {
		t.Errorf("TestNoAssert: expected 0 assertions, got %+v", na)
	} else if !na.Signals.TouchesRealCode {
		t.Errorf("TestNoAssert: should touch real code (calls Add)")
	}
	if sk := findTest(tests, "TestSkipped"); sk == nil || !sk.Signals.Skipped {
		t.Errorf("TestSkipped: expected Skipped=true, got %+v", sk)
	}
	if ta := findTest(tests, "TestTautology"); ta == nil || !ta.Signals.Tautology {
		t.Errorf("TestTautology: expected Tautology=true, got %+v", ta)
	}
	if re := findTest(tests, "TestReal"); re == nil {
		t.Fatal("TestReal not found")
	} else {
		if re.Signals.Assertions == 0 {
			t.Errorf("TestReal: expected >=1 assertion")
		}
		if !re.Signals.TouchesRealCode {
			t.Errorf("TestReal: expected TouchesRealCode=true")
		}
	}
	if h := findTest(tests, "helper"); h == nil || h.Signals.IsTestFunc {
		t.Errorf("helper: expected IsTestFunc=false, got %+v", h)
	}
}

func TestAnalyze_MockOnly(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "mock_test.go", `package fix

import "testing"

func TestMockOnly(t *testing.T) {
	mockStore := newMockStore()
	mockStore.Put("k", "v")
	if mockStore.Get("k") != "v" {
		t.Fatal("mock broke")
	}
}
`)
	// Real package idents do NOT include the mock; the test never touches them.
	tests, err := AnalyzePackageDir(dir, map[string]bool{"RealStore": true})
	if err != nil {
		t.Fatalf("AnalyzePackageDir: %v", err)
	}
	mt := findTest(tests, "TestMockOnly")
	if mt == nil {
		t.Fatal("TestMockOnly not found")
	}
	if !mt.Signals.MockOnly {
		t.Errorf("expected MockOnly=true, got %+v", mt.Signals)
	}
	if mt.Signals.TouchesRealCode {
		t.Errorf("mock-only test should not touch real code")
	}
}

func TestAnalyze_AllowSkipDirective(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "skip_test.go", `package fix

import "testing"

// judge:allow-skip requires a live network
func TestGatedSkip(t *testing.T) {
	t.Skip("offline")
	_ = Add(1, 1)
}
`)
	tests, err := AnalyzePackageDir(dir, map[string]bool{"Add": true})
	if err != nil {
		t.Fatalf("AnalyzePackageDir: %v", err)
	}
	gs := findTest(tests, "TestGatedSkip")
	if gs == nil {
		t.Fatal("TestGatedSkip not found")
	}
	if gs.Signals.AllowSkipReason == "" {
		t.Errorf("expected allow-skip reason to be captured, got %+v", gs.Signals)
	}
}

func TestAnalyze_EndToEndVerdicts(t *testing.T) {
	// Wire analyzer -> Reduce and assert the final verdicts on the fixtures.
	dir := t.TempDir()
	writeFixture(t, dir, "e2e_test.go", `package fix

import "testing"

func TestEmpty(t *testing.T) {}
func TestReal(t *testing.T) {
	if Add(2,2) != 4 { t.Fatal("bad") }
}
`)
	tests, err := AnalyzePackageDir(dir, map[string]bool{"Add": true})
	if err != nil {
		t.Fatalf("AnalyzePackageDir: %v", err)
	}
	for _, at := range tests {
		r := &JudgeRecord{
			Package: at.Package, File: at.File, Line: at.Line,
			TestName: at.TestName, Deterministic: at.Signals,
		}
		Reduce(r)
		switch at.TestName {
		case "TestEmpty":
			if r.Verdict != VerdictFake {
				t.Errorf("TestEmpty verdict=%q want fake", r.Verdict)
			}
		case "TestReal":
			if r.Verdict != VerdictReal {
				t.Errorf("TestReal verdict=%q want real", r.Verdict)
			}
		}
	}
}
