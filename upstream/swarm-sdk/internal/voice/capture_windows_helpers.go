package voice

import (
	"errors"
	"strconv"
	"strings"
)

func validateWindowsCaptureConfig(cfg *Config) error {
	if cfg == nil {
		return errors.New("voice capture config must not be nil")
	}
	if cfg.SampleRate < 8000 || cfg.SampleRate > 48000 {
		return errors.New("Windows voice capture sample rate must be between 8000 and 48000 Hz")
	}
	if cfg.Channels != 1 {
		return errors.New("Windows voice capture supports mono audio only (channels must be 1)")
	}
	if cfg.Encoding != Linear16 {
		return errors.New("Windows voice capture supports linear16 PCM encoding only")
	}
	return nil
}

func windowsFFmpegDiscoveryArgs() []string {
	return []string{"-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy"}
}

func windowsFFmpegCaptureArgs(device string, sampleRate int) []string {
	return []string{
		"-hide_banner", "-loglevel", "error",
		"-f", "dshow", "-i", "audio=" + device,
		"-ac", "1", "-ar", strconv.Itoa(sampleRate),
		"-acodec", "pcm_s16le", "-f", "s16le", "pipe:1",
	}
}

// parseWindowsDShowAudioDevices parses ffmpeg's stderr device listing. It only
// accepts quoted entries after the "DirectShow audio devices" heading and uses
// each indented alternative name as the stable DeviceID for its display name.
func parseWindowsDShowAudioDevices(output string) []DeviceInfo {
	var devices []DeviceInfo
	inAudioSection := false
	for _, line := range strings.Split(output, "\n") {
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "directshow audio devices"):
			inAudioSection = true
			continue
		case inAudioSection && strings.Contains(lower, "directshow video devices"):
			inAudioSection = false
			continue
		}
		if !inAudioSection {
			continue
		}
		start := strings.IndexByte(line, '"')
		if start < 0 {
			continue
		}
		endRel := strings.IndexByte(line[start+1:], '"')
		if endRel < 0 {
			continue
		}
		name := line[start+1 : start+1+endRel]
		if name == "" {
			continue
		}
		if strings.Contains(lower, "alternative name") {
			if len(devices) > 0 {
				devices[len(devices)-1].DeviceID = name
			}
			continue
		}
		devices = append(devices, DeviceInfo{
			Name:        name,
			DeviceID:    name,
			IsAvailable: true,
		})
	}
	return devices
}
