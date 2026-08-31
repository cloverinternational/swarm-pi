//go:build darwin

package voice

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

func init() {
	// Register capture methods for macOS
	RegisterCapture(NewSoxCapture)
	RegisterCapture(NewRecAudioCapture)
}

// SoxCapture uses the 'rec' command (SoX) for audio capture on macOS
type SoxCapture struct {
	*BaseCapture
	cmd         *exec.Cmd
	cmdMu       sync.Mutex
	cmdDone     chan struct{}
	monitorCmd  *exec.Cmd
	monitorDone chan struct{}
}

// NewSoxCapture creates a new SoX-based audio capture
func NewSoxCapture(cfg *Config) (AudioCapture, error) {
	return &SoxCapture{
		BaseCapture: NewBaseCapture(cfg),
	}, nil
}

// Name returns the capture method name
func (s *SoxCapture) Name() string {
	return "SoX (rec command)"
}

// IsAvailable checks if SoX is available on the system
func (s *SoxCapture) IsAvailable() bool {
	_, err := exec.LookPath("rec")
	return err == nil
}

// Initialize prepares the capture (no-op for SoX)
func (s *SoxCapture) Initialize() error {
	if !s.IsAvailable() {
		return ErrSoxNotFound
	}
	return nil
}

// Start begins capturing audio using SoX
func (s *SoxCapture) Start(ctx context.Context) (<-chan []byte, error) {
	if !s.IsAvailable() {
		return nil, ErrSoxNotFound
	}

	if s.IsRunning() {
		return nil, ErrAlreadyRecording
	}

	audioChan := make(chan []byte, 100)

	// Build SoX command:
	// rec -t raw -r 16000 -c 1 -e signed -b 16 -
	args := []string{
		"-t", "raw", // Raw format
		"-r", strconv.Itoa(s.Config().SampleRate), // Sample rate
		"-c", strconv.Itoa(s.Config().Channels), // Channels
		"-e", "signed", // Signed encoding
		"-b", "16", // 16-bit
		"-q", // Quiet mode
		"-",  // Output to stdout
	}

	s.cmdMu.Lock()
	s.cmd = exec.CommandContext(ctx, "rec", args...)
	s.cmdDone = make(chan struct{})
	s.cmdMu.Unlock()

	stdout, err := s.cmd.StdoutPipe()
	if err != nil {
		close(audioChan)
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Redirect stderr to suppress SoX messages
	s.cmd.Stderr = nil

	if err := s.cmd.Start(); err != nil {
		close(audioChan)
		return nil, fmt.Errorf("failed to start rec command: %w", err)
	}

	s.SetRunning(true)

	go func() {
		defer close(audioChan)
		defer close(s.cmdDone)
		defer s.SetRunning(false)

		reader := bufio.NewReader(stdout)
		// Buffer size: ~128ms of audio at 16kHz 16-bit mono
		bufSize := s.Config().SampleRate * 2 / 8 // bytes per 125ms
		buf := make([]byte, bufSize)

		for {
			n, err := reader.Read(buf)
			if err != nil {
				return
			}

			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])

				select {
				case audioChan <- chunk:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return audioChan, nil
}

// Stop halts audio capture
func (s *SoxCapture) Stop() error {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil {
		if err := s.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill rec process: %w", err)
		}
		<-s.cmdDone
	}

	s.SetRunning(false)
	return nil
}

// Close releases resources
func (s *SoxCapture) Close() error {
	return s.Stop()
}

// DiscoverDevices returns a list of available audio input devices on macOS
func (s *SoxCapture) DiscoverDevices() ([]DeviceInfo, error) {
	// Use system_profiler to get audio devices
	cmd := exec.Command("system_profiler", "SPAudioDataType")
	output, err := cmd.Output()
	if err == nil {
		return s.parseSystemProfiler(string(output))
	}

	// Fallback: use SoX --info to query devices (if available)
	if devices, err := s.discoverSoXDevices(); err == nil && len(devices) > 0 {
		return devices, nil
	}

	// Ultimate fallback: return default
	return []DeviceInfo{
		{
			Name:        "Default Microphone",
			DeviceID:    "default",
			IsDefault:   true,
			IsAvailable: true,
		},
	}, nil
}

// parseSystemProfiler parses output from system_profiler SPAudioDataType
func (s *SoxCapture) parseSystemProfiler(output string) ([]DeviceInfo, error) {
	var devices []DeviceInfo
	lines := strings.Split(output, "\n")

	inInputSection := false
	for i, line := range lines {
		// Look for input devices section
		if strings.Contains(line, "Input Devices") || strings.Contains(line, "Built-in Microphone") {
			inInputSection = true
		}

		if inInputSection && i+1 < len(lines) {
			// Extract device name
			if strings.TrimSpace(line) != "" && strings.HasPrefix(line, "      ") {
				name := strings.TrimSpace(line)
				if name != "" && !strings.Contains(name, ":") {
					devices = append(devices, DeviceInfo{
						Name:        name,
						DeviceID:    "default", // macOS uses default device
						IsDefault:   len(devices) == 0,
						IsAvailable: true,
					})
				}
			}
		}

		// Exit input section when we see output section
		if strings.Contains(line, "Output Devices") {
			inInputSection = false
		}
	}

	if len(devices) == 0 {
		devices = []DeviceInfo{
			{
				Name:        "Built-in Microphone",
				DeviceID:    "default",
				IsDefault:   true,
				IsAvailable: true,
			},
		}
	}

	return devices, nil
}

// discoverSoXDevices attempts to discover devices via SoX (limited support)
func (s *SoxCapture) discoverSoXDevices() ([]DeviceInfo, error) {
	// SoX on macOS typically uses the default device
	// Return a basic default device
	return []DeviceInfo{
		{
			Name:        "Default Microphone",
			DeviceID:    "default",
			IsDefault:   true,
			IsAvailable: true,
		},
	}, nil
}

// SetDevice sets the device to use (macOS uses default device via SoX)
func (s *SoxCapture) SetDevice(deviceID string) error {
	s.BaseCapture.SetDevice(deviceID)
	return nil
}

// GetDevice returns the current device
func (s *SoxCapture) GetDevice() string {
	return s.BaseCapture.GetDevice()
}

// GetAudioLevels returns the current audio levels
func (s *SoxCapture) GetAudioLevels() AudioLevels {
	return s.BaseCapture.GetLevels()
}

// StartLevelMonitoring starts monitoring audio levels without full capture
func (s *SoxCapture) StartLevelMonitoring(ctx context.Context) error {
	if s.IsMonitoring() {
		return ErrAlreadyRecording
	}

	// Build SoX command for monitoring
	args := []string{
		"-t", "raw",
		"-r", strconv.Itoa(s.Config().SampleRate),
		"-c", strconv.Itoa(s.Config().Channels),
		"-e", "signed",
		"-b", "16",
		"-q",
		"-",
	}

	s.cmdMu.Lock()
	s.monitorCmd = exec.CommandContext(ctx, "rec", args...)
	s.monitorDone = make(chan struct{})
	s.cmdMu.Unlock()

	stdout, err := s.monitorCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	s.monitorCmd.Stderr = nil

	if err := s.monitorCmd.Start(); err != nil {
		return fmt.Errorf("failed to start rec command: %w", err)
	}

	s.SetMonitoring(true)

	go func() {
		defer close(s.monitorDone)
		defer s.SetMonitoring(false)

		reader := bufio.NewReader(stdout)
		bufSize := s.Config().SampleRate * 2 / 10 // 100ms buffer
		buf := make([]byte, bufSize)

		for {
			n, err := reader.Read(buf)
			if err != nil {
				return
			}

			if n > 0 {
				levels := CalculateAudioLevels(buf[:n])
				s.BaseCapture.SetLevels(levels)
			}

			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()

	return nil
}

// StopLevelMonitoring stops level monitoring
func (s *SoxCapture) StopLevelMonitoring() error {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()

	if s.monitorCmd != nil && s.monitorCmd.Process != nil {
		if err := s.monitorCmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill rec process: %w", err)
		}
		<-s.monitorDone
	}

	s.SetMonitoring(false)
	return nil
}

// IsMonitoring returns whether level monitoring is active
func (s *SoxCapture) IsMonitoring() bool {
	return s.BaseCapture.IsMonitoring()
}

// RecAudioCapture uses macOS built-in 'rec' via CoreAudio
type RecAudioCapture struct {
	*BaseCapture
	cmd     *exec.Cmd
	cmdMu   sync.Mutex
	cmdDone chan struct{}
}

// NewRecAudioCapture creates a new macOS CoreAudio-based capture
func NewRecAudioCapture(cfg *Config) (AudioCapture, error) {
	return &RecAudioCapture{
		BaseCapture: NewBaseCapture(cfg),
	}, nil
}

// Name returns the capture method name
func (r *RecAudioCapture) Name() string {
	return "macOS CoreAudio"
}

// IsAvailable always returns false for now (placeholder for native implementation)
func (r *RecAudioCapture) IsAvailable() bool {
	// TODO: Implement native macOS audio capture using PortAudio or similar
	return false
}

// Initialize prepares the capture
func (r *RecAudioCapture) Initialize() error {
	return ErrPortAudioNotFound
}

// Start begins capturing audio (not implemented)
func (r *RecAudioCapture) Start(ctx context.Context) (<-chan []byte, error) {
	return nil, ErrPortAudioNotFound
}

// Stop halts audio capture
func (r *RecAudioCapture) Stop() error {
	return nil
}

// Close releases resources
func (r *RecAudioCapture) Close() error {
	return nil
}

// DiscoverDevices returns empty list (not implemented)
func (r *RecAudioCapture) DiscoverDevices() ([]DeviceInfo, error) {
	return nil, ErrPortAudioNotFound
}

// SetDevice sets the device (not implemented)
func (r *RecAudioCapture) SetDevice(deviceID string) error {
	return ErrPortAudioNotFound
}

// GetDevice returns empty string (not implemented)
func (r *RecAudioCapture) GetDevice() string {
	return ""
}

// GetAudioLevels returns zero levels (not implemented)
func (r *RecAudioCapture) GetAudioLevels() AudioLevels {
	return AudioLevels{}
}

// StartLevelMonitoring starts monitoring (not implemented)
func (r *RecAudioCapture) StartLevelMonitoring(ctx context.Context) error {
	return ErrPortAudioNotFound
}

// StopLevelMonitoring stops monitoring (not implemented)
func (r *RecAudioCapture) StopLevelMonitoring() error {
	return nil
}

// IsMonitoring returns false (not implemented)
func (r *RecAudioCapture) IsMonitoring() bool {
	return false
}
