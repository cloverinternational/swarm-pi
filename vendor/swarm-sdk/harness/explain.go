package harness

import (
	"encoding/json"
)

// ExplainReport is the redacted, deterministic, serializable view of a compiled
// Plan. It contains NO raw prompt text and NO secret value — only content hashes,
// byte counts, redacted references, and non-sensitive configuration. It is the
// canonical input to the plan digest.
type ExplainReport struct {
	APIVersion   string          `json:"apiVersion"`
	Kind         string          `json:"kind"`
	Metadata     Metadata        `json:"metadata"`
	Runtime      RuntimeExplain  `json:"runtime"`
	Provider     ProviderExplain `json:"provider"`
	Agent        AgentExplain    `json:"agent"`
	Permissions  Permissions     `json:"permissions"`
	Interfaces   Interfaces      `json:"interfaces"`
	Provenance   []FieldSource   `json:"provenance"`
	Capabilities []CapabilityRef `json:"capabilities"`
	Skills       []SkillSpec     `json:"skills,omitempty"`
	SkillRoots   []string        `json:"skillSearchRoots,omitempty"`
	Hooks        []HookSpec      `json:"hooks,omitempty"`
	MCP          []McpServerSpec `json:"mcp,omitempty"`
	// Phase 8a redacted sections: ids, references, enums, limits, prompt
	// hashes, and tool ids ONLY — never a credential value or prompt body.
	// All three are omitempty, so a manifest that omits them produces the
	// exact same canonical JSON (and therefore the same digest) as before.
	Profiles []ProfileSpec  `json:"profiles,omitempty"`
	Fallback *FallbackSpec  `json:"fallback,omitempty"`
	Agents   []SubagentSpec `json:"agents,omitempty"`
	// Budgets is the Phase 9a redacted budgets view: kind, enforcement class,
	// numeric bound, unit, and exhaustion action ONLY — no new category of
	// information enters the report. The enforcement class is reported for
	// every declared budget precisely so a reader can never mistake an
	// observed-only bound for one that will stop the run. It is omitempty, so a
	// manifest without budgets produces byte-identical canonical JSON (and
	// therefore the identical digest) to pre-9a.
	Budgets []BudgetSpec `json:"budgets,omitempty"`
	// Schedules is the Phase 9b redacted schedules view: ids, cron strings,
	// zone names, enum values, referenced ids, counts, and the enforcement
	// class of EVERY dimension. A schedule carries no prompt body and no
	// credential, so no new category of information enters the report.
	Schedules []ScheduleSpec `json:"schedules,omitempty"`
	// RuntimeBindings enumerates the host capabilities this plan REQUIRES but
	// cannot supply (plan.md §3.3). It is reported so an operator — and Phase
	// 9d preflight — can see, before execution, exactly which bindings the
	// selected interface must provide. Both fields are omitempty, so a manifest
	// that declares no schedules produces byte-identical canonical JSON (and
	// therefore the identical digest) to pre-9b.
	RuntimeBindings []RuntimeBindingRequirement `json:"runtimeBindings,omitempty"`
	// Workflows is the Phase 9c redacted workflows view: the harness-side id, the
	// manifest-relative reference, the referenced file's CONTENT HASH and version,
	// the adapted camelCase definition (enum values, ids, counts, sorted map KEY
	// lists, prompt HASHES + byte counts), the required runtime bindings, and the
	// audited list of deliberately dropped fields. A prompt body, a prompt
	// template, a metadata VALUE, and a credential from the referenced file are all
	// structurally unable to reach it. The content hash is what folds the
	// referenced workflow's identity into the digest (D3): swapping the file
	// changes the plan's identity exactly as a changed skill or prompt file does.
	// It is omitempty, so a manifest without workflows produces byte-identical
	// canonical JSON (and therefore the identical digest) to pre-9c.
	Workflows []WorkflowSpec `json:"workflows,omitempty"`
	// Compatibility is the Phase 10c redacted compatibility-preset view (D2):
	// the applied preset's name plus the documented, per-cause shortfall list
	// (D3). Both are omitempty and populated ONLY when `compatibility:` was
	// set, so a manifest that omits it produces byte-identical canonical JSON
	// (and therefore the identical digest) to pre-10c.
	Compatibility          string                 `json:"compatibility,omitempty"`
	CompatibilityShortfall []PresetShortfallEntry `json:"compatibilityShortfall,omitempty"`
	Digest                 string                 `json:"digest,omitempty"`
}

// RuntimeExplain is the redacted runtime view.
type RuntimeExplain struct {
	Workspace string `json:"workspace"`
	Storage   string `json:"storage"`
	Watch     bool   `json:"watch"`
}

// ProviderExplain is the redacted provider view. The credential is described by
// its provenance only; its value is never present.
type ProviderExplain struct {
	ID              string `json:"id"`
	Model           string `json:"model"`
	BaseURL         string `json:"baseURL,omitempty"`
	CredentialRef   string `json:"credentialRef,omitempty"`
	CredentialState string `json:"credentialState,omitempty"` // present | deferred | absent
}

// AgentExplain is the redacted agent view. The system prompt is described by
// hash + byte count + source only; the text never appears.
type AgentExplain struct {
	SystemPromptHash   string   `json:"systemPromptHash"`
	SystemPromptBytes  int      `json:"systemPromptBytes"`
	SystemPromptSource string   `json:"systemPromptSource"`
	Tools              []string `json:"tools"`
	Limits             Limits   `json:"limits"`
}

// CapabilityRef pairs a selected capability ID with its catalog policy class,
// so an explained plan is self-describing about capability posture.
type CapabilityRef struct {
	ID    string      `json:"id"`
	Class PolicyClass `json:"class"`
}

// Explain returns the redacted, deterministic report for the plan.
func (p *Plan) Explain() ExplainReport {
	return p.explainReport(true)
}

// ExplainJSON returns the redacted report as indented canonical JSON.
func (p *Plan) ExplainJSON() ([]byte, error) {
	return json.MarshalIndent(p.Explain(), "", "  ")
}

// explainReport builds the report. When withDigest is false the Digest field is
// left empty so the value can be hashed to produce a stable digest.
func (p *Plan) explainReport(withDigest bool) ExplainReport {
	credState := "absent"
	switch {
	case p.credential.Deferred():
		credState = "deferred"
	case p.credential.Present():
		credState = "present"
	}
	credRef := p.credential.Source()

	caps := make([]CapabilityRef, 0, len(p.tools))
	for _, id := range p.tools {
		class := PolicyClass("unknown")
		if c, ok := LookupCapability(id); ok {
			class = c.Class
		}
		caps = append(caps, CapabilityRef{ID: id, Class: class})
	}

	rep := ExplainReport{
		APIVersion: p.apiVersion,
		Kind:       p.kind,
		Metadata:   copyMetadata(p.metadata),
		Runtime: RuntimeExplain{
			Workspace: p.workspace,
			Storage:   p.storage,
			Watch:     p.watch,
		},
		Provider: ProviderExplain{
			ID:              p.providerID,
			Model:           p.model,
			BaseURL:         p.baseURL,
			CredentialRef:   credRef,
			CredentialState: credState,
		},
		Agent: AgentExplain{
			SystemPromptHash:   p.promptHash,
			SystemPromptBytes:  p.promptBytes,
			SystemPromptSource: p.promptSource,
			Tools:              p.Tools(),
			Limits:             p.limits,
		},
		Permissions:  p.Permissions(),
		Interfaces:   copyInterfaces(p.interfaces),
		Provenance:   p.Provenance(),
		Capabilities: caps,
		Skills:       p.Skills(),
		SkillRoots:   p.SkillSearchRoots(),
		Hooks:        p.Hooks(),
		MCP:          p.MCPServers(),
	}
	if len(p.profiles) > 0 {
		rep.Profiles = p.Profiles()
	}
	if fb, ok := p.Fallback(); ok {
		rep.Fallback = &fb
	}
	if len(p.subagents) > 0 {
		rep.Agents = p.Subagents()
	}
	if len(p.budgets) > 0 {
		rep.Budgets = p.Budgets()
	}
	if len(p.schedules) > 0 {
		rep.Schedules = p.Schedules()
	}
	if len(p.bindings) > 0 {
		rep.RuntimeBindings = p.RequiredRuntimeBindings()
	}
	if len(p.workflows) > 0 {
		rep.Workflows = p.Workflows()
	}
	if p.compatibility != "" {
		rep.Compatibility = p.compatibility
		rep.CompatibilityShortfall = p.CompatibilityShortfall()
	}
	if withDigest {
		rep.Digest = p.digest
	}
	return rep
}

// computeDigest hashes the canonical JSON of the redacted report (with an empty
// Digest field) to produce a deterministic, secret-free content digest.
func computeDigest(p *Plan) string {
	rep := p.explainReport(false)
	b, err := json.Marshal(rep)
	if err != nil {
		// Marshaling a struct of plain fields cannot realistically fail; hash a
		// stable fallback so the digest is still deterministic.
		return hashString("harness.digest.error:" + p.apiVersion + ":" + p.metadata.Name)
	}
	return hashBytes(b)
}
