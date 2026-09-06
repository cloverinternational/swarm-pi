package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
	"github.com/charmbracelet/x/ansi"
)

// Nerd Font icons for file types (using Unicode escape sequences)
const (
	// Folders - nf-fa-folder / nf-fa-folder_open
	iconFolder     = "\uf07b"
	iconFolderOpen = "\uf07c"

	// Common file types
	iconGo          = "\ue627" // nf-seti-go
	iconRust        = "\ue7a8" // nf-dev-rust
	iconPython      = "\ue73c" // nf-dev-python
	iconJS          = "\ue74e" // nf-seti-javascript
	iconTS          = "\ue628" // nf-seti-typescript
	iconJSON        = "\ue60b" // nf-seti-json
	iconYAML        = "\ue6a8" // nf-seti-yml
	iconMD          = "\ue609" // nf-seti-markdown
	iconTOML        = "\ue615" // nf-seti-settings
	iconGit         = "\ue702" // nf-dev-git
	iconDocker      = "\ue7b0" // nf-dev-docker
	iconLock        = "\uf023" // nf-fa-lock
	iconConfig      = "\ue615" // nf-seti-config
	iconShell       = "\ue795" // nf-dev-terminal
	iconMakefile    = "\ue673" // nf-seti-makefile
	iconLicense     = "\ue60a" // nf-seti-license
	iconReadme      = "\uf05a" // nf-fa-info_circle
	iconTest        = "\uf0c3" // nf-fa-flask
	iconHTML        = "\ue736" // nf-seti-html
	iconCSS         = "\ue749" // nf-seti-css
	iconImage       = "\uf1c5" // nf-fa-file_image_o
	iconFileGeneric = "\uf016" // nf-fa-file_o
)

// MentionAutocompleteTheme defines colors for the mention autocomplete UI
type MentionAutocompleteTheme struct {
	Background  string
	Border      string
	Selected    string
	SelectedBg  string
	Text        string
	TextDim     string
	PathText    string
	FolderColor string
	FileColor   string
	GoColor     string
	RustColor   string
	PythonColor string
	JSColor     string
	TSColor     string
	JSONColor   string
	MDColor     string
	ShellColor  string
	DockerColor string
	ConfigColor string
}

// DefaultMentionAutocompleteTheme provides sensible defaults
var DefaultMentionAutocompleteTheme = MentionAutocompleteTheme{
	Background:  palette.Panel,
	Border:      palette.Border,
	Selected:    palette.Accent,
	SelectedBg:  palette.AccentDim,
	Text:        palette.Text,
	TextDim:     palette.TextDim,
	PathText:    palette.Info,
	FolderColor: palette.Warning,
	FileColor:   palette.TextDim,
	GoColor:     "#00ADD8",
	RustColor:   "#DEA584",
	PythonColor: "#3572A5",
	JSColor:     "#f7df1e",
	TSColor:     "#3178c6",
	JSONColor:   "#cbcb41",
	MDColor:     "#519aba",
	ShellColor:  "#89e051",
	DockerColor: "#2496ed",
	ConfigColor: "#6d8086",
}

// MentionAutocomplete manages unified @mention autocomplete UI (agents, tmux, files)
type MentionAutocomplete struct {
	workspaceRoot string
	input         string
	triggerPos    int
	pathFragment  string
	currentDir    string // Current directory being browsed (file mode only)
	matches       []MentionEntry
	selectedIdx   int
	visible       bool
	width         int
	height        int
	theme         MentionAutocompleteTheme
	fileOnlyMode  bool // true when fragment contains "/" (file browsing)

	// Providers for non-file mentions
	agentProvider AgentMentionProvider
	tmuxProvider  TmuxMentionProvider

	// Search cancellation state
	searchCtx    context.Context // context for cancelling stale searches
	searchCancel context.CancelFunc

	// Caching state
	fileCache map[string]*fileCacheEntry
	cacheMu   sync.RWMutex
	cacheTTL  time.Duration // default 5s
}

// fileCacheEntry stores cached file search results
type fileCacheEntry struct {
	entries   []MentionEntry
	timestamp time.Time
}

// fuzzyScore calculates a fuzzy match score (higher = better match)
// Returns score 0-100 and true if query matches target
func fuzzyScore(query, target string) (int, bool) {
	if query == "" {
		return 100, true // Empty query matches everything with max score
	}

	queryLower := strings.ToLower(query)
	targetLower := strings.ToLower(target)

	// Exact match gets highest score
	if queryLower == targetLower {
		return 100, true
	}

	// Prefix match gets high score
	if strings.HasPrefix(targetLower, queryLower) {
		score := 80 + (20 * len(query) / len(target))
		if score > 100 {
			score = 100
		}
		return score, true
	}

	// Fuzzy match: check if all query chars appear in target in order
	queryRunes := []rune(queryLower)
	targetRunes := []rune(targetLower)

	queryIdx := 0
	matches := 0
	for _, targetRune := range targetRunes {
		if queryIdx < len(queryRunes) && targetRune == queryRunes[queryIdx] {
			queryIdx++
			matches++
		}
	}

	if queryIdx >= len(queryRunes) {
		// All query characters found in order
		// Score based on: match ratio, consecutive bonus, position bonus
		baseScore := 40 * matches / len(targetRunes)

		// Consecutive matches bonus
		consecutiveBonus := 0
		for i := 0; i < len(queryRunes)-1; i++ {
			idx1 := strings.IndexRune(targetLower, queryRunes[i])
			idx2 := strings.IndexRune(targetLower[idx1+1:], queryRunes[i+1])
			if idx2 == 0 { // Consecutive match
				consecutiveBonus += 20
			}
		}

		// Early position bonus (matches at start are better)
		firstMatchIdx := strings.IndexRune(targetLower, queryRunes[0])
		positionBonus := 20 * (len(targetRunes) - firstMatchIdx) / len(targetRunes)

		score := baseScore + consecutiveBonus + positionBonus
		if score > 79 {
			score = 79 // Cap fuzzy below prefix matches
		}
		return score, true
	}

	return 0, false
}

// getGitFiles returns tracked files from git ls-files
// Returns nil if not in a git repo or git is unavailable
func (ma *MentionAutocomplete) getGitFiles() []string {
	if ma.workspaceRoot == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", ma.workspaceRoot, "ls-files", "--cached", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return nil // Not a git repo or git not available
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, line := range lines {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

// NewMentionAutocomplete creates a new unified mention autocomplete component
func NewMentionAutocomplete(workspaceRoot string) *MentionAutocomplete {
	return &MentionAutocomplete{
		workspaceRoot: workspaceRoot,
		visible:       false,
		selectedIdx:   0,
		theme:         DefaultMentionAutocompleteTheme,
		width:         80,
		height:        10,
		// Caching config
		fileCache: make(map[string]*fileCacheEntry),
		cacheTTL:  5 * time.Second,
	}
}

// SetAgentProvider sets the agent mention provider
func (ma *MentionAutocomplete) SetAgentProvider(p AgentMentionProvider) {
	ma.agentProvider = p
}

// SetTmuxProvider sets the tmux session mention provider
func (ma *MentionAutocomplete) SetTmuxProvider(p TmuxMentionProvider) {
	ma.tmuxProvider = p
}

// SetTheme updates the background and border colours to match the active theme.
func (ma *MentionAutocomplete) SetTheme(bg, border, selected, selectedBg, text, textDim string) {
	ma.theme.Background = bg
	ma.theme.Border = border
	ma.theme.Selected = selected
	ma.theme.SelectedBg = selectedBg
	ma.theme.Text = text
	ma.theme.TextDim = textDim
}

// SetWorkspaceRoot updates the workspace root
func (ma *MentionAutocomplete) SetWorkspaceRoot(root string) {
	ma.workspaceRoot = root
}

// SetInput updates the input and refreshes matches (with debouncing)
func (ma *MentionAutocomplete) SetInput(input string) {
	ma.input = input

	// Find the last @ that could be a trigger
	triggerPos := -1
	for i := len(input) - 1; i >= 0; i-- {
		if input[i] == '@' {
			if i == 0 || input[i-1] == ' ' || input[i-1] == '\n' || input[i-1] == '\t' {
				triggerPos = i
				break
			}
		}
	}

	if triggerPos == -1 {
		ma.visible = false
		ma.matches = nil
		ma.triggerPos = -1
		ma.pathFragment = ""
		ma.currentDir = ""
		ma.fileOnlyMode = false
		return
	}

	ma.triggerPos = triggerPos
	ma.pathFragment = input[triggerPos+1:]

	// Check for quoted string support (paths with spaces)
	// If fragment starts with quote, extend until closing quote
	if strings.HasPrefix(ma.pathFragment, "\"") || strings.HasPrefix(ma.pathFragment, "'") {
		quoteChar := ma.pathFragment[0]
		// Find closing quote or end of string
		endPos := strings.IndexByte(ma.pathFragment[1:], quoteChar)
		if endPos == -1 {
			// No closing quote yet, fragment continues to end
			// pathFragment already contains everything after @
		} else {
			// Include the closing quote in fragment
			ma.pathFragment = ma.pathFragment[:endPos+2] // +2 for both quotes
		}
	} else {
		// Don't show if there's a space in the fragment (mention is complete)
		if strings.Contains(ma.pathFragment, " ") {
			ma.visible = false
			ma.matches = nil
			return
		}
	}

	// Cancel any in-flight search context from a previous input.
	if ma.searchCancel != nil {
		ma.searchCancel()
	}
	ma.searchCtx, ma.searchCancel = context.WithCancel(context.Background())

	// Run the search synchronously so IsVisible()/matches reflect the current
	// input immediately. A prior debounced time.AfterFunc set visible/matches on
	// a separate goroutine with NO re-render notification — so the popup appeared
	// a keystroke late (or never, on pause) and raced on visible/matches.
	ma.executeSearch()
}

// executeSearch performs the actual search (called after debounce)
func (ma *MentionAutocomplete) executeSearch() {
	// Check if context was cancelled
	if ma.searchCtx == nil {
		return
	}
	select {
	case <-ma.searchCtx.Done():
		return
	default:
	}

	// Determine mode: if fragment contains "/" → file-only mode
	if strings.Contains(ma.pathFragment, "/") {
		ma.fileOnlyMode = true
		ma.matches = ma.getFileMatchesWithCache(ma.pathFragment)
	} else {
		ma.fileOnlyMode = false
		ma.matches = ma.getUnifiedMatches(ma.pathFragment)
	}

	ma.visible = len(ma.matches) > 0
	ma.selectedIdx = 0
}

// getFileMatchesWithCache returns file matches with caching support
func (ma *MentionAutocomplete) getFileMatchesWithCache(fragment string) []MentionEntry {
	// Compute cache key: workspaceRoot + fragment
	cacheKey := ma.workspaceRoot + "::" + fragment

	// Check cache first
	ma.cacheMu.RLock()
	if entry, ok := ma.fileCache[cacheKey]; ok {
		if time.Since(entry.timestamp) < ma.cacheTTL {
			ma.cacheMu.RUnlock()
			return entry.entries
		}
	}
	ma.cacheMu.RUnlock()

	// Cache miss - perform actual search
	results := ma.getFileMatches(fragment)

	// Store in cache
	ma.cacheMu.Lock()
	ma.fileCache[cacheKey] = &fileCacheEntry{
		entries:   results,
		timestamp: time.Now(),
	}
	ma.cacheMu.Unlock()

	return results
}

// getUnifiedMatches returns agents + tmux sessions + files matching the fragment
func (ma *MentionAutocomplete) getUnifiedMatches(fragment string) []MentionEntry {
	var matches []MentionEntry
	filterLower := strings.ToLower(fragment)

	// Section 1: Agents
	if ma.agentProvider != nil {
		agents := ma.agentProvider.GetAgentMentions()
		for _, ag := range agents {
			if fragment == "" || strings.Contains(strings.ToLower(ag.Name), filterLower) {
				matches = append(matches, ag)
			}
		}
	}

	// Section 2: Tmux sessions
	if ma.tmuxProvider != nil {
		sessions := ma.tmuxProvider.GetTmuxSessions()
		for _, s := range sessions {
			if fragment == "" || strings.Contains(strings.ToLower(s.Name), filterLower) {
				matches = append(matches, s)
			}
		}
	}

	// Section 3: Files (limited to top 5 in unified mode)
	fileMatches := ma.getFileMatchesWithCache(fragment)
	maxFiles := 5
	if len(fileMatches) > maxFiles {
		fileMatches = fileMatches[:maxFiles]
	}
	matches = append(matches, fileMatches...)

	return matches
}

// getFileMatches returns file/folder entries matching the path fragment with fuzzy scoring
func (ma *MentionAutocomplete) getFileMatches(fragment string) []MentionEntry {
	// Check for home directory expansion (~/)
	isHomePath := strings.HasPrefix(fragment, "~/")
	var homeDir string

	if isHomePath {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return nil
		}
	} else if ma.workspaceRoot == "" {
		return nil
	}

	// Parse fragment to determine directory and filter
	var dirToList string
	var filterPrefix string

	if isHomePath {
		// Handle home directory paths (~/folder/)
		pathAfterTilde := fragment[2:] // Strip "~/"
		if pathAfterTilde == "" {
			dirToList = homeDir
			filterPrefix = ""
			ma.currentDir = "~/"
		} else if strings.HasSuffix(pathAfterTilde, "/") {
			dirToList = filepath.Join(homeDir, pathAfterTilde)
			filterPrefix = ""
			ma.currentDir = fragment
		} else {
			dir := filepath.Dir(pathAfterTilde)
			if dir == "." {
				dirToList = homeDir
				ma.currentDir = "~/"
			} else {
				dirToList = filepath.Join(homeDir, dir)
				ma.currentDir = "~/" + dir + "/"
			}
			filterPrefix = filepath.Base(pathAfterTilde)
		}
	} else if fragment == "" {
		dirToList = ma.workspaceRoot
		filterPrefix = ""
		ma.currentDir = ""
	} else if strings.HasSuffix(fragment, "/") {
		dirToList = filepath.Join(ma.workspaceRoot, fragment)
		filterPrefix = ""
		ma.currentDir = fragment
	} else {
		dir := filepath.Dir(fragment)
		if dir == "." {
			dirToList = ma.workspaceRoot
			ma.currentDir = ""
		} else {
			dirToList = filepath.Join(ma.workspaceRoot, dir)
			ma.currentDir = dir + "/"
		}
		filterPrefix = filepath.Base(fragment)
	}

	// For home directory paths, skip the workspace bounds check
	if !isHomePath && !ma.isWithinBounds(dirToList) {
		return nil
	}

	// Try git first for better file discovery (respects .gitignore)
	// Note: Git discovery only works for workspace paths, not home directory paths
	var allFiles []string
	if !isHomePath {
		if gitFiles := ma.getGitFiles(); gitFiles != nil {
			// Filter git files to current directory context
			currentDirPrefix := ma.currentDir
			for _, f := range gitFiles {
				// Only include files in current directory or its subdirectories
				if strings.HasPrefix(f, currentDirPrefix) || currentDirPrefix == "" {
					// Get relative path from current directory
					relPath := strings.TrimPrefix(f, currentDirPrefix)
					// Skip files deeper than immediate subdirectory unless in file-only mode
					if !strings.Contains(relPath, "/") || currentDirPrefix == "" {
						allFiles = append(allFiles, relPath)
					}
				}
			}
		}
	}

	// Fall back to directory listing if git not available or no results
	if len(allFiles) == 0 {
		entries, err := os.ReadDir(dirToList)
		if err != nil {
			return nil
		}
		for _, entry := range entries {
			name := entry.Name()
			// Skip hidden files
			if strings.HasPrefix(name, ".") {
				continue
			}
			allFiles = append(allFiles, name)
		}
	}

	// Score and filter files using fuzzy matching
	type scoredMatch struct {
		entry MentionEntry
		score int
	}

	var scoredMatches []scoredMatch

	for _, name := range allFiles {
		// Apply fuzzy scoring
		score, matched := fuzzyScore(filterPrefix, name)
		if !matched {
			continue
		}

		// Build relative path
		var relPath string
		if isHomePath {
			// For home paths, preserve the ~/ prefix
			if fragment == "~/" {
				relPath = name
			} else if strings.HasSuffix(fragment, "/") {
				relPath = fragment[2:] + name // Strip ~/ and append
			} else {
				dir := filepath.Dir(fragment[2:]) // Strip ~/ for path calculation
				if dir == "." {
					relPath = name
				} else {
					relPath = filepath.Join(dir, name)
				}
			}
		} else if fragment == "" {
			relPath = name
		} else if strings.HasSuffix(fragment, "/") {
			relPath = fragment + name
		} else {
			dir := filepath.Dir(fragment)
			if dir == "." {
				relPath = name
			} else {
				relPath = filepath.Join(dir, name)
			}
		}
		relPath = filepath.ToSlash(relPath)

		// Build the full path for checking if it's a directory
		var fullPath string
		if isHomePath {
			fullPath = filepath.Join(homeDir, relPath)
		} else {
			fullPath = filepath.Join(ma.workspaceRoot, relPath)
		}
		isDir := false
		if info, err := os.Stat(fullPath); err == nil {
			isDir = info.IsDir()
		}

		scoredMatches = append(scoredMatches, scoredMatch{
			entry: MentionEntry{
				Type:    MentionFile,
				Name:    name,
				Path:    relPath,
				IsDir:   isDir,
				RelPath: relPath,
			},
			score: score,
		})
	}

	// Sort by score (descending), then directories first, then alphabetically
	sort.Slice(scoredMatches, func(i, j int) bool {
		if scoredMatches[i].score != scoredMatches[j].score {
			return scoredMatches[i].score > scoredMatches[j].score
		}
		if scoredMatches[i].entry.IsDir != scoredMatches[j].entry.IsDir {
			return scoredMatches[i].entry.IsDir
		}
		return strings.ToLower(scoredMatches[i].entry.Name) < strings.ToLower(scoredMatches[j].entry.Name)
	})

	// Limit results
	if len(scoredMatches) > 15 {
		scoredMatches = scoredMatches[:15]
	}

	// Extract entries
	var matches []MentionEntry
	for _, sm := range scoredMatches {
		matches = append(matches, sm.entry)
	}

	return matches
}

// isWithinBounds checks if a path is within the workspace root
func (ma *MentionAutocomplete) isWithinBounds(path string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	absPath = filepath.Clean(absPath)
	rootClean := filepath.Clean(ma.workspaceRoot)

	rel, err := filepath.Rel(rootClean, absPath)
	if err != nil {
		return false
	}

	return !strings.HasPrefix(rel, "..")
}

// SetSize sets the autocomplete dimensions
func (ma *MentionAutocomplete) SetSize(width, height int) {
	ma.width = width
	ma.height = height
	if ma.height > 12 {
		ma.height = 12
	}
}

// IsVisible returns whether autocomplete is shown
func (ma *MentionAutocomplete) IsVisible() bool {
	return ma.visible
}

// Hide hides the autocomplete and cleans up resources
func (ma *MentionAutocomplete) Hide() {
	ma.visible = false
	// Cancel in-flight search context
	if ma.searchCancel != nil {
		ma.searchCancel()
	}
}

// SelectedPath returns the formatted mention text to insert.
// - Agent (plugin): @agent:namespace:name
// - Agent (custom):  @agent:name
// - Tmux session:    @tmux:session-name
// - File:            @file:/absolute/path
func (ma *MentionAutocomplete) SelectedPath() string {
	if !ma.visible || ma.selectedIdx >= len(ma.matches) {
		return ""
	}

	entry := ma.matches[ma.selectedIdx]

	switch entry.Type {
	case MentionAgent:
		if entry.AgentNS != "" {
			return "@agent:" + entry.AgentNS + ":" + entry.AgentID + " "
		}
		return "@agent:" + entry.AgentID + " "
	case MentionTmux:
		return "@tmux:" + entry.SessionName + " "
	case MentionFile:
		// Use consistent @file:path format for both files and directories
		absPath := filepath.Join(ma.workspaceRoot, entry.Path)
		if entry.IsDir {
			return "@file:" + absPath + "/"
		}
		return "@file:" + absPath + " "
	}

	return ""
}

// CompleteSelection completes the current selection and returns the new input value.
func (ma *MentionAutocomplete) CompleteSelection() string {
	if !ma.visible || ma.selectedIdx >= len(ma.matches) {
		return ma.input
	}

	entry := ma.matches[ma.selectedIdx]
	prefix := ma.input[:ma.triggerPos]

	// Check if the original fragment was quoted
	wasQuoted := false
	if strings.HasPrefix(ma.pathFragment, "\"") || strings.HasPrefix(ma.pathFragment, "'") {
		wasQuoted = true
	}

	// Check if this is a home directory path (~/)
	isHomePath := strings.HasPrefix(ma.pathFragment, "~/")

	switch entry.Type {
	case MentionAgent:
		if entry.AgentNS != "" {
			return prefix + "@agent:" + entry.AgentNS + ":" + entry.AgentID + " "
		}
		return prefix + "@agent:" + entry.AgentID + " "
	case MentionTmux:
		return prefix + "@tmux:" + entry.SessionName + " "
	case MentionFile:
		if isHomePath {
			// For home directory paths, reconstruct using ~/ prefix
			homePath := "~/" + entry.Path
			if wasQuoted {
				// Preserve quotes around the path
				if entry.IsDir {
					return prefix + "@file:\"" + homePath + "/\""
				}
				return prefix + "@file:\"" + homePath + "\" "
			}
			if entry.IsDir {
				return prefix + "@file:" + homePath + "/"
			}
			return prefix + "@file:" + homePath + " "
		}

		// Use consistent @file:path format for both files and directories
		absPath := filepath.Join(ma.workspaceRoot, entry.Path)
		if wasQuoted {
			// Preserve quotes around the path
			if entry.IsDir {
				return prefix + "@file:\"" + absPath + "/\""
			}
			return prefix + "@file:\"" + absPath + "\" "
		}
		if entry.IsDir {
			return prefix + "@file:" + absPath + "/"
		}
		return prefix + "@file:" + absPath + " "
	}

	return ma.input
}

// NavigateInto enters the selected directory (right arrow behavior)
// Only works for file entries that are directories.
func (ma *MentionAutocomplete) NavigateInto() (bool, string) {
	if !ma.visible || ma.selectedIdx >= len(ma.matches) {
		return false, ma.input
	}

	entry := ma.matches[ma.selectedIdx]
	if entry.Type != MentionFile || !entry.IsDir {
		return false, ma.input
	}

	newPath := entry.Path + "/"
	newInput := ma.input[:ma.triggerPos] + "@" + newPath
	return true, newInput
}

// NavigateOut goes up one directory level (left arrow behavior)
// Only works when in file-only mode with a current directory.
func (ma *MentionAutocomplete) NavigateOut() (bool, string) {
	if !ma.visible || ma.currentDir == "" {
		return false, ma.input
	}

	parentDir := filepath.Dir(strings.TrimSuffix(ma.currentDir, "/"))
	var newPath string
	if parentDir == "." {
		newPath = ""
	} else {
		newPath = parentDir + "/"
	}

	newInput := ma.input[:ma.triggerPos] + "@" + newPath
	return true, newInput
}

// GetSelectedEntry returns the currently selected mention entry
func (ma *MentionAutocomplete) GetSelectedEntry() (MentionEntry, bool) {
	if !ma.visible || ma.selectedIdx >= len(ma.matches) {
		return MentionEntry{}, false
	}
	return ma.matches[ma.selectedIdx], true
}

// TriggerPosition returns where in the input the @ trigger is
func (ma *MentionAutocomplete) TriggerPosition() int {
	return ma.triggerPos
}

// Update handles keyboard navigation
func (ma *MentionAutocomplete) Update(msg tea.Msg) tea.Cmd {
	if !ma.visible {
		return nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "down", "ctrl+n":
			if ma.selectedIdx < len(ma.matches)-1 {
				ma.selectedIdx++
			}
		case "up", "ctrl+p":
			if ma.selectedIdx > 0 {
				ma.selectedIdx--
			}
		}
	}

	return nil
}

// getFileIcon returns the appropriate nerd font icon and color for a file entry
func (ma *MentionAutocomplete) getFileIcon(entry MentionEntry) (string, string) {
	th := ma.theme

	if entry.IsDir {
		return iconFolder, th.FolderColor
	}

	name := strings.ToLower(entry.Name)
	ext := strings.ToLower(filepath.Ext(entry.Name))

	// Special filenames
	switch name {
	case "dockerfile", "dockerfile.dev", "dockerfile.prod":
		return iconDocker, th.DockerColor
	case "makefile", "gnumakefile":
		return iconMakefile, th.ShellColor
	case "license", "license.md", "license.txt":
		return iconLicense, th.TextDim
	case "readme.md", "readme", "readme.txt":
		return iconReadme, th.MDColor
	case "go.mod", "go.sum":
		return iconGo, th.GoColor
	case "cargo.toml", "cargo.lock":
		return iconRust, th.RustColor
	case "package.json", "package-lock.json":
		return iconJSON, th.JSONColor
	case "tsconfig.json":
		return iconTS, th.TSColor
	case ".gitignore", ".gitattributes":
		return iconGit, th.TextDim
	}

	// Check if it's a test file
	if strings.Contains(name, "_test.") || strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") {
		return iconTest, th.TextDim
	}

	// By extension
	switch ext {
	case ".go":
		return iconGo, th.GoColor
	case ".rs":
		return iconRust, th.RustColor
	case ".py", ".pyw", ".pyx":
		return iconPython, th.PythonColor
	case ".js", ".mjs", ".cjs", ".jsx":
		return iconJS, th.JSColor
	case ".ts", ".tsx", ".mts", ".cts":
		return iconTS, th.TSColor
	case ".json", ".jsonc":
		return iconJSON, th.JSONColor
	case ".yaml", ".yml":
		return iconYAML, th.ConfigColor
	case ".toml":
		return iconTOML, th.ConfigColor
	case ".md", ".markdown", ".mdx":
		return iconMD, th.MDColor
	case ".sh", ".bash", ".zsh", ".fish":
		return iconShell, th.ShellColor
	case ".html", ".htm":
		return iconHTML, th.JSColor
	case ".css", ".scss", ".sass", ".less":
		return iconCSS, th.TSColor
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico":
		return iconImage, th.TextDim
	case ".lock":
		return iconLock, th.TextDim
	case ".conf", ".cfg", ".ini", ".env":
		return iconConfig, th.ConfigColor
	}

	return iconFileGeneric, th.FileColor
}

// getMentionIcon returns the icon and color for any mention entry type
func (ma *MentionAutocomplete) getMentionIcon(entry MentionEntry) (string, string) {
	switch entry.Type {
	case MentionAgent:
		return entry.Icon, entry.IconColor
	case MentionTmux:
		return entry.Icon, entry.IconColor
	case MentionFile:
		return ma.getFileIcon(entry)
	}
	return iconFileGeneric, ma.theme.FileColor
}

// clampMentionLine hard-truncates a styled line to exactly maxWidth visual columns.
func clampMentionLine(content string, maxWidth int, bg string) string {
	w := ansi.StringWidth(content)
	if w > maxWidth {
		content = ansi.Truncate(content, maxWidth, "")
	} else if w < maxWidth {
		pad := strings.Repeat(" ", maxWidth-w)
		if bg != "" {
			pad = lipgloss.NewStyle().Background(lipgloss.Color(bg)).Render(pad)
		}
		content += pad
	}
	return content
}

// View renders the unified mention autocomplete dropdown with section headers
func (ma *MentionAutocomplete) View() string {
	if !ma.visible || len(ma.matches) == 0 {
		return ""
	}

	th := ma.theme
	maxWidth := ma.width - 2
	if maxWidth < 40 {
		maxWidth = 40
	}

	var lines []string

	// Separator
	sepLine := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Render(strings.Repeat("─", maxWidth))
	lines = append(lines, sepLine)

	// Build a flat list of display items (section headers + entries)
	type displayItem struct {
		isHeader bool
		header   string
		entryIdx int // index into ma.matches
	}

	var items []displayItem
	lastType := MentionType(-1)

	for i, entry := range ma.matches {
		// Insert section header when type changes (only in unified mode)
		if !ma.fileOnlyMode && entry.Type != lastType {
			var header string
			switch entry.Type {
			case MentionAgent:
				header = "Agents"
			case MentionTmux:
				header = "Sessions"
			case MentionFile:
				header = "Files"
			}
			items = append(items, displayItem{isHeader: true, header: header})
			lastType = entry.Type
		}
		items = append(items, displayItem{isHeader: false, entryIdx: i})
	}

	// Find the display item index for the selected match
	selectedDisplayIdx := 0
	for di, item := range items {
		if !item.isHeader && item.entryIdx == ma.selectedIdx {
			selectedDisplayIdx = di
			break
		}
	}

	// Visible window (scroll around selected)
	maxVisible := 10
	startIdx := 0
	endIdx := len(items)
	if len(items) > maxVisible {
		startIdx = selectedDisplayIdx - maxVisible/2
		if startIdx < 0 {
			startIdx = 0
		}
		endIdx = startIdx + maxVisible
		if endIdx > len(items) {
			endIdx = len(items)
			startIdx = endIdx - maxVisible
			if startIdx < 0 {
				startIdx = 0
			}
		}
	}

	// Render items
	for i := startIdx; i < endIdx; i++ {
		item := items[i]

		if item.isHeader {
			// Section header: dim, left-aligned
			headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim))
			headerLine := headerStyle.Render("  " + item.header)
			lines = append(lines, clampMentionLine(headerLine, maxWidth, ""))
			continue
		}

		entry := ma.matches[item.entryIdx]
		isSelected := item.entryIdx == ma.selectedIdx
		icon, iconColor := ma.getMentionIcon(entry)

		withBg := func(s lipgloss.Style) lipgloss.Style {
			if isSelected {
				return s.Background(lipgloss.Color(th.SelectedBg))
			}
			return s
		}

		var line strings.Builder
		if isSelected {
			line.WriteString(withBg(lipgloss.NewStyle()).Render(" › "))
		} else {
			line.WriteString("   ")
		}

		line.WriteString(withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(iconColor))).Render(icon))
		line.WriteString(withBg(lipgloss.NewStyle()).Render(" "))

		// Display name
		displayName := entry.Name
		if entry.Type == MentionFile && entry.IsDir {
			displayName += "/"
		}
		nameStyle := withBg(lipgloss.NewStyle())
		if isSelected {
			nameStyle = nameStyle.Foreground(lipgloss.Color(th.Selected)).Bold(true)
		} else {
			nameStyle = nameStyle.Foreground(lipgloss.Color(th.Text))
		}
		line.WriteString(nameStyle.Render(displayName))

		// Description suffix for agents and tmux
		if entry.Description != "" && (entry.Type == MentionAgent || entry.Type == MentionTmux) {
			descStyle := withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim)))
			line.WriteString(descStyle.Render("  " + entry.Description))
		}

		bg := ""
		if isSelected {
			bg = th.SelectedBg
		}
		lines = append(lines, clampMentionLine(line.String(), maxWidth, bg))
	}

	return strings.Join(lines, "\n")
}
