# `internal/presentationcontrol`

## Purpose

This package implements ADR-003's presentation control plane as a NEW,
ADDITIVE, negotiated wire mode: a versioned JSON envelope
(`schema_version`/`plane`/`op`/`correlation_id`/`payload`/`error`), a
4-byte magic preface that lets a single Unix-domain-socket listener
distinguish this mode from the legacy binary automation protocol on first
read, and a minimal verb dispatcher (`presentation.ping`,
`presentation.capabilities`). It reuses `internal/attachcontract`'s
`Version`, `VersionRange`, and `CapabilityAdvertisement` types verbatim
(ADR-004's version/capability model) instead of redefining a parallel
shape.

Per this package's owning contract
(`.swarmflow/swarm-attach-architecture/p06-transport-discovery-simplification/CONTRACT.md`),
this package imports **only** `internal/attachcontract` and the Go standard
library — no `internal/a2a`, `internal/lan`, `internal/inbox`, or any
TUI/CLI package.

## Exported symbols

- `const Plane = "presentation"` — the fixed plane identifier every
  `Envelope` this package produces carries (ADR-003 plane.verb
  namespacing; this package speaks only the presentation plane).
- `var SchemaVersion = attachcontract.Version{Major: 1, Minor: 0}` — the
  schema version this package stamps onto envelopes it constructs. A `var`
  rather than a `const` because Go disallows struct-valued constants.
- `type Envelope struct{...}` — the wire shape:
  `schema_version`/`plane`/`op`/`correlation_id`/`payload,omitempty`/`error,omitempty`.
- `type EnvelopeError struct{ Category, Message string }` — the structured
  error carried by a failed `Envelope`.
- `type Capabilities = attachcontract.CapabilityAdvertisement` — type
  alias, not a redefinition.
- `func NewRequest(op string, payload any) (Envelope, error)` — builds a
  request envelope: JSON-marshals `payload` into `Envelope.Payload` (nil
  payload leaves `Payload` empty/omitted), generates a fresh
  `crypto/rand`-backed `CorrelationID` (never a counter or PID), and stamps
  `SchemaVersion`.
- `func NewErrorResponse(correlationID, category, message string) Envelope`
  — builds an error envelope with a nil `Payload` and a populated `Error`.
- `var Preface = [4]byte{'P','R','C','1'}` — see "Preface disambiguation"
  below.
- `func ReadPreface(conn net.Conn) (matched bool, wrapped net.Conn, err error)`
  — see "Preface mechanism" below.
- `type Dispatcher interface{ Serve(conn net.Conn) error }` and
  `func NewDispatcher(caps Capabilities) Dispatcher` — see "Verb set and
  framing" below.
- `const OpPing = "presentation.ping"`,
  `const OpCapabilities = "presentation.capabilities"` — the two verbs the
  default `Dispatcher` implements.
- `const CategoryMalformedEnvelope`, `CategoryUnknownOperation`,
  `CategoryInternalError` — the `EnvelopeError.Category` values the default
  `Dispatcher` emits.
- `const MaxEnvelopeSize = 1 << 20` (1 MiB) — the largest single NDJSON
  line `Serve` will accept before rejecting the connection.

## Preface disambiguation (exact numeric argument)

The legacy binary automation protocol
(`swarm-tui/internal/headless/automation/protocol.Message`) frames every
message as `[4-byte little-endian length][1-byte type][payload]`
(`protocol.HeaderSize = 5`), and `protocol.DecodeHeader`/`protocol.Decode`
reject any frame whose declared length exceeds
`protocol.MaxPayloadSize = 16*1024*1024 = 16,777,216` bytes.

`Preface`'s bytes are `{0x50, 0x52, 0x43, 0x31}` (ASCII `"PRC1"`). Read as
that same little-endian uint32 interpretation `protocol.DecodeHeader` uses
for a legacy frame's first 4 bytes, `Preface` decodes to:

```
0x31435250 = 826,495,568
```

`826,495,568 / 16,777,216 ≈ 49.27` — i.e. **~49.3x** larger than
`protocol.MaxPayloadSize`. Any legacy frame that happened to begin with
these exact 4 bytes would be immediately rejected by
`protocol.DecodeHeader`/`protocol.Decode` with "message too large" before a
type byte or payload is ever read. This makes leading with `Preface`
unambiguous: a connection that sends it is either speaking this package's
negotiated presentation-plane mode, or is a malformed/hostile legacy client
the legacy decoder would have rejected regardless. `preface_test.go`'s
`TestPrefaceLittleEndianDisambiguation` asserts this exact numeric
relationship directly (not just the two raw constants).

## Preface mechanism (peek-and-wrap, not consume-and-fail-over)

`net.Conn` has no native unread-byte/pushback support, so `ReadPreface`
wraps `conn` in a `bufio.Reader` and calls `Peek(4)`, which buffers those
bytes internally **without** advancing past them. It then:

- **On match**: calls `bufio.Reader.Discard(4)` (an in-buffer pointer
  advance only — no additional network I/O, cannot block or drop data) so
  the returned `wrapped` conn's first `Read` starts immediately at the
  presentation-plane envelope traffic that follows the preface on the
  wire, with the preface itself already stripped.
- **On mismatch**: does **not** discard anything, so the returned `wrapped`
  conn's first `Read`(s) replay the exact 4 peeked bytes, in original
  order, followed by the rest of the underlying stream — the legacy
  dispatch path can be handed `wrapped` directly with **zero bytes lost**.

Both cases return a `wrapped` `net.Conn` (`prefaceConn`) that embeds the
original `net.Conn` for every method except `Read` (so `Write`, `Close`,
deadlines, and addresses are unaffected) and delegates `Read` to the same
`bufio.Reader` used for the peek. `preface_test.go`'s
`TestReadPrefaceNotMatched` verifies exact byte-for-byte order preservation
over a real `net.Pipe()`, not just a length check.

## Verb set and framing

The default `Dispatcher` (from `NewDispatcher`) implements exactly two
verbs:

- `presentation.ping` — echoes `Envelope.Payload` back unchanged.
- `presentation.capabilities` — returns the `Capabilities` value the
  `Dispatcher` was constructed with as the response `Payload`.

**Framing choice: NDJSON (newline-delimited JSON), not length-prefixed.**
`Serve` reads exactly one JSON-encoded `Envelope` per `\n`-terminated line
and writes each response the same way. This matches this program's
existing NDJSON precedent (Phase 05's journal) and keeps the codec trivial
to inspect/tail with standard line-oriented tools. `Serve` bounds the
accepted line size to `MaxEnvelopeSize` (1 MiB): `bufio.Reader`'s own
`ReadString`/`ReadBytes` do **not** bound memory on their own (they keep
accumulating fragments across arbitrarily many internal buffer refills
until a delimiter is found), so this package's internal
`readBoundedLine` helper checks the accumulated length after every
underlying `ReadSlice` call and aborts with an oversized-envelope error the
moment the bound is exceeded, before requesting any further data — this is
what actually prevents a malicious/broken peer that never sends `\n` from
exhausting memory (`dispatch_test.go`'s `TestDispatchOversizedEnvelopeRejected`
constructs a real payload larger than `MaxEnvelopeSize` and proves
rejection, not just a constant check).

On a malformed envelope (invalid JSON) or an oversized line, `Serve`:

1. writes a best-effort `NewErrorResponse` (category
   `CategoryMalformedEnvelope`) to the peer,
2. calls `conn.Close()` itself, and
3. returns a non-nil error — it never panics on malformed input
   (`TestDispatchMalformedEnvelopeClosesConnection`,
   `TestDispatchOversizedEnvelopeRejected`).

An unknown-but-well-formed `Op` is treated more leniently: `Serve` writes a
`CategoryUnknownOperation` error response and **keeps the connection open**
for further requests (`TestDispatchUnknownOperation`), since a decodable
envelope naming an unsupported verb is not itself a framing violation.

A clean peer-initiated close (`conn` returns `io.EOF` with no pending
partial line) makes `Serve` return `nil`.

## Scope

This package implements ADR-003's presentation-plane envelope/dispatcher/
capability-negotiation subset described in this phase's `CONTRACT.md`
("In scope this phase", item 1). Per ADR-003's own "Compatibility" section
("The attached server may retain its existing binary frame format..."),
this package **adds a coexisting negotiated mode**; it does **not** retire,
deprecate, or replace the legacy binary protocol
(`swarm-tui/internal/headless/automation/protocol`) for any existing
client. Explicitly out of scope for this package this phase (see
`CONTRACT.md`'s "Deferred this phase" list):

- Full binary-protocol retirement across the gnet headless server, the
  automation client, or `swarm-ic/backend`'s independent reimplementation
  (a separate Go module entirely out of this worktree's build graph) —
  deferred to Phase 08.
- Wiring this package into `attached.go`'s negotiated-mode branch — owned
  by a different worker (P06.C) in this phase; this package does not
  import or modify `attached.go`.
- Reimplementing every existing legacy attached-server verb
  (`send_key`/`get_frame`/`get_state`/subscriptions/etc.) on the
  presentation plane — this phase's `Dispatcher` intentionally supports
  only a minimal `ping`/`capabilities` verb set; a later phase migrates
  additional verbs as needed.
- A production discovery broker abstraction or multi-interface ranking —
  unrelated to this package; deferred to Phase 08 as part of the LAN
  gossip work.
- Any TLS/transport-security layer for the presentation-plane socket
  itself — out of scope; the existing Unix-socket
  directory/socket-permission and peer-uid checks in `attached.go` remain
  the security boundary for this plane per ADR-003's "Security" section.
