// Harness Phase 4b client transaction engine — ApplyHarnessPlan.
//
// ApplyHarnessPlan is the live-reconfiguration transaction for a client that was
// built from an immutable *harness.Plan (WithHarnessPlan). It diffs the currently
// applied plan against a proposed one, CLASSIFIES every changed field as HOT
// (safe to apply in-place), RESTART-REQUIRED (must rebuild the client), or
// FORBIDDEN (rejected outright), and then either:
//
//   - no-op        (identical digest): returns empty change sets, no mutation;
//   - forbidden    (or any validation error): returns the classification plus a
//     non-nil error and mutates NOTHING;
//   - restart-only: returns RestartRequired plus ErrHarnessRestartRequired and
//     mutates NOTHING (a future host performs the restart by building a client);
//   - hot-only:     applies atomically via the closed initHarnessAgent rebuild,
//     updates the stored applied plan + digest, and returns Applied populated.
//
// The engine is FAIL-CLOSED: every validation and preconstruction step runs
// BEFORE the live client is touched, so a bad plan can never leave the agent in a
// half-configured state. On the (narrow) chance the in-place rebuild itself
// fails, the prior plan is restored best-effort and remains the source of truth.
//
// Redaction: the returned diff never contains a raw system prompt or credential
// value. Prompts are compared by their "sha256:" hash, credentials by a redacted
// source label plus a short hash, and absolute workspace/storage paths by their
// location-portable path hash.
package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ErrNotHarnessClient is returned by ApplyHarnessPlan when the client was NOT
// built from a harness plan (WithHarnessPlan). Live harness reconfiguration is
// only defined for harness-constructed clients.
var ErrNotHarnessClient = errors.New("harness: ApplyHarnessPlan is only supported on clients built with WithHarnessPlan")

// ErrHarnessRestartRequired is the sentinel returned when a proposed plan is
// otherwise valid but contains at least one RESTART-REQUIRED change (provider,
// credential, workspace, storage, base URL, or presentation). The live client
// and its applied plan are left UNCHANGED; the host must build a new client to
// adopt such a plan. Callers detect it with errors.Is.
var ErrHarnessRestartRequired = errors.New("harness: plan change requires a client restart (classified, not applied)")

// ErrHarnessGovernedMutation is returned when a legacy mutable-client API is
// used on a client whose runtime is owned by a compiled harness plan.
var ErrHarnessGovernedMutation = errors.New("harness: legacy client mutation is disabled; use ApplyHarnessPlan")

func (c *Client) rejectHarnessGovernedMutation(op string) error {
	c.mu.RLock()
	governed := c.opts.harness != nil && c.opts.harness.plan != nil
	c.mu.RUnlock()
	if governed {
		return fmt.Errorf("%w: %s", ErrHarnessGovernedMutation, op)
	}
	return nil
}

// Change classes recorded on each HarnessFieldChange.
const (
	harnessClassHot       = "hot"
	harnessClassRestart   = "restart"
	harnessClassForbidden = "forbidden"
)

// Stable field identifiers used in the diff and the Applied/RestartRequired/
// Forbidden sets. They are stable, redaction-safe labels (never values).
const (
	harnessFieldProvider        = "provider"
	harnessFieldModel           = "model"
	harnessFieldBaseURL         = "baseURL"
	harnessFieldCredential      = "credential"
	harnessFieldSystemPrompt    = "systemPrompt"
	harnessFieldTools           = "tools"
	harnessFieldSkills          = "skills"
	harnessFieldHooks           = "hooks"
	harnessFieldMcp             = "mcp"
	harnessFieldProfiles        = "profiles"
	harnessFieldFallback        = "fallback"
	harnessFieldAgents          = "agents"
	harnessFieldMaxOutputTokens = "limits.maxOutputTokens"
	harnessFieldMaxTurns        = "limits.maxTurns"
	harnessFieldTimeoutSeconds  = "limits.timeoutSeconds"
	harnessFieldApprovalMode    = "approvalMode"
	harnessFieldWorkspaceBound  = "permissions.workspaceBoundary"
	harnessFieldAllowMutation   = "permissions.allowMutation"
	harnessFieldWorkspace       = "workspace"
	harnessFieldStorage         = "storage"
	harnessFieldInterfaces      = "interfaces"
)

// ApplyHarnessOptions mirrors WithHarnessAllowYolo for the live-apply path: an
// approvalMode of "yolo" in the NEW plan is only honored when AllowYolo is true.
// Without it, a plan requesting yolo is classified FORBIDDEN and rejected — yolo
// is never applied silently, exactly as during construction (D2).
type ApplyHarnessOptions struct {
	AllowYolo               bool
	ExecutableConsentDigest string
}

// HarnessFieldChange is a single redacted old->new change with its classification.
type HarnessFieldChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
	Class string `json:"class"` // hot | restart | forbidden
}

// HarnessPlanDiff is the redacted, per-field change list between two plans. Every
// value is safe to log: prompts/credentials/paths are represented by hashes or
// labels, never raw secrets.
type HarnessPlanDiff struct {
	Changes []HarnessFieldChange `json:"changes"`
}

// ApplyHarnessResult is the outcome of an ApplyHarnessPlan call. Applied,
// RestartRequired, and Forbidden are disjoint field-name sets; Diff carries the
// full per-field classification. Digest is the digest of the plan that is now
// authoritative (the new plan on a successful hot apply, otherwise the prior
// plan's digest — unchanged, since nothing was applied).
type ApplyHarnessResult struct {
	Diff            HarnessPlanDiff `json:"diff"`
	Applied         []string        `json:"applied"`
	RestartRequired []string        `json:"restartRequired"`
	Forbidden       []string        `json:"forbidden"`
	Digest          string          `json:"digest"`
}

// ApplyHarnessPlan diffs the client's currently-applied harness plan against
// newPlan, classifies each change, and applies HOT-only changes atomically. See
// the file doc for the full transaction semantics and the error sentinels
// (ErrNotHarnessClient, ErrHarnessRestartRequired).
//
// It is concurrency-safe: it holds the client's write lock for the entire
// transaction, so it cannot corrupt an agent mid-turn against a concurrent
// Chat/Execute/HarnessSnapshot.
func (c *Client) ApplyHarnessPlan(_ context.Context, newPlan *harness.Plan, opts ApplyHarnessOptions) (ApplyHarnessResult, error) {
	if newPlan == nil {
		return ApplyHarnessResult{}, fmt.Errorf("harness: ApplyHarnessPlan: nil newPlan")
	}

	c.mu.Lock()
	locked := true
	defer func() {
		if locked {
			c.mu.Unlock()
		}
	}()

	// 1. GUARD: only harness-built clients support live re-apply.
	hc := c.opts.harness
	if hc == nil || hc.plan == nil {
		return ApplyHarnessResult{}, ErrNotHarnessClient
	}
	oldPlan := hc.plan

	// 2. NO-OP: identical digest => nothing changed, empty change sets.
	if oldPlan.Digest() == newPlan.Digest() {
		return ApplyHarnessResult{Digest: newPlan.Digest()}, nil
	}

	// 3. DIFF + CLASSIFY. Digest stays at the prior plan until an actual apply.
	diff, hot, restart, forbidden, classErr := classifyHarnessPlanChange(oldPlan, newPlan, opts.AllowYolo)
	result := ApplyHarnessResult{
		Diff:            diff,
		RestartRequired: restart,
		Forbidden:       forbidden,
		Digest:          oldPlan.Digest(),
	}

	// 4. FAIL-CLOSED: any forbidden change or validation error => no mutation.
	if len(forbidden) > 0 || classErr != nil {
		if classErr == nil {
			classErr = fmt.Errorf("harness: apply rejected: forbidden change(s): %s", strings.Join(forbidden, ", "))
		}
		return result, classErr
	}

	// 5. RESTART-REQUIRED (but nothing forbidden): classified, NOT applied.
	if len(restart) > 0 {
		return result, ErrHarnessRestartRequired
	}

	if err := preflightHarnessExecutableConsent(
		newPlan,
		hc.requireExecutableConsent,
		opts.ExecutableConsentDigest,
	); err != nil {
		return result, err
	}

	// 6. HOT-ONLY. Preflight-construct EVERYTHING a hot apply needs BEFORE the
	//    live client is touched, so a preflight failure mutates nothing.
	approvalMode, err := validateHarnessApprovalMode(newPlan.Permissions().ApprovalMode, opts.AllowYolo)
	if err != nil {
		return result, err
	}
	apiKey := revealHarnessCredential(newPlan)
	if err := sealHarnessProvider(newPlan.ProviderID(), apiKey, newPlan.Credential().Source()); err != nil {
		return result, err
	}
	built, err := buildHarnessTools(newPlan.Tools(), newPlan.Workspace())
	if err != nil {
		return result, err
	}

	// Build the new closed options snapshot from the current options (preserving
	// logger/tracer/clientType/closed toggles) with the plan-derived fields
	// overwritten. Provider/credential/workspace/storage are restart-guarded, so
	// they are byte-identical to the current ones here.
	//
	// bindings and auditor are HOST-PROCESS state, not manifest state: they are
	// supplied once at construction (WithHarnessBindings / WithHarnessAuditor)
	// and a host does not re-supply them on every hot reload. They MUST be
	// carried forward here. Dropping them silently broke two guarantees:
	// harness_bindings.go:485-491 promises a construction-time binding set is
	// what lets a hot re-apply of a binding-requiring plan SUCCEED (dropping it
	// made the second apply fail the fail-closed preflight), and Phase 11b
	// auditing would go dark immediately after the first privileged change —
	// the one moment an audit trail matters most.
	newHC := &harnessConstruction{
		plan:                     newPlan,
		allowYolo:                opts.AllowYolo,
		bindings:                 c.harnessSuppliedBindings(),
		auditor:                  c.harnessSuppliedAuditor(),
		requireExecutableConsent: hc.requireExecutableConsent,
		executableConsentDigest:  opts.ExecutableConsentDigest,
	}
	co := c.opts
	co.providerName = newPlan.ProviderID()
	co.model = newPlan.Model()
	co.apiKey = apiKey
	co.baseURL = newPlan.BaseURL()
	co.systemPrompt = newPlan.RevealSystemPrompt()
	co.maxTokens = newPlan.Limits().MaxOutputTokens
	co.workspaceDir = newPlan.Workspace()
	co.storageDir = newPlan.Storage()
	co.approvalMode = approvalMode
	co.harness = newHC

	// 7. Build the complete replacement off-side. initHarnessAgent is allowed to
	//    mutate its receiver while constructing; using a private receiver means
	//    every failure leaves the live client's agent, managers, options and
	//    truthful snapshot untouched. The client write lock also waits for all
	//    Chat/Execute readers to finish before the eventual swap and old stop.
	candidate := &Client{
		convManager:      c.convManager,
		convStorage:      c.convStorage,
		logger:           c.logger,
		tracer:           c.tracer,
		workspace:        c.workspace,
		opts:             co,
		pendingApprovals: c.pendingApprovals,
		sessionID:        c.sessionID,
	}
	if err := candidate.initHarnessAgent(co, built, approvalMode); err != nil {
		if candidate.mcpManager != nil {
			candidate.mcpManager.Stop()
		}
		if candidate.agent != nil {
			_ = candidate.agent.Stop()
		}
		return result, fmt.Errorf("harness: hot apply failed; prior client preserved: %w", err)
	}

	// initHarnessAgent wires receiver-bound callbacks. Rebind them to the live
	// Client before publication so subscribers, persistence and interactive
	// approvals continue to use the original client identity.
	candidate.agent.SetIntermediateCallback(c.fanout)
	if approvalMode == "interactive" {
		type permissionCheckerSetter interface {
			SetPermissionChecker(tools.PermissionChecker)
		}
		if reg, ok := candidate.agent.ToolRegistry().(permissionCheckerSetter); ok {
			reg.SetPermissionChecker(c.NewInteractiveApprovalChecker())
		}
	}

	oldAgent := c.agent
	oldMCP := c.mcpManager
	c.agent = candidate.agent
	c.agentDef = candidate.agentDef
	c.provider = candidate.provider
	c.mcpManager = candidate.mcpManager
	c.harnessMCPExposure = candidate.harnessMCPExposure
	c.skillRegistry = candidate.skillRegistry
	c.opts = co
	c.wireMessagePersistence()
	retired := c.retireClientRuntime(oldAgent, oldMCP)
	result.Applied = hot
	result.Digest = newPlan.Digest()
	c.mu.Unlock()
	locked = false
	if retired != nil {
		stopClientRuntime(*retired)
	}
	return result, nil
}

// classifyHarnessPlanChange diffs oldP against newP and returns the redacted
// diff plus disjoint field-name sets for hot, restart-required, and forbidden
// changes. A non-nil error is returned when a changed field fails validation
// (unbound/unknown tool, invalid or unauthorized approvalMode); such fields are
// also recorded in the forbidden set with class "forbidden".
func classifyHarnessPlanChange(oldP, newP *harness.Plan, allowYolo bool) (HarnessPlanDiff, []string, []string, []string, error) {
	var diff HarnessPlanDiff
	var hot, restart, forbidden []string
	var valErr error

	add := func(field, oldv, newv, class string) {
		diff.Changes = append(diff.Changes, HarnessFieldChange{Field: field, Old: oldv, New: newv, Class: class})
	}

	// ── Provider identity (RESTART) ──────────────────────────────────────────
	providerChanged := oldP.ProviderID() != newP.ProviderID()
	if providerChanged {
		add(harnessFieldProvider, oldP.ProviderID(), newP.ProviderID(), harnessClassRestart)
		restart = append(restart, harnessFieldProvider)
	}

	// ── Model (HOT within same provider; part of the restart bundle otherwise) ─
	if oldP.Model() != newP.Model() {
		if providerChanged {
			add(harnessFieldModel, oldP.Model(), newP.Model(), harnessClassRestart)
			restart = append(restart, harnessFieldModel)
		} else {
			add(harnessFieldModel, oldP.Model(), newP.Model(), harnessClassHot)
			hot = append(hot, harnessFieldModel)
		}
	}

	// ── Base URL (RESTART: provider transport config) ────────────────────────
	if oldP.BaseURL() != newP.BaseURL() {
		add(harnessFieldBaseURL, oldP.BaseURL(), newP.BaseURL(), harnessClassRestart)
		restart = append(restart, harnessFieldBaseURL)
	}

	// ── Credential reference (RESTART; redacted token, never the value) ──────
	oldCred, newCred := harnessCredToken(oldP), harnessCredToken(newP)
	if oldCred != newCred {
		add(harnessFieldCredential, oldCred, newCred, harnessClassRestart)
		restart = append(restart, harnessFieldCredential)
	}

	// ── System prompt (HOT; compared/shown by hash only) ─────────────────────
	if oldP.SystemPromptHash() != newP.SystemPromptHash() {
		add(harnessFieldSystemPrompt, oldP.SystemPromptHash(), newP.SystemPromptHash(), harnessClassHot)
		hot = append(hot, harnessFieldSystemPrompt)
	}

	// ── Tools (HOT if all new tools bind within the catalog; else FORBIDDEN) ──
	if !harnessEqualStrings(oldP.Tools(), newP.Tools()) {
		oldTools := strings.Join(oldP.Tools(), ",")
		newTools := strings.Join(newP.Tools(), ",")
		if _, err := buildHarnessTools(newP.Tools(), newP.Workspace()); err != nil {
			add(harnessFieldTools, oldTools, newTools, harnessClassForbidden)
			forbidden = append(forbidden, harnessFieldTools)
			if valErr == nil {
				valErr = err
			}
		} else {
			add(harnessFieldTools, oldTools, newTools, harnessClassHot)
			hot = append(hot, harnessFieldTools)
		}
	}

	// ── Limits (HOT, per sub-field) ──────────────────────────────────────────
	// ── Skills (HOT; the initHarnessAgent rebuild re-registers the Skill tool
	// over a fresh registry, exactly like tools — no provider/process change) ──
	//
	// LIMITATION (Phase 5c): harness.SkillSpec carries no executable/script
	// indicator, so this classifier cannot yet distinguish an "instruction-only"
	// skill content change from one that would (in a future capability) ship
	// executable/script material requiring a stronger class than hot. ALL skill
	// changes are classified hot until SkillSpec grows that capability (tracked
	// under plan Phase 5 action #7); this is a deliberate, commented deferral —
	// never a silent one.
	if oldSkillsLabel, newSkillsLabel := harnessSkillsLabel(oldP), harnessSkillsLabel(newP); oldSkillsLabel != newSkillsLabel {
		add(harnessFieldSkills, oldSkillsLabel, newSkillsLabel, harnessClassHot)
		hot = append(hot, harnessFieldSkills)
	}

	// ── Hooks (HOT; the initHarnessAgent rebuild always constructs a fresh
	// agent.Agent and wires buildHarnessHooksManager(newP.Hooks(), ...) onto it
	// via SetHooksManager (Phase 6b) — no separate stale registry/manager
	// reference is retained on *Client itself (verified: buildHarnessHooksManager
	// is called and its result attached directly to the freshly-built agent
	// instance inside initHarnessAgent, never stored on c), so a hooks-only
	// change is exactly as safe to apply hot as tools/skills — no provider or
	// process-identity change) ──
	if oldHooksLabel, newHooksLabel := harnessHooksLabel(oldP), harnessHooksLabel(newP); oldHooksLabel != newHooksLabel {
		add(harnessFieldHooks, oldHooksLabel, newHooksLabel, harnessClassHot)
		hot = append(hot, harnessFieldHooks)
	}

	// ── MCP (HOT; the initHarnessAgent rebuild tears down the previous
	// *mcp.RuntimeManager and re-runs buildHarnessMCPManager (Phase 7b) plus
	// newHarnessMCPExposure (Phase 7c) against the NEW plan, over the same
	// freshly-built tool registry/agent the hot path already rebuilds
	// wholesale — no provider or process-identity change, so an mcp-only
	// change is exactly as safe to apply hot as tools/skills/hooks) ──
	//
	// Before Phase 7d this comparison did not exist at all: since Phase 7a
	// folded the `mcp:` section into the plan digest, an mcp-only change
	// already forced the hot rebuild (re-running the 7b/7c wiring) yet
	// produced NO HarnessFieldChange and never appeared in result.Applied —
	// the identical silent-apply bug Phase 5c fixed for skills and Phase 6c
	// fixed for hooks.
	//
	// Pins/workDir/url resolution are deliberately NOT re-validated here:
	// that is the compile boundary (harness.resolveMcp), exactly as for
	// skills and hooks. Classification compares already-resolved, already-
	// redacted plan state and nothing else.
	if oldMcpLabel, newMcpLabel := harnessMCPLabel(oldP), harnessMCPLabel(newP); oldMcpLabel != newMcpLabel {
		add(harnessFieldMcp, oldMcpLabel, newMcpLabel, harnessClassHot)
		hot = append(hot, harnessFieldMcp)
	}

	// ── Profiles (RESTART: each profiles[] entry carries its own provider
	// identity + credential — the SAME risk category as the document-level
	// provider/credential fields above, just addressed by a profile id
	// instead of the document's single provider/credential pair. Unlike
	// tools/skills/hooks/mcp, initHarnessAgent's rebuild does not get to
	// treat a provider/credential swap as "just more capability composition":
	// it is the identical class of change the top-level provider/credential
	// comparisons already classify RESTART, so profiles follows suit) ──
	if oldProfilesLabel, newProfilesLabel := harnessProfilesLabel(oldP), harnessProfilesLabel(newP); oldProfilesLabel != newProfilesLabel {
		add(harnessFieldProfiles, oldProfilesLabel, newProfilesLabel, harnessClassRestart)
		restart = append(restart, harnessFieldProfiles)
	}

	// ── Fallback (HOT: a policy/composition change over already-classified
	// profile identities — initHarnessAgent re-runs harnessFallbackDecision
	// and re-applies a.SetChain/a.SetNoFallback on every hot rebuild exactly
	// as it re-applies hooks/mcp, so a fallback-only change is exactly as
	// safe to apply hot as tools/skills/hooks/mcp. Deliberately NOT
	// pre-validated here, mirroring the skills/hooks/mcp precedent above: an
	// invalid fallback (unresolved profile id, empty enabled chain) surfaces
	// as an initHarnessAgent error at the actual HOT-ONLY apply step
	// (ApplyHarnessPlan step 7), which already rolls back to the prior plan
	// on failure — the same safety net skills/hooks/mcp already rely on) ──
	if oldFallbackLabel, newFallbackLabel := harnessFallbackLabel(oldP), harnessFallbackLabel(newP); oldFallbackLabel != newFallbackLabel {
		add(harnessFieldFallback, oldFallbackLabel, newFallbackLabel, harnessClassHot)
		hot = append(hot, harnessFieldFallback)
	}

	// ── Agents/subagents (HOT: roster/capability composition, mirrors
	// tools/skills/hooks/mcp — a subagent's OWN provider identity is reached
	// through a profiles[] reference, whose change is already caught by the
	// profiles comparison above, so this field never needs to carry a
	// restart class of its own. Deliberately NOT pre-validated here for the
	// same reason fallback above is not: an invalid agents[] entry (D1
	// rejection, unresolved profile reference, unbuildable per-agent
	// fallback chain) surfaces as an initHarnessAgent error at the HOT-ONLY
	// apply step, which rolls back to the prior plan on failure) ──
	if oldAgentsLabel, newAgentsLabel := harnessAgentsLabel(oldP), harnessAgentsLabel(newP); oldAgentsLabel != newAgentsLabel {
		add(harnessFieldAgents, oldAgentsLabel, newAgentsLabel, harnessClassHot)
		hot = append(hot, harnessFieldAgents)
	}

	ol, nl := oldP.Limits(), newP.Limits()
	if ol.MaxOutputTokens != nl.MaxOutputTokens {
		add(harnessFieldMaxOutputTokens, strconv.Itoa(ol.MaxOutputTokens), strconv.Itoa(nl.MaxOutputTokens), harnessClassHot)
		hot = append(hot, harnessFieldMaxOutputTokens)
	}
	if ol.MaxTurns != nl.MaxTurns {
		add(harnessFieldMaxTurns, strconv.Itoa(ol.MaxTurns), strconv.Itoa(nl.MaxTurns), harnessClassHot)
		hot = append(hot, harnessFieldMaxTurns)
	}
	if ol.TimeoutSeconds != nl.TimeoutSeconds {
		add(harnessFieldTimeoutSeconds, strconv.Itoa(ol.TimeoutSeconds), strconv.Itoa(nl.TimeoutSeconds), harnessClassHot)
		hot = append(hot, harnessFieldTimeoutSeconds)
	}

	// ── Approval mode (HOT within enum; yolo FORBIDDEN unless allowYolo) ─────
	oldAM := harnessNormApproval(oldP)
	newAMraw := newP.Permissions().ApprovalMode
	if oldAM != harnessNormApproval(newP) {
		m, err := validateHarnessApprovalMode(newAMraw, allowYolo)
		if err != nil {
			shown := strings.ToLower(strings.TrimSpace(newAMraw))
			if shown == "" {
				shown = "interactive"
			}
			add(harnessFieldApprovalMode, oldAM, shown, harnessClassForbidden)
			forbidden = append(forbidden, harnessFieldApprovalMode)
			if valErr == nil {
				valErr = err
			}
		} else {
			add(harnessFieldApprovalMode, oldAM, m, harnessClassHot)
			hot = append(hot, harnessFieldApprovalMode)
		}
	}

	// ── Other permission posture (HOT: reflected in snapshot on apply) ───────
	if harnessBoundaryLabel(oldP) != harnessBoundaryLabel(newP) {
		add(harnessFieldWorkspaceBound, harnessBoundaryLabel(oldP), harnessBoundaryLabel(newP), harnessClassHot)
		hot = append(hot, harnessFieldWorkspaceBound)
	}
	if oldP.Permissions().AllowMutation != newP.Permissions().AllowMutation {
		add(harnessFieldAllowMutation,
			strconv.FormatBool(oldP.Permissions().AllowMutation),
			strconv.FormatBool(newP.Permissions().AllowMutation), harnessClassHot)
		hot = append(hot, harnessFieldAllowMutation)
	}

	// ── Workspace / Storage (RESTART; hashed, never raw paths) ───────────────
	if oldP.Workspace() != newP.Workspace() {
		add(harnessFieldWorkspace, hashPathToken(oldP.Workspace()), hashPathToken(newP.Workspace()), harnessClassRestart)
		restart = append(restart, harnessFieldWorkspace)
	}
	if oldP.Storage() != newP.Storage() {
		add(harnessFieldStorage, hashPathToken(oldP.Storage()), hashPathToken(newP.Storage()), harnessClassRestart)
		restart = append(restart, harnessFieldStorage)
	}

	// ── Interfaces / presentation (RESTART) ──────────────────────────────────
	if harnessInterfacesLabel(oldP) != harnessInterfacesLabel(newP) {
		add(harnessFieldInterfaces, harnessInterfacesLabel(oldP), harnessInterfacesLabel(newP), harnessClassRestart)
		restart = append(restart, harnessFieldInterfaces)
	}

	return diff, hot, restart, forbidden, valErr
}

// revealHarnessCredential resolves the plan's declared credential to its value
// (empty when absent/deferred). It is used only for preflight sealing and the
// new provider config; the value is never placed in a diff or result.
func revealHarnessCredential(p *harness.Plan) string {
	if cred := p.Credential(); cred.Present() {
		if v, ok := cred.Reveal(); ok {
			return v
		}
	}
	return ""
}

// harnessCredToken returns a redacted, comparable token for a plan credential:
// its source label plus a short hash of the resolved value. It changes whenever
// the credential reference OR its resolved value changes, without ever exposing
// the value.
func harnessCredToken(p *harness.Plan) string {
	cred := p.Credential()
	src := cred.Source()
	if src == "" {
		src = "absent"
	}
	v := revealHarnessCredential(p)
	if v == "" {
		return src
	}
	sum := sha256.Sum256([]byte(v))
	return src + "#" + hex.EncodeToString(sum[:])[:12]
}

// harnessNormApproval returns the normalized approvalMode of a plan (empty ->
// "interactive"), matching validateHarnessApprovalMode's defaulting so a change
// from unset to "interactive" is not spuriously reported.
func harnessNormApproval(p *harness.Plan) string {
	m := strings.ToLower(strings.TrimSpace(p.Permissions().ApprovalMode))
	if m == "" {
		return "interactive"
	}
	return m
}

// harnessBoundaryLabel renders the tri-state workspaceBoundary as a stable label.
func harnessBoundaryLabel(p *harness.Plan) string {
	b := p.Permissions().WorkspaceBoundary
	if b == nil {
		return "unset"
	}
	return strconv.FormatBool(*b)
}

// harnessInterfacesLabel renders the presentation selection as a stable,
// value-only label for change detection (presentation carries no secrets).
func harnessInterfacesLabel(p *harness.Plan) string {
	in := p.Interfaces()
	theme, format := "", ""
	if in.TUI != nil {
		theme = in.TUI.Theme
	}
	if in.Print != nil {
		format = in.Print.Format
	}
	return fmt.Sprintf("default=%s;tui.theme=%s;print.format=%s", in.Default, theme, format)
}

// harnessSkillsLabel renders a plan's resolved skill selection as a stable,
// REDACTED, comparable label for change detection and diff display. Each
// SkillSpec becomes a compact token "<id>[:<8-char contentHash prefix>][*]" —
// the trailing "*" marks a path-backed (as opposed to id-only/manifest) entry
// — joined by ",", followed by a "roots=" suffix listing the resolved,
// manifest-relative skill search roots. Specs are rendered in their existing
// (declaration) order; nothing is reordered or deduplicated here.
//
// This NEVER includes a SKILL.md body (only a short hash prefix of the
// content hash) and NEVER includes an absolute host path: SkillSpec.Path is
// already a manifest-relative, normalized display label, safe to show as-is,
// exactly like the harness compiler's own provenance recording.
func harnessSkillsLabel(p *harness.Plan) string {
	specs := p.Skills()
	tokens := make([]string, 0, len(specs))
	for _, s := range specs {
		tok := s.ID
		if s.ContentHash != "" {
			h := strings.TrimPrefix(s.ContentHash, "sha256:")
			if len(h) > 8 {
				h = h[:8]
			}
			tok += ":" + h
		}
		if s.Path != "" {
			tok += "*"
		}
		tokens = append(tokens, tok)
	}
	return strings.Join(tokens, ",") + ";roots=" + strings.Join(p.SkillSearchRoots(), ",")
}

// harnessHooksLabel renders a plan's resolved hook declarations as a stable,
// REDACTED, comparable label for change detection and diff display. Each
// HookSpec becomes a compact token joining, in order: id, event, scope,
// matcher, type, a redacted content reference, priority, timeoutSeconds,
// enabled, and the environment allowlist (names only, never values) — all
// separated by ":" — joined across hooks by ",". Specs are rendered in
// their existing (declaration) order; nothing is reordered or deduplicated.
//
// The content reference is type-dependent and NEVER exposes a secret:
//   - type == command: an 8-char prefix of HookSpec.CommandHash. The raw
//     inline command text is never carried on HookSpec at all (only
//     RevealHookCommand, a trusted later-phase accessor, exposes it) and is
//     NEVER read here.
//   - type == script: an 8-char prefix of HookSpec.ContentHash. The script
//     file's content is hashed by harness at compile time but never
//     retained, and is NEVER read here.
//   - type == http: HookSpec.URL as-is — harness (Phase 6a) treats a hook
//     destination URL as non-secret, exactly like harnessInterfacesLabel
//     treats presentation fields, so it is safe to show verbatim.
func harnessHooksLabel(p *harness.Plan) string {
	specs := p.Hooks()
	tokens := make([]string, 0, len(specs))
	for _, h := range specs {
		var ref string
		switch h.Type {
		case harness.HookTypeCommand:
			ref = harnessShortHash(h.CommandHash)
		case harness.HookTypeScript:
			ref = harnessShortHash(h.ContentHash)
		case harness.HookTypeHTTP:
			ref = h.URL
		}
		tok := strings.Join([]string{
			h.ID,
			h.Event,
			h.Scope,
			h.Matcher,
			h.Type,
			ref,
			strconv.Itoa(h.Priority),
			strconv.Itoa(h.TimeoutSeconds),
			strconv.FormatBool(h.Enabled),
			strings.Join(h.Environment, "|"),
		}, ":")
		tokens = append(tokens, tok)
	}
	return strings.Join(tokens, ",")
}

// harnessShortHash trims an optional "sha256:" prefix and returns at most
// the first 8 hex characters — the same redaction shape harnessSkillsLabel
// uses for a skill's content hash. An empty input returns empty.
func harnessShortHash(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) > 8 {
		h = h[:8]
	}
	return h
}

// harnessMCPLabel renders a plan's resolved MCP server declarations as a
// stable, REDACTED, comparable label for change detection and diff display.
// Each McpServerSpec becomes a compact token joining, in order: id, type,
// enabled, a transport-dependent destination reference, workDir, timeout
// seconds, the environment allowlist (NAMES only), the header-name ->
// env-var-NAME reference map (sorted by header name), the tools allowlist,
// the excludeTools denylist, pinned, and unsafeDevMode — all separated by
// ":" — joined across servers by ",". Specs are rendered in their existing
// (declaration) order; nothing is reordered or deduplicated. This is the
// same shape/ordering discipline harnessHooksLabel uses.
//
// REDACTION (structural, not best-effort): harness.McpServerSpec carries NO
// secret VALUES at all — Environment is a copy of the allowlist of env var
// NAMES, and Headers maps a header name to an env var NAME, never a literal
// (see harness/mcp.go's field docs). Resolution of those names to actual
// values happens later and elsewhere (harnessMCPAllowlistedEnv /
// harnessMCPResolvedHeaders in client/harness_mcp.go, in-memory, never on
// the Plan). So a resolved secret is not merely omitted here, it is
// unreachable from a *harness.Plan — there is nothing to leak.
//
// The destination reference is transport-dependent:
//   - type == stdio: an 8-char prefix of a sha256 over the LENGTH-PREFIXED
//     command + args (harnessMCPArgvHash). Command/args are non-secret and
//     could be shown plainly, but hashing keeps the token delimiter-safe:
//     an arg containing ":" or "," could otherwise make two genuinely
//     different plans render one identical label — a silent FALSE NEGATIVE,
//     the exact class of bug this comparison exists to prevent.
//   - type == http|sse: URL as-is — harness (Phase 7a) treats an MCP
//     destination URL as non-secret ("it is not tainted; it is a
//     destination, not a credential"), exactly as harnessHooksLabel shows a
//     hook's HTTP URL verbatim.
func harnessMCPLabel(p *harness.Plan) string {
	specs := p.MCPServers()
	tokens := make([]string, 0, len(specs))
	for _, s := range specs {
		var ref string
		switch s.Type {
		case "stdio":
			ref = harnessMCPArgvHash(s.Command, s.Args)
		default:
			// http | sse (and any future transport): the destination URL.
			ref = s.URL
		}
		headers := make([]string, 0, len(s.Headers))
		for name, envRef := range s.Headers {
			headers = append(headers, name+"="+envRef)
		}
		// Map iteration order is randomised, so sort for determinism.
		sort.Strings(headers)
		tok := strings.Join([]string{
			s.ID,
			s.Type,
			strconv.FormatBool(s.Enabled),
			ref,
			s.WorkDir,
			strconv.Itoa(s.TimeoutSeconds),
			strings.Join(s.Environment, "|"),
			strings.Join(headers, "|"),
			strings.Join(s.Tools, "|"),
			strings.Join(s.ExcludeTools, "|"),
			strconv.FormatBool(s.Pinned),
			strconv.FormatBool(s.UnsafeDevMode),
		}, ":")
		tokens = append(tokens, tok)
	}
	return strings.Join(tokens, ",")
}

// harnessMCPArgvHash returns an 8-char redacted digest of a stdio server's
// command and args. Each element is LENGTH-PREFIXED before hashing so that
// argv boundaries are unambiguous: ["ab","c"] and ["a","bc"] hash
// differently, which a naive concatenation (or any single-separator join,
// since an arg may legally contain that separator) would not guarantee.
func harnessMCPArgvHash(command string, args []string) string {
	h := sha256.New()
	for _, part := range append([]string{command}, args...) {
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	return harnessShortHash(hex.EncodeToString(h.Sum(nil)))
}

// harnessEqualStrings reports whether two ordered string slices are identical.
func harnessEqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// harnessProfilesLabel renders a plan's resolved profiles[] declarations as a
// stable, REDACTED, comparable label — mirrors harnessMCPLabel/
// harnessHooksLabel's shape/ordering discipline. Each ProfileSpec becomes a
// compact token joining, in order: id, provider, model, baseURL (non-secret,
// shown verbatim — the SAME treatment harnessMCPLabel/harnessHooksLabel give
// a destination URL), a redacted credential token (source label + short hash
// of the resolved value, mirroring harnessCredToken; NEVER the raw
// credential), maxOutputTokens, maxTurns, timeoutSeconds — all separated by
// ":" — joined across profiles by ",". Specs are rendered in their existing
// (declaration) order; nothing is reordered or deduplicated.
func harnessProfilesLabel(p *harness.Plan) string {
	specs := p.Profiles()
	tokens := make([]string, 0, len(specs))
	for _, s := range specs {
		tokens = append(tokens, strings.Join([]string{
			s.ID,
			s.Provider,
			s.Model,
			s.BaseURL,
			harnessProfileCredToken(p, s),
			strconv.Itoa(s.Limits.MaxOutputTokens),
			strconv.Itoa(s.Limits.MaxTurns),
			strconv.Itoa(s.Limits.TimeoutSeconds),
		}, ":"))
	}
	return strings.Join(tokens, ",")
}

// harnessProfileCredToken mirrors harnessCredToken for one declared profile's
// credential: a redacted source label plus a short hash of the resolved
// value — never the value itself. "absent" when no credential is declared or
// resolved.
func harnessProfileCredToken(p *harness.Plan, s harness.ProfileSpec) string {
	src := s.CredentialRef
	if src == "" {
		src = "absent"
	}
	cred, ok := p.ProfileCredential(s.ID)
	if !ok || !cred.Present() {
		return src
	}
	v, ok := cred.Reveal()
	if !ok || v == "" {
		return src
	}
	sum := sha256.Sum256([]byte(v))
	return src + "#" + hex.EncodeToString(sum[:])[:12]
}

// harnessFallbackLabel renders a plan's resolved document-level fallback
// policy as a stable, comparable label. A FallbackSpec carries no secrets
// (Chain is a list of profile ids; RetryOn/OnExhaustion are enum values), so
// nothing is redacted here — mirrors harnessInterfacesLabel's non-secret
// treatment. Absent (ok==false) renders as the literal "absent" sentinel,
// which can never collide with a real label (every real label starts with
// "enabled=").
func harnessFallbackLabel(p *harness.Plan) string {
	spec, ok := p.Fallback()
	if !ok {
		return "absent"
	}
	return fmt.Sprintf("enabled=%s;chain=%s;retryOn=%s;cooldown=%d;maxAttempts=%d;onExhaustion=%s",
		strconv.FormatBool(spec.Enabled),
		strings.Join(spec.Chain, "|"),
		strings.Join(spec.RetryOn, "|"),
		spec.CooldownSeconds,
		spec.MaxAttempts,
		spec.OnExhaustion)
}

// harnessAgentsLabel renders a plan's resolved agents[] declarations as a
// stable, REDACTED, comparable label. Each SubagentSpec becomes a compact
// token; a system prompt is represented ONLY by its hash + byte count +
// source (never the text, mirroring harnessFieldSystemPrompt's hash-only
// treatment) and hook content is represented ONLY by a redacted hash/URL
// reference (mirroring harnessHooksLabel's per-hook content reference).
// Tool/delegate names are non-secret and shown verbatim, exactly as the
// document-level harnessFieldTools comparison already shows tool names
// verbatim. Specs are rendered in their existing (declaration) order;
// nothing is reordered or deduplicated.
func harnessAgentsLabel(p *harness.Plan) string {
	specs := p.Subagents()
	tokens := make([]string, 0, len(specs))
	for _, s := range specs {
		skillTokens := make([]string, 0, len(s.Skills))
		for _, sk := range s.Skills {
			tok := sk.ID
			if sk.ContentHash != "" {
				tok += ":" + harnessShortHash(sk.ContentHash)
			}
			skillTokens = append(skillTokens, tok)
		}

		hookTokens := make([]string, 0, len(s.Hooks))
		for _, h := range s.Hooks {
			var ref string
			switch h.Type {
			case harness.HookTypeCommand:
				ref = harnessShortHash(h.CommandHash)
			case harness.HookTypeScript:
				ref = harnessShortHash(h.ContentHash)
			case harness.HookTypeHTTP:
				ref = h.URL
			}
			hookTokens = append(hookTokens, h.ID+"="+ref)
		}

		fb := "none"
		if s.Fallback != nil {
			fb = fmt.Sprintf("enabled=%s;chain=%s", strconv.FormatBool(s.Fallback.Enabled), strings.Join(s.Fallback.Chain, "|"))
		}

		tokens = append(tokens, strings.Join([]string{
			s.ID,
			s.Profile,
			harnessShortHash(s.SystemPromptHash),
			strconv.Itoa(s.SystemPromptBytes),
			s.SystemPromptSource,
			strconv.FormatBool(s.ToolsSpecified),
			strings.Join(s.Tools, "|"),
			strings.Join(skillTokens, "|"),
			strings.Join(hookTokens, "|"),
			strconv.Itoa(s.Limits.MaxOutputTokens),
			strconv.Itoa(s.Limits.MaxTurns),
			strconv.Itoa(s.Limits.TimeoutSeconds),
			fb,
			strings.Join(s.Delegates, "|"),
		}, ":"))
	}
	return strings.Join(tokens, ",")
}
