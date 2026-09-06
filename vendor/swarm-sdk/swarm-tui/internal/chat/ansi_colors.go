package chat

// ANSI true color escape codes - hardcoded to prevent terminal theme override
// These are shared constants used across multiple rendering systems
const (
	// Reset all attributes
	AnsiReset = "\x1b[0m"

	// True color backgrounds (24-bit) - these can't be overridden by terminal themes
	// Dark green background for additions: RGB(30, 60, 30)
	AnsiBgGreen = "\x1b[48;2;30;60;30m"
	// Dark red background for deletions: RGB(75, 30, 30) - more visible red
	AnsiBgRed = "\x1b[48;2;75;30;30m"

	// Foreground colors
	AnsiFgGreen  = "\x1b[38;2;166;227;161m" // Light green for + symbol
	AnsiFgRed    = "\x1b[38;2;243;139;168m" // Light red for - symbol
	AnsiFgDim    = "\x1b[38;2;140;140;140m" // Dim gray for line numbers
	AnsiFgWhite  = "\x1b[38;2;255;255;255m" // Bright white for code text
	AnsiFgMuted  = "\x1b[38;2;147;153;178m" // Muted for connectors
	AnsiFgBlue   = "\x1b[38;2;137;180;250m" // Blue for types/links
	AnsiFgYellow = "\x1b[38;2;249;226;175m" // Yellow for functions

	// Syntax highlighting colors (hardcoded ANSI)
	AnsiFgKeyword = "\x1b[38;2;203;166;247m" // Purple for keywords
	AnsiFgString  = "\x1b[38;2;166;227;161m" // Green for strings
	AnsiFgComment = "\x1b[38;2;108;112;134m" // Gray italic for comments
	AnsiFgNumber  = "\x1b[38;2;250;179;135m" // Orange for numbers
	AnsiFgType    = "\x1b[38;2;137;180;250m" // Blue for types
	AnsiFgFunc    = "\x1b[38;2;249;226;175m" // Yellow for functions

	// Text attributes
	AnsiItalic = "\x1b[3m" // Italic
	AnsiBold   = "\x1b[1m" // Bold
)

// Lowercase versions for backward compatibility with existing code
const (
	ansiReset     = AnsiReset
	ansiBgGreen   = AnsiBgGreen
	ansiBgRed     = AnsiBgRed
	ansiFgGreen   = AnsiFgGreen
	ansiFgRed     = AnsiFgRed
	ansiFgDim     = AnsiFgDim
	ansiFgWhite   = AnsiFgWhite
	ansiFgMuted   = AnsiFgMuted
	ansiFgBlue    = AnsiFgBlue
	ansiFgYellow  = AnsiFgYellow
	ansiFgKeyword = AnsiFgKeyword
	ansiFgString  = AnsiFgString
	ansiFgComment = AnsiFgComment
	ansiFgNumber  = AnsiFgNumber
	ansiFgType    = AnsiFgType
	ansiFgFunc    = AnsiFgFunc
	ansiItalic    = AnsiItalic
	ansiBold      = AnsiBold
)
