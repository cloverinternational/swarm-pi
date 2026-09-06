package judge

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// analyzer.go implements Layer 1: pure static analysis over the Go AST.
//
// Every signal here is a mechanical fact derived from the source. No model is
// consulted. The output (a Deterministic per test) is the cheap first filter
// that decides which tests are worth the expense of Layers 2 (LLM) and 3
// (mutation), and it provides disqualifiers that Reduce treats as fake on their
// own when no mutation signal is available.

// AnalyzedTest pairs a test's deterministic signals with the location and body
// needed by later layers (the LLM judge wants the body; mutation wants the
// referenced real symbols).
type AnalyzedTest struct {
	Package       string
	File          string
	Line          int
	TestName      string
	Signals       Deterministic
	Body          string   // source text of the function body, for the LLM layer
	RealSymbols   []string // identifiers referenced from the package under test
	allowSkipNote string
}

// allowSkipRe matches an audited skip directive, e.g.
//
//	// judge:allow-skip needs a live OpenAI key
var allowSkipRe = regexp.MustCompile(`//\s*judge:allow-skip\s+(.+)`)

// AnalyzePackageDir parses every *_test.go file in dir (non-recursive) and
// returns one AnalyzedTest per function. Non-test functions (helpers, TestMain,
// contract factories) are included with Signals.IsTestFunc == false so the
// caller can record them as helpers rather than silently dropping them.
//
// pkgIdents is the set of exported identifiers belonging to the non-test
// package under test; it is used to decide TouchesRealCode. When empty, the
// heuristic falls back to "references any selector that is not a mock/stdlib".
func AnalyzePackageDir(dir string, pkgIdents map[string]bool) ([]AnalyzedTest, error) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []AnalyzedTest
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		pkgName := file.Name.Name
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			at := analyzeFunc(fset, fn, src, pkgName, path, pkgIdents)
			out = append(out, at)
		}
	}
	return out, nil
}

// analyzeFunc extracts deterministic signals for a single function declaration.
func analyzeFunc(fset *token.FileSet, fn *ast.FuncDecl, src []byte, pkg, path string, pkgIdents map[string]bool) AnalyzedTest {
	pos := fset.Position(fn.Pos())
	at := AnalyzedTest{
		Package:  pkg,
		File:     path,
		Line:     pos.Line,
		TestName: fn.Name.Name,
	}

	isTest := isTestFunc(fn)
	at.Signals.IsTestFunc = isTest
	at.Body = bodyText(src, fset, fn)

	if !isTest {
		// Helpers still get a body slice (cheap) but no scoring.
		return at
	}

	// Empty body: no statements at all.
	at.Signals.EmptyBody = len(fn.Body.List) == 0

	// Audited skip directive in the doc/inline comments of the function.
	if note := findAllowSkip(src, fset, fn); note != "" {
		at.Signals.AllowSkipReason = note
		at.allowSkipNote = note
	}

	v := &bodyVisitor{pkgIdents: pkgIdents}
	ast.Inspect(fn.Body, v.visit)

	at.Signals.Assertions = v.assertions
	at.Signals.Skipped = isUnconditionalSkip(fn.Body)
	at.Signals.Tautology = v.tautology
	at.Signals.TouchesRealCode = v.touchesReal
	at.Signals.ConcurrencyProbe = v.concurrency
	at.Signals.DelegatesAssertion = v.delegatesT
	// Mock-only: uses mocks, asserts, but never touches the real package.
	at.Signals.MockOnly = v.usesMock && !v.touchesReal
	at.RealSymbols = v.realSymbols

	return at
}

// isTestFunc reports whether fn is a `func TestXxx(t *testing.T)` (Go's own
// rule: name starts with Test, single *testing.T parameter, no results). This
// deliberately excludes TestMain, Benchmark*, Fuzz*, Example*, and helpers.
func isTestFunc(fn *ast.FuncDecl) bool {
	name := fn.Name.Name
	if !strings.HasPrefix(name, "Test") || name == "TestMain" {
		return false
	}
	// Disallow exported helpers like "Test" exactly or "Testing" with no t arg.
	if fn.Recv != nil {
		return false
	}
	if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		return false
	}
	star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	return ok && pkgIdent.Name == "testing" && sel.Sel.Name == "T"
}

// bodyVisitor accumulates signals while walking a function body.
type bodyVisitor struct {
	pkgIdents   map[string]bool
	assertions  int
	usesMock    bool
	tautology   bool
	touchesReal bool
	concurrency bool
	delegatesT  bool
	realSymbols []string
	seenReal    map[string]bool
}

var mockNameRe = regexp.MustCompile(`(?i)(mock|fake|stub|spy|dummy)`)

func (v *bodyVisitor) visit(n ast.Node) bool {
	// Goroutine launch is a strong concurrency-probe signal.
	if _, ok := n.(*ast.GoStmt); ok {
		v.concurrency = true
		return true
	}
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return true
	}

	// Assertion delegation: a call that passes the *testing.T (named "t") to
	// another function means the assertions likely live in that helper
	// (e.g. ContractTest_All(t, factory), runSuite(t, ...)). Such tests are not
	// "no-assertion" fakes — they delegate verification.
	for _, arg := range call.Args {
		if id, ok := arg.(*ast.Ident); ok && id.Name == "t" {
			v.delegatesT = true
		}
	}

	// Direct, unqualified calls to real package symbols: Add(...), NewFoo(...).
	// In an in-package (white-box) test the code under test is called by bare
	// name, so this is the common real-code signal.
	if ident, ok := call.Fun.(*ast.Ident); ok {
		if mockNameRe.MatchString(ident.Name) {
			v.usesMock = true
		}
		if v.pkgIdents != nil && v.pkgIdents[ident.Name] {
			v.markReal(ident.Name)
		}
		return true
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return true
	}
	recv := exprName(sel.X)
	method := sel.Sel.Name

	// Assertion sites.
	switch {
	case recv == "t" && (method == "Error" || method == "Errorf" || method == "Fatal" || method == "Fatalf"):
		v.assertions++
	case recv == "require" || recv == "assert":
		v.assertions++
		v.checkTautology(call)
	}

	// Mock vs real-code usage. A selector whose receiver name looks like a mock
	// counts as mock usage; one whose receiver is a known package identifier (or
	// constructs a known real symbol) counts as touching real code.
	if mockNameRe.MatchString(recv) {
		v.usesMock = true
	}
	if v.pkgIdents != nil {
		// recv could be the package qualifier (e.g. conductor.NewOrchestrator).
		if v.pkgIdents[method] || v.pkgIdents[recv] {
			v.markReal(recv + "." + method)
		}
	}
	return true
}

func (v *bodyVisitor) markReal(sym string) {
	v.touchesReal = true
	if v.seenReal == nil {
		v.seenReal = map[string]bool{}
	}
	if !v.seenReal[sym] {
		v.seenReal[sym] = true
		v.realSymbols = append(v.realSymbols, sym)
	}
}

// checkTautology flags assert/require calls whose two compared operands are both
// basic literals (e.g. assert.Equal(t, 1, 1)) or syntactically identical.
func (v *bodyVisitor) checkTautology(call *ast.CallExpr) {
	if len(call.Args) < 3 {
		return
	}
	a, b := call.Args[1], call.Args[2]
	_, aLit := a.(*ast.BasicLit)
	_, bLit := b.(*ast.BasicLit)
	if aLit && bLit {
		v.tautology = true
		return
	}
	if exprName(a) != "" && exprName(a) == exprName(b) {
		v.tautology = true
	}
}

// exprName returns a stable string for simple expressions (idents and
// selectors), used for receiver matching and identity comparison.
func exprName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprName(x.X) + "." + x.Sel.Name
	}
	return ""
}

// bodyText returns the source slice covering the function body braces.
func bodyText(src []byte, fset *token.FileSet, fn *ast.FuncDecl) string {
	start := fset.Position(fn.Body.Lbrace).Offset
	end := fset.Position(fn.Body.Rbrace).Offset
	if start < 0 || end > len(src) || start >= end {
		return ""
	}
	return string(src[start : end+1])
}

// findAllowSkip scans comments attached to (or immediately inside) the function
// for a `// judge:allow-skip <reason>` directive and returns the reason.
func findAllowSkip(src []byte, fset *token.FileSet, fn *ast.FuncDecl) string {
	if fn.Doc != nil {
		for _, c := range fn.Doc.List {
			if m := allowSkipRe.FindStringSubmatch(c.Text); m != nil {
				return strings.TrimSpace(m[1])
			}
		}
	}
	// Also scan the body source text (inline directive next to t.Skip).
	if m := allowSkipRe.FindStringSubmatch(bodyText(src, fset, fn)); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// isUnconditionalSkip reports whether the function body contains a t.Skip
// call that is NOT inside an if/switch/case block. Only truly unconditional
// skips (the first statement, or any unguarded skip) are flagged. Conditional
// skips like `if testing.Short() { t.Skip(...) }` or `if env == "" { t.Skip(...) }`
// are legitimate gating patterns and are NOT flagged.
func isUnconditionalSkip(body *ast.BlockStmt) bool {
	// First, collect all if/switch/select body ranges.
	var ranges []struct{ pos, end token.Pos }
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.IfStmt:
			ranges = append(ranges, struct{ pos, end token.Pos }{x.Body.Pos(), x.Body.End()})
		case *ast.SwitchStmt:
			ranges = append(ranges, struct{ pos, end token.Pos }{x.Body.Pos(), x.Body.End()})
		case *ast.TypeSwitchStmt:
			ranges = append(ranges, struct{ pos, end token.Pos }{x.Body.Pos(), x.Body.End()})
		case *ast.SelectStmt:
			ranges = append(ranges, struct{ pos, end token.Pos }{x.Body.Pos(), x.Body.End()})
		}
		return true
	})

	uncond := false
	ast.Inspect(body, func(n ast.Node) bool {
		if uncond {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if exprName(sel.X) != "t" {
			return true
		}
		method := sel.Sel.Name
		if method != "Skip" && method != "Skipf" && method != "SkipNow" {
			return true
		}
		// Check if the skip call is inside any if/switch/select body.
		skipPos := n.Pos()
		inside := false
		for _, r := range ranges {
			if skipPos >= r.pos && skipPos <= r.end {
				inside = true
				break
			}
		}
		if !inside {
			uncond = true
		}
		return true
	})
	return uncond
}
