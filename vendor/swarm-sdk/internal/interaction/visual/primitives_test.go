package visual

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOptionsRoundTrip(t *testing.T) {
	p := Options{
		Multiselect: true,
		Items: []OptionItem{
			{Key: "a", Label: "A", Description: "the a"},
			{Key: "b", Label: "B", Description: "the b"},
		},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"kind":"options"`) {
		t.Errorf("missing kind tag: %s", b)
	}
	dec, err := DecodePrimitive(b)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := dec.(Options)
	if !ok {
		t.Fatalf("decoded %T, want Options", dec)
	}
	if len(got.Items) != 2 || got.Items[0].Key != "a" {
		t.Errorf("items wrong: %+v", got.Items)
	}
}

func TestCardsWithNestedMermaid(t *testing.T) {
	p := Cards{
		Items: []CardItem{
			{Key: "x", Title: "X", Body: Mermaid{Diagram: "flowchart TD\nA-->B"}},
		},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"kind":"cards"`) {
		t.Errorf("missing kind: %s", b)
	}
	if !strings.Contains(string(b), `"kind":"mermaid"`) {
		t.Errorf("nested body kind missing: %s", b)
	}

	dec, err := DecodePrimitive(b)
	if err != nil {
		t.Fatal(err)
	}
	got := dec.(Cards)
	if len(got.Items) != 1 {
		t.Fatalf("items: %+v", got.Items)
	}
	if _, ok := got.Items[0].Body.(Mermaid); !ok {
		t.Errorf("body decoded as %T, want Mermaid", got.Items[0].Body)
	}
}

func TestDecodePrimitive_UnknownKind(t *testing.T) {
	if _, err := DecodePrimitive(json.RawMessage(`{"kind":"bogus"}`)); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestDecodePrimitive_SplitRejectsWrongCount(t *testing.T) {
	raw := json.RawMessage(`{"kind":"split","items":[{"key":"only","label":"only"}]}`)
	if _, err := DecodePrimitive(raw); err == nil {
		t.Fatal("split must require 2 items")
	}
}

func TestMarkdownRoundTrip(t *testing.T) {
	p := Markdown{Content: "# Hello\n\n*world*"}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"kind":"markdown"`) {
		t.Errorf("missing kind: %s", b)
	}
	dec, err := DecodePrimitive(b)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := dec.(Markdown)
	if !ok {
		t.Fatalf("decoded %T, want Markdown", dec)
	}
	if got.Content != p.Content {
		t.Errorf("content = %q, want %q", got.Content, p.Content)
	}
}

func TestRawHTMLRoundTrip(t *testing.T) {
	p := RawHTML{Content: `<div class="x">hi</div>`}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"kind":"raw_html"`) {
		t.Errorf("missing kind: %s", b)
	}
	dec, err := DecodePrimitive(b)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := dec.(RawHTML)
	if !ok {
		t.Fatalf("decoded %T, want RawHTML", dec)
	}
	if got.Content != p.Content {
		t.Errorf("content mismatch")
	}
}

func TestSplitCompareRoundTrip(t *testing.T) {
	p := SplitCompare{
		Items: []SplitItem{
			{Key: "a", Label: "A", Body: Markdown{Content: "left"}},
			{Key: "b", Label: "B", Body: Markdown{Content: "right"}},
		},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"kind":"split"`) {
		t.Errorf("missing split kind: %s", b)
	}
	dec, err := DecodePrimitive(b)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := dec.(SplitCompare)
	if !ok {
		t.Fatalf("decoded %T, want SplitCompare", dec)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items: %d", len(got.Items))
	}
	md, ok := got.Items[0].Body.(Markdown)
	if !ok {
		t.Errorf("body[0] = %T, want Markdown", got.Items[0].Body)
	}
	if md.Content != "left" {
		t.Errorf("body[0].content = %q", md.Content)
	}
}

func TestOptionsSingleSelect(t *testing.T) {
	// Multiselect=false is the zero-value; verify omitempty behavior doesn't drop
	// data and that round-trip preserves the flag.
	p := Options{
		Items: []OptionItem{{Key: "only", Label: "Only"}},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"multiselect"`) {
		t.Errorf("multiselect should be omitted when false, got: %s", b)
	}
	dec, err := DecodePrimitive(b)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := dec.(Options)
	if !ok {
		t.Fatalf("decoded %T", dec)
	}
	if got.Multiselect {
		t.Error("multiselect should decode as false")
	}
}
