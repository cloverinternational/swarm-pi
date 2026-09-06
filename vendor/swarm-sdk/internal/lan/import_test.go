package lan

// This file is the internal/lan instance of the Phase 08 P08.3
// forbidden-import test mechanism (see
// .swarmflow/swarm-attach-architecture/p08-package-decomposition/CONTRACT.md
// section 3, "Forbidden-import test mechanism"). The SAME checking
// mechanism (collectGoFileImports / collectPackageDirImports /
// importPathMatchesToken / findForbiddenImports, all go/parser-based
// stdlib-only AST parsing, no new module dependency) is duplicated
// verbatim across this file, internal/lifecycle/import_test.go,
// attachclient/import_test.go, and internal/presentationcontrol/import_test.go,
// so the four forbidden-import tests never diverge. Only the per-package
// denylist (forbiddenImports below), the synthetic negative-case fixture,
// and the package clause differ between the four files.
//
// Subpackage coverage: this test checks BOTH internal/lan's own direct
// .go files AND its internal/lan/gossip subpackage's .go files (two
// separate collectPackageDirImports calls, merged into one imports map
// before checking) rather than relying on internal/lan/gossip having its
// own import_test.go, since gossip's implementation is explicitly
// read-only reuse for this phase (see CONTRACT.md's top-level "do not
// touch ... internal/lan/gossip's implementation" line) and is not in
// this worker's # FILES: list.

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ---- BEGIN shared forbidden-import checking mechanism (P08.3, keep in sync across all 4 import_test.go files) ----

// collectGoFileImports parses filename (a real on-disk path when src is
// nil, or the given src string under a synthetic filename when src is
// non-nil -- used by the negative-case sub-test below to construct an
// in-memory fixture without writing a temp file) with go/parser
// (parser.ImportsOnly: only the import block is needed, so declarations
// and function bodies are never parsed) and returns every import path it
// declares.
func collectGoFileImports(t *testing.T, filename string, src interface{}) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("collectGoFileImports: parse %s: %v", filename, err)
	}
	imports := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("collectGoFileImports: unquote import in %s: %v", filename, err)
		}
		imports = append(imports, path)
	}
	return imports
}

// collectPackageDirImports parses every non-test .go file directly inside
// dir (it does NOT recurse into subdirectories; each import_test.go
// documents its own subpackage coverage, if any, next to its
// forbiddenImports var) and returns a map from file basename to that
// file's import paths.
func collectPackageDirImports(t *testing.T, dir string) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("collectPackageDirImports: read dir %s: %v", dir, err)
	}
	out := make(map[string][]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out[filepath.Join(dir, name)] = collectGoFileImports(t, filepath.Join(dir, name), nil)
	}
	return out
}

// importPathMatchesToken reports whether importPath violates a single
// denylist token. A token containing "/" (e.g. "os/exec") is matched as a
// substring of the full import path, since such tokens already name an
// exact sub-path. A bare token (e.g. "daemon", "swarm-tui", "lan") is
// matched against each "/"-delimited segment of importPath, so "lan"
// flags both ".../internal/lan" and ".../internal/lan/gossip" (a
// subpackage import) while never false-flagging an unrelated package
// whose name merely contains "lan" as a substring of a longer segment.
func importPathMatchesToken(importPath, token string) bool {
	if strings.Contains(token, "/") {
		return strings.Contains(importPath, token)
	}
	for _, seg := range strings.Split(importPath, "/") {
		if seg == token {
			return true
		}
	}
	return false
}

// importViolation names exactly which file, import, and denylist entry
// triggered a forbidden-import failure, so a failing test is directly
// actionable without further digging.
type importViolation struct {
	File   string
	Import string
	Token  string
}

// findForbiddenImports checks every import in imports (a file ->
// import-path-list map, as returned by collectPackageDirImports, or
// constructed synthetically for the negative-case test below) against
// denylist and returns every violation found. It deliberately returns
// data rather than calling t.Errorf itself, so the SAME function can be
// used both for the real assertion (TestForbiddenImports) and for
// proving the checker is load-bearing on a deliberately-bad fixture
// (TestForbiddenImports_NegativeCaseCaught) without the negative case
// failing the suite.
func findForbiddenImports(imports map[string][]string, denylist []string) []importViolation {
	var violations []importViolation
	for file, imps := range imports {
		for _, imp := range imps {
			for _, token := range denylist {
				if importPathMatchesToken(imp, token) {
					violations = append(violations, importViolation{File: file, Import: imp, Token: token})
				}
			}
		}
	}
	return violations
}

// ---- END shared forbidden-import checking mechanism ----

// forbiddenImports is internal/lan's (and internal/lan/gossip's) denylist.
// Transcribed as the union of:
//   - Phase 08 CONTRACT.md section 3's starting-point list for
//     internal/lan: "daemon, attachclient, presentationcontrol, CLI/TUI,
//     engine packages."
//   - docs/architecture/swarm-attach/package-boundaries.md's own
//     "## LAN (`internal/lan`)" -> "Forbidden dependencies" bullet
//     (authoritative, verified verbatim): "daemon, attachclient,
//     supervisor, presentationcontrol, CLI/TUI, presence filesystem
//     implementation, concrete gateway HTTP/reverse-proxy adapters, and
//     engine packages. The package does not render gateway responses or
//     own engine handlers."
//
// Note lan's Allowed dependencies explicitly permit "presence public
// values/interfaces" (lan "consumes presence values through an
// interface"), so unlike internal/lifecycle's denylist, bare "presence"
// is deliberately NOT in this list -- only its *filesystem
// implementation* is forbidden, which (since internal/presence currently
// exposes no separate filesystem-implementation sub-package/import path
// distinct from the package itself) has no distinct importable path to
// deny beyond the whole-package token this package's Allowed list
// explicitly permits.
//
// "engine packages" is represented by internal/agent, client, serve, and
// internal/conversation (see internal/lifecycle/import_test.go's
// forbiddenImports comment for the same reasoning). "CLI/TUI" is
// represented by "swarm-tui". "concrete gateway HTTP/reverse-proxy
// adapters" is represented by "gateway" (the top-level gateway package).
//
// Verified against the real current internal/lan/*.go and
// internal/lan/gossip/*.go (non-test) files: every one imports only the
// standard library (crypto/*, encoding/*, net, net/http, net/url, sort,
// strings, sync, time, container/list, os, path/filepath, syscall). This
// test currently passes with zero pre-existing violations.
var forbiddenImports = []string{
	"daemon", "attachclient", "supervisor", "presentationcontrol",
	"swarm-tui", "gateway", "agent", "client", "serve", "conversation",
}

// TestForbiddenImports parses every non-test .go file in internal/lan AND
// internal/lan/gossip (see the subpackage-coverage comment at the top of
// this file) and fails with an actionable per-file/per-import message if
// any import matches forbiddenImports.
func TestForbiddenImports(t *testing.T) {
	imports := collectPackageDirImports(t, ".")
	for file, imps := range collectPackageDirImports(t, "gossip") {
		imports[file] = imps
	}
	for _, v := range findForbiddenImports(imports, forbiddenImports) {
		t.Errorf("%s: forbidden import %q (matches denylist entry %q); see package-boundaries.md's \"LAN (internal/lan)\" Forbidden dependencies bullet", v.File, v.Import, v.Token)
	}
}

// TestForbiddenImports_NegativeCaseCaught proves findForbiddenImports is
// actually load-bearing (not tautological) by constructing a synthetic,
// in-memory .go source containing a deliberately forbidden import
// (attachclient and the top-level gateway package, both of which
// internal/lan must never import per the Forbidden dependencies bullet
// above) and asserting the checker flags both. This never touches a real
// file in the package directory.
func TestForbiddenImports_NegativeCaseCaught(t *testing.T) {
	const src = `package lan

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/attachclient"
	"github.com/Swarm-Code/mono/swarm-sdk/gateway"
)

var _ = attachclient.Client(nil)
var _ = gateway.Server(nil)
var _ context.Context
`
	imps := collectGoFileImports(t, "synthetic_forbidden_fixture.go", src)
	fixture := map[string][]string{"synthetic_forbidden_fixture.go": imps}

	violations := findForbiddenImports(fixture, forbiddenImports)
	if len(violations) == 0 {
		t.Fatal("findForbiddenImports found no violations in a fixture with deliberately forbidden \"attachclient\" and \"gateway\" imports -- the checker is not load-bearing")
	}
	var sawAttachclient, sawGateway bool
	for _, v := range violations {
		if v.Token == "attachclient" {
			sawAttachclient = true
		}
		if v.Token == "gateway" {
			sawGateway = true
		}
	}
	if !sawAttachclient || !sawGateway {
		t.Fatalf("findForbiddenImports flagged violations %+v but did not match both expected tokens \"attachclient\" and \"gateway\"", violations)
	}
}
