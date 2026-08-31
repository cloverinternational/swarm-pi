// Package steeringtools implements the tool surface of the long-lived
// steering agent (see docs/steering-redesign/steering-redesign.pdf).
//
// # PHASE 1 SCOPE
//
// All seven tools are stubs that return a structured "ok" response and do
// NOT perform any subject-side effect. Phase 2 wires them through the
// StreamingSteeringDriver to actually mutate the subject's system prompt,
// gate the next tool call, halt peer loops, etc. The schemas are stable
// from Phase 1 onward so the steering agent's prompt can be authored
// against this surface immediately.
//
// Each tool follows the swarm-sdk idiom in tools/builtin/annoyed.go:
//
//   - typed Params struct with JSON Schema tags
//   - tools.Typed[Params](&FooTool{}) constructor
//   - Run(ctx, params) (*tools.ToolResult, error) method
//
// The package exports All() so callers can register the entire surface in
// one call.
package steeringtools

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// All returns the steering-agent tool surface in canonical order. Callers
// typically pass this to a tools.Registry.Register loop when constructing a
// streaming steering agent.
func All() []tools.Tool {
	return []tools.Tool{
		NewObserveOnlyTool(),
		NewInjectSystemNoteTool(),
		NewRefocusTool(),
		NewBlockNextToolTool(),
		NewHaltPeerLoopTool(),
		NewAskUserTool(),
		NewLogConcernTool(),
	}
}
