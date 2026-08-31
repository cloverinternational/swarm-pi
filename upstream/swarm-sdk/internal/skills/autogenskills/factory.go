// Package autogenskills implements the Hermes-style closed learning loop for
// the Swarm SDK skill system.
package autogenskills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// skillFrontmatter holds parsed YAML frontmatter from a SKILL.md.
type skillFrontmatter struct {
	Name          string
	Description   string
	Version       string
	Author        string
	Category      string
	Tags          string // raw string like "[test, e2e]"
	TriggerReason string
}

// yamlPlainScalar matches values that are safe to emit unquoted in YAML:
// simple tokens with no indicators, no leading/trailing spaces, and no
// characters that could start a flow collection, comment, anchor, or mapping.
var yamlPlainScalar = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._/-]*$`)

// yamlScalar renders s as a single-line YAML scalar that is always safe to
// place after "key: ". Values that aren't trivially plain are double-quoted
// with full escaping (backslashes, quotes, newlines, tabs), which is valid
// YAML in every parser. This is what keeps factory-written SKILL.md files
// readable by the strict registry loader — unquoted descriptions containing
// ": " used to make skills invisible to SkillManage while the curator's disk
// walk still saw them.
func yamlScalar(s string) string {
	if s != "" && yamlPlainScalar.MatchString(s) &&
		!strings.HasSuffix(s, " ") && !strings.Contains(s, "  ") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// unquoteScalar reverses yamlScalar for the lenient line-based parser:
// double-quoted values are unescaped; single-quoted values have their
// doubled quotes collapsed; plain values pass through.
func unquoteScalar(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		inner := s[1 : len(s)-1]
		var b strings.Builder
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

// parseSkillMarkdown reads a SKILL.md file and returns the parsed frontmatter and body.
func parseSkillMarkdown(content string) (skillFrontmatter, string, error) {
	fm := skillFrontmatter{}

	// Find the --- delimiters
	if !strings.HasPrefix(content, "---") {
		return fm, content, nil // no frontmatter
	}

	// Find the closing ---
	end := strings.Index(content[3:], "---")
	if end == -1 {
		return fm, content, nil // no closing delimiter
	}

	fmContent := strings.TrimSpace(content[3 : end+3])
	body := strings.TrimSpace(content[end+6:]) // skip closing ---\n

	// Parse simple key: value pairs (no nested YAML)
	lines := strings.SplitSeq(fmContent, "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		before, after, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.TrimSpace(before)
		value := strings.TrimSpace(after)

		switch key {
		case "name":
			fm.Name = unquoteScalar(value)
		case "description":
			fm.Description = unquoteScalar(value)
		case "version":
			fm.Version = unquoteScalar(value)
		case "author":
			fm.Author = unquoteScalar(value)
		case "category":
			fm.Category = unquoteScalar(value)
		case "tags":
			fm.Tags = value
		case "trigger_reason":
			fm.TriggerReason = unquoteScalar(value)
		}
	}

	return fm, body, nil
}

// bumpVersion increments the patch version: "1.0.0" → "1.0.1", "1.2.3" → "1.2.4"
func bumpVersion(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return version // can't parse, return as-is
	}
	// Parse the patch number
	patch := 0
	fmt.Sscanf(parts[2], "%d", &patch)
	patch++
	return fmt.Sprintf("%s.%s.%d", parts[0], parts[1], patch)
}

// parseTagsFromString parses a tags string like "[test, e2e]" or "test, e2e" into a []string.
// Quoted elements (written by yamlScalar) are unquoted.
func parseTagsFromString(tagsStr string) []string {
	tagsStr = strings.Trim(tagsStr, "[]")
	if tagsStr == "" {
		return nil
	}
	var tags []string
	for t := range strings.SplitSeq(tagsStr, ",") {
		if trimmed := strings.TrimSpace(t); trimmed != "" {
			tags = append(tags, unquoteScalar(trimmed))
		}
	}
	return tags
}

// buildTagsLine renders a YAML flow sequence with each element safely quoted.
func buildTagsLine(tags []string) string {
	quoted := make([]string, 0, len(tags))
	for _, t := range tags {
		quoted = append(quoted, yamlScalar(t))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// buildSkillMarkdownFromFrontmatter assembles SKILL.md from parsed frontmatter + body.
func buildSkillMarkdownFromFrontmatter(fm skillFrontmatter, instructions string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("name: %s\n", yamlScalar(fm.Name)))
	b.WriteString(fmt.Sprintf("description: %s\n", yamlScalar(fm.Description)))
	b.WriteString(fmt.Sprintf("version: %s\n", yamlScalar(fm.Version)))
	b.WriteString(fmt.Sprintf("author: %s\n", yamlScalar(fm.Author)))
	if fm.Category != "" {
		b.WriteString(fmt.Sprintf("category: %s\n", yamlScalar(fm.Category)))
	}
	if fm.Tags != "" {
		b.WriteString(fmt.Sprintf("tags: %s\n", buildTagsLine(parseTagsFromString(fm.Tags))))
	}
	if fm.TriggerReason != "" {
		b.WriteString(fmt.Sprintf("trigger_reason: %s\n", yamlScalar(fm.TriggerReason)))
	}
	b.WriteString("---\n\n")
	b.WriteString(instructions)
	b.WriteString("\n")
	return b.String()
}

// SkillFactory is the only supported way to create autogenerated skills.
//
// CONTRACT:
//   - Construct via NewSkillFactory; zero value is NOT usable.
//   - All creations validate CreateOptions before any disk I/O.
//   - Created skills are immediately registered in the provided Registry.
//   - The factory never mutates Config after construction.
//   - Disk writes use 0755 dirs / 0644 files for safe permissions.
type SkillFactory struct {
	cfg      Config
	registry *skills.Registry
	metrics  *Metrics
}

// NewSkillFactory creates a factory. cfg must be validated before passing.
func NewSkillFactory(cfg Config, registry *skills.Registry, metrics *Metrics) (*SkillFactory, error) {
	if !cfg.IsEnabled() {
		return nil, fmt.Errorf("autogenskills: factory requires enabled config (mode is %q)", cfg.Mode)
	}
	if cfg.AutogenDir == "" {
		return nil, fmt.Errorf("autogenskills: factory requires AutogenDir")
	}
	if registry == nil {
		return nil, fmt.Errorf("autogenskills: factory requires non-nil registry")
	}
	return &SkillFactory{
		cfg:      cfg,
		registry: registry,
		metrics:  metrics,
	}, nil
}

// Create writes a new skill to disk and registers it.
// CONTRACT:
//   - Returns CreationResult with Error set on any validation or I/O failure.
//   - On success, Skill and Path are populated, Error is nil.
//   - Increments SkillCreatedCount in metrics if non-nil.
func (f *SkillFactory) Create(opts CreateOptions) CreationResult {
	if err := opts.Validate(); err != nil {
		return CreationResult{Error: err}
	}
	if minimum := f.cfg.Trigger.MinInstructionsLength; minimum > 0 && utf8.RuneCountInString(opts.Instructions) < minimum {
		return CreationResult{Error: fmt.Errorf("autogenskills: instructions must contain at least %d characters under the configured policy", minimum)}
	}

	skillDir := filepath.Join(f.cfg.AutogenDir, opts.Name)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		return CreationResult{Error: fmt.Errorf(
			"autogenskills: skill %q already exists; view and patch the existing skill instead",
			opts.Name,
		)}
	} else if !os.IsNotExist(err) {
		return CreationResult{Error: fmt.Errorf("autogenskills: inspect %s: %w", skillPath, err)}
	}
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return CreationResult{Error: fmt.Errorf("autogenskills: mkdir %s: %w", skillDir, err)}
	}

	content := buildSkillMarkdown(opts)

	if err := atomicWrite(skillPath, []byte(content), 0o644); err != nil {
		return CreationResult{Error: fmt.Errorf("autogenskills: write %s: %w", skillPath, err)}
	}

	skill := &skills.Skill{
		Metadata: skills.SkillMetadata{
			Name:        opts.Name,
			Description: opts.Description,
			Tags:        opts.Tags,
			Category:    opts.Category,
			Author:      "swarm-autogen",
			Version:     "1.0.0",
			WhenToUse:   fmt.Sprintf("Autogenerated skill for %s", opts.TriggerReason.String()),
		},
		Instructions:  opts.Instructions,
		Path:          skillDir,
		LoadedAt:      time.Now(),
		Source:        "autogen",
		LoadedFrom:    "autogen",
		ContentLoaded: true, // instructions set directly, no lazy-load needed
	}

	f.registry.RegisterSkill(skill, true)

	if f.metrics != nil {
		f.metrics.IncrementSkillCreated()
	}

	return CreationResult{
		Skill:     skill,
		Path:      skillPath,
		CreatedAt: skill.LoadedAt,
	}
}

// Patch modifies an existing skill's content and bumps its version.
// CONTRACT: Returns PatchResult with Error set on any failure.
// On success, the skill is updated in the registry and on disk.
func (f *SkillFactory) Patch(opts PatchOptions) PatchResult {
	if !isSafeSkillDirName(opts.Name) {
		return PatchResult{Error: fmt.Errorf("autogenskills: patch name %q must match ^[a-z0-9]+(-[a-z0-9]+)*$ and not be reserved", opts.Name)}
	}

	// Read existing SKILL.md
	skillPath := filepath.Join(f.cfg.AutogenDir, opts.Name, "SKILL.md")
	content, err := os.ReadFile(skillPath)
	if err != nil {
		return PatchResult{Error: fmt.Errorf("autogenskills: read %s: %w", skillPath, err)}
	}

	// Parse existing content
	fm, body, err := parseSkillMarkdown(string(content))
	if err != nil {
		return PatchResult{Error: fmt.Errorf("autogenskills: parse %s: %w", skillPath, err)}
	}
	if fm.Name != opts.Name {
		return PatchResult{Error: fmt.Errorf("autogenskills: SKILL.md name %q does not match package name %q", fm.Name, opts.Name)}
	}

	previousVersion := fm.Version

	// Apply changes
	newInstructions := body
	if opts.Instructions != "" {
		if opts.AppendInstructions {
			newInstructions = body + "\n\n" + opts.Instructions
		} else {
			newInstructions = opts.Instructions
		}
	}

	if opts.Description != "" {
		fm.Description = opts.Description
	}

	// Add any new tags
	if len(opts.Tags) > 0 {
		existingTags := fm.Tags
		for _, tag := range opts.Tags {
			if !strings.Contains(existingTags, tag) {
				if existingTags != "" && !strings.HasSuffix(existingTags, ",") && !strings.HasSuffix(existingTags, ", ") {
					existingTags += ", "
				}
				existingTags += tag
			}
		}
		fm.Tags = existingTags
	}

	// Bump version
	newVersion := bumpVersion(fm.Version)
	fm.Version = newVersion

	// Reconstruct the SKILL.md
	newContent := buildSkillMarkdownFromFrontmatter(fm, newInstructions)

	// Write back to disk
	if err := atomicWrite(skillPath, []byte(newContent), 0o644); err != nil {
		return PatchResult{Error: fmt.Errorf("autogenskills: write %s: %w", skillPath, err)}
	}

	// Update the skill in the registry
	skill := &skills.Skill{
		Metadata: skills.SkillMetadata{
			Name:        fm.Name,
			Description: fm.Description,
			Tags:        parseTagsFromString(fm.Tags),
			Category:    fm.Category,
			Author:      fm.Author,
			Version:     newVersion,
		},
		Instructions:  newInstructions,
		Path:          filepath.Join(f.cfg.AutogenDir, fm.Name),
		LoadedAt:      time.Now(),
		Source:        "autogen",
		LoadedFrom:    "autogen",
		ContentLoaded: true,
	}

	f.registry.RegisterSkill(skill, true) // overwrite=true

	if f.metrics != nil {
		f.metrics.IncrementSkillPatched()
	}

	return PatchResult{
		Skill:           skill,
		Path:            skillPath,
		PreviousVersion: previousVersion,
		NewVersion:      newVersion,
	}
}

// buildSkillMarkdown assembles the SKILL.md file content.
func buildSkillMarkdown(opts CreateOptions) string {
	var b strings.Builder

	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("name: %s\n", yamlScalar(opts.Name)))
	b.WriteString(fmt.Sprintf("description: %s\n", yamlScalar(opts.Description)))
	b.WriteString("version: 1.0.0\n")
	b.WriteString("author: swarm-autogen\n")
	if opts.Category != "" {
		b.WriteString(fmt.Sprintf("category: %s\n", yamlScalar(opts.Category)))
	}
	if len(opts.Tags) > 0 {
		b.WriteString(fmt.Sprintf("tags: %s\n", buildTagsLine(opts.Tags)))
	}
	b.WriteString(fmt.Sprintf("trigger_reason: %s\n", yamlScalar(opts.TriggerReason.String())))
	if opts.LLMProvider != "" {
		b.WriteString(fmt.Sprintf("llm_provider: %s\n", yamlScalar(opts.LLMProvider)))
	}
	b.WriteString("---\n\n")
	b.WriteString(opts.Instructions)
	b.WriteString("\n")

	return b.String()
}
