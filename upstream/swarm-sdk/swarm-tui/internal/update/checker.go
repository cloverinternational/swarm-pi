package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// GitHubAPIBase is the base URL for GitHub API
const GitHubAPIBase = "https://api.github.com"

// DefaultGitHubRepo is the default GitHub repository for releases
var DefaultGitHubRepo = "Swarm-Code/swarm-releases"

// Checker handles version checking against GitHub Releases
type Checker struct {
	repo        string
	platform    Platform
	httpClient  *http.Client
	githubToken string // Optional: GitHub token for private repos
	apiBaseURL  string
}

// NewChecker creates a new version checker
// Automatically reads GITHUB_TOKEN from environment if set
func NewChecker(repo string) *Checker {
	if repo == "" {
		repo = DefaultGitHubRepo
	}

	// Try to get GitHub token from environment
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}

	return &Checker{
		repo: repo,
		platform: Platform{
			OS:   runtime.GOOS,
			Arch: runtime.GOARCH,
		},
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		githubToken: token,
		apiBaseURL:  GitHubAPIBase,
	}
}

// CheckForUpdate checks if a newer version is available
// currentVersion should be in semver format (e.g., "v1.0.0")
func (c *Checker) CheckForUpdate(ctx context.Context, currentVersion string) (*CheckResult, error) {
	return c.CheckForUpdateChannel(ctx, currentVersion, ChannelStable)
}

// CheckForUpdateChannel checks the newest eligible release for a channel.
func (c *Checker) CheckForUpdateChannel(ctx context.Context, currentVersion string, channel ReleaseChannel) (*CheckResult, error) {
	release, err := c.getReleaseForChannel(ctx, channel)
	if err != nil {
		return &CheckResult{
			CurrentVersion: currentVersion,
			Mandatory:      false,
			Error:          err,
		}, err
	}

	// Normalize versions for comparison
	latestVersion := normalizeVersion(release.TagName)
	current := normalizeVersion(currentVersion)

	// Check if update is available
	updateAvailable, err := compareVersions(current, latestVersion)
	if err != nil {
		return &CheckResult{
			CurrentVersion: currentVersion,
			LatestVersion:  latestVersion,
			Release:        release,
			Error:          err,
		}, err
	}

	// Only mark as mandatory if there's actually an update available
	isMandatory := updateAvailable && release.Mandatory

	return &CheckResult{
		CurrentVersion:  currentVersion,
		LatestVersion:   latestVersion,
		UpdateAvailable: updateAvailable,
		Mandatory:       isMandatory,
		Release:         release,
	}, nil
}

func (c *Checker) getReleaseForChannel(ctx context.Context, channel ReleaseChannel) (*Release, error) {
	switch channel {
	case "", ChannelStable:
		return c.getLatestRelease(ctx)
	case ChannelBeta, ChannelNightly:
		releases, err := c.GetReleases(ctx, 50)
		if err != nil {
			return nil, err
		}

		var selected *Release
		var selectedVersion string
		for _, release := range releases {
			if release == nil || release.Draft {
				continue
			}
			version := normalizeVersion(release.TagName)
			if !semver.IsValid(version) {
				continue
			}
			if channel == ChannelNightly {
				if !release.Prerelease || !strings.Contains(strings.ToLower(version), "nightly") {
					continue
				}
			}
			if channel == ChannelBeta && strings.Contains(strings.ToLower(version), "nightly") {
				continue
			}
			if selected == nil || semver.Compare(version, selectedVersion) > 0 {
				selected = release
				selectedVersion = version
			}
		}
		if selected == nil {
			return nil, fmt.Errorf("no %s release is available", channel)
		}
		return selected, nil
	default:
		return nil, fmt.Errorf("unsupported release channel %q", channel)
	}
}

// getLatestRelease fetches the latest release from GitHub
func (c *Checker) getLatestRelease(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", c.apiBaseURL, c.repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers for GitHub API
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "SwarmOS-TUI")

	// Add authorization if token is available (for private repos)
	if c.githubToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.githubToken))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode release: %w", err)
	}
	if release.Draft || release.Prerelease {
		return nil, fmt.Errorf("latest release %q is not stable", release.TagName)
	}

	return &release, nil
}

// GetReleases fetches multiple releases for version selection
func (c *Checker) GetReleases(ctx context.Context, limit int) ([]*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d", c.apiBaseURL, c.repo, limit)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "SwarmOS-TUI")

	// Add authorization if token is available (for private repos)
	if c.githubToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.githubToken))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var releases []*Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("failed to decode releases: %w", err)
	}

	return releases, nil
}

// GetAssetForPlatform finds the asset for the current platform
func (c *Checker) GetAssetForPlatform(release *Release) (*Asset, *Asset, error) {
	if release == nil {
		return nil, nil, fmt.Errorf("release is required")
	}
	if err := c.platform.Validate(); err != nil {
		return nil, nil, err
	}

	binaryName := c.platform.ReleaseAssetName()
	checksumName := c.platform.ChecksumAssetName()
	var binaryAsset *Asset
	var checksumAsset *Asset

	for i := range release.Assets {
		asset := &release.Assets[i]

		if asset.Name == binaryName {
			if binaryAsset != nil {
				return nil, nil, fmt.Errorf("release contains duplicate asset %q", binaryName)
			}
			binaryAsset = asset
		}
		if asset.Name == checksumName {
			if checksumAsset != nil {
				return nil, nil, fmt.Errorf("release contains duplicate asset %q", checksumName)
			}
			checksumAsset = asset
		}
	}

	if binaryAsset == nil {
		return nil, nil, fmt.Errorf("release asset %q not found", binaryName)
	}
	if checksumAsset == nil {
		return nil, nil, fmt.Errorf("release checksum %q not found", checksumName)
	}

	return binaryAsset, checksumAsset, nil
}

// normalizeVersion removes 'tui/' prefix if present and ensures 'v' prefix
func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	// Remove 'tui/' prefix used in monorepo tags
	version = strings.TrimPrefix(version, "tui/")

	// Ensure 'v' prefix
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	return version
}

// isVersionNewer compares two semver versions
// Returns true if latest > current
func isVersionNewer(current, latest string) bool {
	newer, err := compareVersions(current, latest)
	return err == nil && newer
}

func compareVersions(current, latest string) (bool, error) {
	current = normalizeVersion(current)
	latest = normalizeVersion(latest)
	if !semver.IsValid(current) {
		return false, fmt.Errorf("invalid current version %q", current)
	}
	if !semver.IsValid(latest) {
		return false, fmt.Errorf("invalid latest version %q", latest)
	}
	return semver.Compare(latest, current) > 0, nil
}

// GetPlatform returns the current platform
func (c *Checker) GetPlatform() Platform {
	return c.platform
}
