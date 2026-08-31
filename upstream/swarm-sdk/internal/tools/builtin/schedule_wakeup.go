// Package builtin provides ScheduleWakeup tool for scheduling one-shot wakeup tasks.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ScheduleWakeupParams are the typed parameters for the ScheduleWakeup tool.
type ScheduleWakeupParams struct {
	Prompt string `json:"prompt" description:"The prompt to execute on wakeup." required:"true"`
	Delay  string `json:"delay" description:"Delay before wakeup (e.g., '5m', '1h')." required:"true"`
}

// ScheduleWakeupTool schedules a one-shot wakeup task after a delay.
type ScheduleWakeupTool struct {
	tools.BaseTool

	scheduler *CronScheduler
	logger    observability.Logger
	tracer    observability.Tracer
}

// ScheduleWakeupConfig configures the ScheduleWakeup tool.
type ScheduleWakeupConfig struct {
	Scheduler *CronScheduler
	Logger    observability.Logger
	Tracer    observability.Tracer
}

// NewScheduleWakeupTool creates a new ScheduleWakeup tool.
func NewScheduleWakeupTool(config ScheduleWakeupConfig) (tools.Tool, error) {
	if config.Scheduler == nil {
		return nil, sdkerr.Permanent("schedule_wakeup.missing_scheduler", "scheduler is required")
	}
	if config.Logger == nil {
		config.Logger = noop.NewLogger()
	}
	if config.Tracer == nil {
		config.Tracer = noop.NewTracer()
	}

	t := &ScheduleWakeupTool{
		scheduler: config.Scheduler,
		logger:    config.Logger,
		tracer:    config.Tracer,
	}
	return tools.Typed[ScheduleWakeupParams](t), nil
}

// Name returns the tool name.
func (t *ScheduleWakeupTool) Name() string {
	return "ScheduleWakeup"
}

// Description returns the tool description.
func (t *ScheduleWakeupTool) Description() string {
	return "Schedule a prompt to run after a delay. Used for dynamic scheduling where the model decides when to resume."
}

// IsIdempotent returns false since creating schedules has side effects.
func (t *ScheduleWakeupTool) IsIdempotent() bool {
	return false
}

// OptimizationHints provides guidance for efficient tool use.
func (t *ScheduleWakeupTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// RequiresPermission returns the required permissions for this tool.
func (t *ScheduleWakeupTool) RequiresPermission() []tools.Permission {
	return nil
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *ScheduleWakeupTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// SupportsParallel returns true because scheduling is safe to execute concurrently.
func (t *ScheduleWakeupTool) SupportsParallel() bool {
	return true
}

// Parameters returns the JSON schema for tool parameters.
func (t *ScheduleWakeupTool) Parameters() any {
	return tools.SchemaFor[ScheduleWakeupParams]()
}

// Execute executes the ScheduleWakeup tool by decoding rawParams into ScheduleWakeupParams and calling Run.
func (t *ScheduleWakeupTool) Execute(ctx context.Context, rawParams map[string]any) (*tools.ToolResult, error) {
	b, err := json.Marshal(rawParams)
	if err != nil {
		return nil, fmt.Errorf("schedule_wakeup: marshal params: %w", err)
	}
	var p ScheduleWakeupParams
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("schedule_wakeup: unmarshal params: %w", err)
	}
	return t.Run(ctx, p)
}

// Run executes the ScheduleWakeup tool with typed parameters.
func (t *ScheduleWakeupTool) Run(ctx context.Context, p ScheduleWakeupParams) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "schedule_wakeup.execute")
	defer span.End()

	// Validate parameters
	if p.Prompt == "" {
		err := sdkerr.Permanent("schedule_wakeup.missing_prompt", "prompt parameter is required")
		span.RecordError(err)
		return nil, err
	}
	if p.Delay == "" {
		err := sdkerr.Permanent("schedule_wakeup.missing_delay", "delay parameter is required")
		span.RecordError(err)
		return nil, err
	}

	// Parse delay duration
	delay, err := time.ParseDuration(p.Delay)
	if err != nil {
		err := sdkerr.Permanent("schedule_wakeup.invalid_delay", fmt.Sprintf("invalid delay format: %s", p.Delay))
		span.RecordError(err)
		return nil, err
	}

	span.SetAttribute("delay", p.Delay)
	span.SetAttribute("delay_ms", delay.Milliseconds())

	// Calculate fire time
	fireTime := time.Now().Add(delay)

	// Generate task ID
	taskID := fmt.Sprintf("wakeup-%d", time.Now().UnixNano())

	// Create a one-shot scheduled task
	task := &ScheduledTask{
		ID:        taskID,
		Prompt:    p.Prompt,
		Cron:      fmt.Sprintf("%d %d %d %d *", fireTime.Minute(), fireTime.Hour(), fireTime.Day(), int(fireTime.Month())),
		Recurring: false, // One-shot
		Durable:   false, // Session-only
		CreatedAt: time.Now(),
	}

	// Add to scheduler
	if err := t.scheduler.AddTask(task); err != nil {
		span.RecordError(err)
		t.logger.Error(ctx, "schedule_wakeup.failed",
			observability.F("error", err.Error()))
		return nil, err
	}

	t.logger.Info(ctx, "loop.wakeup_scheduled",
		observability.F("task_id", taskID),
		observability.F("delay", p.Delay),
		observability.F("fire_time", fireTime.Format(time.RFC3339)))

	// Build result
	resultData := map[string]any{
		"id":        taskID,
		"delay":     p.Delay,
		"fire_time": fireTime.Format(time.RFC3339),
		"message":   fmt.Sprintf("Scheduled wakeup %s in %s (at %s).", taskID, p.Delay, fireTime.Format("15:04:05")),
	}

	resultJSON, _ := json.MarshalIndent(resultData, "", "  ")
	return &tools.ToolResult{Output: string(resultJSON)}, nil
}
