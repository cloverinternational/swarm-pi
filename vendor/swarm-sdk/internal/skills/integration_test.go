// Package skills integration tests for the complete skills system.
// These tests verify end-to-end functionality of skill loading, validation,
// XML generation, and activation/deactivation flows.
package skills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIntegrationLoadAndValidate tests loading a skill and validating its metadata.
func TestIntegrationLoadAndValidate(t *testing.T) {
	// Create a temporary skill directory
	tmpDir, err := os.MkdirTemp("", "skills-integration-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test skill directory
	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	// Write a valid SKILL.md file
	skillContent := `---
name: test-skill
description: A test skill for integration testing
version: 1.0.0
author: Test Author
category: testing
tags:
  - test
  - integration
license: MIT
---

# Test Skill

This is a test skill used for integration testing.

## Instructions

When activated, follow these instructions:
1. Log all operations
2. Report success

## Notes

This skill is for testing purposes only.
`
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(skillContent), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	// Test loading the skill
	skill, err := LoadSkill(skillDir)
	if err != nil {
		t.Fatalf("LoadSkill failed: %v", err)
	}

	// Validate loaded skill metadata
	if skill.Metadata.Name != "test-skill" {
		t.Errorf("Expected name 'test-skill', got %q", skill.Metadata.Name)
	}
	if skill.Metadata.Description != "A test skill for integration testing" {
		t.Errorf("Unexpected description: %q", skill.Metadata.Description)
	}
	if skill.Metadata.Version != "1.0.0" {
		t.Errorf("Expected version '1.0.0', got %q", skill.Metadata.Version)
	}
	if skill.Metadata.Author != "Test Author" {
		t.Errorf("Expected author 'Test Author', got %q", skill.Metadata.Author)
	}
	if skill.Metadata.Category != "testing" {
		t.Errorf("Expected category 'testing', got %q", skill.Metadata.Category)
	}
	if skill.Metadata.License != "MIT" {
		t.Errorf("Expected license 'MIT', got %q", skill.Metadata.License)
	}

	// Validate instructions were parsed
	if skill.Instructions == "" {
		t.Error("Expected instructions to be non-empty")
	}
	if !strings.Contains(skill.Instructions, "Test Skill") {
		t.Error("Instructions should contain 'Test Skill'")
	}

	// Test validation
	result := ValidateSkill(skill)
	if !result.Valid {
		t.Errorf("Expected skill to be valid, got errors: %v", result.AllErrors())
	}
}

// TestIntegrationValidateMetadata tests comprehensive metadata validation.
func TestIntegrationValidateMetadata(t *testing.T) {
	testCases := []struct {
		name        string
		metadata    SkillMetadata
		expectValid bool
		errorField  string
	}{
		{
			name: "valid minimal metadata",
			metadata: SkillMetadata{
				Name:        "valid-skill",
				Description: "A valid skill description",
			},
			expectValid: true,
		},
		{
			name: "valid full metadata",
			metadata: SkillMetadata{
				Name:          "full-skill",
				Description:   "A complete skill with all fields",
				Version:       "2.0.0",
				Author:        "Full Author",
				Category:      "development",
				Tags:          []string{"tag1", "tag2"},
				License:       "Apache-2.0",
				Compatibility: ">=1.0.0",
				AllowedTools:  "bash grep file_edit",
			},
			expectValid: true,
		},
		{
			name: "missing name",
			metadata: SkillMetadata{
				Description: "A skill without a name",
			},
			expectValid: false,
			errorField:  "name",
		},
		{
			name: "missing description",
			metadata: SkillMetadata{
				Name: "no-desc",
			},
			expectValid: false,
			errorField:  "description",
		},
		{
			name: "invalid name format - uppercase",
			metadata: SkillMetadata{
				Name:        "InvalidName",
				Description: "Has uppercase letters",
			},
			expectValid: false,
			errorField:  "name",
		},
		{
			name: "invalid name format - starts with hyphen",
			metadata: SkillMetadata{
				Name:        "-invalid",
				Description: "Starts with hyphen",
			},
			expectValid: false,
			errorField:  "name",
		},
		{
			name: "invalid name format - consecutive hyphens",
			metadata: SkillMetadata{
				Name:        "bad--name",
				Description: "Has consecutive hyphens",
			},
			expectValid: false,
			errorField:  "name",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			skill := &Skill{
				Metadata: tc.metadata,
				Path:     "/test/path",
			}
			result := ValidateSkill(skill)

			if tc.expectValid && !result.Valid {
				t.Errorf("Expected valid, got errors: %v", result.AllErrors())
			}
			if !tc.expectValid && result.Valid {
				t.Error("Expected invalid, but validation passed")
			}
			if !tc.expectValid && tc.errorField != "" {
				found := false
				for _, err := range result.Errors {
					if err.Field == tc.errorField {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected error in field %q, got errors: %v", tc.errorField, result.Errors)
				}
			}
		})
	}
}

// TestIntegrationXMLGeneration tests XML output generation.
func TestIntegrationXMLGeneration(t *testing.T) {
	// Create test skills
	skills := []*Skill{
		{
			Metadata: SkillMetadata{
				Name:        "alpha-skill",
				Description: "First skill alphabetically",
				Version:     "1.0.0",
				Priority:    10,
			},
			Path:         "/skills/alpha-skill",
			Instructions: "Alpha instructions here",
		},
		{
			Metadata: SkillMetadata{
				Name:        "beta-skill",
				Description: "Second skill alphabetically",
				Version:     "2.0.0",
				Author:      "Beta Author",
				Category:    "testing",
				Tags:        []string{"test", "beta"},
				License:     "MIT",
				Priority:    5,
			},
			Path:         "/skills/beta-skill",
			Instructions: "Beta instructions here",
		},
	}

	// Test GenerateAvailableSkillsXML
	availableXML := GenerateAvailableSkillsXML(skills)
	if availableXML == "" {
		t.Error("GenerateAvailableSkillsXML returned empty string")
	}
	if !strings.Contains(availableXML, "<available_skills>") {
		t.Error("Expected <available_skills> tag")
	}
	if !strings.Contains(availableXML, "alpha-skill") {
		t.Error("Expected alpha-skill in output")
	}
	if !strings.Contains(availableXML, "beta-skill") {
		t.Error("Expected beta-skill in output")
	}

	// Verify sorting (alpha should come before beta)
	alphaIdx := strings.Index(availableXML, "alpha-skill")
	betaIdx := strings.Index(availableXML, "beta-skill")
	if alphaIdx > betaIdx {
		t.Error("Skills should be sorted alphabetically")
	}

	// Test GenerateActiveSkillsXML — deprecated, always returns ""
	activeXML := GenerateActiveSkillsXML(skills)
	if activeXML != "" {
		t.Errorf("GenerateActiveSkillsXML should return empty string (deprecated no-op), got: %q", activeXML)
	}

	// Test GenerateSkillXMLWithDetails
	detailsXML := GenerateSkillXMLWithDetails(skills[1])
	if detailsXML == "" {
		t.Error("GenerateSkillXMLWithDetails returned empty string")
	}
	if !strings.Contains(detailsXML, "<version>") {
		t.Error("Expected <version> tag in detailed XML")
	}
	if !strings.Contains(detailsXML, "<author>") {
		t.Error("Expected <author> tag in detailed XML")
	}
	if !strings.Contains(detailsXML, "<category>") {
		t.Error("Expected <category> tag in detailed XML")
	}
	if !strings.Contains(detailsXML, "<license>") {
		t.Error("Expected <license> tag in detailed XML")
	}

	// Test GenerateSkillInstructionsXML
	instructionsXML := GenerateSkillInstructionsXML(skills[0])
	if instructionsXML == "" {
		t.Error("GenerateSkillInstructionsXML returned empty string")
	}
	if !strings.Contains(instructionsXML, "skill_instructions") {
		t.Error("Expected skill_instructions tag")
	}
	if !strings.Contains(instructionsXML, "Alpha instructions here") {
		t.Error("Expected instructions content")
	}

	// Test GenerateCombinedInstructionsXML — deprecated, always returns ""
	combinedXML := GenerateCombinedInstructionsXML(skills)
	if combinedXML != "" {
		t.Errorf("GenerateCombinedInstructionsXML should return empty string (deprecated no-op), got: %q", combinedXML)
	}

	// Test GeneratePromptContext
	ctx := GeneratePromptContext(skills, skills[:1])
	if ctx == nil {
		t.Fatal("GeneratePromptContext returned nil")
	}
	if ctx.AvailableSkillsXML == "" {
		t.Error("Expected non-empty AvailableSkillsXML")
	}
	// ActiveSkillsXML and InstructionsXML are deprecated no-ops — always "".
	if ctx.ActiveSkillsXML != "" {
		t.Errorf("ActiveSkillsXML should be empty (deprecated), got: %q", ctx.ActiveSkillsXML)
	}
	if ctx.InstructionsXML != "" {
		t.Errorf("InstructionsXML should be empty (deprecated), got: %q", ctx.InstructionsXML)
	}

	// Test context String() method
	contextStr := ctx.String()
	if contextStr == "" {
		t.Error("Context String() returned empty")
	}
}

// TestIntegrationRegistryActivation tests that activation is a no-op in the
// Claude-style skill model. Skills remain discoverable via Get/List but
// Activate/Deactivate/IsActive/GetActive/GetInstructions are deprecated no-ops.
func TestIntegrationRegistryActivation(t *testing.T) {
	// Create a temporary skills directory
	tmpDir, err := os.MkdirTemp("", "skills-registry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create two test skills
	skill1Dir := filepath.Join(tmpDir, "skill-one")
	skill2Dir := filepath.Join(tmpDir, "skill-two")

	for _, dir := range []string{skill1Dir, skill2Dir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create skill dir %s: %v", dir, err)
		}
	}

	skill1Content := `---
name: skill-one
description: First test skill
---
Skill one instructions
`
	skill2Content := `---
name: skill-two
description: Second test skill
---
Skill two instructions
`
	if err := os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte(skill1Content), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skill2Dir, "SKILL.md"), []byte(skill2Content), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	// Create registry and load skills
	registry := NewRegistry()
	registry.AddSearchPath(tmpDir)
	_, err = registry.DiscoverAll()
	if err != nil {
		t.Fatalf("DiscoverAll failed: %v", err)
	}

	// Activation is a no-op — IsActive always returns false
	if registry.IsActive("skill-one") {
		t.Error("IsActive should always return false in Claude-style model")
	}

	// Activate returns nil for existing skills (existence check preserved)
	if err := registry.Activate("skill-one"); err != nil {
		t.Errorf("Activate existing skill should not error: %v", err)
	}

	// IsActive still returns false even after Activate
	if registry.IsActive("skill-one") {
		t.Error("IsActive should still return false after Activate (no-op)")
	}

	// GetActive always returns nil
	if active := registry.GetActive(); active != nil {
		t.Errorf("GetActive should return nil, got %d skills", len(active))
	}

	// GetInstructions always returns ""
	if instructions := registry.GetInstructions(); instructions != "" {
		t.Errorf("GetInstructions should return empty string, got: %q", instructions)
	}

	// Deactivate is always a no-op
	if err := registry.Deactivate("skill-one"); err != nil {
		t.Errorf("Deactivate should not error: %v", err)
	}

	// Activating non-existent skill still returns error (existence check)
	if err := registry.Activate("non-existent"); err == nil {
		t.Error("Expected error when activating non-existent skill")
	}

	// Get and List still work as expected
	got, found := registry.Get("skill-one")
	if !found {
		t.Error("skill-one should be found via Get")
	}
	if got.Metadata.Name != "skill-one" {
		t.Error("Wrong skill returned")
	}
	allSkills := registry.List()
	if len(allSkills) != 2 {
		t.Errorf("Expected 2 skills in registry, got %d", len(allSkills))
	}
}

// TestIntegrationLoaderWorkflow tests the complete Loader workflow.
// Activation methods are deprecated no-ops; discovery and Get/List still work.
func TestIntegrationLoaderWorkflow(t *testing.T) {
	// Create a temporary skills directory
	tmpDir, err := os.MkdirTemp("", "skills-loader-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create two test skills
	skill1Dir := filepath.Join(tmpDir, "loader-skill-1")
	skill2Dir := filepath.Join(tmpDir, "loader-skill-2")

	for _, dir := range []string{skill1Dir, skill2Dir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create skill dir %s: %v", dir, err)
		}
	}

	skill1Content := `---
name: loader-skill-1
description: First loader test skill
version: 1.0.0
---

# Loader Skill 1

Test instructions for loader skill 1.
`
	skill2Content := `---
name: loader-skill-2
description: Second loader test skill
version: 1.0.0
---

# Loader Skill 2

Test instructions for loader skill 2.
`
	if err := os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte(skill1Content), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md for skill 1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skill2Dir, "SKILL.md"), []byte(skill2Content), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md for skill 2: %v", err)
	}

	// Create and initialize loader
	loader := NewLoader(tmpDir)
	ctx := context.Background()

	if err := loader.Initialize(ctx); err != nil {
		t.Fatalf("Loader.Initialize failed: %v", err)
	}

	// Verify skills were discovered.
	// The loader also seeds built-in default skills, so total count is at least 2.
	skills := loader.List()
	if len(skills) < 2 {
		t.Errorf("Expected at least 2 skills, got %d", len(skills))
	}
	// Confirm the two user skills are present by name.
	foundSkill1, foundSkill2 := false, false
	for _, s := range skills {
		switch s.Metadata.Name {
		case "loader-skill-1":
			foundSkill1 = true
		case "loader-skill-2":
			foundSkill2 = true
		}
	}
	if !foundSkill1 {
		t.Error("Expected loader-skill-1 to be discovered")
	}
	if !foundSkill2 {
		t.Error("Expected loader-skill-2 to be discovered")
	}

	// Activate is a no-op — returns nil for existing skills, no state change.
	if err := loader.Activate("loader-skill-1"); err != nil {
		t.Errorf("Loader.Activate should not error for existing skill: %v", err)
	}
	// GetActiveSkills always returns nil in Claude-style model.
	if active := loader.GetActiveSkills(); active != nil {
		t.Errorf("GetActiveSkills should return nil, got %d", len(active))
	}
	// GetActiveInstructions always returns "".
	if instr := loader.GetActiveInstructions(); instr != "" {
		t.Errorf("GetActiveInstructions should return empty string, got: %q", instr)
	}
	// Deactivate is always a no-op.
	if err := loader.Deactivate("loader-skill-1"); err != nil {
		t.Errorf("Loader.Deactivate should not error: %v", err)
	}
}

// TestIntegrationXMLEscaping tests that special characters are properly escaped.
func TestIntegrationXMLEscaping(t *testing.T) {
	skill := &Skill{
		Metadata: SkillMetadata{
			Name:        "escaping-test",
			Description: "Test <special> & \"characters\" in 'XML'",
		},
		Path:         "/test/escaping",
		Instructions: "Handle <html> & XML \"entities\" properly",
	}

	xml := GenerateSkillXML(skill)

	// Verify special characters are escaped
	if strings.Contains(xml, "<special>") {
		t.Error("< should be escaped in XML output")
	}
	if strings.Contains(xml, "& \"") {
		t.Error("& should be escaped in XML output")
	}

	// Verify the escaped versions are present
	if !strings.Contains(xml, "&lt;") && !strings.Contains(xml, "&#") {
		t.Log("Warning: < may not be properly escaped")
	}
	if !strings.Contains(xml, "&amp;") {
		t.Log("Warning: & may not be properly escaped")
	}
}

// TestIntegrationSanitizeName tests the name sanitization function.
func TestIntegrationSanitizeName(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"My Cool Skill", "my-cool-skill"},
		{"TEST_SKILL", "test-skill"},
		{"already-valid", "already-valid"},
		{"--leading-hyphens--", "leading-hyphens"},
		{"Multiple   Spaces", "multiple-spaces"},
		{"Special@#$Characters!", "specialcharacters"},
		{"Mixed_Case-And_Stuff", "mixed-case-and-stuff"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := SanitizeName(tc.input)
			if result != tc.expected {
				t.Errorf("SanitizeName(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

// TestIntegrationEmptyInputs tests handling of empty/nil inputs.
// TestIntegrationClassifySkillSource tests that classifySkillSource correctly
// identifies autogen skills based on their directory path, even when they're
// discovered via a broader user-level search path.
func TestIntegrationClassifySkillSource(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	tests := []struct {
		name       string
		skillPath  string
		wantSource string
	}{
		{
			name:       "autogen skill under ~/.swarm/skills/autogen",
			skillPath:  filepath.Join(homeDir, ".swarm", "skills", "autogen", "my-skill"),
			wantSource: "autogen",
		},
		{
			name:       "autogen skill under ~/.swarmos/skills/autogen",
			skillPath:  filepath.Join(homeDir, ".swarmos", "skills", "autogen", "my-skill"),
			wantSource: "autogen",
		},
		{
			name:       "regular skill under ~/.swarm/skills",
			skillPath:  filepath.Join(homeDir, ".swarm", "skills", "forge"),
			wantSource: "", // not autogen; falls back to classifySource
		},
		{
			name:       "regular skill under ~/.claude/skills",
			skillPath:  filepath.Join(homeDir, ".claude", "skills", "some-skill"),
			wantSource: "",
		},
		{
			name:       "empty path",
			skillPath:  "",
			wantSource: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifySkillSource(tt.skillPath, homeDir)
			if got != tt.wantSource {
				t.Errorf("classifySkillSource(%q, homeDir) = %q, want %q", tt.skillPath, got, tt.wantSource)
			}
		})
	}
}

// TestIntegrationClassifySource_AutogenDir tests that classifySource correctly
// returns "autogen" when the search path is the autogen directory.
func TestIntegrationClassifySource_AutogenDir(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	tests := []struct {
		name       string
		searchPath string
		wantSource string
	}{
		{
			name:       "autogen search path ~/.swarm/skills/autogen",
			searchPath: filepath.Join(homeDir, ".swarm", "skills", "autogen"),
			wantSource: "autogen",
		},
		{
			name:       "autogen search path ~/.swarmos/skills/autogen",
			searchPath: filepath.Join(homeDir, ".swarmos", "skills", "autogen"),
			wantSource: "autogen",
		},
		{
			name:       "user search path ~/.swarm/skills",
			searchPath: filepath.Join(homeDir, ".swarm", "skills"),
			wantSource: "user",
		},
		{
			name:       "user search path ~/.swarmos/skills",
			searchPath: filepath.Join(homeDir, ".swarmos", "skills"),
			wantSource: "user",
		},
		{
			name:       "user search path ~/.claude/skills",
			searchPath: filepath.Join(homeDir, ".claude", "skills"),
			wantSource: "user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifySource(tt.searchPath, homeDir)
			if got != tt.wantSource {
				t.Errorf("classifySource(%q, homeDir) = %q, want %q", tt.searchPath, got, tt.wantSource)
			}
		})
	}
}

// TestIntegrationDiscoverAll_AutogenSourceClassification tests that when
// DiscoverAll finds skills under ~/.swarm/skills/autogen/ via the broader
// ~/.swarm/skills search path, those skills get Source="autogen" (not "user").
func TestIntegrationDiscoverAll_AutogenSourceClassification(t *testing.T) {
	// Simulate the directory structure:
	//   tmpDir/
	//     skills/          ← search path (like ~/.swarm/skills)
	//       autogen/       ← autogen subdir
	//         my-skill/
	//           SKILL.md
	//       regular-skill/
	//         SKILL.md
	tmpDir, err := os.MkdirTemp("", "autogen-classify-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	skillsDir := filepath.Join(tmpDir, "skills")
	autogenDir := filepath.Join(skillsDir, "autogen")
	regularDir := filepath.Join(skillsDir, "regular-skill")
	autogenSkillDir := filepath.Join(autogenDir, "my-autogen-skill")

	for _, dir := range []string{autogenSkillDir, regularDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create dir %s: %v", dir, err)
		}
	}

	autogenContent := `---
name: my-autogen-skill
description: An autogenerated skill
---

Autogen instructions.
`
	regularContent := `---
name: regular-skill
description: A regular skill
---

Regular instructions.
`

	if err := os.WriteFile(filepath.Join(autogenSkillDir, "SKILL.md"), []byte(autogenContent), 0644); err != nil {
		t.Fatalf("Failed to write autogen SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(regularDir, "SKILL.md"), []byte(regularContent), 0644); err != nil {
		t.Fatalf("Failed to write regular SKILL.md: %v", err)
	}

	// Create a registry and add the skills directory as a search path.
	// This simulates how the main loader adds ~/.swarm/skills as a search path
	// and discovers both regular and autogen skills from it.
	registry := NewRegistry()
	registry.AddSearchPath(skillsDir)

	// We need to set up a fake homeDir that contains our tmpDir so
	// classifySkillSource can detect the autogen pattern.
	// Since classifySkillSource uses filepath.HasPrefix against homeDir-based
	// patterns, we create a custom scenario by using the tmpDir as the base.
	//
	// Instead, let's just test the helper functions directly.

	// Verify classifySkillSource identifies the autogen skill path.
	// We need a homeDir-like structure. Set up the temp dir as:
	//   tmpDir/
	//     .swarm/skills/autogen/my-autogen-skill/SKILL.md
	//     .swarm/skills/regular-skill/SKILL.md
	homeLikeDir := filepath.Join(tmpDir, "home")
	swarmSkillsDir := filepath.Join(homeLikeDir, ".swarm", "skills")
	autogenLikeDir := filepath.Join(swarmSkillsDir, "autogen")
	autogenSkillLikeDir := filepath.Join(autogenLikeDir, "my-autogen-skill")
	regularLikeDir := filepath.Join(swarmSkillsDir, "regular-skill")

	for _, dir := range []string{autogenSkillLikeDir, regularLikeDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create dir %s: %v", dir, err)
		}
	}

	if err := os.WriteFile(filepath.Join(autogenSkillLikeDir, "SKILL.md"), []byte(autogenContent), 0644); err != nil {
		t.Fatalf("Failed to write autogen SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(regularLikeDir, "SKILL.md"), []byte(regularContent), 0644); err != nil {
		t.Fatalf("Failed to write regular SKILL.md: %v", err)
	}

	// Test classifySkillSource with the home-like directory
	got := classifySkillSource(autogenSkillLikeDir, homeLikeDir)
	if got != "autogen" {
		t.Errorf("classifySkillSource(autogen skill, homeLikeDir) = %q, want \"autogen\"", got)
	}

	got = classifySkillSource(regularLikeDir, homeLikeDir)
	if got != "" {
		t.Errorf("classifySkillSource(regular skill, homeLikeDir) = %q, want \"\"", got)
	}

	// Test classifySource with the autogen search path
	got = classifySource(autogenLikeDir, homeLikeDir)
	if got != "autogen" {
		t.Errorf("classifySource(autogen dir, homeLikeDir) = %q, want \"autogen\"", got)
	}

	// Test that the user-level search path still returns "user"
	got = classifySource(swarmSkillsDir, homeLikeDir)
	if got != "user" {
		t.Errorf("classifySource(swarm skills dir, homeLikeDir) = %q, want \"user\"", got)
	}

	// Now test the full DiscoverAll flow with the home-like directory.
	// DiscoverAll classifies sources against os.UserHomeDir(), which honors
	// $HOME on Unix — point it at our fake home so the autogen/user patterns
	// under homeLikeDir are recognized instead of being treated as "project".
	t.Setenv("HOME", homeLikeDir)
	registry2 := NewRegistry()
	registry2.AddSearchPath(swarmSkillsDir)
	_, _ = registry2.DiscoverAll()

	// Check that the autogen skill got Source="autogen"
	autogenSkill, found := registry2.Get("my-autogen-skill")
	if !found {
		t.Fatal("Expected my-autogen-skill to be in registry")
	}
	if autogenSkill.Source != "autogen" {
		t.Errorf("autogen skill Source = %q, want \"autogen\"", autogenSkill.Source)
	}

	// Check that the regular skill got Source="user"
	regularSkill, found := registry2.Get("regular-skill")
	if !found {
		t.Fatal("Expected regular-skill to be in registry")
	}
	if regularSkill.Source != "user" {
		t.Errorf("regular skill Source = %q, want \"user\"", regularSkill.Source)
	}
}

// TestIntegrationEmptyInputs tests handling of empty/nil inputs.
func TestIntegrationEmptyInputs(t *testing.T) {
	// Test with nil skill
	xml := GenerateSkillXML(nil)
	if xml != "" {
		t.Error("Expected empty string for nil skill")
	}

	// Test with empty skills slice
	availableXML := GenerateAvailableSkillsXML([]*Skill{})
	if availableXML != "" {
		t.Error("Expected empty string for empty skills slice")
	}

	// Test with nil skills slice
	availableXML = GenerateAvailableSkillsXML(nil)
	if availableXML != "" {
		t.Error("Expected empty string for nil skills slice")
	}

	// Test validation with nil
	result := ValidateSkill(nil)
	if result.Valid {
		t.Error("Expected invalid result for nil skill")
	}

	// Test GeneratePromptContext with empty inputs
	ctx := GeneratePromptContext([]*Skill{}, []*Skill{})
	if ctx == nil {
		t.Error("GeneratePromptContext should not return nil")
	}
	if ctx.String() != "" {
		t.Error("Empty context String() should be empty")
	}
}
