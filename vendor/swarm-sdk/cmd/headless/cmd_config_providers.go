package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// cmdConfigProviders handles provider configuration commands
func cmdConfigProviders(ctx context.Context, configDir string, args []string, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	if len(args) == 0 {
		return fmt.Errorf("providers subcommand requires an action (list, add, remove, set)")
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
		return listProviders(ctx, configMgr)
	case "add":
		return addProvider(ctx, configMgr, actionArgs)
	case "remove", "delete":
		return removeProvider(ctx, configMgr, actionArgs)
	case "set":
		return setProviderProperty(ctx, configMgr, actionArgs)
	default:
		return fmt.Errorf("unknown providers action: %s (use: list, add, remove, set)", action)
	}
}

// listProviders lists all configured providers
func listProviders(ctx context.Context, configMgr core.ConfigManager) error {
	providers := configMgr.GetProviders()

	if len(providers) == 0 {
		fmt.Println("No providers configured.")
		return nil
	}

	fmt.Printf("Configured Providers (%d):\n\n", len(providers))
	for i, p := range providers {
		fmt.Printf("%d. %s", i+1, p.Name)
		if p.DisplayName != "" {
			fmt.Printf(" (%s)", p.DisplayName)
		}
		fmt.Println()
		fmt.Printf("   API Type: %s\n", getProviderAPIType(p))
		if p.BaseURL != "" {
			fmt.Printf("   Base URL: %s\n", p.BaseURL)
		}
		if p.APIKeyEnv != "" {
			fmt.Printf("   API Key Env: %s\n", p.APIKeyEnv)
		}
		fmt.Printf("   Enabled: %v\n", p.Enabled)
		fmt.Printf("   Default: %v\n", p.Default)
		if len(p.Models) > 0 {
			fmt.Printf("   Models: %d configured\n", len(p.Models))
		}
		fmt.Println()
	}

	return nil
}

// addProvider adds a new provider
func addProvider(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("add requires provider name")
	}

	providerName := args[0]
	options := parseKeyValueArgs(args[1:])

	// Create provider config
	provider := core.ProviderConfig{
		Name:    providerName,
		Enabled: true,
	}

	// Apply options
	if displayName, ok := options["display-name"]; ok {
		provider.DisplayName = displayName
	}
	if apiType, ok := options["api-type"]; ok {
		provider.APIType = apiType
		provider.Type = apiType // legacy field
	}
	if authType, ok := options["auth-type"]; ok {
		provider.AuthType = authType
	}
	if baseURL, ok := options["base-url"]; ok {
		provider.BaseURL = baseURL
	}
	if apiKeyEnv, ok := options["api-key-env"]; ok {
		provider.APIKeyEnv = apiKeyEnv
	}
	if color, ok := options["color"]; ok {
		provider.Color = color
	}
	if enabled, ok := options["enabled"]; ok {
		provider.Enabled = enabled == "true"
	}
	if def, ok := options["default"]; ok {
		provider.Default = def == "true"
	}

	// Save provider
	if err := configMgr.SetProvider(provider); err != nil {
		return fmt.Errorf("failed to set provider: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Added provider: %s\n", providerName)
	if provider.DisplayName != "" {
		fmt.Printf("Display Name: %s\n", provider.DisplayName)
	}
	if provider.APIType != "" {
		fmt.Printf("API Type: %s\n", provider.APIType)
	}

	return nil
}

// removeProvider removes a provider
func removeProvider(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("remove requires provider name")
	}

	providerName := args[0]

	if err := configMgr.DeleteProvider(providerName); err != nil {
		return fmt.Errorf("failed to delete provider: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Removed provider: %s\n", providerName)
	return nil
}

// setProviderProperty sets a property on a provider
func setProviderProperty(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("set requires provider name and key=value pairs")
	}

	providerName := args[0]
	properties := parseKeyValueArgs(args[1:])

	// Find provider
	providers := configMgr.GetProviders()
	var provider *core.ProviderConfig
	for i := range providers {
		if providers[i].Name == providerName {
			provider = &providers[i]
			break
		}
	}

	if provider == nil {
		return fmt.Errorf("provider not found: %s", providerName)
	}

	// Update properties
	changed := false
	for key, value := range properties {
		switch key {
		case "display-name":
			provider.DisplayName = value
			changed = true
		case "api-type":
			provider.APIType = value
			provider.Type = value
			changed = true
		case "auth-type":
			provider.AuthType = value
			changed = true
		case "base-url":
			provider.BaseURL = value
			changed = true
		case "api-key-env":
			provider.APIKeyEnv = value
			changed = true
		case "color":
			provider.Color = value
			changed = true
		case "enabled":
			provider.Enabled = value == "true"
			changed = true
		case "default":
			provider.Default = value == "true"
			changed = true
		default:
			fmt.Fprintf(os.Stderr, "Warning: unknown property: %s\n", key)
		}
	}

	if !changed {
		return fmt.Errorf("no valid properties specified")
	}

	// Save provider
	if err := configMgr.SetProvider(*provider); err != nil {
		return fmt.Errorf("failed to update provider: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Updated provider: %s\n", providerName)
	for key, value := range properties {
		fmt.Printf("  %s = %s\n", key, value)
	}

	return nil
}

// parseKeyValueArgs parses key=value arguments
func parseKeyValueArgs(args []string) map[string]string {
	result := make(map[string]string)
	for _, arg := range args {
		// Handle --key value format
		if after, ok := strings.CutPrefix(arg, "--"); ok {
			key := after
			result[key] = "true" // Default to true for boolean flags
			continue
		}

		// Handle key=value format
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		} else if len(parts) == 1 {
			// Check if previous key needs this as value
			// For now, just skip
		}
	}
	return result
}

// getProviderAPIType returns the API type for a provider
func getProviderAPIType(p core.ProviderConfig) string {
	if p.APIType != "" {
		return p.APIType
	}
	if p.Type != "" {
		return p.Type
	}
	// Try to infer from name
	switch p.Name {
	case "anthropic":
		return "anthropic"
	case "openai":
		return "openai"
	case "cerebras", "openrouter", "deepseek", "groq", "together":
		return "openai-compatible"
	default:
		return "unknown"
	}
}

// nativeFS implements core.ConfigFileSystem using os package
type nativeFS struct{}

func (fs *nativeFS) Read(ctx context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (fs *nativeFS) Write(ctx context.Context, path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

func (fs *nativeFS) Exists(ctx context.Context, path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (fs *nativeFS) MkdirAll(ctx context.Context, path string) error {
	return os.MkdirAll(path, 0755)
}

func (fs *nativeFS) Rename(ctx context.Context, oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (fs *nativeFS) Remove(ctx context.Context, path string) error {
	return os.Remove(path)
}

// jsonPrettyPrint pretty prints JSON
func jsonPrettyPrint(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
