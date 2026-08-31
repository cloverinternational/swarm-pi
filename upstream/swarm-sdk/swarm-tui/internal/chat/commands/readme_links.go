package commands

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/zone"
	"github.com/charmbracelet/x/ansi"
)

const (
	readmeLinkStart    = "\x1b]8;;"
	readmeLinkEnd      = "\x1b\\"
	maxReadmeURLLength = 2048
)

type readmeSegment struct {
	text string
	url  string
}

type readmeWord struct {
	text        string
	url         string
	spaceBefore bool
	style       readmeStyle
}

type readmeStyle struct {
	bold   bool
	italic bool
	code   bool
}

type readmeLinkRegistry struct {
	prefix  string
	nextID  int
	targets map[string]string
}

func newReadmeLinkRegistry(prefix string) *readmeLinkRegistry {
	return &readmeLinkRegistry{
		prefix:  prefix,
		nextID:  0,
		targets: make(map[string]string),
	}
}

func (r *readmeLinkRegistry) mark(label string, target string) string {
	if r == nil || label == "" || target == "" {
		return label
	}
	var id string = fmt.Sprintf("%s-%d", r.prefix, r.nextID)
	r.nextID++
	r.targets[id] = target
	return zone.Mark(id, label)
}

func WrapReadmeLines(text string, width int, baseURL string) []string {
	return wrapReadmeLinesWithRegistry(text, width, baseURL, nil)
}

func ReadmeLinkBaseURL(provider Provider) string {
	return readmeLinkBaseURL(provider)
}

func WrapReadmeLinesWithTargets(text string, width int, baseURL string, prefix string) ([]string, map[string]string) {
	var safePrefix string = strings.TrimSpace(prefix)
	if safePrefix == "" {
		safePrefix = "readme"
	}
	var registry *readmeLinkRegistry = newReadmeLinkRegistry(safePrefix)
	var lines []string = wrapReadmeLinesWithRegistry(text, width, baseURL, registry)
	if len(registry.targets) == 0 {
		return lines, nil
	}
	return lines, registry.targets
}

func wrapReadmeLinesWithRegistry(text string, width int, baseURL string, registry *readmeLinkRegistry) []string {
	var safeText string = sanitizeReadmeText(text)
	if width <= 0 {
		return []string{safeText}
	}

	var rawLines []string = strings.Split(safeText, "\n")
	var wrapped []string = make([]string, 0, len(rawLines))

	for _, line := range rawLines {
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}
		var segments []readmeSegment = parseReadmeSegments(line, baseURL)
		var words []readmeWord = segmentsToWords(segments)
		if len(words) == 0 {
			wrapped = append(wrapped, "")
			continue
		}
		wrapped = append(wrapped, wrapReadmeWords(words, width, registry)...)
	}

	return wrapped
}

func sanitizeReadmeText(text string) string {
	var stripped string = ansi.Strip(text)
	var builder strings.Builder
	builder.Grow(len(stripped))
	for _, r := range stripped {
		if r == '\n' || r == '\t' {
			builder.WriteRune(r)
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func parseReadmeSegments(line string, baseURL string) []readmeSegment {
	var segments []readmeSegment
	var plain strings.Builder

	var idx int
	for idx < len(line) {
		if line[idx] == '[' {
			labelEnd := findClosingBracket(line, idx+1)
			if labelEnd > idx+1 && labelEnd+1 < len(line) && line[labelEnd+1] == '(' {
				urlEnd := findClosingParen(line, labelEnd+2)
				if urlEnd > labelEnd+2 {
					label := line[idx+1 : labelEnd]
					urlText := line[labelEnd+2 : urlEnd]
					linkTarget := parseReadmeLinkDestination(urlText)
					safeLabel := sanitizeReadmeText(label)
					safeURL, ok := resolveReadmeURL(linkTarget, baseURL)
					if ok && safeLabel != "" {
						if plain.Len() > 0 {
							segments = append(segments, readmeSegment{text: plain.String()})
							plain.Reset()
						}
						segments = append(segments, readmeSegment{text: safeLabel, url: safeURL})
						idx = urlEnd + 1
						continue
					}
				}
			}
		}
		plain.WriteByte(line[idx])
		idx++
	}

	if plain.Len() > 0 {
		segments = append(segments, readmeSegment{text: plain.String()})
	}

	return segments
}

func parseReadmeLinkDestination(raw string) string {
	var cleaned string = strings.TrimSpace(raw)
	if cleaned == "" {
		return ""
	}
	if strings.HasPrefix(cleaned, "<") {
		end := strings.Index(cleaned, ">")
		if end == -1 {
			return ""
		}
		return strings.TrimSpace(cleaned[1:end])
	}
	for idx, r := range cleaned {
		if unicode.IsSpace(r) {
			return strings.TrimSpace(cleaned[:idx])
		}
	}
	return cleaned
}

func findClosingBracket(text string, start int) int {
	for idx := start; idx < len(text); idx++ {
		if text[idx] == ']' {
			return idx
		}
	}
	return -1
}

func findClosingParen(text string, start int) int {
	depth := 1
	for idx := start; idx < len(text); idx++ {
		switch text[idx] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return idx
			}
		}
	}
	return -1
}

func resolveReadmeURL(raw string, baseURL string) (string, bool) {
	var cleaned string = strings.TrimSpace(raw)
	cleaned = sanitizeReadmeText(cleaned)
	if cleaned == "" || len(cleaned) > maxReadmeURLLength {
		return "", false
	}
	if strings.ContainsAny(cleaned, " \t\r\n") {
		return "", false
	}
	parsed, err := url.Parse(cleaned)
	if err != nil {
		return "", false
	}
	if parsed.Scheme != "" {
		return sanitizeAbsoluteURL(parsed)
	}
	if parsed.Host != "" {
		return "", false
	}
	var base string = strings.TrimSpace(baseURL)
	if base == "" {
		return "", false
	}
	var baseParsed *url.URL
	baseParsed, err = url.Parse(base)
	if err != nil {
		return "", false
	}
	if !isAllowedReadmeScheme(baseParsed.Scheme) || baseParsed.Host == "" {
		return "", false
	}
	resolved := baseParsed.ResolveReference(parsed)
	return sanitizeAbsoluteURL(resolved)
}

func sanitizeAbsoluteURL(parsed *url.URL) (string, bool) {
	if parsed == nil {
		return "", false
	}
	if !isAllowedReadmeScheme(parsed.Scheme) {
		return "", false
	}
	if parsed.Host == "" {
		return "", false
	}
	var normalized string = parsed.String()
	if len(normalized) > maxReadmeURLLength {
		return "", false
	}
	return normalized, true
}

func isAllowedReadmeScheme(scheme string) bool {
	var normalized string = strings.ToLower(strings.TrimSpace(scheme))
	return normalized == "http" || normalized == "https"
}

func segmentsToWords(segments []readmeSegment) []readmeWord {
	var words []readmeWord
	var pendingSpace bool

	for _, segment := range segments {
		var segmentWords []readmeWord
		segmentWords, pendingSpace = parseInlineMarkdownWords(segment.text, segment.url, pendingSpace)
		words = append(words, segmentWords...)
	}

	return words
}

func parseInlineMarkdownWords(text string, url string, pendingSpace bool) ([]readmeWord, bool) {
	var words []readmeWord
	var current strings.Builder
	var currentSpaceBefore bool
	var style readmeStyle

	flushCurrent := func() {
		if current.Len() == 0 {
			return
		}
		words = append(words, readmeWord{
			text:        current.String(),
			url:         url,
			spaceBefore: currentSpaceBefore,
			style:       style,
		})
		current.Reset()
		currentSpaceBefore = false
	}

	var idx int
	for idx < len(text) {
		r, size := utf8.DecodeRuneInString(text[idx:])
		if r == utf8.RuneError && size == 1 {
			idx++
			continue
		}
		if unicode.IsSpace(r) {
			flushCurrent()
			pendingSpace = true
			idx += size
			continue
		}
		if r == '`' {
			flushCurrent()
			style.code = !style.code
			idx += size
			continue
		}
		if !style.code {
			if strings.HasPrefix(text[idx:], "**") {
				flushCurrent()
				style.bold = !style.bold
				idx += 2
				continue
			}
			if strings.HasPrefix(text[idx:], "__") {
				flushCurrent()
				style.bold = !style.bold
				idx += 2
				continue
			}
			if r == '*' && isItalicMarker(text, idx) {
				flushCurrent()
				style.italic = !style.italic
				idx += size
				continue
			}
			if r == '_' && !strings.HasPrefix(text[idx:], "__") && isItalicMarker(text, idx) {
				flushCurrent()
				style.italic = !style.italic
				idx += size
				continue
			}
		}

		if current.Len() == 0 {
			currentSpaceBefore = pendingSpace
			pendingSpace = false
		}
		current.WriteRune(r)
		idx += size
	}

	flushCurrent()
	return words, pendingSpace
}

func isItalicMarker(text string, idx int) bool {
	if strings.HasPrefix(text[idx:], "**") || strings.HasPrefix(text[idx:], "__") {
		return false
	}
	var prev rune
	var next rune
	var prevIsWord bool
	var nextIsWord bool

	if idx > 0 {
		prev, _ = utf8.DecodeLastRuneInString(text[:idx])
		prevIsWord = unicode.IsLetter(prev) || unicode.IsDigit(prev)
	}
	if idx+1 < len(text) {
		next, _ = utf8.DecodeRuneInString(text[idx+1:])
		nextIsWord = unicode.IsLetter(next) || unicode.IsDigit(next)
	}

	if prevIsWord && nextIsWord {
		return false
	}
	if !nextIsWord {
		return prevIsWord
	}
	return true
}

func wrapReadmeWords(words []readmeWord, width int, registry *readmeLinkRegistry) []string {
	if width <= 0 {
		return []string{""}
	}
	var lines []string
	var current strings.Builder
	var currentWidth int
	var lastURL string

	for _, word := range words {
		if word.text == "" {
			continue
		}
		wordWidth := ansi.StringWidth(word.text)
		if wordWidth == 0 {
			continue
		}
		spaceWidth := 0
		if word.spaceBefore && currentWidth > 0 {
			spaceWidth = 1
		}

		if wordWidth > width {
			if currentWidth > 0 {
				lines = append(lines, current.String())
				current.Reset()
				currentWidth = 0
				lastURL = ""
			}
			chunks := splitWordByWidth(word.text, width)
			for idx, chunk := range chunks {
				if chunk == "" {
					continue
				}
				rendered := renderReadmeWordChunk(chunk, word.style, word.url, registry)
				if idx < len(chunks)-1 {
					lines = append(lines, rendered)
				} else {
					current.WriteString(rendered)
					currentWidth = ansi.StringWidth(chunk)
				}
			}
			if currentWidth > 0 {
				lastURL = word.url
			}
			continue
		}

		if currentWidth > 0 && currentWidth+spaceWidth+wordWidth > width {
			lines = append(lines, current.String())
			current.Reset()
			currentWidth = 0
			spaceWidth = 0
			lastURL = ""
		}

		if spaceWidth > 0 {
			if word.url != "" && word.url == lastURL {
				current.WriteString(renderReadmeWordChunk(" ", word.style, word.url, registry))
			} else {
				current.WriteByte(' ')
			}
			currentWidth++
		}
		current.WriteString(renderReadmeWordChunk(word.text, word.style, word.url, registry))
		currentWidth += wordWidth
		lastURL = word.url
	}

	if currentWidth > 0 || current.Len() > 0 {
		lines = append(lines, current.String())
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func splitWordByWidth(word string, width int) []string {
	if width <= 0 {
		return []string{word}
	}
	var chunks []string
	var current strings.Builder
	var currentWidth int

	for _, r := range word {
		runeWidth := ansi.StringWidth(string(r))
		if currentWidth > 0 && currentWidth+runeWidth > width {
			chunks = append(chunks, current.String())
			current.Reset()
			currentWidth = 0
		}
		current.WriteRune(r)
		currentWidth += runeWidth
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	if len(chunks) == 0 {
		return []string{word}
	}
	return chunks
}

func renderReadmeLink(label string, target string) string {
	if label == "" || target == "" {
		return label
	}
	return readmeLinkStart + target + readmeLinkEnd + label + readmeLinkStart + readmeLinkEnd
}

func renderReadmeLinkWithRegistry(label string, target string, registry *readmeLinkRegistry) string {
	var rendered string = renderReadmeLink(label, target)
	if registry == nil || target == "" {
		return rendered
	}
	return registry.mark(rendered, target)
}

func renderReadmeWordChunk(text string, style readmeStyle, target string, registry *readmeLinkRegistry) string {
	var styled string = applyReadmeStyle(text, style, target != "")
	return renderReadmeLinkWithRegistry(styled, target, registry)
}

func applyReadmeStyle(text string, style readmeStyle, isLink bool) string {
	if text == "" {
		return text
	}
	if !style.bold && !style.italic && !style.code && !isLink {
		return text
	}
	var renderStyle lipgloss.Style = lipgloss.NewStyle()
	if style.code {
		renderStyle = renderStyle.
			Foreground(lipgloss.Color(ColorCyan)).
			Background(lipgloss.Color(ColorPanel))
	}
	if style.bold {
		renderStyle = renderStyle.Bold(true)
	}
	if style.italic {
		renderStyle = renderStyle.Italic(true)
	}
	if isLink {
		renderStyle = renderStyle.Underline(true)
		if !style.code {
			renderStyle = renderStyle.Foreground(lipgloss.Color(ColorBlue))
		}
	}
	return renderStyle.Render(text)
}

func readmeLinkBaseURL(provider Provider) string {
	var candidate string = originFromURL(provider.BaseURL)
	if candidate != "" {
		return candidate
	}
	var name string = strings.ToLower(strings.TrimSpace(provider.Name))
	var display string = strings.ToLower(strings.TrimSpace(provider.DisplayName))
	var apiType string = strings.ToLower(strings.TrimSpace(provider.APIType))
	if name == "openrouter" || display == "openrouter" || apiType == "openrouter" {
		return "https://openrouter.ai"
	}
	return ""
}

func originFromURL(raw string) string {
	var trimmed string = strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	if !isAllowedReadmeScheme(parsed.Scheme) || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

type ReadmeLinkOpenMsg struct {
	URL string
	Err string
}

func OpenReadmeLinkCmd(target string) tea.Cmd {
	return func() tea.Msg {
		err := openBrowser(target)
		if err != nil {
			return ReadmeLinkOpenMsg{URL: target, Err: err.Error()}
		}
		return ReadmeLinkOpenMsg{URL: target}
	}
}
