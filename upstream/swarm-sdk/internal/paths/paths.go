// Package paths is the single source of truth for all on-disk locations used
// by SwarmOS. The canonical root is ~/.swarm (override with env SWARM_HOME).
//
// Helpers resolve the current environment on each call. Directory accessors
// ensure the directory exists with 0700 permissions before returning.
package paths

import (
	"os"
	"path/filepath"
)

// homeDir is overridable in tests. It resolves the user's home directory.
var homeDir = os.UserHomeDir

// resolveHome resolves the home directory on each call so embedded hosts and
// tests can change HOME/SWARM_HOME without inheriting another session's paths.
func resolveHome() string {
	h, err := homeDir()
	if err != nil || h == "" {
		// Fall back to the current working directory so callers still get a
		// usable, non-empty base rather than joining onto "".
		if wd, werr := os.Getwd(); werr == nil {
			h = wd
		} else {
			h = "."
		}
	}
	return h
}

// Root returns the canonical SwarmOS root directory: ~/.swarm, unless the
// SWARM_HOME environment variable is set, in which case that value is used
// verbatim.
func Root() string {
	if v := os.Getenv("SWARM_HOME"); v != "" {
		return v
	}
	return filepath.Join(resolveHome(), ".swarm")
}

// EnsureDir creates dir (and any missing parents) with 0700 permissions.
func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0o700)
}

// ensureDir creates dir at 0700 and returns dir. Errors are intentionally
// swallowed so accessors keep a simple string signature per the CONTRACT;
// callers that need to write will surface any real failure at write time.
func ensureDir(dir string) string {
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

// Config returns ~/.swarm/config, ensuring the directory exists at 0700.
func Config() string {
	return ensureDir(filepath.Join(Root(), "config"))
}

// ConfigFile returns ~/.swarm/config/config.yaml.
func ConfigFile() string {
	return filepath.Join(Config(), "config.yaml")
}

// ProvidersFile returns ~/.swarm/config/providers.json.
func ProvidersFile() string {
	return filepath.Join(Config(), "providers.json")
}

// CredentialsFile returns ~/.swarm/config/credentials.json.
func CredentialsFile() string {
	return filepath.Join(Config(), "credentials.json")
}

// OAuthFile returns ~/.swarm/config/oauth/<provider>.json, ensuring the oauth
// directory exists at 0700.
func OAuthFile(provider string) string {
	dir := ensureDir(filepath.Join(Config(), "oauth"))
	return filepath.Join(dir, provider+".json")
}

// HooksFile returns ~/.swarm/config/hooks.json.
func HooksFile() string {
	return filepath.Join(Config(), "hooks.json")
}

// McpServersFile returns ~/.swarm/config/mcp_servers.json.
func McpServersFile() string {
	return filepath.Join(Config(), "mcp_servers.json")
}

// AgentProfilesFile returns ~/.swarm/config/agent_profiles.json.
func AgentProfilesFile() string {
	return filepath.Join(Config(), "agent_profiles.json")
}

// SystemPromptsFile returns ~/.swarm/config/system_prompts.yaml.
func SystemPromptsFile() string {
	return filepath.Join(Config(), "system_prompts.yaml")
}

// CustomAgentsFile returns ~/.swarm/config/custom_agents.yaml.
func CustomAgentsFile() string {
	return filepath.Join(Config(), "custom_agents.yaml")
}

// SkillsDir returns ~/.swarm/skills, ensuring the directory exists at 0700.
func SkillsDir() string {
	return ensureDir(filepath.Join(Root(), "skills"))
}

// AutogenSkillsDir returns ~/.swarm/skills/autogen, ensuring the directory
// exists at 0700.
func AutogenSkillsDir() string {
	return ensureDir(filepath.Join(SkillsDir(), "autogen"))
}

// ConversationsDir returns ~/.swarm/conversations, ensuring the directory
// exists at 0700.
func ConversationsDir() string {
	return ensureDir(filepath.Join(Root(), "conversations"))
}

// LogFile returns ~/.swarm/logs/swarmos.log, ensuring the logs directory
// exists at 0700.
func LogFile() string {
	dir := ensureDir(filepath.Join(Root(), "logs"))
	return filepath.Join(dir, "swarmos.log")
}

// VaultDir returns ~/.swarm/vault, ensuring the directory exists at 0700.
func VaultDir() string {
	return ensureDir(filepath.Join(Root(), "vault"))
}

// A2ARegistryFile returns ~/.swarm/a2a-registry.sqlite (single global).
func A2ARegistryFile() string {
	return filepath.Join(ensureDir(Root()), "a2a-registry.sqlite")
}

// BackupsDir returns ~/.swarm/backups, ensuring the directory exists at 0700.
func BackupsDir() string {
	return ensureDir(filepath.Join(Root(), "backups"))
}

// LocksDir returns ~/.swarm/locks, ensuring the directory exists at 0700.
func LocksDir() string {
	return ensureDir(filepath.Join(Root(), "locks"))
}

// In joins elem under Root() and ensures the parent directory of the result
// exists at 0700.
func In(elem ...string) string {
	parts := append([]string{Root()}, elem...)
	p := filepath.Join(parts...)
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	return p
}
