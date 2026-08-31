package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/attachclient"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// ── SSE Client for attaching to agent daemons ──────────────────────
//
// The attach SSE path uses a persistent goroutine (startSSELoop) that
// holds an attachclient.Client's auto-reconnecting event stream and
// pushes every decoded event onto a buffered channel.  A BubbleTea Cmd
// (listenSSECmd) reads one event per Update cycle by blocking on that
// channel — the same pattern used by the voice event listener
// (listenForVoiceEvent).  The sseEventMsg/sseAttachmentState wire shape
// this file exposes to the rest of the chat package (app_update.go,
// app_clear.go, app_messaging.go) is UNCHANGED: only the transport and
// reconnect implementation underneath it moved into
// swarm-sdk/attachclient.
//
// Why persistent instead of reconnect-per-event:
// The old model called readSSE (which made a NEW HTTP connection) after
// every single event.  Between closing the old connection and opening
// the new one there was a window — typically a few milliseconds, but
// potentially longer under load — where the SSE subscription on the
// daemon was inactive.  Events emitted during that window (stream_start
// and the first content chunks that follow immediately after a user
// message is accepted) were silently dropped, making sent messages
// appear to receive no response. attachclient's Events() now resumes
// from the last acknowledged cursor on every reconnect (or reports an
// explicit gap — see the "_gap" EventType below) instead of quietly
// starting over, closing that window for good rather than merely
// shrinking it.

// sseEventMsg wraps a single SSE event received from the daemon.
type sseEventMsg struct {
	EventType string // SSE event: field (e.g. "agent", "stream_start")
	Data      string // SSE data: field (raw JSON)
}

// listenSSECmd returns a tea.Cmd that blocks on the sseState event
// channel and delivers the next event into the BubbleTea Update loop.
// Each sseEventMsg handler must call this again to keep the chain alive.
func (a *App) listenSSECmd() tea.Cmd {
	if a.sseState == nil || a.sseState.eventCh == nil {
		return nil
	}
	ch := a.sseState.eventCh
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil // channel closed; attachment ended
		}
		return ev
	}
}

// startSSELoop connects client to base (with capability negotiation when
// the daemon supports it) and drains its auto-reconnecting event stream,
// translating each attachclient.Event back into the sseEventMsg wire shape
// the rest of this package already expects. Reconnect/backoff, explicit
// deadlines, and cursor-based resume (or an explicit gap report instead of
// a silent drop) all live in attachclient now; this loop only bridges
// decoded events onto ch. It runs in its own goroutine started by
// startAttach. When ctx is cancelled (detach) or the stream ends
// permanently it closes ch so the listener cmd returns nil and the chain
// stops cleanly.
func startSSELoop(ctx context.Context, client *attachclient.Impl, base string, ch chan sseEventMsg) {
	defer close(ch)

	if _, err := client.Connect(ctx, attachclient.Target{BaseURL: base}); err != nil {
		if ctx.Err() == nil {
			select {
			case ch <- sseEventMsg{EventType: "_error", Data: fmt.Sprintf("SSE connect error: %v", err)}:
			default:
			}
		}
		return
	}

	stream, err := client.Events(ctx, attachclient.Cursor{})
	if err != nil {
		if ctx.Err() == nil {
			select {
			case ch <- sseEventMsg{EventType: "_error", Data: fmt.Sprintf("SSE connect error: %v", err)}:
			default:
			}
		}
		return
	}
	defer stream.Close()

	for {
		select {
		case ev, ok := <-stream.Events:
			if !ok {
				return
			}
			msg := sseEventMsg{Data: encodeSSEEventData(ev)}
			if ev.Kind == "gap" {
				// Never silently skip a replay gap: surface it as a
				// distinct sentinel EventType so handleDaemonSSEEvent can
				// notify the user instead of feeding a payload-less "gap"
				// event through the normal transcript renderer.
				msg.EventType = "_gap"
				if ev.Gap != nil {
					msg.Data = fmt.Sprintf("event history gap on stream %q: requested after %d, oldest retained %d",
						ev.Gap.StreamID, ev.Gap.RequestedAfter, ev.Gap.OldestRetained)
				}
			}
			select {
			case ch <- msg:
			case <-ctx.Done():
				return
			}
		case err, ok := <-stream.Errs:
			if ok && err != nil && ctx.Err() == nil {
				select {
				case ch <- sseEventMsg{EventType: "_error", Data: fmt.Sprintf("SSE stream error: %v", err)}:
				default:
				}
			}
			return
		case <-ctx.Done():
			return
		}
	}
}

// encodeSSEEventData re-serializes an already-decoded attachclient.Event
// back into the "kind"/"agent.type"/"agent.update"/"payload" JSON shape
// applyDaemonEvent expects. applyDaemonEvent's `func(data string) bool`
// signature (and the exact JSON shapes it is fed) are fixed by
// attach_render_test.go, an existing test file outside this phase's
// attach_sse.go scope, so this file cannot switch that function to consume
// a typed attachclient.Event directly — this is the minimal round trip
// needed to keep that locked contract while still sharing attachclient's
// one canonical decode (attachclient.DecodeEnvelope, used inside
// applyDaemonEvent below) instead of hand-copying a second envelope struct.
func encodeSSEEventData(ev attachclient.Event) string {
	b, err := json.Marshal(map[string]any{
		"kind": ev.Kind,
		"agent": map[string]any{
			"type":   ev.Agent.Type,
			"update": ev.Agent.Update,
		},
		"payload": ev.Payload,
	})
	if err != nil {
		return `{"kind":""}`
	}
	return string(b)
}

// ── Render daemon SSE events into the transcript ─────────────────────

// attachBanner returns a compact "ATTACHED → handle" indicator for the chat
// header while attached to a daemon, so the user knows input is being routed to
// the daemon (not the local agent).  Returns "" when not attached.
func (a *App) attachBanner() string {
	if a.sseState == nil {
		return ""
	}
	label := " ATTACHED → " + a.sseState.addr + " "
	return "  " + lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#1a1a1a")).
		Background(lipgloss.Color(a.theme.Primary)).
		Render(label)
}

// handleDaemonSSEEvent parses one SSE event from the attached daemon and
// renders it into the chat transcript: assistant content streams into the
// trailing assistant message; tool calls/results append their own lines.  This
// replaces the previous behaviour of dumping each raw event as a toast.
func (a *App) handleDaemonSSEEvent(msg sseEventMsg) {
	// Surface synthetic _error events (connection failures from startSSELoop).
	if msg.EventType == "_error" {
		a.addNotification("error", msg.Data)
		return
	}
	// Surface an explicit replay gap (attachclient's reconnect resumed past
	// retained history) as an informational notice rather than silently
	// continuing as if nothing were missed.
	if msg.EventType == "_gap" {
		a.addNotification("info", msg.Data)
		return
	}
	if !a.applyDaemonEvent(msg.Data) {
		return
	}
	a.invalidateViewportCache()
	a.updateViewportContent()
	a.msgViewport.GotoBottom()
}

// applyDaemonEvent parses one daemon SSE payload and mutates the transcript
// (a.messages) accordingly.  It returns true when the transcript changed (so the
// caller should refresh the viewport), false otherwise.  Kept free of any
// viewport/UI calls so it is unit-testable in isolation.
func (a *App) applyDaemonEvent(data string) bool {
	// Decode via attachclient's single canonical envelope decoder instead
	// of a second hand-copied struct (formerly daemonSSEEnvelope here,
	// mirroring cmd/swarmos/attach_cli.go's now-removed sseEnvelope).
	ev := attachclient.DecodeEnvelope([]byte(data))
	if ev.Kind == "" {
		return false // malformed or kind-less payload: ignore, don't panic
	}

	switch ev.Kind {
	case "stream_start":
		// Begin a fresh assistant turn.
		a.messages = append(a.messages, Message{Role: "assistant", Content: "", Timestamp: time.Now()})
		return true
	case "stream_end":
		return true
	case "agent":
		switch ev.Agent.Type {
		case "content":
			var u struct {
				Content string `json:"Content"`
				Append  bool   `json:"Append"`
			}
			_ = json.Unmarshal(ev.Agent.Update, &u)
			a.appendDaemonAssistantText(u.Content, u.Append)
			return true
		case "tool_call":
			var u struct {
				Name string `json:"Name"`
			}
			_ = json.Unmarshal(ev.Agent.Update, &u)
			if u.Name != "" {
				a.messages = append(a.messages, Message{Role: "tool", Content: "→ " + u.Name, Timestamp: time.Now()})
				return true
			}
			return false
		case "tool_result":
			// Keep the trailing assistant message ready for continued output.
			return false
		default:
			return false
		}
	default:
		return false
	}
}

// appendDaemonAssistantText writes streamed assistant text into the trailing
// assistant message (creating one if needed).  Append=false replaces, true adds.
func (a *App) appendDaemonAssistantText(content string, appendMode bool) {
	n := len(a.messages)
	if n == 0 || a.messages[n-1].Role != "assistant" {
		a.messages = append(a.messages, Message{Role: "assistant", Content: "", Timestamp: time.Now()})
		n = len(a.messages)
	}
	if appendMode {
		a.messages[n-1].Content += content
	} else {
		a.messages[n-1].Content = content
	}
}

// ── App state for SSE attachment ────────────────────────────────────

type sseAttachmentState struct {
	addr       string
	sseURL     string
	serveBase  string             // e.g. "http://127.0.0.1:8090" — base for /rpc calls
	client     *attachclient.Impl // owns negotiation, reconnect/backoff, cursor, and per-RPC deadlines
	eventCh    chan sseEventMsg   // persistent event channel; owned by startSSELoop goroutine
	ctx        context.Context
	cancel     context.CancelFunc
	connected  bool
	lastEvent  time.Time
	eventCount int
}

// startAttach initiates a persistent SSE connection to a daemon.
func (a *App) startAttach(msg commands.AttachRequestMsg) tea.Cmd {
	if a.sseState != nil {
		// Already attached — cancel the old context which stops the old goroutine.
		a.sseState.cancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Buffer the channel so the producer (startSSELoop) is never blocked by a
	// single slow Update cycle.  64 events matches the daemon's own SSE buffer.
	eventCh := make(chan sseEventMsg, 64)
	serveBase := strings.TrimSuffix(msg.SSEURL, "/sse")
	client := attachclient.New(attachclient.Deps{})
	a.sseState = &sseAttachmentState{
		addr:      msg.Addr,
		sseURL:    msg.SSEURL,
		serveBase: serveBase,
		client:    client,
		eventCh:   eventCh,
		ctx:       ctx,
		cancel:    cancel,
	}

	// Start the persistent SSE reader goroutine.
	go startSSELoop(ctx, client, serveBase, eventCh)

	// Return the first listen cmd to kick off the BubbleTea chain.
	return a.listenSSECmd()
}

// sendToDaemonCmd forwards a user message to the attached daemon via its
// /rpc client.sendMessage method (via attachclient.Impl.Submit, which
// carries an explicit request deadline instead of the previous
// http.Post-with-no-deadline call). Returns a tea.Cmd that performs the
// call off the UI goroutine and surfaces an AttachErrorMsg on failure.
func (a *App) sendToDaemonCmd(text string) tea.Cmd {
	if a.sseState == nil || a.sseState.client == nil {
		return nil
	}
	client := a.sseState.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), attachclient.DefaultRPCTimeout)
		defer cancel()
		if _, err := client.Submit(ctx, attachclient.Work{
			Method: "client.sendMessage",
			Params: map[string]any{"message": text},
		}); err != nil {
			return commands.AttachErrorMsg{Error: fmt.Sprintf("send to daemon: %v", err)}
		}
		return nil
	}
}
