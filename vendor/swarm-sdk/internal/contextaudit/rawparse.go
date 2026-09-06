package contextaudit

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// FromRequestJSON parses a raw provider request body (as dumped by
// `swarmos --raw` to stderr) into a Report. It auto-detects the wire format:
//
//   - Anthropic: top-level "system" (string or array of text blocks) and
//     "tools" whose entries carry "input_schema".
//   - OpenAI / chat-completions: the system prompt lives in a messages entry
//     with role "system"; "tools" entries wrap a "function" object.
//
// The returned string names the detected format ("anthropic" or "openai").
// Parsing is best-effort and never fails on missing optional fields — the
// goal is attribution of real captured traffic, not strict validation.
func FromRequestJSON(data []byte) (Report, string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return Report{}, "", fmt.Errorf("parse request JSON: %w", err)
	}

	format := detectFormat(top)
	system := parseSystem(top, format)
	tools := parseTools(top, format)
	msgs := parseMessages(top, format)

	return Report{
		System:   AnalyzeSystemPrompt(system),
		Tools:    ToolComponents(tools),
		Messages: MessageComponents(msgs),
	}, format, nil
}

func detectFormat(top map[string]json.RawMessage) string {
	if _, ok := top["system"]; ok {
		return "anthropic"
	}
	if raw, ok := top["tools"]; ok {
		var arr []map[string]json.RawMessage
		if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
			if _, ok := arr[0]["input_schema"]; ok {
				return "anthropic"
			}
			if _, ok := arr[0]["function"]; ok {
				return "openai"
			}
		}
	}
	// No top-level system and no decisive tool shape: assume OpenAI, where the
	// system prompt is carried as a role:"system" message.
	return "openai"
}

func parseSystem(top map[string]json.RawMessage, format string) string {
	if format == "anthropic" {
		raw, ok := top["system"]
		if !ok {
			return ""
		}
		// system may be a plain string ...
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		// ... or an array of {type:"text", text:"..."} blocks (often the split
		// cached + ephemeral system blocks). Concatenate their text.
		var blocks []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &blocks) == nil {
			parts := make([]string, 0, len(blocks))
			for _, b := range blocks {
				if b.Text != "" {
					parts = append(parts, b.Text)
				}
			}
			return joinNL(parts)
		}
		return ""
	}

	// OpenAI: gather every role:"system" message.
	var parts []string
	for _, m := range rawMessages(top) {
		if m.role() == "system" {
			parts = append(parts, m.text())
		}
	}
	return joinNL(parts)
}

func parseTools(top map[string]json.RawMessage, format string) []provider.Tool {
	raw, ok := top["tools"]
	if !ok {
		return nil
	}
	var arr []map[string]json.RawMessage
	if json.Unmarshal(raw, &arr) != nil {
		return nil
	}

	out := make([]provider.Tool, 0, len(arr))
	for _, entry := range arr {
		var t provider.Tool
		if format == "openai" {
			fn, ok := entry["function"]
			if !ok {
				continue
			}
			var f struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
			}
			if json.Unmarshal(fn, &f) != nil {
				continue
			}
			t.Name = f.Name
			t.Description = f.Description
			t.Parameters = rawOrNil(f.Parameters)
		} else { // anthropic
			t.Name = unmarshalString(entry["name"])
			t.Description = unmarshalString(entry["description"])
			t.Parameters = rawOrNil(entry["input_schema"])
		}
		out = append(out, t)
	}
	return out
}

func parseMessages(top map[string]json.RawMessage, format string) []Msg {
	var out []Msg
	for _, m := range rawMessages(top) {
		role := m.role()
		// OpenAI system messages are already counted in the system bucket.
		if format == "openai" && role == "system" {
			continue
		}
		out = append(out, Msg{Role: role, Content: m.text()})
	}
	return out
}

// rawMessage is one entry of the "messages" array, parsed lazily so we can
// size both string content and structured (array-of-blocks) content.
type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (m rawMessage) role() string { return m.Role }

// text returns a sizing-friendly string for the message content. String
// content is returned verbatim; structured content is returned as its raw
// JSON so tool-result and multi-block payloads are counted in full.
func (m rawMessage) text() string {
	if len(m.Content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	return string(m.Content)
}

func rawMessages(top map[string]json.RawMessage) []rawMessage {
	raw, ok := top["messages"]
	if !ok {
		return nil
	}
	var msgs []rawMessage
	_ = json.Unmarshal(raw, &msgs)
	return msgs
}

func unmarshalString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// rawOrNil returns the parameters as an any (decoded JSON) so ToolComponents
// re-marshals it to the same shape a provider would send. Returns nil for
// absent schemas.
func rawOrNil(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

func joinNL(parts []string) string {
	var out strings.Builder
	for i, p := range parts {
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString(p)
	}
	return out.String()
}
