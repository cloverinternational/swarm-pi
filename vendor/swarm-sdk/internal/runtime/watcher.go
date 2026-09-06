package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// RuntimeWatcher watches a runtime config file and reloads on changes
type RuntimeWatcher struct {
	config   *RuntimeConfig
	logger   observability.Logger
	interval time.Duration

	mu       sync.RWMutex
	stopChan chan struct{}
	stopped  bool
}

// NewRuntimeWatcher creates a new runtime config watcher
func NewRuntimeWatcher(configPath string, logger observability.Logger) (*RuntimeWatcher, error) {
	config, err := NewRuntimeConfig(configPath)
	if err != nil {
		return nil, err
	}

	return &RuntimeWatcher{
		config:   config,
		logger:   logger,
		interval: 5 * time.Second, // Check every 5 seconds
		stopChan: make(chan struct{}),
	}, nil
}

// Start begins watching the config file
func (rw *RuntimeWatcher) Start(ctx context.Context) {
	go rw.watch(ctx)
}

// Stop stops watching the config file
func (rw *RuntimeWatcher) Stop() {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if !rw.stopped {
		close(rw.stopChan)
		rw.stopped = true
	}
}

// GetConfig returns the current runtime config
func (rw *RuntimeWatcher) GetConfig() *RuntimeConfig {
	rw.mu.RLock()
	defer rw.mu.RUnlock()
	return rw.config
}

// watch is the main watch loop
func (rw *RuntimeWatcher) watch(ctx context.Context) {
	ticker := time.NewTicker(rw.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-rw.stopChan:
			return
		case <-ticker.C:
			rw.checkAndReload(ctx)
		}
	}
}

// checkAndReload checks if the file changed and reloads if needed
func (rw *RuntimeWatcher) checkAndReload(ctx context.Context) {
	// This is a simplified version - in production would check file mtime
	if err := rw.config.Load(); err != nil {
		rw.logger.Warn(ctx, "runtime.config_reload_failed",
			observability.F("error", err.Error()))
	}
}
