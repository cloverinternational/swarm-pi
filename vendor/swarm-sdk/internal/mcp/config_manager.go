package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Swarm-Code/mono/swarm-core/core"
)

const (
	schemaVersion = 1
)

type ConfigManager struct {
	configDir     string
	workspaceRoot string
	fs            core.ConfigFileSystem
	credentials   CredentialsStore
	plugins       []PluginServerEntry
}

func NewConfigManager(configDir, workspaceRoot string, fs core.ConfigFileSystem, credentials CredentialsStore) *ConfigManager {
	if fs == nil {
		fs = &osFS{}
	}
	if credentials == nil {
		credentials = &noopCredentialsStore{}
	}
	return &ConfigManager{
		configDir:     configDir,
		workspaceRoot: workspaceRoot,
		fs:            fs,
		credentials:   credentials,
	}
}

func (m *ConfigManager) SetPluginServers(servers []PluginServerEntry) {
	m.plugins = append([]PluginServerEntry{}, servers...)
}

// PluginServers returns the configured plugin MCP servers.
func (m *ConfigManager) PluginServers() []PluginServerEntry {
	return append([]PluginServerEntry{}, m.plugins...)
}

func (m *ConfigManager) GlobalPath() string {
	return filepath.Join(m.configDir, "mcp_servers.json")
}

func (m *ConfigManager) ProjectPath() string {
	if m.workspaceRoot == "" {
		return ""
	}
	// Project-local override lives under the workspace's ".swarm" directory
	// (unified from the legacy layout). The global layer path is supplied by
	// the caller via configDir (see GlobalPath).
	return filepath.Join(m.workspaceRoot, ".swarm", "mcp_servers.json")
}

func (m *ConfigManager) LoadLayered(ctx context.Context) (*LayeredConfig, error) {
	globalCfg, err := m.loadConfigFile(ctx, m.GlobalPath())
	if err != nil {
		return nil, err
	}
	projectCfg, err := m.loadConfigFile(ctx, m.ProjectPath())
	if err != nil {
		return nil, err
	}
	return &LayeredConfig{Global: globalCfg, Project: projectCfg}, nil
}

func (m *ConfigManager) Resolve(ctx context.Context) (*ResolvedConfig, error) {
	layered, err := m.LoadLayered(ctx)
	if err != nil {
		return nil, err
	}

	entries := make([]resolvedEntry, 0, 64)
	entries = append(entries, m.buildServerEntries(layered.Project, ScopeProject, OriginProject, precedenceProject, "project")...)
	entries = append(entries, m.buildServerEntries(layered.Global, ScopeGlobal, OriginGlobal, precedenceGlobal, "global")...)

	pluginBase := m.pluginBaseEntries()
	entries = append(entries, pluginBase...)
	entries = append(entries, m.pluginOverrideEntries(layered.Global, precedencePluginOverrideGlobal)...)
	entries = append(entries, m.pluginOverrideEntries(layered.Project, precedencePluginOverrideProject)...)

	resolved := resolveShadowing(entries)
	return &ResolvedConfig{Servers: resolved}, nil
}

func (m *ConfigManager) SaveLayer(ctx context.Context, scope ConfigScope, cfg *ConfigFile) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	cfg.SchemaVersion = schemaVersion
	if err := ValidateConfigFile(cfg); err != nil {
		return err
	}

	path := ""
	switch scope {
	case ScopeGlobal:
		path = m.GlobalPath()
	case ScopeProject:
		path = m.ProjectPath()
	default:
		return fmt.Errorf("unsupported scope: %s", scope)
	}
	if path == "" {
		return fmt.Errorf("config path is empty")
	}

	dir := filepath.Dir(path)
	if err := m.fs.MkdirAll(ctx, dir); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return m.fs.Write(ctx, path, data)
}

func (m *ConfigManager) UpsertServer(ctx context.Context, scope ConfigScope, server ServerConfig) error {
	cfg, err := m.loadConfigFile(ctx, m.pathForScope(scope))
	if err != nil {
		return err
	}
	idx := -1
	for i, s := range cfg.Servers {
		if s.Name == server.Name {
			idx = i
			break
		}
	}
	if idx >= 0 {
		cfg.Servers[idx] = server
	} else {
		cfg.Servers = append(cfg.Servers, server)
	}
	return m.SaveLayer(ctx, scope, cfg)
}

func (m *ConfigManager) RemoveServer(ctx context.Context, scope ConfigScope, name string) error {
	cfg, err := m.loadConfigFile(ctx, m.pathForScope(scope))
	if err != nil {
		return err
	}
	filtered := cfg.Servers[:0]
	for _, srv := range cfg.Servers {
		if srv.Name != name {
			filtered = append(filtered, srv)
		}
	}
	cfg.Servers = filtered
	return m.SaveLayer(ctx, scope, cfg)
}

func (m *ConfigManager) SetServerEnabled(ctx context.Context, scope ConfigScope, name string, enabled bool) error {
	cfg, err := m.loadConfigFile(ctx, m.pathForScope(scope))
	if err != nil {
		return err
	}
	found := false
	for i := range cfg.Servers {
		if cfg.Servers[i].Name == name {
			cfg.Servers[i].Enabled = enabled
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("server not found: %s", name)
	}
	return m.SaveLayer(ctx, scope, cfg)
}

func (m *ConfigManager) SetPluginOverride(ctx context.Context, scope ConfigScope, pluginName, serverName string, patch ServerPatch) error {
	cfg, err := m.loadConfigFile(ctx, m.pathForScope(scope))
	if err != nil {
		return err
	}
	if cfg.PluginOverrides == nil {
		cfg.PluginOverrides = make(map[string]map[string]ServerPatch)
	}
	if cfg.PluginOverrides[pluginName] == nil {
		cfg.PluginOverrides[pluginName] = make(map[string]ServerPatch)
	}
	cfg.PluginOverrides[pluginName][serverName] = patch
	return m.SaveLayer(ctx, scope, cfg)
}

func (m *ConfigManager) ClearPluginOverride(ctx context.Context, scope ConfigScope, pluginName, serverName string) error {
	cfg, err := m.loadConfigFile(ctx, m.pathForScope(scope))
	if err != nil {
		return err
	}
	if cfg.PluginOverrides == nil {
		return nil
	}
	if cfg.PluginOverrides[pluginName] == nil {
		return nil
	}
	delete(cfg.PluginOverrides[pluginName], serverName)
	if len(cfg.PluginOverrides[pluginName]) == 0 {
		delete(cfg.PluginOverrides, pluginName)
	}
	return m.SaveLayer(ctx, scope, cfg)
}

func (m *ConfigManager) ResolveRuntimeServer(ctx context.Context, server ServerConfig) (*RuntimeServer, error) {
	cfg := normalizeServerConfig(server)
	runtime := &RuntimeServer{
		Config:      cfg,
		Headers:     copyStringMap(cfg.Headers),
		Env:         copyStringMap(cfg.Env),
		ResolvedURL: cfg.URL,
		Tools:       normalizeToolsConfig(cfg.Tools),
	}

	credRef := credentialRef(cfg)
	creds, err := m.credentials.Load(ctx)
	if err != nil {
		return nil, err
	}
	if creds == nil {
		creds = &core.Credentials{}
	}
	cred := getCredential(creds, credRef)

	if err := applySecretEnv(runtime, cfg, cred); err != nil {
		return nil, err
	}
	if err := applySecretHeaders(runtime, cfg, cred); err != nil {
		return nil, err
	}

	if cfg.Type == "oauth" && cfg.OAuth != nil {
		secretRef := cfg.OAuth.ClientSecretRef
		if secretRef == "" {
			secretRef = "oauth.client_secret"
		}
		secret, ok := getCredentialHeader(cred, secretRef)
		if !ok {
			return nil, missingCredentialError(credRef, secretRef)
		}
		runtime.OAuthSecret = secret
	}

	if cfg.Type != "oauth" {
		auth := resolveAuthConfig(cfg)
		if err := applyAuth(runtime, cfg, auth, cred); err != nil {
			return nil, err
		}
	}

	return runtime, nil
}

func (m *ConfigManager) loadConfigFile(ctx context.Context, path string) (*ConfigFile, error) {
	if path == "" {
		return defaultConfigFile(), nil
	}
	ok, err := m.fs.Exists(ctx, path)
	if err != nil {
		return nil, err
	}
	if !ok {
		return defaultConfigFile(), nil
	}
	data, err := m.fs.Read(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileSizeBytes {
		return nil, fmt.Errorf("mcp config exceeds size limit")
	}
	if err := validateDepth(data); err != nil {
		return nil, err
	}

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("mcp config is empty")
	}
	if strings.HasPrefix(trimmed, "[") {
		cfg, err := parseLegacyConfig(data)
		if err != nil {
			return nil, err
		}
		return cfg, nil
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	cfg := &ConfigFile{}
	if err := dec.Decode(cfg); err != nil {
		return nil, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing data in config")
	}
	if cfg.SchemaVersion != schemaVersion {
		return nil, fmt.Errorf("unsupported schema_version: %d", cfg.SchemaVersion)
	}
	if err := ValidateConfigFile(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (m *ConfigManager) pathForScope(scope ConfigScope) string {
	switch scope {
	case ScopeGlobal:
		return m.GlobalPath()
	case ScopeProject:
		return m.ProjectPath()
	default:
		return ""
	}
}

func defaultConfigFile() *ConfigFile {
	return &ConfigFile{
		SchemaVersion: schemaVersion,
		Servers:       []ServerConfig{},
	}
}

// ============================================================
// Merge helpers
// ============================================================

const (
	precedencePluginBase            = 10
	precedencePluginOverrideGlobal  = 20
	precedencePluginOverrideProject = 30
	precedenceGlobal                = 40
	precedenceProject               = 50
)

type resolvedEntry struct {
	precedence int
	orderKey   string
	server     ResolvedServer
}

func (m *ConfigManager) buildServerEntries(cfg *ConfigFile, scope ConfigScope, origin OriginType, precedence int, originDetail string) []resolvedEntry {
	if cfg == nil {
		return nil
	}
	entries := make([]resolvedEntry, 0, len(cfg.Servers))
	for _, server := range cfg.Servers {
		entry := ResolvedServer{
			Config:       normalizeServerConfig(server),
			Scope:        scope,
			Origin:       origin,
			OriginDetail: originDetail,
			Editability:  EditabilityEditable,
		}
		entry.HasCredentials = hasCredential(m.credentials, entry.Config)
		entries = append(entries, resolvedEntry{
			precedence: precedence,
			orderKey:   entry.Config.Name,
			server:     entry,
		})
	}
	return entries
}

func (m *ConfigManager) pluginBaseEntries() []resolvedEntry {
	entries := make([]resolvedEntry, 0, len(m.plugins))
	for _, pluginEntry := range m.plugins {
		if !pluginEntry.Plugin.Enabled {
			continue
		}
		origin, detail, editability, warning := pluginOriginInfo(pluginEntry.Plugin)
		server := ResolvedServer{
			Config:         normalizeServerConfig(pluginEntry.Server),
			Scope:          ScopePlugin,
			Origin:         origin,
			OriginDetail:   detail,
			Editability:    editability,
			EditWarning:    warning,
			HasCredentials: hasCredential(m.credentials, pluginEntry.Server),
		}
		entries = append(entries, resolvedEntry{
			precedence: precedencePluginBase,
			orderKey:   pluginEntry.Plugin.Name + "/" + pluginEntry.Server.Name,
			server:     server,
		})
	}
	return entries
}

func (m *ConfigManager) pluginOverrideEntries(cfg *ConfigFile, precedence int) []resolvedEntry {
	if cfg == nil || cfg.PluginOverrides == nil {
		return nil
	}
	pluginMap := m.pluginBaseIndex()
	entries := make([]resolvedEntry, 0)
	for pluginName, overrides := range cfg.PluginOverrides {
		baseMap, ok := pluginMap[pluginName]
		if !ok {
			continue
		}
		for serverName, patch := range overrides {
			base, ok := baseMap[serverName]
			if !ok {
				continue
			}
			merged := mergeServer(base.Server, patch)
			origin, detail, editability, warning := pluginOriginInfo(base.Plugin)
			entry := ResolvedServer{
				Config:         normalizeServerConfig(merged),
				Scope:          ScopePlugin,
				Origin:         origin,
				OriginDetail:   detail,
				Editability:    editability,
				EditWarning:    warning,
				HasCredentials: hasCredential(m.credentials, merged),
				HasOverride:    true,
			}
			entries = append(entries, resolvedEntry{
				precedence: precedence,
				orderKey:   pluginName + "/" + serverName,
				server:     entry,
			})
		}
	}
	return entries
}

func (m *ConfigManager) pluginBaseIndex() map[string]map[string]PluginServerEntry {
	index := make(map[string]map[string]PluginServerEntry)
	for _, entry := range m.plugins {
		if !entry.Plugin.Enabled {
			continue
		}
		if index[entry.Plugin.Name] == nil {
			index[entry.Plugin.Name] = make(map[string]PluginServerEntry)
		}
		index[entry.Plugin.Name][entry.Server.Name] = entry
	}
	return index
}

func resolveShadowing(entries []resolvedEntry) []ResolvedServer {
	if len(entries) == 0 {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].precedence == entries[j].precedence {
			return entries[i].orderKey < entries[j].orderKey
		}
		return entries[i].precedence > entries[j].precedence
	})

	resolved := make([]ResolvedServer, len(entries))
	for i := range entries {
		entry := entries[i]
		shadowedBy := make([]ShadowInfo, 0)
		for j := range i {
			if entries[j].server.Config.Name == entry.server.Config.Name {
				shadowedBy = append(shadowedBy, ShadowInfo{
					Name:   entries[j].server.Config.Name,
					Origin: entries[j].server.Origin,
				})
			}
		}
		entry.server.ShadowedBy = shadowedBy
		resolved[i] = entry.server
	}
	return resolved
}

func pluginOriginInfo(plugin PluginInfo) (OriginType, string, Editability, string) {
	origin := OriginPlugin
	detail := "plugin:" + plugin.Name
	if plugin.Source == "marketplace" {
		origin = OriginMarketplace
		if plugin.MarketplaceName != "" {
			detail = "marketplace:" + plugin.MarketplaceName
		}
	}
	editability := EditabilityReadonly
	warning := ""
	if isOpenSourcePlugin(plugin) {
		editability = EditabilityEditableWithWarning
		warning = "Editing plugin servers is not preserved across updates."
	}
	return origin, detail, editability, warning
}

func isOpenSourcePlugin(plugin PluginInfo) bool {
	if plugin.License == "" || plugin.Repository == "" {
		return false
	}
	license := strings.ToUpper(strings.TrimSpace(plugin.License))
	osi := map[string]bool{
		"APACHE-2.0":   true,
		"MIT":          true,
		"BSD-2-CLAUSE": true,
		"BSD-3-CLAUSE": true,
		"MPL-2.0":      true,
		"LGPL-3.0":     true,
		"GPL-3.0":      true,
		"AGPL-3.0":     true,
		"ISC":          true,
	}
	return osi[license]
}
