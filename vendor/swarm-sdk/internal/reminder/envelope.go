// Package reminder provides the dependency-neutral reminder envelope used by
// hooks and legacy prompt adapters.
package reminder

import (
	"fmt"
	"html"
	"strings"
)

// Wrap returns the canonical, machine-parseable system-reminder envelope.
func Wrap(source, kind string, seq int, body string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "unknown"
	}
	switch kind {
	case "nudge", "block", "review", "context":
	default:
		kind = "context"
	}
	if seq < 1 {
		seq = 1
	}
	return fmt.Sprintf(`<system-reminder source="%s" kind="%s" seq="%d">%s</system-reminder>`,
		html.EscapeString(source), kind, seq, strings.TrimSpace(body))
}
