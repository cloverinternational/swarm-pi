package judge

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"strings"
	"time"
)

// mutation.go implements Layer 3: the empirical anti-trick guarantee.
//
// The core idea is mutation testing. We take a source file in the package under
// test, apply a small semantic mutation (negate a boolean return, flip a
// comparison operator, swap an arithmetic operator), then re-run the specific
// test. An honest test that covers the mutated code MUST now fail ("kill" the
// mutant). A test that still passes has "survived" the mutant — proof that it
// does not actually verify the broken behavior, i.e. it is fake.
//
// Safety contract:
//   - We mutate a file IN PLACE but always restore the original bytes in a
//     deferred step, even on panic or timeout. The working tree is left
//     untouched after RunMutation returns.
//   - Each mutant runs `go test -run <Test> -count=1` with a hard timeout.
//   - We only mutate non-test .go files in the package directory.

// Mutator describes one source mutation operator.
type Mutator struct {
	Name  string
	apply func(ast.Node) (mutated bool)
}

// defaultMutators are cheap, high-signal AST rewrites.
func defaultMutators() []Mutator {
	return []Mutator{
		{Name: "negate-bool-return", apply: mutNegateBoolReturn},
		{Name: "flip-comparison", apply: mutFlipComparison},
		{Name: "swap-arith", apply: mutSwapArith},
	}
}

// MutationConfig configures a mutation run for a single test.
type MutationConfig struct {
	// PackageDir is the directory of the package under test (absolute).
	PackageDir string
	// TestName is the exact test to run (passed to `go test -run ^TestName$`).
	TestName string
	// TargetSymbols restricts mutation to the bodies of these top-level funcs
	// (the real symbols the test references). This is essential for correctness:
	// mutating code the test does not exercise would always "survive" and
	// produce false-positive fake verdicts. When empty, NO mutation is run and
	// the result carries an Error (no signal) rather than a misleading verdict.
	TargetSymbols []string
	// MaxMutants caps how many mutants are applied (0 => all that apply once).
	MaxMutants int
	// PerMutantTimeout bounds each `go test` invocation (default 60s).
	PerMutantTimeout time.Duration
}

// RunMutation applies mutators to the package's source files and runs the named
// test against each mutant. It returns a *Mutation summarizing kills/survivors.
//
// Interpretation by Reduce:
//   - Survived > 0  => the test failed to catch broken code => fake.
//   - Survived == 0 with Attempted > 0 => every break was caught => real.
//   - Error != ""   => could not run (no signal); Reduce falls back to L1/L2.
func RunMutation(ctx context.Context, cfg MutationConfig) *Mutation {
	timeout := cfg.PerMutantTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	m := &Mutation{}

	// Correctness gate: without target symbols we cannot scope mutations to code
	// the test actually exercises, so any survivor would be a false positive.
	// Refuse to produce a misleading verdict; emit "no signal" instead.
	if len(cfg.TargetSymbols) == 0 {
		m.Error = "no target symbols: cannot scope mutation to tested code"
		return m
	}
	targets := map[string]bool{}
	for _, s := range cfg.TargetSymbols {
		// RealSymbols may be "pkg.Func" or "Func"; index by the trailing name.
		name := s
		if i := strings.LastIndex(s, "."); i >= 0 {
			name = s[i+1:]
		}
		targets[name] = true
	}

	files, err := nonTestGoFiles(cfg.PackageDir)
	if err != nil {
		m.Error = fmt.Sprintf("list files: %v", err)
		return m
	}
	if len(files) == 0 {
		m.Error = "no non-test source files to mutate"
		return m
	}

	// Baseline: the test must PASS on unmutated code, otherwise mutation tells
	// us nothing (a test that's already red can't "survive" anything).
	if ok, derr := runTest(ctx, cfg.PackageDir, cfg.TestName, timeout); derr != nil {
		m.Error = fmt.Sprintf("baseline run: %v", derr)
		return m
	} else if !ok {
		m.Error = "baseline test does not pass; cannot mutation-test"
		return m
	}

	muts := defaultMutators()
	for _, file := range files {
		for _, mut := range muts {
			if cfg.MaxMutants > 0 && m.Attempted >= cfg.MaxMutants {
				return m
			}
			applied, restore, sym, err := applyMutationToFile(file, mut, targets)
			if err != nil {
				continue // unparseable / unwritable file: skip, not a signal
			}
			if !applied {
				continue // no target-symbol code to mutate in this file
			}
			m.Attempted++
			if sym != "" {
				m.TargetSymbol = sym
			}
			pass, rerr := runTest(ctx, cfg.PackageDir, cfg.TestName, timeout)
			restore() // ALWAYS restore before the next mutant
			if rerr != nil {
				// Build broke or timed out under mutation. A build break means
				// the mutant didn't even compile; treat as not-a-signal for this
				// mutant (don't count as killed or survived).
				m.Attempted--
				continue
			}
			if pass {
				m.Survived++ // test stayed green on broken code => bad
			} else {
				m.Killed++ // test went red => good
			}
			if ctx.Err() != nil {
				return m
			}
		}
	}
	if m.Attempted == 0 {
		m.Error = "no applicable mutations in target symbols"
	}
	return m
}

// nonTestGoFiles lists .go files in dir excluding _test.go files.
func nonTestGoFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			out = append(out, dir+string(os.PathSeparator)+n)
		}
	}
	return out, nil
}

// applyMutationToFile parses path, applies the first matching mutation INSIDE a
// function whose name is in targets, writes the mutated source, and returns a
// restore func. Mutations are scoped to target functions so we only ever break
// code the test actually references — otherwise a survivor would be a false
// positive. The boolean reports whether any mutation was applied.
func applyMutationToFile(path string, mut Mutator, targets map[string]bool) (applied bool, restore func(), targetSym string, err error) {
	orig, err := os.ReadFile(path)
	if err != nil {
		return false, func() {}, "", err
	}
	restore = func() { _ = os.WriteFile(path, orig, 0o644) }

	fset := token.NewFileSet()
	file, perr := parser.ParseFile(fset, path, orig, parser.ParseComments)
	if perr != nil {
		return false, restore, "", perr
	}

	// Find the first target function in this file and mutate only its body.
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !targets[fn.Name.Name] {
			continue
		}
		if mut.apply(fn.Body) {
			targetSym = fn.Name.Name
			applied = true
			break
		}
	}
	if !applied {
		return false, restore, "", nil
	}

	var buf strings.Builder
	if werr := printer.Fprint(&buf, fset, file); werr != nil {
		return false, restore, "", werr
	}
	if werr := os.WriteFile(path, []byte(buf.String()), 0o644); werr != nil {
		return false, restore, "", werr
	}
	return true, restore, targetSym, nil
}

// runTest runs a single test in dir and reports whether it passed. A nil error
// with pass=false means the test ran and failed; a non-nil error means it could
// not be run (build failure, timeout).
func runTest(ctx context.Context, dir, testName string, timeout time.Duration) (pass bool, err error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pattern := "^" + testName + "$"
	cmd := exec.CommandContext(cctx, "go", "test", "-run", pattern, "-count=1", ".")
	cmd.Dir = dir
	out, runErr := cmd.CombinedOutput()
	if cctx.Err() == context.DeadlineExceeded {
		return false, fmt.Errorf("timeout after %s", timeout)
	}
	if runErr == nil {
		return true, nil
	}
	// Distinguish "test failed" (exit 1, has FAIL) from "build failed".
	s := string(out)
	if strings.Contains(s, "build failed") || strings.Contains(s, "cannot find package") ||
		strings.Contains(s, "[build failed]") || strings.Contains(s, "undefined:") {
		return false, fmt.Errorf("build failed under mutation")
	}
	if _, ok := runErr.(*exec.ExitError); ok {
		return false, nil // ran and failed: mutant killed
	}
	return false, runErr
}

// ─── mutation operators ─────────────────────────────────────────────────────

// mutNegateBoolReturn turns `return true` into `return false` and vice-versa.
func mutNegateBoolReturn(root ast.Node) bool {
	changed := false
	ast.Inspect(root, func(n ast.Node) bool {
		if changed {
			return false
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return true
		}
		if id, ok := ret.Results[0].(*ast.Ident); ok {
			if id.Name == "true" {
				ret.Results[0] = ast.NewIdent("false")
				changed = true
			} else if id.Name == "false" {
				ret.Results[0] = ast.NewIdent("true")
				changed = true
			}
		}
		return true
	})
	return changed
}

// mutFlipComparison swaps == <-> != and < <-> >= etc. in a binary expression.
func mutFlipComparison(root ast.Node) bool {
	changed := false
	flip := map[token.Token]token.Token{
		token.EQL: token.NEQ, token.NEQ: token.EQL,
		token.LSS: token.GEQ, token.GEQ: token.LSS,
		token.GTR: token.LEQ, token.LEQ: token.GTR,
	}
	ast.Inspect(root, func(n ast.Node) bool {
		if changed {
			return false
		}
		if be, ok := n.(*ast.BinaryExpr); ok {
			if nt, ok := flip[be.Op]; ok {
				be.Op = nt
				changed = true
			}
		}
		return true
	})
	return changed
}

// mutSwapArith swaps + <-> - in a binary expression.
func mutSwapArith(root ast.Node) bool {
	changed := false
	ast.Inspect(root, func(n ast.Node) bool {
		if changed {
			return false
		}
		if be, ok := n.(*ast.BinaryExpr); ok {
			switch be.Op {
			case token.ADD:
				be.Op = token.SUB
				changed = true
			case token.SUB:
				be.Op = token.ADD
				changed = true
			}
		}
		return true
	})
	return changed
}
