// Package attachclient implements the thin-client half of the attach
// protocol per docs/architecture/swarm-attach/package-boundaries.md's
// "attachclient" section: resolve a daemon, negotiate contract versions,
// establish a client session, select workspace/conversation, issue RPC,
// consume/replay events, handle approval/cancel, and detach without
// stopping the daemon.
//
// Integration note (P04.A dependency): swarm-sdk/internal/attachcontract
// (the neutral Event v1 / RPC v1 contract package owned by P04.A this
// round) did not exist in this worktree when this package was written.
// Per this phase's contract, attachclient therefore defines its OWN
// interim negotiation types (Version, VersionRange, Capabilities,
// CompatibilityError in negotiate.go) and its own interim gap-signal type
// (GapError in cursor.go) instead of importing attachcontract. These are
// intentionally shaped to match ADR-004 ("major.minor is a JSON string,
// never a bare number"; a stable compatibility-error category; a typed gap
// event for a cursor older than retained history) so that swapping them
// for attachcontract's canonical types later is a mechanical rename, not a
// design change. attachclient never imports daemon, supervisor,
// presentationcontrol, concrete lan, engine internals, CLI parsing, TUI
// models, registry files, or OS process control.
package attachclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/identity"
)

// DefaultRPCTimeout bounds every RPC request/response round trip issued by
// the default HTTP Transport. This fixes the historical bug in
// cmd/swarmos/attach_cli.go's rpcCall, which used http.Client{Timeout: 0}
// (no deadline at all) — a single stuck daemon connection could hang the
// thin client forever.
const DefaultRPCTimeout = 15 * time.Second

// defaultSSEResponseHeaderTimeout bounds only the wait for the initial SSE
// response headers (i.e. "is anyone home"), never the lifetime of the
// stream itself once headers arrive — an SSE body is intentionally
// long-lived and must not be killed by a fixed-duration context.
const defaultSSEResponseHeaderTimeout = 10 * time.Second

// ─── Client interface (package-boundaries.md, verbatim) ────────────────────

// Client is attachclient's public contract, defined exactly as specified in
// docs/architecture/swarm-attach/package-boundaries.md's "attachclient"
// section. Discovery, transport, and credential sources are injected
// interfaces (Discoverer, Transport, CredentialSource below); this
// interface itself never hardcodes a concrete HTTP client.
type Client interface {
	Connect(context.Context, Target) (Session, error)
	Status(context.Context) (Status, error)
	Select(context.Context, Selection) error
	Submit(context.Context, Work) (Execution, error)
	Events(context.Context, Cursor) (EventStream, error)
	Approve(context.Context, ApprovalDecision) error
	Cancel(context.Context, ExecutionID) error
	Detach(context.Context) error
}

// ─── Injected dependency interfaces ────────────────────────────────────────

// Transport is the narrow wire boundary attachclient depends on. A
// production implementation over net/http + SSE is provided in this same
// file (NewHTTPTransport) so callers needing the default behavior don't
// have to implement Transport themselves, but Client never hardcodes it —
// tests and alternate transports inject their own.
type Transport interface {
	// RPC issues one request/response call against base and returns the
	// decoded JSON-RPC "result" member. Implementations MUST apply an
	// explicit deadline derived from ctx (never an unbounded call).
	RPC(ctx context.Context, base, method string, params any) (json.RawMessage, error)
	// OpenEvents opens a resumable event stream against base, requesting
	// replay from (after) the given Cursor when non-zero.
	OpenEvents(ctx context.Context, base string, from Cursor) (RawEventStream, error)
}

// CapabilityProvider is an OPTIONAL Transport capability. If the injected
// Transport also implements CapabilityProvider, Connect negotiates a
// protocol version/capability set before returning a Session (see
// negotiate.go). Transports that do not implement it are treated as legacy
// peers per ADR-004 Compatibility ("missing schema_version means legacy; it
// never means the newest version") and Connect succeeds without a
// negotiated version.
type CapabilityProvider interface {
	Capabilities(ctx context.Context, base string) (Capabilities, error)
}

// RawEventStream is one open, possibly-replaying connection to a daemon's
// event source. Next blocks until the next event, an error (including
// context cancellation), or stream end. Close releases the connection.
type RawEventStream interface {
	Next(ctx context.Context) (RawEvent, error)
	Close() error
}

// RawEvent is the Transport-level, not-yet-interpreted event: a discriminator
// Kind ("gap" is reserved for an explicit retained-history-exceeded signal),
// a best-effort resume Cursor for the event just delivered, and its raw JSON
// body.
type RawEvent struct {
	Kind    string
	Cursor  Cursor
	Payload json.RawMessage
}

// Discoverer resolves a swarm peer handle to an attachable base URL.
// attachclient never depends on a concrete presence/discovery
// implementation; callers inject one (or skip discovery entirely by setting
// Target.BaseURL directly, as today's cmd/swarmos/attach_cli.go does after
// its own a2a.GetPeer readiness wait).
type Discoverer interface {
	Resolve(ctx context.Context, handle string) (baseURL string, err error)
}

// CredentialSource supplies a bearer credential for base, or "" if none is
// required. attachclient never owns credential storage/lifecycle; it only
// consumes one at RPC/event-stream call time.
type CredentialSource interface {
	Credential(ctx context.Context, base string) (token string, err error)
}

// CursorStore is defined in cursor.go (colocated with Cursor/GapError).

// ─── Public DTOs ────────────────────────────────────────────────────────────

// Target names the daemon to attach to: either an already-resolved BaseURL
// (discovery skipped) or a Handle to resolve via the injected Discoverer.
type Target struct {
	Handle  string
	BaseURL string
}

// Session is the result of a successful Connect: client-session identity,
// the resolved base URL it is bound to, and (if negotiation ran) the
// selected contract version/capabilities.
type Session struct {
	ID                string
	Base              string
	DaemonInstanceID  string
	NegotiatedVersion Version
	Capabilities      []string
}

// Status is the daemon-reported session status (provider/model/mode/active
// conversation). It intentionally excludes streaming/token counters, which
// remain client-tracked from the event stream (package-boundaries.md:
// attachclient owns client-local view cache, not engine state).
type Status struct {
	Provider             string
	Model                string
	OperatingMode        string
	ActiveConversationID string
}

// Selection is session-scoped: it names the workspace/conversation THIS
// client is viewing. Two Client instances against the same daemon never
// share Selection state (attachclient invariant: "Selection is
// session-scoped and cannot mutate another client.").
type Selection struct {
	WorkspaceID    string
	ConversationID string
	New            bool // create a new conversation instead of switching to ConversationID
}

// Work is one generic unit of daemon RPC work submitted through Submit
// (e.g. sending a chat message). Method/Params are passed to the injected
// Transport verbatim, with a client-generated correlation ID attached (see
// Submit/Cancel correlation below).
type Work struct {
	Method string
	Params map[string]any
}

// ExecutionID canonically correlates a submitted Work item with its later
// Approve/Cancel calls (attachclient invariant: "Every work item carries
// canonical session/execution/attempt identity.").
type ExecutionID string

// Execution is the result of a successful Submit.
//
// AttemptID and CanonicalExecutionID (added by P07.B, "attempt identity
// wiring") carry the real, canonical identity.AttemptID/identity.ExecutionID
// minted by Submit at call time — see identity.NewAttemptID/NewExecutionID
// in internal/identity/identity.go. They are additive fields alongside the
// pre-existing, locally-derived correlation ID (ExecutionID/ID below,
// which stays "<sessionID>-<seq>" for backward compatibility with existing
// Cancel call sites keyed by it) so no existing caller of Submit/Execution
// is disrupted. These canonical values are what a caller now has available
// to thread through to a future journal-writing producer (per CONTRACT.md:
// "wire identity.NewAttemptID()/identity.NewExecutionID() into the real
// submission path so journal.Record.ExecutionID/AttemptID ... get
// populated by a real producer") — attachclient itself does not write to
// internal/journal (out of scope for this package/phase), it only
// generates and surfaces the identities.
type Execution struct {
	ID                   ExecutionID
	Result               json.RawMessage
	AttemptID            identity.AttemptID
	CanonicalExecutionID identity.ExecutionID
}

// Event is one decoded, application-visible event from Events(). Gap is
// non-nil exactly when Kind == "gap": an explicit signal that the
// requested resume Cursor is older than retained history, never a silent
// skip (attachclient invariant: "Reconnect resumes from an acknowledged
// cursor or reports a gap.").
type Event struct {
	Cursor  Cursor
	Kind    string
	Agent   AgentUpdate
	Payload json.RawMessage
	Gap     *GapError
}

// AgentUpdate mirrors today's daemon SSE envelope's "agent" object
// (Type/Update) — see eventEnvelope in cursor.go, which centralizes the
// decode that cmd/swarmos/attach_cli.go and
// swarm-tui/internal/chat/attach_sse.go previously duplicated as
// sseEnvelope/daemonSSEEnvelope.
type AgentUpdate struct {
	Type   string
	Update json.RawMessage
}

// EventStream is the caller-facing handle returned by Events(): a channel
// of decoded Events, a channel for a terminal error (e.g. bounded retry
// exhausted), and an explicit Close to release resources — Close never
// signals the daemon (see reconnect.go / Detach).
type EventStream struct {
	Events <-chan Event
	Errs   <-chan error
	Close  func()
}

// ApprovalDecision answers one daemon-issued approval request by its
// correlation CallID.
type ApprovalDecision struct {
	CallID string
	Allow  bool
}

// ─── Sentinel errors ────────────────────────────────────────────────────────

var (
	// ErrDetached is returned by every Client method once Detach has been
	// called (attachclient invariant: "Detach releases client resources
	// only." — after Detach, no further daemon calls are made at all).
	ErrDetached = errors.New("attachclient: client is detached")
	// ErrNotConnected is returned when a method that requires a prior
	// Connect is called before one succeeded.
	ErrNotConnected = errors.New("attachclient: not connected")
)

// AuthError reports an HTTP 401/403 from the daemon.
type AuthError struct {
	StatusCode int
	Message    string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("attachclient: auth error (%d): %s", e.StatusCode, e.Message)
}

// RPCError reports a JSON-RPC "error" member returned by the daemon.
type RPCError struct {
	Method  string
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("attachclient: rpc %s: %s", e.Method, e.Message)
}

// ─── Deps / constructor ─────────────────────────────────────────────────────

// Deps holds every injected dependency for New. All fields are optional;
// zero values fall back to production-safe defaults (a bounded-deadline
// HTTP Transport, an in-memory non-authoritative CursorStore, a bounded
// default BackoffPolicy, and RPC v1-major support only).
type Deps struct {
	Transport            Transport
	Discoverer           Discoverer
	Credentials          CredentialSource
	CursorStore          CursorStore
	Backoff              BackoffPolicy
	SupportedVersions    []VersionRange
	RequiredCapabilities []string
	RPCTimeout           time.Duration
}

// Impl is the concrete, production Client implementation. It is exported
// (rather than hidden behind New returning only the Client interface) so
// callers such as cmd/swarmos/attach_cli.go and
// swarm-tui/internal/chat/attach_sse.go can use a couple of narrow extra
// methods (Call, Selection, Session) that sit outside the package-boundaries
// mandated Client interface but are still implemented with the exact same
// injected Transport/negotiation/reconnect/cursor plumbing.
type Impl struct {
	deps Deps

	mu            sync.Mutex
	base          string
	session       Session
	selection     Selection
	cursorNS      string
	detached      bool
	execSeq       uint64
	inflightExec  map[ExecutionID]struct{}
	streamCancels []func()
}

// var _ Client = (*Impl)(nil) gives a compile-time guarantee that Impl
// continues to satisfy the Client interface even as either evolves.
var _ Client = (*Impl)(nil)

// New constructs a production-ready attachclient.Impl (which satisfies
// Client) from the given Deps, applying safe defaults for any zero fields.
func New(deps Deps) *Impl {
	if deps.Transport == nil {
		deps.Transport = NewHTTPTransport(deps.RPCTimeout)
	}
	if deps.CursorStore == nil {
		deps.CursorStore = newMemCursorStore()
	}
	if (deps.Backoff == BackoffPolicy{}) {
		deps.Backoff = DefaultBackoffPolicy()
	}
	if len(deps.SupportedVersions) == 0 {
		deps.SupportedVersions = []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}}
	}
	return &Impl{
		deps:         deps,
		inflightExec: map[ExecutionID]struct{}{},
	}
}

// ─── Client methods ─────────────────────────────────────────────────────────

// newSessionID mints a canonical, parseable identity.ClientSessionID and
// returns its .String() form so existing call sites (which expect a plain
// string, e.g. Session.ID below) are untouched. Per P07.B CONTRACT.md
// ("identity.ClientSessionID ... is the ONLY canonical session key"),
// this replaces the prior ad hoc 8-random-byte "sess-<hex>" scheme (which
// never round-tripped through internal/identity.ParseClientSessionID)
// with a real, cryptographically random, collision-resistant identity
// produced by internal/identity — the package that
// journal.Record.ClientSessionID and every other session-identity
// consumer in this phase (including Worker C's approval-ownership DTO)
// also key off of. The returned string always parses successfully via
// identity.ParseClientSessionID (see session_test.go's round-trip test).
func newSessionID() string {
	return identity.NewClientSessionID().String()
}

// Connect resolves target (via Discoverer when only a Handle is given),
// optionally negotiates capabilities (see CapabilityProvider), and binds
// this Client to the resulting session. It never starts or stops a daemon.
func (c *Impl) Connect(ctx context.Context, target Target) (Session, error) {
	c.mu.Lock()
	detached := c.detached
	c.mu.Unlock()
	if detached {
		return Session{}, ErrDetached
	}

	base := strings.TrimRight(target.BaseURL, "/")
	if base == "" {
		if target.Handle == "" {
			return Session{}, fmt.Errorf("attachclient: connect: target has neither BaseURL nor Handle")
		}
		if c.deps.Discoverer == nil {
			return Session{}, fmt.Errorf("attachclient: connect: handle %q given but no Discoverer configured", target.Handle)
		}
		resolved, err := c.deps.Discoverer.Resolve(ctx, target.Handle)
		if err != nil {
			return Session{}, fmt.Errorf("attachclient: connect: resolve %q: %w", target.Handle, err)
		}
		base = strings.TrimRight(resolved, "/")
	}
	if base == "" {
		return Session{}, fmt.Errorf("attachclient: connect: resolved an empty base URL")
	}

	sess := Session{ID: newSessionID(), Base: base}

	if cp, ok := c.deps.Transport.(CapabilityProvider); ok {
		peerCaps, err := cp.Capabilities(ctx, base)
		if err != nil {
			return Session{}, fmt.Errorf("attachclient: connect: capabilities: %w", err)
		}
		version, err := Negotiate(c.deps.SupportedVersions, peerCaps, c.deps.RequiredCapabilities)
		if err != nil {
			return Session{}, err // *CompatibilityError — hard invariant.
		}
		sess.NegotiatedVersion = version
		sess.Capabilities = peerCaps.Capabilities
		sess.DaemonInstanceID = peerCaps.InstanceID
	}

	c.mu.Lock()
	c.base = base
	c.session = sess
	c.cursorNS = "attachclient/" + sess.ID
	c.mu.Unlock()

	return sess, nil
}

// call is the shared RPC path used by every Client method: it rejects a
// detached/unconnected client without touching the network, attaches a
// credential (if configured), and delegates to the injected Transport.
func (c *Impl) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	base := c.base
	detached := c.detached
	c.mu.Unlock()
	if detached {
		return nil, ErrDetached
	}
	if base == "" {
		return nil, ErrNotConnected
	}
	if c.deps.Credentials != nil {
		tok, err := c.deps.Credentials.Credential(ctx, base)
		if err != nil {
			return nil, fmt.Errorf("attachclient: credential: %w", err)
		}
		ctx = withCredential(ctx, tok)
	}
	return c.deps.Transport.RPC(ctx, base, method, params)
}

// Call issues an arbitrary daemon RPC method. It is NOT part of the
// package-boundaries.md Client interface (which only exposes the generic
// Submit for work items) but is needed by the CLI/TUI rewire for
// non-"work" control calls (mode/model/profile/compact/session listing,
// etc.) that today's attach_cli.go issues directly. It goes through the
// exact same deadline/credential/detached plumbing as every other method.
func (c *Impl) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.call(ctx, method, params)
}

// Status returns the daemon-reported session snapshot.
func (c *Impl) Status(ctx context.Context) (Status, error) {
	raw, err := c.call(ctx, "client.snapshot", map[string]any{})
	if err != nil {
		return Status{}, err
	}
	var snap struct {
		Provider      string `json:"Provider"`
		Model         string `json:"Model"`
		OperatingMode string `json:"OperatingMode"`
		ActiveConvID  string `json:"ActiveConvID"`
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		return Status{}, fmt.Errorf("attachclient: status: malformed snapshot: %w", err)
	}
	return Status{
		Provider:             snap.Provider,
		Model:                snap.Model,
		OperatingMode:        snap.OperatingMode,
		ActiveConversationID: snap.ActiveConvID,
	}, nil
}

// Select switches (or creates) the conversation this session is viewing.
// Selection is stored only on this *Impl — two Clients attached to the same
// daemon never observe each other's Selection.
func (c *Impl) Select(ctx context.Context, sel Selection) error {
	c.mu.Lock()
	detached := c.detached
	c.mu.Unlock()
	if detached {
		return ErrDetached
	}

	if sel.New {
		if _, err := c.call(ctx, "client.createConversation", map[string]any{}); err != nil {
			return err
		}
	} else if sel.ConversationID != "" {
		if _, err := c.call(ctx, "client.switchConversation", map[string]any{"id": sel.ConversationID}); err != nil {
			return err
		}
	}

	c.mu.Lock()
	c.selection = sel
	c.mu.Unlock()
	return nil
}

// Selection returns this Client's current, session-scoped selection. Not
// part of the Client interface; exposed for callers/tests that need to
// observe local state.
func (c *Impl) Selection() Selection {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.selection
}

// SessionInfo returns this Client's current Session (as returned by the
// most recent successful Connect). Not part of the Client interface.
func (c *Impl) SessionInfo() Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

// Submit issues one unit of daemon work (e.g. client.sendMessage) and
// returns a client-generated ExecutionID correlating it for later Cancel.
//
// Attempt identity wiring (P07.B CONTRACT.md, "wire
// identity.NewAttemptID()/identity.NewExecutionID() into the real
// submission path"): Submit is attachclient's real "submit work" entry
// point (the only Client-interface method that issues a unit of daemon
// work and mints a correlation identity for it — Call is the narrower,
// non-work-item escape hatch used for control calls). Each call to Submit
// therefore also mints a real, canonical identity.AttemptID and
// identity.ExecutionID via identity.NewAttemptID()/identity.NewExecutionID()
// (internal/identity/identity.go) — this is the "real producer" the
// journal.Record.ExecutionID/AttemptID fields (internal/journal/record.go,
// out of scope for this package/phase) need upstream of them. Both values
// are surfaced on the returned Execution (CanonicalExecutionID/AttemptID
// fields) so a caller never silently loses them, and both are ALSO sent
// to the daemon as RPC params ("clientAttemptID"/"canonicalExecutionID")
// alongside the pre-existing "clientExecutionID" correlation id, so a
// daemon-side handler that wants to thread them further has them
// available on the wire without a second round trip.
func (c *Impl) Submit(ctx context.Context, w Work) (Execution, error) {
	c.mu.Lock()
	if c.detached {
		c.mu.Unlock()
		return Execution{}, ErrDetached
	}
	c.execSeq++
	id := ExecutionID(fmt.Sprintf("%s-%d", c.session.ID, c.execSeq))
	c.inflightExec[id] = struct{}{}
	c.mu.Unlock()

	attemptID := identity.NewAttemptID()
	canonicalExecID := identity.NewExecutionID()

	params := map[string]any{}
	for k, v := range w.Params {
		params[k] = v
	}
	params["clientExecutionID"] = string(id)
	params["clientAttemptID"] = attemptID.String()
	params["canonicalExecutionID"] = canonicalExecID.String()

	res, err := c.call(ctx, w.Method, params)
	if err != nil {
		c.mu.Lock()
		delete(c.inflightExec, id)
		c.mu.Unlock()
		return Execution{}, err
	}
	return Execution{
		ID:                   id,
		Result:               res,
		AttemptID:            attemptID,
		CanonicalExecutionID: canonicalExecID,
	}, nil
}

// Cancel cancels a previously Submit-ted execution, correlated by
// ExecutionID. Cancelling an unknown or already-completed ID is a local
// error and never reaches the daemon.
func (c *Impl) Cancel(ctx context.Context, id ExecutionID) error {
	c.mu.Lock()
	detached := c.detached
	_, known := c.inflightExec[id]
	if known {
		delete(c.inflightExec, id)
	}
	c.mu.Unlock()
	if detached {
		return ErrDetached
	}
	if !known {
		return fmt.Errorf("attachclient: cancel: unknown or already-completed execution %q", id)
	}
	_, err := c.call(ctx, "client.cancel", map[string]any{"clientExecutionID": string(id)})
	return err
}

// Approve answers a daemon-issued approval request.
func (c *Impl) Approve(ctx context.Context, decision ApprovalDecision) error {
	if decision.CallID == "" {
		return fmt.Errorf("attachclient: approve: empty callID")
	}
	_, err := c.call(ctx, "client.respondApproval", map[string]any{
		"callID": decision.CallID,
		"allow":  decision.Allow,
	})
	return err
}

// Events opens (or resumes) the daemon's event stream from cursor. A zero
// Cursor consults the injected CursorStore for a previously acknowledged
// position (namespaced by this session) before falling back to "from
// start". See reconnect.go for the backoff/gap state machine.
func (c *Impl) Events(ctx context.Context, cursor Cursor) (EventStream, error) {
	c.mu.Lock()
	base := c.base
	detached := c.detached
	ns := c.cursorNS
	c.mu.Unlock()
	if detached {
		return EventStream{}, ErrDetached
	}
	if base == "" {
		return EventStream{}, ErrNotConnected
	}

	if cursor.Zero() {
		if saved, ok, _ := c.deps.CursorStore.Load(ctx, ns); ok {
			cursor = saved
		}
	}

	var tok string
	if c.deps.Credentials != nil {
		t, err := c.deps.Credentials.Credential(ctx, base)
		if err != nil {
			return EventStream{}, fmt.Errorf("attachclient: credential: %w", err)
		}
		tok = t
	}

	store := c.deps.CursorStore
	onAck := func(cur Cursor) {
		_ = store.Save(context.Background(), ns, cur)
	}

	stream := newEventStream(ctx, c.deps.Transport, base, cursor, c.deps.Backoff, tok, onAck)

	c.mu.Lock()
	c.streamCancels = append(c.streamCancels, stream.Close)
	c.mu.Unlock()

	return stream, nil
}

// Detach releases ONLY this client's resources (cancels any open event
// streams, marks the client detached so every subsequent method returns
// ErrDetached without a network call) and never issues a daemon RPC — the
// daemon and its agents keep running (attachclient invariant: "Detach
// releases client resources only.").
func (c *Impl) Detach(ctx context.Context) error {
	c.mu.Lock()
	if c.detached {
		c.mu.Unlock()
		return nil
	}
	c.detached = true
	cancels := c.streamCancels
	c.streamCancels = nil
	c.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	return nil
}

// ─── Credential propagation (context-scoped, transport-agnostic) ──────────

type credentialCtxKey struct{}

func withCredential(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	return context.WithValue(ctx, credentialCtxKey{}, token)
}

func credentialFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(credentialCtxKey{}).(string)
	return v, ok && v != ""
}

// ─── Default net/http + SSE Transport ──────────────────────────────────────

// httpTransport is the production Transport implementation. It is
// unexported; callers obtain it only via NewHTTPTransport, keeping Client
// dependent on the Transport interface rather than a concrete type.
type httpTransport struct {
	client     *http.Client
	rpcTimeout time.Duration
}

// NewHTTPTransport builds the default net/http + SSE Transport. rpcTimeout
// bounds every RPC call (DefaultRPCTimeout when <= 0); it never applies to
// the lifetime of an open SSE stream, which is instead bounded only by the
// caller's context (Detach/ctx cancellation) and a bounded wait for the
// initial response headers.
func NewHTTPTransport(rpcTimeout time.Duration) Transport {
	if rpcTimeout <= 0 {
		rpcTimeout = DefaultRPCTimeout
	}
	return &httpTransport{
		client: &http.Client{
			// No overall Timeout: an SSE body is intentionally long-lived.
			// RPC calls instead get an explicit per-call context deadline
			// below, and both RPC and SSE connect attempts are bounded by
			// ResponseHeaderTimeout so a stuck daemon cannot hang forever
			// waiting merely for headers.
			Transport: &http.Transport{
				ResponseHeaderTimeout: defaultSSEResponseHeaderTimeout,
			},
		},
		rpcTimeout: rpcTimeout,
	}
}

func (t *httpTransport) RPC(ctx context.Context, base, method string, params any) (json.RawMessage, error) {
	rctx, cancel := context.WithTimeout(ctx, t.rpcTimeout)
	defer cancel()

	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	if err != nil {
		return nil, fmt.Errorf("attachclient: rpc %s: encode request: %w", method, err)
	}
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, base+"/rpc", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("attachclient: rpc %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if tok, ok := credentialFromContext(ctx); ok {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		if rctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("attachclient: rpc %s: %w", method, context.DeadlineExceeded)
		}
		return nil, fmt.Errorf("attachclient: rpc %s: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &AuthError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}

	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("attachclient: rpc %s: malformed response: %w", method, err)
	}
	if env.Error != nil {
		return nil, &RPCError{Method: method, Message: env.Error.Message}
	}
	return env.Result, nil
}

func (t *httpTransport) OpenEvents(ctx context.Context, base string, from Cursor) (RawEventStream, error) {
	url := base + "/sse"
	if from.Sequence > 0 {
		url += fmt.Sprintf("?after=%d", from.Sequence)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("attachclient: sse connect: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	if from.Sequence > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(from.Sequence, 10))
	}
	if tok, ok := credentialFromContext(ctx); ok {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("attachclient: sse connect: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &AuthError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("attachclient: sse connect: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &httpEventStream{resp: resp, sc: sc}, nil
}

// httpEventStream reads one SSE frame at a time off resp.Body, translating
// "event:"/"data:"/"id:" lines into a RawEvent the same way
// cmd/swarmos/attach_cli.go and swarm-tui/internal/chat/attach_sse.go used
// to do independently (their duplicated scan loops are replaced by this one
// implementation).
type httpEventStream struct {
	resp *http.Response
	sc   *bufio.Scanner
}

func (s *httpEventStream) Next(ctx context.Context) (RawEvent, error) {
	var eventType, id, data string
	for s.sc.Scan() {
		if err := ctx.Err(); err != nil {
			return RawEvent{}, err
		}
		line := s.sc.Text()
		if line == "" {
			if data == "" {
				continue // blank line before any data: field — keep-alive
			}
			return newRawEventFromSSE(eventType, data, id), nil
		}
		if strings.HasPrefix(line, ":") {
			continue // SSE comment / keep-alive ping
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		switch field {
		case "event":
			eventType = value
		case "id":
			id = value
		case "data":
			if data == "" {
				data = value
			} else {
				data += "\n" + value
			}
		}
	}
	if err := s.sc.Err(); err != nil {
		return RawEvent{}, err
	}
	if err := ctx.Err(); err != nil {
		return RawEvent{}, err
	}
	return RawEvent{}, io.EOF
}

func (s *httpEventStream) Close() error {
	return s.resp.Body.Close()
}

// newRawEventFromSSE builds a RawEvent from one decoded SSE frame. The
// event kind is read from the JSON payload's "kind" field first (today's
// daemon shape) and falls back to the SSE "event:" field; the resume
// sequence is read from an optional JSON "sequence" field first (ADR-004
// Event v1 shape, once serve/stream.go emits it) and falls back to the SSE
// "id:" field.
func newRawEventFromSSE(eventType, data, id string) RawEvent {
	var probe struct {
		Kind     string `json:"kind"`
		Sequence int64  `json:"sequence"`
		StreamID string `json:"stream_id"`
	}
	_ = json.Unmarshal([]byte(data), &probe)

	kind := probe.Kind
	if kind == "" && eventType != "" && eventType != "message" {
		kind = eventType
	}
	seq := probe.Sequence
	if seq == 0 && id != "" {
		if n, err := strconv.ParseInt(id, 10, 64); err == nil {
			seq = n
		}
	}
	return RawEvent{
		Kind:    kind,
		Cursor:  Cursor{StreamID: probe.StreamID, Sequence: seq},
		Payload: json.RawMessage(data),
	}
}
