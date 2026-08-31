package prompttrace

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestNilCollectorNoOp(t *testing.T) {
	var c *Collector
	// Must not panic.
	c.Append("foo", "bar")
	c.AppendMsg("foo", "user", "bar", 0)
	if entries := c.Entries(); entries != nil {
		t.Fatalf("nil collector Entries() = %v, want nil", entries)
	}
	c.Reset()
}

func TestAppendCapturesCaller(t *testing.T) {
	c := NewCollector()
	c.Append("base_prompt", "hello world") // <-- this exact line is the captured source
	entries := c.Entries()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Label != "base_prompt" {
		t.Errorf("Label = %q, want base_prompt", e.Label)
	}
	if e.Chars != len("hello world") {
		t.Errorf("Chars = %d, want %d", e.Chars, len("hello world"))
	}
	if !strings.Contains(e.Source, "prompttrace_test.go:") {
		t.Errorf("Source = %q, want to contain prompttrace_test.go:LINE", e.Source)
	}
	if e.Kind != "section" {
		t.Errorf("Kind = %q, want section", e.Kind)
	}
}

func TestAppendMsgCapturesCaller(t *testing.T) {
	c := NewCollector()
	c.AppendMsg("user_msg", "user", "hi", 1)
	entries := c.Entries()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Kind != "message" {
		t.Errorf("Kind = %q, want message", e.Kind)
	}
	if e.Role != "user" {
		t.Errorf("Role = %q, want user", e.Role)
	}
	if e.MsgIndex != 1 {
		t.Errorf("MsgIndex = %d, want 1", e.MsgIndex)
	}
	if !strings.Contains(e.Source, "prompttrace_test.go:") {
		t.Errorf("Source = %q, want to contain prompttrace_test.go:LINE", e.Source)
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	if got := From(ctx); got != nil {
		t.Fatalf("From empty ctx = %v, want nil", got)
	}
	c := NewCollector()
	ctx = WithCollector(ctx, c)
	if got := From(ctx); got != c {
		t.Fatalf("From ctx = %v, want %v", got, c)
	}
}

func TestResetClears(t *testing.T) {
	c := NewCollector()
	c.Append("a", "x")
	c.Append("b", "yy")
	if len(c.Entries()) != 2 {
		t.Fatalf("pre-reset entries = %d, want 2", len(c.Entries()))
	}
	c.Reset()
	if len(c.Entries()) != 0 {
		t.Fatalf("post-reset entries = %d, want 0", len(c.Entries()))
	}
}

func TestWriteBannerEmpty(t *testing.T) {
	var buf bytes.Buffer
	WriteBanner(&buf, nil)
	out := buf.String()
	if !strings.Contains(out, "system prompt assembled from 0 sections") {
		t.Errorf("empty banner missing sections header: %q", out)
	}
	if !strings.Contains(out, "messages constructed from 0 sites") {
		t.Errorf("empty banner missing messages header: %q", out)
	}
}

func TestAppendToolRecorded(t *testing.T) {
	c := NewCollector()
	c.AppendTool("read_file", 512)
	entries := c.Entries()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Kind != "tool" {
		t.Errorf("Kind = %q, want tool", entries[0].Kind)
	}
	if entries[0].Chars != 512 {
		t.Errorf("Chars = %d, want 512", entries[0].Chars)
	}
}

func TestWriteBannerToolsTable(t *testing.T) {
	var buf bytes.Buffer
	entries := []Entry{
		{Kind: "section", Label: "base_prompt", Chars: 100, Source: "base.go:1"},
		{Kind: "tool", Label: "bash", Chars: 300, Source: "agent.go:50"},
		{Kind: "tool", Label: "read_file", Chars: 1200, Source: "agent.go:50"},
	}
	WriteBanner(&buf, entries)
	out := buf.String()
	for _, want := range []string{
		"tool schemas: 2 tools",
		"read_file",
		"bash",
		"1500", // tool TOTAL chars
	} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q\nfull output:\n%s", want, out)
		}
	}
	// Largest-first: read_file must appear before bash.
	if strings.Index(out, "read_file") > strings.Index(out, "bash") {
		t.Errorf("tools not sorted largest-first:\n%s", out)
	}
}

func TestWriteBannerTable(t *testing.T) {
	var buf bytes.Buffer
	entries := []Entry{
		{Kind: "section", Label: "base_prompt", Chars: 100, Source: "sdk/prompts/base.go:17"},
		{Kind: "section", Label: "available_skills", Chars: 1234, Source: "skills_loader.go:142"},
		{Kind: "message", Label: "user", Role: "user", Chars: 45, Source: "headless.go:88", MsgIndex: 1},
	}
	WriteBanner(&buf, entries)
	out := buf.String()
	for _, want := range []string{
		"system prompt assembled from 2 sections",
		"base_prompt",
		"available_skills",
		"sdk/prompts/base.go:17",
		"TOTAL",
		"1334",
		"messages constructed from 1 sites",
		"headless.go:88",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q\nfull output:\n%s", want, out)
		}
	}
}
