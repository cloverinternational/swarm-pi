package gitops

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	workspaceLeaseHeartbeat = 5 * time.Second
	workspaceLeaseMaxAge    = 20 * time.Second
)

type workspaceLeaseRecord struct {
	PID           int       `json:"pid"`
	ExecutionRoot string    `json:"execution_root"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type WorkspaceLease struct {
	path string
	root string
	stop chan struct{}
	once sync.Once
}

func RegisterWorkspaceLease(executionRoot string) (*WorkspaceLease, error) {
	root, err := workspaceLeaseStateDir()
	if err != nil {
		return nil, err
	}
	return registerWorkspaceLease(root, executionRoot)
}

func registerWorkspaceLease(stateDir, executionRoot string) (*WorkspaceLease, error) {
	canonical, err := filepath.Abs(executionRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace lease path: %w", err)
	}
	dir := filepath.Join(stateDir, workspaceLeaseKey(canonical))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create workspace lease directory: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%d.json", os.Getpid()))
	lease := &WorkspaceLease{path: path, root: canonical, stop: make(chan struct{})}
	if err := lease.touch(); err != nil {
		return nil, err
	}
	go lease.heartbeat()
	return lease, nil
}

func (l *WorkspaceLease) heartbeat() {
	ticker := time.NewTicker(workspaceLeaseHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = l.touch()
		case <-l.stop:
			return
		}
	}
}

func (l *WorkspaceLease) touch() error {
	record := workspaceLeaseRecord{PID: os.Getpid(), ExecutionRoot: l.root, UpdatedAt: time.Now().UTC()}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write workspace lease: %w", err)
	}
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish workspace lease: %w", err)
	}
	return nil
}

func (l *WorkspaceLease) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() { close(l.stop) })
	return os.Remove(l.path)
}

func CountActiveWorkspaceSessions(executionRoot string) int {
	stateDir, err := workspaceLeaseStateDir()
	if err != nil {
		return 0
	}
	return countActiveWorkspaceSessions(stateDir, executionRoot, time.Now())
}

func countActiveWorkspaceSessions(stateDir, executionRoot string, now time.Time) int {
	canonical, err := filepath.Abs(executionRoot)
	if err != nil {
		return 0
	}
	dir := filepath.Join(stateDir, workspaceLeaseKey(canonical))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) > workspaceLeaseMaxAge {
			_ = os.Remove(path)
			continue
		}
		var record workspaceLeaseRecord
		data, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(data, &record) != nil || filepath.Clean(record.ExecutionRoot) != filepath.Clean(canonical) {
			_ = os.Remove(path)
			continue
		}
		count++
	}
	return count
}

func workspaceLeaseStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".swarmos", "workspace-leases"), nil
}

func workspaceLeaseKey(path string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return hex.EncodeToString(sum[:12])
}
