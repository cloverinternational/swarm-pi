// Package builtin provides cron scheduling infrastructure for recurring and
// one-shot agent tasks.
//
// The scheduler runs a 1-second polling loop that checks all registered tasks
// and fires those whose next cron window has been reached. Jitter is
// proportional to the actual cron gap (10% of the gap between consecutive
// fires, capped at 15 min) so a 1-minute cron gets ~6s of jitter, not 15 min.
//
// Delivery modes (selected via CronSchedulerConfig):
//   - PromptSink: fired prompts are enqueued into the host application's
//     message queue (e.g. the TUI chat queue). The client continues the
//     conversation naturally — the prompt appears as a user message. This
//     mirrors how Claude Code delivers cron-fired prompts via
//     enqueuePendingNotification at 'later' priority. See CRON_SCHEDULER.md.
//   - AgentFactory + BGManager: fired prompts spawn a background agent.
//     Suitable for headless/daemon mode with no interactive chat.
//
// To add a new application that wants to receive cron messages: implement
// the PromptSink interface (one method: EnqueuePrompt), pass it in
// CronSchedulerConfig, and call Start().
package builtin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/robfig/cron/v3"
)

// CronJitterConfig configures jitter and expiry behavior for scheduled tasks.
type CronJitterConfig struct {
	RecurringFrac           float64 // fraction of the actual cron gap to jitter (0.1 = 10%)
	RecurringCapMs          int64   // maximum jitter for recurring tasks in milliseconds
	OneShotEarlyJitterMaxMs int64
	RecurringMaxAgeMs       int64
}

// defaultJitterConfig provides default jitter configuration.
var defaultJitterConfig = CronJitterConfig{
	RecurringFrac:           0.1,                     // 10% of the actual cron gap
	RecurringCapMs:          15 * 60 * 1000,          // 15 min cap
	OneShotEarlyJitterMaxMs: 90 * 1000,               // 90s
	RecurringMaxAgeMs:       7 * 24 * 60 * 60 * 1000, // 7 days
}

// PromptSink is an interface for enqueuing fired prompts directly into the host
// application's message queue instead of spawning a background agent.
//
// When configured (via CronSchedulerConfig.PromptSink), fireTaskNow calls
// EnqueuePrompt(ctx, prompt) for each fired task. The host application is
// responsible for routing the prompt to its chat queue, command queue, or
// message bus. This is the recommended delivery mode for interactive apps
// (TUI, REPL) because the prompt enters the same conversation the user is
// having — the client continues naturally between turns.
//
// To add cron support to a new application:
//  1. Implement this interface (EnqueuePrompt).
//  2. Pass your implementation in CronSchedulerConfig.PromptSink.
//  3. Call scheduler.Start().
//
// AgentFactory and BGManager are NOT required when PromptSink is set.
type PromptSink interface {
	EnqueuePrompt(ctx context.Context, prompt string) error
}

// ScheduledTask represents a task to be executed on a schedule.
type ScheduledTask struct {
	ID          string    `json:"id"`
	Prompt      string    `json:"prompt"`
	Cron        string    `json:"cron"`
	Recurring   bool      `json:"recurring"`
	Durable     bool      `json:"durable"`
	CreatedAt   time.Time `json:"created_at"`
	LastFiredAt time.Time `json:"last_fired_at"`
	AgentID     string    `json:"agent_id,omitempty"`
	NextFireAt  time.Time `json:"next_fire_at"`
	AgedOut     bool      `json:"aged_out,omitempty"`
}

// CronScheduler manages scheduled tasks for background agent execution.
type CronScheduler struct {
	tasks         map[string]*ScheduledTask
	mu            sync.RWMutex
	isRunning     bool
	stopChan      chan struct{}
	checkInterval time.Duration

	// Dependencies
	agentFactory agent.Factory
	bgManager    BackgroundAgentManager
	logger       observability.Logger
	workDir      string // For durable task storage
	promptSink   PromptSink

	// runningTasks tracks which task instances are currently executing
	// Key: task.ID, Value: agentID of the spawned background agent
	runningTasks map[string]string
	runningMu    sync.RWMutex
}

// CronSchedulerConfig configures the cron scheduler.
type CronSchedulerConfig struct {
	AgentFactory  agent.Factory
	BGManager     BackgroundAgentManager
	Logger        observability.Logger
	WorkDir       string        // Workspace directory for durable storage
	CheckInterval time.Duration // Default: 1 minute
	PromptSink    PromptSink    // Optional: sink for direct prompt enqueueing
}

// NewCronScheduler creates a new cron scheduler.
func NewCronScheduler(config CronSchedulerConfig) (*CronScheduler, error) {
	// Allow nil AgentFactory and BGManager when PromptSink is provided
	if config.PromptSink == nil {
		if config.AgentFactory == nil {
			return nil, sdkerr.Permanent("cron_scheduler.missing_factory", "agent factory is required")
		}
		if config.BGManager == nil {
			return nil, sdkerr.Permanent("cron_scheduler.missing_manager", "background manager is required")
		}
	}
	if config.Logger == nil {
		return nil, sdkerr.Permanent("cron_scheduler.missing_logger", "logger is required")
	}

	interval := config.CheckInterval
	if interval <= 0 {
		interval = 1 * time.Second // 1s check so sub-minute crons fire promptly
	}

	return &CronScheduler{
		tasks:         make(map[string]*ScheduledTask),
		checkInterval: interval,
		agentFactory:  config.AgentFactory,
		bgManager:     config.BGManager,
		logger:        config.Logger,
		workDir:       config.WorkDir,
		promptSink:    config.PromptSink,
		runningTasks:  make(map[string]string),
	}, nil
}

// Start begins the scheduler loop.
func (s *CronScheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isRunning {
		return
	}

	s.isRunning = true
	s.stopChan = make(chan struct{})

	// Load durable tasks from disk
	s.loadDurableTasks()

	go s.runLoop()

	s.logger.Info(context.Background(), "cron_scheduler.started",
		observability.F("check_interval", s.checkInterval.String()),
		observability.F("task_count", len(s.tasks)))
}

// Stop halts the scheduler loop.
func (s *CronScheduler) Stop() {
	s.mu.Lock()
	if !s.isRunning {
		s.mu.Unlock()
		return
	}

	s.isRunning = false
	close(s.stopChan)
	s.mu.Unlock()

	s.logger.Info(context.Background(), "cron_scheduler.stopped")
}

// runLoop is the main scheduler goroutine. It polls every checkInterval
// (1 second by default) and fires tasks whose next cron window has been
// reached. The 1-second interval ensures sub-minute crons fire promptly;
// the old 1-minute interval could miss a window by up to 59 seconds.
func (s *CronScheduler) runLoop() {
	ticker := time.NewTicker(s.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.checkAndFireTasks()
		case <-s.stopChan:
			return
		}
	}
}

// checkAndFireTasks checks all tasks and fires those that are due.
//
// Firing is a two-phase operation, which is what makes the scheduler robust
// against BOTH the 1-second poll interval AND the asynchronous jitter window:
//
//  1. CLAIM (synchronous, under s.mu): for every due task we immediately record
//     LastFiredAt and advance NextFireAt to the next boundary strictly after
//     `now`, BEFORE dispatching the (possibly jitter-delayed) delivery.
//
//  2. DELIVER (may be async): fireTask applies jitter and enqueues/spawns.
//
// WHY: previously the actual fire — and therefore the LastFiredAt update —
// happened inside fireTaskNow, which fireTask may defer behind an async jitter
// timer (up to ~6s for an every-minute cron). During that window every
// intervening poll still saw the task as due and armed ANOTHER delivery, so a
// single once-per-minute occurrence was delivered ~1×/second (observed 100+
// enqueues per minute in production logs, and the TUI's queue-drain then
// stranded the overflow until the next user interaction). Claiming the
// occurrence up front, in the single scheduler goroutine, guarantees exactly
// one delivery per boundary.
//
// Advancing NextFireAt with schedule.Next(now) also means a delayed poll skips
// past missed boundaries instead of replaying them (no catch-up storm), while
// never skipping the occurrence currently being fired (that one is what made us
// due, and we deliver it before advancing).
func (s *CronScheduler) checkAndFireTasks() {
	now := time.Now()

	var due []*ScheduledTask
	s.mu.Lock()
	for _, task := range s.tasks {
		nextFire, err := s.nextFireLocked(task, now)
		if err != nil {
			s.logger.Warn(context.Background(), "cron_scheduler.calculate_next_fire_failed",
				observability.F("task_id", task.ID),
				observability.F("cron", task.Cron),
				observability.F("error", err.Error()))
			continue
		}
		task.NextFireAt = nextFire
		if !(now.Equal(nextFire) || now.After(nextFire)) {
			continue
		}
		// CLAIM this occurrence so no later poll — and no concurrent jitter
		// timer — can re-arm the same fire.
		task.LastFiredAt = now
		if schedule, perr := cron.ParseStandard(task.Cron); perr == nil {
			task.NextFireAt = schedule.Next(now)
		}
		due = append(due, task)
	}
	s.mu.Unlock()

	// DELIVER outside the lock: fireTask may start jitter timers or spawn
	// background agents, neither of which should hold s.mu.
	for _, task := range due {
		s.fireTask(task, now)
	}
}

// nextFireLocked returns the task's pending fire time, lazily initializing
// NextFireAt on first use. It must be called with s.mu held.
//
// The pending fire time is stored on the task (NextFireAt) and advanced only at
// claim time in checkAndFireTasks. A recurring task anchors its FIRST fire from
// LastFiredAt (if a prior fire is known) or CreatedAt; one-shot tasks likewise.
// After the first claim the stored NextFireAt drives due-ness, so recurring
// boundaries are neither skipped (the old schedule.Next(now) re-anchoring bug)
// nor replayed every poll (the jitter-window refire bug).
func (s *CronScheduler) nextFireLocked(task *ScheduledTask, now time.Time) (time.Time, error) {
	if !task.NextFireAt.IsZero() {
		return task.NextFireAt, nil
	}
	schedule, err := cron.ParseStandard(task.Cron)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression %q: %w", task.Cron, err)
	}
	base := task.LastFiredAt
	if base.IsZero() {
		base = task.CreatedAt
	}
	return schedule.Next(base), nil
}

// fireTask executes a scheduled task. It first checks for 7-day auto-expiry,
// then applies deterministic jitter (proportional to the cron gap) before
// delegating to fireTaskNow. If a non-zero jitter applies, the actual fire is
// dispatched asynchronously after the jitter elapses so the scheduler goroutine
// is not blocked. fireTaskNow either enqueues the prompt via PromptSink (for
// interactive apps) or spawns a background agent (for headless/daemon mode).
func (s *CronScheduler) fireTask(task *ScheduledTask, now time.Time) {
	ctx := context.Background()

	// Check for 7-day auto-expiry
	if task.Recurring && time.Since(task.CreatedAt) > time.Duration(defaultJitterConfig.RecurringMaxAgeMs)*time.Millisecond {
		s.logger.Info(ctx, "cron_scheduler.task_aged_out",
			observability.F("task_id", task.ID),
			observability.F("age_days", time.Since(task.CreatedAt).Hours()/24))
		task.AgedOut = true
	}

	// Apply deterministic jitter WITHOUT blocking the scheduler goroutine.
	// Previously this called time.Sleep(jitter) inline, which froze the single
	// scheduler loop for up to RecurringCapMs (15 min) and made tests sleep
	// for the full jitter window. Instead, when there is a non-zero jitter we
	// re-dispatch the fire asynchronously after the jitter elapses and return now.
	// Jitter is proportional to the actual cron gap (10% of t2-t1), so a
	// 1-minute cron gets ~6s, not 15 min — see calculateJitter below.
	jitter := calculateJitter(task)
	// Aged-out tasks fire their single final time immediately — no jitter delay.
	if jitter > 0 && !task.AgedOut {
		s.logger.Debug(ctx, "cron_scheduler.applying_jitter",
			observability.F("task_id", task.ID),
			observability.F("jitter_ms", jitter.Milliseconds()))
		go func() {
			t := time.NewTimer(jitter)
			defer t.Stop()
			select {
			case <-t.C:
				s.fireTaskNow(task, time.Now())
			case <-s.stopChan:
			}
		}()
		return
	}
	s.fireTaskNow(task, now)
}

// fireTaskNow performs the actual fire (sink-enqueue or background-agent spawn)
// after any jitter delay has elapsed.
func (s *CronScheduler) fireTaskNow(task *ScheduledTask, now time.Time) {
	ctx := context.Background()

	s.logger.Info(ctx, "cron_scheduler.firing_task",
		observability.F("task_id", task.ID),
		observability.F("prompt", truncate(task.Prompt, 50)),
		observability.F("recurring", task.Recurring),
		observability.F("aged_out", task.AgedOut))

	// Check if this task is already running (prevent duplicate spawning)
	s.runningMu.RLock()
	if existingAgentID, isRunning := s.runningTasks[task.ID]; isRunning {
		s.runningMu.RUnlock()
		s.logger.Warn(ctx, "cron_scheduler.task_already_running",
			observability.F("task_id", task.ID),
			observability.F("agent_id", existingAgentID))
		return
	}
	s.runningMu.RUnlock()

	// If PromptSink is configured, use it instead of spawning a background agent
	if s.promptSink != nil {
		if err := s.promptSink.EnqueuePrompt(ctx, task.Prompt); err != nil {
			s.logger.Error(ctx, "cron_scheduler.prompt_sink_failed",
				observability.F("task_id", task.ID),
				observability.F("error", err.Error()))
		} else {
			s.logger.Info(ctx, "cron_scheduler.prompt_enqueued",
				observability.F("task_id", task.ID))
		}

		// Update last fired time
		s.mu.Lock()
		task.LastFiredAt = now
		s.mu.Unlock()

		// If durable, persist the updated task
		if task.Durable {
			s.persistTask(task)
		}

		// If not recurring or aged out, remove the task
		if !task.Recurring || task.AgedOut {
			s.RemoveTask(task.ID)
		}
		return
	}

	// Generate unique agent ID for this task execution
	agentID := fmt.Sprintf("scheduled-%s-%d", task.ID, now.Unix())

	// Create agent configuration
	providerConfig := provider.Config{} // Use default provider config

	var subAgent *agent.Agent
	var err error

	// Create agent using factory
	if task.AgentID != "" {
		// Use custom agent definition if specified
		subAgent, err = s.agentFactory.CreateFromDefinition(ctx, &agent.Definition{
			ID: task.AgentID,
		}, providerConfig)
	} else {
		// Create default background agent
		config := agent.BackgroundConfig{
			AgentID:        agentID,
			AgentName:      "Scheduled Task Agent",
			Description:    fmt.Sprintf("Scheduled task: %s", truncate(task.Prompt, 50)),
			ProviderConfig: providerConfig,
			Tools:          []string{}, // Tools will be registered separately
		}
		subAgent, err = s.agentFactory.CreateBackground(ctx, config)
	}

	if err != nil {
		s.logger.Error(ctx, "cron_scheduler.agent_creation_failed",
			observability.F("task_id", task.ID),
			observability.F("error", err.Error()))
		return
	}

	// Create background agent wrapper
	bgAgent, err := agent.NewBackgroundAgent(agent.BackgroundAgentConfig{
		Agent:           subAgent,
		EventBufferSize: 100,
		Logger:          s.logger,
		Tracer:          nil, // Could add tracer if available
	})

	if err != nil {
		s.logger.Error(ctx, "cron_scheduler.background_agent_creation_failed",
			observability.F("task_id", task.ID),
			observability.F("error", err.Error()))
		return
	}

	// Register as running
	s.runningMu.Lock()
	s.runningTasks[task.ID] = agentID
	s.runningMu.Unlock()

	// Start the background agent
	bgCtx := context.Background()
	if err := bgAgent.Start(bgCtx, task.Prompt); err != nil {
		s.logger.Error(ctx, "cron_scheduler.agent_start_failed",
			observability.F("task_id", task.ID),
			observability.F("error", err.Error()))

		// Remove from running tasks
		s.runningMu.Lock()
		delete(s.runningTasks, task.ID)
		s.runningMu.Unlock()
		return
	}

	// Register with background manager
	if s.bgManager != nil {
		if err := s.bgManager.Add(bgAgent, task.Prompt); err != nil {
			s.logger.Warn(ctx, "cron_scheduler.background_registration_failed",
				observability.F("task_id", task.ID),
				observability.F("error", err.Error()))
		}
	}

	s.logger.Info(ctx, "cron_scheduler.agent_spawned",
		observability.F("task_id", task.ID),
		observability.F("agent_id", agentID))

	// Start cleanup goroutine to remove from running tasks when complete
	go s.waitForTaskCompletion(task.ID, bgAgent)

	// Update last fired time
	s.mu.Lock()
	task.LastFiredAt = now
	s.mu.Unlock()

	// If durable, persist the updated task
	if task.Durable {
		s.persistTask(task)
	}

	// If not recurring or aged out, remove the task
	if !task.Recurring || task.AgedOut {
		s.RemoveTask(task.ID)
	}
}

// waitForTaskCompletion waits for a background agent to complete and cleans up tracking.
func (s *CronScheduler) waitForTaskCompletion(taskID string, bgAgent *agent.BackgroundAgent) {
	// Wait for agent to complete
	result := bgAgent.Wait()

	// Remove from running tasks
	s.runningMu.Lock()
	delete(s.runningTasks, taskID)
	s.runningMu.Unlock()

	s.logger.Info(context.Background(), "cron_scheduler.task_completed",
		observability.F("task_id", taskID),
		observability.F("status", result.Status),
		observability.F("duration", result.Duration))
}

// AddTask adds a new scheduled task.
func (s *CronScheduler) AddTask(task *ScheduledTask) error {
	if task.ID == "" {
		return sdkerr.Permanent("cron_scheduler.invalid_task", "task ID cannot be empty")
	}
	if task.Prompt == "" {
		return sdkerr.Permanent("cron_scheduler.invalid_task", "task prompt cannot be empty")
	}
	if task.Cron == "" {
		return sdkerr.Permanent("cron_scheduler.invalid_task", "task cron cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.tasks[task.ID] = task

	// If durable, persist to disk. Use the Locked variant since we already
	// hold s.mu; persistTask would try to re-acquire RLock and deadlock.
	if task.Durable {
		return s.persistTaskLocked(task)
	}

	return nil
}

// RemoveTask removes a scheduled task.
func (s *CronScheduler) RemoveTask(taskID string) (*ScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[taskID]
	if !ok {
		return nil, sdkerr.Permanent("cron_scheduler.task_not_found",
			fmt.Sprintf("task '%s' not found", taskID))
	}

	delete(s.tasks, taskID)

	// If it was durable, remove from disk
	if task.Durable {
		s.removePersistedTask(taskID)
	}

	return task, nil
}

// ListTasks returns all scheduled tasks.
func (s *CronScheduler) ListTasks() []*ScheduledTask {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]*ScheduledTask, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}

	return tasks
}

// GetTask retrieves a specific task by ID.
func (s *CronScheduler) GetTask(taskID string) (*ScheduledTask, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[taskID]
	return task, ok
}

// loadDurableTasks loads durable tasks from disk.
func (s *CronScheduler) loadDurableTasks() {
	if s.workDir == "" {
		return
	}

	path := s.getTasksFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			s.logger.Warn(context.Background(), "cron_scheduler.load_failed",
				observability.F("path", path),
				observability.F("error", err.Error()))
		}
		return
	}

	var tasks []*ScheduledTask
	if err := json.Unmarshal(data, &tasks); err != nil {
		s.logger.Error(context.Background(), "cron_scheduler.load_parse_failed",
			observability.F("path", path),
			observability.F("error", err.Error()))
		return
	}

	for _, task := range tasks {
		if task.Durable {
			s.tasks[task.ID] = task
		}
	}

	s.logger.Info(context.Background(), "cron_scheduler.loaded_durable_tasks",
		observability.F("count", len(tasks)))
}

// persistTask saves all durable tasks to disk.
// Acquires a read lock — use persistTaskLocked when the caller already holds s.mu.
func (s *CronScheduler) persistTask(task *ScheduledTask) error {
	if s.workDir == "" {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.persistTaskLocked(task)
}

// persistTaskLocked is the lock-free body of persistTask. The caller MUST hold
// s.mu (read or write) before calling this. Previously persistTask re-acquired
// s.mu.RLock() while AddTask/RemoveTask held s.mu.Lock(), which deadlocks Go's
// sync.RWMutex — a writer-pending state blocks subsequent readers including
// the writer itself.
func (s *CronScheduler) persistTaskLocked(task *ScheduledTask) error {
	if s.workDir == "" {
		return nil
	}

	tasks := make([]*ScheduledTask, 0, len(s.tasks))
	for _, t := range s.tasks {
		if t.Durable {
			tasks = append(tasks, t)
		}
	}

	path := s.getTasksFilePath()

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create tasks directory: %w", err)
	}

	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tasks: %w", err)
	}

	if err := os.WriteFile(path, data, 0640); err != nil {
		return fmt.Errorf("failed to write tasks file: %w", err)
	}

	return nil
}

// removePersistedTask removes a task from disk. Caller MUST hold s.mu (write).
// Previously this acquired s.mu.RLock() while RemoveTask held s.mu.Lock(),
// which deadlocks — see persistTaskLocked for the root cause.
func (s *CronScheduler) removePersistedTask(taskID string) {
	if s.workDir == "" {
		return
	}

	var dummy *ScheduledTask
	for _, t := range s.tasks {
		if t.Durable {
			dummy = t
			break
		}
	}

	if dummy != nil {
		_ = s.persistTaskLocked(dummy)
	} else {
		// No durable tasks left, remove the file
		path := s.getTasksFilePath()
		os.Remove(path)
	}
}

// getTasksFilePath returns the path to the durable tasks file.
func (s *CronScheduler) getTasksFilePath() string {
	return filepath.Join(s.workDir, ".swarm", "scheduled_tasks.json")
}

// truncate truncates a string to the specified length.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// calculateJitter calculates deterministic jitter for a task based on its ID.
// For recurring tasks, the jitter is proportional to the actual cron gap
// (10% of the gap between consecutive fires, capped at 15 min) so a 1-minute
// cron gets at most ~6 seconds of jitter, not 15 minutes.
func calculateJitter(task *ScheduledTask) time.Duration {
	// Use SHA256 hash of task ID for deterministic seed
	h := sha256.Sum256([]byte(task.ID))
	// Use first 8 bytes as seed
	seed := int64(h[0])<<56 | int64(h[1])<<48 | int64(h[2])<<40 | int64(h[3])<<32 |
		int64(h[4])<<24 | int64(h[5])<<16 | int64(h[6])<<8 | int64(h[7])
	if seed < 0 {
		seed = -seed
	}

	if !task.Recurring {
		// One-shot tasks: fire up to OneShotEarlyJitterMaxMs early
		jitterMs := seed % defaultJitterConfig.OneShotEarlyJitterMaxMs
		return -time.Duration(jitterMs) * time.Millisecond
	}

	// Recurring tasks: jitter is 10% of the actual cron gap, capped at
	// RecurringCapMs (15 min). Parse the cron to compute the gap between
	// consecutive fires so a */1 * * * * task gets ~6s max, an hourly task
	// gets ~6min max, etc. Anchor from CreatedAt so jitter is deterministic
	// (same task ID → same jitter across calls).
	schedule, err := cron.ParseStandard(task.Cron)
	if err != nil {
		return 0 // invalid cron → no jitter
	}
	base := task.CreatedAt
	if base.IsZero() {
		base = time.Now()
	}
	t1 := schedule.Next(base)
	t2 := schedule.Next(t1)
	if t2.IsZero() {
		return 0 // no second match → nothing to proportion against
	}
	gap := t2.Sub(t1)
	jitterFrac := float64(seed%1000) / 1000.0 // [0, 1)
	jitter := time.Duration(jitterFrac * defaultJitterConfig.RecurringFrac * float64(gap))
	cap := time.Duration(defaultJitterConfig.RecurringCapMs) * time.Millisecond
	if jitter > cap {
		jitter = cap
	}
	return jitter
}
