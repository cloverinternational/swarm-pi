package diffview

import (
	"path/filepath"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// SyntaxHighlighter provides syntax highlighting for code
// Currently uses simple regex-based highlighting; can be upgraded to chroma later
type SyntaxHighlighter struct {
	language string
	enabled  bool
	bgColor  string // Background color to preserve from diff styling
}

// NewSyntaxHighlighter creates a highlighter for the given file path
func NewSyntaxHighlighter(filePath string) *SyntaxHighlighter {
	return &SyntaxHighlighter{
		language: detectLanguage(filePath),
		enabled:  true,
		bgColor:  "",
	}
}

// SetEnabled enables or disables syntax highlighting
func (sh *SyntaxHighlighter) SetEnabled(enabled bool) *SyntaxHighlighter {
	sh.enabled = enabled
	return sh
}

// SetBackgroundColor sets the background color to preserve during highlighting
func (sh *SyntaxHighlighter) SetBackgroundColor(color string) *SyntaxHighlighter {
	sh.bgColor = color
	return sh
}

// Highlight applies syntax highlighting to a line of code
// The background color from diff styling is preserved
func (sh *SyntaxHighlighter) Highlight(content string, bgColor string) string {
	if !sh.enabled {
		return sh.applyBackground(content, bgColor)
	}

	// Apply language-specific highlighting
	highlighted := sh.highlightLine(content)

	// Apply background color if specified
	return sh.applyBackground(highlighted, bgColor)
}

// highlightLine applies syntax highlighting patterns to a line
func (sh *SyntaxHighlighter) highlightLine(line string) string {
	// Define syntax colors
	keywordColor := palette.AccentSoft // Purple for keywords
	stringColor := palette.Success     // Green for strings
	commentColor := palette.TextDim    // Dim for comments
	numberColor := palette.Teal        // Teal for numbers

	// Check for comments first (highest priority)
	if sh.isComment(line) {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(commentColor)).
			Italic(true).
			Render(line)
	}

	// Apply patterns in order of priority
	result := line

	// Highlight strings (simple pattern - doesn't handle escapes perfectly)
	stringPattern := regexp.MustCompile(`"[^"]*"|'[^']*'|` + "`[^`]*`")
	result = stringPattern.ReplaceAllStringFunc(result, func(match string) string {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(stringColor)).
			Render(match)
	})

	// Highlight numbers
	numberPattern := regexp.MustCompile(`\b\d+\.?\d*\b`)
	result = numberPattern.ReplaceAllStringFunc(result, func(match string) string {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(numberColor)).
			Render(match)
	})

	// Highlight keywords based on language
	keywords := sh.getKeywords()
	if len(keywords) > 0 {
		keywordPattern := regexp.MustCompile(`\b(` + strings.Join(keywords, "|") + `)\b`)
		result = keywordPattern.ReplaceAllStringFunc(result, func(match string) string {
			return lipgloss.NewStyle().
				Foreground(lipgloss.Color(keywordColor)).
				Bold(true).
				Render(match)
		})
	}

	return result
}

// isComment checks if the line is a comment
func (sh *SyntaxHighlighter) isComment(line string) bool {
	trimmed := strings.TrimSpace(line)

	switch sh.language {
	case "go", "java", "javascript", "typescript", "c", "cpp", "rust", "swift", "kotlin":
		return strings.HasPrefix(trimmed, "//")
	case "python", "ruby", "bash", "shell", "yaml":
		return strings.HasPrefix(trimmed, "#")
	case "html", "xml":
		return strings.HasPrefix(trimmed, "<!--")
	case "css", "scss":
		return strings.HasPrefix(trimmed, "/*")
	default:
		// Check common patterns
		return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#")
	}
}

// getKeywords returns language-specific keywords
func (sh *SyntaxHighlighter) getKeywords() []string {
	switch sh.language {
	case "go":
		return []string{
			"func", "return", "if", "else", "for", "range", "switch", "case", "default",
			"break", "continue", "package", "import", "type", "struct", "interface",
			"map", "chan", "const", "var", "defer", "go", "select", "nil", "true", "false",
		}
	case "python":
		return []string{
			"def", "return", "if", "elif", "else", "for", "while", "in", "not", "and", "or",
			"class", "import", "from", "as", "try", "except", "finally", "raise", "with",
			"pass", "break", "continue", "lambda", "yield", "None", "True", "False",
		}
	case "javascript", "typescript":
		return []string{
			"function", "return", "if", "else", "for", "while", "switch", "case", "default",
			"break", "continue", "const", "let", "var", "class", "extends", "import", "export",
			"from", "async", "await", "try", "catch", "finally", "throw", "new", "this",
			"null", "undefined", "true", "false",
		}
	case "rust":
		return []string{
			"fn", "return", "if", "else", "for", "while", "loop", "match", "struct", "enum",
			"impl", "trait", "use", "mod", "pub", "let", "mut", "const", "static",
			"async", "await", "move", "self", "Self", "true", "false",
		}
	case "java", "kotlin":
		return []string{
			"public", "private", "protected", "class", "interface", "extends", "implements",
			"return", "if", "else", "for", "while", "switch", "case", "default", "break",
			"continue", "new", "this", "super", "static", "final", "void", "null", "true", "false",
		}
	case "bash", "shell":
		return []string{
			"if", "then", "else", "elif", "fi", "for", "while", "do", "done", "case", "esac",
			"function", "return", "local", "export", "source", "echo", "read", "exit",
		}
	default:
		// Common keywords for most languages
		return []string{
			"if", "else", "for", "while", "return", "function", "class", "import",
			"true", "false", "null", "nil",
		}
	}
}

// applyBackground wraps content with a background color
func (sh *SyntaxHighlighter) applyBackground(content, bgColor string) string {
	if bgColor == "" {
		return content
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(bgColor)).
		Render(content)
}

// detectLanguage determines the language from file extension
func detectLanguage(filePath string) string {
	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
	ext = strings.ToLower(ext)

	languageMap := map[string]string{
		"go":    "go",
		"py":    "python",
		"js":    "javascript",
		"jsx":   "javascript",
		"ts":    "typescript",
		"tsx":   "typescript",
		"rs":    "rust",
		"java":  "java",
		"kt":    "kotlin",
		"c":     "c",
		"cpp":   "cpp",
		"cc":    "cpp",
		"h":     "c",
		"hpp":   "cpp",
		"swift": "swift",
		"rb":    "ruby",
		"php":   "php",
		"sh":    "bash",
		"bash":  "bash",
		"zsh":   "bash",
		"fish":  "fish",
		"html":  "html",
		"htm":   "html",
		"css":   "css",
		"scss":  "scss",
		"sass":  "sass",
		"less":  "less",
		"json":  "json",
		"yaml":  "yaml",
		"yml":   "yaml",
		"toml":  "toml",
		"xml":   "xml",
		"md":    "markdown",
		"sql":   "sql",
	}

	if lang, ok := languageMap[ext]; ok {
		return lang
	}

	// Check filename patterns
	base := strings.ToLower(filepath.Base(filePath))
	if base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") {
		return "dockerfile"
	}
	if base == "makefile" || strings.HasPrefix(base, "makefile.") {
		return "makefile"
	}

	return "text"
}

// GetLanguage returns the detected language
func (sh *SyntaxHighlighter) GetLanguage() string {
	return sh.language
}
