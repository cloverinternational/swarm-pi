package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// PluginDatabase manages plugin/skill discovery and installation.
type PluginDatabase struct {
	mu sync.RWMutex

	// Local index of available plugins
	index *MarketplaceIndex

	// Installed plugins
	installed map[string]*InstalledPlugin

	// Registry sources
	sources []MarketplaceSource

	// Cache directory
	cacheDir string

	// HTTP client for remote requests
	httpClient *http.Client

	// Last update time
	lastUpdate time.Time
}

// MarketplaceIndex is the schema for a plugin marketplace.
type MarketplaceIndex struct {
	// Schema URL for validation
	Schema string `json:"$schema,omitempty"`

	// Name of the marketplace
	Name string `json:"name"`

	// Description of the marketplace
	Description string `json:"description"`

	// Version of the index
	Version string `json:"version,omitempty"`

	// Owner information
	Owner MarketplaceOwner `json:"owner"`

	// Last updated timestamp
	Updated time.Time `json:"updated"`

	// List of plugins
	Plugins []PluginEntry `json:"plugins"`

	// Categories available
	Categories []string `json:"categories,omitempty"`
}

// MarketplaceOwner contains owner information.
type MarketplaceOwner struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// PluginEntry represents a plugin in the marketplace.
type PluginEntry struct {
	// Name is the unique identifier
	Name string `json:"name"`

	// Description of what the plugin does
	Description string `json:"description"`

	// Version string
	Version string `json:"version,omitempty"`

	// Category for organization
	Category string `json:"category"`

	// Source path or URL
	Source string `json:"source"`

	// Homepage URL
	Homepage string `json:"homepage,omitempty"`

	// Tags for discovery
	Tags []string `json:"tags,omitempty"`

	// Author information
	Author string `json:"author,omitempty"`

	// License
	License string `json:"license,omitempty"`

	// Dependencies on other plugins
	Dependencies []string `json:"dependencies,omitempty"`

	// Repository URL
	Repository string `json:"repository,omitempty"`

	// Downloads count
	Downloads int `json:"downloads,omitempty"`

	// Rating score (0-5)
	Rating float64 `json:"rating,omitempty"`

	// Verified by marketplace owner
	Verified bool `json:"verified,omitempty"`

	// Featured plugin
	Featured bool `json:"featured,omitempty"`

	// Minimum compatible version
	MinVersion string `json:"min_version,omitempty"`
}

// MarketplaceSource represents a source for plugin discovery.
type MarketplaceSource struct {
	// Name of the source
	Name string `json:"name"`

	// URL to fetch marketplace.json
	URL string `json:"url"`

	// Type: "http", "github", "local"
	Type string `json:"type"`

	// Enabled status
	Enabled bool `json:"enabled"`

	// Priority for search ordering (higher = first)
	Priority int `json:"priority"`
}

// InstalledPlugin tracks an installed plugin.
type InstalledPlugin struct {
	// Entry from marketplace
	Entry PluginEntry `json:"entry"`

	// InstalledAt timestamp
	InstalledAt time.Time `json:"installed_at"`

	// InstallPath where plugin is installed
	InstallPath string `json:"install_path"`

	// Source from which it was installed
	Source string `json:"source"`

	// Enabled status
	Enabled bool `json:"enabled"`
}

// NewPluginDatabase creates a new plugin database.
func NewPluginDatabase(cacheDir string) *PluginDatabase {
	db := &PluginDatabase{
		index:     &MarketplaceIndex{Plugins: []PluginEntry{}},
		installed: make(map[string]*InstalledPlugin),
		sources:   []MarketplaceSource{},
		cacheDir:  cacheDir,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Add GitHub topic-based Claude Code plugin discovery
	// Skills and plugins often overlap - Claude Code plugins can be used as skills
	db.sources = append(db.sources, MarketplaceSource{
		Name:     "github-claude-plugins",
		URL:      "https://api.github.com/search/repositories?q=topic:claude-code-plugin&sort=stars&order=desc",
		Type:     "github-search",
		Enabled:  true,
		Priority: 100,
	})

	// Add GitHub topic-based skill discovery
	db.sources = append(db.sources, MarketplaceSource{
		Name:     "github-claude-skills",
		URL:      "https://api.github.com/search/repositories?q=topic:claude-code-skill&sort=stars&order=desc",
		Type:     "github-search",
		Enabled:  true,
		Priority: 99,
	})

	// Add GitHub topic-based MCP server discovery (can also be skills)
	db.sources = append(db.sources, MarketplaceSource{
		Name:     "github-mcp-servers",
		URL:      "https://api.github.com/search/repositories?q=topic:mcp-server&sort=stars&order=desc",
		Type:     "github-search",
		Enabled:  true,
		Priority: 95,
	})

	// Add GitHub topic-based slash command discovery
	db.sources = append(db.sources, MarketplaceSource{
		Name:     "github-slash-commands",
		URL:      "https://api.github.com/search/repositories?q=topic:claude-code-slash-command&sort=stars&order=desc",
		Type:     "github-search",
		Enabled:  true,
		Priority: 94,
	})

	// Add awesome-mcp-servers curated list
	db.sources = append(db.sources, MarketplaceSource{
		Name:     "awesome-mcp-servers",
		URL:      "https://raw.githubusercontent.com/punkpeye/awesome-mcp-servers/main/README.md",
		Type:     "awesome-list",
		Enabled:  true,
		Priority: 90,
	})

	// Add SwarmOS official skills marketplace
	db.sources = append(db.sources, MarketplaceSource{
		Name:     "swarmos-official",
		URL:      "https://raw.githubusercontent.com/shareai-lab/swarmos/main/examples/skills/marketplace.json",
		Type:     "http",
		Enabled:  true,
		Priority: 80,
	})

	return db
}

// AddSource adds a marketplace source.
func (db *PluginDatabase) AddSource(source MarketplaceSource) {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Check for duplicate by name
	for i, s := range db.sources {
		if s.Name == source.Name {
			db.sources[i] = source
			return
		}
	}

	db.sources = append(db.sources, source)
}

// RemoveSource removes a marketplace source by name.
func (db *PluginDatabase) RemoveSource(name string) {
	db.mu.Lock()
	defer db.mu.Unlock()

	for i, s := range db.sources {
		if s.Name == name {
			db.sources = append(db.sources[:i], db.sources[i+1:]...)
			return
		}
	}
}

// GetSources returns all marketplace sources.
func (db *PluginDatabase) GetSources() []MarketplaceSource {
	db.mu.RLock()
	defer db.mu.RUnlock()

	sources := make([]MarketplaceSource, len(db.sources))
	copy(sources, db.sources)
	return sources
}

// SetSourceEnabled enables or disables a marketplace source.
func (db *PluginDatabase) SetSourceEnabled(name string, enabled bool) {
	db.mu.Lock()
	defer db.mu.Unlock()

	for i := range db.sources {
		if db.sources[i].Name == name {
			db.sources[i].Enabled = enabled
			return
		}
	}
}

// LoadLocalIndex loads a local marketplace.json file.
func (db *PluginDatabase) LoadLocalIndex(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read index: %w", err)
	}

	var index MarketplaceIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return fmt.Errorf("failed to parse index: %w", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	// Merge plugins
	for _, plugin := range index.Plugins {
		db.index.Plugins = append(db.index.Plugins, plugin)
	}

	return nil
}

// Update fetches latest plugin indexes from all sources.
func (db *PluginDatabase) Update(ctx context.Context) error {
	db.mu.RLock()
	sources := make([]MarketplaceSource, len(db.sources))
	copy(sources, db.sources)
	db.mu.RUnlock()

	var allPlugins []PluginEntry

	for _, source := range sources {
		if !source.Enabled {
			continue
		}

		plugins, err := db.fetchSource(ctx, source)
		if err != nil {
			continue // Skip failed sources
		}

		allPlugins = append(allPlugins, plugins...)
	}

	db.mu.Lock()
	db.index.Plugins = allPlugins
	db.lastUpdate = time.Now()
	db.mu.Unlock()

	// Cache the index
	if db.cacheDir != "" {
		db.saveCache()
	}

	return nil
}

// GetAll returns all plugins in the index.
func (db *PluginDatabase) GetAll() []PluginEntry {
	db.mu.RLock()
	defer db.mu.RUnlock()

	results := make([]PluginEntry, len(db.index.Plugins))
	copy(results, db.index.Plugins)

	// Sort: featured first, then by downloads
	sort.Slice(results, func(i, j int) bool {
		if results[i].Featured != results[j].Featured {
			return results[i].Featured
		}
		return results[i].Downloads > results[j].Downloads
	})

	return results
}

// Search finds plugins matching a query.
func (db *PluginDatabase) Search(query string) []PluginEntry {
	db.mu.RLock()
	defer db.mu.RUnlock()

	query = strings.ToLower(query)
	var results []PluginEntry

	for _, plugin := range db.index.Plugins {
		score := db.calculateScore(plugin, query)
		if score > 0 {
			results = append(results, plugin)
		}
	}

	// Sort by relevance
	sort.Slice(results, func(i, j int) bool {
		si := db.calculateScore(results[i], query)
		sj := db.calculateScore(results[j], query)
		if si != sj {
			return si > sj
		}
		// Featured plugins first
		if results[i].Featured != results[j].Featured {
			return results[i].Featured
		}
		// Then by downloads
		return results[i].Downloads > results[j].Downloads
	})

	return results
}

// SearchByCategory finds plugins in a category.
func (db *PluginDatabase) SearchByCategory(category string) []PluginEntry {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var results []PluginEntry
	for _, plugin := range db.index.Plugins {
		if strings.EqualFold(plugin.Category, category) {
			results = append(results, plugin)
		}
	}
	return results
}

// SearchByTag finds plugins with a specific tag.
func (db *PluginDatabase) SearchByTag(tag string) []PluginEntry {
	db.mu.RLock()
	defer db.mu.RUnlock()

	tag = strings.ToLower(tag)
	var results []PluginEntry
	for _, plugin := range db.index.Plugins {
		for _, t := range plugin.Tags {
			if strings.EqualFold(t, tag) {
				results = append(results, plugin)
				break
			}
		}
	}
	return results
}

// GetCategories returns all available categories.
func (db *PluginDatabase) GetCategories() []string {
	db.mu.RLock()
	defer db.mu.RUnlock()

	categorySet := make(map[string]bool)
	for _, plugin := range db.index.Plugins {
		if plugin.Category != "" {
			categorySet[plugin.Category] = true
		}
	}

	categories := make([]string, 0, len(categorySet))
	for cat := range categorySet {
		categories = append(categories, cat)
	}
	sort.Strings(categories)

	return categories
}

// GetFeatured returns featured plugins.
func (db *PluginDatabase) GetFeatured() []PluginEntry {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var results []PluginEntry
	for _, plugin := range db.index.Plugins {
		if plugin.Featured {
			results = append(results, plugin)
		}
	}
	return results
}

// Install installs a plugin by name.
func (db *PluginDatabase) Install(ctx context.Context, name string, installDir string) (*InstalledPlugin, error) {
	// Find plugin
	db.mu.RLock()
	var entry *PluginEntry
	for i := range db.index.Plugins {
		if strings.EqualFold(db.index.Plugins[i].Name, name) {
			entry = &db.index.Plugins[i]
			break
		}
	}
	db.mu.RUnlock()

	if entry == nil {
		return nil, fmt.Errorf("plugin %q not found", name)
	}

	// Install dependencies first
	for _, dep := range entry.Dependencies {
		if !db.IsInstalled(dep) {
			if _, err := db.Install(ctx, dep, installDir); err != nil {
				return nil, fmt.Errorf("failed to install dependency %q: %w", dep, err)
			}
		}
	}

	// Create install path
	pluginDir := filepath.Join(installDir, entry.Name)
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plugin directory: %w", err)
	}

	// Download/copy plugin files
	if err := db.downloadPlugin(ctx, entry, pluginDir); err != nil {
		return nil, fmt.Errorf("failed to download plugin: %w", err)
	}

	installed := &InstalledPlugin{
		Entry:       *entry,
		InstalledAt: time.Now(),
		InstallPath: pluginDir,
		Source:      "marketplace",
		Enabled:     true,
	}

	db.mu.Lock()
	db.installed[entry.Name] = installed
	db.mu.Unlock()

	// Save installed state
	db.saveInstalled()

	return installed, nil
}

// Uninstall removes a plugin.
func (db *PluginDatabase) Uninstall(name string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	installed, ok := db.installed[name]
	if !ok {
		return fmt.Errorf("plugin %q not installed", name)
	}

	// Remove files
	if err := os.RemoveAll(installed.InstallPath); err != nil {
		return fmt.Errorf("failed to remove plugin files: %w", err)
	}

	delete(db.installed, name)

	return nil
}

// IsInstalled checks if a plugin is installed.
func (db *PluginDatabase) IsInstalled(name string) bool {
	db.mu.RLock()
	defer db.mu.RUnlock()
	_, ok := db.installed[name]
	return ok
}

// GetInstalled returns all installed plugins.
func (db *PluginDatabase) GetInstalled() []*InstalledPlugin {
	db.mu.RLock()
	defer db.mu.RUnlock()

	plugins := make([]*InstalledPlugin, 0, len(db.installed))
	for _, p := range db.installed {
		plugins = append(plugins, p)
	}
	return plugins
}

// Enable enables an installed plugin.
func (db *PluginDatabase) Enable(name string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if p, ok := db.installed[name]; ok {
		p.Enabled = true
		return nil
	}
	return fmt.Errorf("plugin %q not installed", name)
}

// Disable disables an installed plugin.
func (db *PluginDatabase) Disable(name string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if p, ok := db.installed[name]; ok {
		p.Enabled = false
		return nil
	}
	return fmt.Errorf("plugin %q not installed", name)
}

func (db *PluginDatabase) fetchSource(ctx context.Context, source MarketplaceSource) ([]PluginEntry, error) {
	switch source.Type {
	case "http", "github":
		return db.fetchHTTPSource(ctx, source)
	case "github-search":
		return db.fetchGitHubSearch(ctx, source)
	case "awesome-list":
		return db.fetchAwesomeList(ctx, source)
	case "local":
		return db.fetchLocalSource(source)
	default:
		return nil, fmt.Errorf("unknown source type: %s", source.Type)
	}
}

func (db *PluginDatabase) fetchHTTPSource(ctx context.Context, source MarketplaceSource) ([]PluginEntry, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", source.URL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := db.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var index MarketplaceIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}

	return index.Plugins, nil
}

func (db *PluginDatabase) fetchLocalSource(source MarketplaceSource) ([]PluginEntry, error) {
	data, err := os.ReadFile(source.URL)
	if err != nil {
		return nil, err
	}

	var index MarketplaceIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}

	return index.Plugins, nil
}

// GitHubSearchResult represents GitHub API search results.
type GitHubSearchResult struct {
	TotalCount int `json:"total_count"`
	Items      []struct {
		Name        string   `json:"name"`
		FullName    string   `json:"full_name"`
		Description string   `json:"description"`
		HTMLURL     string   `json:"html_url"`
		CloneURL    string   `json:"clone_url"`
		Stars       int      `json:"stargazers_count"`
		Topics      []string `json:"topics"`
		Language    string   `json:"language"`
		License     *struct {
			Name string `json:"name"`
		} `json:"license"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		UpdatedAt string `json:"updated_at"`
	} `json:"items"`
}

// fetchGitHubSearch fetches skills from GitHub search API.
func (db *PluginDatabase) fetchGitHubSearch(ctx context.Context, source MarketplaceSource) ([]PluginEntry, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", source.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "SwarmOS-Skill-Manager")

	resp, err := db.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var result GitHubSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	entries := make([]PluginEntry, 0, len(result.Items))
	for _, item := range result.Items {
		license := ""
		if item.License != nil {
			license = item.License.Name
		}

		// Determine category from topics
		category := "MCP Server"
		if slices.Contains(item.Topics, "claude-code-skill") {
			category = "Skill"
		}

		entry := PluginEntry{
			Name:        item.Name,
			Description: item.Description,
			Source:      item.CloneURL,
			Category:    category,
			Tags:        item.Topics,
			Author:      item.Owner.Login,
			License:     license,
			Homepage:    item.HTMLURL,
			Repository:  item.HTMLURL,
			Downloads:   item.Stars, // Use stars as popularity metric
			Featured:    item.Stars > 100,
			Verified:    false,
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// fetchAwesomeList parses an awesome-list style README.md for MCP servers/skills.
func (db *PluginDatabase) fetchAwesomeList(ctx context.Context, source MarketplaceSource) ([]PluginEntry, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", source.URL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := db.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	content := string(data)
	var entries []PluginEntry

	// Parse markdown links in format: - [Name](URL) - Description
	lines := strings.SplitSeq(content, "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- [") && !strings.HasPrefix(line, "* [") {
			continue
		}

		// Extract name and URL
		nameStart := strings.Index(line, "[")
		nameEnd := strings.Index(line, "]")
		if nameStart == -1 || nameEnd == -1 || nameEnd <= nameStart {
			continue
		}
		name := line[nameStart+1 : nameEnd]

		urlStart := strings.Index(line, "(")
		urlEnd := strings.Index(line, ")")
		if urlStart == -1 || urlEnd == -1 || urlEnd <= urlStart {
			continue
		}
		url := line[urlStart+1 : urlEnd]

		// Extract description (after the closing parenthesis)
		description := ""
		if urlEnd < len(line)-1 {
			rest := strings.TrimSpace(line[urlEnd+1:])
			if strings.HasPrefix(rest, "-") || strings.HasPrefix(rest, ":") {
				description = strings.TrimSpace(rest[1:])
			}
		}

		// Only include GitHub repos
		if !strings.Contains(url, "github.com") {
			continue
		}

		entry := PluginEntry{
			Name:        name,
			Description: description,
			Source:      url + ".git",
			Category:    "MCP Server",
			Homepage:    url,
			Repository:  url,
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

func (db *PluginDatabase) downloadPlugin(ctx context.Context, entry *PluginEntry, destDir string) error {
	// For now, just create a basic skill structure
	// In a full implementation, this would:
	// 1. Clone from git if repository is provided
	// 2. Download archive from source URL
	// 3. Extract and validate

	// Create SKILL.md
	skillMD := fmt.Sprintf(`---
name: %s
description: %s
version: %s
author: %s
category: %s
tags: %v
---

# %s

%s
`, entry.Name, entry.Description, entry.Version, entry.Author, entry.Category, entry.Tags, entry.Name, entry.Description)

	skillPath := filepath.Join(destDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(skillMD), 0644); err != nil {
		return err
	}

	// Create directories for skill resources
	for _, subdir := range []string{"scripts", "references", "assets"} {
		if err := os.MkdirAll(filepath.Join(destDir, subdir), 0755); err != nil {
			return fmt.Errorf("failed to create %s directory: %w", subdir, err)
		}
	}

	return nil
}

func (db *PluginDatabase) calculateScore(plugin PluginEntry, query string) int {
	score := 0

	// Name match
	if strings.Contains(strings.ToLower(plugin.Name), query) {
		score += 10
		if strings.EqualFold(plugin.Name, query) {
			score += 20
		}
	}

	// Description match
	if strings.Contains(strings.ToLower(plugin.Description), query) {
		score += 5
	}

	// Tag match
	for _, tag := range plugin.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			score += 3
		}
	}

	// Category match
	if strings.Contains(strings.ToLower(plugin.Category), query) {
		score += 2
	}

	// Bonus for verified/featured
	if score > 0 {
		if plugin.Verified {
			score += 2
		}
		if plugin.Featured {
			score += 3
		}
	}

	return score
}

func (db *PluginDatabase) saveCache() error {
	if db.cacheDir == "" {
		return nil
	}

	cachePath := filepath.Join(db.cacheDir, "marketplace-cache.json")
	data, err := json.MarshalIndent(db.index, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(cachePath, data, 0644)
}

func (db *PluginDatabase) saveInstalled() error {
	if db.cacheDir == "" {
		return nil
	}

	installedPath := filepath.Join(db.cacheDir, "installed.json")
	data, err := json.MarshalIndent(db.installed, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(installedPath, data, 0644)
}

// LoadInstalled loads the installed plugins state from disk.
func (db *PluginDatabase) LoadInstalled() error {
	if db.cacheDir == "" {
		return nil
	}

	installedPath := filepath.Join(db.cacheDir, "installed.json")
	data, err := os.ReadFile(installedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	return json.Unmarshal(data, &db.installed)
}
