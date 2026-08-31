package version

import "sync"

// ProductVersionFunc reports the version of the *product* embedding this SDK
// (for example the TUI's "v1.26.0"), or "" when it cannot be determined.
type ProductVersionFunc func() string

var (
	productMu       sync.RWMutex
	productResolver ProductVersionFunc
)

// SetProductVersionResolver installs the process-wide resolver used to stamp a
// product version onto artifacts that must be attributable to a specific build
// — today, the GitHub issues published by the `annoyed` tool.
//
// Why this is dependency-injected rather than a direct import: swarm-tui is
// DOWNSTREAM of this package (it lives under swarm-sdk/ and imports it), so
// reading swarm-tui's version constant from here would invert the dependency
// and make the SDK unbuildable without its own consumer. The composition root
// already knows both, so it installs the resolver at startup.
//
// The SDK deliberately does not fall back to its own Version constant when no
// resolver is installed: "v0.2-L" is the SDK's version, not the product's, and
// silently reporting one as the other would reintroduce exactly the ambiguity
// this stamp exists to remove. An absent product version is reported as absent.
func SetProductVersionResolver(fn ProductVersionFunc) {
	productMu.Lock()
	productResolver = fn
	productMu.Unlock()
}

// ProductVersion returns the embedding product's version, or "" when no
// resolver is installed or the resolver itself cannot determine one. It never
// panics and never blocks beyond the resolver call.
func ProductVersion() string {
	productMu.RLock()
	fn := productResolver
	productMu.RUnlock()
	if fn == nil {
		return ""
	}
	return fn()
}
