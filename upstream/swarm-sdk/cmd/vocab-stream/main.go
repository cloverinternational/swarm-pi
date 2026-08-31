// Command vocab-stream scores failure reactions from Swarm --output-format
// stream-json logs. Harbor runs use --no-save-conversation, so the stream is the
// durable provenance source available after a benchmark.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vocab"
)

type block struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Text      string `json:"text"`
	Thinking  string `json:"thinking"`
	ToolUseID string `json:"tool_use_id"`
	Content   any    `json:"content"`
	IsError   bool   `json:"is_error"`
}

type streamEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Message   struct {
		Role    string  `json:"role"`
		Model   string  `json:"model"`
		Content []block `json:"content"`
	} `json:"message"`
}

type pending struct {
	SessionID, Tool, ErrorType, FailureHash string
	HookBlock                               bool
	Prose, Thinking, Model                  string
	NextTool                                string
}

type observation struct {
	Schema      int         `json:"schema"`
	Source      string      `json:"source"`
	SessionID   string      `json:"session_id"`
	Model       string      `json:"model,omitempty"`
	Tool        string      `json:"tool"`
	ErrorType   string      `json:"error_type"`
	HookBlock   bool        `json:"hook_block"`
	FailureHash string      `json:"failure_hash"`
	NextTool    string      `json:"next_tool,omitempty"`
	Terminal    bool        `json:"terminal"`
	Prose       vocab.Score `json:"prose"`
	Thinking    vocab.Score `json:"thinking"`
}

type analyzer struct {
	prose, thinking *vocab.Scorer
	calls           map[string]string
	pending         *pending
	out             *json.Encoder
	n               int
}

func main() {
	in := flag.String("input", "", "Swarm stream-json log (default: stdin)")
	out := flag.String("output", "", "observation JSONL (default: stdout)")
	flag.Parse()
	var reader = os.Stdin
	if *in != "" {
		f, err := os.Open(*in)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		defer func() { _ = f.Close() }()
		reader = f
	}
	var writer = os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		defer func() {
			if err := f.Close(); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}()
		writer = f
	}
	prose, _ := vocab.Embedded("prose")
	thinking, _ := vocab.Embedded("thinking")
	a := analyzer{prose: prose, thinking: thinking, calls: map[string]string{}, out: json.NewEncoder(writer)}
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 64<<10), 16<<20)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if !strings.HasPrefix(line, "{") {
			continue // human-readable hook/tool lines share the stream
		}
		var ev streamEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		a.consume(ev)
	}
	a.flush(true)
	if err := scan.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "vocab-stream: observations=%d\n", a.n)
}

func (a *analyzer) consume(ev streamEvent) {
	switch ev.Type {
	case "assistant":
		if a.pending != nil {
			if ev.Message.Model != "" {
				a.pending.Model = ev.Message.Model
			}
		}
		for _, b := range ev.Message.Content {
			switch b.Type {
			case "tool_use":
				a.calls[b.ID] = b.Name
				if a.pending != nil && a.pending.NextTool == "" {
					a.pending.NextTool = b.Name
				}
			case "thinking":
				if a.pending != nil {
					a.pending.Thinking += b.Thinking + "\n"
				}
			case "text":
				if a.pending != nil {
					a.pending.Prose += b.Text + "\n"
				}
			}
		}
	case "user":
		// A user event containing a tool result closes the assistant reaction to
		// the prior failure. Flush before opening a new failure.
		hasResult := false
		for _, b := range ev.Message.Content {
			if b.Type == "tool_result" {
				hasResult = true
				break
			}
		}
		if hasResult {
			a.flush(false)
		}
		for _, b := range ev.Message.Content {
			if b.Type != "tool_result" || !b.IsError {
				continue
			}
			tool := a.calls[b.ToolUseID]
			typ, material := resultError(b.Content)
			sum := hashFailure(tool, typ, material)
			a.pending = &pending{
				SessionID: ev.SessionID,
				Tool:      tool, ErrorType: typ, FailureHash: sum,
				HookBlock: typ == "tool.blocked_by_hook" ||
					strings.Contains(strings.ToLower(material), "blocked by hook"),
			}
		}
	}
}

func (a *analyzer) flush(terminal bool) {
	if a.pending == nil {
		return
	}
	// A failure at EOF with no subsequent assistant reaction is not a document.
	if strings.TrimSpace(a.pending.Prose) == "" && strings.TrimSpace(a.pending.Thinking) == "" {
		if terminal {
			a.pending = nil
		}
		return
	}
	_ = a.out.Encode(observation{
		Schema: 1, Source: "swarm-stream", SessionID: a.pending.SessionID,
		Model: a.pending.Model, Tool: a.pending.Tool, ErrorType: a.pending.ErrorType,
		HookBlock: a.pending.HookBlock, FailureHash: a.pending.FailureHash,
		NextTool: a.pending.NextTool, Terminal: terminal,
		Prose: a.prose.Score(a.pending.Prose), Thinking: a.thinking.Score(a.pending.Thinking),
	})
	a.n++
	a.pending = nil
}

func resultError(content any) (typ, material string) {
	typ = "unknown"
	switch v := content.(type) {
	case string:
		material = v
		var decoded map[string]any
		if json.Unmarshal([]byte(v), &decoded) == nil {
			if e, ok := decoded["error"].(map[string]any); ok {
				if t, ok := e["type"].(string); ok && t != "" {
					typ = t
				}
				if m, ok := e["message"].(string); ok {
					material = m
				}
			}
		}
	case map[string]any:
		raw, _ := json.Marshal(v)
		material = string(raw)
		if t, ok := v["type"].(string); ok && t != "" {
			typ = t
		}
	}
	return typ, material
}

func hashFailure(parts ...string) string {
	// Keep this local to avoid making the runtime scorer own event identity.
	joined := strings.Join(parts, "\x00")
	var h uint64 = 1469598103934665603 // FNV-1a, stable and sufficient for dedup
	for i := 0; i < len(joined); i++ {
		h ^= uint64(joined[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("%016x", h)
}
