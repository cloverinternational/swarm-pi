// Harness Phase 2 client bridge — redacted runtime snapshot.
//
// HarnessSnapshot reads ACTUAL runtime state (the agent's real provider-visible
// tools, effective provider/model, live system prompt) rather than inferring
// anything from the option list. It contains no secrets: the credential is
// represented only by its redacted provenance label, and absolute host paths are
// normalized to location-portable hashes (MINOR-1) so a snapshot is comparable
// across machines and never leaks a filesystem layout.
package client

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

// HarnessSnapshot is a redacted, location-portable view of a harness-constructed
// client's effective runtime posture. Every field is safe to log or golden.
type HarnessSnapshot struct {
	// Harness bool reports whether this client was built from a harness plan.
	// When false every other field is zero-valued.
	Harness bool `json:"harness"`

	// PlanName / PlanDigest identify the compiled plan (digest is the harness
	// package's own deterministic, redacted digest).
	PlanName   string `json:"planName,omitempty"`
	PlanDigest string `json:"planDigest,omitempty"`

	// Provider / Model are read from the effective agent definition.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`

	// SystemPromptSHA256 / SystemPromptBytes describe the agent's LIVE system
	// prompt (hashed, never the text).
	SystemPromptSHA256 string `json:"systemPromptSha256,omitempty"`
	SystemPromptBytes  int    `json:"systemPromptBytes"`

	// Policy posture (permission axis; separate from exposure).
	ApprovalMode      string `json:"approvalMode,omitempty"`
	WorkspaceBoundary bool   `json:"workspaceBoundary"`
	AllowMutation     bool   `json:"allowMutation"`

	// ExposedTools are the REAL provider-visible tool names from the agent's
	// next-request tool payload (Agent.ProviderTools()). This is the exposure
	// axis read from runtime state, not from the option list.
	ExposedTools []string `json:"exposedTools"`

	// SelectedCatalogIDs are the plan's exact selected stable capability IDs.
	SelectedCatalogIDs []string `json:"selectedCatalogIDs"`

	// CredentialSource is the redacted provenance label (for example
	// "env:ANTHROPIC_API_KEY") — NEVER the secret value. Empty when no
	// credential was declared.
	CredentialSource string `json:"credentialSource,omitempty"`

	// WorkspaceSHA256 / StorageSHA256 are normalized, location-portable hashes of
	// the resolved absolute paths (MINOR-1). Raw absolute host paths are never
	// emitted.
	WorkspaceSHA256 string `json:"workspaceSha256,omitempty"`
	StorageSHA256   string `json:"storageSha256,omitempty"`

	// Skills is the plan's REDACTED, resolved skill selection view
	// (Phase 5d — mirrors harness.Plan.Skills()). Never a SKILL.md body, never
	// an absolute host path: Path is already the manifest-relative, normalized
	// display label the harness compiler recorded.
	Skills []HarnessSkillView `json:"skills,omitempty"`

	// SkillSearchRoots is the plan's resolved, manifest-relative skill search
	// root list (mirrors harness.Plan.SkillSearchRoots()).
	SkillSearchRoots []string `json:"skillSearchRoots,omitempty"`
}

// HarnessSkillView is a redacted view of one selected skill, mirroring
// harness.SkillSpec. It NEVER carries a SKILL.md body or an absolute host
// path.
type HarnessSkillView struct {
	// ID is the stable skill id.
	ID string `json:"id"`
	// Path is the manifest-relative, normalized display label (empty for
	// id-only entries).
	Path string `json:"path,omitempty"`
	// ContentHash is "sha256:<hex>" of the backing SKILL.md (empty for
	// id-only entries).
	ContentHash string `json:"contentHash,omitempty"`
	// Source is the provenance origin: "file" for a path-backed entry,
	// "manifest" for an id-only entry.
	Source string `json:"source"`
}

// HarnessSnapshot returns a redacted view of the effective harness posture read
// from actual runtime state. For a client not built from a harness plan, it
// returns HarnessSnapshot{Harness: false}.
func (c *Client) HarnessSnapshot() HarnessSnapshot {
	c.mu.RLock()
	hc := c.opts.harness
	agent := c.agent
	def := c.agentDef
	approvalMode := c.opts.approvalMode
	c.mu.RUnlock()

	if hc == nil || hc.plan == nil {
		return HarnessSnapshot{Harness: false}
	}
	plan := hc.plan

	snap := HarnessSnapshot{
		Harness:            true,
		PlanName:           plan.Name(),
		PlanDigest:         plan.Digest(),
		ApprovalMode:       approvalMode,
		SelectedCatalogIDs: plan.Tools(),
		ExposedTools:       []string{},
		CredentialSource:   plan.Credential().Source(),
		WorkspaceSHA256:    hashPathToken(plan.Workspace()),
		StorageSHA256:      hashPathToken(plan.Storage()),
		Skills:             harnessSkillViews(plan.Skills()),
		SkillSearchRoots:   append([]string(nil), plan.SkillSearchRoots()...),
	}

	perms := plan.Permissions()
	if perms.WorkspaceBoundary != nil {
		snap.WorkspaceBoundary = *perms.WorkspaceBoundary
	}
	snap.AllowMutation = perms.AllowMutation

	if def != nil {
		snap.Provider = def.Provider
		snap.Model = def.Model
	}

	if agent != nil {
		// LIVE system prompt hash (real runtime state, not the option string).
		sp := agent.SystemPrompt()
		sum := sha256.Sum256([]byte(sp))
		// Match the harness package's "sha256:" prefix convention so the live
		// hash is directly comparable to Plan.SystemPromptHash().
		snap.SystemPromptSHA256 = "sha256:" + hex.EncodeToString(sum[:])
		snap.SystemPromptBytes = len(sp)

		// REAL provider-visible tool names from the next request's payload.
		names := make([]string, 0)
		for _, t := range agent.ProviderTools() {
			names = append(names, t.Name)
		}
		sort.Strings(names)
		snap.ExposedTools = names
	}

	return snap
}

// hashPathToken normalizes an absolute host path to a stable, location-portable
// token (sha256 hex). Empty input yields an empty token.
func hashPathToken(p string) string {
	if p == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(p))
	return hex.EncodeToString(sum[:])
}

// harnessSkillViews converts the plan's redacted SkillSpec list to the
// client-facing HarnessSkillView list, field-for-field, with no redaction gaps
// introduced (both types are already fully redacted).
func harnessSkillViews(specs []harness.SkillSpec) []HarnessSkillView {
	out := make([]HarnessSkillView, 0, len(specs))
	for _, sp := range specs {
		out = append(out, HarnessSkillView{
			ID:          sp.ID,
			Path:        sp.Path,
			ContentHash: sp.ContentHash,
			Source:      sp.Source,
		})
	}
	return out
}
