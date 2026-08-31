package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/plugins"
	"github.com/pelletier/go-toml/v2"
)

// ImportPayload describes the incoming import source.
type ImportPayload struct {
	Path string
	Text string
}

// ImportServer captures a normalized server plus extracted secrets.
type ImportServer struct {
	Config       ServerConfig
	SecretValues map[string]string
}

// ImportResult is the parsed output for preview/apply flows.
type ImportResult struct {
	Servers             []ImportServer
	Warnings            []string
	RequiredCredentials []string
}

// Importer handles parsing and validation for MCP imports.
type Importer struct {
	credentials CredentialsStore
}

// NewImporter creates a new importer with an optional credentials store.
func NewImporter(credentials CredentialsStore) *Importer {
	if credentials == nil {
		credentials = &noopCredentialsStore{}
	}
	return &Importer{credentials: credentials}
}

// Preview parses an import payload and returns normalized configs plus warnings.
func (i *Importer) Preview(ctx context.Context, sourceType string, payload ImportPayload) (*ImportResult, error) {
	kind := strings.ToLower(strings.TrimSpace(sourceType))
	if kind == "" {
		return nil, fmt.Errorf("source_type is required")
	}

	var (
		servers  []ImportServer
		warnings []string
		err      error
	)

	switch kind {
	case "claude":
		servers, warnings, err = i.previewClaude(payload)
	case "codex":
		servers, warnings, err = i.previewCodex(payload)
	case "json":
		servers, warnings, err = i.previewJSON(payload)
	case "toml":
		servers, warnings, err = i.previewTOML(payload)
	default:
		return nil, fmt.Errorf("unsupported source_type: %s", kind)
	}
	if err != nil {
		return nil, err
	}

	servers, dedupeWarnings := dedupeImportServers(servers)
	warnings = append(warnings, dedupeWarnings...)
	if err := validateImportServers(servers); err != nil {
		return nil, err
	}

	required, credentialWarnings := i.collectRequiredCredentials(ctx, servers)
	warnings = append(warnings, credentialWarnings...)

	return &ImportResult{
		Servers:             servers,
		Warnings:            compactWarnings(warnings),
		RequiredCredentials: required,
	}, nil
}

// ============================================================
// Claude import
// ============================================================

type claudeServerConfig struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

var errMissingClaudeMCPServers = errors.New("missing mcpServers")

func (i *Importer) previewClaude(payload ImportPayload) ([]ImportServer, []string, error) {
	if payload.Text != "" || payload.Path != "" {
		data, err := loadImportData(payload)
		if err != nil {
			return nil, nil, err
		}
		return parseClaudeBytes(data)
	}

	paths := discoverClaudePaths()
	var (
		servers  []ImportServer
		warnings []string
	)
	for _, path := range paths {
		data, err := readImportFile(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped %s: %v", path, err))
			continue
		}
		parsed, warn, err := parseClaudeBytes(data)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped %s: %v", path, err))
			continue
		}
		servers = append(servers, parsed...)
		warnings = append(warnings, warn...)
	}
	if len(servers) == 0 {
		warnings = append(warnings, "no Claude MCP configs found")
	}
	return servers, warnings, nil
}

func parseClaudeBytes(data []byte) ([]ImportServer, []string, error) {
	if err := validateJSONImport(data); err != nil {
		return nil, nil, err
	}

	if servers, err := parseClaudeDesktopConfig(data); err == nil {
		return servers, nil, nil
	} else if !errors.Is(err, errMissingClaudeMCPServers) {
		return nil, nil, err
	}
	return parseClaudeMCPConfig(data)
}

func parseClaudeMCPConfig(data []byte) ([]ImportServer, []string, error) {
	var raw map[string]claudeServerConfig
	if err := decodeJSONStrict(data, &raw); err != nil {
		return nil, nil, err
	}
	return mapServersToImport(raw), nil, nil
}

func parseClaudeDesktopConfig(data []byte) ([]ImportServer, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	raw, ok := root["mcpServers"]
	if !ok {
		return nil, errMissingClaudeMCPServers
	}

	var servers map[string]claudeServerConfig
	if err := decodeJSONStrict(raw, &servers); err != nil {
		return nil, err
	}
	parsed := mapServersToImport(servers)
	return parsed, nil
}

func discoverClaudePaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	paths := []string{
		filepath.Join(home, ".claude", ".mcp.json"),
	}

	switch runtime.GOOS {
	case "darwin":
		paths = append(paths, filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"))
	case "windows":
		paths = append(paths, filepath.Join(home, "AppData", "Roaming", "Claude", "claude_desktop_config.json"))
	default:
		paths = append(paths, filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"))
	}

	pluginsDir := filepath.Join(home, ".claude", "plugins")
	_ = filepath.Walk(pluginsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if info.Name() == ".mcp.json" {
			paths = append(paths, path)
		}
		return nil
	})

	return uniqueStrings(paths)
}

// ============================================================
// Codex import
// ============================================================

func (i *Importer) previewCodex(payload ImportPayload) ([]ImportServer, []string, error) {
	data, err := loadImportDataWithDefault(payload, filepath.Join(userHomeDir(), ".codex", "config.toml"))
	if err != nil {
		return nil, nil, err
	}
	raw, err := decodeTOMLToMap(data)
	if err != nil {
		return nil, nil, err
	}
	mcpServers, ok := raw["mcp_servers"]
	if !ok {
		return nil, nil, fmt.Errorf("missing mcp_servers table")
	}

	payloadMap, ok := mcpServers.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("invalid mcp_servers format")
	}

	servers, err := mapToImportServers(payloadMap)
	if err != nil {
		return nil, nil, err
	}
	return servers, nil, nil
}

// ============================================================
// JSON/TOML import
// ============================================================

func (i *Importer) previewJSON(payload ImportPayload) ([]ImportServer, []string, error) {
	data, err := loadImportData(payload)
	if err != nil {
		return nil, nil, err
	}
	if err := validateJSONImport(data); err != nil {
		return nil, nil, err
	}

	isCanonical, err := detectCanonicalJSON(data)
	if err != nil {
		return nil, nil, err
	}
	if isCanonical {
		cfg, warnings, err := parseCanonicalJSON(data)
		if err != nil {
			return nil, nil, err
		}
		return configToImport(cfg), warnings, nil
	}

	mapCfg, err := parseMapJSON(data)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := parsePluginMCPJSON(data, mapCfg)
	if err != nil {
		return nil, nil, err
	}
	return parsed, nil, nil
}

func (i *Importer) previewTOML(payload ImportPayload) ([]ImportServer, []string, error) {
	data, err := loadImportData(payload)
	if err != nil {
		return nil, nil, err
	}
	raw, err := decodeTOMLToMap(data)
	if err != nil {
		return nil, nil, err
	}
	if isCanonicalMap(raw) {
		cfg, warnings, err := parseCanonicalTOML(raw)
		if err != nil {
			return nil, nil, err
		}
		return configToImport(cfg), warnings, nil
	}

	servers, err := mapToImportServers(raw)
	if err != nil {
		return nil, nil, err
	}
	return servers, nil, nil
}

// ============================================================
// Parsing helpers
// ============================================================

type mapServerConfig struct {
	Type    string            `json:"type,omitempty" toml:"type,omitempty"`
	Command string            `json:"command,omitempty" toml:"command,omitempty"`
	Args    []string          `json:"args,omitempty" toml:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty" toml:"env,omitempty"`
	URL     string            `json:"url,omitempty" toml:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty" toml:"headers,omitempty"`
}

func mapServersToImport(raw map[string]claudeServerConfig) []ImportServer {
	servers := make([]ImportServer, 0, len(raw))
	for name, cfg := range raw {
		server, err := buildExternalImport(name, mapServerConfig{
			Type:    cfg.Type,
			Command: cfg.Command,
			Args:    cfg.Args,
			Env:     cfg.Env,
			URL:     cfg.URL,
			Headers: cfg.Headers,
		})
		if err != nil {
			continue
		}
		servers = append(servers, server)
	}
	return servers
}

func parseCanonicalJSON(data []byte) (*ConfigFile, []string, error) {
	var cfg ConfigFile
	if err := decodeJSONStrict(data, &cfg); err != nil {
		return nil, nil, err
	}
	if cfg.SchemaVersion != schemaVersion {
		return nil, nil, fmt.Errorf("unsupported schema_version: %d", cfg.SchemaVersion)
	}
	warnings := []string{}
	if len(cfg.PluginOverrides) > 0 {
		warnings = append(warnings, "plugin_overrides ignored during import")
		cfg.PluginOverrides = nil
	}
	return &cfg, warnings, nil
}

func parseMapJSON(data []byte) (map[string]mapServerConfig, error) {
	var raw map[string]mapServerConfig
	if err := decodeJSONStrict(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func parseCanonicalTOML(raw map[string]any) (*ConfigFile, []string, error) {
	jsonData, err := json.Marshal(raw)
	if err != nil {
		return nil, nil, err
	}
	return parseCanonicalJSON(jsonData)
}

func mapToImportServers(raw map[string]any) ([]ImportServer, error) {
	jsonData, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var mapCfg map[string]mapServerConfig
	if err := decodeJSONStrict(jsonData, &mapCfg); err != nil {
		return nil, err
	}

	servers := make([]ImportServer, 0, len(mapCfg))
	for name, cfg := range mapCfg {
		server, err := buildExternalImport(name, cfg)
		if err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}
	return servers, nil
}

func parsePluginMCPJSON(data []byte, mapCfg map[string]mapServerConfig) ([]ImportServer, error) {
	parsed, err := plugins.ParseMCPConfigBytes(data)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]mapServerConfig, len(mapCfg))
	maps.Copy(byName, mapCfg)

	servers := make([]ImportServer, 0, len(parsed))
	for _, server := range parsed {
		cfg := mapServerConfig{
			Type:    server.Type,
			Command: server.Command,
			Args:    server.Args,
			Env:     server.Environment,
			URL:     server.URL,
		}
		if extra, ok := byName[server.Name]; ok {
			if extra.Headers != nil {
				cfg.Headers = extra.Headers
			}
		}
		converted, err := buildExternalImport(server.Name, cfg)
		if err != nil {
			return nil, err
		}
		servers = append(servers, converted)
	}
	return servers, nil
}

func buildExternalImport(name string, cfg mapServerConfig) (ImportServer, error) {
	server := ServerConfig{
		Name:    name,
		Type:    normalizeExternalType(cfg.Type),
		Command: cfg.Command,
		Args:    copyStringSlice(cfg.Args),
		URL:     cfg.URL,
		Enabled: false,
	}

	secrets := map[string]string{}
	if len(cfg.Env) > 0 {
		keys, values := extractSecretValues(cfg.Env)
		server.SecretEnv = keys
		maps.Copy(secrets, values)
	}
	if len(cfg.Headers) > 0 {
		keys, values := extractSecretValues(cfg.Headers)
		server.SecretHeaders = keys
		maps.Copy(secrets, values)
	}

	server = normalizeServerConfig(server)
	if server.Auth == nil && (server.Type == "http" || server.Type == "sse") {
		server.Auth = &AuthConfig{Mode: "none"}
	}
	return ImportServer{Config: server, SecretValues: secrets}, nil
}

func normalizeExternalType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "streamable-http":
		return "http"
	default:
		return normalized
	}
}

// ============================================================
// Credential helpers
// ============================================================

func (i *Importer) collectRequiredCredentials(ctx context.Context, servers []ImportServer) ([]string, []string) {
	creds, err := i.credentials.Load(ctx)
	if err != nil {
		return nil, []string{"unable to load credentials for preview"}
	}
	if creds == nil {
		creds = &core.Credentials{}
	}

	required := make(map[string]struct{})
	for _, entry := range servers {
		ref := credentialRef(entry.Config)
		cred := getCredential(creds, ref)
		if needsAuthToken(entry.Config) {
			if cred == nil || cred.Token == "" {
				required[ref] = struct{}{}
				continue
			}
		}

		if missingSecretKeys(entry.Config.SecretEnv, entry.SecretValues, cred) ||
			missingSecretKeys(entry.Config.SecretHeaders, entry.SecretValues, cred) ||
			missingOAuthSecret(entry.Config, entry.SecretValues, cred) {
			required[ref] = struct{}{}
		}
	}

	refs := make([]string, 0, len(required))
	for ref := range required {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs, nil
}

func needsAuthToken(server ServerConfig) bool {
	if strings.ToLower(server.Type) == "oauth" {
		return false
	}
	auth := resolveAuthConfig(server)
	mode := strings.ToLower(strings.TrimSpace(auth.Mode))
	return mode != "" && mode != "none"
}

func missingSecretKeys(keys []string, values map[string]string, cred *core.MCPCredential) bool {
	for _, key := range keys {
		if value, ok := values[key]; ok && value != "" {
			continue
		}
		if cred == nil {
			return true
		}
		if cred.Headers == nil || cred.Headers[key] == "" {
			return true
		}
	}
	return false
}

func missingOAuthSecret(server ServerConfig, values map[string]string, cred *core.MCPCredential) bool {
	if strings.ToLower(server.Type) != "oauth" || server.OAuth == nil {
		return false
	}
	secretRef := server.OAuth.ClientSecretRef
	if secretRef == "" {
		secretRef = "oauth.client_secret"
	}
	if value, ok := values[secretRef]; ok && value != "" {
		return false
	}
	if cred == nil || cred.Headers == nil || cred.Headers[secretRef] == "" {
		return true
	}
	return false
}

// ============================================================
// Validation helpers
// ============================================================

func validateImportServers(servers []ImportServer) error {
	cfg := &ConfigFile{
		SchemaVersion: schemaVersion,
		Servers:       make([]ServerConfig, 0, len(servers)),
	}
	for _, entry := range servers {
		cfg.Servers = append(cfg.Servers, entry.Config)
	}
	return ValidateConfigFile(cfg)
}

func validateJSONImport(data []byte) error {
	if len(data) > maxFileSizeBytes {
		return fmt.Errorf("import file exceeds size limit")
	}
	if err := validateDepth(data); err != nil {
		return err
	}
	return nil
}

func decodeJSONStrict(data []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("unexpected trailing data")
	}
	return nil
}

func detectCanonicalJSON(data []byte) (bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, err
	}
	if _, ok := raw["schema_version"]; ok {
		return true, nil
	}
	if _, ok := raw["servers"]; ok {
		return true, nil
	}
	return false, nil
}

func decodeTOMLToMap(data []byte) (map[string]any, error) {
	if len(data) > maxFileSizeBytes {
		return nil, fmt.Errorf("import file exceeds size limit")
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if depth(raw) > maxDepth {
		return nil, fmt.Errorf("config nesting too deep")
	}
	return raw, nil
}

func isCanonicalMap(raw map[string]any) bool {
	if raw == nil {
		return false
	}
	if _, ok := raw["schema_version"]; ok {
		return true
	}
	if _, ok := raw["servers"]; ok {
		return true
	}
	return false
}

// ============================================================
// Utility helpers
// ============================================================

func configToImport(cfg *ConfigFile) []ImportServer {
	if cfg == nil {
		return nil
	}
	servers := make([]ImportServer, 0, len(cfg.Servers))
	for _, server := range cfg.Servers {
		normalized := normalizeServerConfig(server)
		sanitized, secrets := sanitizeImportSecrets(normalized)
		servers = append(servers, ImportServer{Config: sanitized, SecretValues: secrets})
	}
	return servers
}

func extractSecretValues(values map[string]string) ([]string, map[string]string) {
	if len(values) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(values))
	secrets := make(map[string]string, len(values))
	for key, value := range values {
		keys = append(keys, key)
		if !isPlaceholderValue(value) {
			secrets[key] = value
		}
	}
	sort.Strings(keys)
	return keys, secrets
}

func sanitizeImportSecrets(server ServerConfig) (ServerConfig, map[string]string) {
	sanitized := server
	secrets := map[string]string{}

	if len(server.Env) > 0 {
		keys, values := extractSecretValues(server.Env)
		sanitized.SecretEnv = mergeSecretKeys(sanitized.SecretEnv, keys)
		sanitized.Env = nil
		maps.Copy(secrets, values)
	}
	if len(server.Headers) > 0 {
		keys, values := extractSecretValues(server.Headers)
		sanitized.SecretHeaders = mergeSecretKeys(sanitized.SecretHeaders, keys)
		sanitized.Headers = nil
		maps.Copy(secrets, values)
	}

	return sanitized, secrets
}

func mergeSecretKeys(existing []string, extra []string) []string {
	if len(existing) == 0 && len(extra) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(existing)+len(extra))
	for _, key := range existing {
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
	}
	for _, key := range extra {
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func isPlaceholderValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, "$") {
		return true
	}
	if strings.HasPrefix(trimmed, "${") && strings.HasSuffix(trimmed, "}") {
		return true
	}
	return false
}

func dedupeImportServers(servers []ImportServer) ([]ImportServer, []string) {
	if len(servers) == 0 {
		return servers, nil
	}
	seen := make(map[string]struct{}, len(servers))
	deduped := make([]ImportServer, 0, len(servers))
	var warnings []string
	for _, entry := range servers {
		name := entry.Config.Name
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			warnings = append(warnings, fmt.Sprintf("duplicate server name ignored: %s", name))
			continue
		}
		seen[name] = struct{}{}
		deduped = append(deduped, entry)
	}
	return deduped, warnings
}

func compactWarnings(warnings []string) []string {
	if len(warnings) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(warnings))
	unique := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		warning = strings.TrimSpace(warning)
		if warning == "" {
			continue
		}
		if _, ok := seen[warning]; ok {
			continue
		}
		seen[warning] = struct{}{}
		unique = append(unique, warning)
	}
	sort.Strings(unique)
	return unique
}

func loadImportData(payload ImportPayload) ([]byte, error) {
	if payload.Text != "" {
		if err := validateImportSize([]byte(payload.Text)); err != nil {
			return nil, err
		}
		return []byte(payload.Text), nil
	}
	if payload.Path == "" {
		return nil, fmt.Errorf("path or text is required")
	}
	return readImportFile(payload.Path)
}

func loadImportDataWithDefault(payload ImportPayload, fallbackPath string) ([]byte, error) {
	if payload.Text != "" {
		if err := validateImportSize([]byte(payload.Text)); err != nil {
			return nil, err
		}
		return []byte(payload.Text), nil
	}
	if payload.Path != "" {
		return readImportFile(payload.Path)
	}
	if fallbackPath == "" {
		return nil, fmt.Errorf("path or text is required")
	}
	return readImportFile(fallbackPath)
}

func readImportFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := validateImportSize(data); err != nil {
		return nil, err
	}
	return data, nil
}

func validateImportSize(data []byte) error {
	if len(data) > maxFileSizeBytes {
		return fmt.Errorf("import file exceeds size limit")
	}
	return nil
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
