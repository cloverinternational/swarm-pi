package builtin

import (
	"reflect"
	"testing"
)

func TestParamsForSteeringEval_NonQuestionToolUnchanged(t *testing.T) {
	in := map[string]any{"file_path": "foo.go", "content": "x"}
	out := paramsForSteeringEval("Write", in)
	if !reflect.DeepEqual(in, out) {
		t.Errorf("non-question tool params mutated: %v -> %v", in, out)
	}
}

func TestParamsForSteeringEval_TextQuestionUnchanged(t *testing.T) {
	in := map[string]any{
		"question": "Which test framework?",
		"type":     "choice",
		"choices":  []any{"pytest", "unittest"},
	}
	out := paramsForSteeringEval("ask_user_question", in)
	if !reflect.DeepEqual(in, out) {
		t.Errorf("text question mutated: %v -> %v", in, out)
	}
}

func TestParamsForSteeringEval_NilParams(t *testing.T) {
	out := paramsForSteeringEval("ask_user_question", nil)
	if out != nil {
		t.Errorf("nil params should pass through, got %v", out)
	}
}

func TestParamsForSteeringEval_VisualPayloadStripped(t *testing.T) {
	in := map[string]any{
		"question": "Which layout?",
		"type":     "visual_choice",
		"visual": map[string]any{
			"kind": "cards",
			"items": []any{
				map[string]any{"key": "a", "title": "Single column", "description": "clean"},
				map[string]any{"key": "b", "title": "Sidebar"},
			},
		},
	}
	out := paramsForSteeringEval("ask_user_question", in)

	if _, ok := out["visual"]; ok {
		t.Error("visual payload must be stripped from steering view")
	}
	// Type stays as-is: the evaluator needs to know the agent is asking a
	// visual-style question so it can give topically-appropriate guidance.
	// (Rewriting type to 'choice' confuses the evaluator into saying
	// "you should be using visual_choice!" when the agent already is.)
	if out["type"] != "visual_choice" {
		t.Errorf("type = %q, want preserved as 'visual_choice'", out["type"])
	}
	if out["question"] != "Which layout?" {
		t.Errorf("question dropped: %v", out["question"])
	}
	// Ensure we did not mutate the caller's map.
	if _, ok := in["visual"]; !ok {
		t.Error("original params must not be mutated")
	}
	if in["type"] != "visual_choice" {
		t.Error("original params must not be mutated")
	}
}

func TestParamsForSteeringEval_VisualPayloadWithoutTypeFieldStillStripped(t *testing.T) {
	// Defensive: if somehow `visual` is set without `type`, still strip it so
	// the evaluator doesn't see the full layout payload.
	in := map[string]any{
		"question": "Which?",
		"visual":   map[string]any{"kind": "cards"},
	}
	out := paramsForSteeringEval("ask_user_question", in)
	if _, ok := out["visual"]; ok {
		t.Error("visual payload should be stripped even when type is missing")
	}
}

func TestParamsForSteeringEval_VisualPayloadHiddenButTypePreserved(t *testing.T) {
	// The primitive payload contents (cards with mockup descriptions, etc.)
	// bias the evaluator toward "aesthetic premature work" judgments, so we
	// strip them. But the `type` itself stays visible: the evaluator needs
	// to know the question is visual so it can give relevant guidance, and
	// rewriting type to 'choice' caused the evaluator to tell the agent
	// "use visual_choice!" when the agent was already using it.
	visualParams := map[string]any{
		"question": "Which layout?",
		"type":     "visual_choice",
		"visual":   map[string]any{"kind": "cards", "items": []any{}},
	}
	sanitized := paramsForSteeringEval("ask_user_question", visualParams)
	if _, ok := sanitized["visual"]; ok {
		t.Error("visual payload must be stripped")
	}
	if sanitized["type"] != "visual_choice" {
		t.Errorf("type must remain 'visual_choice', got %q", sanitized["type"])
	}
}
