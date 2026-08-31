package builtin

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/interaction"
)

type questionTestBroker struct {
	requests []interaction.QuestionRequest
	ask      func(context.Context, interaction.QuestionRequest) (interaction.QuestionResponse, error)
}

func (b *questionTestBroker) AskQuestion(ctx context.Context, req interaction.QuestionRequest) (interaction.QuestionResponse, error) {
	b.requests = append(b.requests, req)
	return b.ask(ctx, req)
}

func (*questionTestBroker) AskApproval(context.Context, interaction.ApprovalRequest) (interaction.ApprovalResponse, error) {
	return interaction.ApprovalResponse{}, nil
}

func (*questionTestBroker) Notify(context.Context, interaction.Notification) error { return nil }

func (*questionTestBroker) PushScreen(context.Context, any) (any, error) {
	return nil, nil
}

func intPointer(value int) *int    { return &value }
func boolPointer(value bool) *bool { return &value }

func TestAskUserQuestionZeroTimeoutMeansNoTimeout(t *testing.T) {
	broker := &questionTestBroker{
		ask: func(_ context.Context, req interaction.QuestionRequest) (interaction.QuestionResponse, error) {
			if req.Timeout != 0 {
				t.Fatalf("request timeout = %d, want no timeout", req.Timeout)
			}
			return interaction.QuestionResponse{Answer: "yes"}, nil
		},
	}
	tool := &AskUserQuestionTool{broker: broker}

	result, err := tool.Run(context.Background(), AskUserQuestionParams{
		Question: "Continue?",
		Timeout:  intPointer(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Metadata["interaction_outcome"]; got != interaction.OutcomeResponse {
		t.Fatalf("interaction_outcome = %#v", got)
	}
}

func TestAskUserQuestionOmittedTimeoutDefaultsTo300(t *testing.T) {
	broker := &questionTestBroker{
		ask: func(_ context.Context, req interaction.QuestionRequest) (interaction.QuestionResponse, error) {
			if req.Timeout != 300 {
				t.Fatalf("request timeout = %d, want 300", req.Timeout)
			}
			return interaction.QuestionResponse{Confirmed: true}, nil
		},
	}
	tool := &AskUserQuestionTool{broker: broker}

	_, err := tool.Run(context.Background(), AskUserQuestionParams{
		Question: "Continue?",
		Type:     "confirm",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAskUserQuestionKnownNonResponseOutcomesAreResults(t *testing.T) {
	tests := []struct {
		name        string
		response    interaction.QuestionResponse
		err         error
		wantOutcome interaction.Outcome
	}{
		{
			name:        "interactive unavailable",
			err:         interaction.NewError(interaction.OutcomeInteractiveUnavailable, errors.New("not attached")),
			wantOutcome: interaction.OutcomeInteractiveUnavailable,
		},
		{
			name:        "delivery failure",
			err:         interaction.NewError(interaction.OutcomeDeliveryFailure, errors.New("dispatch failed")),
			wantOutcome: interaction.OutcomeDeliveryFailure,
		},
		{
			name:        "framework cancellation",
			err:         interaction.NewError(interaction.OutcomeParentCanceled, context.Canceled),
			wantOutcome: interaction.OutcomeParentCanceled,
		},
		{
			name:        "explicit user timeout",
			response:    interaction.QuestionResponse{Timeout: true},
			wantOutcome: interaction.OutcomeTimeout,
		},
		{
			name:        "user rejection",
			response:    interaction.QuestionResponse{Canceled: true},
			wantOutcome: interaction.OutcomeRejected,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			broker := &questionTestBroker{
				ask: func(context.Context, interaction.QuestionRequest) (interaction.QuestionResponse, error) {
					return test.response, test.err
				},
			}
			tool := &AskUserQuestionTool{broker: broker}
			result, err := tool.Run(context.Background(), AskUserQuestionParams{Question: "Continue?"})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got := result.Metadata["interaction_outcome"]; got != test.wantOutcome {
				t.Fatalf("interaction_outcome = %#v, want %q", got, test.wantOutcome)
			}
		})
	}
}

func TestAskUserQuestionQuestionnaireRequestAndResponse(t *testing.T) {
	index := 0
	answers := []interaction.QuestionnaireAnswer{
		{
			ID:        "database",
			Value:     "postgres",
			Label:     "PostgreSQL",
			WasCustom: false,
			Index:     &index,
		},
		{
			ID:    "region",
			Value: "us-east",
			Label: "untrusted label",
		},
	}
	broker := &questionTestBroker{
		ask: func(_ context.Context, req interaction.QuestionRequest) (interaction.QuestionResponse, error) {
			if req.Type != interaction.QuestionTypeQuestionnaire {
				t.Fatalf("request type = %q, want questionnaire", req.Type)
			}
			if req.Question != "" || len(req.Questions) != 2 {
				t.Fatalf("unexpected questionnaire request: %#v", req)
			}
			if !req.Questions[0].AllowOther {
				t.Fatal("omitted allow_other should default to true")
			}
			if req.Questions[1].AllowOther {
				t.Fatal("explicit allow_other=false was not preserved")
			}
			if got := req.Questions[0].Options[0]; got.Value != "postgres" || got.Label != "PostgreSQL" || got.Description == "" {
				t.Fatalf("structured option = %#v", got)
			}
			return interaction.QuestionResponse{QuestionnaireAnswers: answers}, nil
		},
	}
	tool := &AskUserQuestionTool{broker: broker}

	result, err := tool.Run(context.Background(), AskUserQuestionParams{
		Questions: []QuestionnaireQuestionParams{
			{
				ID:     "database",
				Prompt: "Which database?",
				Label:  "Database",
				Options: []interaction.QuestionOption{{
					Value:       "postgres",
					Label:       "PostgreSQL",
					Description: "Relational database",
				}},
			},
			{
				ID:         "region",
				Prompt:     "Which region?",
				Options:    []interaction.QuestionOption{{Value: "us-east", Label: "US East"}},
				AllowOther: boolPointer(false),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := json.Marshal(result.Metadata["answers"])
	if err != nil {
		t.Fatal(err)
	}
	wantCDATA := "<answers><![CDATA[" + string(encoded) + "]]></answers>"
	if !strings.Contains(result.Output, wantCDATA) {
		t.Fatalf("output = %s, want JSON CDATA %s", result.Output, wantCDATA)
	}
	gotAnswers, ok := result.Metadata["answers"].([]interaction.QuestionnaireAnswer)
	if !ok || len(gotAnswers) != 2 || gotAnswers[1].WasCustom ||
		gotAnswers[1].Label != "US East" || gotAnswers[1].Index == nil || *gotAnswers[1].Index != 0 {
		t.Fatalf("metadata answers = %#v", result.Metadata["answers"])
	}
	if got := result.Metadata["question_type"]; got != "questionnaire" {
		t.Fatalf("question_type = %#v", got)
	}
}

func TestAskUserQuestionQuestionnaireTypedExecution(t *testing.T) {
	broker := &questionTestBroker{
		ask: func(_ context.Context, req interaction.QuestionRequest) (interaction.QuestionResponse, error) {
			if len(req.Questions) != 1 {
				t.Fatalf("decoded questions = %#v", req.Questions)
			}
			item := req.Questions[0]
			if item.AllowOther || item.Options[0].Value != "postgres" || item.Options[0].Description != "Relational" {
				t.Fatalf("decoded questionnaire item = %#v", item)
			}
			return interaction.QuestionResponse{QuestionnaireAnswers: []interaction.QuestionnaireAnswer{{
				ID: "database", Value: "postgres", Label: "PostgreSQL",
			}}}, nil
		},
	}
	tool := NewAskUserQuestionTool(broker)
	result, err := tool.Execute(context.Background(), map[string]any{
		"questions": []any{map[string]any{
			"id":          "database",
			"prompt":      "Which database?",
			"allow_other": false,
			"options": []any{map[string]any{
				"value": "postgres", "label": "PostgreSQL", "description": "Relational",
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, `"id":"database"`) {
		t.Fatalf("typed questionnaire output = %s", result.Output)
	}
}

func TestAskUserQuestionSingletonStructuredOptions(t *testing.T) {
	broker := &questionTestBroker{
		ask: func(_ context.Context, req interaction.QuestionRequest) (interaction.QuestionResponse, error) {
			if len(req.Choices) != 0 || len(req.Options) != 2 {
				t.Fatalf("singleton options were not forwarded: %#v", req)
			}
			return interaction.QuestionResponse{Answer: "postgres"}, nil
		},
	}
	tool := &AskUserQuestionTool{broker: broker}
	result, err := tool.Run(context.Background(), AskUserQuestionParams{
		Question: "Which database?",
		Type:     "choice",
		Options: []interaction.QuestionOption{
			{Value: "postgres", Label: "PostgreSQL"},
			{Value: "sqlite", Label: "SQLite"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata["answer"] != "postgres" {
		t.Fatalf("legacy answer metadata changed: %#v", result.Metadata)
	}
}

func TestAskUserQuestionQuestionnaireNormalizesAndValidatesResponses(t *testing.T) {
	noOther := false
	params := AskUserQuestionParams{Questions: []QuestionnaireQuestionParams{{
		ID:         "database",
		Prompt:     "Which database?",
		Options:    []interaction.QuestionOption{{Value: "postgres", Label: "PostgreSQL"}},
		AllowOther: &noOther,
	}}}
	tests := []struct {
		name    string
		answers []interaction.QuestionnaireAnswer
		wantErr string
	}{
		{name: "missing", wantErr: "0 answers, want 1"},
		{name: "unknown id", answers: []interaction.QuestionnaireAnswer{{ID: "other", Value: "postgres"}}, wantErr: "missing answer id"},
		{name: "unknown value", answers: []interaction.QuestionnaireAnswer{{ID: "database", Value: "mysql"}}, wantErr: "unknown option value"},
		{name: "custom forbidden", answers: []interaction.QuestionnaireAnswer{{ID: "database", Value: "mysql", WasCustom: true}}, wantErr: "does not allow a custom value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tool := &AskUserQuestionTool{broker: &questionTestBroker{
				ask: func(context.Context, interaction.QuestionRequest) (interaction.QuestionResponse, error) {
					return interaction.QuestionResponse{QuestionnaireAnswers: test.answers}, nil
				},
			}}
			_, err := tool.Run(context.Background(), params)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestAskUserQuestionQuestionnaireCustomAnswerProducesWellFormedXML(t *testing.T) {
	custom := `choice]]></answers><injected>bad</injected>`
	tool := &AskUserQuestionTool{broker: &questionTestBroker{
		ask: func(context.Context, interaction.QuestionRequest) (interaction.QuestionResponse, error) {
			return interaction.QuestionResponse{QuestionnaireAnswers: []interaction.QuestionnaireAnswer{{
				ID: "database", Value: custom, Label: "ignored", WasCustom: true,
			}}}, nil
		},
	}}
	result, err := tool.Run(context.Background(), AskUserQuestionParams{Questions: []QuestionnaireQuestionParams{{
		ID:      "database",
		Prompt:  "Which database?",
		Options: []interaction.QuestionOption{{Value: "postgres", Label: "PostgreSQL"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Answers string `xml:"answers"`
	}
	if err := xml.Unmarshal([]byte(result.Output), &decoded); err != nil {
		t.Fatalf("questionnaire result is not well-formed XML: %v\n%s", err, result.Output)
	}
	var answers []interaction.QuestionnaireAnswer
	if err := json.Unmarshal([]byte(decoded.Answers), &answers); err != nil {
		t.Fatalf("answers are not valid JSON: %v\n%s", err, decoded.Answers)
	}
	if len(answers) != 1 || answers[0].Value != custom {
		t.Fatalf("answers = %#v, want custom value preserved", answers)
	}
}

func TestAskUserQuestionRawContextErrorsBecomeInteractionOutcomes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want interaction.Outcome
	}{
		{name: "deadline", err: context.DeadlineExceeded, want: interaction.OutcomeTimeout},
		{name: "cancel", err: context.Canceled, want: interaction.OutcomeParentCanceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tool := &AskUserQuestionTool{broker: &questionTestBroker{
				ask: func(context.Context, interaction.QuestionRequest) (interaction.QuestionResponse, error) {
					return interaction.QuestionResponse{}, test.err
				},
			}}
			result, err := tool.Run(context.Background(), AskUserQuestionParams{Question: "Continue?"})
			if err != nil {
				t.Fatal(err)
			}
			if got := result.Metadata["interaction_outcome"]; got != test.want {
				t.Fatalf("interaction_outcome = %#v, want %q", got, test.want)
			}
		})
	}
}

func TestAskUserQuestionQuestionnaireValidation(t *testing.T) {
	validItem := func() QuestionnaireQuestionParams {
		return QuestionnaireQuestionParams{
			ID:      "database",
			Prompt:  "Which database?",
			Options: []interaction.QuestionOption{{Value: "postgres", Label: "PostgreSQL"}},
		}
	}
	tooManyQuestions := make([]QuestionnaireQuestionParams, maxQuestionnaireQuestions+1)
	for i := range tooManyQuestions {
		tooManyQuestions[i] = validItem()
		tooManyQuestions[i].ID = fmt.Sprintf("question-%d", i)
	}
	tooManyOptions := make([]interaction.QuestionOption, maxQuestionnaireOptions+1)
	for i := range tooManyOptions {
		tooManyOptions[i] = interaction.QuestionOption{Value: fmt.Sprintf("value-%d", i), Label: "Label"}
	}

	tests := []struct {
		name   string
		params AskUserQuestionParams
		want   string
	}{
		{name: "neither singleton nor batch", params: AskUserQuestionParams{}, want: "question parameter is required"},
		{name: "singleton and batch", params: AskUserQuestionParams{Question: "One?", Questions: []QuestionnaireQuestionParams{validItem()}}, want: "exactly one"},
		{name: "type with batch", params: AskUserQuestionParams{Type: "choice", Questions: []QuestionnaireQuestionParams{validItem()}}, want: "type cannot"},
		{name: "singleton field with batch", params: AskUserQuestionParams{Choices: []string{"x"}, Questions: []QuestionnaireQuestionParams{validItem()}}, want: "singleton fields"},
		{name: "choices and options", params: AskUserQuestionParams{Question: "Pick", Type: "choice", Choices: []string{"x"}, Options: validItem().Options}, want: "cannot be used together"},
		{name: "options with text", params: AskUserQuestionParams{Question: "Say", Type: "text", Options: validItem().Options}, want: "only be used with"},
		{name: "too many questions", params: AskUserQuestionParams{Questions: tooManyQuestions}, want: "at most"},
		{name: "empty id", params: AskUserQuestionParams{Questions: []QuestionnaireQuestionParams{{Prompt: "Prompt", Options: validItem().Options}}}, want: ".id must be non-empty"},
		{name: "duplicate id", params: AskUserQuestionParams{Questions: []QuestionnaireQuestionParams{validItem(), validItem()}}, want: "duplicated"},
		{name: "empty prompt", params: AskUserQuestionParams{Questions: []QuestionnaireQuestionParams{{ID: "id", Prompt: " ", Options: validItem().Options}}}, want: ".prompt must be non-empty"},
		{name: "blank optional label", params: AskUserQuestionParams{Questions: []QuestionnaireQuestionParams{{ID: "id", Prompt: "Prompt", Label: " ", Options: validItem().Options}}}, want: ".label must be non-empty"},
		{name: "no options", params: AskUserQuestionParams{Questions: []QuestionnaireQuestionParams{{ID: "id", Prompt: "Prompt"}}}, want: ".options must contain"},
		{name: "empty option value", params: AskUserQuestionParams{Questions: []QuestionnaireQuestionParams{{ID: "id", Prompt: "Prompt", Options: []interaction.QuestionOption{{Label: "Label"}}}}}, want: ".value must be non-empty"},
		{name: "duplicate option value", params: AskUserQuestionParams{Questions: []QuestionnaireQuestionParams{{ID: "id", Prompt: "Prompt", Options: []interaction.QuestionOption{{Value: "same", Label: "One"}, {Value: "same", Label: "Two"}}}}}, want: "duplicated"},
		{name: "empty option label", params: AskUserQuestionParams{Questions: []QuestionnaireQuestionParams{{ID: "id", Prompt: "Prompt", Options: []interaction.QuestionOption{{Value: "value", Label: " "}}}}}, want: ".label must be non-empty"},
		{name: "too many options", params: AskUserQuestionParams{Questions: []QuestionnaireQuestionParams{{ID: "id", Prompt: "Prompt", Options: tooManyOptions}}}, want: "at most"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tool := &AskUserQuestionTool{broker: &questionTestBroker{
				ask: func(context.Context, interaction.QuestionRequest) (interaction.QuestionResponse, error) {
					t.Fatal("broker should not be called for invalid parameters")
					return interaction.QuestionResponse{}, nil
				},
			}}
			_, err := tool.Run(context.Background(), test.params)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}
