// Package mode provides multi-agent orchestration through mode definitions.
package mode

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/fsnotify/fsnotify"
)

// Registry manages mode definitions with thread-safe operations and hot-reload.
type Registry interface {
	// Register adds or updates a mode in the registry
	Register(mode *Mode) error

	// Get retrieves a mode by name
	Get(name string) (*Mode, error)

	// List returns all registered mode names
	List() []string

	// Unregister removes a mode from the registry
	Unregister(name string) error

	// Exists checks if a mode is registered
	Exists(name string) bool

	// Watch starts watching a file for changes and auto-reloads
	Watch(path string) error

	// StopWatch stops watching a specific file
	StopWatch(path string) error

	// StopWatchAll stops all file watchers
	StopWatchAll() error

	// OnReload registers a callback for mode reload events
	OnReload(callback ReloadCallback)

	// GetVersion returns the version of a registered mode
	GetVersion(name string) (string, error)
}

// ReloadCallback is called when a mode is reloaded
type ReloadCallback func(oldMode, newMode *Mode)

// DefaultRegistry is the default implementation of Registry
type DefaultRegistry struct {
	mu        sync.RWMutex
	modes     map[string]*Mode
	watchers  map[string]*fileWatcher
	callbacks []ReloadCallback
	loader    *ModeLoader
	logger    observability.Logger
	tracer    observability.Tracer
	ctx       context.Context
	cancel    context.CancelFunc
}

// fileWatcher manages watching a single file
type fileWatcher struct {
	path     string
	watcher  *fsnotify.Watcher
	stopChan chan struct{}
	mode     *Mode // Last successfully loaded mode from this file
}

// NewRegistry creates a new mode registry
func NewRegistry(logger observability.Logger, tracer observability.Tracer) Registry {
	if logger == nil {
		logger = noop.NewLogger()
	}
	if tracer == nil {
		tracer = noop.NewTracer()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &DefaultRegistry{
		modes:     make(map[string]*Mode),
		watchers:  make(map[string]*fileWatcher),
		callbacks: make([]ReloadCallback, 0),
		loader:    NewModeLoader(logger, tracer),
		logger:    logger,
		tracer:    tracer,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Register adds or updates a mode in the registry
func (r *DefaultRegistry) Register(mode *Mode) error {
	if mode == nil {
		return fmt.Errorf("cannot register nil mode")
	}

	if mode.Name == "" {
		return fmt.Errorf("cannot register mode with empty name")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if mode already exists for reload callback
	oldMode, exists := r.modes[mode.Name]

	// Register the new mode
	r.modes[mode.Name] = mode

	// Log registration
	r.logger.Info(r.ctx, "mode.registry.registered",
		observability.F("mode", mode.Name),
		observability.F("version", mode.Version),
		observability.F("groups", len(mode.Groups)),
		observability.F("updated", exists))

	// Trigger reload callbacks if this was an update
	if exists {
		r.triggerReloadCallbacks(oldMode, mode)
	}

	return nil
}

// Get retrieves a mode by name
func (r *DefaultRegistry) Get(name string) (*Mode, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	mode, exists := r.modes[name]
	if !exists {
		return nil, fmt.Errorf("mode not found: %s", name)
	}

	// Return a copy to prevent external modification
	// Note: This is a shallow copy, deep copy might be needed for full isolation
	modeCopy := *mode
	return &modeCopy, nil
}

// List returns all registered mode names
func (r *DefaultRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.modes))
	for name := range r.modes {
		names = append(names, name)
	}

	return names
}

// Unregister removes a mode from the registry
func (r *DefaultRegistry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.modes[name]; !exists {
		return fmt.Errorf("mode not found: %s", name)
	}

	delete(r.modes, name)

	r.logger.Info(r.ctx, "mode.registry.unregistered",
		observability.F("mode", name))

	return nil
}

// Exists checks if a mode is registered
func (r *DefaultRegistry) Exists(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.modes[name]
	return exists
}

// Watch starts watching a file for changes and auto-reloads
func (r *DefaultRegistry) Watch(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Check if already watching
	r.mu.Lock()
	if _, exists := r.watchers[absPath]; exists {
		r.mu.Unlock()
		return fmt.Errorf("already watching: %s", absPath)
	}
	r.mu.Unlock()

	// Load initial mode
	mode, err := r.loader.LoadFromFile(absPath)
	if err != nil {
		return fmt.Errorf("failed to load initial mode: %w", err)
	}

	// Register the mode
	if err := r.Register(mode); err != nil {
		return fmt.Errorf("failed to register mode: %w", err)
	}

	// Create watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	// Add path to watcher
	if err := watcher.Add(absPath); err != nil {
		watcher.Close()
		return fmt.Errorf("failed to watch path: %w", err)
	}

	fw := &fileWatcher{
		path:     absPath,
		watcher:  watcher,
		stopChan: make(chan struct{}),
		mode:     mode,
	}

	r.mu.Lock()
	r.watchers[absPath] = fw
	r.mu.Unlock()

	// Start watching in goroutine
	go r.watchFile(fw)

	r.logger.Info(r.ctx, "mode.registry.watching",
		observability.F("path", absPath),
		observability.F("mode", mode.Name))

	return nil
}

// watchFile handles file change events
func (r *DefaultRegistry) watchFile(fw *fileWatcher) {
	defer fw.watcher.Close()

	// Debounce timer to avoid multiple reloads for rapid changes
	var debounceTimer *time.Timer

	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}

			// Only handle write events
			if event.Op&fsnotify.Write == fsnotify.Write {
				// Cancel previous debounce timer
				if debounceTimer != nil {
					debounceTimer.Stop()
				}

				// Set new debounce timer (500ms delay)
				debounceTimer = time.AfterFunc(500*time.Millisecond, func() {
					r.reloadMode(fw)
				})
			}

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			r.logger.Error(r.ctx, "mode.registry.watch.error",
				observability.F("path", fw.path),
				observability.F("error", err.Error()))

		case <-fw.stopChan:
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return

		case <-r.ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return
		}
	}
}

// reloadMode reloads a mode from file
func (r *DefaultRegistry) reloadMode(fw *fileWatcher) {
	r.logger.Info(r.ctx, "mode.registry.reloading",
		observability.F("path", fw.path))

	// Load new mode
	newMode, err := r.loader.LoadFromFile(fw.path)
	if err != nil {
		r.logger.Error(r.ctx, "mode.registry.reload.failed",
			observability.F("path", fw.path),
			observability.F("error", err.Error()))
		return
	}

	// Store old mode for callback
	oldMode := fw.mode

	// Update watcher's mode reference
	fw.mode = newMode

	// Register the new mode (will trigger callbacks)
	if err := r.Register(newMode); err != nil {
		r.logger.Error(r.ctx, "mode.registry.reload.register.failed",
			observability.F("path", fw.path),
			observability.F("mode", newMode.Name),
			observability.F("error", err.Error()))
		// Restore old mode reference on failure
		fw.mode = oldMode
		return
	}

	r.logger.Info(r.ctx, "mode.registry.reloaded",
		observability.F("path", fw.path),
		observability.F("mode", newMode.Name),
		observability.F("old_version", oldMode.Version),
		observability.F("new_version", newMode.Version))
}

// StopWatch stops watching a specific file
func (r *DefaultRegistry) StopWatch(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	r.mu.Lock()
	fw, exists := r.watchers[absPath]
	if !exists {
		r.mu.Unlock()
		return fmt.Errorf("not watching: %s", absPath)
	}

	delete(r.watchers, absPath)
	r.mu.Unlock()

	// Signal stop
	close(fw.stopChan)

	r.logger.Info(r.ctx, "mode.registry.stopped.watching",
		observability.F("path", absPath))

	return nil
}

// StopWatchAll stops all file watchers
func (r *DefaultRegistry) StopWatchAll() error {
	r.mu.Lock()
	watchers := make([]*fileWatcher, 0, len(r.watchers))
	paths := make([]string, 0, len(r.watchers))

	for path, fw := range r.watchers {
		watchers = append(watchers, fw)
		paths = append(paths, path)
	}

	// Clear watchers map
	r.watchers = make(map[string]*fileWatcher)
	r.mu.Unlock()

	// Stop all watchers
	for i, fw := range watchers {
		close(fw.stopChan)
		r.logger.Info(r.ctx, "mode.registry.stopped.watching",
			observability.F("path", paths[i]))
	}

	// Cancel context to stop any remaining goroutines
	r.cancel()

	return nil
}

// OnReload registers a callback for mode reload events
func (r *DefaultRegistry) OnReload(callback ReloadCallback) {
	if callback == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.callbacks = append(r.callbacks, callback)
}

// triggerReloadCallbacks calls all registered reload callbacks
func (r *DefaultRegistry) triggerReloadCallbacks(oldMode, newMode *Mode) {
	// Note: callbacks are called while holding the lock
	// This ensures consistency but callbacks should be fast
	for _, callback := range r.callbacks {
		// Run callbacks in goroutines to avoid blocking
		go func(cb ReloadCallback) {
			defer func() {
				if panicValue := recover(); panicValue != nil {
					r.logger.Error(r.ctx, "mode.registry.callback.panic",
						observability.F("panic", fmt.Sprintf("%v", panicValue)))
				}
			}()
			cb(oldMode, newMode)
		}(callback)
	}
}

// GetVersion returns the version of a registered mode
func (r *DefaultRegistry) GetVersion(name string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	mode, exists := r.modes[name]
	if !exists {
		return "", fmt.Errorf("mode not found: %s", name)
	}

	return mode.Version, nil
}

// Close cleanly shuts down the registry
func (r *DefaultRegistry) Close() error {
	return r.StopWatchAll()
}
