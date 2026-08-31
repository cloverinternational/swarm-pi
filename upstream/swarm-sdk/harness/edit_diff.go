package harness

import (
	"errors"
	"strings"
)

// EditDiff is a redacted semantic before/after view. It intentionally excludes
// credential values, credential references, raw prompts, and all YAML text.
type EditDiff struct {
	Before  EditSnapshot `json:"before"`
	After   EditSnapshot `json:"after"`
	Changed bool         `json:"changed"`
}

// EditSnapshot is the non-secret editor diff surface.
type EditSnapshot struct {
	ProviderID   string   `json:"providerID"`
	Model        string   `json:"model"`
	PromptHash   string   `json:"promptHash"`
	Tools        []string `json:"tools"`
	ApprovalMode string   `json:"approvalMode"`
	Limits       Limits   `json:"limits"`
	Workspace    string   `json:"workspace"`
	Storage      string   `json:"storage"`
	// Skills is a REDACTED, per-entry token list: "<id>[:<8-char contentHash
	// prefix>][*]", where the trailing "*" marks a path-backed (vs id-only)
	// entry. It NEVER carries a SKILL.md body or an absolute host path.
	Skills []string `json:"skills,omitempty"`
	// SkillRoots is the resolved, manifest-relative skill search root list.
	SkillRoots []string `json:"skillSearchRoots,omitempty"`
}

// Diff validates the candidate and compares it with the session baseline using
// Plan.Explain, whose contract excludes secret values and raw prompt text.
func (s *EditSession) Diff() (EditDiff, error) {
	if s == nil || s.baselinePlan == nil {
		return EditDiff{}, errors.New("diff harness edit: no valid baseline plan")
	}
	candidate, ds, err := s.Validate()
	if err != nil {
		return EditDiff{}, err
	}
	if ds.HasErrors() {
		return EditDiff{}, &ValidationError{Diagnostics: ds}
	}
	before := editSnapshot(s.baselinePlan.Explain())
	after := editSnapshot(candidate.Explain())
	return EditDiff{
		Before:  before,
		After:   after,
		Changed: s.baselinePlan.Digest() != candidate.Digest(),
	}, nil
}

func editSnapshot(report ExplainReport) EditSnapshot {
	return EditSnapshot{
		ProviderID:   report.Provider.ID,
		Model:        report.Provider.Model,
		PromptHash:   report.Agent.SystemPromptHash,
		Tools:        append([]string(nil), report.Agent.Tools...),
		ApprovalMode: report.Permissions.ApprovalMode,
		Limits:       report.Agent.Limits,
		Workspace:    report.Runtime.Workspace,
		Storage:      report.Runtime.Storage,
		Skills:       editSkillTokens(report.Skills),
		SkillRoots:   append([]string(nil), report.SkillRoots...),
	}
}

// editSkillTokens renders each SkillSpec as a compact, REDACTED token —
// mirroring the client package's harnessSkillsLabel convention — so the
// editor's diff preview can show a skills change without ever including a
// SKILL.md body or an absolute host path (SkillSpec.Path is already
// manifest-relative).
func editSkillTokens(specs []SkillSpec) []string {
	if len(specs) == 0 {
		return nil
	}
	out := make([]string, 0, len(specs))
	for _, sp := range specs {
		tok := sp.ID
		if sp.ContentHash != "" {
			h := strings.TrimPrefix(sp.ContentHash, "sha256:")
			if len(h) > 8 {
				h = h[:8]
			}
			tok += ":" + h
		}
		if sp.Path != "" {
			tok += "*"
		}
		out = append(out, tok)
	}
	return out
}
