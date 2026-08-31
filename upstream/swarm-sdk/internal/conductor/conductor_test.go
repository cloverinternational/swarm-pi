package conductor_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conductor"
)

// ── Workflow parsing ──────────────────────────────────────────────────────────

func TestParseWorkflow_FullFrontMatter(t *testing.T) {
	src := `---
tracker:
  kind: forgejo
  endpoint: https://scm.example.com
  api_key: tok
  repo: Org/repo
polling:
  interval_ms: 5000
agent:
  max_concurrent_agents: 3
  max_turns: 10
---
You are working on {{.Issue.Identifier}}: {{.Issue.Title}}
`
	wf, err := conductor.ParseWorkflowForTest([]byte(src), "/tmp/WORKFLOW.md")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if wf.Config.Tracker.Kind != "forgejo" {
		t.Errorf("tracker.kind = %q, want forgejo", wf.Config.Tracker.Kind)
	}
	if wf.Config.Tracker.Endpoint != "https://scm.example.com" {
		t.Errorf("tracker.endpoint = %q", wf.Config.Tracker.Endpoint)
	}
	if wf.Config.Polling.IntervalMs != 5000 {
		t.Errorf("polling.interval_ms = %d, want 5000", wf.Config.Polling.IntervalMs)
	}
	if wf.Config.Agent.MaxConcurrentAgents != 3 {
		t.Errorf("max_concurrent_agents = %d, want 3", wf.Config.Agent.MaxConcurrentAgents)
	}
	if !strings.Contains(wf.PromptTemplate, "{{.Issue.Identifier}}") {
		t.Errorf("prompt template missing variable, got: %q", wf.PromptTemplate)
	}
}

func TestParseWorkflow_Defaults(t *testing.T) {
	src := "---\ntracker:\n  kind: github\n---\nHello\n"
	wf, err := conductor.ParseWorkflowForTest([]byte(src), "/tmp/WORKFLOW.md")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if wf.Config.Polling.IntervalMs != 30_000 {
		t.Errorf("default polling.interval_ms = %d, want 30000", wf.Config.Polling.IntervalMs)
	}
	if wf.Config.Agent.MaxConcurrentAgents != 10 {
		t.Errorf("default max_concurrent_agents = %d, want 10", wf.Config.Agent.MaxConcurrentAgents)
	}
	if wf.Config.Agent.MaxTurns != 20 {
		t.Errorf("default max_turns = %d, want 20", wf.Config.Agent.MaxTurns)
	}
	if wf.Config.Hooks.TimeoutMs != 60_000 {
		t.Errorf("default hooks.timeout_ms = %d, want 60000", wf.Config.Hooks.TimeoutMs)
	}
}

func TestParseWorkflow_NoFrontMatter(t *testing.T) {
	src := "Just a plain markdown body."
	wf, err := conductor.ParseWorkflowForTest([]byte(src), "/tmp/WORKFLOW.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf.PromptTemplate != src {
		t.Errorf("body = %q, want %q", wf.PromptTemplate, src)
	}
}

func TestParseWorkflow_MissingClosingDelimiter(t *testing.T) {
	src := "---\ntracker:\n  kind: github\n"
	_, err := conductor.ParseWorkflowForTest([]byte(src), "/tmp/WORKFLOW.md")
	if err == nil {
		t.Fatal("expected error for missing closing ---")
	}
}

// ── Prompt template rendering ─────────────────────────────────────────────────

func TestBuildPrompt_Interpolation(t *testing.T) {
	src := "---\ntracker:\n  kind: github\n---\nIssue {{.Issue.Identifier}}: {{.Issue.Title}}"
	wf, _ := conductor.ParseWorkflowForTest([]byte(src), "/tmp/WORKFLOW.md")
	issue := conductor.Issue{Identifier: "#42", Title: "Fix the thing"}
	prompt, err := wf.BuildPrompt(issue, nil)
	if err != nil {
		t.Fatalf("build prompt: %v", err)
	}
	if prompt != "Issue #42: Fix the thing" {
		t.Errorf("prompt = %q", prompt)
	}
}

func TestBuildPrompt_DefaultWhenEmpty(t *testing.T) {
	wf := &conductor.WorkflowDef{}
	issue := conductor.Issue{Identifier: "#7", Title: "Empty test"}
	prompt, err := wf.BuildPrompt(issue, nil)
	if err != nil {
		t.Fatalf("build prompt: %v", err)
	}
	if !strings.Contains(prompt, "#7") {
		t.Errorf("default prompt missing identifier, got: %q", prompt)
	}
}

func TestBuildPrompt_WithAttempt(t *testing.T) {
	src := "---\ntracker:\n  kind: github\n---\n{{if .Attempt}}retry {{.Attempt}}{{else}}first{{end}}"
	wf, _ := conductor.ParseWorkflowForTest([]byte(src), "/tmp/WORKFLOW.md")
	issue := conductor.Issue{Identifier: "#1"}
	// First run: no attempt
	p0, _ := wf.BuildPrompt(issue, nil)
	if p0 != "first" {
		t.Errorf("first run prompt = %q, want 'first'", p0)
	}
	// Retry: attempt = 2
	n := 2
	p2, _ := wf.BuildPrompt(issue, &n)
	if p2 != "retry 2" {
		t.Errorf("retry prompt = %q, want 'retry 2'", p2)
	}
}

// ── Dispatch ordering ─────────────────────────────────────────────────────────

func TestSortForDispatch_PriorityThenAge(t *testing.T) {
	now := time.Now()
	issues := []conductor.Issue{
		{ID: "3", Priority: 0, CreatedAt: now},                     // unset → last
		{ID: "1", Priority: 2, CreatedAt: now.Add(-2 * time.Hour)}, // prio 2, older
		{ID: "2", Priority: 1, CreatedAt: now.Add(-1 * time.Hour)}, // prio 1, newer
		{ID: "4", Priority: 2, CreatedAt: now.Add(-3 * time.Hour)}, // prio 2, oldest
	}
	sorted := conductor.SortForDispatchForTest(issues)
	// Expected order: prio-1 first, then prio-2 oldest, then prio-2 newer, then unset.
	wantOrder := []string{"2", "4", "1", "3"}
	for i, want := range wantOrder {
		if sorted[i].ID != want {
			t.Errorf("sorted[%d].ID = %s, want %s", i, sorted[i].ID, want)
		}
	}
}

// ── Retry backoff ─────────────────────────────────────────────────────────────

func TestBackoffDelay_Progression(t *testing.T) {
	wf := &conductor.WorkflowDef{}
	wf.Config.Agent.MaxRetryBackoffMs = 300_000
	delays := conductor.BackoffDelaysForTest(wf, 6)

	// Attempt 1 is always 1 second (clean continuation).
	if delays[0] != time.Second {
		t.Errorf("attempt 1 delay = %v, want 1s", delays[0])
	}
	// Subsequent delays must grow.
	for i := 1; i < len(delays); i++ {
		if delays[i] < delays[i-1] {
			t.Errorf("delays[%d]=%v < delays[%d]=%v: backoff not monotonic", i, delays[i], i-1, delays[i-1])
		}
	}
	// Must never exceed cap.
	cap := 300 * time.Second
	for i, d := range delays {
		if d > cap {
			t.Errorf("delays[%d]=%v exceeds cap %v", i, d, cap)
		}
	}
}

// ── Label constants ───────────────────────────────────────────────────────────

func TestLabelSets_Disjoint(t *testing.T) {
	terminal := map[string]bool{}
	for _, l := range conductor.DefaultTerminalLabels {
		terminal[l] = true
	}
	for _, l := range conductor.DefaultActiveLabels {
		if terminal[l] {
			t.Errorf("label %q is in both active and terminal sets", l)
		}
	}
}

func TestLabelSets_NonEmpty(t *testing.T) {
	if len(conductor.DefaultActiveLabels) == 0 {
		t.Error("DefaultActiveLabels is empty")
	}
	if len(conductor.DefaultTerminalLabels) == 0 {
		t.Error("DefaultTerminalLabels is empty")
	}
}

// ── Constructor validation ────────────────────────────────────────────────────

func TestConductorNew_MissingWorkflow(t *testing.T) {
	_, err := conductor.New()
	if err == nil || !strings.Contains(err.Error(), "workflow") {
		t.Fatalf("expected workflow error, got: %v", err)
	}
}

func TestConductorNew_MissingClient(t *testing.T) {
	wf := &conductor.WorkflowDef{}
	wf.Config.Tracker = conductor.TrackerConfig{Kind: "github", APIKey: "tok", Repo: "o/r"}
	_, err := conductor.New(conductor.WithWorkflowDef(wf))
	if err == nil || !strings.Contains(err.Error(), "client") {
		t.Fatalf("expected client error, got: %v", err)
	}
}
