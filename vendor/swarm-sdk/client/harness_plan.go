// Harness Phase 2 client bridge — closed, deterministic construction path.
//
// newClientFromHarness builds a Client directly from an immutable *harness.Plan
// with a CLOSED posture: every ambient/implicit source is disabled and only the
// plan's declared provider/credential, prompt, workspace, storage, limits,
// permissions, and EXACT selected tools apply. It reuses no TUI-private
// preassembly and reads no ~/.swarmos legacy store.
//
// The path is failure-atomic: any invalid provider/tool/path/credential/approval
// posture returns an error before (or without leaving) partially-live client
// resources.
package client

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	provanthropic "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	provopenai "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/google/uuid"
)

// harnessNoToolsSentinel is a non-empty ToolHints value used when the plan
// selects ZERO tools. Empty ToolHints means "all registry tools" in the agent
// filter (agent_execute_filter.go), so a zero-tool plan must NOT leave the hints
// empty. This sentinel can never match a real registered tool, guaranteeing
// zero provider-visible tools that no later stage can widen — independent of
// whatever ends up in the registry.
const harnessNoToolsSentinel = "__harness_no_tools__"

// harnessApprovalModes is the closed enum of accepted approval postures (D2).
// "yolo" is accepted only with an explicit named posture (WithHarnessAllowYolo).
var harnessApprovalModes = map[string]struct{}{
	"interactive": {},
	"readonly":    {},
	"yolo":        {},
}

// harnessSealedProviders is the explicit, auditable allowlist of provider
// factories that support provider.Config.NoAmbientEnv and are PROVEN to consult
// NO ambient environment on the closed harness path. It
// is the core seal for the Phase 2 CLOSED-POSTURE guarantee: ambient provider
// credentials/config must be unreachable on the harness path.
//
//   - "anthropic": internal/provider/anthropic.NewFromRegistry honors
//     Config.NoAmbientEnv for credential and raw-dump construction, and the
//     resulting provider carries the posture into stream trace decisions.
//     is_oauth is never set, so OAuth refresh/store paths are unreachable.
//   - "openai": the openai-compatible ProviderFactory (openai/register.go)
//     reads only Config.APIKey / Config.BaseURL and embedded profiles; it seeds
//     no provider credential or config from ambient env. NoAmbientEnv records
//     the same closed contract at the common provider boundary.
//
// Deliberately EXCLUDED because their factory/registration reads ambient env
// that a key alone cannot seal:
//   - "gemini": gemini.Register ALWAYS reads GOOGLE_CLOUD_PROJECT[_ID],
//     GEMINI_API_KEY and NO_BROWSER at registration and bleeds ProjectID/
//     AuthMode into every constructed provider even when a key IS present
//     (gemini/register.go:18-45) — ambient config cannot be sealed by a key.
//   - OAuth-only / keyless providers (ClaudeCode/Google/xai OAuth, Cursor,
//     Ollama, ...): no explicit key can seal them in the minimal Phase 2 core.
//
// Extend this set in a later phase ONLY after auditing the target factory to
// prove it consults no ambient source beyond the explicit key.
var harnessSealedProviders = map[string]struct{}{
	"anthropic": {},
	"openai":    {},
}

// harnessSealedProviderList renders the allowlist as a sorted, comma-joined
// string for user-facing error messages.
func harnessSealedProviderList() string {
	out := make([]string, 0, len(harnessSealedProviders))
	for name := range harnessSealedProviders {
		out = append(out, name)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// sealHarnessProvider enforces the two closed-posture credential guarantees
// BEFORE any resource (storage/registry/agent) is created, so a rejection is
// failure-atomic and leaves zero partial resources:
//
//	(a) PROVIDER ALLOWLIST. Only providers whose factory is proven to read no
//	    ambient environment beyond the explicit key (harnessSealedProviders) may
//	    be built. Any other provider (gemini, ollama, cursor, OAuth-only, ...)
//	    is rejected with an explicit error rather than silently reading ambient
//	    credentials/project/auth-mode.
//	(b) FAIL-CLOSED CREDENTIAL. The resolved key MUST be non-empty. An absent/
//	    deferred credential, or a declared env var that is unset/empty, must NOT
//	    leave apiKey=="" and fall through to a factory that reads the ambient
//	    key. Fail construction instead.
//
// providerID is the ORIGINAL requested identity from the immutable plan. It is
// checked before lossy generic normalization so OAuth/keyless aliases cannot be
// silently reinterpreted as an allowlisted API-key provider. apiKey is resolved
// from the plan credential ONLY; credSource is redacted provenance, never value.
func sealHarnessProvider(providerID, apiKey, credSource string) error {
	requested := strings.ToLower(strings.TrimSpace(providerID))
	if _, sealed := harnessSealedProviders[requested]; !sealed {
		return fmt.Errorf(
			"harness: provider %q is not yet supported in closed harness mode "+
				"(its factory reads ambient environment); supported: %s",
			providerID, harnessSealedProviderList())
	}
	if apiKey == "" {
		src := credSource
		if src == "" {
			src = "absent"
		}
		return fmt.Errorf(
			"harness: provider %q resolved an empty credential (source: %s) in "+
				"closed harness mode; refusing to fall back to ambient environment "+
				"credentials",
			providerID, src)
	}
	return nil
}

// validateHarnessApprovalMode enum-validates the plan's approvalMode and gates
// yolo behind an explicit named posture. It never silently applies yolo.
func validateHarnessApprovalMode(mode string, allowYolo bool) (string, error) {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		m = "interactive"
	}
	if _, ok := harnessApprovalModes[m]; !ok {
		return "", fmt.Errorf("harness: unknown approvalMode %q; must be one of interactive, readonly, yolo", m)
	}
	if m == "yolo" && !allowYolo {
		return "", fmt.Errorf("harness: approvalMode %q requires an explicit named posture (client.WithHarnessAllowYolo); refusing to auto-approve", m)
	}
	return m, nil
}

// newClientFromHarness is the closed-construction entry point invoked by
// client.New when a harness plan is present. See the file doc for the posture.
func newClientFromHarness(o options) (*Client, error) {
	hc := o.harness
	plan := hc.plan
	if plan == nil { // defensive; the caller already checked.
		return nil, fmt.Errorf("harness: nil plan")
	}

	if err := preflightHarnessExecutableConsent(
		plan,
		hc.requireExecutableConsent,
		hc.executableConsentDigest,
	); err != nil {
		return nil, err
	}

	// 1. Approval posture — validate BEFORE creating any resource (D2, atomic).
	perms := plan.Permissions()
	approvalMode, err := validateHarnessApprovalMode(perms.ApprovalMode, hc.allowYolo)
	if err != nil {
		return nil, err
	}

	// 1b. RUNTIME-BINDING PREFLIGHT (Phase 9d, plan.md §3.3). The plan DECLARES
	//     host capabilities it cannot supply; the selected interface either
	//     supplies them or this run is refused. It runs here — before any
	//     resource exists — so a refusal is failure-atomic, and it runs again as
	//     the first statement of initHarnessAgent, which is the single
	//     chokepoint the live re-apply path also crosses. Compilation is
	//     untouched: a manifest with unsatisfied requirements still COMPILES,
	//     it just does not RUN. A plan requiring zero bindings makes this an
	//     exact no-op (HARD INVARIANT).
	if err := preflightHarnessBindings(plan, hc.bindings); err != nil {
		return nil, err
	}

	// 2. Provider identity + typed credential — from the plan ONLY. No ambient
	//    env, no config.json, no providers.json, no credentials.json.
	providerID := plan.ProviderID()
	if providerID == "" {
		return nil, fmt.Errorf("harness: provider id is empty")
	}
	model := plan.Model()
	apiKey := ""
	if cred := plan.Credential(); cred.Present() {
		if v, ok := cred.Reveal(); ok {
			apiKey = v
		}
	}
	baseURL := plan.BaseURL()

	// 2b. CLOSED-POSTURE PROVIDER SEALING (the MAJOR fix). Reject any provider
	//     whose factory reads ambient env, and fail-closed on an empty resolved
	//     credential — BEFORE creating any resource so a rejection is atomic and
	//     leaves zero partial state. This is the sole authority that keeps
	//     ambient credentials/config unreachable on the closed path; the
	//     registry below additionally registers only the sealed factories.
	if err := sealHarnessProvider(providerID, apiKey, plan.Credential().Source()); err != nil {
		return nil, err
	}

	// 3. EXACT tool set by catalog ID -> constructor (D3). No ambient tools.
	workspace := plan.Workspace()
	built, err := buildHarnessTools(plan.Tools(), workspace)
	if err != nil {
		return nil, err
	}

	// 4. Observability: closed default is noop; honor an explicit logger/tracer.
	logger := o.logger
	if logger == nil {
		logger = noop.NewLogger()
	}
	tracer := o.tracer
	if tracer == nil {
		tracer = noop.NewTracer()
	}

	// 5. Closed options snapshot stored on the client. Every ambient toggle is
	//    pinned off so a later Reconfigure/Config read reflects the closed
	//    posture, and Generate reads maxTokens from here.
	co := options{
		providerName: providerID,
		model:        model,
		apiKey:       apiKey,
		baseURL:      baseURL,
		systemPrompt: plan.RevealSystemPrompt(),
		maxTokens:    plan.Limits().MaxOutputTokens,
		workspaceDir: workspace,
		storageDir:   plan.Storage(),
		logger:       logger,
		tracer:       tracer,
		noAutoConfig: true,
		noIndexMd:    true,
		noFallback:   true,
		approvalMode: approvalMode,
		clientType:   o.clientType,
		harness:      hc,
	}

	c := &Client{
		logger:        logger,
		tracer:        tracer,
		workspace:     workspace,
		opts:          co,
		sessionID:     fmt.Sprintf("sess-%d", time.Now().UnixNano()),
		metadataLocks: make(map[string]*sync.Mutex),
		metadataRuns:  make(map[string]struct{}),
	}

	// 6. Storage from the plan (already absolute). No cwd re-derivation.
	if err := c.initStorage(plan.Storage()); err != nil {
		return nil, fmt.Errorf("harness: init storage: %w", err)
	}

	// 7. Provider + agent (client-owned). Agent construction is the LAST
	//    resource created, so any earlier failure leaks nothing and a failure
	//    here returns before c.agent is observable to a caller.
	if err := c.initHarnessAgent(co, built, approvalMode); err != nil {
		return nil, err
	}

	// 8. Seed the session-state surface (Snapshot/ActiveAgent/etc.).
	c.initSessionState("", "", "")
	return c, nil
}

// initHarnessAgent constructs the client-owned provider, tool registry, and
// agent under the closed posture. It sets EXACT provider ToolHints so no later
// stage can widen the active set, installs the approval checker separately from
// exposure, and applies the plan's limits.
func (c *Client) initHarnessAgent(o options, built []builtHarnessTool, approvalMode string) error {
	// PHASE 9d PREFLIGHT — THE CHOKEPOINT. initHarnessAgent has exactly two
	// call sites in the tree: newClientFromHarness (construction: interactive/
	// TUI, print/`swarm -p`, and any daemon that builds a client) and
	// ApplyHarnessPlan (live hot re-apply, plus its rollback). Placing the
	// check HERE, as the literal first statement — before the mcpManager
	// teardown, which is this function's first mutation — is what makes the
	// refusal universal AND non-mutating on both paths. There is no third
	// execution path and no bypass; see harnessPreflightChokepoints
	// (harness_bindings.go) and TestHarnessPreflightNoBypass.
	//
	// The REQUIREMENTS come from the plan being applied (o.harness.plan); the
	// SUPPLY comes from the client's construction-time registry
	// (c.harnessSuppliedBindings), because bindings belong to the host process,
	// not to a manifest — see harnessSuppliedBindings' doc.
	if o.harness != nil && o.harness.plan != nil {
		if err := preflightHarnessExecutableConsent(
			o.harness.plan,
			o.harness.requireExecutableConsent,
			o.harness.executableConsentDigest,
		); err != nil {
			return err
		}
		if err := preflightHarnessBindings(o.harness.plan, c.harnessSuppliedBindings()); err != nil {
			return err
		}
	}

	// Never leave a stale MCP runtime behind on a Reconfigure-style rebuild
	// (Phase 7d). This teardown runs BEFORE the agent is stopped, and in the
	// same order as Client.Close (manager first, then agent), because the
	// ordering is what makes it safe: RuntimeManager.Stop cancels every
	// server's context and closes its client, so no further status callback
	// can fire, and only THEN is the agent those callbacks target destroyed.
	//
	// Without this, a re-apply leaked the previous manager outright: its
	// background reconnect goroutines kept running and kept emitting, while
	// its harnessMCPExposure status handler still held — and would still
	// swap ToolHints on — the OLD, already-Stop()ed agent. Worse, the
	// `if hasMCP` guard below only ever OVERWRITES c.mcpManager, so a new
	// plan declaring zero MCP servers previously left the old manager and
	// old exposure fully live and completely unreferenced by the new agent.
	//
	// Always stopping and rebuilding fresh (rather than trying to diff which
	// servers the new plan still wants) deliberately matches how the agent
	// itself is rebuilt wholesale on this path: it is the simplest shape
	// that cannot leave a half-old, half-new runtime. RuntimeManager.Stop is
	// safe to call again later from Close (cancel/Close/unregister are all
	// idempotent), so double-Stop on the shutdown path is a no-op.
	if c.mcpManager != nil {
		c.mcpManager.Stop()
		c.mcpManager = nil
	}
	c.harnessMCPExposure = nil

	// Never leave a stale agent behind on a Reconfigure-style rebuild.
	if c.agent != nil {
		c.agent.Stop()
		c.agent = nil
	}

	norm := normalizeProviderName(o.providerName)

	// Provider registry: register only the SEALED canonical factories
	// (harnessSealedProviders) and their static aliases. Deliberately NO
	// providers.json / custom-provider lookup, so no legacy store is read on this
	// path. The gemini factory is intentionally NOT registered here: its
	// Register reads ambient GOOGLE_CLOUD_PROJECT[_ID]/GEMINI_API_KEY/NO_BROWSER
	// even at registration time, so on the closed path it is never constructed
	// (rejected upstream by sealHarnessProvider) and never registered.
	reg := provider.NewSimpleRegistry(c.logger)
	if err := provanthropic.Register(reg); err != nil {
		c.logger.Warn(context.Background(), "harness.provider_register_failed",
			observability.F("provider", "anthropic"), observability.F("error", err.Error()))
	}
	if err := provopenai.Register(reg); err != nil {
		c.logger.Warn(context.Background(), "harness.provider_register_failed",
			observability.F("provider", "openai"), observability.F("error", err.Error()))
	}
	for _, alias := range []string{
		"z.ai", "zai", "glm", "cerebras", "openrouter", "codex", "groq",
		"fireworks", "deepseek", "mistral", "together", "moonshot", "replicate",
		"chutes", "qwen", "kimi", "wafer.ai", "wafer", "xai", "grok", "x.ai",
		"supergrok",
	} {
		reg.RegisterAlias(alias, "openai")
	}
	reg.RegisterAlias("claudecode", "anthropic")
	for _, alias := range []string{"xai", "grok", "x.ai", "supergrok"} {
		reg.RegisterCompatible(alias, "openai")
	}
	reg.RegisterCompatible("claudecode", "anthropic")

	// Fresh, EXACT tool registry: register only the plan's selected tools.
	var toolReg tools.Registry = tools.NewSimpleRegistry(c.logger, c.tracer)
	for _, b := range built {
		if err := toolReg.Register(b.tool); err != nil {
			return fmt.Errorf("harness: register tool %q (%s): %w", b.runtimeName, b.catalogID, err)
		}
	}

	// CLOSED STATIC SKILLS (Phase 5b). When the plan selected skills, load them
	// from within the manifest subtree ONLY and register EXACTLY the `Skill`
	// invocation tool over a fresh, empty registry — autoskills/autogen stay OFF.
	// This runs BEFORE the agent is constructed, so any fail-closed error (empty
	// base, missing/unreadable file, hash mismatch, ambiguous id, path escape)
	// aborts construction and leaves NO observable agent. When zero skills are
	// selected, nothing is registered and hints stay identical to the baseline.
	skillTool, skillReg, hasSkills, err := buildHarnessSkillTool(
		o.harness.plan, c.logger, c.tracer, func() string { return c.sessionID })
	if err != nil {
		return err
	}
	if hasSkills {
		if err := toolReg.Register(skillTool); err != nil {
			return fmt.Errorf("harness: register Skill tool: %w", err)
		}
	}
	// Share the loaded registry so callers (and tests) can resolve the exact
	// selected skills by name. Assigned UNCONDITIONALLY (not only inside the
	// hasSkills branch) so a hot re-apply/reload to a zero-skills plan clears a
	// previously-set registry instead of leaving it stale: buildHarnessSkillTool
	// contractually returns a nil *skills.Registry whenever hasSkills is false.
	c.skillRegistry = skillReg

	// Build the provider (client-owned) exclusively through one of the sealed
	// registry factories. Unsupported direct-construction branches are rejected
	// before this function and deliberately do not exist here.
	provCfg := provider.Config{
		Name:         norm,
		APIKey:       o.apiKey,
		BaseURL:      o.baseURL,
		NoAmbientEnv: true,
		Model:        o.model,
		Logger:       c.logger,
		Tracer:       c.tracer,
		Custom:       map[string]any{},
	}
	if builtin, ok := provider.LookupBuiltinProvider(o.providerName); ok && provCfg.BaseURL == "" {
		provCfg.BaseURL = builtin.BaseURL
		provCfg.HTTPMaxRetries = builtin.HTTPMaxRetries
	}

	prov, err := reg.Create(provCfg)
	if err != nil {
		return fmt.Errorf("harness: create provider %q: %w", o.providerName, err)
	}

	// EXACT provider-visible hints. Non-empty selection -> exactly those runtime
	// names. Empty selection -> a sentinel that matches nothing, guaranteeing
	// zero provider-visible tools with no widening.
	hints := make([]string, 0, len(built)+1)
	for _, b := range built {
		hints = append(hints, b.runtimeName)
	}
	// Make the Skill tool provider-visible when skills were registered. This
	// appends BEFORE the zero-length sentinel check, so a skills-only plan is
	// never collapsed to the no-tools sentinel.
	if hasSkills {
		hints = append(hints, skillTool.Name())
	}
	if len(hints) == 0 {
		hints = []string{harnessNoToolsSentinel}
	}

	clientType := o.clientType
	if clientType == "" {
		clientType = ClientTypeSDK
	}

	def := &agent.Definition{
		ID:           uuid.New().String(),
		Name:         "Swarm",
		Provider:     norm,
		Model:        o.model,
		SystemPrompt: o.systemPrompt,
		ToolHints:    hints,
		ClientType:   clientType,
		Capabilities: &agent.Capabilities{
			SupportsTools: true,
			MaxTurns:      o.harness.plan.Limits().MaxTurns,
			Timeout:       time.Duration(o.harness.plan.Limits().TimeoutSeconds) * time.Second,
		},
	}

	agentCfg := agent.Config{
		Definition:       def,
		Provider:         prov,
		ProviderRegistry: reg,
		ToolRegistry:     toolReg,
		Logger:           c.logger,
		Tracer:           c.tracer,
		WorkspacePath:    c.workspace,
		StoragePath:      o.storageDir,
	}

	a, err := agent.New(agentCfg)
	if err != nil {
		return fmt.Errorf("harness: create agent: %w", err)
	}

	// CLOSED HOOK EXECUTION (Phase 6b). When the plan declares hooks, build
	// real executable internal/hooks.Hook instances from plan.Hooks() and
	// wire them into the agent so they fire. Closed posture: ONLY
	// harness-declared hooks are ever reachable here — buildHarnessHooksManager
	// never references internal/hooks/builtin (task-enforcement, sleep-blocker,
	// auto-mode, ...), those standard-client opt-in hooks. This runs AFTER `a`
	// exists (SetHooksManager needs the agent) but BEFORE c.agent = a, so any
	// fail-closed error (script escape/missing/unreadable/hash-mismatch,
	// unsupported type=http, unknown scope) aborts construction atomically and
	// leaves NO observable agent — mirroring the Skill-tool build-error abort
	// above. Zero hooks selected (or every declared hook disabled) => hasHooks
	// is false and SetHooksManager is never called: byte-identical to
	// pre-Phase-6b behavior.
	hm, hasHooks, err := buildHarnessHooksManager(o.harness.plan, c.logger, clientType)
	if err != nil {
		return err
	}
	if hasHooks {
		a.SetHooksManager(hm)
	}

	// CLOSED MCP EXECUTION (Phase 7b). When the plan declares MCP servers,
	// connect to ONLY those plan-declared servers (isolated config source —
	// see harness_mcp.go's file doc) and register their tools into the SAME
	// toolReg the agent already uses. This runs AFTER `toolReg` exists (MCP
	// tools register into it) and BEFORE c.agent = a, so a CONFIG/RESOLUTION
	// fail-closed error (workDir escape/missing) aborts construction
	// atomically and leaves NO observable agent — mirroring the hooks/skills
	// build-error abort above. A live connectivity failure (bad URL, missing
	// binary, network down) is NOT such an error: RuntimeManager.Start only
	// resolves configuration and launches best-effort background connection
	// goroutines, exactly like the standard client's initMCPManager. Zero
	// mcp servers selected (or every declared server disabled) => hasMCP is
	// false and nothing is stored on c: byte-identical to pre-Phase-7b.
	//
	// CLOSED MCP EXPOSURE (Phase 7c). The exposure recompute is constructed
	// HERE, before the manager, for two reasons: its baseline must be the EXACT
	// static `hints` slice computed above (never re-derived), and its status
	// handler must be installed BEFORE RuntimeManager.Start so no server's
	// registration-completion emit can be missed (see harness_mcp.go's PHASE 7c
	// banner). It is handed the live agent as the hint setter, so a recompute
	// swaps agent.Definition.ToolHints atomically under the agent's own lock
	// while a turn may be reading it. When hasMCP is false the handler is never
	// installed and no recompute can ever run, so the hint list stays exactly
	// what was passed to agent.New — byte-identical to pre-Phase-7c.
	mcpExposure := newHarnessMCPExposure(hints, o.harness.plan.MCPServers(), toolReg, a, c.logger)
	mcpMgr, hasMCP, err := buildHarnessMCPManager(
		context.Background(), o.harness.plan, toolReg, c.logger, c.tracer,
		func(rec mcp.ServerStatusRecord) {
			mcpExposure.onServerStatus(context.Background(), rec)
		},
	)
	if err != nil {
		return err
	}
	if hasMCP {
		c.mcpManager = mcpMgr
		c.harnessMCPExposure = mcpExposure
	}

	c.agent = a
	c.agentDef = def
	c.provider = prov

	// CLOSED PROFILES/FALLBACK/AGENTS WIRING (Phase 8b). When the plan
	// declares profiles/fallback/agents, bind them to the live runtime here —
	// AFTER `a` exists (SetChain/SetNoFallback need the agent) but as the
	// LAST wiring step, so a fail-closed error from any of the three
	// (unresolvable profile, unbuildable chain, an unbindable per-subagent
	// field, an unknown profile/agent reference) aborts construction
	// atomically and leaves NO observable agent — mirroring the
	// hooks/skills/MCP build-error aborts above. Zero declared sections =>
	// every builder below returns its explicit zero value and this block is a
	// pure no-op: byte-for-byte unchanged behavior on the closed path (see
	// client/harness_agents.go's file doc, HARD INVARIANT).
	//
	// Closed posture (D3): pin the primary chain to the single declared
	// provider UNLESS the plan declares an explicitly enabled fallback —
	// harnessFallbackDecision returns noFallback==true for both "no fallback:
	// section" and "declared but not enabled", so this is byte-for-byte the
	// same a.SetNoFallback(true) call this line used to make, unconditionally,
	// on every plan that does not opt in.
	profileSpecs, profileCfgs, err := buildHarnessProfiles(o.harness.plan)
	if err != nil {
		return err
	}
	chain, noFallback, err := harnessFallbackDecision(o.harness.plan, profileSpecs, profileCfgs)
	if err != nil {
		return err
	}
	if chain != nil {
		a.SetChain(chain)
		// B1 SECURITY FIX (Phase 10b): thread the SAME sealed per-profile
		// provider.Config values sealHarnessProvider already produced above
		// (via buildHarnessProfiles/harnessProfileProviderConfig — APIKey,
		// BaseURL, NoAmbientEnv) into every declared chain attempt, keyed by
		// the exact fallback.ModelRef the chain itself uses. Without this, a
		// declared `fallback:` chain's attempts silently reconstructed an
		// UNSEALED provider.Config in agent_execute_chain.go's execFn and
		// could fall back to reading ambient environment credentials — the
		// closed-posture seal covered only the PRIMARY provider until now.
		// No new credential reveal happens here: profileCfgs was already
		// resolved, fail-closed, above.
		if spec, ok := o.harness.plan.Fallback(); ok {
			chainCfgs := make(map[string]provider.Config, len(spec.Chain))
			for _, id := range spec.Chain {
				ps, specOK := profileSpecs[id]
				cfg, cfgOK := profileCfgs[id]
				if !specOK || !cfgOK {
					// Unreachable in practice: buildHarnessFallbackChain
					// (called by harnessFallbackDecision above) already
					// fails closed on exactly this condition, so `chain`
					// would be nil and this branch would never run. Skipping
					// rather than panicking keeps this loop fail-closed on
					// its own terms too.
					continue
				}
				ref := fallback.ModelRef{Provider: normalizeProviderName(ps.Provider), Model: ps.Model}
				chainCfgs[ref.Key()] = cfg
			}
			a.SetChainProviderConfigs(chainCfgs)
		}
	}
	a.SetNoFallback(noFallback)

	// D4: plan-declared subagents (if any) are resolved and validated here so
	// an unbindable field (D1), an unresolved profile reference, or an
	// unbuildable per-agent fallback chain fails construction atomically,
	// exactly like the sections above. The closed harness path does not yet
	// expose a live delegation.task tool (see harness_agents.go's file doc for
	// why), so the resulting binding is not retained on c beyond this
	// validation pass in this slice.
	if _, err := buildHarnessSubagentBinding(o.harness.plan, profileSpecs, profileCfgs, norm, o.model); err != nil {
		return err
	}

	// Persist generated transcripts to the conversation store.
	c.wireMessagePersistence()

	// Install the approval checker — EXPOSURE vs PERMISSION separation (C):
	// tool selection above is exposure; the checker below is permission.
	var checker tools.PermissionChecker
	switch approvalMode {
	case "interactive":
		checker = c.NewInteractiveApprovalChecker()
	default:
		checker = approvalCheckerFor(approvalMode) // readonly / yolo
	}
	if checker != nil {
		type permChecker interface {
			SetPermissionChecker(tools.PermissionChecker)
		}
		if pc, ok := toolReg.(permChecker); ok {
			pc.SetPermissionChecker(checker)
		}
	}

	// Fan out intermediate updates to Client subscribers.
	a.SetIntermediateCallback(c.fanout)

	// Resolve tools, etc. Non-fatal warning to match client.New semantics.
	if err := a.Initialize(); err != nil {
		c.logger.Warn(context.Background(), "harness.agent_initialize_warning",
			observability.F("error", err.Error()))
	}

	return nil
}
