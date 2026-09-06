// Command vocab-hook is a fail-open, Claude-Code-compatible shell hook that
// correlates a tool failure with the agent's NEXT prose/thinking block.
//
// Lifecycle:
//
//	PostToolUse: structured failure -> atomic pending record keyed by session_id
//	PreToolUse:  enrich pending from transcript_path -> JSONL observation
//	Stop:        same enrichment for final responses that issue no next tool
//
// A telemetry process must never block agent work, so main always exits 0.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vocab"
)

type payload struct {
	HookEventName string          `json:"hook_event_name"`
	SessionID     string          `json:"session_id"`
	Transcript    string          `json:"transcript_path"`
	CWD           string          `json:"cwd"`
	ToolName      string          `json:"tool_name"`
	ToolResponse  json.RawMessage `json:"tool_response"`
	Error         json.RawMessage `json:"error"`
}

type pending struct {
	Schema      int       `json:"schema"`
	SessionID   string    `json:"session_id"`
	Tool        string    `json:"tool"`
	ErrorType   string    `json:"error_type"`
	HookBlock   bool      `json:"hook_block"`
	FailureHash string    `json:"failure_hash"`
	ObservedAt  time.Time `json:"observed_at"`
	CWD         string    `json:"cwd,omitempty"`
}

type assistantTurn struct {
	ID        string
	Timestamp string
	Model     string
	Prose     string
	Thinking  string
}

type observation struct {
	Schema       int         `json:"schema"`
	ObservedAt   time.Time   `json:"observed_at"`
	ReactionAt   string      `json:"reaction_at,omitempty"`
	SessionID    string      `json:"session_id"`
	AssistantID  string      `json:"assistant_message_id,omitempty"`
	Model        string      `json:"model,omitempty"`
	Tool         string      `json:"tool"`
	ErrorType    string      `json:"error_type"`
	HookBlock    bool        `json:"hook_block"`
	FailureHash  string      `json:"failure_hash"`
	Terminal     bool        `json:"terminal"`
	TriggerEvent string      `json:"trigger_event"`
	CWD          string      `json:"cwd,omitempty"`
	Prose        vocab.Score `json:"prose"`
	Thinking     vocab.Score `json:"thinking"`
}

func main() {
	// The top-level recovery is intentional. Under Claude Code semantics exit 2
	// blocks the tool. Telemetry must not become an outage.
	defer func() { _ = recover() }()
	var p payload
	if err := json.NewDecoder(os.Stdin).Decode(&p); err != nil || p.SessionID == "" {
		return
	}
	switch p.HookEventName {
	case "PostToolUse":
		handlePost(p)
	case "PreToolUse", "Stop":
		handleReaction(p)
	}
}

func baseDir() string {
	if p := strings.TrimSpace(os.Getenv("SWARM_VOCAB_STATE_DIR")); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".swarm", "telemetry", "vocabulary")
}

func ledgerPath() string {
	if p := strings.TrimSpace(os.Getenv("SWARM_VOCAB_LEDGER")); p != "" {
		return p
	}
	return filepath.Join(baseDir(), "observations.jsonl")
}

func sessionKey(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:12])
}

func pendingPath(id string) string {
	return filepath.Join(baseDir(), "pending", sessionKey(id)+".json")
}

func handlePost(p payload) {
	failed, typ, material := classifyError(p)
	if !failed {
		return
	}
	sum := sha256.Sum256([]byte(p.ToolName + "\x00" + typ + "\x00" + material))
	rec := pending{
		Schema:      1,
		SessionID:   p.SessionID,
		Tool:        p.ToolName,
		ErrorType:   typ,
		HookBlock:   typ == "tool.blocked_by_hook" || strings.Contains(strings.ToLower(material), "blocked by hook"),
		FailureHash: hex.EncodeToString(sum[:12]),
		ObservedAt:  time.Now().UTC(),
		CWD:         p.CWD,
	}
	_ = atomicJSON(pendingPath(p.SessionID), rec)
}

func handleReaction(p payload) {
	raw, err := os.ReadFile(pendingPath(p.SessionID))
	if err != nil {
		return
	}
	var pen pending
	if json.Unmarshal(raw, &pen) != nil {
		return
	}
	turn, ok := readLastAssistant(p.Transcript)
	if !ok {
		// Preserve pending for a later event: transcript persistence can lag the
		// hook by a few milliseconds.
		return
	}
	prose := scorer("prose").Score(turn.Prose)
	thinking := scorer("thinking").Score(turn.Thinking)
	obs := observation{
		Schema:       1,
		ObservedAt:   pen.ObservedAt,
		ReactionAt:   turn.Timestamp,
		SessionID:    pen.SessionID,
		AssistantID:  turn.ID,
		Model:        turn.Model,
		Tool:         pen.Tool,
		ErrorType:    pen.ErrorType,
		HookBlock:    pen.HookBlock,
		FailureHash:  pen.FailureHash,
		Terminal:     p.HookEventName == "Stop",
		TriggerEvent: p.HookEventName,
		CWD:          pen.CWD,
		Prose:        prose,
		Thinking:     thinking,
	}
	if appendJSONL(ledgerPath(), obs) == nil {
		_ = os.Remove(pendingPath(p.SessionID))
	}
}

func scorer(channel string) *vocab.Scorer {
	env := "SWARM_VOCAB_PROSE_ARTIFACT"
	if channel == "thinking" {
		env = "SWARM_VOCAB_THINKING_ARTIFACT"
	}
	if path := strings.TrimSpace(os.Getenv(env)); path != "" {
		if s, err := vocab.Load(path); err == nil {
			return s
		}
	}
	s, _ := vocab.Embedded(channel)
	return s
}

func classifyError(p payload) (bool, string, string) {
	for _, raw := range []json.RawMessage{p.Error, p.ToolResponse} {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) != nil {
			continue
		}
		if ok, typ, material := errorValue(value); ok {
			return ok, typ, material
		}
	}
	return false, "", ""
}

func errorValue(value any) (bool, string, string) {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			return true, "unknown", v
		}
	case map[string]any:
		if nested, ok := v["error"]; ok && nested != nil {
			if ok, typ, material := errorValue(nested); ok {
				return ok, typ, material
			}
		}
		typ, _ := v["type"].(string)
		msg, _ := v["message"].(string)
		if typ != "" || msg != "" {
			if typ == "" {
				typ = "unknown"
			}
			return true, typ, msg
		}
		if flag, _ := v["is_error"].(bool); flag {
			return true, "unknown", fmt.Sprint(v["output"])
		}
		if flag, _ := v["isError"].(bool); flag {
			return true, "unknown", fmt.Sprint(v["output"])
		}
		if success, ok := v["success"].(bool); ok && !success {
			return true, "unknown", fmt.Sprint(v["output"])
		}
	}
	return false, "", ""
}

func readLastAssistant(path string) (assistantTurn, bool) {
	if strings.TrimSpace(path) == "" {
		return assistantTurn{}, false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512<<20 {
		return assistantTurn{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return assistantTurn{}, false
	}
	var doc struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Messages) == 0 {
		return assistantTurn{}, false
	}
	for i := len(doc.Messages) - 1; i >= 0; i-- {
		var msg struct {
			ID        string          `json:"id"`
			Role      string          `json:"role"`
			Timestamp string          `json:"timestamp"`
			Model     string          `json:"model"`
			Content   json.RawMessage `json:"content"`
			Thinking  string          `json:"thinking"`
		}
		if json.Unmarshal(doc.Messages[i], &msg) != nil || msg.Role != "assistant" {
			continue
		}
		return assistantTurn{
			ID:        msg.ID,
			Timestamp: msg.Timestamp,
			Model:     msg.Model,
			Prose:     contentText(msg.Content),
			Thinking:  msg.Thinking,
		}, true
	}
	return assistantTurn{}, false
}

func contentText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "" || b.Type == "text" {
			out = append(out, b.Text)
		}
	}
	return strings.Join(out, "\n")
}

func atomicJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func appendJSONL(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
