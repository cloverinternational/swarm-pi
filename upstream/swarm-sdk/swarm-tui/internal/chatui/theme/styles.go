package theme

import (
	"charm.land/lipgloss/v2"
)

// StyleSet provides pre-allocated lipgloss styles to avoid allocations during rendering.
// Create once at initialization and reuse on every frame.
type StyleSet struct {
	// Message borders
	UserBorder      lipgloss.Style
	AssistantBorder lipgloss.Style

	// Message backgrounds
	UserMessage      lipgloss.Style
	UserMessageHover lipgloss.Style
	AssistantMessage lipgloss.Style
	MessageActions   lipgloss.Style

	// User message header label ("You") — colored fg on user message bg
	UserLabel      lipgloss.Style
	UserLabelHover lipgloss.Style

	// Content styles
	Content       lipgloss.Style
	ContentDim    lipgloss.Style
	ContentMuted  lipgloss.Style
	ContentBold   lipgloss.Style
	ContentItalic lipgloss.Style

	// Thinking block
	Thinking       lipgloss.Style
	ThinkingHeader lipgloss.Style

	// Tool rendering
	ToolName    lipgloss.Style
	ToolSuccess lipgloss.Style
	ToolError   lipgloss.Style
	ToolWarning lipgloss.Style
	ToolOutput  lipgloss.Style

	// File paths
	FilePath     lipgloss.Style
	FileAdded    lipgloss.Style
	FileRemoved  lipgloss.Style
	FileModified lipgloss.Style

	// UI elements
	Border       lipgloss.Style
	Header       lipgloss.Style
	Hint         lipgloss.Style
	Timestamp    lipgloss.Style
	StatusActive lipgloss.Style
	StatusIdle   lipgloss.Style

	// Code blocks
	CodeBlock    lipgloss.Style
	CodeLanguage lipgloss.Style
	LineNumber   lipgloss.Style

	// Selection
	Selected lipgloss.Style
	Focused  lipgloss.Style
}

// NewStyleSet creates pre-allocated styles from a theme.
// This should be called once at initialization and when the theme changes.
func NewStyleSet(th Theme) *StyleSet {
	return &StyleSet{
		// Message borders
		UserBorder:      lipgloss.NewStyle().Foreground(lipgloss.Color(th.UserBorderColor())),
		AssistantBorder: lipgloss.NewStyle().Foreground(lipgloss.Color(th.AssistantBorderColor())),

		// Message backgrounds
		UserMessage: lipgloss.NewStyle().
			Background(lipgloss.Color(th.UserMessageBackground())).
			Foreground(lipgloss.Color(th.TextColor())),
		UserMessageHover: lipgloss.NewStyle().
			Background(lipgloss.Color(th.UserMessageBackgroundHover())).
			Foreground(lipgloss.Color(th.TextColor())),
		AssistantMessage: lipgloss.NewStyle().
			Background(lipgloss.Color(th.AssistantMessageBackground())).
			Foreground(lipgloss.Color(th.TextColor())),
		MessageActions: lipgloss.NewStyle().
			Background(lipgloss.Color(th.MessageActionsBackground())).
			Foreground(lipgloss.Color(th.TextColor())),

		// User message "You" label: accent fg on user message bg, bold
		UserLabel: lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.UserBorderColor())).
			Background(lipgloss.Color(th.UserMessageBackground())).
			Bold(true),
		UserLabelHover: lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.UserBorderColor())).
			Background(lipgloss.Color(th.UserMessageBackgroundHover())).
			Bold(true),

		// Content styles
		// NOTE: No Background() here. Backgrounds are applied at the message level
		// so the correct userMessageBackground or assistantMessageBackground shows
		// through. Baking a background here would override the outer container bg.
		Content:       lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextColor())),
		ContentDim:    lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDimColor())),
		ContentMuted:  lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMutedColor())),
		ContentBold:   lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextColor())).Bold(true),
		ContentItalic: lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextColor())).Italic(true),

		// Thinking block
		Thinking:       lipgloss.NewStyle().Foreground(lipgloss.Color(th.ThinkingColor())).Italic(true),
		ThinkingHeader: lipgloss.NewStyle().Foreground(lipgloss.Color(th.ThinkingColor())).Bold(true),

		// Tool rendering
		ToolName:    lipgloss.NewStyle().Foreground(lipgloss.Color(th.ToolNameColor())).Background(lipgloss.Color(th.BackgroundColor())).Bold(true),
		ToolSuccess: lipgloss.NewStyle().Foreground(lipgloss.Color(th.ToolSuccessColor())).Background(lipgloss.Color(th.BackgroundColor())),
		ToolError:   lipgloss.NewStyle().Foreground(lipgloss.Color(th.ToolErrorColor())).Background(lipgloss.Color(th.BackgroundColor())),
		ToolWarning: lipgloss.NewStyle().Foreground(lipgloss.Color(th.ToolWarningColor())).Background(lipgloss.Color(th.BackgroundColor())),
		ToolOutput:  lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDimColor())).Background(lipgloss.Color(th.BackgroundColor())),

		// File paths
		FilePath:     lipgloss.NewStyle().Foreground(lipgloss.Color(th.FilePathColor())).Background(lipgloss.Color(th.BackgroundColor())),
		FileAdded:    lipgloss.NewStyle().Foreground(lipgloss.Color(th.FileAddedColor())).Background(lipgloss.Color(th.BackgroundColor())),
		FileRemoved:  lipgloss.NewStyle().Foreground(lipgloss.Color(th.FileRemovedColor())).Background(lipgloss.Color(th.BackgroundColor())),
		FileModified: lipgloss.NewStyle().Foreground(lipgloss.Color(th.FileModifiedColor())).Background(lipgloss.Color(th.BackgroundColor())),

		// UI elements
		Border:       lipgloss.NewStyle().Foreground(lipgloss.Color(th.BorderColor())).Background(lipgloss.Color(th.BackgroundColor())),
		Header:       lipgloss.NewStyle().Foreground(lipgloss.Color(th.PrimaryColor())).Background(lipgloss.Color(th.BackgroundColor())).Bold(true),
		Hint:         lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMutedColor())).Background(lipgloss.Color(th.BackgroundColor())).Italic(true),
		Timestamp:    lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMutedColor())).Background(lipgloss.Color(th.BackgroundColor())),
		StatusActive: lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessColor())).Background(lipgloss.Color(th.BackgroundColor())),
		StatusIdle:   lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMutedColor())).Background(lipgloss.Color(th.BackgroundColor())),

		// Code blocks
		CodeBlock:    lipgloss.NewStyle().Background(lipgloss.Color(th.BackgroundLightColor())),
		CodeLanguage: lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMutedColor())).Background(lipgloss.Color(th.BackgroundLightColor())).Italic(true),
		LineNumber:   lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMutedColor())).Background(lipgloss.Color(th.BackgroundLightColor())),

		// Selection
		Selected: lipgloss.NewStyle().
			Background(lipgloss.Color(th.PrimaryColor())).
			Foreground(lipgloss.Color(th.BackgroundColor())),
		Focused: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(th.PrimaryColor())),
	}
}

// RoleStyle returns the appropriate border style for a message role.
func (s *StyleSet) RoleStyle(role string) lipgloss.Style {
	if role == "user" {
		return s.UserBorder
	}
	return s.AssistantBorder
}

// StatusStyle returns the appropriate status style.
func (s *StyleSet) StatusStyle(active bool) lipgloss.Style {
	if active {
		return s.StatusActive
	}
	return s.StatusIdle
}
