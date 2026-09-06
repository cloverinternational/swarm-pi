package plugins

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// PluginDir is the directory name containing the plugin manifest.
const PluginDir = ".claude-plugin"

// ManifestFile is the plugin manifest filename.
const ManifestFile = "plugin.json"

// ParseManifest parses a plugin manifest from .claude-plugin/plugin.json.
func ParseManifest(path string) (*PluginManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	var manifest PluginManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	if manifest.Name == "" {
		return nil, fmt.Errorf("manifest missing required field: name")
	}
	if manifest.Description == "" {
		return nil, fmt.Errorf("manifest missing required field: description")
	}

	// Validate name format (lowercase alphanumeric + hyphens)
	if !isValidPluginName(manifest.Name) {
		return nil, fmt.Errorf("invalid plugin name: must be lowercase alphanumeric with hyphens, got %q", manifest.Name)
	}

	return &manifest, nil
}

// isValidPluginName checks if a plugin name follows Claude Code conventions.
func isValidPluginName(name string) bool {
	// Must be lowercase alphanumeric + hyphens, no start/end hyphen, no consecutive hyphens
	pattern := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	return pattern.MatchString(name)
}

// CommandFrontmatter represents the YAML frontmatter in command files.
type CommandFrontmatter struct {
	Description       string   `yaml:"description"`
	AllowedInPlanMode bool     `yaml:"allowed_in_plan_mode"`
	Arguments         []string `yaml:"arguments,omitempty"`
	ArgumentHint      string   `yaml:"argument-hint,omitempty"`
}

// ParseCommand parses a command from a markdown file in commands/.
func ParseCommand(path string) (*Command, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read command file: %w", err)
	}

	content := string(data)
	frontmatter, body := parseFrontmatter(content)

	var fm CommandFrontmatter
	if frontmatter != "" {
		if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
			return nil, fmt.Errorf("failed to parse command frontmatter: %w", err)
		}
	}

	// Derive name from filename
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	cmd := &Command{
		Name:              name,
		Description:       fm.Description,
		AllowedInPlanMode: fm.AllowedInPlanMode,
		Content:           strings.TrimSpace(body),
		ArgumentHint:      fm.ArgumentHint,
		Path:              path,
	}

	// Parse argument placeholders from content
	cmd.Arguments = parseArgumentPlaceholders(cmd.Content, fm.Arguments)

	return cmd, nil
}

// parseArgumentPlaceholders extracts $ARGUMENTS, $1, $2, etc. from content.
func parseArgumentPlaceholders(content string, declaredArgs []string) []CommandArgument {
	var args []CommandArgument

	// Check for $ARGUMENTS
	if strings.Contains(content, "$ARGUMENTS") {
		args = append(args, CommandArgument{
			Name:        "ARGUMENTS",
			Description: "All arguments passed to the command",
		})
	}

	// Check for numbered arguments $1, $2, etc.
	argPattern := regexp.MustCompile(`\$(\d+)`)
	matches := argPattern.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)

	for _, match := range matches {
		if len(match) > 1 && !seen[match[1]] {
			seen[match[1]] = true
			desc := ""
			idx := 0
			fmt.Sscanf(match[1], "%d", &idx)
			if idx > 0 && idx <= len(declaredArgs) {
				desc = declaredArgs[idx-1]
			}
			args = append(args, CommandArgument{
				Name:        match[1],
				Description: desc,
			})
		}
	}

	return args
}

// AgentFrontmatter represents the YAML frontmatter in agent files.
type AgentFrontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Model       string   `yaml:"model"`
	Tools       []string `yaml:"tools"`
	MaxTurns    int      `yaml:"max_turns"`
}

// ParseAgent parses an agent from a markdown file in agents/.
func ParseAgent(path string) (*Agent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent file: %w", err)
	}

	content := string(data)
	frontmatter, body := parseFrontmatter(content)

	var fm AgentFrontmatter
	if frontmatter != "" {
		if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
			return nil, fmt.Errorf("failed to parse agent frontmatter: %w", err)
		}
	}

	// Derive name from filename if not in frontmatter
	name := fm.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	return &Agent{
		Name:         name,
		Description:  fm.Description,
		Model:        fm.Model,
		Tools:        fm.Tools,
		Instructions: strings.TrimSpace(body),
		MaxTurns:     fm.MaxTurns,
		Path:         path,
	}, nil
}

// ParseHooks parses hooks configuration from hooks/hooks.json.
func ParseHooks(path string) (*PluginHooks, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read hooks file: %w", err)
	}

	// The hooks file has a wrapper object with "hooks" key
	var wrapper struct {
		Hooks *PluginHooks `json:"hooks"`
	}

	if err := json.Unmarshal(data, &wrapper); err != nil {
		// Try parsing directly as PluginHooks
		var hooks PluginHooks
		if err := json.Unmarshal(data, &hooks); err != nil {
			return nil, fmt.Errorf("failed to parse hooks: %w", err)
		}
		return &hooks, nil
	}

	if wrapper.Hooks == nil {
		return &PluginHooks{}, nil
	}

	return wrapper.Hooks, nil
}

// MCPConfig represents the .mcp.json file structure.
// Keys are server names, values are server configurations.
type MCPConfig map[string]MCPServerConfig

// MCPServerConfig represents a single MCP server configuration.
type MCPServerConfig struct {
	Type        string            `json:"type,omitempty"` // stdio, sse, http
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	URL         string            `json:"url,omitempty"`
	Environment map[string]string `json:"env,omitempty"`
}

// ParseMCPConfig parses MCP server configurations from .mcp.json.
func ParseMCPConfig(path string) ([]MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read MCP config: %w", err)
	}
	return ParseMCPConfigBytes(data)
}

// ParseMCPConfigBytes parses MCP server configurations from raw .mcp.json data.
func ParseMCPConfigBytes(data []byte) ([]MCPServer, error) {
	var config MCPConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse MCP config: %w", err)
	}

	var servers []MCPServer
	for name, cfg := range config {
		serverType := cfg.Type
		if serverType == "" {
			// Default to stdio if command is provided
			if cfg.Command != "" {
				serverType = "stdio"
			} else if cfg.URL != "" {
				serverType = "http"
			}
		}

		servers = append(servers, MCPServer{
			Name:        name,
			Type:        serverType,
			Command:     cfg.Command,
			Args:        cfg.Args,
			URL:         cfg.URL,
			Environment: cfg.Environment,
			Enabled:     true,
		})
	}

	return servers, nil
}

// LSPConfig represents the .lsp.json file structure.
// Keys are language identifiers, values are server configurations.
type LSPConfig map[string]LSPServerConfig

// LSPServerConfig represents a single LSP server configuration.
type LSPServerConfig struct {
	Command             string            `json:"command"`
	Args                []string          `json:"args,omitempty"`
	ExtensionToLanguage map[string]string `json:"extensionToLanguage,omitempty"`
}

// ParseLSPConfig parses LSP server configurations from .lsp.json.
func ParseLSPConfig(path string) ([]LSPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read LSP config: %w", err)
	}

	var config LSPConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse LSP config: %w", err)
	}

	var servers []LSPServer
	for lang, cfg := range config {
		servers = append(servers, LSPServer{
			Language:            lang,
			Command:             cfg.Command,
			Args:                cfg.Args,
			ExtensionToLanguage: cfg.ExtensionToLanguage,
			Enabled:             true,
		})
	}

	return servers, nil
}

// parseFrontmatter extracts YAML frontmatter and body from markdown content.
func parseFrontmatter(content string) (frontmatter, body string) {
	scanner := bufio.NewScanner(strings.NewReader(content))

	// Check for opening ---
	if !scanner.Scan() {
		return "", content
	}
	firstLine := scanner.Text()
	if strings.TrimSpace(firstLine) != "---" {
		return "", content
	}

	// Read frontmatter until closing ---
	var fmLines []string
	foundEnd := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			foundEnd = true
			break
		}
		fmLines = append(fmLines, line)
	}

	if !foundEnd {
		// No closing ---, treat entire content as body
		return "", content
	}

	// Read remaining content as body
	var bodyLines []string
	for scanner.Scan() {
		bodyLines = append(bodyLines, scanner.Text())
	}

	return strings.Join(fmLines, "\n"), strings.Join(bodyLines, "\n")
}

// IsPluginDirectory checks if a directory contains a Claude Code plugin.
func IsPluginDirectory(path string) bool {
	manifestPath := filepath.Join(path, PluginDir, ManifestFile)
	info, err := os.Stat(manifestPath)
	return err == nil && !info.IsDir()
}

// FindPluginDirectories finds all plugin directories under a given path.
func FindPluginDirectories(root string) ([]string, error) {
	var plugins []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Skip hidden directories (except .claude-plugin itself)
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") && info.Name() != PluginDir {
			return filepath.SkipDir
		}

		// Check if this is a plugin directory
		if info.IsDir() && info.Name() != PluginDir {
			if IsPluginDirectory(path) {
				plugins = append(plugins, path)
				return filepath.SkipDir // Don't recurse into plugin directories
			}
		}

		return nil
	})

	return plugins, err
}

// ParseCommandsDir parses all commands from a commands/ directory.
func ParseCommandsDir(dir string) ([]Command, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No commands directory
		}
		return nil, fmt.Errorf("failed to read commands directory: %w", err)
	}

	var commands []Command
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Only process markdown files
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		cmdPath := filepath.Join(dir, entry.Name())
		cmd, err := ParseCommand(cmdPath)
		if err != nil {
			continue // Skip invalid commands
		}
		commands = append(commands, *cmd)
	}

	return commands, nil
}

// ParseAgentsDir parses all agents from an agents/ directory.
func ParseAgentsDir(dir string) ([]Agent, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No agents directory
		}
		return nil, fmt.Errorf("failed to read agents directory: %w", err)
	}

	var agents []Agent
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Only process markdown files
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		agentPath := filepath.Join(dir, entry.Name())
		agent, err := ParseAgent(agentPath)
		if err != nil {
			continue // Skip invalid agents
		}
		agents = append(agents, *agent)
	}

	return agents, nil
}
