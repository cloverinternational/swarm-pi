package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestAnnoyedToolPublishesRedactedConversation(t *testing.T) {
	const (
		conversationID = "conv-123"
		secret         = "ghp_abcdefghijklmnopqrstuvwxyz123456"
	)
	conv := &conversation.Conversation{
		ID:               conversationID,
		Title:            "Tool trouble",
		CreatedAt:        time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC),
		UpdatedAt:        time.Date(2026, 7, 29, 10, 1, 0, 0, time.UTC),
		BaseSystemPrompt: "must-not-publish-system-prompt",
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "please run it"},
			{
				Role:     conversation.RoleAssistant,
				Content:  "The tool defaulted unexpectedly.",
				Thinking: "must-not-publish-hidden-thinking",
				ToolCalls: []conversation.ToolCall{{
					ID: "call-1", Name: "vault_exec",
					Parameters: map[string]any{"credential": secret, "command": "gh"},
				}},
			},
			{
				Role: conversation.RoleTool,
				ToolResults: []conversation.ToolResult{{
					CallID: "call-1", Name: "vault_exec", Output: "failed with " + secret,
					Error: &conversation.ToolError{Type: "tool.failure", Message: "no output"},
				}},
			},
		},
	}

	var gotRepository, gotTitle, gotBody string
	tool := &AnnoyedTool{config: AnnoyedConfig{
		LoadConversation: func(_ context.Context, id string) (*conversation.Conversation, error) {
			if id != conversationID {
				t.Fatalf("loaded conversation %q, want %q", id, conversationID)
			}
			return conv, nil
		},
		PublishFeedback: func(_ context.Context, publication FeedbackPublication) (string, error) {
			gotRepository, gotTitle = publication.Repository, publication.Title
			gotBody = publication.Body
			return "https://github.com/Swarm-Code/mono/issues/42", nil
		},
	}}
	ctx := tools.WithOwnerInfo(context.Background(), "agent-7", "user", conversationID)
	ctx = tools.WithToolCallID(ctx, "annoyed-call")
	ctx = tools.WithWorkspacePath(ctx, "/home/alice/private-project")

	result, err := tool.Run(ctx, AnnoyedParams{
		Issue:    "Vault tool forced a workaround",
		Severity: "HIGH",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotRepository != defaultAnnoyedRepository {
		t.Fatalf("repository = %q", gotRepository)
	}
	if gotTitle != "[HIGH] Vault tool forced a workaround" {
		t.Fatalf("title = %q", gotTitle)
	}
	for _, want := range []string{
		"## Agent complaint", "Vault tool forced a workaround",
		"Conversation: " + conversationID, "Agent: agent-7", "Tool call: annoyed-call",
		"### user", "please run it", "### assistant", "defaulted unexpectedly",
		"Tool call `vault_exec`", "Tool result `vault_exec`", `"command": "gh"`,
		"Current annoyed invocation",
	} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("issue body missing %q:\n%s", want, gotBody)
		}
	}
	for _, forbidden := range []string{
		secret, "must-not-publish-system-prompt", "must-not-publish-hidden-thinking", "/home/alice",
	} {
		if strings.Contains(gotBody, forbidden) {
			t.Errorf("issue body leaked %q", forbidden)
		}
	}
	if !strings.Contains(gotBody, "[REDACTED") {
		t.Fatalf("issue body did not report redaction:\n%s", gotBody)
	}
	if result.Metadata["issue_url"] != "https://github.com/Swarm-Code/mono/issues/42" {
		t.Fatalf("issue_url metadata = %#v", result.Metadata["issue_url"])
	}
	if result.Metadata["severity"] != "high" {
		t.Fatalf("severity metadata = %#v", result.Metadata["severity"])
	}
	if result.Metadata["history_available"] != true {
		t.Fatalf("history_available = %#v", result.Metadata["history_available"])
	}
}

func TestAnnoyedToolUsesRepositoryOverrideAndFilesWithoutHistory(t *testing.T) {
	t.Setenv("SWARM_ANNOYED_REPOSITORY", "example/feedback")
	var body string
	tool := &AnnoyedTool{config: AnnoyedConfig{
		LoadConversation: func(context.Context, string) (*conversation.Conversation, error) {
			return nil, errors.New("storage offline")
		},
		PublishFeedback: func(_ context.Context, publication FeedbackPublication) (string, error) {
			if publication.Repository != "example/feedback" {
				t.Fatalf("repository = %q", publication.Repository)
			}
			body = publication.Body
			return "https://github.com/example/feedback/issues/1", nil
		},
	}}
	ctx := tools.WithOwnerInfo(context.Background(), "agent", "user", "conv")
	result, err := tool.Run(ctx, AnnoyedParams{Issue: "history loading is annoying"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(body, "History unavailable: storage offline") {
		t.Fatalf("missing history error: %s", body)
	}
	if result.Metadata["history_available"] != false {
		t.Fatalf("history_available = %#v", result.Metadata["history_available"])
	}
}

func TestAnnoyedToolValidatesInputAndRepository(t *testing.T) {
	reporter := func(context.Context, FeedbackPublication) (string, error) {
		t.Fatal("reporter should not run")
		return "", nil
	}
	for _, test := range []struct {
		name   string
		config AnnoyedConfig
		params AnnoyedParams
	}{
		{name: "blank issue", config: AnnoyedConfig{PublishFeedback: reporter}, params: AnnoyedParams{Issue: " \n "}},
		{name: "bad repository", config: AnnoyedConfig{Repository: "../wrong", PublishFeedback: reporter}, params: AnnoyedParams{Issue: "bad config"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tool := &AnnoyedTool{config: test.config}
			if _, err := tool.Run(context.Background(), test.params); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestAnnoyedToolPropagatesReporterFailure(t *testing.T) {
	tool := &AnnoyedTool{config: AnnoyedConfig{
		PublishFeedback: func(context.Context, FeedbackPublication) (string, error) {
			return "", errors.New("github unavailable")
		},
	}}
	if _, err := tool.Run(context.Background(), AnnoyedParams{Issue: "cannot publish"}); err == nil ||
		!strings.Contains(err.Error(), "github unavailable") {
		t.Fatalf("Run error = %v", err)
	}
}

func TestBuildAnnoyedIssueBodyTruncatesUTF8WithHeadAndTail(t *testing.T) {
	conv := &conversation.Conversation{Messages: []*conversation.Message{
		{Role: conversation.RoleUser, Content: "opening-context " + strings.Repeat("é", 500)},
		{Role: conversation.RoleAssistant, Content: strings.Repeat("界", 500) + " latest-trigger"},
	}}
	body, provenance := buildAnnoyedIssueBody(
		context.Background(), conv, nil, "complaint", "medium", 900,
	)
	if !provenance.Truncated {
		t.Fatal("expected truncation")
	}
	if len(body) > 900 {
		t.Fatalf("body length = %d", len(body))
	}
	if !utf8.ValidString(body) {
		t.Fatal("body is not valid UTF-8")
	}
	for _, want := range []string{"complaint", "opening-context", "transcript truncated", "latest-trigger"} {
		if !strings.Contains(body, want) {
			t.Errorf("truncated body missing %q", want)
		}
	}
}

func TestBuildAnnoyedIssueBodyBoundsSingleHugeToolRecord(t *testing.T) {
	parameters := make(map[string]any, 10_000)
	for index := 0; index < 10_000; index++ {
		parameters[fmt.Sprintf("key-%05d", index)] = strings.Repeat("x", 1_000)
	}
	conv := &conversation.Conversation{Messages: []*conversation.Message{{
		Role: conversation.RoleAssistant,
		ToolCalls: []conversation.ToolCall{{
			Name:       "huge_tool",
			Parameters: parameters,
		}},
		ToolResults: []conversation.ToolResult{{
			Name:   "huge_tool",
			Output: strings.Repeat("y", 1_000_000) + "latest-result",
		}},
	}}}
	body, provenance := buildAnnoyedIssueBody(
		context.Background(), conv, nil, "large output complaint", "high", 2_000,
	)
	if !provenance.Truncated {
		t.Fatal("expected bounded transcript provenance")
	}
	if len(body) > 2_000 {
		t.Fatalf("body length = %d", len(body))
	}
	if !strings.Contains(body, "large output complaint") {
		t.Fatal("complaint header was lost")
	}
}

func TestBuildAnnoyedIssueBodyBoundsTypedAggregatesAndManyRecords(t *testing.T) {
	const secret = "ghp_abcdefghijklmnopqrstuvwxyz123456"
	type typedParameters struct {
		Token  string            `json:"token"`
		Values map[string]string `json:"values"`
	}
	values := make(map[string]string, 10_000)
	for index := 0; index < 10_000; index++ {
		values[fmt.Sprintf("key-%05d", index)] = strings.Repeat("value", 100)
	}
	calls := make([]conversation.ToolCall, 10_000)
	for index := range calls {
		calls[index] = conversation.ToolCall{
			Name: "typed_tool",
			Parameters: map[string]any{
				"typed": typedParameters{Token: secret, Values: values},
			},
		}
	}
	conv := &conversation.Conversation{Messages: []*conversation.Message{{
		Role:      conversation.RoleAssistant,
		ToolCalls: calls,
	}}}
	body, provenance := buildAnnoyedIssueBody(
		context.Background(), conv, nil, "typed aggregate complaint", "high", 2_000,
	)
	if !provenance.Truncated {
		t.Fatal("expected bounded transcript provenance")
	}
	if len(body) > 2_000 {
		t.Fatalf("body length = %d", len(body))
	}
	if strings.Contains(body, secret) || !strings.Contains(body, "[REDACTED") {
		t.Fatalf("typed secret was not redacted:\n%s", body)
	}
}

func TestAnnoyedDescriptionExplainsSimpleIssuePublication(t *testing.T) {
	description := (&AnnoyedTool{}).Description()
	for _, phrase := range []string{"GitHub issue", "actionable friction", "redacted", "size-bounded transcript"} {
		if !strings.Contains(description, phrase) {
			t.Errorf("description missing %q", phrase)
		}
	}
}

func TestAnnoyedFormatsOCDEngineeringBrief(t *testing.T) {
	params := AnnoyedParams{
		Issue: "sleep blocker rejects bounded commands", Severity: "high",
		Category:        "hook_false_positive",
		Observed:        "`timeout 300 python3 script.py` was classified as a bare sleep",
		Expected:        "Bounded commands execute; only idle sleep is blocked",
		Evidence:        []string{"tool=Bash", "error_type=tool.blocked_by_hook", "exact command uses timeout as a bound"},
		AcceptanceTests: []string{"allow `timeout 300 python3 script.py`", "block `sleep 300`"},
	}
	got := formatAnnoyedIssue(params)
	for _, want := range []string{
		"sleep blocker rejects bounded commands",
		"## Category\nhook_false_positive",
		"## Observed\n`timeout 300",
		"## Expected\nBounded commands execute",
		"## Evidence\n- tool=Bash",
		"## Acceptance tests\n- allow",
		"<!-- annoyance-fingerprint:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("structured report missing %q:\n%s", want, got)
		}
	}
	if len(annoyedFingerprint(params)) != 16 {
		t.Fatalf("want short stable hex, got %q", annoyedFingerprint(params))
	}
	// The fingerprint keys on the normalized defect class and mechanism, so a
	// re-report of the same defect must collapse onto the same fingerprint even
	// when the volatile transcript details around it differ.
	restated := params
	restated.Severity = "low"
	restated.Expected = "Only genuinely idle sleeps are blocked"
	restated.Evidence = []string{"tool=Bash", "error_id=9f1c-volatile", "second transcript copy"}
	restated.AcceptanceTests = nil
	if got, want := annoyedFingerprint(restated), annoyedFingerprint(params); got != want {
		t.Fatalf("fingerprint is not stable across volatile transcript detail: %q != %q", got, want)
	}
}

func TestAnnoyedBriefBoundsListsAndNormalizesCategory(t *testing.T) {
	var evidence []string
	for i := 0; i < maxAnnoyedEvidenceItems+3; i++ {
		evidence = append(evidence, fmt.Sprintf("evidence-%d", i))
	}
	got := formatAnnoyedIssue(AnnoyedParams{Issue: "x", Category: "NOT_A_CATEGORY", Evidence: evidence})
	if strings.Contains(got, "## Category") {
		t.Fatalf("invalid category should be omitted:\n%s", got)
	}
	if strings.Contains(got, fmt.Sprintf("evidence-%d", maxAnnoyedEvidenceItems)) {
		t.Fatalf("evidence list not bounded:\n%s", got)
	}
	if count := strings.Count(got, "\n- evidence-"); count != maxAnnoyedEvidenceItems {
		t.Fatalf("want %d evidence rows, got %d", maxAnnoyedEvidenceItems, count)
	}
}

func TestAnnoyedToolUsesRedactedComplaintTitle(t *testing.T) {
	const secret = "ghp_abcdefghijklmnopqrstuvwxyz123456"
	var title string
	tool := &AnnoyedTool{config: AnnoyedConfig{
		PublishFeedback: func(_ context.Context, publication FeedbackPublication) (string, error) {
			title = publication.Title
			return "https://github.com/Swarm-Code/mono/issues/100", nil
		},
	}}
	if _, err := tool.Run(context.Background(), AnnoyedParams{Issue: "leaked token " + secret}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(title, secret) || !strings.Contains(title, "[REDACTED") {
		t.Fatalf("title was not redacted: %q", title)
	}
}

func TestAnnoyedToolStopsBeforePublicationWhenCanceled(t *testing.T) {
	called := false
	tool := &AnnoyedTool{config: AnnoyedConfig{
		LoadConversation: func(context.Context, string) (*conversation.Conversation, error) {
			return &conversation.Conversation{Messages: []*conversation.Message{
				{Role: conversation.RoleUser, Content: "history"},
			}}, nil
		},
		PublishFeedback: func(context.Context, FeedbackPublication) (string, error) {
			called = true
			return "", nil
		},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = tools.WithOwnerInfo(ctx, "agent", "user", "conv")
	cancel()
	if _, err := tool.Run(ctx, AnnoyedParams{Issue: "cancel me"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v", err)
	}
	if called {
		t.Fatal("reporter ran after cancellation")
	}
}
