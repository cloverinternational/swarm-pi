package autogenskills

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

func TestBuildSKILLSGuidance_DisabledMode(t *testing.T) {
	result := BuildSKILLSGuidance(ModeNever, nil)
	if result != "" {
		t.Errorf("expected empty string for ModeNever, got: %s", result)
	}
}

func TestBuildSKILLSGuidance_ManualMode(t *testing.T) {
	result := BuildSKILLSGuidance(ModeManual, nil)
	if !strings.Contains(result, "SkillManage") {
		t.Error("expected SkillManage mention in guidance")
	}
	if !strings.Contains(result, "create") {
		t.Error("expected 'create' action mention")
	}
	if !strings.Contains(result, "patch") {
		t.Error("expected 'patch' action mention")
	}
	if !strings.Contains(result, "view") {
		t.Error("expected 'view' action mention")
	}
	if !strings.Contains(result, "list") {
		t.Error("expected 'list' action mention")
	}
}

func TestBuildSKILLSGuidance_WithExistingSkills(t *testing.T) {
	result := BuildSKILLSGuidance(ModeAuto, []string{"git-workflow", "file-ops"})
	if !strings.Contains(result, "git-workflow") {
		t.Error("expected existing skill names in guidance")
	}
	if !strings.Contains(result, "file-ops") {
		t.Error("expected existing skill names in guidance")
	}
	if !strings.Contains(result, "duplicates") {
		t.Error("expected duplicate warning")
	}
}

func TestBuildSKILLSGuidance_NoExistingSkills(t *testing.T) {
	result := BuildSKILLSGuidance(ModeAuto, nil)
	if !strings.Contains(result, "No skills exist yet") {
		t.Error("expected 'no skills' message")
	}
}

func TestBuildSKILLSGuidance_Compact(t *testing.T) {
	result := BuildSKILLSGuidance(ModeAuto, []string{"a", "b", "c"})
	// The guidance teaches Hermes invocation model, multi-skill consultation,
	// and correct execution pattern — it is intentionally more detailed than the
	// old single-paragraph version. 3072 chars is a reasonable upper bound.
	if len(result) > 3072 {
		t.Errorf("guidance should be ≤3072 chars, got %d", len(result))
	}
}

func TestBuildAutogenSkillIndex_Empty(t *testing.T) {
	result := BuildAutogenSkillIndex(nil)
	if result != "" {
		t.Errorf("expected empty string for nil skills, got: %s", result)
	}
	result = BuildAutogenSkillIndex([]*skills.Skill{})
	if result != "" {
		t.Errorf("expected empty string for empty skills, got: %s", result)
	}
}

func TestBuildAutogenSkillIndex_NoAutogenSkills(t *testing.T) {
	allSkills := []*skills.Skill{
		{Metadata: skills.SkillMetadata{Name: "builtin-1"}, Source: "builtin"},
		{Metadata: skills.SkillMetadata{Name: "user-1"}, Source: "user"},
	}
	result := BuildAutogenSkillIndex(allSkills)
	if result != "" {
		t.Errorf("expected empty string for non-autogen skills, got: %s", result)
	}
}

func TestBuildAutogenSkillIndex_WithAutogenSkills(t *testing.T) {
	allSkills := []*skills.Skill{
		{Metadata: skills.SkillMetadata{Name: "builtin-1", Description: "Built-in"}, Source: "builtin"},
		{Metadata: skills.SkillMetadata{Name: "git-workflow", Description: "Git commit patterns", Version: "1.0.0"}, Source: "autogen"},
		{Metadata: skills.SkillMetadata{Name: "file-ops", Description: "File operations", Version: "1.0.1"}, Source: "autogen"},
	}
	result := BuildAutogenSkillIndex(allSkills)
	if !strings.Contains(result, "git-workflow") {
		t.Error("expected git-workflow in index")
	}
	if !strings.Contains(result, "v1.0.0") {
		t.Error("expected version in index")
	}
	if !strings.Contains(result, "file-ops") {
		t.Error("expected file-ops in index")
	}
	if strings.Contains(result, "builtin-1") {
		t.Error("should not include non-autogen skills")
	}
}

// TestBuildAutogenSkillIndex_Capped verifies the index is bounded: when more
// than maxAutogenIndexSkills autogen skills exist, only the cap is rendered and
// an omission marker records the remainder. This is the daemon-prompt safety
// net (mirrors Hermes' bounded skill index).
func TestBuildAutogenSkillIndex_Capped(t *testing.T) {
	var allSkills []*skills.Skill
	total := maxAutogenIndexSkills + 25
	for i := 0; i < total; i++ {
		allSkills = append(allSkills, &skills.Skill{
			Metadata: skills.SkillMetadata{
				Name:        fmt.Sprintf("skill-%03d", i),
				Description: "desc",
				Version:     "1.0.0",
			},
			Source: "autogen",
		})
	}
	result := BuildAutogenSkillIndex(allSkills)

	rendered := strings.Count(result, "- **skill-")
	if rendered != maxAutogenIndexSkills {
		t.Errorf("rendered %d skills, want cap %d", rendered, maxAutogenIndexSkills)
	}
	if !strings.Contains(result, "omitted to bound prompt size") {
		t.Errorf("expected omission marker, got:\n%s", result)
	}
	if !strings.Contains(result, "25 more") {
		t.Errorf("expected omitted count of 25, got:\n%s", result)
	}
}

// TestBuildAutogenSkillIndex_TruncatesDescription verifies long descriptions are
// truncated in the index (progressive disclosure — full text via the view action).
func TestBuildAutogenSkillIndex_TruncatesDescription(t *testing.T) {
	longDesc := strings.Repeat("x", maxAutogenDescChars+50)
	allSkills := []*skills.Skill{
		{Metadata: skills.SkillMetadata{Name: "verbose", Description: longDesc, Version: "1.0.0"}, Source: "autogen"},
	}
	result := BuildAutogenSkillIndex(allSkills)
	if strings.Contains(result, longDesc) {
		t.Error("expected description to be truncated, but full text was rendered")
	}
	if !strings.Contains(result, "…") {
		t.Errorf("expected ellipsis on truncated description, got:\n%s", result)
	}
}
