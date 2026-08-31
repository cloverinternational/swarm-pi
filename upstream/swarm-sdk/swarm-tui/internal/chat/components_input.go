package chat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// ============================================================================
// INPUT HISTORY (SwarmCode style)
// ============================================================================

// InputHistory manages command history for up/down arrow navigation.
//
// Persistence is scoped per working directory: prompts you sent in one
// workspace never surface when you press Up/Down in another. The workspace
// root supplied at construction selects which on-disk history file is used.
type InputHistory struct {
	items          []string // Stored inputs (newest first)
	currentIndex   int      // Position in history (0 = not browsing, 1+ = browsing)
	lastTypedInput string   // Saves current typing when browsing starts
	maxItems       int      // Limit history size
	workspaceRoot  string   // Working directory this history belongs to ("" = default)
	saveMu         sync.Mutex
	saveTimer      *time.Timer
}

// NewInputHistory creates a new input history manager scoped to workspaceRoot.
// Pass the current working directory so Up/Down recall only shows prompts that
// were sent from this directory. An empty workspaceRoot uses the default
// (unscoped) bucket.
func NewInputHistory(workspaceRoot string) *InputHistory {
	h := &InputHistory{
		items:         make([]string, 0),
		currentIndex:  0,
		maxItems:      1000, // Persist up to 1000 items across sessions
		workspaceRoot: workspaceRoot,
	}

	// Load persisted history for this workspace from disk
	if items, err := loadInputHistoryFromDisk(workspaceRoot); err == nil && len(items) > 0 {
		// Limit to maxItems in case file has more
		if len(items) > h.maxItems {
			items = items[:h.maxItems]
		}
		h.items = items
	}

	return h
}

// Add adds a new input to history
func (h *InputHistory) Add(input string) {
	// Don't add empty strings
	if strings.TrimSpace(input) == "" {
		return
	}

	// Don't add if same as most recent
	if len(h.items) > 0 && h.items[0] == input {
		return
	}

	// Prepend to front (newest first)
	h.items = append([]string{input}, h.items...)

	// Limit size
	if len(h.items) > h.maxItems {
		h.items = h.items[:h.maxItems]
	}

	// Reset index (not browsing anymore)
	h.currentIndex = 0
	h.lastTypedInput = ""

	// Debounced persist: coalesce rapid saves into one disk write.
	h.saveMu.Lock()
	if h.saveTimer != nil {
		h.saveTimer.Stop()
	}
	h.saveTimer = time.AfterFunc(2*time.Second, h.saveInputHistoryToDisk)
	h.saveMu.Unlock()
}

// NavigateUp navigates to older entries
func (h *InputHistory) NavigateUp(currentInput string) (string, bool) {
	// Can't go beyond history
	if h.currentIndex >= len(h.items) {
		return currentInput, false
	}

	// If starting to browse (index == 0), save current input
	if h.currentIndex == 0 && strings.TrimSpace(currentInput) != "" {
		h.lastTypedInput = currentInput
	}

	// Move to older entry
	h.currentIndex++
	return h.items[h.currentIndex-1], true
}

// NavigateDown navigates to newer entries
func (h *InputHistory) NavigateDown() (string, bool) {
	// Already at newest
	if h.currentIndex == 0 {
		return "", false
	}

	// Move to newer entry
	h.currentIndex--

	// If back to 0, restore last typed input
	if h.currentIndex == 0 {
		result := h.lastTypedInput
		h.lastTypedInput = ""
		return result, true
	}

	// Return item at new index
	return h.items[h.currentIndex-1], true
}

// Reset resets browsing state (after submission or typing)
func (h *InputHistory) Reset() {
	h.currentIndex = 0
	h.lastTypedInput = ""
}

// ============================================================================
// INPUT HISTORY PERSISTENCE
// ============================================================================

// inputHistoryData represents the JSON structure for persisted history
type inputHistoryData struct {
	Items []string `json:"items"`
}

// getInputHistoryPath returns the path to the per-workspace history file.
//
// History is partitioned by working directory so prompts sent in one project
// never leak into the Up/Down recall of another. The workspaceRoot is encoded
// into a filesystem-safe name; an empty root maps to the shared "__default__"
// bucket (used when no workspace is known).
func getInputHistoryPath(workspaceRoot string) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	name := encodeInputHistoryWorkspace(workspaceRoot)
	return filepath.Join(configDir, "swarmos", "input_history", name+".json"), nil
}

// encodeInputHistoryWorkspace converts a workspace path into a filesystem-safe,
// collision-free filename. Uses URL-safe base64 (no padding) so every distinct
// absolute path gets a distinct, reversible bucket. Empty paths share the
// "__default__" bucket.
func encodeInputHistoryWorkspace(workspaceRoot string) string {
	if workspaceRoot == "" {
		return "__default__"
	}
	return base64.RawURLEncoding.EncodeToString([]byte(workspaceRoot))
}

// loadInputHistoryFromDisk reads persisted history for a workspace from disk
func loadInputHistoryFromDisk(workspaceRoot string) ([]string, error) {
	path, err := getInputHistoryPath(workspaceRoot)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		// File doesn't exist on first run - not an error
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	var historyData inputHistoryData
	if err := json.Unmarshal(data, &historyData); err != nil {
		return nil, err
	}

	return historyData.Items, nil
}

// saveInputHistoryToDisk writes current history to disk
func (h *InputHistory) saveInputHistoryToDisk() {
	path, err := getInputHistoryPath(h.workspaceRoot)
	if err != nil {
		// Silent failure - history persistence is not critical
		return
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	// Marshal data
	historyData := inputHistoryData{
		Items: h.items,
	}
	data, err := json.MarshalIndent(historyData, "", "  ")
	if err != nil {
		return
	}

	// Write to disk (best-effort, ignore errors)
	_ = os.WriteFile(path, data, 0600)
}

// ============================================================================
// UTILITY FUNCTIONS
// ============================================================================

// stripANSI removes ANSI escape codes from a string
func stripANSI(s string) string {
	return ansiEscapeRE.ReplaceAllString(s, "")
}

// truncateToolOutput limits tool output to maxLines lines (Crush technique)

// ============================================================================
// SIMPLE INPUT COMPONENT (Bubbletea v2 compatible)
// ============================================================================

// maxUndoDepth is the maximum number of undo snapshots kept in memory.
// Each snapshot stores a (value, cursor) pair. 200 entries covers typical
// editing sessions without unbounded memory growth.
const maxUndoDepth = 200

// inputSnapshot is a point-in-time capture of the text and cursor position.
// Both fields must be stored together so a restore is always atomic and the
// cursor invariant (0 ≤ cursor ≤ len(value)) is preserved automatically.
type inputSnapshot struct {
	value  string
	cursor int
}

// SimpleInput is a basic text input component compatible with bubbletea v2
type SimpleInput struct {
	value        string
	cursor       int
	placeholder  string
	width        int
	height       int // Maximum height for multi-line display
	focused      bool
	scrollOffset int // For scrolling when content exceeds height

	// Paste state tracking (SwarmCode style) - supports multiple pastes
	pasteEntries []pasteEntry // List of all paste operations (indicator -> original text)
	pasteSeq     int          // Monotonic counter making each paste indicator unique

	// Image attachment support
	attachments  []ImageAttachment // Pasted images
	imageCounter int               // Counter for [Image N]

	// highlightTerms holds keyword strings from active skill triggers.
	// Words in the input that match any of these terms are highlighted
	// in the Warning colour during render.
	highlightTerms []string

	// Undo / redo stacks.
	//
	// undoStack holds snapshots of (value, cursor) taken BEFORE each text
	// mutation. Pressing Ctrl+Z pops from undoStack and pushes the current
	// state onto redoStack.
	//
	// redoStack holds states that were undone. Pressing Ctrl+Y pops from
	// redoStack. Any new text mutation clears redoStack (you cannot redo
	// once you have typed something new — the history has branched).
	undoStack []inputSnapshot
	redoStack []inputSnapshot

	// Word wrap cache to avoid re-wrapping the same text repeatedly.
	// Key is "text|width" string. This dramatically speeds up history
	// navigation which would otherwise re-wrap the same long inputs.
	wrapCache       map[string][]string
	wrapCacheWidth  int      // Width when cache was populated
	wrapCacheText   string   // Last text that was wrapped
	wrapCacheResult []string // Cached result for last text

	// Mouse text selection (see components_input_selection.go).
	// selAnchor/selFocus are BYTE offsets into value. originX/originY are the
	// screen cell of the input's first text column, set by the layout so mouse
	// coordinates can be mapped to byte offsets.
	selAnchor int
	selFocus  int
	selActive bool
	selecting bool
	originX   int
	originY   int
}

// ImageAttachment represents a pasted image
type ImageAttachment struct {
	ID          int    // Corresponds to [Image N]
	Data        []byte // PNG bytes
	MimeType    string // "image/png"
	Placeholder string // "[Image 1]"
	Size        int64  // File size in bytes
}

// pasteEntry tracks a single paste operation for multi-paste support
type pasteEntry struct {
	indicator string // Visual "[Pasted text +N lines]" shown to user
	original  string // Original pasted content
}

// ============================================================================
// PASTE HELPER FUNCTIONS
// ============================================================================

// getPastedTextPrompt generates the visual indicator for pasted text.
// The seq argument makes each indicator unique so that GetSubmitValue can
// unambiguously map an indicator back to its original content even when two
// pastes have identical size. The format surfaces the line count so the user
// knows how much was captured.
func getPastedTextPrompt(text string, seq int) string {
	lineCount := strings.Count(text, "\n") + 1
	messageID := "classic_chat.input.pasted_lines"
	if lineCount == 1 {
		messageID = "classic_chat.input.pasted_line"
	}
	return i18n.T(messageID, seq, formatCount(lineCount))
}

// formatCount renders an integer with thousands separators (e.g. 12345 ->
// "12,345") so large paste sizes are readable at a glance.
func formatCount(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for idx := pre; idx < len(s); idx += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[idx : idx+3])
	}
	return b.String()
}

// pasteIndicatorRe matches the visual paste chip "[#N Pasted ...]" so it can be
// styled distinctly inside the input. The cursor glyph "█" may appear anywhere
// in the line; it is preserved because the regex only targets the chip text.
var pasteIndicatorRe = regexp.MustCompile(`\[#\d+ [^\]]*\]`)

// stylePasteIndicators wraps every paste chip in a distinct style so it reads
// as an attachment rather than literal text the user typed.
func stylePasteIndicators(line string) string {
	if line == "" || !pasteIndicatorRe.MatchString(line) {
		return line
	}
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.AccentSoft)).
		Bold(true)
	return pasteIndicatorRe.ReplaceAllStringFunc(line, func(m string) string {
		// If the cursor glyph is inside the chip, render around it so the
		// cursor stays visible and correctly positioned.
		const cursorChar = "█"
		if strings.Contains(m, cursorChar) {
			parts := strings.SplitN(m, cursorChar, 2)
			return style.Render(parts[0]) + cursorChar + style.Render(parts[1])
		}
		return style.Render(m)
	})
}

// ============================================================================
// SIMPLE INPUT METHODS
// ============================================================================

// NewSimpleInput creates a new simple input

func NewSimpleInput() *SimpleInput {
	return &SimpleInput{
		value:        "",
		cursor:       0,
		placeholder:  i18n.T("classic_chat.input.placeholder"),
		width:        80,
		height:       3, // Default to 3 lines
		focused:      true,
		scrollOffset: 0,
		pasteEntries: nil,
		wrapCache:    make(map[string][]string),
	}
}

// AddImageAttachment adds a pasted image and returns its placeholder
func (i *SimpleInput) AddImageAttachment(imageData []byte, mimeType string) string {
	i.imageCounter++
	placeholder := fmt.Sprintf("[Image %d]", i.imageCounter)

	att := ImageAttachment{
		ID:          i.imageCounter,
		Data:        imageData,
		MimeType:    mimeType,
		Placeholder: placeholder,
		Size:        int64(len(imageData)),
	}

	i.attachments = append(i.attachments, att)
	logDebug("✓ Added image attachment %d (%d bytes)", i.imageCounter, len(imageData))
	return placeholder
}

// GetAttachments returns all image attachments
func (i *SimpleInput) GetAttachments() []ImageAttachment {
	return i.attachments
}

// ClearAttachments removes all image attachments
func (i *SimpleInput) ClearAttachments() {
	i.attachments = nil
	// Note: Don't reset imageCounter - it continues incrementing within the session
	logDebug("✓ Cleared all image attachments")
}

// HasAttachments returns true if there are any image attachments
func (i *SimpleInput) HasAttachments() bool {
	return len(i.attachments) > 0
}

// GetAttachmentCount returns the number of image attachments
func (i *SimpleInput) GetAttachmentCount() int {
	return len(i.attachments)
}

// InsertImagePlaceholder inserts an image placeholder at cursor position
func (i *SimpleInput) InsertImagePlaceholder(placeholder string) {
	// Insert placeholder at cursor
	i.value = i.value[:i.cursor] + placeholder + i.value[i.cursor:]
	i.cursor += len(placeholder)
}

// ============================================================================
// UNDO / REDO
// ============================================================================

// pushUndo captures the current (value, cursor) into undoStack before a text
// mutation is about to happen, and clears redoStack.
//
// Rules (lowest level):
//   - Call this BEFORE mutating i.value or i.cursor in any editing operation.
//   - Do NOT call this for pure cursor movement (no text change).
//   - Do NOT call this from SetValue (programmatic changes bypass undo history).
//   - This always clears redoStack — once you type something new you cannot
//     redo past it (history has branched).
func (i *SimpleInput) pushUndo() {
	snap := inputSnapshot{value: i.value, cursor: i.cursor}
	i.undoStack = append(i.undoStack, snap)
	// Enforce max depth: drop the oldest snapshot (index 0) when over limit.
	// We keep the most-recent maxUndoDepth snapshots.
	if len(i.undoStack) > maxUndoDepth {
		i.undoStack = i.undoStack[1:]
	}
	// Any new mutation invalidates the redo branch.
	i.redoStack = i.redoStack[:0]
}

// Undo restores the most-recently-pushed snapshot.
//
// Algorithm:
//  1. If undoStack is empty 	 nothing to undo, return silently.
//  2. Push the CURRENT state onto redoStack (so Ctrl+Y can come back here).
//  3. Pop the top of undoStack.
//  4. Apply: set i.value and i.cursor from the popped snapshot.
//
// Invariant after Undo: cursor ∈ [0, len(value)] — guaranteed because snapshots
// always capture value+cursor atomically when both are already valid.
func (i *SimpleInput) Undo() {
	if len(i.undoStack) == 0 {
		logDebug("[SimpleInput] Undo: stack empty, nothing to undo")
		return
	}
	// Save current state so Redo can bring us back.
	i.redoStack = append(i.redoStack, inputSnapshot{value: i.value, cursor: i.cursor})

	// Pop and apply the previous state.
	top := i.undoStack[len(i.undoStack)-1]
	i.undoStack = i.undoStack[:len(i.undoStack)-1]
	i.value = top.value
	i.cursor = top.cursor
	logDebug("[SimpleInput] Undo: restored value='%s', cursor=%d (undoStack=%d, redoStack=%d)",
		i.value, i.cursor, len(i.undoStack), len(i.redoStack))
}

// Redo re-applies the most-recently-undone state.
//
// Algorithm (mirror of Undo):
//  1. If redoStack is empty 	 nothing to redo, return silently.
//  2. Push the CURRENT state onto undoStack (Ctrl+Z can undo this redo).
//  3. Pop the top of redoStack.
//  4. Apply: set i.value and i.cursor.
func (i *SimpleInput) Redo() {
	if len(i.redoStack) == 0 {
		logDebug("[SimpleInput] Redo: stack empty, nothing to redo")
		return
	}
	// Save current so Undo can come back here.
	i.undoStack = append(i.undoStack, inputSnapshot{value: i.value, cursor: i.cursor})
	// Enforce max depth for undo stack even during redo replay.
	if len(i.undoStack) > maxUndoDepth {
		i.undoStack = i.undoStack[1:]
	}

	// Pop and apply.
	top := i.redoStack[len(i.redoStack)-1]
	i.redoStack = i.redoStack[:len(i.redoStack)-1]
	i.value = top.value
	i.cursor = top.cursor
	logDebug("[SimpleInput] Redo: restored value='%s', cursor=%d (undoStack=%d, redoStack=%d)",
		i.value, i.cursor, len(i.undoStack), len(i.redoStack))
}

// ClearUndoHistory wipes both undo and redo stacks.  Call this when the input
// is programmatically reset to a brand-new context (e.g. switching
// conversations) so the user cannot Ctrl+Z back into a different chat's text.
func (i *SimpleInput) ClearUndoHistory() {
	i.undoStack = i.undoStack[:0]
	i.redoStack = i.redoStack[:0]
}

// InsertNewline inserts a literal '\n' at the cursor position.
// Called directly by key handlers (e.g. shift+enter, alt+enter) so that
// newline insertion works even when the terminal cannot distinguish
// Shift+Enter from plain Enter at the byte level.
//
// If there is an active selection, the newline replaces it (consistent with
// how typing any character behaves over a selection).
func (i *SimpleInput) InsertNewline() {
	i.DeleteSelection()
	i.pushUndo()
	i.value = i.value[:i.cursor] + "\n" + i.value[i.cursor:]
	i.cursor++
	i.ClearSelection()
	logDebug("[SimpleInput] InsertNewline - cursor=%d", i.cursor)
}

// SetHighlightTerms sets the list of keyword strings that should be highlighted
// inside the input while the user types.  Call this every render frame with the
// current active-skill keyword triggers; it is cheap (just a slice assignment).
func (i *SimpleInput) SetHighlightTerms(terms []string) {
	i.highlightTerms = terms
}

// applySkillHighlights applies Warning-colour bold highlighting to any words in
// line that match one of terms.  The cursor character "█" is preserved exactly
// – the line is split around it so highlighting never corrupts cursor position.
func applySkillHighlights(line string, terms []string) string {
	if len(terms) == 0 || line == "" {
		return line
	}
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Warning)).
		Bold(true)

	const cursorChar = "█"
	parts := strings.SplitN(line, cursorChar, 2)
	for idx, part := range parts {
		parts[idx] = highlightTermsInText(part, terms, style)
	}
	if len(parts) == 2 {
		return parts[0] + cursorChar + parts[1]
	}
	return parts[0]
}

// highlightTermsInText applies style to every occurrence of any term in text
// using case-insensitive whole-word matching.
func highlightTermsInText(text string, terms []string, style lipgloss.Style) string {
	if text == "" || len(terms) == 0 {
		return text
	}
	// Sort longest first so longer phrases take precedence over substrings.
	sorted := make([]string, len(terms))
	copy(sorted, terms)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })

	escaped := make([]string, 0, len(sorted))
	for _, t := range sorted {
		if t != "" {
			escaped = append(escaped, regexp.QuoteMeta(t))
		}
	}
	if len(escaped) == 0 {
		return text
	}

	re, err := regexp.Compile(`(?i)\b(` + strings.Join(escaped, "|") + `)\b`)
	if err != nil {
		return text
	}
	return re.ReplaceAllStringFunc(text, func(match string) string {
		return style.Render(match)
	})
}

// Value returns the current input value
func (i *SimpleInput) Value() string {
	return i.value
}

// GetCursor returns the current cursor position
func (i *SimpleInput) GetCursor() int {
	return i.cursor
}

// SetValue sets the input value
func (i *SimpleInput) SetValue(v string) {
	i.value = v
	i.cursor = len(v)
	// Clear paste state when setting new value
	i.clearPasteState()
	// Clear word wrap cache when value changes
	i.wrapCache = make(map[string][]string)
	i.wrapCacheWidth = 0
	i.wrapCacheText = ""
	i.wrapCacheResult = nil
}

// GetSubmitValue returns the value for submission, replacing ALL paste indicators with original text
func (i *SimpleInput) GetSubmitValue() string {
	if len(i.pasteEntries) == 0 {
		return i.value
	}
	// Replace ALL paste indicators with their original text
	result := i.value
	for _, entry := range i.pasteEntries {
		result = strings.Replace(result, entry.indicator, entry.original, 1)
	}
	return result
}

// clearPasteState resets paste tracking state
func (i *SimpleInput) clearPasteState() {
	i.pasteEntries = nil
}

// Focus focuses the input
func (i *SimpleInput) Focus() {
	i.focused = true
}

// Blur unfocuses the input
func (i *SimpleInput) Blur() {
	i.focused = false
}

// SetWidth sets the input width
func (i *SimpleInput) SetWidth(w int) {
	if i.width != w {
		i.width = w
		// Invalidate word wrap cache when width changes
		i.wrapCache = make(map[string][]string)
		i.wrapCacheWidth = 0
	}
}

// SetHeight sets the maximum input height
func (i *SimpleInput) SetHeight(h int) {
	i.height = h
}

// cachedWordWrap wraps text using cache to avoid re-wrapping the same text.
// This dramatically improves performance when navigating history which
// would otherwise re-wrap the same long inputs repeatedly.
func (i *SimpleInput) cachedWordWrap(text string, width int) []string {
	// Fast path: empty text
	if text == "" {
		return []string{""}
	}

	// Fast path: use last cached result if same text and width
	if width == i.wrapCacheWidth && text == i.wrapCacheText && i.wrapCacheResult != nil {
		return i.wrapCacheResult
	}

	// Check map cache for different texts
	cacheKey := fmt.Sprintf("%s|%d", text, width)
	if cached, ok := i.wrapCache[cacheKey]; ok {
		// Update last cache reference
		i.wrapCacheWidth = width
		i.wrapCacheText = text
		i.wrapCacheResult = cached
		return cached
	}

	// Cache miss: do the actual wrapping
	result := wordWrapText(text, width)

	// Store in cache
	i.wrapCache[cacheKey] = result
	i.wrapCacheWidth = width
	i.wrapCacheText = text
	i.wrapCacheResult = result

	return result
}

// ScrollUp scrolls the input view up
func (i *SimpleInput) ScrollUp() {
	if i.scrollOffset > 0 {
		i.scrollOffset--
	}
}

// ScrollDown scrolls the input view down
func (i *SimpleInput) ScrollDown(totalLines int) {
	if i.scrollOffset < totalLines-i.height {
		i.scrollOffset++
	}
}

// Update handles bubbletea messages
func (i *SimpleInput) Update(msg tea.Msg) tea.Cmd {
	if !i.focused {
		logDebug("[SimpleInput] Update called but not focused")
		return nil
	}

	// Log current state before processing
	logDebug("[SimpleInput] BEFORE UPDATE: value='%s', cursor=%d, len=%d", i.value, i.cursor, len(i.value))

	switch msg := msg.(type) {
	case tea.PasteMsg:
		// Handle paste events - store original text and show visual indicator
		pastedText := msg.Content
		logDebug("[SimpleInput] *** PASTE EVENT *** text='%s', len=%d", pastedText, len(pastedText))

		// Snapshot BEFORE mutating (one undo step for the entire paste).
		i.pushUndo()

		// A paste replaces any active selection (matches typing behaviour).
		i.DeleteSelection()

		// Generate a unique visual indicator for this paste.
		i.pasteSeq++
		indicator := getPastedTextPrompt(pastedText, i.pasteSeq)

		// Add to paste entries list (supports multiple pastes!)
		i.pasteEntries = append(i.pasteEntries, pasteEntry{
			indicator: indicator,
			original:  pastedText,
		})

		// Insert indicator (not the full text!) at cursor position
		i.value = i.value[:i.cursor] + indicator + i.value[i.cursor:]
		i.cursor += len(indicator)

		logDebug("[SimpleInput] Stored paste: indicator='%s', originalLen=%d, totalPastes=%d", indicator, len(pastedText), len(i.pasteEntries))
		return nil

	case tea.PasteStartMsg:
		logDebug("[SimpleInput] *** PASTE START ***")
		return nil

	case tea.PasteEndMsg:
		logDebug("[SimpleInput] *** PASTE END ***")
		return nil

	case tea.KeyMsg:
		key := msg.String()
		k := msg.Key()

		logDebug("[SimpleInput] KeyMsg: string='%s', text='%s', code=%d, mod=%v",
			key, k.Text, k.Code, k.Mod)

		switch key {
		case "ctrl+c":
			// Copy the active selection to the clipboard. When there is no
			// selection we return nil and let the parent handle ctrl+c (its
			// usual quit / interrupt semantics are unaffected because the
			// parent only forwards keys here while the input is focused and we
			// only consume ctrl+c when there is something to copy).
			if i.HasSelection() {
				i.CopySelection()
				return nil
			}
			return nil
		case "ctrl+x":
			// Cut: copy then delete the selection.
			if i.HasSelection() {
				i.CopySelection()
				i.DeleteSelection()
			}
			return nil
		case "shift+enter":
			// Insert a literal newline at the cursor position.
			// This allows composing multi-line messages before sending.
			// (Only fires on terminals with Kitty/extended keyboard protocol.)
			i.DeleteSelection()
			i.pushUndo()
			i.value = i.value[:i.cursor] + "\n" + i.value[i.cursor:]
			i.cursor++
			i.ClearSelection()
			logDebug("[SimpleInput] Shift+Enter - inserted newline, cursor=%d", i.cursor)
			return nil

		case "ctrl+z":
			// Undo: restore the state before the last text mutation.
			// Navigation-only operations (arrow keys) are never on the stack,
			// so Ctrl+Z always moves through meaningful text-change boundaries.
			i.Undo()
			return nil

		case "ctrl+y":
			// Redo: re-apply the most recently undone mutation.
			i.Redo()
			return nil

		case "backspace":
			if i.HasSelection() {
				i.DeleteSelection()
				break
			}
			if len(i.value) > 0 && i.cursor > 0 {
				i.pushUndo()
				i.value = i.value[:i.cursor-1] + i.value[i.cursor:]
				i.cursor--
				logDebug("[SimpleInput] Backspace - new value: '%s'", i.value)
			}
		case "delete":
			if i.HasSelection() {
				i.DeleteSelection()
				break
			}
			if i.cursor < len(i.value) {
				i.pushUndo()
				i.value = i.value[:i.cursor] + i.value[i.cursor+1:]
				logDebug("[SimpleInput] Delete - new value: '%s'", i.value)
			}
		case "left":
			i.ClearSelection()
			if i.cursor > 0 {
				i.cursor--
				logDebug("[SimpleInput] Cursor left: %d", i.cursor)
			}
		case "right":
			i.ClearSelection()
			if i.cursor < len(i.value) {
				i.cursor++
				logDebug("[SimpleInput] Cursor right: %d", i.cursor)
			}
		case "home", "ctrl+a":
			i.ClearSelection()
			i.cursor = 0
		case "end", "ctrl+e":
			i.ClearSelection()
			i.cursor = len(i.value)
		// Word navigation: Alt+Left / Ctrl+Left → move to start of previous word
		case "alt+left", "ctrl+left":
			i.cursor = wordBoundaryLeft(i.value, i.cursor)
			logDebug("[SimpleInput] Word left: cursor=%d", i.cursor)
		// Word navigation: Alt+Right / Ctrl+Right → move to end of next word
		case "alt+right", "ctrl+right":
			i.cursor = wordBoundaryRight(i.value, i.cursor)
			logDebug("[SimpleInput] Word right: cursor=%d", i.cursor)
		// Delete word backward (Ctrl+W, standard readline/shell binding)
		case "ctrl+w", "alt+backspace":
			newPos := wordBoundaryLeft(i.value, i.cursor)
			if newPos != i.cursor {
				i.pushUndo()
				i.value = i.value[:newPos] + i.value[i.cursor:]
				i.cursor = newPos
				logDebug("[SimpleInput] Delete word left: cursor=%d, value='%s'", i.cursor, i.value)
			}
		case "ctrl+u":
			if i.cursor > 0 {
				i.pushUndo()
				i.value = i.value[i.cursor:]
				i.cursor = 0
			}
		case "ctrl+k":
			if i.cursor < len(i.value) {
				i.pushUndo()
				i.value = i.value[:i.cursor]
			}
		case "space":
			// Handle space key explicitly
			i.DeleteSelection()
			i.pushUndo()
			i.value = i.value[:i.cursor] + " " + i.value[i.cursor:]
			i.cursor++
			logDebug("[SimpleInput] Space - new value: '%s'", i.value)
		default:
			// Regular character input - use msg.Key().Text for proper text input
			text := k.Text
			// Some legacy terminals report an unmodified printable key by
			// code without associated text. Bubble Tea's String method still
			// renders that key (for example, "c"), so accept the structured
			// printable code as a conservative fallback. Modifier chords must
			// stay commands rather than becoming text.
			if text == "" && k.Mod == 0 && unicode.IsPrint(k.Code) {
				text = string(k.Code)
			}
			if text != "" {
				// Typing over an active selection replaces it.
				if i.HasSelection() {
					i.DeleteSelection()
				}
				// Bracketed pastes arrive as tea.PasteMsg above. Key events
				// are always committed immediately so fast typing cannot
				// leave the trailing character stranded in a timing buffer.
				i.pushUndo()
				i.value = i.value[:i.cursor] + text + i.value[i.cursor:]
				i.cursor += len(text)
				logDebug("[SimpleInput] Character typed: text='%s'", text)
			} else {
				logDebug("[SimpleInput] Key pressed but no text: key='%s', code=%d", key, k.Code)
			}
		}
		logDebug("[SimpleInput] AFTER KEY: value='%s', cursor=%d", i.value, i.cursor)
	}

	return nil
}

// GetContentHeight returns the true wrapped line count for layout calculations
// This allows the layout to know how tall the input SHOULD be before calling View()
func (i *SimpleInput) GetContentHeight() int {
	if len(i.value) == 0 {
		return 1 // Start with minimum 1 line for placeholder
	}

	maxWidth := i.width - 6
	if maxWidth < 20 {
		maxWidth = 20
	}

	// Add 1 for cursor character when calculating wrap
	textWithCursor := i.value
	if i.focused {
		textWithCursor = i.value[:i.cursor] + "█" + i.value[i.cursor:]
	}

	wrapped := i.cachedWordWrap(textWithCursor, maxWidth)
	logDebug("[GetContentHeight] width=%d, maxWidth=%d, valueLen=%d, wrappedLines=%d", i.width, maxWidth, len(i.value), len(wrapped))
	return len(wrapped)
}

// View renders the input - returns up to i.height lines (no padding)
// Parent uses lipgloss Height() to ensure fixed layout height
func (i *SimpleInput) View() string {
	var displayText string
	if len(i.value) == 0 {
		displayText = lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.TextMuted)).
			Render(i.placeholder)
	} else {
		// Show text with cursor
		if i.focused && i.cursor >= 0 && i.cursor <= len(i.value) {
			if i.HasSelection() {
				// Selection active: render the highlighted value. The cursor
				// glyph is omitted while a selection is shown (the selection
				// itself indicates position), keeping the highlight contiguous.
				displayText = i.renderWithSelection()
			} else {
				before := i.value[:i.cursor]
				after := ""
				if i.cursor < len(i.value) {
					after = i.value[i.cursor:]
				}
				displayText = before + "█" + after
			}
		} else {
			displayText = i.value
		}
	}
	// STRICT WRAPPING: Return ONLY text content without prefix
	// Parent will add "❯ " prefix (2 chars) + box padding (4 chars) = 6 total overhead
	maxWidth := i.width - 6
	if maxWidth < 20 {
		maxWidth = 20
	}

	// Word-aware wrapping (preserves words intact)
	wrapped := i.cachedWordWrap(displayText, maxWidth)

	// Apply skill keyword highlights to each line after wrapping.
	// Doing it post-wrap means lipgloss width calculations during wrapping stay
	// correct (plain text), and we only inject ANSI sequences into the final output.
	if len(i.highlightTerms) > 0 {
		for idx, line := range wrapped {
			wrapped[idx] = applySkillHighlights(line, i.highlightTerms)
		}
	}

	// Style paste indicators (e.g. "[#1 Pasted 3 lines]") so they
	// stand out as distinct chips rather than looking like literal typed text.
	// Applied post-wrap for the same reason as skill highlights.
	if len(i.pasteEntries) > 0 {
		for idx, line := range wrapped {
			wrapped[idx] = stylePasteIndicators(line)
		}
	}

	totalLines := len(wrapped)

	// Find which line has the cursor (still relies on the "█" sentinel being
	// present in the wrapped text — must run BEFORE styleCursor consumes it).
	cursorLine := findCursorLine(wrapped)

	// Render the cursor as an inverted (reverse-video) block ON the character it
	// sits on, instead of leaving the inserted "█" sentinel which would shove the
	// surrounding characters left/right. Done post-wrap (like the highlight/paste
	// passes) so word-wrap width math stays correct on plain text.
	if i.focused && !i.HasSelection() {
		for idx, line := range wrapped {
			wrapped[idx] = styleCursor(line)
		}
	}

	// CRITICAL: Always ensure cursor line is visible
	// When typing at end, cursor should be on last visible line
	var result []string
	if totalLines > i.height {
		// More lines than space - scroll to keep cursor visible
		// Keep cursor on the LAST visible line when near bottom
		startLine := cursorLine - i.height + 1
		if startLine < 0 {
			startLine = 0
		}
		// Don't scroll past the end
		maxStart := totalLines - i.height
		if startLine > maxStart {
			startLine = maxStart
		}
		result = wrapped[startLine : startLine+i.height]
	} else {
		// Content fits: return all lines, padded to i.height
		result = wrapped
		// Pad with empty lines to maintain consistent height
		for len(result) < i.height {
			result = append(result, "")
		}
	}

	// SAFETY: Ensure we NEVER return more than height lines
	if len(result) > i.height && i.height > 0 {
		result = result[len(result)-i.height:]
	}

	logDebug("[SimpleInput.View] width=%d, maxWidth=%d, height=%d, lines=%d, cursor=%d, showing=%d", i.width, maxWidth, i.height, totalLines, cursorLine, len(result))

	return strings.Join(result, "\n")
}

// styleCursor converts the inserted "█" sentinel into an inverted
// (reverse-video) rendering of the character it precedes, so the cursor
// appears ON that character in a single cell rather than pushing surrounding
// text left/right. When the sentinel is at end-of-line (no following
// character), a reverse-video space is rendered so an end cursor stays visible.
//
// This must run AFTER findCursorLine (which locates the sentinel) and AFTER the
// skill/paste post-wrap passes (which SplitN around the sentinel and pass it
// through untouched).
func styleCursor(line string) string {
	const cursorSentinel = "█"
	idx := strings.Index(line, cursorSentinel)
	if idx < 0 {
		return line
	}
	rev := lipgloss.NewStyle().Reverse(true)
	rest := line[idx+len(cursorSentinel):]
	if rest == "" {
		// End-of-line cursor: no character to invert, show a reversed space.
		return line[:idx] + rev.Render(" ")
	}
	r, size := utf8.DecodeRuneInString(rest)
	return line[:idx] + rev.Render(string(r)) + rest[size:]
}

// findCursorLine returns which line (0-indexed) contains the cursor
// after word wrapping. The cursor is the "█" character in the text.
func findCursorLine(wrapped []string) int {
	for i, line := range wrapped {
		if strings.Contains(line, "█") {
			return i
		}
	}
	return len(wrapped) - 1 // Fallback to last line
}

// GetCursorLineIndex returns the 0-based visual line index of the cursor after
// word-wrapping. Returns 0 when the input is empty or the cursor is at the top.
func (i *SimpleInput) GetCursorLineIndex() int {
	if len(i.value) == 0 {
		return 0
	}
	maxWidth := i.width - 6
	if maxWidth < 20 {
		maxWidth = 20
	}
	textWithCursor := i.value[:i.cursor] + "█" + i.value[i.cursor:]
	wrapped := i.cachedWordWrap(textWithCursor, maxWidth)
	return findCursorLine(wrapped)
}

// GetTotalWrappedLines returns the total number of visual lines after word-wrap.
func (i *SimpleInput) GetTotalWrappedLines() int {
	if len(i.value) == 0 {
		return 1
	}
	maxWidth := i.width - 6
	if maxWidth < 20 {
		maxWidth = 20
	}
	textWithCursor := i.value[:i.cursor] + "█" + i.value[i.cursor:]
	wrapped := i.cachedWordWrap(textWithCursor, maxWidth)
	return len(wrapped)
}

// wordBoundaryLeft returns the byte offset of the start of the previous word,
// following shell/readline semantics: skip trailing whitespace, then skip the word.
func wordBoundaryLeft(s string, pos int) int {
	if pos == 0 {
		return 0
	}
	runes := []rune(s[:pos])
	idx := len(runes)
	// Skip whitespace / non-word chars backward
	for idx > 0 && isInputWordSep(runes[idx-1]) {
		idx--
	}
	// Skip word chars backward
	for idx > 0 && !isInputWordSep(runes[idx-1]) {
		idx--
	}
	return len(string(runes[:idx]))
}

// wordBoundaryRight returns the byte offset of the end of the next word.
func wordBoundaryRight(s string, pos int) int {
	if pos >= len(s) {
		return len(s)
	}
	runes := []rune(s)
	runeStart := len([]rune(s[:pos]))
	total := len(runes)
	idx := runeStart
	// Skip whitespace / non-word chars forward
	for idx < total && isInputWordSep(runes[idx]) {
		idx++
	}
	// Skip word chars forward
	for idx < total && !isInputWordSep(runes[idx]) {
		idx++
	}
	return len(string(runes[:idx]))
}

// isInputWordSep reports whether r is a word separator for navigation purposes.
func isInputWordSep(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '/' || r == '.' ||
		r == '-' || r == '_' || r == '(' || r == ')' || r == '[' || r == ']' ||
		r == '{' || r == '}' || r == ':' || r == ';' || r == ',' || r == '"' || r == '\''
}

// wordWrapText wraps text at word boundaries, preserving words intact
// Falls back to character break only for words longer than width

// SetPlaceholder sets the placeholder text
func (i *SimpleInput) SetPlaceholder(text string) {
	i.placeholder = text
}

// ============================================================================
// VISUAL LINE CURSOR MOVEMENT
// ============================================================================

// MoveLineUp moves the cursor up one visual (word-wrapped) line while keeping
// the same visual column. Returns false if already on the first visual line.
func (i *SimpleInput) MoveLineUp() bool {
	return i.moveToVisualLine(-1)
}

// MoveLineDown moves the cursor down one visual (word-wrapped) line while
// keeping the same visual column. Returns false if already on the last visual
// line.
func (i *SimpleInput) MoveLineDown() bool {
	return i.moveToVisualLine(+1)
}

// moveToVisualLine shifts the cursor ±delta visual lines (−1 = up, +1 = down)
// at the same approximate visual column. Returns false when at the boundary so
// the caller can fall through to history navigation.
func (i *SimpleInput) moveToVisualLine(delta int) bool {
	if len(i.value) == 0 {
		return false
	}
	maxWidth := i.width - 6
	if maxWidth < 20 {
		maxWidth = 20
	}

	// Step 1: find (curLine, curCol) by inserting a NUL sentinel at the cursor
	// and word-wrapping. NUL won't appear in normal user input.
	const sentinel = "\x00"
	sentineled := i.value[:i.cursor] + sentinel + i.value[i.cursor:]
	wrappedS := i.cachedWordWrap(sentineled, maxWidth)

	curLine := -1
	curCol := 0
	for li, line := range wrappedS {
		before, _, ok := strings.Cut(line, sentinel)
		if ok {
			curLine = li
			// Measure visual width of the text before the sentinel.
			// Strip any ANSI codes that might be present (shouldn't be in input,
			// but be safe).
			curCol = lipgloss.Width(stripANSI(before))
			break
		}
	}
	if curLine < 0 {
		return false
	}

	targetLine := curLine + delta
	if targetLine < 0 || targetLine >= len(wrappedS) {
		return false // already at first / last visual line
	}

	// Step 2: map the target (targetLine, curCol) back to a byte offset in
	// i.value by building byte ranges for each visual line of the original text.
	ranges := inputBuildLineRanges(i.value, maxWidth)
	if targetLine >= len(ranges) {
		return false
	}
	lr := ranges[targetLine]
	lineText := i.value[lr.start:lr.end]
	i.cursor = lr.start + inputByteAtVisualCol(lineText, curCol)
	return true
}

// inputLineRange describes one visual line produced by word-wrap and the
// corresponding byte range in the original string.
type inputLineRange struct {
	start int // inclusive byte offset in the original string
	end   int // exclusive byte offset (past the last char of this line)
}

// inputBuildLineRanges word-wraps text at width and returns the byte range in
// text that each visual line covers. The algorithm mirrors wordWrapText so
// that line boundaries are consistent with the rendered output.
func inputBuildLineRanges(text string, width int) []inputLineRange {
	if width <= 0 || len(text) == 0 {
		return []inputLineRange{{0, len(text)}}
	}
	var result []inputLineRange
	offset := 0
	remaining := text

	for {
		nlIdx := strings.IndexByte(remaining, '\n')
		var para string
		if nlIdx < 0 {
			para = remaining
		} else {
			para = remaining[:nlIdx]
		}
		ranges := inputWrapParagraph(para, width, offset)
		result = append(result, ranges...)

		if nlIdx < 0 {
			break
		}
		offset += len(para) + 1 // +1 consumes the '\n'
		remaining = remaining[len(para)+1:]
	}
	return result
}

// inputWrapParagraph wraps a single paragraph (no embedded newlines) at width
// and returns the byte ranges within the full original string (using base as
// the starting byte offset of this paragraph).
func inputWrapParagraph(para string, width, base int) []inputLineRange {
	if para == "" {
		return []inputLineRange{{base, base}}
	}

	// Collect words with their byte offsets relative to `para`.
	type wordInfo struct {
		start, end int
		text       string
	}
	var words []wordInfo
	i := 0
	for i < len(para) {
		for i < len(para) && para[i] == ' ' {
			i++
		}
		if i >= len(para) {
			break
		}
		j := i
		for j < len(para) && para[j] != ' ' {
			j++
		}
		words = append(words, wordInfo{start: i, end: j, text: para[i:j]})
		i = j
	}
	if len(words) == 0 {
		return []inputLineRange{{base, base}}
	}

	var result []inputLineRange
	lineStartIdx := 0 // index into words[] for the first word on the current line
	lineWidth := 0

	flush := func(startWordIdx, endWordIdx int) {
		s := base + words[startWordIdx].start
		e := base + words[endWordIdx].end
		result = append(result, inputLineRange{s, e})
	}

	for wi, w := range words {
		ww := lipgloss.Width(w.text)

		// Long word: split it off as its own line(s).
		if ww > width {
			if lineWidth > 0 {
				flush(lineStartIdx, wi-1)
			}
			// Treat the whole long word as one visual line for cursor purposes.
			result = append(result, inputLineRange{base + w.start, base + w.end})
			lineStartIdx = wi + 1
			lineWidth = 0
			continue
		}

		needed := ww
		if lineWidth > 0 {
			needed++ // space before this word
		}
		if lineWidth > 0 && lineWidth+needed > width {
			flush(lineStartIdx, wi-1)
			lineStartIdx = wi
			lineWidth = ww
		} else {
			lineWidth += needed
		}
	}
	// Flush the last line.
	if lineStartIdx < len(words) {
		flush(lineStartIdx, len(words)-1)
	}
	return result
}

// inputByteAtVisualCol returns the byte offset within `line` that corresponds
// to the given visual column (0-based). Clamps to the end of the line when
// col exceeds the line's visual width.
func inputByteAtVisualCol(line string, col int) int {
	if col <= 0 {
		return 0
	}
	visualWidth := 0
	for byteIdx := 0; byteIdx < len(line); {
		r, size := utf8.DecodeRuneInString(line[byteIdx:])
		rw := lipgloss.Width(string(r))
		if visualWidth+rw > col {
			return byteIdx
		}
		visualWidth += rw
		byteIdx += size
		if visualWidth >= col {
			return byteIdx
		}
	}
	return len(line) // clamp to end-of-line
}
