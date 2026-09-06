package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// DelegateOutputParams are the typed parameters for the DelegateOutput tool.
type DelegateOutputParams struct {
	AgentID    string `json:"agent_id"              description:"The delegate agent_id returned by Delegate()" required:"true"`
	Action     string `json:"action"                description:"Action: 'status' (current state + pending questions), 'poll' (wait briefly for a question or completion), 'answer' (send answer to delegate), 'result' (get final output), 'cancel' (stop the delegate)" required:"true"`
	Answer     string `json:"answer,omitempty"      description:"Your answer to the delegate's question. Used with action='answer'."`
	QuestionID string `json:"question_id,omitempty" description:"The question_id from the pending question. Used with action='answer' for precision (optional — omit to answer the current pending question)."`
	Offset     int    `json:"offset,omitempty"      description:"Byte offset for incremental result reads. Use new_offset from a previous result call."`
}

// DelegateOutputTool is the parent agent's control interface for active delegates.
type DelegateOutputTool struct {
	tools.BaseTool
	registry  *DelegateRegistry
	bgManager BackgroundAgentManager
	logger    observability.Logger
	tracer    observability.Tracer
}

// DelegateOutputConfig configures the DelegateOutput tool.
type DelegateOutputConfig struct {
	Registry  *DelegateRegistry
	BGManager BackgroundAgentManager
	Logger    observability.Logger
	Tracer    observability.Tracer
}

// NewDelegateOutputTool creates a new DelegateOutput tool.
func NewDelegateOutputTool(cfg DelegateOutputConfig) (tools.Tool, error) {
	if cfg.Registry == nil {
		return nil, sdkerr.Permanent("delegate_output.missing_registry", "delegate registry is required")
	}
	if cfg.BGManager == nil {
		return nil, sdkerr.Permanent("delegate_output.missing_bgmanager", "background manager is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = noop.NewLogger()
	}
	if cfg.Tracer == nil {
		cfg.Tracer = noop.NewTracer()
	}
	t := &DelegateOutputTool{
		registry:  cfg.Registry,
		bgManager: cfg.BGManager,
		logger:    cfg.Logger,
		tracer:    cfg.Tracer,
	}
	return tools.Typed[DelegateOutputParams](t), nil
}

func (t *DelegateOutputTool) Name() string { return "DelegateOutput" }

func (t *DelegateOutputTool) Description() string {
	return `Interact with an active delegate agent spawned by Delegate().

Actions:
  "status"  — Check current state, pending questions, and task progress.
  "poll"    — Wait up to 10s for a question from the delegate or completion.
              Use this to efficiently wait for questions without busy-looping.
  "answer"  — Send your answer to a pending question. The delegate resumes immediately.
  "result"  — Read the delegate's final output. Use offset for incremental reads.
  "cancel"  — Cancel a running delegate.

Typical polling loop:
  while true:
    out = DelegateOutput(agent_id, "poll")
    if out has question → DelegateOutput(agent_id, "answer", "your answer")
    if out.status == "done" → DelegateOutput(agent_id, "result") and break

Pass offset from a previous 'result' call to read only new content.`
}

func (t *DelegateOutputTool) Parameters() any                        { return tools.SchemaFor[DelegateOutputParams]() }
func (t *DelegateOutputTool) IsIdempotent() bool                     { return false }
func (t *DelegateOutputTool) RequiresPermission() []tools.Permission { return nil }
func (t *DelegateOutputTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// Run dispatches to the appropriate action handler.
func (t *DelegateOutputTool) Run(ctx context.Context, p DelegateOutputParams) (*tools.ToolResult, error) {
	if p.AgentID == "" {
		return nil, sdkerr.Permanent("delegate_output.missing_agent_id", "agent_id is required")
	}

	entry, ok := t.registry.Get(p.AgentID)
	if !ok {
		// Fall back to bgManager for the background agent (for status/result/cancel)
		bg, err := t.bgManager.Get(p.AgentID)
		if err != nil {
			return &tools.ToolResult{
				Output:  fmt.Sprintf("Delegate not found: %s\nIt may have expired or the agent_id is incorrect.", p.AgentID),
				IsError: true,
			}, nil
		}
		// Limited actions without registry entry (no question channel)
		return t.handleBGOnly(ctx, p, bg)
	}

	action := strings.ToLower(strings.TrimSpace(p.Action))
	switch action {
	case "status":
		return t.handleStatus(ctx, p, entry)
	case "poll":
		return t.handlePoll(ctx, p, entry)
	case "answer":
		return t.handleAnswer(ctx, p, entry)
	case "result":
		return t.handleResult(ctx, p, entry)
	case "cancel":
		return t.handleCancel(ctx, p, entry)
	default:
		return nil, sdkerr.Permanent("delegate_output.unknown_action",
			fmt.Sprintf("unknown action %q: must be 'status', 'poll', 'answer', 'result', or 'cancel'", p.Action))
	}
}

// handleStatus returns the delegate's current state and any pending question.
func (t *DelegateOutputTool) handleStatus(_ context.Context, p DelegateOutputParams, entry *DelegateEntry) (*tools.ToolResult, error) {
	bg := entry.bg
	status := bg.Status()
	elapsed := time.Since(entry.spawnedAt).Round(time.Second)

	var sb strings.Builder
	fmt.Fprintf(&sb, "agent_id:  %s\n", p.AgentID)
	fmt.Fprintf(&sb, "status:    %s\n", status)
	fmt.Fprintf(&sb, "elapsed:   %s\n", elapsed)
	fmt.Fprintf(&sb, "task:      %s\n", entry.task)

	if q := entry.qch.PendingQuestion(); q != nil {
		fmt.Fprintf(&sb, "\n─── PENDING QUESTION (delegate is waiting) ───\n")
		fmt.Fprintf(&sb, "question_id: %s\n", q.ID)
		fmt.Fprintf(&sb, "question:    %s\n", q.Question)
		if q.Context != "" {
			fmt.Fprintf(&sb, "context:     %s\n", q.Context)
		}
		fmt.Fprintf(&sb, "asked_at:    %s ago\n", time.Since(q.AskedAt).Round(time.Second))
		fmt.Fprintf(&sb, "\nAnswer with: DelegateOutput(agent_id=\"%s\", action=\"answer\", answer=\"...\", question_id=\"%s\")\n",
			p.AgentID, q.ID)
	} else {
		fmt.Fprintf(&sb, "\n(no pending questions)\n")
	}

	return &tools.ToolResult{Output: sb.String()}, nil
}

// handlePoll waits up to 10 seconds for either a question or delegate completion.
func (t *DelegateOutputTool) handlePoll(ctx context.Context, p DelegateOutputParams, entry *DelegateEntry) (*tools.ToolResult, error) {
	pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	doneChan := entry.bg.Done()

	for {
		// Check for pending question first
		if q := entry.qch.PendingQuestion(); q != nil {
			return t.formatQuestion(p.AgentID, q), nil
		}

		// Check for completion
		select {
		case <-doneChan:
			return t.formatCompletion(entry), nil
		default:
		}

		// Wait for next tick or context
		select {
		case <-pollCtx.Done():
			// Timeout — check one more time before returning "no activity"
			if q := entry.qch.PendingQuestion(); q != nil {
				return t.formatQuestion(p.AgentID, q), nil
			}
			status := entry.bg.Status()
			return &tools.ToolResult{
				Output: fmt.Sprintf("agent_id: %s\nstatus:   %s\nresult:   no questions within 10s — delegate is working\n\nCall poll again or check status later.",
					p.AgentID, status),
			}, nil
		case <-ticker.C:
			// Continue polling
		case <-doneChan:
			return t.formatCompletion(entry), nil
		}
	}
}

// handleAnswer sends the parent's answer to the delegate's pending question.
func (t *DelegateOutputTool) handleAnswer(_ context.Context, p DelegateOutputParams, entry *DelegateEntry) (*tools.ToolResult, error) {
	if p.Answer == "" {
		return &tools.ToolResult{
			Output:  "Error: answer cannot be empty. Provide your answer in the 'answer' parameter.",
			IsError: true,
		}, nil
	}

	var delivered bool
	if p.QuestionID != "" {
		delivered = entry.qch.Answer(p.QuestionID, p.Answer)
	} else {
		delivered = entry.qch.AnswerAny(p.Answer)
	}

	if !delivered {
		pending := entry.qch.PendingQuestion()
		if pending == nil {
			return &tools.ToolResult{
				Output: fmt.Sprintf("No pending question for delegate %s.\nThe delegate may have already received an answer or completed.", p.AgentID),
			}, nil
		}
		// Question exists but wrong ID — helpful mismatch message
		return &tools.ToolResult{
			Output: fmt.Sprintf("Question ID mismatch. Pending question_id is %q.\nCall again with question_id=%q or omit question_id to answer any pending question.",
				pending.ID, pending.ID),
		}, nil
	}

	return &tools.ToolResult{
		Output: fmt.Sprintf("Answer delivered to delegate %s.\nThe delegate has been unblocked and will continue working.\n\nPoll for next question: DelegateOutput(agent_id=\"%s\", action=\"poll\")",
			p.AgentID, p.AgentID),
	}, nil
}

// handleResult reads the delegate's output file with incremental offset support.
func (t *DelegateOutputTool) handleResult(_ context.Context, p DelegateOutputParams, entry *DelegateEntry) (*tools.ToolResult, error) {
	outputFile := entry.bg.OutputFilePath()
	if outputFile == "" {
		return &tools.ToolResult{Output: "No output file available yet."}, nil
	}

	data, err := os.ReadFile(outputFile)
	if err != nil {
		if os.IsNotExist(err) {
			return &tools.ToolResult{Output: "Output file not yet available — delegate may still be starting."}, nil
		}
		return &tools.ToolResult{
			Output:  fmt.Sprintf("Failed to read output: %v", err),
			IsError: true,
		}, nil
	}

	totalBytes := len(data)
	offset := min(max(p.Offset, 0), totalBytes)

	chunk := data[offset:]
	const maxChunk = 8 * 1024 * 1024 // 8 MB
	if len(chunk) > maxChunk {
		chunk = chunk[:maxChunk]
	}
	newOffset := offset + len(chunk)

	// Parse NDJSON lines for readable output
	readable := parseOutputChunk(chunk)

	status := entry.bg.Status()
	var sb strings.Builder
	fmt.Fprintf(&sb, "agent_id:    %s\n", p.AgentID)
	fmt.Fprintf(&sb, "status:      %s\n", status)
	fmt.Fprintf(&sb, "output_file: %s\n", outputFile)
	fmt.Fprintf(&sb, "offset:      %d\n", offset)
	fmt.Fprintf(&sb, "new_offset:  %d\n", newOffset)
	fmt.Fprintf(&sb, "total_bytes: %d\n", totalBytes)
	fmt.Fprintf(&sb, "───────────────────────────────────────────────────────────────\n")
	sb.Write([]byte(readable))

	return &tools.ToolResult{Output: sb.String()}, nil
}

// handleCancel cancels the delegate agent.
func (t *DelegateOutputTool) handleCancel(ctx context.Context, p DelegateOutputParams, entry *DelegateEntry) (*tools.ToolResult, error) {
	entry.bg.Cancel()
	t.registry.Remove(p.AgentID)
	_ = t.bgManager.Cancel(p.AgentID)

	t.logger.Info(ctx, "delegate.cancelled", observability.F("agent_id", p.AgentID))

	return &tools.ToolResult{
		Output: fmt.Sprintf("Delegate %s cancelled.\nIf it had a pending question, it will time out.", p.AgentID),
	}, nil
}

// handleBGOnly handles status/result/cancel for agents not in the delegate registry.
func (t *DelegateOutputTool) handleBGOnly(_ context.Context, p DelegateOutputParams, bg *agent.BackgroundAgent) (*tools.ToolResult, error) {
	action := strings.ToLower(strings.TrimSpace(p.Action))
	switch action {
	case "status":
		return &tools.ToolResult{
			Output: fmt.Sprintf("agent_id: %s\nstatus:   %s\n(note: no question channel — may be a completed or restarted delegate)", p.AgentID, bg.Status()),
		}, nil
	case "cancel":
		bg.Cancel()
		return &tools.ToolResult{Output: fmt.Sprintf("Delegate %s cancelled.", p.AgentID)}, nil
	default:
		return &tools.ToolResult{
			Output:  fmt.Sprintf("Cannot perform action %q on delegate %s (session registry entry not found).", p.Action, p.AgentID),
			IsError: true,
		}, nil
	}
}

// formatQuestion formats a pending question for the parent.
func (t *DelegateOutputTool) formatQuestion(agentID string, q *DelegateQuestion) *tools.ToolResult {
	var sb strings.Builder
	fmt.Fprintf(&sb, "─── DELEGATE QUESTION ───\n")
	fmt.Fprintf(&sb, "agent_id:    %s\n", agentID)
	fmt.Fprintf(&sb, "question_id: %s\n", q.ID)
	fmt.Fprintf(&sb, "question:    %s\n", q.Question)
	if q.Context != "" {
		fmt.Fprintf(&sb, "context:     %s\n", q.Context)
	}
	fmt.Fprintf(&sb, "asked_at:    %s ago\n", time.Since(q.AskedAt).Round(time.Second))
	fmt.Fprintf(&sb, "\nThe delegate is BLOCKED waiting for your answer.\n")
	fmt.Fprintf(&sb, "Answer: DelegateOutput(agent_id=%q, action=\"answer\", answer=\"...\", question_id=%q)\n",
		agentID, q.ID)
	return &tools.ToolResult{Output: sb.String()}
}

// formatCompletion formats the delegate's terminal state.
func (t *DelegateOutputTool) formatCompletion(entry *DelegateEntry) *tools.ToolResult {
	bg := entry.bg
	status := bg.Status()
	elapsed := time.Since(entry.spawnedAt).Round(time.Second)

	var sb strings.Builder
	fmt.Fprintf(&sb, "agent_id: %s\n", bg.AgentID())
	fmt.Fprintf(&sb, "status:   %s\n", status)
	fmt.Fprintf(&sb, "elapsed:  %s\n", elapsed)

	if status == agent.StatusCompleted {
		result := bg.Result()
		if result != nil && result.Result != "" {
			fmt.Fprintf(&sb, "\n─── RESULT ───\n%s\n", result.Result)
		}
	} else if status == agent.StatusFailed {
		result := bg.Result()
		if result != nil && result.Error != nil {
			fmt.Fprintf(&sb, "\nerror: %v\n", result.Error)
		}
	}
	fmt.Fprintf(&sb, "\nGet full output: DelegateOutput(agent_id=%q, action=\"result\")\n", bg.AgentID())
	return &tools.ToolResult{Output: sb.String()}
}

// parseOutputChunk converts NDJSON output lines into readable text.
func parseOutputChunk(chunk []byte) string {
	lines := strings.Split(strings.TrimSpace(string(chunk)), "\n")
	var parts []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Try to parse as JSON event
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			parts = append(parts, line)
			continue
		}
		if t, ok := ev["type"].(string); ok {
			switch t {
			case "chunk":
				if text, ok := ev["text"].(string); ok {
					parts = append(parts, text)
				}
			case "final":
				if result, ok := ev["result"].(string); ok && result != "" {
					parts = append(parts, result)
				}
				if errStr, ok := ev["error"].(string); ok && errStr != "" {
					parts = append(parts, fmt.Sprintf("[error] %s", errStr))
				}
			}
		}
	}
	return strings.Join(parts, "")
}
