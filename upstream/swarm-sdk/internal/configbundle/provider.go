// Package configbundle provides a unified configuration system for Swarm.
package configbundle

// ConfigSourceProvider provides access to config bundle functionality for UI display.
// This interface is implemented by ConfigBundleIntegration in the commands package.
type ConfigSourceProvider interface {
	IsUsingProject() bool
	HasProjectConfig() bool
	ProjectName() string
	ProjectPath() string
	GlobalPath() string
	Source() string
	SourceDisplayName() string
	Toggle() string
	// Config data access
	GetActiveConfigLite() *ConfigBundleLite
	// Editing
	OpenInEditor() error
	// Creation
	CreateProjectConfig(workDir string) error
}

// ConfigBundleLite is a lightweight view of config data for display.
type ConfigBundleLite struct {
	Name        string
	Description string
	Source      string // "global" or "project"
	Path        string

	// Section summaries
	System     ConfigSectionSummary
	Agents     ConfigSectionSummary
	Profiles   ConfigSectionSummary
	Prompts    ConfigSectionSummary
	Tools      ConfigSectionSummary
	Hooks      ConfigSectionSummary
	Skills     ConfigSectionSummary
	Providers  ConfigSectionSummary
	MCPServers ConfigSectionSummary
}

// ConfigSectionSummary provides a summary of a config section.
type ConfigSectionSummary struct {
	HasData   bool     // Does this section have any config?
	Count     int      // How many items?
	Preview   string   // First item name or key value
	Overrides []string // What's different from parent (for project config)
}
