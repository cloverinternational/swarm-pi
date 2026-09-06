// Package hooks implements the event hook system.
// This file provides the HookEventHandler for executing hooks.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/pkg/pool"
)

// HookEventHandler handles hook execution requests.
type HookEventHandler struct {
	system     *HookSystem
	aggregator *HookAggregator
	translator Translator
}

// Global pool manager for hook execution
var (
	hookPoolManager *pool.Manager
	hookPoolMu      sync.RWMutex
)

// SetPoolManager sets the global pool manager for hook execution
func SetPoolManager(pm *pool.Manager) {
	hookPoolMu.Lock()
	defer hookPoolMu.Unlock()
	hookPoolManager = pm
}

// getPoolManager returns the global pool manager
func getPoolManager() *pool.Manager {
	hookPoolMu.RLock()
	defer hookPoolMu.RUnlock()
	return hookPoolManager
}

// NewHookEventHandler creates a new hook event handler.
func NewHookEventHandler(system *HookSystem) *HookEventHandler {
	return &HookEventHandler{
		system:     system,
		aggregator: system.aggregator,
		translator: system.translator,
	}
}

// HandleRequest handles a hook execution request from the message bus.
func (h *HookEventHandler) HandleRequest(ctx context.Context, msg Message) error {
	req, ok := msg.(*HookExecutionRequest)
	if !ok {
		return fmt.Errorf("unexpected message type")
	}

	// Extract input and plan from request
	input := req.Input["input"]
	plan, ok := req.Input["plan"].(*ExecutionPlan)
	if !ok {
		return h.sendResponse(ctx, req.CorrelationID(), false, nil, fmt.Errorf("invalid execution plan"))
	}

	// Execute hooks
	results, err := h.executeHooks(ctx, plan, input)
	if err != nil {
		return h.sendResponse(ctx, req.CorrelationID(), false, nil, err)
	}

	// Aggregate results
	aggregated := h.aggregator.AggregateResults(results, plan.EventName)

	return h.sendResponse(ctx, req.CorrelationID(), aggregated.Success, aggregated.FinalOutput, nil)
}

// sendResponse sends a response back via the message bus.
func (h *HookEventHandler) sendResponse(ctx context.Context, correlationID string, success bool, output *HookResponse, err error) error {
	resp := NewHookExecutionResponse(correlationID, success, output, err)
	return h.system.messageBus.Publish(ctx, resp)
}

// executeHooks executes all hooks in the plan.
func (h *HookEventHandler) executeHooks(ctx context.Context, plan *ExecutionPlan, input any) ([]HookExecutionResult, error) {
	if plan.Sequential {
		return h.executeSequential(ctx, plan, input)
	}
	return h.executeParallel(ctx, plan, input)
}

// executeSequential executes hooks one at a time.
func (h *HookEventHandler) executeSequential(ctx context.Context, plan *ExecutionPlan, input any) ([]HookExecutionResult, error) {
	results := make([]HookExecutionResult, 0, len(plan.Hooks))

	for _, hook := range plan.Hooks {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		result := h.executeHook(ctx, hook, plan.EventName, input)
		results = append(results, result)

		// Stop on blocking decision
		if result.Output != nil && result.Output.Decision.IsBlocking() {
			break
		}
	}

	return results, nil
}

// executeParallel executes hooks concurrently.
func (h *HookEventHandler) executeParallel(ctx context.Context, plan *ExecutionPlan, input any) ([]HookExecutionResult, error) {
	results := make([]HookExecutionResult, len(plan.Hooks))
	var wg sync.WaitGroup

	// Check if we have a pool manager
	poolManager := getPoolManager()
	usePool := poolManager != nil && len(plan.Hooks) > 2

	for i, hook := range plan.Hooks {
		wg.Add(1)
		idx, hk := i, hook // Capture loop variables

		executeFunc := func() {
			defer wg.Done()
			results[idx] = h.executeHook(ctx, hk, plan.EventName, input)
		}

		if usePool {
			// Submit to pool
			err := poolManager.Submit(ctx, pool.PoolTypeHooks, executeFunc)
			if err != nil {
				// Pool is full, fall back to goroutine
				go executeFunc()
			}
		} else {
			// Direct goroutine execution
			go executeFunc()
		}
	}

	wg.Wait()
	return results, nil
}

// executeHook executes a single hook command.
// Output is written to temp files to avoid buffer issues and enable recovery.
func (h *HookEventHandler) executeHook(ctx context.Context, hook CommandHookConfig, eventName HookEventName, input any) HookExecutionResult {
	start := time.Now()
	result := HookExecutionResult{
		HookConfig: hook,
		EventName:  eventName,
	}

	// Prepare input JSON
	inputJSON, err := json.Marshal(input)
	if err != nil {
		result.Error = fmt.Errorf("failed to marshal input: %w", err)
		result.Duration = time.Since(start)
		return result
	}

	// Determine timeout
	timeout := time.Duration(hook.Timeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create temp files for stdout and stderr
	stdoutFile, err := os.CreateTemp("/tmp", "hook-event-stdout-*.txt")
	if err != nil {
		result.Error = fmt.Errorf("failed to create stdout temp file: %w", err)
		result.Duration = time.Since(start)
		return result
	}
	stdoutPath := stdoutFile.Name()
	defer os.Remove(stdoutPath)

	stderrFile, err := os.CreateTemp("/tmp", "hook-event-stderr-*.txt")
	if err != nil {
		stdoutFile.Close()
		os.Remove(stdoutPath)
		result.Error = fmt.Errorf("failed to create stderr temp file: %w", err)
		result.Duration = time.Since(start)
		return result
	}
	stderrPath := stderrFile.Name()
	defer os.Remove(stderrPath)

	// Execute command
	cmd := exec.CommandContext(execCtx, "sh", "-c", hook.Command)
	cmd.Stdin = bytes.NewReader(inputJSON)
	cmd.WaitDelay = 2 * time.Second

	// Redirect output to temp files
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	// Set environment variables
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("HOOK_EVENT=%s", eventName),
		fmt.Sprintf("HOOK_NAME=%s", hook.Name),
	)

	// Run command
	cmdErr := cmd.Run()

	// Close files before reading
	stdoutFile.Close()
	stderrFile.Close()

	// Read output from temp files
	stdoutBytes, _ := os.ReadFile(stdoutPath)
	stderrBytes, _ := os.ReadFile(stderrPath)

	result.Duration = time.Since(start)
	result.Stdout = string(stdoutBytes)
	result.Stderr = string(stderrBytes)

	// Get exit code
	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.Error = cmdErr
			return result
		}
	}

	// Parse output based on exit code (Claude Code semantics)
	// Exit code 0: Success
	// Exit code 1: Non-blocking error (warning)
	// Exit code 2: Blocking error (deny/block)
	switch result.ExitCode {
	case 0:
		result.Success = true
		output, err := h.translator.FromHookResponse(eventName, stdoutBytes)
		if err != nil {
			result.Output = &HookResponse{Decision: DecisionAllow}
		} else {
			result.Output = output
		}
	case 1:
		// Non-blocking error - treat as warning but allow
		result.Success = true
		output, err := h.translator.FromHookResponse(eventName, stdoutBytes)
		if err != nil {
			result.Output = &HookResponse{
				Decision:      DecisionAllow,
				SystemMessage: string(stderrBytes),
			}
		} else {
			result.Output = output
			if result.Output.SystemMessage == "" {
				result.Output.SystemMessage = string(stderrBytes)
			}
		}
	case 2:
		// Blocking error
		result.Success = false
		output, err := h.translator.FromHookResponse(eventName, stdoutBytes)
		if err != nil {
			result.Output = &HookResponse{
				Decision: DecisionBlock,
				Reason:   string(stderrBytes),
			}
		} else {
			result.Output = output
			if result.Output.Decision == DecisionAllow {
				result.Output.Decision = DecisionBlock
			}
			if result.Output.Reason == "" {
				result.Output.Reason = string(stderrBytes)
			}
		}
	default:
		// Other exit codes treated as errors
		result.Success = false
		result.Output = &HookResponse{
			Decision: DecisionBlock,
			Reason:   fmt.Sprintf("hook exited with code %d: %s", result.ExitCode, string(stderrBytes)),
		}
	}

	return result
}

// ExecuteHookDirect executes a hook directly without the message bus.
// Useful for synchronous execution outside the main flow.
func (h *HookEventHandler) ExecuteHookDirect(ctx context.Context, hook CommandHookConfig, eventName HookEventName, data map[string]any) (*HookResponse, error) {
	// Convert to hook input
	input, err := h.translator.ToHookInput(eventName, data, h.system.sessionID, h.system.cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to translate input: %w", err)
	}

	result := h.executeHook(ctx, hook, eventName, input)
	if result.Error != nil {
		return nil, result.Error
	}

	return result.Output, nil
}

// HookRunner provides a simpler interface for running hooks.
type HookRunner struct {
	system *HookSystem
}

// NewHookRunner creates a new hook runner.
func NewHookRunner(system *HookSystem) *HookRunner {
	return &HookRunner{system: system}
}

// Run executes hooks for the given event and returns whether to proceed.
func (r *HookRunner) Run(ctx context.Context, eventName HookEventName, data map[string]any) (bool, *HookResponse, error) {
	result, err := r.system.FireEvent(ctx, eventName, data)
	if err != nil {
		return false, nil, err
	}

	if result.FinalOutput == nil {
		return true, &HookResponse{Decision: DecisionAllow}, nil
	}

	// Check if we should proceed
	shouldProceed := !result.FinalOutput.Decision.IsBlocking()
	if result.FinalOutput.Continue != nil && !*result.FinalOutput.Continue {
		shouldProceed = false
	}

	return shouldProceed, result.FinalOutput, nil
}

// RunBeforeTool is a convenience method for before tool events.
func (r *HookRunner) RunBeforeTool(ctx context.Context, toolName string, toolInput map[string]any) (bool, *HookResponse, error) {
	return r.Run(ctx, EventBeforeTool, map[string]any{
		"tool_name":  toolName,
		"tool_input": toolInput,
	})
}

// RunAfterTool is a convenience method for after tool events.
func (r *HookRunner) RunAfterTool(ctx context.Context, toolName string, toolInput, toolResponse map[string]any) (bool, *HookResponse, error) {
	return r.Run(ctx, EventAfterTool, map[string]any{
		"tool_name":     toolName,
		"tool_input":    toolInput,
		"tool_response": toolResponse,
	})
}

// RunBeforeAgent is a convenience method for before agent events.
func (r *HookRunner) RunBeforeAgent(ctx context.Context, prompt string) (bool, *HookResponse, error) {
	return r.Run(ctx, EventBeforeAgent, map[string]any{
		"prompt": prompt,
	})
}

// RunAfterAgent is a convenience method for after agent events.
func (r *HookRunner) RunAfterAgent(ctx context.Context, prompt, response string) (bool, *HookResponse, error) {
	return r.Run(ctx, EventAfterAgent, map[string]any{
		"prompt":          prompt,
		"prompt_response": response,
	})
}
