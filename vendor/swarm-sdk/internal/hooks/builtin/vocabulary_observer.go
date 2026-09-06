package builtin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vocab"
)

// VocabularyObserverHook correlates a structured tool failure with the agent's
// next assistant message and persists scores from the versioned vocabulary
// artifacts. It stores no raw prose or reasoning, injects no model context, and
// always continues. Telemetry must never become an outage.
type VocabularyObserverHook struct {
	mu       sync.Mutex
	pending  map[string]vocabularyPending
	prose    *vocab.Scorer
	thinking *vocab.Scorer
	ledger   string
}

type vocabularyPending struct {
	Tool        string
	ErrorType   string
	HookBlock   bool
	FailureHash string
	ObservedAt  time.Time
}

type vocabularyObservation struct {
	Schema       int         `json:"schema"`
	Source       string      `json:"source"`
	ObservedAt   time.Time   `json:"observed_at"`
	ReactionAt   string      `json:"reaction_at,omitempty"`
	Conversation string      `json:"conversation_id"`
	AssistantID  string      `json:"assistant_message_id,omitempty"`
	Model        string      `json:"model,omitempty"`
	Tool         string      `json:"tool"`
	ErrorType    string      `json:"error_type"`
	HookBlock    bool        `json:"hook_block"`
	FailureHash  string      `json:"failure_hash"`
	Terminal     bool        `json:"terminal"`
	Prose        vocab.Score `json:"prose"`
	Thinking     vocab.Score `json:"thinking"`
}

type vocabularyTurn struct {
	ID, Timestamp, Model, Prose, Thinking string
}

func NewVocabularyObserverHook() *VocabularyObserverHook {
	prose, _ := vocab.Embedded("prose")
	thinking, _ := vocab.Embedded("thinking")
	home, _ := os.UserHomeDir()
	ledger := filepath.Join(home, ".swarm", "telemetry", "vocabulary", "observations.jsonl")
	if override := strings.TrimSpace(os.Getenv("SWARM_VOCAB_LEDGER")); override != "" {
		ledger = override
	}
	return &VocabularyObserverHook{
		pending:  make(map[string]vocabularyPending),
		prose:    prose,
		thinking: thinking,
		ledger:   ledger,
	}
}

func (h *VocabularyObserverHook) Name() string  { return "vocabulary-observer" }
func (h *VocabularyObserverHook) Priority() int { return 15 }
func (h *VocabularyObserverHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolAfterExecute ||
		event.Type == hooks.EventToolExecutionFailed ||
		event.Type == hooks.EventToolBeforeExecute ||
		event.Type == hooks.EventAgentStopped
}

func (h *VocabularyObserverHook) OnEvent(_ context.Context, event hooks.Event) (hooks.HookResult, error) {
	// This hook is deliberately fail-open, including internal parse/write
	// failures. Missing one observation is preferable to perturbing the agent.
	switch event.Type {
	case hooks.EventToolAfterExecute, hooks.EventToolExecutionFailed:
		h.captureFailure(event)
	case hooks.EventToolBeforeExecute, hooks.EventAgentStopped:
		h.captureReaction(event, event.Type == hooks.EventAgentStopped)
	}
	return hooks.Continue(), nil
}

func (h *VocabularyObserverHook) captureFailure(event hooks.Event) {
	if event.ConversationID == "" {
		return
	}
	class := hooks.ClassifyToolFailure(event)
	errorType, material := vocabularyError(event.Data)
	// TUI adapters expose the structured error as a raw {type,message} map.
	// ClassifyToolFailure intentionally accepts narrower generic shapes, so the
	// observer's own successful structured parse is also authoritative.
	failed := class.Failed || material != ""
	hookBlock := errorType == "tool.blocked_by_hook" ||
		strings.Contains(strings.ToLower(material), "blocked by hook")
	if !failed && !hookBlock {
		return
	}
	if errorType == "" {
		errorType = "unknown"
	}
	sum := sha256.Sum256([]byte(class.ToolName + "\x00" + errorType + "\x00" + material))
	h.mu.Lock()
	h.pending[event.ConversationID] = vocabularyPending{
		Tool:        class.ToolName,
		ErrorType:   errorType,
		HookBlock:   hookBlock,
		FailureHash: hex.EncodeToString(sum[:12]),
		ObservedAt:  time.Now().UTC(),
	}
	h.mu.Unlock()
}

func (h *VocabularyObserverHook) captureReaction(event hooks.Event, terminal bool) {
	if event.ConversationID == "" {
		return
	}
	h.mu.Lock()
	pending, ok := h.pending[event.ConversationID]
	h.mu.Unlock()
	if !ok {
		return
	}
	path := hooks.ResolveTranscriptPath(event.ConversationID)
	if explicit, ok := event.Data["transcript_path"].(string); ok && explicit != "" {
		path = explicit
	}
	turn, ok := readVocabularyTurn(path)
	if !ok {
		return // persistence can lag; preserve pending for the next event
	}
	obs := vocabularyObservation{
		Schema:       1,
		Source:       "builtin-hook",
		ObservedAt:   pending.ObservedAt,
		ReactionAt:   turn.Timestamp,
		Conversation: event.ConversationID,
		AssistantID:  turn.ID,
		Model:        turn.Model,
		Tool:         pending.Tool,
		ErrorType:    pending.ErrorType,
		HookBlock:    pending.HookBlock,
		FailureHash:  pending.FailureHash,
		Terminal:     terminal,
		Prose:        h.prose.Score(turn.Prose),
		Thinking:     h.thinking.Score(turn.Thinking),
	}
	if h.append(obs) {
		h.mu.Lock()
		delete(h.pending, event.ConversationID)
		h.mu.Unlock()
	}
}

func (h *VocabularyObserverHook) append(obs vocabularyObservation) bool {
	raw, err := json.Marshal(obs)
	if err != nil {
		return false
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(h.ledger), 0o700); err != nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	f, err := os.OpenFile(h.ledger, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return false
	}
	// The caller drops the pending observation only when this reports success,
	// so a failed close must not be reported as a durable append.
	return f.Close() == nil
}

func vocabularyError(data map[string]any) (typ, material string) {
	var walk func(any) (string, string)
	walk = func(value any) (string, string) {
		switch v := value.(type) {
		case string:
			return "", v
		case map[string]any:
			if nested, ok := v["error"]; ok && nested != nil {
				if t, m := walk(nested); t != "" || m != "" {
					return t, m
				}
			}
			t, _ := v["type"].(string)
			m, _ := v["message"].(string)
			if t != "" || m != "" {
				return t, m
			}
		}
		return "", ""
	}
	for _, key := range []string{"error", "tool_output", "result"} {
		if t, m := walk(data[key]); t != "" || m != "" {
			if t == "" {
				t = "unknown"
			}
			return t, m
		}
	}
	return "", ""
}

func readVocabularyTurn(path string) (vocabularyTurn, bool) {
	if path == "" {
		return vocabularyTurn{}, false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512<<20 {
		return vocabularyTurn{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return vocabularyTurn{}, false
	}
	var doc struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return vocabularyTurn{}, false
	}
	for i := len(doc.Messages) - 1; i >= 0; i-- {
		var msg struct {
			ID, Role, Timestamp, Model, Thinking string
			Content                              json.RawMessage `json:"content"`
		}
		if json.Unmarshal(doc.Messages[i], &msg) != nil || msg.Role != "assistant" {
			continue
		}
		var prose string
		if json.Unmarshal(msg.Content, &prose) != nil {
			var blocks []struct {
				Type, Text string
			}
			if json.Unmarshal(msg.Content, &blocks) == nil {
				for _, block := range blocks {
					if block.Type == "" || block.Type == "text" {
						prose += block.Text + "\n"
					}
				}
			}
		}
		return vocabularyTurn{msg.ID, msg.Timestamp, msg.Model, prose, msg.Thinking}, true
	}
	return vocabularyTurn{}, false
}
