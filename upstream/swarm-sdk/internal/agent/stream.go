package agent

import (
	"context"
)

// StreamEvent is the sealed interface for all events emitted by [Agent.Stream].
// Use a type switch to handle each event kind.
//
// Possible concrete types:
//   - [TextEvent]       — incremental assistant text (one chunk per call)
//   - [ThinkingEvent]   — extended thinking content (Anthropic claude-3-7+)
//   - [ToolCallEvent]   — the agent is about to execute a tool
//   - [ToolResultEvent] — result of a completed tool execution
//   - [ToolOutputEvent] — streaming stdout/stderr chunk from a running tool (e.g. bash)
//   - [DoneEvent]       — execution finished; carries the full [ExecuteResponse]
//   - [ErrorEvent]      — fatal error; the channel is closed immediately after
type StreamEvent interface {
	// streamEvent is an unexported marker method that prevents external types
	// from accidentally satisfying this interface.
	streamEvent()
}

// TextEvent carries an incremental chunk of the assistant's text response.
// Concatenate all Delta values to reconstruct the full assistant message.
type TextEvent struct {
	// Delta is the new text added in this chunk.
	Delta string
	// Append indicates whether Delta should be appended to the previous chunk
	// (true) or treated as the start of a new message segment (false).
	Append bool
}

func (TextEvent) streamEvent() {}

// ThinkingEvent carries an incremental chunk of extended thinking content.
// Only emitted for providers/models that support extended reasoning.
type ThinkingEvent struct {
	// Delta is the new thinking text added in this chunk.
	Delta string
	// Append indicates whether Delta should be appended to the previous chunk.
	Append bool
}

func (ThinkingEvent) streamEvent() {}

// ToolCallEvent is emitted when the agent decides to call a tool.
// It is fired before the tool executes, making it useful for progress display.
type ToolCallEvent struct {
	// ID is the unique identifier for this tool call (used to correlate with ToolResultEvent).
	ID string
	// Name is the tool name (e.g. "bash", "read_file").
	Name string
	// Params are the raw parameters the agent passed to the tool.
	Params map[string]any
}

func (ToolCallEvent) streamEvent() {}

// ToolResultEvent is emitted after a tool finishes executing.
// The ID field matches the corresponding ToolCallEvent.ID.
type ToolResultEvent struct {
	// ID matches the ToolCallEvent.ID that triggered this execution.
	ID string
	// Name is the tool name.
	Name string
	// Output is the tool's text output (may be truncated for very large results).
	Output string
	// Err is non-nil if the tool execution failed.
	Err error
}

func (ToolResultEvent) streamEvent() {}

// ToolOutputEvent carries a streaming output chunk from a running tool.
// Only emitted for tools that support streaming (e.g. bash with long-running commands).
type ToolOutputEvent struct {
	// Chunk is the incremental output text.
	Chunk string
	// Stream is "stdout" or "stderr".
	Stream string
}

func (ToolOutputEvent) streamEvent() {}

// DoneEvent is the final event on the channel. It signals successful completion
// and carries the full execution result. The channel is closed after this event.
type DoneEvent struct {
	// Response contains the complete execution result including the final message,
	// token counts, turn count, and cost estimate.
	Response *ExecuteResponse
}

func (DoneEvent) streamEvent() {}

// ErrorEvent is emitted when a fatal error stops execution.
// The channel is closed after this event. No DoneEvent will follow.
type ErrorEvent struct {
	// Err is the error that caused execution to stop.
	Err error
}

func (ErrorEvent) streamEvent() {}

// Stream runs the agent and emits execution events on the returned channel.
//
// Stream is the real-time alternative to [Agent.Run]: instead of blocking
// until execution completes, it returns immediately with a channel that
// receives events as they happen — text chunks, tool calls, tool results,
// and a final [DoneEvent] or [ErrorEvent].
//
// The channel is closed after [DoneEvent] or [ErrorEvent] is sent.
// Callers must consume the channel to avoid a goroutine leak.
//
// Example — print text as it streams:
//
//	for event := range ag.Stream(ctx, "Fix the failing tests.") {
//	    switch e := event.(type) {
//	    case agent.TextEvent:
//	        fmt.Print(e.Delta)
//	    case agent.ToolCallEvent:
//	        fmt.Printf("\n[calling %s...]\n", e.Name)
//	    case agent.ToolResultEvent:
//	        if e.Err != nil {
//	            fmt.Printf("[%s failed: %v]\n", e.Name, e.Err)
//	        }
//	    case agent.DoneEvent:
//	        fmt.Printf("\nDone — %d tokens\n", e.Response.TokensUsed)
//	    case agent.ErrorEvent:
//	        log.Fatal(e.Err)
//	    }
//	}
func (a *Agent) Stream(ctx context.Context, message string) <-chan StreamEvent {
	// Buffer of 64 keeps the producer (Execute) from blocking on transient
	// consumer slowness (e.g. a UI render frame). Increase if your tools
	// emit very rapid streaming output.
	ch := make(chan StreamEvent, 64)

	go func() {
		defer close(ch)

		// Save the existing intermediate callback so we can restore it after
		// this Stream call completes — Stream must not permanently replace a
		// callback that the caller registered for other purposes.
		a.mu.RLock()
		prevCB := a.intermediateCallback
		a.mu.RUnlock()

		// Wire our bridge callback to convert IntermediateUpdate → StreamEvent.
		a.SetIntermediateCallback(func(_ context.Context, u IntermediateUpdate) error {
			switch v := u.(type) {
			case ContentUpdate:
				select {
				case ch <- TextEvent{Delta: v.Content, Append: v.Append}:
				case <-ctx.Done():
				}
			case ThinkingUpdate:
				select {
				case ch <- ThinkingEvent{Delta: v.Content, Append: v.Append}:
				case <-ctx.Done():
				}
			case ToolCallUpdate:
				select {
				case ch <- ToolCallEvent{ID: v.ID, Name: v.Name, Params: v.Parameters}:
				case <-ctx.Done():
				}
			case ToolResultUpdate:
				select {
				case ch <- ToolResultEvent{ID: v.ID, Name: "", Output: v.Output, Err: v.Error}:
				case <-ctx.Done():
				}
			case ToolOutputChunk:
				select {
				case ch <- ToolOutputEvent{Chunk: v.Chunk, Stream: v.Stream}:
				case <-ctx.Done():
				}
			}
			return nil
		})

		// Always restore the prior callback when the goroutine exits.
		defer a.SetIntermediateCallback(prevCB)

		result, err := a.Execute(ctx, ExecuteRequest{Message: message})
		if err != nil {
			select {
			case ch <- ErrorEvent{Err: err}:
			default:
			}
			return
		}

		select {
		case ch <- DoneEvent{Response: result}:
		default:
		}
	}()

	return ch
}
