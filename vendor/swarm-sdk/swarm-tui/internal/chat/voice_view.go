package chat

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/charmbracelet/lipgloss"
)

// Voice styles
var (
	voiceRecordingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FF6B6B")).
				Bold(true).
				Padding(0, 1)

	voiceTranscribingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFD93D")).
				Bold(true).
				Padding(0, 1)

	voiceIdleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6BCB77")).
			Padding(0, 1)

	voiceErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6B6B")).
			Padding(0, 1)

	voiceInterimStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#A0A0A0")).
				Italic(true).
				Padding(0, 1)

	voiceContainerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#444444")).
				Padding(0, 1).
				MarginTop(1)
)

// VoiceView renders the voice input UI
type VoiceView struct {
	state       VoiceState
	interimText string
	finalText   string
	lastError   error
	width       int
}

// NewVoiceView creates a new voice view
func NewVoiceView() *VoiceView {
	return &VoiceView{
		state: VoiceStateIdle,
	}
}

// Update updates the voice view state
func (v *VoiceView) Update(state VoiceState, interimText, finalText string, lastError error) {
	v.state = state
	v.interimText = interimText
	v.finalText = finalText
	v.lastError = lastError
}

// SetWidth sets the width of the voice view
func (v *VoiceView) SetWidth(width int) {
	v.width = width
}

// Render renders the voice view
func (v *VoiceView) Render() string {
	if v.state == VoiceStateIdle && v.interimText == "" && v.finalText == "" && v.lastError == nil {
		// Don't render anything if idle with no content
		return ""
	}

	var sections []string

	// Render status indicator
	statusSection := v.renderStatus()
	if statusSection != "" {
		sections = append(sections, statusSection)
	}

	// Render interim text
	if v.interimText != "" {
		interimSection := v.renderInterim()
		sections = append(sections, interimSection)
	}

	// Render error
	if v.lastError != nil {
		errorSection := v.renderError()
		sections = append(sections, errorSection)
	}

	if len(sections) == 0 {
		return ""
	}

	content := strings.Join(sections, "\n")
	return voiceContainerStyle.Render(content)
}

// renderStatus renders the status indicator
func (v *VoiceView) renderStatus() string {
	switch v.state {
	case VoiceStateConnecting:
		return voiceRecordingStyle.Render(i18n.T("chat_b.voice.connecting"))
	case VoiceStateRecording:
		// Animated indicator (will be animated by re-rendering)
		return voiceRecordingStyle.Render(i18n.T("chat_b.voice.recording_stop_hint"))
	case VoiceStateTranscribing:
		return voiceTranscribingStyle.Render(i18n.T("chat_b.voice.transcribing"))
	case VoiceStateError:
		return voiceErrorStyle.Render(i18n.T("chat_b.voice.error_title"))
	default:
		return ""
	}
}

// renderInterim renders the interim transcript text
func (v *VoiceView) renderInterim() string {
	if v.interimText == "" {
		return ""
	}

	// Truncate if too long
	text := v.interimText
	maxLen := v.width - 4 // Account for padding
	if maxLen > 0 && len(text) > maxLen {
		text = text[:maxLen-3] + "..."
	}

	return voiceInterimStyle.Render(fmt.Sprintf("\"%s\"", text))
}

// renderError renders the error message
func (v *VoiceView) renderError() string {
	if v.lastError == nil {
		return ""
	}
	return voiceErrorStyle.Render(i18n.T("chat_b.voice.error", v.lastError))
}

// VoiceHint renders a hint about voice command
func VoiceHint() string {
	return voiceIdleStyle.Render(i18n.T("chat_b.voice.command_hint"))
}

// VoicePrompt renders the voice recording prompt (replaces input when recording)
func (a *App) renderVoicePrompt() string {
	if !a.voiceRecording && !a.voiceTranscribing && a.voiceInterimText == "" {
		return ""
	}

	var prompt string

	if a.voiceRecording {
		prompt = voiceRecordingStyle.Render(i18n.T("chat_b.voice.listening"))
	} else if a.voiceTranscribing {
		prompt = voiceTranscribingStyle.Render(i18n.T("chat_b.voice.transcribing"))
	}

	// Show interim text if available
	if a.voiceInterimText != "" {
		interim := voiceInterimStyle.Render(fmt.Sprintf("  \"%s\"", a.voiceInterimText))
		prompt = prompt + "\n" + interim
	}

	return prompt
}

// IsVoiceActive returns true if voice is currently active
func (a *App) IsVoiceActive() bool {
	return a.voiceRecording || a.voiceTranscribing
}

// GetVoiceText returns the current voice text (interim or final)
func (a *App) GetVoiceText() string {
	if a.voiceInterimText != "" {
		return a.voiceInterimText
	}
	return a.voiceFinalText
}
