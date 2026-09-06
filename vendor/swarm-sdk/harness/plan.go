package harness

import (
	"sort"
)

// FieldSource records where one resolved field's value came from. It is
// non-secret: Ref is a redacted provenance label (for example "env:NAME" or a
// manifest-relative path), never a value.
type FieldSource struct {
	Field  string `json:"field"`
	Origin string `json:"origin"` // manifest | file | env | vault | inline | default
	Ref    string `json:"ref,omitempty"`
}

// Plan is the immutable result of compiling a harness document. All fields are
// unexported and exposed only through copying accessors, so a Plan cannot be
// mutated after construction. Secret material (credential, prompt text) is
// tainted and never surfaces through Explain, Digest, Provenance, or JSON.
type Plan struct {
	apiVersion string
	kind       string
	metadata   Metadata

	workspace string
	storage   string
	watch     bool

	providerID string
	model      string
	baseURL    string
	credential Secret

	promptText   string // tainted; only via RevealSystemPrompt
	promptHash   string
	promptBytes  int
	promptSource string

	tools       []string
	limits      Limits
	permissions Permissions
	interfaces  Interfaces

	skills      []SkillSpec
	searchRoots []string

	// Phase 10c: the applied compatibility preset name ("" if none — the D4
	// default, byte-identical to pre-10c) and the documented, per-cause list
	// of TUI behaviors that preset deliberately does not reproduce (D3). Both
	// are non-secret static catalog data (see presets.go) and are empty
	// together; compatibilityShortfall is never populated when compatibility
	// is "".
	compatibility          string
	compatibilityShortfall []PresetShortfallEntry

	// skillBaseDir is the absolute manifest directory, retained so a trusted
	// later-phase consumer (Phase 5b client) can resolve the plan's selected
	// skills from within the manifest subtree. It is a TAINTED host path: like
	// promptText it is deliberately EXCLUDED from Explain/ExplainJSON, Digest,
	// and Provenance, and is surfaced only through RevealSkillBaseDir.
	skillBaseDir string

	hooks        []HookSpec
	hookCommands map[string]string // tainted; only via RevealHookCommand

	mcpServers []McpServerSpec

	// Phase 8a declarative sections. profileCredentials, agentPrompts, and
	// agentHookCommands are TAINTED side maps (like credential/promptText/
	// hookCommands): they are excluded from Explain/ExplainJSON, Digest, and
	// Provenance and are reachable only through the explicit Reveal-style
	// accessors in agents.go.
	profiles           []ProfileSpec
	profileCredentials map[string]Secret // tainted; only via ProfileCredential
	fallback           *FallbackSpec
	subagents          []SubagentSpec
	agentPrompts       map[string]string            // tainted; only via RevealAgentSystemPrompt
	agentHookCommands  map[string]map[string]string // tainted; only via RevealAgentHookCommand

	// Phase 9a resolved budgets (see budgets.go). Non-secret by construction:
	// kinds, numbers, enforcement classes, and exhaustion actions only. Empty
	// when the manifest declares no budgets, which keeps the redacted surface
	// (and therefore the digest) byte-identical to pre-9a.
	budgets []BudgetSpec

	// Phase 9b resolved schedules and the runtime bindings they require (see
	// schedules.go). Both are non-secret by construction: ids, cron strings,
	// zone names, enum values, counts, and binding names only — a schedule
	// carries no prompt body. Both are empty when the manifest declares no
	// schedules, which keeps the redacted surface (and therefore the digest)
	// byte-identical to pre-9b.
	schedules []ScheduleSpec
	bindings  []RuntimeBindingRequirement

	// Phase 9c resolved workflows (see workflows.go). Non-secret by
	// construction: ids, the manifest-relative reference, the referenced file's
	// content hash and version, enum values, counts, sorted map KEY lists, and
	// prompt HASHES — never a prompt body, a prompt template, a metadata value,
	// or a credential from the referenced file. Empty when the manifest declares
	// no workflows, which keeps the redacted surface (and therefore the digest)
	// byte-identical to pre-9c.
	workflows []WorkflowSpec

	provenance []FieldSource
	digest     string
}

// Compile discovers, loads, validates, resolves, and compiles a harness file
// into an immutable Plan. It has no runtime side effects beyond reading the
// manifest and any referenced asset files.
func Compile(explicitPath string) (*Plan, error) {
	doc, sourcePath, err := Load(explicitPath)
	if err != nil {
		return nil, err
	}
	return compileDocument(doc, sourcePath)
}

// CompileBytes compiles an in-memory manifest. sourcePath is used for format
// detection, diagnostics, and manifest-relative resolution (its directory is the
// resolution root), but the bytes are used verbatim instead of re-reading it.
func CompileBytes(raw []byte, sourcePath string) (*Plan, error) {
	doc, ds := strictDecode(sourcePath, raw)
	if ds.HasErrors() {
		return nil, ds
	}
	return compileDocument(doc, sourcePath)
}

// compileDocument runs semantic validation and resolution, producing a Plan or
// an ordered set of diagnostics.
func compileDocument(doc *Document, sourcePath string) (*Plan, error) {
	var ds Diagnostics

	// 1. Version / kind.
	ds = append(ds, validateVersion(sourcePath, doc.APIVersion, doc.Kind)...)

	// 2. Metadata.
	if doc.Metadata.Name == "" {
		ds = append(ds, newDiag("harness.metadata.name.missing", "metadata.name", "metadata.name is required", sourcePath))
	}

	mdir, mderr := manifestDir(sourcePath)
	if mderr != nil {
		ds = append(ds, newDiag("harness.path.manifestDir", "", "could not determine manifest directory", sourcePath))
		return nil, ds.sorted()
	}

	p := &Plan{
		apiVersion:  doc.APIVersion,
		kind:        doc.Kind,
		metadata:    copyMetadata(doc.Metadata),
		watch:       doc.Runtime.Watch,
		providerID:  doc.Provider.ID,
		model:       doc.Provider.Model,
		baseURL:     doc.Provider.BaseURL,
		limits:      doc.Agent.Limits,
		permissions: normalizePermissions(doc.Permissions),
		interfaces:  copyInterfaces(doc.Interfaces),
	}

	// Retain the absolute manifest directory as a tainted base for later-phase
	// skill loading. It never enters the redacted plan surface (Explain/Digest).
	p.skillBaseDir = mdir

	// 3. Runtime paths (manifest-relative; no CWD dependence, no writes).
	p.workspace = resolveDir(mdir, doc.Runtime.Workspace)
	p.storage = resolveDir(mdir, doc.Runtime.Storage)
	p.addProvenance("runtime.workspace", originFor(doc.Runtime.Workspace), doc.Runtime.Workspace)
	p.addProvenance("runtime.storage", originFor(doc.Runtime.Storage), doc.Runtime.Storage)

	// 4. Provider.
	if doc.Provider.ID == "" {
		ds = append(ds, newDiag("harness.provider.id.missing", "provider.id", "provider.id is required", sourcePath))
	}
	if doc.Provider.Model == "" {
		ds = append(ds, newDiag("harness.provider.model.missing", "provider.model", "provider.model is required", sourcePath))
	}
	if doc.Provider.Credential != nil {
		sec, sd := resolveSecret(sourcePath, "provider.credential", mdir, doc.Provider.Credential)
		if sd != nil {
			ds = append(ds, *sd)
		} else {
			p.credential = sec
			p.addProvenance("provider.credential", sec.sourceOrigin(), sec.Source())
		}
	}

	// 5. System prompt (one-of inline|file, required for the primary agent).
	ds = append(ds, resolvePrompt(p, sourcePath, mdir, doc.Agent.SystemPrompt)...)

	// 5b. Capability policy environment (Phase 8d). The NEVER-DEFAULT
	// acknowledgement list and the document's credential-declaration fact are
	// both document-scoped, so they are resolved ONCE here and threaded into
	// every `tools:` resolution (primary agent AND every agents[] entry) so a
	// subagent can never be a softer gate than the primary agent.
	ack, ackDiags := resolveNeverDefaultAck(sourcePath, doc.Permissions.AcknowledgeNeverDefault)
	ds = append(ds, ackDiags...)
	env := toolPolicyEnv{ack: ack, credentialDeclared: documentDeclaresCredential(doc)}

	// 6. Tools (REQUIRED; tri-state; exact catalog IDs; policy-class enforced).
	// Phase 10c: `compatibility:` (D3/D4/D5), when set, supplies the
	// effective selection INSTEAD of doc.Agent.Tools (which must then be
	// omitted); either way the result flows through the identical
	// resolveTools/checkCapabilityPolicy gate below (D1) — a preset is never a
	// second, softer validation path.
	toolSel, appliedPreset, presetDiags := resolveCompatibilityPreset(sourcePath, doc.Compatibility, doc.Agent.Tools)
	ds = append(ds, presetDiags...)
	ds = append(ds, resolveTools(p, sourcePath, toolSel, env)...)
	if appliedPreset.Name != "" {
		p.compatibility = appliedPreset.Name
		p.compatibilityShortfall = copyShortfallSlice(appliedPreset.Shortfall)
		p.addProvenance("compatibility", "manifest", appliedPreset.Name)
	}

	// 6b. Skills (declarative; declaration + resolution only, no loading/exec).
	ds = append(ds, resolveSkills(p, sourcePath, mdir, doc.Skills)...)

	// 6c. Hooks (declarative; declaration + resolution only, no execution).
	ds = append(ds, resolveHooks(p, sourcePath, mdir, doc.Hooks)...)

	// 6d. Mcp (declarative; declaration + resolution only, no connection).
	ds = append(ds, resolveMcp(p, sourcePath, mdir, doc.Mcp)...)

	// 6e. Profiles / fallback / subagents (declarative; declaration + resolution
	// only — no agent construction, no provider selection, no subagent spawn).
	ds = append(ds, resolveAgents(p, sourcePath, mdir, doc.Profiles, doc.Fallback, doc.Agents, env)...)

	// 6f. Budgets (Phase 9a; declaration + resolution only — every bound is
	// classified against what the runtime can actually enforce, and nothing is
	// enforced, measured, or scheduled here). agent.limits is passed so the D5
	// overlap rule can reject a bound that is declared in two places.
	ds = append(ds, resolveBudgets(p, sourcePath, doc.Budgets, doc.Agent.Limits)...)

	// 6g. Schedules (Phase 9b; declaration + resolution only — no scheduler is
	// constructed, no cron expression registered, no clock read, no goroutine
	// started). It runs AFTER 6e so a schedule's target can be resolved against
	// the declared profiles[]/agents[] ids, and it is told whether those id sets
	// are trustworthy: when an earlier section already failed they are
	// incomplete, and checking references against them would turn one real
	// error into a cascade of misleading "unknown target" diagnostics (the same
	// discipline as detectDelegateCycles and checkAcknowledgementsUsed).
	//
	// The normalized approvalMode is passed so a schedule's approvalPosture can
	// be refused when it would WIDEN the document's audited permissions.
	ds = append(ds, resolveSchedules(p, sourcePath, doc.Schedules, scheduleEnv{
		approvalMode:      p.permissions.ApprovalMode,
		agentIDs:          idSetOfSubagents(p.subagents),
		profileIDs:        idSetOfProfiles(p.profiles),
		targetsResolvable: !ds.HasErrors(),
	})...)

	// 6h. Workflows (Phase 9c; declaration + resolution only — no WorkflowEngine
	// is constructed, no group is planned, no agent or provider is built, no
	// goroutine is started). It runs AFTER 6 and 6e so the D5 capability seam can
	// be checked against the plan's ALREADY-RESOLVED effective tool set (primary
	// agent union every subagent) and after 6c so hook references can be checked
	// against the declared `hooks:` ids. As with schedules it is told whether
	// those sets are trustworthy: when an earlier section already failed they are
	// incomplete, and checking references against them would turn one real error
	// into a cascade of misleading "widens the capability set" diagnostics.
	wfUses, wfDiags := resolveWorkflows(p, sourcePath, mdir, doc.Workflows, workflowEnv{
		selected:   effectiveCapabilityIDs(p),
		hookIDs:    idSetOfHooks(p.hooks),
		resolvable: !ds.HasErrors(),
	})
	ds = append(ds, wfDiags...)

	// 6i. D4 reference integrity for `schedules[].target.kind: workflow`, plus the
	// workflow.engine binding those schedules require. This is a separate pass
	// because Phase 9b's resolveScheduleTarget is reached through scheduleEnv, and
	// D4 permits replacing exactly ONE branch of schedules.go — not extending its
	// env struct. The resolvable flag carries the same anti-cascade discipline.
	targetUses, targetDiags := checkScheduleWorkflowTargets(p, sourcePath, !ds.HasErrors())
	ds = append(ds, targetDiags...)

	// Fold this slice's binding requirements into the Phase 9b list. Both inputs
	// are empty for a manifest that declares no workflows, and mergeWorkflowBindings
	// is then a strict no-op, so the pre-9c plan surface is untouched.
	mergeWorkflowBindings(p, append(wfUses, targetUses...))

	// 6j. Dangling-acknowledgement check (Phase 8d). Every acknowledged id must
	// actually be selected by SOME agent. This runs only when nothing else
	// failed, because an earlier tools failure leaves the selection sets empty
	// and would turn one real error into a cascade of misleading "unused"
	// diagnostics (same discipline as detectDelegateCycles in agents.go).
	if !ds.HasErrors() {
		ds = append(ds, checkAcknowledgementsUsed(p, sourcePath, doc.Permissions.AcknowledgeNeverDefault)...)
	}

	if ds.HasErrors() {
		return nil, ds.sorted()
	}

	// 7. Deterministic provenance + digest over the redacted semantic plan.
	sort.SliceStable(p.provenance, func(i, j int) bool { return p.provenance[i].Field < p.provenance[j].Field })
	p.digest = computeDigest(p)

	return p, nil
}

// resolvePrompt enforces the one-of and reads a contained prompt file. The
// prompt text is tainted; only its hash/byte-count/source enter the plan surface.
func resolvePrompt(p *Plan, sourcePath, mdir string, sp *SystemPrompt) Diagnostics {
	if sp == nil {
		return Diagnostics{newDiag("harness.agent.systemPrompt.missing", "agent.systemPrompt",
			"agent.systemPrompt is required; set exactly one of inline or file", sourcePath)}
	}
	hasInline := sp.Inline != ""
	hasFile := sp.File != ""
	switch {
	case hasInline && hasFile:
		return Diagnostics{newDiag("harness.agent.systemPrompt.exclusive", "agent.systemPrompt",
			"agent.systemPrompt is a one-of; set only one of inline or file, not both", sourcePath)}
	case !hasInline && !hasFile:
		return Diagnostics{newDiag("harness.agent.systemPrompt.empty", "agent.systemPrompt",
			"agent.systemPrompt must set exactly one of inline or file", sourcePath)}
	case hasInline:
		p.setPrompt(sp.Inline, "inline", "inline")
		return nil
	default: // hasFile
		data, _, d := loadContainedFileBytes(sourcePath, "agent.systemPrompt.file", mdir, sp.File)
		if d != nil {
			return Diagnostics{*d}
		}
		p.setPrompt(string(data), "file", sp.File)
		return nil
	}
}

// toolPolicyEnv carries the document-scoped facts a capability's PolicyClass
// and PARITY_BASELINE qualifiers are enforced against (Phase 8d). It is passed
// by value into every `tools:` resolution so the primary agent and every
// agents[] entry are gated by IDENTICAL rules — a subagent must never be able
// to select something the primary agent could not.
type toolPolicyEnv struct {
	// ack is the set of ids named in permissions.acknowledgeNeverDefault. It
	// is already validated (known / not duplicated / actually NEVER-DEFAULT)
	// by resolveNeverDefaultAck before it reaches here.
	ack map[string]struct{}
	// credentialDeclared reports whether the document declares ANY credential
	// reference at all (provider.credential or a profiles[].credential). See
	// documentDeclaresCredential for exactly what this can and cannot prove.
	credentialDeclared bool
}

// resolveNeverDefaultAck validates permissions.acknowledgeNeverDefault ITSELF
// and returns the acknowledged id set. Every rule here is fail-closed and
// exists to keep the field from degenerating into a junk drawer:
//
//   - an empty string entry is rejected (an empty id acknowledges nothing);
//   - a duplicate entry is rejected (an id is acknowledged exactly once);
//   - an id that is not in the capability catalog is rejected;
//   - an id whose catalog class is NOT NEVER-DEFAULT is rejected, so the field
//     can only ever contain ids for which it is actually meaningful.
//
// The "acknowledged but never selected" (dangling) rule is NOT here: it needs
// the fully resolved selection sets of the primary agent and every subagent,
// so it lives in checkAcknowledgementsUsed.
func resolveNeverDefaultAck(sourcePath string, ids []string) (map[string]struct{}, Diagnostics) {
	if len(ids) == 0 {
		return nil, nil
	}
	var ds Diagnostics
	const field = "permissions.acknowledgeNeverDefault"
	ack := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			ds = append(ds, newDiag("harness.permissions.acknowledgeNeverDefault.empty", field,
				"permissions.acknowledgeNeverDefault entry must be a non-empty capability id", sourcePath))
			continue
		}
		if _, dup := ack[id]; dup {
			ds = append(ds, newDiag("harness.permissions.acknowledgeNeverDefault.duplicate", field,
				"duplicate acknowledged capability id "+quote(id), sourcePath))
			continue
		}
		c, known := LookupCapability(id)
		if !known {
			ds = append(ds, newDiag("harness.permissions.acknowledgeNeverDefault.unknown", field,
				"unknown capability id "+quote(id)+"; it is not in the capability catalog", sourcePath))
			continue
		}
		if c.Class != PolicyNeverDefault {
			ds = append(ds, newDiag("harness.permissions.acknowledgeNeverDefault.notNeverDefault", field,
				"capability "+quote(id)+" has policy class "+string(c.Class)+
					"; permissions.acknowledgeNeverDefault accepts only "+string(PolicyNeverDefault)+
					" capabilities, so remove it (selecting it in tools is already sufficient)", sourcePath))
			continue
		}
		ack[id] = struct{}{}
	}
	if ds.HasErrors() {
		return nil, ds
	}
	return ack, nil
}

// documentDeclaresCredential reports whether the document declares ANY typed
// credential reference (provider.credential or any profiles[].credential).
//
// SCOPE, STATED HONESTLY: this is a NECESSARY-condition check, not a
// sufficient one. The v1alpha1 schema has exactly two places a manifest can
// declare credential material, and neither is per-capability. So this can
// prove "this manifest declares no credential material at all, therefore a
// capability the baseline marks unconditionally credential-bound provably
// cannot function" — and it deliberately claims nothing beyond that. It does
// NOT verify that the declared credential is the right credential for the
// capability, nor that it resolves to a usable value at run time.
func documentDeclaresCredential(doc *Document) bool {
	if doc.Provider.Credential != nil {
		return true
	}
	for _, pr := range doc.Profiles {
		if pr.Credential != nil {
			return true
		}
	}
	return false
}

// checkCapabilityPolicy applies the Phase 8d policy-class and qualifier gates
// to ONE selected, catalogued capability. It returns nil when the selection is
// permitted; otherwise it returns the fail-closed diagnostic that rejects it.
// field/codePrefix are supplied by the caller so the diagnostic lands in the
// caller's namespace (agent.tools vs agents[i].tools via rescopeDiags).
func checkCapabilityPolicy(sourcePath, field string, c Capability, env toolPolicyEnv) *Diagnostic {
	switch c.Class {
	case PolicyDeferDiscover:
		// D1: hard reject, no escape hatch. The class means the capability's
		// exact contract/lifecycle is UNRESOLVED, so there is deliberately no
		// flag, posture, or override that makes it selectable. Making one
		// selectable requires reclassifying it in the catalog, which is a
		// reviewable change — not a manifest-author decision.
		d := newDiag("harness.agent.tools.deferDiscover", field,
			"capability "+quote(c.ID)+" has policy class "+string(PolicyDeferDiscover)+
				" and cannot be selected: its exact capability/lifecycle contract is unresolved ("+
				c.SideEffect+"). There is no acknowledgement or override that enables it; "+
				"it must first be reclassified in the capability catalog", sourcePath)
		return &d
	case PolicyNeverDefault:
		// D2: selection is necessary but not sufficient — the id must ALSO be
		// acknowledged individually.
		if _, ok := env.ack[c.ID]; !ok {
			d := newDiag("harness.agent.tools.neverDefault.unacknowledged", field,
				"capability "+quote(c.ID)+" has policy class "+string(PolicyNeverDefault)+
					" ("+c.SideEffect+") and requires an explicit named posture: add "+quote(c.ID)+
					" to permissions.acknowledgeNeverDefault. Selecting it in tools alone is not sufficient", sourcePath)
			return &d
		}
	}

	// D3: the OPT-IN class's stated prerequisites, checked where the manifest
	// can actually express them. Only an UNCONDITIONAL baseline qualifier
	// ("yes") is a stated prerequisite; "often"/"maybe"/"partly" are
	// deliberately conditional cells in PARITY_BASELINE and rejecting on them
	// would assert a requirement the baseline does not make.
	//
	// This gate is class-independent on purpose: a NEVER-DEFAULT capability
	// that is credential-bound must satisfy BOTH its acknowledgement and this
	// prerequisite. They are orthogonal gates, not alternatives.
	if c.CredentialBound == QYes && !env.credentialDeclared {
		d := newDiag("harness.agent.tools.credentialUnbound", field,
			"capability "+quote(c.ID)+" is credential-bound ("+string(QYes)+
				") but the manifest declares no credential reference at all; "+
				"declare provider.credential or a profiles[].credential", sourcePath)
		return &d
	}
	// NOTE (documented gap, deliberately NOT faked): Capability.HostBound is
	// NOT checked here. The v1alpha1 schema has no field in which a manifest
	// declares a host binding (broker/adapter/listener/shell) — `interfaces:`
	// is presentation-only and must never change tools or permissions — so
	// there is nothing to check a host-bound prerequisite AGAINST at compile
	// time. Asserting one would be a check that always passes or always fails
	// for reasons unrelated to the actual binding. It stays a runtime
	// preflight concern (see the catalog side-effect note on
	// interactive.ask_user_question: "missing broker must preflight-fail").
	return nil
}

// checkAcknowledgementsUsed rejects DANGLING acknowledgements: an id in
// permissions.acknowledgeNeverDefault that no agent actually selects. Without
// this rule the field could pre-authorize a high-impact capability that a
// later, unrelated edit silently starts using.
func checkAcknowledgementsUsed(p *Plan, sourcePath string, acked []string) Diagnostics {
	if len(acked) == 0 {
		return nil
	}
	selected := make(map[string]struct{}, len(p.tools))
	for _, id := range p.tools {
		selected[id] = struct{}{}
	}
	for _, sa := range p.subagents {
		for _, id := range sa.Tools {
			selected[id] = struct{}{}
		}
	}
	var ds Diagnostics
	seen := make(map[string]struct{}, len(acked))
	for _, id := range acked {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if _, ok := selected[id]; !ok {
			ds = append(ds, newDiag("harness.permissions.acknowledgeNeverDefault.unused",
				"permissions.acknowledgeNeverDefault",
				"capability "+quote(id)+" is acknowledged but is not selected in any agent's tools; "+
					"remove the acknowledgement or select the capability", sourcePath))
		}
	}
	return ds
}

// resolveTools enforces required/empty-vs-missing semantics, validates each
// selected ID against the stable catalog, and ENFORCES the catalog's policy
// class and stated qualifier prerequisites (Phase 8d). Every rule is
// fail-closed at compile time; none is advisory or deferred to runtime.
func resolveTools(p *Plan, sourcePath string, sel ToolSelection, env toolPolicyEnv) Diagnostics {
	if !sel.Specified {
		return Diagnostics{newDiag("harness.agent.tools.required", "agent.tools",
			"agent.tools is required for the primary agent; use [] to select zero tools (this is different from omitting the field)", sourcePath)}
	}

	var ds Diagnostics
	seen := make(map[string]struct{}, len(sel.IDs))
	out := make([]string, 0, len(sel.IDs))
	for _, id := range sel.IDs {
		if _, dup := seen[id]; dup {
			ds = append(ds, newDiag("harness.agent.tools.duplicate", "agent.tools",
				"duplicate tool id "+quote(id), sourcePath))
			continue
		}
		seen[id] = struct{}{}
		c, known := LookupCapability(id)
		if !known {
			ds = append(ds, newDiag("harness.agent.tools.unknown", "agent.tools",
				"unknown tool id "+quote(id)+"; it is not in the capability catalog", sourcePath))
			continue
		}
		if d := checkCapabilityPolicy(sourcePath, "agent.tools", c, env); d != nil {
			ds = append(ds, *d)
			continue
		}
		out = append(out, id)
	}
	if ds.HasErrors() {
		return ds
	}
	p.tools = out
	p.addProvenance("agent.tools", "manifest", "")
	return nil
}

// --- helpers --------------------------------------------------------------

func (p *Plan) setPrompt(text, origin, ref string) {
	p.promptText = text
	p.promptHash = hashString(text)
	p.promptBytes = len(text)
	p.promptSource = origin
	p.addProvenance("agent.systemPrompt", origin, ref)
}

func (p *Plan) addProvenance(field, origin, ref string) {
	p.provenance = append(p.provenance, FieldSource{Field: field, Origin: origin, Ref: ref})
}

func (s Secret) sourceOrigin() string {
	switch {
	case s.deferred:
		return "vault"
	case s.present:
		// source label is like "env:NAME"/"inline"/"file:path"
		if s.source == "inline" {
			return "inline"
		}
		if len(s.source) >= 4 && s.source[:4] == "env:" {
			return "env"
		}
		if len(s.source) >= 5 && s.source[:5] == "file:" {
			return "file"
		}
	}
	return "manifest"
}

func originFor(v string) string {
	if v == "" {
		return "default"
	}
	return "manifest"
}

// idSetOfSubagents / idSetOfProfiles expose the Phase 8a declared reference
// vocabularies as sets, so a Phase 9b schedule target is checked against exactly
// the ids the plan actually declares.
func idSetOfSubagents(specs []SubagentSpec) map[string]struct{} {
	out := make(map[string]struct{}, len(specs))
	for _, s := range specs {
		out[s.ID] = struct{}{}
	}
	return out
}

func idSetOfProfiles(specs []ProfileSpec) map[string]struct{} {
	out := make(map[string]struct{}, len(specs))
	for _, s := range specs {
		out[s.ID] = struct{}{}
	}
	return out
}

func normalizePermissions(in Permissions) Permissions {
	out := in
	if out.ApprovalMode == "" {
		out.ApprovalMode = "interactive"
	}
	// Defensive-copy the acknowledgement slice so the Plan never shares a
	// backing array with the caller's decoded Document (the Plan is immutable
	// after construction; a shared slice would be a mutation channel).
	out.AcknowledgeNeverDefault = copyStringSlice(in.AcknowledgeNeverDefault)
	return out
}

func copyMetadata(in Metadata) Metadata {
	out := Metadata{Name: in.Name, Description: in.Description}
	if in.Labels != nil {
		out.Labels = make(map[string]string, len(in.Labels))
		for k, v := range in.Labels {
			out.Labels[k] = v
		}
	}
	return out
}

func copyInterfaces(in Interfaces) Interfaces {
	out := Interfaces{Default: in.Default}
	if in.TUI != nil {
		t := *in.TUI
		out.TUI = &t
	}
	if in.Print != nil {
		pr := *in.Print
		out.Print = &pr
	}
	return out
}

// --- immutable accessors --------------------------------------------------

// APIVersion returns the document apiVersion.
func (p *Plan) APIVersion() string { return p.apiVersion }

// Kind returns the document kind.
func (p *Plan) Kind() string { return p.kind }

// Name returns the harness metadata name.
func (p *Plan) Name() string { return p.metadata.Name }

// Metadata returns a copy of the document metadata.
func (p *Plan) Metadata() Metadata { return copyMetadata(p.metadata) }

// Workspace returns the resolved absolute workspace directory.
func (p *Plan) Workspace() string { return p.workspace }

// Storage returns the resolved absolute storage directory.
func (p *Plan) Storage() string { return p.storage }

// Watch reports the runtime watch flag.
func (p *Plan) Watch() bool { return p.watch }

// ProviderID returns the requested provider id.
func (p *Plan) ProviderID() string { return p.providerID }

// Model returns the requested model id.
func (p *Plan) Model() string { return p.model }

// BaseURL returns the optional provider base URL.
func (p *Plan) BaseURL() string { return p.baseURL }

// Credential returns the tainted credential secret (redacted on display).
func (p *Plan) Credential() Secret { return p.credential }

// SystemPromptHash returns the sha256 of the resolved system prompt.
func (p *Plan) SystemPromptHash() string { return p.promptHash }

// SystemPromptBytes returns the byte length of the resolved system prompt.
func (p *Plan) SystemPromptBytes() int { return p.promptBytes }

// SystemPromptSource returns the prompt origin ("inline" or "file").
func (p *Plan) SystemPromptSource() string { return p.promptSource }

// RevealSystemPrompt returns the resolved prompt text for a trusted later-phase
// consumer. It is deliberately explicit and never used by Explain/Digest.
func (p *Plan) RevealSystemPrompt() string { return p.promptText }

// RevealSkillBaseDir returns the absolute manifest directory retained for a
// trusted later-phase consumer (Phase 5b client-side skill loading). Like
// RevealSystemPrompt it is deliberately explicit and TAINTED: it is never
// surfaced through Explain/ExplainJSON, Digest, or Provenance, so a compiled
// plan stays portable and secret-free on its redacted surface.
func (p *Plan) RevealSkillBaseDir() string { return p.skillBaseDir }

// Tools returns a copy of the resolved, ordered capability IDs.
func (p *Plan) Tools() []string {
	out := make([]string, len(p.tools))
	copy(out, p.tools)
	return out
}

// Compatibility returns the applied compatibility preset's name, or "" if
// none was applied (the D4 default — omitting `compatibility:` never applies
// one implicitly).
func (p *Plan) Compatibility() string { return p.compatibility }

// CompatibilityShortfall returns a defensive copy of the documented,
// per-cause list of TUI behaviors the applied preset deliberately does not
// reproduce (D3). It is nil when no preset was applied, or when the applied
// preset (e.g. "minimal") declares no shortfall.
func (p *Plan) CompatibilityShortfall() []PresetShortfallEntry {
	return copyShortfallSlice(p.compatibilityShortfall)
}

// copyShortfallSlice returns a defensive copy so neither the Plan's internal
// slice nor a caller's returned slice can alias the presetCatalog's static
// backing array.
func copyShortfallSlice(in []PresetShortfallEntry) []PresetShortfallEntry {
	if in == nil {
		return nil
	}
	out := make([]PresetShortfallEntry, len(in))
	copy(out, in)
	return out
}

// Limits returns the resolved limits.
func (p *Plan) Limits() Limits { return p.limits }

// Permissions returns a copy of the resolved permissions.
func (p *Plan) Permissions() Permissions {
	out := p.permissions
	if p.permissions.WorkspaceBoundary != nil {
		b := *p.permissions.WorkspaceBoundary
		out.WorkspaceBoundary = &b
	}
	out.AcknowledgeNeverDefault = copyStringSlice(p.permissions.AcknowledgeNeverDefault)
	return out
}

// Interfaces returns a copy of the resolved interface selection.
func (p *Plan) Interfaces() Interfaces { return copyInterfaces(p.interfaces) }

// Provenance returns a copy of per-field source records.
func (p *Plan) Provenance() []FieldSource {
	out := make([]FieldSource, len(p.provenance))
	copy(out, p.provenance)
	return out
}

// Digest returns the deterministic content digest of the redacted plan.
func (p *Plan) Digest() string { return p.digest }
