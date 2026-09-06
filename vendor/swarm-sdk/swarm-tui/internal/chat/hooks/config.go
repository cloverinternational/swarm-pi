package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// Claude Code event name mappings to SwarmOS event types
var claudeCodeEventMap = map[string]string{
	"PreToolUse":        hooks.EventToolBeforeExecute,
	"PostToolUse":       hooks.EventToolAfterExecute,
	"PermissionRequest": hooks.EventToolBeforeExecute, // Same as PreToolUse for permission checks
	"UserPromptSubmit":  "user.prompt_submit",
	"Stop":              "agent.stop",
	"SubagentStop":      "subagent.stop",
	"SessionStart":      "session.start",
	"SessionEnd":        "session.end",
	"Notification":      "notification",
	"PreCompact":        "compact.before",
}

// ClaudeCodeHooksConfig represents the Claude Code hooks.json format
type ClaudeCodeHooksConfig struct {
	Hooks map[string][]ClaudeCodeHookMatcher `json:"hooks"`
}

// ClaudeCodeHookMatcher represents a matcher entry in Claude Code format
type ClaudeCodeHookMatcher struct {
	Matcher string           `json:"matcher,omitempty"`
	Hooks   []ClaudeCodeHook `json:"hooks"`
}

// ClaudeCodeHook represents a single hook in Claude Code format
type ClaudeCodeHook struct {
	Type    string `json:"type"`    // "command" or "prompt"
	Command string `json:"command"` // Shell command to execute
	Prompt  string `json:"prompt"`  // For prompt-based hooks
	Timeout int    `json:"timeout"` // Timeout in seconds
}

// HooksConfig stores custom hook configurations.
type HooksConfig struct {
	mu          sync.RWMutex
	CustomHooks []*hooks.ShellHookConfig `json:"custom_hooks"`
	filePath    string
}

// NewHooksConfig creates a new hooks configuration manager.
func NewHooksConfig() *HooksConfig {
	home, _ := os.UserHomeDir()
	return &HooksConfig{
		CustomHooks: make([]*hooks.ShellHookConfig, 0),
		filePath:    filepath.Join(home, ".swarmos", "hooks.json"),
	}
}

// NewHooksConfigWithProject creates a hooks configuration manager that also loads from project-level configs.
// Priority order (highest to lowest):
// 1. Project-level .claude/settings.json
// 2. User-level ~/.claude/settings.json
// 3. SwarmOS native ~/.swarmos/hooks.json
func NewHooksConfigWithProject(projectDir string) *HooksConfig {
	home, _ := os.UserHomeDir()
	hc := &HooksConfig{
		CustomHooks: make([]*hooks.ShellHookConfig, 0),
		filePath:    filepath.Join(home, ".swarmos", "hooks.json"),
	}

	// Load hooks from multiple sources in reverse priority (lowest first, will be overwritten)
	hc.loadFromPath(filepath.Join(home, ".swarmos", "hooks.json"))
	hc.loadFromClaudeSettings(filepath.Join(home, ".claude", "settings.json"))
	if projectDir != "" {
		// settings.local.json is the highest-priority project override (same as Claude Code)
		hc.loadFromClaudeSettings(filepath.Join(projectDir, ".claude", "settings.json"))
		hc.loadFromClaudeSettings(filepath.Join(projectDir, ".claude", "settings.local.json"))
	}

	return hc
}

// loadFromPath attempts to load hooks from a specific path.
func (hc *HooksConfig) loadFromPath(path string) {
	logDebug("[HooksConfig] Attempting to load from: %s", path)

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logDebug("[HooksConfig] Error reading %s: %v", path, err)
		}
		return
	}

	// Try to detect format
	var rawConfig map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawConfig); err != nil {
		logDebug("[HooksConfig] Error parsing JSON from %s: %v", path, err)
		return
	}

	// Check for Claude Code format (has "hooks" key)
	if hooksData, hasHooks := rawConfig["hooks"]; hasHooks {
		hc.loadClaudeCodeFormat(hooksData)
		return
	}

	// Try SwarmOS format
	var config struct {
		CustomHooks []*hooks.ShellHookConfig `json:"custom_hooks"`
	}
	if err := json.Unmarshal(data, &config); err == nil && len(config.CustomHooks) > 0 {
		hc.CustomHooks = append(hc.CustomHooks, config.CustomHooks...)
	}
}

// loadFromClaudeSettings loads hooks specifically from a Claude Code settings.json file.
func (hc *HooksConfig) loadFromClaudeSettings(path string) {
	logDebug("[HooksConfig] Loading Claude settings from: %s", path)

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logDebug("[HooksConfig] Error reading %s: %v", path, err)
		}
		return
	}

	// Parse the full settings.json structure
	var settings struct {
		Hooks map[string][]ClaudeCodeHookMatcher `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		logDebug("[HooksConfig] Error parsing Claude settings: %v", err)
		return
	}

	if settings.Hooks == nil || len(settings.Hooks) == 0 {
		return
	}

	// Convert Claude Code hooks to SwarmOS format
	hookIndex := len(hc.CustomHooks)
	for eventName, matchers := range settings.Hooks {
		swarmEvent, ok := claudeCodeEventMap[eventName]
		if !ok {
			logDebug("[HooksConfig] Unknown Claude Code event: %s, using lowercase", eventName)
			swarmEvent = strings.ToLower(eventName)
		}

		for _, matcher := range matchers {
			for _, hook := range matcher.Hooks {
				if hook.Type != "command" && hook.Type != "" {
					continue
				}

				hookIndex++
				config := &hooks.ShellHookConfig{
					Name:             generateHookName(eventName, matcher.Matcher, hook.Command, hookIndex),
					Description:      fmt.Sprintf("Claude Code hook: %s", eventName),
					EventPatterns:    []string{swarmEvent},
					Command:          hook.Command,
					Priority:         50,
					Timeout:          "60s",
					Action:           "block",
					Enabled:          true, // Claude Code hooks are enabled by default
					PermissionPolicy: hooks.HookPermissionAllow,
					PassEventAsJSON:  true,
					ToolMatcher:      matcher.Matcher,
					CreatedAt:        time.Now().Format(time.RFC3339),
				}

				hc.CustomHooks = append(hc.CustomHooks, config)
				logDebug("[HooksConfig] Loaded Claude Code hook: %s (event=%s)", config.Name, eventName)
			}
		}
	}
}

// Load reads the hooks configuration from disk.
// Supports both SwarmOS format and Claude Code format.
func (hc *HooksConfig) Load() error {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	logDebug("[HooksConfig] Loading from: %s", hc.filePath)

	data, err := os.ReadFile(hc.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Initialize with empty config
			logDebug("[HooksConfig] File does not exist, initializing empty config")
			hc.CustomHooks = make([]*hooks.ShellHookConfig, 0)
			return nil
		}
		logDebug("[HooksConfig] Error reading file: %v", err)
		return err
	}

	logDebug("[HooksConfig] Read %d bytes from file", len(data))

	// Try to detect format by checking for "hooks" key (Claude Code) vs "custom_hooks" (SwarmOS)
	var rawConfig map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawConfig); err != nil {
		logDebug("[HooksConfig] Error parsing JSON: %v", err)
		return err
	}

	// Check for Claude Code format (has "hooks" key with event categories)
	if hooksData, hasHooks := rawConfig["hooks"]; hasHooks {
		logDebug("[HooksConfig] Detected Claude Code format")
		return hc.loadClaudeCodeFormat(hooksData)
	}

	// SwarmOS native format
	var config struct {
		CustomHooks []*hooks.ShellHookConfig `json:"custom_hooks"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		logDebug("[HooksConfig] Error parsing SwarmOS format: %v", err)
		return err
	}

	hc.CustomHooks = config.CustomHooks
	if hc.CustomHooks == nil {
		hc.CustomHooks = make([]*hooks.ShellHookConfig, 0)
	}

	logDebug("[HooksConfig] Loaded %d custom hooks (SwarmOS format)", len(hc.CustomHooks))
	for i, h := range hc.CustomHooks {
		logDebug("[HooksConfig]   Hook %d: name=%s, enabled=%v, events=%v", i, h.Name, h.Enabled, h.EventPatterns)
	}

	return nil
}

// loadClaudeCodeFormat parses Claude Code hooks format and converts to SwarmOS format.
func (hc *HooksConfig) loadClaudeCodeFormat(data json.RawMessage) error {
	var ccHooks map[string][]ClaudeCodeHookMatcher
	if err := json.Unmarshal(data, &ccHooks); err != nil {
		logDebug("[HooksConfig] Error parsing Claude Code hooks: %v", err)
		return err
	}

	hc.CustomHooks = make([]*hooks.ShellHookConfig, 0)
	hookIndex := 0

	for eventName, matchers := range ccHooks {
		// Map Claude Code event name to SwarmOS event type
		swarmEvent, ok := claudeCodeEventMap[eventName]
		if !ok {
			logDebug("[HooksConfig] Unknown Claude Code event: %s, using as-is", eventName)
			swarmEvent = strings.ToLower(eventName)
		}

		for _, matcher := range matchers {
			for _, hook := range matcher.Hooks {
				// Only support command hooks (not prompt hooks yet)
				if hook.Type != "command" && hook.Type != "" {
					logDebug("[HooksConfig] Skipping non-command hook type: %s", hook.Type)
					continue
				}

				hookIndex++
				hookName := generateHookName(eventName, matcher.Matcher, hook.Command, hookIndex)

				// Determine timeout (Claude Code default is 60s)
				timeout := "60s"
				if hook.Timeout > 0 {
					timeoutDuration := time.Duration(hook.Timeout) * time.Second
					timeout = timeoutDuration.String()
				}

				// Build event patterns with matcher
				eventPatterns := []string{swarmEvent}

				// Create SwarmOS hook config
				config := &hooks.ShellHookConfig{
					Name:             hookName,
					Description:      "Imported from Claude Code format",
					EventPatterns:    eventPatterns,
					Command:          hook.Command,
					Priority:         50,
					Timeout:          timeout,
					Action:           "block_exit2", // Claude Code exit 2 = block semantics
					Enabled:          true,
					PermissionPolicy: hooks.HookPermissionAllow,
					PassEventAsJSON:  true,
					ToolMatcher:      matcher.Matcher, // Store matcher for tool name filtering
					CreatedAt:        time.Now().Format(time.RFC3339),
				}

				hc.CustomHooks = append(hc.CustomHooks, config)
				logDebug("[HooksConfig] Converted Claude Code hook: %s (event=%s, matcher=%s)", hookName, eventName, matcher.Matcher)
			}
		}
	}

	logDebug("[HooksConfig] Loaded %d hooks from Claude Code format", len(hc.CustomHooks))
	return nil
}

// generateHookName creates a unique hook name from Claude Code config
// It attempts to extract a meaningful name from the command string by:
// 1. Taking the first few words of the command (up to 3)
// 2. Stopping at flags, pipes, or redirects
// 3. Sanitizing to create valid identifiers
// 4. Adding timestamp and index suffix for uniqueness
//
// Examples:
//
//	"create a probe to monitor" -> "create_a_probe_TIMESTAMP_a"
//	"git commit -m 'message'"   -> "git_commit_TIMESTAMP_b"
//	"echo 'hello' | grep"       -> "echo__hello__TIMESTAMP_c"
func generateHookName(eventName, matcher, command string, index int) string {
	base := strings.ToLower(eventName)

	// Try to extract a meaningful name from the command
	if command != "" {
		// Extract first few words from the command, limiting to reasonable length
		words := strings.Fields(command)
		if len(words) > 0 {
			// Take up to first 3 words or until we hit a flag/pipe/redirect
			nameWords := []string{}
			for i, word := range words {
				if i >= 3 || strings.HasPrefix(word, "-") || word == "|" || word == ">" || word == "<" {
					break
				}
				// Sanitize each word
				sanitized := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(word, "_")
				if sanitized != "" {
					nameWords = append(nameWords, sanitized)
				}
			}
			if len(nameWords) > 0 {
				base = strings.ToLower(strings.Join(nameWords, "_"))
			}
		}
	} else if matcher != "" && matcher != "*" {
		// Fall back to matcher-based naming if no command
		// Sanitize matcher for use in name
		sanitized := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(matcher, "_")
		base = base + "_" + sanitized
	}

	// Add timestamp and index for uniqueness
	return base + "_" + time.Now().Format("150405") + "_" + string(rune('a'+index-1))
}

// Save writes the hooks configuration to disk.
func (hc *HooksConfig) Save() error {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	// Ensure directory exists
	dir := filepath.Dir(hc.filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	config := struct {
		CustomHooks []*hooks.ShellHookConfig `json:"custom_hooks"`
	}{
		CustomHooks: hc.CustomHooks,
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(hc.filePath, data, 0600)
}

// AddHook adds a new custom hook configuration.
func (hc *HooksConfig) AddHook(config *hooks.ShellHookConfig) error {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	// Check for duplicate name
	for _, h := range hc.CustomHooks {
		if h.Name == config.Name {
			return &HookExistsError{Name: config.Name}
		}
	}

	// Set creation time if not set
	if config.CreatedAt == "" {
		config.CreatedAt = time.Now().Format(time.RFC3339)
	}

	hc.CustomHooks = append(hc.CustomHooks, config)
	return nil
}

// UpdateHook updates an existing hook configuration.
func (hc *HooksConfig) UpdateHook(name string, config *hooks.ShellHookConfig) error {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	for i, h := range hc.CustomHooks {
		if h.Name == name {
			// Preserve creation time
			config.CreatedAt = h.CreatedAt
			hc.CustomHooks[i] = config
			return nil
		}
	}

	return &HookNotFoundError{Name: name}
}

// DeleteHook removes a hook by name.
func (hc *HooksConfig) DeleteHook(name string) error {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	for i, h := range hc.CustomHooks {
		if h.Name == name {
			hc.CustomHooks = append(hc.CustomHooks[:i], hc.CustomHooks[i+1:]...)
			return nil
		}
	}

	return &HookNotFoundError{Name: name}
}

// GetHook retrieves a hook by name.
func (hc *HooksConfig) GetHook(name string) (*hooks.ShellHookConfig, error) {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	for _, h := range hc.CustomHooks {
		if h.Name == name {
			return h, nil
		}
	}

	return nil, &HookNotFoundError{Name: name}
}

// ListHooks returns all custom hook configurations.
func (hc *HooksConfig) ListHooks() []*hooks.ShellHookConfig {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	result := make([]*hooks.ShellHookConfig, len(hc.CustomHooks))
	copy(result, hc.CustomHooks)
	return result
}

// SetEnabled enables or disables a hook.
func (hc *HooksConfig) SetEnabled(name string, enabled bool) error {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	for _, h := range hc.CustomHooks {
		if h.Name == name {
			h.Enabled = enabled
			return nil
		}
	}

	return &HookNotFoundError{Name: name}
}

// SetPermissionPolicy updates the permission policy for a hook.
func (hc *HooksConfig) SetPermissionPolicy(name string, policy hooks.HookPermissionPolicy) error {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	normalized := hooks.NormalizeHookPermissionPolicy(policy)
	for i, h := range hc.CustomHooks {
		if h.Name == name {
			hc.CustomHooks[i].PermissionPolicy = normalized
			return nil
		}
	}

	return fmt.Errorf("hook not found: %s", name)
}

// GetEnabledHooks returns only enabled hook configurations.
func (hc *HooksConfig) GetEnabledHooks() []*hooks.ShellHookConfig {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	var result []*hooks.ShellHookConfig
	for _, h := range hc.CustomHooks {
		if h.Enabled {
			result = append(result, h)
		}
	}
	return result
}

// HookExistsError is returned when trying to add a hook that already exists.
type HookExistsError struct {
	Name string
}

func (e *HookExistsError) Error() string {
	return "hook already exists: " + e.Name
}

// HookNotFoundError is returned when a hook is not found.
type HookNotFoundError struct {
	Name string
}

func (e *HookNotFoundError) Error() string {
	return "hook not found: " + e.Name
}

// LoadHooksConfig loads the hooks configuration from the default location.
func LoadHooksConfig() (*HooksConfig, error) {
	config := NewHooksConfig()
	err := config.Load()
	return config, err
}

// LoadHooksConfigWithProject loads hooks from multiple sources:
// 1. Project-level .claude/settings.json
// 2. User-level ~/.claude/settings.json
// 3. SwarmOS native ~/.swarmos/hooks.json
func LoadHooksConfigWithProject(projectDir string) (*HooksConfig, error) {
	config := NewHooksConfigWithProject(projectDir)
	return config, nil // NewHooksConfigWithProject already loads everything
}

// CreateShellHooksFromConfig creates ShellHook instances from configurations.
func CreateShellHooksFromConfig(configs []*hooks.ShellHookConfig) ([]*hooks.ShellHook, error) {
	var result []*hooks.ShellHook
	for _, config := range configs {
		if !config.Enabled {
			continue
		}
		hook, err := config.ToShellHook()
		if err != nil {
			return nil, err
		}
		result = append(result, hook)
	}
	return result, nil
}
