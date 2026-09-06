package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/update"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

// UpdateCheckMsg is sent when a background update check completes
type UpdateCheckMsg struct {
	Result *update.CheckResult
}

// UpdateDownloadProgressMsg is sent during download progress
type UpdateDownloadProgressMsg struct {
	Progress update.DownloadProgress
}

// UpdateDownloadCompleteMsg is sent when download completes
type UpdateDownloadCompleteMsg struct {
	Result *update.DownloadResult
}

// UpdateApplyMsg is sent when the user wants to apply the update
type UpdateApplyMsg struct {
	Result *update.DownloadResult
}

// checkForUpdateCmd returns a tea.Cmd that checks for updates in the background
func (a *App) checkForUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		if a.updater == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resultChan := a.updater.CheckAsync(ctx, version.Version)
		result := <-resultChan

		return UpdateCheckMsg{Result: result}
	}
}

// handleUpdateCheck handles the result of a background update check
func (a *App) handleUpdateCheck(msg UpdateCheckMsg) (tea.Model, tea.Cmd) {
	a.updateCheckPending = false

	if msg.Result == nil {
		return a, nil
	}

	if msg.Result.Error != nil {
		return a, nil
	}

	if msg.Result.UpdateAvailable {
		a.updateAvailable = msg.Result

		// Check if this is a mandatory update
		if msg.Result.Mandatory {
			a.updateMandatoryBlocked = true
			// Don't start background download for mandatory updates - wait for user action
			return a, nil
		}

		// Start background download immediately for non-mandatory updates
		if a.updater != nil && msg.Result.Release != nil {
			a.updater.StartBackgroundDownload(msg.Result.Release)
		}

		// Check if we should notify (not already notified about this version)
		if a.updater.ShouldNotify(msg.Result.LatestVersion) {
			a.addNotification("info", tr("classic.update.available", msg.Result.LatestVersion))
			if err := a.updater.MarkNotified(msg.Result.LatestVersion); err != nil {
			}
		}
	} else {
	}

	return a, nil
}

// handleUpdateDownloadProgress handles download progress updates
func (a *App) handleUpdateDownloadProgress(msg UpdateDownloadProgressMsg) (tea.Model, tea.Cmd) {
	a.updateDownloading = true
	return a, nil
}

// handleUpdateDownloadComplete handles download completion
func (a *App) handleUpdateDownloadComplete(msg UpdateDownloadCompleteMsg) (tea.Model, tea.Cmd) {
	a.updateDownloading = false

	if msg.Result == nil || msg.Result.Error != nil {
		err := "download failed"
		if msg.Result != nil && msg.Result.Error != nil {
			err = msg.Result.Error.Error()
		}
		a.addNotification("error", tr("classic.update.download_failed", err))
		return a, nil
	}

	a.updateDownloadResult = msg.Result
	a.addNotification("success", tr("classic.update.downloaded"))

	return a, nil
}

// handleUpdateApply handles applying the update and restarting
func (a *App) handleUpdateApply(msg UpdateApplyMsg) (tea.Model, tea.Cmd) {
	if msg.Result == nil {
		return a, nil
	}

	// Apply the binary replacement
	replaceResult, err := a.updater.Apply(msg.Result)
	if err != nil {
		a.addNotification("error", tr("classic.update.apply_failed", err))
		return a, nil
	}

	// Restart the application
	if err := a.updater.Restart(replaceResult); err != nil {
		a.addNotification("error", tr("classic.update.restart_failed", err))
		return a, nil
	}

	// The app will exit here, this is just for safety
	a.quitting = true
	return a, tea.Quit
}

// downloadUpdateCmd returns a tea.Cmd that downloads the update
func (a *App) downloadUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		if a.updater == nil || a.updateAvailable == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		progressChan := make(chan update.DownloadProgress, 10)
		resultChan := a.updater.DownloadAsync(ctx, a.updateAvailable.Release, progressChan)

		// Wait for completion
		result := <-resultChan
		return UpdateDownloadCompleteMsg{Result: result}
	}
}

// renderMandatoryUpdateModal renders a full-screen blocking modal for mandatory updates
func (a *App) renderMandatoryUpdateModal() string {
	if a.updateAvailable == nil {
		return ""
	}

	result := a.updateAvailable
	th := a.theme

	// Styles
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Error)).
		Padding(2, 4).
		Width(70)

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Error)).
		Bold(true).
		MarginBottom(1)

	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text))

	buttonStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.Primary)).
		Foreground(lipgloss.Color(th.BG)).
		Padding(0, 2).
		Bold(true)

	quitStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))

	// Build content
	var content strings.Builder

	content.WriteString(titleStyle.Render(tr("classic.update.mandatory_title")))
	content.WriteString("\n\n")

	content.WriteString(infoStyle.Render(tr("classic.update.versions", result.CurrentVersion, result.LatestVersion)))
	content.WriteString("\n\n")

	content.WriteString(infoStyle.Render(tr("classic.update.mandatory_body")))
	content.WriteString("\n\n")

	if result.Release != nil && result.Release.Body != "" {
		content.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(tr("classic.update.whats_new")))
		content.WriteString("\n")
		// Truncate release notes if too long
		notes := result.Release.Body
		if len(notes) > 300 {
			notes = notes[:297] + "..."
		}
		content.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(notes))
		content.WriteString("\n\n")
	}

	content.WriteString(fmt.Sprintf(
		"%s    %s",
		buttonStyle.Render(tr("classic.update.update_now")),
		quitStyle.Render(tr("classic.update.quit")),
	))

	// Center the modal
	modalContent := boxStyle.Render(content.String())
	return lipgloss.Place(
		a.width,
		a.height,
		lipgloss.Center,
		lipgloss.Center,
		modalContent,
		lipgloss.WithWhitespaceChars(" "),
	)
}
