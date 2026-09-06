package update

import (
	"context"
	"fmt"
	"sync"

	tea "charm.land/bubbletea/v2"
)

// Updater is the main entry point for auto-update functionality
// It coordinates checking, downloading, verifying, and replacing
type Updater struct {
	checker    *Checker
	downloader *Downloader
	verifier   *Verifier
	replacer   *Replacer
	config     *ConfigManager

	mu          sync.RWMutex
	checking    bool
	downloading bool
	// Background download state
	bgDownloadResult *DownloadResult
	bgDownloadCtx    context.Context
	bgDownloadCancel context.CancelFunc
}

// NewUpdater creates a new updater instance
func NewUpdater(gitHubRepo string) (*Updater, error) {
	config, err := NewConfigManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create config manager: %w", err)
	}

	return &Updater{
		checker:    NewChecker(gitHubRepo),
		downloader: NewDownloader(),
		verifier:   NewVerifier(),
		replacer:   NewReplacer(),
		config:     config,
	}, nil
}

// CheckAsync performs an async version check
// It returns a channel that will receive the result
func (u *Updater) CheckAsync(ctx context.Context, currentVersion string) <-chan *CheckResult {
	resultChan := make(chan *CheckResult, 1)

	go func() {
		defer close(resultChan)

		u.mu.Lock()
		if u.checking {
			u.mu.Unlock()
			resultChan <- &CheckResult{Error: fmt.Errorf("check already in progress")}
			return
		}
		u.checking = true
		u.mu.Unlock()

		defer func() {
			u.mu.Lock()
			u.checking = false
			u.mu.Unlock()
		}()

		channel := u.config.Get().Channel
		result, err := u.checker.CheckForUpdateChannel(ctx, currentVersion, channel)
		if err != nil {
			resultChan <- result
			return
		}
		if result.UpdateAvailable && u.config.ShouldSkipVersion(result.LatestVersion) {
			result.UpdateAvailable = false
			result.Mandatory = false
		}

		// Update last check time
		_ = u.config.UpdateLastCheck()

		resultChan <- result
	}()

	return resultChan
}

// ShouldCheck returns true if we should check for updates based on config
func (u *Updater) ShouldCheck() bool {
	return u.config.ShouldCheck()
}

// ShouldNotify returns true if we should notify the user about this version
func (u *Updater) ShouldNotify(version string) bool {
	return u.config.ShouldNotify(version)
}

// MarkNotified records the release version shown to the user.
func (u *Updater) MarkNotified(version string) error {
	return u.config.SetLastNotified(version)
}

// GetConfig returns the current update configuration
func (u *Updater) GetConfig() *Config {
	return u.config.Get()
}

// DownloadAsync downloads the update asynchronously with progress updates
func (u *Updater) DownloadAsync(ctx context.Context, release *Release, progressChan chan<- DownloadProgress) <-chan *DownloadResult {
	resultChan := make(chan *DownloadResult, 1)

	go func() {
		defer close(resultChan)

		u.mu.Lock()
		if u.downloading {
			u.mu.Unlock()
			resultChan <- &DownloadResult{Error: fmt.Errorf("download already in progress")}
			return
		}
		u.downloading = true
		u.mu.Unlock()

		defer func() {
			u.mu.Lock()
			u.downloading = false
			u.mu.Unlock()
		}()

		// Find the asset for current platform
		binaryAsset, checksumAsset, err := u.checker.GetAssetForPlatform(release)
		if err != nil {
			resultChan <- &DownloadResult{Error: err}
			return
		}

		// Download binary
		result, err := u.downloader.DownloadBinary(ctx, binaryAsset, progressChan)
		if err != nil {
			resultChan <- result
			return
		}

		// Get checksum
		expectedHash, err := u.downloader.DownloadChecksum(ctx, checksumAsset)
		if err != nil {
			result.Error = fmt.Errorf("checksum download failed: %w", err)
			resultChan <- result
			return
		}

		// Verify checksum
		if err := u.verifier.VerifyDownloadResult(result, expectedHash, ""); err != nil {
			result.Error = fmt.Errorf("verification failed: %w", err)
			resultChan <- result
			return
		}

		resultChan <- result
	}()

	return resultChan
}

// Apply applies the downloaded update and prepares for restart
// Returns the restart command that should be executed before exit
func (u *Updater) Apply(downloadResult *DownloadResult) (*ReplaceResult, error) {
	result, err := u.replacer.Replace(downloadResult.FilePath)
	if err != nil {
		return result, err
	}

	return result, nil
}

// Restart executes the restart command to launch the new version
func (u *Updater) Restart(result *ReplaceResult) error {
	return u.replacer.Restart(result.RestartCmd)
}

// SkipVersion marks a version to be skipped
func (u *Updater) SkipVersion(version string) error {
	return u.config.SkipVersion(version)
}

// SetUpdateMode changes the update mode
func (u *Updater) SetUpdateMode(mode UpdateMode) error {
	return u.config.SetMode(mode)
}

// GetPlatform returns the current platform
func (u *Updater) GetPlatform() Platform {
	return u.checker.GetPlatform()
}

// IsChecking returns true if a check is in progress
func (u *Updater) IsChecking() bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.checking
}

// IsDownloading returns true if a download is in progress
func (u *Updater) IsDownloading() bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.downloading
}

// UpdateAvailableMsg is a message sent when an update is available
// This can be used with bubbletea's Cmd system
type UpdateAvailableMsg struct {
	Result *CheckResult
}

// CheckForUpdateCmd returns a bubbletea command that checks for updates
func (u *Updater) CheckForUpdateCmd(currentVersion string) func() tea.Msg {
	return func() tea.Msg {
		ctx := context.Background()
		resultChan := u.CheckAsync(ctx, currentVersion)
		result := <-resultChan
		return UpdateAvailableMsg{Result: result}
	}
}

// DownloadProgressMsg is sent during download progress
type DownloadProgressMsg struct {
	Progress DownloadProgress
}

// DownloadCompleteMsg is sent when download is complete
type DownloadCompleteMsg struct {
	Result *DownloadResult
}

// DownloadCmd returns a bubbletea command that downloads the update
func (u *Updater) DownloadCmd(release *Release) func() tea.Msg {
	return func() tea.Msg {
		ctx := context.Background()
		progressChan := make(chan DownloadProgress, 10)
		resultChan := u.DownloadAsync(ctx, release, progressChan)

		// Wait for completion
		result := <-resultChan
		return DownloadCompleteMsg{Result: result}
	}
}

// findSignatureAsset finds the signature asset for the current platform
func (u *Updater) findSignatureAsset(release *Release) *Asset {
	platformName := u.checker.GetPlatform().AssetName()
	sigName := platformName + ".sig"

	for i := range release.Assets {
		asset := &release.Assets[i]
		if asset.Name == sigName {
			return asset
		}
	}
	return nil
}

// StartBackgroundDownload starts downloading the update in the background
// This should be called when an update is detected, before the user is prompted
func (u *Updater) StartBackgroundDownload(release *Release) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.downloading || u.bgDownloadResult != nil {
		return // Already downloading or already downloaded
	}

	u.downloading = true
	u.bgDownloadCtx, u.bgDownloadCancel = context.WithCancel(context.Background())

	go func() {
		defer func() {
			u.mu.Lock()
			u.downloading = false
			u.mu.Unlock()
		}()

		// Find the asset for current platform
		binaryAsset, checksumAsset, err := u.checker.GetAssetForPlatform(release)
		if err != nil {
			return
		}

		// Download binary (no progress channel for background)
		result, err := u.downloader.DownloadBinary(u.bgDownloadCtx, binaryAsset, nil)
		if err != nil {
			return
		}

		// Get checksum
		expectedHash, err := u.downloader.DownloadChecksum(u.bgDownloadCtx, checksumAsset)
		if err != nil {
			return
		}

		// Get signature (if available)
		var signatureHex string
		sigAsset := u.findSignatureAsset(release)
		if sigAsset != nil {
			signatureHex, _ = u.downloader.DownloadSignature(u.bgDownloadCtx, sigAsset)
		}

		// Verify checksum and signature
		if err := u.verifier.VerifyDownloadResult(result, expectedHash, signatureHex); err != nil {
			return
		}

		// Store the result
		u.mu.Lock()
		u.bgDownloadResult = result
		u.mu.Unlock()
	}()
}

// GetBackgroundDownloadResult returns the background download result if available
func (u *Updater) GetBackgroundDownloadResult() *DownloadResult {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.bgDownloadResult
}

// CancelBackgroundDownload cancels any ongoing background download
func (u *Updater) CancelBackgroundDownload() {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.bgDownloadCancel != nil {
		u.bgDownloadCancel()
		u.bgDownloadCancel = nil
	}
}

// ClearBackgroundDownload clears the cached background download result
func (u *Updater) ClearBackgroundDownload() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.bgDownloadResult = nil
}
