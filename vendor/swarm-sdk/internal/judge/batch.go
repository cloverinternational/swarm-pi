package judge

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// batch.go orchestrates the three layers over one or more package directories
// and reduces every test to a JudgeRecord. It also implements the baseline
// allowlist diff so a hard gate can be introduced without failing on the
// pre-existing backlog of fake tests.

// Options controls a batch run.
type Options struct {
	// Judge is the Layer 2 implementation. Use NoopJudge{} to skip the LLM.
	Judge Judge
	// Mutate enables Layer 3 mutation testing for suspicious tests.
	Mutate bool
	// PerMutantTimeoutSeconds bounds each mutant's `go test` (0 => default).
	PerMutantTimeoutSeconds int
	// MaxMutantsPerTest caps mutants applied per suspicious test (0 => all).
	MaxMutantsPerTest int
}

// RunPackage analyzes a single package directory and returns one JudgeRecord per
// function (helpers included, classified as VerdictHelper). It applies Layer 1
// to everything, then Layers 2 and 3 only to suspicious tests (to bound cost).
func RunPackage(ctx context.Context, dir string, opts Options) ([]JudgeRecord, error) {
	pkgIdents, targetSrc, err := collectPackageSymbols(dir)
	if err != nil {
		return nil, err
	}
	tests, err := AnalyzePackageDir(dir, pkgIdents)
	if err != nil {
		return nil, err
	}

	records := make([]JudgeRecord, 0, len(tests))
	for _, at := range tests {
		rec := JudgeRecord{
			Package:       at.Package,
			File:          at.File,
			Line:          at.Line,
			TestName:      at.TestName,
			Deterministic: at.Signals,
		}

		if at.Signals.IsTestFunc && at.Signals.Suspicious() {
			// Layer 2: LLM judgement (best-effort; nil-safe).
			if opts.Judge != nil {
				if ctx.Err() != nil {
					return records, ctx.Err()
				}
				rec.LLM = opts.Judge.Evaluate(ctx, at, targetSrc)
			}
			// Layer 3: mutation — the empirical guarantee.
			if opts.Mutate {
				if ctx.Err() != nil {
					return records, ctx.Err()
				}
				abs, _ := filepath.Abs(dir)
				rec.Mutation = RunMutation(ctx, MutationConfig{
					PackageDir:       abs,
					TestName:         at.TestName,
					TargetSymbols:    at.RealSymbols,
					MaxMutants:       opts.MaxMutantsPerTest,
					PerMutantTimeout: time.Duration(opts.PerMutantTimeoutSeconds) * time.Second,
				})
			}
		}

		Reduce(&rec)
		records = append(records, rec)
	}
	return records, nil
}

// RunTree walks root recursively and runs RunPackage on every directory that
// contains at least one _test.go file. Vendor and hidden dirs are skipped.
func RunTree(ctx context.Context, root string, opts Options) ([]JudgeRecord, error) {
	dirs, err := testDirs(root)
	if err != nil {
		return nil, err
	}
	var all []JudgeRecord
	for _, d := range dirs {
		if ctx.Err() != nil {
			return all, ctx.Err()
		}
		recs, err := RunPackage(ctx, d, opts)
		if err != nil {
			// One bad package should not abort the whole tree; record nothing
			// and continue. (Caller sees fewer records; we never fabricate.)
			continue
		}
		all = append(all, recs...)
	}
	return all, nil
}

// Summary aggregates verdict counts and the list of fake/weak tests.
type Summary struct {
	Total   int           `json:"total"`
	Real    int           `json:"real"`
	Weak    int           `json:"weak"`
	Fake    int           `json:"fake"`
	Helpers int           `json:"helpers"`
	Fakes   []JudgeRecord `json:"fakes"`
	Weaks   []JudgeRecord `json:"weaks"`
}

// Summarize tallies records into a Summary.
func Summarize(records []JudgeRecord) Summary {
	var s Summary
	for _, r := range records {
		s.Total++
		switch r.Verdict {
		case VerdictReal:
			s.Real++
		case VerdictWeak:
			s.Weak++
			s.Weaks = append(s.Weaks, r)
		case VerdictFake:
			s.Fake++
			s.Fakes = append(s.Fakes, r)
		case VerdictHelper:
			s.Helpers++
		}
	}
	return s
}

// ─── baseline allowlist ─────────────────────────────────────────────────────

// Baseline is the set of known-fake test keys ("pkg.TestName") that are
// grandfathered in, so a hard gate only fails on NEW or regressed fakes.
type Baseline struct {
	Fakes []string `json:"fakes"`
}

// Key is the stable identifier for a test across runs.
func Key(r JudgeRecord) string { return r.Package + "." + r.TestName }

// LoadBaseline reads a baseline file. A missing file yields an empty baseline.
func LoadBaseline(path string) (*Baseline, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Baseline{}, nil
	}
	if err != nil {
		return nil, err
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// SaveBaseline writes the current set of fake keys as the new baseline.
func SaveBaseline(path string, records []JudgeRecord) error {
	set := map[string]bool{}
	for _, r := range records {
		if r.Verdict == VerdictFake {
			set[Key(r)] = true
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	data, err := json.MarshalIndent(Baseline{Fakes: keys}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// NewFakes returns fake records whose key is NOT in the baseline. These are the
// regressions a hard gate should fail on.
func (b *Baseline) NewFakes(records []JudgeRecord) []JudgeRecord {
	known := map[string]bool{}
	for _, k := range b.Fakes {
		known[k] = true
	}
	var out []JudgeRecord
	for _, r := range records {
		if r.Verdict == VerdictFake && !known[Key(r)] {
			out = append(out, r)
		}
	}
	return out
}

// ─── helpers ────────────────────────────────────────────────────────────────

// collectPackageSymbols parses non-test files in dir and returns the set of
// top-level declared identifiers (funcs, types, vars, consts) plus the
// concatenated source (used as the LLM target excerpt).
func collectPackageSymbols(dir string) (map[string]bool, string, error) {
	idents := map[string]bool{}
	var src strings.Builder
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		src.Write(data)
		src.WriteByte('\n')
		file, perr := parser.ParseFile(fset, path, data, 0)
		if perr != nil {
			continue
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				idents[d.Name.Name] = true
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						idents[s.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range s.Names {
							idents[n.Name] = true
						}
					}
				}
			}
		}
	}
	return idents, src.String(), nil
}

// testDirs returns directories under root that contain at least one _test.go.
func testDirs(root string) ([]string, error) {
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || (strings.HasPrefix(name, ".") && name != "." && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), "_test.go") {
			seen[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}
