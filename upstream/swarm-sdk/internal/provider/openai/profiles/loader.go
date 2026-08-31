// Package profiles provides provider profile management for OpenAI-compatible APIs.
package profiles

import (
	"embed"
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

//go:embed profiles.toml
var embeddedProfiles embed.FS

// Registry manages provider profiles.
type Registry struct {
	profiles map[string]Profile
}

// NewRegistry creates a new profile registry.
func NewRegistry() *Registry {
	return &Registry{
		profiles: make(map[string]Profile),
	}
}

// LoadBuiltinProfiles loads embedded provider profiles.
func LoadBuiltinProfiles() (*Registry, error) {
	data, err := embeddedProfiles.ReadFile("profiles.toml")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded profiles: %w", err)
	}

	var config struct {
		Profiles map[string]Profile `toml:"profiles"`
	}

	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse profiles: %w", err)
	}

	registry := NewRegistry()
	for name, profile := range config.Profiles {
		// Ensure name matches key
		profile.Name = name
		registry.profiles[name] = profile
	}

	return registry, nil
}

// Get retrieves a profile by name.
func (r *Registry) Get(name string) (Profile, bool) {
	profile, ok := r.profiles[name]
	return profile, ok
}

// Register adds a new profile to the registry.
func (r *Registry) Register(profile Profile) {
	r.profiles[profile.Name] = profile
}

// List returns all registered profile names.
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.profiles))
	for name := range r.profiles {
		names = append(names, name)
	}
	return names
}

// Has checks if a profile exists.
func (r *Registry) Has(name string) bool {
	_, ok := r.profiles[name]
	return ok
}
