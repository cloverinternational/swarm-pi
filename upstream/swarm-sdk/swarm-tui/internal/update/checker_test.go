package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckerUsesPublicStableRelease(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/Swarm-Code/swarm-releases/releases/latest" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("anonymous update check sent authorization header %q", got)
		}
		_ = json.NewEncoder(w).Encode(Release{
			TagName: "v1.23.2",
			Name:    "SwarmOS TUI v1.23.2",
		})
	}))
	defer server.Close()

	checker := NewChecker("")
	checker.apiBaseURL = server.URL
	result, err := checker.CheckForUpdate(context.Background(), "v1.23.1")
	if err != nil {
		t.Fatalf("CheckForUpdate() error = %v", err)
	}
	if !result.UpdateAvailable {
		t.Fatal("expected update to be available")
	}
	if result.LatestVersion != "v1.23.2" {
		t.Fatalf("LatestVersion = %q, want v1.23.2", result.LatestVersion)
	}
	if checker.repo != DefaultGitHubRepo {
		t.Fatalf("repo = %q, want %q", checker.repo, DefaultGitHubRepo)
	}
}

func TestCheckerRejectsInvalidCurrentVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{TagName: "v1.23.2"})
	}))
	defer server.Close()

	checker := NewChecker("")
	checker.apiBaseURL = server.URL
	result, err := checker.CheckForUpdate(context.Background(), "dev")
	if err == nil {
		t.Fatal("expected invalid current version error")
	}
	if result == nil || result.Error == nil {
		t.Fatal("expected error to be preserved in check result")
	}
}

func TestCheckerSelectsReleaseChannelDeterministically(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/Swarm-Code/swarm-releases/releases" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]*Release{
			{TagName: "v1.24.0-nightly.2", Prerelease: true},
			{TagName: "v1.23.3-beta.1", Prerelease: true},
			{TagName: "v1.23.2"},
			{TagName: "not-semver", Prerelease: true},
		})
	}))
	defer server.Close()

	checker := NewChecker("")
	checker.apiBaseURL = server.URL

	beta, err := checker.CheckForUpdateChannel(context.Background(), "v1.23.1", ChannelBeta)
	if err != nil {
		t.Fatalf("beta check error = %v", err)
	}
	if beta.LatestVersion != "v1.23.3-beta.1" {
		t.Fatalf("beta latest = %q, want v1.23.3-beta.1", beta.LatestVersion)
	}

	nightly, err := checker.CheckForUpdateChannel(context.Background(), "v1.23.1", ChannelNightly)
	if err != nil {
		t.Fatalf("nightly check error = %v", err)
	}
	if nightly.LatestVersion != "v1.24.0-nightly.2" {
		t.Fatalf("nightly latest = %q, want v1.24.0-nightly.2", nightly.LatestVersion)
	}
}

func TestNormalizeVersion(t *testing.T) {
	tests := map[string]string{
		"v1.0.0":      "v1.0.0",
		"tui/v1.0.0":  "v1.0.0",
		"1.0.0":       "v1.0.0",
		" v1.23.2 \n": "v1.23.2",
	}
	for input, want := range tests {
		if got := normalizeVersion(input); got != want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsVersionNewerSemverOrdering(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"v1.23.1", "v1.23.2", true},
		{"v1.23.2", "v1.23.2", false},
		{"v1.23.3", "v1.23.2", false},
		{"v1.23.2-beta.1", "v1.23.2", true},
		{"v1.23.2", "v1.23.3-beta.1", true},
		{"invalid", "v1.23.2", false},
	}
	for _, tt := range tests {
		if got := isVersionNewer(tt.current, tt.latest); got != tt.want {
			t.Errorf("isVersionNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestGetAssetForPublishedPlatforms(t *testing.T) {
	platforms := []Platform{
		{OS: "linux", Arch: "amd64"},
		{OS: "linux", Arch: "arm64"},
		{OS: "darwin", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
		{OS: "windows", Arch: "amd64"},
	}
	for _, platform := range platforms {
		t.Run(platform.String(), func(t *testing.T) {
			checker := NewChecker("")
			checker.platform = platform
			release := &Release{Assets: []Asset{
				{Name: platform.ReleaseAssetName(), DownloadURL: "https://example.invalid/binary"},
				{Name: platform.ChecksumAssetName(), DownloadURL: "https://example.invalid/checksum"},
			}}
			binary, checksum, err := checker.GetAssetForPlatform(release)
			if err != nil {
				t.Fatalf("GetAssetForPlatform() error = %v", err)
			}
			if binary.Name != platform.ReleaseAssetName() {
				t.Fatalf("binary = %q, want %q", binary.Name, platform.ReleaseAssetName())
			}
			if checksum.Name != platform.ChecksumAssetName() {
				t.Fatalf("checksum = %q, want %q", checksum.Name, platform.ChecksumAssetName())
			}
		})
	}
}

func TestGetAssetForPlatformFailsClosed(t *testing.T) {
	checker := NewChecker("")
	checker.platform = Platform{OS: "linux", Arch: "amd64"}

	t.Run("missing checksum", func(t *testing.T) {
		release := &Release{Assets: []Asset{{Name: checker.platform.ReleaseAssetName()}}}
		if _, _, err := checker.GetAssetForPlatform(release); err == nil {
			t.Fatal("expected missing checksum error")
		}
	})

	t.Run("generic fallback rejected", func(t *testing.T) {
		release := &Release{Assets: []Asset{
			{Name: "swarmos"},
			{Name: checker.platform.ChecksumAssetName()},
		}}
		if _, _, err := checker.GetAssetForPlatform(release); err == nil {
			t.Fatal("expected generic binary name to be rejected")
		}
	})

	t.Run("duplicate binary rejected", func(t *testing.T) {
		release := &Release{Assets: []Asset{
			{Name: checker.platform.ReleaseAssetName()},
			{Name: checker.platform.ReleaseAssetName()},
			{Name: checker.platform.ChecksumAssetName()},
		}}
		if _, _, err := checker.GetAssetForPlatform(release); err == nil {
			t.Fatal("expected duplicate binary error")
		}
	})

	t.Run("unsupported platform", func(t *testing.T) {
		checker.platform = Platform{OS: "windows", Arch: "arm64"}
		if _, _, err := checker.GetAssetForPlatform(&Release{}); err == nil {
			t.Fatal("expected unsupported platform error")
		}
	})
}
