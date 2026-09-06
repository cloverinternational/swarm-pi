package autogenskills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// TestSkillFactory_New_requiresEnabledConfig fails for disabled modes.
func TestSkillFactory_New_requiresEnabledConfig(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := DefaultConfig() // ModeNever
	_, err := NewSkillFactory(cfg, reg, nil)
	if err == nil {
		t.Error("expected error for disabled config")
	}
}

// TestSkillFactory_New_requiresAutogenDir fails when dir is empty.
func TestSkillFactory_New_requiresAutogenDir(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual}
	_, err := NewSkillFactory(cfg, reg, nil)
	if err == nil {
		t.Error("expected error for empty autogen_dir")
	}
}

// TestSkillFactory_New_requiresRegistry fails when registry is nil.
func TestSkillFactory_New_requiresRegistry(t *testing.T) {
	cfg := Config{Mode: ModeManual, AutogenDir: "/tmp/test"}
	_, err := NewSkillFactory(cfg, nil, nil)
	if err == nil {
		t.Error("expected error for nil registry")
	}
}

// TestSkillFactory_New_success creates a valid factory.
func TestSkillFactory_New_success(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	f, err := NewSkillFactory(cfg, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected non-nil factory")
	}
}

// TestSkillFactory_Create_success writes a skill and registers it.
func TestSkillFactory_Create_success(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)

	opts := CreateOptions{
		Name:          "test-factory-skill",
		Description:   "A test skill created by factory",
		Instructions:  "This is a comprehensive set of instructions that definitely exceeds the two hundred character minimum length requirement so that validation passes without any issues whatsoever in the test.",
		TriggerReason: TriggerManual,
		Tags:          []string{"test", "factory"},
		Category:      "testing",
		LLMProvider:   "anthropic",
	}

	result := f.Create(opts)
	if !result.IsSuccess() {
		t.Fatalf("expected success, got error: %v", result.Error)
	}

	// Verify file exists
	skillPath := filepath.Join(dir, "test-factory-skill", "SKILL.md")
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		t.Errorf("SKILL.md not created at %s", skillPath)
	}

	// Verify skill is registered
	if result.Skill == nil {
		t.Fatal("expected non-nil skill")
	}
	if result.Skill.Metadata.Name != "test-factory-skill" {
		t.Errorf("name = %q, want %q", result.Skill.Metadata.Name, "test-factory-skill")
	}
	if result.Skill.Source != "autogen" {
		t.Errorf("source = %q, want autogen", result.Skill.Source)
	}

	// Verify metrics incremented
	// (we passed nil metrics, so just ensure no panic)
}

// TestSkillFactory_Create_invalidOptions fails on bad options.
func TestSkillFactory_Create_invalidOptions(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)

	opts := CreateOptions{
		Name:          "",
		Description:   "missing name",
		Instructions:  "too short but still more than enough characters to pass validation in this test because it exceeds the minimum by a lot",
		TriggerReason: TriggerManual,
	}

	result := f.Create(opts)
	if result.IsSuccess() {
		t.Error("expected failure for empty name")
	}
	if result.Error == nil {
		t.Error("expected error in result")
	}
}

// TestSkillFactory_Create_metricsIncrements tests metric tracking.
func TestSkillFactory_Create_metricsIncrements(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	m := &Metrics{}
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, m)

	opts := CreateOptions{
		Name:          "metric-test",
		Description:   "Test",
		Instructions:  "This is a comprehensive set of instructions that definitely exceeds the two hundred character minimum length requirement so that validation passes without any issues whatsoever in the test.",
		TriggerReason: TriggerManual,
	}

	f.Create(opts)
	if m.Snapshot().SkillCreatedCount != 1 {
		t.Errorf("SkillCreatedCount = %d, want 1", m.Snapshot().SkillCreatedCount)
	}
}

// TestSkillFactory_Create_writesCorrectContent verifies SKILL.md content.
func TestSkillFactory_Create_writesCorrectContent(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)

	opts := CreateOptions{
		Name:          "content-test",
		Description:   "Content verification",
		Instructions:  "Follow these steps carefully. Step one: analyze. Step two: execute. Step three: verify. This text must be long enough to pass the minimum length validation check so we include extra detail here.",
		TriggerReason: TriggerManual,
		Tags:          []string{"verify", "content"},
		Category:      "test",
		LLMProvider:   "openai",
	}

	f.Create(opts)

	content, err := os.ReadFile(filepath.Join(dir, "content-test", "SKILL.md"))
	if err != nil {
		t.Fatalf("failed to read SKILL.md: %v", err)
	}

	str := string(content)
	if !strings.Contains(str, "name: content-test") {
		t.Error("SKILL.md should contain name")
	}
	if !strings.Contains(str, "category: test") {
		t.Error("SKILL.md should contain category")
	}
	if !strings.Contains(str, "tags: [verify, content]") {
		t.Error("SKILL.md should contain tags")
	}
	if !strings.Contains(str, "llm_provider: openai") {
		t.Error("SKILL.md should contain llm_provider")
	}
	if !strings.Contains(str, "trigger_reason: manual") {
		t.Error("SKILL.md should contain trigger_reason")
	}
}

func TestSkillFactory_Patch_success(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)

	// Create a skill first
	f.Create(CreateOptions{
		Name:          "patch-test-skill",
		Description:   "Original description",
		Instructions:  strings.Repeat("Original instructions. ", 20),
		TriggerReason: TriggerManual,
	})

	// Patch it
	result := f.Patch(PatchOptions{
		Name:          "patch-test-skill",
		Instructions:  strings.Repeat("Improved instructions. ", 20),
		TriggerReason: TriggerLLMNudge,
	})

	if !result.IsSuccess() {
		t.Fatalf("Patch failed: %v", result.Error)
	}
	if result.PreviousVersion != "1.0.0" {
		t.Errorf("expected previous version 1.0.0, got %s", result.PreviousVersion)
	}
	if result.NewVersion != "1.0.1" {
		t.Errorf("expected new version 1.0.1, got %s", result.NewVersion)
	}

	// Verify the registry was updated
	skill, found := reg.Get("patch-test-skill")
	if !found {
		t.Fatal("skill not found in registry after patch")
	}
	if skill.Metadata.Version != "1.0.1" {
		t.Errorf("registry version = %s, want 1.0.1", skill.Metadata.Version)
	}
	if !strings.Contains(skill.Instructions, "Improved instructions") {
		t.Error("expected updated instructions in registry")
	}

	// Verify the disk file was updated
	content, _ := os.ReadFile(result.Path)
	if !strings.Contains(string(content), "version: 1.0.1") {
		t.Error("expected version 1.0.1 in SKILL.md")
	}
	if !strings.Contains(string(content), "Improved instructions") {
		t.Error("expected updated instructions in SKILL.md")
	}
}

func TestSkillFactory_Patch_appendInstructions(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)

	f.Create(CreateOptions{
		Name:          "append-test",
		Description:   "Test",
		Instructions:  strings.Repeat("Base instructions. ", 20),
		TriggerReason: TriggerManual,
	})

	result := f.Patch(PatchOptions{
		Name:               "append-test",
		Instructions:       "Additional edge case: handle nil inputs.",
		AppendInstructions: true,
		TriggerReason:      TriggerLLMNudge,
	})

	if !result.IsSuccess() {
		t.Fatalf("Append patch failed: %v", result.Error)
	}

	skill, _ := reg.Get("append-test")
	if !strings.Contains(skill.Instructions, "Base instructions") {
		t.Error("expected original instructions preserved")
	}
	if !strings.Contains(skill.Instructions, "Additional edge case") {
		t.Error("expected appended instructions")
	}
}

func TestSkillFactory_Patch_notFound(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)

	result := f.Patch(PatchOptions{
		Name:          "nonexistent",
		Instructions:  "New instructions",
		TriggerReason: TriggerLLMNudge,
	})

	if result.IsSuccess() {
		t.Error("expected error when patching nonexistent skill")
	}
}

func TestSkillFactory_Patch_noName(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)

	result := f.Patch(PatchOptions{})
	if result.IsSuccess() {
		t.Error("expected error when patching without name")
	}
}

func TestBumpVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"1.0.0", "1.0.1"},
		{"1.2.3", "1.2.4"},
		{"2.10.99", "2.10.100"},
		{"invalid", "invalid"},
	}
	for _, tt := range tests {
		got := bumpVersion(tt.input)
		if got != tt.expected {
			t.Errorf("bumpVersion(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestParseSkillMarkdown(t *testing.T) {
	content := "---\nname: test-skill\ndescription: Test\nversion: 1.0.0\nauthor: swarm-autogen\n---\n\nInstructions here.\n"
	fm, body, err := parseSkillMarkdown(content)
	if err != nil {
		t.Fatalf("parseSkillMarkdown error: %v", err)
	}
	if fm.Name != "test-skill" {
		t.Errorf("name = %q, want test-skill", fm.Name)
	}
	if fm.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", fm.Version)
	}
	if body != "Instructions here." {
		t.Errorf("body = %q, want Instructions here.", body)
	}
}

func TestParseSkillMarkdown_noFrontmatter(t *testing.T) {
	content := "Just instructions, no frontmatter.\n"
	fm, body, err := parseSkillMarkdown(content)
	if err != nil {
		t.Fatalf("parseSkillMarkdown error: %v", err)
	}
	if fm.Name != "" {
		t.Errorf("expected empty name, got %q", fm.Name)
	}
	if !strings.Contains(body, "Just instructions") {
		t.Errorf("expected body preserved")
	}
}

func TestSkillFactoryCreateConfiguredInstructionMinimum(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{
		Mode: ModeManual, AutogenDir: dir,
		Trigger: TriggerConfig{MinInstructionsLength: 200},
	}
	factory, err := NewSkillFactory(cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := factory.Create(CreateOptions{
		Name: "configured-minimum", Description: "minimum test",
		Instructions: "short", TriggerReason: TriggerManual,
	})
	if result.Error == nil || !strings.Contains(result.Error.Error(), "at least 200") {
		t.Fatalf("configured instruction minimum was not enforced: %v", result.Error)
	}
	if _, err := os.Stat(filepath.Join(dir, "configured-minimum")); !os.IsNotExist(err) {
		t.Fatalf("failed validation wrote a package: %v", err)
	}
}
