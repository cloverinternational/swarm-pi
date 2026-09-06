package commands

import (
	"fmt"
	"net/url"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// AttachCommand connects the TUI to a running agent daemon via SSE.
//
// Usage:
//
//	/attach swarmos-daemon        (resolve a swarm handle via discovery)
//	/attach localhost:8090
//	/attach 192.168.1.50:9090
//	/attach http://localhost:8090
//
// The command opens an SSE connection to the daemon's /sse endpoint
// and streams agent events into the current conversation view. A bare
// token with no ':' is treated as a swarm handle and resolved to the
// peer's serve_url via discovery (so `swarm list` output works directly).
type AttachCommand struct{}

func NewAttachCommand() *AttachCommand {
	return &AttachCommand{}
}

func (c *AttachCommand) Name() string { return "attach" }
func (c *AttachCommand) Description() string {
	return i18n.T("commands.attach.description")
}
func (c *AttachCommand) Aliases() []string   { return []string{"at"} }
func (c *AttachCommand) IsInteractive() bool { return false }

func (c *AttachCommand) Execute(args []string) tea.Cmd {
	if len(args) == 0 {
		// No argument: be helpful instead of erroring. Resolve attachable
		// daemons from discovery. If exactly one exists, attach to it directly
		// (the common single-daemon case). Otherwise guide the user.
		daemons := attachableDaemons()
		switch len(daemons) {
		case 0:
			return func() tea.Msg {
				return AttachErrorMsg{Error: i18n.T("commands.attach.error.none")}
			}
		case 1:
			sseURL := strings.TrimRight(daemons[0].ServeURL, "/") + "/sse"
			return func() tea.Msg {
				return AttachRequestMsg{Addr: daemons[0].Handle, SSEURL: sseURL}
			}
		default:
			handles := make([]string, 0, len(daemons))
			for _, d := range daemons {
				handles = append(handles, d.Handle)
			}
			return func() tea.Msg {
				return AttachErrorMsg{Error: i18n.T("commands.attach.error.multiple", strings.Join(handles, "|"))}
			}
		}
	}

	addr := args[0]
	// Strip scheme if provided.
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")

	// If the token looks like a swarm handle (no host:port colon), resolve it
	// via discovery to the peer's serve_url. This makes `swarm list` output
	// usable directly: /attach <handle> instead of hand-translating to host:port.
	if !strings.Contains(addr, ":") {
		peer, err := a2a.GetPeer(a2a.DefaultSwarmName, addr)
		if err != nil || peer == nil {
			return func() tea.Msg {
				return AttachErrorMsg{Error: i18n.T("commands.attach.error.handle_not_found", addr)}
			}
		}
		if peer.ServeURL == "" {
			return func() tea.Msg {
				return AttachErrorMsg{Error: i18n.T("commands.attach.error.no_endpoint", addr)}
			}
		}
		sseURL := strings.TrimRight(peer.ServeURL, "/") + "/sse"
		return func() tea.Msg {
			return AttachRequestMsg{Addr: addr, SSEURL: sseURL}
		}
	}

	// Build the SSE URL from a host:port address.
	sseURL := fmt.Sprintf("http://%s/sse", addr)
	if _, err := url.Parse(sseURL); err != nil {
		return func() tea.Msg {
			return AttachErrorMsg{Error: i18n.T("commands.attach.error.invalid_address", addr, err)}
		}
	}

	return func() tea.Msg {
		return AttachRequestMsg{Addr: addr, SSEURL: sseURL}
	}
}

// attachableDaemons returns the live swarm peers that expose a serve endpoint
// (i.e. daemons a thin client can render/attach to). Plain TUIs register no
// serve_url and are intentionally excluded.
func attachableDaemons() []a2a.PeerPresence {
	peers, err := a2a.ListPeers(a2a.DefaultSwarmName)
	if err != nil {
		return nil
	}
	out := make([]a2a.PeerPresence, 0, len(peers))
	for _, p := range peers {
		if strings.TrimSpace(p.ServeURL) != "" {
			out = append(out, p)
		}
	}
	return out
}

// Subcommands makes `/attach ` present an instant picker of live attachable
// daemons — no need to run `swarm list` or know a handle. Each item is a
// daemon handle with a status/model/workspace hint as its description.
func (c *AttachCommand) Subcommands() []Subcommand {
	daemons := attachableDaemons()
	if len(daemons) == 0 {
		return []Subcommand{{
			Name:        "",
			Description: i18n.T("commands.attach.picker.none"),
		}}
	}
	subs := make([]Subcommand, 0, len(daemons))
	for _, d := range daemons {
		desc := d.Status
		if desc == "" {
			desc = i18n.T("commands.attach.status.idle")
		}
		if d.Model != "" {
			desc += "   " + d.Model
		}
		if d.CurrentTask != "" {
			desc += "   " + d.CurrentTask
		} else if d.Workspace != "" {
			desc += "   " + d.Workspace
		}
		subs = append(subs, Subcommand{Name: d.Handle, Description: desc})
	}
	return subs
}

// ArgumentCompletions is required by SubcommandProvider. Attach handles have no
// further arguments, so this returns nil.
func (c *AttachCommand) ArgumentCompletions(subcommand string) []string {
	return nil
}

func (c *AttachCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	return c, nil
}

func (c *AttachCommand) View() string {
	return ""
}

// ── Message types ──────────────────────────────────────────────────

// AttachRequestMsg is emitted when the user runs /attach <addr>.
type AttachRequestMsg struct {
	Addr   string // e.g. "localhost:8090"
	SSEURL string // e.g. "http://localhost:8090/sse"
}

// AttachConnectedMsg is emitted when the SSE connection is established.
type AttachConnectedMsg struct {
	Addr string
}

// AttachErrorMsg is emitted when the attach fails.
type AttachErrorMsg struct {
	Error string
}

// AttachDetachedMsg is emitted when the SSE connection is closed.
type AttachDetachedMsg struct {
	Addr string
}

// DetachRequestMsg is emitted when the user runs /detach. The app cancels the
// active SSE attachment (if any) and clears its state.
type DetachRequestMsg struct{}

// ── DetachCommand ───────────────────────────────────────────────────

// DetachCommand cleanly disconnects the current daemon attachment, leaving the
// daemon (and its agents) running — the tmux "detach" half of attach/detach.
type DetachCommand struct{}

func NewDetachCommand() *DetachCommand { return &DetachCommand{} }

func (c *DetachCommand) Name() string { return "detach" }
func (c *DetachCommand) Description() string {
	return i18n.T("commands.detach.description")
}
func (c *DetachCommand) Aliases() []string   { return nil }
func (c *DetachCommand) IsInteractive() bool { return false }

func (c *DetachCommand) Execute(args []string) tea.Cmd {
	return func() tea.Msg { return DetachRequestMsg{} }
}

func (c *DetachCommand) Update(msg tea.Msg) (Command, tea.Cmd) { return c, nil }
func (c *DetachCommand) View() string                          { return "" }
