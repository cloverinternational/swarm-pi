// Package chat provides chat UI components
package chat

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// SpinnerType defines the animation style
type SpinnerType int

const (
	SpinnerTypeMatrix  SpinnerType = iota // Random hex chars with gradient (default)
	SpinnerTypeBraille                    // Classic braille dots
	SpinnerTypeBlocks                     // Block building animation
	SpinnerTypeWave                       // Wave pattern
	SpinnerTypePulse                      // Pulsing circles
	SpinnerTypeBounce                     // Bouncing ball
	SpinnerTypeAurora                     // Aurora borealis effect
	SpinnerTypeDNA                        // DNA helix pattern
)

// SpinnerTypeNames maps type to display name
var SpinnerTypeNames = map[SpinnerType]string{
	SpinnerTypeMatrix:  "Matrix",
	SpinnerTypeBraille: "Braille",
	SpinnerTypeBlocks:  "Blocks",
	SpinnerTypeWave:    "Wave",
	SpinnerTypePulse:   "Pulse",
	SpinnerTypeBounce:  "Bounce",
	SpinnerTypeAurora:  "Aurora",
	SpinnerTypeDNA:     "DNA Helix",
}

// SpinnerTypeDescriptions provides descriptions for each type
var SpinnerTypeDescriptions = map[SpinnerType]string{
	SpinnerTypeMatrix:  "Cyberpunk matrix-style random characters",
	SpinnerTypeBraille: "Classic minimalist braille dots spinner",
	SpinnerTypeBlocks:  "Building blocks loading animation",
	SpinnerTypeWave:    "Smooth wave pattern animation",
	SpinnerTypePulse:   "Pulsing circles with glow effect",
	SpinnerTypeBounce:  "Bouncing ball between walls",
	SpinnerTypeAurora:  "Northern lights color shimmer",
	SpinnerTypeDNA:     "Double helix DNA strand",
}

// GetSpinnerTypeFromString converts string to SpinnerType
func GetSpinnerTypeFromString(s string) SpinnerType {
	for t, name := range SpinnerTypeNames {
		if strings.EqualFold(name, s) {
			return t
		}
	}
	return SpinnerTypeBraille // Default: no random-letter Matrix fallback
}

// Spinner animation constants
const (
	spinnerCharCount  = 8
	ellipsisAnimSpeed = 4
	ellipsisMaxDots   = 3
)

// Spinner is a lightweight animation renderer
// It does NOT have its own goroutine or ticker - it uses the shared AnimationClock
//
// Usage:
//
//	spinner.Start(clock)     // Subscribe to clock, returns tea.Cmd if needed
//	spinner.View(clock)      // Pure render using clock.Frame()
//	spinner.Stop(clock)      // Unsubscribe from clock
type Spinner struct {
	spinnerType SpinnerType
	typeMu      sync.RWMutex

	label   string
	labelMu sync.RWMutex

	isActive  bool
	activeMu  sync.RWMutex
	startTime time.Time

	// Pre-created styles to avoid allocations per frame
	labelStyle lipgloss.Style
	timeStyle  lipgloss.Style

	// Render cache - skip if same frame and label
	lastFrame int
}

// NewSpinner creates a new spinner (lightweight, no goroutines)
// Default to Braille which has better terminal compatibility than Matrix
func NewSpinner() *Spinner {
	return &Spinner{
		spinnerType: SpinnerTypeBraille, // Braille has better terminal compat than Matrix
		labelStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)),
		timeStyle:   lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)),
		lastFrame:   -1, // Invalid frame to force first render
	}
}

// SetType changes the spinner animation type
func (s *Spinner) SetType(t SpinnerType) {
	s.typeMu.Lock()
	s.spinnerType = t
	s.typeMu.Unlock()
}

// GetType returns the current spinner type
func (s *Spinner) GetType() SpinnerType {
	s.typeMu.RLock()
	defer s.typeMu.RUnlock()
	return s.spinnerType
}

// SetLabel updates the spinner label
func (s *Spinner) SetLabel(label string) {
	s.labelMu.Lock()
	s.label = label
	s.labelMu.Unlock()
}

// IsActive returns whether the spinner should be displayed
func (s *Spinner) IsActive() bool {
	s.activeMu.RLock()
	defer s.activeMu.RUnlock()
	return s.isActive
}

// Start activates the spinner and subscribes to the animation clock.
// Returns a tea.Cmd if this call starts the clock's refresh chain. Callers that
// start spinner state from Update must return/batch this command; otherwise the
// spinner can become active without any AnimationTickMsg driving repaint.
func (s *Spinner) Start(clock *AnimationClock) tea.Cmd {
	s.activeMu.Lock()
	wasActive := s.isActive
	s.isActive = true
	s.startTime = time.Now()
	s.activeMu.Unlock()

	// Subscribe to clock if we weren't already active
	if !wasActive && clock != nil {
		return clock.Subscribe()
	}
	return nil
}

// Stop deactivates the spinner and unsubscribes from the animation clock
func (s *Spinner) Stop(clock *AnimationClock) {
	s.activeMu.Lock()
	wasActive := s.isActive
	s.isActive = false
	s.activeMu.Unlock()

	// Unsubscribe from clock if we were active
	if wasActive && clock != nil {
		clock.Unsubscribe()
	}
}

// View renders the spinner using the current frame from the clock
// This is a PURE RENDER FUNCTION - no side effects, no scheduling
func (s *Spinner) View(clock *AnimationClock) string {
	if !s.IsActive() {
		return ""
	}

	frame := 0
	if clock != nil {
		frame = clock.Frame()
	}

	s.typeMu.RLock()
	spinnerType := s.spinnerType
	s.typeMu.RUnlock()

	// Render animation frame
	animation := RenderSpinnerFrame(spinnerType, frame)

	var b strings.Builder
	b.Grow(100) // Pre-allocate
	b.WriteString(animation)

	// Add label with fixed-width ellipsis
	s.labelMu.RLock()
	label := s.label
	s.labelMu.RUnlock()

	if label != "" {
		b.WriteString(" ")
		b.WriteString(s.labelStyle.Render(label))

		ellipsisFrame := (frame / ellipsisAnimSpeed) % (ellipsisMaxDots + 1)
		dots := strings.Repeat(".", ellipsisFrame)
		padding := strings.Repeat(" ", ellipsisMaxDots-ellipsisFrame)
		b.WriteString(s.labelStyle.Render(dots + padding))
	}

	// Add elapsed time
	s.activeMu.RLock()
	startTime := s.startTime
	s.activeMu.RUnlock()

	elapsed := time.Since(startTime)
	if elapsed >= time.Second {
		timeStr := formatElapsedTime(elapsed)
		b.WriteString(" ")
		b.WriteString(s.timeStyle.Render(timeStr))
	}

	return b.String()
}

// ViewCompact renders a compact inline spinner for tool calls
func (s *Spinner) ViewCompact(clock *AnimationClock) string {
	if !s.IsActive() {
		return "●"
	}

	frame := 0
	if clock != nil {
		frame = clock.Frame()
	}

	braille := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return braille[frame%len(braille)]
}

// ============================================================================
// PURE RENDER FUNCTIONS - No state, no side effects
// Given a frame number and type, output a string
// ============================================================================

// RenderSpinnerFrame is a PURE FUNCTION that renders a spinner frame
// It has no side effects and doesn't modify any state
func RenderSpinnerFrame(spinnerType SpinnerType, frame int) string {
	switch spinnerType {
	case SpinnerTypeMatrix:
		return renderMatrix(frame)
	case SpinnerTypeBraille:
		return renderBraille(frame)
	case SpinnerTypeBlocks:
		return renderBlocks(frame)
	case SpinnerTypeWave:
		return renderWave(frame)
	case SpinnerTypePulse:
		return renderPulse(frame)
	case SpinnerTypeBounce:
		return renderBounce(frame)
	case SpinnerTypeAurora:
		return renderAurora(frame)
	case SpinnerTypeDNA:
		return renderDNA(frame)
	default:
		return renderMatrix(frame)
	}
}

// Gradient colors shared by animations
var gradientColors = []string{
	"#FF6B6B", "#FF8E72", "#FFB07A", "#FFD182",
	"#E8E46E", "#A8E6CF", "#88D8B0", "#4ECDC4",
}

func renderMatrix(frame int) string {
	chars := []rune("0123456789abcdefABCDEF~!@#$%^&*+=")
	var b strings.Builder
	for i := range spinnerCharCount {
		// Deterministic "random" based on frame and position
		charIdx := (frame*7 + i*13) % len(chars)
		colorIdx := i % len(gradientColors)
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color(gradientColors[colorIdx])).
			Bold(true)
		b.WriteString(style.Render(string(chars[charIdx])))
	}
	return b.String()
}

func renderBraille(frame int) string {
	braille := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#4ECDC4")).
		Bold(true)
	return style.Render(braille[frame%len(braille)])
}

func renderBlocks(frame int) string {
	blocks := []string{
		"▏", "▎", "▍", "▌", "▋", "▊", "▉", "█",
		"▉", "▊", "▋", "▌", "▍", "▎", "▏", " ",
	}
	var b strings.Builder
	for i := range spinnerCharCount {
		idx := (frame + i*2) % len(blocks)
		colorIdx := i % len(gradientColors)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(gradientColors[colorIdx]))
		b.WriteString(style.Render(blocks[idx]))
	}
	return b.String()
}

func renderWave(frame int) string {
	chars := []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	var b strings.Builder
	for i := range 10 {
		phase := float64(frame)/5.0 + float64(i)*0.5
		height := (math.Sin(phase) + 1) / 2
		idx := int(height * float64(len(chars)-1))
		colorIdx := i % len(gradientColors)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(gradientColors[colorIdx]))
		b.WriteString(style.Render(chars[idx]))
	}
	return b.String()
}

func renderPulse(frame int) string {
	circles := []string{"○", "◔", "◑", "◕", "●", "◕", "◑", "◔"}
	pulseColors := []string{"#555555", "#666666", "#888888", "#AAAAAA", "#4ECDC4", "#AAAAAA", "#888888", "#666666"}
	var b strings.Builder
	for i := range 5 {
		idx := (frame + i*2) % len(circles)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(pulseColors[idx]))
		b.WriteString(style.Render(circles[idx]))
	}
	return b.String()
}

func renderBounce(frame int) string {
	width := 12
	pos := frame % (width * 2)
	if pos >= width {
		pos = width*2 - pos - 1
	}
	var b strings.Builder
	for i := range width {
		var char string
		var color string
		if i == pos {
			char = "●"
			color = "#4ECDC4"
		} else if i == pos-1 || i == pos+1 {
			char = "○"
			color = "#2A9D8F"
		} else {
			char = "·"
			color = "#333333"
		}
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
		if i == pos {
			style = style.Bold(true)
		}
		b.WriteString(style.Render(char))
	}
	return b.String()
}

func renderAurora(frame int) string {
	chars := []string{"░", "▒", "▓", "█", "▓", "▒", "░", " "}
	auroraColors := []string{
		"#00FF87", "#00E676", "#1DE9B6", "#64FFDA",
		"#00BFA5", "#26A69A", "#4DB6AC", "#80CBC4",
	}
	var b strings.Builder
	for i := range 12 {
		charIdx := (frame/2 + i) % len(chars)
		colorIdx := (frame + i) % len(auroraColors)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(auroraColors[colorIdx]))
		b.WriteString(style.Render(chars[charIdx]))
	}
	return b.String()
}

func renderDNA(frame int) string {
	width := 12
	var b strings.Builder
	for i := range width {
		phase := float64(frame)/3.0 + float64(i)*0.5
		y1 := math.Sin(phase)
		y2 := math.Sin(phase + math.Pi)

		var char string
		var color string
		if y1 > y2 {
			if y1 > 0.3 {
				char = "◉"
				color = "#FF6B6B"
			} else if y1 > -0.3 {
				char = "─"
				color = "#666666"
			} else {
				char = "◎"
				color = "#4ECDC4"
			}
		} else {
			if y2 > 0.3 {
				char = "◎"
				color = "#4ECDC4"
			} else if y2 > -0.3 {
				char = "─"
				color = "#666666"
			} else {
				char = "◉"
				color = "#FF6B6B"
			}
		}
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
		b.WriteString(style.Render(char))
	}
	return b.String()
}

// formatElapsedTime formats duration as (XXs) or (Xm XXs)
func formatElapsedTime(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 60 {
		return "(" + padZero(seconds) + "s)"
	}
	minutes := seconds / 60
	secs := seconds % 60
	return "(" + strconv.Itoa(minutes) + "m " + padZero(secs) + "s)"
}

func padZero(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// ============================================================================
// LEGACY COMPATIBILITY - These will be removed after migration
// ============================================================================

// SpinnerRefreshMsg is DEPRECATED - use AnimationTickMsg instead
type SpinnerRefreshMsg struct{}

// SpinnerTickMsg is DEPRECATED
type SpinnerTickMsg struct {
}
