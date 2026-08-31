// Package chat provides a non-blocking memory threshold watcher for the TUI.
//
// The watcher monitors runtime heap usage against soft thresholds (350MB, 500MB,
// 750MB, 1000MB).  When a threshold is crossed going UP it captures a heap
// profile and goroutine dump, logs the event, and optionally triggers GC + OS
// memory release.  The process is never blocked or killed.
//
// Design goals:
//   - Zero blocking: everything runs in a background goroutine.
//   - Configurable: thresholds, interval, and GC behaviour are tunable.
//   - Idempotent: a threshold fires once per upward crossing; resets when
//     memory drops back below the threshold.
//   - Observable: all events are logged with [MemWatch] prefix and written to
//     timestamped profiles under ~/.swarmos/profiles/.
package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sync"
	"time"
)

// ============================================================================
// DEFAULTS
// ============================================================================

var defaultThresholds = []Threshold{
	{LimitMB: 350, Label: "warm", TriggerGC: true, FreeOSMem: false},
	{LimitMB: 500, Label: "hot", TriggerGC: true, FreeOSMem: true},
	{LimitMB: 750, Label: "heavy", TriggerGC: true, FreeOSMem: true},
	{LimitMB: 1000, Label: "critical", TriggerGC: true, FreeOSMem: true},
}

const (
	defaultPollInterval = 30 * time.Second
	defaultOutputDir    = "" // resolved to ~/.swarmos/profiles
	defaultMaxProfiles  = 50 // keep last N heap profiles per threshold
)

// ============================================================================
// TYPES
// ============================================================================

// Threshold defines a single soft memory limit and the actions to take when
// crossed.
type Threshold struct {
	LimitMB   int    // Heap limit in megabytes
	Label     string // Human-readable label (warm, hot, heavy, critical)
	TriggerGC bool   // Call runtime.GC() on breach
	FreeOSMem bool   // Call debug.FreeOSMemory() on breach
}

// WatcherConfig configures the memory watcher.
type WatcherConfig struct {
	// Thresholds are checked in ascending order.  Zero or nil means use
	// the built-in defaults (350/500/750/1000 MB).
	Thresholds []Threshold

	// PollInterval is how often memory stats are sampled.  Zero means
	// 30 seconds.
	PollInterval time.Duration

	// OutputDir is where heap / goroutine profiles are written.  Zero
	// means ~/.swarmos/profiles.
	OutputDir string

	// MaxProfilesPerThreshold caps the number of retained profile files
	// per threshold to avoid unbounded disk growth.  Zero means 50.
	MaxProfilesPerThreshold int

	// OnBreach is an optional callback invoked on every threshold breach.
	// It receives the threshold that was crossed and current heap MB.
	// The callback must not block for long.
	OnBreach func(t Threshold, heapMB float64)
}

// watcherState tracks whether a threshold has been crossed in the current
// upward cycle.  It resets to false when memory drops back below the limit.
type watcherState struct {
	crossed   bool
	crossedAt time.Time
}

// MemoryWatcher monitors heap usage and captures profiles on threshold breach.
// Safe for concurrent use; Start / Stop may be called multiple times.
type MemoryWatcher struct {
	config       WatcherConfig
	thresholds   []Threshold
	states       map[int]*watcherState // key = LimitMB
	stopChan     chan struct{}
	mu           sync.Mutex
	running      bool
	lastActivity time.Time     // updated by RecordActivity
	idleGCDelay  time.Duration // how long idle before GC
}

// ============================================================================
// CONSTRUCTOR
// ============================================================================

// NewMemoryWatcher creates a watcher with the given configuration.  Nil or
// empty fields are filled from defaults.
func NewMemoryWatcher(cfg WatcherConfig) *MemoryWatcher {
	thresholds := cfg.Thresholds
	if len(thresholds) == 0 {
		thresholds = make([]Threshold, len(defaultThresholds))
		copy(thresholds, defaultThresholds)
	}

	interval := cfg.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}

	maxProf := cfg.MaxProfilesPerThreshold
	if maxProf <= 0 {
		maxProf = defaultMaxProfiles
	}

	outputDir := cfg.OutputDir
	if outputDir == "" {
		home, _ := os.UserHomeDir()
		outputDir = filepath.Join(home, ".swarmos", "profiles")
	}

	states := make(map[int]*watcherState, len(thresholds))
	for _, t := range thresholds {
		states[t.LimitMB] = &watcherState{}
	}

	return &MemoryWatcher{
		config: WatcherConfig{
			Thresholds:              thresholds,
			PollInterval:            interval,
			OutputDir:               outputDir,
			MaxProfilesPerThreshold: maxProf,
			OnBreach:                cfg.OnBreach,
		},
		thresholds:   thresholds,
		states:       states,
		stopChan:     make(chan struct{}),
		lastActivity: time.Now(),
		idleGCDelay:  10 * time.Second,
	}
}

// ============================================================================
// LIFECYCLE
// ============================================================================

// Start begins the background polling goroutine.  No-op if already running.
func (w *MemoryWatcher) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running {
		return
	}
	w.running = true
	w.stopChan = make(chan struct{})

	go w.loop()
	logDebug("[MemWatch] started · thresholds=%v · interval=%v · out=%s",
		w.thresholdLabels(), w.config.PollInterval, w.config.OutputDir)
}

// Stop halts the background goroutine.  No-op if not running.
func (w *MemoryWatcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.running {
		return
	}
	w.running = false
	close(w.stopChan)
}

// IsRunning reports whether the watcher is active.
func (w *MemoryWatcher) IsRunning() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.running
}

// ============================================================================
// INTERNAL LOOP
// ============================================================================

func (w *MemoryWatcher) loop() {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.check()
			w.maybeIdleGC()
		case <-w.stopChan:
			return
		}
	}
}

func (w *MemoryWatcher) check() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	heapMB := float64(ms.HeapAlloc) / 1024 / 1024

	for _, t := range w.thresholds {
		state := w.states[t.LimitMB]
		limitF := float64(t.LimitMB)

		if heapMB >= limitF && !state.crossed {
			// First upward crossing for this threshold in this cycle.
			state.crossed = true
			state.crossedAt = time.Now()
			w.fire(t, heapMB, ms)
		} else if heapMB < limitF && state.crossed {
			// Memory dropped back below — reset so we can fire again
			// on the next upward crossing.
			state.crossed = false
			logDebug("[MemWatch] · %s · recovered · heap=%.1fMB · limit=%dMB",
				t.Label, heapMB, t.LimitMB)
		}
	}
}

// RecordActivity marks the current time as the last known activity.
// Call this from the App whenever streaming or tool execution is active
// so idle GC does not fire during work.
func (w *MemoryWatcher) RecordActivity() {
	w.mu.Lock()
	w.lastActivity = time.Now()
	w.mu.Unlock()
}

// maybeIdleGC triggers GC + FreeOSMemory if the process has been idle
// for longer than idleGCDelay.  This addresses the "ants" hypothesis:
// goroutine pools and background buffers may hold memory between active
// bursts; an idle GC reclaims it without impacting latency.
func (w *MemoryWatcher) maybeIdleGC() {
	w.mu.Lock()
	idle := time.Since(w.lastActivity)
	w.mu.Unlock()

	if idle < w.idleGCDelay {
		return
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	pre := float64(ms.HeapAlloc) / 1024 / 1024

	start := time.Now()
	runtime.GC()
	debug.FreeOSMemory()
	elapsed := time.Since(start)

	runtime.ReadMemStats(&ms)
	post := float64(ms.HeapAlloc) / 1024 / 1024

	logDebug("[MemWatch] idle GC   heap %.1fMB -> %.1fMB   took %v   idle=%v",
		pre, post, elapsed, idle.Round(time.Second))
}

// ============================================================================
// BREACH HANDLER
// ============================================================================

func (w *MemoryWatcher) fire(t Threshold, heapMB float64, ms runtime.MemStats) {
	logDebug("[MemWatch] THRESHOLD BREACHED · %s · heap=%.1fMB · limit=%dMB · goroutines=%d · gc=%d",
		t.Label, heapMB, t.LimitMB, runtime.NumGoroutine(), ms.NumGC)

	// Capture profiles asynchronously so we never block the polling loop.
	go w.captureProfiles(t, heapMB)

	// Optional GC + OS memory release.
	if t.TriggerGC {
		start := time.Now()
		runtime.GC()
		logDebug("[MemWatch] · %s · GC took %v", t.Label, time.Since(start))
	}
	if t.FreeOSMem {
		debug.FreeOSMemory()
		logDebug("[MemWatch] · %s · FreeOSMemory called", t.Label)
	}

	// User callback.
	if w.config.OnBreach != nil {
		go w.config.OnBreach(t, heapMB)
	}
}

// ============================================================================
// PROFILE CAPTURE
// ============================================================================

func (w *MemoryWatcher) captureProfiles(t Threshold, heapMB float64) {
	ts := time.Now().Format("20060102-150405")
	label := t.Label

	// Heap profile
	heapPath := filepath.Join(w.config.OutputDir,
		fmt.Sprintf("heap-%s-%s-%.0fMB.prof", label, ts, heapMB))
	if err := w.writeHeapProfile(heapPath); err != nil {
		logDebug("[MemWatch] · %s · heap profile error: %v", label, err)
	} else {
		logDebug("[MemWatch] · %s · heap profile · %s", label, heapPath)
	}

	// Goroutine profile
	grPath := filepath.Join(w.config.OutputDir,
		fmt.Sprintf("goroutine-%s-%s.prof", label, ts))
	if err := w.writeGoroutineProfile(grPath); err != nil {
		logDebug("[MemWatch] · %s · goroutine profile error: %v", label, err)
	} else {
		logDebug("[MemWatch] · %s · goroutine profile · %s", label, grPath)
	}

	// Optional: clean old profiles to avoid disk bloat
	w.cleanupOldProfiles(label)
}

func (w *MemoryWatcher) writeHeapProfile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Force a GC for accurate in-use snapshot
	runtime.GC()
	return pprof.WriteHeapProfile(f)
}

func (w *MemoryWatcher) writeGoroutineProfile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return pprof.Lookup("goroutine").WriteTo(f, 2)
}

// cleanupOldProfiles removes the oldest heap/goroutine profiles for a given
// label once we exceed MaxProfilesPerThreshold.
func (w *MemoryWatcher) cleanupOldProfiles(label string) {
	max := w.config.MaxProfilesPerThreshold
	if max <= 0 {
		return
	}

	patterns := []string{
		fmt.Sprintf("heap-%s-*.prof", label),
		fmt.Sprintf("goroutine-%s-*.prof", label),
	}

	for _, pat := range patterns {
		matches, err := filepath.Glob(filepath.Join(w.config.OutputDir, pat))
		if err != nil || len(matches) <= max {
			continue
		}
		// Sort by mtime, oldest first
		oldest := w.oldestFiles(matches, len(matches)-max)
		for _, p := range oldest {
			_ = os.Remove(p)
		}
	}
}

func (w *MemoryWatcher) oldestFiles(paths []string, n int) []string {
	type fileInfo struct {
		path string
		mtim time.Time
	}
	infos := make([]fileInfo, len(paths))
	for i, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			infos[i] = fileInfo{path: p, mtim: time.Time{}}
			continue
		}
		infos[i] = fileInfo{path: p, mtim: st.ModTime()}
	}

	// Simple bubble sort by mtime (small N, so ok)
	for i := range infos {
		for j := i + 1; j < len(infos); j++ {
			if infos[j].mtim.Before(infos[i].mtim) {
				infos[i], infos[j] = infos[j], infos[i]
			}
		}
	}

	result := make([]string, 0, n)
	for i := 0; i < n && i < len(infos); i++ {
		result = append(result, infos[i].path)
	}
	return result
}

// ============================================================================
// HELPERS
// ============================================================================

func (w *MemoryWatcher) thresholdLabels() []string {
	out := make([]string, len(w.thresholds))
	for i, t := range w.thresholds {
		out[i] = fmt.Sprintf("%dMB(%s)", t.LimitMB, t.Label)
	}
	return out
}

// ============================================================================
// CONVENIENCE: App-level integration
// ============================================================================

// initMemoryWatcher creates and starts a watcher for this App instance.
// Called from NewAppWithOptions when opts.MemoryWatch is true.
func (a *App) initMemoryWatcher() {
	w := NewMemoryWatcher(WatcherConfig{})
	w.Start()
	a.memoryWatcher = w
}
