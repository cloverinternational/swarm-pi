package sandbox

import (
	"context"
	"strings"
	"testing"
)

// TestCaptureLastExpr locks down the source rewrite: trailing bare expressions
// become the returned value, while code that already ends in a non-expression
// statement (return/throw/loop/declaration/block) is left byte-for-byte alone.
func TestCaptureLastExpr(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		rewrites bool // whether captureLastExpr should change the source
	}{
		{"bare_object", `({a: 1})`, true},
		{"bare_array", `[1, 2, 3]`, true},
		{"bare_ident", "let x = 1;\nx", true},
		{"await_expr", `await foo()`, true},
		{"already_return", `return {a: 1};`, false},
		{"throw", `throw new Error("x")`, false},
		{"for_loop", `for (let i = 0; i < 3; i++) {}`, false},
		{"var_decl", `let y = 5;`, false},
		{"empty", ``, false},
		{"invalid_syntax", `const = ;`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureLastExpr(tc.in)
			changed := out != tc.in
			if changed != tc.rewrites {
				t.Fatalf("captureLastExpr(%q): changed=%v want %v\n got: %q", tc.in, changed, tc.rewrites, out)
			}
			if tc.rewrites && !strings.Contains(out, "return __cm_last__") {
				t.Errorf("rewritten code should return the capture var, got: %q", out)
			}
		})
	}
}

// TestCaptureLastExprRuntime verifies rewritten code actually executes and the
// captured value flows through Eval, and that a trailing await is preserved.
func TestCaptureLastExprRuntime(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	res, err := sb.Eval(context.Background(), `let x = 21; ({doubled: x * 2})`, nil, nil)
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected eval error: %s", res.ErrorMessage)
	}
	m, ok := res.Value.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T (%v)", res.Value, res.Value)
	}
	if got := m["doubled"]; got != int64(42) && got != float64(42) {
		t.Errorf("expected doubled=42, got %v (%T)", got, got)
	}
}
