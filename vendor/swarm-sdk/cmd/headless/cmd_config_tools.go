package main

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/forge"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/web_fetch"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/websearch"
)

// cmdConfigTools handles tools listing commands
func cmdConfigTools(ctx context.Context, configDir string, args []string, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	action := "list"
	var category string

	if len(args) > 0 {
		action = args[0]
	}

	if action != "list" {
		return fmt.Errorf("unknown tools action: %s (only 'list' is supported)", action)
	}

	// Parse category filter
	for i := range args {
		if args[i] == "--category" && i+1 < len(args) {
			category = args[i+1]
			break
		}
	}

	return listTools(logger, tracer, category)
}

// listTools lists all available tools
func listTools(logger observability.Logger, tracer observability.Tracer, categoryFilter string) error {
	// Create tool registry with all available tools
	registry := tools.NewSimpleRegistry(logger, tracer)

	// Register builtin tools
	registry.Register(forge.NewFSRead(""))
	registry.Register(forge.NewFSWrite(""))
	registry.Register(builtin.NewBashTool())
	registry.Register(builtin.NewAnnoyedTool())
	registry.Register(forge.NewFSPatch(""))
	registry.Register(builtin.NewListDirTool())
	registry.Register(builtin.NewAgentBrowserTool())

	// Register websearch tool (Anthropic OAuth or Exa backend)
	if websearch.IsAuthConfigured() {
		registry.Register(websearch.New())
	}

	// Register web fetch tool (always available)
	registry.Register(web_fetch.New())

	toolNames := registry.List()
	if len(toolNames) == 0 {
		fmt.Println("No tools available.")
		return nil
	}

	fmt.Printf("Available Tools (%d):\n\n", len(toolNames))

	for i, name := range toolNames {
		tool, err := registry.Get(name)
		if err != nil {
			continue
		}

		// Apply category filter if specified
		if categoryFilter != "" {
			// For now, all tools are builtin
			// Can be extended to check tool.Category() if that interface exists
			if categoryFilter != "builtin" {
				continue
			}
		}

		fmt.Printf("%d. %s\n", i+1, tool.Name())
		fmt.Printf("   Description: %s\n", tool.Description())

		// Show permissions if available
		if permTool, ok := tool.(interface{ RequiredPermissions() []tools.Permission }); ok {
			perms := permTool.RequiredPermissions()
			if len(perms) > 0 {
				fmt.Printf("   Required Permissions: ")
				for j, perm := range perms {
					if j > 0 {
						fmt.Printf(", ")
					}
					fmt.Printf("%s", perm)
				}
				fmt.Println()
			}
		}

		fmt.Println()
	}

	return nil
}
