// Package attachcontract defines the versioned, dependency-light wire
// contracts shared by attach's event-delivery and RPC-capability-negotiation
// boundaries, per docs/architecture/swarm-attach/adr-004-versioned-contracts.md
// ("Event contract", "RPC contract", "Compatibility", "Security") and
// docs/architecture/swarm-attach/package-boundaries.md's neutral-contract-
// package rule.
//
// This package is a leaf: it imports ONLY the Go standard library. It holds
// versioned values and validation ONLY — never HTTP/WS transport code,
// never reconnect/backoff logic, never event-hub/broadcast logic. It is not
// a second orchestrator. Transport (swarm-sdk/serve), client-side reconnect
// and cursor bookkeeping (swarm-sdk/attachclient), and UI all convert to and
// from these DTOs at their own boundaries; they must not maintain their own
// hand-copied wire structs once wired to this package (ADR-004 "Contract
// ownership").
//
// See INDEX.md for the package's public surface.
package attachcontract

import (
	"encoding/json"
	"fmt"
	"time"
)

// EventSchemaVersion is the schema_version this build writes for every new
// Event v1 envelope it produces. It is always a JSON string (see
// version.go's Version type) so "1.10" can never be confused with a
// decimal — ADR-004 "Version model".
const EventSchemaVersion = "1.0"

// MaxEnvelopeBytes bounds the size of a single encoded Envelope this
// package's decoders will attempt to parse, applied BEFORE any allocation
// proportional to attacker-controlled input beyond this bound — ADR-004
// "Security": "Every decoder applies bounds before allocation." Callers
// needing a different bound for their transport (e.g. an already-bounded
// SSE frame reader) may pre-check length themselves; this constant is the
// contract-level backstop so a decoder invoked directly is never unbounded.
const MaxEnvelopeBytes = 1 << 20 // 1 MiB

// Payload is implemented by every concrete Event v1 payload DTO. A producer
// must convert its runtime interface value to a concrete Payload via an
// exhaustive kind switch before it crosses this contract boundary —
// unconstrained `any` values never do (ADR-004 "Event contract", "RPC
// contract"). EventKind returns the wire `kind` discriminator this payload
// corresponds to; it MUST match the constant the payload type is registered
// under in DecodePayload.
type Payload interface {
	EventKind() string
}

// UnknownPayload preserves the raw JSON bytes of a payload whose `kind` this
// build does not recognize. ADR-004 "Compatibility": "An unknown event kind
// under a compatible major can be ignored or preserved while still
// advancing the sequence cursor." Decoding an envelope with an unrecognized
// kind therefore never fails outright — it yields an UnknownPayload instead
// of a decode error, so callers can advance past it (or re-encode it
// unchanged) without having to understand its shape.
type UnknownPayload struct {
	// Kind is the unrecognized wire `kind` value this payload was decoded
	// under.
	Kind string
	// Raw is the exact, unmodified JSON bytes of the "payload" member.
	Raw json.RawMessage
}

// EventKind implements Payload by returning the original unrecognized kind
// string, so an UnknownPayload can be round-tripped through code that only
// inspects EventKind() without needing a type assertion.
func (u UnknownPayload) EventKind() string { return u.Kind }

// MarshalJSON re-emits an UnknownPayload's original bytes verbatim, so an
// envelope decoded with an unrecognized kind and then re-encoded is
// byte-for-byte faithful to its payload (modulo whitespace already
// normalized by the original decode).
func (u UnknownPayload) MarshalJSON() ([]byte, error) {
	if len(u.Raw) == 0 {
		return []byte("null"), nil
	}
	return u.Raw, nil
}

// Source carries Event v1 provenance using ADR-004's canonical snake_case
// correlation identifiers. It corresponds to today's unversioned
// swarm-sdk/serve/stream.go EventSourceEnvelope (Kind, AgentID, PeerHandle,
// ConvID, all camelCase on the wire) — that type is NOT edited by this
// package (a different worker owns wiring serve/stream.go to attachcontract
// this round); Source independently defines the canonical shape those
// fields are renamed to: AgentID -> agent_id, ConvID -> conversation_id,
// PeerHandle -> peer_handle. During migration, Source's decoder also
// accepts the legacy camelCase spellings as read-only aliases (never
// written by this package's encoder) per ADR-004 "Compatibility":
// "unversioned events may be displayed without claiming replay
// completeness." The canonical snake_case fields always win when both a
// canonical and legacy alias are present in the same object.
type Source struct {
	// Kind identifies the class of source: "local", "peer", "subagent",
	// "background", etc. Values are defined by producers, not this package.
	Kind string `json:"kind,omitempty"`
	// AgentID is the sub-agent's unique identifier when Kind == "subagent".
	AgentID string `json:"agent_id,omitempty"`
	// PeerHandle is the A2A handle of the remote peer when Kind == "peer".
	PeerHandle string `json:"peer_handle,omitempty"`
	// ConversationID is the conversation this event belongs to.
	ConversationID string `json:"conversation_id,omitempty"`
	// WorkspaceID optionally binds this event to a specific workspace/issue.
	WorkspaceID string `json:"workspace_id,omitempty"`
}

// sourceWire is Source's decode-only shape: it additionally accepts the
// legacy camelCase field spellings serve/stream.go uses today, as read-only
// migration aliases. It is never used for encoding.
type sourceWire struct {
	Kind             string `json:"kind"`
	AgentID          string `json:"agent_id"`
	LegacyAgentID    string `json:"agentID"`
	PeerHandle       string `json:"peer_handle"`
	LegacyPeerHandle string `json:"peerHandle"`
	ConversationID   string `json:"conversation_id"`
	LegacyConvID     string `json:"convID"`
	WorkspaceID      string `json:"workspace_id"`
}

// UnmarshalJSON accepts both Source's canonical snake_case fields and the
// legacy camelCase aliases described on the Source type. Unknown fields
// beyond those are tolerated (encoding/json's default behavior).
func (s *Source) UnmarshalJSON(data []byte) error {
	var w sourceWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("attachcontract: malformed source: %w", err)
	}
	s.Kind = w.Kind
	s.AgentID = firstNonEmpty(w.AgentID, w.LegacyAgentID)
	s.PeerHandle = firstNonEmpty(w.PeerHandle, w.LegacyPeerHandle)
	s.ConversationID = firstNonEmpty(w.ConversationID, w.LegacyConvID)
	s.WorkspaceID = w.WorkspaceID
	return nil
}

func firstNonEmpty(canonical, legacy string) string {
	if canonical != "" {
		return canonical
	}
	return legacy
}

// Envelope is the Event v1 wire envelope defined by ADR-004's "Event
// contract": schema_version, event_id, stream_id, sequence, kind, at, a
// canonical Source, and a kind-selected Payload.
type Envelope struct {
	// SchemaVersion is the "major.minor" contract version as a JSON string
	// (e.g. "1.0") — never a bare number, so "1.10" is never confused with
	// a decimal. Use version.go's ParseVersion to interpret it and
	// DecodeEnvelope to reject an unsupported major before this envelope's
	// payload is interpreted at all.
	SchemaVersion string `json:"schema_version"`
	// EventID uniquely identifies this event.
	EventID string `json:"event_id"`
	// StreamID identifies the logical event stream this envelope belongs
	// to; a resume cursor (Sequence) is only meaningful within one
	// StreamID.
	StreamID string `json:"stream_id"`
	// Sequence is a monotonically increasing cursor within StreamID. A
	// resume request older than the oldest retained Sequence yields a
	// typed GapPayload (see gap.go), never a silent skip.
	Sequence int64 `json:"sequence"`
	// Kind is the payload discriminator. Every known Kind has a concrete
	// Payload DTO (see DecodePayload); an unrecognized Kind under a
	// compatible major decodes to UnknownPayload rather than failing.
	Kind string `json:"kind"`
	// At is this event's production timestamp (RFC3339/RFC3339Nano on the
	// wire).
	At time.Time `json:"at"`
	// Source carries provenance using canonical snake_case correlation
	// identifiers.
	Source Source `json:"source"`
	// Payload is the Kind-selected concrete DTO, or UnknownPayload when
	// Kind is not recognized by this build. Nil when the envelope carries
	// no payload data.
	Payload Payload `json:"payload,omitempty"`
}

// envelopeWire is Envelope's decode-only shape. Every field is a pointer (or
// RawMessage) so a missing JSON key is distinguishable from an explicit
// zero value — encoding/json otherwise silently leaves a non-pointer field
// at its zero value for an absent key, which would make "required field
// missing" indistinguishable from "field present and zero".
type envelopeWire struct {
	SchemaVersion *string         `json:"schema_version"`
	EventID       *string         `json:"event_id"`
	StreamID      *string         `json:"stream_id"`
	Sequence      *int64          `json:"sequence"`
	Kind          *string         `json:"kind"`
	At            *string         `json:"at"`
	Source        *Source         `json:"source"`
	Payload       json.RawMessage `json:"payload"`
}

// UnmarshalJSON strictly decodes an Event v1 envelope: every required field
// (schema_version, event_id, stream_id, sequence, kind, at) must be present
// and well-formed; unknown top-level JSON fields are tolerated; an
// unrecognized `kind` preserves its payload as UnknownPayload instead of
// failing decode. It applies MaxEnvelopeBytes before allocating anything
// proportional to input size, so this method is safe to call directly on
// untrusted/oversized input (e.g. from a fuzz test) without a panic or
// unbounded allocation.
func (e *Envelope) UnmarshalJSON(data []byte) error {
	if len(data) > MaxEnvelopeBytes {
		return fmt.Errorf("attachcontract: envelope exceeds max size (%d > %d bytes)", len(data), MaxEnvelopeBytes)
	}
	var w envelopeWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("attachcontract: malformed envelope: %w", err)
	}
	if w.SchemaVersion == nil || *w.SchemaVersion == "" {
		return fmt.Errorf("attachcontract: envelope missing required field %q", "schema_version")
	}
	if w.EventID == nil || *w.EventID == "" {
		return fmt.Errorf("attachcontract: envelope missing required field %q", "event_id")
	}
	if w.StreamID == nil || *w.StreamID == "" {
		return fmt.Errorf("attachcontract: envelope missing required field %q", "stream_id")
	}
	if w.Sequence == nil {
		return fmt.Errorf("attachcontract: envelope missing required field %q", "sequence")
	}
	if w.Kind == nil || *w.Kind == "" {
		return fmt.Errorf("attachcontract: envelope missing required field %q", "kind")
	}
	if w.At == nil || *w.At == "" {
		return fmt.Errorf("attachcontract: envelope missing required field %q", "at")
	}
	at, err := time.Parse(time.RFC3339Nano, *w.At)
	if err != nil {
		return fmt.Errorf("attachcontract: envelope field %q is malformed: %w", "at", err)
	}

	payload, err := DecodePayload(*w.Kind, w.Payload)
	if err != nil {
		return err
	}

	e.SchemaVersion = *w.SchemaVersion
	e.EventID = *w.EventID
	e.StreamID = *w.StreamID
	e.Sequence = *w.Sequence
	e.Kind = *w.Kind
	e.At = at
	if w.Source != nil {
		e.Source = *w.Source
	} else {
		e.Source = Source{}
	}
	e.Payload = payload
	return nil
}

// MarshalJSON encodes an Envelope using the exact ADR-004 field spellings
// and casing (schema_version, event_id, stream_id, sequence, kind, at,
// source, payload). At is encoded as RFC3339Nano UTC.
func (e Envelope) MarshalJSON() ([]byte, error) {
	var payloadRaw json.RawMessage
	if e.Payload != nil {
		b, err := json.Marshal(e.Payload)
		if err != nil {
			return nil, fmt.Errorf("attachcontract: encoding envelope payload: %w", err)
		}
		payloadRaw = b
	}
	out := struct {
		SchemaVersion string          `json:"schema_version"`
		EventID       string          `json:"event_id"`
		StreamID      string          `json:"stream_id"`
		Sequence      int64           `json:"sequence"`
		Kind          string          `json:"kind"`
		At            string          `json:"at"`
		Source        Source          `json:"source"`
		Payload       json.RawMessage `json:"payload,omitempty"`
	}{
		SchemaVersion: e.SchemaVersion,
		EventID:       e.EventID,
		StreamID:      e.StreamID,
		Sequence:      e.Sequence,
		Kind:          e.Kind,
		At:            e.At.UTC().Format(time.RFC3339Nano),
		Source:        e.Source,
		Payload:       payloadRaw,
	}
	return json.Marshal(out)
}

// NewEnvelope constructs a producer-side Event v1 envelope. Kind is derived
// from payload.EventKind() (when payload is non-nil) so a producer cannot
// desync the `kind` discriminator from the concrete payload DTO it
// attaches — this is the "exhaustive discriminator switch" ADR-004
// requires happens on the producer side; producers must not pass
// unconstrained `any` here. SchemaVersion is always set to
// EventSchemaVersion, the version this build writes.
func NewEnvelope(streamID, eventID string, sequence int64, at time.Time, source Source, payload Payload) Envelope {
	kind := ""
	if payload != nil {
		kind = payload.EventKind()
	}
	return Envelope{
		SchemaVersion: EventSchemaVersion,
		EventID:       eventID,
		StreamID:      streamID,
		Sequence:      sequence,
		Kind:          kind,
		At:            at,
		Source:        source,
		Payload:       payload,
	}
}

// DecodePayload is the exhaustive kind-discriminator switch that selects a
// concrete Payload DTO for a known `kind`, or preserves an unrecognized
// kind's bytes as UnknownPayload. It never fails decode solely because
// `kind` is unrecognized — only a malformed payload for a KNOWN kind is a
// decode error (ADR-004 "Compatibility": unknown kinds "can be ignored or
// preserved while still advancing the sequence cursor").
//
// Every producer-known kind must be added here as a case that decodes into
// its concrete DTO; this is the "exhaustive discriminator switch" ADR-004's
// "Event contract" requires so unconstrained `any` payloads never cross
// this boundary.
func DecodePayload(kind string, raw json.RawMessage) (Payload, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	switch kind {
	case KindSequenceGap:
		var p GapPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("attachcontract: malformed %q payload: %w", kind, err)
		}
		return p, nil
	default:
		cp := make(json.RawMessage, len(raw))
		copy(cp, raw)
		return UnknownPayload{Kind: kind, Raw: cp}, nil
	}
}
