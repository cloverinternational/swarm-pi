package browser

import (
	"context"
	"maps"
	"os"
	"sync"
	"syscall"
	"time"
)

// ProcessInfo holds information about a managed browser process
type ProcessInfo struct {
	PID        int
	SessionID  string
	ToolType   string // "agent-browser" | "chromedp"
	StartTime  time.Time
	CancelFunc context.CancelFunc
	process    *os.Process // Stored for proper signal management
}

// Registry manages browser process lifecycle
type Registry struct {
	mu        sync.RWMutex
	processes map[string]*ProcessInfo
}

var globalRegistry = &Registry{
	processes: make(map[string]*ProcessInfo),
}

// Global returns the singleton registry
func Global() *Registry {
	return globalRegistry
}

// Register adds a process to the registry
func (r *Registry) Register(info *ProcessInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.processes[info.SessionID] = info
}

// Unregister removes a process from the registry
func (r *Registry) Unregister(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.processes, sessionID)
}

// GetActive returns all active browser processes
func (r *Registry) GetActive() []*ProcessInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*ProcessInfo, 0, len(r.processes))
	for _, p := range r.processes {
		result = append(result, p)
	}
	return result
}

// CleanupAll terminates all registered browser processes
func (r *Registry) CleanupAll() error {
	r.mu.Lock()
	processes := make(map[string]*ProcessInfo, len(r.processes))
	maps.Copy(processes, r.processes)
	r.mu.Unlock()

	var lastErr error
	for _, p := range processes {
		// Cancel context first (graceful)
		if p.CancelFunc != nil {
			p.CancelFunc()
		}

		// Try to find and terminate the process
		if p.process != nil {
			// Try graceful termination
			_ = p.process.Signal(syscall.SIGTERM)
			time.Sleep(100 * time.Millisecond)

			// Force kill if still running
			_ = p.process.Kill()
		} else if p.PID > 0 {
			// Fallback: find process by PID
			proc, err := os.FindProcess(p.PID)
			if err == nil && proc != nil {
				_ = proc.Signal(syscall.SIGTERM)
				time.Sleep(100 * time.Millisecond)
				_ = proc.Kill()
			}
		}

		r.Unregister(p.SessionID)
	}

	return lastErr
}

// StoreProcess stores the os.Process for a given session after process starts
func (r *Registry) StoreProcess(sessionID string, proc *os.Process) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.processes[sessionID]; ok {
		p.process = proc
	}
}
