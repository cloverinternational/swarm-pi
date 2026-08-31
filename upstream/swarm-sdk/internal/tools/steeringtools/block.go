package steeringtools

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// BlockNextToolParams asks the steering layer to reject the subject's next
// tool call, optionally restricted to a specific tool name.
type BlockNextToolParams struct {
	ToolName string `json:"tool_name,omitempty" description:"Optional tool name to block. If empty, blocks the very next tool call regardless of name."`
	Reason   string `json:"reason" description:"Reason shown to the subject when the tool call is blocked" required:"true"`
}

// BlockNextToolTool prepares to block the subject agent's next tool call via
// the hooks layer. Phase 1: records intent only.
type BlockNextToolTool struct{ tools.BaseTool }

// NewBlockNextToolTool constructs the typed block_next_tool tool.
func NewBlockNextToolTool() tools.Tool {
	return tools.Typed[BlockNextToolParams](&BlockNextToolTool{})
}

// Name returns the tool name.
func (t *BlockNextToolTool) Name() string { return "block_next_tool" }

// Description tells the steering agent when to use this tool.
func (t *BlockNextToolTool) Description() string {
	return `Block the subject agent's next tool call (or next call of a named tool).

Use this when the next intended tool call would be wasteful, unsafe, or
clearly off-task. The block is one-shot: only the next matching call is
rejected. The subject sees the reason and can choose a different action.`
}

// Parameters returns the JSON schema for the params struct.
func (t *BlockNextToolTool) Parameters() any { return tools.SchemaFor[BlockNextToolParams]() }

// Run arms the block on the SteeringTarget when one is present in ctx;
// otherwise falls back to the Phase-1 stub response so the tool remains
// safe to invoke in tests that don't supply a target.
func (t *BlockNextToolTool) Run(ctx context.Context, params BlockNextToolParams) (*tools.ToolResult, error) {
	if target, ok := agent.SteeringTargetFromContext(ctx); ok {
		target.ArmBlockNext(params.ToolName, params.Reason)
	}
	result := tools.NewXMLResult(tools.NewXML("result").
		Attr("status", "ok").
		Attr("action", "block_next_tool"))
	if params.ToolName != "" {
		result.Metadata["tool_name"] = params.ToolName
	}
	result.Metadata["reason"] = params.Reason
	return result, nil
}
