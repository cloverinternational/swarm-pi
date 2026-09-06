package chat

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// openVaultControl opens the shared /vault panel or asks before locking the
// whole in-memory vault. It is used by the sidebar and keyboard shortcut so
// those entry points cannot diverge from the slash-command behavior.
func (a *App) openVaultControl() {
	if a.settingsManager == nil {
		return
	}
	vaultSettings := a.settingsManager.GetVaultSettings()
	if vaultSettings == nil {
		return
	}

	if vaultSettings.IsUnlocked() {
		a.activeModal = &Modal{
			Title:    i18n.T("classic_chat.vault.lock_title"),
			Message:  i18n.T("classic_chat.vault.lock_message"),
			Options:  []string{i18n.T("classic_chat.common.cancel"), i18n.T("classic_chat.vault.lock_action")},
			Selected: 0,
			OnSelect: func(option int) {
				a.activeModal = nil
				if option != 1 {
					return
				}
				vaultSettings.LockAll(a.settingsManager.GetState())
				a.sidePanelCache.valid = false
				a.addNotification("info", i18n.T("classic_chat.vault.locked"))
			},
		}
		return
	}

	if a.cmdRegistry == nil {
		return
	}
	cmd, ok := a.cmdRegistry.Get("vault")
	if !ok {
		return
	}
	cmd.Execute(nil)
	if !cmd.IsInteractive() {
		return
	}
	cmdWidth := a.width
	if a.showSidePanel && a.width >= MinWidthForSidePanel {
		cmdWidth -= SidePanelWidth
	}
	_, _ = cmd.Update(tea.WindowSizeMsg{Width: cmdWidth, Height: a.height})
	a.activeCommand = cmd
	a.cmdAutocomplete.Hide()
	a.mentionAutocomplete.Hide()
}

func (a *App) enqueueVaultUnlockRequest(req vaultUnlockRequest) {
	if a.vaultUnlockBroker == nil || a.settingsManager == nil {
		return
	}
	if a.terminalImagesSuppressed() {
		_ = a.vaultUnlockBroker.Respond(req.ID, errVaultUnlockBusy)
		return
	}
	vaultSettings := a.settingsManager.GetVaultSettings()
	if vaultSettings == nil {
		_ = a.vaultUnlockBroker.Respond(req.ID, errVaultUnlockCanceled)
		return
	}

	cmd := newVaultUnlockCommand(
		vaultSettings,
		a.settingsManager.GetState(),
		req.ID,
		req.Scope,
		func() {
			a.activeCommand = nil
			a.sidePanelCache.valid = false
			_ = a.vaultUnlockBroker.Respond(req.ID, nil)
		},
		func() {
			a.activeCommand = nil
			_ = a.vaultUnlockBroker.Respond(req.ID, errVaultUnlockCanceled)
		},
	)
	cmdWidth := a.width
	if a.showSidePanel && a.width >= MinWidthForSidePanel {
		cmdWidth -= SidePanelWidth
	}
	_, _ = cmd.Update(tea.WindowSizeMsg{Width: cmdWidth, Height: a.height})
	a.activeCommand = cmd
	a.cmdAutocomplete.Hide()
	a.mentionAutocomplete.Hide()
}

func (a *App) removeVaultUnlockRequest(requestID string) {
	cmd, ok := a.activeCommand.(*vaultCommand)
	if !ok || cmd.unlockRequestID != requestID {
		return
	}
	cmd.interactive = false
	a.activeCommand = nil
}
