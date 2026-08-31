package chat

// CalculateResponsiveWidth applies responsive breakpoints to determine optimal content width.
// This function can be used by app, commands, and settings for consistent width calculations.
//
// Breakpoint Strategy:
// - Tiny terminals (< 60): Use full width minus padding (min 40)
// - Small terminals (60-80): Use full width for maximum utility
// - Medium terminals (80-120): Standard size, use full width
// - Large terminals (120-160): Use full width, optimal for most workflows
// - XL terminals (160-200): Use full width, great for side-by-side views
// - XXL terminals (200-280): Cap at 240 for comfortable reading
// - XXXL terminals (280-360): Cap at 280 to maintain text flow
// - Extreme terminals (360+): Cap at 320 for readability on ultrawide monitors
func CalculateResponsiveWidth(terminalWidth int) int {
	w := terminalWidth
	if w > 0 {
		w -= 4 // Account for typical padding
	}

	// Apply responsive breakpoints
	switch {
	case w < 40:
		return 40
	case w <= 60:
		return w
	case w <= 80:
		return w
	case w <= 120:
		return w
	case w <= 160:
		return w
	case w <= 200:
		return w
	case w <= 280:
		return 240
	case w <= 360:
		return 280
	default:
		return 320
	}
}
