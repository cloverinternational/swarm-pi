package voice

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestEncodePCM16LEToWAV(t *testing.T) {
	tests := []struct {
		name       string
		pcm        []byte
		sampleRate int
		channels   int
	}{
		{
			name:       "mono",
			pcm:        []byte{0x00, 0x80, 0x00, 0x00, 0xff, 0x7f},
			sampleRate: 16000,
			channels:   1,
		},
		{
			name:       "stereo",
			pcm:        []byte{0x01, 0x00, 0xff, 0xff, 0x02, 0x00, 0xfe, 0xff},
			sampleRate: 48000,
			channels:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wav, err := EncodePCM16LEToWAV(tt.pcm, tt.sampleRate, tt.channels)
			if err != nil {
				t.Fatalf("EncodePCM16LEToWAV() error = %v", err)
			}
			if got, want := len(wav), pcm16WAVHeaderSize+len(tt.pcm); got != want {
				t.Fatalf("WAV length = %d, want %d", got, want)
			}
			if got := string(wav[0:4]); got != "RIFF" {
				t.Errorf("chunk ID = %q, want RIFF", got)
			}
			if got, want := binary.LittleEndian.Uint32(wav[4:8]), uint32(len(wav)-8); got != want {
				t.Errorf("RIFF size = %d, want %d", got, want)
			}
			if got := string(wav[8:12]); got != "WAVE" {
				t.Errorf("format = %q, want WAVE", got)
			}
			if got := string(wav[12:16]); got != "fmt " {
				t.Errorf("fmt chunk ID = %q, want fmt", got)
			}
			if got := binary.LittleEndian.Uint32(wav[16:20]); got != 16 {
				t.Errorf("fmt chunk size = %d, want 16", got)
			}
			if got := binary.LittleEndian.Uint16(wav[20:22]); got != 1 {
				t.Errorf("audio format = %d, want PCM (1)", got)
			}
			if got, want := binary.LittleEndian.Uint16(wav[22:24]), uint16(tt.channels); got != want {
				t.Errorf("channels = %d, want %d", got, want)
			}
			if got, want := binary.LittleEndian.Uint32(wav[24:28]), uint32(tt.sampleRate); got != want {
				t.Errorf("sample rate = %d, want %d", got, want)
			}
			blockAlign := tt.channels * 2
			if got, want := binary.LittleEndian.Uint32(wav[28:32]), uint32(tt.sampleRate*blockAlign); got != want {
				t.Errorf("byte rate = %d, want %d", got, want)
			}
			if got, want := binary.LittleEndian.Uint16(wav[32:34]), uint16(blockAlign); got != want {
				t.Errorf("block align = %d, want %d", got, want)
			}
			if got := binary.LittleEndian.Uint16(wav[34:36]); got != 16 {
				t.Errorf("bits per sample = %d, want 16", got)
			}
			if got := string(wav[36:40]); got != "data" {
				t.Errorf("data chunk ID = %q, want data", got)
			}
			if got, want := binary.LittleEndian.Uint32(wav[40:44]), uint32(len(tt.pcm)); got != want {
				t.Errorf("data size = %d, want %d", got, want)
			}
			if !bytes.Equal(wav[44:], tt.pcm) {
				t.Errorf("PCM payload = %v, want %v", wav[44:], tt.pcm)
			}
		})
	}
}

func TestEncodePCM16LEToWAVRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name       string
		pcm        []byte
		sampleRate int
		channels   int
	}{
		{name: "zero sample rate", sampleRate: 0, channels: 1},
		{name: "zero channels", sampleRate: 16000, channels: 0},
		{name: "partial sample", pcm: []byte{1}, sampleRate: 16000, channels: 1},
		{name: "partial stereo frame", pcm: []byte{1, 0}, sampleRate: 16000, channels: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := EncodePCM16LEToWAV(tt.pcm, tt.sampleRate, tt.channels); err == nil {
				t.Fatal("EncodePCM16LEToWAV() error = nil, want validation error")
			}
		})
	}
}
