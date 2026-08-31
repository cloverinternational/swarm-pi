// Harness Phase 8b — client-side wiring of declarative profiles, fallback
// chains, and subagents (harness/agents.go, Phase 8a: declaration + resolution
// only) onto the closed harness client path.
//
// This file mirrors the structural discipline of client/harness_mcp.go (7b/
// 7c/7d) and client/harness_hooks.go (6b):
//
//   - CLOSED, PLAN-ONLY SOURCE (D4). Every helper here reads EXCLUSIVELY from
//     a *harness.Plan. There is no ambient `~/.swarmos` agent lookup, no
//     project-level agent config, no built-in default roster reachable from
//     any function in this file — there is structurally no code path that
//     could reach one. When a plan declares zero profiles/fallback/agents,
//     every builder below returns its explicit "nothing declared" zero value
//     and initHarnessAgent's wiring is a no-op: byte-for-byte unchanged
//     behavior on the closed path (HARD INVARIANT).
//   - CREDENTIAL DISCIPLINE. A profile credential is revealed via
//     Plan.ProfileCredential(id).Reveal() EXACTLY ONCE, at construction, and
//     flows only into an ephemeral provider.Config. It is never logged, never
//     placed in an error string, never retained beyond that Config. Sealing
//     reuses sealHarnessProvider (harness_plan.go) — the SAME allowlist +
//     fail-closed-empty-credential discipline the document-level provider
//     already enforces — rather than inventing a second credential path.
//   - D1 (unbindable subagent fields fail closed, never silently drop).
//     harness.SubagentSpec carries Skills/SkillRoots/Hooks/Delegates.
//     agent.Definition/agent.SubAgentConfig have no field for the first
//     three. buildHarnessSubagentBinding REJECTS (by named field, by agent
//     id) any subagent that declares skills, skillSearchRoots, or hooks,
//     rather than silently constructing a narrower agent than the manifest
//     declares. Delegates IS bound for real (D2, below). See the doc on
//     buildHarnessSubagentBinding for why skills/hooks specifically cannot be
//     reused as-is in this slice (the existing loaders are document-Plan-
//     scoped, not per-agents[]-entry-scoped, and this file is REUSE-ONLY
//     against client/harness_skills.go and client/harness_hooks.go — their
//     signatures are not changed here).
//   - D2 (delegation is an allowlist, enforced at the binding layer).
//     harnessSubagentBinding.DelegationAllowed(fromID, toID) is the runtime
//     enforcement primitive: fromID may reach toID iff toID is present in
//     fromID's own declared `delegates` list. An agent with no declared
//     delegates permits nothing. See harnessSubagentBinding's doc for why no
//     live invocation site exists yet on this path (delegation.task remains
//     harness.PolicyDeferDiscover and unbound in client/harness_catalog_bind.go
//     — both explicitly out of this slice's file scope) and how this
//     primitive is the prepared enforcement point for when it is bound.
//   - D3 (closed posture: fallback stays off unless explicitly enabled).
//     harnessFallbackDecision returns noFallback==true UNLESS
//     Plan.Fallback() reports ok==true AND spec.Enabled==true. An absent
//     `fallback:` section and a declared-but-disabled one are deliberately
//     indistinguishable here — both must leave the closed posture untouched.
package client

import (
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// harnessProfileProviderConfig resolves ONE declared profile to a sealed
// provider.Config, reusing sealHarnessProvider — the exact allowlist +
// fail-closed-empty-credential discipline harness_plan.go already applies to
// the document-level provider. The credential is revealed exactly once, here,
// via plan.ProfileCredential; it flows only into the returned Config's APIKey
// field and is never logged or placed in an error string (sealHarnessProvider
// itself only ever receives the redacted source label, never the value).
func harnessProfileProviderConfig(plan *harness.Plan, spec harness.ProfileSpec) (provider.Config, error) {
	apiKey := ""
	if cred, ok := plan.ProfileCredential(spec.ID); ok && cred.Present() {
		if v, revealed := cred.Reveal(); revealed {
			apiKey = v
		}
	}
	if err := sealHarnessProvider(spec.Provider, apiKey, spec.CredentialRef); err != nil {
		return provider.Config{}, fmt.Errorf("harness: profiles[%s]: %w", spec.ID, err)
	}

	norm := normalizeProviderName(spec.Provider)
	cfg := provider.Config{
		Name:         norm,
		APIKey:       apiKey,
		BaseURL:      spec.BaseURL,
		NoAmbientEnv: true,
		Model:        spec.Model,
		Custom:       map[string]any{},
	}
	if builtin, ok := provider.LookupBuiltinProvider(spec.Provider); ok && cfg.BaseURL == "" {
		cfg.BaseURL = builtin.BaseURL
		cfg.HTTPMaxRetries = builtin.HTTPMaxRetries
	}
	return cfg, nil
}

// buildHarnessProfiles resolves EVERY profile the plan declares to a sealed
// provider.Config, fail-closed on the first invalid/unsealed one (so a bad
// profile can never leave a partially-resolved map for a later stage to trust
// blindly). Returns (nil, nil, nil) when the plan declares zero profiles —
// the zero-selection no-op this file's HARD INVARIANT requires.
func buildHarnessProfiles(plan *harness.Plan) (map[string]harness.ProfileSpec, map[string]provider.Config, error) {
	specs := plan.Profiles()
	if len(specs) == 0 {
		return nil, nil, nil
	}
	specByID := make(map[string]harness.ProfileSpec, len(specs))
	cfgByID := make(map[string]provider.Config, len(specs))
	for _, spec := range specs {
		cfg, err := harnessProfileProviderConfig(plan, spec)
		if err != nil {
			return nil, nil, err
		}
		specByID[spec.ID] = spec
		cfgByID[spec.ID] = cfg
	}
	return specByID, cfgByID, nil
}

// buildHarnessFallbackChain resolves one FallbackSpec's declared profile
// chain (already validated, non-empty when Enabled — harness/agents.go
// resolveFallback) into a *fallback.Chain of provider/model references, in
// declared order. Every referenced id must be present in BOTH profileSpecs
// and profileCfgs (built together, atomically, by buildHarnessProfiles) —
// re-verified here fail-closed rather than trusted blindly, mirroring the
// re-verification discipline client/harness_mcp.go and client/harness_hooks.go
// apply to their own already-compile-time-checked inputs.
func buildHarnessFallbackChain(spec harness.FallbackSpec, profileSpecs map[string]harness.ProfileSpec, profileCfgs map[string]provider.Config) (*fallback.Chain, error) {
	if len(spec.Chain) == 0 {
		return nil, fmt.Errorf("harness: fallback chain is empty")
	}
	refs := make([]fallback.ModelRef, 0, len(spec.Chain))
	for _, id := range spec.Chain {
		ps, ok := profileSpecs[id]
		if !ok {
			return nil, fmt.Errorf("harness: fallback chain references unresolved profile id %q", id)
		}
		if _, ok := profileCfgs[id]; !ok {
			return nil, fmt.Errorf("harness: fallback chain profile %q has no sealed provider config", id)
		}
		refs = append(refs, fallback.ModelRef{Provider: normalizeProviderName(ps.Provider), Model: ps.Model})
	}
	chain := &fallback.Chain{Primary: refs[0]}
	if len(refs) > 1 {
		chain.Fallbacks = refs[1:]
	}
	return chain, nil
}

// harnessFallbackDecision implements D3 exactly: the closed posture
// (SetNoFallback(true), no chain) is preserved UNLESS Plan.Fallback() reports
// ok==true AND spec.Enabled==true. An absent `fallback:` section (ok==false)
// and a declared-but-not-enabled one (ok==true, Enabled==false) are
// deliberately indistinguishable here — both leave the closed posture
// untouched, exactly as harness/agents.go's CLOSED-POSTURE RULE requires.
func harnessFallbackDecision(plan *harness.Plan, profileSpecs map[string]harness.ProfileSpec, profileCfgs map[string]provider.Config) (chain *fallback.Chain, noFallback bool, err error) {
	spec, ok := plan.Fallback()
	if !ok || !spec.Enabled {
		return nil, true, nil
	}
	c, cerr := buildHarnessFallbackChain(spec, profileSpecs, profileCfgs)
	if cerr != nil {
		return nil, true, fmt.Errorf("harness: fallback: %w", cerr)
	}
	return c, false, nil
}

// harnessSubagentBinding is the closed, plan-only subagent roster prepared by
// buildHarnessSubagentBinding. It is the prepared HOST/RUNTIME BINDING
// SURFACE (plan.md Phase 8 action #6) for delegation: an agent.Definition per
// declared subagent, plus the D2 delegation allowlist. It deliberately stops
// short of registering a live delegation.task tool — that capability remains
// harness.PolicyDeferDiscover in harness/catalog.go and has no constructor
// binding in client/harness_catalog_bind.go's harnessToolBindings map (both
// explicitly out of this slice's file scope: harness/ is not modified by this
// slice, and harness_catalog_bind.go is not in this slice's edit list). A
// plan therefore cannot select "delegation.task" in `tools:` at all today
// (buildHarnessTools rejects any catalogued-but-unbound id before construction
// reaches this file), so there is structurally no live invocation surface for
// this roster to be reachable from yet. This type is what a future phase
// wires into a builtin.SubagentTool once that capability is promoted to
// bound.
type harnessSubagentBinding struct {
	defs      map[string]*agent.Definition
	delegates map[string][]string
}

// Definitions returns a defensive copy of the closed subagent roster, for
// independent inspection (Explain-style tooling, tests). Never nil on a
// non-nil receiver.
func (b *harnessSubagentBinding) Definitions() map[string]*agent.Definition {
	out := make(map[string]*agent.Definition, len(b.defs))
	for k, v := range b.defs {
		cp := *v
		out[k] = &cp
	}
	return out
}

// DelegationAllowed implements D2: fromID may delegate to toID iff toID is
// present in fromID's OWN declared `delegates` list. An agent with no
// declared delegates (including one absent from the roster entirely) permits
// nothing — this is a fail-closed allowlist, never a default-allow graph.
func (b *harnessSubagentBinding) DelegationAllowed(fromID, toID string) bool {
	if b == nil {
		return false
	}
	for _, id := range b.delegates[fromID] {
		if id == toID {
			return true
		}
	}
	return false
}

// buildHarnessSubagentBinding resolves plan.Subagents() into a closed
// harnessSubagentBinding. Returns (nil, nil) when the plan declares zero
// subagents — the zero-selection no-op this file's HARD INVARIANT requires.
//
// D4 (isolation): every def is built EXCLUSIVELY from plan.Subagents() plus
// plan.RevealAgentSystemPrompt/plan.Profiles(); there is no other input. When
// the plan declares agents, this map becomes the caller's entire roster
// (nothing is ever merged with an ambient/default one); when it declares
// none, the caller gets nil and must not substitute anything else.
//
// D1 (fail closed on unbindable fields): agent.Definition has no field for a
// subagent's Skills, SkillRoots, or Hooks. The existing client loaders that
// COULD express them — buildHarnessSkillTool and buildHarnessHooksManager —
// are both scoped to the DOCUMENT-level plan.Skills()/plan.Hooks() selection
// (they read the *harness.Plan directly, not an arbitrary []SkillSpec/
// []HookSpec), and this file is REUSE-ONLY against them (their signatures are
// not changed here — see this file's doc). Reusing them for a per-agents[]-
// entry selection would require changing that signature, which is out of
// this slice's scope. So rather than silently constructing a subagent with a
// narrower capability set than the manifest declares, a subagent that
// declares skills/skillSearchRoots/hooks is REJECTED here, fail-closed and by
// name, aborting the ENTIRE construction atomically (mirrors the
// buildHarnessHooksManager/buildHarnessSkillTool build-error abort in
// initHarnessAgent). Delegates IS bound for real: see harnessSubagentBinding.
//
// Tools: an EXPLICIT, non-empty tools[] selection is bound for real by
// reusing buildHarnessTools (client/harness_catalog_bind.go) — the same
// catalog-ID-to-constructor binder and exact-set discipline the document-
// level agent.tools selection already uses. An OMITTED or explicitly empty
// selection resolves to harnessNoToolsSentinel (zero provider-visible
// tools) — the same safe default the document-level zero-tool case uses —
// rather than "all tools": a subagent's capability set must never be WIDER
// than what the manifest states, and harness.AgentEntry.Tools' "no explicit
// override" semantics are not yet defined against a wider baseline in this
// slice.
func buildHarnessSubagentBinding(
	plan *harness.Plan,
	profileSpecs map[string]harness.ProfileSpec,
	profileCfgs map[string]provider.Config,
	mainProvider, mainModel string,
) (*harnessSubagentBinding, error) {
	specs := plan.Subagents()
	if len(specs) == 0 {
		return nil, nil
	}

	defs := make(map[string]*agent.Definition, len(specs))
	delegates := make(map[string][]string, len(specs))

	for _, spec := range specs {
		// D1(b): fail closed on fields agent.Definition cannot express yet.
		if len(spec.Skills) > 0 || len(spec.SkillRoots) > 0 {
			return nil, fmt.Errorf(
				"harness: agents[%s].skills: per-subagent skills are not yet bound on the harness path", spec.ID)
		}
		if len(spec.Hooks) > 0 {
			return nil, fmt.Errorf(
				"harness: agents[%s].hooks: per-subagent hooks are not yet bound on the harness path", spec.ID)
		}

		providerName, model := mainProvider, mainModel
		if spec.Profile != "" {
			ps, ok := profileSpecs[spec.Profile]
			if !ok {
				return nil, fmt.Errorf("harness: agents[%s].profile: unresolved profile id %q", spec.ID, spec.Profile)
			}
			if _, ok := profileCfgs[spec.Profile]; !ok {
				return nil, fmt.Errorf("harness: agents[%s].profile: profile %q has no sealed provider config", spec.ID, spec.Profile)
			}
			providerName, model = normalizeProviderName(ps.Provider), ps.Model
		}

		hints := []string{harnessNoToolsSentinel}
		if spec.ToolsSpecified && len(spec.Tools) > 0 {
			built, terr := buildHarnessTools(spec.Tools, plan.Workspace())
			if terr != nil {
				return nil, fmt.Errorf("harness: agents[%s].tools: %w", spec.ID, terr)
			}
			hints = make([]string, 0, len(built))
			for _, b := range built {
				hints = append(hints, b.runtimeName)
			}
			if len(hints) == 0 {
				hints = []string{harnessNoToolsSentinel}
			}
		}

		systemPrompt, _ := plan.RevealAgentSystemPrompt(spec.ID)

		var chain *fallback.Chain
		if spec.Fallback != nil && spec.Fallback.Enabled {
			c, cerr := buildHarnessFallbackChain(*spec.Fallback, profileSpecs, profileCfgs)
			if cerr != nil {
				return nil, fmt.Errorf("harness: agents[%s].fallback: %w", spec.ID, cerr)
			}
			chain = c
		}

		defs[spec.ID] = &agent.Definition{
			ID:           spec.ID,
			Name:         spec.ID,
			Provider:     providerName,
			Model:        model,
			SystemPrompt: systemPrompt,
			ToolHints:    hints,
			Chain:        chain,
			Capabilities: &agent.Capabilities{
				SupportsTools: true,
				MaxTurns:      spec.Limits.MaxTurns,
				Timeout:       time.Duration(spec.Limits.TimeoutSeconds) * time.Second,
			},
			Metadata: map[string]any{
				"type": "sub_agent",
			},
		}
		// D2: an agent with no declared delegates gets an explicit empty
		// slice, never a missing map entry a caller could mistake for "not
		// yet computed" — DelegationAllowed treats both the same way
		// (nothing permitted), but the roster itself stays fully populated.
		delegates[spec.ID] = append([]string(nil), spec.Delegates...)
	}

	return &harnessSubagentBinding{defs: defs, delegates: delegates}, nil
}
