package commands

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/update"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

// UpdateAvailableMsg is sent when an update check finds a new version
type UpdateAvailableMsg struct {
	Result *update.CheckResult
}

// UpdateCheckCompleteMsg is sent when a manual update check completes
type UpdateCheckCompleteMsg struct {
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

// UpdateApplyMsg is sent when the user confirms to apply the update
type UpdateApplyMsg struct {
	FilePath string
}

// UpdateSkipMsg is sent when the user skips an update
type UpdateSkipMsg struct {
	Version string
}

// UpdateCommand handles checking and applying updates
type UpdateCommand struct {
	visible     bool
	mode        updateMode
	width       int
	updater     *update.Updater
	checkResult *update.CheckResult
	downloadRes *update.DownloadResult
	progress    update.DownloadProgress
	error       error
}

type updateMode int

const (
	modeIdle updateMode = iota
	modeChecking
	modeAvailable
	modeDownloading
	modeReady
	modeError
)

// NewUpdateCommand creates a new /update command
func NewUpdateCommand(updater *update.Updater) *UpdateCommand {
	return &UpdateCommand{
		updater: updater,
		mode:    modeIdle,
	}
}

func (c *UpdateCommand) Name() string {
	return "update"
}

func (c *UpdateCommand) Description() string {
	return i18n.T("commands.update.description")
}

func (c *UpdateCommand) Aliases() []string {
	return []string{"upgrade"}
}

// Execute runs the command with given arguments
func (c *UpdateCommand) Execute(args []string) tea.Cmd {
	c.visible = true

	// Check for subcommands
	if len(args) > 0 {
		switch args[0] {
		case "check", "status":
			return c.checkForUpdate()
		case "skip":
			if c.checkResult != nil {
				return func() tea.Msg {
					return UpdateSkipMsg{Version: c.checkResult.LatestVersion}
				}
			}
			return nil
		case "cancel":
			c.visible = false
			c.mode = modeIdle
			return nil
		}
	}

	// Default: check for updates
	return c.checkForUpdate()
}

// checkForUpdate returns a command that checks for updates
func (c *UpdateCommand) checkForUpdate() tea.Cmd {
	c.mode = modeChecking
	c.error = nil

	return func() tea.Msg {
		// This would normally use the updater, but we need to pass version
		// The App will handle the actual check via UpdateCheckCmd
		return UpdateCheckCompleteMsg{Result: nil}
	}
}

// SetCheckResult updates the command with check results
func (c *UpdateCommand) SetCheckResult(result *update.CheckResult) {
	c.checkResult = result
	if result.Error != nil {
		c.mode = modeError
		c.error = result.Error
	} else if result.UpdateAvailable {
		c.mode = modeAvailable
	} else {
		c.mode = modeIdle
	}
}

// Update handles messages for interactive mode
func (c *UpdateCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	if !c.visible {
		return c, nil
	}

	switch msg := msg.(type) {
	case UpdateCheckCompleteMsg:
		c.checkResult = msg.Result
		if msg.Result == nil {
			// App will set the result via SetCheckResult
			return c, nil
		}
		if msg.Result.Error != nil {
			c.mode = modeError
			c.error = msg.Result.Error
		} else if msg.Result.UpdateAvailable {
			c.mode = modeAvailable
		} else {
			c.mode = modeIdle
			c.visible = false
		}
		return c, nil

	case UpdateDownloadProgressMsg:
		c.progress = msg.Progress
		c.mode = modeDownloading
		return c, nil

	case UpdateDownloadCompleteMsg:
		c.downloadRes = msg.Result
		if msg.Result.Error != nil {
			c.mode = modeError
			c.error = msg.Result.Error
		} else {
			c.mode = modeReady
		}
		return c, nil

	case tea.KeyMsg:
		return c.handleKeyPress(msg)
	}

	return c, nil
}

func (c *UpdateCommand) handleKeyPress(msg tea.KeyMsg) (Command, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		c.visible = false
		c.mode = modeIdle
		return c, func() tea.Msg { return UpdateSkipMsg{} }

	case "enter":
		switch c.mode {
		case modeAvailable:
			// User wants to download
			c.mode = modeDownloading
			return c, func() tea.Msg {
				// Signal to App to start download
				return UpdateAvailableMsg{Result: c.checkResult}
			}
		case modeReady:
			// User wants to apply update
			if c.downloadRes != nil {
				return c, func() tea.Msg {
					return UpdateApplyMsg{FilePath: c.downloadRes.FilePath}
				}
			}
		}
		return c, nil

	case "y", "Y":
		if c.mode == modeAvailable {
			c.mode = modeDownloading
			return c, func() tea.Msg {
				return UpdateAvailableMsg{Result: c.checkResult}
			}
		}

	case "n", "N":
		if c.mode == modeAvailable {
			c.visible = false
			c.mode = modeIdle
			return c, func() tea.Msg {
				return UpdateSkipMsg{Version: c.checkResult.LatestVersion}
			}
		}
	}

	return c, nil
}

// View renders the update UI
func (c *UpdateCommand) View() string {
	if !c.visible {
		return ""
	}

	panelW := max(c.width-4, 50)
	if panelW > 80 {
		panelW = 80
	}

	accent := lipgloss.Color(palette.Accent)
	dim := lipgloss.Color(palette.TextDim)
	success := lipgloss.Color(palette.Success)
	warn := lipgloss.Color(palette.Warning)
	errColor := lipgloss.Color(palette.Error)

	titleStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
	textStyle := lipgloss.NewStyle()
	dimStyle := lipgloss.NewStyle().Foreground(dim)
	successStyle := lipgloss.NewStyle().Foreground(success)
	warnStyle := lipgloss.NewStyle().Foreground(warn)
	errorStyle := lipgloss.NewStyle().Foreground(errColor).Bold(true)

	borderColor := accent
	if c.mode == modeError {
		borderColor = errColor
	} else if c.mode == modeReady {
		borderColor = success
	}

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(panelW)

	var b strings.Builder

	switch c.mode {
	case modeChecking:
		b.WriteString(titleStyle.Render(i18n.T("commands.update.checking_title")) + "\n\n")
		b.WriteString(dimStyle.Render(i18n.T("commands.update.contacting_github")))

	case modeAvailable:
		b.WriteString(titleStyle.Render(i18n.T("commands.update.available_title")) + "\n\n")
		if c.checkResult != nil {
			b.WriteString(textStyle.Render(i18n.T("commands.update.current_version", c.checkResult.CurrentVersion)) + "\n")
			b.WriteString(successStyle.Render(i18n.T("commands.update.latest_version", c.checkResult.LatestVersion)) + "\n\n")

			if c.checkResult.Release != nil && c.checkResult.Release.Body != "" {
				b.WriteString(dimStyle.Render(i18n.T("commands.update.changelog_label")) + "\n")
				// Show first few lines of changelog
				lines := strings.Split(c.checkResult.Release.Body, "\n")
				maxLines := 5
				for i, line := range lines {
					if i >= maxLines {
						b.WriteString(dimStyle.Render("  ...") + "\n")
						break
					}
					line = strings.TrimSpace(line)
					if line != "" {
						b.WriteString(textStyle.Render("  "+line) + "\n")
					}
				}
				b.WriteString("\n")
			}
		}

		b.WriteString(warnStyle.Render(i18n.T("commands.update.download_install")) + "\n")
		b.WriteString(dimStyle.Render(i18n.T("commands.update.skip_version")) + "\n")
		b.WriteString(dimStyle.Render(i18n.T("commands.update.cancel")))

	case modeDownloading:
		b.WriteString(titleStyle.Render(i18n.T("commands.update.downloading_title")) + "\n\n")
		percent := c.progress.Percent
		barWidth := panelW - 10
		filled := int(percent / 100 * float64(barWidth))
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		b.WriteString(textStyle.Render(fmt.Sprintf("  %s %.1f%%", bar, percent)) + "\n\n")

		if c.progress.BytesPerSecond > 0 {
			b.WriteString(dimStyle.Render(i18n.T("commands.update.speed", c.progress.BytesPerSecond/1024/1024)) + "\n")
		}
		if c.progress.ETA > 0 {
			b.WriteString(dimStyle.Render(i18n.T("commands.update.eta", formatDuration(c.progress.ETA))) + "\n")
		}

	case modeReady:
		b.WriteString(titleStyle.Render(i18n.T("commands.update.ready_title")) + "\n\n")
		b.WriteString(textStyle.Render(i18n.T("commands.update.downloaded_verified")) + "\n\n")
		b.WriteString(warnStyle.Render(i18n.T("commands.update.restart_apply")) + "\n")
		b.WriteString(dimStyle.Render(i18n.T("commands.update.cancel_next_restart")))

	case modeError:
		b.WriteString(errorStyle.Render(i18n.T("commands.update.failed_title")) + "\n\n")
		b.WriteString(textStyle.Render("  "+c.error.Error()) + "\n\n")
		b.WriteString(dimStyle.Render(i18n.T("commands.update.close")))
	}

	return borderStyle.Render(b.String())
}

// IsInteractive returns true while the update panel is open
func (c *UpdateCommand) IsInteractive() bool {
	return c.visible
}

// formatDuration formats a duration for display
func formatDuration(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%dh %dm", seconds/3600, (seconds%3600)/60)
}

// GetCurrentVersion returns the current version for update checks
func GetCurrentVersion() string {
	return version.Version
}
