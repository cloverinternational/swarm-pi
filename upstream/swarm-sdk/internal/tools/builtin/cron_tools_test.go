package builtin

import (
	"context"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPromptSink is a test implementation of PromptSink that records calls.
type TestPromptSink struct {
	calls []string
}

func (t *TestPromptSink) EnqueuePrompt(ctx context.Context, prompt string) error {
	t.calls = append(t.calls, prompt)
	return nil
}

// MockAgentFactory is a test implementation of agent.Factory
type MockAgentFactory struct{}

func (m *MockAgentFactory) CreateFromDefinition(ctx context.Context, def *agent.Definition, providerConfig provider.Config) (*agent.Agent, error) {
	return nil, nil
}

func (m *MockAgentFactory) CreateBackground(ctx context.Context, config agent.BackgroundConfig) (*agent.Agent, error) {
	return nil, nil
}

func (m *MockAgentFactory) CreateSteering(ctx context.Context, config agent.SteeringAgentConfig) (*agent.Agent, error) {
	return nil, nil
}

func (m *MockAgentFactory) CreateWorker(ctx context.Context, config agent.WorkerConfig) (*agent.Agent, error) {
	return nil, nil
}

func (m *MockAgentFactory) CreateSubAgent(ctx context.Context, config agent.SubAgentConfig) (*agent.Agent, error) {
	return nil, nil
}

func TestIsValidCron(t *testing.T) {
	tests := []struct {
		name     string
		cron     string
		expected bool
	}{
		{"empty string", "", false},
		{"valid every minute", "* * * * *", true},
		{"valid every 5 minutes", "*/5 * * * *", true},
		{"valid daily at 9am", "0 9 * * *", true},
		{"valid weekdays at 9am", "0 9 * * 1-5", true},
		{"invalid too few fields", "0 9 * *", false},
		{"invalid too many fields", "0 9 * * * *", false},
		{"invalid minute", "60 * * * *", false},
		{"invalid hour", "0 24 * * *", false},
		{"invalid day", "0 0 32 * *", false},
		{"invalid month", "0 0 1 13 *", false},
		{"invalid day of week", "0 0 * * 8", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidCron(tt.cron)
			assert.Equal(t, tt.expected, result, "isValidCron(%q) = %v, want %v", tt.cron, result, tt.expected)
		})
	}
}

func TestCalculateJitter(t *testing.T) {
	// Test deterministic jitter
	task1 := &ScheduledTask{
		ID:        "test-task-1",
		Recurring: true,
		Cron:      "*/1 * * * *",
	}
	task2 := &ScheduledTask{
		ID:        "test-task-1", // Same ID
		Recurring: true,
		Cron:      "*/1 * * * *",
	}
	task3 := &ScheduledTask{
		ID:        "test-task-2", // Different ID
		Recurring: true,
		Cron:      "*/1 * * * *",
	}

	jitter1 := calculateJitter(task1)
	jitter2 := calculateJitter(task2)
	jitter3 := calculateJitter(task3)

	// Same task ID should produce same jitter
	assert.Equal(t, jitter1, jitter2, "jitter should be deterministic for same task ID")

	// Different task ID should produce different jitter
	assert.NotEqual(t, jitter1, jitter3, "jitter should be different for different task IDs")

	// Test jitter bounds for recurring tasks
	assert.True(t, jitter1 >= 0, "recurring task jitter should be non-negative")
	assert.True(t, jitter1 <= time.Duration(defaultJitterConfig.RecurringCapMs)*time.Millisecond,
		"recurring task jitter should not exceed max")

	// Test jitter for one-shot tasks
	oneShotTask := &ScheduledTask{
		ID:        "one-shot-task",
		Recurring: false,
	}
	oneShotJitter := calculateJitter(oneShotTask)
	assert.True(t, oneShotJitter <= 0, "one-shot task jitter should be non-positive (early)")
	assert.True(t, oneShotJitter >= -time.Duration(defaultJitterConfig.OneShotEarlyJitterMaxMs)*time.Millisecond,
		"one-shot task jitter should not exceed early max")
}

func TestSevenDayExpiry(t *testing.T) {
	logger := noop.NewLogger()

	// Create a test scheduler with a prompt sink
	sink := &TestPromptSink{}
	scheduler, err := NewCronScheduler(CronSchedulerConfig{
		AgentFactory:  nil, // Not needed for this test
		BGManager:     nil, // Not needed for this test
		Logger:        logger,
		WorkDir:       "",
		CheckInterval: 100 * time.Millisecond,
		PromptSink:    sink,
	})
	require.NoError(t, err)

	// Create a task that's 8 days old
	oldTask := &ScheduledTask{
		ID:        "old-task",
		Prompt:    "test prompt",
		Cron:      "* * * * *", // Every minute
		Recurring: true,
		Durable:   false,
		CreatedAt: time.Now().Add(-8 * 24 * time.Hour), // 8 days ago
	}

	// Add the task
	err = scheduler.AddTask(oldTask)
	require.NoError(t, err)

	// Fire the task
	scheduler.fireTask(oldTask, time.Now())

	// Check that the task was marked as aged out
	assert.True(t, oldTask.AgedOut, "task should be marked as aged out")

	// Check that the prompt was enqueued
	assert.Len(t, sink.calls, 1, "prompt should have been enqueued once")
	assert.Equal(t, "test prompt", sink.calls[0], "correct prompt should be enqueued")

	// Check that the task was removed
	_, exists := scheduler.GetTask("old-task")
	assert.False(t, exists, "aged out task should be removed after firing")
}

func TestPromptSinkPath(t *testing.T) {
	logger := noop.NewLogger()

	// Create a test scheduler with a prompt sink
	sink := &TestPromptSink{}
	scheduler, err := NewCronScheduler(CronSchedulerConfig{
		AgentFactory:  nil, // Not needed for this test
		BGManager:     nil, // Not needed for this test
		Logger:        logger,
		WorkDir:       "",
		CheckInterval: 100 * time.Millisecond,
		PromptSink:    sink,
	})
	require.NoError(t, err)

	// Create a task
	task := &ScheduledTask{
		ID:        "test-task",
		Prompt:    "test prompt for sink",
		Cron:      "* * * * *",
		Recurring: false,
		Durable:   false,
		CreatedAt: time.Now(),
	}

	// Add and fire the task
	err = scheduler.AddTask(task)
	require.NoError(t, err)

	scheduler.fireTask(task, time.Now())

	// Check that the prompt was enqueued via sink
	assert.Len(t, sink.calls, 1, "prompt should have been enqueued")
	assert.Equal(t, "test prompt for sink", sink.calls[0], "correct prompt should be enqueued")

	// Check that the one-shot task was removed
	_, exists := scheduler.GetTask("test-task")
	assert.False(t, exists, "one-shot task should be removed after firing")
}

func TestScheduleWakeupTool(t *testing.T) {
	logger := noop.NewLogger()
	tracer := noop.NewTracer()

	// Create a scheduler with a prompt sink (wakeups enqueue into the session)
	scheduler, err := NewCronScheduler(CronSchedulerConfig{
		PromptSink:    &TestPromptSink{},
		Logger:        logger,
		WorkDir:       "",
		CheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	// Create the ScheduleWakeup tool
	tool, err := NewScheduleWakeupTool(ScheduleWakeupConfig{
		Scheduler: scheduler,
		Logger:    logger,
		Tracer:    tracer,
	})
	require.NoError(t, err)

	// Test tool name
	assert.Equal(t, "ScheduleWakeup", tool.Name())

	// Test scheduling a wakeup
	params := map[string]any{
		"prompt": "wakeup test prompt",
		"delay":  "5m",
	}

	result, err := tool.Execute(context.Background(), params)
	require.NoError(t, err)
	assert.NotNil(t, result)

	// Check that the output contains expected fields
	output := result.Output
	assert.Contains(t, output, "wakeup-", "output should contain task ID")
	assert.Contains(t, output, "5m", "output should contain delay")
	assert.Contains(t, output, "fire_time", "output should contain fire time")

	// Verify a task was created
	tasks := scheduler.ListTasks()
	assert.Len(t, tasks, 1, "one task should be created")
	assert.False(t, tasks[0].Recurring, "task should be one-shot")
	assert.Equal(t, "wakeup test prompt", tasks[0].Prompt, "task should have correct prompt")
}

func TestScheduleWakeupValidation(t *testing.T) {
	logger := noop.NewLogger()
	tracer := noop.NewTracer()

	scheduler, err := NewCronScheduler(CronSchedulerConfig{
		PromptSink:    &TestPromptSink{},
		Logger:        logger,
		WorkDir:       "",
		CheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	tool, err := NewScheduleWakeupTool(ScheduleWakeupConfig{
		Scheduler: scheduler,
		Logger:    logger,
		Tracer:    tracer,
	})
	require.NoError(t, err)

	// Test missing prompt
	_, err = tool.Execute(context.Background(), map[string]any{
		"prompt": "",
		"delay":  "5m",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "prompt parameter is required")

	// Test missing delay
	_, err = tool.Execute(context.Background(), map[string]any{
		"prompt": "test",
		"delay":  "",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delay parameter is required")

	// Test invalid delay format
	_, err = tool.Execute(context.Background(), map[string]any{
		"prompt": "test",
		"delay":  "invalid",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid delay format")
}

func TestHumanReadableCron(t *testing.T) {
	tests := []struct {
		cron     string
		expected string
	}{
		{"*/1 * * * *", "every minute"},
		{"*/5 * * * *", "every 5 minutes"},
		{"*/15 * * * *", "every 15 minutes"},
		{"*/30 * * * *", "every 30 minutes"},
		{"0 * * * *", "every hour"},
		{"0 */2 * * *", "every 2 hours"},
		{"0 0 * * *", "daily at midnight"},
		{"0 0 */1 * *", "daily at midnight"},
		{"0 9 * * 1-5", "weekdays at 9am"},
		{"0 9 * * *", "daily at 9am"},
		{"15 14 * * *", "15 14 * * *"}, // Unknown pattern returns as-is
	}

	for _, tt := range tests {
		t.Run(tt.cron, func(t *testing.T) {
			result := humanReadableCron(tt.cron)
			assert.Equal(t, tt.expected, result)
		})
	}
}
func TestRecurringJitterProportionalToCronGap(t *testing.T) {
	// A */1 * * * * (every-minute) recurring task should have jitter
	// proportional to the 60s gap (~6s max), NOT the old flat 15-min cap.
	task := &ScheduledTask{
		ID:        "jitter-proptest-abc12345",
		Recurring: true,
		Cron:      "*/1 * * * *",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	jitter := calculateJitter(task)
	assert.True(t, jitter >= 0, "recurring jitter should be non-negative")
	assert.True(t, jitter <= 6*time.Second+1*time.Second,
		"every-minute cron jitter should be <= ~6s (10%% of 60s gap), got %v", jitter)
	assert.True(t, jitter < 15*time.Minute,
		"every-minute cron jitter must NOT be 15 minutes (old bug), got %v", jitter)
}

func TestRecurringJitterForHourlyCron(t *testing.T) {
	// An hourly cron should have jitter up to ~6 min (10% of 3600s).
	task := &ScheduledTask{
		ID:        "jitter-hourly-def45678",
		Recurring: true,
		Cron:      "0 * * * *",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	jitter := calculateJitter(task)
	assert.True(t, jitter >= 0, "hourly jitter should be non-negative")
	assert.True(t, jitter <= 6*time.Minute+1*time.Second,
		"hourly cron jitter should be <= ~6min (10%% of 3600s gap), got %v", jitter)
}
