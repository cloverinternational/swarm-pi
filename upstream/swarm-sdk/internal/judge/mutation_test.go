package judge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This is the keystone probe: it builds a real throwaway Go module on disk with
// a genuine test and a fake test, then runs the mutation engine against both.
// It asserts the empirical property the whole framework exists to enforce:
//   - an honest test KILLS mutants (goes red when the code is broken)
//   - a fake test lets mutants SURVIVE (stays green when the code is broken)
// If RunMutation regressed, this test fails. It uses no mocks — it shells out to
// the real `go test`.

func TestRunMutation_KillsAndSurvives(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		// judge:allow-skip the go toolchain is required to run mutation tests
		t.Skip("go toolchain not on PATH")
	}

	dir := t.TempDir()
	mustWrite(t, dir, "go.mod", "module muttest\n\ngo 1.21\n")
	// Real code under test.
	mustWrite(t, dir, "calc.go", `package muttest

func Add(a, b int) int { return a + b }

func IsPositive(n int) bool {
	if n > 0 {
		return true
	}
	return false
}
`)
	// One honest test and one fake test in the same package.
	mustWrite(t, dir, "calc_test.go", `package muttest

import "testing"

// honest: asserts the actual result; mutating + or > will break it.
func TestAddReal(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatalf("Add(2,3) wrong")
	}
	if !IsPositive(1) {
		t.Fatalf("IsPositive(1) should be true")
	}
}

// fake: calls the code but asserts nothing meaningful; mutants survive.
func TestAddFake(t *testing.T) {
	_ = Add(2, 3)
	_ = IsPositive(1)
}
`)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	real := RunMutation(ctx, MutationConfig{
		PackageDir: dir, TestName: "TestAddReal",
		TargetSymbols:    []string{"Add", "IsPositive"},
		PerMutantTimeout: 30 * time.Second,
	})
	if real.Error != "" {
		t.Fatalf("real test mutation errored: %s", real.Error)
	}
	if real.Attempted == 0 {
		t.Fatal("expected mutants to be attempted for the real test")
	}
	if real.Killed == 0 {
		t.Errorf("honest test should kill at least one mutant, got killed=%d survived=%d", real.Killed, real.Survived)
	}

	fake := RunMutation(ctx, MutationConfig{
		PackageDir: dir, TestName: "TestAddFake",
		TargetSymbols:    []string{"Add", "IsPositive"},
		PerMutantTimeout: 30 * time.Second,
	})
	if fake.Error != "" {
		t.Fatalf("fake test mutation errored: %s", fake.Error)
	}
	if fake.Survived == 0 {
		t.Errorf("fake test should let mutants survive, got killed=%d survived=%d", fake.Killed, fake.Survived)
	}

	// And the reducer must classify them correctly from these signals.
	rReal := &JudgeRecord{Deterministic: Deterministic{IsTestFunc: true, Assertions: 2, TouchesRealCode: true}, Mutation: real}
	Reduce(rReal)
	if rReal.Verdict != VerdictReal {
		t.Errorf("real test reduced to %q, want real", rReal.Verdict)
	}
	rFake := &JudgeRecord{Deterministic: Deterministic{IsTestFunc: true, Assertions: 0, TouchesRealCode: true}, Mutation: fake}
	Reduce(rFake)
	if rFake.Verdict != VerdictFake {
		t.Errorf("fake test reduced to %q, want fake", rFake.Verdict)
	}

	// Working tree restored: original calc.go bytes intact after all mutations.
	got, _ := os.ReadFile(filepath.Join(dir, "calc.go"))
	if string(got) == "" || !contains(string(got), "return a + b") {
		t.Errorf("calc.go was not restored to original after mutation:\n%s", got)
	}
}

func TestMutationOperators_Apply(t *testing.T) {
	// Each operator must actually find something to mutate in representative code.
	cases := map[string]string{
		"negate-bool-return": "package p\nfunc f() bool { return true }\n",
		"flip-comparison":    "package p\nfunc f(a,b int) bool { return a == b }\n",
		"swap-arith":         "package p\nfunc f(a,b int) int { return a + b }\n",
	}
	for _, mut := range defaultMutators() {
		src, ok := cases[mut.Name]
		if !ok {
			continue
		}
		dir := t.TempDir()
		p := filepath.Join(dir, "p.go")
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		applied, restore, _, err := applyMutationToFile(p, mut, map[string]bool{"f": true})
		if err != nil {
			t.Errorf("%s: apply error: %v", mut.Name, err)
			continue
		}
		if !applied {
			t.Errorf("%s: expected to apply to representative source", mut.Name)
		}
		mutated, _ := os.ReadFile(p)
		restore()
		restored, _ := os.ReadFile(p)
		if string(mutated) == string(restored) {
			t.Errorf("%s: mutated source identical to restored (no change applied)", mut.Name)
		}
		if string(restored) != src {
			t.Errorf("%s: restore did not return original bytes", mut.Name)
		}
	}
}

// TestRunMutation_NoTargetsIsNoSignal guards the false-positive fix: without
// target symbols, mutation must refuse to run (Error set) rather than mutate
// unrelated code and report a misleading survivor.
func TestRunMutation_NoTargetsIsNoSignal(t *testing.T) {
	m := RunMutation(context.Background(), MutationConfig{
		PackageDir: t.TempDir(), TestName: "TestX",
	})
	if m.Error == "" {
		t.Fatal("expected an error/no-signal when TargetSymbols is empty")
	}
	if m.Ran() {
		t.Fatal("no-target mutation must not count as a usable signal")
	}
}

func mustWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
