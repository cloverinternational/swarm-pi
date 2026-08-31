package voice

import (
	"context"
	"testing"
)

// TestConfigDefaults tests that default config values are applied correctly
func TestConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.SampleRate != DefaultSampleRate {
		t.Errorf("expected SampleRate %d, got %d", DefaultSampleRate, cfg.SampleRate)
	}
	if cfg.Channels != DefaultChannels {
		t.Errorf("expected Channels %d, got %d", DefaultChannels, cfg.Channels)
	}
	if cfg.Encoding != Linear16 {
		t.Errorf("expected Encoding %s, got %s", Linear16, cfg.Encoding)
	}
	if cfg.Language != DefaultLanguage {
		t.Errorf("expected Language %s, got %s", DefaultLanguage, cfg.Language)
	}
}

// TestConfigValidate tests config validation
func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name:    "valid default config",
			cfg:     DefaultConfig(),
			wantErr: false,
		},
		{
			name: "invalid sample rate - too low",
			cfg: &Config{
				SampleRate: 1000,
				Channels:   1,
				Encoding:   Linear16,
				Language:   "en",
			},
			wantErr: true,
		},
		{
			name: "invalid channels",
			cfg: &Config{
				SampleRate: 16000,
				Channels:   5,
				Encoding:   Linear16,
				Language:   "en",
			},
			wantErr: true,
		},
		{
			name: "empty language",
			cfg: &Config{
				SampleRate: 16000,
				Channels:   1,
				Encoding:   Linear16,
				Language:   "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestTranscriptBuffer tests transcript buffer operations
func TestTranscriptBuffer(t *testing.T) {
	buf := NewTranscriptBuffer()

	// Test empty buffer
	if buf.HasText() {
		t.Error("empty buffer should not have text")
	}

	// Test interim text
	buf.SetInterim("hello")
	if !buf.HasText() {
		t.Error("buffer should have text after SetInterim")
	}

	// Test final text
	buf.AppendFinal("hello world")
	if buf.GetFinalText() != "hello world" {
		t.Errorf("expected final 'hello world', got '%s'", buf.GetFinalText())
	}

	// Test clear
	buf.Clear()
	if buf.HasText() {
		t.Error("buffer should be empty after Clear")
	}
}

// TestState tests state string conversion
func TestState(t *testing.T) {
	if StateIdle.String() != "idle" {
		t.Errorf("expected 'idle', got '%s'", StateIdle.String())
	}
	if StateRecording.String() != "recording" {
		t.Errorf("expected 'recording', got '%s'", StateRecording.String())
	}
}

// TestVoiceError tests error creation
func TestVoiceError(t *testing.T) {
	cause := context.Canceled
	err := NewVoiceError("TEST001", "test error", cause)

	if err.Code != "TEST001" {
		t.Errorf("expected Code 'TEST001', got '%s'", err.Code)
	}
	if err.Message != "test error" {
		t.Errorf("expected Message 'test error', got '%s'", err.Message)
	}
}

// TestMessageTypes tests message type methods
func TestMessageTypes(t *testing.T) {
	ka := KeepAliveMsg{}
	if ka.MessageType() != "KeepAlive" {
		t.Errorf("KeepAliveMsg.MessageType() = %s, want KeepAlive", ka.MessageType())
	}

	cs := CloseStreamMsg{}
	if cs.MessageType() != "CloseStream" {
		t.Errorf("CloseStreamMsg.MessageType() = %s, want CloseStream", cs.MessageType())
	}

	tt := TranscriptTextMsg{}
	if tt.MessageType() != "TranscriptText" {
		t.Errorf("TranscriptTextMsg.MessageType() = %s, want TranscriptText", tt.MessageType())
	}
}
