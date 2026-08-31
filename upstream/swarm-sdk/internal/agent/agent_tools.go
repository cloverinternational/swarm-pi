package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolmetrics"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// debugLogPath for hook tracing
const agentDebugLogPath = "/tmp/hooks-debug.log"

// logAgentDebug writes a debug message to the log file
func logAgentDebug(format string, args ...any) {
	f, err := os.OpenFile(agentDebugLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "["+time.Now().Format("15:04:05.000")+"] "+format+"\n", args...)
}

// snapshotToolParameters returns a shallow copy of a tool call's parameter
// map for inclusion in a ToolCallUpdate.
//
// WHY THIS EXISTS (issue #189): ToolCallUpdate.Parameters used to carry the
// *same* map[string]any reference as toolCall.Parameters, which the agent
// goes on to execute against immediately after emitting the update
// (exec.Execute(ctx, toolCall.Name, toolCall.Parameters) a few lines below
// each emission site, or a hook's EmitToolBeforeExecute/EmitToolAfterExecute
// pass). Some tool/hook implementations mutate their params map in place
// (e.g. filling in a default, normalizing a path, or attaching parsed
// arguments under a new key). callIntermediateCallback hands the update off
// to the UI without waiting for the tool to finish, so the UI's activity
// describer (swarm-tui's DefaultToolActivityDescriber.Describe, which reads
// params[key] for display text) can run concurrently on a different
// goroutine with exactly that in-place mutation. A plain map has no
// synchronization, so Go's runtime detects the concurrent read+write and
// calls runtime/internal/maps.fatal — an unrecoverable process crash (not a
// panic that can be caught), exactly matching the reported stack trace
// (chat.stringParam -> chat.(*DefaultToolActivityDescriber).Describe ->
// chat.(*ActivityStateManager).BeginTool).
//
// Snapshotting the map at emission time is also the semantically correct
// behavior independent of the race: a "tool call" notification should
// describe the arguments AS THEY WERE AT CALL TIME, not however they look
// after the tool or a hook has since rewritten them.
//
// A shallow copy is sufficient: the race is on top-level key read/write
// (params[key] = value), and every current UI consumer only reads top-level
// string/bool/number values out of the map.
func snapshotToolParameters(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	clone := make(map[string]any, len(params))
	for k, v := range params {
		clone[k] = v
	}
	return clone
}

// hooksEnabled reports whether tool hook emission should run for the current
// turn. It is false when no hooks manager is attached OR when the active
// ExecuteRequest set DisableHooks (per-turn opt-out). Reads reqDisableHooks
// under the lock for consistency with execute()'s writer.
func (a *Agent) hooksEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hooksManager != nil && !a.reqDisableHooks
}

func (a *Agent) executeTools(ctx context.Context, message *conversation.Message) ([]*conversation.Message, error) {
	// Delegate to parallel execution implementation
	return a.executeToolsWithParallelism(ctx, message)
}

// toolExecutionResult holds the result of a tool execution along with metadata.
type toolExecutionResult struct {
	toolCall   conversation.ToolCall
	result     *tools.ToolResult
	err        error
	errorMsg   string
	errorType  string
	skipped    bool // true if tool was skipped (not found, blocked by hook, etc.)
	skipReason string
	// hookContext is additional context injected by post-execute hooks (e.g. task nudges).
	// It is returned as a standalone RoleUser message so it is a first-class entry in
	// conversation history — visible to the model, persisted to the DB, and not buried
	// inside the tool output text.
	hookContext string
}

// isPolicySkip reports whether the tool deliberately did not run because the
// hook policy blocked it or halted its batch. These are non-execution outcomes,
// not tool failures: errorMsg remains model-visible, but err and ToolError must
// stay nil so downstream storage and classifiers do not count them as failures.
func (r *toolExecutionResult) isPolicySkip() bool {
	if r == nil || !r.skipped {
		return false
	}
	return r.skipReason == "blocked_by_hook" || r.skipReason == "batch_blocked"
}

// buildToolInvocations converts conversation.ToolCall slice to tools.ToolInvocation slice.
// It filters out streaming tools which need special handling.
func (a *Agent) buildToolInvocations(ctx context.Context, toolCalls []conversation.ToolCall) ([]*tools.ToolInvocation, []conversation.ToolCall) {
	a.mu.RLock()
	agentID := ""
	if a.definition != nil {
		agentID = a.definition.ID
	}
	conversationID := a.conversationID
	mode := ""
	if a.requestContext != nil {
		if modeValue, ok := a.requestContext["mode"].(string); ok {
			mode = modeValue
		}
	}
	executionMetadata := hosted.ExecutionMetadataFromMap(a.requestContext)
	a.mu.RUnlock()

	var invocations []*tools.ToolInvocation
	var streamingCalls []conversation.ToolCall

	for i, tc := range toolCalls {
		// Check if tool exists and is streaming
		tool, err := a.toolReg.Get(tc.Name)
		if err != nil || tool == nil {
			// Tool not found - will be handled during execution
			// Still create invocation so error is properly returned
			invocations = append(invocations, &tools.ToolInvocation{
				ID:          tc.ID,
				Name:        tc.Name,
				Parameters:  tc.Parameters,
				SourceIndex: i,
				Context: tools.ToolInvocationContext{
					AgentID:        agentID,
					ConversationID: conversationID,
					Mode:           mode,
					Hosted:         executionMetadata,
				},
			})
			continue
		}

		// Check if this is a streaming tool
		if _, isStreaming := tool.(tools.StreamingTool); isStreaming {
			// Streaming tools need sequential execution with callbacks
			streamingCalls = append(streamingCalls, tc)
			continue
		}

		// Non-streaming tool - can be parallelized
		invocations = append(invocations, &tools.ToolInvocation{
			ID:          tc.ID,
			Name:        tc.Name,
			Parameters:  tc.Parameters,
			SourceIndex: i,
			Context: tools.ToolInvocationContext{
				AgentID:        agentID,
				ConversationID: conversationID,
				Mode:           mode,
				Hosted:         executionMetadata,
			},
		})
	}

	return invocations, streamingCalls
}

// preExecuteToolChecks performs pre-execution checks for a tool call.
// Returns (skipResult, preHookContext). skipResult is non-nil when the tool
// should be skipped (not found, blocked by hook). preHookContext carries any
// AdditionalContext from pre-tool hooks (e.g. steering focus reminders) that
// must be injected into the agent's conversation.
func (a *Agent) preExecuteToolChecks(ctx context.Context, toolCall conversation.ToolCall) (*toolExecutionResult, string) {
	// Get tool from registry
	tool, err := a.toolReg.Get(toolCall.Name)
	if err != nil || tool == nil {
		if err == nil {
			err = fmt.Errorf("tool '%s' not found", toolCall.Name)
		}
		return &toolExecutionResult{
			toolCall:   toolCall,
			err:        err,
			errorMsg:   fmt.Sprintf("Error: Tool '%s' not found", toolCall.Name),
			errorType:  "tool.not_found",
			skipped:    true,
			skipReason: "tool_not_found",
		}, ""
	}

	// Bench/observational capture: record that this tool call is about to
	// run. This is the single funnel both dispatch phases pass through
	// (Phase 1 for parallelizable tools, Phase 4 for streaming tools), so
	// placing it here — unconditionally, before the hooksEnabled() gate
	// below — covers every tool call exactly once and stays independent of
	// whether a HooksManager is attached or DisableHooks is set.
	a.observeToolBefore(ctx, toolCall.Name, toolCall.Parameters)

	// Emit hook event BEFORE tool execution
	a.logger.Info(ctx, "agent.hooks_check",
		observability.F("tool", toolCall.Name),
		observability.F("hooks_manager_set", a.hooksManager != nil))

	// IMPORTANT: pre-hooks are emitted BEFORE ToolCallUpdate so their
	// sequence numbers naturally sort before the tool call. UI renders
	// pre-hooks above the tool invocation diamond.
	var preHookContext string
	if a.hooksEnabled() {
		hookResults, hookErr := a.hooksManager.EmitToolBeforeExecute(ctx, toolCall.Name, toolCall.Parameters)

		// Emit hook execution updates for UI and collect AdditionalContext
		var preCtxParts []string
		// A block is enforced on the STRUCTURED signal (HookResult.Blocked), not
		// solely on a returned error. Managers are contractually allowed to
		// surface a block via the flag (see agentbridge.EmitToolBeforeExecute),
		// and some emit guidance without an error at all. Keying enforcement on
		// the flag closes the gap where a flagged-but-errorless block would let
		// the tool run anyway. A non-nil hookErr is still honored for managers
		// that only signal that way.
		var blockedByHook bool
		var blockedHookName, blockMessage string
		for _, hr := range hookResults {
			a.callIntermediateCallback(ctx, HookExecutionUpdate{
				HookName:          hr.HookName,
				ToolName:          toolCall.Name,
				ToolCallID:        toolCall.ID,
				Phase:             "before",
				Success:           hr.Success,
				Output:            hr.Output,
				Blocked:           hr.Blocked,
				Error:             hr.Error,
				Duration:          hr.Duration,
				ExitCode:          hr.ExitCode,
				MatchedPattern:    hr.MatchedPattern,
				TimeoutConfigured: hr.TimeoutConfigured,
				WorkingDir:        hr.WorkingDir,
				Sequence:          a.nextUpdateSequence(),
			})
			if hr.Blocked {
				blockedByHook = true
				if blockedHookName == "" {
					blockedHookName = hr.HookName
				}
				// Prefer the blocking hook's own message: it carries the
				// actionable steering text the model should read, not a generic
				// "blocked by hook" wrapper.
				if blockMessage == "" {
					if hr.Output != "" {
						blockMessage = hr.Output
					} else if hr.AdditionalContext != "" {
						blockMessage = hr.AdditionalContext
					}
				}
			}
			if wrapped := hooks.FormatHookContext(hr.HookName, hr.AdditionalContext); wrapped != "" {
				preCtxParts = append(preCtxParts, wrapped)
				// DEBUG: Log AdditionalContext collection
				logAgentDebug("agent_tools: collected AdditionalContext from %s, len=%d, content=%q",
					hr.HookName, len(hr.AdditionalContext), hr.AdditionalContext)
			}
		}
		preHookContext = strings.Join(preCtxParts, "\n\n")
		// DEBUG: Log final preHookContext
		logAgentDebug("agent_tools: final preHookContext len=%d, parts=%d, content=%q",
			len(preHookContext), len(preCtxParts), preHookContext)

		if hookErr != nil || blockedByHook {
			// Hook blocked the execution. Normalize the two block channels
			// (structured flag + returned error) into one decision and one
			// actionable message, and treat it as a SKIP that yields a
			// tool_result — never a fatal turn error — so the model can read the
			// steering text and correct course instead of the run aborting.
			blockErr := hookErr
			if blockErr == nil {
				name := blockedHookName
				if name == "" {
					name = "hook"
				}
				blockErr = fmt.Errorf("blocked by %s", name)
			}
			reason := blockMessage
			if reason == "" && hookErr != nil {
				reason = hookErr.Error()
			}
			if reason == "" {
				reason = blockErr.Error()
			}
			a.logger.Info(ctx, "agent.tool_blocked_by_hook",
				observability.F("tool", toolCall.Name),
				observability.F("hook", blockedHookName),
				observability.F("signaled_by_error", hookErr != nil),
				observability.F("signaled_by_flag", blockedByHook),
				observability.F("reason", reason))

			// Emit ToolCallUpdate so the UI shows the tool that was blocked,
			// with pre-hooks already in front of it.
			a.callIntermediateCallback(ctx, ToolCallUpdate{
				ID:         toolCall.ID,
				Name:       toolCall.Name,
				Parameters: snapshotToolParameters(toolCall.Parameters),
				Sequence:   a.nextUpdateSequence(),
			})

			return &toolExecutionResult{
				toolCall:   toolCall,
				errorMsg:   fmt.Sprintf("Tool '%s' blocked by hook: %s", toolCall.Name, reason),
				errorType:  "tool.blocked_by_hook",
				skipped:    true,
				skipReason: "blocked_by_hook",
			}, ""
		}
	}

	// Emit tool call update for real-time UI AFTER pre-hooks so their
	// sequence numbers sort above the tool invocation.
	a.callIntermediateCallback(ctx, ToolCallUpdate{
		ID:         toolCall.ID,
		Name:       toolCall.Name,
		Parameters: snapshotToolParameters(toolCall.Parameters),
		Sequence:   a.nextUpdateSequence(),
	})

	return nil, preHookContext
}

// postExecuteToolProcess processes a tool execution result and emits callbacks/hooks.
// Returns the processed result with any validation errors applied.
func (a *Agent) postExecuteToolProcess(ctx context.Context, toolCall conversation.ToolCall, result *tools.ToolResult, execErr error) *toolExecutionResult {
	// Handle tool execution error
	if execErr != nil {
		errorMsg := fmt.Sprintf("Error executing %s: %v", toolCall.Name, execErr)
		errorType := sdkerr.GetType(execErr)
		if errorType == "" {
			errorType = "tool.execution_error"
		}

		// Emit tool result update with error BEFORE post-hooks
		a.callIntermediateCallback(ctx, ToolResultUpdate{
			ID:       toolCall.ID,
			Output:   errorMsg,
			Error:    execErr,
			Sequence: a.nextUpdateSequence(),
		})

		// Emit hook event AFTER tool execution (even on error)
		var hookContextParts []string
		if a.hooksEnabled() {
			afterHookResults := a.hooksManager.EmitToolAfterExecute(ctx, toolCall.Name, toolCall.Parameters, result, execErr)
			for _, hr := range afterHookResults {
				a.callIntermediateCallback(ctx, HookExecutionUpdate{
					HookName:          hr.HookName,
					ToolName:          toolCall.Name,
					ToolCallID:        toolCall.ID,
					Phase:             "after",
					Success:           hr.Success,
					Output:            hr.Output,
					Blocked:           hr.Blocked,
					Error:             hr.Error,
					Duration:          hr.Duration,
					ExitCode:          hr.ExitCode,
					MatchedPattern:    hr.MatchedPattern,
					TimeoutConfigured: hr.TimeoutConfigured,
					WorkingDir:        hr.WorkingDir,
					Sequence:          a.nextUpdateSequence(),
				})
				// Collect AdditionalContext to be returned as a separate message —
				// never mutate result.Output so hook context stays its own entry.
				if wrapped := hooks.FormatHookContext(hr.HookName, hr.AdditionalContext); wrapped != "" {
					hookContextParts = append(hookContextParts, wrapped)
				}
			}
		}

		// Bench/observational capture: independent of hooksEnabled() by
		// design — this must fire even under --no-hooks or on an agent with
		// no HooksManager attached at all (sub-agents). Do not nest inside
		// the a.hooksEnabled() block above.
		a.recordBenchEffects(ctx, toolCall.Name, result, execErr)
		a.observeToolAfter(ctx, toolCall.Name, toolCall.Parameters, result, execErr)

		return &toolExecutionResult{
			toolCall:    toolCall,
			result:      result,
			err:         execErr,
			errorMsg:    errorMsg,
			errorType:   errorType,
			hookContext: strings.Join(hookContextParts, "\n\n"),
		}
	}

	if result == nil {
		return &toolExecutionResult{
			toolCall: toolCall,
			result:   &tools.ToolResult{Output: ""},
			err:      nil,
		}
	}

	// Check if result contains image content - images have different size handling
	hasImageContent := false
	for _, block := range result.Content {
		if block.Type == tools.ContentTypeImage {
			hasImageContent = true
			break
		}
	}

	// Vision routing: If result contains images and primary model lacks vision support,
	// route through the configured vision model for analysis. This allows non-vision
	// models (e.g., deepseek-chat) to effectively use tools that return images.
	if hasImageContent && a.visionRouter != nil {
		routedResult, routeErr := a.visionRouter.AnalyzeImageResult(ctx, toolCall.Name, toolCall.ID, result)
		if routeErr != nil {
			a.logger.Warn(ctx, "agent.vision_route_failed",
				observability.F("tool", toolCall.Name),
				observability.F("call_id", toolCall.ID),
				observability.F("error", routeErr.Error()))
			// Fall through with original result - don't fail the entire operation
		} else if routedResult != nil {
			// Replace result with vision-analyzed result
			result = routedResult
			// Update hasImageContent since the vision router converts images to text
			hasImageContent = false
			a.logger.Info(ctx, "agent.vision_route_success",
				observability.F("tool", toolCall.Name),
				observability.F("call_id", toolCall.ID))
		}
	}

	// Bound oversized text results while preserving useful context from both ends.
	// Image results retain their existing exemption because image payloads have
	// separate size handling.
	outputChars := len(result.Output)
	outputLines := strings.Count(result.Output, "\n") + 1
	outputTokens := outputChars / CharsPerToken

	if !hasImageContent && (outputChars > MaxToolOutputChars || outputLines > MaxToolOutputLines) {
		// Reserve space for the marker itself so the final model-visible payload
		// remains below the global caps.
		const markerReserveChars = 256
		const markerReserveLines = 4
		bounded := toolout.Bound(
			result.Output,
			MaxToolOutputChars-markerReserveChars,
			MaxToolOutputLines-markerReserveLines,
		)
		marker := fmt.Sprintf(
			"\n\n... [%d chars elided from oversized tool result; use a narrower query to show more] ...\n\n",
			bounded.ElidedChars,
		)
		result.Output = bounded.Head + marker + bounded.Tail

		a.logger.Warn(ctx, "agent.tool_output_rejected",
			observability.F("tool", toolCall.Name),
			observability.F("chars", outputChars),
			observability.F("tokens", outputTokens),
			observability.F("lines", outputLines),
			observability.F("elided_chars", bounded.ElidedChars),
			observability.F("truncated", true),
			observability.F("agent_id", a.definition.ID))

		// Retain the historical reject action for token anomaly tracing so
		// dashboards remain continuous while user-visible behavior degrades to
		// truncation rather than failure.
	}

	// Convert content blocks to interface slice for the update
	var contentBlocksInterface []any
	if len(result.Content) > 0 {
		contentBlocksInterface = make([]any, len(result.Content))
		for i, block := range result.Content {
			contentBlocksInterface[i] = block
		}
	}

	// Emit tool result update for real-time UI BEFORE post-hooks
	a.callIntermediateCallback(ctx, ToolResultUpdate{
		ID:            toolCall.ID,
		Output:        result.Output,
		Error:         nil,
		ContentBlocks: contentBlocksInterface,
		Metadata:      result.Metadata,
		Hosted:        result.Hosted,
		TaskClass:     hostedTaskClassFromResult(result),
		Sequence:      a.nextUpdateSequence(),
	})

	// Emit hook event AFTER tool execution (after result for correct UI ordering)
	var hookContextParts []string
	if a.hooksEnabled() {
		afterHookResults := a.hooksManager.EmitToolAfterExecute(ctx, toolCall.Name, toolCall.Parameters, result, nil)
		for _, hr := range afterHookResults {
			a.callIntermediateCallback(ctx, HookExecutionUpdate{
				HookName:          hr.HookName,
				ToolName:          toolCall.Name,
				ToolCallID:        toolCall.ID,
				Phase:             "after",
				Success:           hr.Success,
				Output:            hr.Output,
				Blocked:           hr.Blocked,
				Error:             hr.Error,
				Duration:          hr.Duration,
				ExitCode:          hr.ExitCode,
				MatchedPattern:    hr.MatchedPattern,
				TimeoutConfigured: hr.TimeoutConfigured,
				WorkingDir:        hr.WorkingDir,
				Sequence:          a.nextUpdateSequence(),
			})
			// Collect AdditionalContext to be returned as a separate message —
			// never mutate result.Output so hook context stays its own first-class entry.
			if wrapped := hooks.FormatHookContext(hr.HookName, hr.AdditionalContext); wrapped != "" {
				hookContextParts = append(hookContextParts, wrapped)
			}
		}
	}

	a.logger.Info(ctx, "agent.tool_executed",
		observability.F("tool", toolCall.Name),
		observability.F("agent_id", a.definition.ID))

	// Bench/observational capture: independent of hooksEnabled() by design —
	// this must fire even under --no-hooks or on an agent with no
	// HooksManager attached at all (sub-agents). Do not nest inside the
	// a.hooksEnabled() block above.
	a.recordBenchEffects(ctx, toolCall.Name, result, nil)
	a.observeToolAfter(ctx, toolCall.Name, toolCall.Parameters, result, nil)

	return &toolExecutionResult{
		toolCall:    toolCall,
		result:      result,
		hookContext: strings.Join(hookContextParts, "\n\n"),
	}
}

// convertToolResultToConversation converts a toolExecutionResult to conversation.ToolResult.
func (a *Agent) convertToolResultToConversation(execResult *toolExecutionResult) conversation.ToolResult {
	if execResult.isPolicySkip() {
		return conversation.ToolResult{
			CallID: execResult.toolCall.ID,
			Name:   execResult.toolCall.Name,
			Output: execResult.errorMsg,
		}
	}

	if execResult.err != nil || execResult.skipped {
		return conversation.ToolResult{
			CallID: execResult.toolCall.ID,
			Name:   execResult.toolCall.Name,
			Output: execResult.errorMsg,
			Error: &conversation.ToolError{
				Type:    execResult.errorType,
				Message: execResult.errorMsg,
			},
		}
	}

	if execResult.result == nil {
		return conversation.ToolResult{
			CallID: execResult.toolCall.ID,
			Name:   execResult.toolCall.Name,
			Output: "",
		}
	}

	// Convert tools.ContentBlock to conversation.ContentBlock
	var conversationContent []conversation.ContentBlock
	if len(execResult.result.Content) > 0 {
		conversationContent = make([]conversation.ContentBlock, len(execResult.result.Content))
		for i, block := range execResult.result.Content {
			conversationContent[i] = conversation.ContentBlock{
				Type:        string(block.Type),
				Text:        block.Text,
				Data:        block.Data,
				MimeType:    block.MimeType,
				URI:         block.URI,
				Name:        block.Name,
				Description: block.Description,
				Size:        block.Size,
				Annotations: block.Annotations,
			}
		}
	}

	return conversation.ToolResult{
		CallID:  execResult.toolCall.ID,
		Name:    execResult.toolCall.Name,
		Output:  execResult.result.Output,
		Content: conversationContent,
	}
}

// executeToolsWithParallelism executes tool calls with parallel execution support.
// Parallel-safe tools are executed concurrently, while streaming and exclusive tools
// are executed sequentially.
func (a *Agent) executeToolsWithParallelism(ctx context.Context, message *conversation.Message) ([]*conversation.Message, error) {
	if len(message.ToolCalls) == 0 {
		return nil, nil
	}

	// Track total tool calls executed this Execute() lifecycle.
	a.mu.Lock()
	a.toolCallsTotal += len(message.ToolCalls)
	a.mu.Unlock()

	// Create batch span for the entire tool execution
	ctx, batchSpan := a.tracer.StartSpan(ctx, "agent.tool_batch_execute")
	defer batchSpan.End()
	batchSpan.SetAttribute("tool_count", len(message.ToolCalls))

	// Inject agent capabilities and identity into context for tools to use
	if a.intermediateCallback != nil {
		ctx = WithIntermediateCallback(ctx, a.intermediateCallback)
	}
	// Inject the hooks manager into ctx for nested/streaming tools, but only
	// when hooks are active for this turn (DisableHooks opts out so downstream
	// tools also see no hook surface).
	if a.hooksEnabled() {
		ctx = WithHooksManager(ctx, a.hooksManager)
	}

	// Inject owner identity for observability and process tracking
	a.mu.RLock()
	ownerAgentID := ""
	if a.definition != nil {
		ownerAgentID = a.definition.ID
	}
	ownerConversationID := a.conversationID
	a.mu.RUnlock()
	ctx = tools.WithOwnerInfo(ctx, ownerAgentID, "", ownerConversationID)

	// Inject file tracker for tool file access recording
	ctx = a.BuildContextWithTracker(ctx)

	// Separate streaming tools from parallelizable tools
	invocations, streamingCalls := a.buildToolInvocations(ctx, message.ToolCalls)

	batchSpan.SetAttribute("parallel_tool_count", len(invocations))
	batchSpan.SetAttribute("streaming_tool_count", len(streamingCalls))

	a.logger.Info(ctx, "agent.tool_batch_starting",
		observability.F("total_tools", len(message.ToolCalls)),
		observability.F("parallel_tools", len(invocations)),
		observability.F("streaming_tools", len(streamingCalls)))

	// Results map keyed by POSITIONAL INDEX into message.ToolCalls, not by
	// tool_use ID. This is the only collision-proof scheme: models (and some
	// proxy layers) occasionally emit two tool_use blocks with the same ID
	// in a single assistant turn. If we keyed by ID, the second result would
	// overwrite the first, then aggregation would produce only one
	// tool_result for two IDs and the loop would loop forever retrying.
	// Aggregation below re-pairs positions back to call.IDs in original order.
	results := make(map[int]*toolExecutionResult, len(message.ToolCalls))
	// Pre-hook context per call POSITION (same rationale as `results`).
	preHookCtx := make(map[int]string)

	// Phase 1: Pre-execution checks for all tools (sequential - needed for hooks/UI)
	// This must be done before parallel execution to ensure proper hook ordering
	batchBlocked := false
	var blockedToolName string
	toolsToExecute := make([]*tools.ToolInvocation, 0, len(invocations))
	for _, inv := range invocations {
		// Get the original tool call by source position (collision-proof).
		// SourceIndex is always valid because buildToolInvocations sets it
		// from the message.ToolCalls iteration index.
		pos := inv.SourceIndex
		if pos < 0 || pos >= len(message.ToolCalls) {
			// Defensive: should never happen. Log and skip.
			a.logger.Error(ctx, "agent.invalid_source_index",
				observability.F("tool", inv.Name),
				observability.F("source_index", pos),
				observability.F("total_calls", len(message.ToolCalls)),
			)
			continue
		}
		toolCall := message.ToolCalls[pos]

		// Run pre-execution checks
		skipResult, phc := a.preExecuteToolChecks(ctx, toolCall)
		if phc != "" {
			preHookCtx[pos] = phc
		}
		if skipResult != nil {
			results[pos] = skipResult
			// Emit ToolResultUpdate so the UI can finalize the tool_call block —
			// otherwise the block stays "executing" forever when a hook blocks
			// or the tool is not found (parallel path).
			a.callIntermediateCallback(ctx, ToolResultUpdate{
				ID:       toolCall.ID,
				Output:   skipResult.errorMsg,
				Error:    skipResult.err,
				Sequence: a.nextUpdateSequence(),
			})
			if skipResult.skipReason == "blocked_by_hook" {
				batchBlocked = true
				blockedToolName = toolCall.Name
				break // stop running further pre-checks
			}
			continue
		}

		// Tool passed pre-checks, add to execution list
		toolsToExecute = append(toolsToExecute, inv)
	}

	// Handle batch abortion if any tool was blocked by a hook
	if batchBlocked {
		// Clear execution list so Phase 2 runs nothing
		toolsToExecute = nil

		// Cancel all uncompleted tools
		for i, tc := range message.ToolCalls {
			if _, hasResult := results[i]; !hasResult {
				// Create cancelled result
				cancelMsg := fmt.Sprintf("Tool '%s' cancelled: batch halted because '%s' was blocked by a hook", tc.Name, blockedToolName)

				results[i] = &toolExecutionResult{
					toolCall:   tc,
					skipped:    true,
					skipReason: "batch_blocked",
					errorType:  "tool.batch_blocked",
					errorMsg:   cancelMsg,
				}

				// Emit update to finalize UI
				a.callIntermediateCallback(ctx, ToolResultUpdate{
					ID:       tc.ID,
					Output:   cancelMsg,
					Sequence: a.nextUpdateSequence(),
				})
			}
		}
	}

	// Phase 2: Execute parallelizable tools using ToolCallRuntime
	if len(toolsToExecute) > 0 {
		runtime := tools.NewToolCallRuntime(a.toolReg, a.logger, a.tracer)
		batchResults, err := runtime.ExecuteBatch(ctx, toolsToExecute)
		if err != nil {
			a.logger.Error(ctx, "agent.tool_batch_execution_failed",
				observability.F("error", err.Error()))
			// Continue processing - individual results may still be valid
		}

		// Phase 3: Post-process each result (sequential - needed for hooks/UI)
		for _, batchResult := range batchResults {
			if batchResult == nil || batchResult.Invocation == nil {
				continue
			}
			// Pair via SourceIndex — guaranteed unique even when call.IDs
			// collide. The invocation pointer was preserved verbatim through
			// the runtime, so SourceIndex is the same value buildToolInvocations
			// originally stamped on it.
			pos := batchResult.Invocation.SourceIndex
			if pos < 0 || pos >= len(message.ToolCalls) {
				a.logger.Error(ctx, "agent.invalid_source_index_post",
					observability.F("tool", batchResult.Invocation.Name),
					observability.F("source_index", pos),
				)
				continue
			}
			toolCall := message.ToolCalls[pos]

			// Process the result (validation, hooks, callbacks)
			execResult := a.postExecuteToolProcess(ctx, toolCall, batchResult.Result, batchResult.Error)
			// Inject pre-hook steering context INTO the tool result so the model
			// reads guidance before the tool data — not as a separate user message.
			if phc := preHookCtx[pos]; phc != "" {
				if execResult.result != nil && execResult.result.Output != "" {
					execResult.result.Output = phc + "\n\n---\n\n" + execResult.result.Output
				} else if execResult.result != nil {
					execResult.result.Output = phc
				} else {
					// Fallback: no result object, use hookContext
					execResult.hookContext = phc
				}
			}
			results[pos] = execResult
		}
	}

	// Phase 4: Execute streaming tools sequentially (they need real-time callbacks).
	// Streaming tools weren't in `invocations`, so they don't carry SourceIndex.
	// We resolve their position by walking message.ToolCalls and matching the
	// FIRST unclaimed tc with the same ID+Name+Parameters fingerprint. With
	// duplicate IDs, "claimed" tracking ensures the second streaming call lands
	// in its own position instead of overwriting the first.
	if !batchBlocked {
		streamingClaimedPos := make(map[int]bool, len(streamingCalls))
		for _, toolCall := range streamingCalls {
			// Find first unclaimed position with matching ID + Name.
			pos := -1
			for i, tc := range message.ToolCalls {
				if streamingClaimedPos[i] {
					continue
				}
				if tc.ID == toolCall.ID && tc.Name == toolCall.Name {
					pos = i
					streamingClaimedPos[i] = true
					break
				}
			}
			if pos < 0 {
				a.logger.Error(ctx, "agent.streaming_tool_position_not_found",
					observability.F("tool", toolCall.Name),
					observability.F("id", toolCall.ID),
				)
				continue
			}

			// Run pre-execution checks
			skipResult, phc := a.preExecuteToolChecks(ctx, toolCall)
			if skipResult != nil {
				results[pos] = skipResult
				continue
			}

			// Execute streaming tool
			result, err := a.executeStreamingTool(ctx, toolCall)
			// Inject pre-hook steering context INTO the streaming tool result so
			// the model reads guidance before the tool data.
			if phc != "" && result != nil {
				if result.result != nil && result.result.Output != "" {
					result.result.Output = phc + "\n\n---\n\n" + result.result.Output
				} else if result.result != nil {
					result.result.Output = phc
				} else {
					// Fallback: no result object, use hookContext
					result.hookContext = phc
				}
			}
			results[pos] = result
			if err != nil {
				// Error already recorded in result, continue to next tool
				continue
			}
		}
	}

	// Anthropic requires a tool_result for EVERY tool_use ID in the batch. We
	// always provide one, using each tool's actual result (success or error).
	// Mixed success+error batches are valid per the Anthropic API: the tool_use_id
	// contract is about one-result-per-id, not about uniform success/failure.
	// We log batch partial-failures for diagnostic purposes but never discard a
	// successful sibling's work.
	if len(message.ToolCalls) > 1 {
		for pos, toolCall := range message.ToolCalls {
			execResult, ok := results[pos]
			if !ok || execResult == nil {
				a.logger.Warn(ctx, "agent.parallel_tool_batch_partial_fail",
					observability.F("failed_tool", toolCall.Name),
					observability.F("position", pos),
					observability.F("total_tools", len(message.ToolCalls)),
					observability.F("reason", "no result recorded; siblings kept"),
				)
				break
			}
			if execResult.err != nil || (execResult.skipped && !execResult.isPolicySkip()) {
				a.logger.Warn(ctx, "agent.parallel_tool_batch_partial_fail",
					observability.F("failed_tool", toolCall.Name),
					observability.F("position", pos),
					observability.F("total_tools", len(message.ToolCalls)),
					observability.F("reason", "one tool failed; siblings kept"),
				)
				break
			}
		}
	}

	// Aggregate results in original order. Every tool_use ID gets its actual
	// tool_result — failures surface via convertToolResultToConversation, and
	// successes are preserved regardless of sibling outcomes.
	var allToolResults []conversation.ToolResult

	for pos, toolCall := range message.ToolCalls {
		execResult, ok := results[pos]
		if !ok {
			// This shouldn't happen, but handle gracefully
			errorMsg := fmt.Sprintf("Error: No result for tool '%s' at position %d", toolCall.Name, pos)
			allToolResults = append(allToolResults, conversation.ToolResult{
				CallID: toolCall.ID,
				Name:   toolCall.Name,
				Output: errorMsg,
				Error: &conversation.ToolError{
					Type:    "tool.internal_error",
					Message: errorMsg,
				},
			})
			continue
		}

		convResult := a.convertToolResultToConversation(execResult)
		// Force the CallID to match this call's position-paired ID. With
		// duplicate IDs, two results genuinely share the same string here —
		// that's fine: the Anthropic API uses tool_use_id positional pairing
		// inside the ToolResults slice, so as long as we emit one result per
		// tool_use in original order the API is happy.
		convResult.CallID = toolCall.ID
		convResult.Name = toolCall.Name
		allToolResults = append(allToolResults, convResult)
	}

	// Build the tool-result message (Anthropic requires all tool_result blocks in one message).
	singleMessage := &conversation.Message{
		ID:          fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Role:        conversation.RoleTool,
		Content:     "", // MUST be empty — tool outputs live in ToolResults[].Output
		ToolResults: allToolResults,
		Timestamp:   time.Now(),
	}

	// Collect hook AdditionalContext from every result in original call order.
	// Any non-empty context becomes a standalone RoleUser message appended after the
	// tool-result message.  This keeps hook-injected data (e.g. task nudges) as a
	// first-class conversation history entry — persistent in the DB, visible to the
	// model on the next turn, and never buried inside a tool output string.
	var hookContextParts []string
	for pos := range message.ToolCalls {
		if execResult, ok := results[pos]; ok && execResult != nil && execResult.hookContext != "" {
			hookContextParts = append(hookContextParts, execResult.hookContext)
		}
	}
	if len(hookContextParts) == 0 {
		return []*conversation.Message{singleMessage}, nil
	}
	hookMsg := &conversation.Message{
		ID:        fmt.Sprintf("hook_%d", time.Now().UnixNano()),
		Role:      conversation.RoleUser,
		Content:   strings.Join(hookContextParts, "\n\n"),
		Timestamp: time.Now(),
		Metadata: map[string]any{
			"type":            "hook_context",
			"is_hook_context": true,
		},
	}
	return []*conversation.Message{singleMessage, hookMsg}, nil
}

// executeStreamingTool executes a single streaming tool with real-time output callbacks.
func (a *Agent) executeStreamingTool(ctx context.Context, toolCall conversation.ToolCall) (*toolExecutionResult, error) {
	ctx, span := a.tracer.StartSpan(ctx, "agent.streaming_tool_execute")
	defer span.End()
	span.SetAttribute("tool.name", toolCall.Name)
	span.SetAttribute("tool.id", toolCall.ID)
	// Inject tool call ID into context for unique identification
	ctx = tools.WithToolCallID(ctx, toolCall.ID)

	tool, err := a.toolReg.Get(toolCall.Name)
	if err != nil || tool == nil {
		if err == nil {
			err = fmt.Errorf("tool '%s' not found", toolCall.Name)
		}
		wrappedErr := sdkerr.Wrap(
			err,
			"agent.tool.not_found",
			sdkerr.WithOperation("agent.streaming_tool_execute"),
			sdkerr.WithComponent("agent.tools"),
			sdkerr.WithTraceFromContext(ctx),
		)
		return &toolExecutionResult{
			toolCall:   toolCall,
			err:        wrappedErr,
			errorMsg:   fmt.Sprintf("Error: Tool '%s' not found", toolCall.Name),
			errorType:  "tool.not_found",
			skipped:    true,
			skipReason: "tool_not_found",
		}, wrappedErr
	}

	streamingTool, ok := tool.(tools.StreamingTool)
	if !ok {
		// Not a streaming tool — execute via Executor (validation + permission included).
		exec := tools.NewExecutor(a.toolReg, nil)
		result, execErr := exec.Execute(ctx, toolCall.Name, toolCall.Parameters)
		return a.postExecuteToolProcess(ctx, toolCall, result, execErr), execErr
	}

	// Get agent context for preflight
	a.mu.RLock()
	agentID := ""
	if a.definition != nil {
		agentID = a.definition.ID
	}
	conversationID := a.conversationID
	mode := ""
	if a.requestContext != nil {
		if modeValue, ok := a.requestContext["mode"].(string); ok {
			mode = modeValue
		}
	}
	a.mu.RUnlock()

	// Run preflight checks
	if err := preflightToolExecution(ctx, a.toolReg, tool, toolCall.Name, toolCall.Parameters, agentID, conversationID, mode); err != nil {
		wrappedErr := sdkerr.Wrap(
			err,
			"agent.streaming_tool.preflight_failed",
			sdkerr.WithOperation("agent.streaming_tool_preflight"),
			sdkerr.WithComponent("agent.tools"),
			sdkerr.WithTraceFromContext(ctx),
		)
		observability.RecordError(ctx, a.logger, span, wrappedErr, "agent.streaming_tool_preflight_failed")

		errorMsg := fmt.Sprintf("Error executing %s: %v", toolCall.Name, wrappedErr)
		errorType := sdkerr.GetType(wrappedErr)
		if errorType == "" {
			errorType = "tool.execution_error"
		}

		a.callIntermediateCallback(ctx, ToolResultUpdate{
			ID:       toolCall.ID,
			Output:   errorMsg,
			Error:    wrappedErr,
			Sequence: a.nextUpdateSequence(),
		})

		// Emit after hooks even on preflight failure
		if a.hooksEnabled() {
			afterHookResults := a.hooksManager.EmitToolAfterExecute(ctx, toolCall.Name, toolCall.Parameters, nil, wrappedErr)
			for _, hr := range afterHookResults {
				a.callIntermediateCallback(ctx, HookExecutionUpdate{
					HookName:          hr.HookName,
					ToolName:          toolCall.Name,
					ToolCallID:        toolCall.ID,
					Phase:             "after",
					Success:           hr.Success,
					Output:            hr.Output,
					Blocked:           hr.Blocked,
					Error:             hr.Error,
					Duration:          hr.Duration,
					ExitCode:          hr.ExitCode,
					MatchedPattern:    hr.MatchedPattern,
					TimeoutConfigured: hr.TimeoutConfigured,
					WorkingDir:        hr.WorkingDir,
					Sequence:          a.nextUpdateSequence(),
				})
			}
		}

		// Bench/observational capture: independent of hooksEnabled() by
		// design — this must fire even under --no-hooks or on an agent with
		// no HooksManager attached at all (sub-agents). Do not nest inside
		// the a.hooksEnabled() block above. This is the preflight-failure
		// path, which never reaches postExecuteToolProcess, so it needs its
		// own capture call — result is nil because the tool never ran.
		a.recordBenchEffects(ctx, toolCall.Name, nil, wrappedErr)
		a.observeToolAfter(ctx, toolCall.Name, toolCall.Parameters, nil, wrappedErr)

		return &toolExecutionResult{
			toolCall:  toolCall,
			err:       wrappedErr,
			errorMsg:  errorMsg,
			errorType: errorType,
		}, wrappedErr
	}

	// Execute streaming tool with real-time output callback.
	//
	// Streaming tools bypass the tools-package execution sites entirely, so
	// per-tool accounting is attached here explicitly. Without this the
	// heatmap would be blind to exactly the tools most likely to be hot
	// (bash and friends are streaming).
	streamCtx, endMetrics := toolmetrics.Begin(ctx, toolCall.Name)
	streamAllocBefore := toolmetrics.ReadAlloc()
	streamStart := time.Now()

	result, execErr := streamingTool.ExecuteStreaming(streamCtx, toolCall.Parameters,
		func(chunk string, stream string) {
			a.callIntermediateCallback(ctx, ToolOutputChunk{
				ID:     toolCall.ID,
				Chunk:  chunk,
				Stream: stream,
			})
		})

	streamDur := time.Since(streamStart)
	if toolmetrics.DeepProfileEnabled() {
		if after := toolmetrics.ReadAlloc(); after > streamAllocBefore {
			toolmetrics.RecordAlloc(toolCall.Name, after-streamAllocBefore)
		}
	}
	toolmetrics.Record(toolCall.Name, streamDur, tools.ResultPayloadSize(result),
		execErr != nil || (result != nil && result.IsError))
	endMetrics()
	result = tools.SpillToolResult(ctx, toolCall.Name, result, execErr)

	if execErr != nil {
		execErr = sdkerr.Wrap(
			execErr,
			"agent.streaming_tool.execution_failed",
			sdkerr.WithOperation("agent.streaming_tool_execute"),
			sdkerr.WithComponent("agent.tools"),
			sdkerr.WithTraceFromContext(ctx),
		)
	}

	// Process the result
	return a.postExecuteToolProcess(ctx, toolCall, result, execErr), execErr
}

func hostedTaskClassFromResult(result *tools.ToolResult) hosted.TaskClass {
	if result == nil {
		return ""
	}
	if result.Hosted != nil && result.Hosted.TaskClass != "" {
		return result.Hosted.TaskClass
	}
	if result.Metadata == nil {
		return ""
	}
	rawTaskClass, ok := result.Metadata["task_class"]
	if !ok {
		return ""
	}
	taskClass, ok := rawTaskClass.(string)
	if !ok {
		return ""
	}
	return hosted.TaskClass(strings.TrimSpace(taskClass))
}
