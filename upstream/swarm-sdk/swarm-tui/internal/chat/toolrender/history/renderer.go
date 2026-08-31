// Package history renders HistorySearch and HistoryGet tool results.
package history

import (
	"encoding/json"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// Renderer draws workspace-history tool results as conversation cards instead
// of leaving them to the web-search JSON heuristics.
type Renderer struct{}

// New creates a history renderer.
func New() *Renderer { return &Renderer{} }

// CanRender claims only the dedicated history tools.
func (r *Renderer) CanRender(ctx *toolrender.RenderContext) bool {
	switch ctx.ToolName {
	case "HistorySearch", "history_search", "HistoryGet", "history_get":
		return true
	default:
		return false
	}
}

// PreProcess is stateless; rendering is cheap and width-dependent.
func (r *Renderer) PreProcess(*toolrender.RenderContext) toolrender.CachedResult { return nil }

// Render produces styled lines for search listings and bounded excerpts.
func (r *Renderer) Render(ctx *toolrender.RenderContext, _ toolrender.CachedResult) []string {
	// WHY THE FINAL CLAMP: nothing below is width-aware at all. truncate()
	// caps at a fixed RUNE count (140 for previews, 200 for excerpts), which is
	// unrelated to the viewport and, for CJK or emoji, is up to twice as many
	// columns as it is runes. The i18n headers and the "    ⎿ " gutter are
	// added on top with no measurement. Clamping the assembled line is the only
	// place the real pane width is known.
	//
	// FitLines also expands tabs (one column measured, up to four drawn) and
	// flattens nothing else — titles and previews are stored conversation text
	// and can contain anything a past model or tool emitted.
	return shared.FitLines(r.render(ctx), ctx.Width)
}

// render produces the unclamped lines; Render applies the viewport bound.
func (r *Renderer) render(ctx *toolrender.RenderContext) []string {
	switch ctx.ToolName {
	case "HistoryGet", "history_get":
		return r.renderGet(ctx.Output)
	default:
		return r.renderSearch(ctx.Output)
	}
}

type searchPayload struct {
	Scope         string `json:"scope"`
	WorkspacePath string `json:"workspace_path"`
	Query         string `json:"query"`
	Truncated     bool   `json:"truncated"`
	Results       []struct {
		ID            string `json:"id"`
		Title         string `json:"title"`
		Preview       string `json:"preview"`
		MessageCount  int    `json:"message_count"`
		UpdatedAt     string `json:"updated_at"`
		WorkspacePath string `json:"workspace_path"`
	} `json:"results"`
}

type getPayload struct {
	ConversationID string `json:"conversation_id"`
	Title          string `json:"title"`
	WorkspacePath  string `json:"workspace_path"`
	Truncated      bool   `json:"truncated"`
	Messages       []struct {
		ID      string `json:"id"`
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

func (r *Renderer) renderSearch(output string) []string {
	var payload searchPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &payload); err != nil {
		return fallbackLines(output)
	}
	query := payload.Query
	if strings.TrimSpace(query) == "" {
		query = i18n.T("toolrender.history.recent")
	}
	lines := []string{indent(shared.AnsiBold + shared.AnsiFgBlue + i18n.T("toolrender.history.search_header", query) + shared.AnsiReset)}
	if len(payload.Results) == 0 {
		empty := i18n.T("toolrender.history.no_matches_workspace")
		if payload.Scope == "all" {
			empty = i18n.T("toolrender.history.no_matches_all")
		}
		lines = append(lines, indent(shared.AnsiFgDim+empty+shared.AnsiReset))
		return lines
	}
	plural := i18n.T("toolrender.history.many_conversations")
	if len(payload.Results) == 1 {
		plural = i18n.T("toolrender.history.one_conversation")
	}
	provenance := i18n.T("toolrender.history.workspace", payload.WorkspacePath)
	if payload.Scope == "all" {
		provenance = i18n.T("toolrender.history.all_workspaces")
	}
	lines = append(lines, indent(shared.AnsiFgDim+i18n.T("toolrender.history.search_summary", len(payload.Results), plural, provenance)+shared.AnsiReset))
	for _, result := range payload.Results {
		title := result.Title
		if strings.TrimSpace(title) == "" {
			title = i18n.T("toolrender.history.untitled")
		}
		metadata := i18n.T("toolrender.history.message_count", result.ID, result.MessageCount, result.UpdatedAt)
		if payload.Scope == "all" {
			metadata += i18n.T("toolrender.history.result_workspace", result.WorkspacePath)
		}
		lines = append(lines, indent(shared.AnsiBold+title+shared.AnsiReset+shared.AnsiFgDim+metadata+shared.AnsiReset))
		if preview := strings.TrimSpace(result.Preview); preview != "" {
			lines = append(lines, indent(shared.AnsiFgDim+truncate(preview, 140)+shared.AnsiReset))
		}
	}
	if payload.Truncated {
		lines = append(lines, indent(shared.AnsiFgMuted+i18n.T("toolrender.history.list_truncated")+shared.AnsiReset))
	}
	return lines
}

func (r *Renderer) renderGet(output string) []string {
	var payload getPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &payload); err != nil {
		return fallbackLines(output)
	}
	header := i18n.T("toolrender.history.conversation_header", payload.ConversationID)
	if strings.TrimSpace(payload.Title) != "" {
		header += " — " + payload.Title
	}
	lines := []string{indent(shared.AnsiBold + shared.AnsiFgBlue + header + shared.AnsiReset)}
	if payload.WorkspacePath != "" {
		lines = append(lines, indent(shared.AnsiFgDim+i18n.T("toolrender.history.workspace", payload.WorkspacePath)+shared.AnsiReset))
	}
	if len(payload.Messages) == 0 {
		lines = append(lines, indent(shared.AnsiFgDim+i18n.T("toolrender.history.no_messages")+shared.AnsiReset))
	}
	for _, message := range payload.Messages {
		role := message.Role
		if role == "" {
			role = i18n.T("toolrender.history.unknown_role")
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			content = i18n.T("toolrender.history.empty_message")
		}
		lines = append(lines, indent(shared.AnsiBold+role+shared.AnsiReset+shared.AnsiFgDim+" · "+message.ID+shared.AnsiReset))
		lines = append(lines, indent(truncate(content, 200)))
	}
	if payload.Truncated {
		lines = append(lines, indent(shared.AnsiFgMuted+i18n.T("toolrender.history.excerpt_truncated")+shared.AnsiReset))
	}
	return lines
}

func fallbackLines(output string) []string {
	output = strings.TrimSpace(output)
	if output == "" {
		return []string{indent(shared.AnsiFgDim + i18n.T("toolrender.history.empty_result") + shared.AnsiReset)}
	}
	return []string{indent(truncate(output, 200))}
}

func indent(line string) string {
	// Newlines inside stored conversation text would split one logical line
	// into several without the gutter, so they are flattened here. Renderers
	// return one string per screen line by contract.
	return "    " + shared.AnsiFgMuted + "\u23bf" + shared.AnsiReset + " " + strings.ReplaceAll(line, "\n", " ")
}

func truncate(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max-1]) + "…"
}
