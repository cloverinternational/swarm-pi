package skills

import (
	"fmt"
	"strings"
	"testing"
)

// makeFakeSkills builds n fake *Skill with deterministic names
// skill-000, skill-001, ... skill-(n-1).
func makeFakeSkills(n int) []*Skill {
	skills := make([]*Skill, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("skill-%03d", i)
		skills = append(skills, &Skill{
			Metadata: SkillMetadata{
				Name:        name,
				Description: "desc for " + name,
			},
			Path: "/tmp/skills/" + name,
		})
	}
	return skills
}

const omissionMarkerPrefix = "<!--"

func countSkillBlocks(xml string) int {
	return strings.Count(xml, "<skill>")
}

func TestGenerateAvailableSkillsXMLUnderCapNoMarker(t *testing.T) {
	skills := makeFakeSkills(MaxAvailableSkillsDefault) // exactly at cap
	out := GenerateAvailableSkillsXML(skills)

	if got := countSkillBlocks(out); got != MaxAvailableSkillsDefault {
		t.Fatalf("expected %d skill blocks, got %d", MaxAvailableSkillsDefault, got)
	}
	if strings.Contains(out, "omitted to bound prompt size") {
		t.Fatalf("did not expect omission marker when at/under cap; output:\n%s", out)
	}
}

func TestGenerateAvailableSkillsXMLBelowCapNoMarker(t *testing.T) {
	skills := makeFakeSkills(5)
	out := GenerateAvailableSkillsXML(skills)

	if got := countSkillBlocks(out); got != 5 {
		t.Fatalf("expected 5 skill blocks, got %d", got)
	}
	if strings.Contains(out, "omitted to bound prompt size") {
		t.Fatalf("did not expect omission marker below cap; output:\n%s", out)
	}
}

func TestGenerateAvailableSkillsXMLOverCapMarker(t *testing.T) {
	total := MaxAvailableSkillsDefault + 7
	skills := makeFakeSkills(total)
	out := GenerateAvailableSkillsXML(skills)

	if got := countSkillBlocks(out); got != MaxAvailableSkillsDefault {
		t.Fatalf("expected exactly %d skill blocks, got %d", MaxAvailableSkillsDefault, got)
	}

	wantMarker := fmt.Sprintf("<!-- %d additional skill(s) omitted to bound prompt size", total-MaxAvailableSkillsDefault)
	if !strings.Contains(out, wantMarker) {
		t.Fatalf("expected omission marker %q in output:\n%s", wantMarker, out)
	}
}

func TestGenerateAvailableSkillsXMLCappedNoCap(t *testing.T) {
	skills := makeFakeSkills(100)

	for _, maxSkills := range []int{0, -1} {
		out := GenerateAvailableSkillsXMLCapped(skills, maxSkills)
		if got := countSkillBlocks(out); got != 100 {
			t.Fatalf("maxSkills=%d: expected all 100 skill blocks, got %d", maxSkills, got)
		}
		if strings.Contains(out, "omitted to bound prompt size") {
			t.Fatalf("maxSkills=%d: did not expect omission marker with no cap; output:\n%s", maxSkills, out)
		}
	}
}

func TestGenerateAvailableSkillsXMLCappedCustomCap(t *testing.T) {
	skills := makeFakeSkills(20)
	out := GenerateAvailableSkillsXMLCapped(skills, 8)

	if got := countSkillBlocks(out); got != 8 {
		t.Fatalf("expected 8 skill blocks, got %d", got)
	}
	if !strings.Contains(out, "<!-- 12 additional skill(s) omitted") {
		t.Fatalf("expected '12 additional' omission marker; output:\n%s", out)
	}
}

func TestGenerateAvailableSkillsXMLCappedEmpty(t *testing.T) {
	if out := GenerateAvailableSkillsXML(nil); out != "" {
		t.Fatalf("expected empty string for nil input, got %q", out)
	}
	if out := GenerateAvailableSkillsXML([]*Skill{}); out != "" {
		t.Fatalf("expected empty string for empty slice, got %q", out)
	}
	if out := GenerateAvailableSkillsXMLCapped(nil, 5); out != "" {
		t.Fatalf("expected empty string for nil capped input, got %q", out)
	}
}

func TestGenerateAvailableSkillsXMLDeterministicAlphabetical(t *testing.T) {
	// Provide names out of order; expect alphabetical rendering and a stable result.
	skills := []*Skill{
		{Metadata: SkillMetadata{Name: "zebra", Description: "z"}, Path: "/p/zebra"},
		{Metadata: SkillMetadata{Name: "alpha", Description: "a"}, Path: "/p/alpha"},
		{Metadata: SkillMetadata{Name: "mike", Description: "m"}, Path: "/p/mike"},
	}

	out := GenerateAvailableSkillsXML(skills)

	idxAlpha := strings.Index(out, "<name>alpha</name>")
	idxMike := strings.Index(out, "<name>mike</name>")
	idxZebra := strings.Index(out, "<name>zebra</name>")

	if idxAlpha < 0 || idxMike < 0 || idxZebra < 0 {
		t.Fatalf("expected all names present; output:\n%s", out)
	}
	if !(idxAlpha < idxMike && idxMike < idxZebra) {
		t.Fatalf("expected alphabetical ordering alpha<mike<zebra; got positions %d, %d, %d", idxAlpha, idxMike, idxZebra)
	}

	// Determinism: same input (even if reordered) yields identical output.
	reordered := []*Skill{skills[2], skills[0], skills[1]}
	if got := GenerateAvailableSkillsXML(reordered); got != out {
		t.Fatalf("expected deterministic output regardless of input order\nfirst:\n%s\nsecond:\n%s", out, got)
	}
}

func TestGenerateAvailableSkillsXMLCappedPrefixAvailable(t *testing.T) {
	// Sanity: marker uses the comment prefix and block stays well-formed.
	out := GenerateAvailableSkillsXMLCapped(makeFakeSkills(3), 1)
	if !strings.HasPrefix(out, "<available_skills>\n") {
		t.Fatalf("expected opening tag; output:\n%s", out)
	}
	if !strings.HasSuffix(out, "</available_skills>") {
		t.Fatalf("expected closing tag; output:\n%s", out)
	}
	if !strings.Contains(out, omissionMarkerPrefix) {
		t.Fatalf("expected comment marker; output:\n%s", out)
	}
}

func TestGeneratePromptContextPreservesRankedOrder(t *testing.T) {
	ranked := []*Skill{
		{Metadata: SkillMetadata{Name: "z-relevant", Description: "best match"}, Path: "/p/z"},
		{Metadata: SkillMetadata{Name: "a-less-relevant", Description: "weaker match"}, Path: "/p/a"},
	}

	out := GeneratePromptContext(ranked, nil).AvailableSkillsXML
	relevant := strings.Index(out, "<name>z-relevant</name>")
	lessRelevant := strings.Index(out, "<name>a-less-relevant</name>")
	if relevant < 0 || lessRelevant < 0 || relevant >= lessRelevant {
		t.Fatalf("ranked order was not preserved; output:\n%s", out)
	}
}

func TestGenerateRankedAvailableSkillsXMLHonorsByteBudget(t *testing.T) {
	skills := makeFakeSkills(20)
	for _, skill := range skills {
		skill.Metadata.Description = strings.Repeat("long description ", 40)
	}

	out := GenerateRankedAvailableSkillsXML(skills, 20, 1_600)
	if len(out) > 1_800 {
		t.Fatalf("ranked catalog exceeded bounded budget: got %d chars", len(out))
	}
	if got := countSkillBlocks(out); got == 0 || got >= len(skills) {
		t.Fatalf("expected a non-empty truncated catalog, got %d entries", got)
	}
	if !strings.Contains(out, `SkillManage(action="list")`) {
		t.Fatalf("expected on-demand discovery marker; output:\n%s", out)
	}
}

func TestGenerateRankedAvailableSkillsXMLTruncatesDescriptionWithoutMutation(t *testing.T) {
	original := strings.Repeat("界", MaxPromptDescriptionRunesDefault+20)
	skill := &Skill{
		Metadata: SkillMetadata{Name: "unicode", Description: original},
		Path:     "/p/unicode",
	}

	out := GenerateRankedAvailableSkillsXML([]*Skill{skill}, 1, 10_000)
	if !strings.Contains(out, "…</description>") {
		t.Fatalf("expected rune-safe description truncation; output:\n%s", out)
	}
	if skill.Metadata.Description != original {
		t.Fatal("prompt rendering mutated canonical metadata")
	}
}
