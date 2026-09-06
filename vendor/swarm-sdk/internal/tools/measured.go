package tools

import (
	"context"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolmetrics"
)

// measuredExecute runs tool.Execute with per-tool accounting attached.
//
// Every site in this package that invokes a tool's Execute goes through here
// so the heatmap cannot silently miss a path. There is deliberately NO
// decorator wrapping tools at registration time: Tool has 11 optional
// interfaces (StreamingTool, PermissionedTool, ValidatableTool, ...) and a
// wrapper would silently drop those type assertions, breaking streaming and
// permission enforcement.
func measuredExecute(ctx context.Context, tool Tool, name string, params map[string]any) (*ToolResult, error) {
	ctx, end := toolmetrics.Begin(ctx, name)
	defer end()

	allocBefore := toolmetrics.ReadAlloc()
	start := time.Now()

	result, err := tool.Execute(ctx, params)

	d := time.Since(start)

	if toolmetrics.DeepProfileEnabled() {
		if after := toolmetrics.ReadAlloc(); after > allocBefore {
			toolmetrics.RecordAlloc(name, after-allocBefore)
		}
	}

	toolmetrics.Record(name, d, ResultPayloadSize(result), isErrorResult(result, err))
	return SpillToolResult(ctx, name, result, err), err
}

// isErrorResult treats both a returned error and a result-flagged error as a
// failure. Several tools report failure via IsError while returning nil error,
// so keying only on err would undercount.
func isErrorResult(r *ToolResult, err error) bool {
	if err != nil {
		return true
	}
	return r != nil && r.IsError
}

// ResultPayloadSize reports the byte size a tool result hands downstream.
//
// Binary content blocks (images, PDFs, audio) are counted because they are
// typically far larger than Output and are what history persistence and
// provider serialization actually pay for.
func ResultPayloadSize(r *ToolResult) int {
	if r == nil {
		return 0
	}
	n := len(r.Output)
	for i := range r.Content {
		n += len(r.Content[i].Text)
		n += len(r.Content[i].Data)
	}
	return n
}
