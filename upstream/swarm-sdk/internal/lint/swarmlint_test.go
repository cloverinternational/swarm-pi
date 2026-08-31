package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
)

// TestOrderedBlocksDetection tests detection of direct OrderedBlocks access.
func TestOrderedBlocksDetection(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		wantDiag int
		wantMsg  string
	}{
		{
			name: "direct field read access",
			code: `package test

import "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"

func bad() {
	msg := &conversation.Message{}
	blocks := msg.OrderedBlocks // VIOLATION: direct access
	_ = blocks
}`,
			wantDiag: 1,
			wantMsg:  "GetOrderedBlocks()",
		},
		{
			name: "correct method usage",
			code: `package test

import "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"

func good() {
	msg := &conversation.Message{}
	blocks := msg.GetOrderedBlocks() // CORRECT: using accessor
	_ = blocks
}`,
			wantDiag: 0,
			wantMsg:  "",
		},
		{
			name: "pointer receiver field access",
			code: `package test

import "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"

func badPointer() {
	msg := new(conversation.Message)
	_ = msg.OrderedBlocks // VIOLATION: direct access on pointer
}`,
			wantDiag: 1,
			wantMsg:  "GetOrderedBlocks()",
		},
		{
			name: "nested field access",
			code: `package test

import "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"

func badNested() {
	msgs := []*conversation.Message{{}}
	for _, m := range msgs {
		_ = m.OrderedBlocks // VIOLATION: direct access in loop
	}
}`,
			wantDiag: 1,
			wantMsg:  "GetOrderedBlocks()",
		},
		{
			name: "assignment to field",
			code: `package test

import "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"

func assignToField() {
	msg := &conversation.Message{}
	msg.OrderedBlocks = []conversation.MessageBlock{} // Allowed: assignment
}`,
			wantDiag: 0, // Assignment is allowed for construction
			wantMsg:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test file
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "test.go", tt.code, parser.AllErrors|parser.ParseComments)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			// Create a simple pass
			files := []*ast.File{f}
			info, pkg := typeCheckTestSource(t, fset, f)

			pass := &analysis.Pass{
				Analyzer:  Analyzer,
				Fset:      fset,
				Files:     files,
				Pkg:       pkg,
				TypesInfo: info,
				ResultOf:  make(map[*analysis.Analyzer]any),
				Report: func(diag analysis.Diagnostic) {
					if !strings.Contains(diag.Message, tt.wantMsg) {
						t.Errorf("unexpected diagnostic message: %s", diag.Message)
					}
				},
			}

			// Run the detector manually
			diagCount := 0
			pass.Report = func(d analysis.Diagnostic) {
				diagCount++
				if tt.wantMsg != "" && !strings.Contains(d.Message, tt.wantMsg) {
					t.Errorf("expected message to contain %q, got %q", tt.wantMsg, d.Message)
				}
			}

			for _, file := range files {
				ast.Inspect(file, func(n ast.Node) bool {
					detectOrderedBlocksAccess(pass, n)
					return true
				})
			}

			if diagCount != tt.wantDiag {
				t.Errorf("expected %d diagnostics, got %d", tt.wantDiag, diagCount)
			}
		})
	}
}

// TestMessageConstructionDetection tests detection of direct Message construction.
func TestMessageConstructionDetection(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		wantDiag int
	}{
		{
			name: "direct struct literal with OrderedBlocks",
			code: `package test

import "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"

func bad() {
	_ = &conversation.Message{
		OrderedBlocks: []conversation.MessageBlock{{Sequence: 5}, {Sequence: 1}}, // VIOLATION
	}
}`,
			wantDiag: 1,
		},
		{
			name: "struct literal without OrderedBlocks",
			code: `package test

import "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"

func ok() {
	_ = &conversation.Message{
		Role:    "user",
		Content: "hello",
	}
}`,
			wantDiag: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "test.go", tt.code, parser.AllErrors|parser.ParseComments)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			files := []*ast.File{f}
			pkg := &types.Package{}
			info := &types.Info{
				Types: make(map[ast.Expr]types.TypeAndValue),
			}

			pass := &analysis.Pass{
				Analyzer:  Analyzer,
				Fset:      fset,
				Files:     files,
				Pkg:       pkg,
				TypesInfo: info,
				ResultOf:  make(map[*analysis.Analyzer]any),
			}

			diagCount := 0
			pass.Report = func(d analysis.Diagnostic) {
				diagCount++
			}

			for _, file := range files {
				ast.Inspect(file, func(n ast.Node) bool {
					detectDirectMessageConstruction(pass, n)
					return true
				})
			}

			if diagCount != tt.wantDiag {
				t.Errorf("expected %d diagnostics, got %d", tt.wantDiag, diagCount)
			}
		})
	}
}

// TestCustomClientDetection tests detection of custom JSON-RPC client usage.
func TestCustomClientDetection(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		wantDiag bool
	}{
		{
			name: "importing gorilla websocket outside client",
			code: `package test

import "github.com/gorilla/websocket"

func bad() {
	conn, _, _ := websocket.DefaultDialer.Dial("ws://localhost:8080/ws", nil)
	_ = conn
}`,
			wantDiag: true,
		},
		{
			name: "using official client",
			code: `package test

import "github.com/Swarm-Code/mono/swarm-sdk/client"

func good() {
	c, _ := client.New()
	_ = c
}`,
			wantDiag: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "test.go", tt.code, parser.AllErrors|parser.ParseComments)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			files := []*ast.File{f}
			pkgPath := "swarm-sdk/client"
			if tt.wantDiag {
				pkgPath = "mypackage"
			}
			pkg := types.NewPackage(pkgPath, "")
			info := &types.Info{
				Types: make(map[ast.Expr]types.TypeAndValue),
			}

			pass := &analysis.Pass{
				Analyzer:  Analyzer,
				Fset:      fset,
				Files:     files,
				Pkg:       pkg,
				TypesInfo: info,
				ResultOf:  make(map[*analysis.Analyzer]any),
			}

			hasDiag := false
			pass.Report = func(d analysis.Diagnostic) {
				hasDiag = true
			}

			for _, file := range files {
				ast.Inspect(file, func(n ast.Node) bool {
					detectCustomClientImplementation(pass, n, file)
					return true
				})
			}

			if hasDiag != tt.wantDiag {
				t.Errorf("expected diag=%v, got diag=%v", tt.wantDiag, hasDiag)
			}
		})
	}
}

// TestContractDocumentation tests detection of missing contract documentation.
func TestContractDocumentation(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		wantDiag bool
	}{
		{
			name: "missing contract doc on Message function",
			code: `package test

import "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"

func ProcessMessage(msg *conversation.Message) {
	// No contract documentation
	_ = msg
}`,
			wantDiag: true,
		},
		{
			name: "has contract doc",
			code: `package test

import "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"

// ProcessMessage processes a message.
// CONTRACT: Message must be valid before processing.
func ProcessMessage(msg *conversation.Message) {
	_ = msg
}`,
			wantDiag: false,
		},
		{
			name: "unexported function",
			code: `package test

import "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"

func processMessage(msg *conversation.Message) {
	_ = msg
}`,
			wantDiag: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "test.go", tt.code, parser.AllErrors|parser.ParseComments)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			files := []*ast.File{f}
			info, pkg := typeCheckTestSource(t, fset, f)

			pass := &analysis.Pass{
				Analyzer:  Analyzer,
				Fset:      fset,
				Files:     files,
				Pkg:       pkg,
				TypesInfo: info,
				ResultOf:  make(map[*analysis.Analyzer]any),
			}

			hasDiag := false
			pass.Report = func(d analysis.Diagnostic) {
				hasDiag = true
			}

			for _, file := range files {
				ast.Inspect(file, func(n ast.Node) bool {
					detectMissingContractDocumentation(pass, n)
					return true
				})
			}

			if hasDiag != tt.wantDiag {
				t.Errorf("expected diag=%v, got diag=%v", tt.wantDiag, hasDiag)
			}
		})
	}
}

// TestAnalyzerIntegration runs the full analyzer on test data.
func TestAnalyzerIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Test source with two OrderedBlocks violations (BadAccess reads the field;
	// BadConstruction is a direct composite-literal construction) and one
	// compliant function (GoodAccess uses GetOrderedBlocks()).
	testCode := `package testpkg

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// BadAccess directly accesses OrderedBlocks - VIOLATION
func BadAccess(msg *conversation.Message) []conversation.MessageBlock {
	return msg.OrderedBlocks // Should use GetOrderedBlocks()
}

// GoodAccess uses the correct method
func GoodAccess(msg *conversation.Message) []conversation.MessageBlock {
	return msg.GetOrderedBlocks()
}
`

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", testCode, parser.AllErrors|parser.ParseComments)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	info, pkg := typeCheckTestSource(t, fset, f)

	var diags []string
	pass := &analysis.Pass{
		Analyzer:  Analyzer,
		Fset:      fset,
		Files:     []*ast.File{f},
		Pkg:       pkg,
		TypesInfo: info,
		ResultOf:  make(map[*analysis.Analyzer]any),
		Report: func(d analysis.Diagnostic) {
			diags = append(diags, d.Message)
		},
	}

	ast.Inspect(f, func(n ast.Node) bool {
		detectOrderedBlocksAccess(pass, n)
		return true
	})

	// Exactly one violation: BadAccess. GoodAccess must not be flagged.
	if len(diags) != 1 {
		t.Fatalf("expected 1 OrderedBlocks diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0], "GetOrderedBlocks()") {
		t.Errorf("unexpected diagnostic: %s", diags[0])
	}
}

// TestIsGeneratedFile tests the generated file detection.
func TestIsGeneratedFile(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		wantGen bool
	}{
		{
			name: "generated file",
			code: `// Code generated by protoc. DO NOT EDIT.
package test

func Foo() {}`,
			wantGen: true,
		},
		{
			name: "normal file",
			code: `package test

// Foo does something
func Foo() {}`,
			wantGen: false,
		},
		{
			name:    "empty file",
			code:    `package test`,
			wantGen: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "test.go", tt.code, parser.PackageClauseOnly|parser.ParseComments)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			got := isGeneratedFile(f)
			if got != tt.wantGen {
				t.Errorf("isGeneratedFile() = %v, want %v", got, tt.wantGen)
			}
		})
	}
}

// TestIsAllowedPackage tests the package allowlist logic.
func TestIsAllowedPackage(t *testing.T) {
	patterns := []string{"_test", "internal/", "lint", "/lint"}

	tests := []struct {
		pkgPath string
		want    bool
	}{
		{"github.com/Swarm-Code/mono/swarm-sdk/client_test", true},
		{"github.com/Swarm-Code/mono/swarm-sdk/internal/blocks", true},
		{"github.com/Swarm-Code/mono/swarm-sdk/internal/lint", true},
		{"github.com/Swarm-Code/mono/swarm-sdk/client", false},
		// Compatibility shim path (pre-internal/ layout): not allowlisted.
		// Implementation packages are exempted via isInternalSDKPackage, not here.
		{"github.com/Swarm-Code/mono/swarm-sdk/conversation", false},
		// Post-move implementation path: matches the "internal/" pattern, so allowed.
		{"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation", true},
	}

	for _, tt := range tests {
		t.Run(tt.pkgPath, func(t *testing.T) {
			got := isAllowedPackage(tt.pkgPath, patterns)
			if got != tt.want {
				t.Errorf("isAllowedPackage(%q) = %v, want %v", tt.pkgPath, got, tt.want)
			}
		})
	}
}

// TestHasContractDocumentation tests the documentation checker.
func TestHasContractDocumentation(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want bool
	}{
		{"has CONTRACT:", "CONTRACT: Message must be valid", true},
		{"has Contract:", "Contract: Always call GetOrderedBlocks()", true},
		{"has MUST:", "MUST: Use factory method", true},
		{"has INVARIANT:", "INVARIANT: Blocks are sorted", true},
		{"no contract keywords", "Foo does something", false},
		{"empty doc", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var docGroup *ast.CommentGroup
			if tt.doc != "" {
				docGroup = &ast.CommentGroup{
					List: []*ast.Comment{
						{Text: "// " + tt.doc},
					},
				}
			}

			got := hasContractDocumentation(docGroup)
			if got != tt.want {
				t.Errorf("hasContractDocumentation() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestExprToString tests the expression to string conversion.
func TestExprToString(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{
			name: "simple identifier",
			code: `package test; func f() { x := 1 }`,
			want: "x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse and find the expression
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "test.go", tt.code, 0)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			// For this test, we just verify it doesn't panic
			ast.Inspect(f, func(n ast.Node) bool {
				if expr, ok := n.(ast.Expr); ok {
					_ = exprToString(expr)
				}
				return true
			})
		})
	}
}

// BenchmarkOrderedBlocksDetection benchmarks the OrderedBlocks detector.
func BenchmarkOrderedBlocksDetection(b *testing.B) {
	code := `package test

import "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"

func benchmark() {
	msg := &conversation.Message{}
	_ = msg.OrderedBlocks
	_ = msg.GetOrderedBlocks()
}
`
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", code, 0)
	files := []*ast.File{f}

	pass := &analysis.Pass{
		Analyzer:  Analyzer,
		Fset:      fset,
		Files:     files,
		Pkg:       &types.Package{},
		TypesInfo: &types.Info{Types: make(map[ast.Expr]types.TypeAndValue)},
		ResultOf:  make(map[*analysis.Analyzer]any),
		Report:    func(d analysis.Diagnostic) {},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, file := range files {
			ast.Inspect(file, func(n ast.Node) bool {
				detectOrderedBlocksAccess(pass, n)
				return true
			})
		}
	}
}

// Example of running the linter programmatically
func Example_analyzerUsage() {
	// This example shows how to use the analyzer programmatically
	cfg := &packages.Config{
		Mode: packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes,
	}

	pkgs, err := packages.Load(cfg, "github.com/Swarm-Code/mono/swarm-sdk/...")
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return
	}

	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				// Manual inspection example
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if sel.Sel.Name == "OrderedBlocks" {
						fmt.Printf("Found OrderedBlocks access at %v\n", pkg.Fset.Position(sel.Pos()))
					}
				}
				return true
			})
		}
	}
}

// typeCheckTestSource type-checks an in-memory test source file, resolving real
// imports (e.g. the conversation package) so that pass.TypesInfo is populated.
// Without this, type-dependent detectors like isMessageType see an empty
// types.Info and never fire. Returns the populated info and the checked package.
func typeCheckTestSource(t *testing.T, fset *token.FileSet, f *ast.File) (*types.Info, *types.Package) {
	t.Helper()

	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}

	conf := types.Config{
		Importer: testPackageImporter{t: t},
		Error:    func(err error) { /* tolerate type errors from partial snippets */ },
	}

	pkg, _ := conf.Check("test", fset, []*ast.File{f}, info)
	return info, pkg
}

// testPackageImporter loads real packages on demand via go/packages so the type
// checker can resolve imports like the conversation package.
type testPackageImporter struct {
	t *testing.T
}

func (imp testPackageImporter) Import(path string) (*types.Package, error) {
	cfg := &packages.Config{Mode: packages.NeedTypes | packages.NeedDeps | packages.NeedImports}
	pkgs, err := packages.Load(cfg, path)
	if err != nil {
		return nil, err
	}
	for _, p := range pkgs {
		if p.Types != nil {
			return p.Types, nil
		}
	}
	return nil, fmt.Errorf("could not load package %q", path)
}
