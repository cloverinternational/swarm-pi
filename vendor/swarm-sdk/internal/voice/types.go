package voice

// VoiceEvent represents events emitted by the voice system
type VoiceEvent interface {
	isVoiceEvent()
}

// EventInterim is emitted when an interim transcript is received
type EventInterim struct {
	Text string
}

func (EventInterim) isVoiceEvent() {}

// EventFinal is emitted when a final transcript is received
type EventFinal struct {
	Text      string
	StartTime float64
	EndTime   float64
}

func (EventFinal) isVoiceEvent() {}

// EventTranscript is emitted when any transcript is received
type EventTranscript struct {
	Transcript Transcript
}

func (EventTranscript) isVoiceEvent() {}

// EventRecordingStarted is emitted when recording begins
type EventRecordingStarted struct{}

func (EventRecordingStarted) isVoiceEvent() {}

// EventRecordingStopped is emitted when recording stops
type EventRecordingStopped struct{}

func (EventRecordingStopped) isVoiceEvent() {}

// EventDisconnected is emitted when the connection is lost
type EventDisconnected struct{}

func (EventDisconnected) isVoiceEvent() {}

// EventReady is emitted when the provider is ready
type EventReady struct {
	Provider ProviderType
}

func (EventReady) isVoiceEvent() {}

// EventConnected is emitted when connected to the voice service
type EventConnected struct{}

func (EventConnected) isVoiceEvent() {}

// EventError is emitted when an error occurs
type EventError struct {
	Error error
}

func (EventError) isVoiceEvent() {}

// Transcript represents a single transcript with metadata
type Transcript struct {
	Text       string  `json:"text"`
	IsFinal    bool    `json:"is_final"`
	Confidence float64 `json:"confidence,omitempty"`
	Start      float64 `json:"start,omitempty"`
	End        float64 `json:"end,omitempty"`
	Channel    int     `json:"channel,omitempty"`
	Duration   float64 `json:"duration,omitempty"`
}

// State represents the current state of the voice client
type State int

const (
	StateIdle State = iota
	StateConnecting
	StateConnected
	StateRecording
	StateProcessing
	StateFinalizing
	StateClosed
)

// String returns the string representation of the State
func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateRecording:
		return "recording"
	case StateProcessing:
		return "processing"
	case StateFinalizing:
		return "finalizing"
	case StateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// VoiceStats contains statistics about the voice session
type VoiceStats struct {
	TotalDuration    float64 // Total audio duration in seconds
	TotalChunks      int     // Number of audio chunks sent
	TotalAudioBytes  int64   // Total audio bytes sent
	TotalTranscripts int     // Number of transcripts received
	TranscriptCount  int     // Alias for TotalTranscripts
	AudioBytesSent   int64   // Alias for TotalAudioBytes
	LastActivity     int64   // Unix timestamp of last activity
}

// Message is the interface for all WebSocket messages
type Message interface {
	isMessage()
}

// TranscriptTextMsg represents a transcript message from the server.
// The Anthropic voice API returns the transcript text in the "data" field.
// "text" is kept for any other providers that may use it.
type TranscriptTextMsg struct {
	Type     string  `json:"type"`
	Data     string  `json:"data"` // Anthropic API field
	Text     string  `json:"text"` // fallback for other providers
	IsFinal  bool    `json:"is_final"`
	Channel  int     `json:"channel"`
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
}

// GetText returns the transcript text, preferring the Data field over Text.
func (m TranscriptTextMsg) GetText() string {
	if m.Data != "" {
		return m.Data
	}
	return m.Text
}

func (TranscriptTextMsg) isMessage() {}

// MessageType returns the message type name
func (TranscriptTextMsg) MessageType() string { return "TranscriptText" }

// TranscriptEndpointMsg represents an endpoint message from the server
type TranscriptEndpointMsg struct {
	Type string `json:"type"`
}

func (TranscriptEndpointMsg) isMessage() {}

// MessageType returns the message type name
func (TranscriptEndpointMsg) MessageType() string { return "TranscriptEndpoint" }

// TranscriptErrorMsg represents an error message from transcription
type TranscriptErrorMsg struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (TranscriptErrorMsg) isMessage() {}

// MessageType returns the message type name
func (TranscriptErrorMsg) MessageType() string { return "TranscriptError" }

// ServerErrorMsg represents a server error
type ServerErrorMsg struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (ServerErrorMsg) isMessage() {}

// MessageType returns the message type name
func (ServerErrorMsg) MessageType() string { return "ServerError" }

// KeepAliveMsg represents a keep-alive message
type KeepAliveMsg struct {
	Type string `json:"type"`
}

func (KeepAliveMsg) isMessage() {}

// MessageType returns the message type name
func (KeepAliveMsg) MessageType() string { return "KeepAlive" }

// CloseStreamMsg represents a close stream message
type CloseStreamMsg struct {
	Type string `json:"type"`
}

func (CloseStreamMsg) isMessage() {}

// MessageType returns the message type name
func (CloseStreamMsg) MessageType() string { return "CloseStream" }
