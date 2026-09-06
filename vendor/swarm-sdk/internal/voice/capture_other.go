//go:build !linux && !darwin

package voice

import (
	"context"
)

func init() {
	// No native capture methods available on other platforms
	// Users must implement their own AudioCapture
}

// FallbackCapture is a placeholder for platforms without native support
type FallbackCapture struct {
	*BaseCapture
}

// NewFallbackCapture creates a new fallback capture (unavailable)
func NewFallbackCapture(cfg *Config) (AudioCapture, error) {
	return &FallbackCapture{
		BaseCapture: NewBaseCapture(cfg),
	}, nil
}

// Name returns the capture method name
func (f *FallbackCapture) Name() string {
	return "No native capture available"
}

// IsAvailable always returns false for fallback
func (f *FallbackCapture) IsAvailable() bool {
	return false
}

// Initialize returns an error for fallback
func (f *FallbackCapture) Initialize() error {
	return ErrNoAudioCapture
}

// Start returns an error for fallback
func (f *FallbackCapture) Start(ctx context.Context) (<-chan []byte, error) {
	return nil, ErrNoAudioCapture
}

// Stop does nothing for fallback
func (f *FallbackCapture) Stop() error {
	return nil
}

// Close does nothing for fallback
func (f *FallbackCapture) Close() error {
	return nil
}

// DiscoverDevices returns empty list (not implemented)
func (f *FallbackCapture) DiscoverDevices() ([]DeviceInfo, error) {
	return nil, ErrNoAudioCapture
}

// SetDevice sets the device (not implemented)
func (f *FallbackCapture) SetDevice(deviceID string) error {
	return ErrNoAudioCapture
}

// GetDevice returns empty string (not implemented)
func (f *FallbackCapture) GetDevice() string {
	return ""
}

// GetAudioLevels returns zero levels (not implemented)
func (f *FallbackCapture) GetAudioLevels() AudioLevels {
	return AudioLevels{}
}

// StartLevelMonitoring starts monitoring (not implemented)
func (f *FallbackCapture) StartLevelMonitoring(ctx context.Context) error {
	return ErrNoAudioCapture
}

// StopLevelMonitoring stops monitoring (not implemented)
func (f *FallbackCapture) StopLevelMonitoring() error {
	return nil
}

// IsMonitoring returns false (not implemented)
func (f *FallbackCapture) IsMonitoring() bool {
	return false
}
