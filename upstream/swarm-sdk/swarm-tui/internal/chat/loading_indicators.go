// Package chat provides chat UI components
package chat

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// LoadingRefreshMsg is DEPRECATED - use AnimationTickMsg instead
type LoadingRefreshMsg struct{}

// LoadingIndicatorType represents different types of loading animations
type LoadingIndicatorType int

const (
	LoadingSpinner LoadingIndicatorType = iota
	LoadingDots
	LoadingGlitch
	LoadingPulse
	LoadingBar
)

// LoadingIndicator is a lightweight loading animation renderer
// It does NOT have its own goroutine - it uses the shared AnimationClock
type LoadingIndicator struct {
	indicatorType   LoadingIndicatorType
	indicatorTypeMu sync.RWMutex
	label           string
	labelMu         sync.RWMutex
	Width           int
	Theme           Theme

	isActive  bool
	activeMu  sync.RWMutex
	startTime time.Time

	// Determinate progress support (0.0-1.0). When hasProgress is false the
	// indicator falls back to its existing indeterminate rendering.
	progress    float64
	hasProgress bool
	progressMu  sync.RWMutex

	// Pre-created styles to avoid allocations per frame
	accentStyle  lipgloss.Style
	textStyle    lipgloss.Style
	mutedStyle   lipgloss.Style
	primaryStyle lipgloss.Style
	borderStyle  lipgloss.Style
	errorStyle   lipgloss.Style
	stylesInited bool

	// Render cache to skip identical frames
	lastFrame int
}

// NewLoadingIndicator creates a new loading indicator
func NewLoadingIndicator(indicatorType LoadingIndicatorType, label string, theme Theme) *LoadingIndicator {
	li := &LoadingIndicator{
		indicatorType: indicatorType,
		label:         label,
		Width:         40,
		Theme:         theme,
		lastFrame:     -1, // Invalid frame to force first render
	}
	li.initStyles()
	return li
}

// initStyles pre-creates all styles to avoid allocations during rendering
func (li *LoadingIndicator) initStyles() {
	if li.stylesInited {
		return
	}
	li.accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(li.Theme.Accent))
	li.textStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(li.Theme.Text))
	li.mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(li.Theme.TextMuted))
	li.primaryStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(li.Theme.Primary))
	li.borderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(li.Theme.Border))
	li.errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(li.Theme.Error)).Bold(true)
	li.stylesInited = true
}

// Start activates the loading indicator
// Returns a tea.Cmd to start the animation tick chain if this is the first subscriber
func (li *LoadingIndicator) Start(clock *AnimationClock) tea.Cmd {
	li.activeMu.Lock()
	wasActive := li.isActive
	li.isActive = true
	li.startTime = time.Now()
	li.activeMu.Unlock()

	if !wasActive && clock != nil {
		return clock.Subscribe()
	}
	return nil
}

// Stop deactivates the loading indicator
func (li *LoadingIndicator) Stop(clock *AnimationClock) {
	li.activeMu.Lock()
	wasActive := li.isActive
	li.isActive = false
	li.activeMu.Unlock()

	if wasActive && clock != nil {
		clock.Unsubscribe()
	}
}

// SetText updates the loading indicator's label text
func (li *LoadingIndicator) SetText(label string) {
	li.labelMu.Lock()
	li.label = label
	li.labelMu.Unlock()
}

// SetProgress sets a determinate progress percentage (0.0-1.0), clamping
// out-of-range values. Once set, LoadingBar indicators render a filled
// percentage bar instead of the indeterminate ping-pong animation.
func (li *LoadingIndicator) SetProgress(pct float64) {
	if pct < 0 {
		pct = 0
	} else if pct > 1 {
		pct = 1
	}
	li.progressMu.Lock()
	li.progress = pct
	li.hasProgress = true
	li.progressMu.Unlock()
}

// ClearProgress resets the indicator back to indeterminate mode, allowing
// the same LoadingIndicator to be reused across determinate/indeterminate
// phases (e.g. compacting vs. non-compacting states).
func (li *LoadingIndicator) ClearProgress() {
	li.progressMu.Lock()
	li.progress = 0
	li.hasProgress = false
	li.progressMu.Unlock()
}

// GetProgress returns the current progress percentage and whether a
// determinate percentage has been set via SetProgress.
func (li *LoadingIndicator) GetProgress() (pct float64, ok bool) {
	li.progressMu.RLock()
	defer li.progressMu.RUnlock()
	return li.progress, li.hasProgress
}

// IsActive returns whether the indicator is running
func (li *LoadingIndicator) IsActive() bool {
	li.activeMu.RLock()
	defer li.activeMu.RUnlock()
	return li.isActive
}

// View renders the current loading indicator state
func (li *LoadingIndicator) View(clock *AnimationClock) string {
	if !li.IsActive() {
		return ""
	}

	frame := 0
	if clock != nil {
		frame = clock.Frame()
	}

	switch li.GetIndicatorType() {
	case LoadingSpinner:
		return li.renderSpinner(frame)
	case LoadingDots:
		return li.renderDots(frame)
	case LoadingGlitch:
		return li.renderGlitch(frame)
	case LoadingPulse:
		return li.renderPulse(frame)
	case LoadingBar:
		if pct, ok := li.GetProgress(); ok {
			_ = pct
			return li.renderProgressBar(frame)
		}
		return li.renderBar(frame)
	default:
		return li.renderSpinner(frame)
	}
}

// ViewWithTime renders the indicator with elapsed time
func (li *LoadingIndicator) ViewWithTime(clock *AnimationClock) string {
	indicator := li.View(clock)
	elapsed := li.GetElapsedTimeString()

	if elapsed != "" {
		return fmt.Sprintf("%s %s", indicator, li.mutedStyle.Render(elapsed))
	}

	return indicator
}

// getLabel returns the current label
func (li *LoadingIndicator) getLabel() string {
	li.labelMu.RLock()
	defer li.labelMu.RUnlock()
	return li.label
}

// renderSpinner creates a rotating spinner
func (li *LoadingIndicator) renderSpinner(frame int) string {
	spinners := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinner := spinners[frame%len(spinners)]

	spinnerText := li.accentStyle.Render(spinner)

	label := li.getLabel()
	if label != "" {
		return fmt.Sprintf("%s %s", spinnerText, li.mutedStyle.Render(label))
	}

	return spinnerText
}

// renderDots creates animated dots with fixed width
func (li *LoadingIndicator) renderDots(frame int) string {
	const maxDots = 6
	dotCount := (frame / 6) % (maxDots + 1)
	dots := strings.Repeat("·", dotCount)
	padding := strings.Repeat(" ", maxDots-dotCount)

	label := li.getLabel()
	if label != "" {
		return fmt.Sprintf("%s%s%s", li.textStyle.Render(label), li.accentStyle.Render(dots), padding)
	}

	return li.accentStyle.Render(dots) + padding
}

// renderGlitch creates a glitch text effect
func (li *LoadingIndicator) renderGlitch(frame int) string {
	glitchChars := []rune{'█', '░', '▒', '▓', '▀', '▄', '▌', '▐', '■', '□', '▪', '▫'}

	baseText := li.getLabel()
	if baseText == "" {
		baseText = i18n.T("classic_chat_2.loading.processing_upper")
	}

	var glitchText strings.Builder
	glitchText.Grow(len(baseText) * 4) // Pre-allocate

	for i, char := range baseText {
		if (frame+i)%15 == 0 {
			glitchChar := glitchChars[(frame+i)%len(glitchChars)]
			glitchText.WriteString(li.errorStyle.Render(string(glitchChar)))
		} else if (frame+i)%7 == 0 {
			glitchText.WriteString(li.accentStyle.Render(string(char)))
		} else {
			glitchText.WriteString(li.textStyle.Render(string(char)))
		}
	}

	return glitchText.String()
}

// renderPulse creates a pulsing indicator
func (li *LoadingIndicator) renderPulse(frame int) string {
	intensity := (math.Sin(float64(frame)*0.2) + 1) / 2

	var pulseChar string
	var pulseStyle lipgloss.Style
	if intensity > 0.7 {
		pulseChar = "●"
		pulseStyle = li.accentStyle
	} else if intensity > 0.4 {
		pulseChar = "◐"
		pulseStyle = li.primaryStyle
	} else {
		pulseChar = "○"
		pulseStyle = li.mutedStyle
	}

	pulseText := pulseStyle.Render(pulseChar)

	label := li.getLabel()
	if label != "" {
		return fmt.Sprintf("%s %s", pulseText, li.textStyle.Render(label))
	}

	return pulseText
}

// renderBar creates an animated progress bar
func (li *LoadingIndicator) renderBar(frame int) string {
	barWidth := 20
	if li.Width > 0 {
		barWidth = li.Width
	}

	position := frame % (barWidth * 2)
	if position >= barWidth {
		position = barWidth*2 - position - 1
	}

	var bar strings.Builder
	bar.Grow(barWidth * 4) // Pre-allocate

	for i := 0; i < barWidth; i++ {
		dist := abs(i - position)
		if dist <= 1 {
			bar.WriteString(li.accentStyle.Render("█"))
		} else if dist <= 2 {
			bar.WriteString(li.primaryStyle.Render("▓"))
		} else {
			bar.WriteString(li.borderStyle.Render("░"))
		}
	}

	result := bar.String()
	label := li.getLabel()
	if label != "" {
		result = fmt.Sprintf("%s %s", li.textStyle.Render(label), result)
	}

	return result
}

// renderProgressBar creates a determinate left-to-right filled progress bar
// based on the percentage set via SetProgress, along with a trailing
// percentage label (e.g. " 42%").
func (li *LoadingIndicator) renderProgressBar(frame int) string {
	_ = frame // determinate render does not depend on animation frame

	barWidth := 20
	if li.Width > 0 {
		barWidth = li.Width
	}

	pct, _ := li.GetProgress()
	filled := int(math.Round(pct * float64(barWidth)))
	if filled < 0 {
		filled = 0
	} else if filled > barWidth {
		filled = barWidth
	}

	var bar strings.Builder
	bar.Grow(barWidth*4 + 8) // Pre-allocate

	if filled > 0 {
		bar.WriteString(li.accentStyle.Render(strings.Repeat("█", filled)))
	}
	if barWidth-filled > 0 {
		bar.WriteString(li.borderStyle.Render(strings.Repeat("░", barWidth-filled)))
	}

	percentLabel := fmt.Sprintf(" %3d%%", int(math.Round(pct*100)))
	result := bar.String() + li.mutedStyle.Render(percentLabel)

	label := li.getLabel()
	if label != "" {
		result = fmt.Sprintf("%s %s", li.textStyle.Render(label), result)
	}

	return result
}

// GetElapsedTime returns the time since the indicator started
func (li *LoadingIndicator) GetElapsedTime() time.Duration {
	if !li.IsActive() {
		return 0
	}
	li.activeMu.RLock()
	startTime := li.startTime
	li.activeMu.RUnlock()
	return time.Since(startTime)
}

// GetElapsedTimeString returns a formatted elapsed time string
func (li *LoadingIndicator) GetElapsedTimeString() string {
	elapsed := li.GetElapsedTime()
	if elapsed < time.Second {
		return ""
	}

	seconds := int(elapsed.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("(%ds)", seconds)
	}

	minutes := seconds / 60
	remainingSeconds := seconds % 60
	return fmt.Sprintf("(%dm%ds)", minutes, remainingSeconds)
}

// CycleIndicatorType cycles to the next loading indicator type
func (li *LoadingIndicator) CycleIndicatorType() {
	switch li.GetIndicatorType() {
	case LoadingSpinner:
		li.SetIndicatorType(LoadingDots)
		li.SetText(i18n.T("classic_chat_2.loading.agent_working"))
	case LoadingDots:
		li.SetIndicatorType(LoadingGlitch)
		li.SetText(i18n.T("classic_chat_2.loading.processing_upper"))
	case LoadingGlitch:
		li.SetIndicatorType(LoadingPulse)
		li.SetText(i18n.T("classic_chat_2.loading.agent_thinking"))
	case LoadingPulse:
		li.SetIndicatorType(LoadingBar)
		li.SetText(i18n.T("classic_chat_2.loading.processing"))
	case LoadingBar:
		li.SetIndicatorType(LoadingSpinner)
		li.SetText(i18n.T("classic_chat_2.loading.agent_thinking"))
	}
}

// SetIndicatorType changes the active animation style. Thread-safe: may be
// called from a background tea.Cmd goroutine (e.g. compaction progress
// reporting) while View() renders concurrently from the Update loop.
func (li *LoadingIndicator) SetIndicatorType(t LoadingIndicatorType) {
	li.indicatorTypeMu.Lock()
	li.indicatorType = t
	li.indicatorTypeMu.Unlock()
}

// GetIndicatorType returns the active animation style. Thread-safe.
func (li *LoadingIndicator) GetIndicatorType() LoadingIndicatorType {
	li.indicatorTypeMu.RLock()
	defer li.indicatorTypeMu.RUnlock()
	return li.indicatorType
}

// GetIndicatorTypeString returns the name of the current indicator type
func (li *LoadingIndicator) GetIndicatorTypeString() string {
	switch li.GetIndicatorType() {
	case LoadingSpinner:
		return i18n.T("classic_chat_2.loading.spinner")
	case LoadingDots:
		return i18n.T("classic_chat_2.loading.dots")
	case LoadingGlitch:
		return i18n.T("classic_chat_2.loading.glitch")
	case LoadingPulse:
		return i18n.T("classic_chat_2.loading.pulse")
	case LoadingBar:
		return i18n.T("classic_chat_2.loading.bar")
	default:
		return i18n.T("classic_chat_2.common.unknown")
	}
}

// Note: abs() function is already defined in app.go
