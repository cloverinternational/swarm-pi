package main

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// cmdConfigHooks handles hook configuration commands
func cmdConfigHooks(ctx context.Context, configDir string, args []string, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	if len(args) == 0 {
		return fmt.Errorf("hooks subcommand requires an action (list, add, remove, set)")
	}

	action := args[0]
	actionArgs := args[1:]

	// Create config manager
	configMgr := core.NewFileConfigManager(configDir, &nativeFS{})
	if err := configMgr.Load(ctx); err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	switch action {
	case "list":
		return listHooks(ctx, configMgr)
	case "add":
		return addHook(ctx, configMgr, actionArgs)
	case "remove", "delete":
		return removeHook(ctx, configMgr, actionArgs)
	case "set":
		return setHookProperty(ctx, configMgr, actionArgs)
	default:
		return fmt.Errorf("unknown hooks action: %s (use: list, add, remove, set)", action)
	}
}

// listHooks lists all hooks
func listHooks(ctx context.Context, configMgr core.ConfigManager) error {
	hooks := configMgr.GetHooks()

	if hooks == nil || len(hooks.Hooks) == 0 {
		fmt.Println("No hooks configured.")
		return nil
	}

	fmt.Printf("Configured Hooks (%d):\n\n", len(hooks.Hooks))
	for i, h := range hooks.Hooks {
		fmt.Printf("%d. %s\n", i+1, h.Name)
		if h.Description != "" {
			fmt.Printf("   Description: %s\n", h.Description)
		}
		fmt.Printf("   Event: %s\n", h.Event)
		fmt.Printf("   Command: %s\n", h.Command)
		if h.ToolMatch != "" {
			fmt.Printf("   Tool Match: %s\n", h.ToolMatch)
		}
		if h.Phase != "" {
			fmt.Printf("   Phase: %s\n", h.Phase)
		}
		fmt.Printf("   Enabled: %v\n", h.Enabled)
		if h.Timeout > 0 {
			fmt.Printf("   Timeout: %d seconds\n", h.Timeout)
		}
		fmt.Println()
	}

	return nil
}

// addHook adds a new hook
func addHook(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("add requires hook name")
	}

	hookName := args[0]
	options := parseKeyValueArgs(args[1:])

	hooks := configMgr.GetHooks()
	if hooks == nil {
		hooks = &core.HookConfig{}
	}

	// Check if hook already exists
	for _, h := range hooks.Hooks {
		if h.Name == hookName {
			return fmt.Errorf("hook already exists: %s", hookName)
		}
	}

	// Create hook
	hook := core.Hook{
		Name:    hookName,
		Enabled: true,
	}

	// Apply options (required fields)
	event, ok := options["event"]
	if !ok {
		return fmt.Errorf("event is required (use --event=<event>)")
	}
	hook.Event = event

	command, ok := options["command"]
	if !ok {
		return fmt.Errorf("command is required (use --command=<command>)")
	}
	hook.Command = command

	// Optional fields
	if description, ok := options["description"]; ok {
		hook.Description = description
	}
	if toolMatch, ok := options["tool-match"]; ok {
		hook.ToolMatch = toolMatch
	}
	if phase, ok := options["phase"]; ok {
		hook.Phase = phase
	}
	if enabled, ok := options["enabled"]; ok {
		hook.Enabled = enabled == "true"
	}

	// Add hook
	hooks.Hooks = append(hooks.Hooks, hook)

	if err := configMgr.SetHooks(hooks); err != nil {
		return fmt.Errorf("failed to add hook: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Added hook: %s\n", hookName)
	fmt.Printf("Event: %s\n", event)
	fmt.Printf("Command: %s\n", command)
	return nil
}

// removeHook removes a hook
func removeHook(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("remove requires hook name")
	}

	hookName := args[0]

	hooks := configMgr.GetHooks()
	if hooks == nil {
		return fmt.Errorf("no hooks configured")
	}

	// Find and remove hook
	found := false
	newHooks := make([]core.Hook, 0, len(hooks.Hooks))
	for _, h := range hooks.Hooks {
		if h.Name == hookName {
			found = true
			continue
		}
		newHooks = append(newHooks, h)
	}

	if !found {
		return fmt.Errorf("hook not found: %s", hookName)
	}

	hooks.Hooks = newHooks

	if err := configMgr.SetHooks(hooks); err != nil {
		return fmt.Errorf("failed to update hooks: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Removed hook: %s\n", hookName)
	return nil
}

// setHookProperty sets a property on a hook
func setHookProperty(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("set requires hook name and key=value pairs")
	}

	hookName := args[0]
	properties := parseKeyValueArgs(args[1:])

	hooks := configMgr.GetHooks()
	if hooks == nil {
		return fmt.Errorf("no hooks configured")
	}

	// Find hook
	var hook *core.Hook
	for i := range hooks.Hooks {
		if hooks.Hooks[i].Name == hookName {
			hook = &hooks.Hooks[i]
			break
		}
	}

	if hook == nil {
		return fmt.Errorf("hook not found: %s", hookName)
	}

	// Update properties
	changed := false
	for key, value := range properties {
		switch key {
		case "description":
			hook.Description = value
			changed = true
		case "event":
			hook.Event = value
			changed = true
		case "command":
			hook.Command = value
			changed = true
		case "tool-match":
			hook.ToolMatch = value
			changed = true
		case "phase":
			hook.Phase = value
			changed = true
		case "enabled":
			hook.Enabled = value == "true"
			changed = true
		default:
			fmt.Printf("Warning: unknown property: %s\n", key)
		}
	}

	if !changed {
		return fmt.Errorf("no valid properties specified")
	}

	if err := configMgr.SetHooks(hooks); err != nil {
		return fmt.Errorf("failed to update hooks: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Updated hook: %s\n", hookName)
	for key, value := range properties {
		fmt.Printf("  %s = %s\n", key, value)
	}

	return nil
}
