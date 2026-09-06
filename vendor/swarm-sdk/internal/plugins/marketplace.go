package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MarketplaceFormat identifies the marketplace index format.
type MarketplaceFormat string

const (
	FormatSwarmOS    MarketplaceFormat = "swarmos"    // SwarmOS marketplace.json
	FormatClaudeCode MarketplaceFormat = "claudecode" // Claude Code .claude-plugin/marketplace.json
	FormatUnknown    MarketplaceFormat = "unknown"
)

// ClaudeMarketplaceIndex represents Claude Code's .claude-plugin/marketplace.json.
type ClaudeMarketplaceIndex struct {
	// Schema URL (optional, for validation)
	Schema string `json:"$schema,omitempty"`

	// Name of the marketplace (optional)
	Name string `json:"name,omitempty"`

	// Description of the marketplace (optional)
	Description string `json:"description,omitempty"`

	// Owner information (optional)
	Owner *struct {
		Name  string `json:"name,omitempty"`
		Email string `json:"email,omitempty"`
	} `json:"owner,omitempty"`

	// Plugins in the marketplace
	Plugins []ClaudePluginEntry `json:"plugins"`
}

// ClaudePluginAuthor represents the author field which can be a string or object.
type ClaudePluginAuthor struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// ClaudePluginSource represents the source field which can be a string or object.
type ClaudePluginSource struct {
	Source string `json:"source,omitempty"` // "url", "git", etc.
	URL    string `json:"url,omitempty"`
}

// ClaudePluginEntry represents a plugin in Claude Code's marketplace format.
type ClaudePluginEntry struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Version      string          `json:"version,omitempty"`
	Source       json.RawMessage `json:"source"` // Git URL, local path, HTTP URL, or object
	Homepage     string          `json:"homepage,omitempty"`
	Repository   string          `json:"repository,omitempty"`
	Tags         []string        `json:"tags,omitempty"`
	Author       json.RawMessage `json:"author,omitempty"` // Can be string or object
	Category     string          `json:"category,omitempty"`
	License      string          `json:"license,omitempty"`
	Dependencies []string        `json:"dependencies,omitempty"`
	Verified     bool            `json:"verified,omitempty"`
	Featured     bool            `json:"featured,omitempty"`
}

// GetAuthorName extracts the author name from the Author field.
func (e ClaudePluginEntry) GetAuthorName() string {
	if len(e.Author) == 0 {
		return ""
	}

	// Try to unmarshal as string first
	var authorStr string
	if err := json.Unmarshal(e.Author, &authorStr); err == nil {
		return authorStr
	}

	// Try to unmarshal as object
	var authorObj ClaudePluginAuthor
	if err := json.Unmarshal(e.Author, &authorObj); err == nil {
		return authorObj.Name
	}

	return ""
}

// GetSource extracts the source URL from the Source field.
func (e ClaudePluginEntry) GetSource() string {
	if len(e.Source) == 0 {
		return ""
	}

	// Try to unmarshal as string first
	var sourceStr string
	if err := json.Unmarshal(e.Source, &sourceStr); err == nil {
		return sourceStr
	}

	// Try to unmarshal as object
	var sourceObj ClaudePluginSource
	if err := json.Unmarshal(e.Source, &sourceObj); err == nil {
		if sourceObj.URL != "" {
			return sourceObj.URL
		}
		return sourceObj.Source
	}

	return ""
}

// MarketplaceSource represents a source for plugin discovery.
type MarketplaceSource struct {
	// Name of the marketplace
	Name string `json:"name"`

	// URL or path to the marketplace index
	URL string `json:"url"`

	// Type: "github", "http", "local", "git"
	Type string `json:"type"`

	// Format of the marketplace index
	Format MarketplaceFormat `json:"format,omitempty"`

	// Enabled status
	Enabled bool `json:"enabled"`

	// Priority for search ordering (higher = first)
	Priority int `json:"priority"`

	// AutoUpdate enables automatic updates
	AutoUpdate bool `json:"auto_update,omitempty"`

	// LastUpdated timestamp
	LastUpdated time.Time `json:"last_updated"`
}

// MarketplaceDatabase manages plugin marketplaces.
type MarketplaceDatabase struct {
	mu sync.RWMutex

	// Sources for marketplace discovery
	sources []MarketplaceSource

	// Cached plugin entries from all sources
	entries map[string][]PluginMarketplaceEntry

	// Cache directory
	cacheDir string

	// HTTP client
	httpClient *http.Client

	// Last update time
	lastUpdate time.Time
}

// PluginMarketplaceEntry is a unified representation for marketplace plugins.
type PluginMarketplaceEntry struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Version      string   `json:"version,omitempty"`
	Source       string   `json:"source"` // Install source (git, URL, path)
	Marketplace  string   `json:"marketplace"`
	Category     string   `json:"category,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Author       string   `json:"author,omitempty"`
	License      string   `json:"license,omitempty"`
	Homepage     string   `json:"homepage,omitempty"`
	Repository   string   `json:"repository,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Downloads    int      `json:"downloads,omitempty"`
	Rating       float64  `json:"rating,omitempty"`
	Verified     bool     `json:"verified,omitempty"`
	Featured     bool     `json:"featured,omitempty"`
}

// NewMarketplaceDatabase creates a new marketplace database.
func NewMarketplaceDatabase(cacheDir string) *MarketplaceDatabase {
	db := &MarketplaceDatabase{
		sources:  []MarketplaceSource{},
		entries:  make(map[string][]PluginMarketplaceEntry),
		cacheDir: cacheDir,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Add official Anthropic Claude plugins repository
	// This is the official, curated list of high-quality Claude Code plugins
	db.sources = append(db.sources, MarketplaceSource{
		Name:       "claude-official",
		URL:        "https://raw.githubusercontent.com/anthropics/claude-plugins-official/main/.claude-plugin/marketplace.json",
		Type:       "http",
		Format:     FormatClaudeCode,
		Enabled:    true,
		Priority:   200, // Highest priority - official source
		AutoUpdate: true,
	})

	// Add GitHub topic-based Claude Code plugin discovery
	// This searches for repositories with the "claude-code-plugin" topic
	db.sources = append(db.sources, MarketplaceSource{
		Name:       "github-claude-plugins",
		URL:        "https://api.github.com/search/repositories?q=topic:claude-code-plugin&sort=stars&order=desc",
		Type:       "github",
		Format:     FormatClaudeCode,
		Enabled:    true,
		Priority:   100,
		AutoUpdate: true,
	})

	// Add GitHub topic-based MCP server discovery
	db.sources = append(db.sources, MarketplaceSource{
		Name:       "github-mcp-servers",
		URL:        "https://api.github.com/search/repositories?q=topic:mcp-server&sort=stars&order=desc",
		Type:       "github",
		Format:     FormatClaudeCode,
		Enabled:    true,
		Priority:   95,
		AutoUpdate: true,
	})

	// Add awesome-mcp-servers curated list
	db.sources = append(db.sources, MarketplaceSource{
		Name:       "awesome-mcp-servers",
		URL:        "https://raw.githubusercontent.com/punkpeye/awesome-mcp-servers/main/README.md",
		Type:       "awesome-list",
		Format:     FormatClaudeCode,
		Enabled:    true,
		Priority:   90,
		AutoUpdate: true,
	})

	// Add SwarmOS official marketplace
	db.sources = append(db.sources, MarketplaceSource{
		Name:       "swarmos-official",
		URL:        "https://raw.githubusercontent.com/shareai-lab/swarmos/main/examples/plugins/marketplace.json",
		Type:       "http",
		Format:     FormatSwarmOS,
		Enabled:    true,
		Priority:   80,
		AutoUpdate: true,
	})

	return db
}

// AddSource adds a marketplace source.
func (db *MarketplaceDatabase) AddSource(source MarketplaceSource) {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Check for duplicate
	for i, s := range db.sources {
		if s.Name == source.Name {
			db.sources[i] = source
			return
		}
	}

	db.sources = append(db.sources, source)
}

// RemoveSource removes a marketplace source.
func (db *MarketplaceDatabase) RemoveSource(name string) {
	db.mu.Lock()
	defer db.mu.Unlock()

	for i, s := range db.sources {
		if s.Name == name {
			db.sources = append(db.sources[:i], db.sources[i+1:]...)
			delete(db.entries, name)
			return
		}
	}
}

// GetSources returns all marketplace sources.
func (db *MarketplaceDatabase) GetSources() []MarketplaceSource {
	db.mu.RLock()
	defer db.mu.RUnlock()

	sources := make([]MarketplaceSource, len(db.sources))
	copy(sources, db.sources)
	return sources
}

// Update fetches latest plugin indexes from all enabled sources.
func (db *MarketplaceDatabase) Update(ctx context.Context) error {
	db.mu.RLock()
	sources := make([]MarketplaceSource, len(db.sources))
	copy(sources, db.sources)
	db.mu.RUnlock()

	for _, source := range sources {
		if !source.Enabled {
			continue
		}

		entries, err := db.fetchSource(ctx, source)
		if err != nil {
			continue // Skip failed sources
		}

		db.mu.Lock()
		db.entries[source.Name] = entries
		db.mu.Unlock()
	}

	db.mu.Lock()
	db.lastUpdate = time.Now()
	db.mu.Unlock()

	// Save cache
	return db.saveCache()
}

// UpdateSource updates a single marketplace source.
func (db *MarketplaceDatabase) UpdateSource(ctx context.Context, name string) error {
	db.mu.RLock()
	var source *MarketplaceSource
	for i := range db.sources {
		if db.sources[i].Name == name {
			source = &db.sources[i]
			break
		}
	}
	db.mu.RUnlock()

	if source == nil {
		return fmt.Errorf("marketplace source %q not found", name)
	}

	entries, err := db.fetchSource(ctx, *source)
	if err != nil {
		return err
	}

	db.mu.Lock()
	db.entries[name] = entries
	db.sources[db.findSourceIndex(name)].LastUpdated = time.Now()
	db.mu.Unlock()

	return db.saveCache()
}

func (db *MarketplaceDatabase) findSourceIndex(name string) int {
	for i, s := range db.sources {
		if s.Name == name {
			return i
		}
	}
	return -1
}

// fetchSource fetches plugins from a single source.
func (db *MarketplaceDatabase) fetchSource(ctx context.Context, source MarketplaceSource) ([]PluginMarketplaceEntry, error) {
	var data []byte
	var err error

	switch source.Type {
	case "http":
		data, err = db.fetchHTTP(ctx, source.URL)
	case "github":
		// GitHub API search returns a special format
		return db.fetchGitHubSearch(ctx, source)
	case "awesome-list":
		// Parse awesome-list markdown format
		return db.fetchAwesomeList(ctx, source)
	case "local":
		data, err = os.ReadFile(source.URL)
	case "git":
		// For git sources, we'd need to clone/pull the repo
		// For now, treat as HTTP if it's a raw URL
		if strings.HasPrefix(source.URL, "http") {
			data, err = db.fetchHTTP(ctx, source.URL)
		} else {
			return nil, fmt.Errorf("git sources not yet implemented")
		}
	default:
		return nil, fmt.Errorf("unknown source type: %s", source.Type)
	}

	if err != nil {
		return nil, err
	}

	// Detect format if not specified
	format := source.Format
	if format == "" || format == FormatUnknown {
		format = DetectMarketplaceFormat(data)
	}

	return db.parseMarketplace(data, format, source.Name, source.URL)
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

// fetchGitHubSearch fetches plugins from GitHub search API.
func (db *MarketplaceDatabase) fetchGitHubSearch(ctx context.Context, source MarketplaceSource) ([]PluginMarketplaceEntry, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", source.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "SwarmOS-Plugin-Manager")

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

	entries := make([]PluginMarketplaceEntry, 0, len(result.Items))
	for _, item := range result.Items {
		license := ""
		if item.License != nil {
			license = item.License.Name
		}

		entry := PluginMarketplaceEntry{
			Name:        item.Name,
			Description: item.Description,
			Source:      item.CloneURL,
			Marketplace: source.Name,
			Tags:        item.Topics,
			Author:      item.Owner.Login,
			License:     license,
			Homepage:    item.HTMLURL,
			Repository:  item.HTMLURL,
			Downloads:   item.Stars, // Use stars as popularity metric
			Featured:    item.Stars > 100,
			Verified:    false, // GitHub repos are not verified by default
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// fetchAwesomeList parses an awesome-list style README.md for MCP servers.
func (db *MarketplaceDatabase) fetchAwesomeList(ctx context.Context, source MarketplaceSource) ([]PluginMarketplaceEntry, error) {
	data, err := db.fetchHTTP(ctx, source.URL)
	if err != nil {
		return nil, err
	}

	content := string(data)
	var entries []PluginMarketplaceEntry

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

		entry := PluginMarketplaceEntry{
			Name:        name,
			Description: description,
			Source:      url + ".git",
			Marketplace: source.Name,
			Homepage:    url,
			Repository:  url,
			Category:    "MCP Server",
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// fetchHTTP fetches data from an HTTP URL.
func (db *MarketplaceDatabase) fetchHTTP(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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

	return io.ReadAll(resp.Body)
}

// parseMarketplace parses marketplace data based on format.
func (db *MarketplaceDatabase) parseMarketplace(data []byte, format MarketplaceFormat, marketplace, sourceURL string) ([]PluginMarketplaceEntry, error) {
	switch format {
	case FormatClaudeCode:
		return db.parseClaudeMarketplace(data, marketplace, sourceURL)
	case FormatSwarmOS:
		return db.parseSwarmOSMarketplace(data, marketplace)
	default:
		// Try Claude format first, then SwarmOS
		entries, err := db.parseClaudeMarketplace(data, marketplace, sourceURL)
		if err == nil && len(entries) > 0 {
			return entries, nil
		}
		return db.parseSwarmOSMarketplace(data, marketplace)
	}
}

// parseClaudeMarketplace parses Claude Code marketplace format.
func (db *MarketplaceDatabase) parseClaudeMarketplace(data []byte, marketplace, sourceURL string) ([]PluginMarketplaceEntry, error) {
	var index ClaudeMarketplaceIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}

	// Determine base repository URL for relative paths
	var baseRepoURL string
	if strings.Contains(sourceURL, "raw.githubusercontent.com") {
		// Extract owner/repo from raw GitHub URL
		// Example: https://raw.githubusercontent.com/anthropics/claude-plugins-official/main/.claude-plugin/marketplace.json
		// Convert to: https://github.com/anthropics/claude-plugins-official
		parts := strings.Split(sourceURL, "/")
		if len(parts) >= 5 {
			owner := parts[3]
			repo := parts[4]
			baseRepoURL = fmt.Sprintf("https://github.com/%s/%s", owner, repo)
		}
	} else if strings.Contains(sourceURL, "github.com") && !strings.Contains(sourceURL, "api.github.com") {
		// Direct GitHub URL
		// Extract up to owner/repo
		parts := strings.Split(sourceURL, "/")
		if len(parts) >= 5 {
			baseRepoURL = strings.Join(parts[:5], "/")
		}
	}

	entries := make([]PluginMarketplaceEntry, len(index.Plugins))
	for i, p := range index.Plugins {
		source := p.GetSource()

		// Resolve relative paths to full Git URLs
		if strings.HasPrefix(source, "./") && baseRepoURL != "" {
			// For official Claude marketplace, plugins are in subdirectories
			// We need to clone the whole repo and point to the subdirectory
			// Git doesn't support subdirectory cloning directly via URL
			// So we'll use the format: repo.git#subdirectory
			subdirectory := strings.TrimPrefix(source, "./")
			source = baseRepoURL + ".git#" + subdirectory
		}

		entries[i] = PluginMarketplaceEntry{
			Name:         p.Name,
			Description:  p.Description,
			Version:      p.Version,
			Source:       source,
			Marketplace:  marketplace,
			Category:     p.Category,
			Tags:         p.Tags,
			Author:       p.GetAuthorName(),
			License:      p.License,
			Homepage:     p.Homepage,
			Repository:   p.Repository,
			Dependencies: p.Dependencies,
			Verified:     p.Verified,
			Featured:     p.Featured,
		}
	}

	return entries, nil
}

// SwarmOSMarketplaceIndex represents SwarmOS marketplace.json format.
type SwarmOSMarketplaceIndex struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Version     string               `json:"version,omitempty"`
	Categories  []string             `json:"categories,omitempty"`
	Plugins     []SwarmOSPluginEntry `json:"plugins"`
}

// SwarmOSPluginEntry represents a plugin in SwarmOS format.
type SwarmOSPluginEntry struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Version      string   `json:"version,omitempty"`
	Category     string   `json:"category"`
	Source       string   `json:"source"`
	Homepage     string   `json:"homepage,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Author       string   `json:"author,omitempty"`
	License      string   `json:"license,omitempty"`
	Repository   string   `json:"repository,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Downloads    int      `json:"downloads,omitempty"`
	Rating       float64  `json:"rating,omitempty"`
	Verified     bool     `json:"verified,omitempty"`
	Featured     bool     `json:"featured,omitempty"`
}

// parseSwarmOSMarketplace parses SwarmOS marketplace format.
func (db *MarketplaceDatabase) parseSwarmOSMarketplace(data []byte, marketplace string) ([]PluginMarketplaceEntry, error) {
	var index SwarmOSMarketplaceIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}

	entries := make([]PluginMarketplaceEntry, len(index.Plugins))
	for i, p := range index.Plugins {
		entries[i] = PluginMarketplaceEntry{
			Name:         p.Name,
			Description:  p.Description,
			Version:      p.Version,
			Source:       p.Source,
			Marketplace:  marketplace,
			Category:     p.Category,
			Tags:         p.Tags,
			Author:       p.Author,
			License:      p.License,
			Homepage:     p.Homepage,
			Repository:   p.Repository,
			Dependencies: p.Dependencies,
			Downloads:    p.Downloads,
			Rating:       p.Rating,
			Verified:     p.Verified,
			Featured:     p.Featured,
		}
	}

	return entries, nil
}

// DetectMarketplaceFormat detects the format of marketplace data.
func DetectMarketplaceFormat(data []byte) MarketplaceFormat {
	// Try parsing as a generic JSON to check structure
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		return FormatUnknown
	}

	// Check for plugins array first
	_, hasPlugins := generic["plugins"]
	if !hasPlugins {
		return FormatUnknown
	}

	// Claude Code format detection:
	// 1. Has $schema field pointing to Claude Code schema
	// 2. Has "owner" field (Claude specific) instead of "categories" (SwarmOS)
	// 3. Can have optional "name" and "description" metadata
	if schema, ok := generic["$schema"].(string); ok {
		if strings.Contains(schema, "claude-code") || strings.Contains(schema, "anthropic.com") {
			return FormatClaudeCode
		}
	}

	// Check for Claude-specific "owner" field
	if _, hasOwner := generic["owner"]; hasOwner {
		return FormatClaudeCode
	}

	// SwarmOS format detection:
	// Has "name", "description", "plugins", and often "categories" or "version"
	if _, hasName := generic["name"]; hasName {
		if _, hasCategories := generic["categories"]; hasCategories {
			return FormatSwarmOS
		}
		if _, hasVersion := generic["version"]; hasVersion {
			return FormatSwarmOS
		}
		// If it has name + plugins but no owner, it's likely SwarmOS
		return FormatSwarmOS
	}

	// Default to Claude Code if it just has plugins
	return FormatClaudeCode
}

// Search searches all marketplace entries.
// If query is empty, returns featured plugins or all plugins sorted by priority.
func (db *MarketplaceDatabase) Search(query string) []PluginMarketplaceEntry {
	db.mu.RLock()
	defer db.mu.RUnlock()

	query = strings.ToLower(query)

	// Empty query - return featured or top plugins for browsing
	if query == "" {
		return db.getDefaultBrowseResults()
	}

	var results []PluginMarketplaceEntry
	seen := make(map[string]bool) // Deduplicate by name

	for _, entries := range db.entries {
		for _, entry := range entries {
			// Skip duplicates - keep first occurrence (highest priority source)
			if seen[entry.Name] {
				continue
			}

			score := db.calculateScore(entry, query)
			if score > 0 {
				results = append(results, entry)
				seen[entry.Name] = true
			}
		}
	}

	// Sort by relevance
	sort.Slice(results, func(i, j int) bool {
		si := db.calculateScore(results[i], query)
		sj := db.calculateScore(results[j], query)
		if si != sj {
			return si > sj
		}
		// Featured first
		if results[i].Featured != results[j].Featured {
			return results[i].Featured
		}
		// Verified next
		if results[i].Verified != results[j].Verified {
			return results[i].Verified
		}
		// Then by downloads
		return results[i].Downloads > results[j].Downloads
	})

	return results
}

// getDefaultBrowseResults returns featured plugins and top plugins for browsing
func (db *MarketplaceDatabase) getDefaultBrowseResults() []PluginMarketplaceEntry {
	// Collect all plugins, deduplicated by name (keeping highest priority)
	pluginMap := make(map[string]PluginMarketplaceEntry)

	// Get sources by priority (highest first)
	sources := make([]string, 0, len(db.sources))
	for _, s := range db.sources {
		if s.Enabled {
			sources = append(sources, s.Name)
		}
	}

	// Sort sources by priority
	sort.Slice(sources, func(i, j int) bool {
		var pi, pj int
		for _, s := range db.sources {
			if s.Name == sources[i] {
				pi = s.Priority
			}
			if s.Name == sources[j] {
				pj = s.Priority
			}
		}
		return pi > pj
	})

	// Add plugins from highest priority sources first
	for _, sourceName := range sources {
		if entries, ok := db.entries[sourceName]; ok {
			for _, entry := range entries {
				// Keep first occurrence (highest priority)
				if _, exists := pluginMap[entry.Name]; !exists {
					pluginMap[entry.Name] = entry
				}
			}
		}
	}

	// Convert to slice
	results := make([]PluginMarketplaceEntry, 0, len(pluginMap))
	for _, entry := range pluginMap {
		results = append(results, entry)
	}

	// Sort: Featured first, then Verified, then by downloads
	sort.Slice(results, func(i, j int) bool {
		// Featured plugins first
		if results[i].Featured != results[j].Featured {
			return results[i].Featured
		}
		// Then verified
		if results[i].Verified != results[j].Verified {
			return results[i].Verified
		}
		// Then by downloads/stars
		if results[i].Downloads != results[j].Downloads {
			return results[i].Downloads > results[j].Downloads
		}
		// Finally alphabetically
		return results[i].Name < results[j].Name
	})

	return results
}

// calculateScore calculates search relevance score.
func (db *MarketplaceDatabase) calculateScore(entry PluginMarketplaceEntry, query string) int {
	score := 0

	// Name match
	if strings.Contains(strings.ToLower(entry.Name), query) {
		score += 10
		if strings.EqualFold(entry.Name, query) {
			score += 20
		}
	}

	// Description match
	if strings.Contains(strings.ToLower(entry.Description), query) {
		score += 5
	}

	// Tag match
	for _, tag := range entry.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			score += 3
		}
	}

	// Category match
	if strings.Contains(strings.ToLower(entry.Category), query) {
		score += 2
	}

	// Bonus for verified/featured
	if score > 0 {
		if entry.Verified {
			score += 2
		}
		if entry.Featured {
			score += 3
		}
	}

	return score
}

// GetByCategory returns plugins in a category, deduplicated.
func (db *MarketplaceDatabase) GetByCategory(category string) []PluginMarketplaceEntry {
	db.mu.RLock()
	defer db.mu.RUnlock()

	pluginMap := make(map[string]PluginMarketplaceEntry)

	// Get sources sorted by priority
	type sourcePriority struct {
		name     string
		priority int
	}
	priorities := make([]sourcePriority, 0, len(db.sources))
	for _, s := range db.sources {
		priorities = append(priorities, sourcePriority{s.Name, s.Priority})
	}
	sort.Slice(priorities, func(i, j int) bool {
		return priorities[i].priority > priorities[j].priority
	})

	// Add matching entries from highest priority sources first
	for _, sp := range priorities {
		if entries, ok := db.entries[sp.name]; ok {
			for _, entry := range entries {
				if strings.EqualFold(entry.Category, category) {
					if _, exists := pluginMap[entry.Name]; !exists {
						pluginMap[entry.Name] = entry
					}
				}
			}
		}
	}

	results := make([]PluginMarketplaceEntry, 0, len(pluginMap))
	for _, entry := range pluginMap {
		results = append(results, entry)
	}
	return results
}

// GetFeatured returns featured plugins, deduplicated.
func (db *MarketplaceDatabase) GetFeatured() []PluginMarketplaceEntry {
	db.mu.RLock()
	defer db.mu.RUnlock()

	pluginMap := make(map[string]PluginMarketplaceEntry)

	// Get sources sorted by priority
	type sourcePriority struct {
		name     string
		priority int
	}
	priorities := make([]sourcePriority, 0, len(db.sources))
	for _, s := range db.sources {
		priorities = append(priorities, sourcePriority{s.Name, s.Priority})
	}
	sort.Slice(priorities, func(i, j int) bool {
		return priorities[i].priority > priorities[j].priority
	})

	// Add featured entries from highest priority sources first
	for _, sp := range priorities {
		if entries, ok := db.entries[sp.name]; ok {
			for _, entry := range entries {
				if entry.Featured {
					if _, exists := pluginMap[entry.Name]; !exists {
						pluginMap[entry.Name] = entry
					}
				}
			}
		}
	}

	results := make([]PluginMarketplaceEntry, 0, len(pluginMap))
	for _, entry := range pluginMap {
		results = append(results, entry)
	}
	return results
}

// GetAll returns all plugins from all marketplaces, deduplicated.
// Plugins from higher priority sources are kept when duplicates exist.
func (db *MarketplaceDatabase) GetAll() []PluginMarketplaceEntry {
	db.mu.RLock()
	defer db.mu.RUnlock()

	// Use a map to deduplicate by name
	pluginMap := make(map[string]PluginMarketplaceEntry)

	// Get sources sorted by priority (highest first)
	type sourcePriority struct {
		name     string
		priority int
	}
	priorities := make([]sourcePriority, 0, len(db.sources))
	for _, s := range db.sources {
		priorities = append(priorities, sourcePriority{s.Name, s.Priority})
	}
	sort.Slice(priorities, func(i, j int) bool {
		return priorities[i].priority > priorities[j].priority
	})

	// Add entries from highest priority sources first
	for _, sp := range priorities {
		if entries, ok := db.entries[sp.name]; ok {
			for _, entry := range entries {
				// Keep first occurrence (highest priority source)
				if _, exists := pluginMap[entry.Name]; !exists {
					pluginMap[entry.Name] = entry
				}
			}
		}
	}

	// Convert map to slice
	results := make([]PluginMarketplaceEntry, 0, len(pluginMap))
	for _, entry := range pluginMap {
		results = append(results, entry)
	}

	return results
}

// Get returns a plugin by name.
func (db *MarketplaceDatabase) Get(name string) *PluginMarketplaceEntry {
	db.mu.RLock()
	defer db.mu.RUnlock()

	for _, entries := range db.entries {
		for _, entry := range entries {
			if strings.EqualFold(entry.Name, name) {
				return &entry
			}
		}
	}
	return nil
}

// GetCategories returns all available categories.
func (db *MarketplaceDatabase) GetCategories() []string {
	db.mu.RLock()
	defer db.mu.RUnlock()

	categories := make(map[string]bool)
	for _, entries := range db.entries {
		for _, entry := range entries {
			if entry.Category != "" {
				categories[entry.Category] = true
			}
		}
	}

	result := make([]string, 0, len(categories))
	for cat := range categories {
		result = append(result, cat)
	}
	sort.Strings(result)

	return result
}

// saveCache saves the marketplace cache to disk.
func (db *MarketplaceDatabase) saveCache() error {
	if db.cacheDir == "" {
		return nil
	}

	if err := os.MkdirAll(db.cacheDir, 0755); err != nil {
		return err
	}

	// Save entries
	entriesPath := filepath.Join(db.cacheDir, "marketplace-entries.json")
	entriesData, err := json.MarshalIndent(db.entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(entriesPath, entriesData, 0644); err != nil {
		return err
	}

	// Save sources
	sourcesPath := filepath.Join(db.cacheDir, "marketplace-sources.json")
	sourcesData, err := json.MarshalIndent(db.sources, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sourcesPath, sourcesData, 0644)
}

// LoadCache loads the marketplace cache from disk.
func (db *MarketplaceDatabase) LoadCache() error {
	if db.cacheDir == "" {
		return nil
	}

	// Load entries
	entriesPath := filepath.Join(db.cacheDir, "marketplace-entries.json")
	if data, err := os.ReadFile(entriesPath); err == nil {
		json.Unmarshal(data, &db.entries)
	}

	// Load sources
	sourcesPath := filepath.Join(db.cacheDir, "marketplace-sources.json")
	if data, err := os.ReadFile(sourcesPath); err == nil {
		var sources []MarketplaceSource
		if err := json.Unmarshal(data, &sources); err == nil {
			// Merge with default sources
			for _, s := range sources {
				db.AddSource(s)
			}
		}
	}

	return nil
}

// AddGitHubMarketplace adds a GitHub-hosted marketplace.
func (db *MarketplaceDatabase) AddGitHubMarketplace(owner, repo string) {
	// Check for Claude Code format first
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/.claude-plugin/marketplace.json", owner, repo)
	name := fmt.Sprintf("%s-%s", owner, repo)

	db.AddSource(MarketplaceSource{
		Name:       name,
		URL:        url,
		Type:       "github",
		Format:     FormatClaudeCode,
		Enabled:    true,
		Priority:   50,
		AutoUpdate: true,
	})
}

// ToPluginSearchResult converts a marketplace entry to a search result.
func (e PluginMarketplaceEntry) ToPluginSearchResult(installed bool) PluginSearchResult {
	return PluginSearchResult{
		Type:        "plugin",
		Name:        e.Name,
		Description: e.Description,
		Version:     e.Version,
		Author:      e.Author,
		Category:    e.Category,
		Keywords:    e.Tags,
		Source:      e.Marketplace,
		Installed:   installed,
		Downloads:   e.Downloads,
		Rating:      e.Rating,
		Verified:    e.Verified,
		Featured:    e.Featured,
	}
}

// ToUnifiedSearchResult converts a marketplace entry to a unified search result.
func (e PluginMarketplaceEntry) ToUnifiedSearchResult(installed bool) UnifiedSearchResult {
	return UnifiedSearchResult{
		ResultType:  "plugin",
		Name:        e.Name,
		Description: e.Description,
		Version:     e.Version,
		Author:      e.Author,
		Category:    e.Category,
		Tags:        e.Tags,
		Source:      e.Marketplace,
		Installed:   installed,
		Downloads:   e.Downloads,
		Rating:      e.Rating,
		Verified:    e.Verified,
		Featured:    e.Featured,
	}
}
