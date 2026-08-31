// Package voice provides real-time speech-to-text input via WebSocket streaming.
// This package implements voice input functionality similar to Claude Code's /voice system.
package voice

import "errors"

// Audio capture errors
var (
	// ErrNoAudioCapture indicates no audio capture method is available on the system
	ErrNoAudioCapture = errors.New("no audio capture method available")

	// ErrParecNotFound indicates the PulseAudio/PipeWire 'parec' command is not found
	ErrParecNotFound = errors.New("parec not found - install with: apt-get install pulseaudio-utils")

	// ErrSoxNotFound indicates the SoX 'rec' command is not found
	ErrSoxNotFound = errors.New("sox (rec command) not found - install with: apt-get install sox")

	// ErrArecordNotFound indicates the ALSA 'arecord' command is not found
	ErrArecordNotFound = errors.New("arecord not found - install with: apt-get install alsa-utils")

	// ErrPortAudioNotFound indicates PortAudio is not available
	ErrPortAudioNotFound = errors.New("portaudio not found")

	// ErrPermissionDenied indicates microphone permission was denied
	ErrPermissionDenied = errors.New("microphone permission denied")

	// ErrAudioDeviceOpen indicates failure to open audio device
	ErrAudioDeviceOpen = errors.New("failed to open audio device")
)

// WebSocket errors
var (
	// ErrWebSocketClosed indicates the WebSocket connection is closed
	ErrWebSocketClosed = errors.New("websocket connection closed")

	// ErrWebSocketConnect indicates failure to connect to WebSocket
	ErrWebSocketConnect = errors.New("failed to connect to websocket")

	// ErrWebSocketWrite indicates failure to write to WebSocket
	ErrWebSocketWrite = errors.New("failed to write to websocket")

	// ErrWebSocketRead indicates failure to read from WebSocket
	ErrWebSocketRead = errors.New("failed to read from websocket")
)

// Transcription errors
var (
	// ErrTranscriptionFail indicates transcription failed
	ErrTranscriptionFail = errors.New("transcription failed")

	// ErrTranscriptionTimeout indicates transcription timed out
	ErrTranscriptionTimeout = errors.New("transcription timed out")

	// ErrInvalidTranscript indicates an invalid transcript was received
	ErrInvalidTranscript = errors.New("invalid transcript received")
)

// Authentication errors
var (
	// ErrAuthTokenExpired indicates the auth token has expired
	ErrAuthTokenExpired = errors.New("auth token expired")

	// ErrAuthTokenInvalid indicates the auth token is invalid
	ErrAuthTokenInvalid = errors.New("auth token invalid")

	// ErrNoAuthToken indicates no auth token was provided
	ErrNoAuthToken = errors.New("no auth token provided")
)

// Client state errors
var (
	// ErrInvalidConfig indicates invalid configuration
	ErrInvalidConfig = errors.New("invalid configuration")

	// ErrAlreadyRecording indicates recording is already in progress
	ErrAlreadyRecording = errors.New("already recording")

	// ErrNotRecording indicates no recording is in progress
	ErrNotRecording = errors.New("not recording")

	// ErrClientClosed indicates the client has been closed
	ErrClientClosed = errors.New("client has been closed")
)

// VoiceError represents a detailed voice system error
type VoiceError struct {
	Code    string
	Message string
	Cause   error
}

// Error implements the error interface
func (e *VoiceError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

// Unwrap returns the underlying cause
func (e *VoiceError) Unwrap() error {
	return e.Cause
}

// NewVoiceError creates a new VoiceError
func NewVoiceError(code, message string, cause error) *VoiceError {
	return &VoiceError{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}

// Is checks if the error matches the target error
func (e *VoiceError) Is(target error) bool {
	t, ok := target.(*VoiceError)
	if !ok {
		return false
	}
	return e.Code == t.Code
}
