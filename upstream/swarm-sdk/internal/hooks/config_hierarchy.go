// Package hooks implements the event hook system.
// This file provides configuration hierarchy loading (project -> user -> system).
package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// HookDefinition defines a matcher with associated hooks.
type HookDefinition struct {
	Matcher    string              `json:"matcher" yaml:"matcher"`
	Sequential bool                `json:"sequential,omitempty" yaml:"sequential,omitempty"`
	Hooks      []CommandHookConfig `json:"hooks" yaml:"hooks"`
	// Compiled regex (internal)
	compiled *regexp.Regexp
}

// Compile compiles the matcher pattern into a regex.
func (h *HookDefinition) Compile() error {
	if h.Matcher == "" || h.Matcher == "*" {
		h.compiled = regexp.MustCompile(".*")
		return nil
	}
	re, err := regexp.Compile(h.Matcher)
	if err != nil {
		return err
	}
	h.compiled = re
	return nil
}

// Matches checks if the input matches this matcher.
func (h *HookDefinition) Matches(input string) bool {
	if h.compiled == nil {
		return h.Matcher == "*" || h.Matcher == "" || h.Matcher == input
	}
	return h.compiled.MatchString(input)
}

// CommandHookConfig defines a single command hook.
type CommandHookConfig struct {
	Type        string       `json:"type" yaml:"type"`                     // "command"
	Name        string       `json:"name,omitempty" yaml:"name,omitempty"` // unique identifier
	Command     string       `json:"command" yaml:"command"`               // shell command
	Description string       `json:"description,omitempty" yaml:"description,omitempty"`
	Timeout     int          `json:"timeout,omitempty" yaml:"timeout,omitempty"` // milliseconds
	Source      ConfigSource `json:"source,omitempty" yaml:"source,omitempty"`   // where this came from
}

// GetKey returns a unique key for deduplication.
func (c *CommandHookConfig) GetKey() string {
	return fmt.Sprintf("%s:%s", c.Name, c.Command)
}

// HooksConfig is the complete hooks configuration.
// Supports both Claude Code and Gemini CLI naming conventions.
type HooksConfig struct {
	// === Gemini CLI naming (canonical) ===
	SessionStart        []HookDefinition `json:"SessionStart,omitempty" yaml:"SessionStart,omitempty"`
	SessionEnd          []HookDefinition `json:"SessionEnd,omitempty" yaml:"SessionEnd,omitempty"`
	BeforeAgent         []HookDefinition `json:"BeforeAgent,omitempty" yaml:"BeforeAgent,omitempty"`
	AfterAgent          []HookDefinition `json:"AfterAgent,omitempty" yaml:"AfterAgent,omitempty"`
	BeforeModel         []HookDefinition `json:"BeforeModel,omitempty" yaml:"BeforeModel,omitempty"`
	AfterModel          []HookDefinition `json:"AfterModel,omitempty" yaml:"AfterModel,omitempty"`
	BeforeToolSelection []HookDefinition `json:"BeforeToolSelection,omitempty" yaml:"BeforeToolSelection,omitempty"`
	BeforeTool          []HookDefinition `json:"BeforeTool,omitempty" yaml:"BeforeTool,omitempty"`
	AfterTool           []HookDefinition `json:"AfterTool,omitempty" yaml:"AfterTool,omitempty"`
	PreCompress         []HookDefinition `json:"PreCompress,omitempty" yaml:"PreCompress,omitempty"`
	Notification        []HookDefinition `json:"Notification,omitempty" yaml:"Notification,omitempty"`

	// === Claude Code naming (aliases) ===
	UserPromptSubmit []HookDefinition `json:"UserPromptSubmit,omitempty" yaml:"UserPromptSubmit,omitempty"` // -> BeforeAgent
	Stop             []HookDefinition `json:"Stop,omitempty" yaml:"Stop,omitempty"`                         // -> AfterAgent
	SubagentStop     []HookDefinition `json:"SubagentStop,omitempty" yaml:"SubagentStop,omitempty"`         // Claude-only
	PreToolUse       []HookDefinition `json:"PreToolUse,omitempty" yaml:"PreToolUse,omitempty"`             // -> BeforeTool
	PostToolUse      []HookDefinition `json:"PostToolUse,omitempty" yaml:"PostToolUse,omitempty"`           // -> AfterTool
	PreCompact       []HookDefinition `json:"PreCompact,omitempty" yaml:"PreCompact,omitempty"`             // -> PreCompress

	// Disabled hooks list
	Disabled []string `json:"disabled,omitempty" yaml:"disabled,omitempty"`
}

// NormalizeConfig merges Claude Code aliases into canonical Gemini CLI fields.
// Call this after loading to ensure all hooks are in canonical locations.
func (c *HooksConfig) NormalizeConfig() {
	// Merge Claude Code aliases into canonical fields
	c.BeforeAgent = append(c.BeforeAgent, c.UserPromptSubmit...)
	c.UserPromptSubmit = nil

	c.AfterAgent = append(c.AfterAgent, c.Stop...)
	c.Stop = nil

	c.BeforeTool = append(c.BeforeTool, c.PreToolUse...)
	c.PreToolUse = nil

	c.AfterTool = append(c.AfterTool, c.PostToolUse...)
	c.PostToolUse = nil

	c.PreCompress = append(c.PreCompress, c.PreCompact...)
	c.PreCompact = nil
}

// GetDefinitionsForEvent returns hook definitions for the given event.
// Supports both Claude Code and Gemini CLI event names.
func (c *HooksConfig) GetDefinitionsForEvent(event HookEventName) []HookDefinition {
	// Normalize the event name to canonical format
	unified := NormalizeEventName(string(event))

	switch unified {
	case UnifiedSessionStart:
		return c.SessionStart
	case UnifiedSessionEnd:
		return c.SessionEnd
	case UnifiedBeforeAgent:
		return c.BeforeAgent
	case UnifiedAfterAgent:
		return c.AfterAgent
	case UnifiedSubagentStop:
		return c.SubagentStop
	case UnifiedBeforeModel:
		return c.BeforeModel
	case UnifiedAfterModel:
		return c.AfterModel
	case UnifiedBeforeToolSelection:
		return c.BeforeToolSelection
	case UnifiedBeforeTool:
		return c.BeforeTool
	case UnifiedAfterTool:
		return c.AfterTool
	case UnifiedPreCompress:
		return c.PreCompress
	case UnifiedNotification:
		return c.Notification
	default:
		return nil
	}
}

// ConfigHierarchy manages loading hooks from multiple configuration sources.
type ConfigHierarchy struct {
	// Configuration search paths in priority order (highest first)
	ProjectPath string
	UserPath    string
	SystemPath  string

	// Loaded configurations
	projectConfig *HooksConfig
	userConfig    *HooksConfig
	systemConfig  *HooksConfig

	// Merged configuration
	mergedConfig *HooksConfig

	// Disabled hooks (union of all sources)
	disabledHooks map[string]bool
}

// NewConfigHierarchy creates a new config hierarchy loader.
func NewConfigHierarchy(projectDir string) *ConfigHierarchy {
	return &ConfigHierarchy{
		ProjectPath: projectDir,
		// User-level hooks live in the canonical ~/.swarm/config directory, so
		// loadFromPath discovers ~/.swarm/config/hooks.json (== paths.HooksFile()).
		UserPath:      paths.Config(),
		SystemPath:    "/etc/swarmos",
		disabledHooks: make(map[string]bool),
	}
}

// Load loads and merges configurations from all sources.
func (h *ConfigHierarchy) Load() (*HooksConfig, error) {
	var err error

	// Load from each source
	h.projectConfig, err = h.loadFromPath(h.ProjectPath, SourceProject)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load project config: %w", err)
	}

	h.userConfig, err = h.loadFromPath(h.UserPath, SourceUser)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load user config: %w", err)
	}

	h.systemConfig, err = h.loadFromPath(h.SystemPath, SourceSystem)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load system config: %w", err)
	}

	// Build disabled hooks set (union of all sources)
	h.buildDisabledSet()

	// Merge configurations
	h.mergedConfig = h.merge()

	// Compile all matchers
	if err := h.compileAllMatchers(); err != nil {
		return nil, fmt.Errorf("failed to compile matchers: %w", err)
	}

	return h.mergedConfig, nil
}

// GetMergedConfig returns the merged configuration.
func (h *ConfigHierarchy) GetMergedConfig() *HooksConfig {
	return h.mergedConfig
}

// IsHookDisabled checks if a hook is in the disabled list.
func (h *ConfigHierarchy) IsHookDisabled(name string) bool {
	return h.disabledHooks[name]
}

// loadFromPath loads configuration from a directory path.
func (h *ConfigHierarchy) loadFromPath(basePath string, source ConfigSource) (*HooksConfig, error) {
	searchPaths := []string{
		filepath.Join(basePath, "hooks.json"),
		filepath.Join(basePath, "hooks.yaml"),
		filepath.Join(basePath, "hooks.yml"),
		filepath.Join(basePath, ".swarm", "hooks.json"),
		filepath.Join(basePath, ".swarm", "hooks.yaml"),
		filepath.Join(basePath, "settings.json"), // Look for hooks section
	}

	for _, path := range searchPaths {
		if _, err := os.Stat(path); err == nil {
			config, err := h.loadFile(path, source)
			if err != nil {
				return nil, err
			}
			return config, nil
		}
	}

	// No config found, return empty
	return &HooksConfig{}, nil
}

// loadFile loads a configuration file.
func (h *ConfigHierarchy) loadFile(path string, source ConfigSource) (*HooksConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config HooksConfig
	ext := filepath.Ext(path)

	switch ext {
	case ".json":
		// Check if it's a settings file with hooks section
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(data, &settings); err == nil {
			if hooksData, ok := settings["hooks"]; ok {
				if err := json.Unmarshal(hooksData, &config); err != nil {
					return nil, fmt.Errorf("failed to parse hooks section: %w", err)
				}
			} else {
				// Try parsing as direct hooks config
				if err := json.Unmarshal(data, &config); err != nil {
					return nil, fmt.Errorf("failed to parse JSON config: %w", err)
				}
			}
		} else {
			return nil, fmt.Errorf("failed to parse JSON: %w", err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to parse YAML config: %w", err)
		}
	default:
		// Try JSON first, then YAML
		if err := json.Unmarshal(data, &config); err != nil {
			if err := yaml.Unmarshal(data, &config); err != nil {
				return nil, fmt.Errorf("failed to parse config as JSON or YAML")
			}
		}
	}

	// Normalize Claude Code aliases to canonical Gemini CLI names
	config.NormalizeConfig()

	// Tag all hooks with their source
	h.tagSource(&config, source)

	return &config, nil
}

// tagSource tags all hooks in a config with their source.
func (h *ConfigHierarchy) tagSource(config *HooksConfig, source ConfigSource) {
	tagDefinitions := func(defs []HookDefinition) {
		for i := range defs {
			for j := range defs[i].Hooks {
				defs[i].Hooks[j].Source = source
			}
		}
	}

	tagDefinitions(config.SessionStart)
	tagDefinitions(config.SessionEnd)
	tagDefinitions(config.BeforeAgent)
	tagDefinitions(config.AfterAgent)
	tagDefinitions(config.SubagentStop)
	tagDefinitions(config.BeforeModel)
	tagDefinitions(config.AfterModel)
	tagDefinitions(config.BeforeToolSelection)
	tagDefinitions(config.BeforeTool)
	tagDefinitions(config.AfterTool)
	tagDefinitions(config.PreCompress)
	tagDefinitions(config.Notification)
}

// buildDisabledSet builds the union of disabled hooks from all sources.
func (h *ConfigHierarchy) buildDisabledSet() {
	h.disabledHooks = make(map[string]bool)

	addDisabled := func(config *HooksConfig) {
		if config == nil {
			return
		}
		for _, name := range config.Disabled {
			h.disabledHooks[name] = true
		}
	}

	addDisabled(h.projectConfig)
	addDisabled(h.userConfig)
	addDisabled(h.systemConfig)
}

// merge merges configurations with deduplication.
// Priority: project > user > system
func (h *ConfigHierarchy) merge() *HooksConfig {
	merged := &HooksConfig{}

	// Merge in reverse priority order so higher priority overwrites
	mergeDefinitions := func(target *[]HookDefinition, sources ...*[]HookDefinition) {
		seen := make(map[string]bool)

		for _, source := range sources {
			if source == nil {
				continue
			}
			for _, def := range *source {
				for _, hook := range def.Hooks {
					key := hook.GetKey()
					if !seen[key] {
						seen[key] = true
						// Check if there's an existing definition with same matcher
						found := false
						for i := range *target {
							if (*target)[i].Matcher == def.Matcher {
								(*target)[i].Hooks = append((*target)[i].Hooks, hook)
								found = true
								break
							}
						}
						if !found {
							*target = append(*target, HookDefinition{
								Matcher:    def.Matcher,
								Sequential: def.Sequential,
								Hooks:      []CommandHookConfig{hook},
							})
						}
					}
				}
			}
		}
	}

	// Merge each event type (project first for highest priority in seen map)
	if h.projectConfig != nil {
		mergeDefinitions(&merged.SessionStart, &h.projectConfig.SessionStart)
		mergeDefinitions(&merged.SessionEnd, &h.projectConfig.SessionEnd)
		mergeDefinitions(&merged.BeforeAgent, &h.projectConfig.BeforeAgent)
		mergeDefinitions(&merged.AfterAgent, &h.projectConfig.AfterAgent)
		mergeDefinitions(&merged.SubagentStop, &h.projectConfig.SubagentStop)
		mergeDefinitions(&merged.BeforeModel, &h.projectConfig.BeforeModel)
		mergeDefinitions(&merged.AfterModel, &h.projectConfig.AfterModel)
		mergeDefinitions(&merged.BeforeToolSelection, &h.projectConfig.BeforeToolSelection)
		mergeDefinitions(&merged.BeforeTool, &h.projectConfig.BeforeTool)
		mergeDefinitions(&merged.AfterTool, &h.projectConfig.AfterTool)
		mergeDefinitions(&merged.PreCompress, &h.projectConfig.PreCompress)
		mergeDefinitions(&merged.Notification, &h.projectConfig.Notification)
	}

	if h.userConfig != nil {
		mergeDefinitions(&merged.SessionStart, &h.userConfig.SessionStart)
		mergeDefinitions(&merged.SessionEnd, &h.userConfig.SessionEnd)
		mergeDefinitions(&merged.BeforeAgent, &h.userConfig.BeforeAgent)
		mergeDefinitions(&merged.AfterAgent, &h.userConfig.AfterAgent)
		mergeDefinitions(&merged.SubagentStop, &h.userConfig.SubagentStop)
		mergeDefinitions(&merged.BeforeModel, &h.userConfig.BeforeModel)
		mergeDefinitions(&merged.AfterModel, &h.userConfig.AfterModel)
		mergeDefinitions(&merged.BeforeToolSelection, &h.userConfig.BeforeToolSelection)
		mergeDefinitions(&merged.BeforeTool, &h.userConfig.BeforeTool)
		mergeDefinitions(&merged.AfterTool, &h.userConfig.AfterTool)
		mergeDefinitions(&merged.PreCompress, &h.userConfig.PreCompress)
		mergeDefinitions(&merged.Notification, &h.userConfig.Notification)
	}

	if h.systemConfig != nil {
		mergeDefinitions(&merged.SessionStart, &h.systemConfig.SessionStart)
		mergeDefinitions(&merged.SessionEnd, &h.systemConfig.SessionEnd)
		mergeDefinitions(&merged.BeforeAgent, &h.systemConfig.BeforeAgent)
		mergeDefinitions(&merged.AfterAgent, &h.systemConfig.AfterAgent)
		mergeDefinitions(&merged.SubagentStop, &h.systemConfig.SubagentStop)
		mergeDefinitions(&merged.BeforeModel, &h.systemConfig.BeforeModel)
		mergeDefinitions(&merged.AfterModel, &h.systemConfig.AfterModel)
		mergeDefinitions(&merged.BeforeToolSelection, &h.systemConfig.BeforeToolSelection)
		mergeDefinitions(&merged.BeforeTool, &h.systemConfig.BeforeTool)
		mergeDefinitions(&merged.AfterTool, &h.systemConfig.AfterTool)
		mergeDefinitions(&merged.PreCompress, &h.systemConfig.PreCompress)
		mergeDefinitions(&merged.Notification, &h.systemConfig.Notification)
	}

	return merged
}

// compileAllMatchers compiles regex patterns for all matchers.
func (h *ConfigHierarchy) compileAllMatchers() error {
	if h.mergedConfig == nil {
		return nil
	}

	compileList := func(defs []HookDefinition) error {
		for i := range defs {
			if err := defs[i].Compile(); err != nil {
				return fmt.Errorf("invalid matcher %q: %w", defs[i].Matcher, err)
			}
		}
		return nil
	}

	events := [][]HookDefinition{
		h.mergedConfig.SessionStart,
		h.mergedConfig.SessionEnd,
		h.mergedConfig.BeforeAgent,
		h.mergedConfig.AfterAgent,
		h.mergedConfig.SubagentStop,
		h.mergedConfig.BeforeModel,
		h.mergedConfig.AfterModel,
		h.mergedConfig.BeforeToolSelection,
		h.mergedConfig.BeforeTool,
		h.mergedConfig.AfterTool,
		h.mergedConfig.PreCompress,
		h.mergedConfig.Notification,
	}

	for _, eventDefs := range events {
		if err := compileList(eventDefs); err != nil {
			return err
		}
	}

	return nil
}
