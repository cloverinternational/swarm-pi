// Package protocol defines the versioned Chrome extension RPC and event wire contracts.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome"
)

const (
	// RPCContractV1 is the canonical RPC contract written by this build.
	RPCContractV1 = "swarm.chrome.rpc/1.0"
	// EventsContractV1 is the canonical event contract written by this build.
	EventsContractV1 = "swarm.chrome.events/1.0"
	// MaxMessageBytes bounds protocol decoding before payload interpretation.
	MaxMessageBytes    = 1 << 20
	MaxIdentifierBytes = 256
	MaxListEntries     = 256
	MaxCapabilities    = 64
	// MaxSafeInteger is the largest integer represented exactly by the
	// extension's JavaScript Number wire decoder.
	MaxSafeInteger = uint64(1<<53 - 1)
)

// VersionRange advertises an inclusive minor range for one major version.
type VersionRange struct {
	Major    uint32 `json:"major"`
	MinMinor uint32 `json:"min_minor"`
	MaxMinor uint32 `json:"max_minor"`
}

// Valid reports whether a range is non-empty.
func (r VersionRange) Valid() bool { return r.MinMinor <= r.MaxMinor }

// Advertisement independently advertises RPC/events ranges and optional capabilities.
type Advertisement struct {
	RPC          []VersionRange `json:"rpc"`
	Events       []VersionRange `json:"events"`
	Capabilities []string       `json:"capabilities"`
}

// Negotiated is the independently selected pair of contract versions.
type Negotiated struct {
	RPC          string   `json:"rpc"`
	Events       string   `json:"events"`
	Capabilities []string `json:"capabilities"`
}

// Negotiate selects the highest mutually supported minor for both contracts
// and fails closed if a required peer capability is absent.
func Negotiate(local, peer Advertisement, requiredCapabilities []string) (Negotiated, *chrome.Error) {
	if !validAdvertisement(local) || !validAdvertisement(peer) ||
		len(requiredCapabilities) > MaxCapabilities || !validStrings(requiredCapabilities, MaxIdentifierBytes) {
		return Negotiated{}, chrome.NewError(chrome.ErrProtocolIncompatible, "Chrome protocol advertisement is malformed.")
	}
	rpc, ok := selectVersion(local.RPC, peer.RPC)
	if !ok {
		return Negotiated{}, chrome.NewError(chrome.ErrProtocolIncompatible, "No compatible Chrome RPC contract; update Swarm or the extension.")
	}
	events, ok := selectVersion(local.Events, peer.Events)
	if !ok {
		return Negotiated{}, chrome.NewError(chrome.ErrProtocolIncompatible, "No compatible Chrome events contract; update Swarm or the extension.")
	}
	for _, required := range requiredCapabilities {
		if !contains(peer.Capabilities, required) {
			err := chrome.NewError(chrome.ErrProtocolIncompatible, "Chrome extension is missing required capability "+required+".")
			err.Details["missing_capability"] = required
			return Negotiated{}, err
		}
	}
	caps := intersection(local.Capabilities, peer.Capabilities)
	return Negotiated{
		RPC:          fmt.Sprintf("swarm.chrome.rpc/%d.%d", rpc.Major, rpc.Minor),
		Events:       fmt.Sprintf("swarm.chrome.events/%d.%d", events.Major, events.Minor),
		Capabilities: caps,
	}, nil
}

type version struct{ Major, Minor uint32 }

func selectVersion(left, right []VersionRange) (version, bool) {
	var best version
	found := false
	for _, a := range left {
		if !a.Valid() {
			continue
		}
		for _, b := range right {
			if !b.Valid() || a.Major != b.Major {
				continue
			}
			low := max(a.MinMinor, b.MinMinor)
			high := min(a.MaxMinor, b.MaxMinor)
			if low > high {
				continue
			}
			candidate := version{a.Major, high}
			if !found || candidate.Major > best.Major ||
				(candidate.Major == best.Major && candidate.Minor > best.Minor) {
				best, found = candidate, true
			}
		}
	}
	return best, found
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func intersection(left, right []string) []string {
	var result []string
	for _, value := range left {
		if contains(right, value) && !contains(result, value) {
			result = append(result, value)
		}
	}
	if result == nil {
		return []string{}
	}
	return result
}

// Request is the extension RPC request envelope. FamilyCapability is bridge-only.
type Request struct {
	Contract         string          `json:"contract"`
	Kind             string          `json:"kind"`
	RequestID        string          `json:"request_id"`
	FamilyCapability string          `json:"family_capability"`
	Generation       uint64          `json:"generation"`
	Operation        string          `json:"operation"`
	DeadlineUnixMS   int64           `json:"deadline_unix_ms"`
	Payload          json.RawMessage `json:"payload"`
}

// ResponseStatus is the response success discriminator.
type ResponseStatus string

const (
	StatusOK    ResponseStatus = "ok"
	StatusError ResponseStatus = "error"
)

// TerminalState is the exactly-once request terminal state.
type TerminalState string

const (
	TerminalCompleted TerminalState = "completed"
	TerminalRejected  TerminalState = "rejected"
	TerminalFailed    TerminalState = "failed"
)

// Response is the terminal extension RPC response envelope.
type Response struct {
	Contract   string          `json:"contract"`
	Kind       string          `json:"kind"`
	RequestID  string          `json:"request_id"`
	ClaimID    string          `json:"claim_id"`
	Generation uint64          `json:"generation"`
	Operation  string          `json:"operation"`
	Status     ResponseStatus  `json:"status"`
	Terminal   TerminalState   `json:"terminal"`
	Result     json.RawMessage `json:"result"`
	Error      *chrome.Error   `json:"error"`
}

// Event is the asynchronous event envelope. RequestID is legal only for progress events.
type Event struct {
	Contract   string          `json:"contract"`
	Kind       string          `json:"kind"`
	EventID    string          `json:"event_id"`
	Sequence   uint64          `json:"sequence"`
	Event      string          `json:"event"`
	Generation uint64          `json:"generation"`
	RequestID  string          `json:"request_id,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

// Message is one of Request, Response, or Event.
type Message interface{ chromeMessage() }

func (Request) chromeMessage()  {}
func (Response) chromeMessage() {}
func (Event) chromeMessage()    {}

// DecodeMessage rejects unsupported contracts and unknown fields before returning a typed DTO.
func DecodeMessage(data []byte) (Message, error) {
	if len(data) > MaxMessageBytes {
		return nil, fmt.Errorf("chrome protocol: message exceeds %d bytes", MaxMessageBytes)
	}
	var discriminator struct {
		Contract string `json:"contract"`
		Kind     string `json:"kind"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return nil, fmt.Errorf("chrome protocol: malformed message: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("chrome protocol: malformed message: %w", err)
	}
	expected := RPCContractV1
	if discriminator.Kind == "event" {
		expected = EventsContractV1
	}
	if discriminator.Contract != expected {
		return nil, chrome.NewError(chrome.ErrProtocolIncompatible, "Chrome protocol version is incompatible; update Swarm or the extension.")
	}
	var message Message
	switch discriminator.Kind {
	case "request":
		if err := requireFields(fields, "contract", "kind", "request_id", "family_capability", "generation", "operation", "deadline_unix_ms", "payload"); err != nil {
			return nil, err
		}
		message = &Request{}
	case "response":
		if err := requireFields(fields, "contract", "kind", "request_id", "claim_id", "generation", "operation", "status", "terminal", "result", "error"); err != nil {
			return nil, err
		}
		message = &Response{}
	case "event":
		if err := requireFields(fields, "contract", "kind", "event_id", "sequence", "event", "generation", "payload"); err != nil {
			return nil, err
		}
		message = &Event{}
	default:
		return nil, fmt.Errorf("chrome protocol: unknown kind %q", discriminator.Kind)
	}
	if err := decodeStrict(data, message); err != nil {
		return nil, fmt.Errorf("chrome protocol: invalid %s: %w", discriminator.Kind, err)
	}
	if err := ValidateMessage(message); err != nil {
		return nil, err
	}
	return message, nil
}

func requireFields(fields map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("chrome protocol: required field %q is missing", name)
		}
	}
	return nil
}

// ValidateMessage checks envelope fields and terminal mappings.
func ValidateMessage(message Message) error {
	switch value := message.(type) {
	case *Request:
		if value.Contract != RPCContractV1 || value.Kind != "request" || value.RequestID == "" ||
			value.FamilyCapability == "" || !validWireUint(value.Generation) || value.Operation == "" ||
			!validWireInt(value.DeadlineUnixMS) || !isJSONObject(value.Payload) {
			return fmt.Errorf("chrome protocol: malformed request correlation fields")
		}
		if !validIdentifier(value.RequestID) || !validIdentifier(value.FamilyCapability) ||
			!validIdentifier(value.Operation) || len(value.Payload) > MaxMessageBytes {
			return fmt.Errorf("chrome protocol: request fields exceed bounds")
		}
	case *Response:
		if value.Contract != RPCContractV1 || value.Kind != "response" || value.RequestID == "" ||
			value.ClaimID == "" || !validWireUint(value.Generation) || value.Operation == "" {
			return fmt.Errorf("chrome protocol: malformed response correlation fields")
		}
		if !validIdentifier(value.RequestID) || !validIdentifier(value.ClaimID) ||
			!validIdentifier(value.Operation) || len(value.Result) > MaxMessageBytes {
			return fmt.Errorf("chrome protocol: response fields exceed bounds")
		}
		resultNull := isNull(value.Result)
		switch value.Terminal {
		case TerminalCompleted:
			if value.Status != StatusOK || resultNull || value.Error != nil {
				return protocolMismatch("completed requires ok, non-null result, and null error")
			}
		case TerminalRejected:
			if value.Status != StatusError || !resultNull || value.Error == nil ||
				value.Error.Execution != chrome.ExecutionNotStarted {
				return protocolMismatch("rejected requires error, null result, and not_started")
			}
		case TerminalFailed:
			if value.Status != StatusError || value.Error == nil ||
				(value.Error.Execution != chrome.ExecutionFailed &&
					value.Error.Execution != chrome.ExecutionIndeterminate) {
				return protocolMismatch("failed requires error execution failed or indeterminate")
			}
		default:
			return protocolMismatch("unknown terminal state")
		}
	case *Event:
		if value.Contract != EventsContractV1 || value.Kind != "event" || value.EventID == "" ||
			!validWireUint(value.Sequence) || value.Event == "" || !validWireUint(value.Generation) ||
			!isJSONObject(value.Payload) {
			return fmt.Errorf("chrome protocol: malformed event correlation fields")
		}
		if !validIdentifier(value.EventID) || !validIdentifier(value.Event) ||
			(value.RequestID != "" && !validIdentifier(value.RequestID)) || len(value.Payload) > MaxMessageBytes {
			return fmt.Errorf("chrome protocol: event fields exceed bounds")
		}
		progress := value.Event == "request_accepted" || value.Event == "request_started"
		if (value.RequestID != "") != progress {
			return protocolMismatch("request_id is required only for request progress events")
		}
	default:
		return fmt.Errorf("chrome protocol: unsupported message type %T", message)
	}
	return nil
}

// ValidateResponseCorrelation proves that a response belongs to the pending request.
func ValidateResponseCorrelation(response Response, pending Request, claimID string) error {
	if response.RequestID != pending.RequestID || response.ClaimID != claimID ||
		response.Generation != pending.Generation || response.Operation != pending.Operation {
		return protocolMismatch("response does not match pending claim, generation, request, or operation")
	}
	return nil
}

func protocolMismatch(reason string) error {
	err := chrome.NewError(chrome.ErrProtocolMismatch, "Chrome protocol message did not match the pending request.")
	err.Details["reason"] = reason
	return err
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > MaxIdentifierBytes {
		return false
	}
	for _, r := range value {
		if r <= '\x1f' || r == '\x7f' {
			return false
		}
	}
	return true
}
func validStrings(values []string, maxBytes int) bool {
	for _, value := range values {
		if value == "" || len(value) > maxBytes {
			return false
		}
	}
	return true
}
func validAdvertisement(value Advertisement) bool {
	return len(value.RPC) > 0 && len(value.RPC) <= MaxListEntries &&
		len(value.Events) > 0 && len(value.Events) <= MaxListEntries &&
		len(value.Capabilities) <= MaxCapabilities && validStrings(value.Capabilities, MaxIdentifierBytes)
}

// EncodedSizeOK reports whether a message can be emitted within the wire bound.
func EncodedSizeOK(message any) bool {
	raw, err := json.Marshal(message)
	return err == nil && len(raw) <= MaxMessageBytes
}
func isNull(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func isJSONObject(raw json.RawMessage) bool {
	if isNull(raw) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func validWireUint(value uint64) bool {
	return value > 0 && value <= MaxSafeInteger
}

func validWireInt(value int64) bool {
	return value > 0 && uint64(value) <= MaxSafeInteger
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func min(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

func max(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

// ParseContractVersion validates a namespaced contract spelling and returns its version.
func ParseContractVersion(contract, namespace string) (uint32, uint32, error) {
	prefix := namespace + "/"
	if !strings.HasPrefix(contract, prefix) {
		return 0, 0, fmt.Errorf("chrome protocol: contract %q is outside %q", contract, namespace)
	}
	var major, minor uint32
	if _, err := fmt.Sscanf(strings.TrimPrefix(contract, prefix), "%d.%d", &major, &minor); err != nil {
		return 0, 0, fmt.Errorf("chrome protocol: malformed contract %q", contract)
	}
	if contract != fmt.Sprintf("%s/%d.%d", namespace, major, minor) {
		return 0, 0, fmt.Errorf("chrome protocol: malformed contract %q", contract)
	}
	return major, minor, nil
}
