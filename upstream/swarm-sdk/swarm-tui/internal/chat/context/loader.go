package context

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// FileLoader implements ContextLoader using filesystem operations.
type FileLoader struct {
	config            ContextConfig
	globalConfig      ContextConfig
	projectConfig     ContextConfig
	configPath        string
	projectConfigPath string
	workspaceRoot     string
	excludedSources   map[string]bool
}

// NewFileLoader creates a new file-based context loader using the current working directory.
func NewFileLoader() *FileLoader {
	cwd, _ := os.Getwd()
	return NewFileLoaderWithRoot(cwd)
}

// NewFileLoaderWithRoot creates a loader scoped to a workspace root.
func NewFileLoaderWithRoot(workspaceRoot string) *FileLoader {
	home, _ := os.UserHomeDir()
	return NewFileLoaderWithConfigDir(filepath.Join(home, ".swarmos"), workspaceRoot)
}

// NewFileLoaderWithConfigDir creates a loader scoped to the provided config directory.
// This is useful when SwarmOS is running with an overridden config dir (for example, tests or sandboxed runs).
func NewFileLoaderWithConfigDir(configDir string, workspaceRoot string) *FileLoader {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".swarmos")
	}

	loader := &FileLoader{
		configPath: filepath.Join(configDir, "context_config.json"),
	}
	loader.SetWorkspaceRoot(workspaceRoot)

	// Load config or use defaults.
	if err := loader.loadConfig(); err != nil {
		loader.globalConfig = DefaultConfig()
		loader.projectConfig = ContextConfig{}
		loader.config = mergeConfigs(loader.globalConfig, loader.projectConfig)
		_ = loader.saveConfigToPath(loader.globalConfig, loader.configPath)
	}

	return loader
}

// SetWorkspaceRoot updates the workspace root used for project overrides.
func (l *FileLoader) SetWorkspaceRoot(workspaceRoot string) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	l.workspaceRoot = workspaceRoot
	if workspaceRoot == "" {
		l.projectConfigPath = ""
		return
	}
	l.projectConfigPath = filepath.Join(workspaceRoot, ".swarm", "context_config.json")
}

// ExcludeSources prevents selected context sources from being loaded without
// mutating the persisted global or project context configuration.
func (l *FileLoader) ExcludeSources(sourceIDs ...string) {
	if l.excludedSources == nil {
		l.excludedSources = make(map[string]bool)
	}
	for _, id := range sourceIDs {
		if id = strings.TrimSpace(id); id != "" {
			l.excludedSources[id] = true
		}
	}
}

func (l *FileLoader) withExclusions(config ContextConfig) ContextConfig {
	if len(l.excludedSources) == 0 {
		return config
	}
	config.Sources = append([]ContextSourceConfig(nil), config.Sources...)
	for id := range l.excludedSources {
		if source, idx, ok := FindSourceByID(config.Sources, id); ok {
			source.Enabled = false
			config.Sources[idx] = source
			continue
		}
		config.Sources = append(config.Sources, ContextSourceConfig{ID: id, Enabled: false})
	}
	return config
}

// loadConfig loads configuration from disk.
func (l *FileLoader) loadConfig() error {
	globalCfg, globalNeedsSave, err := l.loadConfigFile(l.configPath, true)
	if err != nil {
		return err
	}

	projectCfg, projectNeedsSave, err := l.loadConfigFile(l.projectConfigPath, false)
	if err != nil {
		return err
	}

	l.globalConfig = globalCfg
	l.projectConfig = projectCfg
	l.config = mergeConfigs(globalCfg, projectCfg)

	if globalNeedsSave {
		_ = l.saveConfigToPath(globalCfg, l.configPath)
	}
	if projectNeedsSave {
		_ = l.saveConfigToPath(projectCfg, l.projectConfigPath)
	}

	return nil
}

func (l *FileLoader) loadConfigFile(path string, createDefault bool) (ContextConfig, bool, error) {
	if path == "" {
		return ContextConfig{}, false, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if createDefault {
				return DefaultConfig(), true, nil
			}
			return ContextConfig{}, false, nil
		}
		return ContextConfig{}, false, err
	}

	config, migrated, err := parseConfigData(data)
	if err != nil {
		return ContextConfig{}, false, err
	}
	return NormalizeConfig(config), migrated, nil
}

func parseConfigData(data []byte) (ContextConfig, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return ContextConfig{}, false, err
	}

	if _, ok := raw["sources"]; ok {
		var cfg ContextConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return ContextConfig{}, false, err
		}
		return cfg, false, nil
	}

	var legacy legacyContextConfig
	if err := json.Unmarshal(data, &legacy); err != nil {
		return ContextConfig{}, false, err
	}
	legacy = applyLegacyDefaults(legacy, raw)
	return migrateLegacyConfig(legacy), true, nil
}

func (l *FileLoader) saveConfigToPath(config ContextConfig, path string) error {
	if path == "" {
		return nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// GetConfig returns the merged configuration (global + project overrides).
func (l *FileLoader) GetConfig() ContextConfig {
	if err := l.loadConfig(); err == nil {
		return l.withExclusions(l.config)
	}
	return l.withExclusions(l.config)
}

// GetGlobalConfig returns the global configuration only.
func (l *FileLoader) GetGlobalConfig() ContextConfig {
	if err := l.loadConfig(); err == nil {
		return l.globalConfig
	}
	return l.globalConfig
}

// GetProjectConfig returns the project configuration only.
func (l *FileLoader) GetProjectConfig() ContextConfig {
	if err := l.loadConfig(); err == nil {
		return l.projectConfig
	}
	return l.projectConfig
}

// SetConfig updates the global configuration.
func (l *FileLoader) SetConfig(config ContextConfig) error {
	return l.SetGlobalConfig(config)
}

// SetGlobalConfig persists the global configuration.
func (l *FileLoader) SetGlobalConfig(config ContextConfig) error {
	config = NormalizeConfig(config)
	l.globalConfig = config
	l.config = mergeConfigs(l.globalConfig, l.projectConfig)
	return l.saveConfigToPath(config, l.configPath)
}

// SetProjectConfig persists the project configuration.
func (l *FileLoader) SetProjectConfig(config ContextConfig) error {
	config = NormalizeConfig(config)
	l.projectConfig = config
	l.config = mergeConfigs(l.globalConfig, l.projectConfig)
	return l.saveConfigToPath(config, l.projectConfigPath)
}

func mergeConfigs(globalCfg, projectCfg ContextConfig) ContextConfig {
	return NormalizeConfig(ContextConfig{
		Defaults: mergeDefaults(globalCfg.Defaults, projectCfg.Defaults),
		Sources:  mergeSources(globalCfg.Sources, projectCfg.Sources),
	})
}

func mergeDefaults(globalDefaults, projectDefaults ContextDefaults) ContextDefaults {
	merged := globalDefaults
	if projectDefaults.RefreshMode != "" && projectDefaults.RefreshMode != RefreshInherit {
		merged.RefreshMode = projectDefaults.RefreshMode
	}
	if projectDefaults.TTLSeconds != nil {
		merged.TTLSeconds = projectDefaults.TTLSeconds
	}
	if projectDefaults.CachePolicy != "" && projectDefaults.CachePolicy != CacheInherit {
		merged.CachePolicy = projectDefaults.CachePolicy
	}
	if projectDefaults.MCPTimeoutMS != nil {
		merged.MCPTimeoutMS = projectDefaults.MCPTimeoutMS
	}
	if projectDefaults.MCPConcurrency != nil {
		merged.MCPConcurrency = projectDefaults.MCPConcurrency
	}
	return merged
}

func mergeSources(globalSources, projectSources []ContextSourceConfig) []ContextSourceConfig {
	globalSources = NormalizeSourceConfigs(globalSources)
	projectSources = NormalizeSourceConfigs(projectSources)

	if len(projectSources) == 0 {
		return globalSources
	}

	out := make([]ContextSourceConfig, len(globalSources))
	copy(out, globalSources)

	index := make(map[string]int)
	for i, source := range out {
		if source.ID == "" {
			continue
		}
		index[source.ID] = i
	}

	for _, source := range projectSources {
		if source.ID == "" {
			continue
		}
		if idx, ok := index[source.ID]; ok {
			out[idx] = source
			continue
		}
		index[source.ID] = len(out)
		out = append(out, source)
	}

	return out
}

// LoadContext loads all enabled context sources.
func (l *FileLoader) LoadContext(config ContextConfig, workDir string) (*LoadedContext, error) {
	ctx := &LoadedContext{
		Sources: make([]ContextSource, 0),
	}

	home, _ := os.UserHomeDir()
	seen := make(map[string]bool)

	for _, source := range NormalizeSourceConfigs(config.Sources) {
		if l.excludedSources[source.ID] {
			continue
		}
		if source.ID != "" {
			if seen[source.ID] {
				continue
			}
			seen[source.ID] = true
		}

		if !source.Enabled || IsMCPSourceConfig(source) {
			continue
		}

		switch source.ID {
		case SourceIDGlobalClaudeMd:
			path := filepath.Join(home, ".claude", "CLAUDE.md")
			if content := l.tryReadFile(path); content != "" {
				ctx.Sources = append(ctx.Sources, ContextSource{
					Name:    "globalClaudeMd",
					Path:    path,
					Content: content,
				})
			}
		case SourceIDGlobalSwarmMd:
			path := filepath.Join(home, ".swarm", "SWARM.md")
			if content := l.tryReadFile(path); content != "" {
				ctx.Sources = append(ctx.Sources, ContextSource{
					Name:    "globalSwarmMd",
					Path:    path,
					Content: content,
				})
			}
		case SourceIDProjectClaudeMd:
			content := l.tryReadFile(filepath.Join(workDir, ".claude", "CLAUDE.md"))
			path := filepath.Join(workDir, ".claude", "CLAUDE.md")
			if content == "" {
				content = l.tryReadFile(filepath.Join(workDir, "CLAUDE.md"))
				path = filepath.Join(workDir, "CLAUDE.md")
			}
			if content != "" {
				ctx.Sources = append(ctx.Sources, ContextSource{
					Name:    "claudeMd",
					Path:    path,
					Content: content,
				})
			}
		case SourceIDProjectSwarmMd:
			content := l.tryReadFile(filepath.Join(workDir, ".swarm", "SWARM.md"))
			path := filepath.Join(workDir, ".swarm", "SWARM.md")
			if content == "" {
				content = l.tryReadFile(filepath.Join(workDir, "SWARM.md"))
				path = filepath.Join(workDir, "SWARM.md")
			}
			if content != "" {
				ctx.Sources = append(ctx.Sources, ContextSource{
					Name:    "swarmMd",
					Path:    path,
					Content: content,
				})
			}
		case SourceIDAgentsMd:
			content := l.tryReadFile(filepath.Join(workDir, ".swarm", "AGENTS.md"))
			path := filepath.Join(workDir, ".swarm", "AGENTS.md")
			if content == "" {
				content = l.tryReadFile(filepath.Join(workDir, ".claude", "AGENTS.md"))
				path = filepath.Join(workDir, ".claude", "AGENTS.md")
			}
			if content == "" {
				content = l.tryReadFile(filepath.Join(workDir, "AGENTS.md"))
				path = filepath.Join(workDir, "AGENTS.md")
			}
			if content != "" {
				ctx.Sources = append(ctx.Sources, ContextSource{
					Name:    "agentsMd",
					Path:    path,
					Content: content,
				})
			}
		case SourceIDIndexMd:
			content := l.tryReadFile(filepath.Join(workDir, "INDEX.md"))
			if content != "" {
				ctx.Sources = append(ctx.Sources, ContextSource{
					Name:    "indexMd",
					Path:    filepath.Join(workDir, "INDEX.md"),
					Content: content,
				})
			}
		case SourceIDGitStatus:
			if status := l.getGitStatus(workDir); status != "" {
				ctx.Sources = append(ctx.Sources, ContextSource{
					Name:    "gitStatus",
					Content: status,
				})
			}
		case SourceIDProjectName:
			projectName := filepath.Base(workDir)
			if projectName != "" && projectName != "." {
				ctx.Sources = append(ctx.Sources, ContextSource{
					Name:    "projectName",
					Content: projectName,
				})
			}
		case SourceIDCurrentDate:
			currentDate := nowFunc().Format("2006-01-02")
			ctx.Sources = append(ctx.Sources, ContextSource{
				Name:    "currentDate",
				Content: currentDate,
			})
		}
	}

	return ctx, nil
}

// ToggleSource enables or disables a specific source in the global config.
func (l *FileLoader) ToggleSource(sourceID string) {
	cfg := l.GetGlobalConfig()
	if source, idx, ok := FindSourceByID(cfg.Sources, sourceID); ok {
		cfg.Sources[idx].Enabled = !source.Enabled
		_ = l.SetGlobalConfig(cfg)
		return
	}

	if def, ok := DefaultSourceConfig(sourceID); ok {
		def.Enabled = !def.Enabled
		cfg.Sources = append(cfg.Sources, def)
		_ = l.SetGlobalConfig(cfg)
	}
}

// tryReadFile attempts to read a file, returning empty string on error.
func (l *FileLoader) tryReadFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// getGitStatus returns git status output, or empty string if not a git repo.
func (l *FileLoader) getGitStatus(workDir string) string {
	// Check if it's a git repo.
	gitDir := filepath.Join(workDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return ""
	}

	var result strings.Builder

	// Get absolute path.
	absPath, err := filepath.Abs(workDir)
	if err != nil {
		absPath = workDir
	}
	result.WriteString("Working Directory: " + absPath + "\n\n")

	// Get current branch.
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = workDir
	branchOutput, err := cmd.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(branchOutput))
	result.WriteString("Current branch: " + branch + "\n")

	// Get last 5 commits (short format).
	cmd = exec.Command("git", "log", "--oneline", "-5", "--format=%h %s")
	cmd.Dir = workDir
	commitsOutput, err := cmd.Output()
	if err == nil {
		commits := strings.TrimSpace(string(commitsOutput))
		if commits != "" {
			result.WriteString("\nLast 5 commits:\n" + commits + "\n")
		}
	}

	// Get short status (all changes).
	cmd = exec.Command("git", "status", "--short")
	cmd.Dir = workDir
	statusOutput, err := cmd.Output()
	if err != nil {
		return result.String()
	}
	status := strings.TrimSpace(string(statusOutput))
	if status != "" {
		result.WriteString("\nStatus:\n" + status + "\n")
	}

	// Get staged files with full paths.
	cmd = exec.Command("git", "diff", "--cached", "--name-status")
	cmd.Dir = workDir
	stagedFilesOutput, err := cmd.Output()
	if err == nil {
		stagedFiles := strings.TrimSpace(string(stagedFilesOutput))
		if stagedFiles != "" {
			result.WriteString("\nStaged files:\n" + stagedFiles + "\n")
		}
	}

	return result.String()
}

var nowFunc = time.Now
