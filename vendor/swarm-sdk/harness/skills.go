package harness

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

// SkillsSection is the declarative v1alpha1 `skills:` block. It is DECLARATION +
// RESOLUTION only: it names skills (by stable id, optionally backed by a
// manifest-relative path) and optional manifest-relative search roots. It does
// NOT load SKILL.md contents into the document, register skills, or enable
// autoskills — those are later, client-side concerns.
//
// Two on-disk shapes are accepted (mirroring the tri-state ToolSelection):
//
//   - a sequence form, so `skills: []` and `skills: [{id: ...}, ...]` are valid;
//   - an object form `skills: {entries: [...], searchRoots: [...]}` when search
//     roots need to be declared alongside the entries.
//
// Both forms strict-decode their contents (unknown keys under an entry, or under
// the object, are rejected).
type SkillsSection struct {
	Entries     []SkillEntry
	SearchRoots []string
}

// SkillEntry is one declared skill selection. ID is required and stable; Path is
// an optional manifest-relative directory (containing SKILL.md) or SKILL.md file.
// Id-only entries are declared here but only resolved by id in Phase 5b.
type SkillEntry struct {
	ID   string `json:"id"`
	Path string `json:"path,omitempty"`
}

// skillsObject is the object form of the skills section. It is separate from
// SkillsSection so the custom decoder can strict-decode it without recursing
// back into SkillsSection.UnmarshalJSON.
type skillsObject struct {
	Entries     []SkillEntry `json:"entries,omitempty"`
	SearchRoots []string     `json:"searchRoots,omitempty"`
}

// UnmarshalJSON accepts either the sequence form or the object form and rejects
// unknown fields in both. A JSON null is treated as an absent (empty) section.
// It is only invoked when the `skills` key is present, so an omitted section
// leaves the zero value (no skills).
func (s *SkillsSection) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if string(trimmed) == "null" {
		s.Entries = nil
		s.SearchRoots = nil
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '[' {
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.DisallowUnknownFields()
		var entries []SkillEntry
		if err := dec.Decode(&entries); err != nil {
			return err
		}
		s.Entries = entries
		s.SearchRoots = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var obj skillsObject
	if err := dec.Decode(&obj); err != nil {
		return err
	}
	s.Entries = obj.Entries
	s.SearchRoots = obj.SearchRoots
	return nil
}

// SkillSpec is one resolved skill carried on the immutable Plan. It is fully
// redacted for display: it names the skill and describes any backing file by
// content hash ONLY — the SKILL.md body is never stored, hashed-into-plan aside.
type SkillSpec struct {
	// ID is the stable skill id.
	ID string `json:"id"`
	// Path is the manifest-relative, normalized display label (empty for id-only
	// entries). It intentionally mirrors the provenance ref rather than an
	// absolute host path, so a compiled plan stays portable.
	Path string `json:"path,omitempty"`
	// ContentHash is "sha256:<hex>" of the backing SKILL.md (empty for id-only
	// entries). It changes whenever the selected skill's content changes.
	ContentHash string `json:"contentHash,omitempty"`
	// Source is the provenance origin: "file" for a path-backed entry, "manifest"
	// for an id-only entry.
	Source string `json:"source"`
}

// resolveSkills validates and resolves the declarative skills section against the
// manifest directory. Path-backed entries fail closed when the referenced skill
// does not exist / is unreadable. Contents are hashed but NEVER stored.
func resolveSkills(p *Plan, sourcePath, mdir string, sec SkillsSection) Diagnostics {
	var ds Diagnostics

	seen := make(map[string]struct{}, len(sec.Entries))
	out := make([]SkillSpec, 0, len(sec.Entries))
	for i, e := range sec.Entries {
		field := "skills[" + strconv.Itoa(i) + "]"
		if e.ID == "" {
			ds = append(ds, newDiag("harness.skills.id.missing", field+".id",
				"skill entry requires a non-empty id", sourcePath))
			continue
		}
		if _, dup := seen[e.ID]; dup {
			ds = append(ds, newDiag("harness.skills.id.duplicate", field+".id",
				"duplicate skill id "+quote(e.ID), sourcePath))
			continue
		}
		seen[e.ID] = struct{}{}

		spec := SkillSpec{ID: e.ID}
		if e.Path == "" {
			// Id-only: declared now, resolved by id in Phase 5b. No content hash.
			spec.Source = "manifest"
			out = append(out, spec)
			p.addProvenance("skills."+e.ID, "manifest", "")
			continue
		}

		// Path-backed: resolve manifest-relative (containment + symlink guarded),
		// then fail closed if the referenced skill is missing/unreadable.
		abs, d := resolveContainedFile(sourcePath, field+".path", mdir, e.Path)
		if d != nil {
			ds = append(ds, *d)
			continue
		}
		hash, hd := hashSkillContent(sourcePath, field+".path", abs)
		if hd != nil {
			ds = append(ds, *hd)
			continue
		}
		spec.Path = filepath.Clean(e.Path)
		spec.ContentHash = hash
		spec.Source = "file"
		out = append(out, spec)
		p.addProvenance("skills."+e.ID, "file", filepath.Clean(e.Path))
	}

	roots := make([]string, 0, len(sec.SearchRoots))
	for i, r := range sec.SearchRoots {
		field := "skills.searchRoots[" + strconv.Itoa(i) + "]"
		if r == "" {
			ds = append(ds, newDiag("harness.skills.searchRoot.empty", field,
				"search root is empty", sourcePath))
			continue
		}
		abs, d := resolveContainedFile(sourcePath, field, mdir, r)
		if d != nil {
			ds = append(ds, *d)
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			ds = append(ds, newDiag("harness.skills.searchRoot.notDir", field,
				"search root must be an existing directory", sourcePath))
			continue
		}
		roots = append(roots, filepath.Clean(r))
	}

	if ds.HasErrors() {
		return ds
	}
	p.skills = out
	p.searchRoots = roots
	return ds
}

// hashSkillContent computes the content hash of a path-backed skill WITHOUT
// retaining its contents. When the resolved path is a directory it hashes the
// contained SKILL.md (which must exist); when it is a file it hashes that file.
func hashSkillContent(sourcePath, fieldPath, abs string) (string, *Diagnostic) {
	info, err := os.Stat(abs)
	if err != nil {
		d := newDiag("harness.skills.unreadable", fieldPath,
			"referenced skill could not be read", sourcePath)
		return "", &d
	}
	target := abs
	if info.IsDir() {
		target = filepath.Join(abs, "SKILL.md")
		if _, err := os.Stat(target); err != nil {
			d := newDiag("harness.skills.skillFileMissing", fieldPath,
				"skill directory does not contain SKILL.md", sourcePath)
			return "", &d
		}
	}
	data, err := os.ReadFile(target)
	if err != nil {
		d := newDiag("harness.skills.unreadable", fieldPath,
			"referenced skill file could not be read", sourcePath)
		return "", &d
	}
	return hashBytes(data), nil
}

// Skills returns a copy of the resolved skill specs carried on the plan.
func (p *Plan) Skills() []SkillSpec {
	out := make([]SkillSpec, len(p.skills))
	copy(out, p.skills)
	return out
}

// SkillSearchRoots returns a copy of the resolved, manifest-relative skill search
// roots (carried for Phase 5b id-only lookup; not enumerated in 5a).
func (p *Plan) SkillSearchRoots() []string {
	out := make([]string, len(p.searchRoots))
	copy(out, p.searchRoots)
	return out
}
