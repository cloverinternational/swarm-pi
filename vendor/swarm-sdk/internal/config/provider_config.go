package config

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// ProviderConfig represents a saved provider configuration
type ProviderConfig struct {
	Name           string         `json:"name"`
	Type           string         `json:"type"` // "anthropic", "openai", etc.
	APIKey         string         `json:"api_key,omitempty"`
	BaseURL        string         `json:"base_url,omitempty"`
	HTTPMaxRetries *int           `json:"http_max_retries,omitempty"`
	Model          string         `json:"model,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// ProviderConfigManager manages persistence of provider configurations
type ProviderConfigManager struct {
	mu         sync.RWMutex
	configPath string
	configs    map[string]ProviderConfig
}

// NewProviderConfigManager creates a new manager
func NewProviderConfigManager() (*ProviderConfigManager, error) {
	// Route to the single canonical providers store shared with the rest of the
	// SDK so the previously-duplicated legacy providers.json writer collapses
	// onto the single ~/.swarm/config/providers.json. paths.ProvidersFile()
	// ensures the parent config directory exists at 0700.
	return &ProviderConfigManager{
		configPath: paths.ProvidersFile(),
		configs:    make(map[string]ProviderConfig),
	}, nil
}

// Load loads configurations from disk
func (m *ProviderConfigManager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.configPath)
	if os.IsNotExist(err) {
		return nil // No config yet
	}
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &m.configs)
}

// Save saves configurations to disk
func (m *ProviderConfigManager) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := json.MarshalIndent(m.configs, "", "  ")
	if err != nil {
		return err
	}

	// Atomic, cross-process-locked write with secure (0600) permissions. The
	// lock guards the load-modify-save sequence used by AddProvider/Remove.
	return atomicfile.WithLock(m.configPath, func() error {
		return atomicfile.Write(m.configPath, data)
	})
}

// AddProvider adds or updates a provider configuration
func (m *ProviderConfigManager) AddProvider(config ProviderConfig) error {
	m.mu.Lock()
	m.configs[config.Name] = config
	m.mu.Unlock()

	return m.Save()
}

// GetProvider retrieves a provider configuration
func (m *ProviderConfigManager) GetProvider(name string) (ProviderConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config, ok := m.configs[name]
	return config, ok
}

// ListProviders returns all provider configurations
func (m *ProviderConfigManager) ListProviders() []ProviderConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var list []ProviderConfig
	for _, config := range m.configs {
		list = append(list, config)
	}
	return list
}

// RemoveProvider removes a provider configuration
func (m *ProviderConfigManager) RemoveProvider(name string) error {
	m.mu.Lock()
	delete(m.configs, name)
	m.mu.Unlock()

	return m.Save()
}
