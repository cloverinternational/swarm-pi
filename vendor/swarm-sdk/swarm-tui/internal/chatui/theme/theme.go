// Package theme provides styling abstractions for the chat UI.
//
// The theme package separates styling concerns from rendering logic,
// allowing themes to be swapped without modifying renderers.
//
// Colors are matched to the main TUI palette for consistency.
package theme

// Theme defines the color palette and styling for the chat UI.
// All methods return color strings suitable for lipgloss.Color().
type Theme interface {
	// Role-based colors
	UserBorderColor() string
	AssistantBorderColor() string
	ThinkingColor() string

	// Semantic tool colors
	ToolNameColor() string
	ToolSuccessColor() string
	ToolErrorColor() string
	ToolWarningColor() string

	// File-type colors
	FilePathColor() string
	FileAddedColor() string
	FileRemovedColor() string
	FileModifiedColor() string

	// Base text colors
	TextColor() string
	TextDimColor() string
	TextMutedColor() string

	// Background colors
	BackgroundColor() string
	BackgroundLightColor() string
	BackgroundLighterColor() string

	// Message background colors
	UserMessageBackground() string
	UserMessageBackgroundHover() string
	AssistantMessageBackground() string
	MessageActionsBackground() string

	// Border and accent colors
	BorderColor() string
	PrimaryColor() string
	SecondaryColor() string
	AccentColor() string

	// Status colors
	SuccessColor() string
	WarningColor() string
	ErrorColor() string
	InfoColor() string
}

// Palette colors from internal/ui/palette/palette.go
// These MUST match exactly to ensure 1:1 visual consistency
const (
	paletteSurface   = "#0E1118"
	palettePanel     = "#151B24"
	palettePanelAlt  = "#1B2231"
	paletteBorder    = "#2A3140"
	paletteText      = "#FFFFFF"
	paletteTextDim   = "#8A93A6"
	paletteTextMuted = "#5C5C5C"

	paletteAccent     = "#6E64E8"
	paletteAccentDim  = "#3B3473"
	paletteAccentSoft = "#A78BFA"
	paletteTeal       = "#39D2C0"
	paletteInfo       = "#4C9AFF"

	paletteSuccess = "#4AD7A5"
	paletteWarning = "#F4C95D"
	paletteError   = "#FF5555"

	// Dark theme message backgrounds
	paletteUserMessageBg      = "#1F2028"
	paletteUserMessageBgHover = "#252530"
	paletteAssistantMessageBg = "#181920"
	paletteMessageActionsBg   = "#1A1E28"
)

// defaultTheme implements Theme with colors matching the main TUI palette.
type defaultTheme struct{}

// DefaultTheme returns the standard dark theme matching the TUI palette.
func DefaultTheme() Theme {
	return &defaultTheme{}
}

// User border uses Info color (blue) - matches main TUI
func (t *defaultTheme) UserBorderColor() string { return paletteInfo }

// Assistant border uses Success color (green) - matches main TUI
func (t *defaultTheme) AssistantBorderColor() string { return paletteSuccess }

// Thinking uses TextMuted (gray italic) - matches main TUI
func (t *defaultTheme) ThinkingColor() string { return paletteTextMuted }

// Tool colors
func (t *defaultTheme) ToolNameColor() string    { return paletteAccent }
func (t *defaultTheme) ToolSuccessColor() string { return paletteSuccess }
func (t *defaultTheme) ToolErrorColor() string   { return paletteError }
func (t *defaultTheme) ToolWarningColor() string { return paletteWarning }

// File colors
func (t *defaultTheme) FilePathColor() string     { return paletteInfo }
func (t *defaultTheme) FileAddedColor() string    { return paletteSuccess }
func (t *defaultTheme) FileRemovedColor() string  { return paletteError }
func (t *defaultTheme) FileModifiedColor() string { return paletteWarning }

// Text colors - exact match to TUI palette
func (t *defaultTheme) TextColor() string      { return paletteText }
func (t *defaultTheme) TextDimColor() string   { return paletteTextDim }
func (t *defaultTheme) TextMutedColor() string { return paletteTextMuted }

// Background colors - exact match to TUI palette
func (t *defaultTheme) BackgroundColor() string        { return paletteSurface }
func (t *defaultTheme) BackgroundLightColor() string   { return palettePanel }
func (t *defaultTheme) BackgroundLighterColor() string { return palettePanelAlt }

// Border and accent colors - exact match to TUI palette
func (t *defaultTheme) BorderColor() string    { return paletteBorder }
func (t *defaultTheme) PrimaryColor() string   { return paletteAccent }
func (t *defaultTheme) SecondaryColor() string { return paletteAccentSoft }
func (t *defaultTheme) AccentColor() string    { return paletteTeal }

// Status colors - exact match to TUI palette
func (t *defaultTheme) SuccessColor() string { return paletteSuccess }
func (t *defaultTheme) WarningColor() string { return paletteWarning }
func (t *defaultTheme) ErrorColor() string   { return paletteError }
func (t *defaultTheme) InfoColor() string    { return paletteInfo }

// Message background colors
func (t *defaultTheme) UserMessageBackground() string      { return paletteUserMessageBg }
func (t *defaultTheme) UserMessageBackgroundHover() string { return paletteUserMessageBgHover }
func (t *defaultTheme) AssistantMessageBackground() string { return paletteAssistantMessageBg }
func (t *defaultTheme) MessageActionsBackground() string   { return paletteMessageActionsBg }

// lightTheme implements Theme with a light color scheme.
type lightTheme struct{}

// LightTheme returns a light theme for terminals with light backgrounds.
func LightTheme() Theme {
	return &lightTheme{}
}

func (t *lightTheme) UserBorderColor() string      { return "#2563EB" }
func (t *lightTheme) AssistantBorderColor() string { return "#059669" }
func (t *lightTheme) ThinkingColor() string        { return "#9CA3AF" }

func (t *lightTheme) ToolNameColor() string    { return "#2563EB" }
func (t *lightTheme) ToolSuccessColor() string { return "#059669" }
func (t *lightTheme) ToolErrorColor() string   { return "#DC2626" }
func (t *lightTheme) ToolWarningColor() string { return "#D97706" }

func (t *lightTheme) FilePathColor() string     { return "#2563EB" }
func (t *lightTheme) FileAddedColor() string    { return "#059669" }
func (t *lightTheme) FileRemovedColor() string  { return "#DC2626" }
func (t *lightTheme) FileModifiedColor() string { return "#D97706" }

func (t *lightTheme) TextColor() string      { return "#1F2937" }
func (t *lightTheme) TextDimColor() string   { return "#6B7280" }
func (t *lightTheme) TextMutedColor() string { return "#9CA3AF" }

func (t *lightTheme) BackgroundColor() string        { return "#FFFFFF" }
func (t *lightTheme) BackgroundLightColor() string   { return "#F9FAFB" }
func (t *lightTheme) BackgroundLighterColor() string { return "#F3F4F6" }

func (t *lightTheme) BorderColor() string    { return "#E5E7EB" }
func (t *lightTheme) PrimaryColor() string   { return "#2563EB" }
func (t *lightTheme) SecondaryColor() string { return "#7C3AED" }
func (t *lightTheme) AccentColor() string    { return "#D97706" }

func (t *lightTheme) SuccessColor() string { return "#059669" }
func (t *lightTheme) WarningColor() string { return "#D97706" }
func (t *lightTheme) ErrorColor() string   { return "#DC2626" }
func (t *lightTheme) InfoColor() string    { return "#2563EB" }

// Message background colors - Claude Code-inspired for light terminals
func (t *lightTheme) UserMessageBackground() string      { return "#F0F0F0" }
func (t *lightTheme) UserMessageBackgroundHover() string { return "#FCFCFC" }
func (t *lightTheme) AssistantMessageBackground() string { return "#F8F8F8" }
func (t *lightTheme) MessageActionsBackground() string   { return "#E8ECF4" }
