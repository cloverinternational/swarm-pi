// Harness Phase 7c tests — MCP tool EXPOSURE on the closed path: transactional
// hint recompute, allow/block enforcement, and the stated tool-name collision
// policy (see the PHASE 7c banner in harness_mcp.go).
//
// HOW THESE TESTS SIMULATE MCP REGISTRATION, AND WHY THAT IS FAITHFUL:
// internal/mcp.RuntimeManager only registers tools after a REAL transport
// connects, and its connect/sleep seams (connectOnceFunc, sleepFn) are
// unexported, so package client cannot drive a live server without a real MCP
// server process. Instead these tests register tools into the client's OWN
// registry — the same registry the manager is handed — through fakeMCPTool,
// which mirrors internal/mcp's mcpToolWrapper exactly on the three properties
// exposure depends on:
//
//	name      = "mcp_<server>_<tool>"                        (uniqueToolName)
//	metadata  = {Source: ToolSourceMCP, ServerName: <server>} (ToolMetadata)
//	filtering = registry Enable/Disable per tool              (applyToolConfig)
//
// Assertions read the REAL provider payload via HarnessSnapshot().ExposedTools
// (i.e. agent.ProviderTools()), never the hint list directly, so everything
// between "a tool is in the registry with MCP provenance" and "the model can
// see it" is exercised end to end.
//
// TWO TIERS, AND WHY:
//
//  1. BEHAVIOURAL tests run against a client whose plan declares NO mcp block,
//     with the exposure constructed directly over that client's REAL agent and
//     REAL registry. This is deliberate and load-bearing for determinism: a
//     plan that DOES declare a server spawns RuntimeManager retry goroutines
//     that emit status (connecting/error/connecting/...) on their own schedule,
//     each legitimately triggering a recompute — which is precisely the
//     production behaviour we want, but it makes "not yet exposed" orderings
//     racy. Removing the live manager isolates the exposure contract.
//
//  2. WIRING tests use a REAL plan-declared server and assert that the
//     production instance exists, carries the plan's baseline/declared set, and
//     that the manager's OWN background status emissions reach it and widen
//     exposure with no help from the test. That is an eventual-consistency
//     assertion (wait for something that must happen), so it cannot flake.
package client

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// --- fixtures ---------------------------------------------------------------

// fakeMCPTool stands in for internal/mcp's mcpToolWrapper: an already-namespaced
// unique name plus MCP provenance metadata naming its server.
type fakeMCPTool struct {
	tools.BaseTool
	uniqueName string
	serverName string
	// source allows a test to forge a tool that LOOKS like an MCP tool by name
	// but does not carry MCP provenance. Empty means tools.ToolSourceMCP.
	source tools.ToolSource
	// marker distinguishes two tools registered under the same name, so a test
	// can prove WHICH one survived a collision.
	marker string
}

func (f *fakeMCPTool) Name() string        { return f.uniqueName }
func (f *fakeMCPTool) Description() string { return "fake mcp tool " + f.marker }
func (f *fakeMCPTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (f *fakeMCPTool) Execute(_ context.Context, _ map[string]any) (*tools.ToolResult, error) {
	return &tools.ToolResult{Output: "ok:" + f.marker}, nil
}

func (f *fakeMCPTool) ToolMetadata() *tools.ToolMetadata {
	src := f.source
	if src == "" {
		src = tools.ToolSourceMCP
	}
	return &tools.ToolMetadata{Source: src, ServerName: f.serverName, Category: "external"}
}

// mcpToolName mirrors internal/mcp uniqueToolName for names that need no
// sanitizing (all fixtures here use [a-z-] server ids and [a-z_] tool names).
func mcpToolName(server, tool string) string { return "mcp_" + server + "_" + tool }

// declaredSpec builds an ENABLED plan-declared server spec (optionally with an
// allow or block list), matching what harness.Plan.MCPServers() yields.
func declaredSpec(id string, allow, block []string) harness.McpServerSpec {
	return harness.McpServerSpec{ID: id, Type: "stdio", Command: "/usr/bin/true", Enabled: true, Tools: allow, ExcludeTools: block}
}

// registerFakeMCPTool registers a namespaced MCP-provenance tool, mirroring
// internal/mcp registerTools. It returns the registry name.
func registerFakeMCPTool(t *testing.T, reg tools.Registry, server, tool, marker string) string {
	t.Helper()
	name := mcpToolName(server, tool)
	if err := reg.Register(&fakeMCPTool{uniqueName: name, serverName: server, marker: marker}); err != nil {
		t.Fatalf("register %q: %v", name, err)
	}
	return name
}

// disableTool applies the registry Enable/Disable toggle applyToolConfig uses
// for a filtered-out (allowlist-omitted / excludeTools-named) MCP tool.
func disableTool(t *testing.T, reg tools.Registry, name string) {
	t.Helper()
	toggler, ok := reg.(interface{ DisableTool(string) error })
	if !ok {
		t.Fatalf("registry does not support the Enable/Disable toggle applyToolConfig requires")
	}
	if err := toggler.DisableTool(name); err != nil {
		t.Fatalf("DisableTool(%q): %v", name, err)
	}
}

// harnessMCPClient builds a closed-path client whose plan selects the given
// catalog tools and declares the given mcp block, and returns it with its live
// tool registry.
func harnessMCPClient(t *testing.T, toolsBlock, mcpBlock string) (*Client, tools.Registry) {
	t.Helper()
	dir := t.TempDir()
	manifest := "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: mcpexposure\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n  credential:\n    inline: test-key\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: " + toolsBlock + "\n" +
		"permissions:\n  approvalMode: readonly\n" +
		mcpBlock
	plan := compileMcpPlan(t, dir, manifest)

	c, err := New(WithHarnessPlan(plan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if c.agent == nil {
		t.Fatalf("client has no agent")
	}
	return c, c.agent.ToolRegistry()
}

// exposureFixture builds a MANAGER-FREE client (no mcp block => no
// RuntimeManager, no background status goroutines, no recompute the test did not
// ask for) plus an exposure over that client's REAL agent and REAL registry,
// seeded with the agent's actual static hint baseline and the given declared
// specs. Tier 1 of the two tiers described in the file header.
func exposureFixture(t *testing.T, toolsBlock string, specs ...harness.McpServerSpec) (*Client, tools.Registry, *harnessMCPExposure) {
	t.Helper()
	c, reg := harnessMCPClient(t, toolsBlock, "")
	if c.MCPManager() != nil || c.harnessMCPExposure != nil {
		t.Fatalf("fixture invariant: the behavioural fixture must have NO live manager")
	}
	exp := newHarnessMCPExposure(c.agentDef.ToolHints, specs, reg, c.agent, c.logger)
	return c, reg, exp
}

// recomputeNow drives the exposure exactly as the production status handler
// does after a server finishes registering (and filtering) its tools.
func recomputeNow(exp *harnessMCPExposure, server, state string) {
	exp.onServerStatus(context.Background(),
		mcp.ServerStatusRecord{Name: server, Status: mcp.ServerStatus{State: state}})
}

// exposedSet returns the REAL provider-visible tool names (the payload the next
// request would ship) as a set.
func exposedSet(t *testing.T, c *Client) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, n := range c.HarnessSnapshot().ExposedTools {
		out[n] = true
	}
	return out
}

// --- #6/#7: exposure recompute ---------------------------------------------

// TestHarnessMCPExposureDeclaredToolBecomesProviderVisible is the Phase 7b MAJOR
// closed: a plan-declared, enabled server's tool must appear in the REAL
// provider payload once registered and recomputed, alongside (never instead of)
// the plan's catalog tools, and must be executable.
func TestHarnessMCPExposureDeclaredToolBecomesProviderVisible(t *testing.T) {
	c, reg, exp := exposureFixture(t, "[forge.read]", declaredSpec("alpha", nil, nil))

	// Baseline: exactly the static Phase 7b hint set.
	if got := c.HarnessSnapshot().ExposedTools; len(got) != 1 || got[0] != "Read" {
		t.Fatalf("baseline ExposedTools = %v, want exactly [Read]", got)
	}

	name := registerFakeMCPTool(t, reg, "alpha", "search", "alpha-search")

	// NON-VACUITY: registration ALONE must not expose anything — that is exactly
	// the Phase 7b state (registered and executable, never provider-visible). If
	// this stopped holding, the assertions below would prove nothing about the
	// recompute being the mechanism.
	if exposedSet(t, c)[name] {
		t.Fatalf("registration alone exposed %q; the recompute is not what makes it visible", name)
	}

	recomputeNow(exp, "alpha", "connected")

	after := exposedSet(t, c)
	if !after[name] {
		t.Errorf("registered MCP tool %q is NOT provider-visible after recompute: %v", name, c.HarnessSnapshot().ExposedTools)
	}
	if !after["Read"] {
		t.Errorf("catalog tool Read was displaced by the recompute: %v", c.HarnessSnapshot().ExposedTools)
	}
	if len(after) != 2 {
		t.Errorf("ExposedTools = %v, want exactly [Read %s]", c.HarnessSnapshot().ExposedTools, name)
	}

	// Exposed implies executable through the same registry.
	if _, err := reg.Get(name); err != nil {
		t.Errorf("exposed MCP tool must be executable: Get(%q): %v", name, err)
	}
}

// TestHarnessMCPExposureRecomputeIsIdempotent: a server emits status many times;
// repeated recomputes must converge on the same set and never duplicate an entry.
func TestHarnessMCPExposureRecomputeIsIdempotent(t *testing.T) {
	c, reg, exp := exposureFixture(t, "[forge.read]", declaredSpec("alpha", nil, nil))
	name := registerFakeMCPTool(t, reg, "alpha", "search", "alpha-search")

	for range 5 {
		recomputeNow(exp, "alpha", "connected")
	}

	got := c.HarnessSnapshot().ExposedTools
	count := 0
	for _, n := range got {
		if n == name {
			count++
		}
	}
	if count != 1 {
		t.Errorf("tool %q appears %d times in %v, want exactly 1", name, count, got)
	}
	if len(got) != 2 {
		t.Errorf("ExposedTools = %v, want exactly 2 entries", got)
	}
}

// --- #6: allow/block filters -----------------------------------------------

// TestHarnessMCPExposureExcludedToolNeverVisibleNorExecutable: a tool named by
// excludeTools is blocked at REGISTRATION level by internal/mcp (registered,
// then DisableTool'd by applyToolConfig), which removes it from List() and makes
// Get() fail. Exposure must not re-widen it.
func TestHarnessMCPExposureExcludedToolNeverVisibleNorExecutable(t *testing.T) {
	spec := declaredSpec("alpha", nil, []string{"dangerous"})
	c, reg, exp := exposureFixture(t, "[forge.read]", spec)

	// The spec's blocklist reaches internal/mcp unchanged (Phase 7b translation).
	if tc := harnessMCPToolsConfig(spec); tc == nil || tc.Mode != "blocklist" || len(tc.Disabled) != 1 || tc.Disabled[0] != "dangerous" {
		t.Fatalf("excludeTools did not translate to an internal/mcp blocklist: %+v", tc)
	}

	safe := registerFakeMCPTool(t, reg, "alpha", "safe", "safe")
	blocked := registerFakeMCPTool(t, reg, "alpha", "dangerous", "dangerous")
	disableTool(t, reg, blocked) // exactly what applyToolConfig does

	recomputeNow(exp, "alpha", "connected")

	got := exposedSet(t, c)
	if got[blocked] {
		t.Errorf("excluded tool %q is provider-visible: %v", blocked, c.HarnessSnapshot().ExposedTools)
	}
	if !got[safe] {
		t.Errorf("non-excluded tool %q should be provider-visible: %v", safe, c.HarnessSnapshot().ExposedTools)
	}
	if _, err := reg.Get(blocked); err == nil {
		t.Errorf("excluded tool %q must NOT be executable", blocked)
	}
}

// TestHarnessMCPExposureAllowlistOmittedToolNeverVisibleNorExecutable: with a
// non-empty `tools` allowlist, a discovered tool NOT on the list is disabled by
// applyToolConfig, so it is neither exposed nor executable.
func TestHarnessMCPExposureAllowlistOmittedToolNeverVisibleNorExecutable(t *testing.T) {
	spec := declaredSpec("alpha", []string{"allowed"}, nil)
	c, reg, exp := exposureFixture(t, "[forge.read]", spec)

	if tc := harnessMCPToolsConfig(spec); tc == nil || tc.Mode != "allowlist" || len(tc.Enabled) != 1 || tc.Enabled[0] != "allowed" {
		t.Fatalf("tools did not translate to an internal/mcp allowlist: %+v", tc)
	}

	allowed := registerFakeMCPTool(t, reg, "alpha", "allowed", "allowed")
	omitted := registerFakeMCPTool(t, reg, "alpha", "omitted", "omitted")
	disableTool(t, reg, omitted)

	recomputeNow(exp, "alpha", "connected")

	got := exposedSet(t, c)
	if got[omitted] {
		t.Errorf("allowlist-omitted tool %q is provider-visible: %v", omitted, c.HarnessSnapshot().ExposedTools)
	}
	if !got[allowed] {
		t.Errorf("allowlisted tool %q should be provider-visible: %v", allowed, c.HarnessSnapshot().ExposedTools)
	}
	if _, err := reg.Get(omitted); err == nil {
		t.Errorf("allowlist-omitted tool %q must NOT be executable", omitted)
	}
}

// --- closed posture: only declared + enabled servers ------------------------

// TestHarnessMCPExposureDisabledServerContributesNothing: an enabled:false entry
// contributes nothing to exposure even when a tool is registered under its name,
// and (end to end) yields no manager and no exposure wiring at all.
func TestHarnessMCPExposureDisabledServerContributesNothing(t *testing.T) {
	disabled := harness.McpServerSpec{ID: "offserver", Type: "stdio", Command: "/usr/bin/true", Enabled: false}
	c, reg, exp := exposureFixture(t, "[forge.read]", disabled)

	name := registerFakeMCPTool(t, reg, "offserver", "tool", "off")
	recomputeNow(exp, "offserver", "connected")

	if exposedSet(t, c)[name] {
		t.Errorf("disabled server's tool %q became provider-visible: %v", name, c.HarnessSnapshot().ExposedTools)
	}
	for _, h := range exp.computeHints() {
		if h == name {
			t.Errorf("disabled server's tool %q must never be hinted: %v", name, exp.computeHints())
		}
	}

	// End to end: a plan whose only mcp entry is disabled wires nothing at all.
	// NB: the id is "offserver", not "off" — a bare `off` is a YAML boolean.
	c2, _ := harnessMCPClient(t, "[forge.read]",
		"mcp:\n  - id: offserver\n    type: stdio\n    command: /usr/bin/true\n    enabled: false\n")
	if c2.harnessMCPExposure != nil {
		t.Errorf("no exposure may be wired when every declared server is disabled")
	}
	if c2.MCPManager() != nil {
		t.Errorf("no manager may exist when every declared server is disabled")
	}
}

// TestHarnessMCPExposureUndeclaredServerNeverExposed: a tool carrying MCP
// provenance for a server the plan never declared is never exposed, and such a
// server's status record cannot even trigger a recompute.
func TestHarnessMCPExposureUndeclaredServerNeverExposed(t *testing.T) {
	c, reg, exp := exposureFixture(t, "[forge.read]", declaredSpec("alpha", nil, nil))

	ghost := registerFakeMCPTool(t, reg, "ghost", "exfil", "ghost")

	recomputeNow(exp, "alpha", "connected") // triggered by a DECLARED server
	if exposedSet(t, c)[ghost] {
		t.Errorf("undeclared server's tool %q became provider-visible: %v", ghost, c.HarnessSnapshot().ExposedTools)
	}

	recomputeNow(exp, "ghost", "connected") // an undeclared server cannot trigger one
	if exposedSet(t, c)[ghost] {
		t.Errorf("undeclared server status record exposed its tool %q: %v", ghost, c.HarnessSnapshot().ExposedTools)
	}
}

// TestHarnessMCPExposureRequiresMCPProvenance: a tool whose NAME looks
// namespaced but which does not carry ToolSourceMCP provenance is never exposed
// — exposure is provenance-checked, not name-pattern-checked.
func TestHarnessMCPExposureRequiresMCPProvenance(t *testing.T) {
	c, reg, exp := exposureFixture(t, "[forge.read]", declaredSpec("alpha", nil, nil))

	forged := mcpToolName("alpha", "forged")
	if err := reg.Register(&fakeMCPTool{
		uniqueName: forged, serverName: "alpha",
		source: tools.ToolSourceBuiltin, marker: "forged",
	}); err != nil {
		t.Fatalf("register forged tool: %v", err)
	}

	recomputeNow(exp, "alpha", "connected")
	if exposedSet(t, c)[forged] {
		t.Errorf("non-MCP-provenance tool %q became provider-visible: %v", forged, c.HarnessSnapshot().ExposedTools)
	}
}

// --- bidirectional: unregister removes exposure ----------------------------

// TestHarnessMCPExposureUnregisterRemovesVisibility: exposure tracks reality in
// BOTH directions. After unregisterTools plus the trailing status emit
// (monitorConnection's setStatus("error", "connection lost")), the tool is gone
// from the provider payload AND dropped from the recomputed hint list.
func TestHarnessMCPExposureUnregisterRemovesVisibility(t *testing.T) {
	c, reg, exp := exposureFixture(t, "[forge.read]", declaredSpec("alpha", nil, nil))

	name := registerFakeMCPTool(t, reg, "alpha", "search", "alpha-search")
	recomputeNow(exp, "alpha", "connected")
	if !exposedSet(t, c)[name] {
		t.Fatalf("precondition: %q should be visible before disconnect", name)
	}

	if err := reg.Unregister(name); err != nil {
		t.Fatalf("Unregister(%q): %v", name, err)
	}
	recomputeNow(exp, "alpha", "error")

	if exposedSet(t, c)[name] {
		t.Errorf("unregistered tool %q still provider-visible: %v", name, c.HarnessSnapshot().ExposedTools)
	}
	for _, h := range exp.computeHints() {
		if h == name {
			t.Errorf("unregistered tool %q still hinted: %v", name, exp.computeHints())
		}
	}
	if !exposedSet(t, c)["Read"] {
		t.Errorf("catalog tool lost during the removal recompute: %v", c.HarnessSnapshot().ExposedTools)
	}
}

// TestHarnessMCPExposureStaleHintCannotResurrectTool: removal is self-correcting
// even with a maximally stale hint list, because buildProviderTools resolves
// every hint through toolReg.Get. Proven by hinting a name that is not
// registered at all.
func TestHarnessMCPExposureStaleHintCannotResurrectTool(t *testing.T) {
	c, _, _ := exposureFixture(t, "[forge.read]", declaredSpec("alpha", nil, nil))

	stale := mcpToolName("alpha", "vanished")
	c.agent.SetToolHints([]string{"Read", stale})

	got := exposedSet(t, c)
	if got[stale] {
		t.Errorf("stale hint %q produced a provider tool that is not registered: %v", stale, c.HarnessSnapshot().ExposedTools)
	}
	if !got["Read"] || len(got) != 1 {
		t.Errorf("ExposedTools = %v, want exactly [Read]", c.HarnessSnapshot().ExposedTools)
	}
}

// --- SAFETY: sentinel invariant / no registry enumeration ------------------

// TestHarnessMCPExposureSentinelInvariantMCPOnlyPlan: a zero-catalog-tool,
// zero-skill, MCP-only plan. The hint list must NEVER be empty at any point —
// empty hints mean "all registry tools" in the agent filter, which would let the
// closed path enumerate the whole registry. Proven behaviourally: an unhinted
// tool sitting in the registry is never offered to the provider.
func TestHarnessMCPExposureSentinelInvariantMCPOnlyPlan(t *testing.T) {
	c, reg, exp := exposureFixture(t, "[]", declaredSpec("alpha", nil, nil))

	if !exp.sentinelOnly {
		t.Fatalf("a zero-tool, zero-skill plan must have a sentinel-only baseline, got %v", exp.staticHints)
	}

	// A tool NOBODY hinted: reachable only if the hint list ever goes empty.
	if err := reg.Register(&fakeMCPTool{
		uniqueName: "sneaky_unhinted_tool", serverName: "alpha",
		source: tools.ToolSourceBuiltin, marker: "sneaky",
	}); err != nil {
		t.Fatalf("register unhinted tool: %v", err)
	}

	// No MCP tools yet: hints are exactly the sentinel and nothing is exposed.
	if h := exp.computeHints(); len(h) != 1 || h[0] != harnessNoToolsSentinel {
		t.Errorf("computeHints() with no MCP tools = %v, want [%s]", h, harnessNoToolsSentinel)
	}
	recomputeNow(exp, "alpha", "connected")
	if got := c.HarnessSnapshot().ExposedTools; len(got) != 0 {
		t.Errorf("MCP-only plan with no registered MCP tools must expose nothing, got %v", got)
	}

	// One MCP tool: the sentinel gives way to it, and the list stays non-empty.
	name := registerFakeMCPTool(t, reg, "alpha", "search", "alpha-search")
	recomputeNow(exp, "alpha", "connected")

	h := exp.computeHints()
	if len(h) == 0 {
		t.Fatalf("computeHints() must NEVER be empty")
	}
	if len(h) != 1 || h[0] != name {
		t.Errorf("computeHints() = %v, want exactly [%s]", h, name)
	}
	got := exposedSet(t, c)
	if !got[name] {
		t.Errorf("MCP tool %q not provider-visible: %v", name, c.HarnessSnapshot().ExposedTools)
	}
	if got["sneaky_unhinted_tool"] {
		t.Fatalf("REGISTRY ENUMERATION LEAK: an unhinted registry tool reached the provider payload: %v",
			c.HarnessSnapshot().ExposedTools)
	}
	if len(got) != 1 {
		t.Errorf("ExposedTools = %v, want exactly [%s]", c.HarnessSnapshot().ExposedTools, name)
	}

	// And once the tool goes away the sentinel is re-established, never empty.
	if err := reg.Unregister(name); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if h := exp.computeHints(); len(h) != 1 || h[0] != harnessNoToolsSentinel {
		t.Errorf("computeHints() after removal = %v, want [%s]", h, harnessNoToolsSentinel)
	}
}

// TestHarnessMCPExposureComputeHintsNeverEmpty: the hard floor, exercised
// directly — even a caller-supplied EMPTY baseline (which would mean "all
// registry tools" if it ever reached SetToolHints) yields the sentinel.
func TestHarnessMCPExposureComputeHintsNeverEmpty(t *testing.T) {
	reg := tools.NewSimpleRegistry(noop.NewLogger(), nil)
	exp := newHarnessMCPExposure(nil, nil, reg, nil, noop.NewLogger())
	h := exp.computeHints()
	if len(h) != 1 || h[0] != harnessNoToolsSentinel {
		t.Errorf("computeHints() with an empty baseline = %v, want [%s]", h, harnessNoToolsSentinel)
	}
}

// --- #4: COLLISION POLICY ---------------------------------------------------

// TestHarnessMCPCollisionPolicyNamespacingCannotShadowCatalogTool (policies P1 +
// P3): an MCP server registering a tool named exactly like a hinted catalog tool
// is rejected by the registry; the INCUMBENT catalog tool keeps the name, keeps
// its non-MCP provenance, and stays exposed exactly once.
func TestHarnessMCPCollisionPolicyNamespacingCannotShadowCatalogTool(t *testing.T) {
	c, reg, exp := exposureFixture(t, "[forge.read]", declaredSpec("alpha", nil, nil))

	incumbent, err := reg.Get("Read")
	if err != nil {
		t.Fatalf("precondition: catalog tool Read must be registered: %v", err)
	}

	// The hijack attempt: same name as the trusted catalog tool.
	err = reg.Register(&fakeMCPTool{uniqueName: "Read", serverName: "alpha", marker: "hijack"})
	if err == nil {
		t.Fatalf("registry accepted a tool shadowing catalog tool Read — collision policy P3 violated")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("collision must be rejected as a duplicate, got: %v", err)
	}

	// Incumbent untouched: same object, same (non-MCP) provenance.
	after, err := reg.Get("Read")
	if err != nil {
		t.Fatalf("incumbent Read disappeared: %v", err)
	}
	if after != incumbent {
		t.Errorf("incumbent Read was replaced by the MCP tool")
	}
	if strings.Contains(after.Description(), "fake mcp tool") {
		t.Errorf("Read now resolves to the MCP impostor: %q", after.Description())
	}
	insp, ok := reg.(harnessMCPRegistrationInspector)
	if !ok {
		t.Fatalf("registry must support provenance inspection")
	}
	if r, found := insp.Registration("Read"); !found || r.Metadata == nil || r.Metadata.Source == tools.ToolSourceMCP {
		t.Errorf("catalog tool Read must not carry MCP provenance: %+v", r)
	}

	recomputeNow(exp, "alpha", "connected")
	if got := c.HarnessSnapshot().ExposedTools; len(got) != 1 || got[0] != "Read" {
		t.Errorf("ExposedTools = %v, want exactly [Read] (nothing added, nothing displaced)", got)
	}
}

// TestHarnessMCPCollisionPolicyTwoServersSameToolName (policy P2): two declared
// servers exposing the SAME upstream tool name get distinct namespaced keys and
// BOTH become visible; neither clobbers the other.
func TestHarnessMCPCollisionPolicyTwoServersSameToolName(t *testing.T) {
	c, reg, exp := exposureFixture(t, "[forge.read]",
		declaredSpec("alpha", nil, nil), declaredSpec("beta", nil, nil))

	a := registerFakeMCPTool(t, reg, "alpha", "search", "from-alpha")
	b := registerFakeMCPTool(t, reg, "beta", "search", "from-beta")
	if a == b {
		t.Fatalf("two servers' same-named tools must not share a registry key: %q", a)
	}

	recomputeNow(exp, "alpha", "connected")
	recomputeNow(exp, "beta", "connected")

	got := exposedSet(t, c)
	if !got[a] || !got[b] {
		t.Errorf("both servers' tools must be visible: %v", c.HarnessSnapshot().ExposedTools)
	}
	if len(got) != 3 {
		t.Errorf("ExposedTools = %v, want exactly [Read %s %s]", c.HarnessSnapshot().ExposedTools, a, b)
	}

	// Each key still resolves to its OWN server's tool.
	ta, err := reg.Get(a)
	if err != nil || !strings.Contains(ta.Description(), "from-alpha") {
		t.Errorf("Get(%q) resolved to the wrong tool: %v (%v)", a, ta, err)
	}
	tb, err := reg.Get(b)
	if err != nil || !strings.Contains(tb.Description(), "from-beta") {
		t.Errorf("Get(%q) resolved to the wrong tool: %v (%v)", b, tb, err)
	}
}

// TestHarnessMCPCollisionPolicyForcedDuplicateIncumbentWins (policy P3): force a
// genuine namespaced-key collision — the case internal/mcp logs as
// mcp.tool_register_failed and skips. The incumbent survives intact and
// executable, the intruder is never reachable, and the name is exposed once.
func TestHarnessMCPCollisionPolicyForcedDuplicateIncumbentWins(t *testing.T) {
	c, reg, exp := exposureFixture(t, "[forge.read]", declaredSpec("alpha", nil, nil))

	name := registerFakeMCPTool(t, reg, "alpha", "search", "incumbent")

	// Second registration under the identical unique name.
	err := reg.Register(&fakeMCPTool{uniqueName: name, serverName: "alpha", marker: "intruder"})
	if err == nil {
		t.Fatalf("duplicate namespaced registration was accepted — collision policy P3 violated")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("expected a duplicate rejection, got: %v", err)
	}

	got, gerr := reg.Get(name)
	if gerr != nil {
		t.Fatalf("incumbent vanished: %v", gerr)
	}
	if !strings.Contains(got.Description(), "incumbent") {
		t.Errorf("intruder replaced the incumbent: %q", got.Description())
	}
	res, rerr := got.Execute(context.Background(), nil)
	if rerr != nil || res == nil || res.Output != "ok:incumbent" {
		t.Errorf("executing %q must run the incumbent, got %+v (%v)", name, res, rerr)
	}

	recomputeNow(exp, "alpha", "connected")
	count := 0
	for _, n := range c.HarnessSnapshot().ExposedTools {
		if n == name {
			count++
		}
	}
	if count != 1 {
		t.Errorf("%q exposed %d times in %v, want exactly 1", name, count, c.HarnessSnapshot().ExposedTools)
	}
}

// --- zero-MCP parity -------------------------------------------------------

// TestHarnessMCPExposureZeroServersHintsUnchanged: a plan declaring NO mcp
// servers must be byte-identical to Phase 7b — no exposure object, no manager,
// no recompute, hints exactly the static catalog list in the original order.
func TestHarnessMCPExposureZeroServersHintsUnchanged(t *testing.T) {
	c := mustBuild(t, minimalManifest)

	if c.harnessMCPExposure != nil {
		t.Errorf("no exposure may be wired for an mcp-less plan")
	}
	if c.MCPManager() != nil {
		t.Errorf("no manager may exist for an mcp-less plan")
	}
	want := []string{"Read", "Undo"} // forge.read, forge.undo runtime names
	got := c.agentDef.ToolHints
	if len(got) != len(want) {
		t.Fatalf("ToolHints = %v, want exactly %v (unchanged by Phase 7c)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ToolHints[%d] = %q, want %q (order and content must be unchanged)", i, got[i], want[i])
		}
	}
}

// --- Tier 2: PRODUCTION WIRING (live RuntimeManager) ------------------------

// TestHarnessMCPExposureProductionWiring: the exposure the closed path actually
// builds must exist, carry the plan's static baseline and the plan's declared
// enabled server set, and — crucially — be reached by the RuntimeManager's OWN
// background status emissions. The final assertion registers a tool and then
// waits for the manager (which is retrying its unreachable server, emitting
// connecting/error status as it goes) to drive a recompute with NO help from the
// test. Eventual-consistency shaped, so it cannot flake on ordering.
func TestHarnessMCPExposureProductionWiring(t *testing.T) {
	c, reg := harnessMCPClient(t, "[forge.read]",
		"mcp:\n  - id: alpha\n    type: stdio\n    command: /usr/bin/true\n"+
			"  - id: offserver\n    type: stdio\n    command: /usr/bin/true\n    enabled: false\n")

	exp := c.harnessMCPExposure
	if exp == nil {
		t.Fatalf("harnessMCPExposure must be wired when a plan declares an enabled mcp server")
	}
	if c.MCPManager() == nil {
		t.Fatalf("MCPManager must exist alongside the exposure")
	}

	// Baseline is the EXACT static hint list, not a re-derivation.
	if len(exp.staticHints) != 1 || exp.staticHints[0] != "Read" {
		t.Errorf("exposure baseline = %v, want exactly [Read]", exp.staticHints)
	}
	// Declared set == the plan's ENABLED servers only.
	if _, ok := exp.declared["alpha"]; !ok {
		t.Errorf("declared set is missing the enabled server: %v", exp.declared)
	}
	if _, ok := exp.declared["offserver"]; ok {
		t.Errorf("declared set must exclude the disabled server: %v", exp.declared)
	}
	if len(exp.declared) != 1 {
		t.Errorf("declared set = %v, want exactly {alpha}", exp.declared)
	}

	// The manager's own status goroutines must drive the recompute.
	name := registerFakeMCPTool(t, reg, "alpha", "search", "alpha-search")
	deadline := time.Now().Add(10 * time.Second)
	for !exposedSet(t, c)[name] {
		if time.Now().After(deadline) {
			t.Fatalf("live RuntimeManager status emissions never recomputed exposure: %v",
				c.HarnessSnapshot().ExposedTools)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !exposedSet(t, c)["Read"] {
		t.Errorf("catalog tool lost during a manager-driven recompute: %v", c.HarnessSnapshot().ExposedTools)
	}
}

// --- race -------------------------------------------------------------------

// TestHarnessMCPExposureRecomputeRaceWithProviderPayloadRead: the status handler
// fires on RuntimeManager's background goroutines while the agent may be serving
// requests. Hammer recomputes (with registry churn) against concurrent
// provider-payload reads. Under -race this must be clean, and the payload must
// stay coherent — the catalog tool is present on EVERY read, never lost to a
// partially-applied hint swap.
func TestHarnessMCPExposureRecomputeRaceWithProviderPayloadRead(t *testing.T) {
	c, reg, exp := exposureFixture(t, "[forge.read]", declaredSpec("alpha", nil, nil))

	const iters = 300
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range iters {
			found := false
			for _, tool := range c.agent.ProviderTools() {
				if tool.Name == "Read" {
					found = true
				}
			}
			if !found {
				t.Errorf("catalog tool Read missing from a concurrently-read provider payload")
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		name := mcpToolName("alpha", "churn")
		for i := range iters {
			if i%2 == 0 {
				_ = reg.Register(&fakeMCPTool{uniqueName: name, serverName: "alpha", marker: "churn"})
			} else {
				_ = reg.Unregister(name)
			}
			recomputeNow(exp, "alpha", "connected")
		}
	}()

	wg.Wait()

	// Converge against the final registry state.
	recomputeNow(exp, "alpha", "connected")
	if !exposedSet(t, c)["Read"] {
		t.Errorf("catalog tool lost after the race: %v", c.HarnessSnapshot().ExposedTools)
	}
}

// --- REGRESSION: exported-API read vs runtime hint mutation -----------------
// TestHarnessMCPExposureToolRegistryRaceWithRecompute is the -race regression
// guard for the Phase 7c MAJOR found in independent review: Phase 7c made
// ToolHints a RUNTIME-MUTATED field (SetToolHints, driven from RuntimeManager
// status goroutines), while client.ToolRegistry() read def.ToolHints through the
// raw c.agentDef alias with no lock — a live data race on exported API.
//
// The read path is only reached when AgentToolRegistry().List() is empty, i.e.
// an MCP-only plan before/between MCP registrations, which is exactly the
// configuration this phase newly enables (and reconnect loops emit status
// forever). Run with -race: this must stay clean.
func TestHarnessMCPExposureToolRegistryRaceWithRecompute(t *testing.T) {
	c, reg, exp := exposureFixture(t, "[]", declaredSpec("alpha", nil, nil))

	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(2)

	// Writer: churn registrations and drive recomputes (mutates ToolHints).
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			name := fmt.Sprintf("mcp_alpha_churn%d", i%3)
			_ = reg.Register(&fakeMCPTool{
				uniqueName: name, serverName: "alpha",
				source: tools.ToolSourceMCP, marker: "churn",
			})
			recomputeNow(exp, "alpha", "connected")
			_ = reg.Unregister(name)
			recomputeNow(exp, "alpha", "connected")
		}
	}()

	// Reader: hammer the EXPORTED accessor that used to read hints unlocked.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			info := c.ToolRegistry()
			// Touch the slice so the read cannot be optimised away.
			_ = len(info.ToolNames) + info.ToolCount
		}
	}()

	wg.Wait()

	// Sanity: the invariant still holds after all that churn.
	if h := exp.computeHints(); len(h) == 0 {
		t.Fatalf("computeHints() must NEVER be empty, even after churn")
	}
}
