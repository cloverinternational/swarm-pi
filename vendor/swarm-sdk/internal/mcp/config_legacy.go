package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type legacyOAuthConfig struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	Scopes       string `json:"scopes,omitempty"`
}

type legacyServerConfig struct {
	Name          string             `json:"name"`
	Type          string             `json:"type,omitempty"`
	Command       string             `json:"command,omitempty"`
	Args          []string           `json:"args,omitempty"`
	Env           map[string]string  `json:"env,omitempty"`
	WorkingDir    string             `json:"working_dir,omitempty"`
	URL           string             `json:"url,omitempty"`
	Headers       map[string]string  `json:"headers,omitempty"`
	Timeout       int                `json:"timeout,omitempty"`
	Enabled       bool               `json:"enabled"`
	OAuth         *legacyOAuthConfig `json:"oauth,omitempty"`
	EnabledTools  map[string]bool    `json:"enabled_tools,omitempty"`
	DisabledTools map[string]bool    `json:"disabled_tools,omitempty"`
}

func parseLegacyConfig(data []byte) (*ConfigFile, error) {
	var legacy []legacyServerConfig
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}
	servers := make([]ServerConfig, 0, len(legacy))
	for _, old := range legacy {
		cfg := ServerConfig{
			Name:       old.Name,
			Type:       old.Type,
			Command:    old.Command,
			Args:       old.Args,
			Env:        old.Env,
			WorkingDir: old.WorkingDir,
			URL:        old.URL,
			Headers:    old.Headers,
			TimeoutSec: old.Timeout,
			Enabled:    old.Enabled,
		}
		if old.OAuth != nil {
			if old.OAuth.ClientSecret != "" {
				return nil, fmt.Errorf("legacy oauth client_secret must be moved to credentials")
			}
			cfg.OAuth = &OAuthConfig{
				ClientID: old.OAuth.ClientID,
				Scopes:   splitScopes(old.OAuth.Scopes),
			}
		}
		if len(old.EnabledTools) > 0 && len(old.DisabledTools) > 0 {
			return nil, fmt.Errorf("legacy enabled_tools and disabled_tools are mutually exclusive")
		}
		if len(old.EnabledTools) > 0 {
			cfg.Tools = &ToolsConfig{Mode: "allowlist", Enabled: mapKeys(old.EnabledTools)}
		}
		if len(old.DisabledTools) > 0 {
			cfg.Tools = &ToolsConfig{Mode: "blocklist", Disabled: mapKeys(old.DisabledTools)}
		}
		servers = append(servers, cfg)
	}
	cfg := &ConfigFile{SchemaVersion: schemaVersion, Servers: servers}
	if err := ValidateConfigFile(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func splitScopes(scopes string) []string {
	if scopes == "" {
		return nil
	}
	return strings.Fields(scopes)
}

func mapKeys(input map[string]bool) []string {
	keys := make([]string, 0, len(input))
	for key, enabled := range input {
		if enabled {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}
