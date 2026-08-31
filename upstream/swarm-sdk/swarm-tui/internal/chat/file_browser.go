package chat

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileItem represents a file or folder in the file browser
type FileItem struct {
	Name  string
	Path  string
	IsDir bool
	Icon  string
}

// FileBrowser handles hierarchical file browsing for @-mentions
type FileBrowser struct {
	Active          bool
	Query           string
	Items           []FileItem
	SelectedIdx     int
	MentionStartPos int
	CurrentPath     string
	PathStack       []string
}

// NewFileBrowser creates a new file browser
func NewFileBrowser() *FileBrowser {
	return &FileBrowser{
		CurrentPath: ".",
		PathStack:   []string{},
	}
}

// Activate starts file browsing at the given mention position
func (fb *FileBrowser) Activate(mentionPos int) {
	fb.Active = true
	fb.MentionStartPos = mentionPos
	fb.SelectedIdx = 0
	fb.CurrentPath = "."
	fb.PathStack = []string{}
}

// Deactivate stops file browsing
func (fb *FileBrowser) Deactivate() {
	fb.Active = false
	fb.Items = nil
	fb.Query = ""
}

// GetFileIcon returns an emoji icon for the file type
func GetFileIcon(item FileItem) string {
	if item.IsDir {
		if item.Name == ".." {
			return "⬆️"
		}
		return "📁"
	}

	ext := strings.ToLower(filepath.Ext(item.Name))
	switch ext {
	case ".go":
		return "🔵"
	case ".js", ".ts", ".jsx", ".tsx":
		return "🟡"
	case ".py":
		return "🐍"
	case ".md", ".txt":
		return "📄"
	case ".json", ".yaml", ".yml", ".toml":
		return "⚙️"
	case ".sh", ".bash":
		return "🔧"
	case ".css", ".scss", ".sass":
		return "🎨"
	case ".html", ".htm":
		return "🌐"
	case ".jpg", ".jpeg", ".png", ".gif", ".svg", ".webp":
		return "🖼️"
	case ".pdf":
		return "📕"
	case ".zip", ".tar", ".gz":
		return "📦"
	default:
		return "📄"
	}
}

// UpdateItems refreshes the file list for current directory and query
func (fb *FileBrowser) UpdateItems(query string) error {
	fb.Query = query

	items, err := fb.listDirectory(fb.CurrentPath)
	if err != nil {
		return err
	}

	// Filter by query
	queryLower := strings.ToLower(query)
	var matches []FileItem

	for _, item := range items {
		nameLower := strings.ToLower(item.Name)

		// Match on filename
		if query == "" || strings.Contains(nameLower, queryLower) {
			item.Icon = GetFileIcon(item)
			matches = append(matches, item)
		}
	}

	fb.Items = matches

	// Reset selection if out of bounds
	if fb.SelectedIdx >= len(matches) {
		fb.SelectedIdx = 0
	}

	return nil
}

// listDirectory returns files and folders in the given directory
func (fb *FileBrowser) listDirectory(dir string) ([]FileItem, error) {
	var items []FileItem

	// Get absolute path
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	// Add ".." if not at root
	if dir != "." && absDir != "/" {
		items = append(items, FileItem{
			Name:  "..",
			Path:  filepath.Dir(absDir),
			IsDir: true,
			Icon:  "⬆️",
		})
	}

	// Read directory
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}

	// Sort: directories first, then files
	var dirs []FileItem
	var files []FileItem

	for _, entry := range entries {
		name := entry.Name()

		// Skip hidden files and common patterns
		if strings.HasPrefix(name, ".") ||
			name == "node_modules" ||
			name == "vendor" ||
			name == "__pycache__" ||
			name == "dist" ||
			name == "build" {
			continue
		}

		fullPath := filepath.Join(absDir, name)
		item := FileItem{
			Name:  name,
			Path:  fullPath,
			IsDir: entry.IsDir(),
		}

		if entry.IsDir() {
			dirs = append(dirs, item)
		} else {
			files = append(files, item)
		}
	}

	// Sort both lists
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	// Combine: directories first, then files
	items = append(items, dirs...)
	items = append(items, files...)

	return items, nil
}

// NavigateInto enters a directory or returns the selected file path
func (fb *FileBrowser) NavigateInto() (filePath string, isFile bool) {
	if fb.SelectedIdx < 0 || fb.SelectedIdx >= len(fb.Items) {
		return "", false
	}

	selectedItem := fb.Items[fb.SelectedIdx]

	if selectedItem.IsDir {
		// Navigate into directory
		if selectedItem.Name == ".." {
			// Go back
			fb.CurrentPath = selectedItem.Path
			if len(fb.PathStack) > 0 {
				fb.PathStack = fb.PathStack[:len(fb.PathStack)-1]
			}
		} else {
			// Go into folder
			fb.PathStack = append(fb.PathStack, fb.CurrentPath)
			fb.CurrentPath = selectedItem.Path
		}
		fb.SelectedIdx = 0
		fb.UpdateItems(fb.Query)
		return "", false
	}

	// File selected - return path
	cwd, _ := os.Getwd()
	relPath, err := filepath.Rel(cwd, selectedItem.Path)
	if err != nil {
		relPath = selectedItem.Path
	}

	return relPath, true
}

// NavigateBack goes to parent directory
func (fb *FileBrowser) NavigateBack() {
	if len(fb.PathStack) > 0 {
		fb.CurrentPath = fb.PathStack[len(fb.PathStack)-1]
		fb.PathStack = fb.PathStack[:len(fb.PathStack)-1]
		fb.SelectedIdx = 0
		fb.UpdateItems(fb.Query)
	}
}

// MoveSelection moves the selected item up or down
func (fb *FileBrowser) MoveSelection(delta int) {
	newIdx := fb.SelectedIdx + delta
	if newIdx < 0 {
		newIdx = 0
	}
	if newIdx >= len(fb.Items) {
		newIdx = len(fb.Items) - 1
	}
	fb.SelectedIdx = newIdx
}

// GetCurrentDir returns a user-friendly current directory string
func (fb *FileBrowser) GetCurrentDir() string {
	if fb.CurrentPath == "." {
		return "./"
	}
	cwd, _ := os.Getwd()
	rel, err := filepath.Rel(cwd, fb.CurrentPath)
	if err != nil {
		return fb.CurrentPath
	}
	return rel + "/"
}
