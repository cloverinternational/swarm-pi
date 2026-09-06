package contextaudit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestBuildReportJSONClassifiesHiddenContext(t *testing.T) {
	system := strings.Join([]string{
		"You are a helpful agent.",
		`<context name="claudeMd">` + strings.Repeat("x", 100) + `</context>`,
		`<context name="swarmos_context_ephemeral">` + strings.Repeat("e", 80) + `</context>`,
		`<context name="swarmos_cached_context">` + strings.Repeat("c", 60) + `</context>`,
		`<available_skills>` + strings.Repeat("z", 200) + `</available_skills>`,
	}, "\n")

	msgs := []Msg{
		{Role: "user", Content: "what does this repo do?"},
		{Role: "user", Content: `<system-reminder source="task-maintenance-reminder-hook">[Task Nudge] track your work</system-reminder>`},
		{Role: "assistant", Content: "It builds agents."},
	}

	rep := BuildReportJSON(system, nil, msgs, "anthropic")

	// Grand total must equal the sum of the three buckets.
	if rep.GrandTotalBytes != rep.System.Bytes+rep.Tools.Bytes+rep.Messages.Bytes {
		t.Fatalf("grand total mismatch: %d != %d+%d+%d", rep.GrandTotalBytes, rep.System.Bytes, rep.Tools.Bytes, rep.Messages.Bytes)
	}

	kinds := map[string]Kind{}
	hiddenBytes := 0
	for _, c := range rep.System.Components {
		kinds[c.Label] = c.Kind
		if c.Hidden {
			hiddenBytes += c.Bytes
		}
	}
	if kinds["context:swarmos_context_ephemeral"] != KindEphemeral {
		t.Errorf("ephemeral block misclassified: %v", kinds["context:swarmos_context_ephemeral"])
	}
	if kinds["context:swarmos_cached_context"] != KindCached {
		t.Errorf("cached block misclassified: %v", kinds["context:swarmos_cached_context"])
	}
	if kinds["context:claudeMd"] != KindContext {
		t.Errorf("claudeMd misclassified: %v", kinds["context:claudeMd"])
	}

	// The task-nudge message must be flagged hidden.
	var foundNudge bool
	for _, c := range rep.Messages.Components {
		if c.Kind == KindTaskNudge {
			foundNudge = true
			if !c.Hidden || !c.Ephemeral {
				t.Errorf("task nudge should be hidden+ephemeral, got %+v", c)
			}
		}
	}
	if !foundNudge {
		t.Errorf("expected a task_nudge message component, got %+v", rep.Messages.Components)
	}

	// Hidden summary must account for the ephemeral + cached + nudge bytes.
	if rep.Hidden.Count < 3 {
		t.Errorf("hidden count = %d, want >= 3", rep.Hidden.Count)
	}
	if rep.Hidden.Bytes <= 0 || rep.Hidden.PctOfTotal <= 0 {
		t.Errorf("hidden summary not populated: %+v", rep.Hidden)
	}

	// JSON round-trips.
	b, err := rep.JSON(true)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var back ReportJSON
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.GrandTotalTokens != rep.GrandTotalTokens {
		t.Errorf("token round-trip mismatch: %d != %d", back.GrandTotalTokens, rep.GrandTotalTokens)
	}
}

func TestFromRequestJSONFull(t *testing.T) {
	raw := `{
		"system": "You are helpful.\n<context name=\"swarmos_context_ephemeral\">EPHEMERAL</context>",
		"tools": [{"name":"read","description":"read a file","input_schema":{"type":"object"}}],
		"messages": [{"role":"user","content":"hi"}]
	}`
	_, repJSON, format, err := FromRequestJSONFull([]byte(raw))
	if err != nil {
		t.Fatalf("FromRequestJSONFull: %v", err)
	}
	if format != "anthropic" {
		t.Errorf("format = %q, want anthropic", format)
	}
	if len(repJSON.Tools.Components) != 1 || repJSON.Tools.Components[0].Kind != KindTool {
		t.Errorf("tool bucket wrong: %+v", repJSON.Tools.Components)
	}
	if repJSON.Hidden.Count == 0 {
		t.Errorf("expected ephemeral system block to be counted hidden: %+v", repJSON.Hidden)
	}
}

func TestBuildProviderRequestReportJSONCountsCanonicalPayload(t *testing.T) {
	req := provider.ChatRequest{
		SystemPrompt: "base\n<context name=\"swarmos_context_ephemeral\">live</context>",
		Tools: []provider.Tool{{
			Name: "read", Description: "read a file",
			Parameters: map[string]any{"type": "object"},
		}},
		Messages: []*conversation.Message{
			{
				Role:     conversation.RoleUser,
				Content:  `<system-reminder source="task-maintenance-reminder-hook">task nudge</system-reminder>`,
				Thinking: strings.Repeat("t", 40),
			},
			{
				Role:    conversation.RoleTool,
				Content: "",
				ToolResults: []conversation.ToolResult{{
					Output: strings.Repeat("full-result-", 30),
				}},
			},
		},
	}

	rep := BuildProviderRequestReportJSON(req)
	if rep.Format != "canonical-final" || !rep.Estimate {
		t.Fatalf("unexpected report identity: format=%q estimate=%v", rep.Format, rep.Estimate)
	}
	if rep.GrandTotalBytes != rep.System.Bytes+rep.Tools.Bytes+rep.Messages.Bytes {
		t.Fatalf("grand total mismatch: %+v", rep)
	}
	if len(rep.Messages.Components) != 2 {
		t.Fatalf("message components = %d, want 2", len(rep.Messages.Components))
	}
	if rep.Messages.Components[0].Kind != KindTaskNudge || !rep.Messages.Components[0].Hidden {
		t.Fatalf("reminder classification = %+v", rep.Messages.Components[0])
	}
	if rep.Messages.Components[1].Bytes <= len(req.Messages[1].Content) {
		t.Fatalf("tool result payload was not counted: %+v", rep.Messages.Components[1])
	}
}

func TestBuildProviderRequestReportJSONIgnoresPersistenceFields(t *testing.T) {
	base := &conversation.Message{Role: conversation.RoleUser, Content: "same provider payload"}
	withPersistence := &conversation.Message{
		ID:        "persisted-id",
		Timestamp: time.Unix(123, 0),
		Role:      conversation.RoleUser,
		Content:   "same provider payload",
		Tokens:    &conversation.TokenUsage{Input: 9999, Output: 8888},
		PromptIDs: []string{"prompt-1", "prompt-2"},
	}

	a := BuildProviderRequestReportJSON(provider.ChatRequest{Messages: []*conversation.Message{base}})
	b := BuildProviderRequestReportJSON(provider.ChatRequest{Messages: []*conversation.Message{withPersistence}})
	if a.Messages.Bytes != b.Messages.Bytes {
		t.Fatalf("persistence fields changed provider estimate: %d != %d", a.Messages.Bytes, b.Messages.Bytes)
	}
}

func TestBuildProviderRequestReportJSONSplitsCodexRuntimeContext(t *testing.T) {
	content := "<swarm_runtime_guidance>\n<swarm_runtime_context>\n" +
		"<cached_context>stable data</cached_context>\n" +
		"<ephemeral_context><system-reminder>live data</system-reminder></ephemeral_context>\n" +
		"</swarm_runtime_context>\n</swarm_runtime_guidance>\n\nordinary question"
	rep := BuildProviderRequestReportJSON(provider.ChatRequest{Messages: []*conversation.Message{{
		Role: conversation.RoleUser, Content: content,
	}}})

	var base, cached, ephemeral *ComponentJSON
	for i := range rep.Messages.Components {
		component := &rep.Messages.Components[i]
		switch component.Kind {
		case KindCached:
			cached = component
		case KindEphemeral:
			ephemeral = component
		case KindMessage:
			base = component
		}
	}
	if base == nil || base.Hidden || base.Kind != KindMessage {
		t.Fatalf("ordinary user content was hidden: %+v", rep.Messages.Components)
	}
	if cached == nil || !cached.Hidden || cached.Ephemeral {
		t.Fatalf("cached runtime context classification = %+v", cached)
	}
	if ephemeral == nil || !ephemeral.Hidden || !ephemeral.Ephemeral {
		t.Fatalf("ephemeral runtime context classification = %+v", ephemeral)
	}
	componentBytes := 0
	for _, component := range rep.Messages.Components {
		componentBytes += component.Bytes
	}
	if componentBytes != rep.Messages.Bytes {
		t.Fatalf("split message bytes = %d, bucket bytes = %d", componentBytes, rep.Messages.Bytes)
	}
}

func TestBuildProviderRequestReportJSONConservativelyHidesAmbiguousGuidance(t *testing.T) {
	content := "<swarm_runtime_guidance>\n" +
		"<cached_context>arbitrary </cached_context> literal </cached_context>\n" +
		"embedded </swarm_runtime_guidance> literal\n" +
		"</swarm_runtime_guidance>\n\nordinary-looking suffix"
	rep := BuildProviderRequestReportJSON(provider.ChatRequest{Messages: []*conversation.Message{{
		Role: conversation.RoleUser, Content: content,
	}}})

	if len(rep.Messages.Components) == 0 {
		t.Fatal("expected conservative hidden component")
	}
	componentBytes := 0
	for _, component := range rep.Messages.Components {
		componentBytes += component.Bytes
		if !component.Hidden {
			t.Fatalf("ambiguous guidance leaked into visible attribution: %+v", rep.Messages.Components)
		}
	}
	if componentBytes != rep.Messages.Bytes || rep.Hidden.Bytes != rep.Messages.Bytes {
		t.Fatalf("ambiguous bytes did not reconcile: components=%d messages=%d hidden=%d",
			componentBytes, rep.Messages.Bytes, rep.Hidden.Bytes)
	}
}
