package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conductor"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lifecycle"
	attclient "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/client"
)

// peerStatusText renders a presence peer's raw, free-form Status string
// through internal/lifecycle's canonical DTO instead of printing it
// verbatim, so `swarm list`/`swarm status` output is internally consistent
// with the vocabulary the rest of ADR-005's adapters (healthz/readyz,
// client.snapshot, A2ADebugResponse) use.
//
// This deliberately reuses lifecycle.NewStatusViewFromLegacy — the SAME
// bounded FromLegacyStatus mapping (legacy "idle"/"working"/"stopping")
// internal/lifecycle already exposes from Phase 02 — rather than writing a
// second, independently maintained legacy-status mapping here. Per the
// Phase 03 readiness-status-registry contract, this phase does NOT change
// internal/a2a presence's Status field Go type (it stays a free-form
// string here); a peer whose Status falls outside that bounded legacy set
// (for example the "active" value cmd/swarmos/main.go still writes for a
// local TUI peer, which is owned by files this change does not touch)
// falls back to the raw status text unchanged, preserving today's CLI
// behavior for values this phase's bounded mapping does not (yet) cover
// rather than fabricating a lifecycle state for them.
//
// hasDiscoveryEvidence is true here: every peer this function is called
// for came back from a.ListPeers/a2a.GetPeer, i.e. was actually found by
// discovery, so an absent/stopped legacy status (were the bounded mapping
// ever extended to cover one) would legitimately mean "offline" rather
// than "unknown". pendingUpgrade is always false: the CLI has no upgrade-
// intent evidence for a remote peer at this layer.
func peerStatusText(rawStatus string) string {
	view, err := lifecycle.NewStatusViewFromLegacy(rawStatus, true, false)
	if err != nil {
		return rawStatus
	}
	return view.Summary()
}

// runSwarmCLI implements the `swarmos swarm <subcommand>` family:
//
//	swarmos swarm list                     — list live peers
//	swarmos swarm task <handle> <prompt>   — send a task to a peer
//	swarmos swarm status                   — compact one-line status per peer
//	swarmos swarm attach <handle> [cmd]    — control a live TUI's screen
//
// Every subcommand here is a bounded ADR-002 Compatibility-matrix legacy
// spelling (docs/architecture/swarm-attach/adr-002-cli-taxonomy.md,
// "## Compatibility"): `list`/`status` -> canonical `swarm peer list`,
// `task` -> canonical `swarm peer task`, `attach` -> canonical
// `swarm peer control`, `stop` -> canonical `swarm daemon stop`, `up` ->
// canonical `swarm daemon start`. Each case below emits the matrix's exact
// stderr warning text via warnLegacyAlias (main.go) before delegating to
// the SAME implementation the canonical `swarm peer ...` dispatch
// (peer_cli.go) also calls — no behavior changes, alias warning only.
func runSwarmCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarmos swarm <list|task|status|attach>")
	}
	// None of these legacy subcommands currently accept a machine-output
	// flag of their own; isMachineOutputArgs is still consulted (rather
	// than hardcoding false) so a future --json/--output-format addition to
	// this command family automatically suppresses the warning per
	// ADR-002's machine-output carve-out without another call-site change.
	machineMode := isMachineOutputArgs(args[1:])
	switch args[0] {
	case "list":
		warnLegacyAlias("swarm swarm list", machineMode)
		return swarmList()
	case "status":
		warnLegacyAlias("swarm swarm status", machineMode)
		return swarmStatus()
	case "task":
		warnLegacyAlias("swarm swarm task", machineMode)
		return swarmTask(args[1:])
	case "attach":
		warnLegacyAlias("swarm swarm attach", machineMode)
		return swarmAttach(args[1:])
	case "stop":
		warnLegacyAlias("swarm swarm stop", machineMode)
		return swarmStop(args[1:])
	case "up":
		warnLegacyAlias("swarm swarm up", machineMode)
		return swarmUp(args[1:])
	default:
		return fmt.Errorf("unknown swarm subcommand %q — valid: list, task, status, attach, stop, up", args[0])
	}
}

// swarmUp ensures THE global background daemon is running (spawning it detached
// if absent) and prints its serve endpoint. Idempotent — safe to run from cron
// or a shell profile to guarantee the daemon is available.
//
//	swarmos swarm up   — ensure the global daemon, print its endpoint
func swarmUp(args []string) error {
	_ = args
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	peer, err := ensureGlobalDaemon(ctx, "")
	if err != nil {
		return err
	}
	fmt.Printf("global daemon %q is up\n", peer.Handle)
	fmt.Printf("  serve:  %s/rpc  (events: %s/sse)\n", peer.ServeURL, peer.ServeURL)
	fmt.Printf("  task:   swarmos swarm task %s \"your prompt\"\n", peer.Handle)
	fmt.Printf("  stop:   swarmos swarm stop\n")
	return nil
}

// swarmStop stops a background daemon by handle (defaults to the global daemon
// when no handle is given). This is the explicit "kill" for the keep-running-
// silently model: closing the UI never stops the daemon — this does.
//
//	swarmos swarm stop            — stop THE global daemon
//	swarmos swarm stop <handle>   — stop a specific daemon
func swarmStop(args []string) error {
	handle := globalDaemonHandle
	if len(args) >= 1 && strings.TrimSpace(args[0]) != "" {
		handle = strings.TrimSpace(args[0])
	}
	if err := stopDaemon(handle); err != nil {
		return err
	}
	fmt.Printf("stopped daemon %q\n", handle)
	return nil
}

// swarmList prints every live peer in the default swarm.
func swarmList() error {
	peers, err := a2a.ListPeers(a2a.DefaultSwarmName)
	if err != nil {
		return fmt.Errorf("list peers: %w", err)
	}
	if len(peers) == 0 {
		fmt.Println("no peers in swarm (start swarmos to register one)")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "HANDLE\tTYPE\tSTATUS\tMODEL\tWORKSPACE\tENDPOINT")
	for _, p := range peers {
		ws := p.Workspace
		if ws == "" {
			ws = "-"
		}
		ep := p.EndpointURL
		if ep == "" {
			ep = "-"
		}
		peerType := string(p.Type)
		if peerType == "" {
			peerType = "tui"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			p.Handle, peerType, peerStatusText(p.Status), p.Model, ws, ep)
	}
	return w.Flush()
}

// swarmStatus prints a compact current-task line per peer.
func swarmStatus() error {
	peers, err := a2a.ListPeers(a2a.DefaultSwarmName)
	if err != nil {
		return fmt.Errorf("list peers: %w", err)
	}
	if len(peers) == 0 {
		fmt.Println("swarm is empty")
		return nil
	}
	for _, p := range peers {
		task := p.CurrentTask
		if task == "" {
			task = "(idle)"
		}
		ctrl := ""
		if p.ControlSocket != "" {
			ctrl = " [ctrl]"
		}
		fmt.Printf("%-30s  %-18s  %s%s\n", p.Handle, peerStatusText(p.Status), task, ctrl)
	}
	return nil
}

// swarmTask sends a one-shot prompt to a named peer via the A2A HTTP protocol.
func swarmTask(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: swarmos swarm task <handle> <prompt>")
	}
	handle := args[0]
	prompt := strings.Join(args[1:], " ")

	pool := conductor.NewLivePeerPool("", "")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(15*time.Second))
	defer cancel()

	peers, err := pool.List()
	if err != nil {
		return fmt.Errorf("list peers: %w", err)
	}
	found := false
	for _, p := range peers {
		if p.Handle == handle {
			found = true
			fmt.Printf("sending task to peer %q (%s)…\n", p.Handle, p.EndpointURL)
			break
		}
	}
	if !found {
		fmt.Printf("available peers:\n")
		for _, p := range peers {
			fmt.Printf("  %s  (%s)\n", p.Handle, p.Status)
		}
		return fmt.Errorf("peer %q not found in swarm", handle)
	}
	if err := pool.SendTask(ctx, handle, prompt); err != nil {
		return fmt.Errorf("send task: %w", err)
	}
	fmt.Printf("task delivered to %q — watch its TUI to see execution\n", handle)
	return nil
}

// swarmAttach connects to a running TUI's control socket and runs a command.
//
// Usage:
//
//	swarmos swarm attach <handle>                  — print socket info
//	swarmos swarm attach <handle> frame            — print current screen
//	swarmos swarm attach <handle> key <key>        — send a key (e.g. enter)
//	swarmos swarm attach <handle> text <text>      — type text into the TUI
//	swarmos swarm attach <handle> wait <text> [ms] — wait for text to appear
//	swarmos swarm attach <handle> state            — dump full model state as JSON
func swarmAttach(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarmos swarm attach <handle> [frame|key <k>|text <t>|wait <t> [ms]|state]")
	}
	handle := args[0]
	subArgs := args[1:]

	peer, err := a2a.GetPeer(a2a.DefaultSwarmName, handle)
	if err != nil {
		return fmt.Errorf("get peer %q: %w", handle, err)
	}
	if peer == nil {
		return fmt.Errorf("peer %q not found — is a TUI running?", handle)
	}
	if peer.ControlSocket == "" {
		return fmt.Errorf("peer %q has no control socket (needs newer swarmos)", handle)
	}

	// No sub-command: print info.
	if len(subArgs) == 0 {
		fmt.Printf("peer:    %s\n", peer.Handle)
		fmt.Printf("status:  %s\n", peer.Status)
		fmt.Printf("socket:  %s\n", peer.ControlSocket)
		if peer.EndpointURL != "" {
			fmt.Printf("a2a:     %s\n", peer.EndpointURL)
		}
		fmt.Printf("\nCommands:\n")
		fmt.Printf("  swarmos swarm attach %s frame\n", handle)
		fmt.Printf("  swarmos swarm attach %s key enter\n", handle)
		fmt.Printf("  swarmos swarm attach %s text \"hello\"\n", handle)
		fmt.Printf("  swarmos swarm attach %s wait \"some text\"\n", handle)
		fmt.Printf("  swarmos swarm attach %s state\n", handle)
		fmt.Printf("  swarmos swarm attach %s debug\n", handle)
		return nil
	}

	isDaemon := peer.Type == a2a.PeerTypeDaemon

	// Connect using the embedded automation client — no external binary needed.
	c := attclient.New("unix://" + peer.ControlSocket)
	if err := c.Connect(); err != nil {
		errMsg := "connect to control socket: %w\n(is the TUI still running?)"
		if isDaemon {
			errMsg = "connect to control socket: %w\n(is the daemon still running? check: swarmos swarm list)"
		}
		return fmt.Errorf(errMsg, err)
	}
	defer c.Close()

	switch subArgs[0] {
	case "frame":
		frame, err := c.GetFrame()
		if err != nil {
			return fmt.Errorf("get frame: %w", err)
		}
		fmt.Print(frame.Content)

	case "key":
		if len(subArgs) < 2 {
			return fmt.Errorf("usage: attach <handle> key <key>  (e.g. enter, ctrl+c, tab)")
		}
		if err := c.SendKey(subArgs[1]); err != nil {
			return fmt.Errorf("send key: %w", err)
		}

	case "text":
		if len(subArgs) < 2 {
			return fmt.Errorf("usage: attach <handle> text <text>")
		}
		if err := c.SendText(strings.Join(subArgs[1:], " ")); err != nil {
			return fmt.Errorf("send text: %w", err)
		}

	case "wait":
		if len(subArgs) < 2 {
			return fmt.Errorf("usage: attach <handle> wait <text> [timeout_ms]")
		}
		timeout := 30 * time.Second
		if len(subArgs) >= 3 {
			if ms, err := strconv.Atoi(subArgs[2]); err == nil && ms > 0 {
				timeout = time.Duration(ms) * time.Millisecond
			}
		}
		if err := c.WaitForContent(subArgs[1], timeout); err != nil {
			return fmt.Errorf("wait: %w", err)
		}

	case "state":
		st, err := c.GetState()
		if err != nil {
			return fmt.Errorf("get state: %w", err)
		}
		data, _ := json.MarshalIndent(st, "", "  ")
		fmt.Println(string(data))

	case "debug":
		dbg, err := c.GetA2ADebug()
		if err != nil {
			return fmt.Errorf("get a2a debug: %w", err)
		}
		data, _ := json.MarshalIndent(dbg, "", "  ")
		fmt.Println(string(data))

	default:
		return fmt.Errorf("unknown attach command %q — valid: frame, key, text, wait, state, debug", subArgs[0])
	}
	return nil
}
