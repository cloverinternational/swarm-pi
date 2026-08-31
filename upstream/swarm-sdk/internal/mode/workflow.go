package mode

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode/gates"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// WorkflowEngine manages the execution of a mode's workflow (DAG of agent groups).
type WorkflowEngine struct {
	mode              *Mode
	logger            observability.Logger
	tracer            observability.Tracer
	gateManager       *gates.GateManager
	gateActionHandler *GateActionHandler
	eventCallback     WorkflowEventCallback // Optional callback for real-time events
}

// WorkflowEventCallback is called during workflow execution for real-time updates
type WorkflowEventCallback func(event WorkflowEvent)

// WorkflowEvent represents an event during workflow execution
type WorkflowEvent struct {
	Type      WorkflowEventType
	Timestamp time.Time

	// Group context
	GroupID   string
	GroupName string

	// Agent context
	AgentID   string
	AgentName string

	// Event data
	Message string
	Output  string // Agent output text (for completed/failed events)
	Error   error

	// Progress data
	Progress float64 // 0.0 to 1.0
	Tokens   int
	Cost     float64
	Duration time.Duration

	// Intermediate agent activity (for real-time updates)
	IntermediateUpdate agent.IntermediateUpdate
}

// WorkflowEventType defines types of workflow events
type WorkflowEventType string

const (
	WorkflowEventStarted        WorkflowEventType = "workflow_started"
	WorkflowEventCompleted      WorkflowEventType = "workflow_completed"
	WorkflowEventFailed         WorkflowEventType = "workflow_failed"
	WorkflowEventGroupStarted   WorkflowEventType = "group_started"
	WorkflowEventGroupCompleted WorkflowEventType = "group_completed"
	WorkflowEventGroupFailed    WorkflowEventType = "group_failed"
	WorkflowEventAgentStarted   WorkflowEventType = "agent_started"
	WorkflowEventAgentCompleted WorkflowEventType = "agent_completed"
	WorkflowEventAgentFailed    WorkflowEventType = "agent_failed"
	WorkflowEventAgentActivity  WorkflowEventType = "agent_activity"
)

// WorkflowResult contains the results of a workflow execution.
type WorkflowResult struct {
	ModeID       string                  `json:"mode_id"`
	Status       WorkflowStatus          `json:"status"`
	GroupResults map[string]*GroupResult `json:"group_results"`
	StartTime    time.Time               `json:"start_time"`
	EndTime      time.Time               `json:"end_time"`
	Duration     time.Duration           `json:"duration"`
	Error        error                   `json:"error,omitempty"`
}

// WorkflowStatus represents the overall status of the workflow.
type WorkflowStatus string

const (
	WorkflowStatusPending   WorkflowStatus = "pending"
	WorkflowStatusRunning   WorkflowStatus = "running"
	WorkflowStatusCompleted WorkflowStatus = "completed"
	WorkflowStatusFailed    WorkflowStatus = "failed"
	WorkflowStatusCancelled WorkflowStatus = "cancelled"
)

// NewWorkflowEngine creates a new workflow engine.
func NewWorkflowEngine(m *Mode, logger observability.Logger, tracer observability.Tracer) *WorkflowEngine {
	if logger == nil {
		logger = noop.NewLogger()
	}
	if tracer == nil {
		tracer = noop.NewTracer()
	}
	// Create gate manager
	gateManager := gates.NewGateManager(logger, tracer, nil)

	// Create workflow engine (we'll set gate action handler after creation)
	engine := &WorkflowEngine{
		mode:        m,
		logger:      logger,
		tracer:      tracer,
		gateManager: gateManager,
	}

	// Create gate action handler (needs engine reference)
	engine.gateActionHandler = NewGateActionHandler(engine, logger, tracer)

	// Load global gates
	if len(m.Gates) > 0 {
		if err := gateManager.LoadGates(m.Gates); err != nil {
			logger.Error(context.Background(), "failed to load global gates",
				observability.F("error", err.Error()))
		}
	}

	// Load group gates
	for _, group := range m.Groups {
		if len(group.Gates) > 0 {
			if err := gateManager.LoadGates(group.Gates); err != nil {
				logger.Error(context.Background(), "failed to load gates for group",
					observability.F("group_id", group.ID),
					observability.F("error", err.Error()))
			}
		}
	}

	return engine
}

// SetEventCallback sets an optional callback for real-time workflow events
func (we *WorkflowEngine) SetEventCallback(callback WorkflowEventCallback) {
	we.eventCallback = callback
}

// emitEvent emits an event if a callback is registered
func (we *WorkflowEngine) emitEvent(event WorkflowEvent) {
	if we.eventCallback != nil {
		we.eventCallback(event)
	}
}

// Plan validates the workflow structure and returns an execution plan (topological sort).
// It returns an error if circular dependencies are detected.
func (we *WorkflowEngine) Plan() ([][]*AgentGroup, error) {
	// Build dependency graph
	graph := make(map[string][]string)
	inDegree := make(map[string]int)
	groups := make(map[string]*AgentGroup)

	for _, group := range we.mode.Groups {
		groups[group.ID] = group
		inDegree[group.ID] = 0 // Initialize
	}

	for _, group := range we.mode.Groups {
		for _, depID := range group.DependsOn {
			if _, exists := groups[depID]; !exists {
				return nil, fmt.Errorf("group %s depends on non-existent group %s", group.ID, depID)
			}
			graph[depID] = append(graph[depID], group.ID)
			inDegree[group.ID]++
		}
	}

	// Topological sort (Kahn's algorithm)
	var queue []string
	for id, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}

	processedCount := 0

	// We can't strictly return layers for parallel execution just from a simple topo sort list.
	// However, for the purpose of "Plan", returning a valid linear order is often sufficient,
	// or we can simulate execution to find layers.
	// For this implementation, let's just detect cycles and return a valid linear order
	// grouped by dependency layers if possible, or just a flat list.
	// Actually, a better approach for the engine is to determine runnable groups dynamically.
	// But Plan() is useful for validation.

	// Let's just validate cycle detection here.
	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]
		processedCount++

		for _, neighborID := range graph[currentID] {
			inDegree[neighborID]--
			if inDegree[neighborID] == 0 {
				queue = append(queue, neighborID)
			}
		}
	}

	if processedCount != len(we.mode.Groups) {
		return nil, fmt.Errorf("circular dependency detected in workflow")
	}

	return nil, nil // Plan is mainly for validation in this step
}

// DryRunResult contains the results of a dry-run validation.
type DryRunResult struct {
	Valid             bool                        `json:"valid"`
	ExecutionPlan     [][]string                  `json:"execution_plan"` // Groups organized by execution layer
	TotalGroups       int                         `json:"total_groups"`
	TotalAgents       int                         `json:"total_agents"`
	EstimatedDuration time.Duration               `json:"estimated_duration"`
	ValidationErrors  []string                    `json:"validation_errors,omitempty"`
	Warnings          []string                    `json:"warnings,omitempty"`
	GroupDetails      map[string]*GroupDryRunInfo `json:"group_details"`
}

// GroupDryRunInfo contains dry-run information about a group.
type GroupDryRunInfo struct {
	GroupID           string        `json:"group_id"`
	GroupName         string        `json:"group_name"`
	ExecutionStrategy string        `json:"execution_strategy"`
	AgentCount        int           `json:"agent_count"`
	Dependencies      []string      `json:"dependencies"`
	DependedOnBy      []string      `json:"depended_on_by"`
	EstimatedDuration time.Duration `json:"estimated_duration"`
	Layer             int           `json:"layer"` // Execution layer (0 = first, 1 = second, etc.)
}

// DryRun validates the workflow structure and simulates execution without calling LLMs.
// This is useful for testing workflows before actual execution.
func (we *WorkflowEngine) DryRun(ctx context.Context) (*DryRunResult, error) {
	ctx, span := we.tracer.StartSpan(ctx, "workflow.dry_run")
	defer span.End()

	we.logger.Info(ctx, "Starting workflow dry-run",
		observability.F("mode_id", we.mode.ID),
		observability.F("mode_name", we.mode.Name))

	result := &DryRunResult{
		Valid:            true,
		ValidationErrors: make([]string, 0),
		Warnings:         make([]string, 0),
		GroupDetails:     make(map[string]*GroupDryRunInfo),
		TotalGroups:      len(we.mode.Groups),
	}

	// Step 1: Validate mode structure
	if err := we.mode.Validate(); err != nil {
		result.Valid = false
		result.ValidationErrors = append(result.ValidationErrors, fmt.Sprintf("Mode validation failed: %v", err))
		return result, nil // Return result with errors, don't error out
	}

	// Step 2: Build dependency graph and detect cycles
	graph := make(map[string][]string)        // group_id -> dependent groups
	reverseGraph := make(map[string][]string) // group_id -> dependencies
	inDegree := make(map[string]int)
	groups := make(map[string]*AgentGroup)

	for _, group := range we.mode.Groups {
		groups[group.ID] = group
		inDegree[group.ID] = 0
		result.TotalAgents += len(group.Agents)
	}

	// Build graphs
	for _, group := range we.mode.Groups {
		for _, depID := range group.DependsOn {
			if _, exists := groups[depID]; !exists {
				result.Valid = false
				result.ValidationErrors = append(result.ValidationErrors,
					fmt.Sprintf("Group '%s' depends on non-existent group '%s'", group.ID, depID))
			} else {
				graph[depID] = append(graph[depID], group.ID)
				reverseGraph[group.ID] = append(reverseGraph[group.ID], depID)
				inDegree[group.ID]++
			}
		}
	}

	// Step 3: Topological sort to detect cycles and create execution plan
	var queue []string
	for id, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}

	processedCount := 0
	executionLayers := make([][]string, 0)
	groupLayers := make(map[string]int)

	// Process groups layer by layer
	layer := 0
	for len(queue) > 0 {
		// All groups in queue can execute in parallel (same layer)
		currentLayer := make([]string, len(queue))
		copy(currentLayer, queue)
		executionLayers = append(executionLayers, currentLayer)

		// Assign layer to groups
		for _, id := range currentLayer {
			groupLayers[id] = layer
		}

		// Process current layer
		nextQueue := make([]string, 0)
		for _, currentID := range queue {
			processedCount++

			// Reduce in-degree for dependent groups
			for _, neighborID := range graph[currentID] {
				inDegree[neighborID]--
				if inDegree[neighborID] == 0 {
					nextQueue = append(nextQueue, neighborID)
				}
			}
		}

		queue = nextQueue
		layer++
	}

	// Check for circular dependencies
	if processedCount != len(we.mode.Groups) {
		result.Valid = false
		result.ValidationErrors = append(result.ValidationErrors, "Circular dependency detected in workflow")
	}

	result.ExecutionPlan = executionLayers

	// Step 4: Analyze each group
	var totalEstimatedDuration time.Duration
	for _, group := range we.mode.Groups {
		info := &GroupDryRunInfo{
			GroupID:           group.ID,
			GroupName:         group.Name,
			ExecutionStrategy: string(group.Execution),
			AgentCount:        len(group.Agents),
			Dependencies:      group.DependsOn,
			DependedOnBy:      graph[group.ID],
			Layer:             groupLayers[group.ID],
		}

		// Estimate duration based on group timeout or default
		if group.Timeout > 0 {
			info.EstimatedDuration = group.Timeout
		} else {
			// Default estimates based on agent count and execution strategy
			switch group.Execution {
			case ExecutionParallel:
				// Parallel: ~5 minutes per group (max agent time)
				info.EstimatedDuration = 5 * time.Minute
			case ExecutionSequential:
				// Sequential: ~3 minutes per agent
				info.EstimatedDuration = time.Duration(len(group.Agents)) * 3 * time.Minute
			case ExecutionAdversarial:
				// Adversarial: ~5 minutes per agent (multiple rounds)
				info.EstimatedDuration = time.Duration(len(group.Agents)) * 5 * time.Minute
			default:
				info.EstimatedDuration = 5 * time.Minute
			}
		}

		result.GroupDetails[group.ID] = info

		// Warnings
		if len(group.Agents) == 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Group '%s' has no agents", group.ID))
		}

		if len(group.Agents) > 10 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Group '%s' has many agents (%d) which may be slow and expensive", group.ID, len(group.Agents)))
		}

		if group.Execution == ExecutionParallel && len(group.Agents) > 5 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Group '%s' has %d parallel agents which may hit rate limits", group.ID, len(group.Agents)))
		}
	}

	// Step 5: Calculate total estimated duration (layer by layer)
	for _, layer := range executionLayers {
		// Find max duration in this layer (parallel execution)
		var maxDuration time.Duration
		for _, groupID := range layer {
			if info, ok := result.GroupDetails[groupID]; ok {
				if info.EstimatedDuration > maxDuration {
					maxDuration = info.EstimatedDuration
				}
			}
		}
		totalEstimatedDuration += maxDuration
	}

	result.EstimatedDuration = totalEstimatedDuration

	// Mode-level timeout check
	if we.mode.Config.MaxDuration > 0 && totalEstimatedDuration > we.mode.Config.MaxDuration {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Estimated duration (%v) exceeds mode max duration (%v)", totalEstimatedDuration, we.mode.Config.MaxDuration))
	}

	// Step 6: Log summary
	we.logger.Info(ctx, "Dry-run completed",
		observability.F("valid", result.Valid),
		observability.F("total_groups", result.TotalGroups),
		observability.F("total_agents", result.TotalAgents),
		observability.F("execution_layers", len(executionLayers)),
		observability.F("estimated_duration", totalEstimatedDuration.String()),
		observability.F("errors", len(result.ValidationErrors)),
		observability.F("warnings", len(result.Warnings)))

	return result, nil
}

// Execute runs the workflow.
func (we *WorkflowEngine) Execute(ctx context.Context, input string, factory agent.Factory) (*WorkflowResult, error) {
	ctx, span := we.tracer.StartSpan(ctx, "workflow.execute")
	defer span.End()

	observability.AddModeAttributes(span, we.mode.ID, we.mode.Name)

	// Validate first
	if _, err := we.Plan(); err != nil {
		observability.RecordError(ctx, we.logger, span, err, "workflow.plan_failed")
		return nil, sdkerr.Wrap(
			err,
			"mode.workflow.plan_failed",
			sdkerr.WithOperation("mode.workflow.execute"),
			sdkerr.WithComponent("mode.workflow"),
			sdkerr.WithTraceFromContext(ctx),
		)
	}

	// Evaluate workflow_starting gates
	startTime := time.Now()
	evalCtx := &gates.EvaluationContext{
		Workflow:        we.mode,
		AllGroupResults: make(map[string]any),
		WorkflowContext: make(map[string]any),
		ElapsedTime:     0,
		TotalCost:       0,
		TotalTokens:     0,
		Logger:          we.logger,
		Tracer:          we.tracer,
	}

	gateResult, err := we.gateManager.EvaluateGates(ctx, gates.TriggerWorkflowStarting, evalCtx)
	if err != nil {
		return nil, sdkerr.Wrap(
			err,
			"mode.workflow.gate_evaluation_failed",
			sdkerr.WithOperation("mode.workflow.evaluate_start_gates"),
			sdkerr.WithComponent("mode.workflow"),
			sdkerr.WithTraceFromContext(ctx),
		)
	}

	if !gateResult.Allow {
		return &WorkflowResult{
			ModeID:    we.mode.ID,
			Status:    WorkflowStatusFailed,
			StartTime: startTime,
			EndTime:   time.Now(),
			Error: sdkerr.Steering(
				"mode.workflow.blocked_by_gate",
				fmt.Sprintf("workflow blocked by gate: %s", gateResult.Reason),
				sdkerr.WithOperation("mode.workflow.evaluate_start_gates"),
				sdkerr.WithComponent("mode.workflow"),
				sdkerr.WithTraceFromContext(ctx),
			),
		}, nil
	}

	result := &WorkflowResult{
		ModeID:       we.mode.ID,
		Status:       WorkflowStatusRunning,
		GroupResults: make(map[string]*GroupResult),
		StartTime:    startTime,
	}

	// Track completion status of groups
	completedGroups := make(map[string]bool)
	var mu sync.Mutex

	// Map group IDs to groups for easy access
	groups := make(map[string]*AgentGroup)
	for _, group := range we.mode.Groups {
		groups[group.ID] = group
	}

	// Channel to signal group completion
	// Buffer size = number of groups to prevent blocking
	completionCh := make(chan *GroupResult, len(we.mode.Groups))

	// Error channel
	errCh := make(chan error, len(we.mode.Groups))

	// WaitGroup for all active goroutines
	var wg sync.WaitGroup

	// Helper to start runnable groups
	startRunnableGroups := func() {
		mu.Lock()
		defer mu.Unlock()

		for _, group := range we.mode.Groups {
			// Skip if already completed or running (we check results map for running/completed)
			if _, exists := result.GroupResults[group.ID]; exists {
				continue
			}

			// Check dependencies
			allDepsMet := true
			for _, depID := range group.DependsOn {
				if !completedGroups[depID] {
					allDepsMet = false
					break
				}
			}

			if allDepsMet {
				// Evaluate group_starting gates
				evalCtx.Group = group
				evalCtx.ElapsedTime = time.Since(startTime)

				gateResult, err := we.gateManager.EvaluateGates(ctx, gates.TriggerGroupStarting, evalCtx)
				if err != nil {
					errCh <- sdkerr.Wrap(
						err,
						"mode.workflow.group_start_gate_failed",
						sdkerr.WithOperation("mode.workflow.evaluate_group_start_gates"),
						sdkerr.WithComponent("mode.workflow"),
						sdkerr.WithTraceFromContext(ctx),
						sdkerr.WithAttr("group_id", group.ID),
						sdkerr.WithAttr("group_name", group.Name),
					)
					continue
				}

				if !gateResult.Allow {
					// Handle gate actions
					switch gateResult.Action {
					case gates.ActionSkipGroup:
						// Mark as completed (skipped)
						result.GroupResults[group.ID] = &GroupResult{
							GroupID:   group.ID,
							GroupName: group.Name,
							Status:    GroupStatusCompleted,
							Results:   make(map[string]*AgentResult),
							StartTime: time.Now(),
							EndTime:   time.Now(),
						}
						completedGroups[group.ID] = true
						continue

					case gates.ActionBlock:
						errCh <- sdkerr.Steering(
							"mode.workflow.group_blocked_by_gate",
							fmt.Sprintf("group %s blocked by gate: %s", group.Name, gateResult.Reason),
							sdkerr.WithOperation("mode.workflow.evaluate_group_start_gates"),
							sdkerr.WithComponent("mode.workflow"),
							sdkerr.WithTraceFromContext(ctx),
							sdkerr.WithAttr("group_id", group.ID),
						)
						continue

					default:
						// For other actions, fail for now
						errCh <- sdkerr.Steering(
							"mode.workflow.group_blocked_by_gate",
							fmt.Sprintf("group %s blocked by gate: %s", group.Name, gateResult.Reason),
							sdkerr.WithOperation("mode.workflow.evaluate_group_start_gates"),
							sdkerr.WithComponent("mode.workflow"),
							sdkerr.WithTraceFromContext(ctx),
							sdkerr.WithAttr("group_id", group.ID),
						)
						continue
					}
				}

				// Mark as running (placeholder in results)
				result.GroupResults[group.ID] = &GroupResult{Status: GroupStatusRunning}

				// Start group execution
				wg.Add(1)
				go func(g *AgentGroup) {
					defer wg.Done()

					// Build context for this group from dependencies
					mu.Lock()
					contextBuilder := NewContextBuilder(input, result.GroupResults)
					groupInput := contextBuilder.BuildForGroup(g)
					mu.Unlock()

					coordinator := NewGroupCoordinator(g, we.logger, we.tracer)

					// Wire intermediate callback for real-time agent activity updates.
					// The coordinator wraps each agent's callback with SubAgentUpdate
					// carrying agent ID/name, which we unwrap here into the event.
					if we.eventCallback != nil {
						// Pass event callback to coordinator for agent lifecycle events
						coordinator.SetEventCallback(we.eventCallback)

						coordinator.SetIntermediateCallback(func(ctx context.Context, update agent.IntermediateUpdate) error {
							evt := WorkflowEvent{
								Type:      WorkflowEventAgentActivity,
								Timestamp: time.Now(),
								GroupID:   g.ID,
								GroupName: g.Name,
							}
							// Unwrap SubAgentUpdate to extract agent identity
							if sub, ok := update.(agent.SubAgentUpdate); ok {
								evt.AgentID = sub.AgentID
								evt.AgentName = sub.AgentName
								evt.IntermediateUpdate = sub.Update
							} else {
								evt.IntermediateUpdate = update
							}
							we.emitEvent(evt)
							return nil
						})

						// Emit group_started event
						we.emitEvent(WorkflowEvent{
							Type:      WorkflowEventGroupStarted,
							Timestamp: time.Now(),
							GroupID:   g.ID,
							GroupName: g.Name,
						})
					}

					res, err := coordinator.Execute(ctx, groupInput, factory)

					// Emit group lifecycle event
					if we.eventCallback != nil {
						if err != nil || res.Status != GroupStatusCompleted {
							groupErr := err
							if groupErr == nil && res.Error != nil {
								groupErr = res.Error
							}
							we.emitEvent(WorkflowEvent{
								Type:      WorkflowEventGroupFailed,
								Timestamp: time.Now(),
								GroupID:   g.ID,
								GroupName: g.Name,
								Error:     groupErr,
								Duration:  res.Duration,
							})
						} else {
							totalTok := 0
							for _, ar := range res.Results {
								totalTok += ar.Tokens
							}
							we.emitEvent(WorkflowEvent{
								Type:      WorkflowEventGroupCompleted,
								Timestamp: time.Now(),
								GroupID:   g.ID,
								GroupName: g.Name,
								Duration:  res.Duration,
								Tokens:    totalTok,
							})
						}
					}

					if err != nil {
						errCh <- sdkerr.Wrap(
							err,
							"mode.workflow.group_execution_failed",
							sdkerr.WithOperation("mode.workflow.execute_group"),
							sdkerr.WithComponent("mode.workflow"),
							sdkerr.WithTraceFromContext(ctx),
							sdkerr.WithAttr("group_id", g.ID),
							sdkerr.WithAttr("group_name", g.Name),
						)
					}

					// Update context for gate evaluation
					mu.Lock()
					evalCtx.GroupResult = res
					evalCtx.AllGroupResults[g.ID] = res
					evalCtx.ElapsedTime = time.Since(startTime)
					// Update cost and tokens (simplified - should be from actual execution)
					for _, agentRes := range res.Results {
						evalCtx.TotalTokens += agentRes.Tokens
						evalCtx.TotalCost += agentRes.Cost
					}
					mu.Unlock()

					// Evaluate group_completed gates
					gateResult, gateErr := we.gateManager.EvaluateGates(ctx, gates.TriggerGroupCompleted, evalCtx)
					if gateErr != nil {
						errCh <- sdkerr.Wrap(
							gateErr,
							"mode.workflow.group_complete_gate_failed",
							sdkerr.WithOperation("mode.workflow.evaluate_group_complete_gates"),
							sdkerr.WithComponent("mode.workflow"),
							sdkerr.WithTraceFromContext(ctx),
							sdkerr.WithAttr("group_id", g.ID),
							sdkerr.WithAttr("group_name", g.Name),
						)
						return
					}

					// Handle gate result (retry, route, block, etc.)
					if !gateResult.Allow {
						handledResult, handleErr := we.gateActionHandler.HandleGateResult(ctx, gateResult, g, groupInput, factory)
						if handleErr != nil {
							errCh <- sdkerr.Wrap(
								handleErr,
								"mode.workflow.gate_action_failed",
								sdkerr.WithOperation("mode.workflow.handle_gate_action"),
								sdkerr.WithComponent("mode.workflow"),
								sdkerr.WithTraceFromContext(ctx),
								sdkerr.WithAttr("group_id", g.ID),
								sdkerr.WithAttr("group_name", g.Name),
							)
							return
						}
						if handledResult != nil {
							res = handledResult
						}
					}

					completionCh <- res
				}(group)
			}
		}
	}

	// Start initial groups
	startRunnableGroups()

	// Monitor execution.
	//
	// Policy knobs (from we.mode.Config):
	//
	//   FailOnSteeringBlock — when false (default), gate blocks on a group are
	//     treated as skips rather than fatal errors.  The group is marked
	//     completed (skipped) and the pipeline continues.
	//
	//   TimeoutBehavior:
	//     "fail"    — cancel remaining work and return an error (hard stop).
	//     "partial" — return completed group results; incomplete groups are
	//                 marked with status GroupStatusCancelled. (default)
	//     "continue"— not yet implemented; behaves as "partial".
	//
	// When a non-steering-block error arrives on errCh (a genuine execution
	// failure) the workflow always fails fast — partial results from already-
	// completed groups are preserved in result.GroupResults.
	completedCount := 0
	totalGroups := len(we.mode.Groups)
	failOnBlock := we.mode.Config.FailOnSteeringBlock

	for completedCount < totalGroups {
		select {
		case <-ctx.Done():
			// Context cancelled or deadline exceeded.
			timeoutBehavior := we.mode.Config.TimeoutBehavior
			if timeoutBehavior == "fail" {
				result.Status = WorkflowStatusFailed
			} else {
				// "partial" or "continue": return what completed.
				result.Status = WorkflowStatusCancelled
			}
			result.Error = sdkerr.Wrap(
				ctx.Err(),
				"mode.workflow.cancelled",
				sdkerr.WithOperation("mode.workflow.execute"),
				sdkerr.WithComponent("mode.workflow"),
				sdkerr.WithTraceFromContext(ctx),
			)
			result.EndTime = time.Now()
			result.Duration = result.EndTime.Sub(result.StartTime)
			observability.RecordError(ctx, we.logger, span, result.Error, "workflow.cancelled")
			return result, result.Error

		case err := <-errCh:
			// An async group goroutine sent an infrastructure error (gate eval
			// failure, execution error, etc.).
			//
			// Distinguish steering-block errors from genuine failures:
			// sdkerr.Steering() produces errors with code containing "blocked_by_gate".
			isBlock := false
			if err != nil {
				if se, ok := err.(interface{ Code() string }); ok {
					code := se.Code()
					isBlock = strings.Contains(code, "blocked_by_gate") ||
						strings.Contains(code, "gate_blocked")
				}
			}
			if isBlock && !failOnBlock {
				// Gate block on a non-critical path: log and continue.
				we.logger.Warn(ctx, "workflow.group_blocked_by_gate.skipped",
					observability.F("error", err.Error()))
				// The group that was blocked was never added to completedGroups,
				// so we increment completedCount here so the loop can finish.
				mu.Lock()
				completedCount++
				mu.Unlock()
				startRunnableGroups()
				continue
			}
			// Genuine failure — fail fast, preserve partial results.
			result.Status = WorkflowStatusFailed
			result.Error = sdkerr.Wrap(
				err,
				"mode.workflow.group_failed",
				sdkerr.WithOperation("mode.workflow.execute"),
				sdkerr.WithComponent("mode.workflow"),
				sdkerr.WithTraceFromContext(ctx),
			)
			result.EndTime = time.Now()
			result.Duration = result.EndTime.Sub(result.StartTime)
			observability.RecordError(ctx, we.logger, span, result.Error, "workflow.group_failed")
			return result, result.Error

		case res := <-completionCh:
			mu.Lock()
			result.GroupResults[res.GroupID] = res
			if res.Status == GroupStatusCompleted {
				completedGroups[res.GroupID] = true
				completedCount++
			} else {
				// Group completed with a non-success status (failed / timeout).
				// Fail fast; partial results from already-completed groups are
				// preserved in result.GroupResults for diagnostics.
				result.Status = WorkflowStatusFailed
				groupErr := sdkerr.Permanent(
					"mode.workflow.group_failed",
					fmt.Sprintf("group %s failed with status %s", res.GroupName, res.Status),
					sdkerr.WithOperation("mode.workflow.execute"),
					sdkerr.WithComponent("mode.workflow"),
					sdkerr.WithTraceFromContext(ctx),
					sdkerr.WithAttr("group_id", res.GroupID),
					sdkerr.WithAttr("group_name", res.GroupName),
					sdkerr.WithAttr("group_status", res.Status),
				)
				if res.Error != nil {
					groupErr = sdkerr.Wrap(
						res.Error,
						"mode.workflow.group_failed",
						sdkerr.WithOperation("mode.workflow.execute"),
						sdkerr.WithComponent("mode.workflow"),
						sdkerr.WithTraceFromContext(ctx),
						sdkerr.WithAttr("group_id", res.GroupID),
						sdkerr.WithAttr("group_name", res.GroupName),
						sdkerr.WithAttr("group_status", res.Status),
					)
				}
				result.Error = groupErr
				mu.Unlock()
				result.EndTime = time.Now()
				result.Duration = result.EndTime.Sub(result.StartTime)
				observability.RecordError(ctx, we.logger, span, result.Error, "workflow.group_execution_failed")
				return result, result.Error
			}
			mu.Unlock()
			// Kick off any newly unblocked groups.
			startRunnableGroups()
		}
	}

	// All groups completed successfully.
	result.Status = WorkflowStatusCompleted
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)
	we.logger.Info(ctx, "workflow.completed",
		observability.F("mode_id", we.mode.ID),
		observability.F("duration", result.Duration.Seconds()))
	return result, nil
}
