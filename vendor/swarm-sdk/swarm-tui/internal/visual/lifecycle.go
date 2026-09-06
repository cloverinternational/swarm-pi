package visual

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// LockInfo is written to server.json so other processes can detect a running server.
type LockInfo struct {
	Port      int    `json:"port"`
	PID       int    `json:"pid"`
	SessionID string `json:"sessionID"`
	StartedAt int64  `json:"started_at"`
	AuthToken string `json:"auth_token,omitempty"`
}

// LockFilePath returns the standard server.json path inside a session's visual dir.
func LockFilePath(sessionVisualDir string) string {
	return filepath.Join(sessionVisualDir, "server.json")
}

// WriteLockFile atomically writes LockInfo as JSON.
func WriteLockFile(path string, info LockInfo) error {
	if info.StartedAt == 0 {
		info.StartedAt = time.Now().Unix()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadLockFile returns LockInfo or an error.
func ReadLockFile(path string) (LockInfo, error) {
	var info LockInfo
	data, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, fmt.Errorf("parse lock file: %w", err)
	}
	return info, nil
}

// ReapLockFile deletes the lock file. No-op if absent.
func ReapLockFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// IsStale returns true if the lock file is missing, malformed, or the owning
// process isn't alive / not responding to /health within timeoutMS.
func IsStale(path string, timeoutMS int) bool {
	info, err := ReadLockFile(path)
	if err != nil {
		return true
	}
	if !isProcessAlive(info.PID) {
		return true
	}
	client := http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", info.Port))
	if err != nil {
		return true
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return true
	}
	var body struct {
		SessionID string `json:"sessionID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return true
	}
	return body.SessionID != info.SessionID
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
