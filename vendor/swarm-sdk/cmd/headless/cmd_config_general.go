package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// cmdConfigShow shows all configuration
func cmdConfigShow(ctx context.Context, configDir string, args []string, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	// Create config manager
	configMgr := core.NewFileConfigManager(configDir, &nativeFS{})
	if err := configMgr.Load(ctx); err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	cfg := configMgr.GetConfig()
	if cfg == nil {
		fmt.Println("No configuration found.")
		return nil
	}

	fmt.Println("Configuration:")
	fmt.Println()

	// General settings
	if cfg.DefaultProvider != "" {
		fmt.Printf("  Default Provider: %s\n", cfg.DefaultProvider)
	}
	if cfg.DefaultModel != "" {
		fmt.Printf("  Default Model: %s\n", cfg.DefaultModel)
	}
	if cfg.DefaultMode != "" {
		fmt.Printf("  Default Mode: %s\n", cfg.DefaultMode)
	}
	if cfg.DefaultAgent != "" {
		fmt.Printf("  Default Agent: %s\n", cfg.DefaultAgent)
	}
	if cfg.Theme != "" {
		fmt.Printf("  Theme: %s\n", cfg.Theme)
	}

	// Display settings
	fmt.Println()
	fmt.Println("Display Settings:")
	fmt.Printf("  Show Thinking: %v\n", cfg.ShowThinking)
	fmt.Printf("  Show Token Count: %v\n", cfg.ShowTokenCount)
	fmt.Printf("  Show Tool Output: %v\n", cfg.ShowToolOutput)
	fmt.Printf("  Compact Mode: %v\n", cfg.CompactMode)
	if cfg.MaxOutputLines > 0 {
		fmt.Printf("  Max Output Lines: %d\n", cfg.MaxOutputLines)
	}
	fmt.Printf("  Syntax Highlighting: %v\n", cfg.SyntaxHighlighting)

	// Behavior settings
	fmt.Println()
	fmt.Println("Behavior Settings:")
	fmt.Printf("  Auto Save Conversations: %v\n", cfg.AutoSaveConversations)
	fmt.Printf("  Confirm Before Exit: %v\n", cfg.ConfirmBeforeExit)
	fmt.Printf("  Enable Logging: %v\n", cfg.EnableLogging)
	if cfg.LogLevel != "" {
		fmt.Printf("  Log Level: %s\n", cfg.LogLevel)
	}

	// Compaction settings
	if cfg.EnableCompaction {
		fmt.Println()
		fmt.Println("Compaction Settings:")
		fmt.Printf("  Enable Compaction: %v\n", cfg.EnableCompaction)
		if cfg.CompactionThreshold > 0 {
			fmt.Printf("  Compaction Threshold: %d\n", cfg.CompactionThreshold)
		}
		if cfg.WarningThreshold > 0 {
			fmt.Printf("  Warning Threshold: %d\n", cfg.WarningThreshold)
		}
		if cfg.PreserveRecentMessages > 0 {
			fmt.Printf("  Preserve Recent Messages: %d\n", cfg.PreserveRecentMessages)
		}
	}

	// Cloud sync settings
	if cfg.SyncConversations || cfg.SyncSettings {
		fmt.Println()
		fmt.Println("Cloud Sync Settings:")
		fmt.Printf("  Sync Conversations: %v\n", cfg.SyncConversations)
		fmt.Printf("  Sync Settings: %v\n", cfg.SyncSettings)
		fmt.Printf("  Encrypt Cloud Data: %v\n", cfg.EncryptCloudData)
	}

	fmt.Println()
	return nil
}

// cmdConfigSet sets a configuration value
func cmdConfigSet(ctx context.Context, configDir string, args []string, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	if len(args) < 2 {
		return fmt.Errorf("set requires key and value")
	}

	key := args[0]
	value := args[1]

	// Create config manager
	configMgr := core.NewFileConfigManager(configDir, &nativeFS{})
	if err := configMgr.Load(ctx); err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	cfg := configMgr.GetConfig()
	if cfg == nil {
		cfg = core.DefaultConfig()
	}

	// Update configuration based on key
	changed := false
	switch key {
	case "defaultProvider":
		cfg.DefaultProvider = value
		changed = true
	case "defaultModel":
		cfg.DefaultModel = value
		changed = true
	case "defaultMode":
		cfg.DefaultMode = value
		changed = true
	case "defaultAgent":
		cfg.DefaultAgent = value
		changed = true
	case "theme":
		cfg.Theme = value
		changed = true
	case "showThinking":
		cfg.ShowThinking = value == "true"
		changed = true
	case "showTokenCount":
		cfg.ShowTokenCount = value == "true"
		changed = true
	case "showToolOutput":
		cfg.ShowToolOutput = value == "true"
		changed = true
	case "compactMode":
		cfg.CompactMode = value == "true"
		changed = true
	case "maxOutputLines":
		if lines, err := strconv.Atoi(value); err == nil {
			cfg.MaxOutputLines = lines
			changed = true
		}
	case "syntaxHighlighting":
		cfg.SyntaxHighlighting = value == "true"
		changed = true
	case "autoSaveConversations":
		cfg.AutoSaveConversations = value == "true"
		changed = true
	case "confirmBeforeExit":
		cfg.ConfirmBeforeExit = value == "true"
		changed = true
	case "enableLogging":
		cfg.EnableLogging = value == "true"
		changed = true
	case "logLevel":
		cfg.LogLevel = value
		changed = true
	case "enableCompaction":
		cfg.EnableCompaction = value == "true"
		changed = true
	case "compactionThreshold":
		if threshold, err := strconv.Atoi(value); err == nil {
			cfg.CompactionThreshold = threshold
			changed = true
		}
	case "syncConversations":
		cfg.SyncConversations = value == "true"
		changed = true
	case "syncSettings":
		cfg.SyncSettings = value == "true"
		changed = true
	case "encryptCloudData":
		cfg.EncryptCloudData = value == "true"
		changed = true
	default:
		return fmt.Errorf("unknown configuration key: %s", key)
	}

	if !changed {
		return fmt.Errorf("failed to set configuration value")
	}

	if err := configMgr.SetConfig(cfg); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	if err := configMgr.Save(ctx); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Printf("Set %s = %s\n", key, value)
	return nil
}

// cmdConfigGet gets a configuration value
func cmdConfigGet(ctx context.Context, configDir string, args []string, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	if len(args) < 1 {
		return fmt.Errorf("get requires key")
	}

	key := args[0]

	// Create config manager
	configMgr := core.NewFileConfigManager(configDir, &nativeFS{})
	if err := configMgr.Load(ctx); err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	cfg := configMgr.GetConfig()
	if cfg == nil {
		fmt.Printf("%s: <not set>\n", key)
		return nil
	}

	// Get configuration value based on key
	var value any
	switch key {
	case "defaultProvider":
		value = cfg.DefaultProvider
	case "defaultModel":
		value = cfg.DefaultModel
	case "defaultMode":
		value = cfg.DefaultMode
	case "defaultAgent":
		value = cfg.DefaultAgent
	case "theme":
		value = cfg.Theme
	case "showThinking":
		value = cfg.ShowThinking
	case "showTokenCount":
		value = cfg.ShowTokenCount
	case "showToolOutput":
		value = cfg.ShowToolOutput
	case "compactMode":
		value = cfg.CompactMode
	case "maxOutputLines":
		value = cfg.MaxOutputLines
	case "syntaxHighlighting":
		value = cfg.SyntaxHighlighting
	case "autoSaveConversations":
		value = cfg.AutoSaveConversations
	case "confirmBeforeExit":
		value = cfg.ConfirmBeforeExit
	case "enableLogging":
		value = cfg.EnableLogging
	case "logLevel":
		value = cfg.LogLevel
	case "enableCompaction":
		value = cfg.EnableCompaction
	case "compactionThreshold":
		value = cfg.CompactionThreshold
	case "syncConversations":
		value = cfg.SyncConversations
	case "syncSettings":
		value = cfg.SyncSettings
	case "encryptCloudData":
		value = cfg.EncryptCloudData
	default:
		return fmt.Errorf("unknown configuration key: %s", key)
	}

	if value == "" || value == 0 || value == false {
		fmt.Printf("%s: <not set>\n", key)
	} else {
		fmt.Printf("%s: %v\n", key, value)
	}

	return nil
}
