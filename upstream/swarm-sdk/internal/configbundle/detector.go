// Package configbundle provides a unified configuration system for Swarm.
package configbundle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Detector handles detecting and monitoring for project config changes.
type Detector struct {
	currentPath    string
	currentWorkDir string
	onDetect       []func(*ProjectConfigInfo)
	onLost         []func()
	pollInterval   time.Duration
	cancel         context.CancelFunc
}

// DetectorOptions contains options for creating a Detector.
type DetectorOptions struct {
	// PollInterval is the interval for polling for config changes.
	// If 0, defaults to 1 second.
	PollInterval time.Duration
}

// ProjectConfigInfo contains information about a detected project config.
type ProjectConfigInfo struct {
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Exists      bool      `json:"exists"`
	DetectedAt  time.Time `json:"detectedAt"`
}

// NewDetector creates a new project config detector.
func NewDetector(opts DetectorOptions) *Detector {
	pollInterval := opts.PollInterval
	if pollInterval == 0 {
		pollInterval = time.Second
	}

	return &Detector{
		onDetect:     make([]func(*ProjectConfigInfo), 0),
		onLost:       make([]func(), 0),
		pollInterval: pollInterval,
	}
}

// OnDetect registers a callback for when a project config is detected.
func (d *Detector) OnDetect(callback func(*ProjectConfigInfo)) {
	d.onDetect = append(d.onDetect, callback)
}

// OnLost registers a callback for when a project config is lost (deleted or directory changed).
func (d *Detector) OnLost(callback func()) {
	d.onLost = append(d.onLost, callback)
}

// Start begins monitoring for project config changes.
func (d *Detector) Start(ctx context.Context, workDir string) {
	monitorCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.currentWorkDir = workDir

	// Initial check
	d.check(monitorCtx, workDir)

	// Start polling
	go d.poll(monitorCtx)
}

// Stop stops monitoring for project config changes.
func (d *Detector) Stop() {
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
}

// SetWorkDir changes the working directory being monitored.
func (d *Detector) SetWorkDir(workDir string) {
	d.currentWorkDir = workDir
	// Immediately check new directory
	d.check(context.Background(), workDir)
}

// GetCurrentPath returns the current project config path, or empty if none.
func (d *Detector) GetCurrentPath() string {
	return d.currentPath
}

// HasProjectConfig returns true if a project config is currently detected.
func (d *Detector) HasProjectConfig() bool {
	return d.currentPath != ""
}

// poll runs the polling loop.
func (d *Detector) poll(ctx context.Context) {
	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.check(ctx, d.currentWorkDir)
		}
	}
}

// check checks for project config in the current work directory.
func (d *Detector) check(ctx context.Context, workDir string) {
	newPath := FindProjectConfig(workDir)

	// Check if path changed
	if newPath != d.currentPath {
		oldPath := d.currentPath
		d.currentPath = newPath

		if newPath != "" {
			// New project config detected
			info, err := DetectProjectConfig(workDir)
			if err == nil && info != nil {
				for _, cb := range d.onDetect {
					cb(info)
				}
			}
		} else if oldPath != "" {
			// Lost project config
			for _, cb := range d.onLost {
				cb()
			}
		}
	}
}

// DetectProjectConfig checks if a project config exists in the given directory.
// Returns info about the config if found, or nil if not found.
func DetectProjectConfig(workDir string) (*ProjectConfigInfo, error) {
	path := FindProjectConfig(workDir)
	if path == "" {
		return &ProjectConfigInfo{Exists: false}, nil
	}

	// Read the config to get metadata
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Parse just the metadata we need
	var partial struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := parseJSON(data, &partial); err != nil {
		// If parsing fails, still return info with path
		return &ProjectConfigInfo{
			Path:       path,
			Name:       filepath.Base(filepath.Dir(path)),
			Exists:     true,
			DetectedAt: time.Now(),
		}, nil
	}

	return &ProjectConfigInfo{
		Path:        path,
		Name:        partial.Name,
		Description: partial.Description,
		Exists:      true,
		DetectedAt:  time.Now(),
	}, nil
}

// parseJSON is a helper to parse JSON data.
func parseJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// FindProjectConfigInDir searches for .swarm/config.json starting from the given directory.
// This is an alias for FindProjectConfig for API clarity.
func FindProjectConfigInDir(dir string) string {
	return FindProjectConfig(dir)
}

// Watch watches for project config changes and returns a channel.
// The channel receives the path when a config is detected, or empty string when lost.
func Watch(ctx context.Context, workDir string, pollInterval time.Duration) <-chan string {
	ch := make(chan string, 1)

	detector := NewDetector(DetectorOptions{PollInterval: pollInterval})
	detector.OnDetect(func(info *ProjectConfigInfo) {
		select {
		case ch <- info.Path:
		default:
		}
	})
	detector.OnLost(func() {
		select {
		case ch <- "":
		default:
		}
	})

	go func() {
		detector.Start(ctx, workDir)
		<-ctx.Done()
		detector.Stop()
		close(ch)
	}()

	return ch
}
