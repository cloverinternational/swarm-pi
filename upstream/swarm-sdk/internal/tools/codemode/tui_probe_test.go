// Package codemode_test — TUI integration probes.
//
// These tests verify the three behaviours fixed in the TUI codemode wiring:
//
//	Probe 1 — effective registry: after Install(), the wrapped registry exposes
//	           run_code and hides sandboxed tools; the raw registry is unchanged.
//
//	Probe 2 — toggle simulation: switching effectiveRegistry between the wrapped
//	           and raw registries (as done by RecreateAgent) correctly changes
//	           which tools are visible to callers of GetToolRegistry().
//
//	Probe 3 — dynamic refresh: registering a new tool into the raw registry AFTER
//	           Install() causes its JS stub to appear in run_code immediately.
package codemode_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/codemode"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func makeSimpleTool(name, desc string) tools.Tool {
	return &mockTool{name: name, description: desc}
}

func listContains(list []string, target string) bool {
	return slices.Contains(list, target)
}

// installCodeMode mirrors what the TUI's NewSDKIntegrationWithOptions does:
// install explicitly so the caller can capture the wrapped registry.
func installCodeMode(t *testing.T, reg tools.Registry) tools.Registry {
	t.Helper()
	cm := codemode.NewFromConfig(&codemode.Config{Enabled: true, Persist: true})
	exec := tools.NewExecutor(reg, nil)
	wrapped, err := cm.Install(reg, exec)
	if err != nil {
		t.Fatalf("codemode.Install failed: %v", err)
	}
	return wrapped
}

// ─── Probe 1: effective registry ──────────────────────────────────────────────

// TestProbe1_EffectiveRegistry verifies that after Install():
//   - the wrapped (effective) registry exposes run_code
//   - the wrapped registry hides sandboxed tools
//   - the raw registry is UNCHANGED and still exposes the original tools
func TestProbe1_EffectiveRegistry(t *testing.T) {
	rawReg := tools.NewRegistry()
	_ = rawReg.Register(makeSimpleTool("bash", "Run shell commands"))
	_ = rawReg.Register(makeSimpleTool("read_file", "Read a file"))
	_ = rawReg.Register(makeSimpleTool("write_file", "Write a file"))

	effectiveReg := installCodeMode(t, rawReg)

	t.Run("effective_registry_has_run_code", func(t *testing.T) {
		if !listContains(effectiveReg.List(), "run_code") {
			t.Errorf("effectiveRegistry.List() = %v — want run_code", effectiveReg.List())
		}
	})

	t.Run("effective_registry_hides_sandboxed_tools", func(t *testing.T) {
		for _, name := range []string{"bash", "read_file", "write_file"} {
			if listContains(effectiveReg.List(), name) {
				t.Errorf("effectiveRegistry.List() still contains %q — should be hidden by codemode", name)
			}
		}
	})

	t.Run("raw_registry_unchanged", func(t *testing.T) {
		for _, name := range []string{"bash", "read_file", "write_file"} {
			if !listContains(rawReg.List(), name) {
				t.Errorf("rawRegistry.List() lost %q — should be unmodified", name)
			}
		}
		if listContains(rawReg.List(), "run_code") {
			t.Error("rawRegistry.List() gained run_code — raw registry should be unmodified")
		}
	})

	t.Run("run_code_tool_is_callable", func(t *testing.T) {
		runCode, err := effectiveReg.Get("run_code")
		if err != nil {
			t.Fatalf("effectiveReg.Get(run_code) error: %v", err)
		}
		ctx := context.Background()
		result, err := runCode.Execute(ctx, map[string]any{
			"code": `return "probe1_ok";`,
		})
		if err != nil {
			t.Fatalf("run_code.Execute error: %v", err)
		}
		if result.IsError {
			t.Errorf("run_code returned error: %s", result.Output)
		}
	})
}

// ─── Probe 2: toggle simulation ───────────────────────────────────────────────

// TestProbe2_ToggleSimulation verifies the effectiveRegistry toggle pattern used
// by RecreateAgent(). Simulates: OFF → ON → OFF and checks the tool list at
// each stage matches expectations.
func TestProbe2_ToggleSimulation(t *testing.T) {
	rawReg := tools.NewRegistry()
	_ = rawReg.Register(makeSimpleTool("bash", "Run bash"))
	_ = rawReg.Register(makeSimpleTool("read_file", "Read file"))

	// Simulate the SDKIntegration.effectiveRegistry field.
	// Stage 0: codemode OFF — effectiveRegistry == rawRegistry
	var effectiveReg tools.Registry = rawReg

	t.Run("stage0_codemode_off", func(t *testing.T) {
		if listContains(effectiveReg.List(), "run_code") {
			t.Error("run_code should NOT be present when codemode is off")
		}
		if !listContains(effectiveReg.List(), "bash") {
			t.Error("bash should be present when codemode is off")
		}
	})

	// Stage 1: codemode ON — effectiveRegistry = wrapped
	effectiveReg = installCodeMode(t, rawReg)

	t.Run("stage1_codemode_on", func(t *testing.T) {
		if !listContains(effectiveReg.List(), "run_code") {
			t.Errorf("run_code should be present when codemode is on; list=%v", effectiveReg.List())
		}
		if listContains(effectiveReg.List(), "bash") {
			t.Error("bash should be HIDDEN when codemode is on (sandboxed)")
		}
		if listContains(effectiveReg.List(), "read_file") {
			t.Error("read_file should be HIDDEN when codemode is on (sandboxed)")
		}
	})

	// Stage 2: codemode OFF again — effectiveRegistry = rawRegistry
	effectiveReg = rawReg

	t.Run("stage2_codemode_off_again", func(t *testing.T) {
		if listContains(effectiveReg.List(), "run_code") {
			t.Error("run_code should NOT be present after toggling codemode off")
		}
		if !listContains(effectiveReg.List(), "bash") {
			t.Error("bash should be present again after toggling codemode off")
		}
		if !listContains(effectiveReg.List(), "read_file") {
			t.Error("read_file should be present again after toggling codemode off")
		}
	})
}

// ─── Probe 3: dynamic refresh ─────────────────────────────────────────────────

// TestProbe3_DynamicToolRefresh verifies that registering a tool into the raw
// registry AFTER Install() causes codeModeRegistry.Register() to rebuild the
// run_code stubs so the new tool is available inside the JS sandbox.
func TestProbe3_DynamicToolRefresh(t *testing.T) {
	rawReg := tools.NewRegistry()
	_ = rawReg.Register(makeSimpleTool("existing_tool", "Tool registered before install"))

	effectiveReg := installCodeMode(t, rawReg)

	// Verify existing_tool is in run_code at install time.
	t.Run("existing_tool_in_stubs_at_install", func(t *testing.T) {
		runCode, err := effectiveReg.Get("run_code")
		if err != nil {
			t.Fatalf("run_code not found: %v", err)
		}
		if !strings.Contains(runCode.Description(), "existing_tool") {
			t.Errorf("run_code description should mention existing_tool; got: %s", runCode.Description())
		}
	})

	// Now register a NEW tool into the raw registry (simulates an MCP server
	// connecting asynchronously after the agent was created).
	// The fix: codeModeRegistry.Register() calls refreshRunCode() automatically.
	newTool := &mockTool{
		name:        "mcp_dynamic_tool",
		description: "Tool added after codemode install (simulates async MCP connect)",
		fn: func(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
			return tools.NewToolResult("dynamic_result"), nil
		},
	}
	if err := effectiveReg.Register(newTool); err != nil {
		t.Fatalf("Register new tool failed: %v", err)
	}

	t.Run("dynamic_tool_appears_in_run_code_list", func(t *testing.T) {
		if !listContains(effectiveReg.List(), "run_code") {
			t.Error("run_code should still be present after dynamic registration")
		}
		// mcp_dynamic_tool should NOT be in List() — it's sandboxed.
		if listContains(effectiveReg.List(), "mcp_dynamic_tool") {
			t.Error("mcp_dynamic_tool should be hidden (sandboxed)")
		}
	})

	t.Run("dynamic_tool_stub_is_in_run_code_description", func(t *testing.T) {
		runCode, err := effectiveReg.Get("run_code")
		if err != nil {
			t.Fatalf("run_code not found after dynamic registration: %v", err)
		}
		desc := runCode.Description()
		if !strings.Contains(desc, "mcp_dynamic_tool") {
			t.Errorf("run_code description should mention mcp_dynamic_tool after dynamic registration;\ndescription: %s", desc)
		}
	})

	t.Run("dynamic_tool_is_callable_from_js", func(t *testing.T) {
		runCode, err := effectiveReg.Get("run_code")
		if err != nil {
			t.Fatalf("run_code not found: %v", err)
		}
		ctx := context.Background()
		result, err := runCode.Execute(ctx, map[string]any{
			"code": `
				const r = await mcp_dynamic_tool({});
				return r;
			`,
		})
		if err != nil {
			t.Fatalf("run_code.Execute error: %v", err)
		}
		if result.IsError {
			t.Errorf("run_code returned error calling mcp_dynamic_tool: %s", result.Output)
		}
		if !strings.Contains(result.Output+result.Metadata["result"].(string), "dynamic_result") {
			t.Errorf("expected dynamic_result in output, got output=%q result=%v",
				result.Output, result.Metadata["result"])
		}
	})
}
