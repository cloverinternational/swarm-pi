// Package protocol defines the wire protocol for headless TUI automation.
//
// Protocol format:
//
//	[4 bytes: length][1 byte: type][payload...]
//
// All multi-byte integers are little-endian.
package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/lifecycle"
)

// MessageType identifies the type of message.
type MessageType uint8

const (
	// Commands (client -> server)
	TypeSendKey     MessageType = 0x01
	TypeSendKeys    MessageType = 0x02
	TypeSendText    MessageType = 0x03
	TypeResize      MessageType = 0x04
	TypeClick       MessageType = 0x05
	TypeScroll      MessageType = 0x06
	TypeSendImage   MessageType = 0x07
	TypeGetFrame    MessageType = 0x10
	TypeGetState    MessageType = 0x11
	TypeGetField    MessageType = 0x12
	TypeGetA2ADebug MessageType = 0x13
	TypeSubscribe   MessageType = 0x20
	TypeUnsubscribe MessageType = 0x21
	TypeNavigate    MessageType = 0x30
	TypeWaitScreen  MessageType = 0x31
	TypeWaitContent MessageType = 0x32

	// Responses (server -> client)
	TypeOK         MessageType = 0x80
	TypeError      MessageType = 0x81
	TypeFrame      MessageType = 0x82
	TypeState      MessageType = 0x83
	TypeFieldValue MessageType = 0x84
	TypeA2ADebug   MessageType = 0x85

	// Events (server -> client, pushed)
	TypeFrameUpdate  MessageType = 0x90
	TypeStateChange  MessageType = 0x91
	TypeScreenChange MessageType = 0x92

	// Live SDK event stream (server → client, pushed)
	// Client sends TypeSubscribeEvents; server streams TypeEventData NDJSON lines.
	TypeSubscribeEvents MessageType = 0x93
	TypeEventData       MessageType = 0x94
)

// Header is the message header.
const HeaderSize = 5

// MaxPayloadSize limits message size to 16MB.
const MaxPayloadSize = 16 * 1024 * 1024

// Message represents a protocol message.
type Message struct {
	Type    MessageType
	Payload []byte
}

// Encode encodes a message to bytes.
func (m *Message) Encode() []byte {
	length := uint32(len(m.Payload) + 1) // +1 for type byte
	buf := make([]byte, HeaderSize+len(m.Payload))
	binary.LittleEndian.PutUint32(buf[0:4], length)
	buf[4] = byte(m.Type)
	copy(buf[5:], m.Payload)
	return buf
}

// Decode decodes a message from bytes.
func Decode(data []byte) (*Message, error) {
	if len(data) < HeaderSize {
		return nil, fmt.Errorf("message too short: %d bytes", len(data))
	}

	length := binary.LittleEndian.Uint32(data[0:4])
	if length > MaxPayloadSize {
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}

	if uint32(len(data)) < HeaderSize-1+length {
		return nil, fmt.Errorf("incomplete message: have %d, need %d", len(data), HeaderSize-1+length)
	}

	return &Message{
		Type:    MessageType(data[4]),
		Payload: data[5 : 4+length],
	}, nil
}

// DecodeHeader decodes just the header to get the expected length.
func DecodeHeader(data []byte) (length uint32, msgType MessageType, err error) {
	if len(data) < HeaderSize {
		return 0, 0, fmt.Errorf("header too short: %d bytes", len(data))
	}
	length = binary.LittleEndian.Uint32(data[0:4])
	msgType = MessageType(data[4])
	return length, msgType, nil
}

// Command payloads

// SendKeyCmd is the payload for TypeSendKey.
type SendKeyCmd struct {
	Key string `json:"key"`
}

// SendKeysCmd is the payload for TypeSendKeys.
type SendKeysCmd struct {
	Keys []string `json:"keys"`
}

// SendTextCmd is the payload for TypeSendText.
type SendTextCmd struct {
	Text string `json:"text"`
}

// SendImageCmd is the payload for TypeSendImage. Data is base64-encoded image bytes
// without a data: URL prefix; callers may include Text as the user prompt.
type SendImageCmd struct {
	Text     string `json:"text,omitempty"`
	FileName string `json:"file_name,omitempty"`
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

// ResizeCmd is the payload for TypeResize.
type ResizeCmd struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// ClickCmd is the payload for TypeClick.
type ClickCmd struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// ScrollCmd is the payload for TypeScroll.
type ScrollCmd struct {
	X  int  `json:"x"`
	Y  int  `json:"y"`
	Up bool `json:"up"`
}

// GetFieldCmd is the payload for TypeGetField.
type GetFieldCmd struct {
	Path string `json:"path"`
}

// SubscribeCmd is the payload for TypeSubscribe.
type SubscribeCmd struct {
	Events []string `json:"events"` // "frame", "state", "screen"
}

// NavigateCmd is the payload for TypeNavigate.
type NavigateCmd struct {
	Screen string `json:"screen"`
}

// WaitScreenCmd is the payload for TypeWaitScreen.
type WaitScreenCmd struct {
	Screen    string `json:"screen"`
	TimeoutMs int    `json:"timeout_ms"`
}

// WaitContentCmd is the payload for TypeWaitContent.
type WaitContentCmd struct {
	Text      string `json:"text"`
	TimeoutMs int    `json:"timeout_ms"`
}

// Response payloads

// ErrorResponse is the payload for TypeError.
type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// FrameResponse is the payload for TypeFrame.
type FrameResponse struct {
	Width      int      `json:"width"`
	Height     int      `json:"height"`
	Content    string   `json:"content"`
	Lines      []string `json:"lines"`
	FrameNum   int      `json:"frame_num"`
	RenderTime int64    `json:"render_time_ns"`
}

// FieldValueResponse is the payload for TypeFieldValue.
type FieldValueResponse struct {
	Path  string `json:"path"`
	Value any    `json:"value"`
	Found bool   `json:"found"`
}

// A2ADebugResponse is the payload for TypeA2ADebug. It surfaces the live
// internal state of a TUI's A2A integration so external tools (`swarmos swarm
// attach <handle> debug`) can trace what's actually happening without
// guessing. New fields can be appended without breaking older clients —
// readers should tolerate unknown JSON keys.
type A2ADebugResponse struct {
	// Self identity
	Handle      string `json:"handle"`
	SessionID   string `json:"session_id"`
	EndpointURL string `json:"endpoint_url"`
	Workspace   string `json:"workspace,omitempty"`
	A2AEnabled  bool   `json:"a2a_enabled"`
	BinaryPath  string `json:"binary_path,omitempty"`
	Version     string `json:"version,omitempty"`

	// Agent state
	AgentState string `json:"agent_state,omitempty"` // "idle" | "executing" | "stopped" | "error"

	// Swarm visibility
	SwarmStatus      string `json:"swarm_status,omitempty"`       // "idle" | "working" | "busy" | "away"
	SwarmCurrentTask string `json:"swarm_current_task,omitempty"` // human-readable task
	SwarmModel       string `json:"swarm_model,omitempty"`

	// Conversation / message routing
	ActiveConversationID string `json:"active_conversation_id,omitempty"`
	PendingMessageCount  int    `json:"pending_message_count"` // size of A2A runtime's pending queue
	InboxCount           int    `json:"inbox_count"`           // unread messages in filesystem inbox

	// Tasks
	ActiveTaskCount int      `json:"active_task_count"` // tasks currently in spawnAsyncTask
	ActiveTaskIDs   []string `json:"active_task_ids,omitempty"`

	// Discovery
	KnownPeerCount   int      `json:"known_peer_count"`
	KnownPeerHandles []string `json:"known_peer_handles,omitempty"`

	// Recent inbound activity (informational, may be empty when none recorded)
	LastInboundFrom      string `json:"last_inbound_from,omitempty"`
	LastInboundAt        string `json:"last_inbound_at,omitempty"`
	LastInboundSwarmType string `json:"last_inbound_swarm_type,omitempty"`
	LastInboundTaskID    string `json:"last_inbound_task_id,omitempty"`

	// WebSocket server diagnostics. These are the raw counters from the
	// inbound WS path — exposing them lets a remote debug client see
	// EXACTLY where in the pipeline a message is being dropped:
	//   - WSUpgradeAttempts > 0 but WSUpgradeSuccess == 0:    handshake failing
	//   - WSUpgradeSuccess > 0 but WSMessagesReceived == 0:   socket idle (sender disconnected before sending?)
	//   - WSMessagesReceived > 0 but WSRequestCount == 0:     message type wasn't "request"
	//   - WSRequestCount > 0 but WSSendMessageCalls == 0:     method != "SendMessage" OR delegate not wired
	//   - WSSendMessageCalls > 0 but agent_state stays idle:  spawnAsyncTask path broken
	WSUpgradeAttempts    int64  `json:"ws_upgrade_attempts"`
	WSUpgradeSuccess     int64  `json:"ws_upgrade_success"`
	WSUpgradeFailures    int64  `json:"ws_upgrade_failures"`
	WSMessagesReceived   int64  `json:"ws_messages_received"`
	WSMessagesDispatched int64  `json:"ws_messages_dispatched"`
	WSRequestCount       int64  `json:"ws_request_count"`
	WSSendMessageCalls   int64  `json:"ws_send_message_calls"`
	WSSendMessageErrors  int64  `json:"ws_send_message_errors"`
	WSUnknownTypeCount   int64  `json:"ws_unknown_type_count"`
	WSActiveConnections  int    `json:"ws_active_connections"`
	WSHasDelegate        bool   `json:"ws_has_delegate"`
	WSHasRequestHandler  bool   `json:"ws_has_request_handler"`
	WSLastUpgradeError   string `json:"ws_last_upgrade_error,omitempty"`
	WSLastReceiveError   string `json:"ws_last_receive_error,omitempty"`

	// Free-form notes set by the host (e.g. "A2ARuntime: nil"). Useful for
	// surfacing initialization failures that don't fit the typed fields.
	Notes []string `json:"notes,omitempty"`

	// Lifecycle additively surfaces ADR-005's canonical status DTO (state,
	// intent, reason, and the explicitly-derived friendly DisplayLabel)
	// alongside the legacy free-form AgentState/SwarmStatus strings above.
	// It is a pointer with `omitempty` so it is entirely absent from the
	// wire payload — and therefore invisible to any existing consumer —
	// until a caller with real lifecycle evidence populates it; nothing in
	// this package populates it itself (this package holds no lifecycle
	// evidence of its own). Per ADR-005 "Readiness, publication, and
	// adapters", the canonical State inside must never be replaced by the
	// DisplayLabel it also carries — see internal/lifecycle.StatusView.
	Lifecycle *lifecycle.StatusView `json:"lifecycle,omitempty"`

	// Timestamp the snapshot was taken (server clock).
	Timestamp string `json:"timestamp"`
}

// Helper functions

// NewCommand creates a command message.
func NewCommand(msgType MessageType, payload any) (*Message, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Message{Type: msgType, Payload: data}, nil
}

// NewOK creates an OK response.
func NewOK() *Message {
	return &Message{Type: TypeOK}
}

// NewError creates an error response.
func NewError(code int, message string) *Message {
	data, _ := json.Marshal(ErrorResponse{Code: code, Message: message})
	return &Message{Type: TypeError, Payload: data}
}

// NewFrame creates a frame response.
func NewFrame(width, height int, content string, lines []string, frameNum int, renderTime int64) *Message {
	data, _ := json.Marshal(FrameResponse{
		Width:      width,
		Height:     height,
		Content:    content,
		Lines:      lines,
		FrameNum:   frameNum,
		RenderTime: renderTime,
	})
	return &Message{Type: TypeFrame, Payload: data}
}

// NewState creates a state response.
func NewState(state any) *Message {
	data, _ := json.Marshal(state)
	return &Message{Type: TypeState, Payload: data}
}

// NewA2ADebug creates an A2A debug response message.
func NewA2ADebug(debug A2ADebugResponse) *Message {
	data, _ := json.Marshal(debug)
	return &Message{Type: TypeA2ADebug, Payload: data}
}

// ParsePayload parses the JSON payload into the given struct.
func (m *Message) ParsePayload(v any) error {
	return json.Unmarshal(m.Payload, v)
}
