package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"time"
)

// builtinFS holds the embedded skill directories under skills/builtins/.
// Every subdirectory that contains a SKILL.md is treated as a built-in skill.
//
// IMPORTANT: Go's embed package excludes files/directories whose names begin
// with '.' or '_', but it does NOT exclude directories like node_modules or
// vendor. The builtins/ tree must therefore be kept lean: no npm, pip, or Go
// vendor directories may be placed under builtins/. CI should enforce this.
//
//go:embed builtins
var builtinFS embed.FS

// RegisterDefaultSkills loads all built-in skills from the embedded filesystem
// and registers them into the provided Registry.
//
// Built-in skills use the path "builtin:<name>" so they are clearly
// distinguishable from user-installed skills in logs and the UI.
//
// Built-in skills are registered with overwrite=false so that a user who
// installs a skill with the same name in ~/.swarm/skills/ will have their
// version take precedence once DiscoverAll runs afterward.
func RegisterDefaultSkills(r *Registry) error {
	skills, err := loadEmbeddedSkills()
	if err != nil {
		return fmt.Errorf("loading built-in skills: %w", err)
	}
	for _, skill := range skills {
		r.RegisterSkill(skill, false)
	}

	// Register programmatic builtin skills
	r.RegisterSkill(loopSkill(), false)

	return nil
}

// loadEmbeddedSkills walks the embedded builtins/ tree and parses every
// directory that contains a SKILL.md into a Skill struct.
func loadEmbeddedSkills() ([]*Skill, error) {
	var loaded []*Skill

	// Walk the top-level entries under builtins/
	entries, err := builtinFS.ReadDir("builtins")
	if err != nil {
		return nil, fmt.Errorf("reading builtins directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDirPath := path.Join("builtins", entry.Name())
		skill, err := loadEmbeddedSkill(skillDirPath, entry.Name())
		if err != nil {
			// Non-fatal: skip broken built-ins and continue
			continue
		}
		loaded = append(loaded, skill)
	}

	return loaded, nil
}

// loadEmbeddedSkill parses a single skill directory from the embedded FS.
func loadEmbeddedSkill(dirPath, dirName string) (*Skill, error) {
	skillMDPath := path.Join(dirPath, "SKILL.md")
	data, err := builtinFS.ReadFile(skillMDPath)
	if err != nil {
		return nil, fmt.Errorf("SKILL.md not found in embedded %s", dirPath)
	}

	metadata, instructions, frontmatterHooks, validationErrors, err := ParseSkillMDContentWithValidation(data)
	if err != nil {
		return nil, fmt.Errorf("parsing embedded SKILL.md in %s: %w", dirPath, err)
	}
	for _, ve := range validationErrors {
		if ve.Fatal {
			return nil, fmt.Errorf("validation error in embedded skill %s: %s", dirPath, ve.Error())
		}
	}

	// Use directory name as fallback for the skill name
	if metadata.Name == "" {
		metadata.Name = dirName
	}

	skill := &Skill{
		Metadata:     *metadata,
		Instructions: instructions,
		Path:         fmt.Sprintf("builtin:%s", metadata.Name),
		LoadedAt:     time.Now(),
	}

	// Set frontmatter hooks if present
	if frontmatterHooks != nil {
		skill.Hooks = frontmatterHooks
	}

	// Populate Scripts from embedded scripts/ sub-directory
	skill.Scripts = readEmbeddedScripts(dirPath)

	// Populate References from embedded references/ sub-directory
	skill.References = readEmbeddedReferences(dirPath)

	// Populate Assets from embedded assets/ sub-directory
	skill.Assets = readEmbeddedAssets(dirPath)

	return skill, nil
}

func readEmbeddedScripts(dirPath string) []SkillScript {
	scriptsDir := path.Join(dirPath, "scripts")
	entries, err := builtinFS.ReadDir(scriptsDir)
	if err != nil {
		return nil
	}
	var scripts []SkillScript
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		scripts = append(scripts, SkillScript{
			Name:     e.Name(),
			Path:     path.Join("scripts", e.Name()),
			Language: detectLanguage(e.Name()),
		})
	}
	return scripts
}

func readEmbeddedReferences(dirPath string) []SkillReference {
	refsDir := path.Join(dirPath, "references")
	entries, err := builtinFS.ReadDir(refsDir)
	if err != nil {
		return nil
	}
	var refs []SkillReference
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		refs = append(refs, SkillReference{
			Name:   e.Name(),
			Path:   path.Join("references", e.Name()),
			Format: detectFormat(e.Name()),
		})
	}
	return refs
}

func readEmbeddedAssets(dirPath string) []SkillAsset {
	assetsDir := path.Join(dirPath, "assets")
	entries, err := builtinFS.ReadDir(assetsDir)
	if err != nil {
		return nil
	}
	var assets []SkillAsset
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		assets = append(assets, SkillAsset{
			Name:     e.Name(),
			Path:     path.Join("assets", e.Name()),
			MimeType: detectMimeType(e.Name()),
		})
	}
	return assets
}

// EmbeddedSkillContent returns the raw bytes of a file inside a built-in skill.
// skillName is the skill's name (e.g. "swarm-skill").
// relPath is the path relative to the skill directory (e.g. "references/SKILL-AUTHORING.md").
func EmbeddedSkillContent(skillName, relPath string) ([]byte, error) {
	fullPath := path.Join("builtins", skillName, relPath)
	return builtinFS.ReadFile(fullPath)
}

// EmbeddedSkillFS returns a sub-filesystem rooted at a specific built-in skill directory.
// Useful for serving embedded assets (e.g. templates) via http.FS.
func EmbeddedSkillFS(skillName string) (fs.FS, error) {
	return fs.Sub(builtinFS, path.Join("builtins", skillName))
}

// ListBuiltinSkillNames returns the names of all embedded built-in skills.
func ListBuiltinSkillNames() []string {
	entries, err := builtinFS.ReadDir("builtins")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			// Only include directories that have a SKILL.md
			if _, err := builtinFS.ReadFile(path.Join("builtins", e.Name(), "SKILL.md")); err == nil {
				names = append(names, e.Name())
			}
		}
	}
	return names
}
