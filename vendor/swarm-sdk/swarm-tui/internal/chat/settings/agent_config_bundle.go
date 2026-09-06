package settings

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// AgentConfigBundle represents a complete, shareable agent configuration package.
// This bundles together agent profiles and custom agents for easy export/import.
//
// Use cases:
// - Share team configurations
// - Backup configurations
// - Switch between different working contexts
// - Migrate configurations between machines
type AgentConfigBundle struct {
	// Metadata
	Version     string    `json:"version"`     // Bundle format version (e.g., "1.0")
	Name        string    `json:"name"`        // Bundle name (e.g., "Team Config", "My Setup")
	Description string    `json:"description"` // What this bundle contains
	CreatedAt   time.Time `json:"created_at"`  // When bundle was created
	Author      string    `json:"author"`      // Who created it (optional)

	// Configuration data
	Profiles      *ProfilesConfig    `json:"profiles,omitempty"`      // Agent profiles (role→model mappings)
	CustomAgents  *CustomAgentConfig `json:"custom_agents,omitempty"` // Custom agent definitions
	ActiveProfile string             `json:"active_profile"`          // Which profile is active
	ActiveAgent   string             `json:"active_agent"`            // Which custom agent is default

	// Bundle-level settings
	Tags     []string          `json:"tags,omitempty"`     // Searchable tags (e.g., "development", "production")
	Metadata map[string]string `json:"metadata,omitempty"` // Additional custom metadata
}

// BundleManager handles agent config bundle operations
type BundleManager struct {
	bundleDir string // Directory for stored bundles
}

// NewBundleManager creates a bundle manager
func NewBundleManager() *BundleManager {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	bundleDir := filepath.Join(home, ".swarmos", "config_bundles")

	// Ensure directory exists
	os.MkdirAll(bundleDir, 0755)

	return &BundleManager{
		bundleDir: bundleDir,
	}
}

// CreateBundle creates a new config bundle from current settings
func (bm *BundleManager) CreateBundle(name, description string, profiles *ProfilesConfig, agents *CustomAgentConfig) (*AgentConfigBundle, error) {
	bundle := &AgentConfigBundle{
		Version:      "1.0",
		Name:         name,
		Description:  description,
		CreatedAt:    time.Now(),
		Profiles:     profiles,
		CustomAgents: agents,
		Tags:         []string{},
		Metadata:     make(map[string]string),
	}

	// Set active profile/agent from configs
	if profiles != nil {
		bundle.ActiveProfile = profiles.DefaultProfile
	}
	if agents != nil {
		bundle.ActiveAgent = agents.DefaultAgent
	}

	return bundle, nil
}

// ExportBundle saves a bundle to a JSON file
func (bm *BundleManager) ExportBundle(bundle *AgentConfigBundle, filename string) error {
	// Validate bundle
	if err := bundle.Validate(); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.validation_failed"), err)
	}

	// Determine file path
	var filePath string
	if filepath.IsAbs(filename) {
		filePath = filename
	} else {
		filePath = filepath.Join(bm.bundleDir, filename)
	}

	// Ensure .json extension
	if filepath.Ext(filePath) != ".json" {
		filePath += ".json"
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.marshal_failed"), err)
	}

	// Atomic write (temp file + rename)
	tempPath := filePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.write_temp_failed"), err)
	}

	if err := os.Rename(tempPath, filePath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.rename_temp_failed"), err)
	}

	return nil
}

// ImportBundle loads a bundle from a JSON file
func (bm *BundleManager) ImportBundle(filename string) (*AgentConfigBundle, error) {
	// Determine file path
	var filePath string
	if filepath.IsAbs(filename) {
		filePath = filename
	} else {
		filePath = filepath.Join(bm.bundleDir, filename)
		// Try with .json extension if not found
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			filePath = filepath.Join(bm.bundleDir, filename+".json")
		}
	}

	// Read file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.read_failed"), err)
	}

	// Unmarshal
	var bundle AgentConfigBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.parse_failed"), err)
	}

	// Validate
	if err := bundle.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.validation_failed"), err)
	}

	return &bundle, nil
}

// ListBundles returns all available bundles in the bundle directory
func (bm *BundleManager) ListBundles() ([]BundleInfo, error) {
	entries, err := os.ReadDir(bm.bundleDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []BundleInfo{}, nil
		}
		return nil, fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.read_directory_failed"), err)
	}

	var bundles []BundleInfo
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		// Try to load bundle metadata
		filePath := filepath.Join(bm.bundleDir, entry.Name())
		bundle, err := bm.ImportBundle(filePath)
		if err != nil {
			// Skip invalid bundles
			continue
		}

		info := BundleInfo{
			Filename:    entry.Name(),
			Name:        bundle.Name,
			Description: bundle.Description,
			CreatedAt:   bundle.CreatedAt,
			Author:      bundle.Author,
			Tags:        bundle.Tags,
		}
		bundles = append(bundles, info)
	}

	return bundles, nil
}

// DeleteBundle removes a bundle file
func (bm *BundleManager) DeleteBundle(filename string) error {
	var filePath string
	if filepath.IsAbs(filename) {
		filePath = filename
	} else {
		filePath = filepath.Join(bm.bundleDir, filename)
		if filepath.Ext(filePath) != ".json" {
			filePath += ".json"
		}
	}

	return os.Remove(filePath)
}

// ApplyBundle applies a bundle's configuration to the system
func (bm *BundleManager) ApplyBundle(bundle *AgentConfigBundle, profileMgr *ProfileManager, agentSettings *AgentsSettings) error {
	// Validate bundle first
	if err := bundle.Validate(); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.validation_failed"), err)
	}

	// Apply profiles if present
	if bundle.Profiles != nil && profileMgr != nil {
		// Merge or replace profiles
		for _, profile := range bundle.Profiles.Profiles {
			if err := profileMgr.UpdateProfile(profile.ID, profile); err != nil {
				// Profile doesn't exist, create it
				if _, err := profileMgr.CreateProfile(profile.Name, profile.Description); err != nil {
					return fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.apply_profile_failed", profile.ID), err)
				}
			}
		}

		// Set default profile
		if bundle.ActiveProfile != "" {
			if err := profileMgr.SetDefaultProfile(bundle.ActiveProfile); err != nil {
				return fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.set_profile_failed"), err)
			}
		}

		// Save profiles
		if err := profileMgr.Save(); err != nil {
			return fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.save_profiles_failed"), err)
		}
	}

	// Apply custom agents if present
	if bundle.CustomAgents != nil && agentSettings != nil {
		// Merge custom agents
		for _, agent := range bundle.CustomAgents.Agents {
			// Check if agent exists
			existing := agentSettings.GetAgent(agent.ID)
			if existing != nil {
				// Update existing
				agentSettings.UpdateAgent(agent.ID, agent)
			} else {
				// Create new
				agentSettings.CreateAgent(agent)
			}
		}

		// Set default agent
		if bundle.ActiveAgent != "" {
			agentSettings.SetDefaultAgent(bundle.ActiveAgent)
		}

		// Save agents
		if err := agentSettings.save(); err != nil {
			return fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.save_agents_failed"), err)
		}
	}

	return nil
}

// BundleInfo contains summary information about a bundle (for listing)
type BundleInfo struct {
	Filename    string
	Name        string
	Description string
	CreatedAt   time.Time
	Author      string
	Tags        []string
}

// Validate checks if the bundle is valid
func (b *AgentConfigBundle) Validate() error {
	if b.Version == "" {
		return fmt.Errorf("%s", i18n.T("settings.bundle.error.version_required"))
	}

	if b.Name == "" {
		return fmt.Errorf("%s", i18n.T("settings.bundle.error.name_required"))
	}

	// At least one configuration type must be present
	if b.Profiles == nil && b.CustomAgents == nil {
		return fmt.Errorf("%s", i18n.T("settings.bundle.error.content_required"))
	}

	// Validate profiles if present
	if b.Profiles != nil {
		if err := b.Profiles.Validate(); err != nil {
			return fmt.Errorf("%s: %w", i18n.T("settings.bundle.error.profiles_invalid"), err)
		}
	}

	// Validate custom agents if present
	if b.CustomAgents != nil {
		// Basic validation - check that agents have required fields
		for i, agent := range b.CustomAgents.Agents {
			if agent.ID == "" {
				return fmt.Errorf("%s", i18n.T("settings.bundle.error.agent_id_missing", i))
			}
			// Provider/Model are optional - agents can rely on profiles for model selection
			_ = agent.Provider
			_ = agent.Model
		}
	}

	return nil
}

// Clone creates a deep copy of the bundle
func (b *AgentConfigBundle) Clone() *AgentConfigBundle {
	clone := &AgentConfigBundle{
		Version:       b.Version,
		Name:          b.Name,
		Description:   b.Description,
		CreatedAt:     b.CreatedAt,
		Author:        b.Author,
		ActiveProfile: b.ActiveProfile,
		ActiveAgent:   b.ActiveAgent,
	}

	// Deep copy tags
	if len(b.Tags) > 0 {
		clone.Tags = make([]string, len(b.Tags))
		copy(clone.Tags, b.Tags)
	}

	// Deep copy metadata
	if len(b.Metadata) > 0 {
		clone.Metadata = make(map[string]string)
		maps.Copy(clone.Metadata, b.Metadata)
	}

	// Note: Profiles and CustomAgents are pointers, would need deep copy if modifying
	clone.Profiles = b.Profiles
	clone.CustomAgents = b.CustomAgents

	return clone
}
