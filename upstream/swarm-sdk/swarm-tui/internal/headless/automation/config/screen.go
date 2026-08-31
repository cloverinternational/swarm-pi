// Package config provides screen configuration for TUI automation.
//
// Screen configurations define the structure of each screen in the TUI,
// including regions, navigation paths, and detection patterns.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds all screen configurations.
type Config struct {
	Screens map[string]*Screen `yaml:"screens"`
}

// Screen defines a single screen in the TUI.
type Screen struct {
	// ID is the unique identifier for this screen.
	ID string `yaml:"id"`
	// Type is the Go type name for detection (e.g., "ScreenChat").
	Type string `yaml:"type"`
	// Regions defines interactive areas on this screen.
	Regions []Region `yaml:"regions"`
	// Navigation defines key sequences to reach other screens.
	Navigation map[string][]string `yaml:"navigation"`
	// Detection defines patterns to identify this screen.
	Detection *Detection `yaml:"detection,omitempty"`
}

// Region defines an interactive area on a screen.
type Region struct {
	// ID is the unique identifier for this region.
	ID string `yaml:"id"`
	// Bounds defines the region's position and size.
	Bounds Bounds `yaml:"bounds"`
	// Clickable indicates if this region responds to clicks.
	Clickable bool `yaml:"clickable,omitempty"`
	// Scrollable indicates if this region can be scrolled.
	Scrollable bool `yaml:"scrollable,omitempty"`
	// Input indicates if this region accepts text input.
	Input bool `yaml:"input,omitempty"`
	// Optional indicates if this region may not always be visible.
	Optional bool `yaml:"optional,omitempty"`
	// NavigationTarget is the screen to navigate to when clicked.
	NavigationTarget string `yaml:"navigation_target,omitempty"`
}

// Bounds defines position and size. Negative values are from the edge.
type Bounds struct {
	X      int `yaml:"x"`
	Y      int `yaml:"y"`
	Width  int `yaml:"width"`
	Height int `yaml:"height"`
}

// Resolve resolves negative bounds relative to viewport size.
func (b Bounds) Resolve(viewportWidth, viewportHeight int) Bounds {
	resolved := b

	// Negative X means from right edge
	if b.X < 0 {
		resolved.X = viewportWidth + b.X
	}

	// Negative Y means from bottom edge
	if b.Y < 0 {
		resolved.Y = viewportHeight + b.Y
	}

	// Negative width means extend to (viewportWidth + width)
	if b.Width < 0 {
		resolved.Width = viewportWidth + b.Width - resolved.X
	}

	// Negative height means extend to (viewportHeight + height)
	if b.Height < 0 {
		resolved.Height = viewportHeight + b.Height - resolved.Y
	}

	return resolved
}

// Detection defines how to identify a screen.
type Detection struct {
	// TextPatterns are strings that must appear on the screen.
	TextPatterns []string `yaml:"text_patterns,omitempty"`
	// TitlePattern is a regex for the title/header.
	TitlePattern string `yaml:"title_pattern,omitempty"`
	// RequiredRegions are region IDs that must be visible.
	RequiredRegions []string `yaml:"required_regions,omitempty"`
}

// Load loads a configuration from a YAML file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	return Parse(data)
}

// Parse parses configuration from YAML data.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Validate and set defaults
	for id, screen := range cfg.Screens {
		if screen.ID == "" {
			screen.ID = id
		}
	}

	return &cfg, nil
}

// GetScreen returns a screen by ID.
func (c *Config) GetScreen(id string) *Screen {
	return c.Screens[id]
}

// GetNavigationPath returns the key sequence to navigate from one screen to another.
func (c *Config) GetNavigationPath(from, to string) ([]string, error) {
	fromScreen := c.Screens[from]
	if fromScreen == nil {
		return nil, fmt.Errorf("unknown screen: %s", from)
	}

	// Direct navigation
	for name, keys := range fromScreen.Navigation {
		// Check if navigation name matches target or contains target
		if name == "to_"+to || strings.HasSuffix(name, "_"+to) {
			return keys, nil
		}
	}

	// TODO: Implement multi-hop navigation via BFS
	return nil, fmt.Errorf("no navigation path from %s to %s", from, to)
}

// FindRegion finds a region by ID across all screens.
func (c *Config) FindRegion(regionID string) (*Screen, *Region) {
	for _, screen := range c.Screens {
		for i := range screen.Regions {
			if screen.Regions[i].ID == regionID {
				return screen, &screen.Regions[i]
			}
		}
	}
	return nil, nil
}

// Navigator provides high-level navigation using screen configs.
type Navigator struct {
	config        *Config
	currentScreen string
}

// NewNavigator creates a navigator with the given config.
func NewNavigator(cfg *Config) *Navigator {
	return &Navigator{
		config: cfg,
	}
}

// SetCurrentScreen sets the current screen.
func (n *Navigator) SetCurrentScreen(screenID string) {
	n.currentScreen = screenID
}

// GetCurrentScreen returns the current screen.
func (n *Navigator) GetCurrentScreen() string {
	return n.currentScreen
}

// GetKeysTo returns the keys needed to navigate to a screen.
func (n *Navigator) GetKeysTo(targetScreen string) ([]string, error) {
	if n.currentScreen == "" {
		return nil, fmt.Errorf("current screen not set")
	}
	return n.config.GetNavigationPath(n.currentScreen, targetScreen)
}

// DetectScreen attempts to detect which screen is displayed based on content.
func (n *Navigator) DetectScreen(content string) string {
	for id, screen := range n.config.Screens {
		if screen.Detection == nil {
			continue
		}

		// Check text patterns
		matched := true
		for _, pattern := range screen.Detection.TextPatterns {
			if !strings.Contains(content, pattern) {
				matched = false
				break
			}
		}

		if matched {
			return id
		}
	}
	return ""
}
