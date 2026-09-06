package skills

import (
	"strings"
	"testing"
)

func TestRankForContextPrioritizesActiveExactAndSemanticMatches(t *testing.T) {
	mk := func(name, description string, priority int) *Skill {
		return &Skill{Metadata: SkillMetadata{Name: name, Description: description, Priority: priority}}
	}
	active := mk("active-skill", "unrelated", 0)
	exact := mk("oauth-debug", "Diagnose OAuth refresh failures", 0)
	semantic := mk("auth-helper", "Authentication and token debugging", 0)
	highPriority := mk("generic", "general utility", 100)

	got := RankForContext([]*Skill{highPriority, semantic, exact, active}, []*Skill{active}, "debug oauth token refresh")
	want := []string{"active-skill", "oauth-debug", "auth-helper", "generic"}
	for i, name := range want {
		if got[i].Metadata.Name != name {
			t.Fatalf("rank[%d]=%q, want %q", i, got[i].Metadata.Name, name)
		}
	}
}

func TestRankForContextDeterministicTieBreak(t *testing.T) {
	a := &Skill{Metadata: SkillMetadata{Name: "a", Description: "same"}}
	b := &Skill{Metadata: SkillMetadata{Name: "b", Description: "same"}}
	got := RankForContext([]*Skill{b, a}, nil, "same")
	if got[0].Metadata.Name != "a" || got[1].Metadata.Name != "b" {
		t.Fatalf("unexpected deterministic order: %s, %s", got[0].Metadata.Name, got[1].Metadata.Name)
	}
}

// Relevance must not be driven by description text the ranked catalog truncates
// away. Scoring the full description while rendering only the first
// MaxPromptDescriptionRunesDefault runes lets a skill win on vocabulary the
// model never sees, which yields confident wrong selections instead of misses.
func TestSkillRelevanceIgnoresDescriptionBeyondPromptTruncation(t *testing.T) {
	filler := strings.Repeat("padding ", MaxPromptDescriptionRunesDefault)
	hidden := &Skill{Metadata: SkillMetadata{Name: "hidden", Description: filler + " kubernetes"}}
	visible := &Skill{Metadata: SkillMetadata{Name: "visible", Description: "kubernetes deployment help"}}

	query := "kubernetes"
	terms := relevanceTerms(query)
	hiddenScore := skillRelevanceScore(hidden, query, terms)
	visibleScore := skillRelevanceScore(visible, query, terms)

	if hiddenScore != 0 {
		t.Fatalf("term past the prompt truncation boundary scored %d, want 0", hiddenScore)
	}
	if visibleScore <= hiddenScore {
		t.Fatalf("visible match scored %d, must beat hidden %d", visibleScore, hiddenScore)
	}
	if got := RankForContext([]*Skill{hidden, visible}, nil, query); got[0].Metadata.Name != "visible" {
		t.Fatalf("rank[0]=%q, want %q", got[0].Metadata.Name, "visible")
	}
}
