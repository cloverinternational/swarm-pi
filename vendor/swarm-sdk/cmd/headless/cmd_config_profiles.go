package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// cmdConfigProfiles handles profile configuration commands
func cmdConfigProfiles(ctx context.Context, configDir string, args []string, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	if len(args) == 0 {
		return fmt.Errorf("profiles subcommand requires an action (list, show, create, delete, use, export, import)")
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
		return listProfiles(ctx, configMgr)
	case "show":
		return showProfile(ctx, configMgr, actionArgs)
	case "create", "add":
		return createProfile(ctx, configMgr, actionArgs)
	case "delete", "remove":
		return deleteProfile(ctx, configMgr, actionArgs)
	case "use", "activate":
		return useProfile(ctx, configMgr, actionArgs)
	case "export":
		return exportProfile(ctx, configMgr, actionArgs)
	case "import":
		return importProfile(ctx, configMgr, actionArgs)
	default:
		return fmt.Errorf("unknown profiles action: %s (use: list, show, create, delete, use, export, import)", action)
	}
}

// listProfiles lists all profiles
func listProfiles(ctx context.Context, configMgr core.ConfigManager) error {
	profiles := configMgr.GetProfiles()

	if len(profiles) == 0 {
		fmt.Println("No profiles configured.")
		fmt.Println("\nCreate a profile with: headless config profiles create <name>")
		return nil
	}

	// Get active profile from config
	cfg := configMgr.GetConfig()
	activeProfile := cfg.DefaultAgent

	fmt.Printf("Configured Profiles (%d):\n\n", len(profiles))
	for i, p := range profiles {
		active := ""
		if p.Name == activeProfile {
			active = " [ACTIVE]"
		}
		fmt.Printf("%d. %s%s\n", i+1, p.Name, active)
		if p.Description != "" {
			fmt.Printf("   Description: %s\n", p.Description)
		}
		if p.SystemPrompt != "" {
			preview := p.SystemPrompt
			if len(preview) > 60 {
				preview = preview[:60] + "..."
			}
			fmt.Printf("   System Prompt: %s\n", preview)
		}
		if p.Temperature > 0 {
			fmt.Printf("   Temperature: %.2f\n", p.Temperature)
		}
		if p.MaxTokens > 0 {
			fmt.Printf("   Max Tokens: %d\n", p.MaxTokens)
		}
		if len(p.Tools) > 0 {
			fmt.Printf("   Tools: %s\n", strings.Join(p.Tools, ", "))
		}
		if len(p.DisabledTools) > 0 {
			fmt.Printf("   Disabled Tools: %s\n", strings.Join(p.DisabledTools, ", "))
		}
		fmt.Println()
	}

	return nil
}

// showProfile shows detailed information about a profile
func showProfile(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("show requires profile name")
	}

	profileName := args[0]

	// Find profile
	profiles := configMgr.GetProfiles()
	var profile *core.AgentProfile
	for i := range profiles {
		if profiles[i].Name == profileName {
			profile = &profiles[i]
			break
		}
	}

	if profile == nil {
		return fmt.Errorf("profile not found: %s", profileName)
	}

	// Print as JSON
	return jsonPrettyPrint(profile)
}

// createProfile creates a new profile
func createProfile(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("create requires profile name")
	}

	profileName := args[0]
	options := parseKeyValueArgs(args[1:])

	// Check if profile already exists
	profiles := configMgr.GetProfiles()
	for _, p := range profiles {
		if p.Name == profileName {
			return fmt.Errorf("profile already exists: %s (use 'set' to modify)", profileName)
		}
	}

	// Create profile
	profile := core.AgentProfile{
		Name: profileName,
	}

	// Apply options
	if description, ok := options["description"]; ok {
		profile.Description = description
	}
	if systemPrompt, ok := options["system-prompt"]; ok {
		profile.SystemPrompt = systemPrompt
	}
	if temp, ok := options["temperature"]; ok {
		if t, err := strconv.ParseFloat(temp, 64); err == nil {
			profile.Temperature = t
		}
	}
	if maxTokens, ok := options["max-tokens"]; ok {
		if mt, err := strconv.Atoi(maxTokens); err == nil {
			profile.MaxTokens = mt
		}
	}
	if tools, ok := options["tools"]; ok {
		profile.Tools = strings.Split(tools, ",")
	}
	if disabledTools, ok := options["disabled-tools"]; ok {
		profile.DisabledTools = strings.Split(disabledTools, ",")
	}

	// Save profile
	if err := configMgr.SetProfile(profile); err != nil {
		return fmt.Errorf("failed to create profile: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Created profile: %s\n", profileName)
	if profile.Description != "" {
		fmt.Printf("Description: %s\n", profile.Description)
	}

	return nil
}

// deleteProfile deletes a profile
func deleteProfile(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("delete requires profile name")
	}

	profileName := args[0]

	if err := configMgr.DeleteProfile(profileName); err != nil {
		return fmt.Errorf("failed to delete profile: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Deleted profile: %s\n", profileName)
	return nil
}

// useProfile sets the active profile
func useProfile(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("use requires profile name")
	}

	profileName := args[0]

	// Verify profile exists
	profiles := configMgr.GetProfiles()
	found := false
	for _, p := range profiles {
		if p.Name == profileName {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("profile not found: %s", profileName)
	}

	// Update config to use this profile
	cfg := configMgr.GetConfig()
	if cfg == nil {
		cfg = core.DefaultConfig()
	}
	cfg.DefaultAgent = profileName

	if err := configMgr.SetConfig(cfg); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Active profile set to: %s\n", profileName)
	return nil
}

// exportProfile exports a profile to JSON
func exportProfile(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("export requires profile name")
	}

	profileName := args[0]

	// Find profile
	profiles := configMgr.GetProfiles()
	var profile *core.AgentProfile
	for i := range profiles {
		if profiles[i].Name == profileName {
			profile = &profiles[i]
			break
		}
	}

	if profile == nil {
		return fmt.Errorf("profile not found: %s", profileName)
	}

	// Export as JSON to stdout
	return jsonPrettyPrint(profile)
}

// importProfile imports a profile from JSON
func importProfile(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("import requires JSON file path")
	}

	filePath := args[0]

	// Read JSON file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Parse JSON
	var profile core.AgentProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	if profile.Name == "" {
		return fmt.Errorf("profile name is required in JSON")
	}

	// Check if profile already exists
	profiles := configMgr.GetProfiles()
	for _, p := range profiles {
		if p.Name == profile.Name {
			return fmt.Errorf("profile already exists: %s (delete it first or change the name in JSON)", profile.Name)
		}
	}

	// Save profile
	if err := configMgr.SetProfile(profile); err != nil {
		return fmt.Errorf("failed to import profile: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Imported profile: %s\n", profile.Name)
	if profile.Description != "" {
		fmt.Printf("Description: %s\n", profile.Description)
	}

	return nil
}
