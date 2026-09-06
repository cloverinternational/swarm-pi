// Package version provides build-time version information.
// These variables are set via -ldflags at compile time.
package version

import (
	"fmt"
	"runtime"
	"strings"
	"time"
)

// Build-time variables (set via -ldflags)
var (
	// Version is the semantic version (e.g., "v1.1.11")
	Version = "v1.28.0"

	// GitCommit is the short git commit hash
	GitCommit = "unknown"

	// BuildTime is the ISO8601 timestamp of the build
	BuildTime = "unknown"

	// BuildID is a unique identifier for this build (commit-timestamp)
	BuildID = "unknown"

	// BuildNum is the monotonically increasing build number from commit count
	BuildNum = "0"
)

// Info returns a formatted version info string for logging.
func Info() string {
	return fmt.Sprintf("SwarmOS %s (commit: %s, built: %s, go: %s)",
		Version, GitCommit, BuildTime, runtime.Version())
}

// DisplayVersion returns the version for display (just the semver, no build number).
func DisplayVersion() string {
	return Version
}

// FullVersion returns the full version with build number for detailed logging.
func FullVersion() string {
	if BuildNum != "" && BuildNum != "0" {
		return fmt.Sprintf("%s+%s", Version, BuildNum)
	}
	return Version
}

// ShortInfo returns a compact build identifier.
func ShortInfo() string {
	return fmt.Sprintf("%s-%s", GitCommit, shortTime(BuildTime))
}

// LogFields returns version info as structured fields for logging.
func LogFields() map[string]interface{} {
	return map[string]interface{}{
		"version":    Version,
		"git_commit": GitCommit,
		"build_time": BuildTime,
		"build_id":   BuildID,
		"go_version": runtime.Version(),
		"os_arch":    fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// shortTime converts ISO8601 to a shorter format for display.
func shortTime(isoTime string) string {
	if isoTime == "unknown" {
		return "unknown"
	}
	// Try to parse and format to shorter string
	t, err := time.Parse(time.RFC3339, isoTime)
	if err != nil {
		// If it's already in a different format, just take first 16 chars
		if len(isoTime) > 16 {
			return isoTime[:16]
		}
		return isoTime
	}
	return t.Format("2006-01-02T15:04")
}

// BannerString returns a startup banner with build info.
func BannerString() string {
	var b strings.Builder
	b.WriteString("╔════════════════════════════════════════════════════╗\n")
	b.WriteString(fmt.Sprintf("║  SwarmOS %-42s ║\n", Version))
	b.WriteString(fmt.Sprintf("║  Build:  %-42s ║\n", BuildID))
	b.WriteString(fmt.Sprintf("║  Commit: %-42s ║\n", GitCommit))
	b.WriteString(fmt.Sprintf("║  Built:  %-42s ║\n", BuildTime))
	b.WriteString("╚════════════════════════════════════════════════════╝")
	return b.String()
}
