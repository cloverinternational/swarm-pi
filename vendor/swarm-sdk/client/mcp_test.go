package client

import "testing"

// TestWithMCP_OptionSetsFields confirms the option stores the config path
// and flips the enable flag so initAgent knows to wire the manager.
func TestWithMCP_OptionSetsFields(t *testing.T) {
	o := options{}
	WithMCP("/tmp/custom/path")(&o)
	if !o.enableMCP {
		t.Errorf("enableMCP: expected true")
	}
	if o.mcpConfigPath != "/tmp/custom/path" {
		t.Errorf("mcpConfigPath: got %q", o.mcpConfigPath)
	}
}

// TestWithMCP_EmptyPathOptsIntoDefault confirms that WithMCP("") is still
// "yes, enable MCP" — the empty path signals "use the default XDG location"
// rather than "don't enable."
func TestWithMCP_EmptyPathOptsIntoDefault(t *testing.T) {
	o := options{}
	WithMCP("")(&o)
	if !o.enableMCP {
		t.Errorf("WithMCP(\"\") should still enable MCP")
	}
	if o.mcpConfigPath != "" {
		t.Errorf("path: got %q want empty (signals default)", o.mcpConfigPath)
	}
}

// TestMCPManager_NilWhenNotEnabled confirms a Client constructed without
// WithMCP has a nil MCP manager — callers can use the getter as an
// "is MCP enabled" probe.
func TestMCPManager_NilWhenNotEnabled(t *testing.T) {
	c := newTestClient()
	if c.MCPManager() != nil {
		t.Errorf("expected nil MCPManager on Client without WithMCP")
	}
}
