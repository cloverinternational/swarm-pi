package chat

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// vaultSubcommandResultMsg carries the textual output of a non-interactive
// /vault subcommand (team, approve, status) so the update loop can append it to
// the chat transcript as a system message. level is "info"/"success"/"error".
type vaultSubcommandResultMsg struct {
	level string
	text  string
}

// vaultCommand implements the /vault slash command. It pops up an interactive
// window that lets the user unlock (or lock) the credential vault in real time
// while chatting with the agent. On unlock, the shared settings.VaultSettings
// fires its onVaultUnlock callback (wired in app_init.go) which injects the
// vault provider into the live SDK/agent — so credentials become available to
// the agent immediately, without leaving the chat.
//
// This type lives in the `chat` package (not `internal/chat/commands`) on
// purpose: package `settings` imports `commands`, so `commands` cannot import
// `settings`. The `chat` package imports both, making it the only place that
// can wrap a settings panel as a commands.Command.
type vaultCommand struct {
	vault           *settings.VaultSettings
	state           *settings.State
	interactive     bool
	width           int
	height          int
	unlockRequestID string
	onUnlocked      func()
	onCanceled      func()
}

// newVaultCommand creates the /vault command backed by the shared vault
// settings panel and settings state (both owned by the settings manager).
func newVaultCommand(vault *settings.VaultSettings, state *settings.State) *vaultCommand {
	return &vaultCommand{
		vault:  vault,
		state:  state,
		width:  80,
		height: 24,
	}
}

func newVaultUnlockCommand(
	vaultSettings *settings.VaultSettings,
	state *settings.State,
	requestID string,
	scope string,
	onUnlocked func(),
	onCanceled func(),
) *vaultCommand {
	cmd := newVaultCommand(vaultSettings, state)
	cmd.interactive = true
	cmd.unlockRequestID = requestID
	cmd.onUnlocked = onUnlocked
	cmd.onCanceled = onCanceled
	if vaultSettings != nil {
		vaultSettings.SelectScope(scope, state)
	}
	return cmd
}

func (c *vaultCommand) finishUnlock(success bool) {
	if c.onUnlocked == nil && c.onCanceled == nil {
		return
	}
	c.interactive = false
	if success {
		if c.onUnlocked != nil {
			c.onUnlocked()
		}
		return
	}
	if c.onCanceled != nil {
		c.onCanceled()
	}
}

// Name returns the command name (without the leading /).
func (c *vaultCommand) Name() string { return "vault" }

// Description returns the autocomplete description.
func (c *vaultCommand) Description() string {
	return i18n.T("classic_chat_3.vault.description")
}

// Aliases returns alternative names for this command.
func (c *vaultCommand) Aliases() []string { return []string{"unlock"} }

// Execute activates the interactive vault panel, or — when args are given —
// runs a non-interactive subcommand (team / approve / status) that reports its
// result to the chat transcript via vaultSubcommandResultMsg.
func (c *vaultCommand) Execute(args []string) tea.Cmd {
	if len(args) == 0 {
		c.interactive = true
		return nil
	}

	sub := strings.ToLower(args[0])
	rest := args[1:]
	switch sub {
	case "team":
		return c.runTeam(rest)
	case "approve":
		return c.runApprove(rest)
	case "status":
		return c.runStatus(rest)
	default:
		// Unknown subcommand: fall back to opening the interactive panel so the
		// user is never stuck (e.g. "/vault unlock" alias behavior).
		c.interactive = true
		return nil
	}
}

// vaultResult builds a tea.Cmd that posts a subcommand result to the transcript.
func vaultResult(level, text string) tea.Cmd {
	return func() tea.Msg { return vaultSubcommandResultMsg{level: level, text: text} }
}

// runTeam handles `/vault team identity|members|allow|revoke`.
func (c *vaultCommand) runTeam(args []string) tea.Cmd {
	if c.vault == nil {
		return vaultResult("error", i18n.T("classic_chat_3.vault.unavailable"))
	}
	if len(args) == 0 {
		return vaultResult("info", i18n.T("classic_chat_3.vault.team_usage"))
	}
	recipientsPath := c.vault.GetRecipientsPath()

	switch strings.ToLower(args[0]) {
	case "identity":
		pub := c.vault.GetIdentityPublicKey()
		if pub == "" {
			return vaultResult("error", i18n.T("classic_chat_3.vault.no_identity"))
		}
		return vaultResult("info", i18n.T("classic_chat_3.vault.public_key", pub))

	case "members":
		if recipientsPath == "" {
			return vaultResult("error", i18n.T("classic_chat_3.vault.no_roster"))
		}
		members, err := vault.Members(recipientsPath)
		if err != nil {
			return vaultResult("error", i18n.T("classic_chat_3.vault.read_roster_failed", err))
		}
		if len(members) == 0 {
			return vaultResult("info", i18n.T("classic_chat_3.vault.roster_empty"))
		}
		var b strings.Builder
		b.WriteString(i18n.T("classic_chat_3.vault.roster_count", len(members)))
		for _, m := range members {
			name := m.Comment
			if name == "" {
				name = i18n.T("classic_chat_3.vault.unnamed")
			}
			b.WriteString(fmt.Sprintf("  • %s  %s\n", name, truncateKey(m.PublicKey)))
		}
		return vaultResult("info", strings.TrimRight(b.String(), "\n"))

	case "allow":
		if recipientsPath == "" {
			return vaultResult("error", i18n.T("classic_chat_3.vault.no_roster"))
		}
		if len(args) < 2 {
			return vaultResult("info", i18n.T("classic_chat_3.vault.allow_usage"))
		}
		pubkey := args[1]
		name := ""
		if len(args) >= 3 {
			name = strings.Join(args[2:], " ")
		}
		note, err := vault.AllowMember(recipientsPath, pubkey, name)
		if err != nil {
			return vaultResult("error", i18n.T("classic_chat_3.vault.add_member_failed", err))
		}
		msg := i18n.T("classic_chat_3.vault.added_member", truncateKey(pubkey))
		if note != "" {
			msg += " " + note
		}
		msg += i18n.T("classic_chat_3.vault.reseal_note")
		return vaultResult("success", msg)

	case "revoke":
		if recipientsPath == "" {
			return vaultResult("error", i18n.T("classic_chat_3.vault.no_roster"))
		}
		if len(args) < 2 {
			return vaultResult("info", i18n.T("classic_chat_3.vault.revoke_usage"))
		}
		note, err := vault.RevokeMember(recipientsPath, args[1])
		if err != nil {
			return vaultResult("error", i18n.T("classic_chat_3.vault.revoke_member_failed", err))
		}
		msg := i18n.T("classic_chat_3.vault.revoked_member", args[1])
		if note != "" {
			msg += " " + note
		}
		msg += i18n.T("classic_chat_3.vault.revocation_warning")
		return vaultResult("success", msg)

	default:
		return vaultResult("info", i18n.T("classic_chat_3.vault.team_usage"))
	}
}

// runApprove handles `/vault approve <requestId>` — the second-person approval
// action performed from within the TUI.
func (c *vaultCommand) runApprove(args []string) tea.Cmd {
	if len(args) == 0 {
		return vaultResult("info", i18n.T("classic_chat_3.vault.approve_usage"))
	}
	requestID := args[0]

	provider := vault.GetDefaultVaultProvider()
	if provider == nil || !provider.IsEnabled() {
		return vaultResult("error", i18n.T("classic_chat_3.vault.locked_approve"))
	}
	if c.vault == nil {
		return vaultResult("error", i18n.T("classic_chat_3.vault.unavailable"))
	}
	identityPath := c.vault.GetIdentityPath()
	if identityPath == "" || !vault.IdentityExists(identityPath) {
		return vaultResult("error", i18n.T("classic_chat_3.vault.no_approver_identity"))
	}
	identity, err := vault.LoadX25519Identity(identityPath)
	if err != nil {
		return vaultResult("error", i18n.T("classic_chat_3.vault.load_identity_failed", err))
	}

	exec := provider.GetExecutor()
	if exec == nil {
		return vaultResult("error", "vault executor unavailable")
	}
	satisfied, err := exec.ApproveTwoPerson(requestID, identity)
	if err != nil {
		return vaultResult("error", fmt.Sprintf("Approval failed: %v", err))
	}
	if satisfied {
		return vaultResult("success", fmt.Sprintf(
			"Approval recorded for %s. Threshold met — the original requester can now retry vault_exec to run the command.", requestID))
	}
	return vaultResult("success", fmt.Sprintf(
		"Approval recorded for %s. More distinct approvers are still required.", requestID))
}

// runStatus handles `/vault status <requestId>` — read-only poll of a pending
// two-person request. With no requestId, it opens the interactive panel.
func (c *vaultCommand) runStatus(args []string) tea.Cmd {
	if len(args) == 0 {
		c.interactive = true
		return nil
	}
	requestID := args[0]

	provider := vault.GetDefaultVaultProvider()
	if provider == nil || !provider.IsEnabled() {
		return vaultResult("error", "Vault is locked — unlock it (type /vault) before checking status.")
	}
	exec := provider.GetExecutor()
	if exec == nil {
		return vaultResult("error", "vault executor unavailable")
	}
	info, ok := exec.TwoPersonRequestInfo(requestID)
	if !ok {
		return vaultResult("info", fmt.Sprintf("Request %s is not pending (unknown, already finalized, or expired).", requestID))
	}
	approvals := len(info.Contributors)
	remaining := info.Threshold - approvals
	if remaining < 0 {
		remaining = 0
	}
	status := fmt.Sprintf("%d of %d approvers", approvals, info.Threshold)
	if approvals >= info.Threshold {
		status += " — SATISFIED (requester may retry vault_exec)"
	} else {
		status += fmt.Sprintf(" — need %d more distinct approver(s)", remaining)
	}
	return vaultResult("info", fmt.Sprintf("Two-person request %s (%s):\n  command: %s\n  %s",
		info.ID, info.CredentialID, info.Command, status))
}

// truncateKey shortens an age public key for display.
func truncateKey(key string) string {
	if len(key) <= 20 {
		return key
	}
	return key[:12] + "…" + key[len(key)-6:]
}

// firstLine returns the first line of s (for compact notification text).
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// IsInteractive returns true while the panel is open. handleSlashCommand sets
// this command as a.activeCommand when true; the update loop clears
// a.activeCommand once this returns false (i.e. once the panel is closed).
func (c *vaultCommand) IsInteractive() bool { return c.interactive }

// Update handles messages for the interactive panel.
func (c *vaultCommand) Update(msg tea.Msg) (commands.Command, tea.Cmd) {
	if !c.interactive {
		return c, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if c.vault == nil || c.state == nil {
			c.interactive = false
			return c, nil
		}
		key := msg.String()
		wasUnlocking := c.state.VaultState == "unlocking"
		// Let the vault panel handle the key first (passphrase entry,
		// unlock/lock, scope switch, navigation, etc).
		if c.vault.HandleKey(key, c.state) {
			if c.state.VaultState == "unlocked" &&
				vault.GetDefaultVaultProvider().IsEnabled() {
				c.finishUnlock(true)
			} else if key == "esc" && wasUnlocking {
				c.finishUnlock(false)
			}
			return c, nil
		}
		// Unhandled keys close the panel. In the "locked"/"unlocked" states
		// the panel does not consume "q", so it falls through to here.
		switch key {
		case "q", "esc", "ctrl+c":
			if c.onUnlocked != nil || c.onCanceled != nil {
				c.finishUnlock(false)
			} else {
				c.interactive = false
			}
		}
		return c, nil

	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.height = msg.Height
	}

	return c, nil
}

// View renders the vault panel inside a bordered modal box. Returns "" when
// closed so the chat renders normally.
func (c *vaultCommand) View() string {
	if !c.interactive || c.vault == nil || c.state == nil {
		return ""
	}

	// Constrain the modal to a sensible width/height within the available area.
	boxWidth := c.width - 4
	if boxWidth > 76 {
		boxWidth = 76
	}
	if boxWidth < 20 {
		boxWidth = 20
	}

	inner := c.vault.Render(c.state, boxWidth, c.height-4)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#39D2C0")).
		Padding(1, 2).
		Width(boxWidth)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#666666")).
		Italic(true).
		Render("Press q to close")

	return box.Render(inner + "\n\n" + hint)
}
