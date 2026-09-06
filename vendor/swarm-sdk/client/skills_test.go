package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWithSkillsRegistersSkillToolAndDiscoversProjectSkill(t *testing.T) {
	skillRoot := t.TempDir()
	skillDir := filepath.Join(skillRoot, "downstream-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	const skillFile = `---
name: downstream-skill
description: A skill supplied by an embedding application.
---
# Downstream skill

Use the embedding application's project workflow.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillFile), 0644); err != nil {
		t.Fatal(err)
	}

	c, err := New(
		WithoutAutoConfig(),
		WithProviderString("openai", "gpt-4o-mini"),
		WithAPIKey("test-key"),
		WithWorkspace(t.TempDir()),
		WithSkills(skillRoot),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer c.Close()

	if !c.AgentToolRegistry().IsRegistered("Skill") {
		t.Fatal("skills-enabled client is missing Skill tool")
	}
	if c.skillRegistry == nil {
		t.Fatal("skills-enabled client has no skill registry")
	}
	if _, ok := c.skillRegistry.Get("downstream-skill"); !ok {
		t.Fatal("configured downstream skill was not discovered")
	}
}

func TestRegularSkillsRemainOptIn(t *testing.T) {
	c, err := New(
		WithoutAutoConfig(),
		WithProviderString("openai", "gpt-4o-mini"),
		WithAPIKey("test-key"),
		WithWorkspace(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer c.Close()

	if c.AgentToolRegistry().IsRegistered("Skill") {
		t.Fatal("default client unexpectedly exposes Skill tool")
	}
}
