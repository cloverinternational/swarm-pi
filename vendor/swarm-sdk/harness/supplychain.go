// Package harness — supply-chain introspection (plan.md Phase 11 action #8:
// "supply-chain policy for executable hooks/MCP packages").
//
// # Scope: package-level visibility, client-level opt-in enforcement
//
// This file adds exactly one read-only view over state two other files
// (hooks.go, mcp.go) already resolved and already expose via Plan.Hooks()/
// Plan.MCPServers(): which of a plan's authorized hooks and MCP servers
// execute LOCAL code. It adds no field to Plan, touches neither compile()
// nor resolve() in plan.go, and both exported functions below are pure
// post-processing — computing a SupplyChainReport (or checking
// HasExecutableSupplyChain) has no side effect and is safe to call on any
// compiled Plan, repeatedly, before any client is constructed from it.
//
// # Why this exists
//
// A `type: command`/`type: script` hook runs `sh -c <command>` (or a script
// file's content) with the full privileges of the host process
// (client/harness_hooks.go's harnessCommandHook.OnEvent). A `type: stdio`
// MCP server spawns McpServerSpec.Command as a local subprocess
// (client/harness_mcp.go). Both are genuine code-execution surfaces — the
// same trust level as running a shell script from an unknown source — yet
// nothing in the harness package itself previously surfaced that fact in one
// place a host could check BEFORE calling client.WithHarnessPlan. This file
// closes that visibility gap without coupling the harness package to a host
// policy. The client package provides the opt-in
// WithHarnessRequireExecutableConsent gate described in
// harness/docs/SECURITY.md.
//
// # Zero secrets, by construction
//
// SupplyChainHookEntry never carries a resolved/inline command STRING for a
// `type == command` hook — only HookSpec.CommandHash, exactly like
// Plan.Hooks() already guarantees (RevealHookCommand is the only path to the
// literal text, and this file never calls it). SupplyChainMCPEntry's
// Command/Args ARE plain, deliberately: they are operational identifiers,
// not credentials, exactly as McpServerSpec's own doc comment states —
// mirroring, not inventing, an existing redaction decision.
package harness

// SupplyChainReport lists every plan-authorized capability that executes
// local code or a local subprocess. It exists so a host can decide — BEFORE
// constructing a client from this plan — whether it trusts the manifest's
// origin. It changes nothing: producing this report has no side effect and
// is safe to call on any compiled Plan.
//
// Both fields are omitempty-shaped (nil, not an empty non-nil slice, when
// there is nothing to report) so a plan with no executable supply chain at
// all produces a report that is trivially "empty" under either a nil check
// or a JSON `omitempty` tag, matching the rest of this package's redacted
// report conventions (see ExplainReport).
type SupplyChainReport struct {
	Hooks []SupplyChainHookEntry `json:"hooks,omitempty"`
	MCP   []SupplyChainMCPEntry  `json:"mcp,omitempty"`
}

// SupplyChainHookEntry is one hook that executes local code when it fires.
// Only hooks with Type == "command" or Type == "script" are ever represented
// here — see ExecutableSupplyChain.
type SupplyChainHookEntry struct {
	// ID is HookSpec.ID.
	ID string `json:"id"`
	// Event is HookSpec.Event.
	Event string `json:"event"`
	// Scope is HookSpec.Scope (global|interface|agent|tool).
	Scope string `json:"scope"`
	// Kind is "command" or "script" — the two EXECUTING hook types. It is
	// never "http": an http hook is a network call, not local execution, and
	// http hooks never reach this slice (see ExecutableSupplyChain).
	Kind string `json:"kind"`
	// CommandHash is HookSpec.CommandHash for Kind == "command", or
	// HookSpec.ContentHash for Kind == "script". Either way it is a
	// "sha256:<hex>" digest, NEVER the literal command/script text — the same
	// guarantee Plan.Hooks() already makes. See TestSupplyChainReportNeverLeaksHookCommandSecret.
	CommandHash string `json:"commandHash,omitempty"`
}

// SupplyChainMCPEntry is one MCP server that spawns a local subprocess when
// started. Only servers with Type == "stdio" are ever represented here — see
// ExecutableSupplyChain.
type SupplyChainMCPEntry struct {
	// ID is McpServerSpec.ID.
	ID string `json:"id"`
	// Command is McpServerSpec.Command, shown plainly (it is an operational
	// identifier, not a credential — see McpServerSpec's own doc comment,
	// which this file deliberately does not re-litigate).
	Command string `json:"command"`
	// Args is McpServerSpec.Args, shown plainly for the same reason.
	Args []string `json:"args,omitempty"`
}

// ExecutableSupplyChain reports every hook and MCP server on p that executes
// local code or a local subprocess. It is built entirely out of Plan.Hooks()
// and Plan.MCPServers() — both already redacted-safe — so this function adds
// no new category of information to what those two accessors already expose;
// it only filters and reshapes it.
//
// A hook is included iff its Type is "command" or "script" (the two
// EXECUTING hook types). A "http" hook is a network call, not local
// execution, and is deliberately excluded — conflating the two would turn
// every manifest with an outbound webhook into a false-positive "this plan
// runs arbitrary code" warning.
//
// An MCP server is included iff its Type is "stdio" (it spawns Command as a
// subprocess). A "http"/"sse" MCP server is a network client and is
// deliberately excluded for the identical reason.
func (p *Plan) ExecutableSupplyChain() SupplyChainReport {
	var report SupplyChainReport

	for _, h := range p.Hooks() {
		switch h.Type {
		case HookTypeCommand:
			report.Hooks = append(report.Hooks, SupplyChainHookEntry{
				ID:          h.ID,
				Event:       h.Event,
				Scope:       h.Scope,
				Kind:        HookTypeCommand,
				CommandHash: h.CommandHash,
			})
		case HookTypeScript:
			report.Hooks = append(report.Hooks, SupplyChainHookEntry{
				ID:          h.ID,
				Event:       h.Event,
				Scope:       h.Scope,
				Kind:        HookTypeScript,
				CommandHash: h.ContentHash,
			})
		}
		// HookTypeHTTP (and any future non-executing type) is deliberately
		// excluded — see the func doc.
	}

	for _, m := range p.MCPServers() {
		if m.Type != McpTypeStdio {
			// McpTypeHTTP / McpTypeSSE are network clients, not local
			// execution — deliberately excluded, see the func doc.
			continue
		}
		var args []string
		if m.Args != nil {
			args = append([]string(nil), m.Args...)
		}
		report.MCP = append(report.MCP, SupplyChainMCPEntry{
			ID:      m.ID,
			Command: m.Command,
			Args:    args,
		})
	}

	return report
}

// HasExecutableSupplyChain reports whether ExecutableSupplyChain would return
// at least one entry in either slice. It is meant to be cheap enough for a
// host to call on EVERY plan without ceremony (for example, before printing a
// one-line warning banner), so it walks the plan's own resolved hook/MCP
// specs directly instead of constructing (and discarding) a full
// SupplyChainReport — that report allocates a defensive copy of every hook
// and MCP server on the plan, which is unnecessary work for a caller that
// only wants a yes/no answer.
func (p *Plan) HasExecutableSupplyChain() bool {
	for _, h := range p.hooks {
		if h.Type == HookTypeCommand || h.Type == HookTypeScript {
			return true
		}
	}
	for _, m := range p.mcpServers {
		if m.Type == McpTypeStdio {
			return true
		}
	}
	return false
}
