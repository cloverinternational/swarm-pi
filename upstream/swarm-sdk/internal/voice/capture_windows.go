//go:build windows

package voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// ErrFFmpegNotFound explains how to enable voice capture on Windows.
var ErrFFmpegNotFound = errors.New("ffmpeg executable not found in PATH; install ffmpeg for Windows and add its bin directory to PATH")

// WindowsCapture records a DirectShow microphone through ffmpeg.
type WindowsCapture struct {
	*BaseCapture

	mu         sync.Mutex
	ffmpegPath string
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	done       chan struct{}
}

// NewWindowsCapture creates an ffmpeg DirectShow capture.
func NewWindowsCapture(cfg *Config) (AudioCapture, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	} else {
		configCopy := *cfg
		configCopy.ApplyDefaults()
		cfg = &configCopy
	}

	return &WindowsCapture{BaseCapture: NewBaseCapture(cfg)}, nil
}

func (w *WindowsCapture) Name() string { return "Windows DirectShow (ffmpeg)" }

func (w *WindowsCapture) Description() string {
	return "Captures Windows microphones through ffmpeg's DirectShow input."
}

// Initialize validates the requested output and locates ffmpeg.
func (w *WindowsCapture) Initialize() error {
	if err := validateWindowsCaptureConfig(w.Config()); err != nil {
		return err
	}
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrFFmpegNotFound, err)
	}
	w.mu.Lock()
	w.ffmpegPath = path
	w.mu.Unlock()
	return nil
}

// IsAvailable reports whether ffmpeg can be found. Initialize provides details.
func (w *WindowsCapture) IsAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// Start begins signed 16-bit little-endian mono PCM capture.
func (w *WindowsCapture) Start(ctx context.Context) (<-chan []byte, error) {
	device, err := w.resolveDevice()
	if err != nil {
		return nil, err
	}
	out := make(chan []byte, 16)
	if err := w.startProcess(ctx, device, out, false); err != nil {
		close(out)
		return nil, err
	}
	return out, nil
}

func (w *WindowsCapture) startProcess(parent context.Context, device string, out chan []byte, monitoring bool) error {
	if parent == nil {
		return errors.New("voice capture context must not be nil")
	}
	if err := w.Initialize(); err != nil {
		return err
	}

	w.mu.Lock()
	if w.cmd != nil {
		w.mu.Unlock()
		return ErrAlreadyRecording
	}
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, w.ffmpegPath, windowsFFmpegCaptureArgs(device, w.Config().SampleRate)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		w.mu.Unlock()
		return fmt.Errorf("create ffmpeg audio pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdout.Close()
		w.mu.Unlock()
		return fmt.Errorf("start ffmpeg DirectShow capture for %q: %w", device, err)
	}

	done := make(chan struct{})
	w.cmd = cmd
	w.cancel = cancel
	w.done = done
	w.SetRunning(!monitoring)
	w.SetMonitoring(monitoring)
	w.mu.Unlock()

	go w.readProcess(ctx, cmd, stdout, out, done)
	return nil
}

func (w *WindowsCapture) readProcess(ctx context.Context, cmd *exec.Cmd, stdout io.ReadCloser, out chan []byte, done chan struct{}) {
	defer close(done)
	if out != nil {
		defer close(out)
	}
	defer func() {
		_ = stdout.Close()
		_ = cmd.Wait()
		w.mu.Lock()
		if w.cmd == cmd {
			w.cmd = nil
			w.cancel = nil
			w.done = nil
			w.SetRunning(false)
			w.SetMonitoring(false)
			w.SetLevels(AudioLevels{})
		}
		w.mu.Unlock()
	}()

	bufSize := w.Config().SampleRate * 2 / 10
	if bufSize < 2 {
		bufSize = 2
	}
	buf := make([]byte, bufSize)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			w.SetLevels(CalculateAudioLevels(chunk))
			if out != nil {
				select {
				case out <- chunk:
				case <-ctx.Done():
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// Stop cancels capture and waits for the reader and process waiter to finish.
func (w *WindowsCapture) Stop() error { return w.stopProcess() }

func (w *WindowsCapture) stopProcess() error {
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	w.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	if done != nil {
		<-done
	}
	return nil
}

func (w *WindowsCapture) Close() error { return w.stopProcess() }

// DiscoverDevices asks ffmpeg for DirectShow audio inputs. ffmpeg writes this
// listing to stderr and normally returns a non-zero status after enumeration.
func (w *WindowsCapture) DiscoverDevices() ([]DeviceInfo, error) {
	if err := w.Initialize(); err != nil {
		return nil, err
	}
	w.mu.Lock()
	path := w.ffmpegPath
	w.mu.Unlock()
	cmd := exec.Command(path, windowsFFmpegDiscoveryArgs()...)
	output, runErr := cmd.CombinedOutput()
	devices := parseWindowsDShowAudioDevices(string(output))
	if len(devices) > 0 {
		return devices, nil
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = "ffmpeg returned no DirectShow audio devices"
	}
	if runErr != nil {
		return nil, fmt.Errorf("discover DirectShow microphones: %w: %s", runErr, detail)
	}
	return nil, errors.New(detail)
}

func (w *WindowsCapture) resolveDevice() (string, error) {
	device := w.GetDevice()
	// DirectShow does not expose the Windows system-default marker. Treat the
	// first enumerated input as this backend's deterministic default fallback.
	if device != "" && device != "default" {
		return device, nil
	}
	devices, err := w.DiscoverDevices()
	if err != nil {
		return "", fmt.Errorf("resolve default Windows microphone: %w", err)
	}
	if len(devices) == 0 {
		return "", errors.New("resolve default Windows microphone: no DirectShow audio input devices found")
	}
	return devices[0].DeviceID, nil
}

func (w *WindowsCapture) SetDevice(deviceID string) error {
	if strings.TrimSpace(deviceID) == "" {
		return errors.New("DirectShow microphone device ID must not be empty")
	}
	if w.IsRunning() || w.IsMonitoring() {
		return errors.New("cannot change microphone while capture is active")
	}
	w.BaseCapture.SetDevice(deviceID)
	return nil
}

func (w *WindowsCapture) GetDevice() string { return w.BaseCapture.GetDevice() }

func (w *WindowsCapture) GetAudioLevels() AudioLevels { return w.BaseCapture.GetLevels() }

func (w *WindowsCapture) StartLevelMonitoring(ctx context.Context) error {
	device, err := w.resolveDevice()
	if err != nil {
		return err
	}
	return w.startProcess(ctx, device, nil, true)
}

func (w *WindowsCapture) StopLevelMonitoring() error { return w.stopProcess() }

func (w *WindowsCapture) IsMonitoring() bool { return w.BaseCapture.IsMonitoring() }

func init() { RegisterCapture(NewWindowsCapture) }
