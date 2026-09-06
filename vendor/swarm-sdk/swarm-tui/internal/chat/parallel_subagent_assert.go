package chat

import (
	"fmt"
	"strings"

	"github.com/getsentry/sentry-go"
)

// assertParallelSubAgentsNotMerged checks if parallel Task calls created separate boxes.
// If they merged into one box, it logs a detailed report to Sentry instead of crashing.
//
// IMPORTANT: This assertion must only fire when ALL Task tool results have been
// received. During parallel execution, the first Task can complete before the
// second sub-agent has started streaming — at that point only 1 sub-agent block
// exists, but 2 Task calls were issued. That is a timing window, NOT a merge bug.
//
// BACKGROUND TASK EXEMPTION: Task calls that return {"status":"async_launched"} run
// as background agents and return a tool result BEFORE any SubAgentUpdate events are
// emitted. Those calls never create synchronous sub_agent_activity blocks and must be
// excluded from the expected count.
func assertParallelSubAgentsNotMerged(msg *Message) {
	if msg == nil || msg.Role != "assistant" {
		return
	}

	// Count Task tool calls
	taskCallIDs := make(map[string]string) // callID -> task description
	for _, block := range msg.OrderedBlocks {
		if block.Type == "tool_call" && block.ToolCall != nil && block.ToolCall.Name == "Task" {
			if taskDesc, ok := block.ToolCall.Parameters["task"].(string); ok {
				taskCallIDs[block.ToolCall.ID] = taskDesc
			}
		}
	}

	// If less than 2 Task calls, nothing to check
	if len(taskCallIDs) < 2 {
		return
	}

	// Count Task tool RESULTS that have arrived so far, and detect async-launched ones.
	// Only assert when ALL results are in — otherwise we're checking mid-flight
	// and will false-positive when one Task finishes before another starts streaming.
	taskResultCount := 0
	asyncLaunchedCount := 0
	erroredCount := 0
	for _, block := range msg.OrderedBlocks {
		if block.Type == "tool_result" && block.ToolResult != nil {
			if _, isTaskCall := taskCallIDs[block.ToolResult.CallID]; isTaskCall {
				taskResultCount++
				// Task calls that were promoted to background agents return
				// async_launched immediately, before any SubAgentUpdate events.
				// They will not have a synchronous sub_agent_activity block.
				output := block.ToolResult.GetOutput()
				if strings.Contains(output, `"async_launched"`) || strings.Contains(output, `async_launched`) {
					asyncLaunchedCount++
				}
				// Task calls that errored out never started the sub-agent,
				// so they also won't have a sub_agent_activity block.
				if block.ToolResult.Error != "" {
					erroredCount++
				}
			}
		}
	}

	if taskResultCount < len(taskCallIDs) {
		// Not all Task results have arrived yet — too early to assert.
		return
	}

	// How many of the Task calls were synchronous (should have a sub_agent_activity block)?
	// Exclude both async-launched tasks AND errored tasks from expected count.
	expectedSyncBlocks := len(taskCallIDs) - asyncLaunchedCount - erroredCount

	// Count sub_agent_activity blocks
	subAgentBlocks := 0
	subAgentDetails := []string{}
	for i, block := range msg.OrderedBlocks {
		if block.Type == "sub_agent_activity" && block.SubAgentActivity != nil {
			subAgentBlocks++
			sa := block.SubAgentActivity
			detail := fmt.Sprintf("  Block[%d]: AgentID='%s' AgentName='%s' Task='%s' InnerBlocks=%d",
				i, sa.AgentID, sa.AgentName, sa.TaskInstruction, len(sa.Blocks))
			subAgentDetails = append(subAgentDetails, detail)
		}
	}

	// Also check for duplicate AgentIDs — a sign of the SDK bug where parallel
	// calls to the same agent_id/preset produce non-unique IDs.
	seenAgentIDs := make(map[string]int)
	for _, block := range msg.OrderedBlocks {
		if block.Type == "sub_agent_activity" && block.SubAgentActivity != nil {
			id := block.SubAgentActivity.AgentID
			if id != "" {
				seenAgentIDs[id]++
			}
		}
	}
	var duplicateIDs []string
	for id, count := range seenAgentIDs {
		if count > 1 {
			duplicateIDs = append(duplicateIDs, fmt.Sprintf("'%s' (x%d)", id, count))
		}
	}

	// ASSERTION: Number of synchronous sub-agent blocks MUST equal number of sync Task calls,
	// BUT only when sub-agents actually started (subAgentBlocks > 0) AND there are
	// expected sync blocks. Background/async tasks are excluded from the count.
	// If all Tasks failed before streaming began (e.g. unknown agent_id),
	// subAgentBlocks will legitimately be 0 — that is NOT a merge bug.
	if expectedSyncBlocks < 2 {
		// Fewer than 2 sync tasks — no merge to detect (or all went async).
		return
	}

	if subAgentBlocks > 0 && subAgentBlocks < expectedSyncBlocks {
		// BUG DETECTED! Log to Sentry instead of crashing
		logParallelSubAgentBugToSentry(msg, taskCallIDs, taskResultCount, asyncLaunchedCount, erroredCount, expectedSyncBlocks, subAgentBlocks, subAgentDetails, duplicateIDs)
	}
}

// logParallelSubAgentBugToSentry logs the parallel sub-agent rendering bug to Sentry
// with full diagnostic context instead of crashing the application.
func logParallelSubAgentBugToSentry(msg *Message, taskCallIDs map[string]string, taskResultCount, asyncLaunchedCount, erroredCount, expectedSyncBlocks, subAgentBlocks int, subAgentDetails, duplicateIDs []string) {
	// Build diagnostic report for Sentry context
	crashReport := strings.Builder{}
	crashReport.WriteString("\n")
	crashReport.WriteString("═══ PARALLEL SUB-AGENT RENDERING BUG DETECTED ═══\n")
	crashReport.WriteString("\n")
	crashReport.WriteString(fmt.Sprintf("Total Task calls:   %d\n", len(taskCallIDs)))
	crashReport.WriteString(fmt.Sprintf("Async-launched:     %d (excluded from check)\n", asyncLaunchedCount))
	crashReport.WriteString(fmt.Sprintf("Errored tasks:      %d (excluded from check)\n", erroredCount))
	crashReport.WriteString(fmt.Sprintf("Expected sync boxes: %d\n", expectedSyncBlocks))
	crashReport.WriteString(fmt.Sprintf("Actual boxes found:  %d\n", subAgentBlocks))
	crashReport.WriteString(fmt.Sprintf("Task results received: %d/%d\n", taskResultCount, len(taskCallIDs)))
	crashReport.WriteString("\n")

	if len(duplicateIDs) > 0 {
		crashReport.WriteString("═══ DUPLICATE AGENT IDs DETECTED ═══\n")
		crashReport.WriteString("  " + strings.Join(duplicateIDs, ", ") + "\n")
		crashReport.WriteString("  This indicates the SDK is not generating unique IDs for parallel calls.\n")
		crashReport.WriteString("\n")
	}

	crashReport.WriteString("═══ TASK TOOL CALLS ═══\n")
	for callID, taskDesc := range taskCallIDs {
		crashReport.WriteString(fmt.Sprintf("  CallID: %s\n", callID))
		crashReport.WriteString(fmt.Sprintf("    Task: %s\n", taskDesc))
	}
	crashReport.WriteString("\n")

	crashReport.WriteString("═══ SUB-AGENT BLOCKS FOUND ═══\n")
	if len(subAgentDetails) == 0 {
		crashReport.WriteString("  (none!)\n")
	} else {
		for _, detail := range subAgentDetails {
			crashReport.WriteString(detail + "\n")
		}
	}
	crashReport.WriteString("\n")

	crashReport.WriteString("═══ FULL MESSAGE STRUCTURE ═══\n")
	for i, block := range msg.OrderedBlocks {
		crashReport.WriteString(fmt.Sprintf("[%d] Type: %s", i, block.Type))
		switch block.Type {
		case "tool_call":
			if tc := block.ToolCall; tc != nil {
				crashReport.WriteString(fmt.Sprintf(" | Tool: %s | ID: %s", tc.Name, tc.ID))
			}
		case "sub_agent_activity":
			if sa := block.SubAgentActivity; sa != nil {
				crashReport.WriteString(fmt.Sprintf(" | AgentID: '%s' | AgentName: '%s'", sa.AgentID, sa.AgentName))
			}
		case "tool_result":
			if tr := block.ToolResult; tr != nil {
				crashReport.WriteString(fmt.Sprintf(" | CallID: %s | ToolName: %s", tr.CallID, tr.ToolName))
			}
		}
		crashReport.WriteString("\n")
	}

	// Build task call details for context
	taskCallsData := make(map[string]any)
	for callID, taskDesc := range taskCallIDs {
		taskCallsData[callID] = taskDesc
	}

	// Build message structure for context
	messageStructure := make([]map[string]any, len(msg.OrderedBlocks))
	for i, block := range msg.OrderedBlocks {
		entry := map[string]any{
			"index": i,
			"type":  block.Type,
		}
		switch block.Type {
		case "tool_call":
			if tc := block.ToolCall; tc != nil {
				entry["tool"] = tc.Name
				entry["id"] = tc.ID
			}
		case "sub_agent_activity":
			if sa := block.SubAgentActivity; sa != nil {
				entry["agent_id"] = sa.AgentID
				entry["agent_name"] = sa.AgentName
			}
		case "tool_result":
			if tr := block.ToolResult; tr != nil {
				entry["call_id"] = tr.CallID
				entry["tool_name"] = tr.ToolName
			}
		}
		messageStructure[i] = entry
	}

	// Log to console as well (but don't crash)
	fmt.Printf("\n%s\n", crashReport.String())

	// Capture to Sentry with full context
	hub := sentry.CurrentHub()
	event := sentry.NewEvent()
	event.Level = sentry.LevelWarning
	event.Message = "Parallel sub-agent rendering bug detected: sub-agent blocks merged"
	event.Fingerprint = []string{"parallel-subagent-merge-bug", fmt.Sprintf("expected-%d", expectedSyncBlocks), fmt.Sprintf("actual-%d", subAgentBlocks)}

	event.Exception = []sentry.Exception{{
		Type:   "ParallelSubAgentMergeBug",
		Value:  fmt.Sprintf("Expected %d sync sub-agent blocks but found %d", expectedSyncBlocks, subAgentBlocks),
		Module: "internal/chat",
	}}

	hub.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetTag("bug_type", "parallel_subagent_merge")
		scope.SetTag("expected_sync_blocks", fmt.Sprintf("%d", expectedSyncBlocks))
		scope.SetTag("actual_sync_blocks", fmt.Sprintf("%d", subAgentBlocks))
		scope.SetTag("total_task_calls", fmt.Sprintf("%d", len(taskCallIDs)))
		scope.SetTag("async_launched_count", fmt.Sprintf("%d", asyncLaunchedCount))
		scope.SetTag("errored_count", fmt.Sprintf("%d", erroredCount))

		scope.SetContext("parallel_subagent_bug", map[string]any{
			"total_task_calls":     len(taskCallIDs),
			"async_launched_count": asyncLaunchedCount,
			"errored_count":        erroredCount,
			"expected_sync_blocks": expectedSyncBlocks,
			"actual_sync_blocks":   subAgentBlocks,
			"task_result_count":    taskResultCount,
			"duplicate_agent_ids":  duplicateIDs,
			"sub_agent_details":    subAgentDetails,
			"task_calls":           taskCallsData,
			"message_structure":    messageStructure,
			"full_report":          crashReport.String(),
		})

		if len(duplicateIDs) > 0 {
			scope.SetTag("has_duplicate_agent_ids", "true")
		}
	})

	eventID := hub.CaptureEvent(event)
	if eventID != nil {
		fmt.Printf("[Sentry] Parallel sub-agent bug logged with event ID: %s\n", *eventID)
	}
}
