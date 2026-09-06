package mcp

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
)

const (
	maxFileSizeBytes = 512 * 1024
	maxServers       = 200
	maxToolNames     = 500
	maxMapEntries    = 64
	maxKeyLength     = 128
	maxValueLength   = 2048
	maxArgs          = 64
	maxDepth         = 10
)

var serverNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func ValidateConfigFile(cfg *ConfigFile) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if cfg.SchemaVersion != schemaVersion {
		return fmt.Errorf("unsupported schema_version: %d", cfg.SchemaVersion)
	}
	if len(cfg.Servers) > maxServers {
		return fmt.Errorf("too many servers")
	}
	seen := make(map[string]bool, len(cfg.Servers))
	for _, server := range cfg.Servers {
		if seen[server.Name] {
			return fmt.Errorf("duplicate server name: %s", server.Name)
		}
		seen[server.Name] = true
		if err := validateServerConfig(server); err != nil {
			return err
		}
	}
	for pluginName, overrides := range cfg.PluginOverrides {
		if !isValidKey(pluginName) {
			return fmt.Errorf("invalid plugin name: %s", pluginName)
		}
		for serverName, patch := range overrides {
			if !isValidKey(serverName) {
				return fmt.Errorf("invalid override server name: %s", serverName)
			}
			if err := validateServerPatch(patch); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateServerConfig(server ServerConfig) error {
	if !serverNamePattern.MatchString(server.Name) {
		return fmt.Errorf("invalid server name: %s", server.Name)
	}
	server = normalizeServerConfig(server)
	if err := validateString(server.Name); err != nil {
		return err
	}
	if server.CredentialRef != "" && !serverNamePattern.MatchString(server.CredentialRef) {
		return fmt.Errorf("invalid credential_ref: %s", server.CredentialRef)
	}

	switch server.Type {
	case "stdio":
		if server.Command == "" {
			return fmt.Errorf("stdio server requires command")
		}
	case "http", "sse", "oauth":
		if server.URL == "" {
			return fmt.Errorf("%s server requires url", server.Type)
		}
		if err := validateURL(server.URL); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid server type: %s", server.Type)
	}

	if server.Type == "oauth" {
		if server.OAuth == nil || server.OAuth.ClientID == "" {
			return fmt.Errorf("oauth server requires client_id")
		}
	}

	if len(server.Args) > maxArgs {
		return fmt.Errorf("too many args")
	}
	for _, arg := range server.Args {
		if err := validateString(arg); err != nil {
			return err
		}
	}
	if err := validateString(server.Command); err != nil {
		return err
	}
	if err := validateString(server.WorkingDir); err != nil {
		return err
	}
	if err := validateString(server.URL); err != nil {
		return err
	}

	if err := validateStringMap(server.Env); err != nil {
		return err
	}
	if err := validateStringMap(server.Headers); err != nil {
		return err
	}
	if err := validateStringSlice(server.SecretEnv); err != nil {
		return err
	}
	if err := validateStringSlice(server.SecretHeaders); err != nil {
		return err
	}
	if overlapKeys(server.Env, server.SecretEnv) {
		return fmt.Errorf("env keys overlap with secret_env")
	}
	if overlapKeys(server.Headers, server.SecretHeaders) {
		return fmt.Errorf("headers keys overlap with secret_headers")
	}

	if server.TimeoutSec < 0 {
		return fmt.Errorf("timeout_sec must be non-negative")
	}
	if server.Retries < 0 {
		return fmt.Errorf("retries must be non-negative")
	}

	if server.Auth != nil {
		if err := validateAuthConfig(*server.Auth); err != nil {
			return err
		}
	}
	if server.OAuth != nil {
		if err := validateOAuthConfig(*server.OAuth); err != nil {
			return err
		}
	}
	if server.Tools != nil {
		if err := validateToolsConfig(*server.Tools); err != nil {
			return err
		}
	}

	return nil
}

func validateServerPatch(patch ServerPatch) error {
	if patch.Command != nil {
		if err := validateString(*patch.Command); err != nil {
			return err
		}
	}
	if patch.WorkingDir != nil {
		if err := validateString(*patch.WorkingDir); err != nil {
			return err
		}
	}
	if patch.URL != nil {
		if err := validateString(*patch.URL); err != nil {
			return err
		}
	}
	if patch.Args != nil {
		if len(*patch.Args) > maxArgs {
			return fmt.Errorf("too many args")
		}
		for _, arg := range *patch.Args {
			if err := validateString(arg); err != nil {
				return err
			}
		}
	}
	if patch.Env != nil {
		if err := validateStringMap(*patch.Env); err != nil {
			return err
		}
	}
	if patch.Headers != nil {
		if err := validateStringMap(*patch.Headers); err != nil {
			return err
		}
	}
	if patch.SecretEnv != nil {
		if err := validateStringSlice(*patch.SecretEnv); err != nil {
			return err
		}
	}
	if patch.SecretHeaders != nil {
		if err := validateStringSlice(*patch.SecretHeaders); err != nil {
			return err
		}
	}
	if patch.Auth != nil {
		if err := validateAuthConfig(*patch.Auth); err != nil {
			return err
		}
	}
	if patch.OAuth != nil {
		if err := validateOAuthConfig(*patch.OAuth); err != nil {
			return err
		}
	}
	if patch.Tools != nil {
		if err := validateToolsConfig(*patch.Tools); err != nil {
			return err
		}
	}
	return nil
}

func validateAuthConfig(auth AuthConfig) error {
	switch auth.Mode {
	case "", "bearer", "header", "env", "query", "none":
		// ok
	default:
		return fmt.Errorf("invalid auth mode: %s", auth.Mode)
	}
	if err := validateString(auth.Header); err != nil {
		return err
	}
	if err := validateString(auth.Prefix); err != nil {
		return err
	}
	if err := validateString(auth.Env); err != nil {
		return err
	}
	if err := validateString(auth.Query); err != nil {
		return err
	}
	return nil
}

func validateOAuthConfig(oauth OAuthConfig) error {
	if oauth.ClientID != "" {
		if err := validateString(oauth.ClientID); err != nil {
			return err
		}
	}
	if oauth.ClientSecretRef != "" {
		if err := validateString(oauth.ClientSecretRef); err != nil {
			return err
		}
	}
	if err := validateString(oauth.AuthURL); err != nil {
		return err
	}
	if err := validateString(oauth.TokenURL); err != nil {
		return err
	}
	if len(oauth.Scopes) > maxToolNames {
		return fmt.Errorf("too many oauth scopes")
	}
	for _, scope := range oauth.Scopes {
		if err := validateString(scope); err != nil {
			return err
		}
	}
	return nil
}

func validateToolsConfig(tools ToolsConfig) error {
	switch tools.Mode {
	case "", "all", "allowlist", "blocklist":
		// ok
	default:
		return fmt.Errorf("invalid tools mode: %s", tools.Mode)
	}
	if tools.Mode == "allowlist" && len(tools.Disabled) > 0 {
		return fmt.Errorf("tools.disabled not allowed in allowlist mode")
	}
	if tools.Mode == "blocklist" && len(tools.Enabled) > 0 {
		return fmt.Errorf("tools.enabled not allowed in blocklist mode")
	}
	if len(tools.Enabled) > maxToolNames || len(tools.Disabled) > maxToolNames {
		return fmt.Errorf("too many tool entries")
	}
	if err := validateToolList(tools.Enabled, "enabled"); err != nil {
		return err
	}
	if err := validateToolList(tools.Disabled, "disabled"); err != nil {
		return err
	}
	return nil
}

func validateStringMap(values map[string]string) error {
	if len(values) > maxMapEntries {
		return fmt.Errorf("too many entries")
	}
	for key, value := range values {
		if !isValidKey(key) {
			return fmt.Errorf("invalid key: %s", key)
		}
		if err := validateString(value); err != nil {
			return err
		}
	}
	return nil
}

func validateStringSlice(values []string) error {
	if len(values) > maxMapEntries {
		return fmt.Errorf("too many entries")
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !isValidKey(value) {
			return fmt.Errorf("invalid key: %s", value)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("duplicate key: %s", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateString(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > maxValueLength {
		return fmt.Errorf("value too long")
	}
	if hasControlChars(value) {
		return fmt.Errorf("value contains control characters")
	}
	return nil
}

func isValidKey(value string) bool {
	if value == "" {
		return false
	}
	if len(value) > maxKeyLength {
		return false
	}
	return !hasControlChars(value)
}

func validateToolList(values []string, label string) error {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validateString(value); err != nil {
			return err
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("duplicate %s tool: %s", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func hasControlChars(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func overlapKeys(m map[string]string, keys []string) bool {
	if len(m) == 0 || len(keys) == 0 {
		return false
	}
	for _, key := range keys {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}

func validateURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme != "http" {
		return fmt.Errorf("invalid url scheme")
	}
	host := parsed.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("http url not allowed")
}

func validateDepth(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if depth(value) > maxDepth {
		return fmt.Errorf("config nesting too deep")
	}
	return nil
}

func depth(value any) int {
	switch v := value.(type) {
	case map[string]any:
		max := 0
		for _, child := range v {
			if d := depth(child); d > max {
				max = d
			}
		}
		return max + 1
	case []any:
		max := 0
		for _, child := range v {
			if d := depth(child); d > max {
				max = d
			}
		}
		return max + 1
	default:
		return 1
	}
}
