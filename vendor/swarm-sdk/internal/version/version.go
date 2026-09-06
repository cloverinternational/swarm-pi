// Package version provides version information for the SwarmOS SDK.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
)

// Version constants for the SDK
const (
	// Version is the semantic version of the SDK
	Version = "v0.2-L"

	// APIVersion is the version of the SDK API
	APIVersion = "v1"
)

// Build-time variables (can be set via -ldflags)
var (
	// GitCommit is the git commit hash of the SDK
	GitCommit = "unknown"

	// BuildTime is the build timestamp
	BuildTime = "unknown"
)

var (
	vcsBuildInfoOnce sync.Once
	vcsRevision      string
	vcsTime          string
	vcsModified      bool
	vcsModifiedKnown bool
)

func readVCSBuildInfo() {
	vcsBuildInfoOnce.Do(func() {
		info, ok := debug.ReadBuildInfo()
		if !ok {
			return
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				vcsRevision = setting.Value
			case "vcs.time":
				vcsTime = setting.Value
			case "vcs.modified":
				vcsModifiedKnown = true
				vcsModified = setting.Value == "true"
			}
		}
	})
}

// EffectiveGitCommit returns an explicit linker value when present, otherwise
// the VCS revision embedded by the Go toolchain. Dirty builds are marked.
func EffectiveGitCommit() string {
	if GitCommit != "" && GitCommit != "unknown" {
		return GitCommit
	}
	readVCSBuildInfo()
	if vcsRevision == "" {
		return "unknown"
	}
	if EffectiveBuildDirty() {
		return vcsRevision + "-dirty"
	}
	return vcsRevision
}

// EffectiveBuildTime returns an explicit linker value when present, otherwise
// the timestamp embedded by the Go toolchain.
func EffectiveBuildTime() string {
	if BuildTime != "" && BuildTime != "unknown" {
		return BuildTime
	}
	readVCSBuildInfo()
	if vcsTime == "" {
		return "unknown"
	}
	return vcsTime
}

// EffectiveBuildDirty reports the toolchain's embedded vcs.modified setting.
func EffectiveBuildDirty() bool {
	readVCSBuildInfo()
	return vcsModifiedKnown && vcsModified
}

// Info returns formatted version information for the SDK
func Info() string {
	return fmt.Sprintf("SwarmOS SDK %s (API: %s, commit: %s, built: %s, go: %s)",
		Version, APIVersion, EffectiveGitCommit(), EffectiveBuildTime(), runtime.Version())
}

// UserAgent returns a user agent string for SDK HTTP requests
func UserAgent() string {
	return fmt.Sprintf("SwarmOS-SDK/%s (%s/%s)", Version, runtime.GOOS, runtime.GOARCH)
}

// IsCompatibleAPI checks if the given API version is compatible with this SDK
func IsCompatibleAPI(apiVersion string) bool {
	// For now, we only support exact version match
	// In the future, we might support backward compatibility
	return apiVersion == APIVersion
}
