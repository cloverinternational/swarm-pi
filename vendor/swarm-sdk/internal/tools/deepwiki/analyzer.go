package deepwiki

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileInfo holds metadata about a discovered file.
type FileInfo struct {
	Path     string // absolute path
	RelPath  string // relative to repo root
	Language string // detected language
	Size     int64
	IsCode   bool
}

// Analyzer walks a repository and extracts code entities from files.
type Analyzer struct {
	// Language-specific parsers registered by file extension.
	parsers map[string]LanguageParser
}

// LanguageParser extracts code entities and edges from a single file.
type LanguageParser interface {
	ParseFile(info FileInfo, source []byte) ([]*CodeEntity, []CodeEdge, error)
}

// NewAnalyzer creates an analyzer with built-in language parsers.
func NewAnalyzer() *Analyzer {
	a := &Analyzer{
		parsers: make(map[string]LanguageParser),
	}
	// Register built-in parsers
	gop := &GoParser{}
	a.parsers[".go"] = gop

	pyp := &GenericParser{lang: "python", funcPatterns: pythonPatterns}
	a.parsers[".py"] = pyp

	tsp := &GenericParser{lang: "typescript", funcPatterns: typescriptPatterns}
	a.parsers[".ts"] = tsp
	a.parsers[".tsx"] = tsp

	jsp := &GenericParser{lang: "javascript", funcPatterns: javascriptPatterns}
	a.parsers[".js"] = jsp
	a.parsers[".jsx"] = jsp

	rsp := &GenericParser{lang: "rust", funcPatterns: rustPatterns}
	a.parsers[".rs"] = rsp

	return a
}

// WalkRepo discovers all indexable files in the repository.
func (a *Analyzer) WalkRepo(repoPath string, excludeDirs, focusDirs []string) ([]FileInfo, error) {
	// Always start with the universal defaults so callers cannot accidentally
	// wipe them out by passing an empty slice.  Caller-supplied dirs are merged
	// on top — additive semantics only.
	excludeSet := make(map[string]bool, len(defaultExcludeDirs)+len(excludeDirs))
	for _, d := range defaultExcludeDirs {
		excludeSet[d] = true
	}
	for _, d := range excludeDirs {
		excludeSet[d] = true
	}

	focusSet := make(map[string]bool, len(focusDirs))
	for _, d := range focusDirs {
		focusSet[d] = true
	}
	useFocus := len(focusSet) > 0

	var files []FileInfo

	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}

		// Skip directories — but never skip the root of the walk itself,
		// even if its name matches an excluded dir (e.g. analysing a repo
		// named "node_modules" or "deepwiki-open" directly).
		if info.IsDir() {
			if path == repoPath {
				return nil
			}
			name := info.Name()
			if excludeSet[name] || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip large files (>1MB)
		if info.Size() > 1<<20 {
			return nil
		}

		// Skip binary/generated files
		if isBinaryExt(filepath.Ext(path)) {
			return nil
		}

		// Skip known junk files by name regardless of extension.
		basename := filepath.Base(path)
		if isJunkFilename(basename) {
			return nil
		}

		rel, _ := filepath.Rel(repoPath, path)

		// Focus dir filter
		if useFocus {
			inFocus := false
			for dir := range focusSet {
				if strings.HasPrefix(rel, dir+"/") || strings.HasPrefix(rel, dir+"\\") || rel == dir {
					inFocus = true
					break
				}
			}
			if !inFocus {
				return nil
			}
		}

		lang := detectLanguage(filepath.Ext(path))
		files = append(files, FileInfo{
			Path:     path,
			RelPath:  rel,
			Language: lang,
			Size:     info.Size(),
			IsCode:   isCodeLanguage(lang),
		})

		return nil
	})

	return files, err
}

// AnalyzeFile runs the appropriate language parser on a file.
// Returns entities and edges, or an error if no parser handles the language.
func (a *Analyzer) AnalyzeFile(f FileInfo) ([]*CodeEntity, []CodeEdge, error) {
	ext := filepath.Ext(f.Path)
	parser, ok := a.parsers[ext]
	if !ok {
		return nil, nil, nil // no parser — not an error, just no entities
	}

	source, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, nil, err
	}

	return parser.ParseFile(f, source)
}

// DetectPrimaryLanguage finds the most common code language in the file list.
func (a *Analyzer) DetectPrimaryLanguage(files []FileInfo) string {
	counts := make(map[string]int)
	for _, f := range files {
		if f.IsCode && f.Language != "" {
			counts[f.Language]++
		}
	}
	if len(counts) == 0 {
		return "unknown"
	}

	type lc struct {
		lang  string
		count int
	}
	var sorted []lc
	for l, c := range counts {
		sorted = append(sorted, lc{l, c})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })
	return sorted[0].lang
}

// --- Language detection helpers ---

var extToLang = map[string]string{
	".go":     "go",
	".py":     "python",
	".js":     "javascript",
	".jsx":    "javascript",
	".ts":     "typescript",
	".tsx":    "typescript",
	".rs":     "rust",
	".java":   "java",
	".c":      "c",
	".cpp":    "cpp",
	".h":      "c",
	".hpp":    "cpp",
	".cs":     "csharp",
	".rb":     "ruby",
	".php":    "php",
	".swift":  "swift",
	".kt":     "kotlin",
	".scala":  "scala",
	".lua":    "lua",
	".sh":     "shell",
	".bash":   "shell",
	".zsh":    "shell",
	".md":     "markdown",
	".rst":    "rst",
	".txt":    "text",
	".json":   "json",
	".yaml":   "yaml",
	".yml":    "yaml",
	".toml":   "toml",
	".xml":    "xml",
	".html":   "html",
	".css":    "css",
	".scss":   "scss",
	".sql":    "sql",
	".proto":  "protobuf",
	".svelte": "svelte",
}

func detectLanguage(ext string) string {
	if lang, ok := extToLang[strings.ToLower(ext)]; ok {
		return lang
	}
	return ""
}

var codeLanguages = map[string]bool{
	"go": true, "python": true, "javascript": true, "typescript": true,
	"rust": true, "java": true, "c": true, "cpp": true, "csharp": true,
	"ruby": true, "php": true, "swift": true, "kotlin": true, "scala": true,
	"lua": true, "shell": true, "sql": true, "protobuf": true, "svelte": true,
}

func isCodeLanguage(lang string) bool {
	return codeLanguages[lang]
}

var binaryExts = map[string]bool{
	// Compiled / native objects
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".o": true,
	".a": true, ".lib": true, ".obj": true, ".class": true, ".jar": true,
	".war": true, ".pyc": true, ".pyo": true, ".wasm": true,
	// Images
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
	".webp": true, ".svg": true, ".bmp": true, ".tiff": true,
	// Archives
	".zip": true, ".gz": true, ".tar": true, ".7z": true, ".rar": true,
	".br": true, ".zst": true,
	// Documents
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	// Media
	".mp3": true, ".mp4": true, ".avi": true, ".mov": true, ".webm": true,
	// Fonts
	".ttf": true, ".otf": true, ".woff": true, ".woff2": true, ".eot": true,
	// Lock / generated / noise
	".lock": true, ".map": true, ".snap": true,
	// Logs / tmp
	".log": true, ".tmp": true, ".bak": true, ".swp": true,
}

func isBinaryExt(ext string) bool {
	return binaryExts[strings.ToLower(ext)]
}

// isJunkFilename returns true for files that are not useful to index regardless
// of their extension: lock files, minified bundles, package manifests for deps,
// generated test outputs, postman collections, and session-artifact MDs.
func isJunkFilename(name string) bool {
	// Exact name matches
	exactSkip := map[string]bool{
		"package-lock.json": true, "bun.lock": true, "yarn.lock": true,
		"pnpm-lock.yaml": true, "Cargo.lock": true, "poetry.lock": true,
		"go.sum": true, "Gemfile.lock": true, "composer.lock": true,
		"CHANGELOG.md": true,
	}
	if exactSkip[name] {
		return true
	}

	lower := strings.ToLower(name)

	// Minified / source-map files
	if strings.HasSuffix(lower, ".min.js") ||
		strings.HasSuffix(lower, ".min.css") ||
		strings.HasSuffix(lower, ".js.map") ||
		strings.HasSuffix(lower, ".css.map") {
		return true
	}

	// Lock-file variants not caught by extension
	if strings.HasSuffix(lower, "-lock.json") {
		return true
	}

	// Postman / Bruno collections
	if strings.HasSuffix(lower, ".postman_collection.json") ||
		strings.HasSuffix(lower, ".bru") {
		return true
	}

	// Session-artifact MDs: uppercase snake-case prefixes agents tend to dump
	for _, pfx := range []string{
		"TOKEN_COUNT_", "SERVERLESS_", "WASM_", "IMAGE_",
		"PARALLEL_SUBAGENT", "WATCHGUARD_", "ENUMERATION_",
		"SESSION_", "DEPLOYMENT_", "FINAL_", "PENTEST_", "THIS_SESSION",
	} {
		if strings.HasPrefix(name, pfx) {
			return true
		}
	}

	// Session-artifact MD suffixes
	for _, sfx := range []string{
		"_summary.md", "_report.md", "_diagnosis.md", "_complete.md",
		"_timeline.md", "_plan.md", "_index.md", "_quickref.md",
		"_reproduction.md", "_annotated.txt",
	} {
		if strings.HasSuffix(lower, sfx) {
			return true
		}
	}

	return false
}
