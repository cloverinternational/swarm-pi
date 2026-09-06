package chat

import (
	"strings"
	"testing"
)

func TestGoalEvaluatorPromptRequiresObservableEvidence(t *testing.T) {
	prompt := goalEvaluatorSystemPrompt()
	for _, required := range []string{
		"Assistant claims are not evidence",
		"help, usage, error, or log output",
		"observable requested state",
		"NOT_MET",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("goal evaluator prompt missing %q", required)
		}
	}
}

func TestParseGoalVerdictRejectsNotMetBeforeMet(t *testing.T) {
	result := parseGoalVerdict("NOT_MET: only help text showed the requested flag")
	if result.Ok || result.Impossible {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !strings.Contains(result.Reason, "help text") {
		t.Fatalf("reason lost: %+v", result)
	}
}

func TestParseGoalVerdictAcceptsObservableMet(t *testing.T) {
	result := parseGoalVerdict("MET: tests passed and the requested state is visible")
	if !result.Ok || result.Impossible {
		t.Fatalf("unexpected result: %+v", result)
	}
}
