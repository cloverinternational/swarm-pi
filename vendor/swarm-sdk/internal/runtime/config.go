// Package runtime provides runtime configuration for dynamic agent behavior.
package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// RuntimeConfig is a hot-reloadable configuration that agents can modify.
// It's checked before every tool execution, allowing dynamic behavior changes.
type RuntimeConfig struct {
	mu sync.RWMutex

	// Path to the config file
	configPath string

	// LastModified tracks when the config was last updated
	lastModified time.Time

	// Hooks defines dynamic hooks to register at runtime
	Hooks []DynamicHook `json:"hooks"`

	// ToolPermissions overrides tool permissions
	ToolPermissions map[string]ToolPermission `json:"tool_permissions"`

	// AgentBehaviors defines per-agent behavior overrides
	AgentBehaviors map[string]AgentBehavior `json:"agent_behaviors"`

	// GlobalSettings are system-wide settings
	GlobalSettings map[string]any `json:"global_settings"`

	// Raw holds the full JSON for extensibility
	Raw map[string]any `json:"-"`
}

// DynamicHook defines a hook that can be added at runtime
type DynamicHook struct {
	Name     string         `json:"name"`
	Type     string         `json:"type"` // "pre_tool", "post_tool", "pre_message", etc.
	Priority int            `json:"priority"`
	Enabled  bool           `json:"enabled"`
	Filter   map[string]any `json:"filter"`   // Conditions to match
	Action   map[string]any `json:"action"`   // What to do
	Metadata map[string]any `json:"metadata"` // Extra data
}

// ToolPermission defines permissions for a specific tool
type ToolPermission struct {
	Allowed      bool     `json:"allowed"`
	AllowedPaths []string `json:"allowed_paths,omitempty"` // Glob patterns
	DeniedPaths  []string `json:"denied_paths,omitempty"`  // Glob patterns
	MaxSize      int64    `json:"max_size,omitempty"`      // Max file size
	Timeout      int      `json:"timeout,omitempty"`       // Timeout in seconds
}

// AgentBehavior defines behavior overrides for a specific agent
type AgentBehavior struct {
	MaxTurns    int            `json:"max_turns,omitempty"`
	Temperature float64        `json:"temperature,omitempty"`
	Disabled    bool           `json:"disabled,omitempty"`
	Custom      map[string]any `json:"custom,omitempty"`
}

// NewRuntimeConfig creates a new runtime config from a file
func NewRuntimeConfig(path string) (*RuntimeConfig, error) {
	rc := &RuntimeConfig{
		configPath:      path,
		ToolPermissions: make(map[string]ToolPermission),
		AgentBehaviors:  make(map[string]AgentBehavior),
		GlobalSettings:  make(map[string]any),
	}

	if err := rc.Load(); err != nil {
		return nil, err
	}

	return rc, nil
}

// Load loads the configuration from the file
func (rc *RuntimeConfig) Load() error {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	// Read file
	data, err := os.ReadFile(rc.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, that's OK
			return nil
		}
		return fmt.Errorf("failed to read config: %w", err)
	}

	// Parse JSON
	if err := json.Unmarshal(data, rc); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	// Store raw JSON for extensibility
	if err := json.Unmarshal(data, &rc.Raw); err != nil {
		return fmt.Errorf("failed to parse raw config: %w", err)
	}

	// Update last modified time
	info, err := os.Stat(rc.configPath)
	if err == nil {
		rc.lastModified = info.ModTime()
	}

	return nil
}

// GetHooks returns all enabled hooks
func (rc *RuntimeConfig) GetHooks() []DynamicHook {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	var enabled []DynamicHook
	for _, hook := range rc.Hooks {
		if hook.Enabled {
			enabled = append(enabled, hook)
		}
	}
	return enabled
}

// GetToolPermission returns the permission for a specific tool
func (rc *RuntimeConfig) GetToolPermission(toolName string) (ToolPermission, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	perm, exists := rc.ToolPermissions[toolName]
	return perm, exists
}

// GetAgentBehavior returns the behavior override for a specific agent
func (rc *RuntimeConfig) GetAgentBehavior(agentID string) (AgentBehavior, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	behavior, exists := rc.AgentBehaviors[agentID]
	return behavior, exists
}

// GetGlobalSetting returns a global setting value
func (rc *RuntimeConfig) GetGlobalSetting(key string) (any, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	value, exists := rc.GlobalSettings[key]
	return value, exists
}

// LastModified returns when the config was last modified
func (rc *RuntimeConfig) LastModified() time.Time {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.lastModified
}
