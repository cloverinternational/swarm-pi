package skills

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// skillLogger is a file-backed logger for diagnostic messages emitted during
// skill discovery (e.g. unloadable skills found on disk). It intentionally
// does NOT use the default stdlib "log" package, which writes to os.Stderr:
// DiscoverSkills runs on the hot path of TUI startup, and any stray stderr
// write corrupts the alternate-screen render (stray lines appear above/over
// the TUI). Diagnostics are instead written to ~/.swarmos/logs/skills.log,
// mirroring the convention used by internal/cache/profiling/logger.go.
var (
	skillLoggerOnce sync.Once
	skillLoggerInst *log.Logger
)

func getSkillLogger() *log.Logger {
	skillLoggerOnce.Do(func() {
		out := io.Discard // never fall back to stderr/stdout
		if home, err := os.UserHomeDir(); err == nil {
			logDir := filepath.Join(home, ".swarmos", "logs")
			if err := os.MkdirAll(logDir, 0o755); err == nil {
				if f, err := os.OpenFile(filepath.Join(logDir, "skills.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
					out = f
				}
			}
		}
		skillLoggerInst = log.New(out, "", log.LstdFlags)
	})
	return skillLoggerInst
}

// ParseSkillMD parses a SKILL.md file and returns metadata, instructions, and hooks.
func ParseSkillMD(path string) (*SkillMetadata, string, *SkillHooks, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to read SKILL.md: %w", err)
	}

	return ParseSkillMDContent(data)
}

// ParseSkillMDContent parses SKILL.md content from bytes.
// Returns the metadata, instructions, and any hooks defined in the frontmatter.
// Frontmatter hooks (YAML `hooks:` section) are parsed alongside the metadata.
func ParseSkillMDContent(data []byte) (*SkillMetadata, string, *SkillHooks, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))

	var frontmatterLines []string
	var contentLines []string
	inFrontmatter := false
	frontmatterDone := false
	lineNum := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineNum++

		// Check for frontmatter delimiters
		if lineNum == 1 && line == "---" {
			inFrontmatter = true
			continue
		}

		if inFrontmatter && line == "---" {
			inFrontmatter = false
			frontmatterDone = true
			continue
		}

		if inFrontmatter {
			frontmatterLines = append(frontmatterLines, line)
		} else if frontmatterDone || lineNum > 1 {
			contentLines = append(contentLines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, "", nil, fmt.Errorf("error reading content: %w", err)
	}

	// Parse frontmatter
	var metadata SkillMetadata
	var hooks SkillHooks
	hasFrontmatterHooks := false

	if len(frontmatterLines) > 0 {
		frontmatterYAML := strings.Join(frontmatterLines, "\n")

		// Parse metadata fields
		if err := yaml.Unmarshal([]byte(frontmatterYAML), &metadata); err != nil {
			// Legacy autogen skills written before factory quoting was fixed
			// have unquoted scalars containing ": " (invalid YAML). Rather than
			// silently dropping those skills — which desynchronizes the registry
			// from the curator's disk walk — first try a structured recovery
			// that quotes the offending flat scalars. Unlike the line-by-line
			// lenient parser, this preserves nested structures (hooks, triggers)
			// so they are not silently discarded.
			recovered := false
			if sanitized, changed := sanitizeFrontmatterYAML(frontmatterLines); changed {
				if err2 := yaml.Unmarshal([]byte(sanitized), &metadata); err2 == nil {
					// Route the hooks parse below through the sanitized YAML too.
					frontmatterYAML = sanitized
					recovered = true
				}
			}
			if !recovered {
				// Last resort: recover flat string fields line by line. Nested
				// structures are not recoverable in this mode.
				lenient, ok := parseFrontmatterLenient(frontmatterLines)
				if !ok {
					return nil, "", nil, fmt.Errorf("failed to parse frontmatter: %w", err)
				}
				metadata = *lenient
			}
		}

		// Try to parse hooks from frontmatter (YAML `hooks:` section)
		var frontmatterHooks struct {
			Hooks *SkillHooks `yaml:"hooks"`
		}
		if err := yaml.Unmarshal([]byte(frontmatterYAML), &frontmatterHooks); err == nil {
			if frontmatterHooks.Hooks != nil {
				hooks = *frontmatterHooks.Hooks
				hasFrontmatterHooks = true
			}
		}
	}

	// Join content
	content := strings.TrimSpace(strings.Join(contentLines, "\n"))

	// Description fallback: if metadata.Description is empty, extract from markdown content.
	// Mirrors Claude Code's extractDescriptionFromMarkdown (src/utils/markdownConfigLoader.ts).
	// Uses the first non-empty paragraph after any H1 heading.
	if metadata.Description == "" && content != "" {
		metadata.Description = extractDescriptionFromMarkdown(content)
	}

	if hasFrontmatterHooks {
		return &metadata, content, &hooks, nil
	}
	return &metadata, content, nil, nil
}

// ParseSkillMDContentWithValidation parses SKILL.md content and validates per agentskills.io spec.
// Returns the metadata, instructions, hooks, and any validation errors (non-fatal errors are warnings).
func ParseSkillMDContentWithValidation(data []byte) (*SkillMetadata, string, *SkillHooks, []ValidationError, error) {
	metadata, content, hooks, err := ParseSkillMDContent(data)
	if err != nil {
		return nil, "", nil, nil, err
	}

	// Validate the metadata
	validationErrors := ValidateMetadata(metadata)

	return metadata, content, hooks, validationErrors, nil
}

// parseFrontmatterLenient recovers flat string fields from frontmatter that
// strict YAML rejected (legacy autogen skills with unquoted ": " in values).
// It only handles single-line "key: value" pairs plus flow-style tags; nested
// structures (hooks, triggers) are not recoverable in this mode. Returns
// ok=false when nothing usable was found so genuinely corrupt files still
// fail loudly.
func parseFrontmatterLenient(lines []string) (*SkillMetadata, bool) {
	md := &SkillMetadata{}
	recognized := 0
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		// Nested-structure keys start deeper than column 0 or have empty
		// values; skip them rather than misparse.
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "name":
			md.Name = unquoteFrontmatterScalar(value)
		case "description":
			md.Description = unquoteFrontmatterScalar(value)
		case "version":
			md.Version = unquoteFrontmatterScalar(value)
		case "author":
			md.Author = unquoteFrontmatterScalar(value)
		case "category":
			md.Category = unquoteFrontmatterScalar(value)
		case "license":
			md.License = unquoteFrontmatterScalar(value)
		case "compatibility":
			md.Compatibility = unquoteFrontmatterScalar(value)
		case "tags":
			trimmed := strings.Trim(value, "[]")
			for _, t := range strings.Split(trimmed, ",") {
				if tag := unquoteFrontmatterScalar(strings.TrimSpace(t)); tag != "" {
					md.Tags = append(md.Tags, tag)
				}
			}
		default:
			continue
		}
		recognized++
	}
	// Require at least a name or description — otherwise this wasn't
	// skill frontmatter at all.
	if recognized == 0 || (md.Name == "" && md.Description == "") {
		return nil, false
	}
	return md, true
}

// sanitizeFrontmatterYAML repairs legacy frontmatter that strict YAML rejects
// by double-quoting unquoted flat (column-0) scalar values. Legacy autogen
// skills commonly contain an unquoted "description:" whose value embeds ": ",
// which YAML misreads as a nested mapping. Quoting the value makes the whole
// document parse again — crucially preserving nested structures such as
// "hooks:" / "triggers:" that the line-by-line lenient parser cannot recover.
//
// Only top-level scalar lines are altered. Indented (nested) lines, mapping/
// sequence openers (empty value), comments, and values that are already quoted
// or use flow/block indicators are left untouched. Returns changed=false when
// no line needed quoting, so callers can skip a redundant re-parse.
func sanitizeFrontmatterYAML(lines []string) (string, bool) {
	changed := false
	out := make([]string, len(lines))
	for i, raw := range lines {
		out[i] = raw
		// Nested lines (any leading whitespace) belong to structures we must
		// not reflow; leave them verbatim.
		if raw == "" || raw[0] == ' ' || raw[0] == '\t' {
			continue
		}
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := strings.Cut(raw, ":")
		if !ok {
			continue
		}
		val := strings.TrimSpace(value)
		// Empty value → mapping/sequence opener (e.g. "hooks:"); keep as-is.
		if val == "" {
			continue
		}
		// Already quoted or a flow/block/anchor/tag indicator → assume valid.
		switch val[0] {
		case '"', '\'', '[', '{', '|', '>', '&', '*', '!', '#':
			continue
		}
		// Only rewrite values that actually break plain-scalar YAML. The
		// dominant real-world failure is an embedded ": " (colon+space) that
		// YAML reads as a nested mapping; also guard trailing ":" and inline
		// " #" comment starts and tabs. Clean scalars are left untouched so a
		// document that never needed repair reports changed=false.
		if !(strings.Contains(val, ": ") || strings.HasSuffix(val, ":") ||
			strings.Contains(val, " #") || strings.ContainsRune(val, '\t')) {
			continue
		}
		// Quote the scalar, escaping backslashes and double quotes.
		escaped := strings.ReplaceAll(val, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		out[i] = key + ": \"" + escaped + "\""
		changed = true
	}
	if !changed {
		return "", false
	}
	return strings.Join(out, "\n"), true
}

// unquoteFrontmatterScalar strips a level of YAML quoting from a scalar
// recovered by the lenient parser.
func unquoteFrontmatterScalar(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var b strings.Builder
		inner := s[1 : len(s)-1]
		for i := 0; i < len(inner); i++ {
			if inner[i] == '\\' && i+1 < len(inner) {
				i++
				switch inner[i] {
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(inner[i])
				}
				continue
			}
			b.WriteByte(inner[i])
		}
		return b.String()
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}

// LoadSkill loads a complete skill from a directory.
// This is a convenience wrapper that ignores validation warnings and hooks.
// Use LoadSkillWithValidation for full validation and hooks support.
func LoadSkill(skillDir string) (*Skill, error) {
	skill, _, err := LoadSkillWithValidation(skillDir)
	return skill, err
}

// LoadSkillWithValidation loads a complete skill from a directory with validation.
// Returns the skill, any validation errors/warnings, and any fatal parsing errors.
// Non-fatal validation errors are returned but don't prevent loading.
func LoadSkillWithValidation(skillDir string) (*Skill, []ValidationError, error) {
	// Check for SKILL.md
	skillMDPath := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(skillMDPath); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("SKILL.md not found in %s", skillDir)
	}

	// Read and parse SKILL.md with validation
	data, err := os.ReadFile(skillMDPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read SKILL.md: %w", err)
	}

	metadata, instructions, frontmatterHooks, validationErrors, err := ParseSkillMDContentWithValidation(data)
	if err != nil {
		return nil, nil, err
	}

	// Check for fatal validation errors
	for _, ve := range validationErrors {
		if ve.Fatal {
			return nil, validationErrors, fmt.Errorf("validation error: %s", ve.Error())
		}
	}

	skill := &Skill{
		Metadata:      *metadata,
		Instructions:  instructions,
		Path:          skillDir,
		ContentLoaded: true, // full instructions parsed from disk
	}

	// Set frontmatter hooks first (if present in YAML)
	if frontmatterHooks != nil {
		skill.Hooks = frontmatterHooks
	}

	// Load scripts
	scriptsDir := filepath.Join(skillDir, "scripts")
	if entries, err := os.ReadDir(scriptsDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				script := SkillScript{
					Name:     entry.Name(),
					Path:     filepath.Join("scripts", entry.Name()),
					Language: detectLanguage(entry.Name()),
				}
				skill.Scripts = append(skill.Scripts, script)
			}
		}
	}

	// Load references
	refsDir := filepath.Join(skillDir, "references")
	if entries, err := os.ReadDir(refsDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				ref := SkillReference{
					Name:   entry.Name(),
					Path:   filepath.Join("references", entry.Name()),
					Format: detectFormat(entry.Name()),
				}
				skill.References = append(skill.References, ref)
			}
		}
	}

	// Load assets
	assetsDir := filepath.Join(skillDir, "assets")
	if entries, err := os.ReadDir(assetsDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				asset := SkillAsset{
					Name:     entry.Name(),
					Path:     filepath.Join("assets", entry.Name()),
					MimeType: detectMimeType(entry.Name()),
				}
				skill.Assets = append(skill.Assets, asset)
			}
		}
	}

	// Load hooks configuration from hooks.json file (merges with frontmatter hooks).
	// Frontmatter hooks take precedence for overlapping event types; hooks.json
	// fills in any event types not already set by frontmatter.
	hooksPath := filepath.Join(skillDir, "hooks.json")
	if hooksData, err := os.ReadFile(hooksPath); err == nil {
		var jsonHooks SkillHooks
		if err := json.Unmarshal(hooksData, &jsonHooks); err == nil {
			if skill.Hooks == nil {
				// No frontmatter hooks — use hooks.json entirely
				skill.Hooks = &jsonHooks
			} else {
				// Merge: hooks.json fills in event types not set by frontmatter
				if len(jsonHooks.PreToolUse) > 0 && len(skill.Hooks.PreToolUse) == 0 {
					skill.Hooks.PreToolUse = jsonHooks.PreToolUse
				}
				if len(jsonHooks.PostToolUse) > 0 && len(skill.Hooks.PostToolUse) == 0 {
					skill.Hooks.PostToolUse = jsonHooks.PostToolUse
				}
				if len(jsonHooks.Stop) > 0 && len(skill.Hooks.Stop) == 0 {
					skill.Hooks.Stop = jsonHooks.Stop
				}
				if len(jsonHooks.SessionStart) > 0 && len(skill.Hooks.SessionStart) == 0 {
					skill.Hooks.SessionStart = jsonHooks.SessionStart
				}
			}
		}
	}

	return skill, validationErrors, nil
}

// skipDirs contains directory names that should never be traversed during
// skill discovery. Most are common tooling artifact directories that never
// contain a SKILL.md and can be extremely large. "archive" is different: it
// DOES contain SKILL.md files, but those are skills the autogenskills curator
// has retired (see autogenskills/curator.go, which moves unused skills into
// <root>/archive/ and itself skips that directory when reviewing). Discovery
// must skip it too, otherwise every archived skill is re-injected into the
// system prompt and the curator's archiving reclaims zero context.
var skipDirs = map[string]struct{}{
	"node_modules": {},
	".git":         {},
	".svn":         {},
	".hg":          {},
	"venv":         {},
	".venv":        {},
	"__pycache__":  {},
	".mypy_cache":  {},
	".ruff_cache":  {},
	"dist":         {},
	"build":        {},
	".next":        {},
	".nuxt":        {},
	".output":      {},
	"vendor":       {},
	".cache":       {},
	".tmp":         {},
	"tmp":          {},
	"archive":      {},
}

// DiscoverSkills finds all skills in a directory.
// Common tooling artifact directories (node_modules, .git, venv, etc.) are
// skipped automatically to avoid slow walks and accidental matches.
//
// Symlink-following: filepath.Walk does NOT descend into symlinked directories.
// We use a custom recursive walk that follows directory symlinks so that skills
// installed as symlinks (e.g. ~/.claude/skills/clinear -> /path/to/clinear)
// are fully discovered. Cycle detection prevents infinite loops.
//
// Deduplication: If the same SKILL.md is reachable through different paths
// (e.g., via symlinks or overlapping parent directories), it is only loaded
// once. Canonical paths are resolved via filepath.EvalSymlinks.
// Mirrors src/skills/loadSkillsDir.ts:107-124 in Claude Code.
func DiscoverSkills(rootDir string) ([]*Skill, error) {
	var skills []*Skill
	seenPaths := make(map[string]bool) // Canonical SKILL.md path dedup
	seenDirs := make(map[string]bool)  // Real dir paths visited (cycle guard)

	var walk func(dir string) error
	walk = func(dir string) error {
		// Resolve the real directory path to detect symlink cycles
		realDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return nil // Skip unresolvable paths
		}
		if seenDirs[realDir] {
			return nil // Already visited this real dir — cycle or duplicate
		}
		seenDirs[realDir] = true

		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil // Skip unreadable directories
		}

		for _, entry := range entries {
			name := entry.Name()
			fullPath := filepath.Join(dir, name)

			// For directories and symlinks, check if we should descend
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				// Resolve to get real info (follows the symlink)
				info, statErr := os.Stat(fullPath)
				if statErr != nil || !info.IsDir() {
					continue // Not a directory (or broken symlink), skip
				}

				// Skip known tooling artifact directories
				if _, skip := skipDirs[name]; skip {
					continue
				}
				// Skip hidden directories (starting with '.'), except the root
				if dir != rootDir && len(name) > 0 && name[0] == '.' {
					continue
				}

				// Recurse (follows symlinked directories)
				_ = walk(fullPath)
				continue
			}

			// Regular file: check for SKILL.md
			if name != "SKILL.md" {
				continue
			}

			// Dedup by canonical path (handles multiple symlinks to same file)
			canonical, evalErr := filepath.EvalSymlinks(fullPath)
			if evalErr != nil {
				canonical = fullPath
			}
			if seenPaths[canonical] {
				continue // Already loaded via a different path
			}
			seenPaths[canonical] = true

			skillDir := filepath.Dir(fullPath)
			skill, loadErr := LoadSkill(skillDir)
			if loadErr == nil {
				skills = append(skills, skill)
			} else {
				// Never drop a skill silently: an unloadable skill on disk
				// is invisible to SkillManage/the registry while the
				// autogenskills curator (which walks the filesystem
				// directly) still sees it — the resulting split-brain broke
				// skill consolidation for months before anyone noticed.
				getSkillLogger().Printf("skills: skipping unloadable skill at %s: %v", skillDir, loadErr)
			}
		}
		return nil
	}

	_ = walk(rootDir)
	return skills, nil
}

func detectLanguage(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".py":
		return "python"
	case ".sh", ".bash":
		return "bash"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".go":
		return "go"
	case ".rb":
		return "ruby"
	default:
		return ""
	}
}

func detectFormat(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".md":
		return "markdown"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".txt":
		return "text"
	default:
		return ""
	}
}

func detectMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".json":
		return "application/json"
	case ".html":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	default:
		return "application/octet-stream"
	}
}

// extractDescriptionFromMarkdown extracts a description from markdown content
// when the frontmatter description field is missing. It returns the first
// non-empty paragraph after any H1 heading, trimmed and truncated to 256 chars.
// Mirrors Claude Code's extractDescriptionFromMarkdown
// (src/utils/markdownConfigLoader.ts).
func extractDescriptionFromMarkdown(content string) string {
	lines := strings.Split(content, "\n")
	skippedHeading := false
	var paragraphLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip the first H1 heading
		if !skippedHeading && strings.HasPrefix(trimmed, "# ") {
			skippedHeading = true
			continue
		}

		// Skip other headings
		if strings.HasPrefix(trimmed, "#") && len(trimmed) > 0 && trimmed[0] == '#' {
			if len(paragraphLines) > 0 {
				break // End of first paragraph
			}
			continue
		}

		// Empty line marks end of paragraph
		if trimmed == "" {
			if len(paragraphLines) > 0 {
				break
			}
			continue
		}

		paragraphLines = append(paragraphLines, trimmed)
	}

	if len(paragraphLines) == 0 {
		return ""
	}

	desc := strings.Join(paragraphLines, " ")
	// Truncate to 256 chars with ellipsis
	if len(desc) > 256 {
		desc = desc[:253] + "\u2026"
	}
	return desc
}
