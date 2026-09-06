//go:build linux

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
	// Register capture methods in priority order
	// ParecCapture is best for PipeWire/PulseAudio (modern Linux)
	RegisterCapture(NewParecCapture)
	// ArecordCapture is for ALSA (direct hardware access)
	RegisterCapture(NewArecordCapture)
	// SoxCapture is a fallback using the 'rec' command
	RegisterCapture(NewSoxCapture)
}

// ParecCapture uses PulseAudio/PipeWire 'parec' for audio capture on Linux
// This is the preferred method for modern Linux systems using PipeWire or PulseAudio
type ParecCapture struct {
	*BaseCapture
	cmd         *exec.Cmd
	cmdMu       sync.Mutex
	cmdDone     chan struct{}
	monitorCmd  *exec.Cmd
	monitorDone chan struct{}
	device      string
}

// NewParecCapture creates a new Parec-based audio capture
func NewParecCapture(cfg *Config) (AudioCapture, error) {
	capture := &ParecCapture{
		BaseCapture: NewBaseCapture(cfg),
		device:      "", // Empty means default device
	}
	return capture, nil
}

// Name returns the capture method name
func (p *ParecCapture) Name() string {
	return "PulseAudio/PipeWire (parec)"
}

// IsAvailable checks if parec is available on the system
func (p *ParecCapture) IsAvailable() bool {
	// Check for parec (PulseAudio/PipeWire recording tool)
	if _, err := exec.LookPath("parec"); err == nil {
		return true
	}
	// Also check for parecord (symlink/alias)
	if _, err := exec.LookPath("parecord"); err == nil {
		return true
	}
	return false
}

// Initialize prepares the capture
func (p *ParecCapture) Initialize() error {
	if !p.IsAvailable() {
		return fmt.Errorf("parec not found: %w", ErrNoAudioCapture)
	}
	return nil
}

// Start begins capturing audio using parec
func (p *ParecCapture) Start(ctx context.Context) (<-chan []byte, error) {
	if !p.IsAvailable() {
		return nil, fmt.Errorf("parec not found: %w", ErrNoAudioCapture)
	}
	if p.IsRunning() {
		return nil, ErrAlreadyRecording
	}

	audioChan := make(chan []byte, 100)

	// Build parec command:
	// parec --format=s16le --rate=16000 --channels=1 [--device=xxx] --file-format=raw /dev/null
	// Or simply: parec --format=s16le --rate=16000 --channels=1 - (to stdout)
	args := []string{
		"--format=s16le",
		"--rate=" + strconv.Itoa(p.Config().SampleRate),
		"--channels=" + strconv.Itoa(p.Config().Channels),
		"--latency-ms=100", // Reasonable latency
	}
	if p.device != "" {
		args = append(args, "--device="+p.device)
	}

	p.cmdMu.Lock()
	p.cmd = exec.CommandContext(ctx, "parec", args...)
	p.cmdDone = make(chan struct{})
	p.cmdMu.Unlock()

	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		close(audioChan)
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Redirect stderr to suppress messages
	p.cmd.Stderr = nil

	if err := p.cmd.Start(); err != nil {
		close(audioChan)
		return nil, fmt.Errorf("failed to start parec command: %w", err)
	}

	p.SetRunning(true)

	go func() {
		defer close(audioChan)
		defer close(p.cmdDone)
		defer p.SetRunning(false)

		reader := bufio.NewReader(stdout)
		// Buffer size: ~100ms of audio at 16kHz 16-bit mono
		bufSize := p.Config().SampleRate * 2 / 10 // bytes per 100ms
		buf := make([]byte, bufSize)

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			n, err := reader.Read(buf)
			if err != nil {
				// Context cancelled or pipe closed
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
func (p *ParecCapture) Stop() error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()

	if p.cmd != nil && p.cmd.Process != nil {
		if err := p.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill parec process: %w", err)
		}
		// Wait for process to exit
		if p.cmdDone != nil {
			select {
			case <-p.cmdDone:
			default:
			}
		}
	}
	p.SetRunning(false)
	return nil
}

// Close releases resources
func (p *ParecCapture) Close() error {
	return p.Stop()
}

// DiscoverDevices returns a list of available audio input devices using pactl
func (p *ParecCapture) DiscoverDevices() ([]DeviceInfo, error) {
	// Use pactl to list sources (input devices)
	cmd := exec.Command("pactl", "list", "short", "sources")
	output, err := cmd.Output()
	if err != nil {
		// Try alternative method with detailed list
		return p.discoverDevicesDetailed()
	}

	var devices []DeviceInfo
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			deviceID := fields[1]

			// Skip monitor devices (output monitors, not actual microphones)
			if strings.Contains(deviceID, ".monitor") {
				continue
			}

			// Get a human-readable name
			name := p.formatDeviceName(deviceID)

			devices = append(devices, DeviceInfo{
				Name:        name,
				DeviceID:    deviceID,
				IsDefault:   len(devices) == 0, // First device is default
				IsAvailable: true,
			})
		}
	}

	// If no devices found, try detailed discovery
	if len(devices) == 0 {
		return p.discoverDevicesDetailed()
	}

	return devices, nil
}

// discoverDevicesDetailed uses pactl list sources (detailed) for device discovery
func (p *ParecCapture) discoverDevicesDetailed() ([]DeviceInfo, error) {
	cmd := exec.Command("pactl", "list", "sources")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list pulse sources: %w", err)
	}

	var devices []DeviceInfo
	var currentDevice *DeviceInfo

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()

		// New source entry
		if strings.HasPrefix(line, "Source #") {
			if currentDevice != nil && currentDevice.DeviceID != "" {
				// Skip monitor devices
				if !strings.Contains(currentDevice.DeviceID, ".monitor") {
					devices = append(devices, *currentDevice)
				}
			}
			currentDevice = &DeviceInfo{IsAvailable: true}
		} else if currentDevice != nil {
			trimmed := strings.TrimSpace(line)

			if after, ok := strings.CutPrefix(trimmed, "Name: "); ok {
				currentDevice.DeviceID = after
				if currentDevice.Name == "" {
					currentDevice.Name = currentDevice.DeviceID
				}
			} else if after, ok := strings.CutPrefix(trimmed, "device.description = "); ok {
				desc := after
				desc = strings.Trim(desc, "\"")
				currentDevice.Name = desc
			} else if strings.HasPrefix(trimmed, "device.icon_name") && strings.Contains(trimmed, "microphone") {
				// This is definitely a microphone, not a monitor
			}
		}
	}

	// Add last device
	if currentDevice != nil && currentDevice.DeviceID != "" {
		if !strings.Contains(currentDevice.DeviceID, ".monitor") {
			devices = append(devices, *currentDevice)
		}
	}

	// Mark first as default
	for i := range devices {
		if i == 0 {
			devices[i].IsDefault = true
		}
	}

	// If still no devices, return a default option
	if len(devices) == 0 {
		devices = []DeviceInfo{
			{
				Name:        "Default Microphone",
				DeviceID:    "",
				IsDefault:   true,
				IsAvailable: true,
			},
		}
	}

	return devices, nil
}

// formatDeviceName creates a human-readable name from a device ID
func (p *ParecCapture) formatDeviceName(deviceID string) string {
	// Handle common patterns
	name := deviceID

	// alsa_input.usb-... -> USB Device
	// alsa_input.pci-... -> Built-in Audio
	if strings.Contains(name, "alsa_input") {
		if strings.Contains(name, "usb") {
			if idx := strings.Index(name, "usb-"); idx >= 0 {
				rest := name[idx+4:]
				if dotIdx := strings.Index(rest, "."); dotIdx > 0 {
					usbName := rest[:dotIdx]
					usbName = strings.ReplaceAll(usbName, "_", " ")
					return fmt.Sprintf("USB Audio (%s)", usbName)
				}
			}
			return "USB Audio Device"
		}
		if strings.Contains(name, "pci") {
			return "Built-in Audio"
		}
		return "ALSA Input"
	}

	// bluez_source.XX:XX:XX:XX:XX:XX -> Bluetooth Device
	if strings.HasPrefix(name, "bluez_source.") {
		return "Bluetooth Headset"
	}

	// Generic cleanup
	if strings.Contains(name, ".") {
		parts := strings.Split(name, ".")
		name = parts[len(parts)-1]
	}
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.Title(strings.ToLower(name))

	return name
}

// SetDevice sets the PulseAudio source to use
func (p *ParecCapture) SetDevice(deviceID string) error {
	p.device = deviceID
	p.BaseCapture.SetDevice(deviceID)
	return nil
}

// GetDevice returns the current device
func (p *ParecCapture) GetDevice() string {
	return p.device
}

// GetAudioLevels returns the current audio levels
func (p *ParecCapture) GetAudioLevels() AudioLevels {
	return p.BaseCapture.GetLevels()
}

// StartLevelMonitoring starts monitoring audio levels without full capture
func (p *ParecCapture) StartLevelMonitoring(ctx context.Context) error {
	if p.IsMonitoring() {
		return ErrAlreadyRecording
	}

	args := []string{
		"--format=s16le",
		"--rate=" + strconv.Itoa(p.Config().SampleRate),
		"--channels=" + strconv.Itoa(p.Config().Channels),
		"--latency-ms=50", // Lower latency for monitoring
	}
	if p.device != "" {
		args = append(args, "--device="+p.device)
	}

	p.cmdMu.Lock()
	p.monitorCmd = exec.CommandContext(ctx, "parec", args...)
	p.monitorDone = make(chan struct{})
	p.cmdMu.Unlock()

	stdout, err := p.monitorCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	p.monitorCmd.Stderr = nil

	if err := p.monitorCmd.Start(); err != nil {
		return fmt.Errorf("failed to start parec command: %w", err)
	}

	p.SetMonitoring(true)

	go func() {
		defer close(p.monitorDone)
		defer p.SetMonitoring(false)

		reader := bufio.NewReader(stdout)
		bufSize := p.Config().SampleRate * 2 / 10 // 100ms buffer
		buf := make([]byte, bufSize)

		for {
			n, err := reader.Read(buf)
			if err != nil {
				return
			}
			if n > 0 {
				levels := CalculateAudioLevels(buf[:n])
				p.BaseCapture.SetLevels(levels)
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
func (p *ParecCapture) StopLevelMonitoring() error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()

	if p.monitorCmd != nil && p.monitorCmd.Process != nil {
		if err := p.monitorCmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill parec process: %w", err)
		}
		if p.monitorDone != nil {
			select {
			case <-p.monitorDone:
			default:
			}
		}
	}
	p.SetMonitoring(false)
	return nil
}

// IsMonitoring returns whether level monitoring is active
func (p *ParecCapture) IsMonitoring() bool {
	return p.BaseCapture.IsMonitoring()
}

// SoxCapture uses the 'rec' command (SoX) for audio capture on Linux
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
			select {
			case <-ctx.Done():
				return
			default:
			}
			n, err := reader.Read(buf)
			if err != nil {
				// Context cancelled or pipe closed
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
		// Wait for process to exit
		if s.cmdDone != nil {
			select {
			case <-s.cmdDone:
			default:
			}
		}
	}
	s.SetRunning(false)
	return nil
}

// Close releases resources
func (s *SoxCapture) Close() error {
	return s.Stop()
}

// DiscoverDevices returns a list of available audio input devices
// SoX uses the system default device, but we can query PulseAudio/PipeWire
func (s *SoxCapture) DiscoverDevices() ([]DeviceInfo, error) {
	// Try pactl first (PulseAudio/PipeWire)
	if devices, err := s.discoverPulseDevices(); err == nil && len(devices) > 0 {
		return devices, nil
	}
	// Fallback to ALSA devices via arecord
	if devices, err := s.discoverALSADevices(); err == nil && len(devices) > 0 {
		return devices, nil
	}
	// Ultimate fallback: just return the default device
	return []DeviceInfo{
		{
			Name:        "Default Microphone",
			DeviceID:    "default",
			IsDefault:   true,
			IsAvailable: true,
		},
	}, nil
}

// discoverPulseDevices discovers devices via PulseAudio/PipeWire
func (s *SoxCapture) discoverPulseDevices() ([]DeviceInfo, error) {
	cmd := exec.Command("pactl", "list", "short", "sources")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var devices []DeviceInfo
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			deviceID := fields[1]

			// Skip monitor devices
			if strings.Contains(deviceID, ".monitor") {
				continue
			}

			// Parse device name from the ID
			name := deviceID
			if strings.Contains(deviceID, ".") {
				parts := strings.Split(deviceID, ".")
				name = parts[len(parts)-1]
			}
			name = strings.ReplaceAll(name, "_", " ")
			name = strings.Title(strings.ToLower(name))

			isDefault := len(devices) == 0

			devices = append(devices, DeviceInfo{
				Name:        name,
				DeviceID:    deviceID,
				IsDefault:   isDefault,
				IsAvailable: true,
			})
		}
	}
	return devices, nil
}

// discoverALSADevices discovers devices via ALSA arecord
func (s *SoxCapture) discoverALSADevices() ([]DeviceInfo, error) {
	cmd := exec.Command("arecord", "-L")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var devices []DeviceInfo
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	isDefault := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Skip indented lines (descriptions)
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		// This is a device ID
		name := line
		if strings.Contains(line, ":") {
			parts := strings.Split(line, ":")
			name = parts[0]
		}
		name = strings.ReplaceAll(name, "_", " ")
		name = strings.Title(strings.ToLower(name))

		devices = append(devices, DeviceInfo{
			Name:        name,
			DeviceID:    line,
			IsDefault:   isDefault,
			IsAvailable: true,
		})
		isDefault = false
	}
	return devices, nil
}

// SetDevice sets the device to use (Note: SoX uses default, this is for compatibility)
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
		if s.monitorDone != nil {
			select {
			case <-s.monitorDone:
			default:
			}
		}
	}
	s.SetMonitoring(false)
	return nil
}

// IsMonitoring returns whether level monitoring is active
func (s *SoxCapture) IsMonitoring() bool {
	return s.BaseCapture.IsMonitoring()
}

// ArecordCapture uses ALSA 'arecord' for audio capture on Linux
type ArecordCapture struct {
	*BaseCapture
	cmd         *exec.Cmd
	cmdMu       sync.Mutex
	cmdDone     chan struct{}
	monitorCmd  *exec.Cmd
	monitorDone chan struct{}
	device      string
}

// NewArecordCapture creates a new ALSA-based audio capture
func NewArecordCapture(cfg *Config) (AudioCapture, error) {
	device := "default" // Default ALSA device
	capture := &ArecordCapture{
		BaseCapture: NewBaseCapture(cfg),
		device:      device,
	}
	capture.BaseCapture.SetDevice(device)
	return capture, nil
}

// Name returns the capture method name
func (a *ArecordCapture) Name() string {
	return "ALSA (arecord command)"
}

// IsAvailable checks if arecord is available on the system
func (a *ArecordCapture) IsAvailable() bool {
	_, err := exec.LookPath("arecord")
	return err == nil
}

// Initialize prepares the capture (no-op for ALSA)
func (a *ArecordCapture) Initialize() error {
	if !a.IsAvailable() {
		return ErrArecordNotFound
	}
	return nil
}

// Start begins capturing audio using ALSA
func (a *ArecordCapture) Start(ctx context.Context) (<-chan []byte, error) {
	if !a.IsAvailable() {
		return nil, ErrArecordNotFound
	}
	if a.IsRunning() {
		return nil, ErrAlreadyRecording
	}

	audioChan := make(chan []byte, 100)

	// Build arecord command:
	// arecord -f S16_LE -r 16000 -c 1 -t raw
	args := []string{
		"-D", a.device, // Device
		"-f", "S16_LE", // 16-bit signed little-endian
		"-r", strconv.Itoa(a.Config().SampleRate), // Sample rate
		"-c", strconv.Itoa(a.Config().Channels), // Channels
		"-t", "raw", // Raw format
		"-q", // Quiet mode
	}

	a.cmdMu.Lock()
	a.cmd = exec.CommandContext(ctx, "arecord", args...)
	a.cmdDone = make(chan struct{})
	a.cmdMu.Unlock()

	stdout, err := a.cmd.StdoutPipe()
	if err != nil {
		close(audioChan)
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Redirect stderr to suppress arecord messages
	a.cmd.Stderr = nil

	if err := a.cmd.Start(); err != nil {
		close(audioChan)
		return nil, fmt.Errorf("failed to start arecord command: %w", err)
	}

	a.SetRunning(true)

	go func() {
		defer close(audioChan)
		defer close(a.cmdDone)
		defer a.SetRunning(false)

		reader := bufio.NewReader(stdout)
		// Buffer size: ~128ms of audio at 16kHz 16-bit mono
		bufSize := a.Config().SampleRate * 2 / 8 // bytes per 125ms
		buf := make([]byte, bufSize)

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			n, err := reader.Read(buf)
			if err != nil {
				// Context cancelled or pipe closed
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
func (a *ArecordCapture) Stop() error {
	a.cmdMu.Lock()
	defer a.cmdMu.Unlock()

	if a.cmd != nil && a.cmd.Process != nil {
		if err := a.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill arecord process: %w", err)
		}
		// Wait for process to exit
		if a.cmdDone != nil {
			select {
			case <-a.cmdDone:
			default:
			}
		}
	}
	a.SetRunning(false)
	return nil
}

// Close releases resources
func (a *ArecordCapture) Close() error {
	return a.Stop()
}

// DiscoverDevices returns a list of available audio input devices using arecord
func (a *ArecordCapture) DiscoverDevices() ([]DeviceInfo, error) {
	// First try hardware devices with arecord -l
	hwCmd := exec.Command("arecord", "-l")
	hwOutput, err := hwCmd.Output()
	if err == nil {
		devices := a.parseHardwareDevices(string(hwOutput))
		if len(devices) > 0 {
			// Add default device at the beginning
			devices = append([]DeviceInfo{{
				Name:        "Default ALSA Device",
				DeviceID:    "default",
				IsDefault:   true,
				IsAvailable: true,
			}}, devices...)
			return devices, nil
		}
	}

	// Fallback to arecord -L (includes plugins)
	cmd := exec.Command("arecord", "-L")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list devices: %w", err)
	}

	var devices []DeviceInfo
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	isDefault := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Skip indented lines (descriptions)
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		// This is a device ID
		name := line
		if strings.Contains(line, ":") {
			parts := strings.Split(line, ":")
			name = parts[0]
		}
		name = strings.ReplaceAll(name, "_", " ")
		name = strings.Title(strings.ToLower(name))

		devices = append(devices, DeviceInfo{
			Name:        name,
			DeviceID:    line,
			IsDefault:   isDefault,
			IsAvailable: true,
		})
		isDefault = false
	}

	// If no devices found, return default
	if len(devices) == 0 {
		devices = []DeviceInfo{
			{
				Name:        "Default ALSA Device",
				DeviceID:    "default",
				IsDefault:   true,
				IsAvailable: true,
			},
		}
	}
	return devices, nil
}

// parseHardwareDevices parses arecord -l output
func (a *ArecordCapture) parseHardwareDevices(output string) []DeviceInfo {
	var devices []DeviceInfo
	lines := strings.SplitSeq(output, "\n")

	for line := range lines {
		// Parse lines like:
		// card 0: Intel [HDA Intel], device 0: ALC269VC Analog [ALC269VC Analog]
		if strings.Contains(line, "card ") && strings.Contains(line, "device ") {
			// Extract card and device numbers
			cardIdx := strings.Index(line, "card ")
			deviceIdx := strings.Index(line, "device ")

			if cardIdx >= 0 && deviceIdx > cardIdx {
				// Extract numbers
				cardStr := line[cardIdx+5:]
				deviceStr := line[deviceIdx+7:]

				var cardNum, deviceNum int
				if _, err := fmt.Sscanf(cardStr, "%d", &cardNum); err != nil {
					continue
				}
				if _, err := fmt.Sscanf(deviceStr, "%d", &deviceNum); err != nil {
					continue
				}

				// Extract device name (in brackets after the colon)
				name := fmt.Sprintf("card %d device %d", cardNum, deviceNum)
				if _, after, ok := strings.Cut(line, ": "); ok {
					rest := after
					if bracketIdx := strings.Index(rest, " ["); bracketIdx >= 0 {
						nameEnd := strings.Index(rest[bracketIdx+2:], "]")
						if nameEnd >= 0 {
							name = rest[bracketIdx+2 : bracketIdx+2+nameEnd]
						}
					}
				}

				devices = append(devices, DeviceInfo{
					Name:        name,
					DeviceID:    fmt.Sprintf("hw:%d,%d", cardNum, deviceNum),
					IsDefault:   len(devices) == 0,
					IsAvailable: true,
				})
			}
		}
	}
	return devices
}

// SetDevice sets the ALSA device to use
func (a *ArecordCapture) SetDevice(deviceID string) error {
	a.device = deviceID
	a.BaseCapture.SetDevice(deviceID)
	return nil
}

// GetDevice returns the current device
func (a *ArecordCapture) GetDevice() string {
	return a.device
}

// GetAudioLevels returns the current audio levels
func (a *ArecordCapture) GetAudioLevels() AudioLevels {
	return a.BaseCapture.GetLevels()
}

// StartLevelMonitoring starts monitoring audio levels without full capture
func (a *ArecordCapture) StartLevelMonitoring(ctx context.Context) error {
	if a.IsMonitoring() {
		return ErrAlreadyRecording
	}
	// Build arecord command for monitoring
	args := []string{
		"-D", a.device,
		"-f", "S16_LE",
		"-r", strconv.Itoa(a.Config().SampleRate),
		"-c", strconv.Itoa(a.Config().Channels),
		"-t", "raw",
		"-q",
	}

	a.cmdMu.Lock()
	a.monitorCmd = exec.CommandContext(ctx, "arecord", args...)
	a.monitorDone = make(chan struct{})
	a.cmdMu.Unlock()

	stdout, err := a.monitorCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	a.monitorCmd.Stderr = nil

	if err := a.monitorCmd.Start(); err != nil {
		return fmt.Errorf("failed to start arecord command: %w", err)
	}

	a.SetMonitoring(true)

	go func() {
		defer close(a.monitorDone)
		defer a.SetMonitoring(false)

		reader := bufio.NewReader(stdout)
		bufSize := a.Config().SampleRate * 2 / 10 // 100ms buffer
		buf := make([]byte, bufSize)

		for {
			n, err := reader.Read(buf)
			if err != nil {
				return
			}
			if n > 0 {
				levels := CalculateAudioLevels(buf[:n])
				a.BaseCapture.SetLevels(levels)
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
func (a *ArecordCapture) StopLevelMonitoring() error {
	a.cmdMu.Lock()
	defer a.cmdMu.Unlock()

	if a.monitorCmd != nil && a.monitorCmd.Process != nil {
		if err := a.monitorCmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill arecord process: %w", err)
		}
		if a.monitorDone != nil {
			select {
			case <-a.monitorDone:
			default:
			}
		}
	}
	a.SetMonitoring(false)
	return nil
}

// IsMonitoring returns whether level monitoring is active
func (a *ArecordCapture) IsMonitoring() bool {
	return a.BaseCapture.IsMonitoring()
}
