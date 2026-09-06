package chat

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// PermissionConfig stores tool permission settings on disk.
type PermissionConfig struct {
	mu     sync.RWMutex
	config tools.PermissionConfig
	path   string
}

// LoadPermissionConfig loads permission policies from ~/.swarmos/permissions.json.
func LoadPermissionConfig() (*PermissionConfig, error) {
	path, err := permissionConfigPath()
	if err != nil {
		return nil, err
	}

	cfg := &PermissionConfig{
		config: tools.DefaultPermissionConfig(),
		path:   path,
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		if saveErr := cfg.Save(); saveErr != nil {
			return cfg, saveErr
		}
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read permissions config: %w", err)
	}

	parsed, err := tools.DecodePermissionConfigStrict(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse permissions config: %w", err)
	}

	normalizePermissionConfig(&parsed)
	cfg.config = parsed
	return cfg, nil
}

// NewPermissionConfigWithDefaults creates an in-memory config with defaults.
func NewPermissionConfigWithDefaults() *PermissionConfig {
	path, _ := permissionConfigPath()
	cfg := &PermissionConfig{
		config: tools.DefaultPermissionConfig(),
		path:   path,
	}
	return cfg
}

// Save persists permission policies to disk with restrictive permissions.
func (c *PermissionConfig) Save() error {
	c.mu.RLock()
	config := c.config
	path := c.path
	c.mu.RUnlock()

	if path == "" {
		return fmt.Errorf("permission config path is empty")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create permissions dir: %w", err)
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal permissions config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write permissions config: %w", err)
	}

	return nil
}

// Config returns the current permission config.
func (c *PermissionConfig) Config() tools.PermissionConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return clonePermissionConfig(c.config)
}

// SetConfig replaces the current config.
func (c *PermissionConfig) SetConfig(config tools.PermissionConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	normalizePermissionConfig(&config)
	c.config = clonePermissionConfig(config)
}

// PolicyFor returns the policy for a permission.
func (c *PermissionConfig) PolicyFor(permission tools.Permission) tools.PermissionPolicy {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.config.Defaults.Policies == nil {
		return tools.PolicyAllow
	}
	if policy, ok := c.config.Defaults.Policies[permission]; ok {
		return policy
	}
	return tools.PolicyAllow
}

// SetPolicy updates the policy for a permission.
func (c *PermissionConfig) SetPolicy(permission tools.Permission, policy tools.PermissionPolicy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config.Defaults.Policies == nil {
		c.config.Defaults.Policies = tools.DefaultPermissionPolicies()
	}
	c.config.Defaults.Policies[permission] = policy
}

// PoliciesMap returns policies keyed by Permission type.
func (c *PermissionConfig) PoliciesMap() map[tools.Permission]tools.PermissionPolicy {
	c.mu.RLock()
	defer c.mu.RUnlock()

	defaults := tools.DefaultPermissionPolicies()
	out := make(map[tools.Permission]tools.PermissionPolicy, len(defaults))
	for perm, policy := range defaults {
		if c.config.Defaults.Policies != nil {
			if custom, ok := c.config.Defaults.Policies[perm]; ok {
				policy = custom
			}
		}
		out[perm] = policy
	}
	return out
}

func permissionConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home dir: %w", err)
	}
	return filepath.Join(home, ".swarmos", "permissions.json"), nil
}

// LoadProjectPermissionConfig loads project-level permissions from <workspace>/.swarmos/permissions.json.
func LoadProjectPermissionConfig(workspaceRoot string) (tools.PermissionConfig, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return tools.PermissionConfig{}, nil
	}

	path := filepath.Join(workspaceRoot, ".swarmos", "permissions.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tools.PermissionConfig{}, nil
		}
		return tools.PermissionConfig{}, fmt.Errorf("failed to read project permissions config: %w", err)
	}

	parsed, err := tools.DecodePermissionConfigStrict(data)
	if err != nil {
		return tools.PermissionConfig{}, fmt.Errorf("failed to parse project permissions config: %w", err)
	}

	return parsed, nil
}

// SaveProjectPermissionConfig saves project-level permissions to <workspaceRoot>/.swarmos/permissions.json.
func SaveProjectPermissionConfig(workspaceRoot string, config tools.PermissionConfig) error {
	if strings.TrimSpace(workspaceRoot) == "" {
		return fmt.Errorf("workspace root is empty")
	}

	dir := filepath.Join(workspaceRoot, ".swarmos")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create project .swarmos dir: %w", err)
	}

	path := filepath.Join(dir, "permissions.json")
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal project permissions config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write project permissions config: %w", err)
	}

	return nil
}

func normalizePermissionConfig(config *tools.PermissionConfig) {
	if config.Version == 0 {
		config.Version = 1
	}
	if config.Level == "" {
		config.Level = tools.LevelBalanced
	}
	if config.TimeoutSeconds <= 0 {
		config.TimeoutSeconds = 300
	}
	if config.TimeoutBehavior == "" {
		config.TimeoutBehavior = "stop"
	}
	if config.Defaults.Policies == nil {
		config.Defaults.Policies = tools.DefaultPermissionPolicies()
	} else {
		for perm, policy := range tools.DefaultPermissionPolicies() {
			if _, ok := config.Defaults.Policies[perm]; !ok {
				config.Defaults.Policies[perm] = policy
			}
		}
	}
	if config.Overrides.Tools == nil {
		config.Overrides.Tools = make(map[string]tools.OverridePolicy)
	}
	if config.Overrides.Permissions == nil {
		config.Overrides.Permissions = make(map[tools.Permission]tools.OverridePolicy)
	}
	if config.Rules == nil {
		config.Rules = make([]tools.PermissionRule, 0)
	}
}

func clonePermissionConfig(config tools.PermissionConfig) tools.PermissionConfig {
	cloned := config
	if config.Defaults.Policies != nil {
		cloned.Defaults.Policies = make(map[tools.Permission]tools.PermissionPolicy, len(config.Defaults.Policies))
		maps.Copy(cloned.Defaults.Policies, config.Defaults.Policies)
	}
	if config.Overrides.Tools != nil {
		cloned.Overrides.Tools = make(map[string]tools.OverridePolicy, len(config.Overrides.Tools))
		maps.Copy(cloned.Overrides.Tools, config.Overrides.Tools)
	}
	if config.Overrides.Permissions != nil {
		cloned.Overrides.Permissions = make(map[tools.Permission]tools.OverridePolicy, len(config.Overrides.Permissions))
		maps.Copy(cloned.Overrides.Permissions, config.Overrides.Permissions)
	}
	if config.Rules != nil {
		cloned.Rules = append([]tools.PermissionRule(nil), config.Rules...)
	}
	return cloned
}
