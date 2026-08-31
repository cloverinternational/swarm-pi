package chat

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// bashClampSelection keeps an index within [0, n) (or -1 when the list is empty).
func bashClampSelection(idx, n int) int {
	if n <= 0 {
		return -1
	}
	if idx < 0 {
		return 0
	}
	if idx >= n {
		return n - 1
	}
	return idx
}

// bashMoveSelection moves the selection by delta with clamping (no wraparound).
func bashMoveSelection(idx, delta, n int) int {
	return bashClampSelection(idx+delta, n)
}

// bashResolveSelection preserves a selected process across filtering and
// state-driven reordering, falling back to the clamped prior index if it left
// the list. It returns (-1, "") for an empty list.
func bashResolveSelection(rows []bashRow, selectedIdx int, selectedID string) (int, string) {
	if len(rows) == 0 {
		return -1, ""
	}
	if selectedID != "" {
		for i, row := range rows {
			if row.id == selectedID {
				return i, selectedID
			}
		}
	}
	selectedIdx = bashClampSelection(selectedIdx, len(rows))
	return selectedIdx, rows[selectedIdx].id
}

// bashVisibleWindow returns the same selected-centered row window used by the
// renderer. The returned start is the absolute index of window[0] in rows.
func bashVisibleWindow(rows []bashRow, selectedIdx, capN int) (window []bashRow, start int) {
	if capN <= 0 {
		capN = bashActivityDefaultCap
	}
	if len(rows) <= capN {
		return rows, 0
	}
	selectedIdx = bashClampSelection(selectedIdx, len(rows))
	start = selectedIdx - capN/2
	if start < 0 {
		start = 0
	}
	if maxStart := len(rows) - capN; start > maxStart {
		start = maxStart
	}
	return rows[start : start+capN], start
}

// bashKillTargetID resolves which running command id the selected index maps to,
// returning "" when the selection is out of range or not a running command.
func bashKillTargetID(rows []bashRow, selectedIdx int) string {
	if selectedIdx < 0 || selectedIdx >= len(rows) {
		return ""
	}
	r := rows[selectedIdx]
	if r.state != bgprocess.StateRunning {
		return ""
	}
	return r.id
}

// handleBashPanelKey routes keys while the BASH side-panel section is focused.
// It returns handled=false when the key is not one the panel consumes, so the
// caller can fall through to normal chat handling.
func (a *App) handleBashPanelKey(key string) (handled bool, cmd tea.Cmd) {
	if !a.bashPanelFocused {
		return false, nil
	}
	rows := a.currentBashRows()
	n := len(rows)
	a.bashSelectedIdx, a.bashSelectedID = bashResolveSelection(rows, a.bashSelectedIdx, a.bashSelectedID)
	switch key {
	case "up", "ctrl+p":
		a.bashSelectedIdx = bashMoveSelection(a.bashSelectedIdx, -1, n)
		if a.bashSelectedIdx >= 0 {
			a.bashSelectedID = rows[a.bashSelectedIdx].id
		}
		a.invalidateSidePanelCache()
		return true, nil
	case "down", "ctrl+n":
		a.bashSelectedIdx = bashMoveSelection(a.bashSelectedIdx, 1, n)
		if a.bashSelectedIdx >= 0 {
			a.bashSelectedID = rows[a.bashSelectedIdx].id
		}
		a.invalidateSidePanelCache()
		return true, nil
	case "k", "ctrl+x":
		return true, a.killSelectedBashCommand(rows)
	case "enter":
		if a.bashSelectedIdx >= 0 {
			a.openBashDetail(rows[a.bashSelectedIdx].id)
		}
		return true, nil
	case "esc":
		a.bashPanelFocused = false
		a.invalidateSidePanelCache()
		return true, nil
	}
	return false, nil
}

// currentBashRows rebuilds the render-ready rows for interaction decisions.
func (a *App) currentBashRows() []bashRow {
	if a.sdk == nil {
		return nil
	}
	procs := a.sdk.GetSidePanelBashProcesses()
	if len(procs) == 0 {
		return nil
	}
	return buildBashRows(procs, a.sdk.GetBashProcessTail, time.Now())
}

// killSelectedBashCommand cancels the currently selected running command.
func (a *App) killSelectedBashCommand(rows []bashRow) tea.Cmd {
	a.bashSelectedIdx, a.bashSelectedID = bashResolveSelection(rows, a.bashSelectedIdx, a.bashSelectedID)
	id := bashKillTargetID(rows, a.bashSelectedIdx)
	if id == "" {
		a.addNotification("warning", i18n.T("classic_chat.bash.select_running"))
		return nil
	}
	if a.sdk == nil {
		a.addNotification("error", i18n.T("classic_chat.bash.sdk_uninitialized"))
		return nil
	}
	if err := a.sdk.CancelBashProcess(id); err != nil {
		a.addNotification("error", i18n.T("classic_chat.bash.kill_failed", err))
		return nil
	}
	a.addNotification("success", i18n.T("classic_chat.bash.killed", id))
	a.invalidateSidePanelCache()
	return nil
}

// toggleBashPanelFocus flips keyboard focus into/out of the BASH side panel.
func (a *App) toggleBashPanelFocus() {
	a.bashPanelFocused = !a.bashPanelFocused
	if a.bashPanelFocused {
		rows := a.currentBashRows()
		a.bashSelectedIdx, a.bashSelectedID = bashResolveSelection(rows, a.bashSelectedIdx, a.bashSelectedID)
	}
	a.invalidateSidePanelCache()
}

// invalidateSidePanelCache forces the side panel to re-render on the next frame.
func (a *App) invalidateSidePanelCache() {
	a.sidePanelCache.valid = false
}
