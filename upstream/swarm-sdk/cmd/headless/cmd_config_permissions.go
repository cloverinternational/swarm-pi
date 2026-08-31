package main

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// cmdConfigPermissions handles permission configuration commands
func cmdConfigPermissions(ctx context.Context, configDir string, args []string, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	if len(args) == 0 {
		return fmt.Errorf("permissions subcommand requires an action (list, set, level)")
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
		return listPermissions(ctx, configMgr)
	case "set":
		return setPermission(ctx, configMgr, actionArgs)
	case "level":
		return setPermissionLevel(ctx, configMgr, actionArgs)
	default:
		return fmt.Errorf("unknown permissions action: %s (use: list, set, level)", action)
	}
}

// listPermissions lists all permission policies
func listPermissions(ctx context.Context, configMgr core.ConfigManager) error {
	policies := configMgr.GetPermissionPolicies()

	if policies == nil {
		fmt.Println("No permission policies configured (using defaults).")
		return nil
	}

	fmt.Print("Permission Configuration:\n")
	fmt.Printf("Level: %s\n", policies.Level)
	fmt.Printf("Timeout: %d seconds (%s on timeout)\n", policies.TimeoutSeconds, policies.TimeoutBehavior)
	fmt.Println()

	// List tool overrides
	if len(policies.Overrides.Tools) > 0 {
		fmt.Println("Tool Overrides:")
		for tool, policy := range policies.Overrides.Tools {
			fmt.Printf("  %s: %s\n", tool, policy)
		}
		fmt.Println()
	}

	// List permission overrides
	if len(policies.Overrides.Permissions) > 0 {
		fmt.Println("Permission Overrides:")
		for perm, policy := range policies.Overrides.Permissions {
			fmt.Printf("  %s: %s\n", perm, policy)
		}
		fmt.Println()
	}

	// List default policies
	if len(policies.Defaults.Policies) > 0 {
		fmt.Println("Default Policies:")
		for perm, policy := range policies.Defaults.Policies {
			fmt.Printf("  %s: %s\n", perm, policy)
		}
		fmt.Println()
	}

	return nil
}

// setPermission sets a permission override for a tool
func setPermission(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("set requires tool name and policy (always_allow/always_ask/always_deny)")
	}

	toolName := args[0]
	policyStr := args[1]

	// Validate policy
	var policy tools.OverridePolicy
	switch policyStr {
	case "always_allow", "allow":
		policy = tools.OverrideAlwaysAllow
	case "always_ask", "ask":
		policy = tools.OverrideAlwaysAsk
	case "always_deny", "deny":
		policy = tools.OverrideAlwaysDeny
	default:
		return fmt.Errorf("invalid policy: %s (use: always_allow, always_ask, always_deny)", policyStr)
	}

	policies := configMgr.GetPermissionPolicies()
	if policies == nil {
		policies = core.DefaultPermissionConfig()
	}

	if policies.Overrides.Tools == nil {
		policies.Overrides.Tools = make(map[string]tools.OverridePolicy)
	}

	// Set tool override
	policies.Overrides.Tools[toolName] = policy

	if err := configMgr.SetPermissionPolicies(policies); err != nil {
		return fmt.Errorf("failed to update permissions: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Set permission for %s: %s\n", toolName, policy)
	return nil
}

// setPermissionLevel sets the permission level
func setPermissionLevel(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("level requires permission level (always_ask/balanced/permissive)")
	}

	levelStr := args[0]

	// Validate level
	var level tools.PermissionLevel
	switch levelStr {
	case "always_ask":
		level = tools.LevelAlwaysAsk
	case "balanced":
		level = tools.LevelBalanced
	case "permissive":
		level = tools.LevelPermissive
	default:
		return fmt.Errorf("invalid level: %s (use: always_ask, balanced, permissive)", levelStr)
	}

	policies := configMgr.GetPermissionPolicies()
	if policies == nil {
		policies = core.DefaultPermissionConfig()
	}

	policies.Level = level

	if err := configMgr.SetPermissionPolicies(policies); err != nil {
		return fmt.Errorf("failed to update permissions: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Set permission level: %s\n", level)
	return nil
}
