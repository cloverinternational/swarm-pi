package rendering

import (
	"regexp"
	"strings"
)

// DetectContentType analyzes content and metadata to determine the best rendering type
func DetectContentType(content string, toolName string, metadata map[string]any) ContentType {
	// 1. Check metadata hints first
	if renderType, ok := metadata["render_type"].(string); ok {
		return parseRenderType(renderType)
	}

	// 2. Tool-specific detection
	switch toolName {
	case "bash":
		if hasANSI(content) {
			return ContentTypeANSI
		}
	case "file_read", "Read":
		// Check for language/extension in metadata
		if lang, ok := metadata["language"].(string); ok && lang != "" {
			return ContentTypeCode
		}
		if ext, ok := metadata["extension"].(string); ok {
			if detectFromExtension(ext) == ContentTypeCode {
				return ContentTypeCode
			}
		}
	case "grep", "Grep":
		// Grep output typically has ANSI colors
		return ContentTypeANSI
	case "git":
		// Git commands often have colored output
		if hasANSI(content) {
			return ContentTypeANSI
		}
	}

	// 3. Content-based detection
	return detectFromContent(content)
}

// parseRenderType converts a string to ContentType
func parseRenderType(renderType string) ContentType {
	switch strings.ToLower(renderType) {
	case "ansi":
		return ContentTypeANSI
	case "code":
		return ContentTypeCode
	case "json":
		return ContentTypeJSON
	case "yaml", "yml":
		return ContentTypeYAML
	case "xml":
		return ContentTypeXML
	case "diff":
		return ContentTypeDiff
	case "markdown", "md":
		return ContentTypeMarkdown
	default:
		return ContentTypePlainText
	}
}

// hasANSI checks if content contains ANSI escape codes
func hasANSI(content string) bool {
	// Look for common ANSI escape sequences
	ansiPattern := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiPattern.MatchString(content)
}

// detectFromExtension returns content type based on file extension
func detectFromExtension(ext string) ContentType {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")

	// Code file extensions
	codeExtensions := map[string]bool{
		"go": true, "py": true, "js": true, "ts": true, "jsx": true, "tsx": true,
		"java": true, "c": true, "cpp": true, "h": true, "hpp": true,
		"rs": true, "rb": true, "php": true, "swift": true, "kt": true,
		"cs": true, "sh": true, "bash": true, "zsh": true, "fish": true,
		"html": true, "css": true, "scss": true, "sass": true, "less": true,
		"sql": true, "r": true, "m": true, "scala": true, "clj": true,
		"vim": true, "lua": true, "pl": true, "tcl": true, "awk": true,
	}

	if codeExtensions[ext] {
		return ContentTypeCode
	}

	// Markup/Data formats
	switch ext {
	case "json":
		return ContentTypeJSON
	case "yaml", "yml":
		return ContentTypeYAML
	case "xml":
		return ContentTypeXML
	case "md", "markdown":
		return ContentTypeMarkdown
	case "diff", "patch":
		return ContentTypeDiff
	}

	return ContentTypePlainText
}

// detectFromContent analyzes the content itself to determine type
func detectFromContent(content string) ContentType {
	trimmed := strings.TrimSpace(content)

	// Check for JSON
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		// Simple JSON detection - could be improved
		if isLikelyJSON(trimmed) {
			return ContentTypeJSON
		}
	}

	// Check for YAML
	if isLikelyYAML(trimmed) {
		return ContentTypeYAML
	}

	// Check for diff
	if isLikelyDiff(trimmed) {
		return ContentTypeDiff
	}

	// Check for ANSI codes
	if hasANSI(content) {
		return ContentTypeANSI
	}

	// Default to plain text
	return ContentTypePlainText
}

// isLikelyJSON performs a simple check for JSON-like structure
func isLikelyJSON(content string) bool {
	// Look for common JSON patterns
	hasQuotedKeys := regexp.MustCompile(`"[^"]+"\s*:`).MatchString(content)
	hasCommas := strings.Contains(content, ",")
	return hasQuotedKeys || (hasCommas && (strings.HasPrefix(content, "{") || strings.HasPrefix(content, "[")))
}

// isLikelyYAML performs a simple check for YAML-like structure
func isLikelyYAML(content string) bool {
	lines := strings.Split(content, "\n")
	yamlKeyPattern := regexp.MustCompile(`^\s*[\w-]+:\s*`)

	matchCount := 0
	for _, line := range lines {
		if yamlKeyPattern.MatchString(line) {
			matchCount++
		}
	}

	// If more than 30% of lines look like YAML keys
	return matchCount > len(lines)/3
}

// isLikelyDiff performs a simple check for diff-like structure
func isLikelyDiff(content string) bool {
	lines := strings.Split(content, "\n")
	diffLineCount := 0

	for _, line := range lines {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") ||
			strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") ||
			strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "diff ") {
			diffLineCount++
		}
	}

	// If more than 20% of lines look like diff markers
	return diffLineCount > len(lines)/5
}

// ExtensionToLanguage maps file extensions to language names for syntax highlighting
func ExtensionToLanguage(ext string) string {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")

	languageMap := map[string]string{
		"go":    "go",
		"py":    "python",
		"js":    "javascript",
		"ts":    "typescript",
		"jsx":   "jsx",
		"tsx":   "tsx",
		"java":  "java",
		"c":     "c",
		"cpp":   "cpp",
		"h":     "c",
		"hpp":   "cpp",
		"rs":    "rust",
		"rb":    "ruby",
		"php":   "php",
		"swift": "swift",
		"kt":    "kotlin",
		"cs":    "csharp",
		"sh":    "bash",
		"bash":  "bash",
		"zsh":   "bash",
		"fish":  "fish",
		"html":  "html",
		"css":   "css",
		"scss":  "scss",
		"sql":   "sql",
		"r":     "r",
		"m":     "objective-c",
		"scala": "scala",
		"vim":   "vim",
		"lua":   "lua",
		"pl":    "perl",
		"json":  "json",
		"yaml":  "yaml",
		"yml":   "yaml",
		"xml":   "xml",
		"md":    "markdown",
		"diff":  "diff",
	}

	if lang, ok := languageMap[ext]; ok {
		return lang
	}

	return ""
}
