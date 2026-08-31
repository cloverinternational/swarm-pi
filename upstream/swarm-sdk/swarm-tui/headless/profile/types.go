// Package profile provides legacy agent profile types for the headless IPC server.
// This package is deprecated and will be replaced by swarm-sdk/profiles.
package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ModelAlias is a string alias for model selection.
type ModelAlias string

// Common model aliases.
const (
	AliasMain        ModelAlias = "main"
	AliasSteering    ModelAlias = "steering"
	AliasBackground  ModelAlias = "background"
	AliasSubAgent    ModelAlias = "sub_agent"
	AliasThinking    ModelAlias = "thinking"
	AliasLongContext ModelAlias = "long_context"
)

// ModelPointer points to a specific provider/model combination.
type ModelPointer struct {
	Provider         string            `json:"provider"`
	Model            string            `json:"model"`
	SystemPrompt     string            `json:"system_prompt,omitempty"`
	Capabilities     AgentCapabilities `json:"capabilities"`
	ReasoningLevel   string            `json:"reasoning_level,omitempty"`
	DisableReasoning bool              `json:"disable_reasoning,omitempty"`
}

// AgentCapabilities represents agent capabilities.
type AgentCapabilities struct {
	WebSearch     bool `json:"web_search,omitempty"`
	ProjectMemory bool `json:"project_memory,omitempty"`
	ToolUse       bool `json:"tool_use,omitempty"`
	Vision        bool `json:"vision,omitempty"`
	Steering      bool `json:"steering,omitempty"`
	CodeMode      bool `json:"code_mode,omitempty"`
	ComputerUse   bool `json:"computer_use,omitempty"`
}

// AgentProfile represents an agent profile configuration.
type AgentProfile struct {
	ID           string                      `json:"id"`
	Name         string                      `json:"name"`
	Description  string                      `json:"description,omitempty"`
	SystemPrompt string                      `json:"system_prompt,omitempty"`
	Temperature  float64                     `json:"temperature,omitempty"`
	MaxTokens    int                         `json:"max_tokens,omitempty"`
	Pointers     map[ModelAlias]ModelPointer `json:"pointers,omitempty"`
	CreatedAt    time.Time                   `json:"created_at"`
	UpdatedAt    time.Time                   `json:"updated_at"`
	IsDefault    bool                        `json:"is_default,omitempty"`
}

// GetPointer retrieves the model pointer for a given alias from this profile.
func (p *AgentProfile) GetPointer(alias ModelAlias) (*ModelPointer, error) {
	if pointer, ok := p.Pointers[alias]; ok {
		return &pointer, nil
	}
	return nil, fmt.Errorf("alias %s not found in profile %s", alias, p.ID)
}

// ProfilesConfig holds all profiles and the default.
type ProfilesConfig struct {
	DefaultProfile string         `json:"default_profile"`
	Profiles       []AgentProfile `json:"profiles"`
}

// ProfileStore manages agent profiles on disk.
type ProfileStore struct {
	configDir string
	mu        sync.RWMutex
}

// NewProfileStore creates a new profile store.
func NewProfileStore(configDir string) *ProfileStore {
	return &ProfileStore{
		configDir: configDir,
	}
}

// GetProfile retrieves a profile by ID.
func (s *ProfileStore) GetProfile(profileID string) (*AgentProfile, error) {
	config, err := s.LoadConfig()
	if err != nil {
		return nil, err
	}

	for i := range config.Profiles {
		if config.Profiles[i].ID == profileID {
			return &config.Profiles[i], nil
		}
	}

	return nil, fmt.Errorf("profile not found: %s", profileID)
}

// GetDefaultProfile retrieves the default profile or the first profile if none is set.
func (s *ProfileStore) GetDefaultProfile() (*AgentProfile, error) {
	config, err := s.LoadConfig()
	if err != nil {
		return nil, err
	}

	if config.DefaultProfile != "" {
		return s.GetProfile(config.DefaultProfile)
	}

	if len(config.Profiles) > 0 {
		return &config.Profiles[0], nil
	}

	return nil, fmt.Errorf("no profiles configured")
}

// LoadConfig loads the profiles configuration from disk.
func (s *ProfileStore) LoadConfig() (*ProfilesConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	configPath := filepath.Join(s.configDir, "agent_profiles.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty config if file doesn't exist
			return &ProfilesConfig{
				Profiles: []AgentProfile{},
			}, nil
		}
		return nil, fmt.Errorf("read profiles config: %w", err)
	}

	var config ProfilesConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse profiles config: %w", err)
	}

	return &config, nil
}

// SaveConfig saves the profiles configuration to disk.
func (s *ProfileStore) SaveConfig(config *ProfilesConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.configDir, 0755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	configPath := filepath.Join(s.configDir, "agent_profiles.json")
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal profiles config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("write profiles config: %w", err)
	}

	return nil
}
