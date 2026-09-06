// Package server — attached.go
//
// AttachedServer embeds the automation control plane into a LIVE running
// swarmos TUI.  Unlike the gnet-based Server (which owns its own headless
// tea.Program), AttachedServer hooks into an already-running tea.Program by:
//
//   - Input injection : p.Send(tea.KeyPressMsg{...}) — bubbletea's official
//     external-event API, no reader hijacking needed.
//   - Frame capture   : model.View() — *chat.App uses pointer receivers, so
//     the stored reference always reflects current state.
//   - State           : state.Extract(model) — same as the gnet server.
//   - SDK events      : a chan []byte fed by the caller (main.go subscribes
//     to client.Subscribe and forwards NDJSON-encoded events).
//
// The server speaks the exact same binary protocol as the existing gnet Server,
// so the existing tui-client binary (and client.Client) work without change.
// It uses stdlib net.Listener (not gnet) because it needs no high-throughput
// multiplexing — a developer control socket handles one or two clients.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/attachcontract"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/presentationcontrol"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/inject"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/protocol"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/state"
	"golang.org/x/text/unicode/norm"
)

const (
	// maxAttachedFrameSize bounds the complete length-prefixed frame body
	// (type byte plus payload). Attached control requests are small JSON
	// commands, so 1 MiB leaves ample compatibility headroom while preventing
	// an untrusted local peer from forcing an allocation from a uint32 length.
	// maxAttachedFrameSize allows compressed screenshot/image injection from
	// Swarm Desktop Inspect while still bounding local control-socket memory use.
	//
	// This is the type-inclusive declared-length maximum from ADR-004's
	// presentation contract and ADR-007/Phase 01's CONTRACT.md: exactly
	// 8 MiB, so the maximum payload (declared length minus the one-byte
	// message type) is 8 MiB minus 1 byte. readMessageWithTimeout rejects
	// any declared length above this bound before allocating a payload
	// buffer, so an untrusted local peer cannot force an oversized
	// allocation merely by writing a large length prefix.
	maxAttachedFrameSize  = 8 << 20
	controlReadTimeout    = 10 * time.Second
	controlWriteTimeout   = 10 * time.Second
	socketProbeTimeout    = 250 * time.Millisecond
	maxWaitContentTimeout = 5 * time.Minute

	// Stable wire error for an oversized outbound attached payload.
	attachedPayloadTooLargeCode     = 413
	attachedPayloadTooLargeCategory = "attached_payload_too_large"
)

// ErrAttachedPayloadTooLarge is returned before deadline mutation, encode
// allocation, or connection writes when an outbound payload exceeds ADR-004's
// maximum of 8 MiB minus one byte.
var ErrAttachedPayloadTooLarge = errors.New(attachedPayloadTooLargeCategory)

// maxAttachedConnections bounds the number of simultaneous connections (and
// therefore simultaneous SDK-event subscribers, since any connection may
// send TypeSubscribeEvents) accepted by one AttachedServer instance. It is a
// package variable, not a const, so tests can shrink it to prove the cap
// deterministically without opening dozens of real sockets. A connection
// accepted over the cap is closed immediately in handleConn before any
// peer-credential check, message read, or dispatch — it is rejected as a
// resource-exhaustion guard, not as an authentication decision.
var maxAttachedConnections = 64

// AttachedServer controls a live running TUI via its tea.Program reference.
type AttachedServer struct {
	program  *tea.Program
	model    tea.Model
	modelMu  sync.RWMutex
	injector *inject.Injector
	eventCh  <-chan []byte // NDJSON-encoded SDK events from the caller
	addr     string

	mu       sync.Mutex
	listener net.Listener
	socket   os.FileInfo
	conns    map[net.Conn]struct{}
	done     chan struct{}
	stopped  bool
}

// SetModel updates the live model used for view/state extraction after a
// bootstrap shell promotes a fully initialized model.
func (s *AttachedServer) SetModel(model tea.Model) {
	if model == nil {
		return
	}
	s.modelMu.Lock()
	s.model = model
	s.modelMu.Unlock()
}

func (s *AttachedServer) currentModel() tea.Model {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return s.model
}

// A2ADebugger is implemented by host models that want to expose their A2A
// integration's live state over the attached control socket. The chat App
// implements this so `swarmos swarm attach <handle> debug` can return a
// rich snapshot instead of just the surface UI state. Hosts that don't have
// an A2A integration can simply not implement the interface; the attached
// server falls back to a stub response.
type A2ADebugger interface {
	GetA2ADebug() protocol.A2ADebugResponse
}

// AutomationImageReceiver is implemented by models that can accept a rich image
// prompt from the attach/control plane and route it through their normal LLM
// message pipeline.
type AutomationImageReceiver interface {
	ReceiveAutomationImage(protocol.SendImageCmd) error
}

// NewAttached creates a control server attached to a running tea.Program.
//
//   - program  : the live tea.Program (for p.Send key injection)
//   - model    : the tea.Model (for View() + state extraction); must be the
//     same pointer that bubbletea holds so View() is always current
//   - width/height : current terminal dimensions (for WindowSizeMsg)
//   - eventCh  : caller-provided channel of NDJSON event bytes; nil = no streaming
//   - socketPath : Unix socket path, e.g. "~/.swarmos/swarms/default/peers/h.ctrl"
func NewAttached(
	program *tea.Program,
	model tea.Model,
	width, height int,
	eventCh <-chan []byte,
	socketPath string,
) *AttachedServer {
	return &AttachedServer{
		program:  program,
		model:    model,
		injector: inject.NewInjector(width, height),
		eventCh:  eventCh,
		addr:     socketPath,
		conns:    make(map[net.Conn]struct{}),
		done:     make(chan struct{}),
	}
}

// Listen binds the Unix socket synchronously without starting the accept
// loop. Callers that advertise the socket path (peer presence files) should
// Listen FIRST, publish the path second, and then run Start — otherwise
// clients race the bind and see "connection refused" on a freshly advertised
// socket. Idempotent: a second call while bound is a no-op.
func (s *AttachedServer) Listen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return nil
	}
	if s.stopped {
		return fmt.Errorf("attached server: stopped")
	}
	parent := filepath.Dir(s.addr)
	if parent == "." {
		return fmt.Errorf("attached server: socket path must include a private parent directory")
	}
	absoluteParent, err := filepath.Abs(parent)
	if err != nil {
		return fmt.Errorf("attached server: resolve socket directory %q: %w", parent, err)
	}
	if absoluteParent == filepath.Dir(absoluteParent) {
		return fmt.Errorf("attached server: refusing to change filesystem root permissions")
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("attached server: create socket directory %q: %w", parent, err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return fmt.Errorf("attached server: secure socket directory %q: %w", parent, err)
	}
	if err := removeStaleSocket(s.addr); err != nil {
		return fmt.Errorf("attached server: prepare socket %q: %w", s.addr, err)
	}
	ln, err := net.Listen("unix", s.addr)
	if err != nil {
		return fmt.Errorf("attached server: listen %q: %w", s.addr, err)
	}
	if unixListener, ok := ln.(*net.UnixListener); ok {
		// Cleanup is identity-checked in Stop so a replacement socket cannot be
		// unlinked when this listener closes.
		unixListener.SetUnlinkOnClose(false)
	}
	if err := os.Chmod(s.addr, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(s.addr)
		return fmt.Errorf("attached server: secure socket %q: %w", s.addr, err)
	}
	info, err := os.Lstat(s.addr)
	if err != nil {
		_ = ln.Close()
		_ = os.Remove(s.addr)
		return fmt.Errorf("attached server: stat socket %q: %w", s.addr, err)
	}
	s.listener = ln
	s.socket = info
	return nil
}

// removeStaleSocket refuses to replace a live or ambiguous endpoint. It only
// unlinks a socket whose identity is unchanged after a refused dial.
func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("path exists and is not a Unix socket")
	}

	conn, dialErr := net.DialTimeout("unix", path, socketProbeTimeout)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("socket is already served by a live process")
	}
	if !isStaleSocketError(dialErr) && !errors.Is(dialErr, os.ErrNotExist) {
		return fmt.Errorf("cannot prove existing socket is stale: %w", dialErr)
	}

	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(info, current) {
		return fmt.Errorf("socket changed while checking staleness")
	}
	return os.Remove(path)
}

// Start begins listening on the Unix socket (binding it first if Listen was
// not already called). Blocks until ctx is cancelled or Stop() is called.
func (s *AttachedServer) Start(ctx context.Context) error {
	if err := s.Listen(); err != nil {
		return err
	}
	s.mu.Lock()
	ln := s.listener
	s.mu.Unlock()

	// Close listener when context is cancelled.
	go func() {
		<-ctx.Done()
		s.Stop()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			stopped := s.stopped
			s.mu.Unlock()
			if stopped {
				return nil
			}
			return fmt.Errorf("attached server: accept: %w", err)
		}
		go s.handleConn(conn)
	}
}

// Stop closes the listener.  Idempotent.
func (s *AttachedServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		close(s.done)
		if s.listener != nil {
			_ = s.listener.Close()
		}
		for conn := range s.conns {
			_ = conn.Close()
		}
		// Remove only the filesystem entry this server actually bound. Another
		// process may have replaced the path after our listener was unlinked.
		if s.socket != nil {
			if current, err := os.Lstat(s.addr); err == nil && os.SameFile(s.socket, current) {
				_ = os.Remove(s.addr)
			}
		}
	}
}

// SocketPath returns the Unix socket path.
func (s *AttachedServer) SocketPath() string { return s.addr }

// presentationCapabilities is the Capabilities advertisement handed to
// presentationcontrol.NewDispatcher for every negotiated-mode connection
// this phase. It advertises a single supported major.minor range
// (1.0-1.0), matching the brief's guidance that "a single version range
// {Major:1, MinMinor:0, MaxMinor:0} is sufficient" for this phase's
// minimal ping/capabilities verb set — this is not the full legacy verb
// registry (see presentationcontrol's own INDEX.md "Scope" section: it
// intentionally does not reimplement send_key/get_frame/get_state/etc.
// this phase).
var presentationCapabilities = presentationcontrol.Capabilities{
	SupportedRanges: []attachcontract.VersionRange{
		{Major: 1, MinMinor: 0, MaxMinor: 0},
	},
}

// ─── per-connection handler ───────────────────────────────────────────────────

// rejectConn writes a wire-level TypeError frame identifying WHY a
// connection is being refused, then lets the caller close it.
//
// Before this existed, every early-exit path in handleConn (server
// stopped, connection cap exceeded, peer-credential mismatch) just closed
// the raw net.Conn with zero response. That is not merely an unhelpful
// silence: if the peer had already written its request bytes (as every
// real client does immediately after connect — see client.Client.Connect
// followed by sendAndReceive's single Write) those bytes are still sitting
// unread in the connection's kernel receive buffer at Close time. Closing a
// stream socket with unread data pending makes the OS send RST instead of
// a graceful FIN, so the rejection a client actually observes is an opaque
// ECONNRESET/EPIPE ("connection reset by peer" / "broken pipe") with no
// indication of cause — indistinguishable, from the CLI, from a crashed
// peer or a degraded socket (issue #249's exact symptom: `swarm swarm
// attach <handle> state|debug|frame` resetting against live, idle-ready
// peers). Writing a real TypeError frame first — which every existing
// client already decodes into a readable "server error N: message" via
// sendAndReceive/GetState/GetFrame/GetA2ADebug's shared TypeError handling
// — turns that opaque reset into an actionable, distinguishable error.
//
// Best-effort: rejectConn never blocks the caller past controlWriteTimeout
// and its own write failure is deliberately ignored, since the connection
// is being torn down regardless.
func rejectConn(conn net.Conn, code int, message string) {
	_ = writeMessageWithTimeout(conn, protocol.NewError(code, message), controlWriteTimeout)
}

const (
	attachedServerStoppedCode       = 503
	attachedServerStoppedCategory   = "server_stopped"
	attachedTooManyConnsCode        = 429
	attachedTooManyConnsCategory    = "too_many_connections"
	attachedPeerUIDMismatchCode     = 403
	attachedPeerUIDMismatchCategory = "peer_credential_rejected"
)

func (s *AttachedServer) handleConn(conn net.Conn) {
	// Keep the exact map key separate from the wrapped connection used by the
	// protocol probe below. ReadPreface returns a decorating net.Conn, and
	// deleting that wrapper from s.conns would miss the original key and leak
	// one connection slot for every completed client request.
	trackedConn := conn
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		rejectConn(conn, attachedServerStoppedCode, attachedServerStoppedCategory+": control server is shutting down")
		_ = conn.Close()
		return
	}
	if len(s.conns) >= maxAttachedConnections {
		s.mu.Unlock()
		rejectConn(conn, attachedTooManyConnsCode, fmt.Sprintf("%s: control socket already has the maximum of %d connections", attachedTooManyConnsCategory, maxAttachedConnections))
		_ = conn.Close()
		return
	}
	s.conns[conn] = struct{}{}
	s.mu.Unlock()
	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		delete(s.conns, trackedConn)
		s.mu.Unlock()
	}()
	if err := validatePeerUser(conn); err != nil {
		rejectConn(conn, attachedPeerUIDMismatchCode, fmt.Sprintf("%s: %s", attachedPeerUIDMismatchCategory, err))
		return
	}

	// Presentation-plane negotiation: peek the first 4 bytes for
	// presentationcontrol.Preface WITHOUT destructively consuming them, so
	// a legacy client (the overwhelming default — including the
	// cross-module swarm-ic/backend consumer, which cannot be caught by
	// any build in this worktree) gets those exact same bytes back,
	// byte-for-byte and in order, via the returned wrapped conn on a
	// mismatch. validatePeerUser above still runs on the raw conn first,
	// exactly as before this change, so both the legacy and negotiated
	// paths remain subject to the identical peer-user authorization check.
	//
	// A read/write error from ReadPreface itself (e.g. the peer closed
	// before sending even 4 bytes) is intentionally NOT treated as fatal
	// here: matched is false in that case, wrapped still replays whatever
	// bytes were actually available, and handing it to the unmodified
	// legacy loop below reproduces the exact same short-read/EOF outcome
	// the pre-existing code would have hit reading directly from conn.
	matched, wrapped, _ := presentationcontrol.ReadPreface(conn)
	if matched {
		// Negotiated presentation-plane mode: hand off to the new
		// dispatcher instead of entering the legacy readMessage loop.
		// ReadPreface has already discarded the 4 preface bytes from the
		// wrapped conn's read buffer (see presentationcontrol's INDEX.md
		// "Preface mechanism"), so Serve reads envelope traffic starting
		// immediately after the preface.
		_ = presentationcontrol.NewDispatcher(presentationCapabilities).Serve(wrapped)
		return
	}

	// Legacy path — completely unmodified below except that conn.Read is
	// now var wrapped (byte-for-byte replaying whatever ReadPreface
	// peeked, including on a genuine mismatch) rather than conn.Read
	// directly; Write/Close/deadlines/addresses are unaffected since
	// wrapped promotes every non-Read net.Conn method straight through to
	// the same underlying conn.
	conn = wrapped

	buf := make([]byte, 0, 65536)
	writer := &attachedWriter{conn: conn}

	for {
		// Read next message (same protocol as gnet server).
		msg, remainder, err := readMessage(conn, buf)
		if err != nil {
			if err != io.EOF {
				// Connection closed — normal for a developer tool.
			}
			return
		}
		buf = remainder

		s.dispatchWithWriter(writer, msg)
		if writer.failed {
			return
		}
	}
}

// readMessage reads one complete protocol.Message from a net.Conn.
// Wire format: [4-byte LE length][1-byte type][payload...]
// where length = 1 (type byte) + len(payload).
func readMessage(conn net.Conn, _ []byte) (*protocol.Message, []byte, error) {
	return readMessageWithTimeout(conn, controlReadTimeout)
}

func readMessageWithTimeout(conn net.Conn, timeout time.Duration) (*protocol.Message, []byte, error) {
	// Persistent protocol clients may remain idle between commands. Wait for the
	// first frame byte without an idle timeout, then bound completion of the
	// started frame to prevent slow or partial writes from pinning a handler.
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return nil, nil, fmt.Errorf("clear read deadline: %w", err)
	}
	hdr := make([]byte, protocol.HeaderSize)
	if _, err := io.ReadFull(conn, hdr[:1]); err != nil {
		return nil, nil, err
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, nil, fmt.Errorf("set read deadline: %w", err)
	}
	if _, err := io.ReadFull(conn, hdr[1:]); err != nil {
		return nil, nil, err
	}
	length, msgType, err := protocol.DecodeHeader(hdr)
	if err != nil {
		return nil, nil, err
	}
	if length == 0 {
		return nil, nil, fmt.Errorf("malformed frame length: 0")
	}
	if length > maxAttachedFrameSize {
		return nil, nil, fmt.Errorf("frame too large: %d bytes (maximum %d)", length, maxAttachedFrameSize)
	}
	// Payload is (length - 1) bytes (length includes the type byte).
	payloadLen := int(length) - 1
	var payload []byte
	if payloadLen > 0 {
		payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return nil, nil, err
		}
	}
	return &protocol.Message{Type: msgType, Payload: payload}, nil, nil
}

// attachedWriter is the sole outbound path for one attached connection. Once a
// write fails it rejects subsequent frames. An oversized payload produces at
// most one small, known-bounded error frame before the connection terminates.
type attachedWriter struct {
	conn              net.Conn
	failed            bool
	oversizedReported bool
}

func (w *attachedWriter) write(msg *protocol.Message) error {
	if w.failed {
		return io.ErrClosedPipe
	}
	err := writeMessageWithTimeout(w.conn, msg, controlWriteTimeout)
	if err == nil {
		return nil
	}
	w.failed = true
	if errors.Is(err, ErrAttachedPayloadTooLarge) && !w.oversizedReported {
		w.oversizedReported = true
		// This fixed category frame is far below the payload cap. Use the raw
		// checked writer to avoid recursion while retaining write bounds.
		_ = writeMessageWithTimeout(
			w.conn,
			protocol.NewError(attachedPayloadTooLargeCode, attachedPayloadTooLargeCategory),
			controlWriteTimeout,
		)
	}
	return err
}

func (w *attachedWriter) ok() {
	_ = w.write(protocol.NewOK())
}

func (w *attachedWriter) err(code int, text string) {
	_ = w.write(protocol.NewError(code, text))
}

// sendMsg preserves the package test seam while routing through the same
// checked writer used by live attached connections.
func sendMsg(conn net.Conn, msg *protocol.Message) {
	_ = (&attachedWriter{conn: conn}).write(msg)
}

func writeMessageWithTimeout(conn net.Conn, msg *protocol.Message, timeout time.Duration) error {
	if msg == nil {
		return fmt.Errorf("write attached message: nil message")
	}
	if len(msg.Payload) > maxAttachedFrameSize-1 {
		return fmt.Errorf("%w: payload is %d bytes (maximum %d)",
			ErrAttachedPayloadTooLarge, len(msg.Payload), maxAttachedFrameSize-1)
	}
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	data := msg.Encode()
	for len(data) > 0 {
		written, err := conn.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

// sendOK sends a TypeOK response.
func sendOK(conn net.Conn) { sendMsg(conn, protocol.NewOK()) }

// sendErr sends a TypeError response.
func sendErr(conn net.Conn, code int, text string) {
	sendMsg(conn, protocol.NewError(code, text))
}

// ─── dispatch ─────────────────────────────────────────────────────────────────

func (s *AttachedServer) dispatch(conn net.Conn, msg *protocol.Message) {
	s.dispatchWithWriter(&attachedWriter{conn: conn}, msg)
}

func (s *AttachedServer) dispatchWithWriter(writer *attachedWriter, msg *protocol.Message) {
	switch msg.Type {

	// ── Input injection ──────────────────────────────────────────────────────

	case protocol.TypeSendKey:
		if s.program == nil {
			writer.err(501, "key injection not supported on headless daemon — use 'swarmos swarm task' to send work")
			return
		}
		var cmd protocol.SendKeyCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			writer.err(400, err.Error())
			return
		}
		s.program.Send(s.injector.Key(cmd.Key))
		writer.ok()

	case protocol.TypeSendKeys:
		if s.program == nil {
			writer.err(501, "key injection not supported on headless daemon — use 'swarmos swarm task' to send work")
			return
		}
		var cmd protocol.SendKeysCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			writer.err(400, err.Error())
			return
		}
		for _, k := range cmd.Keys {
			s.program.Send(s.injector.Key(k))
		}
		writer.ok()

	case protocol.TypeSendText:
		if s.program == nil {
			writer.err(501, "text injection not supported on headless daemon — use 'swarmos swarm task' to send work")
			return
		}
		var cmd protocol.SendTextCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			writer.err(400, err.Error())
			return
		}
		text := norm.NFC.String(cmd.Text)
		s.program.Send(s.injector.Text(text))
		writer.ok()

	case protocol.TypeSendImage:
		receiver, ok := s.currentModel().(AutomationImageReceiver)
		if !ok {
			writer.err(501, "image injection not supported by this peer")
			return
		}
		var cmd protocol.SendImageCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			writer.err(400, err.Error())
			return
		}
		cmd.Text = norm.NFC.String(cmd.Text)
		if err := receiver.ReceiveAutomationImage(cmd); err != nil {
			writer.err(400, err.Error())
			return
		}
		writer.ok()

	case protocol.TypeResize:
		if s.program == nil {
			writer.ok() // no-op on daemon; no terminal to resize
			return
		}
		var cmd protocol.ResizeCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			writer.err(400, err.Error())
			return
		}
		s.injector.Resize(cmd.Width, cmd.Height)
		s.program.Send(s.injector.WindowSize(cmd.Width, cmd.Height))
		writer.ok()

	// ── Mouse injection ──────────────────────────────────────────────────────
	// Desktop's Inspect-tab viewport forwards real clicks/scroll so the
	// remote TUI's own mouse handling (chat/app_update.go's MouseClickMsg/
	// MouseWheelMsg cases: text selection, cursor placement, message-viewport
	// scroll) drives exactly as it would from a real terminal. Injected via
	// s.program.Send like every other command here, so it works regardless
	// of whether real terminal mouse-mode is enabled on the remote PTY.

	case protocol.TypeClick:
		if s.program == nil {
			writer.err(501, "mouse injection not supported on headless daemon")
			return
		}
		var cmd protocol.ClickCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			writer.err(400, err.Error())
			return
		}
		for _, m := range s.injector.MouseClick(cmd.X, cmd.Y, inject.MouseLeft) {
			s.program.Send(m)
		}
		writer.ok()

	case protocol.TypeScroll:
		if s.program == nil {
			writer.err(501, "mouse injection not supported on headless daemon")
			return
		}
		var cmd protocol.ScrollCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			writer.err(400, err.Error())
			return
		}
		s.program.Send(s.injector.Scroll(cmd.X, cmd.Y, cmd.Up))
		writer.ok()

	// ── Frame / state reads ──────────────────────────────────────────────────

	case protocol.TypeGetFrame:
		// model.View() returns tea.View; .Content is the ANSI string.
		// *chat.App uses pointer receivers — this reference always reflects live state.
		content := s.currentModel().View().Content
		lines := strings.Split(content, "\n")
		resp := protocol.NewFrame(
			80, len(lines),
			content, lines,
			0, 0,
		)
		_ = writer.write(resp)

	case protocol.TypeGetState:
		fullState := state.Extract(s.currentModel())
		resp := protocol.NewState(fullState)
		_ = writer.write(resp)

	case protocol.TypeGetField:
		var cmd protocol.GetFieldCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			writer.err(400, err.Error())
			return
		}
		value, found := state.ExtractField(s.currentModel(), cmd.Path)
		data, _ := json.Marshal(protocol.FieldValueResponse{
			Path:  cmd.Path,
			Value: value,
			Found: found,
		})
		resp := &protocol.Message{Type: protocol.TypeFieldValue, Payload: data}
		_ = writer.write(resp)

	case protocol.TypeGetA2ADebug:
		// Surface the live A2A integration state for diagnostic tools.
		// The model must implement A2ADebugger; if it doesn't, we return
		// a stub response with Notes explaining why so callers can tell
		// "feature unsupported" apart from "no A2A activity to report".
		if dbg, ok := s.currentModel().(A2ADebugger); ok {
			resp := dbg.GetA2ADebug()
			resp.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
			_ = writer.write(protocol.NewA2ADebug(resp))
		} else {
			_ = writer.write(protocol.NewA2ADebug(protocol.A2ADebugResponse{
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
				Notes:     []string{"model does not implement A2ADebugger interface"},
			}))
		}

	// ── Wait for content ──────────────────────────────────────────────────────

	case protocol.TypeWaitContent:
		var cmd protocol.WaitContentCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			writer.err(400, err.Error())
			return
		}
		waitTimeout := time.Duration(cmd.TimeoutMs) * time.Millisecond
		if waitTimeout <= 0 || waitTimeout > maxWaitContentTimeout {
			writer.err(400, fmt.Sprintf("timeout_ms must be between 1 and %d", maxWaitContentTimeout.Milliseconds()))
			return
		}
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		timer := time.NewTimer(waitTimeout)
		defer timer.Stop()
		for {
			if strings.Contains(s.currentModel().View().Content, cmd.Text) {
				writer.ok()
				return
			}
			select {
			case <-s.done:
				return
			case <-timer.C:
				writer.err(408, fmt.Sprintf("timeout waiting for %q", cmd.Text))
				return
			case <-ticker.C:
			}
		}

	// ── SDK event stream ──────────────────────────────────────────────────────

	case protocol.TypeSubscribeEvents:
		// Stream NDJSON SDK events until the client disconnects or context ends.
		s.streamEvents(writer)

	default:
		writer.err(400, fmt.Sprintf("unsupported command on attached server: 0x%02x", msg.Type))
	}
}

// streamEvents drains eventCh and forwards each event as a TypeEventData frame
// to the connected client.  Returns when the client disconnects.
func (s *AttachedServer) streamEvents(writer *attachedWriter) {
	if s.eventCh == nil {
		writer.err(501, "event streaming not configured on this instance")
		return
	}
	// Acknowledge the subscription.
	writer.ok()

	for {
		select {
		case <-s.done:
			return
		case data, ok := <-s.eventCh:
			if !ok {
				return // channel closed
			}
			resp := &protocol.Message{
				Type:    protocol.TypeEventData,
				Payload: data,
			}
			if err := writer.write(resp); err != nil {
				return // client disconnected
			}
		}
	}
}
