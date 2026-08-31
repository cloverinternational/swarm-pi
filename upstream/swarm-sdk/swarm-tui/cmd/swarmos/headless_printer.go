package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/headless/ipc"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

// headlessPrinter manages structured JSONL output for -p mode, mirroring the
// event pipeline in headless/ipc/server_print.go. It buffers content/thinking/
// tool_use blocks per turn and flushes them as a single assistant event on
// message completion — exactly matching Claude's stream-json event ordering.
type headlessPrinter struct {
	pw                *ipc.PrintWriter
	outputFmt         string
	sessionID         string
	verbose           bool
	showThinking      bool
	includeHookEvents bool
	debugToStderr     bool

	mu              sync.Mutex
	printInitOnce   sync.Once
	printResultOnce sync.Once
	printDoneCh     chan struct{}

	// Buffered content blocks for the current assistant turn.
	printTurnBlocks []ipc.PrintContentBlock
	// Most recent token counts (arrives before message complete).
	printLastTokens agent.TokenCountUpdate
	// Provider message ID from last token count event.
	printLastMsgID string
	// Model name for assistant events.
	modelName string
	// Accumulated result text for text/json output formats.
	resultText string
	// Turn counters across the initial and any required-output repair execution.
	numTurns             int
	currentExecutionTurn int
	// Total number of tool_use blocks emitted across the whole run, plus a
	// per-tool-name breakdown. Populated into the final result event so the
	// analysis pipeline can read tool usage directly without walking the stream.
	toolCallCount   int
	toolCallsByName map[string]int
	// exhaustedErr captures a terminal failure observed via an ExhaustedUpdate
	// when ExecuteMessage itself returns nil. Used as a fallback so the final
	// result event always serializes as a structured error rather than an
	// empty success.
	exhaustedErr error
	// Start time for duration_ms in result event.
	startTime time.Time
	// Colored, timestamped log writer for stderr.
	log *logWriter
}

func newHeadlessPrinter(outputFmt, model string, verbose, showThinking, includeHookEvents, debugToStderr bool, w io.Writer, logW *logWriter) *headlessPrinter {
	// sessionID is sourced from conversation.ProcessSessionID() rather than a
	// fresh UUID so that the session_id emitted on every stream-json event is the
	// SAME value stamped into metadata.custom.session_id on the conversation this
	// run creates. Previously the two were unrelated UUIDs, which is why the
	// event stream and the conversation store could not be joined (PLAN.md gap
	// G1). Same shape as before (a v4 UUID), minted once, process-wide.
	return &headlessPrinter{
		pw:                ipc.NewPrintWriter(w),
		outputFmt:         outputFmt,
		sessionID:         conversation.ProcessSessionID(),
		verbose:           verbose,
		showThinking:      showThinking,
		includeHookEvents: includeHookEvents,
		debugToStderr:     debugToStderr,
		printDoneCh:       make(chan struct{}),
		modelName:         model,
		startTime:         time.Now(),
		log:               logW,
		toolCallsByName:   make(map[string]int),
	}
}

// stdoutGuard wraps an io.Writer and diverts any write that doesn't look like
// valid JSON (i.e. doesn't start with '{') to stderr. This prevents stray
// fmt.Print / library banners from corrupting the NDJSON stream.
type stdoutGuard struct {
	real     io.Writer
	fallback io.Writer
}

func newStdoutGuard(real, fallback io.Writer) *stdoutGuard {
	return &stdoutGuard{real: real, fallback: fallback}
}

func (g *stdoutGuard) Write(p []byte) (int, error) {
	// If the write starts with '{', it's likely JSON — send to real stdout.
	if len(p) > 0 && p[0] == '{' {
		return g.real.Write(p)
	}
	// Otherwise divert to stderr so the NDJSON stream stays clean.
	return g.fallback.Write(p)
}

// isStreamJSON returns true when all event types should be emitted live.
func (h *headlessPrinter) isStreamJSON() bool {
	return h.outputFmt == "stream-json"
}

// emitInit emits the system/init event (only in stream-json mode).
func (h *headlessPrinter) emitInit(tools []string) {
	h.printInitOnce.Do(func() {
		if !h.isStreamJSON() {
			return
		}
		h.pw.WriteInit(ipc.PrintSystemInit{
			Type:           "system",
			Subtype:        "init",
			CWD:            func() string { cwd, _ := os.Getwd(); return cwd }(),
			SessionID:      h.sessionID,
			Tools:          tools,
			MCPServers:     []ipc.PrintMCPServerStatus{},
			Model:          h.modelName,
			PermissionMode: "default",
			SlashCommands:  []string{},
			APIKeySource:   "none",
			SwarmVersion:   version.Version,
			OutputStyle:    "default",
			Agents:         []string{},
			Skills:         []string{},
			Plugins:        []ipc.PrintPlugin{},
			UUID:           ipc.NewEventUUID(),
			FastModeState:  "off",
		})
	})
}

// flushTurn emits all buffered content blocks as a single PrintAssistantMessage.
func (h *headlessPrinter) flushTurn() {
	if len(h.printTurnBlocks) == 0 {
		return
	}
	if !h.isStreamJSON() {
		h.printTurnBlocks = nil
		return
	}

	tok := h.printLastTokens
	// Match Anthropic stream-json schema: input_tokens is the UNCACHED delta
	// when cache reporting is available; cache_read_input_tokens carries the
	// portion served from cache (OpenAI prompt_tokens_details.cached_tokens or
	// Anthropic cache_read_input_tokens). Falls back to the full InputTokens
	// when no cache breakdown was reported (providers like Ollama, MiniMax).
	inputForPrint := tok.InputTokens
	if tok.CacheReadTokens > 0 || tok.CacheCreationTokens > 0 {
		inputForPrint = tok.UncachedInputTokens
	}
	usage := ipc.PrintMessageUsage{
		InputTokens:              inputForPrint,
		CacheCreationInputTokens: tok.CacheCreationTokens,
		CacheReadInputTokens:     tok.CacheReadTokens,
		CacheCreation:            ipc.PrintCacheCreation{Tokens: tok.CacheCreationTokens},
		OutputTokens:             tok.OutputTokens,
		ServiceTier:              "standard",
		InferenceGeo:             "",
	}

	// Snapshot and reset the buffer atomically.
	blocks := h.printTurnBlocks
	h.printTurnBlocks = nil

	h.pw.WriteAssistant(ipc.PrintAssistantMessage{
		Type: "assistant",
		Message: ipc.PrintMessage{
			Model:        h.modelName,
			ID:           h.printLastMsgID,
			Type:         "message",
			Role:         "assistant",
			Content:      blocks,
			StopReason:   nil,
			StopSequence: nil,
			Usage:        usage,
			ContextMgmt:  nil,
		},
		ParentToolUseID: nil,
		SessionID:       h.sessionID,
		UUID:            ipc.NewEventUUID(),
	})
}

// emitResult emits the final PrintResultEvent and closes printDoneCh.
func (h *headlessPrinter) emitResult(resultText string, execErr error) {
	h.printResultOnce.Do(func() {
		// Fall back to a terminal error observed via ExhaustedUpdate if the
		// execution path itself returned nil — guarantees a failed run never
		// serializes as an empty success.
		if execErr == nil && h.exhaustedErr != nil {
			execErr = h.exhaustedErr
		}
		subtype := "success"
		isError := false
		errMsg := ""
		if execErr != nil {
			subtype = "error_during_execution"
			isError = true
			errMsg = execErr.Error()
		}

		finalText := resultText
		if isError {
			finalText = errMsg
		}

		evt := ipc.PrintResultEvent{
			Type:                "result",
			Subtype:             subtype,
			IsError:             isError,
			DurationMs:          time.Since(h.startTime).Milliseconds(),
			DurationApiMs:       0,
			NumTurns:            h.numTurns,
			Result:              finalText,
			StopReason:          nil,
			SessionID:           h.sessionID,
			TotalCostUSD:        0,
			WallDeadlineSeconds: *maxWallSecondsFlag,
			Usage: func() ipc.PrintResultUsage {
				tok := h.printLastTokens
				// Match Anthropic stream-json schema; see emitAssistant comment.
				inputForPrint := tok.InputTokens
				if tok.CacheReadTokens > 0 || tok.CacheCreationTokens > 0 {
					inputForPrint = tok.UncachedInputTokens
				}
				return ipc.PrintResultUsage{
					InputTokens:              inputForPrint,
					CacheCreationInputTokens: tok.CacheCreationTokens,
					CacheReadInputTokens:     tok.CacheReadTokens,
					OutputTokens:             tok.OutputTokens,
					ServerToolUse:            ipc.PrintServerToolUse{},
					ServiceTier:              "standard",
					CacheCreation:            ipc.PrintCacheCreation{Tokens: tok.CacheCreationTokens},
					InferenceGeo:             "",
					Iterations:               []interface{}{},
					Speed:                    "standard",
				}
			}(),
			ModelUsage:        map[string]ipc.PrintModelUsageEntry{},
			PermissionDenials: []interface{}{},
			FastModeState:     "off",
			UUID:              ipc.NewEventUUID(),
			// Tool-call accounting, always populated (0 / {} when no tools ran)
			// so the analysis pipeline reads it straight from the result event.
			ToolCallCount: h.toolCallCount,
			ToolCallsByName: func() map[string]int {
				if h.toolCallsByName == nil {
					return map[string]int{}
				}
				return h.toolCallsByName
			}(),
		}

		switch h.outputFmt {
		case "text":
			h.pw.WriteText(finalText)
		default: // "stream-json" or "json"
			h.pw.WriteResult(evt)
		}

		close(h.printDoneCh)
	})
}

// handleUpdate routes an agent.IntermediateUpdate through the headlessPrinter.
// For stream-json: buffers content blocks and emits structured events.
// For text: prints content directly to stdout, metadata to stderr.
// For json: accumulates result text, emits only final result event.
func (h *headlessPrinter) handleUpdate(update agent.IntermediateUpdate) {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch u := update.(type) {
	case agent.ContentUpdate:
		// Buffer for stream-json; print for text mode.
		if h.isStreamJSON() {
			// Merge adjacent text blocks to reduce noise.
			if n := len(h.printTurnBlocks); n > 0 && h.printTurnBlocks[n-1].Type == "text" {
				h.printTurnBlocks[n-1].Text += u.Content
			} else {
				h.printTurnBlocks = append(h.printTurnBlocks, ipc.PrintContentBlock{
					Type: "text",
					Text: u.Content,
				})
			}
		}
		// Always accumulate text for result / text output.
		h.resultText += u.Content
		if h.outputFmt == "text" {
			fmt.Print(u.Content)
		}

	case agent.ThinkingUpdate:
		if h.isStreamJSON() && h.showThinking {
			// Merge adjacent thinking blocks.
			if n := len(h.printTurnBlocks); n > 0 && h.printTurnBlocks[n-1].Type == "thinking" {
				h.printTurnBlocks[n-1].Thinking += u.Content
			} else {
				h.printTurnBlocks = append(h.printTurnBlocks, ipc.PrintContentBlock{
					Type:     "thinking",
					Thinking: u.Content,
				})
			}
		}
		if h.showThinking {
			h.log.Debug(catDebug, "%s", u.Content)
		}

	case agent.ToolCallUpdate:
		// Account every tool_use block (independent of output format) so the
		// final result event can report tool_call_count / tool_calls_by_name.
		h.toolCallCount++
		if h.toolCallsByName == nil {
			h.toolCallsByName = make(map[string]int)
		}
		h.toolCallsByName[u.Name]++
		if h.isStreamJSON() {
			h.printTurnBlocks = append(h.printTurnBlocks, ipc.PrintContentBlock{
				Type:   "tool_use",
				ID:     u.ID,
				Name:   u.Name,
				Input:  u.Parameters,
				Caller: &ipc.PrintToolCaller{Type: "direct"},
			})
		}
		h.log.Info(catTool, "%s", u.Name)
		if h.debugToStderr {
			h.log.Engine("tool call: %s (id=%s)", u.Name, u.ID)
		}

	case agent.ToolResultUpdate:
		// Flush buffered assistant blocks BEFORE emitting tool result
		// (matches Claude's event ordering: assistant → user → assistant).
		h.flushTurn()

		if h.isStreamJSON() {
			content := u.Output
			isError := u.Error != nil
			if isError && content == "" {
				content = u.Error.Error()
			}
			h.pw.WriteUser(ipc.PrintUserMessage{
				Type: "user",
				Message: ipc.PrintUserContent{
					Role: "user",
					Content: []ipc.PrintToolResultContent{
						{
							Type:      "tool_result",
							ToolUseID: u.ID,
							Content:   content,
							IsError:   isError,
						},
					},
				},
				ParentToolUseID: nil,
				SessionID:       h.sessionID,
				UUID:            ipc.NewEventUUID(),
				ToolUseResult:   toolUseResultFromMetadata(u.Output, u.Metadata),
			})
		}
		if u.Error != nil {
			h.log.Error(catError, "%v", u.Error)
		} else if u.Output != "" {
			h.log.Info(catTool, "\n%s", u.Output)
		}

	case agent.AssistantMessageUpdate:
		// Finalize the turn — flush all buffered blocks.
		if u.Turn <= h.currentExecutionTurn {
			h.numTurns += u.Turn
		} else {
			h.numTurns += u.Turn - h.currentExecutionTurn
		}
		h.currentExecutionTurn = u.Turn
		h.flushTurn()
		// Engine tracing: turn boundary with finish reason.
		if h.debugToStderr {
			h.log.Engine("turn %d complete: finish=%s in=%d out=%d",
				u.Turn, u.FinishReason, u.InputTokens, u.OutputTokens)
		}

	case agent.TokenCountUpdate:
		if u.InputTokens > 0 || u.OutputTokens > 0 {
			h.printLastTokens = u
		}
		// Engine tracing: context window pressure.
		if h.debugToStderr && u.ContextWindow > 0 {
			h.log.Engine("context: %d/%d (%.1f%%) effective=%d threshold=%d",
				u.InputTokens, u.ContextWindow, u.PctUsed,
				u.EffectiveWindow, u.AutoCompactThreshold)
		}
		if h.verbose {
			if u.ContextWindow > 0 {
				h.log.Debug(catTokens, "turn=%d input=%d output=%d window=%d effective=%d threshold=%d pct=%.1f%%",
					u.Turn, u.InputTokens, u.OutputTokens,
					u.ContextWindow, u.EffectiveWindow, u.AutoCompactThreshold, u.PctUsed)
			} else {
				h.log.Debug(catTokens, "turn=%d input=%d output=%d", u.Turn, u.InputTokens, u.OutputTokens)
			}
		}

	case agent.CompactionNeededUpdate:
		h.log.Warn(catCompaction, "Context approaching limit: %d/%d tokens (%.1f%% used)",
			u.CurrentTokens, u.ContextLimit, u.PercentUsed*100)
		if h.debugToStderr {
			h.log.Engine("compaction needed: %d/%d tokens (%.1f%% used)",
				u.CurrentTokens, u.ContextLimit, u.PercentUsed*100)
		}

	case agent.ToolOutputChunk:
		h.log.Raw("%s", u.Chunk)

	case agent.HookOutputChunk:
		// Show incremental hook output on stderr.
		if u.IsStderr {
			h.log.Debug(catHook, "[%s stderr] %s", u.HookName, u.Chunk)
		} else {
			h.log.Debug(catHook, "[%s] %s", u.HookName, u.Chunk)
		}
		// Emit structured hook_progress NDJSON event if --include-hook-events is set.
		if h.includeHookEvents && h.isStreamJSON() {
			h.pw.WriteHookProgress(ipc.PrintHookProgressEvent{
				Type:      "system",
				Subtype:   "hook_progress",
				HookID:    u.HookID,
				HookName:  u.HookName,
				HookEvent: "",
				Stdout:    u.Chunk,
				Stderr:    "",
				Output:    u.Chunk,
				UUID:      ipc.NewEventUUID(),
				SessionID: h.sessionID,
			})
		}

	case agent.HookExecutionUpdate:
		// Always show colored hook info on stderr.
		if u.Status == "started" {
			h.log.Debug(catHook, "→ %s [%s] started", u.HookName, u.HookID)
		} else {
			outcome := "success"
			if !u.Success {
				outcome = "error"
			}
			if u.Blocked {
				h.log.Error(catHook, "✗ %s blocked: %s (%.0fms)", u.HookName, u.Error, u.Duration.Seconds()*1000)
			} else if u.Success {
				h.log.Info(catHook, "✓ %s [%s] %s (%.0fms)", u.HookName, u.Phase, outcome, u.Duration.Seconds()*1000)
			} else {
				h.log.Warn(catHook, "✗ %s error: %s (%.0fms)", u.HookName, u.Error, u.Duration.Seconds()*1000)
			}
		}

		// Emit structured NDJSON hook events if --include-hook-events is set.
		if h.includeHookEvents && h.isStreamJSON() {
			if u.Status == "started" {
				h.pw.WriteHookStarted(ipc.PrintHookStartedEvent{
					Type:      "system",
					Subtype:   "hook_started",
					HookID:    u.HookID,
					HookName:  u.HookName,
					HookEvent: u.MatchedPattern,
					UUID:      ipc.NewEventUUID(),
					SessionID: h.sessionID,
				})
			} else {
				outcome := "success"
				if !u.Success {
					outcome = "error"
				}
				if u.Blocked {
					outcome = "cancelled"
				}
				h.pw.WriteHookResponse(ipc.PrintHookResponseEvent{
					Type:      "system",
					Subtype:   "hook_response",
					HookID:    u.HookID,
					HookName:  u.HookName,
					HookEvent: u.MatchedPattern,
					Output:    u.Output,
					Stdout:    u.Output,
					Stderr:    u.Error,
					ExitCode:  u.ExitCode,
					Outcome:   outcome,
					UUID:      ipc.NewEventUUID(),
					SessionID: h.sessionID,
				})
			}
		}
		if u.Blocked {
			h.log.Error(catHook, "blocked by hook %s: %s", u.HookName, u.Error)
		}

	case agent.FallbackUpdate:
		// Fallback chain progress. Surfaced to stderr so a failed run is
		// diagnosable; the terminal error (when the whole chain fails) is
		// returned by ExecuteMessage and serialized by emitResult as a
		// structured is_error result, so nothing is dropped here.
		switch u.Status {
		case "failed":
			if u.Error != nil {
				h.log.Warn(catError, "fallback attempt %d failed (%s/%s): %v",
					u.AttemptIndex, u.Provider, u.Model, u.Error)
			} else {
				h.log.Warn(catError, "fallback attempt %d failed (%s/%s)",
					u.AttemptIndex, u.Provider, u.Model)
			}
		case "success":
			if u.AttemptIndex > 0 {
				h.log.Info(catSystem, "fallback succeeded on %s/%s (primary %s/%s failed)",
					u.Provider, u.Model, u.FromProvider, u.FromModel)
			}
		default: // "attempting"
			h.log.Debug(catSystem, "fallback attempting %s/%s (index %d)",
				u.Provider, u.Model, u.AttemptIndex)
		}

	case agent.ExhaustedUpdate:
		// Every model in the fallback chain failed. In headless mode the agent
		// returns immediately rather than waiting on a profile picker, and
		// ExecuteMessage returns the terminal error which emitResult serializes
		// as is_error. Record an explicit error so the final result is never an
		// empty success even if (for any reason) ExecuteMessage returns nil.
		var msg strings.Builder
		fmt.Fprintf(&msg, "all %d fallback attempts failed for %s/%s", u.Attempts, u.Provider, u.Model)
		for i, e := range u.Errors {
			if e != nil {
				fmt.Fprintf(&msg, "; [%d] %v", i, e)
			}
		}
		h.log.Error(catError, "%s", msg.String())
		if h.exhaustedErr == nil {
			h.exhaustedErr = fmt.Errorf("%s", msg.String())
		}
		// Never leave the agent blocked on the response channel.
		if u.Response != nil {
			select {
			case u.Response <- agent.FallbackDecision{Cancel: true}:
			default:
			}
		}

	default:
		// Unknown update type. If it signals a terminal failure, capture it so
		// the final result event still serializes as a structured error rather
		// than silently dropping (the old behavior produced no parseable result
		// for agent.FallbackUpdate / agent.ExhaustedUpdate). We never block the
		// stream here; emitResult handles the actual error serialization.
		h.log.Debug(catDebug, "Unhandled update type: %T", update)
	}
}

// toolUseResultFromMetadata builds an ipc.PrintToolUseResult for a tool result,
// surfacing the structured bash tool-output contract carried on the tool
// result metadata (snake_case keys). It is a pure function so it can be unit
// tested. Missing or wrong-typed keys are skipped without panicking, so plain
// text tools (nil metadata) get the same back-compatible result as before.
func toolUseResultFromMetadata(output string, meta map[string]any) ipc.PrintToolUseResult {
	res := ipc.PrintToolUseResult{
		Stdout:           output,
		Interrupted:      false,
		IsImage:          false,
		NoOutputExpected: false,
	}
	if meta == nil {
		return res
	}

	// asInt coerces int/int64/float64 JSON-round-tripped numbers to *int.
	asInt := func(v any) (*int, bool) {
		switch n := v.(type) {
		case int:
			return &n, true
		case int64:
			i := int(n)
			return &i, true
		case float64:
			i := int(n)
			return &i, true
		default:
			return nil, false
		}
	}
	// asInt64 coerces int64/int/float64 numbers to *int64.
	asInt64 := func(v any) (*int64, bool) {
		switch n := v.(type) {
		case int64:
			return &n, true
		case int:
			i := int64(n)
			return &i, true
		case float64:
			i := int64(n)
			return &i, true
		default:
			return nil, false
		}
	}

	if p, ok := asInt(meta["exit_code"]); ok {
		res.ExitCode = p
	}
	if p, ok := asInt64(meta["duration_ms"]); ok {
		res.DurationMs = p
	}
	if b, ok := meta["truncated"].(bool); ok {
		res.Truncated = b
	}
	if s, ok := meta["truncation_method"].(string); ok {
		res.TruncationMethod = s
	}
	if p, ok := asInt(meta["original_output_bytes"]); ok {
		res.OriginalOutputBytes = p
	}
	if p, ok := asInt(meta["returned_output_bytes"]); ok {
		res.ReturnedOutputBytes = p
	}
	if s, ok := meta["output_path"].(string); ok {
		res.FullOutputPath = s
	}
	if c, ok := meta["output_contract"].(map[string]any); ok {
		res.OutputContract = c
	}

	return res
}
