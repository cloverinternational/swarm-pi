package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// VS Code-like color palette
var (
	// Background colors
	vscodeBg        = lipgloss.Color("#1e1e1e") // Main editor background
	vscodeSidebarBg = lipgloss.Color("#252526") // Sidebar background
	vscodeActiveBg  = lipgloss.Color("#37373d") // Active/selected background
	vscodeHoverBg   = lipgloss.Color("#2a2d2e") // Hover background
	vscodeBorder    = lipgloss.Color("#3c3c3c") // Border color
	vscodeLineNumBg = lipgloss.Color("#1e1e1e") // Line number gutter

	// Text colors
	vscodeText    = lipgloss.Color("#cccccc") // Default text
	vscodeTextDim = lipgloss.Color("#858585") // Dimmed text
	vscodeLineNum = lipgloss.Color("#858585") // Line numbers

	// Syntax highlighting colors (VS Code Dark+ theme)
	vscodeKeyword     = lipgloss.Color("#569cd6") // Blue - keywords
	vscodeString      = lipgloss.Color("#ce9178") // Orange - strings
	vscodeNumber      = lipgloss.Color("#b5cea8") // Light green - numbers
	vscodeComment     = lipgloss.Color("#6a9955") // Green - comments
	vscodeFunction    = lipgloss.Color("#dcdcaa") // Yellow - function names
	vscodeType        = lipgloss.Color("#4ec9b0") // Cyan - types
	vscodePunctuation = lipgloss.Color("#d4d4d4") // White - punctuation

	// File tree colors
	vscodeFolderIcon = lipgloss.Color("#dcb67a") // Folder icon color
	vscodeFileIcon   = lipgloss.Color("#519aba") // Default file icon

	// Status bar
	vscodeStatusBg = lipgloss.Color("#007acc") // Blue status bar
	vscodeStatusFg = lipgloss.Color("#ffffff") // White status text
)

// File type icon mappings
var fileTypeIcons = map[string]struct {
	icon  string
	color string
}{
	".go":   {"󰟓", "#00add8"},
	".js":   {"󰌞", "#f7df1e"},
	".ts":   {"󰛦", "#3178c6"},
	".jsx":  {"󰜈", "#61dafb"},
	".tsx":  {"󰜈", "#61dafb"},
	".py":   {"󰌠", "#3776ab"},
	".rs":   {"󱘗", "#dea584"},
	".rb":   {"󰴭", "#cc342d"},
	".java": {"󰬷", "#007396"},
	".c":    {"󰙱", "#a8b9cc"},
	".cpp":  {"󰙲", "#00599c"},
	".h":    {"󰙲", "#a8b9cc"},
	".css":  {"󰌜", "#264de4"},
	".scss": {"󰟬", "#cc6699"},
	".html": {"󰌝", "#e34f26"},
	".json": {"󰘦", "#cbcb41"},
	".yaml": {"󰈙", "#cb171e"},
	".yml":  {"󰈙", "#cb171e"},
	".toml": {"󰈙", "#9c4221"},
	".xml":  {"󰗀", "#e37933"},
	".md":   {"󰍔", "#519aba"},
	".txt":  {"󰈙", "#89e051"},
	".sh":   {"󰆍", "#89e051"},
	".bash": {"󰆍", "#89e051"},
	".sql":  {"󰆼", "#e38c00"},
	".mod":  {"󰟓", "#00add8"},
}

// Language keywords for syntax highlighting
var languageKeywords = map[string][]string{
	".go": {"func", "return", "if", "else", "for", "range", "switch", "case", "default",
		"break", "continue", "defer", "go", "select", "chan", "map", "struct",
		"interface", "type", "const", "var", "package", "import", "true", "false",
		"nil", "make", "new", "len", "cap", "append", "copy", "delete", "panic", "recover"},
	".py": {"def", "return", "if", "elif", "else", "for", "while", "break", "continue",
		"pass", "class", "import", "from", "as", "try", "except", "finally", "raise",
		"with", "lambda", "yield", "global", "assert", "True", "False", "None", "async", "await"},
	".js": {"function", "return", "if", "else", "for", "while", "switch", "case",
		"break", "continue", "var", "let", "const", "class", "extends",
		"import", "export", "from", "try", "catch", "finally", "throw", "new",
		"this", "typeof", "async", "await", "true", "false", "null", "undefined"},
	".ts": {"function", "return", "if", "else", "for", "while", "switch", "case",
		"break", "continue", "var", "let", "const", "class", "extends", "implements",
		"interface", "type", "enum", "import", "export", "from", "try", "catch",
		"finally", "throw", "new", "this", "typeof", "async", "await", "true",
		"false", "null", "undefined", "public", "private", "protected", "static"},
	".rs": {"fn", "let", "mut", "const", "if", "else", "match", "loop", "while", "for",
		"break", "continue", "return", "struct", "enum", "impl", "trait", "type",
		"pub", "mod", "use", "self", "super", "async", "await", "true", "false"},
}

// FileTreeNode represents a node in the file tree
type FileTreeNode struct {
	Name     string
	Path     string
	IsDir    bool
	Children []*FileTreeNode
	Expanded bool
	Parent   *FileTreeNode
}

// FileTreeModel manages the file tree display
type FileTreeModel struct {
	root    *FileTreeNode
	cursor  int
	items   []*FileTreeNode
	width   int
	height  int
	offsetY int
	focused bool
}

// FileViewerModel manages the file content display
type FileViewerModel struct {
	content      string
	lines        []string
	filename     string
	width        int
	height       int
	scrollY      int
	scrollX      int
	lineNumWidth int
	focused      bool
}

// ViewerComponent manages the file tree and file viewer UI
type ViewerComponent struct {
	fileTree   FileTreeModel
	fileViewer FileViewerModel
	showTree   bool
	treeWidth  int
	width      int
	height     int
	focusTree  bool
	rootPath   string
}

// NewViewerComponent creates a new ViewerComponent
func NewViewerComponent() *ViewerComponent {
	cwd, err := os.Getwd()
	if err != nil {
		homeDir, _ := os.UserHomeDir()
		cwd = homeDir
	}

	vc := &ViewerComponent{
		showTree:  true,
		treeWidth: 35,
		width:     80,
		height:    24,
		focusTree: true,
		rootPath:  cwd,
	}

	vc.fileTree = NewFileTreeModel(cwd)
	vc.fileTree.focused = true
	vc.fileViewer = NewFileViewerModel()

	return vc
}

// NewFileTreeModel creates a new file tree model
func NewFileTreeModel(rootPath string) FileTreeModel {
	m := FileTreeModel{
		width:  35,
		height: 24,
	}
	m.root = m.buildTree(rootPath, nil)
	if m.root != nil {
		m.root.Expanded = true
		m.rebuildItems()
	}
	return m
}

// NewFileViewerModel creates a new file viewer model
func NewFileViewerModel() FileViewerModel {
	return FileViewerModel{
		width:        50,
		height:       24,
		lineNumWidth: 4,
	}
}

// buildTree recursively builds the file tree
func (m *FileTreeModel) buildTree(path string, parent *FileTreeNode) *FileTreeNode {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}

	node := &FileTreeNode{
		Name:     filepath.Base(path),
		Path:     path,
		IsDir:    info.IsDir(),
		Parent:   parent,
		Expanded: false,
		Children: []*FileTreeNode{},
	}

	if node.IsDir {
		entries, err := os.ReadDir(path)
		if err != nil {
			return node
		}

		var children []*FileTreeNode
		for _, entry := range entries {
			name := entry.Name()
			// Skip hidden files except important ones
			if strings.HasPrefix(name, ".") {
				if name != ".git" && name != ".github" && name != ".gitignore" && name != ".env" {
					continue
				}
			}
			// Skip large directories
			if name == "node_modules" || name == "vendor" || name == "__pycache__" || name == ".next" {
				continue
			}
			childPath := filepath.Join(path, name)
			child := m.buildTree(childPath, node)
			if child != nil {
				children = append(children, child)
			}
		}

		sort.Slice(children, func(i, j int) bool {
			if children[i].IsDir != children[j].IsDir {
				return children[i].IsDir
			}
			return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
		})

		node.Children = children
	}

	return node
}

func (m *FileTreeModel) rebuildItems() {
	m.items = nil
	if m.root != nil {
		m.flattenTree(m.root, 0)
	}
}

func (m *FileTreeModel) flattenTree(node *FileTreeNode, depth int) {
	m.items = append(m.items, node)
	if node.Expanded && node.IsDir {
		for _, child := range node.Children {
			m.flattenTree(child, depth+1)
		}
	}
}

func (m *FileTreeModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

func (m *FileTreeModel) Update(msg tea.KeyMsg) {
	key := msg.String()
	switch key {
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
			m.ensureVisible()
		}
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.ensureVisible()
		}
	case "right", "l":
		if m.cursor < len(m.items) {
			item := m.items[m.cursor]
			if item.IsDir && !item.Expanded {
				item.Expanded = true
				m.rebuildItems()
			}
		}
	case "left", "h":
		if m.cursor < len(m.items) {
			item := m.items[m.cursor]
			if item.IsDir && item.Expanded {
				item.Expanded = false
				m.rebuildItems()
			} else if item.Parent != nil && item.Parent != m.root {
				for i, n := range m.items {
					if n == item.Parent {
						m.cursor = i
						m.ensureVisible()
						break
					}
				}
			}
		}
	case "home", "g":
		m.cursor = 0
		m.offsetY = 0
	case "end", "G":
		m.cursor = len(m.items) - 1
		m.ensureVisible()
	}
}

func (m *FileTreeModel) ensureVisible() {
	if m.cursor < m.offsetY {
		m.offsetY = m.cursor
	} else if m.cursor >= m.offsetY+m.height {
		m.offsetY = m.cursor - m.height + 1
	}
}

// View renders the file tree with VS Code styling
func (m *FileTreeModel) View() string {
	var lines []string

	visibleHeight := m.height
	start := m.offsetY
	end := start + visibleHeight
	if end > len(m.items) {
		end = len(m.items)
	}

	sidebarStyle := lipgloss.NewStyle().Background(vscodeSidebarBg)
	selectedStyle := lipgloss.NewStyle().Background(vscodeActiveBg).Foreground(vscodeText)
	normalStyle := lipgloss.NewStyle().Foreground(vscodeText)
	dimStyle := lipgloss.NewStyle().Foreground(vscodeTextDim)

	for i := start; i < end; i++ {
		item := m.items[i]
		depth := m.getDepth(item)

		var line strings.Builder

		// Indentation
		for range depth {
			line.WriteString("  ")
		}

		// Icon and name
		if item.IsDir {
			folderStyle := lipgloss.NewStyle().Foreground(vscodeFolderIcon)
			if item.Expanded {
				line.WriteString(folderStyle.Render("▼ 󰉋 "))
			} else {
				line.WriteString(folderStyle.Render("▶ 󰉋 "))
			}
			line.WriteString(item.Name)
		} else {
			line.WriteString("  ")
			ext := strings.ToLower(filepath.Ext(item.Name))
			if iconInfo, ok := fileTypeIcons[ext]; ok {
				iconStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(iconInfo.color))
				line.WriteString(iconStyle.Render(iconInfo.icon + " "))
			} else {
				iconStyle := lipgloss.NewStyle().Foreground(vscodeFileIcon)
				line.WriteString(iconStyle.Render("󰈙 "))
			}
			line.WriteString(item.Name)
		}

		content := line.String()

		// Ensure exact width by calculating visible width
		visWidth := visibleWidth(content)
		if visWidth < m.width {
			content += strings.Repeat(" ", m.width-visWidth)
		} else if visWidth > m.width {
			content = truncateVisible(content, m.width-1) + "…"
		}

		var styledLine string
		if i == m.cursor {
			if m.focused {
				styledLine = selectedStyle.Render(content)
			} else {
				styledLine = dimStyle.Background(vscodeHoverBg).Render(content)
			}
		} else {
			styledLine = sidebarStyle.Render(normalStyle.Render(content))
		}

		lines = append(lines, styledLine)
	}

	// Fill remaining space
	emptyLine := sidebarStyle.Render(strings.Repeat(" ", m.width))
	for len(lines) < visibleHeight {
		lines = append(lines, emptyLine)
	}

	return strings.Join(lines, "\n")
}

func (m *FileTreeModel) getDepth(node *FileTreeNode) int {
	depth := 0
	current := node.Parent
	for current != nil {
		depth++
		current = current.Parent
	}
	return depth
}

func (m *FileTreeModel) GetSelectedPath() string {
	if m.cursor < len(m.items) {
		return m.items[m.cursor].Path
	}
	return ""
}

func (m *FileTreeModel) IsSelectedDir() bool {
	if m.cursor < len(m.items) {
		return m.items[m.cursor].IsDir
	}
	return false
}

// FileViewerModel methods

func (v *FileViewerModel) SetSize(width, height int) {
	v.width = width
	v.height = height
}

func (v *FileViewerModel) LoadFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	v.filename = path
	v.content = string(content)
	v.lines = strings.Split(v.content, "\n")
	v.scrollY = 0
	v.scrollX = 0

	v.lineNumWidth = len(fmt.Sprintf("%d", len(v.lines))) + 2
	if v.lineNumWidth < 4 {
		v.lineNumWidth = 4
	}

	return nil
}

func (v *FileViewerModel) Update(msg tea.KeyMsg) {
	key := msg.String()
	maxScroll := len(v.lines) - v.height + 2
	if maxScroll < 0 {
		maxScroll = 0
	}

	switch key {
	case "down", "j":
		if v.scrollY < maxScroll {
			v.scrollY++
		}
	case "up", "k":
		if v.scrollY > 0 {
			v.scrollY--
		}
	case "right", "l":
		v.scrollX += 4
	case "left", "h":
		if v.scrollX >= 4 {
			v.scrollX -= 4
		} else {
			v.scrollX = 0
		}
	case "home", "g":
		v.scrollY = 0
	case "end", "G":
		v.scrollY = maxScroll
	case "ctrl+d":
		v.scrollY += v.height / 2
		if v.scrollY > maxScroll {
			v.scrollY = maxScroll
		}
	case "ctrl+u":
		v.scrollY -= v.height / 2
		if v.scrollY < 0 {
			v.scrollY = 0
		}
	}
}

// View renders the file viewer with syntax highlighting
func (v *FileViewerModel) View() string {
	if len(v.lines) == 0 || v.filename == "" {
		return v.renderEmptyState()
	}

	var lines []string
	ext := strings.ToLower(filepath.Ext(v.filename))
	keywords := languageKeywords[ext]

	start := v.scrollY
	end := start + v.height
	if end > len(v.lines) {
		end = len(v.lines)
	}

	contentWidth := v.width - v.lineNumWidth - 1
	if contentWidth < 10 {
		contentWidth = 10
	}

	lineNumStyle := lipgloss.NewStyle().Foreground(vscodeLineNum).Background(vscodeLineNumBg)
	gutterStyle := lipgloss.NewStyle().Foreground(vscodeBorder).Background(vscodeBg)
	codeStyle := lipgloss.NewStyle().Background(vscodeBg)

	for i := start; i < end; i++ {
		lineNum := i + 1
		lineNumStr := fmt.Sprintf("%*d ", v.lineNumWidth-1, lineNum)

		line := v.lines[i]

		if v.scrollX > 0 && len(line) > v.scrollX {
			line = line[v.scrollX:]
		} else if v.scrollX > 0 {
			line = ""
		}

		visWidth := visibleWidth(line)
		if visWidth > contentWidth {
			line = truncateVisible(line, contentWidth-1) + "…"
		}

		highlightedLine := v.highlightLine(line, ext, keywords)

		hlVisWidth := visibleWidth(stripViewerANSI(highlightedLine))
		if hlVisWidth < contentWidth {
			highlightedLine += codeStyle.Render(strings.Repeat(" ", contentWidth-hlVisWidth))
		}

		fullLine := lineNumStyle.Render(lineNumStr) +
			gutterStyle.Render("│") +
			highlightedLine

		lines = append(lines, fullLine)
	}

	emptyLineNum := lineNumStyle.Render(strings.Repeat(" ", v.lineNumWidth))
	emptyContent := codeStyle.Render(strings.Repeat(" ", contentWidth))
	emptyLine := emptyLineNum + gutterStyle.Render("│") + emptyContent

	for len(lines) < v.height {
		lines = append(lines, emptyLine)
	}

	return strings.Join(lines, "\n")
}

func (v *FileViewerModel) renderEmptyState() string {
	var lines []string

	emptyStyle := lipgloss.NewStyle().Foreground(vscodeTextDim).Background(vscodeBg)

	msg := "No file selected"
	hint := "Select a file from the tree"

	for i := 0; i < v.height; i++ {
		var line string
		if i == v.height/2-1 {
			padding := (v.width - len(msg)) / 2
			if padding < 0 {
				padding = 0
			}
			line = strings.Repeat(" ", padding) + msg
		} else if i == v.height/2 {
			padding := (v.width - len(hint)) / 2
			if padding < 0 {
				padding = 0
			}
			line = strings.Repeat(" ", padding) + hint
		} else {
			line = ""
		}

		if len(line) < v.width {
			line += strings.Repeat(" ", v.width-len(line))
		}
		lines = append(lines, emptyStyle.Render(line))
	}

	return strings.Join(lines, "\n")
}

func (v *FileViewerModel) highlightLine(line string, ext string, keywords []string) string {
	if len(line) == 0 {
		return ""
	}

	var result strings.Builder

	keywordStyle := lipgloss.NewStyle().Foreground(vscodeKeyword)
	stringStyle := lipgloss.NewStyle().Foreground(vscodeString)
	commentStyle := lipgloss.NewStyle().Foreground(vscodeComment)
	numberStyle := lipgloss.NewStyle().Foreground(vscodeNumber)
	funcStyle := lipgloss.NewStyle().Foreground(vscodeFunction)
	typeStyle := lipgloss.NewStyle().Foreground(vscodeType)
	normalStyle := lipgloss.NewStyle().Foreground(vscodeText).Background(vscodeBg)
	punctStyle := lipgloss.NewStyle().Foreground(vscodePunctuation)

	commentPrefixes := map[string]string{
		".go": "//", ".js": "//", ".ts": "//", ".c": "//", ".cpp": "//",
		".java": "//", ".rs": "//", ".py": "#", ".rb": "#", ".sh": "#",
		".yaml": "#", ".yml": "#",
	}

	if prefix, ok := commentPrefixes[ext]; ok {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, prefix) {
			return commentStyle.Render(line)
		}
	}

	i := 0
	for i < len(line) {
		ch := line[i]

		if ch == '"' || ch == '\'' || ch == '`' {
			quote := ch
			j := i + 1
			for j < len(line) {
				if line[j] == byte(quote) && (j == i+1 || line[j-1] != '\\') {
					j++
					break
				}
				j++
			}
			result.WriteString(stringStyle.Render(line[i:j]))
			i = j
			continue
		}

		if ch >= '0' && ch <= '9' {
			j := i
			for j < len(line) && ((line[j] >= '0' && line[j] <= '9') || line[j] == '.') {
				j++
			}
			result.WriteString(numberStyle.Render(line[i:j]))
			i = j
			continue
		}

		if isIdentStart(ch) {
			j := i
			for j < len(line) && isIdentPart(line[j]) {
				j++
			}
			word := line[i:j]

			isKw := false
			if slices.Contains(keywords, word) {
				result.WriteString(keywordStyle.Render(word))
				isKw = true
			}

			if !isKw {
				if j < len(line) && line[j] == '(' {
					result.WriteString(funcStyle.Render(word))
				} else if len(word) > 0 && word[0] >= 'A' && word[0] <= 'Z' {
					result.WriteString(typeStyle.Render(word))
				} else {
					result.WriteString(normalStyle.Render(word))
				}
			}
			i = j
			continue
		}

		if isPunctuation(ch) {
			result.WriteString(punctStyle.Render(string(ch)))
		} else {
			result.WriteString(normalStyle.Render(string(ch)))
		}
		i++
	}

	return result.String()
}

func (v *FileViewerModel) SetViewportWidth(width int) {
	v.width = width
}

func (v *FileViewerModel) SetShowCursor(show bool) {
	v.focused = show
}

// ViewerComponent methods

func (vc *ViewerComponent) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()

		switch key {
		case "tab":
			if vc.showTree {
				vc.focusTree = !vc.focusTree
				vc.fileTree.focused = vc.focusTree
				vc.fileViewer.focused = !vc.focusTree
			}
			return nil

		case "ctrl+b":
			vc.showTree = !vc.showTree
			if vc.showTree {
				vc.focusTree = true
				vc.fileTree.focused = true
				vc.fileViewer.focused = false
			} else {
				vc.focusTree = false
				vc.fileTree.focused = false
				vc.fileViewer.focused = true
			}
			vc.updateSizes()
			return nil
		}

		if vc.showTree && vc.focusTree {
			vc.fileTree.Update(msg)

			if key == "enter" {
				selectedPath := vc.fileTree.GetSelectedPath()
				if selectedPath != "" && !vc.fileTree.IsSelectedDir() {
					vc.fileViewer.LoadFile(selectedPath)
					vc.focusTree = false
					vc.fileTree.focused = false
					vc.fileViewer.focused = true
					vc.updateSizes()
				} else if vc.fileTree.IsSelectedDir() {
					item := vc.fileTree.items[vc.fileTree.cursor]
					item.Expanded = !item.Expanded
					vc.fileTree.rebuildItems()
				}
			}
		} else if !vc.focusTree {
			vc.fileViewer.Update(msg)
		}

	case tea.WindowSizeMsg:
		vc.width = msg.Width
		vc.height = msg.Height
		vc.updateSizes()
	}

	return nil
}

func (vc *ViewerComponent) updateSizes() {
	if vc.showTree {
		vc.fileTree.SetSize(vc.treeWidth, vc.height)
		vc.fileViewer.SetSize(vc.width-vc.treeWidth-1, vc.height)
	} else {
		vc.fileViewer.SetSize(vc.width, vc.height)
	}
}

func (vc *ViewerComponent) SetSize(width, height int) {
	vc.width = width
	vc.height = height
	vc.updateSizes()
}

// View renders the component
func (vc *ViewerComponent) View() string {
	if vc.showTree {
		treeView := vc.fileTree.View()
		viewerView := vc.fileViewer.View()

		treeLines := strings.Split(treeView, "\n")
		viewerLines := strings.Split(viewerView, "\n")

		maxLines := vc.height
		for len(treeLines) < maxLines {
			treeLines = append(treeLines, lipgloss.NewStyle().Background(vscodeSidebarBg).Render(strings.Repeat(" ", vc.treeWidth)))
		}
		for len(viewerLines) < maxLines {
			viewerLines = append(viewerLines, lipgloss.NewStyle().Background(vscodeBg).Render(strings.Repeat(" ", vc.width-vc.treeWidth-1)))
		}

		borderStyle := lipgloss.NewStyle().Foreground(vscodeBorder).Background(vscodeBorder)

		var result []string
		for i := range maxLines {
			var treeLine, viewerLine string
			if i < len(treeLines) {
				treeLine = treeLines[i]
			}
			if i < len(viewerLines) {
				viewerLine = viewerLines[i]
			}
			result = append(result, treeLine+borderStyle.Render("│")+viewerLine)
		}

		return strings.Join(result, "\n")
	}

	return vc.fileViewer.View()
}

// Helper functions

func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentPart(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

func isPunctuation(ch byte) bool {
	return ch == '(' || ch == ')' || ch == '[' || ch == ']' || ch == '{' || ch == '}' ||
		ch == ',' || ch == ';' || ch == ':' || ch == '.' || ch == '=' || ch == '+' ||
		ch == '-' || ch == '*' || ch == '/' || ch == '<' || ch == '>' || ch == '!' ||
		ch == '&' || ch == '|' || ch == '^' || ch == '%' || ch == '~' || ch == '?'
}

// visibleWidth calculates the visible width of a string (accounting for ANSI codes)
func visibleWidth(s string) int {
	return utf8.RuneCountInString(stripViewerANSI(s))
}

// truncateVisible truncates a string to a visible width, preserving ANSI codes
func truncateVisible(s string, width int) string {
	if ansi.StringWidth(s) <= width {
		return s
	}
	// Use ansi.Truncate to preserve ANSI escape sequences
	return ansi.Truncate(s, width, "")
}

// stripViewerANSI removes ANSI escape codes
func stripViewerANSI(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}

// App integration methods

func (a *App) renderViewerScreen() string {
	if a.viewerComponent == nil {
		a.viewerComponent = NewViewerComponent()
	}

	availableHeight := a.height - 2
	if availableHeight < 10 {
		availableHeight = 10
	}

	a.viewerComponent.SetSize(a.width, availableHeight)

	// Header
	filePath := "EXPLORER"
	if a.viewerComponent.fileViewer.filename != "" {
		filePath = filepath.Base(a.viewerComponent.fileViewer.filename)
	}

	headerStyle := lipgloss.NewStyle().Background(vscodeStatusBg).Foreground(vscodeStatusFg).Bold(true)

	headerLeft := headerStyle.Render(" " + filePath + " ")

	headerRight := ""
	if a.viewerComponent.fileViewer.filename != "" {
		dir := filepath.Dir(a.viewerComponent.fileViewer.filename)
		if len(dir) > 40 {
			dir = "..." + dir[len(dir)-37:]
		}
		headerRight = headerStyle.Render(" " + dir + " ")
	}

	headerMiddle := a.width - visibleWidth(headerLeft) - visibleWidth(headerRight)
	if headerMiddle < 0 {
		headerMiddle = 0
	}
	header := headerLeft + headerStyle.Render(strings.Repeat(" ", headerMiddle)) + headerRight

	// Main content
	mainView := a.viewerComponent.View()

	// Footer
	footerStyle := lipgloss.NewStyle().Background(lipgloss.Color("#252526")).Foreground(vscodeText)

	focusIndicator := "EXPLORER"
	if !a.viewerComponent.focusTree {
		focusIndicator = "EDITOR"
	}

	posInfo := ""
	if a.viewerComponent.fileViewer.filename != "" {
		lineNum := a.viewerComponent.fileViewer.scrollY + 1
		totalLines := len(a.viewerComponent.fileViewer.lines)
		posInfo = fmt.Sprintf("Ln %d/%d", lineNum, totalLines)
	}

	footerLeft := footerStyle.Render(" " + focusIndicator + " ")
	footerRight := footerStyle.Render(" Tab:Switch  Ctrl+B:Tree  ESC:Back  " + posInfo + " ")

	footerMiddle := a.width - visibleWidth(footerLeft) - visibleWidth(footerRight)
	if footerMiddle < 0 {
		footerMiddle = 0
	}
	footer := footerLeft + footerStyle.Render(strings.Repeat(" ", footerMiddle)) + footerRight

	return header + "\n" + mainView + "\n" + footer
}

func (a *App) handleViewerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "esc" {
		a.screen = ScreenHome
		if animCmd := a.focusHomeInput(); animCmd != nil {
			return a, animCmd
		}
		return a, nil
	}

	if a.viewerComponent != nil {
		cmd := a.viewerComponent.Update(msg)
		if cmd != nil {
			return a, cmd
		}
	}

	return a, nil
}
