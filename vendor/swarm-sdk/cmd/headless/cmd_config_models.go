package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// cmdConfigModels handles model configuration commands
func cmdConfigModels(ctx context.Context, configDir string, args []string, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	if len(args) == 0 {
		return fmt.Errorf("models subcommand requires an action (list, add, remove, set)")
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
		return listModels(ctx, configMgr, actionArgs)
	case "add":
		return addModel(ctx, configMgr, actionArgs)
	case "remove", "delete":
		return removeModel(ctx, configMgr, actionArgs)
	case "set":
		return setModelProperty(ctx, configMgr, actionArgs)
	default:
		return fmt.Errorf("unknown models action: %s (use: list, add, remove, set)", action)
	}
}

// listModels lists models for a provider
func listModels(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("list requires provider name")
	}

	providerName := args[0]

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

	if len(provider.Models) == 0 {
		fmt.Printf("No models configured for provider: %s\n", providerName)
		return nil
	}

	fmt.Printf("Models for %s (%d):\n\n", providerName, len(provider.Models))
	for i, m := range provider.Models {
		fmt.Printf("%d. %s", i+1, m.ID)
		if m.Name != "" {
			fmt.Printf(" (%s)", m.Name)
		}
		fmt.Println()
		if m.ContextWindow > 0 {
			fmt.Printf("   Context Window: %d\n", m.ContextWindow)
		}
		if m.MaxOutput > 0 {
			fmt.Printf("   Max Output: %d\n", m.MaxOutput)
		}
		fmt.Printf("   Enabled: %v\n", m.Enabled)
		fmt.Printf("   Default: %v\n", m.Default)
		if m.Vision {
			fmt.Printf("   Vision: ✓\n")
		}
		if m.Thinking {
			fmt.Printf("   Thinking: ✓\n")
		}
		fmt.Println()
	}

	return nil
}

// addModel adds a model to a provider
func addModel(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("add requires provider name and model ID")
	}

	providerName := args[0]
	modelID := args[1]
	options := parseKeyValueArgs(args[2:])

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

	// Check if model already exists
	for _, m := range provider.Models {
		if m.ID == modelID {
			return fmt.Errorf("model already exists: %s", modelID)
		}
	}

	// Create model config
	model := core.ModelConfig{
		ID:      modelID,
		Enabled: true,
	}

	// Apply options
	if name, ok := options["name"]; ok {
		model.Name = name
	}
	if contextWindow, ok := options["context-window"]; ok {
		if cw, err := strconv.Atoi(contextWindow); err == nil {
			model.ContextWindow = cw
		}
	}
	if maxOutput, ok := options["max-output"]; ok {
		if mo, err := strconv.Atoi(maxOutput); err == nil {
			model.MaxOutput = mo
		}
	}
	if enabled, ok := options["enabled"]; ok {
		model.Enabled = enabled == "true"
	}
	if def, ok := options["default"]; ok {
		model.Default = def == "true"
	}
	if vision, ok := options["vision"]; ok {
		model.Vision = vision == "true"
	}
	if thinking, ok := options["thinking"]; ok {
		model.Thinking = thinking == "true"
	}

	// Add model to provider
	provider.Models = append(provider.Models, model)

	// Save provider
	if err := configMgr.SetProvider(*provider); err != nil {
		return fmt.Errorf("failed to update provider: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Added model %s to provider %s\n", modelID, providerName)
	if model.Name != "" {
		fmt.Printf("Display Name: %s\n", model.Name)
	}

	return nil
}

// removeModel removes a model from a provider
func removeModel(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("remove requires provider name and model ID")
	}

	providerName := args[0]
	modelID := args[1]

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

	// Find and remove model
	found := false
	newModels := make([]core.ModelConfig, 0, len(provider.Models))
	for _, m := range provider.Models {
		if m.ID == modelID {
			found = true
			continue
		}
		newModels = append(newModels, m)
	}

	if !found {
		return fmt.Errorf("model not found: %s", modelID)
	}

	provider.Models = newModels

	// Save provider
	if err := configMgr.SetProvider(*provider); err != nil {
		return fmt.Errorf("failed to update provider: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Removed model %s from provider %s\n", modelID, providerName)
	return nil
}

// setModelProperty sets a property on a model
func setModelProperty(ctx context.Context, configMgr core.ConfigManager, args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("set requires provider name, model ID, and key=value pairs")
	}

	providerName := args[0]
	modelID := args[1]
	properties := parseKeyValueArgs(args[2:])

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

	// Find model
	var model *core.ModelConfig
	for i := range provider.Models {
		if provider.Models[i].ID == modelID {
			model = &provider.Models[i]
			break
		}
	}

	if model == nil {
		return fmt.Errorf("model not found: %s", modelID)
	}

	// Update properties
	changed := false
	for key, value := range properties {
		switch key {
		case "name":
			model.Name = value
			changed = true
		case "context-window":
			if cw, err := strconv.Atoi(value); err == nil {
				model.ContextWindow = cw
				changed = true
			}
		case "max-output":
			if mo, err := strconv.Atoi(value); err == nil {
				model.MaxOutput = mo
				changed = true
			}
		case "enabled":
			model.Enabled = value == "true"
			changed = true
		case "default":
			model.Default = value == "true"
			changed = true
		case "vision":
			model.Vision = value == "true"
			changed = true
		case "thinking":
			model.Thinking = value == "true"
			changed = true
		default:
			fmt.Printf("Warning: unknown property: %s\n", key)
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

	fmt.Printf("Updated model %s in provider %s\n", modelID, providerName)
	for key, value := range properties {
		fmt.Printf("  %s = %s\n", key, value)
	}

	return nil
}
