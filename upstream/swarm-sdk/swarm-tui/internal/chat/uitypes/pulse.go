package uitypes

import (
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// PulseTextConfig configures the pulsing text animation
type PulseTextConfig struct {
	// BaseColor is the text color at full intensity
	BaseColor string
	// DimColor is the text color at minimum intensity
	DimColor string
	// PulseSpeed controls the animation speed (higher = faster, default: 0.15)
	PulseSpeed float64
	// MinIntensity is the minimum opacity (0.0-1.0, default: 0.3)
	MinIntensity float64
	// MaxIntensity is the maximum opacity (0.0-1.0, default: 1.0)
	MaxIntensity float64
}

// DefaultPulseTextConfig returns sensible defaults for pulsing text
func DefaultPulseTextConfig() PulseTextConfig {
	return PulseTextConfig{
		BaseColor:    palette.Accent,
		DimColor:     palette.TextMuted,
		PulseSpeed:   0.15,
		MinIntensity: 0.3,
		MaxIntensity: 1.0,
	}
}

// CalculatePulseIntensity computes the current intensity using a sine wave
func CalculatePulseIntensity(frame int, speed, min, max float64) float64 {
	phase := float64(frame) * speed
	sine := math.Sin(phase)
	normalized := (sine + 1.0) / 2.0
	return min + (normalized * (max - min))
}

// RenderPulseText renders text with pulsing intensity based on animation frame
func RenderPulseText(text string, frame int, config PulseTextConfig) string {
	if text == "" {
		return ""
	}

	intensity := CalculatePulseIntensity(
		frame,
		config.PulseSpeed,
		config.MinIntensity,
		config.MaxIntensity,
	)

	var color string
	if intensity > 0.7 {
		color = config.BaseColor
	} else if intensity > 0.4 {
		color = config.DimColor
	} else {
		color = config.DimColor
	}

	style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	if intensity < 0.4 {
		style = style.Faint(true)
	}

	return style.Render(text)
}

// RenderPulseTextWithDots renders status text with animated trailing dots
func RenderPulseTextWithDots(status string, frame int, config PulseTextConfig) string {
	if status == "" {
		return ""
	}

	dotCycle := (frame / 8) % 4
	var dots strings.Builder
	for range dotCycle {
		dots.WriteString(".")
	}

	paddedDots := dots.String()
	for len(paddedDots) < 3 {
		paddedDots += " "
	}

	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(config.BaseColor))
	pulsingDots := RenderPulseText(paddedDots, frame, config)

	return statusStyle.Render(status) + pulsingDots
}
