package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors skill directories for changes and triggers a reload
// when SKILL.md files are added, modified, or removed.
//
// It mirrors Claude Code's skillChangeDetector.ts, which uses chokidar
// with a 300ms debounce and file stability threshold. In Go, we use
// fsnotify directly with a debounce timer.
//
// CONTRACT:
//   - Start() must be called after the Loader is initialized
//   - Stop() must be called to release filesystem watcher resources
//   - The onChange callback is called on the goroutine that processes
//     fsnotify events; it must be safe to call from any goroutine
//   - Watched paths are the Loader's SearchPaths
type Watcher struct {
	loader    *Loader
	onChange  func()
	fsWatcher *fsnotify.Watcher
	done      chan struct{}

	// Debounce state
	debounceMu    sync.Mutex
	debounceTimer *time.Timer
}

// Constants matching Claude Code's skillChangeDetector.ts
const (
	// ReloadDebounceMs is the debounce window for skill change events.
	// Multiple rapid changes (e.g., git checkout) are coalesced into a
	// single reload. Mirrors RELOAD_DEBOUNCE_MS = 300 in Claude Code.
	ReloadDebounceMs = 300
)

// NewWatcher creates a new skill directory watcher.
//
// Parameters:
//   - loader: the skill loader whose registry will be reloaded on change
//   - onChange: callback invoked after a debounced skill change (e.g., to notify UI)
func NewWatcher(loader *Loader, onChange func()) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	return &Watcher{
		loader:    loader,
		onChange:  onChange,
		fsWatcher: fw,
		done:      make(chan struct{}),
	}, nil
}

// Start begins watching all skill search paths for changes.
// It recursively adds each search path and its subdirectories to the fsnotify
// watcher and starts the event processing goroutine.
func (w *Watcher) Start() error {
	// Add all search paths (recursively) to the watcher
	paths := w.loader.SearchPaths
	for _, p := range paths {
		if p == "" {
			continue
		}
		w.addRecursive(p)
	}

	// Start event processing goroutine
	go w.processEvents()

	return nil
}

// addRecursive walks the directory tree rooted at dir and adds each directory
// to the fsnotify watcher. This is required because fsnotify does not
// recursively watch subdirectories by default.
func (w *Watcher) addRecursive(dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}
		if info.IsDir() {
			_ = w.fsWatcher.Add(path)
		}
		return nil
	})
}

// Stop releases the filesystem watcher and stops the event loop.
// It is safe to call Stop() even if Start() was never called.
func (w *Watcher) Stop() {
	close(w.done)
	if w.fsWatcher != nil {
		w.fsWatcher.Close()
	}
	w.debounceMu.Lock()
	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}
	w.debounceMu.Unlock()
}

// processEvents reads fsnotify events and schedules debounced reloads.
// This runs in a dedicated goroutine started by Start().
func (w *Watcher) processEvents() {
	for {
		select {
		case <-w.done:
			return

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			// Log the error but continue watching
			_ = err
		}
	}
}

// handleEvent processes a single filesystem event.
// Only SKILL.md files trigger a reload; directory create events
// cause the watcher to recursively add the new directory.
func (w *Watcher) handleEvent(event fsnotify.Event) {
	base := filepath.Base(event.Name)

	// For newly created directories, add them recursively to the watcher
	if event.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			w.addRecursive(event.Name)
		}
	}

	// Only trigger reload for SKILL.md changes
	if base == "SKILL.md" {
		w.scheduleReload(event.Name)
	}
}

// scheduleReload debounces multiple rapid change events into a single reload.
// Mirrors Claude Code's scheduleReload function which uses a 300ms debounce.
func (w *Watcher) scheduleReload(changedPath string) {
	w.debounceMu.Lock()
	defer w.debounceMu.Unlock()

	// Cancel any pending reload
	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}

	// Schedule a new reload after the debounce window
	w.debounceTimer = time.AfterFunc(ReloadDebounceMs*time.Millisecond, func() {
		w.reload()
	})
}

// reload performs the actual skill registry reload.
// It clears the registry and re-discovers all skills from search paths.
func (w *Watcher) reload() {
	// Clear existing skills
	w.loader.Registry.Clear()

	// Re-register default skills (built-ins)
	if err := RegisterDefaultSkills(w.loader.Registry); err != nil {
		// Non-fatal
		_ = err
	}

	// Re-discover all skills from search paths
	if _, err := w.loader.Registry.DiscoverAll(); err != nil {
		// Non-fatal
		_ = err
	}

	// Notify listeners
	if w.onChange != nil {
		w.onChange()
	}
}
