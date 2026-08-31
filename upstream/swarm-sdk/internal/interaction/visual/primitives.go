// Package visual defines the data-only primitive types used by the
// visual_choice question type. No rendering logic lives here — see
// swarm-tui/internal/visual for the HTML renderer.
package visual

import (
	"encoding/json"
	"fmt"
)

// VisualPrimitive is the tagged-union interface implemented by every primitive.
// Kind() returns the JSON discriminator used for deserialization.
type VisualPrimitive interface {
	Kind() string
}

// ─── Interactive primitives ─────────────────────────────────────────────────

// Options is a text-list picker. User selects one (or many if Multiselect).
type Options struct {
	Multiselect bool         `json:"multiselect,omitempty"`
	Items       []OptionItem `json:"items"`
}

// OptionItem is a single text option.
type OptionItem struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

func (Options) Kind() string { return "options" }

func (o Options) MarshalJSON() ([]byte, error) {
	type alias Options
	return json.Marshal(struct {
		Kind string `json:"kind"`
		alias
	}{Kind: o.Kind(), alias: alias(o)})
}

// Cards is a grid of rich-body cards; body may be any presentation primitive.
type Cards struct {
	Multiselect bool       `json:"multiselect,omitempty"`
	Items       []CardItem `json:"items"`
}

// CardItem is one card; Body is optional.
type CardItem struct {
	Key         string          `json:"key"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Body        VisualPrimitive `json:"body,omitempty"`
}

func (Cards) Kind() string { return "cards" }

func (c Cards) MarshalJSON() ([]byte, error) {
	type alias Cards
	return json.Marshal(struct {
		Kind string `json:"kind"`
		alias
	}{Kind: c.Kind(), alias: alias(c)})
}

// SplitCompare is a two-item side-by-side picker (A/B).
type SplitCompare struct {
	Items []SplitItem `json:"items"` // must be exactly 2
}

// SplitItem is one side of a split-compare.
type SplitItem struct {
	Key   string          `json:"key"`
	Label string          `json:"label"`
	Body  VisualPrimitive `json:"body,omitempty"`
}

func (SplitCompare) Kind() string { return "split" }

func (s SplitCompare) MarshalJSON() ([]byte, error) {
	type alias SplitCompare
	return json.Marshal(struct {
		Kind string `json:"kind"`
		alias
	}{Kind: s.Kind(), alias: alias(s)})
}

// ─── Presentation primitives ────────────────────────────────────────────────

// Markdown renders markdown text client-side via embedded marked.js.
type Markdown struct {
	Content string `json:"content"`
}

func (Markdown) Kind() string { return "markdown" }

func (m Markdown) MarshalJSON() ([]byte, error) {
	type alias Markdown
	return json.Marshal(struct {
		Kind string `json:"kind"`
		alias
	}{Kind: m.Kind(), alias: alias(m)})
}

// Mermaid renders a mermaid diagram client-side.
type Mermaid struct {
	Diagram string `json:"diagram"`
	Caption string `json:"caption,omitempty"`
}

func (Mermaid) Kind() string { return "mermaid" }

func (m Mermaid) MarshalJSON() ([]byte, error) {
	type alias Mermaid
	return json.Marshal(struct {
		Kind string `json:"kind"`
		alias
	}{Kind: m.Kind(), alias: alias(m)})
}

// RawHTML is the escape hatch. Content runs through a sanitizer at render time.
type RawHTML struct {
	Content string `json:"content"`
}

func (RawHTML) Kind() string { return "raw_html" }

func (r RawHTML) MarshalJSON() ([]byte, error) {
	type alias RawHTML
	return json.Marshal(struct {
		Kind string `json:"kind"`
		alias
	}{Kind: r.Kind(), alias: alias(r)})
}

// ─── Decoding ───────────────────────────────────────────────────────────────

// DecodePrimitive inspects the kind discriminator and returns the concrete type.
func DecodePrimitive(raw json.RawMessage) (VisualPrimitive, error) {
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("primitive missing 'kind': %w", err)
	}
	switch probe.Kind {
	case "options":
		var o Options
		if err := json.Unmarshal(raw, &o); err != nil {
			return nil, fmt.Errorf("decode options: %w", err)
		}
		return o, nil
	case "cards":
		return decodeCards(raw)
	case "split":
		return decodeSplit(raw)
	case "markdown":
		var m Markdown
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("decode markdown: %w", err)
		}
		return m, nil
	case "mermaid":
		var m Mermaid
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("decode mermaid: %w", err)
		}
		return m, nil
	case "raw_html":
		var r RawHTML
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("decode raw_html: %w", err)
		}
		return r, nil
	default:
		return nil, fmt.Errorf("unknown primitive kind: %q", probe.Kind)
	}
}

func decodeCards(raw json.RawMessage) (Cards, error) {
	var envelope struct {
		Multiselect bool `json:"multiselect"`
		Items       []struct {
			Key         string          `json:"key"`
			Title       string          `json:"title"`
			Description string          `json:"description"`
			Body        json.RawMessage `json:"body"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Cards{}, fmt.Errorf("decode cards: %w", err)
	}
	out := Cards{Multiselect: envelope.Multiselect, Items: make([]CardItem, 0, len(envelope.Items))}
	for i, it := range envelope.Items {
		card := CardItem{Key: it.Key, Title: it.Title, Description: it.Description}
		if len(it.Body) > 0 && string(it.Body) != "null" {
			body, err := DecodePrimitive(it.Body)
			if err != nil {
				return Cards{}, fmt.Errorf("card[%d].body: %w", i, err)
			}
			card.Body = body
		}
		out.Items = append(out.Items, card)
	}
	return out, nil
}

func decodeSplit(raw json.RawMessage) (SplitCompare, error) {
	var envelope struct {
		Items []struct {
			Key   string          `json:"key"`
			Label string          `json:"label"`
			Body  json.RawMessage `json:"body"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return SplitCompare{}, fmt.Errorf("decode split: %w", err)
	}
	if len(envelope.Items) != 2 {
		return SplitCompare{}, fmt.Errorf("split requires exactly 2 items, got %d", len(envelope.Items))
	}
	out := SplitCompare{Items: make([]SplitItem, 0, 2)}
	for i, it := range envelope.Items {
		side := SplitItem{Key: it.Key, Label: it.Label}
		if len(it.Body) > 0 && string(it.Body) != "null" {
			body, err := DecodePrimitive(it.Body)
			if err != nil {
				return SplitCompare{}, fmt.Errorf("split[%d].body: %w", i, err)
			}
			side.Body = body
		}
		out.Items = append(out.Items, side)
	}
	return out, nil
}
