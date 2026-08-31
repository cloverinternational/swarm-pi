// Package client — system_prompt_composition.go
//
// Surfaces the full breakdown of everything that goes into a turn — the system
// prompt as actually sent to the provider (decomposed into labeled sections),
// the tool schemas (which ship in the request's Tools array, not the prompt
// text). This mirrors the TUI's Context settings panel
// (swarm-tui/internal/chat/mode_helpers.go: addSystemPromptToHistory) so an
// embedding UI can show the operator exactly what the model receives.
package client

import (
	"encoding/json"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/contextaudit"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	provanthropic "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
)

// PromptSection is one labeled slice of the assembled system prompt.
type PromptSection struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // "base" | "skills" | "context" | "memory"
	Title     string `json:"title"`
	Icon      string `json:"icon"`
	Content   string `json:"content"`
	LineCount int    `json:"lineCount"`
	CharCount int    `json:"charCount"`
}

// ToolDescriptor describes one tool that ships in the request's Tools array
// (separate from the system prompt text). CharCount is the real per-tool
// context cost: name + description + JSON-serialized parameter schema.
type ToolDescriptor struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CharCount   int    `json:"charCount"`
}

// SystemPromptComposition is the complete, structured breakdown of a turn's
// context: the raw prompt as sent (OAuth prefix mirrored), its decomposed
// sections and the tool schemas.
type SystemPromptComposition struct {
	Raw         string           `json:"raw"`
	Provider    string           `json:"provider"`
	Model       string           `json:"model"`
	OAuth       bool             `json:"oauth"`
	Sections    []PromptSection  `json:"sections"`
	Tools       []ToolDescriptor `json:"tools"`
	ToolChars   int              `json:"toolChars"`
	PromptChars int              `json:"promptChars"`
}

// SystemPromptComposition returns the full breakdown of what the model receives:
//
//   - Raw: the system prompt exactly as sent to the provider, including the
//     request-time Anthropic OAuth CLI prefix the provider prepends inside
//     Chat() (mirrored here since the agent's stored prompt omits it).
//   - Sections: Raw decomposed via contextaudit.SplitSystemPrompt into the base
//     prompt, every <context name="X"> block, and the <available_skills> block.
//   - Tools: the exact tool list that would ship in the next request's Tools
//     array (after mode filtering), with per-tool schema sizes.
//
// Safe to call between turns; reads live agent state.
func (c *Client) SystemPromptComposition() SystemPromptComposition {
	out := SystemPromptComposition{
		Provider: c.ProviderName(),
		Model:    c.CurrentModel(),
		OAuth:    c.oauthActive,
	}

	// Raw prompt as actually sent — start from the agent's composed prompt
	// (base + INDEX.md + skills guidance/index + metadata + mode) and mirror the
	// provider's request-time OAuth prefix.
	raw := ""
	if c.agent != nil {
		raw = c.agent.SystemPrompt()
	}
	if raw == "" {
		raw = c.opts.systemPrompt
	}
	if c.oauthActive && provider.NormalizeProviderName(c.ProviderName()) == "anthropic" {
		prefix := provanthropic.GetCLISystemPromptPrefix()
		if !strings.HasPrefix(raw, prefix) {
			raw = prefix + "\n\n" + raw
		}
	}
	out.Raw = raw
	out.PromptChars = len(raw)

	addSection := func(s PromptSection) {
		if strings.TrimSpace(s.Content) == "" {
			return
		}
		s.LineCount = strings.Count(s.Content, "\n") + 1
		if s.CharCount == 0 {
			s.CharCount = len(s.Content)
		}
		out.Sections = append(out.Sections, s)
	}

	// Decompose the composed prompt into base + context + available_skills.
	for _, sec := range contextaudit.SplitSystemPrompt(raw) {
		typ, title, icon := promptSectionMeta(sec.Label)
		addSection(PromptSection{
			ID:      "sysprompt-" + strings.ReplaceAll(sec.Label, ":", "-"),
			Type:    typ,
			Title:   title,
			Icon:    icon,
			Content: sec.Content,
		})
	}

	// Tools — shipped in the request's Tools array, not the prompt. Surface them
	// with the same per-tool sizing the context-probe uses.
	if c.agent != nil {
		for _, t := range c.agent.ProviderTools() {
			size := len(t.Name) + len(t.Description)
			if t.Parameters != nil {
				if b, err := json.Marshal(t.Parameters); err == nil {
					size += len(b)
				}
			}
			out.Tools = append(out.Tools, ToolDescriptor{
				Name:        t.Name,
				Description: t.Description,
				CharCount:   size,
			})
			out.ToolChars += size
		}
	}

	return out
}

// promptSectionMeta maps a contextaudit split-label to a display type, title,
// and icon — mirroring the TUI's systemSectionMeta.
func promptSectionMeta(label string) (typ, title, icon string) {
	switch {
	case label == "base_prompt":
		return "base", "System Instructions (raw)", "◆"
	case label == "available_skills":
		return "skills", "Available Skills", "✦"
	case strings.HasPrefix(label, "context:"):
		return "context", "Context: " + strings.TrimPrefix(label, "context:"), "▤"
	default:
		return "base", label, "◆"
	}
}
