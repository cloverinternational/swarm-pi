package harness

import (
	"strconv"
	"strings"
)

// Phase 8a — declarative profiles, fallback chains, and subagents.
//
// This file is DECLARATION + RESOLUTION ONLY. It parses, validates, and
// resolves references; it NEVER constructs a client/agent/provider, spawns a
// subagent, opens a network connection, or executes a fallback. Wiring is a
// later, client-side concern (Phase 8b), mirroring how SkillEntry (5a),
// HookEntry (6a), and McpEntry (7a) each preceded their execution phase.
//
// Three sections are modeled here and they share ONE reference model:
//
//   - profiles[]  reusable provider/model/limits/credential bundles, addressed
//     by a stable id. Profile ids are the ONLY vocabulary a fallback chain may
//     reference (see resolveFallback).
//   - fallback    a declarative CHAIN OF PROFILE IDS plus a closed retry
//     taxonomy. It never serializes a live provider.
//   - agents[]    subagents expressed as referenced specs with BOUNDED
//     overrides (profile / systemPrompt / tools / skills / hooks / limits /
//     fallback / delegates). Every override sub-shape is resolved by the SAME
//     resolver the primary agent uses — see resolveSubagent.
//
// CLOSED-POSTURE RULE (do not violate; see also client/harness_plan.go, which
// pins a.SetNoFallback(true) on the closed path): merely DECLARING a
// `fallback:` section must not weaken that posture. FallbackEntry.Enabled
// defaults to FALSE and only an explicit `enabled: true` expresses intent to
// use a chain. 8a wires nothing, so this is a schema-level guarantee: a
// Phase 8b consumer must read FallbackSpec.Enabled and may only relax
// SetNoFallback when it is true. An enabled chain is additionally required to
// be non-empty AND to declare a non-empty retryOn taxonomy, so "enabled" can
// never silently mean "retry on anything".

// Closed enum for fallback.retryOn. Any value outside this set is a fail-closed
// diagnostic; unknown taxonomy values are NEVER silently ignored.
const (
	// FallbackRetryTimeout covers a request that exceeded its deadline.
	FallbackRetryTimeout = "timeout"
	// FallbackRetryRateLimit covers an explicit provider rate-limit refusal.
	FallbackRetryRateLimit = "rateLimit"
	// FallbackRetryProviderError covers a provider-side error response that is
	// neither a rate limit nor an overload signal (for example a 5xx).
	FallbackRetryProviderError = "providerError"
	// FallbackRetryNetworkError covers transport-level failures (dial/TLS/reset)
	// where no provider response was received.
	FallbackRetryNetworkError = "networkError"
	// FallbackRetryOverloaded covers an explicit provider capacity/overload
	// signal that is distinct from a quota rate limit.
	FallbackRetryOverloaded = "overloaded"
)

// Closed enum for fallback.onExhaustion. Both values are defined precisely so a
// Phase 8b consumer has no room for a silent third behaviour:
//
//   - fail:    when the chain is exhausted the failure is surfaced to the
//     caller as an error. Nothing is retried and no result is produced.
//   - degrade: when the chain is exhausted the LAST attempt's outcome is
//     returned to the caller as an explicitly degraded (non-fatal) result. It
//     must still be reported as degraded; it must never be presented as a
//     successful primary-provider result.
const (
	// FallbackOnExhaustionFail surfaces the failure to the caller.
	FallbackOnExhaustionFail = "fail"
	// FallbackOnExhaustionDegrade returns the last attempt as a degraded result.
	FallbackOnExhaustionDegrade = "degrade"
)

// ProfileEntry is one declared entry under the v1alpha1 `profiles:` section: a
// reusable provider/model/limits bundle addressed by a stable id.
//
// Credential uses the SAME typed Ref machinery as provider.credential and is
// resolved with resolveSecret, so it is always a REFERENCE (env/file/vault) and
// never a literal. An `inline:` credential is rejected outright here (stricter
// than provider.credential, consistent with the 6a/7a discipline that a
// manifest must not carry secret material).
type ProfileEntry struct {
	ID         string `json:"id"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	BaseURL    string `json:"baseURL,omitempty"`
	Credential *Ref   `json:"credential,omitempty"`
	Limits     Limits `json:"limits,omitempty"`
}

// FallbackEntry is the declared `fallback:` section (also usable per-agent). It
// carries ONLY ids and declarative policy — never a serialized live provider.
type FallbackEntry struct {
	// Enabled must be EXPLICITLY true to express intent (closed-posture rule).
	Enabled bool `json:"enabled,omitempty"`
	// Chain is an ordered list of declared PROFILE ids. Profile ids are the one
	// and only vocabulary accepted here.
	Chain []string `json:"chain,omitempty"`
	// RetryOn is the closed failure taxonomy that may trigger the next chain
	// entry. Required (non-empty) when Enabled is true.
	RetryOn []string `json:"retryOn,omitempty"`
	// CooldownSeconds is a non-negative pause before the next attempt; 0 means
	// no cooldown.
	CooldownSeconds int `json:"cooldownSeconds,omitempty"`
	// MaxAttempts bounds total attempts; 0 (unset) resolves to len(Chain), i.e.
	// exactly one attempt per declared chain entry. It is never unbounded.
	MaxAttempts int `json:"maxAttempts,omitempty"`
	// OnExhaustion is one of fail|degrade; unset resolves to "fail".
	OnExhaustion string `json:"onExhaustion,omitempty"`
}

// AgentEntry is one declared entry under the v1alpha1 `agents:` section: a
// subagent expressed as a referenced spec with BOUNDED overrides. Every
// override field reuses the primary agent's sub-shape verbatim, which is what
// makes the effective capability set of each agent independently inspectable.
type AgentEntry struct {
	ID string `json:"id"`
	// Profile references a declared profiles[] id (optional; empty means the
	// subagent inherits the document's provider selection in Phase 8b).
	Profile string `json:"profile,omitempty"`
	// SystemPrompt is the same one-of(inline|file) shape as agent.systemPrompt.
	SystemPrompt *SystemPrompt `json:"systemPrompt,omitempty"`
	// Tools is the same tri-state selection as agent.tools, except that it is
	// OPTIONAL for a subagent: omitting it means "no explicit override" while
	// `tools: []` still means "explicitly zero tools".
	Tools ToolSelection `json:"tools,omitempty"`
	// Skills reuses the 5a skills section shape (sequence or object form).
	Skills SkillsSection `json:"skills,omitempty"`
	// Hooks reuses the 6a hooks section shape.
	Hooks []HookEntry `json:"hooks,omitempty"`
	// Limits is an optional override subset.
	Limits Limits `json:"limits,omitempty"`
	// Fallback is an optional per-agent override of the same fallback shape.
	Fallback *FallbackEntry `json:"fallback,omitempty"`
	// Delegates names other declared agents[] ids this subagent may delegate to.
	// It is DECLARATION ONLY in 8a (nothing is spawned), but it makes the
	// delegation graph explicit so cycles can be rejected before execution.
	Delegates []string `json:"delegates,omitempty"`
}

// ProfileSpec is one resolved profile carried on the immutable Plan. It is
// fully redacted: the credential is described by its provenance label + state
// exactly like ProviderExplain, and the value lives only in the Plan's tainted
// side map (see Plan.ProfileCredential).
type ProfileSpec struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"baseURL,omitempty"`
	// CredentialRef is the non-secret provenance label (for example "env:NAME").
	CredentialRef string `json:"credentialRef,omitempty"`
	// CredentialState is present | deferred | absent.
	CredentialState string `json:"credentialState,omitempty"`
	Limits          Limits `json:"limits,omitempty"`
}

// FallbackSpec is the resolved fallback policy carried on the Plan. Every field
// is a non-secret id or enum value.
type FallbackSpec struct {
	// Enabled is the explicit opt-in. False (the default) means a Phase 8b
	// consumer must keep the closed posture (SetNoFallback(true)).
	Enabled         bool     `json:"enabled"`
	Chain           []string `json:"chain,omitempty"`
	RetryOn         []string `json:"retryOn,omitempty"`
	CooldownSeconds int      `json:"cooldownSeconds"`
	MaxAttempts     int      `json:"maxAttempts"`
	OnExhaustion    string   `json:"onExhaustion"`
}

// SubagentSpec is one resolved subagent carried on the immutable Plan. Like
// AgentExplain, the system prompt is described by hash + byte count + source
// only; the text is tainted and reachable only via Plan.RevealAgentSystemPrompt.
// Inline hook commands are likewise hashed here and revealed only via
// Plan.RevealAgentHookCommand.
type SubagentSpec struct {
	ID      string `json:"id"`
	Profile string `json:"profile,omitempty"`

	SystemPromptHash   string `json:"systemPromptHash,omitempty"`
	SystemPromptBytes  int    `json:"systemPromptBytes,omitempty"`
	SystemPromptSource string `json:"systemPromptSource,omitempty"`

	// ToolsSpecified distinguishes "no tools override" from "explicitly zero
	// tools", mirroring ToolSelection's tri-state.
	ToolsSpecified bool     `json:"toolsSpecified"`
	Tools          []string `json:"tools,omitempty"`

	Skills     []SkillSpec `json:"skills,omitempty"`
	SkillRoots []string    `json:"skillSearchRoots,omitempty"`
	Hooks      []HookSpec  `json:"hooks,omitempty"`

	Limits    Limits        `json:"limits,omitempty"`
	Fallback  *FallbackSpec `json:"fallback,omitempty"`
	Delegates []string      `json:"delegates,omitempty"`
}

// resolveAgents validates and resolves the profiles/fallback/agents sections.
// It performs NO construction and NO execution: it only resolves references,
// reuses the primary agent's resolvers for the override sub-shapes, and carries
// redacted specs onto the Plan.
//
// env is the document-scoped capability policy environment (Phase 8d). It is
// threaded verbatim into every subagent's `tools:` resolution so an agents[]
// entry is gated by exactly the same policy-class rules as the primary agent.
func resolveAgents(p *Plan, sourcePath, mdir string, profiles []ProfileEntry, fb *FallbackEntry, agents []AgentEntry, env toolPolicyEnv) Diagnostics {
	var ds Diagnostics

	// 1. Profiles (declared ids become the fallback chain vocabulary).
	profileSpecs, creds, profileIDs, pds := resolveProfiles(p, sourcePath, mdir, profiles)
	ds = append(ds, pds...)

	// 2. Document-level fallback.
	var fbSpec *FallbackSpec
	if fb != nil {
		spec, fds := resolveFallback(sourcePath, "fallback", "harness.fallback", fb, profileIDs)
		ds = append(ds, fds...)
		fbSpec = spec
		p.addProvenance("fallback", "manifest", "")
	}

	// 3. Subagents.
	subSpecs, prompts, hookCmds, ads := resolveSubagents(p, sourcePath, mdir, agents, profileIDs, env)
	ds = append(ds, ads...)

	if ds.HasErrors() {
		return ds
	}

	p.profiles = profileSpecs
	p.profileCredentials = creds
	p.fallback = fbSpec
	p.subagents = subSpecs
	p.agentPrompts = prompts
	p.agentHookCommands = hookCmds
	return ds
}

// resolveProfiles validates the profiles section. Duplicate/empty ids, missing
// provider/model, negative limits, and inline credentials are all fail-closed.
func resolveProfiles(p *Plan, sourcePath, mdir string, entries []ProfileEntry) ([]ProfileSpec, map[string]Secret, map[string]struct{}, Diagnostics) {
	var ds Diagnostics

	ids := make(map[string]struct{}, len(entries))
	specs := make([]ProfileSpec, 0, len(entries))
	creds := make(map[string]Secret, len(entries))

	for i, e := range entries {
		field := "profiles[" + strconv.Itoa(i) + "]"

		if e.ID == "" {
			ds = append(ds, newDiag("harness.profiles.id.missing", field+".id",
				"profile entry requires a non-empty id", sourcePath))
			continue
		}
		if _, dup := ids[e.ID]; dup {
			ds = append(ds, newDiag("harness.profiles.id.duplicate", field+".id",
				"duplicate profile id "+quote(e.ID), sourcePath))
			continue
		}
		ids[e.ID] = struct{}{}

		entryOK := true

		if e.Provider == "" {
			ds = append(ds, newDiag("harness.profiles.provider.missing", field+".provider",
				"profile provider is required", sourcePath))
			entryOK = false
		}
		if e.Model == "" {
			ds = append(ds, newDiag("harness.profiles.model.missing", field+".model",
				"profile model is required", sourcePath))
			entryOK = false
		}

		lds := validateLimits(sourcePath, field+".limits", "harness.profiles.limits", e.Limits)
		if len(lds) > 0 {
			ds = append(ds, lds...)
			entryOK = false
		}

		spec := ProfileSpec{
			ID:              e.ID,
			Provider:        e.Provider,
			Model:           e.Model,
			BaseURL:         e.BaseURL,
			Limits:          e.Limits,
			CredentialState: "absent",
		}

		if e.Credential != nil {
			// A profile credential must be an indirect REFERENCE. An inline
			// literal is rejected so a manifest can never carry secret material.
			if e.Credential.Inline != "" {
				ds = append(ds, newDiag("harness.profiles.credential.inlineForbidden", field+".credential",
					"profile credential must be a reference (env/file/vault); an inline literal secret is not allowed in a manifest", sourcePath))
				entryOK = false
			} else {
				sec, sd := resolveSecret(sourcePath, field+".credential", mdir, e.Credential)
				if sd != nil {
					ds = append(ds, *sd)
					entryOK = false
				} else {
					creds[e.ID] = sec
					spec.CredentialRef = sec.Source()
					switch {
					case sec.Deferred():
						spec.CredentialState = "deferred"
					case sec.Present():
						spec.CredentialState = "present"
					}
					p.addProvenance("profiles."+e.ID+".credential", sec.sourceOrigin(), sec.Source())
				}
			}
		}

		if !entryOK {
			continue
		}

		specs = append(specs, spec)
		p.addProvenance("profiles."+e.ID, "manifest", "")
	}

	return specs, creds, ids, ds
}

// resolveFallback validates one fallback block (document-level or per-agent)
// against the declared profile ids. codePrefix scopes the diagnostic codes so a
// per-agent override reports under its own namespace.
func resolveFallback(sourcePath, field, codePrefix string, fb *FallbackEntry, profileIDs map[string]struct{}) (*FallbackSpec, Diagnostics) {
	var ds Diagnostics

	// Chain: every entry must resolve to a DECLARED profile id, and an id may
	// appear at most once. A repeated id is a degenerate cycle (the chain would
	// revisit the same provider), so it is rejected with the cycle path shown.
	chain := make([]string, 0, len(fb.Chain))
	seen := make(map[string]int, len(fb.Chain))
	for i, id := range fb.Chain {
		cf := field + ".chain[" + strconv.Itoa(i) + "]"
		if id == "" {
			ds = append(ds, newDiag(codePrefix+".chain.empty", cf,
				"fallback chain entry must not be empty", sourcePath))
			continue
		}
		if at, dup := seen[id]; dup {
			ds = append(ds, newDiag(codePrefix+".chain.cycle", cf,
				"fallback chain revisits profile "+quote(id)+" (cycle: "+
					strings.Join(append(append([]string{}, fb.Chain[at:i]...), id), " -> ")+
					"); a chain must reference each profile at most once", sourcePath))
			continue
		}
		if _, ok := profileIDs[id]; !ok {
			ds = append(ds, newDiag(codePrefix+".chain.unknownProfile", cf,
				"fallback chain references unknown profile id "+quote(id)+
					"; it must match a declared profiles[].id", sourcePath))
			continue
		}
		seen[id] = i
		chain = append(chain, id)
	}

	// RetryOn: closed taxonomy; unknown values are rejected, never ignored.
	retryOn := make([]string, 0, len(fb.RetryOn))
	retrySeen := make(map[string]struct{}, len(fb.RetryOn))
	for i, r := range fb.RetryOn {
		rf := field + ".retryOn[" + strconv.Itoa(i) + "]"
		switch r {
		case FallbackRetryTimeout, FallbackRetryRateLimit, FallbackRetryProviderError,
			FallbackRetryNetworkError, FallbackRetryOverloaded:
		default:
			ds = append(ds, newDiag(codePrefix+".retryOn.invalid", rf,
				"fallback retryOn must be one of timeout, rateLimit, providerError, networkError, overloaded; got "+quote(r), sourcePath))
			continue
		}
		if _, dup := retrySeen[r]; dup {
			ds = append(ds, newDiag(codePrefix+".retryOn.duplicate", rf,
				"duplicate fallback retryOn value "+quote(r), sourcePath))
			continue
		}
		retrySeen[r] = struct{}{}
		retryOn = append(retryOn, r)
	}

	if fb.CooldownSeconds < 0 {
		ds = append(ds, newDiag(codePrefix+".cooldownSeconds.invalid", field+".cooldownSeconds",
			"fallback cooldownSeconds must not be negative", sourcePath))
	}
	if fb.MaxAttempts < 0 {
		ds = append(ds, newDiag(codePrefix+".maxAttempts.invalid", field+".maxAttempts",
			"fallback maxAttempts must not be negative", sourcePath))
	}

	onExhaustion := fb.OnExhaustion
	switch onExhaustion {
	case FallbackOnExhaustionFail, FallbackOnExhaustionDegrade:
	case "":
		onExhaustion = FallbackOnExhaustionFail
	default:
		ds = append(ds, newDiag(codePrefix+".onExhaustion.invalid", field+".onExhaustion",
			"fallback onExhaustion must be one of fail, degrade; got "+quote(fb.OnExhaustion), sourcePath))
	}

	// CLOSED-POSTURE: an explicit opt-in must also be fully specified, so
	// "enabled" can never silently mean "empty chain" or "retry on anything".
	if fb.Enabled {
		if len(fb.Chain) == 0 {
			ds = append(ds, newDiag(codePrefix+".chain.required", field+".chain",
				"fallback chain must be non-empty when fallback is enabled", sourcePath))
		}
		if len(fb.RetryOn) == 0 {
			ds = append(ds, newDiag(codePrefix+".retryOn.required", field+".retryOn",
				"fallback retryOn must declare at least one failure kind when fallback is enabled", sourcePath))
		}
	}

	if ds.HasErrors() {
		return nil, ds
	}

	maxAttempts := fb.MaxAttempts
	if maxAttempts == 0 {
		// Documented default: exactly one attempt per declared chain entry.
		maxAttempts = len(chain)
	}

	return &FallbackSpec{
		Enabled:         fb.Enabled,
		Chain:           chain,
		RetryOn:         retryOn,
		CooldownSeconds: fb.CooldownSeconds,
		MaxAttempts:     maxAttempts,
		OnExhaustion:    onExhaustion,
	}, ds
}

// resolveSubagents validates the agents section and resolves each subagent's
// bounded overrides by REUSING the primary agent's resolvers (see
// resolveSubagent). It then rejects any cycle in the delegation graph.
func resolveSubagents(p *Plan, sourcePath, mdir string, entries []AgentEntry, profileIDs map[string]struct{}, env toolPolicyEnv) ([]SubagentSpec, map[string]string, map[string]map[string]string, Diagnostics) {
	var ds Diagnostics

	ids := make(map[string]struct{}, len(entries))
	order := make([]string, 0, len(entries))
	fieldOf := make(map[string]string, len(entries))
	edges := make(map[string][]string, len(entries))
	specs := make([]SubagentSpec, 0, len(entries))
	prompts := make(map[string]string, len(entries))
	hookCmds := make(map[string]map[string]string, len(entries))

	// Pass 1: id uniqueness (needed before delegate references can be checked).
	for i, e := range entries {
		field := "agents[" + strconv.Itoa(i) + "]"
		if e.ID == "" {
			ds = append(ds, newDiag("harness.agents.id.missing", field+".id",
				"agent entry requires a non-empty id", sourcePath))
			continue
		}
		if _, dup := ids[e.ID]; dup {
			ds = append(ds, newDiag("harness.agents.id.duplicate", field+".id",
				"duplicate agent id "+quote(e.ID), sourcePath))
			continue
		}
		ids[e.ID] = struct{}{}
		order = append(order, e.ID)
		fieldOf[e.ID] = field
	}

	// Pass 2: per-entry resolution.
	for i, e := range entries {
		field := "agents[" + strconv.Itoa(i) + "]"
		if e.ID == "" {
			continue
		}
		if fieldOf[e.ID] != field {
			// A duplicate id already reported in pass 1.
			continue
		}

		spec, prompt, cmds, delegates, eds := resolveSubagent(p, sourcePath, mdir, field, e, profileIDs, ids, env)
		if len(eds) > 0 {
			ds = append(ds, eds...)
			continue
		}
		edges[e.ID] = delegates
		if prompt != "" {
			prompts[e.ID] = prompt
		}
		if len(cmds) > 0 {
			hookCmds[e.ID] = cmds
		}
		specs = append(specs, spec)
	}

	// Pass 3: cycle validation over the delegation graph. Skipped when earlier
	// errors exist, so a cycle report is never built from a partial graph.
	if !ds.HasErrors() {
		ds = append(ds, detectDelegateCycles(sourcePath, order, edges, fieldOf)...)
	}

	return specs, prompts, hookCmds, ds
}

// resolveSubagent resolves ONE subagent. Every override sub-shape is delegated
// to the exact resolver the primary agent uses — resolvePrompt, resolveTools,
// resolveSkills, resolveHooks — by running them against a scratch Plan and then
// re-scoping their diagnostics/provenance onto this agent's field namespace
// (see rescopeDiags). This keeps ONE implementation of each sub-shape rather
// than forking a parallel subagent-only variant.
func resolveSubagent(p *Plan, sourcePath, mdir, field string, e AgentEntry, profileIDs, agentIDs map[string]struct{}, env toolPolicyEnv) (SubagentSpec, string, map[string]string, []string, Diagnostics) {
	var ds Diagnostics

	spec := SubagentSpec{ID: e.ID, Limits: e.Limits}

	// Profile reference integrity.
	if e.Profile != "" {
		if _, ok := profileIDs[e.Profile]; !ok {
			ds = append(ds, newDiag("harness.agents.profile.unknown", field+".profile",
				"agent references unknown profile id "+quote(e.Profile)+
					"; it must match a declared profiles[].id", sourcePath))
		} else {
			spec.Profile = e.Profile
		}
	}

	ds = append(ds, validateLimits(sourcePath, field+".limits", "harness.agents.limits", e.Limits)...)

	scratch := &Plan{}

	// systemPrompt: REUSE resolvePrompt (one-of inline|file + containment +
	// taint). Optional for a subagent, so it is only invoked when declared.
	if e.SystemPrompt != nil {
		pds := resolvePrompt(scratch, sourcePath, mdir, e.SystemPrompt)
		ds = append(ds, rescopeDiags(pds, field+".systemPrompt", "agent.systemPrompt",
			"harness.agent.systemPrompt", "harness.agents.systemPrompt")...)
	}

	// tools: REUSE resolveTools (same capability catalog as agent.tools).
	// The SAME toolPolicyEnv is passed, so a subagent is gated by exactly the
	// same Phase 8d policy-class rules as the primary agent — an agents[]
	// entry can never be a softer path to a DEFER-DISCOVER or an
	// unacknowledged NEVER-DEFAULT capability.
	if e.Tools.Specified {
		tds := resolveTools(scratch, sourcePath, e.Tools, env)
		ds = append(ds, rescopeDiags(tds, field+".tools", "agent.tools",
			"harness.agent.tools", "harness.agents.tools")...)
	}

	// skills: REUSE the 5a resolver and section shape.
	sds := resolveSkills(scratch, sourcePath, mdir, e.Skills)
	ds = append(ds, rescopeDiags(sds, field+".skills", "skills",
		"harness.skills", "harness.agents.skills")...)

	// hooks: REUSE the 6a resolver and entry shape.
	hds := resolveHooks(scratch, sourcePath, mdir, e.Hooks)
	ds = append(ds, rescopeDiags(hds, field+".hooks", "hooks",
		"harness.hooks", "harness.agents.hooks")...)

	// Per-agent fallback override: same shape, same vocabulary, own namespace.
	if e.Fallback != nil {
		fbSpec, fds := resolveFallback(sourcePath, field+".fallback", "harness.agents.fallback", e.Fallback, profileIDs)
		ds = append(ds, fds...)
		spec.Fallback = fbSpec
	}

	// Delegate references (declaration only; nothing is spawned in 8a).
	delegates := make([]string, 0, len(e.Delegates))
	dseen := make(map[string]struct{}, len(e.Delegates))
	for j, d := range e.Delegates {
		df := field + ".delegates[" + strconv.Itoa(j) + "]"
		if d == "" {
			ds = append(ds, newDiag("harness.agents.delegates.empty", df,
				"agent delegate entry must not be empty", sourcePath))
			continue
		}
		if _, dup := dseen[d]; dup {
			ds = append(ds, newDiag("harness.agents.delegates.duplicate", df,
				"duplicate delegate id "+quote(d), sourcePath))
			continue
		}
		if _, ok := agentIDs[d]; !ok {
			ds = append(ds, newDiag("harness.agents.delegates.unknown", df,
				"agent delegates to unknown agent id "+quote(d)+
					"; it must match a declared agents[].id", sourcePath))
			continue
		}
		dseen[d] = struct{}{}
		delegates = append(delegates, d)
	}

	if ds.HasErrors() {
		return SubagentSpec{}, "", nil, nil, ds
	}

	// Lift the reused resolvers' results off the scratch plan.
	if e.SystemPrompt != nil {
		spec.SystemPromptHash = scratch.promptHash
		spec.SystemPromptBytes = scratch.promptBytes
		spec.SystemPromptSource = scratch.promptSource
	}
	spec.ToolsSpecified = e.Tools.Specified
	if e.Tools.Specified {
		spec.Tools = copyStringSlice(scratch.tools)
		if spec.Tools == nil {
			spec.Tools = []string{}
		}
	}
	if len(scratch.skills) > 0 {
		spec.Skills = scratch.Skills()
	}
	if len(scratch.searchRoots) > 0 {
		spec.SkillRoots = scratch.SkillSearchRoots()
	}
	if len(scratch.hooks) > 0 {
		spec.Hooks = scratch.Hooks()
	}
	if len(delegates) > 0 {
		spec.Delegates = delegates
	}

	// Carry provenance from the reused resolvers under this agent's namespace.
	p.addProvenance("agents."+e.ID, "manifest", "")
	for _, fs := range scratch.provenance {
		p.addProvenance("agents."+e.ID+"."+strings.TrimPrefix(fs.Field, "agent."), fs.Origin, fs.Ref)
	}

	cmds := make(map[string]string, len(scratch.hookCommands))
	for k, v := range scratch.hookCommands {
		cmds[k] = v
	}

	return spec, scratch.promptText, cmds, delegates, nil
}

// rescopeDiags maps diagnostics emitted by a REUSED primary-agent resolver onto
// a subagent's field path and code namespace, so reuse never produces a
// misleading "agent.tools"/"skills[0]" field path for an agents[] entry. Generic
// codes (for example harness.path.missing) are intentionally left untouched.
func rescopeDiags(ds Diagnostics, fieldPrefix, innerField, innerCode, outerCode string) Diagnostics {
	if len(ds) == 0 {
		return nil
	}
	out := make(Diagnostics, len(ds))
	copy(out, ds)
	for i := range out {
		out[i].FieldPath = fieldPrefix + strings.TrimPrefix(out[i].FieldPath, innerField)
		if strings.HasPrefix(out[i].Code, innerCode) {
			out[i].Code = outerCode + out[i].Code[len(innerCode):]
		}
	}
	return out
}

// validateLimits rejects negative limit values under the given field/code
// namespace.
func validateLimits(sourcePath, field, codePrefix string, l Limits) Diagnostics {
	var ds Diagnostics
	if l.MaxOutputTokens < 0 {
		ds = append(ds, newDiag(codePrefix+".maxOutputTokens.invalid", field+".maxOutputTokens",
			"maxOutputTokens must not be negative", sourcePath))
	}
	if l.MaxTurns < 0 {
		ds = append(ds, newDiag(codePrefix+".maxTurns.invalid", field+".maxTurns",
			"maxTurns must not be negative", sourcePath))
	}
	if l.TimeoutSeconds < 0 {
		ds = append(ds, newDiag(codePrefix+".timeoutSeconds.invalid", field+".timeoutSeconds",
			"timeoutSeconds must not be negative", sourcePath))
	}
	return ds
}

// detectDelegateCycles rejects cycles in the subagent delegation graph.
//
// Algorithm: iterative-intent depth-first search with three-colour marking —
// white (unvisited), grey (on the current DFS stack), black (fully explored) —
// over the directed graph agents[].id -> agents[].delegates[]. Encountering a
// GREY node closes a cycle, and the cycle path is reconstructed from the
// current stack, so the diagnostic reports the actual path (for example
// "a -> b -> a"). This catches self-references (a -> a) and true multi-hop
// cycles of any length (a -> b -> c -> a). It is a proper traversal, NOT a
// depth cap: every node and edge is visited at most once, so an acyclic but
// deeply nested delegation graph is accepted rather than falsely rejected.
// Roots are visited in declaration order and edges in declared order, so the
// reported cycle is deterministic.
func detectDelegateCycles(sourcePath string, order []string, edges map[string][]string, fieldOf map[string]string) Diagnostics {
	const (
		white = 0
		grey  = 1
		black = 2
	)

	color := make(map[string]int, len(order))
	stack := make([]string, 0, len(order))
	var found Diagnostics

	var visit func(id string) bool
	visit = func(id string) bool {
		color[id] = grey
		stack = append(stack, id)
		for _, next := range edges[id] {
			switch color[next] {
			case grey:
				at := 0
				for i, s := range stack {
					if s == next {
						at = i
						break
					}
				}
				path := append(append([]string{}, stack[at:]...), next)
				found = append(found, newDiag("harness.agents.delegates.cycle", fieldOf[next]+".delegates",
					"agent delegation cycle detected: "+strings.Join(path, " -> ")+
						"; delegation references must form a directed acyclic graph", sourcePath))
				return true
			case white:
				if visit(next) {
					return true
				}
			case black:
				// Already fully explored; cannot be part of a new cycle.
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return false
	}

	for _, id := range order {
		if color[id] != white {
			continue
		}
		stack = stack[:0]
		if visit(id) {
			return found
		}
	}
	return nil
}

// --- immutable accessors --------------------------------------------------

// Profiles returns a copy of the resolved profile specs carried on the plan.
func (p *Plan) Profiles() []ProfileSpec {
	out := make([]ProfileSpec, len(p.profiles))
	copy(out, p.profiles)
	return out
}

// ProfileCredential returns the tainted credential Secret for a declared
// profile id. Like Plan.Credential the value itself is only reachable through
// Secret.Reveal, and it never appears in Explain/ExplainJSON or the digest.
func (p *Plan) ProfileCredential(id string) (Secret, bool) {
	s, ok := p.profileCredentials[id]
	return s, ok
}

// Fallback returns a copy of the resolved document-level fallback policy. ok is
// false when no fallback section was declared. A declared-but-not-enabled
// policy returns ok==true with Enabled==false: declaring the section never, by
// itself, weakens the closed posture.
func (p *Plan) Fallback() (FallbackSpec, bool) {
	if p.fallback == nil {
		return FallbackSpec{}, false
	}
	out := *p.fallback
	out.Chain = copyStringSlice(p.fallback.Chain)
	out.RetryOn = copyStringSlice(p.fallback.RetryOn)
	return out, true
}

// Subagents returns a deep copy of the resolved subagent specs carried on the
// plan.
func (p *Plan) Subagents() []SubagentSpec {
	out := make([]SubagentSpec, len(p.subagents))
	copy(out, p.subagents)
	for i := range out {
		out[i].Tools = copyStringSlice(out[i].Tools)
		out[i].SkillRoots = copyStringSlice(out[i].SkillRoots)
		out[i].Delegates = copyStringSlice(out[i].Delegates)
		if out[i].Skills != nil {
			sk := make([]SkillSpec, len(out[i].Skills))
			copy(sk, out[i].Skills)
			out[i].Skills = sk
		}
		if out[i].Hooks != nil {
			hk := make([]HookSpec, len(out[i].Hooks))
			copy(hk, out[i].Hooks)
			for j := range hk {
				hk[j].Environment = copyStringSlice(hk[j].Environment)
			}
			out[i].Hooks = hk
		}
		if out[i].Fallback != nil {
			fb := *out[i].Fallback
			fb.Chain = copyStringSlice(out[i].Fallback.Chain)
			fb.RetryOn = copyStringSlice(out[i].Fallback.RetryOn)
			out[i].Fallback = &fb
		}
	}
	return out
}

// RevealAgentSystemPrompt returns the resolved system prompt text for a
// declared subagent id, for a trusted later-phase consumer (Phase 8b). It
// mirrors RevealSystemPrompt: the text is tainted and never used by
// Explain/ExplainJSON, Digest, or Provenance.
func (p *Plan) RevealAgentSystemPrompt(agentID string) (prompt string, ok bool) {
	prompt, ok = p.agentPrompts[agentID]
	return prompt, ok
}

// RevealAgentHookCommand returns the resolved inline command text for a
// type == command hook declared under a subagent, mirroring RevealHookCommand.
func (p *Plan) RevealAgentHookCommand(agentID, hookID string) (command string, ok bool) {
	byHook, ok := p.agentHookCommands[agentID]
	if !ok {
		return "", false
	}
	command, ok = byHook[hookID]
	return command, ok
}
