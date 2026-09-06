package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSupplyChainFixture writes manifest to a fresh temp directory as
// harness.yaml and compiles it with the REAL harness.Compile path (Load ->
// strictDecode -> compileDocument), exactly like an operator's manifest
// would be compiled. No test in this file hand-builds a Plan or a HookSpec/
// McpServerSpec struct literal — every case below proves ExecutableSupplyChain/
// HasExecutableSupplyChain against genuine compiler output, per D5.
func writeSupplyChainFixture(t *testing.T, manifest string) *Plan {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "harness.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	plan, err := Compile(path)
	if err != nil {
		t.Fatalf("Compile(%s): %v\n--- manifest ---\n%s", path, err, manifest)
	}
	return plan
}

// writeSupplyChainFixtureWithScript is like writeSupplyChainFixture but also
// writes a hook script file inside the SAME temp directory (required for a
// `type: script` hook entry, which must resolve a manifest-relative,
// contained file). scriptRelPath is the manifest-relative path the fixture's
// YAML should reference (e.g. "hooks/on-start.sh").
func writeSupplyChainFixtureWithScript(t *testing.T, manifest, scriptRelPath, scriptBody string) *Plan {
	t.Helper()
	dir := t.TempDir()
	scriptAbs := filepath.Join(dir, filepath.FromSlash(scriptRelPath))
	if err := os.MkdirAll(filepath.Dir(scriptAbs), 0o755); err != nil {
		t.Fatalf("mkdir for script fixture: %v", err)
	}
	if err := os.WriteFile(scriptAbs, []byte(scriptBody), 0o755); err != nil {
		t.Fatalf("write script fixture: %v", err)
	}
	path := filepath.Join(dir, "harness.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	plan, err := Compile(path)
	if err != nil {
		t.Fatalf("Compile(%s): %v\n--- manifest ---\n%s", path, err, manifest)
	}
	return plan
}

// TestExecutableSupplyChainIncludesCommandHookAndStdioMCP is the D5 positive
// case: a `type: command` hook plus a `type: stdio` MCP server must both
// appear in ExecutableSupplyChain(), and HasExecutableSupplyChain() must be
// true.
func TestExecutableSupplyChainIncludesCommandHookAndStdioMCP(t *testing.T) {
	const manifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: supplychain-positive
provider:
  id: anthropic
  model: claude-harness-test
agent:
  systemPrompt:
    inline: "supply-chain positive-case prompt"
  tools: []
hooks:
  - id: notify-tool-calls
    event: tool.before
    scope: global
    type: command
    command: "echo hook-fired"
mcp:
  - id: local-fs
    type: stdio
    command: /usr/bin/true
    args:
      - "--flag"
      - "value"
`
	plan := writeSupplyChainFixture(t, manifest)

	if !plan.HasExecutableSupplyChain() {
		t.Fatal("HasExecutableSupplyChain() = false, want true (plan has a command hook and a stdio mcp server)")
	}

	report := plan.ExecutableSupplyChain()

	if len(report.Hooks) != 1 {
		t.Fatalf("report.Hooks = %+v, want exactly 1 entry", report.Hooks)
	}
	h := report.Hooks[0]
	if h.ID != "notify-tool-calls" || h.Event != "tool.before" || h.Scope != "global" || h.Kind != HookTypeCommand {
		t.Fatalf("report.Hooks[0] = %+v, want id=notify-tool-calls event=tool.before scope=global kind=command", h)
	}
	if h.CommandHash == "" || !strings.HasPrefix(h.CommandHash, "sha256:") {
		t.Fatalf("report.Hooks[0].CommandHash = %q, want a non-empty sha256:<hex> digest", h.CommandHash)
	}
	// Cross-check against the plan's own HookSpec, so this test would fail if
	// ExecutableSupplyChain ever diverged from Plan.Hooks()'s own hash.
	wantHash := plan.Hooks()[0].CommandHash
	if h.CommandHash != wantHash {
		t.Fatalf("report.Hooks[0].CommandHash = %q, want Plan.Hooks()[0].CommandHash = %q", h.CommandHash, wantHash)
	}

	if len(report.MCP) != 1 {
		t.Fatalf("report.MCP = %+v, want exactly 1 entry", report.MCP)
	}
	m := report.MCP[0]
	if m.ID != "local-fs" || m.Command != "/usr/bin/true" {
		t.Fatalf("report.MCP[0] = %+v, want id=local-fs command=/usr/bin/true", m)
	}
	if len(m.Args) != 2 || m.Args[0] != "--flag" || m.Args[1] != "value" {
		t.Fatalf("report.MCP[0].Args = %v, want [--flag value]", m.Args)
	}
}

// TestExecutableSupplyChainIncludesScriptHook covers the OTHER executing hook
// kind (Kind == "script"), which D2's doc comment names alongside "command"
// but D5's enumerated cases do not spell out on their own. CommandHash must
// carry HookSpec.ContentHash (a script hook has no CommandHash at all).
func TestExecutableSupplyChainIncludesScriptHook(t *testing.T) {
	const manifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: supplychain-script-hook
provider:
  id: anthropic
  model: claude-harness-test
agent:
  systemPrompt:
    inline: "supply-chain script-hook prompt"
  tools: []
hooks:
  - id: on-start
    event: session.start
    scope: global
    type: script
    path: hooks/on-start.sh
`
	plan := writeSupplyChainFixtureWithScript(t, manifest, "hooks/on-start.sh", "#!/bin/sh\necho started\n")

	if !plan.HasExecutableSupplyChain() {
		t.Fatal("HasExecutableSupplyChain() = false, want true (plan has a script hook)")
	}

	report := plan.ExecutableSupplyChain()
	if len(report.Hooks) != 1 {
		t.Fatalf("report.Hooks = %+v, want exactly 1 entry", report.Hooks)
	}
	h := report.Hooks[0]
	if h.ID != "on-start" || h.Kind != HookTypeScript {
		t.Fatalf("report.Hooks[0] = %+v, want id=on-start kind=script", h)
	}
	wantHash := plan.Hooks()[0].ContentHash
	if wantHash == "" {
		t.Fatal("plan.Hooks()[0].ContentHash is empty; fixture did not resolve as expected")
	}
	if h.CommandHash != wantHash {
		t.Fatalf("report.Hooks[0].CommandHash = %q, want Plan.Hooks()[0].ContentHash = %q", h.CommandHash, wantHash)
	}
	if len(report.MCP) != 0 {
		t.Fatalf("report.MCP = %+v, want empty (no mcp section declared)", report.MCP)
	}
}

// TestExecutableSupplyChainExcludesHTTPOnly is the D5 CRITICAL negative case:
// a plan with ONLY a `type: http` hook and a `type: http` (plus `type: sse`)
// MCP server must report EMPTY slices and HasExecutableSupplyChain() == false.
// Getting this wrong means every network-only manifest gets a false-positive
// "this plan executes arbitrary code" warning from any host that builds one
// on top of HasExecutableSupplyChain().
func TestExecutableSupplyChainExcludesHTTPOnly(t *testing.T) {
	const manifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: supplychain-http-only-negative
provider:
  id: anthropic
  model: claude-harness-test
agent:
  systemPrompt:
    inline: "supply-chain http-only negative-case prompt"
  tools: []
hooks:
  - id: notify-webhook
    event: tool.after
    scope: global
    type: http
    url: "https://example.com/hooks/notify"
mcp:
  - id: remote-http
    type: http
    url: "https://example.com/mcp"
  - id: remote-sse
    type: sse
    url: "https://example.com/mcp/sse"
`
	plan := writeSupplyChainFixture(t, manifest)

	// Sanity: the plan really does carry these entries (proves the negative
	// result below is "correctly filtered", not "fixture failed to declare
	// them").
	if len(plan.Hooks()) != 1 || len(plan.MCPServers()) != 2 {
		t.Fatalf("fixture sanity check failed: plan.Hooks()=%d plan.MCPServers()=%d, want 1 and 2",
			len(plan.Hooks()), len(plan.MCPServers()))
	}

	if plan.HasExecutableSupplyChain() {
		t.Fatal("HasExecutableSupplyChain() = true, want false: this plan declares only type=http hooks/mcp servers, which are network calls, not local execution")
	}

	report := plan.ExecutableSupplyChain()
	if len(report.Hooks) != 0 {
		t.Fatalf("report.Hooks = %+v, want empty (the only hook is type=http)", report.Hooks)
	}
	if len(report.MCP) != 0 {
		t.Fatalf("report.MCP = %+v, want empty (both mcp servers are type=http/sse)", report.MCP)
	}
}

// TestExecutableSupplyChainEmptyWhenNeitherHooksNorMCPDeclared is the D5
// baseline case: a plan that declares neither `hooks:` nor `mcp:` at all must
// report empty and false, not merely "empty because everything was filtered".
func TestExecutableSupplyChainEmptyWhenNeitherHooksNorMCPDeclared(t *testing.T) {
	const manifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: supplychain-none-declared
provider:
  id: anthropic
  model: claude-harness-test
agent:
  systemPrompt:
    inline: "supply-chain no-hooks-no-mcp prompt"
  tools: []
`
	plan := writeSupplyChainFixture(t, manifest)

	if len(plan.Hooks()) != 0 || len(plan.MCPServers()) != 0 {
		t.Fatalf("fixture sanity check failed: plan.Hooks()=%d plan.MCPServers()=%d, want 0 and 0",
			len(plan.Hooks()), len(plan.MCPServers()))
	}
	if plan.HasExecutableSupplyChain() {
		t.Fatal("HasExecutableSupplyChain() = true, want false: plan declares no hooks and no mcp servers at all")
	}
	report := plan.ExecutableSupplyChain()
	if len(report.Hooks) != 0 || len(report.MCP) != 0 {
		t.Fatalf("report = %+v, want a report with both slices empty", report)
	}
}

// TestSupplyChainReportNeverLeaksHookCommandSecret is the D4 assertion,
// mirroring the spirit of Phase 11b's
// TestHarnessAuditEventsNeverCarryTheRawCredential: no known-secret fixture
// value declared in a `type: command` hook's inline command may EVER appear
// in a SupplyChainReport, in any field, under any string representation.
func TestSupplyChainReportNeverLeaksHookCommandSecret(t *testing.T) {
	const secretMarker = "CAFEBABE-SUPPLYCHAIN-SECRET-9f8e7d6c5b4a"
	manifest := `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: supplychain-secret-leak-check
provider:
  id: anthropic
  model: claude-harness-test
agent:
  systemPrompt:
    inline: "supply-chain secret-leak-check prompt"
  tools: []
hooks:
  - id: leaky-candidate
    event: tool.before
    scope: global
    type: command
    command: "curl -H 'Authorization: Bearer ` + secretMarker + `' https://example.com"
`
	plan := writeSupplyChainFixture(t, manifest)

	// Sanity: the secret really is on the plan somewhere (via the one
	// deliberate reveal accessor), so a false pass below can't be explained
	// by the fixture silently failing to carry the command at all.
	revealed, ok := plan.RevealHookCommand("leaky-candidate")
	if !ok || !strings.Contains(revealed, secretMarker) {
		t.Fatalf("fixture sanity check failed: RevealHookCommand(%q) = (%q, %v), want the command to contain %q",
			"leaky-candidate", revealed, ok, secretMarker)
	}

	report := plan.ExecutableSupplyChain()
	if len(report.Hooks) != 1 {
		t.Fatalf("report.Hooks = %+v, want exactly 1 entry", report.Hooks)
	}
	h := report.Hooks[0]

	if strings.Contains(h.ID, secretMarker) ||
		strings.Contains(h.Event, secretMarker) ||
		strings.Contains(h.Scope, secretMarker) ||
		strings.Contains(h.Kind, secretMarker) ||
		strings.Contains(h.CommandHash, secretMarker) {
		t.Fatalf("report.Hooks[0] = %+v carries the raw secret marker %q; SupplyChainHookEntry must only ever carry CommandHash, never the command text", h, secretMarker)
	}

	// Belt-and-suspenders: also check the %+v rendering of the WHOLE report
	// (catches a future field added to either entry type that this test
	// wasn't updated to name explicitly) and its JSON encoding, exactly the
	// two forms an operator or a host log line would actually see.
	if strings.Contains(fmt.Sprintf("%+v", report), secretMarker) {
		t.Fatalf("%%+v rendering of SupplyChainReport contains the raw secret marker %q", secretMarker)
	}
	jsonBytes, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal(report): %v", err)
	}
	if strings.Contains(string(jsonBytes), secretMarker) {
		t.Fatalf("JSON encoding of SupplyChainReport contains the raw secret marker %q: %s", secretMarker, jsonBytes)
	}
}

// TestDocsExamplesCompile proves every example manifest under
// harness/docs/examples/*.yaml is a REAL, compiling manifest — never
// aspirational prose. A doc example that does not compile is a bug (see the
// GATES section of this slice's task description).
func TestDocsExamplesCompile(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("docs", "examples", "*.yaml"))
	if err != nil {
		t.Fatalf("glob docs/examples/*.yaml: %v", err)
	}
	if len(matches) < 3 {
		t.Fatalf("found %d example manifest(s) under harness/docs/examples/*.yaml, want at least 3: %v", len(matches), matches)
	}
	for _, path := range matches {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			if _, err := Compile(path); err != nil {
				t.Fatalf("harness.Compile(%s): %v", path, err)
			}
		})
	}
}
