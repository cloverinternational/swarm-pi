package steeringtools

import (
	"context"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// HaltPeerLoopParams asks the A2A runtime to drop further inbound DMs from
// a specific peer for a bounded TTL.
type HaltPeerLoopParams struct {
	Peer       string `json:"peer" description:"Peer handle (e.g., 'pop-os-695063') whose inbound DMs should be muted" required:"true"`
	Reason     string `json:"reason" description:"Reason for halting the peer loop (visible to the user via audit)" required:"true"`
	TTLSeconds int    `json:"ttl_seconds,omitempty" description:"How long to mute inbound DMs from this peer. Defaults to 60s, hard-capped to 600s by the runtime."`
}

// HaltPeerLoopTool prepares to mute inbound DMs from a peer for a bounded
// TTL. Phase 1: records intent only. Phase 2 wires this to A2ARuntime.
type HaltPeerLoopTool struct{ tools.BaseTool }

// NewHaltPeerLoopTool constructs the typed halt_peer_loop tool.
func NewHaltPeerLoopTool() tools.Tool { return tools.Typed[HaltPeerLoopParams](&HaltPeerLoopTool{}) }

// Name returns the tool name.
func (t *HaltPeerLoopTool) Name() string { return "halt_peer_loop" }

// Description tells the steering agent when to use this tool.
func (t *HaltPeerLoopTool) Description() string {
	return `Halt the peer-DM loop with a specific peer for a bounded time.

Use this when two agents are caught in a clearly unproductive ping-pong
(mutual mirroring, drift past the hard cap, etc.). This does NOT terminate
the subject agent — it only drops further inbound DMs from the named peer
for the requested TTL (default 60s, max 600s).`
}

// Parameters returns the JSON schema for the params struct.
func (t *HaltPeerLoopTool) Parameters() any { return tools.SchemaFor[HaltPeerLoopParams]() }

// Run records the halt on the SteeringTarget when present and clamps
// the TTL to [60s, 600s] before forwarding. The target also clamps,
// but doing it here keeps the tool-result metadata honest about what
// was actually requested.
func (t *HaltPeerLoopTool) Run(ctx context.Context, params HaltPeerLoopParams) (*tools.ToolResult, error) {
	ttl := params.TTLSeconds
	if ttl <= 0 {
		ttl = 60
	}
	if ttl > 600 {
		ttl = 600
	}
	if target, ok := agent.SteeringTargetFromContext(ctx); ok {
		_ = target.HaltPeerLoop(params.Peer, params.Reason, time.Duration(ttl)*time.Second)
	}
	result := tools.NewXMLResult(tools.NewXML("result").
		Attr("status", "ok").
		Attr("action", "halt_peer_loop"))
	result.Metadata["peer"] = params.Peer
	result.Metadata["reason"] = params.Reason
	result.Metadata["ttl_seconds"] = ttl
	return result, nil
}
