package voice

import (
	"reflect"
	"testing"
)

func TestParseWindowsDShowAudioDevices(t *testing.T) {
	output := `[dshow @ 000001] "Integrated Camera" (video)
[dshow @ 000001] DirectShow audio devices (some may be both audio and video devices)
[dshow @ 000001]  "Microphone Array (Realtek(R) Audio)" (audio)
[dshow @ 000001]    Alternative name "@device_cm_{ABC}\\wave_{ONE}"
[dshow @ 000001]  "USB Mic; name with spaces" (audio)
[dshow @ 000001]    Alternative name "@device_cm_{DEF}\\wave_{TWO}"
dummy: Immediate exit requested`

	got := parseWindowsDShowAudioDevices(output)
	want := []DeviceInfo{
		{Name: "Microphone Array (Realtek(R) Audio)", DeviceID: `@device_cm_{ABC}\\wave_{ONE}`, IsAvailable: true},
		{Name: "USB Mic; name with spaces", DeviceID: `@device_cm_{DEF}\\wave_{TWO}`, IsAvailable: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWindowsDShowAudioDevices() = %#v, want %#v", got, want)
	}
}

func TestParseWindowsDShowAudioDevicesIgnoresVideoAndMalformedLines(t *testing.T) {
	output := `[dshow @ 1] DirectShow video devices
[dshow @ 1]  "Camera" (video)
[dshow @ 1] DirectShow audio devices
[dshow @ 1] unquoted device
[dshow @ 1] "Microphone (audio)`
	if got := parseWindowsDShowAudioDevices(output); len(got) != 0 {
		t.Fatalf("unexpected devices: %#v", got)
	}
}

func TestWindowsFFmpegCaptureArgsAreShellFree(t *testing.T) {
	device := `Mic & Speakers "Studio"`
	got := windowsFFmpegCaptureArgs(device, 22050)
	want := []string{
		"-hide_banner", "-loglevel", "error",
		"-f", "dshow", "-i", `audio=Mic & Speakers "Studio"`,
		"-ac", "1", "-ar", "22050",
		"-acodec", "pcm_s16le", "-f", "s16le", "pipe:1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("windowsFFmpegCaptureArgs() = %#v, want %#v", got, want)
	}
}

func TestValidateWindowsCaptureConfig(t *testing.T) {
	valid := &Config{SampleRate: 16000, Channels: 1, Encoding: Linear16}
	if err := validateWindowsCaptureConfig(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cases := []*Config{
		nil,
		{SampleRate: 7999, Channels: 1, Encoding: Linear16},
		{SampleRate: 16000, Channels: 2, Encoding: Linear16},
		{SampleRate: 16000, Channels: 1, Encoding: MP3},
	}
	for _, cfg := range cases {
		if err := validateWindowsCaptureConfig(cfg); err == nil {
			t.Fatalf("invalid config accepted: %#v", cfg)
		}
	}
}
