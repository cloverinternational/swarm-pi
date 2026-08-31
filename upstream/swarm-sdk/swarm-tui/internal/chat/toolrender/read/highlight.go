package read

import (
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
)

// detectLanguageFromPath determines programming language from file extension.
// Returns a language identifier string used for keyword/type lookup.
func detectLanguageFromPath(filePath string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filePath), "."))

	langMap := map[string]string{
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
		"html":  "html",
		"htm":   "html",
		"css":   "css",
		"scss":  "scss",
		"json":  "json",
		"yaml":  "yaml",
		"yml":   "yaml",
		"toml":  "toml",
		"xml":   "xml",
		"md":    "markdown",
		"sql":   "sql",
	}

	if lang, ok := langMap[ext]; ok {
		return lang
	}

	// Check special filenames
	base := strings.ToLower(filepath.Base(filePath))
	if base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") {
		return "dockerfile"
	}
	if base == "makefile" || strings.HasPrefix(base, "makefile.") {
		return "makefile"
	}

	return "text"
}

// highlightLineANSI applies syntax highlighting to a line using hardcoded ANSI codes.
// Input is always raw text (no ANSI codes), so we truncate first then highlight.
func highlightLineANSI(line string, lang string, maxWidth int, bgColor string) string {
	// Truncate raw text FIRST (before any ANSI codes).
	//
	// This truncation MUST be width-aware. It used to compare len() (bytes)
	// against maxWidth (columns) and slice bytes, which broke three ways at
	// once: a CJK/emoji line was cut in the middle of a UTF-8 sequence and the
	// invalid bytes reached the frame; a wide rune consumed up to four "columns"
	// of budget so short lines were truncated far too early; and a line of wide
	// runes shorter in bytes than maxWidth was left at twice the pane width.
	//
	// Tabs are expanded before measuring: a tab is one column to every width
	// function but draws up to four on the terminal, shearing everything to its
	// right.
	displayLine := shared.ExpandTabsANSI(line)
	if shared.PrintableWidth(displayLine) > maxWidth {
		displayLine = shared.TruncateANSI(displayLine, maxWidth, "...")
	}

	// Check for comment first (highest priority)
	trimmed := strings.TrimSpace(displayLine)
	if isCommentLine(trimmed, lang) {
		commentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086")).Background(lipgloss.Color(bgColor)).Italic(true)
		return commentStyle.Render(displayLine)
	}

	// Apply highlighting to already-truncated line
	return highlightPatterns(displayLine, lang, bgColor)
}

// isCommentLine checks if a trimmed line is a comment for the given language.
func isCommentLine(trimmed string, lang string) bool {
	switch lang {
	case "go", "java", "javascript", "typescript", "c", "cpp", "rust", "swift", "kotlin":
		return strings.HasPrefix(trimmed, "//")
	case "python", "ruby", "bash", "shell", "yaml", "dockerfile", "makefile":
		return strings.HasPrefix(trimmed, "#")
	case "html", "xml":
		return strings.HasPrefix(trimmed, "<!--")
	case "css", "scss":
		return strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*")
	default:
		return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#")
	}
}

// highlightPatterns applies syntax highlighting patterns using lipgloss styles.
// Uses lipgloss instead of raw ANSI to avoid corruption when patterns match
// inside previously-added ANSI sequences.
func highlightPatterns(line string, lang string, bgColor string) string {
	// Define styles using lipgloss
	stringStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")).Background(lipgloss.Color(bgColor)).Background(lipgloss.Color(bgColor))  // Green
	numberStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")).Background(lipgloss.Color(bgColor)).Background(lipgloss.Color(bgColor))  // Orange
	keywordStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cba6f7")).Background(lipgloss.Color(bgColor)).Bold(true)                          // Purple bold
	typeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#89b4fa")).Background(lipgloss.Color(bgColor)).Background(lipgloss.Color(bgColor))    // Blue
	defaultStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4")).Background(lipgloss.Color(bgColor)).Background(lipgloss.Color(bgColor)) // White

	// Get language-specific keywords and types
	keywords := getKeywordsForLang(lang)
	types := getTypesForLang(lang)

	// Tokenize the line and apply styles to each token
	return tokenizeAndHighlight(line, keywords, types, stringStyle, numberStyle, keywordStyle, typeStyle, defaultStyle)
}

// tokenizeAndHighlight breaks a line into tokens and applies syntax highlighting.
// This is safer than regex replacement which can corrupt ANSI sequences.
// Coalesces consecutive same-style characters for efficiency.
func tokenizeAndHighlight(line string, keywords, types []string,
	stringStyle, numberStyle, keywordStyle, typeStyle, defaultStyle lipgloss.Style) string {

	if len(line) == 0 {
		return ""
	}

	// Build keyword and type sets for O(1) lookup
	keywordSet := make(map[string]bool)
	for _, kw := range keywords {
		keywordSet[kw] = true
	}
	typeSet := make(map[string]bool)
	for _, t := range types {
		typeSet[t] = true
	}

	var result strings.Builder
	var defaultChars strings.Builder // Accumulate consecutive default-style chars
	i := 0

	// Flush accumulated default chars
	flushDefault := func() {
		if defaultChars.Len() > 0 {
			result.WriteString(defaultStyle.Render(defaultChars.String()))
			defaultChars.Reset()
		}
	}

	for i < len(line) {
		ch := line[i]

		// Handle string literals
		if ch == '"' || ch == '\'' || ch == '`' {
			flushDefault()
			quote := ch
			start := i
			i++
			for i < len(line) && line[i] != quote {
				if line[i] == '\\' && i+1 < len(line) {
					i++ // Skip escaped character
				}
				i++
			}
			if i < len(line) {
				i++ // Include closing quote
			}
			result.WriteString(stringStyle.Render(line[start:i]))
			continue
		}

		// Handle numbers
		if ch >= '0' && ch <= '9' {
			flushDefault()
			start := i
			for i < len(line) && ((line[i] >= '0' && line[i] <= '9') || line[i] == '.') {
				i++
			}
			result.WriteString(numberStyle.Render(line[start:i]))
			continue
		}

		// Handle identifiers (keywords, types, variables)
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' {
			flushDefault()
			start := i
			for i < len(line) && ((line[i] >= 'a' && line[i] <= 'z') ||
				(line[i] >= 'A' && line[i] <= 'Z') ||
				(line[i] >= '0' && line[i] <= '9') || line[i] == '_') {
				i++
			}
			word := line[start:i]

			if keywordSet[word] {
				result.WriteString(keywordStyle.Render(word))
			} else if typeSet[word] {
				result.WriteString(typeStyle.Render(word))
			} else {
				result.WriteString(defaultStyle.Render(word))
			}
			continue
		}

		// Default: accumulate characters (spaces, punctuation, etc.)
		defaultChars.WriteByte(ch)
		i++
	}

	// Flush any remaining default chars
	flushDefault()

	return result.String()
}

// getKeywordsForLang returns language-specific keywords for syntax highlighting.
func getKeywordsForLang(lang string) []string {
	switch lang {
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
			"pass", "break", "continue", "lambda", "yield", "None", "True", "False", "self",
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
	case "bash", "shell", "dockerfile":
		return []string{
			"if", "then", "else", "elif", "fi", "for", "while", "do", "done", "case", "esac",
			"function", "return", "local", "export", "source", "echo", "read", "exit",
			"FROM", "RUN", "CMD", "COPY", "ADD", "ENV", "WORKDIR", "EXPOSE", "ENTRYPOINT",
		}
	case "html", "xml":
		return []string{} // HTML uses tags, not keywords
	case "css", "scss":
		return []string{
			"import", "media", "keyframes", "font-face", "supports", "charset",
		}
	default:
		return []string{
			"if", "else", "for", "while", "return", "function", "class", "import",
			"true", "false", "null", "nil",
		}
	}
}

// getTypesForLang returns language-specific type names for syntax highlighting.
func getTypesForLang(lang string) []string {
	switch lang {
	case "go":
		return []string{
			"string", "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64",
			"float32", "float64", "complex64", "complex128",
			"bool", "byte", "rune", "error", "any",
		}
	case "typescript":
		return []string{
			"string", "number", "boolean", "any", "void", "never", "unknown",
			"Array", "Promise", "Map", "Set", "Record", "Partial", "Required",
		}
	case "rust":
		return []string{
			"i8", "i16", "i32", "i64", "i128", "isize",
			"u8", "u16", "u32", "u64", "u128", "usize",
			"f32", "f64", "bool", "char", "str", "String",
			"Vec", "Option", "Result", "Box", "Rc", "Arc",
		}
	case "java", "kotlin":
		return []string{
			"int", "long", "short", "byte", "float", "double", "boolean", "char",
			"String", "Integer", "Long", "Double", "Boolean", "Object", "List", "Map", "Set",
		}
	case "c", "cpp":
		return []string{
			"int", "long", "short", "char", "float", "double", "void",
			"unsigned", "signed", "size_t", "bool",
		}
	default:
		return []string{}
	}
}
