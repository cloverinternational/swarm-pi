// cmd/swarmos/attach_cli.go
//
// `swarmos attach <handle>` — a THIN CLIENT that renders a persistent background
// daemon's state over its serve interface (/rpc JSON-RPC + /sse events). ZERO
// engine logic runs in this process: the header comes from the daemon's
// Snapshot, and the live transcript is accumulated from the daemon's /sse event
// stream (the same agent.IntermediateUpdate stream the TUI renders from). Input
// is forwarded as /rpc commands. Detach (Ctrl-C) leaves the daemon + its agents
// running; re-running `attach` restores the view.
//
// Transport/reconnect/negotiation plumbing lives in
// swarm-sdk/attachclient (package attachclient): this file no longer keeps
// its own hand-copied SSE envelope struct or ad hoc, reconnect-less
// http.Client{Timeout: 0} loop. Every RPC now carries an explicit deadline
// (attachclient.DefaultRPCTimeout) and the event stream automatically
// reconnects with bounded backoff, resuming from the last acknowledged
// cursor or surfacing an explicit gap instead of silently dropping events.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/attachclient"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
)

// waitForAttachablePeer polls the peer registry until handle is attachable
// (present with a reachable serve endpoint) or timeout elapses.
//
// This closes the attach race: right after `swarm up` (or daemon restart) the
// presence file exists BEFORE the serve listener is bound and advertised, so
// a single GetPeer would fail with "exposes no serve endpoint" even though
// the daemon is milliseconds from ready. The old behavior was a hard error;
// waiting briefly makes `swarm up && swarm attach` reliable.
func waitForAttachablePeer(handle string, timeout time.Duration) (*a2a.PeerPresence, error) {
	deadline := time.Now().Add(timeout)
	var lastState string
	notified := false
	for {
		peer, err := a2a.GetPeer(a2a.DefaultSwarmName, handle)
		switch {
		case err != nil || peer == nil:
			lastState = "not registered"
		case peer.ServeURL == "":
			lastState = "registered but its serve endpoint is not published yet"
		case !daemonServeReachable(peer.ServeURL):
			lastState = fmt.Sprintf("registered at %s but not accepting connections yet", peer.ServeURL)
		default:
			return peer, nil
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf(
				"attach: peer %q is %s after waiting %s\n  check:  swarmos swarm status\n  start:  swarmos swarm up",
				handle, lastState, timeout)
		}
		if !notified {
			fmt.Printf("waiting for %q (%s)...\n", handle, lastState)
			notified = true
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func runAttachCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarmos attach <handle>   (run `swarmos swarm list` to see daemons)")
	}
	handle := args[0]

	peer, err := waitForAttachablePeer(handle, 10*time.Second)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := attachclient.New(attachclient.Deps{})
	if _, err := client.Connect(ctx, attachclient.Target{BaseURL: peer.ServeURL}); err != nil {
		return fmt.Errorf("attach: connect: %w", err)
	}
	st := &attachState{client: client, handle: handle, ctx: ctx}
	fmt.Printf("attached to daemon %q at %s — Ctrl-C to detach (daemon keeps running)\n", handle, strings.TrimRight(peer.ServeURL, "/"))

	st.loadHistory() // seed transcript from the daemon so reattach restores scrollback
	st.render()      // initial header + restored transcript

	// Accumulate the daemon's /sse event stream into the transcript + re-render.
	go st.streamEvents(ctx)

	// Input loop: forward user input as daemon COMMANDS over /rpc. ZERO logic
	// here — the daemon executes and streams back over /sse.
	fmt.Println("commands: :mode <m>  :model <name>  :profile <id>  :new  :sessions  :switch <id>  :msg <text>  :compact  :cancel  :q")
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		if ctx.Err() != nil {
			break
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		if line == ":q" || line == ":quit" {
			break
		}
		st.dispatch(line)
	}
	// Detach releases ONLY this client's resources (cancels the event
	// stream reconnect loop, marks the client detached) — it never issues a
	// daemon RPC, so the daemon and its agents keep running.
	_ = client.Detach(context.Background())
	fmt.Println("\ndetached. daemon still running.")
	return nil
}

// ─── thin-client state ────────────────────────────────────────────────────────

type convLine struct{ role, content string }

// attachState holds ONLY presentation state accumulated from the daemon. No
// engine logic; every value originates from a daemon snapshot or event.
type attachState struct {
	client *attachclient.Impl
	handle string
	ctx    context.Context

	mu        sync.Mutex
	lines     []convLine // finalized transcript turns
	assistant string     // in-progress assistant text (current turn)
	streaming bool
	tokIn     int
	tokOut    int
}

func (s *attachState) dispatch(line string) {
	fields := strings.Fields(line)
	cmd := fields[0]
	arg := strings.TrimSpace(strings.TrimPrefix(line, cmd))
	var err error
	switch cmd {
	case ":mode":
		_, err = s.client.Call(s.ctx, "client.setMode", map[string]any{"mode": arg})
	case ":new":
		s.mu.Lock()
		s.lines = nil
		s.assistant = ""
		s.mu.Unlock()
		err = s.client.Select(s.ctx, attachclient.Selection{New: true})
	case ":sessions":
		var res json.RawMessage
		if res, err = s.client.Call(s.ctx, "client.listConversations", map[string]any{}); err == nil {
			fmt.Printf("  sessions: %s\n", string(res))
		}
	case ":switch":
		if err = s.client.Select(s.ctx, attachclient.Selection{ConversationID: arg}); err == nil {
			s.loadHistory() // re-render the switched-to session's transcript (tmux window switch)
		}
	case ":msg":
		s.mu.Lock()
		s.lines = append(s.lines, convLine{role: "user", content: arg})
		s.mu.Unlock()
		s.render()
		_, err = s.client.Submit(s.ctx, attachclient.Work{Method: "client.sendMessage", Params: map[string]any{"message": arg}})
	case ":cancel":
		// Interrupt the running turn on the daemon (tmux-like Ctrl-C for the
		// agent, distinct from detaching). The daemon keeps running. This is
		// an unconditional interrupt request (not correlated to a specific
		// Submit-ted execution), so it goes through Call rather than Cancel.
		_, err = s.client.Call(s.ctx, "client.cancel", map[string]any{})
	case ":model":
		_, err = s.client.Call(s.ctx, "client.setModel", map[string]any{"model": arg})
	case ":profile":
		_, err = s.client.Call(s.ctx, "client.switchProfile", map[string]any{"profileId": arg})
	case ":compact":
		_, err = s.client.Call(s.ctx, "client.compact", map[string]any{})
	case ":approve":
		err = s.client.Approve(s.ctx, attachclient.ApprovalDecision{CallID: arg, Allow: true})
	case ":deny":
		err = s.client.Approve(s.ctx, attachclient.ApprovalDecision{CallID: arg, Allow: false})
	default:
		fmt.Printf("  unknown command %q (try :mode :new :sessions :switch :msg :q)\n", cmd)
		return
	}
	if err != nil {
		fmt.Printf("  command error: %v\n", err)
	}
	s.render()
}

// ─── event stream → transcript ────────────────────────────────────────────────

// streamEvents consumes attachclient's decoded, auto-reconnecting event
// stream. Reconnect/backoff/cursor-resume/gap-detection all live in
// attachclient (reconnect.go); this loop only renders what it receives.
func (s *attachState) streamEvents(ctx context.Context) {
	stream, err := s.client.Events(ctx, attachclient.Cursor{})
	if err != nil {
		if ctx.Err() == nil {
			fmt.Printf("  (event stream unavailable: %v)\n", err)
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
			s.applyEvent(ev)
		case err, ok := <-stream.Errs:
			if ok && err != nil && ctx.Err() == nil {
				fmt.Printf("  (event stream error: %v)\n", err)
			}
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *attachState) applyEvent(ev attachclient.Event) {
	switch ev.Kind {
	case "gap":
		// Explicit gap signal (never a silent skip): the resume cursor was
		// older than the daemon's retained history.
		s.mu.Lock()
		msg := "history gap: some earlier events could not be replayed"
		if ev.Gap != nil {
			msg = fmt.Sprintf("history gap on stream %q: requested after %d, oldest retained %d",
				ev.Gap.StreamID, ev.Gap.RequestedAfter, ev.Gap.OldestRetained)
		}
		s.lines = append(s.lines, convLine{role: "!gap", content: msg})
		s.mu.Unlock()
	case "stream_start":
		s.mu.Lock()
		s.streaming = true
		s.assistant = ""
		s.mu.Unlock()
	case "stream_end":
		s.mu.Lock()
		s.flushAssistantLocked()
		s.streaming = false
		s.mu.Unlock()
	case "approval_requested":
		var p struct {
			CallID string `json:"callID"`
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		s.mu.Lock()
		s.lines = append(s.lines, convLine{role: "APPROVE?", content: fmt.Sprintf("%s   →  :approve %s   |   :deny %s", p.Reason, p.CallID, p.CallID)})
		s.mu.Unlock()
	case "agent":
		switch ev.Agent.Type {
		case "content":
			var u struct {
				Content string `json:"Content"`
				Append  bool   `json:"Append"`
			}
			_ = json.Unmarshal(ev.Agent.Update, &u)
			s.mu.Lock()
			if u.Append {
				s.assistant += u.Content
			} else {
				s.assistant = u.Content
			}
			s.mu.Unlock()
		case "token_count":
			var u struct{ InputTokens, OutputTokens int }
			_ = json.Unmarshal(ev.Agent.Update, &u)
			s.mu.Lock()
			s.tokIn, s.tokOut = u.InputTokens, u.OutputTokens
			s.mu.Unlock()
		case "tool_call":
			var u struct {
				Name       string          `json:"Name"`
				Parameters json.RawMessage `json:"Parameters"`
			}
			_ = json.Unmarshal(ev.Agent.Update, &u)
			s.mu.Lock()
			s.flushAssistantLocked() // preamble before the tool, in order
			s.lines = append(s.lines, convLine{role: "tool→", content: u.Name + " " + compactJSON(u.Parameters)})
			s.mu.Unlock()
		case "tool_result":
			var u struct {
				Output string `json:"Output"`
			}
			_ = json.Unmarshal(ev.Agent.Update, &u)
			s.mu.Lock()
			s.lines = append(s.lines, convLine{role: "tool✓", content: summarizeOutput(u.Output)})
			s.mu.Unlock()
		default:
			return // thinking/hook/fallback — not rendered in this minimal client yet
		}
	default:
		return
	}
	s.render()
}

// ─── render ───────────────────────────────────────────────────────────────────

func (s *attachState) render() {
	// Header from the daemon snapshot (provider/model/mode live).
	var prov, model, mode string
	if snap, err := s.client.Status(s.ctx); err == nil {
		prov, model, mode = snap.Provider, snap.Model, snap.OperatingMode
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Print("\033[2J\033[H")
	fmt.Printf("┌─ daemon %s ─ %s/%s ─ mode %s ─ streaming=%v ─ tok in=%d out=%d\n",
		s.handle, prov, model, mode, s.streaming, s.tokIn, s.tokOut)
	fmt.Printf("└──────────────────────────────────────────────────────────────\n")
	for _, ln := range s.lines {
		fmt.Printf("  [%s] %s\n", ln.role, ln.content)
	}
	if s.assistant != "" {
		fmt.Printf("  [assistant] %s▌\n", s.assistant)
	}
}

// loadHistory seeds the transcript from the daemon's persisted conversation so a
// (re)attaching client restores the full scrollback — the tmux "reattach shows
// the prior view" property. Pulls the active conversation's messages via
// client.getMessages; ZERO state is computed locally.
func (s *attachState) loadHistory() {
	snap, err := s.client.Status(s.ctx)
	if err != nil {
		return
	}
	if snap.ActiveConversationID == "" {
		return
	}
	mres, err := s.client.Call(s.ctx, "client.getMessages", map[string]any{"convID": snap.ActiveConversationID})
	if err != nil {
		return
	}
	var mw struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(mres, &mw) != nil {
		return
	}
	s.mu.Lock()
	s.lines = s.lines[:0]
	for _, m := range mw.Messages {
		s.lines = append(s.lines, convLine{role: m.Role, content: m.Content})
	}
	s.mu.Unlock()
}

// flushAssistantLocked moves any in-progress assistant text into a finalized
// transcript line. Caller must hold s.mu.
func (s *attachState) flushAssistantLocked() {
	if s.assistant != "" {
		s.lines = append(s.lines, convLine{role: "assistant", content: s.assistant})
		s.assistant = ""
	}
}

// compactJSON collapses a raw JSON value to a single truncated line for display.
func compactJSON(raw []byte) string {
	s := strings.Join(strings.Fields(string(raw)), " ")
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

// summarizeOutput extracts the stdout payload from a tool result (or truncates
// the raw output) for a compact transcript line.
func summarizeOutput(out string) string {
	if i := strings.Index(out, "<![CDATA["); i >= 0 {
		if j := strings.Index(out[i:], "]]>"); j > 0 {
			out = out[i+len("<![CDATA[") : i+j]
		}
	}
	out = strings.TrimSpace(strings.Join(strings.Fields(out), " "))
	if len(out) > 100 {
		out = out[:100] + "…"
	}
	return out
}

// rpcCall issues one JSON-RPC 2.0 request to base's /rpc endpoint with an
// explicit deadline (attachclient.DefaultRPCTimeout) via attachclient's
// default Transport. It exists for call sites elsewhere in this package
// (e.g. global_daemon.go's runDaemonBackedTUI, which is outside this
// phase's attach-rewire scope) that issue a one-off RPC without holding a
// full attachclient.Client/Session — attach_cli.go's own thin-client state
// (attachState) now goes through attachclient.Impl directly instead.
func rpcCall(base, method string, params any) (json.RawMessage, error) {
	return attachclient.NewHTTPTransport(0).RPC(context.Background(), strings.TrimRight(base, "/"), method, params)
}
