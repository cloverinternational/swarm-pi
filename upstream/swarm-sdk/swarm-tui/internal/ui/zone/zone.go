// Package zone provides the disabled click-zone behavior used by the TUI.
//
// The application does not scan rendered output for zones, so markers have
// always been render-only no-ops and lookups cannot produce bounds. Keeping
// that behavior local avoids pulling Bubble Tea v1 into the v2 application.
package zone

import tea "charm.land/bubbletea/v2"

// Info is retained for call-site compatibility. Disabled zones never return an
// Info value from Get.
type Info struct{}

// NewGlobal initializes zone support. Zones are intentionally disabled.
func NewGlobal() {}

// SetEnabled is retained for compatibility with existing entrypoints.
func SetEnabled(bool) {}

// Mark returns content unchanged because the application does not scan zones.
func Mark(_ string, content string) string { return content }

// Get returns nil because disabled zones do not track rendered bounds.
func Get(string) *Info { return nil }

// InBounds always reports false for disabled zones.
func (*Info) InBounds(tea.Mouse) bool { return false }
