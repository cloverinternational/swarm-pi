package contextaudit

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// This file adds a machine-readable (JSON) projection of a context Report plus
// a classification of every contributor by *kind* and *visibility*. The point
// is to make the "hidden" context — ephemeral system blocks, injected
// swarmos_context, <system-reminder> nudges, task/skill reminders, hook output
// — explicit and quantified, both for the `swarm audit` CLI and the TUI
// context tab. Token counts remain estimates (4 chars/token) unless a caller
// overrides them with real provider usage.

// Kind classifies a context contributor by its semantic source.
type Kind string

const (
	KindBase       Kind = "base"        // base system prompt + scaffolding
	KindContext    Kind = "context"     // a named <context name="X"> block
	KindCached     Kind = "cached"      // swarmos_cached_context (injected, cacheable)
	KindEphemeral  Kind = "ephemeral"   // swarmos_context_ephemeral / <swarmos_context>
	KindSkills     Kind = "skills"      // <available_skills> catalog
	KindReminder   Kind = "reminder"    // <system-reminder> block
	KindTaskNudge  Kind = "task_nudge"  // task maintenance / nudge reminder
	KindSkillNudge Kind = "skill_nudge" // autogenskills review nudge
	KindHook       Kind = "hook"        // hook-injected context
	KindInjected   Kind = "injected"    // other injected user/context message
	KindTool       Kind = "tool"        // a tool/skill JSON schema
	KindMessage    Kind = "message"     // an ordinary conversation message
)

// Class is the classification result for one contributor.
type Class struct {
	Kind      Kind
	Ephemeral bool // regenerated every turn / not part of the durable transcript
	Hidden    bool // not normally shown to the user (the thing we want to expose)
}

// classify determines the Kind and visibility flags for a contributor given its
// label and (when available) its text content. content may be empty for
// label-only classification.
func classify(label, content string) Class {
	lower := strings.ToLower(label)
	body := content

	// System-prompt sections.
	switch {
	case strings.HasPrefix(label, "base_prompt"):
		return Class{Kind: KindBase}
	case label == "available_skills" || strings.Contains(lower, "available_skills"):
		return Class{Kind: KindSkills}
	case strings.HasPrefix(label, "context:"):
		name := strings.ToLower(strings.TrimPrefix(label, "context:"))
		switch {
		case strings.Contains(name, "ephemeral"):
			return Class{Kind: KindEphemeral, Ephemeral: true, Hidden: true}
		case strings.Contains(name, "cached"):
			return Class{Kind: KindCached, Hidden: true}
		case strings.Contains(name, "swarmos_context"):
			return Class{Kind: KindEphemeral, Ephemeral: true, Hidden: true}
		default:
			return Class{Kind: KindContext}
		}
	}

	// Message-level classification: scan content for the wrappers that hide
	// injected context inside otherwise-ordinary messages.
	if body != "" {
		lb := strings.ToLower(body)
		switch {
		case strings.Contains(lb, "<system-reminder"):
			// Distinguish the specific nudge families for precise attribution.
			switch {
			case strings.Contains(lb, "task nudge") || strings.Contains(lb, "task-maintenance") || strings.Contains(lb, "task_nudge"):
				return Class{Kind: KindTaskNudge, Ephemeral: true, Hidden: true}
			case strings.Contains(lb, "skill review") || strings.Contains(lb, "autogenskills"):
				return Class{Kind: KindSkillNudge, Ephemeral: true, Hidden: true}
			case strings.Contains(lb, "source=\"") && strings.Contains(lb, "hook"):
				return Class{Kind: KindHook, Ephemeral: true, Hidden: true}
			default:
				return Class{Kind: KindReminder, Ephemeral: true, Hidden: true}
			}
		case strings.Contains(lb, "<swarmos_context"):
			return Class{Kind: KindEphemeral, Ephemeral: true, Hidden: true}
		}
	}

	return Class{Kind: KindMessage}
}

// ComponentJSON is the serialized form of one contributor with its
// classification and share of the total context.
type ComponentJSON struct {
	Label     string  `json:"label"`
	Kind      Kind    `json:"kind"`
	Bytes     int     `json:"bytes"`
	Tokens    int     `json:"tokens"`
	Pct       float64 `json:"pct_of_bucket"`
	Ephemeral bool    `json:"ephemeral,omitempty"`
	Hidden    bool    `json:"hidden,omitempty"`
}

// BucketJSON is a named group of components (system / tools / messages).
type BucketJSON struct {
	Name       string          `json:"name"`
	Bytes      int             `json:"bytes"`
	Tokens     int             `json:"tokens"`
	Pct        float64         `json:"pct_of_total"`
	Components []ComponentJSON `json:"components"`
}

// HiddenSummaryJSON aggregates every contributor flagged Hidden — the headline
// "context you couldn't see" figure.
type HiddenSummaryJSON struct {
	Sources    []string `json:"sources"` // distinct kinds contributing hidden context
	Count      int      `json:"count"`   // number of hidden components
	Bytes      int      `json:"bytes"`
	Tokens     int      `json:"tokens"`
	PctOfTotal float64  `json:"pct_of_total"`
}

// ReportJSON is the full machine-readable breakdown of one assembled request.
type ReportJSON struct {
	Format           string            `json:"format,omitempty"`
	Estimate         bool              `json:"estimate"` // true = token counts are heuristic
	System           BucketJSON        `json:"system"`
	Tools            BucketJSON        `json:"tools"`
	Messages         BucketJSON        `json:"messages"`
	GrandTotalBytes  int               `json:"grand_total_bytes"`
	GrandTotalTokens int               `json:"grand_total_tokens"`
	Hidden           HiddenSummaryJSON `json:"hidden_context"`
}

// buildBucket converts labeled (component, class) pairs into a BucketJSON.
func buildBucket(name string, comps []ComponentJSON) BucketJSON {
	total := 0
	for _, c := range comps {
		total += c.Bytes
	}
	for i := range comps {
		if total > 0 {
			comps[i].Pct = 100 * float64(comps[i].Bytes) / float64(total)
		}
	}
	return BucketJSON{Name: name, Bytes: total, Tokens: estTokens(total), Components: comps}
}

// classifiedSystem builds system components from the split sections so each
// carries its Kind/visibility (content is available here, unlike Report.System).
func classifiedSystem(system string) []ComponentJSON {
	secs := SplitSystemPrompt(system)
	out := make([]ComponentJSON, 0, len(secs))
	for _, s := range secs {
		label := s.Label
		if label == "base_prompt" {
			label = "base_prompt + scaffolding"
		}
		cl := classify(label, s.Content)
		out = append(out, ComponentJSON{
			Label: label, Kind: cl.Kind, Bytes: len(s.Content), Tokens: estTokens(len(s.Content)),
			Ephemeral: cl.Ephemeral, Hidden: cl.Hidden,
		})
	}
	sortComponentsDesc(out)
	return out
}

func classifiedTools(tools []provider.Tool) []ComponentJSON {
	comps := ToolComponents(tools)
	out := make([]ComponentJSON, 0, len(comps))
	for _, c := range comps {
		out = append(out, ComponentJSON{
			Label: c.Label, Kind: KindTool, Bytes: c.Bytes, Tokens: c.Tokens(),
		})
	}
	return out
}

func classifiedMessages(msgs []Msg) []ComponentJSON {
	out := make([]ComponentJSON, 0, len(msgs))
	for i, m := range msgs {
		role := m.Role
		if role == "" {
			role = "?"
		}
		cl := classify("["+itoa(i)+"] "+role, m.Content)
		out = append(out, ComponentJSON{
			Label: "[" + itoa(i) + "] " + role, Kind: cl.Kind, Bytes: len(m.Content),
			Tokens: estTokens(len(m.Content)), Ephemeral: cl.Ephemeral, Hidden: cl.Hidden,
		})
	}
	return out
}

// classifiedProviderMessages attributes the provider-relevant payload of each
// canonical message. It intentionally excludes persistence/display fields such
// as IDs, timestamps, edit history, stored token accounting, and sub-agent UI
// state. The result is still an estimate: concrete provider translators may
// encode these canonical fields differently on the wire.
func classifiedProviderMessages(msgs []*conversation.Message) []ComponentJSON {
	out := make([]ComponentJSON, 0, len(msgs))
	for i, msg := range msgs {
		if msg == nil {
			continue
		}
		payload := map[string]any{
			"role":    msg.Role,
			"content": msg.Content,
		}
		if msg.Thinking != "" {
			payload["thinking"] = msg.Thinking
		}
		if len(msg.OrderedBlocks) > 0 {
			payload["ordered_blocks"] = msg.OrderedBlocks
		}
		if len(msg.ToolCalls) > 0 {
			payload["tool_calls"] = msg.ToolCalls
		}
		if len(msg.ToolResults) > 0 {
			payload["tool_results"] = msg.ToolResults
		}
		if len(msg.Metadata) > 0 {
			payload["metadata"] = msg.Metadata
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			// Provider-specific metadata can contain diagnostic values that JSON
			// cannot encode. Drop metadata rather than losing the entire message
			// attribution; the core content/tool/thinking fields remain counted.
			delete(payload, "metadata")
			raw, _ = json.Marshal(payload)
		}
		label := "[" + itoa(i) + "] " + string(msg.Role)
		guidance, ordinary, hasGuidance := splitLeadingRuntimeGuidance(msg.Content)
		if hasGuidance {
			cachedSourceBytes := taggedBlockBytes(guidance, "cached_context")
			ephemeralSourceBytes := taggedBlockBytes(guidance, "ephemeral_context")
			otherGuidanceSourceBytes := len(guidance) - cachedSourceBytes - ephemeralSourceBytes
			if otherGuidanceSourceBytes < 0 {
				otherGuidanceSourceBytes = 0
			}
			allocated := allocateComponentBytes(len(raw), []int{
				len(ordinary), otherGuidanceSourceBytes, cachedSourceBytes, ephemeralSourceBytes,
			})
			baseBytes := allocated[0]
			if baseBytes > 0 {
				cl := classify(label, ordinary)
				out = append(out, ComponentJSON{
					Label: label, Kind: cl.Kind, Bytes: baseBytes, Tokens: estTokens(baseBytes),
					Ephemeral: cl.Ephemeral, Hidden: cl.Hidden,
				})
			}
			if allocated[1] > 0 {
				out = append(out, ComponentJSON{
					Label: label + " runtime_guidance", Kind: KindInjected,
					Bytes: allocated[1], Tokens: estTokens(allocated[1]), Hidden: true,
				})
			}
			if allocated[2] > 0 {
				out = append(out, ComponentJSON{
					Label: label + " cached_context", Kind: KindCached,
					Bytes: allocated[2], Tokens: estTokens(allocated[2]), Hidden: true,
				})
			}
			if allocated[3] > 0 {
				out = append(out, ComponentJSON{
					Label: label + " ephemeral_context", Kind: KindEphemeral,
					Bytes: allocated[3], Tokens: estTokens(allocated[3]), Ephemeral: true, Hidden: true,
				})
			}
			continue
		}
		cl := classify(label, msg.Content)
		out = append(out, ComponentJSON{
			Label: label, Kind: cl.Kind, Bytes: len(raw), Tokens: estTokens(len(raw)),
			Ephemeral: cl.Ephemeral, Hidden: cl.Hidden,
		})
	}
	return out
}

func splitLeadingRuntimeGuidance(content string) (guidance, ordinary string, ok bool) {
	const startTag = "<swarm_runtime_guidance>"
	const endTag = "</swarm_runtime_guidance>"
	trimmed := strings.TrimLeftFunc(content, unicode.IsSpace)
	if !strings.HasPrefix(trimmed, startTag) {
		return "", content, false
	}
	// Runtime context is arbitrary text and may itself contain delimiter
	// literals. If the envelope is malformed or ambiguous, conservatively
	// classify the whole message as injected guidance instead of allowing any
	// injected bytes to leak into the ordinary visible-message bucket.
	if strings.Count(trimmed, endTag) != 1 {
		return content, "", true
	}
	leadingBytes := len(content) - len(trimmed)
	endRel := strings.Index(trimmed[len(startTag):], endTag)
	if endRel < 0 {
		return content, "", true
	}
	end := leadingBytes + len(startTag) + endRel + len(endTag)
	return content[:end], content[end:], true
}

func allocateComponentBytes(total int, weights []int) []int {
	allocated := make([]int, len(weights))
	weightTotal := 0
	for _, weight := range weights {
		if weight > 0 {
			weightTotal += weight
		}
	}
	if total <= 0 || weightTotal == 0 {
		return allocated
	}
	used := 0
	lastWeighted := 0
	for i, weight := range weights {
		if weight <= 0 {
			continue
		}
		lastWeighted = i
		allocated[i] = total * weight / weightTotal
		used += allocated[i]
	}
	allocated[lastWeighted] += total - used
	return allocated
}

func taggedBlockBytes(content, tag string) int {
	open := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	if strings.Count(content, open) != strings.Count(content, closeTag) {
		// Keep ambiguous bytes in the caller's generic hidden-guidance bucket.
		return 0
	}
	total := 0
	for offset := 0; offset < len(content); {
		startRel := strings.Index(content[offset:], open)
		if startRel < 0 {
			break
		}
		start := offset + startRel
		endRel := strings.Index(content[start+len(open):], closeTag)
		if endRel < 0 {
			break
		}
		end := start + len(open) + endRel + len(closeTag)
		total += end - start
		offset = end
	}
	return total
}

// BuildReportJSON assembles a classified ReportJSON directly from the request
// parts, preserving message content so hidden-in-message context is detected.
func BuildReportJSON(system string, tools []provider.Tool, msgs []Msg, format string) ReportJSON {
	sys := buildBucket("system", classifiedSystem(system))
	tl := buildBucket("tools", classifiedTools(tools))
	ms := buildBucket("messages", classifiedMessages(msgs))

	grand := sys.Bytes + tl.Bytes + ms.Bytes
	setBucketShare(&sys, grand)
	setBucketShare(&tl, grand)
	setBucketShare(&ms, grand)

	rep := ReportJSON{
		Format:           format,
		Estimate:         true,
		System:           sys,
		Tools:            tl,
		Messages:         ms,
		GrandTotalBytes:  grand,
		GrandTotalTokens: estTokens(grand),
	}
	rep.Hidden = summarizeHidden(rep, grand)
	return rep
}

// BuildProviderRequestReportJSON attributes a final canonical provider request
// after request-scoped mutations such as runtime context injection. It stores
// only labels and sizes in the returned report; prompt and tool-result content
// is used transiently for classification and is not retained.
func BuildProviderRequestReportJSON(req provider.ChatRequest) ReportJSON {
	sys := buildBucket("system", classifiedSystem(req.SystemPrompt))
	tl := buildBucket("tools", classifiedTools(req.Tools))
	ms := buildBucket("messages", classifiedProviderMessages(req.Messages))

	grand := sys.Bytes + tl.Bytes + ms.Bytes
	setBucketShare(&sys, grand)
	setBucketShare(&tl, grand)
	setBucketShare(&ms, grand)

	rep := ReportJSON{
		Format:           "canonical-final",
		Estimate:         true,
		System:           sys,
		Tools:            tl,
		Messages:         ms,
		GrandTotalBytes:  grand,
		GrandTotalTokens: estTokens(grand),
	}
	rep.Hidden = summarizeHidden(rep, grand)
	return rep
}

// FromRequestJSONFull parses a raw provider request body into both the text
// Report and the classified ReportJSON. It is the JSON-aware sibling of
// FromRequestJSON.
func FromRequestJSONFull(data []byte) (Report, ReportJSON, string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return Report{}, ReportJSON{}, "", err
	}
	format := detectFormat(top)
	system := parseSystem(top, format)
	tools := parseTools(top, format)
	msgs := parseMessages(top, format)

	rep := Report{
		System:   AnalyzeSystemPrompt(system),
		Tools:    ToolComponents(tools),
		Messages: MessageComponents(msgs),
	}
	return rep, BuildReportJSON(system, tools, msgs, format), format, nil
}

func setBucketShare(b *BucketJSON, grand int) {
	if grand > 0 {
		b.Pct = 100 * float64(b.Bytes) / float64(grand)
	}
}

func summarizeHidden(rep ReportJSON, grand int) HiddenSummaryJSON {
	var sum HiddenSummaryJSON
	seen := map[Kind]bool{}
	var order []string
	add := func(comps []ComponentJSON) {
		for _, c := range comps {
			if !c.Hidden {
				continue
			}
			sum.Count++
			sum.Bytes += c.Bytes
			sum.Tokens += c.Tokens
			if !seen[c.Kind] {
				seen[c.Kind] = true
				order = append(order, string(c.Kind))
			}
		}
	}
	add(rep.System.Components)
	add(rep.Tools.Components)
	add(rep.Messages.Components)
	sum.Sources = order
	if grand > 0 {
		sum.PctOfTotal = 100 * float64(sum.Bytes) / float64(grand)
	}
	return sum
}

// JSON serializes a ReportJSON, optionally indented.
func (r ReportJSON) JSON(pretty bool) ([]byte, error) {
	if pretty {
		return json.MarshalIndent(r, "", "  ")
	}
	return json.Marshal(r)
}

func sortComponentsDesc(c []ComponentJSON) {
	// simple insertion sort keeps it dependency-free and stable for small N
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j].Bytes > c[j-1].Bytes; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
