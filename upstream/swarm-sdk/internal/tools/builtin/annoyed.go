package builtin

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

const defaultAnnoyedRepository = "Swarm-Code/mono"
const maxAnnoyedIssueChars = 4000

// AnnoyedParams are the typed parameters for the annoyed tool.
type AnnoyedParams struct {
	Issue           string   `json:"issue" description:"A brief description of the issue, bug, inefficiency, or frustration" required:"true"`
	Severity        string   `json:"severity,omitempty" description:"Severity: low, medium, or high"`
	Category        string   `json:"category,omitempty" description:"Defect class: hook_false_positive, tool_failure, inefficiency, misleading_error, missing_capability, or other"`
	Observed        string   `json:"observed,omitempty" description:"What concretely happened; state the mechanism, not a vibe"`
	Expected        string   `json:"expected,omitempty" description:"What should have happened instead"`
	Evidence        []string `json:"evidence,omitempty" description:"Bounded, non-secret evidence such as the tool, error class, and reproduction shape"`
	AcceptanceTests []string `json:"acceptance_tests,omitempty" description:"Objective tests that a fix must pass"`
}

// ConversationLoader loads one exact conversation by ID.
type ConversationLoader func(context.Context, string) (*conversation.Conversation, error)

// FeedbackPublication is the sanitized feedback issue to publish.
type FeedbackPublication struct {
	Repository string
	Title      string
	Body       string
	// Deprecated: feedback is published directly in Body.
	ArtifactPath string
	// Deprecated: feedback is published directly in Body.
	Ciphertext []byte
}

// FeedbackPublisher publishes sanitized feedback and returns its issue URL.
type FeedbackPublisher func(ctx context.Context, publication FeedbackPublication) (string, error)

// AnnoyedConfig configures issue publication. Repository defaults to
// Swarm-Code/mono and can be overridden by SWARM_ANNOYED_REPOSITORY.
type AnnoyedConfig struct {
	Repository       string
	LoadConversation ConversationLoader
	PublishFeedback  FeedbackPublisher
	// Deprecated: feedback is no longer encrypted.
	Recipients   []string
	MaxBodyChars int
}

// AnnoyedTool lets agents publish feedback and frustrations as GitHub issues.
type AnnoyedTool struct {
	tools.BaseTool
	config AnnoyedConfig
}

// NewAnnoyedTool creates a new AnnoyedTool wrapped as a typed Tool.
func NewAnnoyedTool() tools.Tool {
	return NewAnnoyedToolWithConfig(AnnoyedConfig{})
}

// NewAnnoyedToolWithConfig creates a configured annoyed tool.
func NewAnnoyedToolWithConfig(config AnnoyedConfig) tools.Tool {
	if config.PublishFeedback == nil {
		config.PublishFeedback = reportGitHubFeedbackIssue
	}
	return tools.Typed[AnnoyedParams](&AnnoyedTool{config: config})
}

// Name returns the tool name.
func (t *AnnoyedTool) Name() string { return "annoyed" }

// Description returns the tool description.
func (t *AnnoyedTool) Description() string {
	return `Publish a GitHub issue when a tool, hook, default, constraint, error message, or execution experience has a real product defect.
Use it once for actionable friction; do not report successful operations merely because their output mentions words such as fallback, unavailable, or retry.
Skip invalid agent input, expected test failures, permission denials, and user cancellations.
	Be a demanding harness editor, not a complaint box: identify the mechanism, distinguish observed from expected behavior, provide bounded evidence, and include objective acceptance tests.
The issue includes a redacted, size-bounded transcript of the active conversation and is created immediately.`
}

// Parameters returns the JSON schema for tool parameters.
func (t *AnnoyedTool) Parameters() any { return tools.SchemaFor[AnnoyedParams]() }

// Run publishes the complaint and its redacted conversation provenance.
func (t *AnnoyedTool) Run(ctx context.Context, params AnnoyedParams) (*tools.ToolResult, error) {
	issue := strings.TrimSpace(params.Issue)
	if issue == "" {
		return nil, errors.New("annoyed: issue is required")
	}
	structuredIssue := formatAnnoyedIssue(params)
	// The limit applies to the ASSEMBLED report -- issue text plus category,
	// observed, expected, evidence and acceptance tests -- not to the `issue`
	// field alone. Saying "issue exceeds" sent callers off trimming the wrong
	// field, so name what is actually measured and report the real count.
	if assembled := utf8.RuneCountInString(structuredIssue); assembled > maxAnnoyedIssueChars {
		return nil, fmt.Errorf(
			"annoyed: assembled report is %d characters, exceeding the %d-character limit "+
				"(the limit covers issue, category, observed, expected, evidence and acceptance_tests combined, not the issue field alone)",
			assembled, maxAnnoyedIssueChars)
	}
	severity := normalizeAnnoyedSeverity(params.Severity)
	repository, err := annoyedRepository(t.config.Repository, os.Getenv("SWARM_ANNOYED_REPOSITORY"))
	if err != nil {
		return nil, err
	}

	conversationID := tools.OwnerConversationID(ctx)
	var conv *conversation.Conversation
	var historyErr error
	if conversationID == "" {
		historyErr = errors.New("active conversation ID unavailable")
	} else if t.config.LoadConversation == nil {
		historyErr = errors.New("conversation loader unavailable")
	} else {
		conv, historyErr = t.config.LoadConversation(ctx, conversationID)
	}

	body, provenance := buildAnnoyedIssueBody(ctx, conv, historyErr, structuredIssue, severity, t.config.MaxBodyChars)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("annoyed: build issue report: %w", err)
	}
	title := annoyedPublicTitle(issue, severity)
	publisher := t.config.PublishFeedback
	if publisher == nil {
		publisher = reportGitHubFeedbackIssue
	}
	publicationURL, err := publisher(ctx, FeedbackPublication{
		Repository: repository,
		Title:      title,
		Body:       body,
	})
	if err != nil {
		return nil, err
	}

	b := tools.NewXML("result").
		Attr("status", "ok").
		Field("issue", issue).
		Field("publication_url", publicationURL).
		Field("repository", repository)
	if severity != "" {
		b.Attr("severity", severity)
	}

	result := tools.NewXMLResult(b)
	result.Metadata["issue"] = issue
	result.Metadata["publication_url"] = publicationURL
	result.Metadata["issue_url"] = publicationURL
	result.Metadata["repository"] = repository
	result.Metadata["conversation_id"] = conversationID
	result.Metadata["history_available"] = historyErr == nil && conv != nil
	result.Metadata["transcript_truncated"] = provenance.Truncated
	result.Metadata["redaction_count"] = provenance.RedactionCount
	result.Metadata["fingerprint"] = annoyedFingerprint(params)
	if category := normalizeAnnoyedCategory(params.Category); category != "" {
		result.Metadata["category"] = category
	}
	if severity != "" {
		result.Metadata["severity"] = severity
	}

	return result, nil
}

func normalizeAnnoyedSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

const maxAnnoyedEvidenceItems = 8

// formatAnnoyedIssue turns terse agent feedback into a reviewable engineering
// brief while preserving the original Issue as the first paragraph. Every field
// is optional for backward compatibility; the transcript remains the provenance
// source of record and is redacted by buildAnnoyedIssueBody.
func formatAnnoyedIssue(params AnnoyedParams) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(params.Issue))
	writeSection := func(title, value string) {
		if value = strings.TrimSpace(value); value != "" {
			fmt.Fprintf(&b, "\n\n## %s\n%s", title, value)
		}
	}
	if category := normalizeAnnoyedCategory(params.Category); category != "" {
		writeSection("Category", category)
	}
	writeSection("Observed", params.Observed)
	writeSection("Expected", params.Expected)
	writeList := func(title string, values []string) {
		kept := make([]string, 0, min(len(values), maxAnnoyedEvidenceItems))
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				kept = append(kept, value)
			}
			if len(kept) >= maxAnnoyedEvidenceItems {
				break
			}
		}
		if len(kept) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n\n## %s", title)
		for _, value := range kept {
			fmt.Fprintf(&b, "\n- %s", value)
		}
	}
	writeList("Evidence", params.Evidence)
	writeList("Acceptance tests", params.AcceptanceTests)
	fmt.Fprintf(&b, "\n\n<!-- annoyance-fingerprint: %s -->", annoyedFingerprint(params))
	return b.String()
}

// annoyedFingerprint is stable across transcript copies and volatile error IDs:
// it keys on the normalized defect class and the agent's concise mechanism.
func annoyedFingerprint(params AnnoyedParams) string {
	material := strings.Join([]string{
		normalizeAnnoyedCategory(params.Category),
		strings.ToLower(strings.TrimSpace(params.Issue)),
		strings.ToLower(strings.TrimSpace(params.Observed)),
	}, "\x00")
	sum := sha256.Sum256([]byte(material))
	return fmt.Sprintf("%x", sum[:8])
}

func normalizeAnnoyedCategory(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "hook_false_positive", "tool_failure", "inefficiency", "misleading_error", "missing_capability", "other":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}
