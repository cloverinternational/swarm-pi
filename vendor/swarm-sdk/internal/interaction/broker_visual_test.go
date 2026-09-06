package interaction

import (
	"encoding/json"
	"testing"
)

func TestQuestionTypeVisualChoice(t *testing.T) {
	if QuestionTypeVisualChoice != "visual_choice" {
		t.Errorf("got %q, want %q", QuestionTypeVisualChoice, "visual_choice")
	}
}

func TestQuestionRequestVisualField(t *testing.T) {
	req := QuestionRequest{
		Type:   QuestionTypeVisualChoice,
		Visual: map[string]any{"kind": "options"},
	}
	if req.Visual == nil {
		t.Fatal("Visual should be settable")
	}
}

func TestQuestionnaireInteractionContractJSON(t *testing.T) {
	index := 1
	answer := QuestionnaireAnswer{
		ID:        "database",
		Value:     "postgres",
		Label:     "PostgreSQL",
		WasCustom: false,
		Index:     &index,
	}
	got, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":"database","value":"postgres","label":"PostgreSQL","was_custom":false,"index":1}`
	if string(got) != want {
		t.Fatalf("answer JSON = %s, want %s", got, want)
	}

	answer.Index = nil
	got, err = json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"id":"database","value":"postgres","label":"PostgreSQL","was_custom":false}` {
		t.Fatalf("answer JSON with omitted index = %s", got)
	}
}

func TestQuestionnaireRequestAndResponseFields(t *testing.T) {
	req := QuestionRequest{
		Type: QuestionTypeQuestionnaire,
		Questions: []QuestionnaireQuestion{{
			ID:         "database",
			Prompt:     "Which database?",
			Label:      "Database",
			Options:    []QuestionOption{{Value: "postgres", Label: "PostgreSQL"}},
			AllowOther: false,
		}},
	}
	resp := QuestionResponse{
		QuestionnaireAnswers: []QuestionnaireAnswer{{
			ID:        "database",
			Value:     "postgres",
			Label:     "PostgreSQL",
			WasCustom: false,
		}},
	}
	if req.Type != "questionnaire" || len(req.Questions) != 1 {
		t.Fatalf("questionnaire request was not retained: %#v", req)
	}
	if req.Questions[0].AllowOther {
		t.Fatal("explicit false AllowOther was not retained")
	}
	if len(resp.QuestionnaireAnswers) != 1 {
		t.Fatalf("questionnaire response was not retained: %#v", resp)
	}
}
