package agent

import (
	"strings"
	"testing"
)

func TestParseFindingsEvaluationsRequiresEveryFinding(t *testing.T) {
	summaries := []FindingSummary{
		{ID: "finding-a", ToolName: "Bash"},
		{ID: "finding-b", ToolName: "Read"},
	}

	_, err := parseFindingsEvaluations(`[{"finding_id":"finding-a","priority":81.23,"should_promote":true,"reasoning":"useful"}]`, summaries)
	if err == nil {
		t.Fatal("expected missing evaluation error")
	}
	if !strings.Contains(err.Error(), "expected 2 evaluations") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFindingsEvaluationsRejectsUnknownID(t *testing.T) {
	summaries := []FindingSummary{{ID: "finding-a", ToolName: "Bash"}}

	_, err := parseFindingsEvaluations(`[{"finding_id":"other","priority":81.23,"should_promote":true,"reasoning":"useful"}]`, summaries)
	if err == nil {
		t.Fatal("expected unknown id error")
	}
	if !strings.Contains(err.Error(), "unknown finding_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFindingsEvaluationsAllowsSingleObjectForSingleFinding(t *testing.T) {
	summaries := []FindingSummary{{ID: "finding-a", ToolName: "Bash"}}

	evals, err := parseFindingsEvaluations(`{"finding_id":"finding-a","priority":81.234,"should_promote":false,"reasoning":"useful"}`, summaries)
	if err != nil {
		t.Fatalf("parseFindingsEvaluations: %v", err)
	}
	if got := len(evals); got != 1 {
		t.Fatalf("evaluations: got %d, want 1", got)
	}
	if got := evals[0].Priority; got != 81.23 {
		t.Fatalf("priority: got %.2f, want 81.23", got)
	}
	if !evals[0].ShouldPromote {
		t.Fatal("should_promote should be recomputed from priority")
	}
}

func TestFallbackFindingsEvaluationsPromotesFailedFindings(t *testing.T) {
	evals := fallbackFindingsEvaluations([]FindingSummary{
		{
			ID:              "finding-a",
			ToolName:        "Bash",
			Success:         false,
			ContextSummary:  "Shell: make test",
			InitialPriority: 50,
			ErrorMessage:    "test failed",
			Tags:            []string{"error", "needs-attention"},
		},
	}, nil)

	if got := len(evals); got != 1 {
		t.Fatalf("evaluations: got %d, want 1", got)
	}
	if !evals[0].ShouldPromote {
		t.Fatal("failed finding should be promoted by fallback")
	}
	if evals[0].Priority < 80 {
		t.Fatalf("priority: got %.2f, want >=80", evals[0].Priority)
	}
	if evals[0].Source != "fallback" {
		t.Fatalf("source: got %q, want fallback", evals[0].Source)
	}
}
