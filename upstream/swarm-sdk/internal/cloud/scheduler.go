package cloud

// Package cloud — scheduler CRUD.
// These methods expose the compute manager's /v1/schedules and /v1/triggers
// routes through the same authenticated cloud.Client used for sessions.
//
// Types are minimal mirrors of the compute-layer scheduler types so that
// callers do not need to import swarm-cloud directly.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ScheduleVariableMap is a string-to-string map for schedule message variables.
type ScheduleVariableMap map[string]string

// ScheduleTriggerType identifies the type of event trigger.
type ScheduleTriggerType string

const (
	ScheduleTriggerTypeWebhook   ScheduleTriggerType = "webhook"
	ScheduleTriggerTypeFile      ScheduleTriggerType = "file"
	ScheduleTriggerTypeGit       ScheduleTriggerType = "git"
	ScheduleTriggerTypeEvent     ScheduleTriggerType = "event"
	ScheduleTriggerTypeWebsocket ScheduleTriggerType = "websocket"
)

// CloudSchedule defines when a cloud agent should run.
// Mirrors swarm-cloud/internal/agents/compute/scheduler.Schedule.
type CloudSchedule struct {
	ID        string              `json:"id"`
	AgentID   string              `json:"agent_id"`
	ProjectID string              `json:"project_id"`
	Message   string              `json:"message"`
	Variables ScheduleVariableMap `json:"variables,omitempty"`
	// CronExpr is a standard 5-field cron expression, e.g. "0 9 * * 1".
	CronExpr string `json:"cron_expr,omitempty"`
	// Interval is a fixed interval alternative to CronExpr.
	Interval  time.Duration `json:"interval,omitempty"`
	StartTime time.Time     `json:"start_time"`
	EndTime   *time.Time    `json:"end_time,omitempty"`
	LastRun   *time.Time    `json:"last_run,omitempty"`
	NextRun   *time.Time    `json:"next_run,omitempty"`
	Enabled   bool          `json:"enabled"`
	MaxRuns   int           `json:"max_runs,omitempty"`
	RunCount  int           `json:"run_count,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// CloudTriggerConfig holds type-specific trigger configuration.
type CloudTriggerConfig struct {
	// Webhook
	Secret     string   `json:"secret,omitempty"`
	AllowedIPs []string `json:"allowed_ips,omitempty"`
	Headers    []string `json:"headers,omitempty"`
	// File
	Path      string   `json:"path,omitempty"`
	Recursive bool     `json:"recursive,omitempty"`
	Patterns  []string `json:"patterns,omitempty"`
	// Git
	Repository string   `json:"repository,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	Events     []string `json:"events,omitempty"`
}

// CloudTrigger defines an event that fires a cloud agent.
// Mirrors swarm-cloud/internal/agents/compute/scheduler.Trigger.
type CloudTrigger struct {
	ID           string              `json:"id"`
	AgentID      string              `json:"agent_id"`
	ProjectID    string              `json:"project_id"`
	Message      string              `json:"message"`
	Variables    ScheduleVariableMap `json:"variables,omitempty"`
	Type         ScheduleTriggerType `json:"type"`
	Config       CloudTriggerConfig  `json:"config"`
	Enabled      bool                `json:"enabled"`
	LastTrigger  *time.Time          `json:"last_trigger,omitempty"`
	TriggerCount int                 `json:"trigger_count,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

type schedulesResponse struct {
	Schedules []*CloudSchedule `json:"schedules"`
}

type triggersResponse struct {
	Triggers []*CloudTrigger `json:"triggers"`
}

// CreateSchedule creates a new cron or interval schedule on the compute manager.
//
//	CONTRACT: schedule.ID, schedule.AgentID, and either CronExpr or Interval
//	must be non-empty. The compute manager validates CronExpr syntax and returns
//	an error for malformed expressions.
func (c *Client) CreateSchedule(ctx context.Context, schedule CloudSchedule) (*CloudSchedule, error) {
	if strings.TrimSpace(schedule.ID) == "" {
		return nil, fmt.Errorf("schedule ID is required")
	}
	if strings.TrimSpace(schedule.AgentID) == "" {
		return nil, fmt.Errorf("schedule agent_id is required")
	}
	resp, err := c.authorizedJSONRequest(ctx, http.MethodPost, "/v1/schedules", schedule)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create schedule returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out CloudSchedule
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSchedules returns all schedules registered on the compute manager.
func (c *Client) ListSchedules(ctx context.Context) ([]*CloudSchedule, error) {
	resp, err := c.authorizedJSONRequest(ctx, http.MethodGet, "/v1/schedules", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list schedules returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out schedulesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Schedules, nil
}

// DeleteSchedule removes a schedule by ID.
func (c *Client) DeleteSchedule(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("schedule ID is required")
	}
	resp, err := c.authorizedJSONRequest(ctx, http.MethodDelete, "/v1/schedules/"+id, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete schedule returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// CreateTrigger registers a new event trigger (webhook, git, file, etc.)
// on the compute manager.
//
//	CONTRACT: trigger.ID, trigger.AgentID, and trigger.Type must be non-empty.
func (c *Client) CreateTrigger(ctx context.Context, trigger CloudTrigger) (*CloudTrigger, error) {
	if strings.TrimSpace(trigger.ID) == "" {
		return nil, fmt.Errorf("trigger ID is required")
	}
	if strings.TrimSpace(trigger.AgentID) == "" {
		return nil, fmt.Errorf("trigger agent_id is required")
	}
	if trigger.Type == "" {
		return nil, fmt.Errorf("trigger type is required")
	}
	resp, err := c.authorizedJSONRequest(ctx, http.MethodPost, "/v1/triggers", trigger)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create trigger returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out CloudTrigger
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTriggers returns all triggers registered on the compute manager.
func (c *Client) ListTriggers(ctx context.Context) ([]*CloudTrigger, error) {
	resp, err := c.authorizedJSONRequest(ctx, http.MethodGet, "/v1/triggers", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list triggers returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out triggersResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Triggers, nil
}

// DeleteTrigger removes a trigger by ID.
func (c *Client) DeleteTrigger(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("trigger ID is required")
	}
	resp, err := c.authorizedJSONRequest(ctx, http.MethodDelete, "/v1/triggers/"+id, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete trigger returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
