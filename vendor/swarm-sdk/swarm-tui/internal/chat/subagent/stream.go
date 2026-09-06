package subagent

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/uitypes"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// StreamConfig configures how streaming content is displayed and truncated
type StreamConfig struct {
	// MaxLinesCollapsed is the max lines to show when collapsed
	MaxLinesCollapsed int
	// MaxLinesExpanded is the max lines before truncation in expanded view
	MaxLinesExpanded int
	// TruncateIndicatorFormat is the format string for truncation (e.g., "... %d more lines")
	TruncateIndicatorFormat string
	// ShowStreamMarker shows animated marker while streaming
	ShowStreamMarker bool
	// StreamMarkerText is the text for the streaming marker (e.g., "streaming")
	StreamMarkerText string
	// TruncateIndicatorColor is the color for truncation indicators
	TruncateIndicatorColor string
	// StreamMarkerColor is the color for the streaming marker
	StreamMarkerColor string
}

// DefaultStreamConfig returns sensible defaults for streaming content
func DefaultStreamConfig() StreamConfig {
	return StreamConfig{
		MaxLinesCollapsed:       5,
		MaxLinesExpanded:        50,
		TruncateIndicatorFormat: "... %d more lines",
		ShowStreamMarker:        true,
		StreamMarkerText:        "streaming",
		TruncateIndicatorColor:  palette.TextMuted,
		StreamMarkerColor:       palette.Accent,
	}
}

// StreamState represents the current state of streaming content
type StreamState struct {
	// TotalLines is the total number of content lines
	TotalLines int
	// DisplayedLines is how many lines are currently shown
	DisplayedLines int
	// IsTruncated indicates if content is truncated
	IsTruncated bool
	// IsStreaming indicates if content is actively being received
	IsStreaming bool
	// IsExpanded indicates if the viewer is in expanded mode
	IsExpanded bool
}

// SubAgentStreamRenderer handles rendering of streaming sub-agent content
type SubAgentStreamRenderer struct {
	config StreamConfig
}

// NewSubAgentStreamRenderer creates a new stream renderer with given config
func NewSubAgentStreamRenderer(config StreamConfig) *SubAgentStreamRenderer {
	return &SubAgentStreamRenderer{config: config}
}

// NewDefaultSubAgentStreamRenderer creates a stream renderer with default config
func NewDefaultSubAgentStreamRenderer() *SubAgentStreamRenderer {
	return NewSubAgentStreamRenderer(DefaultStreamConfig())
}

// GetConfig returns the current configuration
func (r *SubAgentStreamRenderer) GetConfig() StreamConfig {
	return r.config
}

// SetConfig updates the configuration
func (r *SubAgentStreamRenderer) SetConfig(config StreamConfig) {
	r.config = config
}

// CalculateTruncation determines how many lines to display and if truncation is needed
// totalLines: total number of content lines
// isExpanded: whether the viewer is in expanded mode
// Returns: number of lines to display, whether truncation is needed
func (r *SubAgentStreamRenderer) CalculateTruncation(totalLines int, isExpanded bool) (displayLines int, truncated bool) {
	maxLines := r.config.MaxLinesCollapsed
	if isExpanded {
		maxLines = r.config.MaxLinesExpanded
	}

	if totalLines <= maxLines {
		return totalLines, false
	}
	return maxLines, true
}

// RenderStreamingContent renders content with truncation and optional streaming marker
// lines: the content lines to render
// state: current streaming state
// frame: animation frame for streaming marker
// Returns: rendered lines with truncation indicator and streaming marker as needed
func (r *SubAgentStreamRenderer) RenderStreamingContent(lines []string, state StreamState, frame int) []string {
	if len(lines) == 0 {
		if state.IsStreaming {
			// Show streaming marker even with no content yet
			return r.renderStreamMarker(frame)
		}
		return nil
	}

	// Calculate truncation
	maxLines := r.config.MaxLinesCollapsed
	if state.IsExpanded {
		maxLines = r.config.MaxLinesExpanded
	}

	var result []string

	// Truncate if needed
	if len(lines) > maxLines {
		result = append(result, lines[:maxLines]...)
		remaining := len(lines) - maxLines

		// Add truncation indicator
		indicatorStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(r.config.TruncateIndicatorColor)).
			Italic(true)
		indicator := fmt.Sprintf(r.config.TruncateIndicatorFormat, remaining)
		result = append(result, indicatorStyle.Render(indicator))
	} else {
		result = append(result, lines...)
	}

	// Add streaming marker if actively streaming
	if state.IsStreaming && r.config.ShowStreamMarker {
		markerLines := r.renderStreamMarker(frame)
		result = append(result, markerLines...)
	}

	return result
}

// renderStreamMarker creates the animated streaming indicator
func (r *SubAgentStreamRenderer) renderStreamMarker(frame int) []string {
	// Use pulsing text for the streaming marker
	pulseConfig := uitypes.PulseTextConfig{
		BaseColor:    r.config.StreamMarkerColor,
		DimColor:     palette.TextMuted,
		PulseSpeed:   0.15,
		MinIntensity: 0.3,
		MaxIntensity: 1.0,
	}

	marker := uitypes.RenderPulseTextWithDots(r.config.StreamMarkerText, frame, pulseConfig)
	return []string{"", marker}
}

// RenderTailContent renders the last N lines of content (tail-style)
// Useful for showing the most recent output during streaming
// lines: all content lines
// maxLines: maximum lines to show
// frame: animation frame for streaming marker
// isStreaming: whether content is actively streaming
// Returns: the last N lines with optional streaming marker
func (r *SubAgentStreamRenderer) RenderTailContent(lines []string, maxLines int, frame int, isStreaming bool) []string {
	if len(lines) == 0 {
		if isStreaming {
			return r.renderStreamMarker(frame)
		}
		return nil
	}

	var result []string

	// Show truncation indicator if there's more content above
	if len(lines) > maxLines {
		truncated := len(lines) - maxLines
		indicatorStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(r.config.TruncateIndicatorColor)).
			Italic(true)
		indicator := fmt.Sprintf("... %d lines above ...", truncated)
		result = append(result, indicatorStyle.Render(indicator))
		result = append(result, "")

		// Show only the last maxLines
		result = append(result, lines[len(lines)-maxLines:]...)
	} else {
		result = append(result, lines...)
	}

	// Add streaming marker
	if isStreaming && r.config.ShowStreamMarker {
		markerLines := r.renderStreamMarker(frame)
		result = append(result, markerLines...)
	}

	return result
}

// RenderCollapsedPreview renders a compact preview for collapsed state
// lines: all content lines
// maxChars: maximum characters for the preview
// frame: animation frame
// isStreaming: whether actively streaming
// Returns: single-line preview with optional streaming indicator
func (r *SubAgentStreamRenderer) RenderCollapsedPreview(lines []string, maxChars int, frame int, isStreaming bool) string {
	previewStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextMuted)).
		Italic(true)

	if len(lines) == 0 {
		if isStreaming {
			pulseConfig := uitypes.DefaultPulseTextConfig()
			return uitypes.RenderPulseTextWithDots("Starting", frame, pulseConfig)
		}
		return previewStyle.Render("(empty)")
	}

	// Get last non-empty line as preview
	var preview string
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			preview = trimmed
			break
		}
	}

	if preview == "" {
		preview = "(empty)"
	}

	// Truncate to maxChars
	if len(preview) > maxChars {
		preview = preview[:maxChars-3] + "..."
	}

	// Replace newlines with space
	preview = strings.ReplaceAll(preview, "\n", " ")

	if isStreaming {
		// Add pulsing indicator
		pulseConfig := uitypes.DefaultPulseTextConfig()
		pulsingDots := uitypes.RenderPulseText("...", frame, pulseConfig)
		return previewStyle.Render(preview) + " " + pulsingDots
	}

	return previewStyle.Render(preview)
}

// FormatStatusLine creates a status line for the sub-agent header
// status: status text (e.g., "Working", "Complete", "Error")
// toolCount: number of tools executed
// lineCount: total output lines
// isStreaming: whether actively streaming
// frame: animation frame
// Returns: formatted status line
func (r *SubAgentStreamRenderer) FormatStatusLine(status string, toolCount int, lineCount int, isStreaming bool, frame int) string {
	var parts []string

	// Status with optional pulse
	if isStreaming && status != "" {
		pulseConfig := uitypes.PulseTextConfig{
			BaseColor:    palette.Accent,
			DimColor:     palette.TextMuted,
			PulseSpeed:   0.15,
			MinIntensity: 0.4,
			MaxIntensity: 1.0,
		}
		parts = append(parts, uitypes.RenderPulseTextWithDots(status, frame, pulseConfig))
	} else if status != "" {
		statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted))
		parts = append(parts, statusStyle.Render(status))
	}

	// Metadata
	metaStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextMuted)).
		Faint(true)

	if toolCount > 0 {
		parts = append(parts, metaStyle.Render(fmt.Sprintf("[%d tools]", toolCount)))
	}
	if lineCount > 0 && !isStreaming {
		parts = append(parts, metaStyle.Render(fmt.Sprintf("[%d lines]", lineCount)))
	}

	return strings.Join(parts, " ")
}
