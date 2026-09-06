// Package configbundle provides a unified configuration system for Swarm.
package configbundle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNewManager(t *testing.T) {
	// Create a temp directory for global config
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "config.json")

	// Create manager without existing config
	mgr, err := NewManager(context.Background(), Options{
		GlobalPath: globalPath,
		WorkDir:    tmpDir,
	})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	if mgr == nil {
		t.Fatal("Manager is nil")
	}

	// Should have created default global config
	if mgr.global == nil {
		t.Fatal("Global config is nil")
	}

	if mgr.global.Source != SourceGlobal {
		t.Errorf("Expected SourceGlobal, got %s", mgr.global.Source)
	}
}

func TestFindProjectConfig(t *testing.T) {
	// Create a temp directory structure
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project", "subdir")
	swarmDir := filepath.Join(tmpDir, "project", ".swarm")

	// Create directories
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("Failed to create project dir: %v", err)
	}
	if err := os.MkdirAll(swarmDir, 0755); err != nil {
		t.Fatalf("Failed to create .swarm dir: %v", err)
	}

	// Create project config
	configPath := filepath.Join(swarmDir, "config.json")
	configData := `{"schemaVersion": 1, "name": "Test Project"}`
	if err := os.WriteFile(configPath, []byte(configData), 0644); err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	// Test finding config from subdirectory
	found := FindProjectConfig(projectDir)
	if found != configPath {
		t.Errorf("Expected %s, got %s", configPath, found)
	}

	// Test not finding config outside project
	found = FindProjectConfig(tmpDir)
	if found != "" {
		t.Errorf("Expected empty string for no config, got %s", found)
	}
}

func TestManagerToggle(t *testing.T) {
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "config.json")

	// Create global config
	globalConfig := `{"schemaVersion": 1, "name": "Global"}`
	if err := os.WriteFile(globalPath, []byte(globalConfig), 0644); err != nil {
		t.Fatalf("Failed to create global config: %v", err)
	}

	// Create project config
	projectDir := filepath.Join(tmpDir, "project")
	swarmDir := filepath.Join(projectDir, ".swarm")
	if err := os.MkdirAll(swarmDir, 0755); err != nil {
		t.Fatalf("Failed to create .swarm dir: %v", err)
	}
	projectPath := filepath.Join(swarmDir, "config.json")
	projectConfig := `{"schemaVersion": 1, "name": "Project"}`
	if err := os.WriteFile(projectPath, []byte(projectConfig), 0644); err != nil {
		t.Fatalf("Failed to create project config: %v", err)
	}

	// Create manager
	mgr, err := NewManager(context.Background(), Options{
		GlobalPath: globalPath,
		WorkDir:    projectDir,
		AutoSwitch: false,
	})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Should have detected project config
	if !mgr.HasProjectConfig() {
		t.Error("Should have detected project config")
	}

	// Should not be using project initially with AutoSwitch=false
	if mgr.IsUsingProject() {
		t.Error("Should not be using project initially with AutoSwitch=false")
	}

	// Toggle to project
	mgr.Toggle()
	if !mgr.IsUsingProject() {
		t.Error("Should be using project after toggle")
	}

	// Toggle back to global
	mgr.Toggle()
	if mgr.IsUsingProject() {
		t.Error("Should not be using project after second toggle")
	}
}

func TestMergeConfigs(t *testing.T) {
	mgr := &Manager{}

	showThinking := true
	global := &ConfigBundle{
		SchemaVersion: SchemaVersion,
		Name:          "Global",
		System: SystemConfig{
			Theme:           "dark",
			ShowThinking:    &showThinking,
			MaxOutputLines:  100,
			DefaultProvider: "anthropic",
		},
		Agents: AgentsConfig{
			DefaultAgent: "default",
			Definitions: []AgentDefinition{
				{ID: "agent1", Name: "Agent 1"},
			},
		},
		Credentials: CredentialsConfig{
			ProviderKeys: map[string]string{
				"anthropic": "global-key",
			},
			Inherit: true,
		},
	}

	project := &ConfigBundle{
		SchemaVersion: SchemaVersion,
		Name:          "Project",
		System: SystemConfig{
			Theme:           "light",  // Override
			MaxOutputLines:  200,      // Override
			DefaultProvider: "openai", // Override
		},
		Agents: AgentsConfig{
			Definitions: []AgentDefinition{
				{ID: "agent1", Name: "Agent 1 Updated"}, // Override
				{ID: "agent2", Name: "Agent 2"},         // New
			},
		},
		Credentials: CredentialsConfig{
			ProviderKeys: map[string]string{
				"openai": "project-key", // New key (cannot override global)
			},
			Inherit: true,
		},
	}

	result := mgr.mergeConfigs(global, project)

	// Check system settings
	if result.System.Theme != "light" {
		t.Errorf("Expected theme 'light', got '%s'", result.System.Theme)
	}
	if result.System.MaxOutputLines != 200 {
		t.Errorf("Expected MaxOutputLines 200, got %d", result.System.MaxOutputLines)
	}
	if result.System.DefaultProvider != "openai" {
		t.Errorf("Expected DefaultProvider 'openai', got '%s'", result.System.DefaultProvider)
	}
	// ShowThinking should be inherited from global since project doesn't set it
	if result.System.ShowThinking == nil || !*result.System.ShowThinking {
		t.Error("ShowThinking should be inherited from global when not set in project")
	}

	// Check agents
	if len(result.Agents.Definitions) != 2 {
		t.Errorf("Expected 2 agent definitions, got %d", len(result.Agents.Definitions))
	}

	// Check credentials - project cannot override global keys
	if result.Credentials.ProviderKeys["anthropic"] != "global-key" {
		t.Error("Global key should not be overridden by project")
	}
	if result.Credentials.ProviderKeys["openai"] != "project-key" {
		t.Error("Project should be able to add new keys")
	}
}

func TestMergePolicy(t *testing.T) {
	policy := DefaultMergePolicy()

	if policy.System != MergeModeMerge {
		t.Errorf("Expected System merge mode, got %s", policy.System)
	}
	if policy.Agents != MergeModeMerge {
		t.Errorf("Expected Agents merge mode, got %s", policy.Agents)
	}
	if policy.Hooks != MergeModeAppend {
		t.Errorf("Expected Hooks append mode, got %s", policy.Hooks)
	}
	if policy.Credentials != MergeModeAppend {
		t.Errorf("Expected Credentials append mode, got %s", policy.Credentials)
	}
}

func TestDetectProjectConfig(t *testing.T) {
	tmpDir := t.TempDir()
	swarmDir := filepath.Join(tmpDir, ".swarm")
	if err := os.MkdirAll(swarmDir, 0755); err != nil {
		t.Fatalf("Failed to create .swarm dir: %v", err)
	}

	// Without config
	info, err := DetectProjectConfig(tmpDir)
	if err != nil {
		t.Fatalf("DetectProjectConfig failed: %v", err)
	}
	if info.Exists {
		t.Error("Should not find config when none exists")
	}

	// Create config
	configPath := filepath.Join(swarmDir, "config.json")
	configData := `{"schemaVersion": 1, "name": "Test", "description": "Test config"}`
	if err := os.WriteFile(configPath, []byte(configData), 0644); err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	// With config
	info, err = DetectProjectConfig(tmpDir)
	if err != nil {
		t.Fatalf("DetectProjectConfig failed: %v", err)
	}
	if !info.Exists {
		t.Error("Should find config when it exists")
	}
	if info.Name != "Test" {
		t.Errorf("Expected name 'Test', got '%s'", info.Name)
	}
	if info.Path != configPath {
		t.Errorf("Expected path '%s', got '%s'", configPath, info.Path)
	}
}

func TestCreateProject(t *testing.T) {
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "global.json")

	// Create global config
	globalConfig := `{"schemaVersion": 1, "name": "Global"}`
	if err := os.WriteFile(globalPath, []byte(globalConfig), 0644); err != nil {
		t.Fatalf("Failed to create global config: %v", err)
	}

	// Create manager
	mgr, err := NewManager(context.Background(), Options{
		GlobalPath: globalPath,
		WorkDir:    tmpDir,
	})
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Create project config
	project, err := mgr.CreateProject("")
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	if project == nil {
		t.Fatal("Project is nil")
	}
	if project.Source != SourceProject {
		t.Errorf("Expected SourceProject, got %s", project.Source)
	}
	if mgr.projectPath == "" {
		t.Error("Project path should be set")
	}

	// Verify file was created
	if _, err := os.Stat(mgr.projectPath); os.IsNotExist(err) {
		t.Error("Project config file was not created")
	}
}

func TestCredentialsSecurity(t *testing.T) {
	mgr := &Manager{}

	// Global has anthropic key
	global := &ConfigBundle{
		Credentials: CredentialsConfig{
			ProviderKeys: map[string]string{
				"anthropic": "secret-global-key",
			},
			Inherit: true,
		},
	}

	// Project tries to override anthropic key (should be ignored)
	project := &ConfigBundle{
		Credentials: CredentialsConfig{
			ProviderKeys: map[string]string{
				"anthropic": "malicious-project-key", // Should be ignored
				"openai":    "project-openai-key",    // Should be added
			},
			Inherit: true,
		},
	}

	result := mgr.mergeConfigs(global, project)

	// Verify global key is preserved
	if result.Credentials.ProviderKeys["anthropic"] != "secret-global-key" {
		t.Error("Project should NOT be able to override global credential")
	}

	// Verify project can add new keys
	if result.Credentials.ProviderKeys["openai"] != "project-openai-key" {
		t.Error("Project should be able to add new credentials")
	}
}
