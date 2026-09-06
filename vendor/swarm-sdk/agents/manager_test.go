package agents_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/agents"
)

// TestEmbeddedBuiltins verifies all 5 built-in agents load correctly.
func TestEmbeddedBuiltins(t *testing.T) {
	m := agents.NewDefault()

	expected := []string{
		"general-assistant",
		"code-reviewer",
		"research-agent",
		"background-worker",
		"agent_constructor",
	}

	for _, id := range expected {
		def, ok := m.Get(id)
		if !ok {
			t.Errorf("built-in agent %q not found", id)
			continue
		}
		if def.ID != id {
			t.Errorf("agent %q: ID mismatch: got %q", id, def.ID)
		}
		if def.Name == "" {
			t.Errorf("agent %q: empty Name", id)
		}
		if def.SystemPrompt == "" {
			t.Errorf("agent %q: empty SystemPrompt", id)
		}
		// "inherit" sentinel must be normalised to empty string
		if def.Model == "inherit" {
			t.Errorf("agent %q: Model should be empty (normalised from 'inherit'), got %q", id, def.Model)
		}
		// sub_agent metadata must be set
		if def.Metadata == nil || def.Metadata["type"] != "sub_agent" {
			t.Errorf("agent %q: missing metadata type=sub_agent", id)
		}
	}

	// IDs() must include all expected
	ids := m.IDs()
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	for _, id := range expected {
		if !idSet[id] {
			t.Errorf("IDs() missing %q", id)
		}
	}
}

// TestProjectOverridesBuiltin verifies project-scoped files override built-ins.
func TestProjectOverridesBuiltin(t *testing.T) {
	projectDir := t.TempDir()

	// Write a project-scoped override for research-agent with a different system prompt.
	override := `---
id: research-agent
name: Custom Research Agent
model: inherit
tools:
  - "*"
capabilities:
  max_tokens: 4096
  temperature: 0.3
---
Custom research system prompt for testing.
`
	if err := os.WriteFile(filepath.Join(projectDir, "research-agent.md"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := agents.New(projectDir, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	def, ok := m.Get("research-agent")
	if !ok {
		t.Fatal("research-agent not found after override")
	}
	if def.SystemPrompt != "Custom research system prompt for testing." {
		t.Errorf("expected overridden system prompt, got %q", def.SystemPrompt)
	}
	if def.Name != "Custom Research Agent" {
		t.Errorf("expected overridden name, got %q", def.Name)
	}

	// Other built-ins must still be present.
	if _, ok := m.Get("general-assistant"); !ok {
		t.Error("general-assistant missing after project override")
	}
}

// TestUserOverridesBuiltin verifies user-scoped files override built-ins
// but are themselves overridden by project-scoped files.
func TestUserOverridesBuiltin(t *testing.T) {
	userDir := t.TempDir()
	projectDir := t.TempDir()

	userAgent := `---
id: general-assistant
name: User General Assistant
model: inherit
---
User-level system prompt.
`
	projectAgent := `---
id: general-assistant
name: Project General Assistant
model: inherit
---
Project-level system prompt.
`
	if err := os.WriteFile(filepath.Join(userDir, "general-assistant.md"), []byte(userAgent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "general-assistant.md"), []byte(projectAgent), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := agents.New(projectDir, userDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	def, ok := m.Get("general-assistant")
	if !ok {
		t.Fatal("general-assistant not found")
	}
	// Project wins over user.
	if def.SystemPrompt != "Project-level system prompt." {
		t.Errorf("project should win over user, got %q", def.SystemPrompt)
	}
}

// TestMissingDirsOK verifies that non-existent directories are silently skipped.
func TestMissingDirsOK(t *testing.T) {
	m, err := agents.New("/tmp/does-not-exist-swarm-agents-xyz", "/tmp/also-does-not-exist-xyz")
	if err != nil {
		t.Fatalf("New with missing dirs should not error: %v", err)
	}
	// Built-ins must still load.
	if _, ok := m.Get("general-assistant"); !ok {
		t.Error("general-assistant missing when dirs don't exist")
	}
}

// TestNewAgentInProjectDir verifies a brand-new agent only in project dir is found.
func TestNewAgentInProjectDir(t *testing.T) {
	projectDir := t.TempDir()

	newAgent := `---
id: my-custom-agent
name: My Custom Agent
model: inherit
tools:
  - Bash
capabilities:
  max_tokens: 2048
  temperature: 0.7
---
You are a custom agent for testing.
`
	if err := os.WriteFile(filepath.Join(projectDir, "my-custom-agent.md"), []byte(newAgent), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := agents.New(projectDir, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	def, ok := m.Get("my-custom-agent")
	if !ok {
		t.Fatal("my-custom-agent not found")
	}
	if def.SystemPrompt != "You are a custom agent for testing." {
		t.Errorf("unexpected system prompt: %q", def.SystemPrompt)
	}
	if len(def.ToolHints) != 1 || def.ToolHints[0] != "Bash" {
		t.Errorf("unexpected tool hints: %v", def.ToolHints)
	}
}

// TestReload verifies that Reload() picks up new files added after construction.
func TestReload(t *testing.T) {
	projectDir := t.TempDir()

	m, err := agents.New(projectDir, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Not present before adding file.
	if _, ok := m.Get("late-agent"); ok {
		t.Fatal("late-agent should not exist yet")
	}

	// Add file.
	lateAgent := `---
id: late-agent
name: Late Agent
model: inherit
---
Added after initial load.
`
	if err := os.WriteFile(filepath.Join(projectDir, "late-agent.md"), []byte(lateAgent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if _, ok := m.Get("late-agent"); !ok {
		t.Error("late-agent not found after Reload")
	}
}
