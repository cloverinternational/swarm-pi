package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// cmdConfig is the main entry point for the config command
func cmdConfig(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	// Initialize config directory
	configDir, err := getConfigDir(config.ConfigDir)
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	// Parse subcommand from remaining args
	args := os.Args[2:] // Skip "headless config"
	if len(args) == 0 {
		printConfigUsage()
		return nil
	}

	// Remove any flags that were already parsed
	var cleanArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// Skip flags and their values
		if arg == "-config" || arg == "--config" || arg == "-verbose" || arg == "--verbose" {
			if i+1 < len(args) && !startsWith(args[i+1], "-") {
				i++ // Skip the value
			}
			continue
		}
		if startsWith(arg, "-") {
			continue
		}
		cleanArgs = append(cleanArgs, arg)
	}

	if len(cleanArgs) == 0 {
		printConfigUsage()
		return nil
	}

	subcommand := cleanArgs[0]
	subArgs := cleanArgs[1:]

	// Route to appropriate handler
	switch subcommand {
	case "providers":
		return cmdConfigProviders(ctx, configDir, subArgs, config, logger, tracer)
	case "models":
		return cmdConfigModels(ctx, configDir, subArgs, config, logger, tracer)
	case "profiles":
		return cmdConfigProfiles(ctx, configDir, subArgs, config, logger, tracer)
	case "mcp":
		return cmdConfigMCP(ctx, configDir, subArgs, config, logger, tracer)
	case "tools":
		return cmdConfigTools(ctx, configDir, subArgs, config, logger, tracer)
	case "permissions":
		return cmdConfigPermissions(ctx, configDir, subArgs, config, logger, tracer)
	case "hooks":
		return cmdConfigHooks(ctx, configDir, subArgs, config, logger, tracer)
	case "show":
		return cmdConfigShow(ctx, configDir, subArgs, config, logger, tracer)
	case "set":
		return cmdConfigSet(ctx, configDir, subArgs, config, logger, tracer)
	case "get":
		return cmdConfigGet(ctx, configDir, subArgs, config, logger, tracer)
	default:
		fmt.Fprintf(os.Stderr, "Unknown config subcommand: %s\n", subcommand)
		printConfigUsage()
		return fmt.Errorf("unknown config subcommand: %s", subcommand)
	}
}

// getConfigDir returns the configuration directory, using default if not specified
func getConfigDir(configDir string) (string, error) {
	if configDir != "" {
		return configDir, nil
	}

	// Use the single canonical config dir: ~/.swarm/config
	return paths.Config(), nil
}

// startsWith checks if a string starts with a prefix
func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// printConfigUsage prints usage information for the config command
func printConfigUsage() {
	fmt.Print(`Usage: headless config <subcommand> [options]

Configuration Management Subcommands:

  providers               Manage provider configurations
    list                    List all providers
    add <name> <options>    Add a new provider
    remove <name>           Remove a provider
    set <name> <key>=<val>  Set provider property

  models                  Manage models for providers
    list <provider>         List models for a provider
    add <provider> <id>     Add a model to a provider
    remove <provider> <id>  Remove a model from a provider
    set <provider> <id> <key>=<val>  Set model property

  profiles                Manage agent profiles
    list                    List all profiles
    show <name>             Show profile details
    create <name>           Create a new profile
    delete <name>           Delete a profile
    use <name>              Set active profile
    export <name>           Export profile to JSON
    import <file>           Import profile from JSON

  mcp                     Manage MCP servers
    list                    List all MCP servers
    add <name> <command>    Add an MCP server
    remove <name>           Remove an MCP server
    set <name> <key>=<val>  Set MCP server property

  tools                   List available tools
    list                    List all tools
    list --category <cat>   List tools by category

  permissions             Manage tool permissions
    list                    List all permission policies
    set <tool> <mode>       Set permission for tool (allow/deny/ask)

  hooks                   Manage hooks
    list                    List all hooks
    add <name>              Add a new hook
    remove <name>           Remove a hook
    set <name> <key>=<val>  Set hook property

  show                    Show all configuration
  set <key> <value>       Set a configuration value
  get <key>               Get a configuration value

Examples:
  headless config providers list
  headless config providers add openai --api-type openai --base-url https://api.openai.com/v1
  headless config models list anthropic
  headless config profiles create code-reviewer
  headless config profiles use code-reviewer
  headless config mcp add filesystem --command npx --args "-y @modelcontextprotocol/server-filesystem /tmp"
  headless config permissions set Bash ask
  headless config set defaultProvider anthropic
  headless config get defaultModel
`)
}
