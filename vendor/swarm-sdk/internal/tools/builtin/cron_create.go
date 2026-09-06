// Package builtin provides CronCreate tool for scheduling recurring agent tasks.
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
	"github.com/robfig/cron/v3"
)

// CronCreateParams are the typed parameters for the CronCreate tool.
type CronCreateParams struct {
	Prompt    string `json:"prompt" description:"The prompt to execute on the scheduled agent." required:"true"`
	Cron      string `json:"cron" description:"Cron expression (5-field: minute hour day-of-month month day-of-week). Examples: '*/5 * * * *' every 5min, '0 9 * * 1-5' weekdays at 9am, '0 0 1 * *' monthly." required:"true"`
	Recurring bool   `json:"recurring,omitempty" description:"Recurring job? (default: false for one-shot)"`
	Durable   bool   `json:"durable,omitempty" description:"Persist to disk? (default: false, session-only)"`
	AgentID   string `json:"agent_id,omitempty" description:"Specific agent to use for execution"`
}

// CronCreateTool creates scheduled recurring or one-shot agent tasks.
type CronCreateTool struct {
	tools.BaseTool

	scheduler *CronScheduler
	logger    observability.Logger
	tracer    observability.Tracer
}

// CronCreateConfig configures the CronCreate tool.
type CronCreateConfig struct {
	Scheduler *CronScheduler
	Logger    observability.Logger
	Tracer    observability.Tracer
}

// NewCronCreateTool creates a new CronCreate tool.
func NewCronCreateTool(config CronCreateConfig) (tools.Tool, error) {
	if config.Scheduler == nil {
		return nil, sdkerr.Permanent("cron_create.missing_scheduler", "scheduler is required")
	}
	if config.Logger == nil {
		config.Logger = noop.NewLogger()
	}
	if config.Tracer == nil {
		config.Tracer = noop.NewTracer()
	}

	t := &CronCreateTool{
		scheduler: config.Scheduler,
		logger:    config.Logger,
		tracer:    config.Tracer,
	}
	return tools.Typed[CronCreateParams](t), nil
}

// Name returns the tool name.
func (t *CronCreateTool) Name() string {
	return "CronCreate"
}

// Description returns the tool description.
func (t *CronCreateTool) Description() string {
	return "Schedule a prompt to run at a future time - either recurring on a cron schedule, or once at a specific time. " +
		"Uses standard 5-field cron. Pass durable: true to persist to disk; otherwise session-only."
}

// IsIdempotent returns false since creating schedules has side effects.
func (t *CronCreateTool) IsIdempotent() bool {
	return false
}

// OptimizationHints provides guidance for efficient tool use.
func (t *CronCreateTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// RequiresPermission returns the required permissions for this tool.
func (t *CronCreateTool) RequiresPermission() []tools.Permission {
	return nil
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *CronCreateTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// SupportsParallel returns true because scheduling is safe to execute concurrently.
func (t *CronCreateTool) SupportsParallel() bool {
	return true
}

// Parameters returns the JSON schema for tool parameters.
func (t *CronCreateTool) Parameters() any {
	return tools.SchemaFor[CronCreateParams]()
}

// Execute executes the CronCreate tool by decoding rawParams into CronCreateParams and calling Run.
func (t *CronCreateTool) Execute(ctx context.Context, rawParams map[string]any) (*tools.ToolResult, error) {
	b, err := json.Marshal(rawParams)
	if err != nil {
		return nil, fmt.Errorf("cron_create: marshal params: %w", err)
	}
	var p CronCreateParams
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("cron_create: unmarshal params: %w", err)
	}
	return t.Run(ctx, p)
}

// Run executes the CronCreate tool with typed parameters.
func (t *CronCreateTool) Run(ctx context.Context, p CronCreateParams) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "cron_create.execute")
	defer span.End()

	// Validate parameters
	if p.Prompt == "" {
		err := sdkerr.Permanent("cron_create.missing_prompt", "prompt parameter is required")
		span.RecordError(err)
		return nil, err
	}
	if p.Cron == "" {
		err := sdkerr.Permanent("cron_create.missing_cron", "cron parameter is required")
		span.RecordError(err)
		return nil, err
	}

	// Validate cron expression
	if !isValidCron(p.Cron) {
		err := sdkerr.Permanent("cron_create.invalid_cron", "invalid cron expression: "+p.Cron)
		span.RecordError(err)
		return nil, err
	}

	span.SetAttribute("cron", p.Cron)
	span.SetAttribute("recurring", p.Recurring)
	span.SetAttribute("durable", p.Durable)

	t.logger.Info(ctx, "cron_create.creating",
		observability.F("cron", p.Cron),
		observability.F("recurring", p.Recurring),
		observability.F("durable", p.Durable))

	// Generate task ID
	taskID := generateTaskID()

	// Create the scheduled task
	task := &ScheduledTask{
		ID:        taskID,
		Prompt:    p.Prompt,
		Cron:      p.Cron,
		Recurring: p.Recurring,
		Durable:   p.Durable,
		CreatedAt: time.Now(),
		AgentID:   p.AgentID,
	}

	// Add to scheduler
	if err := t.scheduler.AddTask(task); err != nil {
		span.RecordError(err)
		t.logger.Error(ctx, "cron_create.failed",
			observability.F("error", err.Error()))
		return nil, err
	}

	t.logger.Info(ctx, "cron_create.created",
		observability.F("task_id", taskID),
		observability.F("cron", p.Cron))

	// Build result
	typeStr := "one-shot"
	if p.Recurring {
		typeStr = "recurring"
	}

	persistStr := "session-only"
	if p.Durable {
		persistStr = "persisted"
	}

	resultData := map[string]any{
		"id":             taskID,
		"cron":           p.Cron,
		"human_schedule": humanReadableCron(p.Cron),
		"type":           typeStr,
		"persistence":    persistStr,
		"message":        fmt.Sprintf("Scheduled %s task %s (%s). %s.", typeStr, taskID, humanReadableCron(p.Cron), persistStr),
	}

	resultJSON, _ := json.MarshalIndent(resultData, "", "  ")
	return &tools.ToolResult{Output: string(resultJSON)}, nil
}

// isValidCron performs basic validation of a cron expression.
// For now, accepts any non-empty string. Full cron validation can be added.
func isValidCron(cronExpr string) bool {
	if cronExpr == "" {
		return false
	}
	// Use robfig/cron/v3 ParseStandard for real validation
	_, err := cron.ParseStandard(cronExpr)
	return err == nil
}

// generateTaskID generates a unique task ID.
func generateTaskID() string {
	return fmt.Sprintf("task-%d", time.Now().UnixNano())
}

// humanReadableCron converts a cron expression to human-readable text.
// TODO: Implement proper cron-to-text conversion
func humanReadableCron(cron string) string {
	// Simple mappings for common patterns
	switch cron {
	case "*/1 * * * *":
		return "every minute"
	case "*/5 * * * *":
		return "every 5 minutes"
	case "*/15 * * * *":
		return "every 15 minutes"
	case "*/30 * * * *":
		return "every 30 minutes"
	case "0 * * * *":
		return "every hour"
	case "0 */2 * * *":
		return "every 2 hours"
	case "0 0 * * *":
		return "daily at midnight"
	case "0 0 */1 * *":
		return "daily at midnight"
	case "0 9 * * 1-5":
		return "weekdays at 9am"
	case "0 9 * * *":
		return "daily at 9am"
	default:
		return cron
	}
}
