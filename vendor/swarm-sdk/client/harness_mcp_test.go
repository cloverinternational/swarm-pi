// Harness Phase 7b tests — client-side MCP EXECUTION wiring on the closed
// path. Three tiers:
//
//  1. Direct unit tests against the unexported helpers (harnessMCPAllowlistedEnv,
//     harnessMCPResolvedHeaders, harnessMCPToolsConfig, resolveHarnessMCPWorkDir)
//     — no plan/manager construction needed, proving the resolution/allowlist
//     logic in isolation.
//  2. Direct unit tests against buildHarnessMCPManager itself, proving the
//     zero/all-disabled no-op invariant and the workDir fail-closed path.
//  3. Full end-to-end wiring tests via New(WithHarnessPlan(...)) (mirroring
//     harness_hooks_test.go/harness_skills_test.go), proving initHarnessAgent's
//     atomic wiring, config isolation from a REAL rogue ambient project config
//     file, and best-effort (non-fatal) live connectivity failure handling.
package client

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// compileMcpPlan compiles an in-memory manifest anchored at dir (so
// manifest-relative mcp workDir entries resolve against dir and
// RevealSkillBaseDir()==dir, matching compileHookPlan/compilePlanInDir).
func compileMcpPlan(t *testing.T, dir, manifest string) *harness.Plan {
	t.Helper()
	src := filepath.Join(dir, "harness.yaml")
	plan, err := harness.CompileBytes([]byte(manifest), src)
	if err != nil {
		t.Fatalf("CompileBytes: %v", err)
	}
	return plan
}

// baseMcpClientManifest returns a minimal valid manifest (matching
// harness/mcp_test.go's baseMcpManifest shape) with the given raw mcp block
// (already including the `mcp:` key) spliced in.
func baseMcpClientManifest(mcpBlock string) string {
	return "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: mcpclient\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n  credential:\n    inline: test-key\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: []\n" +
		"permissions:\n  approvalMode: readonly\n" +
		mcpBlock
}

// --- Tier 1: unexported helper unit tests -----------------------------------

// TestHarnessMCPAllowlistedEnv: only allowlisted NAMES that are ALSO set in
// the ambient environment are resolved; an allowlisted-but-unset name is
// omitted (not an error); a non-allowlisted ambient var never appears.
func TestHarnessMCPAllowlistedEnv(t *testing.T) {
	t.Setenv("HARNESS_MCP_TEST_FOO", "foo-value")
	t.Setenv("HARNESS_MCP_TEST_NOT_ALLOWLISTED", "should-never-appear")
	// HARNESS_MCP_TEST_UNSET deliberately left unset.

	env := harnessMCPAllowlistedEnv(context.Background(), noop.NewLogger(), "srv",
		[]string{"HARNESS_MCP_TEST_FOO", "HARNESS_MCP_TEST_UNSET"})

	if env["HARNESS_MCP_TEST_FOO"] != "foo-value" {
		t.Errorf("allowlisted+set var not resolved: %v", env)
	}
	if _, ok := env["HARNESS_MCP_TEST_UNSET"]; ok {
		t.Errorf("allowlisted-but-unset var must be omitted, not present as empty: %v", env)
	}
	if _, ok := env["HARNESS_MCP_TEST_NOT_ALLOWLISTED"]; ok {
		t.Errorf("non-allowlisted ambient var leaked into resolved env: %v", env)
	}
	if len(env) != 1 {
		t.Errorf("resolved env = %v, want exactly 1 entry", env)
	}
}

// TestHarnessMCPAllowlistedEnvEmptyAllowlist: a nil/empty allowlist resolves
// to nil — never falls back to the ambient environment.
func TestHarnessMCPAllowlistedEnvEmptyAllowlist(t *testing.T) {
	t.Setenv("HARNESS_MCP_TEST_AMBIENT", "leaked?")
	env := harnessMCPAllowlistedEnv(context.Background(), noop.NewLogger(), "srv", nil)
	if env != nil {
		t.Errorf("empty allowlist must resolve to nil, got %v", env)
	}
}

// TestHarnessMCPResolvedHeaders: a header resolves to the referenced
// allowlisted name's value; a header referencing a name that was omitted
// (unset) from the resolved env map is itself omitted, not present as empty.
func TestHarnessMCPResolvedHeaders(t *testing.T) {
	resolvedEnv := map[string]string{"API_TOKEN": "secret-value"}
	headerRefs := map[string]string{
		"Authorization": "API_TOKEN",
		"X-Missing":     "UNRESOLVED_NAME",
	}
	headers := harnessMCPResolvedHeaders(headerRefs, resolvedEnv)
	if headers["Authorization"] != "secret-value" {
		t.Errorf("Authorization header not resolved: %v", headers)
	}
	if _, ok := headers["X-Missing"]; ok {
		t.Errorf("header referencing an unresolved name must be omitted: %v", headers)
	}
	if len(headers) != 1 {
		t.Errorf("headers = %v, want exactly 1 entry", headers)
	}
}

// TestHarnessMCPToolsConfigTranslation: Tools -> allowlist mode, ExcludeTools
// -> blocklist mode, neither set -> nil (internal/mcp default: all tools).
func TestHarnessMCPToolsConfigTranslation(t *testing.T) {
	if tc := harnessMCPToolsConfig(harness.McpServerSpec{Tools: []string{"read_file"}}); tc == nil || tc.Mode != "allowlist" || len(tc.Enabled) != 1 || tc.Enabled[0] != "read_file" {
		t.Errorf("Tools translation wrong: %+v", tc)
	}
	if tc := harnessMCPToolsConfig(harness.McpServerSpec{ExcludeTools: []string{"dangerous"}}); tc == nil || tc.Mode != "blocklist" || len(tc.Disabled) != 1 || tc.Disabled[0] != "dangerous" {
		t.Errorf("ExcludeTools translation wrong: %+v", tc)
	}
	if tc := harnessMCPToolsConfig(harness.McpServerSpec{}); tc != nil {
		t.Errorf("neither set must translate to nil, got %+v", tc)
	}
}

// TestHarnessMCPWorkDirResolvesValid: a workDir that exists under base
// resolves to its real absolute path.
func TestHarnessMCPWorkDirResolvesValid(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "srv"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatalf("EvalSymlinks(base): %v", err)
	}
	got, err := resolveHarnessMCPWorkDir(base, realBase, "srv", "x")
	if err != nil {
		t.Fatalf("resolveHarnessMCPWorkDir: %v", err)
	}
	wantAbs, _ := filepath.EvalSymlinks(filepath.Join(base, "srv"))
	if got != wantAbs {
		t.Errorf("resolveHarnessMCPWorkDir() = %q, want %q", got, wantAbs)
	}
}

// TestHarnessMCPWorkDirEscapeFailsClosed: a workDir escaping the base via
// "../" is rejected.
func TestHarnessMCPWorkDirEscapeFailsClosed(t *testing.T) {
	base := t.TempDir()
	realBase, _ := filepath.EvalSymlinks(base)
	if _, err := resolveHarnessMCPWorkDir(base, realBase, "../outside", "x"); err == nil {
		t.Errorf("expected escape to fail closed")
	}
}

// TestHarnessMCPWorkDirMissingFailsClosed: a workDir that does not exist is
// rejected.
func TestHarnessMCPWorkDirMissingFailsClosed(t *testing.T) {
	base := t.TempDir()
	realBase, _ := filepath.EvalSymlinks(base)
	if _, err := resolveHarnessMCPWorkDir(base, realBase, "does-not-exist", "x"); err == nil {
		t.Errorf("expected missing workDir to fail closed")
	}
}

// TestHarnessMCPWorkDirNotDirectoryFailsClosed: a workDir that resolves to a
// regular FILE (not a directory) is rejected.
func TestHarnessMCPWorkDirNotDirectoryFailsClosed(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "afile"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	realBase, _ := filepath.EvalSymlinks(base)
	if _, err := resolveHarnessMCPWorkDir(base, realBase, "afile", "x"); err == nil {
		t.Errorf("expected a file-not-directory workDir to fail closed")
	}
}

// --- Tier 2: buildHarnessMCPManager direct unit tests -----------------------

// TestHarnessMCPZeroServersNoManager: an mcp-less plan returns (nil, false, nil).
func TestHarnessMCPZeroServersNoManager(t *testing.T) {
	dir := t.TempDir()
	plan := compileMcpPlan(t, dir, baseMcpClientManifest(""))

	mgr, hasMCP, err := buildHarnessMCPManager(context.Background(), plan, nil, noop.NewLogger(), noop.NewTracer())
	if err != nil {
		t.Fatalf("buildHarnessMCPManager: %v", err)
	}
	if hasMCP || mgr != nil {
		t.Errorf("hasMCP=%v mgr=%v for an mcp-less plan, want (false, nil)", hasMCP, mgr)
	}
}

// TestHarnessMCPAllDisabledNoManager: every declared entry is enabled: false
// -> present on plan.MCPServers() but never turned into a manager.
func TestHarnessMCPAllDisabledNoManager(t *testing.T) {
	dir := t.TempDir()
	manifest := baseMcpClientManifest(
		"mcp:\n  - id: disabled-server\n    type: stdio\n    command: /usr/bin/true\n    enabled: false\n")
	plan := compileMcpPlan(t, dir, manifest)

	if len(plan.MCPServers()) != 1 {
		t.Fatalf("plan.MCPServers() len = %d, want 1 (disabled entry still present)", len(plan.MCPServers()))
	}

	mgr, hasMCP, err := buildHarnessMCPManager(context.Background(), plan, nil, noop.NewLogger(), noop.NewTracer())
	if err != nil {
		t.Fatalf("buildHarnessMCPManager: %v", err)
	}
	if hasMCP || mgr != nil {
		t.Errorf("hasMCP=%v mgr=%v for an all-disabled plan, want (false, nil)", hasMCP, mgr)
	}
}

// TestHarnessMCPWorkDirFailClosedAbortsAtomically: a stdio entry's workDir
// existed at compile time but is removed before buildHarnessMCPManager runs
// (mirrors TestHarnessHooksWiringBadHookAbortsAtomically's script-vanish
// pattern) — the RE-VERIFICATION at this layer must independently catch it.
func TestHarnessMCPWorkDirFailClosedAbortsAtomically(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "srv"), 0o755); err != nil {
		t.Fatalf("mkdir srv: %v", err)
	}
	manifest := baseMcpClientManifest(
		"mcp:\n  - id: vanish\n    type: stdio\n    command: /usr/bin/true\n    workDir: srv\n")
	plan := compileMcpPlan(t, dir, manifest)

	if err := os.RemoveAll(filepath.Join(dir, "srv")); err != nil {
		t.Fatalf("remove srv: %v", err)
	}

	mgr, hasMCP, err := buildHarnessMCPManager(context.Background(), plan, nil, noop.NewLogger(), noop.NewTracer())
	if err == nil {
		t.Fatalf("expected buildHarnessMCPManager to fail closed on a vanished workDir")
	}
	if hasMCP || mgr != nil {
		t.Errorf("hasMCP=%v mgr=%v on a fail-closed error, want (false, nil)", hasMCP, mgr)
	}
}

// --- Tier 3: full initHarnessAgent wiring (end-to-end) ----------------------

// TestHarnessMCPWiringZeroMCPBuildsUnchanged: an mcp-less plan builds a
// client exactly as before Phase 7b — no manager, no behavior change.
func TestHarnessMCPWiringZeroMCPBuildsUnchanged(t *testing.T) {
	c := mustBuild(t, minimalManifest)
	if c.agent == nil {
		t.Fatalf("client has no agent")
	}
	if c.MCPManager() != nil {
		t.Errorf("expected nil MCPManager for an mcp-less plan")
	}
}

// TestHarnessMCPWiringEnabledFalseNeverStarted: a plan with only a disabled
// mcp entry builds cleanly with no observable manager, but the entry is
// still visible on plan.MCPServers() (declared, never started).
func TestHarnessMCPWiringEnabledFalseNeverStarted(t *testing.T) {
	dir := t.TempDir()
	manifest := baseMcpClientManifest(
		"mcp:\n  - id: disabled-server\n    type: stdio\n    command: /usr/bin/true\n    enabled: false\n")
	plan := compileMcpPlan(t, dir, manifest)

	c, err := New(WithHarnessPlan(plan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if c.MCPManager() != nil {
		t.Errorf("expected nil MCPManager when every declared mcp entry is disabled")
	}
	if len(plan.MCPServers()) != 1 || plan.MCPServers()[0].Enabled {
		t.Errorf("disabled entry must remain visible on plan.MCPServers(): %+v", plan.MCPServers())
	}
}

// TestHarnessMCPWiringLiveConnectivityFailureNonFatal: a declared stdio
// server whose pinned command does not exist, and a declared http server
// pointing at an unreachable URL, must NOT abort client construction —
// connection is best-effort, exactly like the standard client's
// initMCPManager. The manager IS built (hasMCP=true) even though neither
// server will ever successfully connect.
func TestHarnessMCPWiringLiveConnectivityFailureNonFatal(t *testing.T) {
	dir := t.TempDir()
	manifest := baseMcpClientManifest(
		"mcp:\n" +
			"  - id: no-such-binary\n    type: stdio\n    command: /no/such/path/to/an/mcp-server-binary\n" +
			"  - id: unreachable-http\n    type: http\n    url: http://127.0.0.1:1/mcp\n")
	plan := compileMcpPlan(t, dir, manifest)

	c, err := New(WithHarnessPlan(plan))
	if err != nil {
		t.Fatalf("New(harness) must not fail on a live connectivity problem: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if c.agent == nil {
		t.Fatalf("client has no agent")
	}
	if c.MCPManager() == nil {
		t.Fatalf("expected a non-nil MCPManager (config was valid; only live connectivity fails)")
	}
	states := c.MCPManager().GetServerStates()
	if len(states) != 2 {
		t.Errorf("GetServerStates() len = %d, want 2 (both declared servers present regardless of connectivity)", len(states))
	}
}

// TestHarnessMCPWiringConfigIsolationFromAmbientProjectConfig: the plan's
// workspace directory (defaults to the manifest directory) carries a REAL
// `.swarmos/mcp_servers.json` with a rogue server definition — the exact
// file/path the STANDARD (non-harness) client's initMCPManager would merge
// in as a project-scope layer. The harness closed path must never surface
// it: only the plan-declared server appears in the resulting manager's
// server states.
func TestHarnessMCPWiringConfigIsolationFromAmbientProjectConfig(t *testing.T) {
	dir := t.TempDir()
	swarmosDir := filepath.Join(dir, ".swarmos")
	if err := os.MkdirAll(swarmosDir, 0o755); err != nil {
		t.Fatalf("mkdir .swarmos: %v", err)
	}
	rogue := `{"schema_version":1,"servers":[{"name":"rogue-project-server","type":"stdio","command":"/usr/bin/true","enabled":true}]}`
	if err := os.WriteFile(filepath.Join(swarmosDir, "mcp_servers.json"), []byte(rogue), 0o644); err != nil {
		t.Fatalf("write rogue project config: %v", err)
	}

	manifest := baseMcpClientManifest(
		"mcp:\n  - id: declared-only\n    type: stdio\n    command: /usr/bin/true\n")
	plan := compileMcpPlan(t, dir, manifest)
	if plan.Workspace() != dir {
		t.Fatalf("test invalid: plan.Workspace() = %q, want manifest dir %q (workspace defaults to it)", plan.Workspace(), dir)
	}

	c, err := New(WithHarnessPlan(plan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	mgr := c.MCPManager()
	if mgr == nil {
		t.Fatalf("expected a non-nil MCPManager")
	}
	states := mgr.GetServerStates()
	if len(states) != 1 {
		t.Fatalf("GetServerStates() len = %d, want exactly 1 (the plan-declared server only): %+v", len(states), states)
	}
	if states[0].Name != "declared-only" {
		t.Errorf("GetServerStates()[0].Name = %q, want %q", states[0].Name, "declared-only")
	}
	for _, s := range states {
		if s.Name == "rogue-project-server" {
			t.Fatalf("rogue ambient project config server surfaced on the closed harness path: %+v", states)
		}
	}
}
