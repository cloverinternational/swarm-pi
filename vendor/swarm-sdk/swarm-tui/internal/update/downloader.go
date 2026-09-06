package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Downloader handles downloading release assets
type Downloader struct {
	httpClient *http.Client
}

// NewDownloader creates a new downloader
func NewDownloader() *Downloader {
	return &Downloader{
		httpClient: &http.Client{
			Timeout: 10 * time.Minute, // Long timeout for large binaries
		},
	}
}

// DownloadResult contains the result of a download operation
type DownloadResult struct {
	FilePath     string
	Size         int64
	SHA256       string
	Verified     bool
	VerifiedHash string
	Error        error
}

// DownloadBinary downloads the binary asset to a temporary file
// It returns a channel for progress updates
func (d *Downloader) DownloadBinary(ctx context.Context, asset *Asset, progressChan chan<- DownloadProgress) (*DownloadResult, error) {
	result := &DownloadResult{
		Size: asset.Size,
	}

	// Create temp directory for download
	tmpDir, err := os.MkdirTemp("", "swarmos-update-*")
	if err != nil {
		result.Error = fmt.Errorf("failed to create temp directory: %w", err)
		return result, result.Error
	}

	// Determine file name (handle archives)
	fileName := asset.Name
	filePath := filepath.Join(tmpDir, fileName)
	result.FilePath = filePath

	return d.downloadWithHTTP(ctx, asset, progressChan, result, filePath)
}

// downloadWithHTTP downloads using direct HTTP request
func (d *Downloader) downloadWithHTTP(ctx context.Context, asset *Asset, progressChan chan<- DownloadProgress, result *DownloadResult, filePath string) (*DownloadResult, error) {

	// Create the file
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		result.Error = fmt.Errorf("failed to create file: %w", err)
		return result, result.Error
	}
	defer file.Close()

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.DownloadURL, nil)
	if err != nil {
		result.Error = fmt.Errorf("failed to create request: %w", err)
		return result, result.Error
	}

	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "SwarmOS-TUI")

	// Execute request
	resp, err := d.httpClient.Do(req)
	if err != nil {
		result.Error = fmt.Errorf("failed to download: %w", err)
		return result, result.Error
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Errorf("download returned status %d", resp.StatusCode)
		return result, result.Error
	}

	// Setup progress tracking
	progress := DownloadProgress{
		TotalBytes: asset.Size,
		Done:       false,
	}

	startTime := time.Now()
	var downloaded int64

	// Create hash for checksum calculation
	hash := sha256.New()

	// Create multi-writer to write to both file and hash
	writer := io.MultiWriter(file, hash)

	// Download with progress
	buf := make([]byte, 32*1024) // 32KB buffer
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			written, writeErr := writer.Write(buf[:n])
			if writeErr != nil {
				result.Error = fmt.Errorf("failed to write file: %w", writeErr)
				return result, result.Error
			}
			downloaded += int64(written)

			// Update progress
			if progressChan != nil {
				elapsed := time.Since(startTime)
				if elapsed > 0 {
					progress.Downloaded = downloaded
					progress.Percent = float64(downloaded) / float64(progress.TotalBytes) * 100

					// Calculate speed
					bytesPerSecond := float64(downloaded) / elapsed.Seconds()
					progress.BytesPerSecond = bytesPerSecond

					// Calculate ETA
					if bytesPerSecond > 0 {
						remaining := float64(progress.TotalBytes-downloaded) / bytesPerSecond
						progress.ETA = time.Duration(remaining) * time.Second
					}
				}

				select {
				case progressChan <- progress:
				default:
					// Channel full, skip update
				}
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			result.Error = fmt.Errorf("download error: %w", err)
			return result, result.Error
		}
	}

	// Final progress update
	if progressChan != nil {
		progress.Downloaded = downloaded
		progress.Percent = 100
		progress.Done = true
		select {
		case progressChan <- progress:
		default:
		}
	}

	// Calculate and store hash
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))

	return result, nil
}

// DownloadChecksum downloads and parses the checksum file
func (d *Downloader) DownloadChecksum(ctx context.Context, asset *Asset) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.DownloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", "SwarmOS-TUI")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download checksum: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum download returned status %d", resp.StatusCode)
	}

	// Read checksum file
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil {
		return "", fmt.Errorf("failed to read checksum: %w", err)
	}
	if len(data) > 4096 {
		return "", fmt.Errorf("checksum response exceeds 4096 bytes")
	}

	// Parse checksum file (format: "hash  filename" or just "hash")
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return "", fmt.Errorf("empty checksum file")
	}

	// Get the hash from the first line
	parts := strings.Fields(lines[0])
	if len(parts) == 0 {
		return "", fmt.Errorf("invalid checksum format")
	}

	hash := strings.ToLower(parts[0])
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("invalid SHA-256 checksum")
	}
	return hash, nil
}

// ExtractArchive extracts a .tar.gz or .zip archive to the same directory
func (d *Downloader) ExtractArchive(archivePath string) (string, error) {
	dir := filepath.Dir(archivePath)

	if strings.HasSuffix(archivePath, ".tar.gz") {
		return d.extractTarGz(archivePath, dir)
	} else if strings.HasSuffix(archivePath, ".zip") {
		return d.extractZip(archivePath, dir)
	}

	// Not an archive, return as-is
	return archivePath, nil
}

// extractTarGz extracts a .tar.gz archive
func (d *Downloader) extractTarGz(archivePath, destDir string) (string, error) {
	// This requires archive/tar and compress/gzip
	// For simplicity, we'll skip this for now and assume binaries are not archived
	// In production, this would properly extract the archive
	return "", fmt.Errorf("tar.gz extraction not implemented - use raw binaries")
}

// extractZip extracts a .zip archive
func (d *Downloader) extractZip(archivePath, destDir string) (string, error) {
	// This requires archive/zip
	// For simplicity, we'll skip this for now and assume binaries are not archived
	// In production, this would properly extract the archive
	return "", fmt.Errorf("zip extraction not implemented - use raw binaries")
}

// DownloadSignature downloads and returns the signature file content
func (d *Downloader) DownloadSignature(ctx context.Context, asset *Asset) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.DownloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", "SwarmOS-TUI")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download signature: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("signature download returned status %d", resp.StatusCode)
	}

	// Read signature file (should be hex-encoded)
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read signature: %w", err)
	}

	return strings.TrimSpace(string(data)), nil
}
