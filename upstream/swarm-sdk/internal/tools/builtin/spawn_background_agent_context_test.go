package builtin

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestSpawnBackgroundAgentToolStartsWithDetachedSubagentContext is a static
// regression test for issue #232: BackgroundTask (SpawnBackgroundAgentTool)
// launched a background worker with a context.Background() that discarded
// every value from the caller's request context (workspace, trace,
// permission-checker, owner, the agent.IsSubAgent marker steering hooks rely
// on) even though the sibling Subagent(run_in_background=true) path
// (subagent.go's detachedSubagentContext) got this right: strip
// cancellation/deadline via context.WithoutCancel while preserving values.
//
// This parses the REAL source of Run (the method Execute delegates to) and
// asserts, by AST inspection rather than a substring grep, that:
//  1. It calls detachedSubagentContext(...) to build the context handed to
//     bgAgent.Start.
//  2. It does NOT call the bare context.Background() anywhere in its body —
//     that call is exactly what silently drops the caller's context values.
func TestSpawnBackgroundAgentToolStartsWithDetachedSubagentContext(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to resolve this test file's own path")
	}
	dir := filepath.Dir(file)

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, filepath.Join(dir, "spawn_background_agent.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse spawn_background_agent.go: %v", err)
	}

	var run *ast.FuncDecl
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Run" || fn.Body == nil {
			continue
		}
		run = fn
		break
	}
	if run == nil {
		t.Fatal("could not find (*SpawnBackgroundAgentTool) Run in spawn_background_agent.go")
	}

	var callsDetached, callsBareContextBackground bool
	ast.Inspect(run.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if fn.Name == "detachedSubagentContext" {
				callsDetached = true
			}
		case *ast.SelectorExpr:
			if pkg, ok := fn.X.(*ast.Ident); ok && pkg.Name == "context" && fn.Sel.Name == "Background" {
				callsBareContextBackground = true
			}
		}
		return true
	})

	if !callsDetached {
		t.Error("Run no longer calls detachedSubagentContext(ctx) -- the background agent's " +
			"context must strip cancellation/deadline while preserving the caller's context values (issue #232)")
	}
	if callsBareContextBackground {
		t.Error("Run calls bare context.Background() -- this discards every value from the " +
			"caller's context (workspace, trace, permission-checker, owner, agent.IsSubAgent marker); " +
			"use detachedSubagentContext(ctx) instead (issue #232)")
	}
}

// TestDetachedSubagentContextUsedBySpawnAndSubagentAgree is a value-level
// companion to the static test above: it directly exercises the shared
// detachedSubagentContext helper with the exact shape spawn_background_agent.go
// now depends on (a parent carrying a context value AND an already-fired
// cancellation), proving the contract BackgroundTask now relies on:
//   - context values survive detachment (so hooks/tools reading them still work)
//   - the parent's cancellation does NOT propagate (so the background agent
//     is not killed within microseconds of Start() returning, issue #232's
//     reported "nested context deadline exceeded" within ~1ms)
//   - the detached context carries no deadline of its own
func TestDetachedSubagentContextUsedBySpawnAndSubagentAgree(t *testing.T) {
	type ctxKey string
	const key ctxKey = "owner_id"

	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key, "spawn-background-agent-owner"))
	cancel() // simulate the request context ending the instant Start() returns

	detached := detachedSubagentContext(parent)

	if got := detached.Value(key); got != "spawn-background-agent-owner" {
		t.Fatalf("detached context lost a caller-supplied value: got %v", got)
	}
	if err := detached.Err(); err != nil {
		t.Fatalf("detached context inherited the parent's cancellation: %v", err)
	}
	if _, hasDeadline := detached.Deadline(); hasDeadline {
		t.Fatal("detached context unexpectedly carries a deadline")
	}
	select {
	case <-detached.Done():
		t.Fatal("detached context's Done channel fired despite parent cancellation being stripped")
	case <-time.After(10 * time.Millisecond):
	}
}
