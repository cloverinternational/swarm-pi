// Package update provides auto-update functionality for SwarmOS TUI.
// It handles version checking, downloading, verification, and binary replacement
// with support for Linux, macOS, and Windows platforms.
package update

import (
	"fmt"
	"time"
)

// UpdateMode defines how updates are handled
type UpdateMode string

const (
	// ModePrompt asks user before downloading (default)
	ModePrompt UpdateMode = "prompt"
	// ModeAutomatic downloads and applies on exit automatically
	ModeAutomatic UpdateMode = "automatic"
	// ModeManual only checks on-demand via /update command
	ModeManual UpdateMode = "manual"
	// ModeDisabled never checks for updates
	ModeDisabled UpdateMode = "disabled"
)

// ReleaseChannel defines which releases to consider
type ReleaseChannel string

const (
	ChannelStable  ReleaseChannel = "stable"
	ChannelBeta    ReleaseChannel = "beta"
	ChannelNightly ReleaseChannel = "nightly"
)

// Config holds update preferences
type Config struct {
	Mode          UpdateMode     `json:"mode"`
	Channel       ReleaseChannel `json:"channel"`
	CheckInterval time.Duration  `json:"check_interval"`
	LastCheck     time.Time      `json:"last_check"`
	SkipVersions  []string       `json:"skip_versions"`
	LastNotified  string         `json:"last_notified"` // Version we last notified about
	AutoRestart   bool           `json:"auto_restart"`  // Restart after update (default: true)
}

// DefaultConfig returns the default update configuration
func DefaultConfig() *Config {
	return &Config{
		Mode:          ModePrompt,
		Channel:       ChannelStable,
		CheckInterval: 6 * time.Hour,
		SkipVersions:  []string{},
		AutoRestart:   true,
	}
}

// Release represents a GitHub release
type Release struct {
	ID          int       `json:"id"`
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"` // Release notes / changelog
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Mandatory   bool      `json:"mandatory,omitempty"` // If true, this is a mandatory update
	CreatedAt   time.Time `json:"created_at"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []Asset   `json:"assets"`
}

// Asset represents a release asset (binary)
type Asset struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	DownloadURL string    `json:"browser_download_url"`
	CreatedAt   time.Time `json:"created_at"`
}

// CheckResult contains the result of a version check
type CheckResult struct {
	CurrentVersion  string
	LatestVersion   string
	UpdateAvailable bool
	Mandatory       bool // If true, user must update to continue
	Release         *Release
	Error           error
}

// DownloadProgress tracks download progress
type DownloadProgress struct {
	TotalBytes     int64
	Downloaded     int64
	Percent        float64
	BytesPerSecond float64
	ETA            time.Duration
	Done           bool
	Error          error
}

// Platform identifies the current platform
type Platform struct {
	OS   string // "linux", "darwin", "windows"
	Arch string // "amd64", "arm64"
}

// String returns the platform string in the format used for release assets
func (p Platform) String() string {
	switch p.OS {
	case "windows":
		return "windows-" + p.Arch
	case "darwin":
		return "darwin-" + p.Arch
	default:
		return "linux-" + p.Arch
	}
}

// Validate reports whether the release pipeline publishes this platform.
func (p Platform) Validate() error {
	switch p.OS {
	case "linux", "darwin":
		if p.Arch == "amd64" || p.Arch == "arm64" {
			return nil
		}
	case "windows":
		if p.Arch == "amd64" {
			return nil
		}
	}
	return fmt.Errorf("unsupported release platform %s/%s", p.OS, p.Arch)
}

// BinaryName returns the expected binary name for this platform.
func (p Platform) BinaryName() string {
	switch p.OS {
	case "windows":
		return "swarmos.exe"
	default:
		return "swarmos"
	}
}

// AssetName returns the expected asset name pattern for this platform
func (p Platform) AssetName() string {
	return "swarmos-" + p.String()
}

// ReleaseAssetName returns the exact executable name published in GitHub Releases.
func (p Platform) ReleaseAssetName() string {
	name := p.AssetName()
	if p.OS == "windows" {
		return name + ".exe"
	}
	return name
}

// ChecksumAssetName returns the expected checksum file name for this platform
func (p Platform) ChecksumAssetName() string {
	return p.ReleaseAssetName() + ".sha256"
}
