package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// MCPConfigManager handles MCP server configuration persistence
type MCPConfigManager struct {
	configDir string
}

// NewMCPConfigManager creates a new MCP config manager
func NewMCPConfigManager() (*MCPConfigManager, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("commands_b.mcp_config.home_failed"), err)
	}

	configDir := filepath.Join(homeDir, ".swarmos")

	// Create config directory if it doesn't exist
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("commands_b.mcp_config.create_failed"), err)
	}

	return &MCPConfigManager{
		configDir: configDir,
	}, nil
}

// LoadServers loads MCP server configurations
func (m *MCPConfigManager) LoadServers() ([]*MCPServerConfig, error) {
	path := filepath.Join(m.configDir, "mcp_servers.json")

	// If file doesn't exist, return default servers
	if _, err := os.Stat(path); os.IsNotExist(err) {
		servers := m.getDefaultServers()
		if err := m.SaveServers(servers); err != nil {
			return nil, err
		}
		return servers, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("commands_b.mcp_config.read_failed"), err)
	}

	var servers []*MCPServerConfig
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("commands_b.mcp_config.parse_failed"), err)
	}

	return servers, nil
}

// SaveServers saves MCP server configurations
func (m *MCPConfigManager) SaveServers(servers []*MCPServerConfig) error {
	path := filepath.Join(m.configDir, "mcp_servers.json")

	data, err := json.MarshalIndent(servers, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("commands_b.mcp_config.marshal_failed"), err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("commands_b.mcp_config.write_failed"), err)
	}

	return nil
}

// getDefaultServers returns example MCP server configurations
func (m *MCPConfigManager) getDefaultServers() []*MCPServerConfig {
	return []*MCPServerConfig{
		{
			Name:          "filesystem",
			Command:       "npx",
			Args:          []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
			Env:           map[string]string{},
			Enabled:       false, // Disabled by default - user must enable
			EnabledTools:  make(map[string]bool),
			DisabledTools: make(map[string]bool),
		},
		{
			Name:    "github",
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-github"},
			Env: map[string]string{
				"GITHUB_PERSONAL_ACCESS_TOKEN": "", // User must set
			},
			Enabled:       false,
			EnabledTools:  make(map[string]bool),
			DisabledTools: make(map[string]bool),
		},
		{
			Name:    "brave-search",
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-brave-search"},
			Env: map[string]string{
				"BRAVE_API_KEY": "", // User must set
			},
			Enabled:       false,
			EnabledTools:  make(map[string]bool),
			DisabledTools: make(map[string]bool),
		},
	}
}
