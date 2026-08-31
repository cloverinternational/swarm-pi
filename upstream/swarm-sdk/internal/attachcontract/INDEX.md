# attachcontract

`swarm-sdk/internal/attachcontract` — versioned Event v1 and RPC v1
capability-negotiation DTOs for Swarm attach, per
`docs/architecture/swarm-attach/adr-004-versioned-contracts.md` ("Event
contract", "RPC contract", "Compatibility", "Security") and
`docs/architecture/swarm-attach/package-boundaries.md`'s neutral-contract-
package rule.

## Purpose

A dependency-light leaf holding **versioned values and validation only**.
It defines the wire shape of Event v1 (the envelope streamed to attached
clients) and the RPC v1 support types needed for capability negotiation
(what a Swarm information/readiness method returns, and the stable
compatibility-error category returned when negotiation fails). It does
**not** define full RPC method/result DTOs for every existing JSON-RPC
method — only what attach/event delivery needs this phase.

## Import constraint (hard boundary)

This package imports **only the Go standard library**. It MUST NOT import
`internal/a2a`, `internal/lifecycle`, `internal/identity`, `serve`,
`attachclient`, or any other package in this repository. Verify with:

```sh
go list -deps ./internal/attachcontract | grep -v '^github.com/Swarm-Code/mono/swarm-sdk/internal/attachcontract$' | grep '\.'
```

(the second `grep` selects import paths containing a dot, i.e. non-stdlib;
this command's output must be empty.)

## Not a second orchestrator (hard boundary)

Per package-boundaries.md: "A neutral contract package may contain
versioned values and validation only; it may not become a second
orchestrator." Concretely, this package contains **no**:

- HTTP/WS transport code (that is `swarm-sdk/serve`);
- reconnect/backoff logic or acknowledged-cursor bookkeeping (that is
  `swarm-sdk/attachclient`);
- event-hub/broadcast/fan-out logic (that is `swarm-sdk/serve`'s
  `EventLog`/`SSEHandler`).

Producers (`serve`) and consumers (`attachclient`, UI) convert to/from
these DTOs at their own boundaries; they must not maintain their own
hand-copied wire structs once wired to this package.

## Exported surface

### envelope.go — Event v1 envelope

- `const EventSchemaVersion = "1.0"` — the schema_version this build writes.
- `const MaxEnvelopeBytes` — size bound applied before allocation-heavy decode.
- `type Payload interface { EventKind() string }` — implemented by every
  concrete per-`kind` payload DTO.
- `type UnknownPayload struct { Kind string; Raw json.RawMessage }` — opaque,
  round-trippable preservation of an unrecognized `kind`'s payload bytes;
  decoding an envelope with an unrecognized kind under a compatible major
  never fails, so the sequence cursor can still advance.
- `type Source struct { Kind, AgentID, PeerHandle, ConversationID,
  WorkspaceID string }` — canonical snake_case (`agent_id`,
  `peer_handle`, `conversation_id`, `workspace_id`) provenance object.
  Its decoder also accepts legacy camelCase aliases
  (`agentID`/`peerHandle`/`convID`) from today's `serve/stream.go`
  `EventSourceEnvelope`, read-only, during migration; canonical fields win
  when both are present. The encoder only ever emits canonical spellings.
- `type Envelope struct { SchemaVersion, EventID, StreamID string; Sequence
  int64; Kind string; At time.Time; Source Source; Payload Payload }` — the
  Event v1 wire shape (`schema_version`, `event_id`, `stream_id`,
  `sequence`, `kind`, `at`, `source`, `payload`). Custom
  `MarshalJSON`/`UnmarshalJSON` enforce exact field casing, required-field
  presence, and unknown-top-level-field tolerance.
- `func NewEnvelope(streamID, eventID string, sequence int64, at time.Time,
  source Source, payload Payload) Envelope` — producer constructor; derives
  `Kind` from `payload.EventKind()` so a producer cannot desync the
  discriminator from the concrete DTO.
- `func DecodePayload(kind string, raw json.RawMessage) (Payload, error)` —
  the exhaustive kind-discriminator switch; unknown kinds decode to
  `UnknownPayload` rather than erroring.

### gap.go — typed sequence-gap event

- `const KindSequenceGap = "sequence_gap"`.
- `type GapPayload struct { StreamID string; RequestedCursor,
  OldestAvailableCursor int64 }` — reported, explicitly and typed (never a
  silent skip), when a requested resume cursor is older than retained
  history.
- `func NewGapPayload(streamID string, requestedCursor, oldestAvailableCursor
  int64) GapPayload`.

### capabilities.go — capability negotiation support types

- `type VersionRange struct { Major, MinMinor, MaxMinor int }` with
  `Valid()`/`Contains(Version) bool`.
- `type CapabilityAdvertisement struct { SupportedRanges []VersionRange;
  Capabilities []string }` with `HasCapability(name string) bool` — the DTO
  a Swarm information/readiness method returns.
- `type CompatibilityReason string` — closed set:
  `ReasonUnknownMajor`, `ReasonNoOverlap`, `ReasonMissingCapability`,
  `ReasonMalformedVersion`.
- `type CompatibilityError struct { Reason CompatibilityReason; Supported
  []VersionRange; Requested, Missing string }` implementing `error`. Its
  `Error()` text is built only from these typed fields — it never includes
  request payload content or secrets.

### version.go — major.minor version type and negotiation

- `type Version struct { Major, Minor int }` with `String()`,
  `MarshalJSON`/`UnmarshalJSON` (always a JSON string), and
  `Compare(Version) int`.
- `func ParseVersion(s string) (Version, error)` — strict `major.minor`
  parsing; rejects `""`, `"1"`, `"1.0.0"`, `"v1.0"`, non-numeric
  components, and internal whitespace. Failure returns a
  `*CompatibilityError{Reason: ReasonMalformedVersion}`.
- `func SelectVersion(local, remote CapabilityAdvertisement,
  requiredCapabilities []string) (Version, error)` — selects the highest
  mutually supported minor within a supported major; returns a typed
  `*CompatibilityError` for no-overlap, unknown-major, and
  missing-required-capability failures.
- `func DecodeEnvelope(data []byte, supportedMajors []int) (Envelope,
  error)` — strict decoder that extracts and validates `schema_version`
  and rejects an unsupported major **before** any kind-specific payload
  interpretation is attempted (`DecodePayload` never runs for a rejected
  major).

## Tests

- `envelope_test.go` — field-casing round trip, required-field, unknown-
  field tolerance, unknown-kind opaque preservation, malformed
  sequence/at rejection, max-size bound.
- `version_test.go` — malformed-version rejection table, valid parsing,
  JSON-string-only enforcement, version-range selection (compatible
  newer-minor, no-overlap, unknown-major, missing-capability),
  major-rejected-before-variant-data.
- `capabilities_test.go` — `CompatibilityError` never leaks a simulated
  secret/request-payload sentinel string.
- `fixtures_test.go` — golden Event v1 JSON fixture, producer/consumer
  round trip via `NewEnvelope`/`DecodeEnvelope`, and `FuzzDecodeEnvelope`
  proving bounded, panic-free decoding of malformed/oversized input.
