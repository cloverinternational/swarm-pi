// Package builtin provides CronDelete tool for cancelling scheduled tasks.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// CronDeleteParams are the typed parameters for the CronDelete tool.
type CronDeleteParams struct {
	ID string `json:"id" description:"Task ID to cancel" required:"true"`
}

// CronDeleteTool cancels scheduled tasks.
type CronDeleteTool struct {
	tools.BaseTool

	scheduler *CronScheduler
	logger    observability.Logger
	tracer    observability.Tracer
}

// CronDeleteConfig configures the CronDelete tool.
type CronDeleteConfig struct {
	Scheduler *CronScheduler
	Logger    observability.Logger
	Tracer    observability.Tracer
}

// NewCronDeleteTool creates a new CronDelete tool.
func NewCronDeleteTool(config CronDeleteConfig) (tools.Tool, error) {
	if config.Scheduler == nil {
		return nil, sdkerr.Permanent("cron_delete.missing_scheduler", "scheduler is required")
	}
	if config.Logger == nil {
		config.Logger = noop.NewLogger()
	}
	if config.Tracer == nil {
		config.Tracer = noop.NewTracer()
	}

	t := &CronDeleteTool{
		scheduler: config.Scheduler,
		logger:    config.Logger,
		tracer:    config.Tracer,
	}
	return tools.Typed[CronDeleteParams](t), nil
}

// Name returns the tool name.
func (t *CronDeleteTool) Name() string {
	return "CronDelete"
}

// Description returns the tool description.
func (t *CronDeleteTool) Description() string {
	return "Cancel a scheduled cron job by ID. Removes it from the scheduler and from disk if it was a durable (persisted) task."
}

// IsIdempotent returns false since deletion has side effects.
func (t *CronDeleteTool) IsIdempotent() bool {
	return false
}

// OptimizationHints provides guidance for efficient tool use.
func (t *CronDeleteTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// RequiresPermission returns the required permissions for this tool.
func (t *CronDeleteTool) RequiresPermission() []tools.Permission {
	return nil
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *CronDeleteTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// SupportsParallel returns true because deletion is safe to execute concurrently.
func (t *CronDeleteTool) SupportsParallel() bool {
	return true
}

// Parameters returns the JSON schema for tool parameters.
func (t *CronDeleteTool) Parameters() any {
	return tools.SchemaFor[CronDeleteParams]()
}

// Execute executes the CronDelete tool by decoding rawParams into CronDeleteParams and calling Run.
func (t *CronDeleteTool) Execute(ctx context.Context, rawParams map[string]any) (*tools.ToolResult, error) {
	b, err := json.Marshal(rawParams)
	if err != nil {
		return nil, fmt.Errorf("cron_delete: marshal params: %w", err)
	}
	var p CronDeleteParams
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("cron_delete: unmarshal params: %w", err)
	}
	return t.Run(ctx, p)
}

// Run executes the CronDelete tool with typed parameters.
func (t *CronDeleteTool) Run(ctx context.Context, p CronDeleteParams) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "cron_delete.execute")
	defer span.End()

	if p.ID == "" {
		err := sdkerr.Permanent("cron_delete.missing_id", "id parameter is required")
		span.RecordError(err)
		return nil, err
	}

	span.SetAttribute("task_id", p.ID)

	t.logger.Info(ctx, "cron_delete.deleting",
		observability.F("task_id", p.ID))

	// Remove from scheduler
	task, err := t.scheduler.RemoveTask(p.ID)
	if err != nil {
		span.RecordError(err)
		t.logger.Warn(ctx, "cron_delete.not_found",
			observability.F("task_id", p.ID))
		return nil, err
	}

	t.logger.Info(ctx, "cron_delete.deleted",
		observability.F("task_id", p.ID),
		observability.F("cron", task.Cron),
		observability.F("durable", task.Durable))

	resultData := map[string]any{
		"id":      p.ID,
		"message": fmt.Sprintf("Cancelled scheduled task %s", p.ID),
	}

	resultJSON, _ := json.MarshalIndent(resultData, "", "  ")
	return &tools.ToolResult{Output: string(resultJSON)}, nil
}
