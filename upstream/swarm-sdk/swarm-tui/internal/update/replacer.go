package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// MarkerFileName is the name of the marker file that indicates a pending update
const MarkerFileName = ".swarmos-update-pending"

// Replacer handles atomic binary replacement and process restart
type Replacer struct {
	platform Platform
}

// NewReplacer creates a new replacer
func NewReplacer() *Replacer {
	return &Replacer{
		platform: Platform{
			OS:   runtime.GOOS,
			Arch: runtime.GOARCH,
		},
	}
}

// ReplaceResult contains the result of a binary replacement
type ReplaceResult struct {
	OldPath    string
	NewPath    string
	BackupPath string
	MarkerPath string // Path to the pending update marker
	RestartCmd *exec.Cmd
	Error      error
}

// Replace performs atomic binary replacement
// The downloadedFile is the path to the new binary (already verified)
// After replacement, the returned RestartCmd can be used to restart the application
func (r *Replacer) Replace(downloadedFile string) (*ReplaceResult, error) {
	result := &ReplaceResult{
		NewPath: downloadedFile,
	}

	// Get current executable path
	currentPath, err := os.Executable()
	if err != nil {
		result.Error = fmt.Errorf("failed to get current executable: %w", err)
		return result, result.Error
	}

	// Resolve symlinks to get the real path
	realPath, err := filepath.EvalSymlinks(currentPath)
	if err != nil {
		realPath = currentPath
	}
	result.OldPath = realPath

	// Create backup path
	result.BackupPath = realPath + ".backup"

	// Create marker file path (in same directory as executable)
	result.MarkerPath = filepath.Join(filepath.Dir(realPath), MarkerFileName)

	switch r.platform.OS {
	case "windows":
		return r.replaceWindows(result, downloadedFile, realPath)
	default:
		return r.replaceUnix(result, downloadedFile, realPath)
	}
}

// replaceUnix handles binary replacement on Unix-like systems (Linux, macOS)
func (r *Replacer) replaceUnix(result *ReplaceResult, downloadedFile, realPath string) (*ReplaceResult, error) {
	// 1. Remove old backup if exists
	_ = os.Remove(result.BackupPath)

	// 2. Copy current binary to backup (not rename, as we might be on a different filesystem)
	if err := copyFile(realPath, result.BackupPath); err != nil {
		result.Error = fmt.Errorf("failed to create backup: %w", err)
		return result, result.Error
	}

	// 3. Ensure new binary is executable
	if err := os.Chmod(downloadedFile, 0755); err != nil {
		result.Error = fmt.Errorf("failed to set executable permission: %w", err)
		return result, result.Error
	}

	// 4. Create marker file BEFORE replacing binary
	// This indicates that we have a pending update that needs verification
	markerData := fmt.Sprintf("%s\n%s\n%d", result.BackupPath, realPath, time.Now().Unix())
	if err := os.WriteFile(result.MarkerPath, []byte(markerData), 0644); err != nil {
		result.Error = fmt.Errorf("failed to create marker file: %w", err)
		return result, result.Error
	}

	// 5. Rename new binary to current location
	// On Unix, renaming over an executable that's currently running works
	if err := os.Rename(downloadedFile, realPath); err != nil {
		// Try to restore backup
		_ = os.Rename(result.BackupPath, realPath)
		_ = os.Remove(result.MarkerPath)
		result.Error = fmt.Errorf("failed to replace binary: %w", err)
		return result, result.Error
	}

	// 6. Create restart command
	result.RestartCmd = r.createRestartCmd(realPath)

	return result, nil
}

// replaceWindows handles binary replacement on Windows
// Windows cannot overwrite a running executable, so we use a different approach:
// 1. Write new binary to exe.new
// 2. Create a helper script/batch file to rename after exit
// 3. Return command that will be executed after current process exits
func (r *Replacer) replaceWindows(result *ReplaceResult, downloadedFile, realPath string) (*ReplaceResult, error) {
	// 1. Remove old backup if exists
	_ = os.Remove(result.BackupPath)

	// 2. Copy current binary to backup
	if err := copyFile(realPath, result.BackupPath); err != nil {
		result.Error = fmt.Errorf("failed to create backup: %w", err)
		return result, result.Error
	}

	// 3. Copy new binary to .new location (Windows allows this)
	newPath := realPath + ".new"
	if err := copyFile(downloadedFile, newPath); err != nil {
		result.Error = fmt.Errorf("failed to copy new binary: %w", err)
		return result, result.Error
	}

	// 4. Create marker file BEFORE creating updater script
	markerData := fmt.Sprintf("%s\n%s\n%d", result.BackupPath, realPath, time.Now().Unix())
	if err := os.WriteFile(result.MarkerPath, []byte(markerData), 0644); err != nil {
		result.Error = fmt.Errorf("failed to create marker file: %w", err)
		return result, result.Error
	}

	// 5. Create a batch script to perform the rename after we exit
	scriptPath := realPath + ".updater.bat"
	script := fmt.Sprintf(`@echo off
timeout /t 2 /nobreak >nul
move /y "%s" "%s"
if exist "%s" del "%s"
start "" "%s"
del "%s"
`, newPath, realPath, result.BackupPath, result.BackupPath, realPath, scriptPath)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		result.Error = fmt.Errorf("failed to create updater script: %w", err)
		return result, result.Error
	}

	// 6. Create restart command that runs the batch script
	result.RestartCmd = exec.Command("cmd", "/c", scriptPath)
	result.RestartCmd.Stdout = os.Stdout
	result.RestartCmd.Stderr = os.Stderr
	result.RestartCmd.Stdin = os.Stdin

	return result, nil
}

// createRestartCmd creates a command to restart the application
func (r *Replacer) createRestartCmd(exePath string) *exec.Cmd {
	// Get current arguments (skip the program name)
	args := os.Args[1:]

	cmd := exec.Command(exePath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	cmd.SysProcAttr = restartSysProcAttr()

	return cmd
}

// Restart restarts the application using the restart command
// This should be called after a successful Replace
func (r *Replacer) Restart(cmd *exec.Cmd) error {
	if cmd == nil {
		return fmt.Errorf("no restart command provided")
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start new process: %w", err)
	}

	// Exit current process
	os.Exit(0)

	return nil // Never reached
}

// Rollback restores the backup binary
// This should be called if the new binary fails to start
func (r *Replacer) Rollback(result *ReplaceResult) error {
	if result.BackupPath == "" || result.OldPath == "" {
		return fmt.Errorf("no backup to rollback to")
	}

	// Remove the failed binary
	_ = os.Remove(result.OldPath)

	// Restore backup
	if err := os.Rename(result.BackupPath, result.OldPath); err != nil {
		return fmt.Errorf("failed to restore backup: %w", err)
	}

	// Remove marker file
	_ = os.Remove(result.MarkerPath)

	return nil
}

// CheckPendingUpdate checks if there's a pending update that needs verification
// Returns the marker data if a pending update exists, or nil if none
func CheckPendingUpdate() (*PendingUpdate, error) {
	currentPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to get current executable: %w", err)
	}

	realPath, err := filepath.EvalSymlinks(currentPath)
	if err != nil {
		realPath = currentPath
	}

	markerPath := filepath.Join(filepath.Dir(realPath), MarkerFileName)

	data, err := os.ReadFile(markerPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No pending update
		}
		return nil, fmt.Errorf("failed to read marker file: %w", err)
	}

	// Parse marker file
	lines := splitLines(string(data))
	if len(lines) < 3 {
		return nil, fmt.Errorf("invalid marker file format")
	}

	var timestamp int64
	fmt.Sscanf(lines[2], "%d", &timestamp)

	return &PendingUpdate{
		BackupPath: lines[0],
		BinaryPath: lines[1],
		Timestamp:  time.Unix(timestamp, 0),
		MarkerPath: markerPath,
	}, nil
}

// PendingUpdate represents a pending update that needs verification
type PendingUpdate struct {
	BackupPath string
	BinaryPath string
	Timestamp  time.Time
	MarkerPath string
}

// ClearMarker removes the pending update marker file
// This should be called after a successful startup verification
func (p *PendingUpdate) ClearMarker() error {
	if p.MarkerPath == "" {
		return nil
	}
	return os.Remove(p.MarkerPath)
}

// Rollback restores the backup binary for a pending update
func (p *PendingUpdate) Rollback() error {
	if p.BackupPath == "" || p.BinaryPath == "" {
		return fmt.Errorf("no backup to rollback to")
	}

	// Check if backup exists
	if _, err := os.Stat(p.BackupPath); os.IsNotExist(err) {
		return fmt.Errorf("backup file not found: %s", p.BackupPath)
	}

	// Remove the potentially failed binary
	_ = os.Remove(p.BinaryPath)

	// Restore backup
	if err := os.Rename(p.BackupPath, p.BinaryPath); err != nil {
		return fmt.Errorf("failed to restore backup: %w", err)
	}

	// Remove marker file
	_ = p.ClearMarker()

	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = dstFile.ReadFrom(srcFile)
	return err
}

// splitLines splits a string into lines
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
