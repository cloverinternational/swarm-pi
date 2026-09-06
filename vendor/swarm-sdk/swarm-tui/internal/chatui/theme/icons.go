package theme

// ToolIcon returns the appropriate icon for a tool name.
func ToolIcon(toolName string) string {
	icons := map[string]string{
		// Core tools
		"Read":      "",
		"Edit":      "",
		"Write":     "",
		"Bash":      "",
		"Grep":      "",
		"Glob":      "",
		"LS":        "",
		"MultiEdit": "",

		// Web tools
		"WebFetch":  "",
		"WebSearch": "",

		// Task tools
		"Task":       "",
		"TaskManage": "",
		"TodoWrite":  "",

		// Git tools
		"GitStatus": "",
		"GitDiff":   "",
		"GitCommit": "",
		"GitPush":   "",
		"GitPull":   "",

		// MCP tools (generic)
		"mcp": "󰣆",

		// Notebook tools
		"NotebookRead": "",
		"NotebookEdit": "",

		// Agent tools
		"AskUserQuestion": "",
		"EnterPlanMode":   "",
		"ExitPlanMode":    "",
		"Skill":           "",
		"KillShell":       "",
		"TaskOutput":      "",
	}

	if icon, ok := icons[toolName]; ok {
		return icon
	}
	return "󰣆" // Default tool icon
}

// FileIcon returns the appropriate icon for a file extension.
func FileIcon(ext string) string {
	icons := map[string]string{
		// Languages
		"go":    "󰟓",
		"py":    "",
		"js":    "",
		"ts":    "",
		"tsx":   "",
		"jsx":   "",
		"rs":    "",
		"c":     "",
		"cpp":   "",
		"h":     "",
		"hpp":   "",
		"java":  "",
		"rb":    "",
		"php":   "",
		"swift": "",
		"kt":    "",
		"scala": "",
		"cs":    "",
		"lua":   "",
		"sh":    "",
		"bash":  "",
		"zsh":   "",
		"fish":  "",
		"ps1":   "",
		"r":     "",
		"sql":   "",

		// Config
		"json": "",
		"yaml": "",
		"yml":  "",
		"toml": "",
		"xml":  "",
		"ini":  "",
		"conf": "",
		"env":  "",

		// Markup
		"md":     "",
		"html":   "",
		"css":    "",
		"scss":   "",
		"sass":   "",
		"less":   "",
		"svelte": "",
		"vue":    "",

		// Data
		"csv": "",
		"txt": "",
		"log": "",

		// Binary
		"exe":   "",
		"dll":   "",
		"so":    "",
		"dylib": "",

		// Archive
		"zip": "",
		"tar": "",
		"gz":  "",
		"rar": "",

		// Images
		"png":  "",
		"jpg":  "",
		"jpeg": "",
		"gif":  "",
		"svg":  "",
		"ico":  "",
		"webp": "",

		// Docs
		"pdf":  "",
		"doc":  "",
		"docx": "",
		"xls":  "",
		"xlsx": "",
		"ppt":  "",
		"pptx": "",

		// Git
		"gitignore": "",
		"gitconfig": "",

		// Docker
		"dockerfile": "",

		// Lock files
		"lock": "",
	}

	if icon, ok := icons[ext]; ok {
		return icon
	}
	return "󰈙" // Default file icon
}

// StatusIcon returns the icon for a status string.
func StatusIcon(status string) string {
	icons := map[string]string{
		"success":   "✓",
		"error":     "✗",
		"warning":   "⚠",
		"info":      "ℹ",
		"running":   "●",
		"completed": "✓",
		"failed":    "✗",
		"cancelled": "○",
		"pending":   "○",
		"loading":   "◌",
	}

	if icon, ok := icons[status]; ok {
		return icon
	}
	return "●"
}

// BorderChars contains characters for drawing borders and connectors.
var BorderChars = struct {
	Vertical       string
	Horizontal     string
	TopLeft        string
	TopRight       string
	BottomLeft     string
	BottomRight    string
	VerticalRight  string
	VerticalLeft   string
	HorizontalDown string
	HorizontalUp   string
	Cross          string
	ThickVertical  string
	TreeBranch     string
	TreeCorner     string
	TreeVertical   string
}{
	Vertical:       "│",
	Horizontal:     "─",
	TopLeft:        "┌",
	TopRight:       "┐",
	BottomLeft:     "└",
	BottomRight:    "┘",
	VerticalRight:  "├",
	VerticalLeft:   "┤",
	HorizontalDown: "┬",
	HorizontalUp:   "┴",
	Cross:          "┼",
	ThickVertical:  "▌",
	TreeBranch:     "├",
	TreeCorner:     "└",
	TreeVertical:   "│",
}
