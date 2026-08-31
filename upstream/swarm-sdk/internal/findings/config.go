package findings

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadConfig loads configuration from the findings config file.
func LoadConfig(baseDir string) (Config, error) {
	configPath := filepath.Join(ExpandCacheDir(baseDir), "config.yaml")

	// Start with defaults
	config := DefaultConfig()

	// Try to load from file
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file exists, use defaults
			return config, nil
		}
		return config, fmt.Errorf("failed to read config: %w", err)
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("failed to parse config: %w", err)
	}

	return config, nil
}

// SaveConfig saves configuration to the findings config file.
func SaveConfig(baseDir string, config Config) error {
	configPath := filepath.Join(ExpandCacheDir(baseDir), "config.yaml")

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// EnsureDir ensures the findings base directory exists.
func EnsureDir(baseDir string) error {
	path := ExpandCacheDir(baseDir)
	return os.MkdirAll(path, 0755)
}
