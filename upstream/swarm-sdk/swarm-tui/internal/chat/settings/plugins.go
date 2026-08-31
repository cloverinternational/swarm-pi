package settings

import (
	"context"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/plugins"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/safego"
)

// PluginsSettings manages the plugins configuration UI
type PluginsSettings struct {
	loader        *plugins.Loader
	marketplace   *plugins.MarketplaceDatabase
	searcher      *plugins.UnifiedSearcher
	searchResults []plugins.UnifiedSearchResult
	errorMessage  string
	successMsg    string

	// View state
	viewMode           string // "installed", "available", or "sources"
	marketplaceEntries []plugins.PluginMarketplaceEntry
	marketplaceLoaded  bool
	marketplaceLoading bool
	marketplaceSources []plugins.MarketplaceSource

	// Add source input state
	addSourceState string // "", "name", "url", "type"
	newSourceName  string
	newSourceURL   string
	newSourceType  string

	// Callbacks
	onPluginToggle    func(name string, enabled bool) error
	onPluginInstall   func(name string) error
	onPluginUninstall func(name string) error
}

// NewPluginsSettings creates a new plugins settings component
func NewPluginsSettings() *PluginsSettings {
	return &PluginsSettings{
		searchResults:      []plugins.UnifiedSearchResult{},
		viewMode:           "installed",
		marketplaceEntries: []plugins.PluginMarketplaceEntry{},
	}
}

// SetLoader sets the plugin loader
func (p *PluginsSettings) SetLoader(loader *plugins.Loader) {
	p.loader = loader
}

// SetMarketplace sets the marketplace database
func (p *PluginsSettings) SetMarketplace(marketplace *plugins.MarketplaceDatabase) {
	p.marketplace = marketplace
}

// SetSearcher sets the unified searcher
func (p *PluginsSettings) SetSearcher(searcher *plugins.UnifiedSearcher) {
	p.searcher = searcher
}

// LoadMarketplace loads marketplace entries in the background
func (p *PluginsSettings) LoadMarketplace() {
	logDebug("[PluginsSettings] LoadMarketplace called, marketplace=%v, loading=%v, loaded=%v", p.marketplace != nil, p.marketplaceLoading, p.marketplaceLoaded)

	if p.marketplace == nil {
		logDebug("[PluginsSettings] LoadMarketplace: marketplace is nil")
		return
	}
	if p.marketplaceLoading {
		logDebug("[PluginsSettings] LoadMarketplace: already loading")
		return
	}
	if p.marketplaceLoaded {
		logDebug("[PluginsSettings] LoadMarketplace: already loaded with %d entries", len(p.marketplaceEntries))
		return
	}

	p.marketplaceLoading = true
	logDebug("[PluginsSettings] LoadMarketplace: starting background load")

	safego.Go("settings.plugins.loadMarketplace", func() {
		// Update marketplace (fetch from sources)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		logDebug("[PluginsSettings] LoadMarketplace: calling marketplace.Update")
		if err := p.marketplace.Update(ctx); err != nil {
			logDebug("[PluginsSettings] LoadMarketplace: Update failed: %v", err)
			p.errorMessage = "Failed to load marketplace: " + err.Error()
		} else {
			logDebug("[PluginsSettings] LoadMarketplace: Update succeeded")
		}

		// Get all entries
		p.marketplaceEntries = p.marketplace.GetAll()
		logDebug("[PluginsSettings] LoadMarketplace: got %d entries", len(p.marketplaceEntries))
		p.marketplaceLoaded = true
		p.marketplaceLoading = false
	})
}

// GetMarketplaceEntries returns cached marketplace entries
func (p *PluginsSettings) GetMarketplaceEntries() []plugins.PluginMarketplaceEntry {
	return p.marketplaceEntries
}

// LoadSources loads marketplace sources from the database
func (p *PluginsSettings) LoadSources() {
	if p.marketplace == nil {
		return
	}
	p.marketplaceSources = p.marketplace.GetSources()
	logDebug("[PluginsSettings] LoadSources: got %d sources", len(p.marketplaceSources))
}

// GetSources returns cached marketplace sources
func (p *PluginsSettings) GetSources() []plugins.MarketplaceSource {
	return p.marketplaceSources
}

// AddSource adds a new marketplace source
func (p *PluginsSettings) AddSource(name, url, sourceType string) error {
	if p.marketplace == nil {
		return fmt.Errorf("marketplace not initialized")
	}

	source := plugins.MarketplaceSource{
		Name:       name,
		URL:        url,
		Type:       sourceType,
		Enabled:    true,
		Priority:   50, // Default priority
		AutoUpdate: true,
	}

	p.marketplace.AddSource(source)
	p.LoadSources()
	p.successMsg = fmt.Sprintf("Added source: %s", name)
	return nil
}

// RemoveSource removes a marketplace source
func (p *PluginsSettings) RemoveSource(name string) error {
	if p.marketplace == nil {
		return fmt.Errorf("marketplace not initialized")
	}

	p.marketplace.RemoveSource(name)
	p.LoadSources()
	p.successMsg = fmt.Sprintf("Removed source: %s", name)
	return nil
}

// ToggleSource toggles a marketplace source enabled state
func (p *PluginsSettings) ToggleSource(name string) error {
	if p.marketplace == nil {
		return fmt.Errorf("marketplace not initialized")
	}

	// Find current state
	sources := p.marketplace.GetSources()
	for _, src := range sources {
		if src.Name == name {
			// Toggle by removing and re-adding with opposite enabled state
			p.marketplace.RemoveSource(name)
			src.Enabled = !src.Enabled
			p.marketplace.AddSource(src)
			p.LoadSources()
			if src.Enabled {
				p.successMsg = fmt.Sprintf("Enabled source: %s", name)
			} else {
				p.successMsg = fmt.Sprintf("Disabled source: %s", name)
			}
			return nil
		}
	}

	return fmt.Errorf("source not found: %s", name)
}

// SetCallbacks sets the action callbacks
func (p *PluginsSettings) SetCallbacks(
	toggle func(name string, enabled bool) error,
	install func(name string) error,
	uninstall func(name string) error,
) {
	p.onPluginToggle = toggle
	p.onPluginInstall = install
	p.onPluginUninstall = uninstall
}

// GetPlugins returns all loaded plugins
func (p *PluginsSettings) GetPlugins() []*plugins.Plugin {
	if p.loader == nil {
		return nil
	}
	return p.loader.List()
}

// GetEnabledPlugins returns currently enabled plugins
func (p *PluginsSettings) GetEnabledPlugins() []*plugins.Plugin {
	if p.loader == nil {
		return nil
	}
	return p.loader.GetEnabled()
}

// getPluginSource determines the source badge for a plugin
func (p *PluginsSettings) getPluginSource(plugin *plugins.Plugin) string {
	switch plugin.Source {
	case plugins.SourceUser:
		return "USR"
	case plugins.SourceProject:
		return "PRJ"
	case plugins.SourceMarketplace:
		return "MKT"
	case plugins.SourceGit:
		return "GIT"
	default:
		return "LOC"
	}
}

// Render renders the plugins settings UI
func (p *PluginsSettings) HandleKey(key string, state *State) bool {
	switch state.PluginsState {
	case "list":
		return p.handleListKey(key, state)
	case "detail":
		return p.handleDetailKey(key, state)
	case "search":
		return p.handleSearchKey(key, state)
	case "marketplace":
		return p.handleMarketplaceKey(key, state)
	case "add_source":
		return p.handleAddSourceKey(key, state)
	}
	return false
}

// handleListKey handles keyboard input in list view
func (p *PluginsSettings) handleListKey(key string, state *State) bool {
	logDebug("[PluginsSettings] handleListKey: key=%q, viewMode=%q, loader=%v", key, p.viewMode, p.loader != nil)
	p.errorMessage = ""
	p.successMsg = ""

	// Determine current list length based on view mode
	var listLen int
	switch p.viewMode {
	case "available":
		listLen = len(p.marketplaceEntries)
	case "sources":
		listLen = len(p.marketplaceSources)
	default:
		listLen = len(p.GetPlugins())
	}
	logDebug("[PluginsSettings] handleListKey: listLen=%d", listLen)

	switch key {
	case "1":
		// Switch to installed view
		logDebug("[PluginsSettings] Switching to installed view")
		if p.viewMode != "installed" {
			p.viewMode = "installed"
			state.PluginsSelected = 0
			state.PluginsScrollOffset = 0
		}
		return true

	case "2":
		// Switch to available/marketplace view
		logDebug("[PluginsSettings] Switching to available view, marketplaceLoaded=%v, marketplaceLoading=%v", p.marketplaceLoaded, p.marketplaceLoading)
		if p.viewMode != "available" {
			p.viewMode = "available"
			// Load marketplace if not already loaded
			if !p.marketplaceLoaded && !p.marketplaceLoading {
				logDebug("[PluginsSettings] Calling LoadMarketplace")
				p.LoadMarketplace()
			}
			state.PluginsSelected = 0
			state.PluginsScrollOffset = 0
		}
		return true

	case "3":
		// Switch to sources view
		logDebug("[PluginsSettings] Switching to sources view")
		if p.viewMode != "sources" {
			p.viewMode = "sources"
			// Load sources
			p.LoadSources()
			state.PluginsSelected = 0
			state.PluginsScrollOffset = 0
		}
		return true

	case "up", "k":
		if state.PluginsSelected > 0 {
			state.PluginsSelected--
			if state.PluginsSelected < state.PluginsScrollOffset {
				state.PluginsScrollOffset = state.PluginsSelected
			}
		}
		return true

	case "down", "j":
		if state.PluginsSelected < listLen-1 {
			state.PluginsSelected++
			if state.PluginsSelected >= state.PluginsScrollOffset+10 {
				state.PluginsScrollOffset = state.PluginsSelected - 9
			}
		}
		return true

	case "space", "enter":
		if p.viewMode == "available" {
			// Install from marketplace
			if listLen > 0 && state.PluginsSelected < listLen {
				entry := p.marketplaceEntries[state.PluginsSelected]
				// Check if already installed
				if p.loader != nil && p.loader.Get(entry.Name) != nil {
					p.errorMessage = "Already installed: " + entry.Name
				} else if p.onPluginInstall != nil {
					if err := p.onPluginInstall(entry.Name); err != nil {
						p.errorMessage = "Install failed: " + err.Error()
					} else {
						p.successMsg = "Installed: " + entry.Name
					}
				} else {
					p.errorMessage = "Install not available"
				}
			}
		} else if p.viewMode == "sources" {
			// Toggle source enabled/disabled
			if listLen > 0 && state.PluginsSelected < listLen {
				source := p.marketplaceSources[state.PluginsSelected]
				if err := p.ToggleSource(source.Name); err != nil {
					p.errorMessage = err.Error()
				}
			}
		} else {
			// Toggle enabled/disabled for installed plugins
			allPlugins := p.GetPlugins()
			if len(allPlugins) > 0 && state.PluginsSelected < len(allPlugins) {
				plugin := allPlugins[state.PluginsSelected]
				if p.loader != nil {
					if plugin.Enabled {
						if err := p.loader.Disable(plugin.Manifest.Name); err != nil {
							p.errorMessage = "Failed to disable: " + err.Error()
						} else {
							p.successMsg = "Disabled: " + plugin.Manifest.Name
						}
					} else {
						if err := p.loader.Enable(plugin.Manifest.Name); err != nil {
							p.errorMessage = "Failed to enable: " + err.Error()
						} else {
							p.successMsg = "Enabled: " + plugin.Manifest.Name
						}
					}
				}
				if p.onPluginToggle != nil {
					p.onPluginToggle(plugin.Manifest.Name, !plugin.Enabled)
				}
			}
		}
		return true

	case "d":
		if p.viewMode == "installed" {
			allPlugins := p.GetPlugins()
			if len(allPlugins) > 0 && state.PluginsSelected < len(allPlugins) {
				state.PluginsState = "detail"
				state.PluginsDetailTab = 0
			}
		} else if p.viewMode == "sources" {
			// Delete source
			if listLen > 0 && state.PluginsSelected < listLen {
				source := p.marketplaceSources[state.PluginsSelected]
				if err := p.RemoveSource(source.Name); err != nil {
					p.errorMessage = err.Error()
				}
				if state.PluginsSelected >= len(p.marketplaceSources) && state.PluginsSelected > 0 {
					state.PluginsSelected--
				}
			}
		}
		return true

	case "a":
		if p.viewMode == "sources" {
			// Enter add source mode
			state.PluginsState = "add_source"
			p.addSourceState = "name"
			p.newSourceName = ""
			p.newSourceURL = ""
			p.newSourceType = "http"
		}
		return true

	case "/":
		state.PluginsState = "search"
		state.PluginsSearchQuery = ""
		state.PluginsSearchSelected = 0
		state.PluginsSearchOffset = 0
		state.PluginsCursorPos = 0
		p.searchResults = []plugins.UnifiedSearchResult{}
		return true

	case "m":
		state.PluginsState = "marketplace"
		state.PluginsMarketplaceSelected = 0
		return true

	case "u":
		if p.viewMode == "installed" {
			allPlugins := p.GetPlugins()
			if len(allPlugins) > 0 && state.PluginsSelected < len(allPlugins) {
				plugin := allPlugins[state.PluginsSelected]
				if p.loader != nil {
					if err := p.loader.Uninstall(plugin.Manifest.Name); err != nil {
						p.errorMessage = "Failed to uninstall: " + err.Error()
					} else {
						p.successMsg = "Uninstalled: " + plugin.Manifest.Name
						if state.PluginsSelected >= len(p.GetPlugins()) && state.PluginsSelected > 0 {
							state.PluginsSelected--
						}
					}
				}
				if p.onPluginUninstall != nil {
					p.onPluginUninstall(plugin.Manifest.Name)
				}
			}
		}
		return true

	case "r", "R":
		if p.viewMode == "available" {
			// Refresh marketplace
			p.marketplaceLoaded = false
			p.marketplaceLoading = false
			p.LoadMarketplace()
			p.successMsg = "Refreshing marketplace..."
		} else if p.viewMode == "sources" {
			// Refresh sources and marketplace
			p.LoadSources()
			p.marketplaceLoaded = false
			p.marketplaceLoading = false
			p.LoadMarketplace()
			p.successMsg = "Refreshing sources and marketplace..."
		} else if p.loader != nil {
			if err := p.loader.Refresh(context.Background()); err != nil {
				p.errorMessage = "Failed to refresh: " + err.Error()
			} else {
				p.successMsg = "Plugins refreshed"
			}
		}
		return true
	}

	return false
}

// handleDetailKey handles keyboard input in detail view
func (p *PluginsSettings) handleDetailKey(key string, state *State) bool {
	p.errorMessage = ""
	p.successMsg = ""

	switch key {
	case "esc", "backspace", "q":
		state.PluginsState = "list"
		return true

	case "tab":
		state.PluginsDetailTab = (state.PluginsDetailTab + 1) % 4
		return true

	case "shift+tab":
		state.PluginsDetailTab = (state.PluginsDetailTab + 3) % 4
		return true

	case "space", "enter":
		allPlugins := p.GetPlugins()
		if len(allPlugins) > 0 && state.PluginsSelected < len(allPlugins) {
			plugin := allPlugins[state.PluginsSelected]
			if p.loader != nil {
				if plugin.Enabled {
					if err := p.loader.Disable(plugin.Manifest.Name); err != nil {
						p.errorMessage = "Failed to disable: " + err.Error()
					} else {
						p.successMsg = "Disabled: " + plugin.Manifest.Name
					}
				} else {
					if err := p.loader.Enable(plugin.Manifest.Name); err != nil {
						p.errorMessage = "Failed to enable: " + err.Error()
					} else {
						p.successMsg = "Enabled: " + plugin.Manifest.Name
					}
				}
			}
		}
		return true
	}

	return false
}

// handleSearchKey handles keyboard input in search view
func (p *PluginsSettings) handleSearchKey(key string, state *State) bool {
	p.errorMessage = ""
	p.successMsg = ""

	switch key {
	case "esc":
		state.PluginsState = "list"
		return true

	case "up", "ctrl+p":
		if state.PluginsSearchSelected > 0 {
			state.PluginsSearchSelected--
			if state.PluginsSearchSelected < state.PluginsSearchOffset {
				state.PluginsSearchOffset = state.PluginsSearchSelected
			}
		}
		return true

	case "down", "ctrl+n":
		if state.PluginsSearchSelected < len(p.searchResults)-1 {
			state.PluginsSearchSelected++
			if state.PluginsSearchSelected >= state.PluginsSearchOffset+8 {
				state.PluginsSearchOffset = state.PluginsSearchSelected - 7
			}
		}
		return true

	case "enter":
		// Perform search or install selected
		if len(p.searchResults) > 0 && state.PluginsSearchSelected < len(p.searchResults) {
			result := p.searchResults[state.PluginsSearchSelected]
			if result.Installed {
				p.errorMessage = "Already installed"
			} else if p.onPluginInstall != nil {
				if err := p.onPluginInstall(result.Name); err != nil {
					p.errorMessage = "Install failed: " + err.Error()
				} else {
					p.successMsg = "Installed: " + result.Name
					p.searchResults[state.PluginsSearchSelected].Installed = true
				}
			}
		} else if state.PluginsSearchQuery != "" && p.marketplace != nil {
			// Perform search
			results := p.marketplace.Search(state.PluginsSearchQuery)
			p.searchResults = make([]plugins.UnifiedSearchResult, len(results))
			for i, r := range results {
				installed := false
				if p.loader != nil {
					installed = p.loader.Get(r.Name) != nil
				}
				p.searchResults[i] = r.ToUnifiedSearchResult(installed)
			}
			state.PluginsSearchSelected = 0
			state.PluginsSearchOffset = 0
		}
		return true

	case "backspace":
		if state.PluginsCursorPos > 0 {
			state.PluginsSearchQuery = state.PluginsSearchQuery[:state.PluginsCursorPos-1] + state.PluginsSearchQuery[state.PluginsCursorPos:]
			state.PluginsCursorPos--
		}
		return true

	case "delete":
		if state.PluginsCursorPos < len(state.PluginsSearchQuery) {
			state.PluginsSearchQuery = state.PluginsSearchQuery[:state.PluginsCursorPos] + state.PluginsSearchQuery[state.PluginsCursorPos+1:]
		}
		return true

	case "left":
		if state.PluginsCursorPos > 0 {
			state.PluginsCursorPos--
		}
		return true

	case "right":
		if state.PluginsCursorPos < len(state.PluginsSearchQuery) {
			state.PluginsCursorPos++
		}
		return true

	case "home", "ctrl+a":
		state.PluginsCursorPos = 0
		return true

	case "end", "ctrl+e":
		state.PluginsCursorPos = len(state.PluginsSearchQuery)
		return true

	case "space":
		state.PluginsSearchQuery = state.PluginsSearchQuery[:state.PluginsCursorPos] + " " + state.PluginsSearchQuery[state.PluginsCursorPos:]
		state.PluginsCursorPos++
		return true

	default:
		// Regular character input - accept all printable ASCII characters
		if len(key) == 1 && key[0] >= 33 && key[0] <= 126 {
			state.PluginsSearchQuery = state.PluginsSearchQuery[:state.PluginsCursorPos] + key + state.PluginsSearchQuery[state.PluginsCursorPos:]
			state.PluginsCursorPos++
			return true
		}
	}

	// Consume all keys when in search mode to prevent parent handlers from processing them
	return true
}

// handleMarketplaceKey handles keyboard input in marketplace view
func (p *PluginsSettings) handleMarketplaceKey(key string, state *State) bool {
	p.errorMessage = ""
	p.successMsg = ""

	switch key {
	case "esc", "backspace", "q":
		state.PluginsState = "list"
		return true

	case "up", "k":
		if state.PluginsMarketplaceSelected > 0 {
			state.PluginsMarketplaceSelected--
		}
		return true

	case "down", "j":
		if p.marketplace != nil {
			sources := p.marketplace.GetSources()
			if state.PluginsMarketplaceSelected < len(sources)-1 {
				state.PluginsMarketplaceSelected++
			}
		}
		return true

	case "u":
		// Update selected marketplace
		if p.marketplace != nil {
			sources := p.marketplace.GetSources()
			if state.PluginsMarketplaceSelected < len(sources) {
				source := sources[state.PluginsMarketplaceSelected]
				if err := p.marketplace.UpdateSource(context.Background(), source.Name); err != nil {
					p.errorMessage = "Update failed: " + err.Error()
				} else {
					p.successMsg = "Updated: " + source.Name
				}
			}
		}
		return true
	}

	return false
}

// handleAddSourceKey handles keyboard input in add source form
func (p *PluginsSettings) handleAddSourceKey(key string, state *State) bool {
	p.errorMessage = ""

	switch key {
	case "esc":
		// Cancel and return to sources view
		state.PluginsState = "list"
		p.addSourceState = ""
		p.newSourceName = ""
		p.newSourceURL = ""
		p.newSourceType = "http"
		return true

	case "tab":
		// Move to next field
		switch p.addSourceState {
		case "name":
			p.addSourceState = "url"
		case "url":
			p.addSourceState = "type"
		case "type":
			p.addSourceState = "name"
		}
		return true

	case "shift+tab":
		// Move to previous field
		switch p.addSourceState {
		case "name":
			p.addSourceState = "type"
		case "url":
			p.addSourceState = "name"
		case "type":
			p.addSourceState = "url"
		}
		return true

	case "enter":
		// Validate and save
		if p.newSourceName == "" {
			p.errorMessage = "Name is required"
			p.addSourceState = "name"
			return true
		}
		if p.newSourceURL == "" {
			p.errorMessage = "URL is required"
			p.addSourceState = "url"
			return true
		}

		// Add the source
		if err := p.AddSource(p.newSourceName, p.newSourceURL, p.newSourceType); err != nil {
			p.errorMessage = err.Error()
		} else {
			// Success - return to sources view
			state.PluginsState = "list"
			p.addSourceState = ""
			p.newSourceName = ""
			p.newSourceURL = ""
			p.newSourceType = "http"
		}
		return true

	case "backspace":
		// Delete character
		switch p.addSourceState {
		case "name":
			if len(p.newSourceName) > 0 {
				p.newSourceName = p.newSourceName[:len(p.newSourceName)-1]
			}
		case "url":
			if len(p.newSourceURL) > 0 {
				p.newSourceURL = p.newSourceURL[:len(p.newSourceURL)-1]
			}
		}
		return true

	case "left", "right":
		// Cycle through type options
		if p.addSourceState == "type" {
			types := []string{"http", "github", "github-search", "awesome-list"}
			for i, t := range types {
				if t == p.newSourceType {
					if key == "right" {
						p.newSourceType = types[(i+1)%len(types)]
					} else {
						p.newSourceType = types[(i+len(types)-1)%len(types)]
					}
					break
				}
			}
		}
		return true

	case "space":
		// Add space to text fields
		switch p.addSourceState {
		case "name":
			p.newSourceName += " "
		case "url":
			p.newSourceURL += " "
		case "type":
			// Cycle through type options with space
			types := []string{"http", "github", "github-search", "awesome-list"}
			for i, t := range types {
				if t == p.newSourceType {
					p.newSourceType = types[(i+1)%len(types)]
					break
				}
			}
		}
		return true

	default:
		// Regular character input
		if len(key) == 1 && key[0] >= 32 && key[0] <= 126 {
			switch p.addSourceState {
			case "name":
				p.newSourceName += key
			case "url":
				p.newSourceURL += key
			}
			return true
		}
	}

	return true
}
