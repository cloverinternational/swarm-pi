package mcp

import "github.com/Swarm-Code/mono/swarm-sdk/internal/plugins"

// PluginInfo describes a plugin and its metadata.
type PluginInfo struct {
	Name            string
	Source          plugins.PluginSource
	License         string
	Repository      string
	MarketplaceName string
	Enabled         bool
}

// PluginServerEntry binds a plugin to its MCP server config.
type PluginServerEntry struct {
	Plugin PluginInfo
	Server ServerConfig
}
