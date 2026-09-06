package serve

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// eventSchemaVersion is the Event v1 contract's schema version, per
// docs/architecture/swarm-attach/adr-004-versioned-contracts.md's "Event
// contract": "schema_version, event_id, stream_id, sequence, kind, and at
// ... major.minor is the serialized schema version ... The JSON spelling
// is a string so 1.10 cannot be confused with a decimal number."
//
// NOTE ON attachcontract: CONTRACT.md (P04's governing task list) directs
// this file to build on swarm-sdk/internal/attachcontract's Event v1
// envelope/gap types (P04.A's output, a sibling worker in this same
// round). That package did not exist anywhere in this working tree at any
// point this file was edited (verified repeatedly via `find`/`grep` across
// swarm-sdk/internal — see the final report for this task). Per this
// task's explicit instruction ("If attachcontract is not yet
// complete/compiling when you start, proceed with the CURRENT
// swarm-sdk/serve/stream.go shape ... do not block indefinitely"), the
// Event v1 shape (schema_version/event_id/stream_id/sequence/kind/at, a
// snake_case source object, a typed gap payload, and an exhaustive
// per-kind payload discriminator switch) is implemented directly in this
// file instead of imported. The JSON field names below match ADR-004
// exactly, so swapping EventEnvelope/EventSourceEnvelope/GapPayload for
// attachcontract's equivalents later is a pure Go-side type substitution
// with no wire-format change.
const eventSchemaVersion = "1.0"

// EventKindGap is the EventEnvelope.Kind value SSEHandler emits when a
// resume cursor (Last-Event-ID / ?after=) predates EventLog's currently
// retained history. ADR-004's Event contract requires "a typed gap event
// for a cursor older than retained history" — never a silent skip to the
// oldest retained event and never a silently dropped request.
const EventKindGap = "gap"

// GapPayload is the Kind == EventKindGap payload. It tells a resuming
// client precisely what happened instead of leaving it to infer a
// discontinuity from a jump in sequence numbers: the cursor it asked to
// resume after, the oldest sequence EventLog still retains, and the
// current high-water mark it can restart live-tailing from.
type GapPayload struct {
	// Requested is the sequence number the client asked to resume after
	// (from Last-Event-ID or ?after=).
	Requested uint64 `json:"requested"`
	// Oldest is the oldest sequence number EventLog currently retains.
	// Requested < Oldest-1 is what triggers this gap event: every event in
	// (Requested, Oldest) was evicted from the ring buffer and cannot be
	// replayed.
	Oldest uint64 `json:"oldest"`
	// Latest is EventLog's current high-water-mark sequence number at the
	// moment the gap was detected, so the client knows where a fresh
	// live-tail-only connection would resume from.
	Latest uint64 `json:"latest"`
}

// EventEnvelope is the JSON shape pushed for Client.Subscribe events.
//
// It is intentionally flat so TS/JS clients can dispatch on Kind without
// reaching into a discriminator union.  Agent carries the underlying
// IntermediateUpdate (when Kind == "agent"); Payload carries the typed
// non-agent payload struct otherwise.
//
// The SchemaVersion/EventID/StreamID/Sequence fields are ADR-004's Event
// v1 envelope additions. EncodeEvent alone (used directly by
// websocket.go's client.event notifications and by direct unit-test
// construction) always sets SchemaVersion but leaves EventID/StreamID/
// Sequence at their zero value: those three are only meaningful for an
// event that has actually passed through an *EventLog ring buffer (which
// is what assigns a durable stream identity and monotonic cursor).
// EventLog.appendLocked fills them in for every event it records,
// including SSE traffic. Zero-value Sequence (a nil *uint64) is
// indistinguishable on the wire from "not part of a resumable stream" —
// exactly the ADR-004 Compatibility behavior for events not backed by a
// replay-capable log ("unversioned events may be displayed without
// claiming replay completeness"; here the events ARE schema-versioned,
// but the same "no replay guarantee" caveat applies when no cursor was
// assigned).
type EventEnvelope struct {
	SchemaVersion string  `json:"schema_version,omitempty"`
	EventID       string  `json:"event_id,omitempty"`
	StreamID      string  `json:"stream_id,omitempty"`
	Sequence      *uint64 `json:"sequence,omitempty"`

	Kind    string               `json:"kind"`
	At      string               `json:"at,omitempty"`
	Agent   *AgentEnvelope       `json:"agent,omitempty"`
	Payload any                  `json:"payload,omitempty"`
	Source  *EventSourceEnvelope `json:"source,omitempty"`
}

// EventSourceEnvelope carries event provenance over the wire so a remote UI can
// route events to the right view — e.g. render sub-agent (Kind="subagent",
// AgentID set) activity in a separate window, peer activity by handle, etc.
// Omitted for ordinary local events.
//
// Field spellings: this struct carries BOTH the legacy camelCase fields
// (AgentID/PeerHandle/ConvID, JSON keys "agentID"/"peerHandle"/"convID")
// and ADR-004's canonical snake_case Event v1 fields (JSON keys
// "agent_id"/"peer_handle"/"conv_id"/"workspace_id") with identical
// values, additively — never removed, per ADR-004 Compatibility
// ("unversioned events may be displayed without claiming replay
// completeness") and CONTRACT.md ("Existing camelCase JSON field
// spellings remain accepted for legacy unversioned readers ... new/
// versioned traffic uses the canonical snake_case contract fields").
//
// Caller check performed before keeping the legacy fields (required by
// CONTRACT.md before deciding whether they must remain on the wire):
//   - swarm-sdk/gateway/web/app.js:1025 — the mobile PWA's own
//     multiplexed-conversation filter reads `env.source.convID` directly
//     off the raw SSE JSON to decide whether an event belongs to the
//     conversation currently on screen. Removing "convID" from the wire
//     would silently break that filter (every event would look like it
//     belongs to no conversation, or all conversations would bleed
//     together, depending on the exact fallback logic) with no compile-time
//     signal, since app.js is untyped JS consumed at runtime only.
//   - swarm-sdk/swarm-tui/cmd/swarmos/attach_cli.go and
//     swarm-sdk/swarm-tui/internal/chat/attach_sse.go (the thin
//     TUI-attach and in-app SSE clients) parse `kind`/`agent.type`/
//     `agent.update`/`payload` but do NOT read `source.agentID`/
//     `source.convID`/`source.peerHandle` today — so those two files
//     would not need the legacy fields, but app.js above does, so the
//     legacy fields are kept regardless.
//   - swarm-ic/backend/internal/server/serve_mount.go bridges /sse bytes
//     opaquely (`mx.SSEHandler().ServeHTTP(w, r)`) without parsing the
//     envelope in Go at all, so it has no opinion on field spelling.
type EventSourceEnvelope struct {
	Kind string `json:"kind,omitempty"`

	// Legacy camelCase spellings — see the doc comment above for exactly
	// which caller (app.js) still requires these on the wire.
	AgentID    string `json:"agentID,omitempty"`
	PeerHandle string `json:"peerHandle,omitempty"`
	ConvID     string `json:"convID,omitempty"`

	// Canonical Event v1 snake_case fields (ADR-004 "Event contract": "a
	// consistently named source object with snake-case correlation
	// identifiers"). WorkspaceID has no legacy JSON alias because
	// client.EventSource.WorkspaceID has no prior wire representation —
	// there is nothing to preserve compatibility with.
	AgentIDV1     string `json:"agent_id,omitempty"`
	PeerHandleV1  string `json:"peer_handle,omitempty"`
	ConvIDV1      string `json:"conv_id,omitempty"`
	WorkspaceIDV1 string `json:"workspace_id,omitempty"`
}

// AgentEnvelope wraps an agent.IntermediateUpdate with its UpdateType for
// transport.  IntermediateUpdate is an interface, so we tag it with the
// concrete kind to make decoding deterministic on the client side.
type AgentEnvelope struct {
	Type   string `json:"type"`
	Update any    `json:"update"`
}

// EncodeEvent maps a client.Event to its wire envelope.
func EncodeEvent(ev client.Event) EventEnvelope {
	out := EventEnvelope{SchemaVersion: eventSchemaVersion, Kind: string(ev.Kind)}
	if !ev.At.IsZero() {
		out.At = ev.At.Format(timeFormat)
	}
	if ev.Agent != nil {
		out.Agent = &AgentEnvelope{
			Type:   ev.Agent.UpdateType(),
			Update: ev.Agent,
		}
	}
	if payload := payloadForEvent(ev); payload != nil {
		out.Payload = payload
	}
	// Carry provenance for non-local events (sub-agents, peers, background) so a
	// remote UI can route them to a separate window.
	//
	// For LOCAL events we historically omitted Source entirely. We now still
	// emit it when a conversation id is present so multiplexing clients can
	// filter the stream by convID (additive: clients that ignored Source are
	// unaffected; the previously-emitted non-local shape is unchanged). Local
	// events with no convID continue to omit Source.
	emitSource := ev.Source.Kind != "" && ev.Source.Kind != client.SourceLocal
	if !emitSource && ev.Source.Kind == client.SourceLocal && ev.Source.ConvID != "" {
		emitSource = true
	}
	if emitSource {
		out.Source = &EventSourceEnvelope{
			Kind: string(ev.Source.Kind),

			AgentID:    ev.Source.AgentID,
			PeerHandle: ev.Source.PeerHandle,
			ConvID:     ev.Source.ConvID,

			AgentIDV1:     ev.Source.AgentID,
			PeerHandleV1:  ev.Source.PeerHandle,
			ConvIDV1:      ev.Source.ConvID,
			WorkspaceIDV1: ev.Source.WorkspaceID,
		}
	}
	return out
}

// payloadForEvent converts ev.Payload — an unconstrained `any` set by the
// client package — into the concrete DTO for ev.Kind via an exhaustive
// discriminator switch over every client.EventKind constant, per
// ADR-004's Event contract: "Each known kind has a concrete payload DTO.
// Producers convert runtime interface values to that DTO with an
// exhaustive discriminator switch." Every case type-asserts to the exact
// struct client/events.go (or client/approval.go for ApprovalRequest)
// documents for that EventKind, so a mismatched or forged payload never
// crosses the wire silently mistyped as something it isn't.
//
// client.EventAgent carries no Payload (the Agent field carries the
// update instead); client.EventStreamStart, EventStreamEnd,
// EventConfigLoaded, EventConfigSaved, and EventHistoryCleared carry no
// payload by design — see their emission sites in
// swarm-sdk/client/messaging.go and swarm-sdk/client/crud.go.
//
// An EventKind this switch does not recognize (a client package newer
// than this encoder) or a recognized Kind whose Payload does not type-
// assert to its documented struct (defensive: never fabricate a typed
// shape for data that doesn't match) falls through to the default case,
// which preserves the raw payload instead of dropping it. This is
// ADR-004 Compatibility's rule for "an unknown event kind under a
// compatible major": "ignored or preserved while still advancing the
// sequence cursor" — EventLog.appendLocked assigns a sequence number to
// every recorded envelope regardless of whether its Kind was recognized
// here, so cursor advancement never depends on this switch's outcome.
func payloadForEvent(ev client.Event) any {
	switch ev.Kind {
	case client.EventAgent:
		return nil
	case client.EventStreamStart, client.EventStreamEnd,
		client.EventConfigLoaded, client.EventConfigSaved,
		client.EventHistoryCleared:
		return nil
	case client.EventConvSwitched, client.EventConvCreated,
		client.EventConvDeleted, client.EventConvUpdated:
		if p, ok := ev.Payload.(client.ConvPayload); ok {
			return p
		}
	case client.EventModelChanged:
		if p, ok := ev.Payload.(client.ModelPayload); ok {
			return p
		}
	case client.EventModeChanged:
		if p, ok := ev.Payload.(client.ModePayload); ok {
			return p
		}
	case client.EventProfileChanged:
		if p, ok := ev.Payload.(client.ProfilePayload); ok {
			return p
		}
	case client.EventAgentChanged:
		if p, ok := ev.Payload.(client.AgentPayload); ok {
			return p
		}
	case client.EventHookChanged:
		if p, ok := ev.Payload.(client.HookPayload); ok {
			return p
		}
	case client.EventToolChanged:
		if p, ok := ev.Payload.(client.ToolPayload); ok {
			return p
		}
	case client.EventSystemPromptChanged:
		if p, ok := ev.Payload.(client.SystemPromptPayload); ok {
			return p
		}
	case client.EventContextSourceChanged:
		if p, ok := ev.Payload.(client.ContextSourcePayload); ok {
			return p
		}
	case client.EventConfigChanged:
		if p, ok := ev.Payload.(client.ConfigPayload); ok {
			return p
		}
	case client.EventCompaction:
		if p, ok := ev.Payload.(client.CompactionPayload); ok {
			return p
		}
	case client.EventThemeChanged:
		if p, ok := ev.Payload.(client.ThemePayload); ok {
			return p
		}
	case client.EventHistoryResult:
		if p, ok := ev.Payload.(client.HistoryResultPayload); ok {
			return p
		}
	case client.EventApprovalRequested:
		if p, ok := ev.Payload.(client.ApprovalRequest); ok {
			return p
		}
	case client.EventError:
		if p, ok := ev.Payload.(client.ErrorPayload); ok {
			return p
		}
	case client.EventPeerJoined:
		if p, ok := ev.Payload.(client.PeerJoinedPayload); ok {
			return p
		}
	case client.EventPeerLeft:
		if p, ok := ev.Payload.(client.PeerLeftPayload); ok {
			return p
		}
	case client.EventPeerStatus:
		if p, ok := ev.Payload.(client.PeerStatusPayload); ok {
			return p
		}
	case client.EventTaskDispatched:
		if p, ok := ev.Payload.(client.TaskDispatchedPayload); ok {
			return p
		}
	case client.EventTaskCompleted:
		if p, ok := ev.Payload.(client.TaskCompletedPayload); ok {
			return p
		}
	case client.EventTaskFailed:
		if p, ok := ev.Payload.(client.TaskFailedPayload); ok {
			return p
		}
	case client.EventTaskRetrying:
		if p, ok := ev.Payload.(client.TaskRetryingPayload); ok {
			return p
		}
	}
	return ev.Payload
}

// EncodeUpdate wraps a raw IntermediateUpdate the same way EncodeEvent does
// for streaming methods that emit per-call updates.
func EncodeUpdate(u agent.IntermediateUpdate) AgentEnvelope {
	return AgentEnvelope{
		Type:   u.UpdateType(),
		Update: u,
	}
}

// timeFormat is RFC3339Nano, matching the rest of the SDK's time encoding.
const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

// marshalEnvelope is a small helper used by SSE / the event log to turn an
// envelope into bounded-encoded bytes (see bounded_json.go — this never
// calls json.Marshal, a reachable Marshaler/TextMarshaler, or Error() on an
// arbitrary value). An envelope that does not fit or cannot be safely
// encoded falls back to a small fixed error payload carrying the same
// stable response_too_large/response_encoding_error category HTTP/WS use,
// so the stream never crashes or emits malformed JSON mid-flight; the
// fallback itself is small, fixed-shape, and always encodes successfully.
func marshalEnvelope(v any) []byte {
	b, err := encodeBoundedJSON(context.Background(), v)
	if err == nil {
		return b
	}
	data := responseEncodingErrorData
	if classifyBoundedError(err) == boundedEncodeOversize {
		data = responseTooLargeData
	}
	fallback := map[string]any{"kind": "error", "payload": map[string]string{"error": data}}
	if b, ferr := encodeBoundedJSON(context.Background(), fallback); ferr == nil {
		return b
	}
	return []byte(`{"kind":"error","payload":{"error":"response_encoding_error"}}`)
}
