// Package settings — ProfileManager shim.
//
// ProfileManager is now a thin wrapper around sdk/profiles.Manager.
// All business logic lives in the SDK package.  The TUI settings layer only
// adds the UI-facing glue (GetAvailableProfileIDs, etc.).
package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	sdkprofiles "github.com/Swarm-Code/mono/swarm-sdk/internal/profiles"
)

// ============================================================================
// PROFILE MANAGER — TUI wrapper around sdk/profiles.Manager
// ============================================================================

// ProfileManager wraps sdk/profiles.Manager with TUI-specific helpers.
type ProfileManager struct {
	*sdkprofiles.Manager
}

// NewProfileManager creates a ProfileManager backed by the SDK manager.
// Config path: ~/.swarmos/agent_profiles.json
func NewProfileManager() *ProfileManager {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	configDir := filepath.Join(home, ".swarmos")
	return &ProfileManager{Manager: sdkprofiles.NewManager(configDir)}
}

// LoadProfiles reads and returns a ProfilesConfig from an arbitrary path.
// Used by tests and import/export flows.
func (m *ProfileManager) LoadProfiles(path string) (ProfilesConfig, error) {
	tmp := sdkprofiles.NewManager(filepath.Dir(path))
	return *tmp.GetConfig(), nil
}

// SaveProfiles writes a ProfilesConfig to an arbitrary path.
func (m *ProfileManager) SaveProfiles(config ProfilesConfig, path string) error {
	return m.Manager.SaveProfiles(config, path)
}

// GetAvailableProfileIDs returns IDs of all profiles for dropdown menus.
func (m *ProfileManager) GetAvailableProfileIDs() []string {
	profiles := m.ListProfiles()
	ids := make([]string, len(profiles))
	for i, p := range profiles {
		ids[i] = p.ID
	}
	return ids
}

// ResolveAlias resolves an alias to a ModelPointer (legacy compat).
func (m *ProfileManager) ResolveAlias(alias ModelAlias) (ModelPointer, error) {
	return m.Manager.ResolveAlias(alias)
}

// ResolveChain resolves an alias to its full fallback.Chain.
func (m *ProfileManager) ResolveChain(alias ModelAlias) (*fallback.Chain, error) {
	return m.Manager.ResolveChain(alias)
}

// ResolveRoleConfig resolves an alias to its full RoleConfig.
func (m *ProfileManager) ResolveRoleConfig(alias ModelAlias) (RoleConfig, error) {
	return m.Manager.ResolveRoleConfig(alias)
}

// SetRoleInProfile updates a single role within a profile and saves.
func (m *ProfileManager) SetRoleInProfile(profileID string, alias ModelAlias, rc RoleConfig) error {
	return m.Manager.SetRoleInProfile(profileID, alias, rc)
}

// SetRetryPolicyInProfile updates the retry policy for a profile and saves.
func (m *ProfileManager) SetRetryPolicyInProfile(profileID string, policy *ProfileRetryPolicy) error {
	return m.Manager.SetRetryPolicyInProfile(profileID, policy)
}

// GetResolvedModel returns the resolved model information for a given profile and role alias.
// Returns: profileName, roleAlias, provider, model string
func (pm *ProfileManager) GetResolvedModel(profileID, roleAlias string) (string, string, string, string) {
	profile, err := pm.Manager.GetProfile(profileID)
	if err != nil {
		return profileID, roleAlias, "", ""
	}
	profileName := profile.Name

	chain, err := pm.Manager.ResolveChainForProfile(profileID, sdkprofiles.ModelAlias(roleAlias))
	if err != nil {
		return profileName, roleAlias, "", ""
	}

	return profileName, roleAlias, chain.Primary.Provider, chain.Primary.Model
}

// RenameProfile renames a profile by ID. Passes through to SDK manager.
func (m *ProfileManager) RenameProfile(id string, newName string) error {
	return m.Manager.RenameProfile(id, newName)
}

// ============================================================================
// HELPERS
// ============================================================================

// Ensure time is used (it's referenced indirectly via the SDK types).
var _ = time.Now
var _ = fmt.Sprintf
