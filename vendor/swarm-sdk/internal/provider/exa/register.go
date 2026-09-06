package exa

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// Register explicitly registers the Exa provider factory into the given
// registry. There is no init() side-effect and no global registry: callers
// must inject the registry they want the provider added to.
//
// Example:
//
//	reg := provider.NewSimpleRegistry(logger)
//	if err := exa.Register(reg); err != nil {
//	    // handle error
//	}
//	p, err := reg.Create(provider.Config{
//	    Name:   "exa",
//	    APIKey: os.Getenv("EXA_API_KEY"),
//	})
func Register(reg *provider.SimpleRegistry) error {
	return reg.Register("exa", func(cfg provider.Config) (provider.Provider, error) {
		return New(cfg)
	})
}

// RegisterExa explicitly registers the Exa provider factory in the given
// registry. It accepts any registry exposing Register so callers holding the
// provider.Registry interface (rather than the concrete *SimpleRegistry) can
// still wire the Exa provider.
//
// Deprecated: prefer Register(reg *provider.SimpleRegistry) for new code. This
// interface form is retained for callers that only have the Registry interface.
func RegisterExa(reg interface {
	Register(name string, factory provider.ProviderFactory) error
}) error {
	return reg.Register("exa", func(cfg provider.Config) (provider.Provider, error) {
		return New(cfg)
	})
}
