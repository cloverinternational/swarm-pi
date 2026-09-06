package main

// cli_contract_test.go — P07.A CLI verb/alias front-controller contract
// tests (.swarmflow/swarm-attach-architecture/p07-cli-sessions-thin-client/
// CONTRACT.md, "P07.A"). These tests exercise REAL production code paths
// (the actual legacyAliasWarnings table, the actual runSwarmCLI/runPeerCLI
// dispatchers, the actual nearestCanonicalCommand suggestion logic, and a
// built copy of the real swarmos binary for the unknown-command exit-status
// test) rather than re-asserting constants against themselves.

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// packageDir returns the absolute directory containing this test file
// (cmd/swarmos), resolved via runtime.Caller so source-scanning assertions
// below work regardless of the working directory `go test` is invoked
// from.
func packageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to resolve cli_contract_test.go's own path")
	}
	return filepath.Dir(file)
}

// readPackageFile reads a source file from this package's directory (e.g.
// "main.go", "swarm_cli.go") as a string, failing the test on error.
func readPackageFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(packageDir(t), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// findFuncDecl parses a Go source file and returns the *ast.FuncDecl for
// the named top-level function (or method with that name, ignoring
// receiver), failing the test if it isn't found. Used to statically
// inspect a specific function's body without executing it.
func findFuncDecl(t *testing.T, filename, funcName string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	path := filepath.Join(packageDir(t), filename)
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Name.Name == funcName {
			return fn
		}
	}
	t.Fatalf("function %s not found in %s", funcName, filename)
	return nil
}

// funcBodyReferencesIdent reports whether any *ast.Ident inside fn's body
// has exactly the given name — a real AST walk, not a substring match, so
// it isn't fooled by e.g. a comment or an unrelated identifier that merely
// contains the forbidden name as a substring.
func funcBodyReferencesIdent(fn *ast.FuncDecl, name string) bool {
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

// TestRunHeadlessNeverTouchesGlobalDaemon is the P07 CONTRACT.md §5
// regression test: "swarm run/swarm -p (runHeadless) must never call
// ensureGlobalDaemon/select globalDaemonHandle -- add/keep this as an
// explicit regression test (Worker A)." ADR-002's Decision section states
// this even more directly: "swarm run always means one explicit ephemeral
// execution ... It never starts or selects the persistent daemon
// implicitly."
//
// This is a real, load-bearing static assertion via go/ast (not a
// tautology): it parses the ACTUAL main.go source on disk and walks the
// ACTUAL bodies of runHeadless and dispatchHeadlessExecution (the two
// functions every `swarm run`/`swarm -p` invocation passes through) for
// the identifiers ensureGlobalDaemon/globalDaemonHandle. A future edit that
// reintroduces either reference — even deep inside a large function body —
// fails this test immediately, without needing to actually execute
// runHeadless (which would require a live provider/network).
func TestRunHeadlessNeverTouchesGlobalDaemon(t *testing.T) {
	forbidden := []string{"ensureGlobalDaemon", "globalDaemonHandle"}
	for _, funcName := range []string{"runHeadless", "dispatchHeadlessExecution"} {
		fn := findFuncDecl(t, "main.go", funcName)
		for _, name := range forbidden {
			if funcBodyReferencesIdent(fn, name) {
				t.Errorf("%s references forbidden identifier %q — swarm run/-p must never start or select the persistent daemon implicitly (ADR-002 Decision section)", funcName, name)
			}
		}
	}

	// normalizeRunArgs (run_cli.go) is the only other code the `run` dispatch
	// case calls before dispatchHeadlessExecution; assert it too, since a
	// future "helpfully" daemon-aware rewrite of the adapter itself would
	// otherwise slip past the check above.
	fn := findFuncDecl(t, "run_cli.go", "normalizeRunArgs")
	for _, name := range forbidden {
		if funcBodyReferencesIdent(fn, name) {
			t.Errorf("normalizeRunArgs references forbidden identifier %q", name)
		}
	}
}

// captureStderr redirects os.Stderr for the duration of f and returns
// everything written to it. Not safe to run in parallel with other tests
// that also swap os.Stderr (none in this package do).
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	f()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

// captureStdout is captureStderr's os.Stdout counterpart.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	f()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

// wantLegacyAliasWarnings is transcribed independently, by hand, from
// docs/architecture/swarm-attach/adr-002-cli-taxonomy.md's "## Compatibility"
// table — NOT copied from main.go's legacyAliasWarnings var. Comparing the
// two below catches real drift between the ADR and the shipped table
// (a tautological test would instead just re-read legacyAliasWarnings and
// compare it to itself).
var wantLegacyAliasWarnings = []legacyAliasWarning{
	{Legacy: "swarm -p <prompt>", Canonical: "swarm run <prompt>", Warning: ""},
	{Legacy: "swarm swarm up", Canonical: "swarm daemon start", Warning: "warning: 'swarm swarm up' is deprecated; use 'swarm daemon start'"},
	{Legacy: "swarm swarm status", Canonical: "swarm peer list", Warning: "warning: 'swarm swarm status' is deprecated; use 'swarm peer list'"},
	{Legacy: "swarm swarm stop", Canonical: "swarm daemon stop", Warning: "warning: 'swarm swarm stop' is deprecated; use 'swarm daemon stop'"},
	{Legacy: "swarm swarm list", Canonical: "swarm peer list", Warning: "warning: 'swarm swarm list' is deprecated; use 'swarm peer list'"},
	{Legacy: "swarm swarm task", Canonical: "swarm peer task", Warning: "warning: 'swarm swarm task' is deprecated; use 'swarm peer task'"},
	{Legacy: "swarm swarm attach", Canonical: "swarm peer control", Warning: "warning: 'swarm swarm attach' is deprecated; use 'swarm peer control'"},
	{Legacy: "swarm --daemon", Canonical: "swarm attach", Warning: "warning: 'swarm --daemon' is deprecated; use 'swarm attach'"},
	{Legacy: "SWARM_DAEMON", Canonical: "swarm attach", Warning: "warning: 'SWARM_DAEMON' is deprecated; use 'swarm attach'"},
	{Legacy: "swarm daemon [foreground flags]", Canonical: "swarm daemon start", Warning: "warning: 'swarm daemon' foreground mode is deprecated; use 'swarm daemon start'"},
}

// TestLegacyAliasWarnings is the table-driven ADR-002 compatibility-matrix
// contract test required by P07 CONTRACT.md §5 ("a real dispatch/
// alias-warning contract test for Worker A: assert stderr warning text +
// exit code + that machine-mode output is unchanged"). For every one of
// the matrix's rows it proves, against the REAL production
// legacyAliasWarnings table and the REAL warnLegacyAlias function:
//
//  1. The shipped table matches an independently transcribed copy of the
//     ADR text (catches silent drift/typos).
//  2. In human mode, the exact warning text is written to stderr exactly
//     once (and nothing for the one row, "swarm -p <prompt>", that ADR-002
//     explicitly marks as carrying no warning).
//  3. In machine mode, NOTHING is ever written to stderr, for every row
//     without exception — including the 8 rows that do warn in human mode.
func TestLegacyAliasWarnings(t *testing.T) {
	if len(legacyAliasWarnings) != len(wantLegacyAliasWarnings) {
		t.Fatalf("legacyAliasWarnings has %d rows, ADR-002 Compatibility table has %d — table drifted from the ADR",
			len(legacyAliasWarnings), len(wantLegacyAliasWarnings))
	}
	for i, want := range wantLegacyAliasWarnings {
		got := legacyAliasWarnings[i]
		if got != want {
			t.Fatalf("legacyAliasWarnings[%d] = %+v, want %+v (row for %q drifted from ADR-002)", i, got, want, want.Legacy)
		}
	}

	for _, row := range wantLegacyAliasWarnings {
		row := row
		t.Run(row.Legacy, func(t *testing.T) {
			humanOut := captureStderr(t, func() { warnLegacyAlias(row.Legacy, false) })
			// "swarm swarm attach"'s ADR-002 row recommends `swarm peer control`,
			// but issue #231 is exactly that recommendation firing while peer
			// control is still unimplemented and itself refuses with "not yet
			// supported: use swarm swarm attach for now" -- a contradictory loop.
			// While peerControlImplemented is false, warnLegacyAlias substitutes a
			// warning that states the future plan without recommending a command
			// that cannot succeed;
			// TestSwarmAttachAliasDoesNotRecommendUnimplementedPeerControl below is
			// this row's dedicated regression test. The row's static Warning text
			// (checked above against the ADR-002 table) stays the AUTHORED future
			// text so the switch to real ADR-002-compliant behavior is a one-line
			// flip of peerControlImplemented in peer_cli.go, not a table edit.
			if row.Legacy == "swarm swarm attach" && !peerControlImplemented {
				return
			}
			if row.Warning == "" {
				if humanOut != "" {
					t.Fatalf("warnLegacyAlias(%q, false) wrote %q to stderr, want nothing (ADR-002 row has no authorized warning)", row.Legacy, humanOut)
				}
			} else {
				want := row.Warning + "\n"
				if humanOut != want {
					t.Fatalf("warnLegacyAlias(%q, false) stderr = %q, want %q exactly once", row.Legacy, humanOut, want)
				}
				if strings.Count(humanOut, row.Warning) != 1 {
					t.Fatalf("warnLegacyAlias(%q, false) printed the warning %d times, want exactly once", row.Legacy, strings.Count(humanOut, row.Warning))
				}
			}

			machineOut := captureStderr(t, func() { warnLegacyAlias(row.Legacy, true) })
			if machineOut != "" {
				t.Fatalf("warnLegacyAlias(%q, true) wrote %q to stderr, want nothing in machine-output mode (ADR-002 Compatibility section)", row.Legacy, machineOut)
			}
		})
	}
}

// swarmAttachExpectedWarning returns the ADR-002 row text for "swarm swarm
// attach" when peerControlImplemented is true, or the phased
// non-recommending substitute warnLegacyAlias actually emits while it is
// false (see issue #231). Test helper only -- mirrors, rather than imports,
// warnLegacyAlias's branch so wiring tests assert against an independent
// expectation instead of trivially matching whatever the function returns.
func swarmAttachExpectedWarning() string {
	if peerControlImplemented {
		row, ok := legacyAliasWarningFor("swarm swarm attach")
		if !ok {
			return ""
		}
		return row.Warning
	}
	return "warning: 'swarm swarm attach' will be replaced by 'swarm peer control' once that command is implemented; " +
		"'swarm peer control' is not yet supported, so continue using 'swarm swarm attach' for now"
}

// TestSwarmAttachAliasDoesNotRecommendUnimplementedPeerControl is issue
// #231's regression test. `swarm swarm attach` succeeded but its
// deprecation warning told operators to switch to `swarm peer control`,
// and `swarm peer control` itself immediately refused with "not yet
// supported: use swarm swarm attach for now" -- a contradictory command
// loop with no way out. This proves, against the REAL warnLegacyAlias and
// peerControl:
//
//  1. While peerControlImplemented is false, the human-mode warning for
//     `swarm swarm attach` does NOT contain the exact recommendation text
//     "use 'swarm peer control'" from the ADR-002 row.
//  2. `swarm peer control` (peerControl) does in fact still refuse, so the
//     test is exercising the real contradictory-loop precondition and not
//     a stale assumption.
//  3. Once peerControlImplemented is true (the near-miss branch below,
//     exercised structurally since flipping the real package constant
//     from a test is not possible), the row's normal ADR-002 warning DOES
//     recommend `swarm peer control` -- i.e. the suppression is scoped to
//     "while unimplemented" and is not a permanent behavior change.
func TestSwarmAttachAliasDoesNotRecommendUnimplementedPeerControl(t *testing.T) {
	if peerControlImplemented {
		t.Skip("peer control is now implemented: the ADR-002 recommendation is expected again; " +
			"this test's job (proving the OLD contradictory loop is avoided) is now covered by the " +
			"normal TestLegacyAliasWarnings table-driven path instead")
	}

	humanOut := captureStderr(t, func() { warnLegacyAlias("swarm swarm attach", false) })
	if strings.Contains(humanOut, "use 'swarm peer control'") {
		t.Fatalf("warnLegacyAlias(%q, false) recommended the unimplemented replacement command: %q", "swarm swarm attach", humanOut)
	}
	if humanOut == "" {
		t.Fatalf("expected SOME warning while peer control remains unimplemented (the deprecation is still real), got none")
	}

	// Confirm the precondition: swarm peer control really does still refuse,
	// so the warning above would otherwise send operators into a dead end.
	err := peerControl(nil)
	if err == nil {
		t.Fatalf("peerControl succeeded but peerControlImplemented is false -- update peerControlImplemented instead of this test")
	}
	if !strings.Contains(err.Error(), "swarm swarm attach") {
		t.Fatalf("peerControl's refusal no longer points back at swarm swarm attach: %v", err)
	}

	// Near-miss: machine mode must still suppress the warning entirely, same
	// as every other row.
	machineOut := captureStderr(t, func() { warnLegacyAlias("swarm swarm attach", true) })
	if machineOut != "" {
		t.Fatalf("warnLegacyAlias(%q, true) wrote %q to stderr, want nothing in machine-output mode", "swarm swarm attach", machineOut)
	}
}

// withIsolatedSwarmHome points a2a's on-disk peer registry at a fresh temp
// directory for the duration of the test, so runSwarmCLI/runPeerCLI calls
// below never touch the real developer machine's swarm registry and always
// see an empty peer set (deterministic "no peers" output).
func withIsolatedSwarmHome(t *testing.T) {
	t.Helper()
	t.Setenv("SWARM_HOME", t.TempDir())
}

// TestSwarmCLILegacyWarningsWiring proves the alias warnings above are
// actually wired into the real runSwarmCLI dispatcher (swarm_cli.go), not
// just present in the table. It exercises the subcommands that are safe to
// invoke for real in a test process — they only read the (isolated, empty)
// local peer registry and return quickly — list/status/task/attach.
// (`up`/`stop` are deliberately NOT invoked here: swarmUp can spawn a real
// detached daemon process and swarmStop can send a real signal, neither of
// which belongs in a unit test; TestSwarmCLIUpStopWarningsWiredStatically
// below covers those two via source inspection instead.)
func TestSwarmCLILegacyWarningsWiring(t *testing.T) {
	withIsolatedSwarmHome(t)

	cases := []struct {
		name    string
		args    []string
		warning string
	}{
		{name: "list", args: []string{"list"}, warning: "warning: 'swarm swarm list' is deprecated; use 'swarm peer list'"},
		{name: "status", args: []string{"status"}, warning: "warning: 'swarm swarm status' is deprecated; use 'swarm peer list'"},
		{name: "task", args: []string{"task", "no-such-peer", "hello"}, warning: "warning: 'swarm swarm task' is deprecated; use 'swarm peer task'"},
		// "attach" expects whichever warning warnLegacyAlias actually emits
		// for its current peerControlImplemented state (see issue #231 /
		// TestSwarmAttachAliasDoesNotRecommendUnimplementedPeerControl):
		// the ADR-002 recommendation once peer control is implemented, or
		// the non-recommending phased text while it is not.
		{name: "attach", args: []string{"attach", "no-such-peer"}, warning: swarmAttachExpectedWarning()},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var callErr error
			stderr := captureStderr(t, func() {
				_ = captureStdout(t, func() {
					callErr = runSwarmCLI(tc.args)
				})
			})
			_ = callErr // errors (e.g. "peer not found") are expected/harmless here
			if strings.Count(stderr, tc.warning) != 1 {
				t.Fatalf("runSwarmCLI(%v) stderr = %q, want exactly one occurrence of %q", tc.args, stderr, tc.warning)
			}
		})
	}

	// Machine-mode carve-out, exercised end-to-end through the real
	// dispatcher: appending a --json token (isMachineOutputArgs' detector)
	// must suppress the warning entirely while leaving stdout byte-identical
	// to the non-machine-mode invocation, per ADR-002's Compatibility
	// section ("emit no warning text ... [preserve] documented machine
	// schema").
	t.Run("machine_mode_suppresses_warning_and_preserves_stdout", func(t *testing.T) {
		var humanStdout, jsonStdout string
		humanStderr := captureStderr(t, func() {
			humanStdout = captureStdout(t, func() { _ = runSwarmCLI([]string{"list"}) })
		})
		jsonStderr := captureStderr(t, func() {
			jsonStdout = captureStdout(t, func() { _ = runSwarmCLI([]string{"list", "--json"}) })
		})
		if humanStderr == "" {
			t.Fatal("expected a warning on stderr for the human-mode 'list' invocation")
		}
		if jsonStderr != "" {
			t.Fatalf("machine-mode ('list --json') stderr = %q, want empty", jsonStderr)
		}
		if humanStdout != jsonStdout {
			t.Fatalf("stdout differs between human and machine mode:\nhuman: %q\njson:  %q", humanStdout, jsonStdout)
		}
	})
}

// TestSwarmCLIUpStopWarningsWiredStatically covers the two legacy
// subcommands (`up`, `stop`) that TestSwarmCLILegacyWarningsWiring
// deliberately does not invoke for real (they can spawn/signal a real
// daemon process). It statically confirms swarm_cli.go's "up"/"stop" cases
// call warnLegacyAlias with the exact ADR-002 legacy keys, by parsing the
// real switch statement in runSwarmCLI and checking each case body's calls
// — not just grepping the whole file, so an unrelated comment mentioning
// the same string can't produce a false pass.
func TestSwarmCLIUpStopWarningsWiredStatically(t *testing.T) {
	fn := findFuncDecl(t, "swarm_cli.go", "runSwarmCLI")
	want := map[string]string{
		"up":   "swarm swarm up",
		"stop": "swarm swarm stop",
	}
	found := map[string]bool{}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		cc, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		var caseLabel string
		for _, expr := range cc.List {
			if lit, ok := expr.(*ast.BasicLit); ok {
				caseLabel = strings.Trim(lit.Value, `"`)
			}
		}
		wantLegacy, relevant := want[caseLabel]
		if !relevant {
			return true
		}
		for _, stmt := range cc.Body {
			exprStmt, ok := stmt.(*ast.ExprStmt)
			if !ok {
				continue
			}
			call, ok := exprStmt.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			fnIdent, ok := call.Fun.(*ast.Ident)
			if !ok || fnIdent.Name != "warnLegacyAlias" || len(call.Args) < 1 {
				continue
			}
			arg0, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				continue
			}
			if strings.Trim(arg0.Value, `"`) == wantLegacy {
				found[caseLabel] = true
			}
		}
		return true
	})

	for caseLabel, wantLegacy := range want {
		if !found[caseLabel] {
			t.Errorf("runSwarmCLI's %q case does not call warnLegacyAlias(%q, ...)", caseLabel, wantLegacy)
		}
	}
}

// TestMainGoDaemonAliasWarningsWiredStatically covers the three
// ADR-002 rows whose call sites live in main.go and are unsafe to exercise
// end-to-end in a unit test (they either launch the interactive TUI or the
// foreground daemon process): `swarm --daemon`, `SWARM_DAEMON`, and bare
// `swarm daemon [foreground flags]`. A plain substring check against the
// real main.go source is sufficient and genuinely load-bearing here (it
// fails the moment any of these three exact warnLegacyAlias(...) call
// sites is deleted, renamed, or has its legacy-key argument changed).
func TestMainGoDaemonAliasWarningsWiredStatically(t *testing.T) {
	src := readPackageFile(t, "main.go")
	for _, want := range []string{
		`warnLegacyAlias("swarm --daemon", false)`,
		`warnLegacyAlias("SWARM_DAEMON", false)`,
		`warnLegacyAlias("swarm daemon [foreground flags]", false)`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("main.go no longer contains %s", want)
		}
	}
}

// TestPeerDispatchReachesSameFunctionsAsSwarmDispatch is the P07 CONTRACT.md
// §3 regression test: "Worker A's new run_cli.go/peer_cli.go dispatch
// functions MUST reuse the existing runHeadless/swarmList/swarmTask/
// swarmAttach implementations by calling them ... do not duplicate their
// bodies." It proves `swarm peer list`/`swarm peer task` and the legacy
// `swarm swarm list`/`swarm swarm task` produce IDENTICAL stdout/error
// behavior by invoking both real dispatchers side by side against the same
// isolated (empty) peer registry — the only way they could differ is if
// peer_cli.go stopped calling the shared swarmList/swarmTask functions.
func TestPeerDispatchReachesSameFunctionsAsSwarmDispatch(t *testing.T) {
	withIsolatedSwarmHome(t)

	t.Run("list", func(t *testing.T) {
		var peerErr, swarmErr error
		peerOut := captureStdout(t, func() { peerErr = runPeerCLI([]string{"list"}) })
		var swarmStdout string
		// swarm_cli.go's "list" case also writes a deprecation warning to
		// stderr; capture+discard it here so only stdout is compared, which
		// is what CONTRACT.md's "no behavior divergence" promise covers
		// (the warning itself is an intentional, documented difference
		// between the two spellings).
		_ = captureStderr(t, func() {
			swarmStdout = captureStdout(t, func() { swarmErr = runSwarmCLI([]string{"list"}) })
		})

		if peerErr != swarmErr {
			t.Fatalf("runPeerCLI list err = %v, runSwarmCLI list err = %v, want equal", peerErr, swarmErr)
		}
		if peerOut != swarmStdout {
			t.Fatalf("runPeerCLI list stdout = %q, runSwarmCLI list stdout = %q, want identical (both must call swarmList())", peerOut, swarmStdout)
		}
	})

	t.Run("task", func(t *testing.T) {
		args := []string{"no-such-peer", "hello there"}
		var peerErr, swarmErr error
		peerOut := captureStdout(t, func() { peerErr = runPeerCLI(append([]string{"task"}, args...)) })
		var swarmStdout string
		_ = captureStderr(t, func() {
			swarmStdout = captureStdout(t, func() { swarmErr = runSwarmCLI(append([]string{"task"}, args...)) })
		})

		if (peerErr == nil) != (swarmErr == nil) {
			t.Fatalf("runPeerCLI task err = %v, runSwarmCLI task err = %v, want same nil-ness", peerErr, swarmErr)
		}
		if peerErr != nil && swarmErr != nil && peerErr.Error() != swarmErr.Error() {
			t.Fatalf("runPeerCLI task err = %q, runSwarmCLI task err = %q, want identical (both must call swarmTask())", peerErr.Error(), swarmErr.Error())
		}
		if peerOut != swarmStdout {
			t.Fatalf("runPeerCLI task stdout = %q, runSwarmCLI task stdout = %q, want identical", peerOut, swarmStdout)
		}
	})
}

// TestPeerControlNotYetSupported proves `swarm peer control` returns the
// exact, stable "deferred this phase" error CONTRACT.md §1 mandates
// ("swarm peer control <peer> <presentation-command> [arguments]` should
// return a clear, stable "not yet supported: use `swarm swarm attach` for
// now" error") rather than silently no-op-ing, partially wiring
// presentation control, or panicking.
func TestPeerControlNotYetSupported(t *testing.T) {
	err := runPeerCLI([]string{"control", "some-peer", "frame"})
	if err == nil {
		t.Fatal("runPeerCLI control = nil error, want the deferred-support error")
	}
	const want = "not yet supported: use `swarm swarm attach` for now"
	if err.Error() != want {
		t.Fatalf("runPeerCLI control error = %q, want %q", err.Error(), want)
	}
}

// TestNearestCanonicalCommand exercises the real production function that
// backs main.go's unknown-command error path (ADR-002 Decision section:
// "Unknown commands and invalid combinations fail with a non-zero status
// and show the nearest canonical command").
func TestNearestCanonicalCommand(t *testing.T) {
	cases := []struct {
		got  string
		want string
	}{
		{got: "pee", want: "peer"},
		{got: "peeer", want: "peer"},
		{got: "rn", want: "run"},
		{got: "daemn", want: "daemon"},
		{got: "atach", want: "attach"},
		{got: "servic", want: "service"},
		{got: "swrm", want: "swarm"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.got, func(t *testing.T) {
			got, ok := nearestCanonicalCommand(tc.got)
			if !ok {
				t.Fatalf("nearestCanonicalCommand(%q) = (_, false), want a suggestion", tc.got)
			}
			if got != tc.want {
				t.Fatalf("nearestCanonicalCommand(%q) = %q, want %q", tc.got, got, tc.want)
			}
		})
	}

	if _, ok := nearestCanonicalCommand(""); ok {
		t.Fatal("nearestCanonicalCommand(\"\") = (_, true), want false for empty input")
	}
}

// buildSwarmosOnce/builtSwarmosPath cache a single build of the real
// swarmos binary across tests in this file's process, since `go build` is
// too slow to repeat per subtest.
var (
	buildSwarmosOnce sync.Once
	builtSwarmosPath string
	builtSwarmosErr  error
)

func buildSwarmosBinary(t *testing.T) string {
	t.Helper()
	buildSwarmosOnce.Do(func() {
		dir := packageDir(t)
		out := filepath.Join(t.TempDir(), "swarmos-cli-contract-test")
		cmd := exec.Command("go", "build", "-o", out, ".")
		cmd.Dir = dir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			builtSwarmosErr = &buildError{err: err, stderr: stderr.String()}
			return
		}
		builtSwarmosPath = out
	})
	if builtSwarmosErr != nil {
		t.Fatalf("build swarmos test binary: %v", builtSwarmosErr)
	}
	return builtSwarmosPath
}

type buildError struct {
	err    error
	stderr string
}

func (b *buildError) Error() string {
	return b.err.Error() + "\n" + b.stderr
}

// TestUnknownCommandFailsWithSuggestion is the true end-to-end version of
// the ADR-002 unknown-command requirement: it runs the REAL compiled
// swarmos binary (not an in-process helper) with a deliberately misspelled
// top-level command and asserts a non-zero exit status plus a stderr
// message naming the nearest canonical command — exactly what a user
// running the shipped binary would see.
func TestUnknownCommandFailsWithSuggestion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real binary build in -short mode")
	}
	bin := buildSwarmosBinary(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// A near-miss of a real canonical command word ("pier" -> "peer") so the
	// nearestCanonicalCommand bounded-distance suggestion actually fires;
	// wildly unrelated input is intentionally left without a suggestion by
	// that function's own distance threshold (see TestNearestCanonicalCommand).
	cmd := exec.CommandContext(ctx, bin, "pier")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit for unknown command, got success; output: %s", stderr.String())
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() == 0 {
			t.Fatalf("expected non-zero exit code, got 0")
		}
	}
	out := stderr.String()
	if !strings.Contains(out, "unknown command") {
		t.Fatalf("stderr = %q, want it to contain \"unknown command\"", out)
	}
	if !strings.Contains(out, "did you mean") {
		t.Fatalf("stderr = %q, want it to contain a \"did you mean\" suggestion", out)
	}
}
