// Package loader discovers hook configurations on disk and converts them
// into registered [hooks.Hook] instances.
//
// It supports two on-disk formats:
//
//  1. Claude Code "settings.json" under a `hooks` key (the Claude Code schema
//     with matchers).
//  2. SwarmOS native "hooks.json" under a `custom_hooks` key (ShellHookConfig
//     list, the schema the TUI writes).
//
// File lookup precedence for [LoadDefault] (highest wins, read in reverse so
// later sources append but take precedence by position):
//
//  1. `<projectDir>/.claude/settings.json`
//  2. `~/.claude/settings.json`
//  3. `~/.swarm/config/hooks.json`
//
// A mirror of this logic lives in
// `swarm-tui/internal/chat/hooks/config.go`. Schema changes must be applied
// to both files; this duplication is intentional while the TUI still owns
// its own hook loader.
package loader

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// claudeCodeEventMap translates Claude Code event names into SDK-native
// event strings as defined in `hooks/event.go` and `hooks/hook_events.go`.
var claudeCodeEventMap = map[string]string{
	"PreToolUse":        hooks.EventToolBeforeExecute,
	"PostToolUse":       hooks.EventToolAfterExecute,
	"PermissionRequest": hooks.EventToolBeforeExecute,
	"UserPromptSubmit":  "user.prompt_submit",
	"Stop":              hooks.EventAgentStopped,
	"SubagentStop":      "subagent.stop",
	"SessionStart":      string(hooks.EventSessionStart),
	"SessionEnd":        string(hooks.EventSessionEnd),
	"Notification":      string(hooks.EventNotification),
	"PreCompact":        "compact.before",
}

// claudeCodeSettings is the subset of a Claude Code settings.json we parse.
type claudeCodeSettings struct {
	Hooks map[string][]claudeCodeHookMatcher `json:"hooks"`
}

type claudeCodeHookMatcher struct {
	Matcher string           `json:"matcher,omitempty"`
	Hooks   []claudeCodeHook `json:"hooks"`
}

type claudeCodeHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Prompt  string `json:"prompt,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// swarmOSConfig is the subset of a ~/.swarm/config/hooks.json we parse.
type swarmOSConfig struct {
	CustomHooks []*hooks.ShellHookConfig `json:"custom_hooks"`
}

// Config is the result of loading hook configs from disk. Configs are
// returned as [hooks.ShellHookConfig] values; callers turn them into
// runnable hooks via [hooks.ShellHookConfig.ToShellHook].
type Config struct {
	Hooks []*hooks.ShellHookConfig
}

// LoadFromFile loads a single file. Accepts Claude Code format (detected by
// a top-level "hooks" key) or SwarmOS native format (detected by
// "custom_hooks"). Missing files are reported as (nil, nil); malformed files
// return an error.
func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseBytes(data, path)
}

// LoadDefault merges hook configs from the three default disk locations.
// Later sources take precedence (Project beats User beats SwarmOS-native);
// in practice we append in reverse so the highest-priority entries end up
// last in the slice — hook execution respects Priority fields, not slice
// order, so order only matters for debugging.
//
// Any source file that is missing is skipped silently. An error is returned
// only if a file exists but fails to parse.
func LoadDefault(projectDir string) (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}

	sources := []string{
		paths.HooksFile(),
		filepath.Join(home, ".claude", "settings.json"),
	}
	if projectDir != "" {
		// settings.json before settings.local.json so local overrides take precedence
		// (later entries in the slice are appended last, giving them higher effective priority).
		sources = append(sources,
			filepath.Join(projectDir, ".claude", "settings.json"),
			filepath.Join(projectDir, ".claude", "settings.local.json"),
		)
	}

	merged := &Config{}
	for _, src := range sources {
		cfg, loadErr := LoadFromFile(src)
		if loadErr != nil {
			return nil, loadErr
		}
		if cfg == nil {
			continue
		}
		merged.Hooks = append(merged.Hooks, cfg.Hooks...)
	}
	return merged, nil
}

// ToHooks converts the loaded configs into runnable hooks. Disabled configs
// are skipped. Any per-hook conversion error is wrapped with the hook name.
func (c *Config) ToHooks() ([]hooks.Hook, error) {
	if c == nil {
		return nil, nil
	}
	out := make([]hooks.Hook, 0, len(c.Hooks))
	for _, cfg := range c.Hooks {
		if cfg == nil || !cfg.Enabled {
			continue
		}
		h, err := cfg.ToShellHook()
		if err != nil {
			return nil, fmt.Errorf("build hook %q: %w", cfg.Name, err)
		}
		out = append(out, h)
	}
	return out, nil
}

// parseBytes detects the format of a settings payload and returns the
// ShellHookConfig list it encodes.
func parseBytes(data []byte, path string) (*Config, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// Claude Code format
	if raw, ok := probe["hooks"]; ok {
		var settings claudeCodeSettings
		settings.Hooks = map[string][]claudeCodeHookMatcher{}
		if err := json.Unmarshal(raw, &settings.Hooks); err != nil {
			return nil, fmt.Errorf("parse claude hooks in %s: %w", path, err)
		}
		return convertClaudeSettings(settings), nil
	}

	// SwarmOS native format
	if _, ok := probe["custom_hooks"]; ok {
		var sw swarmOSConfig
		if err := json.Unmarshal(data, &sw); err != nil {
			return nil, fmt.Errorf("parse swarmos hooks in %s: %w", path, err)
		}
		return &Config{Hooks: sw.CustomHooks}, nil
	}

	// Neither key present: treat as an empty config (e.g. a settings.json
	// that exists but has no hook section).
	return &Config{}, nil
}

func convertClaudeSettings(s claudeCodeSettings) *Config {
	out := &Config{}
	index := 0
	for eventName, matchers := range s.Hooks {
		swarmEvent, ok := claudeCodeEventMap[eventName]
		if !ok {
			swarmEvent = strings.ToLower(eventName)
		}
		for _, matcher := range matchers {
			for _, h := range matcher.Hooks {
				// Prompt-type hooks have no shell command — skip.
				if h.Type != "" && h.Type != "command" {
					continue
				}
				if h.Command == "" {
					continue
				}
				index++
				timeout := "60s"
				if h.Timeout > 0 {
					timeout = fmt.Sprintf("%ds", h.Timeout)
				}
				cfg := &hooks.ShellHookConfig{
					Name:             generateHookName(eventName, matcher.Matcher, index),
					Description:      fmt.Sprintf("Claude Code hook: %s", eventName),
					EventPatterns:    []string{swarmEvent},
					Command:          h.Command,
					Priority:         50,
					Timeout:          timeout,
					Action:           "block_exit2",
					Enabled:          true,
					PermissionPolicy: hooks.HookPermissionAllow,
					PassEventAsJSON:  true,
					ToolMatcher:      matcher.Matcher,
					CreatedAt:        time.Now().UTC().Format(time.RFC3339),
				}
				out.Hooks = append(out.Hooks, cfg)
			}
		}
	}
	return out
}

var nameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func generateHookName(eventName, matcher string, index int) string {
	base := strings.ToLower(eventName)
	if matcher != "" && matcher != "*" {
		base = base + "_" + nameSanitizer.ReplaceAllString(matcher, "_")
	}
	return fmt.Sprintf("%s_%d", base, index)
}
