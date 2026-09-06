package settings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/configformat"
)

// TestSystemPromptSettings_MigratesToYAML verifies the system_prompts config
// load/save routes through configformat: save writes system_prompts.yaml, and a
// legacy system_prompts.json is still read via .yaml->.yml->.json precedence.
func TestSystemPromptSettings_MigratesToYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "system_prompts.json") // legacy path the constructor would compute

	s := &SystemPromptSettings{
		configPath: cfgPath,
		config: SystemPromptConfig{
			ActivePrompt: "Default Assistant",
			Prompts:      []SystemPromptEntry{{Name: "Default Assistant", Content: "hi", Builtin: true}},
		},
	}

	// save() must write the canonical .yaml, not .json.
	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "system_prompts.yaml")); err != nil {
		t.Fatalf("expected system_prompts.yaml to be written: %v", err)
	}

	// load() must read it back via configformat.
	s2 := &SystemPromptSettings{configPath: cfgPath}
	if err := s2.load(); err != nil {
		t.Fatalf("load yaml: %v", err)
	}
	if s2.config.ActivePrompt != "Default Assistant" || len(s2.config.Prompts) != 1 {
		t.Fatalf("round-trip mismatch: %+v", s2.config)
	}

	// Legacy JSON fallback: a dir with ONLY system_prompts.json must still load.
	dir2 := t.TempDir()
	legacy := SystemPromptConfig{ActivePrompt: "Legacy", Prompts: []SystemPromptEntry{{Name: "Legacy", Content: "x"}}}
	if err := configformat.SaveAs(filepath.Join(dir2, "system_prompts.json"), legacy, 0644); err != nil {
		t.Fatalf("seed legacy json: %v", err)
	}
	s3 := &SystemPromptSettings{configPath: filepath.Join(dir2, "system_prompts.json")}
	if err := s3.load(); err != nil {
		t.Fatalf("load legacy json: %v", err)
	}
	if s3.config.ActivePrompt != "Legacy" {
		t.Fatalf("legacy json not read: %+v", s3.config)
	}
}

// TestAgentsSettings_MigratesToYAML verifies the custom_agents config load/save
// routes through configformat (writes custom_agents.yaml, reads legacy .json).
func TestAgentsSettings_MigratesToYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "custom_agents.json")

	s := &AgentsSettings{configPath: cfgPath}
	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "custom_agents.yaml")); err != nil {
		t.Fatalf("expected custom_agents.yaml to be written: %v", err)
	}

	// load() reads it back.
	s2 := &AgentsSettings{configPath: cfgPath}
	if err := s2.load(); err != nil {
		t.Fatalf("load yaml: %v", err)
	}

	// Legacy JSON in a fresh dir still loads via precedence.
	dir2 := t.TempDir()
	if err := configformat.SaveAs(filepath.Join(dir2, "custom_agents.json"), s.config, 0644); err != nil {
		t.Fatalf("seed legacy json: %v", err)
	}
	s3 := &AgentsSettings{configPath: filepath.Join(dir2, "custom_agents.json")}
	if err := s3.load(); err != nil {
		t.Fatalf("load legacy json: %v", err)
	}
}
