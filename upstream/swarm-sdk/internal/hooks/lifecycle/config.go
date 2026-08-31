package lifecycle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadConfig loads hook configuration from a file.
// Supports JSON and YAML formats based on file extension.
func LoadConfig(path string) (*LifecycleHooksConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	return ParseConfig(data, filepath.Ext(path))
}

// ParseConfig parses hook configuration from bytes.
func ParseConfig(data []byte, format string) (*LifecycleHooksConfig, error) {
	var config LifecycleHooksConfig

	switch format {
	case ".json":
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to parse JSON config: %w", err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to parse YAML config: %w", err)
		}
	default:
		// Try JSON first, then YAML
		if err := json.Unmarshal(data, &config); err != nil {
			if err := yaml.Unmarshal(data, &config); err != nil {
				return nil, fmt.Errorf("failed to parse config as JSON or YAML")
			}
		}
	}

	// Compile all matchers
	if err := compileAllMatchers(&config); err != nil {
		return nil, fmt.Errorf("failed to compile matchers: %w", err)
	}

	return &config, nil
}

// LoadFromDirectory loads hooks configuration from a directory.
// Looks for hooks.json, hooks.yaml, or .swarm/hooks.json.
func LoadFromDirectory(dir string) (*LifecycleHooksConfig, error) {
	searchPaths := []string{
		filepath.Join(dir, "hooks.json"),
		filepath.Join(dir, "hooks.yaml"),
		filepath.Join(dir, "hooks.yml"),
		filepath.Join(dir, ".swarm", "hooks.json"),
		filepath.Join(dir, ".swarm", "hooks.yaml"),
		filepath.Join(dir, ".claude", "hooks.json"),
		filepath.Join(dir, ".claude", "hooks.yaml"),
	}

	for _, path := range searchPaths {
		if _, err := os.Stat(path); err == nil {
			return LoadConfig(path)
		}
	}

	// Return empty config if no file found
	return &LifecycleHooksConfig{}, nil
}

// MergeConfigs merges multiple configs, with later configs taking precedence.
func MergeConfigs(configs ...*LifecycleHooksConfig) *LifecycleHooksConfig {
	merged := &LifecycleHooksConfig{}

	for _, cfg := range configs {
		if cfg == nil {
			continue
		}

		merged.PreToolUse = append(merged.PreToolUse, cfg.PreToolUse...)
		merged.PostToolUse = append(merged.PostToolUse, cfg.PostToolUse...)
		merged.Stop = append(merged.Stop, cfg.Stop...)
		merged.SessionStart = append(merged.SessionStart, cfg.SessionStart...)
		merged.Notification = append(merged.Notification, cfg.Notification...)
		merged.PreMessage = append(merged.PreMessage, cfg.PreMessage...)
		merged.PostMessage = append(merged.PostMessage, cfg.PostMessage...)
	}

	return merged
}

// SaveConfig saves hook configuration to a file.
func SaveConfig(config *LifecycleHooksConfig, path string) error {
	var data []byte
	var err error

	ext := filepath.Ext(path)
	switch ext {
	case ".yaml", ".yml":
		data, err = yaml.Marshal(config)
	default:
		data, err = json.MarshalIndent(config, "", "  ")
	}

	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Ensure directory exists
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

func compileAllMatchers(config *LifecycleHooksConfig) error {
	compile := func(matchers []HookMatcher) error {
		for i := range matchers {
			if err := matchers[i].Compile(); err != nil {
				return fmt.Errorf("invalid matcher %q: %w", matchers[i].Matcher, err)
			}
		}
		return nil
	}

	if err := compile(config.PreToolUse); err != nil {
		return err
	}
	if err := compile(config.PostToolUse); err != nil {
		return err
	}
	if err := compile(config.Stop); err != nil {
		return err
	}
	if err := compile(config.SessionStart); err != nil {
		return err
	}
	if err := compile(config.Notification); err != nil {
		return err
	}
	if err := compile(config.PreMessage); err != nil {
		return err
	}
	if err := compile(config.PostMessage); err != nil {
		return err
	}

	return nil
}
