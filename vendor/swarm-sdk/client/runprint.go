package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// RunPrintMode selects the output format used by [RunPrint].
type RunPrintMode string

const (
	// RunPrintModeText writes a terse, human-readable transcript: one line
	// per tool call, one line per tool result, then the agent's final
	// response text. Suitable for terminals and log files.
	RunPrintModeText RunPrintMode = "text"

	// RunPrintModeJSON writes one NDJSON event per line. Event shape:
	//
	//	{"type":"tool_call",   "id":"…","name":"…","input":{…},"sequence":N}
	//	{"type":"tool_result", "id":"…","ok":bool,"error":"…","preview":"…","sequence":N}
	//	{"type":"text",        "delta":"…","sequence":N}
	//	{"type":"thinking",    "delta":"…","sequence":N}
	//	{"type":"final",       "message":"…","input_tokens":N,"output_tokens":N,"turn_count":N}
	//	{"type":"error",       "message":"…"}
	//
	// Consumers read line-by-line and unmarshal into a discriminated union
	// keyed on the "type" field. The schema is intentionally minimal in v1;
	// new event types are additive and existing fields will not be removed.
	RunPrintModeJSON RunPrintMode = "json"
)

// RunPrintOption configures a [RunPrint] invocation.
type RunPrintOption func(*runPrintOptions)

type runPrintOptions struct {
	mode            RunPrintMode
	previewMaxBytes int
	suppressFinal   bool
}

// WithRunPrintMode sets the output format. Default is [RunPrintModeText].
func WithRunPrintMode(m RunPrintMode) RunPrintOption {
	return func(o *runPrintOptions) { o.mode = m }
}

// WithRunPrintPreviewBytes sets the maximum number of bytes from a tool
// result to include in the text-mode preview line. JSON mode is unaffected
// (full output is emitted only via the agent's regular Subscribe stream;
// RunPrint summarises in JSON too). Default 200; pass 0 to disable previews.
func WithRunPrintPreviewBytes(n int) RunPrintOption {
	return func(o *runPrintOptions) { o.previewMaxBytes = n }
}

// WithRunPrintSuppressFinal omits the final-message line in text mode and
// the "final" event in JSON mode. The final message is still returned via
// [RunPrintStats].Message — useful when the caller wants to format it itself.
func WithRunPrintSuppressFinal() RunPrintOption {
	return func(o *runPrintOptions) { o.suppressFinal = true }
}

// RunPrintStats summarises a [RunPrint] execution. Returned even when the
// underlying Execute call fails, so callers can attribute partial work.
type RunPrintStats struct {
	// Message is the agent's final response text. May be empty on error.
	Message string
	// ToolCalls is the total number of tool invocations the agent made.
	ToolCalls int
	// ToolCounts is a per-tool-name breakdown of invocations.
	ToolCounts map[string]int
	// TurnCount is the number of provider calls (one per LLM turn).
	TurnCount int
	// InputTokens is the cumulative input token count across all turns.
	InputTokens int
	// OutputTokens is the cumulative output token count across all turns.
	OutputTokens int
}

// RunPrint executes req against c, streams a transcript to w, and returns
// stats describing what happened. It is the SDK's "-p" / one-shot driver:
// no session state, no approval prompts beyond what hooks impose, no
// subscriber wiring required by the caller.
//
// Pass nil for w to discard streaming output and only consume the returned
// stats. The returned *RunPrintStats is non-nil even when err is non-nil
// (it captures any tool calls that completed before the failure).
//
// Example:
//
//	stats, err := client.RunPrint(ctx, c,
//	    agent.ExecuteRequest{Message: "summarize repo"},
//	    os.Stdout,
//	    client.WithRunPrintMode(client.RunPrintModeJSON),
//	)
//	if err != nil { return err }
//	log.Printf("agent made %d tool calls", stats.ToolCalls)
//
// Cancellation: pass a context with a deadline; the underlying Execute
// honours it.
func RunPrint(ctx context.Context, c *Client, req agent.ExecuteRequest, w io.Writer, opts ...RunPrintOption) (*RunPrintStats, error) {
	o := runPrintOptions{
		mode:            RunPrintModeText,
		previewMaxBytes: 200,
	}
	for _, fn := range opts {
		fn(&o)
	}
	if w == nil {
		w = io.Discard
	}

	stats := &RunPrintStats{ToolCounts: map[string]int{}}
	var mu sync.Mutex
	emit := func(write func() error) {
		mu.Lock()
		defer mu.Unlock()
		_ = write()
	}

	unsub := c.SubscribeUpdates(func(_ context.Context, u agent.IntermediateUpdate) error {
		switch v := u.(type) {
		case agent.ToolCallUpdate:
			mu.Lock()
			stats.ToolCalls++
			stats.ToolCounts[v.Name]++
			mu.Unlock()
			emit(func() error { return writeToolCall(w, o.mode, v) })
		case agent.ToolResultUpdate:
			emit(func() error { return writeToolResult(w, o.mode, v, o.previewMaxBytes) })
		case agent.ContentUpdate:
			if o.mode == RunPrintModeJSON {
				emit(func() error {
					return writeJSON(w, jsonEvent{"type": "text", "delta": v.Content, "sequence": v.Sequence})
				})
			}
			// Text mode skips per-delta content; the final message is
			// printed once at the end via writeFinal so callers see one
			// coherent block instead of interleaved tokens.
		case agent.ThinkingUpdate:
			if o.mode == RunPrintModeJSON {
				emit(func() error {
					return writeJSON(w, jsonEvent{"type": "thinking", "delta": v.Content, "sequence": v.Sequence})
				})
			}
		}
		return nil
	})
	defer unsub()

	resp, err := c.Execute(ctx, req)
	if err != nil {
		if o.mode == RunPrintModeJSON {
			emit(func() error { return writeJSON(w, jsonEvent{"type": "error", "message": err.Error()}) })
		} else {
			emit(func() error { _, werr := fmt.Fprintf(w, "[error] %s\n", err.Error()); return werr })
		}
		return stats, err
	}
	if resp != nil {
		stats.Message = resp.Message
		stats.TurnCount = resp.TurnCount
		stats.InputTokens = resp.InputTokens
		stats.OutputTokens = resp.OutputTokens
	}
	if !o.suppressFinal {
		emit(func() error { return writeFinal(w, o.mode, stats) })
	}
	return stats, nil
}

type jsonEvent map[string]any

func writeJSON(w io.Writer, ev jsonEvent) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

func writeToolCall(w io.Writer, mode RunPrintMode, u agent.ToolCallUpdate) error {
	if mode == RunPrintModeJSON {
		return writeJSON(w, jsonEvent{
			"type":     "tool_call",
			"id":       u.ID,
			"name":     u.Name,
			"input":    u.Parameters,
			"sequence": u.Sequence,
		})
	}
	_, err := fmt.Fprintf(w, "→ %s\n", u.Name)
	return err
}

func writeToolResult(w io.Writer, mode RunPrintMode, u agent.ToolResultUpdate, previewMax int) error {
	ok := u.Error == nil
	if mode == RunPrintModeJSON {
		ev := jsonEvent{
			"type":     "tool_result",
			"id":       u.ID,
			"ok":       ok,
			"sequence": u.Sequence,
		}
		if u.Error != nil {
			ev["error"] = u.Error.Error()
		}
		if previewMax > 0 {
			ev["preview"] = truncateForPreview(u.Output, previewMax)
		}
		return writeJSON(w, ev)
	}
	if ok {
		_, err := fmt.Fprintf(w, "← %s ok\n", u.ID)
		return err
	}
	_, err := fmt.Fprintf(w, "← %s err: %s\n", u.ID, u.Error.Error())
	return err
}

func writeFinal(w io.Writer, mode RunPrintMode, s *RunPrintStats) error {
	if mode == RunPrintModeJSON {
		return writeJSON(w, jsonEvent{
			"type":          "final",
			"message":       s.Message,
			"input_tokens":  s.InputTokens,
			"output_tokens": s.OutputTokens,
			"turn_count":    s.TurnCount,
		})
	}
	if s.Message == "" {
		return nil
	}
	_, err := fmt.Fprintf(w, "\n%s\n", s.Message)
	return err
}

func truncateForPreview(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
