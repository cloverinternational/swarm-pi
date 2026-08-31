package commands

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// SwarmCommand implements the /swarm command for A2A management
type SwarmCommand struct {
	interactive   bool
	width         int
	height        int
	swarmProvider SwarmProvider // Interface to get A2A/swarm info

	// UI state
	focusIndex    int                // Which element is focused
	inputMode     swarmInputMode     // Current input mode
	inputBuffer   string             // Text input buffer
	statusMessage string             // Status message to display
	confirmAction swarmConfirmAction // Pending confirmation action
}

type swarmInputMode int

const (
	swarmModeNormal swarmInputMode = iota
	swarmModeInputHandle
	swarmModeInputPeer
	swarmModeConfirm
)

type swarmConfirmAction int

const (
	swarmConfirmNone swarmConfirmAction = iota
	swarmConfirmEnable
	swarmConfirmDisable
)

// SwarmProvider provides access to swarm/A2A functionality
type SwarmProvider interface {
	IsA2AEnabled() bool
	GetA2AHandle() string
	GetA2AAddress() string
	ListPeers() []a2a.SwarmPeerInfo
	GetSwarmStatus() a2a.AgentSwarmStatus

	// Actions
	EnableA2A(ctx context.Context, handle string) error
	DisableA2A(ctx context.Context) error
	AddPeer(ctx context.Context, endpointURL string) error
}

// NewSwarmCommand creates a new swarm command
func NewSwarmCommand() *SwarmCommand {
	return &SwarmCommand{
		interactive: false,
		width:       80,
		height:      24,
		focusIndex:  0,
		inputMode:   swarmModeNormal,
	}
}

// Name returns the command name (without leading /)
func (c *SwarmCommand) Name() string {
	return "swarm"
}

// Description returns autocomplete description
func (c *SwarmCommand) Description() string {
	return i18n.T("commands.swarm.description")
}

// Aliases returns alternative names
func (c *SwarmCommand) Aliases() []string {
	return []string{"peers", "a2a"}
}

// Execute runs when command is invoked
func (c *SwarmCommand) Execute(args []string) tea.Cmd {
	// "/swarm chat" → open the full-screen swarm chat room
	if len(args) > 0 && args[0] == "chat" {
		return func() tea.Msg { return SwarmChatOpenMsg{} }
	}
	c.interactive = true
	return nil
}

// IsInteractive returns true if command shows UI
func (c *SwarmCommand) IsInteractive() bool {
	return c.interactive
}

// SetSwarmProvider sets the swarm provider
func (c *SwarmCommand) SetSwarmProvider(provider SwarmProvider) {
	c.swarmProvider = provider
}

// Update handles messages (for interactive commands)
func (c *SwarmCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	if !c.interactive {
		return c, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch c.inputMode {
		case swarmModeNormal:
			return c.updateNormal(msg)
		case swarmModeInputHandle:
			return c.updateInputHandle(msg)
		case swarmModeInputPeer:
			return c.updateInputPeer(msg)
		case swarmModeConfirm:
			return c.updateConfirm(msg)
		}

	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.height = msg.Height
	}

	return c, nil
}

// updateNormal handles normal mode key events
func (c *SwarmCommand) updateNormal(msg tea.KeyMsg) (Command, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		c.interactive = false
		return c, nil
	case "e":
		// Enable A2A
		if c.swarmProvider != nil && !c.swarmProvider.IsA2AEnabled() {
			c.inputMode = swarmModeInputHandle
			c.inputBuffer = ""
			c.statusMessage = i18n.T("commands.swarm.enter_handle")
		}
	case "d":
		// Disable A2A
		if c.swarmProvider != nil && c.swarmProvider.IsA2AEnabled() {
			c.confirmAction = swarmConfirmDisable
			c.inputMode = swarmModeConfirm
			c.statusMessage = i18n.T("commands.swarm.disable_confirm")
		}
	case "a":
		// Add peer
		if c.swarmProvider != nil && c.swarmProvider.IsA2AEnabled() {
			c.inputMode = swarmModeInputPeer
			c.inputBuffer = ""
			c.statusMessage = i18n.T("commands.swarm.enter_peer_url")
		}
	case "r":
		// Refresh - just clear status
		c.statusMessage = ""
	}
	return c, nil
}

// updateInputHandle handles handle input mode
func (c *SwarmCommand) updateInputHandle(msg tea.KeyMsg) (Command, tea.Cmd) {
	switch msg.String() {
	case "esc":
		c.inputMode = swarmModeNormal
		c.inputBuffer = ""
		c.statusMessage = ""
		return c, nil
	case "enter":
		if c.inputBuffer != "" {
			// Enable A2A with the entered handle
			err := c.swarmProvider.EnableA2A(context.Background(), c.inputBuffer)
			if err != nil {
				c.statusMessage = i18n.T("commands.common.error_prefix", err)
			} else {
				c.statusMessage = i18n.T("commands.swarm.enabled_success")
			}
			c.inputMode = swarmModeNormal
			c.inputBuffer = ""
		}
		return c, nil
	case "backspace":
		if len(c.inputBuffer) > 0 {
			c.inputBuffer = c.inputBuffer[:len(c.inputBuffer)-1]
		}
	default:
		// Add character to buffer
		if len(msg.String()) == 1 && msg.String()[0] >= 32 {
			c.inputBuffer += msg.String()
		}
	}
	return c, nil
}

// updateInputPeer handles peer input mode
func (c *SwarmCommand) updateInputPeer(msg tea.KeyMsg) (Command, tea.Cmd) {
	switch msg.String() {
	case "esc":
		c.inputMode = swarmModeNormal
		c.inputBuffer = ""
		c.statusMessage = ""
		return c, nil
	case "enter":
		if c.inputBuffer != "" {
			// Add peer with the entered endpoint
			err := c.swarmProvider.AddPeer(context.Background(), c.inputBuffer)
			if err != nil {
				c.statusMessage = i18n.T("commands.swarm.add_peer_failed", err)
			} else {
				c.statusMessage = i18n.T("commands.swarm.peer_added")
			}
			c.inputMode = swarmModeNormal
			c.inputBuffer = ""
		}
		return c, nil
	case "backspace":
		if len(c.inputBuffer) > 0 {
			c.inputBuffer = c.inputBuffer[:len(c.inputBuffer)-1]
		}
	default:
		// Add character to buffer
		if len(msg.String()) == 1 && msg.String()[0] >= 32 {
			c.inputBuffer += msg.String()
		}
	}
	return c, nil
}

// updateConfirm handles confirmation mode
func (c *SwarmCommand) updateConfirm(msg tea.KeyMsg) (Command, tea.Cmd) {
	switch msg.String() {
	case "esc", "n":
		c.inputMode = swarmModeNormal
		c.confirmAction = swarmConfirmNone
		c.statusMessage = ""
		return c, nil
	case "y":
		switch c.confirmAction {
		case swarmConfirmDisable:
			err := c.swarmProvider.DisableA2A(context.Background())
			if err != nil {
				c.statusMessage = i18n.T("commands.common.error_prefix", err)
			} else {
				c.statusMessage = i18n.T("commands.swarm.disabled")
			}
		}
		c.inputMode = swarmModeNormal
		c.confirmAction = swarmConfirmNone
		return c, nil
	}
	return c, nil
}

// View renders the command UI (for interactive commands)
func (c *SwarmCommand) View() string {
	if !c.interactive {
		return ""
	}

	// Header style
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(ColorCyan)).
		Padding(0, 1)

	// Section style
	sectionStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(ColorYellow)).
		PaddingTop(1)

	// Label style
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Width(18)

	// Value style
	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite))

	// Status style (enabled/disabled)
	enabledStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGreen)).
		Bold(true)
	disabledStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorRed)).
		Bold(true)

	// Build content
	var content strings.Builder

	content.WriteString(headerStyle.Render(i18n.T("commands.swarm.title")))
	content.WriteString("\n")

	if c.swarmProvider == nil {
		content.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorRed)).
			Render(i18n.T("commands.swarm.provider_unavailable")))
		return c.renderFrame(content.String())
	}

	// A2A Status Section
	content.WriteString(sectionStyle.Render(i18n.T("commands.common.status")))
	content.WriteString("\n")

	a2aEnabled := c.swarmProvider.IsA2AEnabled()
	statusText := disabledStyle.Render(i18n.T("commands.common.disabled_upper"))
	if a2aEnabled {
		statusText = enabledStyle.Render(i18n.T("commands.common.enabled_upper"))
	}

	content.WriteString(labelStyle.Render(i18n.T("commands.swarm.a2a_mode_label")))
	content.WriteString(statusText)
	content.WriteString("\n")

	if a2aEnabled {
		content.WriteString(labelStyle.Render(i18n.T("commands.swarm.handle_label")))
		content.WriteString(valueStyle.Render(c.swarmProvider.GetA2AHandle()))
		content.WriteString("\n")

		content.WriteString(labelStyle.Render(i18n.T("commands.swarm.listen_address_label")))
		content.WriteString(valueStyle.Render(c.swarmProvider.GetA2AAddress()))
		content.WriteString("\n")

		content.WriteString(labelStyle.Render(i18n.T("commands.swarm.status_label")))
		swarmStatus := c.swarmProvider.GetSwarmStatus()
		statusStr := string(swarmStatus.Status)
		if swarmStatus.CurrentTask != "" {
			statusStr += fmt.Sprintf(" (%s)", swarmStatus.CurrentTask)
		}
		content.WriteString(valueStyle.Render(statusStr))
		content.WriteString("\n")
	}

	// Peers Section
	if a2aEnabled {
		content.WriteString(sectionStyle.Render(i18n.T("commands.swarm.connected_peers")))
		content.WriteString("\n")

		peers := c.swarmProvider.ListPeers()
		if len(peers) == 0 {
			content.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGray)).
				Italic(true).
				Render(i18n.T("commands.swarm.no_peers")))
		} else {
			for _, peer := range peers {
				peerLine := c.renderPeer(peer, labelStyle, valueStyle)
				content.WriteString(peerLine)
				content.WriteString("\n")
			}
		}
	}

	// Status message (for errors/success)
	if c.statusMessage != "" {
		content.WriteString("\n")
		msgStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorWhite)).
			Background(lipgloss.Color(ColorBlue)).
			Padding(0, 1)
		content.WriteString(msgStyle.Render(c.statusMessage))
		if c.inputMode == swarmModeInputHandle || c.inputMode == swarmModeInputPeer {
			content.WriteString(" ")
			content.WriteString(c.inputBuffer)
			content.WriteString("▌")
		}
	}

	// Help footer
	content.WriteString("\n\n")
	content.WriteString(c.renderHelpFooter(a2aEnabled))

	return c.renderFrame(content.String())
}

// renderHelpFooter shows available keyboard shortcuts
func (c *SwarmCommand) renderHelpFooter(a2aEnabled bool) string {
	keyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorCyan)).
		Bold(true)
	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray))

	var shortcuts []string

	if c.inputMode == swarmModeNormal {
		if a2aEnabled {
			shortcuts = []string{
				keyStyle.Render("[d]") + descStyle.Render(i18n.T("commands.swarm.action.disable")),
				keyStyle.Render("[a]") + descStyle.Render(i18n.T("commands.swarm.action.add_peer")),
				keyStyle.Render("[r]") + descStyle.Render(i18n.T("commands.swarm.action.refresh")),
			}
		} else {
			shortcuts = []string{
				keyStyle.Render("[e]") + descStyle.Render(i18n.T("commands.swarm.action.enable")),
			}
		}
		shortcuts = append(shortcuts, keyStyle.Render("[q]")+descStyle.Render(i18n.T("commands.common.action.close")))
	} else if c.inputMode == swarmModeConfirm {
		shortcuts = []string{
			keyStyle.Render("[y]") + descStyle.Render(i18n.T("commands.common.action.confirm")),
			keyStyle.Render("[n/esc]") + descStyle.Render(i18n.T("commands.common.action.cancel")),
		}
	} else {
		shortcuts = []string{
			keyStyle.Render("[enter]") + descStyle.Render(i18n.T("commands.common.action.submit")),
			keyStyle.Render("[esc]") + descStyle.Render(i18n.T("commands.common.action.cancel")),
		}
	}

	return strings.Join(shortcuts, "  ")
}

func (c *SwarmCommand) renderPeer(peer a2a.SwarmPeerInfo, labelStyle, valueStyle lipgloss.Style) string {
	var parts []string
	// Handle
	handleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorCyan)).
		Bold(true)
	parts = append(parts, handleStyle.Render(peer.Handle))
	// Status indicator based on SwarmStatus
	status := "🟢" // idle
	switch peer.Status {
	case a2a.SwarmStatusWorking:
		status = "🟡"
	case a2a.SwarmStatusBusy:
		status = "🔴"
	case a2a.SwarmStatusAway:
		status = "⚪"
	}
	parts = append(parts, status)
	// Current task
	if peer.CurrentTask != "" {
		parts = append(parts, lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorGray)).
			Italic(true).
			Render(i18n.T("commands.swarm.working_on", peer.CurrentTask)))
	}
	// Running time
	if peer.RunningTime != "" {
		parts = append(parts, lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorGray)).
			Render(fmt.Sprintf("(%s)", peer.RunningTime)))
	}
	return strings.Join(parts, " ")
}

func (c *SwarmCommand) renderFrame(content string) string {
	frameStyle := lipgloss.NewStyle().
		Width(c.width - 4).
		Height(c.height - 4).
		Padding(2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorCyan))

	return frameStyle.Render(content)
}

// Subcommands returns sub-command completions for /swarm.
func (c *SwarmCommand) Subcommands() []Subcommand {
	return []Subcommand{
		{Name: "chat", Description: i18n.T("commands.swarm.subcommand.chat")},
	}
}

// ArgumentCompletions returns completions for a specific subcommand's arguments.
func (c *SwarmCommand) ArgumentCompletions(subcommand string) []string {
	return nil
}

// SwarmChatOpenMsg is emitted by /swarm chat to open the ScreenSwarmChat screen.
type SwarmChatOpenMsg struct{}
