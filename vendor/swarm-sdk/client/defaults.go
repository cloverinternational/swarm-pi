package client

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/forge"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/web_fetch"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/websearch"
)

// DefaultTools returns the standard swarm tool bundle scoped to workspace.
//
// The bundle is what an autonomous (non-interactive) agent typically needs:
//
//   - forge.DefaultTools(workspace) — file Read/Write/Patch, UnifiedGrep,
//     Undo, Shell, SemanticRename
//   - builtin.NewBashTool / NewAgentBrowserTool / NewListDirTool
//   - ii.GetProductivityTools — unified TaskManage batch tool
//   - web_fetch.New, websearch.New
//
// Project-memory tools are intentionally excluded: most one-shot consumers
// (sac, batch jobs, scripts) don't want persistent SQLite state created in
// the workspace. Use [WithProjectMemory] to opt in.
func DefaultTools(workspace string) []tools.Tool {
	return defaultToolsWithObservability(workspace, noop.NewLogger(), noop.NewTracer())
}

// defaultToolsWithObservability is the implementation; exposed for tests
// and for future variants that want custom observability without forcing
// every caller to assemble the bundle by hand.
func defaultToolsWithObservability(workspace string, logger observability.Logger, tracer observability.Tracer) []tools.Tool {
	out := make([]tools.Tool, 0, 16)
	out = append(out, forge.DefaultTools(workspace)...)
	out = append(out,
		builtin.NewBashTool(),
		builtin.NewAnnoyedTool(),
		builtin.NewAgentBrowserTool(),
		builtin.NewListDirTool(),
	)
	out = append(out, ii.GetProductivityTools()...)
	out = append(out,
		web_fetch.New(),
		websearch.New(),
	)
	return out
}
