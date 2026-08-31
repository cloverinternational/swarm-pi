package settings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/safego"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// SkillsSettings manages the skills configuration UI
type SkillsSettings struct {
	loader        *skills.Loader
	searchResults []skills.SkillSearchResult
	errorMessage  string
	successMsg    string

	// View state
	viewMode           string // "installed", "available", or "sources"
	marketplaceEntries []skills.PluginEntry
	marketplaceLoaded  bool
	marketplaceLoading bool
	marketplaceSources []skills.MarketplaceSource

	// Add source input state
	addSourceState string // "", "name", "url", "type"
	newSourceName  string
	newSourceURL   string
	newSourceType  string

	// Callbacks
	onSkillToggle    func(name string, enabled bool) error
	onSkillInstall   func(name string) error
	onSkillUninstall func(name string) error
}

// NewSkillsSettings creates a new skills settings component
func NewSkillsSettings() *SkillsSettings {
	return &SkillsSettings{
		searchResults:      []skills.SkillSearchResult{},
		viewMode:           "installed",
		marketplaceEntries: []skills.PluginEntry{},
	}
}

// SetLoader sets the skill loader
func (s *SkillsSettings) SetLoader(loader *skills.Loader) {
	s.loader = loader
}

// LoadMarketplace loads marketplace entries in the background
func (s *SkillsSettings) LoadMarketplace() {
	logDebug("[SkillsSettings] LoadMarketplace called, loader=%v, loading=%v, loaded=%v", s.loader != nil, s.marketplaceLoading, s.marketplaceLoaded)

	if s.loader == nil {
		logDebug("[SkillsSettings] LoadMarketplace: loader is nil")
		return
	}
	if s.marketplaceLoading {
		logDebug("[SkillsSettings] LoadMarketplace: already loading")
		return
	}
	if s.marketplaceLoaded {
		logDebug("[SkillsSettings] LoadMarketplace: already loaded with %d entries", len(s.marketplaceEntries))
		return
	}

	s.marketplaceLoading = true
	logDebug("[SkillsSettings] LoadMarketplace: starting background load")

	safego.Go("settings.skills.loadMarketplace", func() {
		// Update marketplace (fetch from sources)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		logDebug("[SkillsSettings] LoadMarketplace: calling Database.Update")
		if err := s.loader.Database.Update(ctx); err != nil {
			logDebug("[SkillsSettings] LoadMarketplace: Update failed: %v", err)
			s.errorMessage = "Failed to load marketplace: " + err.Error()
		} else {
			logDebug("[SkillsSettings] LoadMarketplace: Update succeeded")
		}

		// Get all entries
		s.marketplaceEntries = s.loader.Database.GetAll()
		logDebug("[SkillsSettings] LoadMarketplace: got %d entries", len(s.marketplaceEntries))
		s.marketplaceLoaded = true
		s.marketplaceLoading = false
	})
}

// GetMarketplaceEntries returns cached marketplace entries
func (s *SkillsSettings) GetMarketplaceEntries() []skills.PluginEntry {
	return s.marketplaceEntries
}

// LoadSources loads marketplace sources from the database
func (s *SkillsSettings) LoadSources() {
	if s.loader == nil {
		return
	}
	s.marketplaceSources = s.loader.Database.GetSources()
	logDebug("[SkillsSettings] LoadSources: got %d sources", len(s.marketplaceSources))
}

// GetSources returns cached marketplace sources
func (s *SkillsSettings) GetSources() []skills.MarketplaceSource {
	return s.marketplaceSources
}

// AddSource adds a new marketplace source
func (s *SkillsSettings) AddSource(name, url, sourceType string) error {
	if s.loader == nil {
		return fmt.Errorf("loader not initialized")
	}

	source := skills.MarketplaceSource{
		Name:     name,
		URL:      url,
		Type:     sourceType,
		Enabled:  true,
		Priority: 50, // Default priority
	}

	s.loader.Database.AddSource(source)
	s.LoadSources()
	s.successMsg = fmt.Sprintf("Added source: %s", name)
	return nil
}

// RemoveSource removes a marketplace source
func (s *SkillsSettings) RemoveSource(name string) error {
	if s.loader == nil {
		return fmt.Errorf("loader not initialized")
	}

	s.loader.Database.RemoveSource(name)
	s.LoadSources()
	s.successMsg = fmt.Sprintf("Removed source: %s", name)
	return nil
}

// ToggleSource toggles a marketplace source enabled state
func (s *SkillsSettings) ToggleSource(name string) error {
	if s.loader == nil {
		return fmt.Errorf("loader not initialized")
	}

	// Find current state
	sources := s.loader.Database.GetSources()
	for _, src := range sources {
		if src.Name == name {
			s.loader.Database.SetSourceEnabled(name, !src.Enabled)
			s.LoadSources()
			if !src.Enabled {
				s.successMsg = fmt.Sprintf("Enabled source: %s", name)
			} else {
				s.successMsg = fmt.Sprintf("Disabled source: %s", name)
			}
			return nil
		}
	}

	return fmt.Errorf("source not found: %s", name)
}

// SetCallbacks sets the action callbacks
func (s *SkillsSettings) SetCallbacks(
	toggle func(name string, enabled bool) error,
	install func(name string) error,
	uninstall func(name string) error,
) {
	s.onSkillToggle = toggle
	s.onSkillInstall = install
	s.onSkillUninstall = uninstall
}

// GetSkills returns all loaded skills
func (s *SkillsSettings) GetSkills() []*skills.Skill {
	if s.loader == nil {
		return nil
	}
	return s.loader.List()
}

// GetActiveSkills returns currently active skills
func (s *SkillsSettings) GetActiveSkills() []*skills.Skill {
	if s.loader == nil {
		return nil
	}
	return s.loader.GetActiveSkills()
}

// getSkillSource determines the source of a skill (SYS/USR/PRJ/POL)
// Mirrors SDK loader search paths in swarm-sdk/skills/loader.go.
// Previously only recognized ~/.swarmos/skills as "USR"; now recognizes
// all SDK search paths: ~/.claude/skills, ~/.claude/commands, ~/.swarm/skills.
// Also adds POL (policy) for managed skills via SWARM_MANAGED_SKILLS_DIR.
func (s *SkillsSettings) getSkillSource(skill *skills.Skill) string {
	if skill == nil || skill.Path == "" {
		return "SYS"
	}

	path := skill.Path
	homeDir, _ := os.UserHomeDir()

	// User-level skills directories (SDK loader.go:57-61)
	userPatterns := []string{
		filepath.Join(homeDir, ".swarmos", "skills"),
		filepath.Join(homeDir, ".claude", "skills"),
		filepath.Join(homeDir, ".claude", "commands"),
		filepath.Join(homeDir, ".swarm", "skills"),
	}
	for _, pattern := range userPatterns {
		if strings.HasPrefix(path, pattern) {
			return "USR"
		}
	}

	// Project-local skills directories (SDK registry.go:202-206, skills_manager.go:55-59)
	projectPatterns := []string{
		".claude" + string(filepath.Separator) + "skills",
		".claude" + string(filepath.Separator) + "commands",
		".swarm" + string(filepath.Separator) + "skills",
		".swarmos" + string(filepath.Separator) + "skills",
	}
	for _, pattern := range projectPatterns {
		if strings.Contains(path, pattern) {
			return "PRJ"
		}
	}

	// Managed/policy skills (SDK loader.go:44-50)
	if managedDir := os.Getenv("SWARM_MANAGED_SKILLS_DIR"); managedDir != "" {
		if strings.HasPrefix(path, managedDir) {
			return "POL"
		}
	}

	// Default to system/built-in
	return "SYS"
}

// Render renders the skills settings UI
func (s *SkillsSettings) HandleKey(key string, state *State) bool {
	switch state.SkillsState {
	case "list":
		return s.handleListKey(key, state)
	case "detail":
		return s.handleDetailKey(key, state)
	case "search":
		return s.handleSearchKey(key, state)
	case "add_source":
		return s.handleAddSourceKey(key, state)
	}
	return false
}

// handleListKey handles keyboard input in list view
func (s *SkillsSettings) handleListKey(key string, state *State) bool {
	logDebug("[SkillsSettings] handleListKey: key=%q, viewMode=%q, loader=%v", key, s.viewMode, s.loader != nil)
	s.errorMessage = ""
	s.successMsg = ""

	// Determine current list length based on view mode
	var listLen int
	switch s.viewMode {
	case "available":
		listLen = len(s.marketplaceEntries)
	case "sources":
		listLen = len(s.marketplaceSources)
	default:
		listLen = len(s.GetSkills())
	}
	logDebug("[SkillsSettings] handleListKey: listLen=%d", listLen)

	switch key {
	case "1":
		// Switch to installed view
		if s.viewMode != "installed" {
			s.viewMode = "installed"
			state.SkillsSelected = 0
			state.SkillsScrollOffset = 0
		}
		return true

	case "2":
		// Switch to available/marketplace view
		logDebug("[SkillsSettings] Switching to available view, marketplaceLoaded=%v, marketplaceLoading=%v", s.marketplaceLoaded, s.marketplaceLoading)
		if s.viewMode != "available" {
			s.viewMode = "available"
			// Load marketplace if not already loaded
			if !s.marketplaceLoaded && !s.marketplaceLoading {
				logDebug("[SkillsSettings] Calling LoadMarketplace")
				s.LoadMarketplace()
			}
			state.SkillsSelected = 0
			state.SkillsScrollOffset = 0
		}
		return true

	case "3":
		// Switch to sources view
		logDebug("[SkillsSettings] Switching to sources view")
		if s.viewMode != "sources" {
			s.viewMode = "sources"
			// Load sources
			s.LoadSources()
			state.SkillsSelected = 0
			state.SkillsScrollOffset = 0
		}
		return true

	case "up", "k":
		if state.SkillsSelected > 0 {
			state.SkillsSelected--
			// Adjust scroll
			if state.SkillsSelected < state.SkillsScrollOffset {
				state.SkillsScrollOffset = state.SkillsSelected
			}
		}
		return true

	case "down", "j":
		if state.SkillsSelected < listLen-1 {
			state.SkillsSelected++
			// Adjust scroll (assuming ~10 visible items)
			if state.SkillsSelected >= state.SkillsScrollOffset+10 {
				state.SkillsScrollOffset = state.SkillsSelected - 9
			}
		}
		return true

	case "space", "enter":
		if s.viewMode == "available" {
			// Install from marketplace
			if listLen > 0 && state.SkillsSelected < listLen {
				entry := s.marketplaceEntries[state.SkillsSelected]
				// Check if already installed
				if s.loader != nil && s.loader.Database.IsInstalled(entry.Name) {
					s.errorMessage = "Already installed: " + entry.Name
				} else if s.loader != nil {
					homeDir, _ := os.UserHomeDir()
					installDir := filepath.Join(homeDir, ".swarmos", "skills")
					_, err := s.loader.Database.Install(context.Background(), entry.Name, installDir)
					if err != nil {
						s.errorMessage = "Install failed: " + err.Error()
					} else {
						s.successMsg = "Installed: " + entry.Name
					}
				}
			}
		} else if s.viewMode == "sources" {
			// Toggle source enabled/disabled
			if listLen > 0 && state.SkillsSelected < listLen {
				source := s.marketplaceSources[state.SkillsSelected]
				if err := s.ToggleSource(source.Name); err != nil {
					s.errorMessage = err.Error()
				}
			}
		} else {
			// Toggle skill activation
			allSkills := s.GetSkills()
			if len(allSkills) > 0 && state.SkillsSelected < len(allSkills) {
				skill := allSkills[state.SkillsSelected]
				activeSkills := s.GetActiveSkills()
				isActive := false
				for _, sk := range activeSkills {
					if sk.Metadata.Name == skill.Metadata.Name {
						isActive = true
						break
					}
				}

				if s.loader != nil {
					if isActive {
						if err := s.loader.Deactivate(skill.Metadata.Name); err != nil {
							s.errorMessage = "Failed to deactivate: " + err.Error()
						} else {
							s.successMsg = "Deactivated: " + skill.Metadata.Name
						}
					} else {
						if err := s.loader.Activate(skill.Metadata.Name); err != nil {
							s.errorMessage = "Failed to activate: " + err.Error()
						} else {
							s.successMsg = "Activated: " + skill.Metadata.Name
						}
					}
				}

				if s.onSkillToggle != nil {
					s.onSkillToggle(skill.Metadata.Name, !isActive)
				}
			}
		}
		return true

	case "d":
		if s.viewMode == "sources" {
			// Delete source
			if listLen > 0 && state.SkillsSelected < listLen {
				source := s.marketplaceSources[state.SkillsSelected]
				if err := s.RemoveSource(source.Name); err != nil {
					s.errorMessage = err.Error()
				}
				// Adjust selection if needed
				if state.SkillsSelected >= len(s.marketplaceSources) && state.SkillsSelected > 0 {
					state.SkillsSelected--
				}
			}
		} else if s.viewMode == "installed" {
			// Show detail view (only for installed)
			allSkills := s.GetSkills()
			if len(allSkills) > 0 && state.SkillsSelected < len(allSkills) {
				state.SkillsState = "detail"
				state.SkillsDetailTab = 0
			}
		}
		return true

	case "/":
		// Show search view
		state.SkillsState = "search"
		state.SkillsSearchQuery = ""
		state.SkillsSearchSelected = 0
		state.SkillsSearchOffset = 0
		state.SkillsCursorPos = 0
		s.searchResults = []skills.SkillSearchResult{}
		return true

	case "u":
		// Uninstall selected skill (only for installed)
		if s.viewMode == "installed" {
			allSkills := s.GetSkills()
			if len(allSkills) > 0 && state.SkillsSelected < len(allSkills) {
				skill := allSkills[state.SkillsSelected]
				if s.loader != nil {
					if err := s.loader.Uninstall(skill.Metadata.Name); err != nil {
						s.errorMessage = "Failed to uninstall: " + err.Error()
					} else {
						s.successMsg = "Uninstalled: " + skill.Metadata.Name
						// Adjust selection if needed
						if state.SkillsSelected >= len(s.GetSkills()) && state.SkillsSelected > 0 {
							state.SkillsSelected--
						}
					}
				}
				if s.onSkillUninstall != nil {
					s.onSkillUninstall(skill.Metadata.Name)
				}
			}
		}
		return true

	case "a":
		// Add source (only in sources view)
		if s.viewMode == "sources" {
			state.SkillsState = "add_source"
			s.addSourceState = "name"
			s.newSourceName = ""
			s.newSourceURL = ""
			s.newSourceType = "http"
		}
		return true

	case "r", "R":
		// Refresh
		if s.viewMode == "available" {
			// Refresh marketplace
			s.marketplaceLoaded = false
			s.marketplaceLoading = false
			s.LoadMarketplace()
			s.successMsg = "Refreshing marketplace..."
		} else if s.viewMode == "sources" {
			// Refresh sources list and reload marketplace
			s.LoadSources()
			s.marketplaceLoaded = false
			s.marketplaceLoading = false
			s.LoadMarketplace()
			s.successMsg = "Refreshing sources and marketplace..."
		} else if s.loader != nil {
			if err := s.loader.Initialize(context.Background()); err != nil {
				s.errorMessage = "Failed to refresh: " + err.Error()
			} else {
				s.successMsg = "Skills refreshed"
			}
		}
		return true

	case "pageup", "ctrl+u":
		state.SkillsScrollOffset = maxInt(0, state.SkillsScrollOffset-5)
		if state.SkillsSelected > state.SkillsScrollOffset+9 {
			state.SkillsSelected = state.SkillsScrollOffset + 9
		}
		return true

	case "pagedown", "ctrl+d":
		maxOffset := maxInt(0, listLen-10)
		state.SkillsScrollOffset = minInt(state.SkillsScrollOffset+5, maxOffset)
		if state.SkillsSelected < state.SkillsScrollOffset {
			state.SkillsSelected = state.SkillsScrollOffset
		}
		return true
	}

	return false
}

// handleDetailKey handles keyboard input in detail view
func (s *SkillsSettings) handleDetailKey(key string, state *State) bool {
	allSkills := s.GetSkills()
	s.errorMessage = ""
	s.successMsg = ""

	switch key {
	case "esc", "backspace", "q":
		state.SkillsState = "list"
		return true

	case "tab":
		state.SkillsDetailTab = (state.SkillsDetailTab + 1) % 3
		return true

	case "shift+tab":
		state.SkillsDetailTab = (state.SkillsDetailTab + 2) % 3
		return true

	case "space", "enter":
		// Toggle activation
		if len(allSkills) > 0 && state.SkillsSelected < len(allSkills) {
			skill := allSkills[state.SkillsSelected]
			activeSkills := s.GetActiveSkills()
			isActive := false
			for _, sk := range activeSkills {
				if sk.Metadata.Name == skill.Metadata.Name {
					isActive = true
					break
				}
			}

			if s.loader != nil {
				if isActive {
					if err := s.loader.Deactivate(skill.Metadata.Name); err != nil {
						s.errorMessage = "Failed to deactivate: " + err.Error()
					} else {
						s.successMsg = "Deactivated: " + skill.Metadata.Name
					}
				} else {
					if err := s.loader.Activate(skill.Metadata.Name); err != nil {
						s.errorMessage = "Failed to activate: " + err.Error()
					} else {
						s.successMsg = "Activated: " + skill.Metadata.Name
					}
				}
			}

			if s.onSkillToggle != nil {
				s.onSkillToggle(skill.Metadata.Name, !isActive)
			}
		}
		return true

	case "u":
		// Uninstall
		if len(allSkills) > 0 && state.SkillsSelected < len(allSkills) {
			skill := allSkills[state.SkillsSelected]
			if s.loader != nil {
				if err := s.loader.Uninstall(skill.Metadata.Name); err != nil {
					s.errorMessage = "Failed to uninstall: " + err.Error()
				} else {
					s.successMsg = "Uninstalled: " + skill.Metadata.Name
					state.SkillsState = "list"
					if state.SkillsSelected >= len(s.GetSkills()) && state.SkillsSelected > 0 {
						state.SkillsSelected--
					}
				}
			}
			if s.onSkillUninstall != nil {
				s.onSkillUninstall(skill.Metadata.Name)
			}
		}
		return true
	}

	return false
}

// handleSearchKey handles keyboard input in search view
func (s *SkillsSettings) handleSearchKey(key string, state *State) bool {
	s.errorMessage = ""
	s.successMsg = ""

	switch key {
	case "esc":
		state.SkillsState = "list"
		return true

	case "up", "ctrl+p":
		if state.SkillsSearchSelected > 0 {
			state.SkillsSearchSelected--
			if state.SkillsSearchSelected < state.SkillsSearchOffset {
				state.SkillsSearchOffset = state.SkillsSearchSelected
			}
		}
		return true

	case "down", "ctrl+n":
		if state.SkillsSearchSelected < len(s.searchResults)-1 {
			state.SkillsSearchSelected++
			if state.SkillsSearchSelected >= state.SkillsSearchOffset+8 {
				state.SkillsSearchOffset = state.SkillsSearchSelected - 7
			}
		}
		return true

	case "enter":
		// If we have a search result selected, install it
		if len(s.searchResults) > 0 && state.SkillsSearchSelected < len(s.searchResults) {
			result := s.searchResults[state.SkillsSearchSelected]
			if result.Installed {
				s.errorMessage = "Already installed"
			} else if s.loader != nil {
				skill, err := s.loader.Install(context.Background(), result.Name)
				if err != nil {
					s.errorMessage = "Install failed: " + err.Error()
				} else {
					s.successMsg = "Installed: " + skill.Metadata.Name
					// Mark as installed in results
					s.searchResults[state.SkillsSearchSelected].Installed = true
				}
			}
			if s.onSkillInstall != nil {
				s.onSkillInstall(result.Name)
			}
		} else if state.SkillsSearchQuery != "" && s.loader != nil {
			// Perform search
			results, err := s.loader.Search(context.Background(), state.SkillsSearchQuery)
			if err != nil {
				s.errorMessage = "Search failed: " + err.Error()
			} else {
				s.searchResults = results
				state.SkillsSearchSelected = 0
				state.SkillsSearchOffset = 0
			}
		}
		return true

	case "backspace":
		if state.SkillsCursorPos > 0 {
			state.SkillsSearchQuery = state.SkillsSearchQuery[:state.SkillsCursorPos-1] + state.SkillsSearchQuery[state.SkillsCursorPos:]
			state.SkillsCursorPos--
		}
		return true

	case "delete":
		if state.SkillsCursorPos < len(state.SkillsSearchQuery) {
			state.SkillsSearchQuery = state.SkillsSearchQuery[:state.SkillsCursorPos] + state.SkillsSearchQuery[state.SkillsCursorPos+1:]
		}
		return true

	case "left":
		if state.SkillsCursorPos > 0 {
			state.SkillsCursorPos--
		}
		return true

	case "right":
		if state.SkillsCursorPos < len(state.SkillsSearchQuery) {
			state.SkillsCursorPos++
		}
		return true

	case "home", "ctrl+a":
		state.SkillsCursorPos = 0
		return true

	case "end", "ctrl+e":
		state.SkillsCursorPos = len(state.SkillsSearchQuery)
		return true

	case "space":
		state.SkillsSearchQuery = state.SkillsSearchQuery[:state.SkillsCursorPos] + " " + state.SkillsSearchQuery[state.SkillsCursorPos:]
		state.SkillsCursorPos++
		return true

	default:
		// Regular character input - accept all printable ASCII characters (except space which is handled above)
		if len(key) == 1 && key[0] >= 33 && key[0] <= 126 {
			state.SkillsSearchQuery = state.SkillsSearchQuery[:state.SkillsCursorPos] + key + state.SkillsSearchQuery[state.SkillsCursorPos:]
			state.SkillsCursorPos++
			return true
		} else if len(key) > 1 && !strings.HasPrefix(key, "ctrl+") && !strings.HasPrefix(key, "alt+") {
			// Multi-byte characters
			state.SkillsSearchQuery = state.SkillsSearchQuery[:state.SkillsCursorPos] + key + state.SkillsSearchQuery[state.SkillsCursorPos:]
			state.SkillsCursorPos += len(key)
			return true
		}
	}

	// Consume all keys when in search mode to prevent parent handlers from processing them
	return true
}

// handleAddSourceKey handles keyboard input in add source form
func (s *SkillsSettings) handleAddSourceKey(key string, state *State) bool {
	s.errorMessage = ""

	switch key {
	case "esc":
		// Cancel and return to sources list
		state.SkillsState = "list"
		s.addSourceState = ""
		return true

	case "tab":
		// Move to next field
		switch s.addSourceState {
		case "name":
			s.addSourceState = "url"
		case "url":
			s.addSourceState = "type"
		case "type":
			s.addSourceState = "name"
		}
		return true

	case "shift+tab":
		// Move to previous field
		switch s.addSourceState {
		case "name":
			s.addSourceState = "type"
		case "url":
			s.addSourceState = "name"
		case "type":
			s.addSourceState = "url"
		}
		return true

	case "enter":
		if s.addSourceState == "type" {
			// Try to save
			if s.newSourceName == "" {
				s.errorMessage = "Name is required"
				s.addSourceState = "name"
				return true
			}
			if s.newSourceURL == "" {
				s.errorMessage = "URL is required"
				s.addSourceState = "url"
				return true
			}

			// Add the source
			if err := s.AddSource(s.newSourceName, s.newSourceURL, s.newSourceType); err != nil {
				s.errorMessage = err.Error()
			} else {
				// Success - return to list
				state.SkillsState = "list"
				s.addSourceState = ""
			}
		} else {
			// Move to next field on enter
			switch s.addSourceState {
			case "name":
				s.addSourceState = "url"
			case "url":
				s.addSourceState = "type"
			}
		}
		return true

	case "left":
		// For type field, cycle through options
		if s.addSourceState == "type" {
			switch s.newSourceType {
			case "http":
				s.newSourceType = "awesome-list"
			case "github-search":
				s.newSourceType = "http"
			case "awesome-list":
				s.newSourceType = "github-search"
			}
		}
		return true

	case "right":
		// For type field, cycle through options
		if s.addSourceState == "type" {
			switch s.newSourceType {
			case "http":
				s.newSourceType = "github-search"
			case "github-search":
				s.newSourceType = "awesome-list"
			case "awesome-list":
				s.newSourceType = "http"
			}
		}
		return true

	case "backspace":
		// Handle backspace for text fields
		switch s.addSourceState {
		case "name":
			if len(s.newSourceName) > 0 {
				s.newSourceName = s.newSourceName[:len(s.newSourceName)-1]
			}
		case "url":
			if len(s.newSourceURL) > 0 {
				s.newSourceURL = s.newSourceURL[:len(s.newSourceURL)-1]
			}
		}
		return true

	default:
		// Regular character input for text fields
		if s.addSourceState == "name" || s.addSourceState == "url" {
			if len(key) == 1 && key[0] >= 32 && key[0] <= 126 {
				if s.addSourceState == "name" {
					s.newSourceName += key
				} else {
					s.newSourceURL += key
				}
				return true
			}
		}
	}

	// Consume all keys in add source mode
	return true
}
