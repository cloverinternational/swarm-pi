package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/interaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/interaction/visual"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// AskUserQuestionParams are the typed parameters for the ask_user_question tool.
type AskUserQuestionParams struct {
	Question    string                        `json:"question,omitempty"      description:"A single question to ask. Exactly one of question or questions must be supplied."`
	Type        string                        `json:"type,omitempty"          description:"Type of question: 'text' for free-form input, 'choice' for single selection, 'multi_choice' for multiple selections, 'confirm' for yes/no, 'number' for numeric input, 'visual_choice' for a visual primitive picker" enum:"text,choice,multi_choice,confirm,number,visual_choice" default:"text"`
	Choices     []string                      `json:"choices,omitempty"       description:"Available choices (required for 'choice' and 'multi_choice' types)"`
	Options     []interaction.QuestionOption  `json:"options,omitempty" description:"Structured options for a singleton choice question. Each option has a unique value, a non-empty label, and an optional description."`
	Questions   []QuestionnaireQuestionParams `json:"questions,omitempty" description:"A questionnaire of related questions. Exactly one of question or questions must be supplied."`
	Visual      map[string]any                `json:"visual,omitempty"        description:"Visual primitive payload (required when type == 'visual_choice'). Shape: {kind: 'options'|'cards'|'split'|'markdown'|'mermaid'|'raw_html', ...primitive fields}"`
	Default     string                        `json:"default,omitempty"       description:"Default value if user doesn't respond or for text input placeholder"`
	Timeout     *int                          `json:"timeout,omitempty"       description:"Timeout in seconds (default: 300, 0 for no timeout)"                                                                                                                   default:"300"`
	Title       string                        `json:"title,omitempty"         description:"Optional title for the question dialog"`
	Description string                        `json:"description,omitempty"   description:"Optional detailed description providing context about why you're asking"`
	Priority    string                        `json:"priority,omitempty"      description:"Priority level for the question"                                                                                                                                        enum:"low,medium,high,critical" default:"medium"`
}

// QuestionnaireQuestionParams is one model-facing questionnaire item.
type QuestionnaireQuestionParams struct {
	ID         string                       `json:"id"                    description:"Unique, non-empty identifier for this question."`
	Prompt     string                       `json:"prompt"                description:"The question shown to the user."`
	Label      string                       `json:"label,omitempty"       description:"Optional short, non-empty heading for the question."`
	Options    []interaction.QuestionOption `json:"options"               description:"Available structured options, each with a unique value and non-empty label."`
	AllowOther *bool                        `json:"allow_other,omitempty" description:"Whether the user may provide a custom answer. Defaults to true when omitted."`
}

const (
	maxQuestionnaireQuestions = 10
	maxQuestionnaireOptions   = 20
)

// AskUserQuestionTool allows agents to ask questions to users during execution.
// This is a Ring 1 implementation that uses the interaction broker.
type AskUserQuestionTool struct {
	tools.BaseTool
	broker interaction.UserInteractionBroker
}

// NewAskUserQuestionTool creates a new ask_user_question tool wrapped as a typed Tool.
func NewAskUserQuestionTool(broker interaction.UserInteractionBroker) tools.Tool {
	t := &AskUserQuestionTool{broker: broker}
	return tools.Typed[AskUserQuestionParams](t)
}

// Name returns the tool name.
func (t *AskUserQuestionTool) Name() string { return "ask_user_question" }

// Description returns the tool description.
func (t *AskUserQuestionTool) Description() string {
	return `Ask the user a question and wait for their response. Use this when clarification
would materially change the outcome — don't use it for questions you can answer from context
or for simple guesses you can verify with a quick test.

For several related decisions, send one questionnaire with 'questions' instead of
'question'. Each item requires a unique id, a prompt, and structured options:

  {"questions":[{
    "id":"database",
    "prompt":"Which database should we use?",
    "label":"Database",
    "options":[
      {"value":"postgres","label":"PostgreSQL","description":"Relational and full-featured"},
      {"value":"sqlite","label":"SQLite","description":"Simple embedded storage"}
    ],
    "allow_other":false
  }]}

Omitting allow_other permits a custom answer. Set it to false to require one of the
listed values. Do not combine 'question' and 'questions'.

## When to prefer 'visual_choice' over 'choice'

Strongly prefer 'visual_choice' any time the answer is easier to SEE than to read:

- UI / layout / component design ("which layout?", "which button style?", "which nav?")
- Architectural options that map to diagrams ("which service topology?", "which state machine?")
- Side-by-side comparisons where prose would be dense ("flat vs tree structure?")
- Any "show me options" where you would otherwise describe three designs in a paragraph

If the user says "plan / design / layout / mock up / sketch / wireframe / compare", treat
that as a strong signal to enter plan mode AND to offer 'visual_choice' for decisions that
benefit from visual comparison. Do NOT fall back to a bulleted list in chat when a
visual_choice picker would be a better fit.

## visual_choice payload

Supply a 'visual' object with a 'kind' discriminator and primitive-specific fields:

  { "kind": "options",  "items": [{"key","label","description?"}, ...] }
  { "kind": "cards",    "items": [{"key","title","description?","body?"}, ...] }
  { "kind": "split",    "items": [ {A}, {B} ] }   // exactly 2 items
  { "kind": "markdown", "content": "# prose + fenced mermaid blocks" }
  { "kind": "mermaid",  "diagram": "flowchart TD\n...", "caption?": "..." }
  { "kind": "raw_html", "content": "<div>...</div>" }

Interactive kinds (options/cards/split) accept any presentation kind as a nested 'body':

  {"kind":"cards","items":[
    {"key":"flat","title":"Flat","body":{"kind":"mermaid","diagram":"graph TD\nA-->B\nA-->C"}},
    {"key":"tree","title":"Tree","body":{"kind":"mermaid","diagram":"graph TD\nR-->A-->C"}}
  ]}

The active client renders the picker inline; in the TUI the user reviews and selects it in the terminal.

## Other types

- 'text' — free-form input
- 'choice' — single text option (only when visual treatment adds nothing)
- 'multi_choice' — multiple text options
- 'confirm' — yes/no (use for deletions, destructive actions)
- 'number' — numeric input

## Rules

- Do NOT use for questions you can answer from context.
- Do NOT use in the middle of an already-specified multi-step task.
- Prefer visual_choice over text choice for anything design-adjacent.
- Prefer attempting + showing results over asking upfront on non-design tasks.`
}

// Parameters returns the JSON schema generated from AskUserQuestionParams.
func (t *AskUserQuestionTool) Parameters() any {
	return tools.SchemaFor[AskUserQuestionParams]()
}

// Run executes the tool with typed parameters.
func (t *AskUserQuestionTool) Run(ctx context.Context, p AskUserQuestionParams) (*tools.ToolResult, error) {
	hasQuestions := len(p.Questions) != 0
	if p.Question == "" && !hasQuestions {
		return nil, fmt.Errorf("question parameter is required")
	}
	hasQuestion := strings.TrimSpace(p.Question) != ""
	if !hasQuestion && !hasQuestions {
		return nil, fmt.Errorf("question parameter is required")
	}
	if hasQuestion && hasQuestions {
		return nil, fmt.Errorf("exactly one of question or questions is required")
	}

	// Resolve question type with default
	questionType := interaction.QuestionTypeText
	if hasQuestions {
		if p.Type != "" {
			return nil, fmt.Errorf("type cannot be used with questions")
		}
		questionType = interaction.QuestionTypeQuestionnaire
	} else if p.Type != "" {
		// Validate type before accepting it
		validTypes := map[string]bool{
			"text": true, "choice": true, "multi_choice": true,
			"confirm": true, "number": true, "visual_choice": true,
		}
		if !validTypes[p.Type] {
			return nil, fmt.Errorf("invalid question type: %q (must be text, choice, multi_choice, confirm, number, or visual_choice)", p.Type)
		}
		questionType = interaction.QuestionType(p.Type)
	}

	questions, err := validateQuestionnaire(p.Questions)
	if err != nil {
		return nil, err
	}
	if err := validateOptions("options", p.Options); err != nil {
		return nil, err
	}
	if len(p.Choices) > 0 && len(p.Options) > 0 {
		return nil, fmt.Errorf("choices and options cannot be used together")
	}
	if len(p.Options) > 0 &&
		questionType != interaction.QuestionTypeChoice &&
		questionType != interaction.QuestionTypeMultiChoice {
		return nil, fmt.Errorf("options can only be used with choice or multi_choice questions")
	}
	if hasQuestions && (len(p.Choices) != 0 || len(p.Options) != 0 || p.Visual != nil || p.Default != "") {
		return nil, fmt.Errorf("singleton fields choices, options, visual, and default cannot be used with questions")
	}

	// Resolve timeout with default
	timeout := 300
	if p.Timeout != nil {
		if *p.Timeout < 0 {
			return nil, fmt.Errorf("timeout must be zero or greater")
		}
		timeout = *p.Timeout
	}

	// Validate choices for choice-based questions
	if (questionType == interaction.QuestionTypeChoice || questionType == interaction.QuestionTypeMultiChoice) && len(p.Choices) == 0 && len(p.Options) == 0 {
		return nil, fmt.Errorf("choices or options are required for %s questions", questionType)
	}

	// Decode visual primitive for visual_choice questions
	var visualPayload visual.VisualPrimitive
	if questionType == interaction.QuestionTypeVisualChoice {
		if p.Visual == nil {
			return nil, fmt.Errorf("visual parameter is required for visual_choice questions")
		}
		raw, err := json.Marshal(p.Visual)
		if err != nil {
			return nil, fmt.Errorf("encode visual param: %w", err)
		}
		visualPayload, err = visual.DecodePrimitive(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid visual primitive: %w", err)
		}
	}

	// Build metadata
	metadata := interaction.QuestionMetadata{
		Priority: "medium",
		ToolName: t.Name(),
		Source:   "agent_tool",
	}
	if p.Title != "" {
		metadata.Title = p.Title
	}
	if p.Description != "" {
		metadata.Description = p.Description
	}
	if p.Priority != "" {
		metadata.Priority = p.Priority
	}
	if agentID, ok := ctx.Value("agent_id").(string); ok && agentID != "" {
		metadata.AgentID = agentID
	}
	if planID, ok := ctx.Value("plan_id").(string); ok && planID != "" {
		metadata.PlanID = planID
	} else if pp, ok := t.broker.(interface {
		CurrentPlanID(context.Context, string) string
	}); ok {
		metadata.PlanID = pp.CurrentPlanID(ctx, metadata.AgentID)
	}

	request := interaction.QuestionRequest{
		ID:        fmt.Sprintf("question-%d", time.Now().UnixNano()),
		Question:  p.Question,
		Type:      questionType,
		Choices:   p.Choices,
		Options:   p.Options,
		Questions: questions,
		Default:   p.Default,
		Timeout:   timeout,
		Context:   make(map[string]any),
		Metadata:  metadata,
		Visual:    visualPayload, // nil for non-visual types
	}

	if t.broker == nil {
		return interactionOutcomeResult(interaction.OutcomeInteractiveUnavailable, "interactive broker is not configured"), nil
	}
	askCtx, askCancel := questionContext(ctx, timeout)
	defer askCancel()
	response, err := t.broker.AskQuestion(askCtx, request)
	if err != nil {
		if outcome := interaction.OutcomeOf(err); outcome != "" {
			return interactionOutcomeResult(outcome, err.Error()), nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return questionTimeoutResult(p.Default), nil
		}
		if errors.Is(err, context.Canceled) {
			return interactionOutcomeResult(interaction.OutcomeParentCanceled, err.Error()), nil
		}
		return nil, fmt.Errorf("failed to ask question: %w", err)
	}
	if response.Canceled {
		return interactionOutcomeResult(interaction.OutcomeRejected, "user rejected the question"), nil
	}
	if response.Timeout {
		return questionTimeoutResult(p.Default), nil
	}

	switch questionType {
	case interaction.QuestionTypeQuestionnaire:
		answers, err := normalizeQuestionnaireAnswers(questions, response.QuestionnaireAnswers)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(answers)
		if err != nil {
			return nil, fmt.Errorf("encode questionnaire answers: %w", err)
		}
		return tools.NewXMLResult(tools.NewXML("result").
			Attr("type", string(questionType)).
			Field("answers", safeCDATA(string(encoded)))).
			WithMetadata("answers", answers).
			WithMetadata("question_type", string(questionType)).
			WithMetadata("interaction_outcome", interaction.OutcomeResponse), nil
	case interaction.QuestionTypeMultiChoice:
		if len(response.Answers) > 0 {
			return tools.NewXMLResult(tools.NewXML("result").
				Attr("type", "multi_choice").
				Field("answer", fmt.Sprintf("%v", response.Answers))).
				WithMetadata("answers", response.Answers).
				WithMetadata("question_type", string(questionType)).
				WithMetadata("interaction_outcome", interaction.OutcomeResponse), nil
		}
		return tools.NewXMLResult(tools.NewXML("result").
			Attr("type", "multi_choice").
			Attr("selected", "none")).
			WithMetadata("interaction_outcome", interaction.OutcomeResponse), nil
	case interaction.QuestionTypeConfirm:
		return tools.NewXMLResult(tools.NewXML("result").
			Attr("type", "confirm").
			AttrBool("confirmed", response.Confirmed)).
			WithMetadata("confirmed", response.Confirmed).
			WithMetadata("question_type", string(questionType)).
			WithMetadata("interaction_outcome", interaction.OutcomeResponse), nil
	default:
		// text, choice, number
		answer := response.Answer
		if answer == "" {
			answer = "(no response)"
		}
		return tools.NewXMLResult(tools.NewXML("result").
			Attr("type", string(questionType)).
			Field("answer", answer)).
			WithMetadata("answer", response.Answer).
			WithMetadata("question_type", string(questionType)).
			WithMetadata("interaction_outcome", interaction.OutcomeResponse), nil
	}
}

func questionTimeoutResult(defaultValue string) *tools.ToolResult {
	if defaultValue == "" {
		return interactionOutcomeResult(interaction.OutcomeTimeout, "user did not respond before the timeout")
	}
	return tools.NewXMLResult(tools.NewXML("result").
		Attr("type", "interaction").
		Attr("outcome", string(interaction.OutcomeTimeout)).
		Field("message", "user did not respond before the timeout").
		Field("answer", defaultValue)).
		WithMetadata("interaction_outcome", interaction.OutcomeTimeout).
		WithMetadata("answer", defaultValue).
		WithMetadata("used_default", true)
}

func normalizeQuestionnaireAnswers(
	questions []interaction.QuestionnaireQuestion,
	answers []interaction.QuestionnaireAnswer,
) ([]interaction.QuestionnaireAnswer, error) {
	if len(answers) != len(questions) {
		return nil, fmt.Errorf("questionnaire response has %d answers, want %d", len(answers), len(questions))
	}
	byID := make(map[string]interaction.QuestionnaireAnswer, len(answers))
	for _, answer := range answers {
		if _, exists := byID[answer.ID]; exists {
			return nil, fmt.Errorf("questionnaire response contains duplicate answer id %q", answer.ID)
		}
		byID[answer.ID] = answer
	}

	normalized := make([]interaction.QuestionnaireAnswer, 0, len(questions))
	for _, question := range questions {
		answer, ok := byID[question.ID]
		if !ok {
			return nil, fmt.Errorf("questionnaire response is missing answer id %q", question.ID)
		}
		if answer.WasCustom {
			value := strings.TrimSpace(answer.Value)
			if !question.AllowOther {
				return nil, fmt.Errorf("questionnaire answer %q does not allow a custom value", question.ID)
			}
			if value == "" {
				return nil, fmt.Errorf("questionnaire answer %q has an empty custom value", question.ID)
			}
			normalized = append(normalized, interaction.QuestionnaireAnswer{
				ID:        question.ID,
				Value:     value,
				Label:     value,
				WasCustom: true,
			})
			continue
		}

		found := false
		for index, option := range question.Options {
			if answer.Value != option.Value {
				continue
			}
			canonicalIndex := index
			normalized = append(normalized, interaction.QuestionnaireAnswer{
				ID:    question.ID,
				Value: option.Value,
				Label: option.Label,
				Index: &canonicalIndex,
			})
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("questionnaire answer %q has unknown option value %q", question.ID, answer.Value)
		}
	}
	return normalized, nil
}

func safeCDATA(value string) string {
	return strings.ReplaceAll(value, "]]>", "]]]]><![CDATA[>")
}

func validateQuestionnaire(params []QuestionnaireQuestionParams) ([]interaction.QuestionnaireQuestion, error) {
	if len(params) == 0 {
		return nil, nil
	}
	if len(params) > maxQuestionnaireQuestions {
		return nil, fmt.Errorf("questions must contain at most %d items", maxQuestionnaireQuestions)
	}

	questions := make([]interaction.QuestionnaireQuestion, 0, len(params))
	ids := make(map[string]struct{}, len(params))
	for i, item := range params {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return nil, fmt.Errorf("questions[%d].id must be non-empty", i)
		}
		if _, exists := ids[id]; exists {
			return nil, fmt.Errorf("questions[%d].id %q is duplicated", i, id)
		}
		ids[id] = struct{}{}
		if strings.TrimSpace(item.Prompt) == "" {
			return nil, fmt.Errorf("questions[%d].prompt must be non-empty", i)
		}
		if item.Label != "" && strings.TrimSpace(item.Label) == "" {
			return nil, fmt.Errorf("questions[%d].label must be non-empty when provided", i)
		}
		if err := validateOptions(fmt.Sprintf("questions[%d].options", i), item.Options); err != nil {
			return nil, err
		}
		if len(item.Options) == 0 {
			return nil, fmt.Errorf("questions[%d].options must contain at least one item", i)
		}
		allowOther := true
		if item.AllowOther != nil {
			allowOther = *item.AllowOther
		}
		questions = append(questions, interaction.QuestionnaireQuestion{
			ID:         id,
			Prompt:     item.Prompt,
			Label:      item.Label,
			Options:    item.Options,
			AllowOther: allowOther,
		})
	}
	return questions, nil
}

func validateOptions(field string, options []interaction.QuestionOption) error {
	if len(options) > maxQuestionnaireOptions {
		return fmt.Errorf("%s must contain at most %d items", field, maxQuestionnaireOptions)
	}
	values := make(map[string]struct{}, len(options))
	for i, option := range options {
		value := strings.TrimSpace(option.Value)
		if value == "" {
			return fmt.Errorf("%s[%d].value must be non-empty", field, i)
		}
		if _, exists := values[value]; exists {
			return fmt.Errorf("%s[%d].value %q is duplicated", field, i, value)
		}
		values[value] = struct{}{}
		if strings.TrimSpace(option.Label) == "" {
			return fmt.Errorf("%s[%d].label must be non-empty", field, i)
		}
	}
	return nil
}

func interactionOutcomeResult(outcome interaction.Outcome, message string) *tools.ToolResult {
	return tools.NewXMLResult(tools.NewXML("result").
		Attr("type", "interaction").
		Attr("outcome", string(outcome)).
		Field("message", message)).
		WithMetadata("interaction_outcome", outcome)
}

func questionContext(parent context.Context, timeout int) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(parent)
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if timeout == 0 {
		ctx, cancel = context.WithCancel(base)
	} else {
		ctx, cancel = context.WithTimeout(base, time.Duration(timeout)*time.Second)
	}
	stopParentCancellation := context.AfterFunc(parent, cancel)
	return ctx, func() {
		stopParentCancellation()
		cancel()
	}
}
