package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSkillMD(t *testing.T) {
	content := `---
name: test-skill
description: A test skill for unit testing
version: 1.0.0
author: Test Author
category: testing
tags:
  - test
  - unit
---

# Test Skill

This is the instructions content.

## Usage

Use this skill for testing purposes.
`

	metadata, instructions, _, err := ParseSkillMDContent([]byte(content))
	if err != nil {
		t.Fatalf("ParseSkillMDContent() error = %v", err)
	}

	if metadata.Name != "test-skill" {
		t.Errorf("Name = %q, want %q", metadata.Name, "test-skill")
	}

	if metadata.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", metadata.Version, "1.0.0")
	}

	if metadata.Author != "Test Author" {
		t.Errorf("Author = %q, want %q", metadata.Author, "Test Author")
	}

	if len(metadata.Tags) != 2 {
		t.Errorf("Tags count = %d, want 2", len(metadata.Tags))
	}

	if instructions == "" {
		t.Error("Expected non-empty instructions")
	}

	if !contains(instructions, "Test Skill") {
		t.Error("Instructions should contain 'Test Skill'")
	}
}

func TestParseSkillMDNoFrontmatter(t *testing.T) {
	content := `# Simple Skill

This skill has no frontmatter.
Just plain markdown content.
`

	metadata, instructions, _, err := ParseSkillMDContent([]byte(content))
	if err != nil {
		t.Fatalf("ParseSkillMDContent() error = %v", err)
	}

	if metadata.Name != "" {
		t.Errorf("Expected empty name, got %q", metadata.Name)
	}

	if instructions == "" {
		t.Error("Expected non-empty instructions")
	}
}

func TestLoadSkill(t *testing.T) {
	// Create temp directory with skill structure
	tmpDir, err := os.MkdirTemp("", "skill-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create SKILL.md
	skillMD := `---
name: test-loader
description: Test skill for loader
version: 2.0.0
category: testing
---

# Test Loader Skill

Instructions for the test loader skill.
`
	if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), []byte(skillMD), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	// Create scripts directory with a test script
	scriptsDir := filepath.Join(tmpDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("Failed to create scripts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "test.py"), []byte("print('hello')"), 0644); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	// Create references directory
	refsDir := filepath.Join(tmpDir, "references")
	if err := os.MkdirAll(refsDir, 0755); err != nil {
		t.Fatalf("Failed to create refs dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "api.md"), []byte("# API Reference"), 0644); err != nil {
		t.Fatalf("Failed to write reference: %v", err)
	}

	// Load the skill
	skill, err := LoadSkill(tmpDir)
	if err != nil {
		t.Fatalf("LoadSkill() error = %v", err)
	}

	if skill.Metadata.Name != "test-loader" {
		t.Errorf("Name = %q, want %q", skill.Metadata.Name, "test-loader")
	}

	if skill.Metadata.Version != "2.0.0" {
		t.Errorf("Version = %q, want %q", skill.Metadata.Version, "2.0.0")
	}

	if len(skill.Scripts) != 1 {
		t.Errorf("Scripts count = %d, want 1", len(skill.Scripts))
	} else if skill.Scripts[0].Language != "python" {
		t.Errorf("Script language = %q, want %q", skill.Scripts[0].Language, "python")
	}

	if len(skill.References) != 1 {
		t.Errorf("References count = %d, want 1", len(skill.References))
	} else if skill.References[0].Format != "markdown" {
		t.Errorf("Reference format = %q, want %q", skill.References[0].Format, "markdown")
	}
}

func TestRegistry(t *testing.T) {
	registry := NewRegistry()

	// Create temp skill directory
	tmpDir, err := os.MkdirTemp("", "registry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	skillMD := `---
name: registry-test
description: Test skill for registry
---

Registry test instructions.
`
	if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), []byte(skillMD), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	// Load skill
	skill, err := registry.LoadFromPath(tmpDir)
	if err != nil {
		t.Fatalf("LoadFromPath() error = %v", err)
	}

	// Get skill
	got, ok := registry.Get("registry-test")
	if !ok {
		t.Error("Expected to find skill in registry")
	}
	if got.Metadata.Name != skill.Metadata.Name {
		t.Errorf("Got different skill than loaded")
	}

	// List skills
	skills := registry.List()
	if len(skills) != 1 {
		t.Errorf("List() count = %d, want 1", len(skills))
	}

	// Activate/Deactivate are deprecated no-ops in Claude-style model.
	// Activate still validates existence (returns nil for known skills).
	if err := registry.Activate("registry-test"); err != nil {
		t.Fatalf("Activate() on existing skill should not error, got: %v", err)
	}

	// IsActive always returns false — activation is a no-op.
	if registry.IsActive("registry-test") {
		t.Error("IsActive() should always return false (deprecated no-op)")
	}

	// GetActive always returns nil.
	active := registry.GetActive()
	if active != nil {
		t.Errorf("GetActive() should return nil, got %d skills", len(active))
	}

	if err := registry.Deactivate("registry-test"); err != nil {
		t.Fatalf("Deactivate() should not error, got: %v", err)
	}

	if registry.IsActive("registry-test") {
		t.Error("IsActive() should still be false after Deactivate")
	}

	// Search
	results := registry.Search("registry")
	if len(results) != 1 {
		t.Errorf("Search() count = %d, want 1", len(results))
	}

	results = registry.Search("nonexistent")
	if len(results) != 0 {
		t.Errorf("Search(nonexistent) count = %d, want 0", len(results))
	}

	// Unload
	registry.Unload("registry-test")
	if _, ok := registry.Get("registry-test"); ok {
		t.Error("Expected skill to be unloaded")
	}
}

func TestPluginDatabase(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db := NewPluginDatabase(tmpDir)

	// Create a test marketplace index
	indexContent := `{
		"name": "test-marketplace",
		"description": "Test marketplace",
		"plugins": [
			{
				"name": "test-plugin",
				"description": "A test plugin",
				"category": "testing",
				"source": "./test-plugin",
				"tags": ["test", "demo"],
				"featured": true
			},
			{
				"name": "another-plugin",
				"description": "Another plugin for testing",
				"category": "development",
				"source": "./another-plugin",
				"tags": ["dev"]
			}
		]
	}`

	indexPath := filepath.Join(tmpDir, "marketplace.json")
	if err := os.WriteFile(indexPath, []byte(indexContent), 0644); err != nil {
		t.Fatalf("Failed to write marketplace.json: %v", err)
	}

	// Load local index
	if err := db.LoadLocalIndex(indexPath); err != nil {
		t.Fatalf("LoadLocalIndex() error = %v", err)
	}

	// Search
	results := db.Search("test")
	if len(results) != 2 {
		t.Errorf("Search(test) count = %d, want 2", len(results))
	}

	// Search by category
	results = db.SearchByCategory("testing")
	if len(results) != 1 {
		t.Errorf("SearchByCategory(testing) count = %d, want 1", len(results))
	}

	// Search by tag
	results = db.SearchByTag("demo")
	if len(results) != 1 {
		t.Errorf("SearchByTag(demo) count = %d, want 1", len(results))
	}

	// Get categories
	categories := db.GetCategories()
	if len(categories) != 2 {
		t.Errorf("GetCategories() count = %d, want 2", len(categories))
	}

	// Get featured
	featured := db.GetFeatured()
	if len(featured) != 1 {
		t.Errorf("GetFeatured() count = %d, want 1", len(featured))
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"script.py", "python"},
		{"script.sh", "bash"},
		{"script.bash", "bash"},
		{"script.js", "javascript"},
		{"script.ts", "typescript"},
		{"script.go", "go"},
		{"script.rb", "ruby"},
		{"script.unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := detectLanguage(tt.filename)
			if got != tt.want {
				t.Errorf("detectLanguage(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"doc.md", "markdown"},
		{"data.json", "json"},
		{"config.yaml", "yaml"},
		{"config.yml", "yaml"},
		{"readme.txt", "text"},
		{"unknown.xyz", ""},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := detectFormat(tt.filename)
			if got != tt.want {
				t.Errorf("detectFormat(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

// Validation Tests per agentskills.io Specification

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
		errMsg  string
	}{
		// Valid names
		{"my-skill", false, ""},
		{"skill123", false, ""},
		{"a", false, ""},
		{"my-cool-skill", false, ""},
		{"skill-1-2-3", false, ""},

		// Invalid: empty
		{"", true, "name is required"},

		// Invalid: uppercase
		{"MySkill", true, "lowercase"},
		{"SKILL", true, "lowercase"},

		// Invalid: starts with hyphen
		{"-skill", true, "start or end with a hyphen"},

		// Invalid: ends with hyphen
		{"skill-", true, "start or end with a hyphen"},

		// Invalid: consecutive hyphens
		{"my--skill", true, "consecutive hyphens"},

		// Invalid: special characters
		{"my_skill", true, "alphanumeric"},
		{"my.skill", true, "alphanumeric"},
		{"my skill", true, "alphanumeric"},
		{"my@skill", true, "alphanumeric"},

		// Invalid: too long (> 64 chars)
		{"this-is-a-very-long-skill-name-that-exceeds-the-maximum-allowed-length-of-64-chars", true, "maximum length"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateName(tt.name)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateName(%q) expected error, got nil", tt.name)
				} else if tt.errMsg != "" && !containsSubstr(err.Error(), tt.errMsg) {
					t.Errorf("ValidateName(%q) error = %q, want to contain %q", tt.name, err.Error(), tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("ValidateName(%q) unexpected error: %v", tt.name, err)
			}
		})
	}
}

func TestValidateDescription(t *testing.T) {
	tests := []struct {
		name    string
		desc    string
		wantErr bool
	}{
		{"valid description", "This is a valid description", false},
		{"short description", "A", false},
		{"empty description", "", true},
		{"whitespace only", "   ", true},
		// Derived from the constant so raising the limit cannot silently leave
		// this test asserting a stale boundary.
		{"max length", string(make([]byte, MaxDescriptionLength)), false},
		{"exceeds max length", string(make([]byte, MaxDescriptionLength+1)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDescription(tt.desc)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateDescription() expected error for %q", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateDescription() unexpected error for %q: %v", tt.name, err)
			}
		})
	}
}

func TestValidateLicense(t *testing.T) {
	tests := []struct {
		name    string
		license string
		wantErr bool
	}{
		{"empty license", "", false},
		{"MIT license", "MIT", false},
		{"Apache-2.0 license", "Apache-2.0", false},
		{"max length", string(make([]byte, 256)), false},
		{"exceeds max length", string(make([]byte, 257)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLicense(tt.license)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateLicense() expected error for %q", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateLicense() unexpected error for %q: %v", tt.name, err)
			}
		})
	}
}

func TestValidateCompatibility(t *testing.T) {
	tests := []struct {
		name    string
		compat  string
		wantErr bool
	}{
		{"empty compatibility", "", false},
		{"version requirement", ">=1.0.0", false},
		{"max length", string(make([]byte, 500)), false},
		{"exceeds max length", string(make([]byte, 501)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCompatibility(tt.compat)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateCompatibility() expected error for %q", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateCompatibility() unexpected error for %q: %v", tt.name, err)
			}
		})
	}
}

func TestValidateAllowedTools(t *testing.T) {
	tests := []struct {
		name    string
		tools   string
		wantErr bool
	}{
		{"empty tools", "", false},
		{"single tool", "bash", false},
		{"multiple tools", "bash read write", false},
		{"tools with underscores", "file_read file_write", false},
		{"tools with hyphens", "web-fetch code-edit", false},
		{"invalid characters", "bash@tool", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAllowedTools(tt.tools)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateAllowedTools() expected error for %q", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateAllowedTools() unexpected error for %q: %v", tt.name, err)
			}
		})
	}
}

func TestValidateMetadata(t *testing.T) {
	tests := []struct {
		name      string
		meta      *SkillMetadata
		wantFatal int
		wantTotal int
	}{
		{
			name: "valid metadata",
			meta: &SkillMetadata{
				Name:        "test-skill",
				Description: "A test skill",
			},
			wantFatal: 0,
			wantTotal: 0,
		},
		{
			name: "missing name",
			meta: &SkillMetadata{
				Description: "A test skill",
			},
			wantFatal: 1,
			wantTotal: 1,
		},
		{
			name: "missing description",
			meta: &SkillMetadata{
				Name: "test-skill",
			},
			wantFatal: 1,
			wantTotal: 1,
		},
		{
			name: "invalid name format",
			meta: &SkillMetadata{
				Name:        "Invalid-Name",
				Description: "A test skill",
			},
			wantFatal: 1,
			wantTotal: 1,
		},
		{
			name: "full valid metadata",
			meta: &SkillMetadata{
				Name:          "my-skill",
				Description:   "A comprehensive skill",
				License:       "MIT",
				Compatibility: ">=1.0.0",
				AllowedTools:  "bash read write",
			},
			wantFatal: 0,
			wantTotal: 0,
		},
		{
			name:      "nil metadata",
			meta:      nil,
			wantFatal: 1,
			wantTotal: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateMetadata(tt.meta)

			fatalCount := 0
			for _, err := range errs {
				if err.Fatal {
					fatalCount++
				}
			}

			if fatalCount != tt.wantFatal {
				t.Errorf("ValidateMetadata() fatal count = %d, want %d", fatalCount, tt.wantFatal)
			}

			if len(errs) != tt.wantTotal {
				t.Errorf("ValidateMetadata() total errors = %d, want %d", len(errs), tt.wantTotal)
			}
		})
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"My Skill", "my-skill"},
		{"my_skill", "my-skill"},
		{"MY-SKILL", "my-skill"},
		{"my--skill", "my-skill"},
		{"-my-skill-", "my-skill"},
		{"my@skill!", "myskill"},
		{"  My  Cool  Skill  ", "my-cool-skill"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizeName(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetAllowedToolsList(t *testing.T) {
	tests := []struct {
		name string
		meta *SkillMetadata
		want int
	}{
		{
			name: "empty tools",
			meta: &SkillMetadata{AllowedTools: ""},
			want: 0,
		},
		{
			name: "single tool",
			meta: &SkillMetadata{AllowedTools: "bash"},
			want: 1,
		},
		{
			name: "multiple tools",
			meta: &SkillMetadata{AllowedTools: "bash read write edit"},
			want: 4,
		},
		{
			name: "nil metadata",
			meta: nil,
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetAllowedToolsList(tt.meta)
			if len(got) != tt.want {
				t.Errorf("GetAllowedToolsList() = %d tools, want %d", len(got), tt.want)
			}
		})
	}
}

// XML Generation Tests

func TestGenerateSkillXML(t *testing.T) {
	skill := &Skill{
		Metadata: SkillMetadata{
			Name:        "test-skill",
			Description: "A test skill for XML generation",
		},
		Path: "/skills/test-skill",
	}

	xml := GenerateSkillXML(skill)

	if !containsSubstr(xml, "<name>test-skill</name>") {
		t.Error("XML should contain skill name")
	}
	if !containsSubstr(xml, "<description>A test skill for XML generation</description>") {
		t.Error("XML should contain skill description")
	}
	if !containsSubstr(xml, "<location>/skills/test-skill/SKILL.md</location>") {
		t.Error("XML should contain skill location")
	}
}

func TestGenerateAvailableSkillsXML(t *testing.T) {
	skills := []*Skill{
		{
			Metadata: SkillMetadata{Name: "skill-b", Description: "Second skill"},
			Path:     "/skills/skill-b",
		},
		{
			Metadata: SkillMetadata{Name: "skill-a", Description: "First skill"},
			Path:     "/skills/skill-a",
		},
	}

	xml := GenerateAvailableSkillsXML(skills)

	if !containsSubstr(xml, "<available_skills>") {
		t.Error("XML should contain available_skills opening tag")
	}
	if !containsSubstr(xml, "</available_skills>") {
		t.Error("XML should contain available_skills closing tag")
	}
	if !containsSubstr(xml, "<name>skill-a</name>") {
		t.Error("XML should contain skill-a")
	}
	if !containsSubstr(xml, "<name>skill-b</name>") {
		t.Error("XML should contain skill-b")
	}
}

func TestGenerateAvailableSkillsXMLEmpty(t *testing.T) {
	xml := GenerateAvailableSkillsXML(nil)
	if xml != "" {
		t.Errorf("Expected empty string for nil skills, got %q", xml)
	}

	xml = GenerateAvailableSkillsXML([]*Skill{})
	if xml != "" {
		t.Errorf("Expected empty string for empty skills, got %q", xml)
	}
}

func TestGenerateSkillXMLWithDetails(t *testing.T) {
	skill := &Skill{
		Metadata: SkillMetadata{
			Name:          "detailed-skill",
			Description:   "A skill with all details",
			Version:       "1.0.0",
			Author:        "Test Author",
			Category:      "testing",
			Tags:          []string{"test", "demo"},
			License:       "MIT",
			Compatibility: ">=1.0.0",
			AllowedTools:  "bash read",
		},
		Path: "/skills/detailed-skill",
	}

	xml := GenerateSkillXMLWithDetails(skill)

	expectedFields := []string{
		"<name>detailed-skill</name>",
		"<version>1.0.0</version>",
		"<author>Test Author</author>",
		"<category>testing</category>",
		"<tags>test, demo</tags>",
		"<license>MIT</license>",
		"<compatibility>&gt;=1.0.0</compatibility>", // Note: escaped
		"<allowed-tools>bash read</allowed-tools>",
	}

	for _, field := range expectedFields {
		if !containsSubstr(xml, field) {
			t.Errorf("XML should contain %s", field)
		}
	}
}

func TestGenerateActiveSkillsXML(t *testing.T) {
	skills := []*Skill{
		{
			Metadata: SkillMetadata{
				Name:        "active-skill",
				Description: "An active skill",
				Priority:    10,
			},
		},
	}

	// GenerateActiveSkillsXML is a deprecated no-op — always returns "".
	xml := GenerateActiveSkillsXML(skills)
	if xml != "" {
		t.Errorf("GenerateActiveSkillsXML should return empty string (deprecated no-op), got: %q", xml)
	}
}

func TestGenerateSkillInstructionsXML(t *testing.T) {
	skill := &Skill{
		Metadata: SkillMetadata{
			Name:        "instruction-skill",
			Description: "A skill with instructions",
		},
		Instructions: "# Instructions\n\nFollow these steps...",
	}

	xml := GenerateSkillInstructionsXML(skill)

	if !containsSubstr(xml, `<skill_instructions name="instruction-skill">`) {
		t.Error("XML should contain skill_instructions tag with name")
	}
	if !containsSubstr(xml, "<![CDATA[") {
		t.Error("XML should contain CDATA for instructions")
	}
	if !containsSubstr(xml, "# Instructions") {
		t.Error("XML should contain actual instructions")
	}
}

func TestSkillPromptContext(t *testing.T) {
	available := []*Skill{
		{
			Metadata: SkillMetadata{Name: "available-skill", Description: "Available"},
			Path:     "/skills/available",
		},
	}
	active := []*Skill{
		{
			Metadata:     SkillMetadata{Name: "active-skill", Description: "Active"},
			Instructions: "Instructions here",
		},
	}

	ctx := GeneratePromptContext(available, active)

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

	// String() returns only the non-empty AvailableSkillsXML part.
	combined := ctx.String()
	if combined == "" {
		t.Error("Expected non-empty combined string (AvailableSkillsXML)")
	}
}

func TestParseSkillMDContentWithValidation(t *testing.T) {
	content := `---
name: valid-skill
description: A valid skill
license: MIT
compatibility: ">=1.0.0"
allowed-tools: bash read write
metadata:
  custom-key: custom-value
---

# Valid Skill

Instructions here.
`

	metadata, instructions, _, errors, err := ParseSkillMDContentWithValidation([]byte(content))
	if err != nil {
		t.Fatalf("ParseSkillMDContentWithValidation() error = %v", err)
	}

	if len(errors) > 0 {
		t.Errorf("Expected no validation errors, got %d", len(errors))
	}

	if metadata.Name != "valid-skill" {
		t.Errorf("Name = %q, want %q", metadata.Name, "valid-skill")
	}

	if metadata.License != "MIT" {
		t.Errorf("License = %q, want %q", metadata.License, "MIT")
	}

	if metadata.AllowedTools != "bash read write" {
		t.Errorf("AllowedTools = %q, want %q", metadata.AllowedTools, "bash read write")
	}

	if instructions == "" {
		t.Error("Expected non-empty instructions")
	}
}

func TestParseSkillMDContentWithValidationErrors(t *testing.T) {
	content := `---
name: Invalid-Name
description: ""
---
Instructions.
`

	_, _, _, errors, err := ParseSkillMDContentWithValidation([]byte(content))
	if err != nil {
		t.Fatalf("ParseSkillMDContentWithValidation() error = %v", err)
	}

	// With description fallback, description: "" is filled from content ("Instructions.")
	// So we only expect the name validation error now.
	if len(errors) < 1 {
		t.Errorf("Expected at least 1 validation error, got %d", len(errors))
	}

	// Check that we have an error for name (description is now filled by fallback)
	hasNameError := false
	for _, e := range errors {
		if e.Field == "name" {
			hasNameError = true
		}
	}

	if !hasNameError {
		t.Error("Expected validation error for name field")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr, 0))
}

func containsAt(s, substr string, start int) bool {
	for i := start; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func containsSubstr(s, substr string) bool {
	return contains(s, substr)
}

func TestDescriptionFallback_FromH1(t *testing.T) {
	content := `---
name: fallback-skill
---
# Fallback Skill

This is the first paragraph that should become the description.

## Usage

More details here.
`
	metadata, _, _, err := ParseSkillMDContent([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Description != "This is the first paragraph that should become the description." {
		t.Errorf("expected description from first paragraph, got: %q", metadata.Description)
	}
}

func TestDescriptionFallback_NoH1(t *testing.T) {
	content := `---
name: no-h1-skill
---
This paragraph becomes the description when there is no H1.

Second paragraph is ignored.
`
	metadata, _, _, err := ParseSkillMDContent([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Description != "This paragraph becomes the description when there is no H1." {
		t.Errorf("expected description from first paragraph, got: %q", metadata.Description)
	}
}

func TestDescriptionFallback_ExplicitDescription(t *testing.T) {
	content := `---
name: explicit-desc-skill
description: Explicit description in frontmatter
---
# Skill

This paragraph should NOT override the explicit description.
`
	metadata, _, _, err := ParseSkillMDContent([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Description != "Explicit description in frontmatter" {
		t.Errorf("expected explicit description, got: %q", metadata.Description)
	}
}

func TestDescriptionFallback_Truncation(t *testing.T) {
	var longPara strings.Builder
	for range 60 {
		longPara.WriteString("word ")
	}
	content := "---\nname: long-desc-skill\n---\n# Long\n\n" + longPara.String() + "\n"
	metadata, _, _, err := ParseSkillMDContent([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Description) > 256 {
		t.Errorf("description should be truncated to 256 chars, got %d: %q", len(metadata.Description), metadata.Description)
	}
	if metadata.Description == "" {
		t.Error("description should not be empty")
	}
}

func TestDescriptionFallback_EmptyContent(t *testing.T) {
	content := `---
name: empty-content-skill
---
`
	metadata, _, _, err := ParseSkillMDContent([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Description != "" {
		t.Errorf("expected empty description for empty content, got: %q", metadata.Description)
	}
}
