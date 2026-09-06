package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// cmdConfigMCP handles MCP server configuration commands
func cmdConfigMCP(ctx context.Context, configDir string, args []string, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	if len(args) == 0 {
		return fmt.Errorf("mcp subcommand requires an action (list, add, remove, set)")
	}

	action := args[0]
	actionArgs := args[1:]

	// Create MCP config manager
	mcpMgr := mcp.NewConfigManager(configDir, "", &nativeFS{}, nil)

	switch action {
	case "list":
		return listMCPServers(ctx, mcpMgr, actionArgs)
	case "add":
		return addMCPServer(ctx, mcpMgr, actionArgs)
	case "remove", "delete":
		return removeMCPServer(ctx, mcpMgr, actionArgs)
	case "set":
		return setMCPProperty(ctx, mcpMgr, actionArgs)
	case "enable":
		return setMCPEnabled(ctx, mcpMgr, actionArgs, true)
	case "disable":
		return setMCPEnabled(ctx, mcpMgr, actionArgs, false)
	default:
		return fmt.Errorf("unknown mcp action: %s (use: list, add, remove, set, enable, disable)", action)
	}
}

// listMCPServers lists all MCP servers
func listMCPServers(ctx context.Context, mcpMgr *mcp.ConfigManager, args []string) error {
	// Parse scope flag
	scope := "all"
	for i := range args {
		if args[i] == "--scope" && i+1 < len(args) {
			scope = args[i+1]
			break
		}
	}

	// Load layered config
	layered, err := mcpMgr.LoadLayered(ctx)
	if err != nil {
		return fmt.Errorf("failed to load MCP configuration: %w", err)
	}

	globalCount := 0
	projectCount := 0
	if layered.Global != nil {
		globalCount = len(layered.Global.Servers)
	}
	if layered.Project != nil {
		projectCount = len(layered.Project.Servers)
	}

	if globalCount == 0 && projectCount == 0 {
		fmt.Println("No MCP servers configured.")
		fmt.Println("\nAdd a server with: headless config mcp add <name> <command> [args...]")
		return nil
	}

	// Show global servers
	if (scope == "all" || scope == "global") && layered.Global != nil && len(layered.Global.Servers) > 0 {
		fmt.Printf("Global MCP Servers (%d):\n\n", len(layered.Global.Servers))
		for i, server := range layered.Global.Servers {
			printMCPServer(i+1, server, "global")
		}
	}

	// Show project servers
	if (scope == "all" || scope == "project") && layered.Project != nil && len(layered.Project.Servers) > 0 {
		fmt.Printf("Project MCP Servers (%d):\n\n", len(layered.Project.Servers))
		for i, server := range layered.Project.Servers {
			printMCPServer(i+1, server, "project")
		}
	}

	return nil
}

func printMCPServer(index int, server mcp.ServerConfig, scope string) {
	fmt.Printf("%d. %s [%s]\n", index, server.Name, scope)
	if server.Type != "" {
		fmt.Printf("   Type: %s\n", server.Type)
	}
	if server.Command != "" {
		fmt.Printf("   Command: %s\n", server.Command)
	}
	if len(server.Args) > 0 {
		fmt.Printf("   Args: %s\n", strings.Join(server.Args, " "))
	}
	if server.WorkingDir != "" {
		fmt.Printf("   Working Dir: %s\n", server.WorkingDir)
	}
	if server.URL != "" {
		fmt.Printf("   URL: %s\n", server.URL)
	}
	fmt.Printf("   Enabled: %v\n", server.Enabled)
	if len(server.Env) > 0 {
		fmt.Printf("   Environment: %d vars\n", len(server.Env))
	}
	if server.TimeoutSec > 0 {
		fmt.Printf("   Timeout: %d seconds\n", server.TimeoutSec)
	}
	fmt.Println()
}

// addMCPServer adds a new MCP server
func addMCPServer(ctx context.Context, mcpMgr *mcp.ConfigManager, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("add requires server name and command")
	}

	serverName := args[0]
	command := args[1]

	// Parse remaining args
	var serverArgs []string
	options := make(map[string]string)
	scope := "global"

	for i := 2; i < len(args); i++ {
		arg := args[i]
		if after, ok := strings.CutPrefix(arg, "--"); ok {
			// Parse option
			key := after
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				options[key] = args[i+1]
				i++
			} else {
				options[key] = "true"
			}
		} else {
			// Add to server args
			serverArgs = append(serverArgs, arg)
		}
	}

	// Check for scope option
	if s, ok := options["scope"]; ok {
		scope = s
		delete(options, "scope")
	}

	// Load existing config
	layered, err := mcpMgr.LoadLayered(ctx)
	if err != nil {
		return fmt.Errorf("failed to load MCP configuration: %w", err)
	}

	var cfg *mcp.ConfigFile
	var configScope mcp.ConfigScope
	if scope == "project" {
		cfg = layered.Project
		configScope = mcp.ScopeProject
		if cfg == nil {
			cfg = &mcp.ConfigFile{Servers: []mcp.ServerConfig{}}
		}
	} else {
		cfg = layered.Global
		configScope = mcp.ScopeGlobal
		if cfg == nil {
			cfg = &mcp.ConfigFile{Servers: []mcp.ServerConfig{}}
		}
	}

	// Check if server already exists
	for _, s := range cfg.Servers {
		if s.Name == serverName {
			return fmt.Errorf("MCP server already exists: %s (in %s scope)", serverName, scope)
		}
	}

	// Create MCP server config
	server := mcp.ServerConfig{
		Name:    serverName,
		Command: command,
		Args:    serverArgs,
		Enabled: true,
	}

	// Apply options
	if serverType, ok := options["type"]; ok {
		server.Type = serverType
	}
	if workDir, ok := options["working-dir"]; ok {
		server.WorkingDir = workDir
	}
	if url, ok := options["url"]; ok {
		server.URL = url
	}
	if enabled, ok := options["enabled"]; ok {
		server.Enabled = enabled == "true"
	}
	if timeout, ok := options["timeout"]; ok {
		if t, err := strconv.Atoi(timeout); err == nil {
			server.TimeoutSec = t
		}
	}
	if envStr, ok := options["env"]; ok {
		server.Env = make(map[string]string)
		for pair := range strings.SplitSeq(envStr, ",") {
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) == 2 {
				server.Env[parts[0]] = parts[1]
			}
		}
	}

	// Add server
	cfg.Servers = append(cfg.Servers, server)

	// Save config
	if err := mcpMgr.SaveLayer(ctx, configScope, cfg); err != nil {
		return fmt.Errorf("failed to save MCP configuration: %w", err)
	}

	fmt.Printf("Added MCP server: %s [%s]\n", serverName, scope)
	fmt.Printf("Command: %s\n", command)
	if len(serverArgs) > 0 {
		fmt.Printf("Args: %s\n", strings.Join(serverArgs, " "))
	}
	return nil
}

// removeMCPServer removes an MCP server
func removeMCPServer(ctx context.Context, mcpMgr *mcp.ConfigManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("remove requires server name")
	}

	serverName := args[0]
	scope := "global"

	// Parse scope option
	for i := 1; i < len(args); i++ {
		if args[i] == "--scope" && i+1 < len(args) {
			scope = args[i+1]
			break
		}
	}

	// Load existing config
	layered, err := mcpMgr.LoadLayered(ctx)
	if err != nil {
		return fmt.Errorf("failed to load MCP configuration: %w", err)
	}

	var cfg *mcp.ConfigFile
	var configScope mcp.ConfigScope
	if scope == "project" {
		cfg = layered.Project
		configScope = mcp.ScopeProject
	} else {
		cfg = layered.Global
		configScope = mcp.ScopeGlobal
	}

	if cfg == nil {
		return fmt.Errorf("no MCP configuration found in %s scope", scope)
	}

	// Find and remove server
	found := false
	newServers := make([]mcp.ServerConfig, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		if s.Name == serverName {
			found = true
			continue
		}
		newServers = append(newServers, s)
	}

	if !found {
		return fmt.Errorf("MCP server not found: %s (in %s scope)", serverName, scope)
	}

	cfg.Servers = newServers

	// Save config
	if err := mcpMgr.SaveLayer(ctx, configScope, cfg); err != nil {
		return fmt.Errorf("failed to save MCP configuration: %w", err)
	}

	fmt.Printf("Removed MCP server: %s [%s]\n", serverName, scope)
	return nil
}

// setMCPProperty sets a property on an MCP server
func setMCPProperty(ctx context.Context, mcpMgr *mcp.ConfigManager, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("set requires server name and key=value pairs")
	}

	serverName := args[0]
	properties := parseKeyValueArgs(args[1:])

	scope := "global"
	if s, ok := properties["scope"]; ok {
		scope = s
		delete(properties, "scope")
	}

	// Load existing config
	layered, err := mcpMgr.LoadLayered(ctx)
	if err != nil {
		return fmt.Errorf("failed to load MCP configuration: %w", err)
	}

	var cfg *mcp.ConfigFile
	var configScope mcp.ConfigScope
	if scope == "project" {
		cfg = layered.Project
		configScope = mcp.ScopeProject
	} else {
		cfg = layered.Global
		configScope = mcp.ScopeGlobal
	}

	if cfg == nil {
		return fmt.Errorf("no MCP configuration found in %s scope", scope)
	}

	// Find server
	var server *mcp.ServerConfig
	for i := range cfg.Servers {
		if cfg.Servers[i].Name == serverName {
			server = &cfg.Servers[i]
			break
		}
	}

	if server == nil {
		return fmt.Errorf("MCP server not found: %s (in %s scope)", serverName, scope)
	}

	// Update properties
	changed := false
	for key, value := range properties {
		switch key {
		case "type":
			server.Type = value
			changed = true
		case "command":
			server.Command = value
			changed = true
		case "args":
			server.Args = strings.Split(value, " ")
			changed = true
		case "working-dir":
			server.WorkingDir = value
			changed = true
		case "url":
			server.URL = value
			changed = true
		case "enabled":
			server.Enabled = value == "true"
			changed = true
		case "timeout":
			if t, err := strconv.Atoi(value); err == nil {
				server.TimeoutSec = t
				changed = true
			}
		default:
			fmt.Printf("Warning: unknown property: %s\n", key)
		}
	}

	if !changed {
		return fmt.Errorf("no valid properties specified")
	}

	// Save config
	if err := mcpMgr.SaveLayer(ctx, configScope, cfg); err != nil {
		return fmt.Errorf("failed to save MCP configuration: %w", err)
	}

	fmt.Printf("Updated MCP server: %s [%s]\n", serverName, scope)
	for key, value := range properties {
		fmt.Printf("  %s = %s\n", key, value)
	}

	return nil
}

// setMCPEnabled enables or disables an MCP server
func setMCPEnabled(ctx context.Context, mcpMgr *mcp.ConfigManager, args []string, enabled bool) error {
	if len(args) == 0 {
		return fmt.Errorf("requires server name")
	}

	serverName := args[0]
	scope := "global"

	// Parse scope option
	for i := 1; i < len(args); i++ {
		if args[i] == "--scope" && i+1 < len(args) {
			scope = args[i+1]
			break
		}
	}

	// Load existing config
	layered, err := mcpMgr.LoadLayered(ctx)
	if err != nil {
		return fmt.Errorf("failed to load MCP configuration: %w", err)
	}

	var cfg *mcp.ConfigFile
	var configScope mcp.ConfigScope
	if scope == "project" {
		cfg = layered.Project
		configScope = mcp.ScopeProject
	} else {
		cfg = layered.Global
		configScope = mcp.ScopeGlobal
	}

	if cfg == nil {
		return fmt.Errorf("no MCP configuration found in %s scope", scope)
	}

	// Find server
	var server *mcp.ServerConfig
	for i := range cfg.Servers {
		if cfg.Servers[i].Name == serverName {
			server = &cfg.Servers[i]
			break
		}
	}

	if server == nil {
		return fmt.Errorf("MCP server not found: %s (in %s scope)", serverName, scope)
	}

	server.Enabled = enabled

	// Save config
	if err := mcpMgr.SaveLayer(ctx, configScope, cfg); err != nil {
		return fmt.Errorf("failed to save MCP configuration: %w", err)
	}

	status := "enabled"
	if !enabled {
		status = "disabled"
	}
	fmt.Printf("MCP server %s: %s [%s]\n", serverName, status, scope)
	return nil
}
