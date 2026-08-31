package chat

import (
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
)

// ============================================================================
// TEXT WRAPPING (for fixed-height rendering)
// ============================================================================

// wrapContentToWidth wraps content to fit within the specified width.
// lineWrapCache caches wrapped lines to avoid re-wrapping on scroll.
// Key is the uint64 line hash — no string allocation per lookup.
// The cache is invalidated on width change so width need not be in the key.
var lineWrapCache = struct {
	sync.RWMutex
	cache map[uint64][]string
	width int // Width the cache was built for; reset clears all entries
}{
	cache: make(map[uint64][]string),
}

// clearLineWrapCache clears the cache (call on width change)
func clearLineWrapCache(newWidth int) {
	lineWrapCache.Lock()
	defer lineWrapCache.Unlock()
	if lineWrapCache.width != newWidth {
		lineWrapCache.cache = make(map[uint64][]string)
		lineWrapCache.width = newWidth
	}
}

// wrapLinesToWidth wraps a slice of lines to fit within the specified width.
// Returns a new []string with wrapped lines. This is the core implementation —
// no join/split roundtrip. Callers that already have []string should prefer
// this over wrapContentToWidth to avoid two O(n) string allocations.
func wrapLinesToWidth(lines []string, width int) []string {
	if width <= 0 {
		result := make([]string, len(lines))
		copy(result, lines)
		return result
	}

	// Clear cache if width changed
	lineWrapCache.RLock()
	cacheWidth := lineWrapCache.width
	lineWrapCache.RUnlock()
	if cacheWidth != width {
		clearLineWrapCache(width)
	}

	result := make([]string, 0, len(lines))

	for _, line := range lines {
		lineHash := quickHash(line)

		lineWrapCache.RLock()
		cached, ok := lineWrapCache.cache[lineHash]
		lineWrapCache.RUnlock()

		if ok {
			result = append(result, cached...)
			continue
		}

		// Cache miss - wrap the line
		wrapped := wrapLineToWidth(line, width)

		lineWrapCache.Lock()
		lineWrapCache.cache[lineHash] = wrapped
		lineWrapCache.Unlock()

		result = append(result, wrapped...)
	}

	return result
}

// wrapLinesToWidthWithMapping wraps lines and returns both the wrapped result
// and a mapping from raw line index to cumulative wrapped line count.
// mapping[i] = total wrapped lines before raw line i (0-indexed within the input slice).
func wrapLinesToWidthWithMapping(lines []string, width int) ([]string, []int) {
	if width <= 0 {
		result := make([]string, len(lines))
		copy(result, lines)
		mapping := make([]int, len(lines)+1)
		for i := range lines {
			mapping[i+1] = mapping[i] + 1
		}
		return result, mapping
	}

	// Clear cache if width changed
	lineWrapCache.RLock()
	cacheWidth := lineWrapCache.width
	lineWrapCache.RUnlock()
	if cacheWidth != width {
		clearLineWrapCache(width)
	}

	result := make([]string, 0, len(lines)*2)
	mapping := make([]int, len(lines)+1)
	mapping[0] = 0

	for i, line := range lines {
		lineHash := quickHash(line)

		lineWrapCache.RLock()
		cached, ok := lineWrapCache.cache[lineHash]
		lineWrapCache.RUnlock()

		if !ok {
			cached = wrapLineToWidth(line, width)
			lineWrapCache.Lock()
			lineWrapCache.cache[lineHash] = cached
			lineWrapCache.Unlock()
		}

		result = append(result, cached...)
		mapping[i+1] = mapping[i] + len(cached)
	}

	return result, mapping
}

// wrapLineToWidth wraps a single line to the specified width.
// It uses word-aware wrapping (breaks at spaces) and preserves styles.
// Continuation lines get space-only indent (no repeated bullets).
// IMPORTANT: Styles are re-applied at the start of each continuation line.
func wrapLineToWidth(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}

	// Defensive: this function's contract is one line, and SetContent splits on
	// newlines before anything reaches here. A renderer that returns embedded
	// newlines inside a single "line" would otherwise be counted as one row
	// while the terminal paints several, desynchronising every line-index
	// calculation downstream. Splitting costs a Contains check on the fast path.
	if strings.Contains(line, "\n") {
		var out []string
		for _, segment := range strings.Split(line, "\n") {
			out = append(out, wrapLineToWidth(segment, width)...)
		}
		return out
	}

	// Reduce the line to SGR-only styling before measuring anything.
	//
	// Width accounting is only correct if this function and the ansi helpers it
	// calls agree on which bytes are invisible. They agree on SGR; they do not
	// reliably agree on cursor-movement, erase or OSC sequences, and a stray or
	// truncated ESC makes them disagree completely. When they disagree the
	// wrapper emits a line it believes fits and the terminal paints something
	// wider, which under-counts preWrappedMapping and breaks YOffset
	// arithmetic, scroll anchoring, hit-testing and image placement.
	//
	// Non-SGR escapes have no business in viewport content anyway: a cursor
	// movement embedded in a message would move the real cursor and corrupt the
	// frame. SanitizeANSI keeps colour and attributes and drops the rest, and
	// short-circuits on lines with no ESC at all, which is nearly all of them.
	// Hyperlinks are kept here (and only here): this is viewport content on its
	// way to the terminal, and links were inserted by Linkify after the tool
	// renderers already applied strict sanitization to their output.
	line = shared.SanitizeANSIKeepingHyperlinks(line)

	// Quick check: if line fits, return as-is
	visualWidth := lipgloss.Width(line)
	if visualWidth <= width {
		return []string{line}
	}

	// Detect indent for continuation lines (spaces only, no bullets)
	indent, indentWidth := detectContinuationIndent(line)

	// If indent is too wide, reduce it
	maxIndent := width / 4
	if indentWidth > maxIndent {
		indent = strings.Repeat(" ", maxIndent)
		indentWidth = maxIndent
	}

	// Extract words while preserving ANSI codes attached to them
	words := extractWordsWithStyles(line)
	if len(words) == 0 {
		// Whitespace-only (or styling-only) line. There is nothing to wrap, but
		// it can still be wider than the viewport: this codebase pads blank
		// lines and carries a background colour on them, so an over-wide blank
		// paints a visible block and the terminal wraps it into a phantom row
		// that preWrappedMapping knows nothing about. Blanks past the edge
		// carry no information, so clip instead of overflowing.
		return []string{ansi.Truncate(line, width, "")}
	}

	var result []string
	var currentLine strings.Builder
	currentWidth := 0
	isFirstLine := true

	// Track active ANSI styles to re-apply on continuation lines
	// We need to track ALL active sequences (foreground, background, bold, etc.)
	var activeStyle string
	var activeBgSeq string // Track background sequence separately
	// activeLink is the opening sequence of the hyperlink currently in effect,
	// or "" outside a link. A hyperlink must be closed at the end of every
	// physical line and reopened on the next one: left open it would claim
	// every cell painted afterwards, and simply dropped it would make the
	// wrapped remainder of a link unclickable. Reopening uses the same id, so
	// the terminal still treats the segments as one link and highlights them
	// together on hover.
	var activeLink string

	// lineOpen tracks whether the line being built currently has an OPEN link
	// on it. It is deliberately separate from activeLink: activeLink is the
	// logical state of the text, lineOpen is the physical state of this one
	// output line. Balancing every line individually is what keeps a link from
	// bleeding past its end, and it cannot be derived from activeLink alone
	// because ansi.TruncateLeft sometimes re-emits the link itself at a cut.
	lineOpen := false

	// noteLink folds a just-written fragment into both states.
	noteLink := func(fragment string) {
		if !strings.Contains(fragment, oscPrefix) {
			return
		}
		if open, sawClose := extractLinkState(fragment); sawClose {
			activeLink, lineOpen = "", false
		} else if open != "" {
			activeLink, lineOpen = open, true
		}
	}

	// ensureLinkOpen re-opens the active link on a fresh line, but only when the
	// fragment about to be written does not already carry its own opening
	// sequence — otherwise the line would hold two opens against one close.
	ensureLinkOpen := func(fragment string) {
		if activeLink == "" || lineOpen {
			return
		}
		// Only the FIRST sequence matters. A fragment that merely ENDS a link
		// ("text" + close) contains an OSC but does not open one, and still
		// needs the link reopened before its text — otherwise that text is
		// orphaned: visible, but no longer part of the link.
		if opensFirst, found := firstLinkSequenceIsOpen(fragment); found && opensFirst {
			return
		}
		currentLine.WriteString(activeLink)
		lineOpen = true
	}

	// Extract the initial background sequence from the line start
	// (set by reapplyBackground before wrapping)
	activeBgSeq = extractBackgroundSeq(line)

	for _, word := range words {
		wordWidth := lipgloss.Width(word.text)

		// Update active style from this word's ANSI codes
		if newStyle := extractLastStyle(word.raw); newStyle != "" {
			activeStyle = newStyle
		}
		// Track background sequences separately
		if bgSeq := extractBackgroundSeqFromWord(word.raw); bgSeq != "" {
			activeBgSeq = bgSeq
		}
		// NOTE: hyperlink state is deliberately NOT updated here. activeLink
		// must describe the state at the END of the text already on the line,
		// because the flush below decides whether to close a link before
		// breaking. Updating it from this word first would close the link one
		// word early and strand the word's own terminator on the next line.
		// It is updated after the word is written instead.

		// Calculate available width
		availableWidth := width
		if !isFirstLine {
			availableWidth = width - indentWidth
		}

		// Check if we need to wrap
		needsWrap := false
		if currentWidth > 0 {
			// Adding space + word
			if currentWidth+1+wordWidth > availableWidth {
				needsWrap = true
			}
		} else {
			// First word on line
			if wordWidth > availableWidth && !isFirstLine {
				// Word is too long, will need to break it
				needsWrap = false
			}
		}

		if needsWrap && currentLine.Len() > 0 {
			// Finish current line
			if lineOpen {
				currentLine.WriteString(linkClose)
				lineOpen = false
			}
			currentLine.WriteString("\x1b[0m")
			result = append(result, currentLine.String())
			currentLine.Reset()
			currentWidth = 0
			isFirstLine = false

			// Start new line: first apply background, then indent, then re-apply styles
			// This ensures the indent spaces have the correct background color
			if activeBgSeq != "" {
				currentLine.WriteString(activeBgSeq)
			}
			currentLine.WriteString(indent)
			if activeStyle != "" {
				currentLine.WriteString(activeStyle)
			}
			currentWidth = indentWidth
		}

		// Recompute available width for whatever line we're now on (the flush
		// above may have just moved us onto an indented continuation line).
		lineAvailable := width
		if !isFirstLine {
			lineAvailable = width - indentWidth
		}
		if lineAvailable < 1 {
			lineAvailable = 1
		}

		// HARD-BREAK oversized whitespace-free tokens. extractWordsWithStyles
		// splits only on spaces/tabs, so a single token with no whitespace at
		// all (e.g. compact/minified JSON — exactly what TaskManage's raw
		// tool-result blob looks like: `{"status":"succeeded","results":[...`
		// with zero spaces around structural characters) becomes ONE "word"
		// that can be many thousands of columns wide. The code below used to
		// special-case this ("Word is too long, will need to break it") but
		// never actually broke it — needsWrap was simply left false and the
		// whole oversized word was appended to a single result line further
		// down, producing a wrapped line whose width vastly exceeds `width`.
		// That corrupts every downstream assumption that preWrappedLines[i]
		// fits within `width` columns (YOffset arithmetic, mapping, scroll
		// anchoring, hit-testing) — this was reported live as "gigantic
		// [TaskManage] output ... treated as 1 line ... scroll is weird."
		//
		// STYLED tokens are broken too. They used to be exempted "rather than
		// risk splitting an ANSI sequence", but ansi.Hardwrap is ANSI-aware and
		// re-emits the active style on each continuation, so the risk it was
		// guarding against does not exist. The exemption did cause a real bug:
		// a Kitty image is drawn as a run of Unicode placeholder cells carrying
		// a foreground colour that encodes the image ID, and that run is one
		// styled, whitespace-free token. Left unbroken, an image wider than the
		// viewport produced a wrapped line wider than `width`, which under-counts
		// preWrappedMapping; publishTerminalImageFrame maps the anchor's raw line
		// through that mapping to test visibility, judged the placement
		// off-screen, and dropped it from the published frame. The cells stayed
		// on screen with no image behind them — reported as "the image is blank
		// but the space where it should be is there", most visibly after Ctrl+O,
		// which changes indentation and flips an image from fitting to not.
		if wordWidth > lineAvailable {
			// Consume the token a line at a time. A single Hardwrap of the whole
			// token cannot be used here: it would size every chunk for the FIRST
			// line's width, and the continuation lines carry an indent, so each
			// following chunk would overflow by exactly indentWidth.
			//
			// ansi.Truncate / ansi.TruncateLeft are grapheme- and ANSI-aware and
			// re-emit the styles still in effect at the cut, which is what keeps
			// a Kitty placeholder run bound to its image ID across the break.
			rest := word.raw
			for ansi.StringWidth(rest) > 0 {
				room := lineAvailable - currentWidth
				needSpace := currentWidth > 0 && (isFirstLine || currentWidth > indentWidth)
				if needSpace {
					room--
				}

				if room < 1 {
					// No usable room left: finish this line and continue on the next.
					if lineOpen {
						currentLine.WriteString(linkClose)
						lineOpen = false
					}
					currentLine.WriteString("\x1b[0m")
					result = append(result, currentLine.String())
					currentLine.Reset()
					isFirstLine = false

					if activeBgSeq != "" {
						currentLine.WriteString(activeBgSeq)
					}
					currentLine.WriteString(indent)
					if activeStyle != "" {
						currentLine.WriteString(activeStyle)
					}
					// No explicit link reopen here: ansi.TruncateLeft re-emits
					// the state in effect at the cut — hyperlink included — at
					// the head of the remaining text. Writing activeLink too
					// would open the link twice against a single close.
					currentWidth = indentWidth
					lineAvailable = width - indentWidth
					if lineAvailable < 1 {
						lineAvailable = 1
					}
					continue
				}

				chunk := ansi.Truncate(rest, room, "")
				chunkWidth := ansi.StringWidth(chunk)
				if chunkWidth == 0 {
					// The next grapheme is wider than the room available and
					// cannot be split (a two-column rune with one column left).
					// Give up rather than spin.
					break
				}
				if needSpace {
					currentLine.WriteString(" ")
					currentWidth++
				}
				currentLine.WriteString(chunk)
				currentWidth += chunkWidth
				noteLink(chunk)
				rest = ansi.TruncateLeft(rest, room, "")
			}
			continue
		}

		// Add space between words (except at line start)
		if currentWidth > 0 && (isFirstLine || currentWidth > indentWidth) {
			currentLine.WriteString(" ")
			currentWidth++
		}

		// Add the word (with its ANSI codes)
		ensureLinkOpen(word.raw)
		currentLine.WriteString(word.raw)
		currentWidth += wordWidth
		noteLink(word.raw)
	}

	// Don't forget the last line
	if currentLine.Len() > 0 {
		// A link left open at the end of the final line would claim whatever
		// the viewport paints next.
		if lineOpen {
			currentLine.WriteString(linkClose)
			lineOpen = false
		}
		result = append(result, currentLine.String())
	}

	if len(result) == 0 {
		return []string{""}
	}

	return result
}

// firstLinkSequenceIsOpen reports whether the first OSC 8 sequence in s opens a
// link (rather than closing one), and whether any was found at all.
func firstLinkSequenceIsOpen(s string) (isOpen, found bool) {
	idx := strings.Index(s, oscPrefix)
	if idx < 0 {
		return false, false
	}
	for j := idx + len(oscPrefix); j < len(s); j++ {
		if s[j] == '\x07' || (s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\') {
			payload := s[idx+len(oscPrefix) : j]
			semi := strings.IndexByte(payload, ';')
			// "params;" with an empty URI is the close sequence.
			return semi >= 0 && semi != len(payload)-1, true
		}
	}
	return false, false
}

// extractLinkState reports the hyperlink state left behind by a fragment.
//
// It returns the last OSC 8 opening sequence found, or sawClose=true when the
// fragment ends with a link terminator. The wrapper needs this because a link
// spans arbitrary text: to keep a wrapped link clickable on every physical
// line, each continuation must reopen whatever link was still in effect.
func extractLinkState(s string) (open string, sawClose bool) {
	i := 0
	for {
		idx := strings.Index(s[i:], oscPrefix)
		if idx < 0 {
			return open, sawClose
		}
		start := i + idx
		// Find the terminator: BEL or ST.
		end, width := -1, 0
		for j := start + len(oscPrefix); j < len(s); j++ {
			if s[j] == '\x07' {
				end, width = j, 1
				break
			}
			if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
				end, width = j, 2
				break
			}
		}
		if end < 0 {
			return open, sawClose // unterminated; ignore
		}
		payload := s[start+len(oscPrefix) : end]
		// A closing sequence carries an empty URI: "params;" with nothing after.
		if semi := strings.IndexByte(payload, ';'); semi == len(payload)-1 && semi >= 0 {
			open, sawClose = "", true
		} else if semi >= 0 {
			open, sawClose = s[start:end+width], false
		}
		i = end + width
	}
}

// extractLastStyle finds the last ANSI style sequence in a string.
// Returns empty string if no style found or if it ends with a reset.
func extractLastStyle(s string) string {
	var lastStyle string
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Found start of ANSI sequence
			start := i
			i += 2
			// Find the end 'm'
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				seq := s[start : i+1]
				// Check if this is a reset
				if seq == "\x1b[0m" || seq == "\x1b[m" {
					lastStyle = ""
				} else {
					lastStyle = seq
				}
				i++
			}
		} else {
			i++
		}
	}
	return lastStyle
}

// extractBackgroundSeq extracts the first background ANSI sequence from a string.
// Background sequences use SGR parameter 48 (e.g., \x1b[48;2;R;G;Bm for true color).
func extractBackgroundSeq(s string) string {
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			start := i
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				seq := s[start : i+1]
				// Check if this is a background color sequence (contains 48;)
				if isBackgroundSequence(seq) {
					return seq
				}
				// Reset clears background
				if seq == "\x1b[0m" || seq == "\x1b[m" {
					return ""
				}
				i++
			}
		} else {
			i++
		}
	}
	return ""
}

// extractBackgroundSeqFromWord extracts the last background sequence from a word's raw ANSI.
// Returns empty if the word ends with a reset or has no background sequence.
func extractBackgroundSeqFromWord(s string) string {
	var lastBg string
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			start := i
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				seq := s[start : i+1]
				if seq == "\x1b[0m" || seq == "\x1b[m" {
					lastBg = ""
				} else if isBackgroundSequence(seq) {
					lastBg = seq
				}
				i++
			}
		} else {
			i++
		}
	}
	return lastBg
}

// isBackgroundSequence checks if an ANSI sequence sets a background color.
// Matches: \x1b[48;...m (standard), \x1b[48;2;R;G;Bm (true color), \x1b[48;5;Nm (256 color),
// and \x1b[4X;...m where X is 0-7 (standard background colors 40-47).
func isBackgroundSequence(seq string) bool {
	if len(seq) < 5 {
		return false
	}
	// Strip \x1b[ prefix and m suffix
	inner := seq[2 : len(seq)-1]
	// Check for "48;" prefix (extended background) or standard 40-47 codes
	if len(inner) >= 3 && inner[:3] == "48;" {
		return true
	}
	// Check for standard background colors: 40-47, 100-107
	if len(inner) == 2 && inner[0] == '4' && inner[1] >= '0' && inner[1] <= '7' {
		return true
	}
	if len(inner) == 3 && inner[0] == '1' && inner[1] == '0' && inner[2] >= '0' && inner[2] <= '7' {
		return true
	}
	return false
}

// styledWord holds a word with its raw form (including ANSI) and display text
type styledWord struct {
	raw  string // Original with ANSI codes
	text string // Display text only (for width calculation)
}

// extractWordsWithStyles splits line into words, preserving ANSI codes with each word
func extractWordsWithStyles(line string) []styledWord {
	var words []styledWord
	var currentWord strings.Builder
	var currentText strings.Builder
	inEscape := false
	inWord := false

	for i := 0; i < len(line); i++ {
		c := line[i]

		// Handle ANSI escape sequences.
		//
		// This must mirror how a terminal (and ansi.StringWidth) consumes an
		// escape, not assume every ESC introduces a well-formed SGR ending in
		// 'm'. Tool output frequently contains a bare or truncated ESC; the old
		// code entered escape mode on any ESC and stayed there until it found an
		// 'm', so everything after a stray ESC was swallowed as "invisible"
		// escape bytes. Those bytes then measured as zero width while the
		// terminal still painted them, producing a wrapped line far wider than
		// the viewport — the same corruption of preWrappedMapping that hid
		// images, reachable from any tool that prints a raw ESC.
		if c == '\x1b' {
			currentWord.WriteByte(c)
			if i+1 < len(line) && line[i+1] == '[' {
				// CSI: parameters follow, terminated by a final byte.
				inEscape = true
			} else if i+1 < len(line) && line[i+1] == ']' {
				// OSC (used by terminal hyperlinks). The payload is arbitrary
				// text — a URL — and runs until BEL or ST, so it cannot be
				// treated as a two-byte escape without leaking the URL into
				// the visible line as literal characters.
				currentWord.WriteByte(line[i+1])
				i++
				for i+1 < len(line) {
					i++
					currentWord.WriteByte(line[i])
					if line[i] == '\x07' { // BEL terminator
						break
					}
					if line[i] == '\x1b' && i+1 < len(line) && line[i+1] == '\\' {
						i++
						currentWord.WriteByte(line[i]) // ST terminator
						break
					}
				}
			} else if i+1 < len(line) {
				// Two-byte escape (ESC + one byte); consume both.
				currentWord.WriteByte(line[i+1])
				i++
			}
			continue
		}
		if inEscape {
			currentWord.WriteByte(c)
			// A CSI sequence ends at its final byte, anywhere in @ through ~.
			// Terminating only on 'm' let a cursor-movement or erase sequence
			// run on until the next unrelated 'm' in ordinary text.
			if c >= 0x40 && c <= 0x7e {
				inEscape = false
			}
			continue
		}

		// Handle spaces (word boundaries)
		if c == ' ' || c == '\t' {
			if inWord {
				// End of word
				words = append(words, styledWord{
					raw:  currentWord.String(),
					text: currentText.String(),
				})
				currentWord.Reset()
				currentText.Reset()
				inWord = false
			}
			// Skip the space (we'll add spaces back when joining)
			continue
		}

		// Regular character - part of a word
		inWord = true
		currentWord.WriteByte(c)
		currentText.WriteByte(c)
	}

	// Don't forget last word
	if currentWord.Len() > 0 {
		words = append(words, styledWord{
			raw:  currentWord.String(),
			text: currentText.String(),
		})
	}

	return words
}

// detectContinuationIndent returns spaces-only indent for continuation lines.
// It measures the visual indent (including bullets) but returns only spaces.
func detectContinuationIndent(line string) (string, int) {
	indentWidth := 0
	inEscape := false

	for _, r := range line {
		// Skip ANSI sequences
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}

		// Count indent width
		if r == ' ' || r == '\t' {
			if r == '\t' {
				indentWidth += 4 // Tab = 4 spaces
			} else {
				indentWidth++
			}
		} else if r == '-' || r == '\u2022' || r == '\u25b8' || r == '\u25b9' || r == '\u2502' || r == '\u258c' || r == '\u2588' {
			// Bullet/border char - count it plus space after
			indentWidth += 2
			break
		} else {
			// Content starts
			break
		}
	}

	// Return spaces only (no bullets for continuation)
	return strings.Repeat(" ", indentWidth), indentWidth
}

// ============================================================================
// UNIFIED RENDERING FUNCTION
// ============================================================================

// wrappedContentResult holds the output of wrapping and padding
type wrappedContentResult struct {
	content   string // Final rendered content (wrapped and padded)
	lineCount int    // Number of lines in final content
	maxWidth  int    // Maximum visual width of any line
}

// wrapAndPadContent handles both wrapping and padding in one unified function.
// This ensures both fast and selection rendering paths produce identical output.
// Returns: wrapped+padded content, line count, max width, and hash for cache validation
func wrapAndPadContent(lines []string, yOffset, height, width int) wrappedContentResult {
	return wrapAndPadContentAnchored(lines, yOffset, height, width, false)
}

// wrapAndPadContentAnchored is wrapAndPadContent with control over which side is
// kept when the wrapped visible window exceeds height. When anchorBottom is true
// the LAST `height` wrapped lines are kept instead of the first; this is used by
// the synchronous fallback render while the user is pinned to the bottom during
// streaming, so the intermediate (pre-wrap) frame already shows the true bottom.
// Without this, the fallback shows the top of the last raw window and the async
// pre-wrap result then snaps the viewport down by the wrap-delta — a visible
// per-chunk bounce.
func wrapAndPadContentAnchored(lines []string, yOffset, height, width int, anchorBottom bool) wrappedContentResult {
	if width <= 0 {
		width = 1
	}
	if height <= 0 {
		height = 1
	}

	// Get visible lines
	top := yOffset
	if top < 0 {
		top = 0
	}
	bottom := yOffset + height
	// Clamp top to not exceed len(lines) - prevents slice bounds panic
	if top > len(lines) {
		top = len(lines)
	}
	if bottom > len(lines) {
		bottom = len(lines)
	}

	// Wrap visible lines directly — no join/split roundtrip.
	var contentLines []string
	if len(lines) == 0 {
		contentLines = []string{""}
	} else {
		visible := lines[top:bottom]
		contentLines = wrapLinesToWidth(visible, width)
	}

	// Calculate max width
	maxWidth := 0
	for _, line := range contentLines {
		w := ansi.StringWidth(line)
		if w > maxWidth {
			maxWidth = w
		}
	}

	// Pad to exact height
	if len(contentLines) < height {
		// Pad with empty lines
		padding := make([]string, height-len(contentLines))
		for i := range padding {
			padding[i] = ""
		}
		contentLines = append(contentLines, padding...)
	} else if len(contentLines) > height {
		if anchorBottom {
			// Keep the LAST height visual lines so a bottom-pinned viewport shows
			// the newest content immediately, matching the post-prewrap frame.
			contentLines = contentLines[len(contentLines)-height:]
		} else {
			// Truncate to height (keep top)
			contentLines = contentLines[:height]
		}
	}

	result := strings.Join(contentLines, "\n")

	return wrappedContentResult{
		content:   result,
		lineCount: len(contentLines),
		maxWidth:  maxWidth,
	}
}
