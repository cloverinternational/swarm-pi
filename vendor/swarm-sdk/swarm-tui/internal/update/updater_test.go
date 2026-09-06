package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadAsyncRequiresPublishedChecksum(t *testing.T) {
	payload := []byte("verified swarmos release")
	sum := sha256.Sum256(payload)
	expected := hex.EncodeToString(sum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/binary":
			_, _ = w.Write(payload)
		case "/checksum":
			_, _ = w.Write([]byte(expected + "  swarmos-linux-amd64\n"))
		case "/broken-checksum":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case "/malformed-checksum":
			_, _ = w.Write([]byte("not-a-sha256\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	newUpdater := func() *Updater {
		checker := NewChecker("")
		checker.platform = Platform{OS: "linux", Arch: "amd64"}
		return &Updater{
			checker:    checker,
			downloader: &Downloader{httpClient: server.Client()},
			verifier:   NewVerifier(),
		}
	}

	t.Run("verified", func(t *testing.T) {
		release := &Release{Assets: []Asset{
			{Name: "swarmos-linux-amd64", DownloadURL: server.URL + "/binary", Size: int64(len(payload))},
			{Name: "swarmos-linux-amd64.sha256", DownloadURL: server.URL + "/checksum"},
		}}
		result := <-newUpdater().DownloadAsync(context.Background(), release, nil)
		if result.Error != nil {
			t.Fatalf("DownloadAsync() error = %v", result.Error)
		}
		if !result.Verified || result.VerifiedHash != expected {
			t.Fatalf("verification = (%v, %q), want (true, %q)", result.Verified, result.VerifiedHash, expected)
		}
		t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(result.FilePath)) })
	})

	t.Run("missing checksum asset", func(t *testing.T) {
		release := &Release{Assets: []Asset{
			{Name: "swarmos-linux-amd64", DownloadURL: server.URL + "/binary"},
		}}
		result := <-newUpdater().DownloadAsync(context.Background(), release, nil)
		if result.Error == nil {
			t.Fatal("expected missing checksum asset to fail")
		}
	})

	t.Run("checksum download failure", func(t *testing.T) {
		release := &Release{Assets: []Asset{
			{Name: "swarmos-linux-amd64", DownloadURL: server.URL + "/binary", Size: int64(len(payload))},
			{Name: "swarmos-linux-amd64.sha256", DownloadURL: server.URL + "/broken-checksum"},
		}}
		result := <-newUpdater().DownloadAsync(context.Background(), release, nil)
		if result.Error == nil {
			t.Fatal("expected checksum download failure")
		}
		if result.FilePath != "" {
			t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(result.FilePath)) })
		}
	})

	t.Run("malformed checksum", func(t *testing.T) {
		release := &Release{Assets: []Asset{
			{Name: "swarmos-linux-amd64", DownloadURL: server.URL + "/binary", Size: int64(len(payload))},
			{Name: "swarmos-linux-amd64.sha256", DownloadURL: server.URL + "/malformed-checksum"},
		}}
		result := <-newUpdater().DownloadAsync(context.Background(), release, nil)
		if result.Error == nil {
			t.Fatal("expected malformed checksum failure")
		}
		if result.FilePath != "" {
			t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(result.FilePath)) })
		}
	})
}

func TestCheckAsyncHonorsSkippedVersionAndNotification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{TagName: "v1.23.2"})
	}))
	defer server.Close()

	checker := NewChecker("")
	checker.apiBaseURL = server.URL
	checker.httpClient = server.Client()
	config := &ConfigManager{
		configPath: filepath.Join(t.TempDir(), "update.json"),
		config:     DefaultConfig(),
	}
	if err := config.SkipVersion("v1.23.2"); err != nil {
		t.Fatalf("SkipVersion() error = %v", err)
	}
	updater := &Updater{checker: checker, config: config}

	result := <-updater.CheckAsync(context.Background(), "v1.23.1")
	if result.Error != nil {
		t.Fatalf("CheckAsync() error = %v", result.Error)
	}
	if result.UpdateAvailable {
		t.Fatal("skipped version should not be offered")
	}

	if !updater.ShouldNotify("v1.23.3") {
		t.Fatal("new version should be eligible for notification")
	}
	if err := updater.MarkNotified("v1.23.3"); err != nil {
		t.Fatalf("MarkNotified() error = %v", err)
	}
	if updater.ShouldNotify("v1.23.3") {
		t.Fatal("recorded version should not notify twice")
	}
}
