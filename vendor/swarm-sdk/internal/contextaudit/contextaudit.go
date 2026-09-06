// Package contextaudit measures where the tokens go in an assembled LLM
// request: the system prompt (broken into its tagged sub-sections), the
// tool/skill JSON schemas, and the conversation history. It is the shared
// engine behind the cmd/ctx-breakdown raw-dump analyzer and swarm-tui's
// context-probe, so both surfaces report sizes identically.
//
// Token counts use the SDK's canonical 4-bytes-per-token heuristic (see the
// tokens package). They are approximations — good to roughly ±15% on English
// and code, worse on whitespace-heavy text — not a real tokenizer. The point
// is relative attribution: which sections, tools, and messages dominate the
// context, not an exact billing figure.
package contextaudit

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// charsPerToken mirrors tokens.charsPerToken. Duplicated as a local constant
// so estimates here move in lockstep with the rest of the SDK without taking
// a dependency just for one integer.
const charsPerToken = 4

// estTokens converts a byte count to an estimated token count, matching
// tokens.Estimate semantics: zero stays zero, any non-empty input rounds up
// to at least one token.
func estTokens(n int) int {
	if n <= 0 {
		return 0
	}
	t := n / charsPerToken
	if t == 0 {
		return 1
	}
	return t
}

// Component is one measured contributor to a request — a system-prompt
// section, a tool schema, or a single message.
type Component struct {
	// Label identifies the contributor (e.g. "context:claudeMd", a tool name,
	// or "[2] user").
	Label string
	// Bytes is the byte length of the contributor's text.
	Bytes int
}

// Tokens returns the estimated token count for c.Bytes.
func (c Component) Tokens() int { return estTokens(c.Bytes) }

// Msg is a role-tagged message body, decoupled from any provider type so the
// analyzer (which parses raw provider JSON) and the probe (which holds SDK
// messages) can both feed the same renderer.
type Msg struct {
	Role    string
	Content string
}

// Report is the full breakdown of one request, split into the three buckets
// that make up provider context.
type Report struct {
	System   []Component
	Tools    []Component
	Messages []Component
}

// AnalyzeSystemPrompt splits an assembled system prompt into its labeled
// sub-sections (see SplitSystemPrompt) and returns them as sized Components,
// largest-first. The base-prompt remainder is labeled
// "base_prompt + scaffolding".
func AnalyzeSystemPrompt(system string) []Component {
	secs := SplitSystemPrompt(system)
	comps := make([]Component, 0, len(secs))
	for _, s := range secs {
		label := s.Label
		if label == "base_prompt" {
			label = "base_prompt + scaffolding"
		}
		comps = append(comps, Component{Label: label, Bytes: len(s.Content)})
	}
	sortDesc(comps)
	return comps
}

// ToolComponents measures each tool's contribution as the byte length of its
// name, description, and JSON-serialized parameter schema combined — i.e. the
// payload a provider receives per tool. Returned largest-first.
func ToolComponents(tools []provider.Tool) []Component {
	comps := make([]Component, 0, len(tools))
	for _, t := range tools {
		size := len(t.Name) + len(t.Description)
		if t.Parameters != nil {
			if b, err := json.Marshal(t.Parameters); err == nil {
				size += len(b)
			}
		}
		comps = append(comps, Component{Label: t.Name, Bytes: size})
	}
	sortDesc(comps)
	return comps
}

// MessageComponents measures each message in order (the index is part of the
// label, so order is preserved rather than sorted by size).
func MessageComponents(msgs []Msg) []Component {
	comps := make([]Component, 0, len(msgs))
	for i, m := range msgs {
		role := m.Role
		if role == "" {
			role = "?"
		}
		comps = append(comps, Component{
			Label: fmt.Sprintf("[%d] %s", i, role),
			Bytes: len(m.Content),
		})
	}
	return comps
}

// Render writes the three breakdown tables and a grand total to w. The layout
// echoes the [PROMPT PROVENANCE] banner so output is familiar to anyone who
// has used `swarmos --raw`.
func (r Report) Render(w io.Writer) {
	sysTotal := writeTable(w, "SYSTEM PROMPT", "section", r.System)
	toolTotal := writeTable(w, fmt.Sprintf("TOOL SCHEMAS (%d tools)", len(r.Tools)), "tool", r.Tools)
	msgTotal := writeTable(w, fmt.Sprintf("MESSAGES (%d)", len(r.Messages)), "message", r.Messages)

	grand := sysTotal + toolTotal + msgTotal
	fmt.Fprintf(w, "\n%s\n", eq)
	fmt.Fprintf(w, "  GRAND TOTAL  %12s bytes   ~%s tokens (at 4 ch/tok)\n",
		commas(grand), commas(estTokens(grand)))
	fmt.Fprintf(w, "%s\n", dash)
	writeShareLine(w, "system prompt", sysTotal, grand)
	writeShareLine(w, "tool schemas", toolTotal, grand)
	writeShareLine(w, "messages", msgTotal, grand)
	fmt.Fprintf(w, "%s\n", eq)
}

func writeShareLine(w io.Writer, name string, part, whole int) {
	share := 0.0
	if whole > 0 {
		share = 100 * float64(part) / float64(whole)
	}
	fmt.Fprintf(w, "    %-15s ~%9s tokens   %5.1f%%\n", name, commas(estTokens(part)), share)
}

// writeTable renders one bucket and returns its total byte count.
func writeTable(w io.Writer, title, colname string, comps []Component) int {
	total := 0
	for _, c := range comps {
		total += c.Bytes
	}
	fmt.Fprintf(w, "\n%s\n", eq)
	fmt.Fprintf(w, "  %s — %s bytes (~%s tokens)\n", title, commas(total), commas(estTokens(total)))
	fmt.Fprintf(w, "%s\n", dash)
	if len(comps) == 0 {
		fmt.Fprintf(w, "  (none)\n")
		return total
	}
	fmt.Fprintf(w, "  %-36s %10s %10s %8s\n", colname, "bytes", "~tokens", "share")
	for _, c := range comps {
		share := 0.0
		if total > 0 {
			share = 100 * float64(c.Bytes) / float64(total)
		}
		fmt.Fprintf(w, "  %-36s %10s %10s %7.1f%%\n",
			truncateLabel(c.Label, 36), commas(c.Bytes), commas(c.Tokens()), share)
	}
	return total
}

// --- system-prompt section splitting -----------------------------------------

// Section is a labeled slice of an assembled system prompt: a named context
// block, the available-skills block, or the base-prompt remainder. Unlike
// Component (which carries only a size), Section carries the text, so callers
// that render the prompt (e.g. the TUI) can show each part with its content.
type Section struct {
	Label   string // "base_prompt", "available_skills", or "context:NAME"
	Content string
}

type promptSpan struct {
	label string
	inner string
	start int
	end   int
}

// SplitSystemPrompt decomposes an assembled system prompt into labeled
// sections: each <context name="X"> block ("context:X"), the
// <available_skills> block, and all remaining text ("base_prompt"). The base
// section is returned first, then the tagged blocks in document order. Empty
// sections are omitted. Malformed/unterminated tags are skipped — this is a
// diagnostic, not a validator.
func SplitSystemPrompt(system string) []Section {
	spans := collectPromptSpans(system)

	// Base = the text left after removing every tagged span, in order.
	var base strings.Builder
	cursor := 0
	for _, sp := range spans { // ascending by start (collectPromptSpans sorts)
		if sp.start > cursor {
			base.WriteString(system[cursor:sp.start])
		}
		if sp.end > cursor {
			cursor = sp.end
		}
	}
	if cursor < len(system) {
		base.WriteString(system[cursor:])
	}

	var out []Section
	if b := strings.TrimSpace(base.String()); b != "" {
		out = append(out, Section{Label: "base_prompt", Content: b})
	}
	for _, sp := range spans {
		if strings.TrimSpace(sp.inner) != "" {
			out = append(out, Section{Label: sp.label, Content: sp.inner})
		}
	}
	return out
}

// collectPromptSpans finds every <context name="X">...</context> block and the
// first <available_skills>...</available_skills> block, returned sorted by
// start offset.
func collectPromptSpans(system string) []promptSpan {
	var spans []promptSpan

	const open = `<context name="`
	const closeCtx = `</context>`
	i := 0
	for {
		idx := strings.Index(system[i:], open)
		if idx < 0 {
			break
		}
		full := i + idx
		nameStart := full + len(open)
		q := strings.Index(system[nameStart:], `"`)
		if q < 0 {
			break
		}
		name := system[nameStart : nameStart+q]
		gt := strings.Index(system[nameStart+q:], ">")
		if gt < 0 {
			break
		}
		bodyStart := nameStart + q + gt + 1
		end := strings.Index(system[bodyStart:], closeCtx)
		if end < 0 {
			break
		}
		spans = append(spans, promptSpan{
			label: "context:" + name,
			inner: system[bodyStart : bodyStart+end],
			start: full,
			end:   bodyStart + end + len(closeCtx),
		})
		i = bodyStart + end + len(closeCtx)
	}

	const skOpen = "<available_skills>"
	const skClose = "</available_skills>"
	if s0 := strings.Index(system, skOpen); s0 >= 0 {
		bodyStart := s0 + len(skOpen)
		if e := strings.Index(system[bodyStart:], skClose); e >= 0 {
			spans = append(spans, promptSpan{
				label: "available_skills",
				inner: system[bodyStart : bodyStart+e],
				start: s0,
				end:   bodyStart + e + len(skClose),
			})
		}
	}

	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	return spans
}

// --- small formatting helpers (kept local; no fmt-heavy deps) -----------------

const eq = "══════════════════════════════════════════════════════════════════════"
const dash = "──────────────────────────────────────────────────────────────────────"

func sortDesc(c []Component) {
	sort.SliceStable(c, func(i, j int) bool { return c[i].Bytes > c[j].Bytes })
}

func truncateLabel(label string, max int) string {
	if len(label) <= max {
		return label
	}
	if max <= 1 {
		return label[:max]
	}
	return label[:max-1] + "…"
}

// commas formats an integer with thousands separators (12345 -> "12,345").
func commas(n int) string {
	if n < 0 {
		return "-" + commas(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		b.WriteByte(',')
	}
	for i := pre; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}
