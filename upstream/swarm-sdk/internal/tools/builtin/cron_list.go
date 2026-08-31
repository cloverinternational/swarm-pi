// Package builtin provides CronList tool for listing scheduled tasks.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// CronListParams are the typed parameters for the CronList tool.
// No parameters needed - lists all scheduled tasks.
type CronListParams struct {
	// Empty - no parameters required
}

// CronListTool lists all scheduled cron tasks.
type CronListTool struct {
	tools.BaseTool

	scheduler *CronScheduler
	logger    observability.Logger
	tracer    observability.Tracer
}

// CronListConfig configures the CronList tool.
type CronListConfig struct {
	Scheduler *CronScheduler
	Logger    observability.Logger
	Tracer    observability.Tracer
}

// NewCronListTool creates a new CronList tool.
func NewCronListTool(config CronListConfig) (tools.Tool, error) {
	if config.Scheduler == nil {
		return nil, sdkerr.Permanent("cron_list.missing_scheduler", "scheduler is required")
	}
	if config.Logger == nil {
		config.Logger = noop.NewLogger()
	}
	if config.Tracer == nil {
		config.Tracer = noop.NewTracer()
	}

	t := &CronListTool{
		scheduler: config.Scheduler,
		logger:    config.Logger,
		tracer:    config.Tracer,
	}
	return tools.Typed[CronListParams](t), nil
}

// Name returns the tool name.
func (t *CronListTool) Name() string {
	return "CronList"
}

// Description returns the tool description.
func (t *CronListTool) Description() string {
	return "List all scheduled cron jobs. Shows task ID, cron expression, schedule type (recurring/one-shot), and persistence mode (durable/session-only)."
}

// IsIdempotent returns true since listing is read-only.
func (t *CronListTool) IsIdempotent() bool {
	return true
}

// OptimizationHints provides guidance for efficient tool use.
func (t *CronListTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// RequiresPermission returns the required permissions for this tool.
func (t *CronListTool) RequiresPermission() []tools.Permission {
	return nil
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *CronListTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// SupportsParallel returns true because listing is safe to execute concurrently.
func (t *CronListTool) SupportsParallel() bool {
	return true
}

// Parameters returns the JSON schema for tool parameters.
func (t *CronListTool) Parameters() any {
	return tools.SchemaFor[CronListParams]()
}

// Execute executes the CronList tool by decoding rawParams into CronListParams and calling Run.
func (t *CronListTool) Execute(ctx context.Context, rawParams map[string]any) (*tools.ToolResult, error) {
	var p CronListParams
	return t.Run(ctx, p)
}

// Run executes the CronList tool with typed parameters.
func (t *CronListTool) Run(ctx context.Context, p CronListParams) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "cron_list.execute")
	defer span.End()

	t.logger.Info(ctx, "cron_list.listing")

	tasks := t.scheduler.ListTasks()

	span.SetAttribute("task_count", len(tasks))

	// Build task info list
	taskInfos := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		typeStr := "one-shot"
		if task.Recurring {
			typeStr = "recurring"
		}

		persistStr := "session"
		if task.Durable {
			persistStr = "durable"
		}

		taskInfo := map[string]any{
			"id":             task.ID,
			"cron":           task.Cron,
			"human_schedule": humanReadableCron(task.Cron),
			"type":           typeStr,
			"persistence":    persistStr,
		}

		if task.AgentID != "" {
			taskInfo["agent_id"] = task.AgentID
		}

		taskInfos = append(taskInfos, taskInfo)
	}

	// Build output
	var output strings.Builder

	if len(tasks) == 0 {
		output.WriteString("No scheduled tasks.\n")
	} else {
		output.WriteString(fmt.Sprintf("Scheduled Tasks (%d total):\n\n", len(tasks)))

		for _, info := range taskInfos {
			output.WriteString(fmt.Sprintf("  %s: %s (%s, %s)\n",
				info["id"],
				info["human_schedule"],
				info["type"],
				info["persistence"]))
			output.WriteString(fmt.Sprintf("    Cron: %s\n", info["cron"]))
			if agentID, ok := info["agent_id"]; ok {
				output.WriteString(fmt.Sprintf("    Agent: %s\n", agentID))
			}
			output.WriteString("\n")
		}
	}

	resultData := map[string]any{
		"total_tasks": len(tasks),
		"tasks":       taskInfos,
	}

	resultJSON, _ := json.MarshalIndent(resultData, "", "  ")

	return &tools.ToolResult{
		Output: output.String() + "\n" + string(resultJSON),
	}, nil
}
