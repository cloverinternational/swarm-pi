package voice

import (
	"context"
	"sync"
)

// DeviceInfo contains information about an audio capture device
type DeviceInfo struct {
	// Name is the human-readable display name
	Name string
	// DeviceID is the system identifier (e.g., "hw:1,0" for ALSA, "default")
	DeviceID string
	// IsDefault indicates if this is the system default device
	IsDefault bool
	// IsAvailable indicates if the device is currently available
	IsAvailable bool
}

// AudioLevels contains real-time audio level information
type AudioLevels struct {
	// PeakDB is the instantaneous peak level in decibels (-60 to 0)
	PeakDB float32
	// RMSDB is the root-mean-square level over the measurement window
	RMSDB float32
	// IsActive indicates if audio signal is detected above threshold
	IsActive bool
}

// AudioCapture defines the interface for audio capture implementations
type AudioCapture interface {
	// Initialize sets up the audio capture device
	Initialize() error

	// Start begins capturing audio and returns a channel of audio chunks
	// The channel should be closed when the context is cancelled or Stop is called
	Start(ctx context.Context) (<-chan []byte, error)

	// Stop halts audio capture
	Stop() error

	// IsAvailable checks if this capture method works on the current system
	IsAvailable() bool

	// Close releases all resources
	Close() error

	// Name returns a human-readable name for the capture method
	Name() string

	// DiscoverDevices returns a list of available audio input devices
	// Returns an empty list if device discovery is not supported
	DiscoverDevices() ([]DeviceInfo, error)

	// SetDevice sets the device to use for capture
	// deviceID should be a DeviceID from DiscoverDevices()
	SetDevice(deviceID string) error

	// GetDevice returns the currently selected device ID
	GetDevice() string

	// GetAudioLevels returns the current audio levels during capture
	// Returns zero values if capture is not running
	GetAudioLevels() AudioLevels

	// StartLevelMonitoring starts capturing audio for level monitoring only
	// This is useful for testing microphone input without full transcription
	StartLevelMonitoring(ctx context.Context) error

	// StopLevelMonitoring stops level monitoring mode
	StopLevelMonitoring() error

	// IsMonitoring returns true if level monitoring is active
	IsMonitoring() bool
}

// AudioCaptureFunc is a factory function that creates an AudioCapture
type AudioCaptureFunc func(cfg *Config) (AudioCapture, error)

// captureRegistry holds registered capture methods in priority order
var (
	captureRegistry []AudioCaptureFunc
	captureMu       sync.RWMutex
)

// RegisterCapture registers an audio capture method
// Capture methods are tried in registration order (first registered = highest priority)
func RegisterCapture(fn AudioCaptureFunc) {
	captureMu.Lock()
	defer captureMu.Unlock()
	captureRegistry = append(captureRegistry, fn)
}

// NewAudioCapture creates the best available audio capture for the current system
// It tries each registered capture method in order until one is available
func NewAudioCapture(cfg *Config) (AudioCapture, error) {
	captureMu.RLock()
	defer captureMu.RUnlock()

	for _, fn := range captureRegistry {
		capture, err := fn(cfg)
		if err != nil {
			// Log error but continue trying other methods
			continue
		}
		if capture != nil && capture.IsAvailable() {
			return capture, nil
		}
		if capture != nil {
			capture.Close()
		}
	}

	return nil, ErrNoAudioCapture
}

// ListCaptures returns a list of available capture method names
func ListCaptures() []string {
	captureMu.RLock()
	defer captureMu.RUnlock()

	names := make([]string, 0, len(captureRegistry))
	for _, fn := range captureRegistry {
		// Create capture to get its name, then close it
		capture, err := fn(&Config{})
		if err != nil {
			continue
		}
		if capture != nil {
			names = append(names, capture.Name())
			capture.Close()
		}
	}
	return names
}

// CaptureInfo contains information about a capture method
type CaptureInfo struct {
	Name        string
	Available   bool
	Description string
}

// GetCaptureInfo returns information about all registered capture methods
func GetCaptureInfo() []CaptureInfo {
	captureMu.RLock()
	defer captureMu.RUnlock()

	infos := make([]CaptureInfo, 0, len(captureRegistry))
	for _, fn := range captureRegistry {
		capture, err := fn(&Config{})
		if err != nil {
			continue
		}
		if capture != nil {
			infos = append(infos, CaptureInfo{
				Name:      capture.Name(),
				Available: capture.IsAvailable(),
			})
			capture.Close()
		}
	}
	return infos
}

// BaseCapture provides common functionality for capture implementations
type BaseCapture struct {
	config     *Config
	running    bool
	monitoring bool
	mu         sync.RWMutex
	device     string
	levels     AudioLevels
	levelsMu   sync.RWMutex
}

// NewBaseCapture creates a new BaseCapture
func NewBaseCapture(cfg *Config) *BaseCapture {
	return &BaseCapture{
		config: cfg,
		device: "default",
	}
}

// Config returns the capture configuration
func (b *BaseCapture) Config() *Config {
	return b.config
}

// SetRunning sets the running state
func (b *BaseCapture) SetRunning(running bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.running = running
}

// IsRunning returns whether capture is running
func (b *BaseCapture) IsRunning() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.running
}

// SetMonitoring sets the monitoring state
func (b *BaseCapture) SetMonitoring(monitoring bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.monitoring = monitoring
}

// IsMonitoring returns whether level monitoring is active
func (b *BaseCapture) IsMonitoring() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.monitoring
}

// SetDevice sets the device ID
func (b *BaseCapture) SetDevice(deviceID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.device = deviceID
}

// GetDevice returns the current device ID
func (b *BaseCapture) GetDevice() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.device
}

// SetLevels sets the current audio levels
func (b *BaseCapture) SetLevels(levels AudioLevels) {
	b.levelsMu.Lock()
	defer b.levelsMu.Unlock()
	b.levels = levels
}

// GetLevels returns the current audio levels
func (b *BaseCapture) GetLevels() AudioLevels {
	b.levelsMu.RLock()
	defer b.levelsMu.RUnlock()
	return b.levels
}

// CalculateAudioLevels calculates RMS and peak levels from PCM audio data
// Expects 16-bit signed little-endian PCM data
func CalculateAudioLevels(pcmData []byte) AudioLevels {
	if len(pcmData) < 2 {
		return AudioLevels{PeakDB: -60, RMSDB: -60, IsActive: false}
	}

	// Convert bytes to 16-bit samples
	numSamples := len(pcmData) / 2
	if numSamples == 0 {
		return AudioLevels{PeakDB: -60, RMSDB: -60, IsActive: false}
	}

	var sumSquares float64
	var peakSample float64

	for i := range numSamples {
		// Read 16-bit signed little-endian sample
		sample := int16(pcmData[i*2]) | (int16(pcmData[i*2+1]) << 8)
		normalizedSample := float64(sample) / 32768.0

		sumSquares += normalizedSample * normalizedSample
		absSample := normalizedSample
		if absSample < 0 {
			absSample = -absSample
		}
		if absSample > peakSample {
			peakSample = absSample
		}
	}

	// Calculate RMS
	rms := 0.0
	if sumSquares > 0 {
		rms = sqrt(sumSquares / float64(numSamples))
	}

	// Convert to decibels
	// -60dB is effectively silent, 0dB is maximum
	peakDB := linearToDB(peakSample)
	rmsDB := linearToDB(rms)

	// Consider active if RMS is above -40dB (quiet speech threshold)
	isActive := rmsDB > -40

	return AudioLevels{
		PeakDB:   peakDB,
		RMSDB:    rmsDB,
		IsActive: isActive,
	}
}

// linearToDB converts a linear amplitude (0-1) to decibels
func linearToDB(amplitude float64) float32 {
	if amplitude <= 0 {
		return -60 // Minimum floor
	}
	// dB = 20 * log10(amplitude)
	db := 20 * log10(amplitude)
	if db < -60 {
		db = -60
	}
	if db > 0 {
		db = 0
	}
	return float32(db)
}

// sqrt returns the square root of x
func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Newton's method for square root
	z := x
	for range 100 {
		z = (z + x/z) / 2
	}
	return z
}

// log10 returns the base-10 logarithm of x
func log10(x float64) float64 {
	if x <= 0 {
		return -60
	}
	// Use natural log and convert
	ln := 0.0
	if x < 1 {
		// For values < 1, use approximation
		ln = -log10(1 / x)
	} else {
		// Taylor series approximation for ln(x) around 1
		y := (x - 1) / (x + 1)
		y2 := y * y
		ln = 2 * y * (1 + y2/3 + y2*y2/5 + y2*y2*y2/7 + y2*y2*y2*y2/9)
	}
	return ln / 2.302585092994046 // ln(10)
}
