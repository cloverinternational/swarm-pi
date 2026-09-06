// Package harness — Phase 10c compatibility presets (plan.md Phase 10 actions
// #4/#5). This file is DECLARE + RESOLVE ONLY: no client, provider, or tool
// is ever constructed here, and this file imports nothing outside the
// harness package itself.
//
// # D1 — a preset EXPANDS, it never BYPASSES
//
// A preset is a named, static list of stable catalog IDs (PresetSpec.IDs).
// resolveCompatibilityPreset's ONLY job is to decide which ToolSelection
// resolveTools (plan.go) should validate: either the manifest's own
// `agent.tools`, or — when `compatibility:` is set — a copy of the selected
// preset's IDs wrapped in the exact same tri-state ToolSelection shape a
// hand-written list would produce. From that point on a preset's IDs are
// indistinguishable from a hand-written list: they pass through
// resolveTools' duplicate/unknown-ID checks and checkCapabilityPolicy's
// class gate (DEFER-DISCOVER hard-rejected, NEVER-DEFAULT requires
// permissions.acknowledgeNeverDefault, credential-bound requires a declared
// credential reference) exactly like any other selection. This file adds NO
// parallel validation path and no override.
//
// # D2 — presets are inspectable, never opaque
//
// Plan.Compatibility() reports the applied preset's name (or "" if none) and
// Plan.CompatibilityShortfall() reports the documented, per-cause list of TUI
// behaviors that preset deliberately does not reproduce. Both are threaded
// into ExplainReport (explain.go) so `Explain`/`ExplainJSON` answer "what
// exactly did tui-v1 give me, and what did it deliberately leave out" from
// the report alone.
//
// # D3 — tui-v1 is generated from DOCUMENTED behavior; its shortfall is explicit
//
// Every ID in the tui-v1 preset below is commented with the PRIMARY SOURCE
// that proves the TUI registers it by default: either a phase10a audit report
// row, or (where the audit's own row conflated distinct schemas — see the
// forge.grep/forge.semantic_grep note below) the authoritative constructor
// call site itself. An ID is included in tui-v1's expansion ONLY when ALL of
// the following hold:
//
//  1. the TUI registers it by default (cited);
//  2. its catalog PolicyClass is NOT DEFER-DISCOVER (the class gate would
//     reject it unconditionally — see checkCapabilityPolicy in plan.go); and
//  3. it is in the Phase 10b BOUND set (client/harness_catalog_bind.go's
//     harnessToolBindings), not the HOST-BINDING-REQUIRED set — an ID this
//     client cannot construct today would make tui-v1 "name a capability
//     nothing can bind", exactly the decoration PARITY_HARNESS.md warns
//     against. (This file does NOT import client/ — that would violate
//     harness purity — so this is a documentation-time fact captured in the
//     comments below, current as of the Phase 10b commit named per entry; if
//     a future phase moves an ID between those two buckets, tui-v1's
//     selected-ID set should be revisited, but nothing here breaks: an ID
//     that becomes unbindable simply fails at the CLIENT layer with the
//     existing precise "host runtime binding" error, and one that becomes
//     bindable is simply a documented opportunity to grow tui-v1, tracked as
//     a TODO comment below rather than silently assumed.)
//
// Every TUI-documented capability that fails test 2 or test 3 is instead
// recorded in tuiV1Shortfall, grouped by the cause it failed (D3's required
// three groups): ShortfallDeferDiscover, ShortfallHostBindingRequired, or
// ShortfallNoManifestSection (a TUI subsystem with no catalog ID / manifest
// key at all, so there is nothing a preset could even select).
//
// # D4 — minimal stays closed, small, and is the default
//
// `minimal` selects exactly the five catalog IDs forge.DefaultTools binds on
// EVERY root (internal/tools/forge/registry.go:14-20) — the pure file
// read/search/mutation core with no shell, no task state, no network, no
// vault. It carries no Shortfall list: minimal never claims to reproduce TUI
// behavior, so there is nothing to document as deliberately dropped.
// Omitting `compatibility:` entirely (the pre-10c and still-default case)
// applies NEITHER preset — see resolveCompatibilityPreset.
//
// # D5 — preset + explicit agent.tools: rejected outright (chosen option (a))
//
// Declaring BOTH `compatibility:` and `agent.tools` is a compile-time error
// (harness.compatibility.conflictsWithTools), never a silent union or
// override. Rationale, per the project's existing discipline (Phase 8d's
// NEVER-DEFAULT acknowledgement, Phase 9c's D5 capability-widening reject):
// a "subset check" alternative would require this file to special-case
// COMPARE two independently-evolving ID lists and would silently change
// meaning every time either the preset or the manifest's list changes,
// without the author ever touching the file that broke. Reject-outright
// requires no such hidden coupling: an author who wants to start from tui-v1
// and diverge does so by NOT setting `compatibility:` and copy-pasting
// LookupPreset("tui-v1").IDs (or by reading the Explain report) — an explicit,
// visible action, not an implicit merge.
package harness

import "strings"

// PresetShortfallCause is the closed set of reasons ONE documented TUI
// capability could not be included in a preset's expansion (D3's three
// required groups; see the package doc above).
type PresetShortfallCause string

const (
	// ShortfallDeferDiscover: the capability's catalog PolicyClass is
	// DEFER-DISCOVER, a hard compile-time reject with no override
	// (checkCapabilityPolicy, plan.go). Reclassifying it is a reviewable
	// catalog change, never a preset's decision.
	ShortfallDeferDiscover PresetShortfallCause = "defer-discover"
	// ShortfallHostBindingRequired: the capability is class-legal (KEEP/
	// OPT-IN/NEVER-DEFAULT) but, as of the cited Phase 10b binder state, this
	// client package cannot CONSTRUCT it without a host-supplied broker,
	// service, credential, or ordering guarantee it does not have.
	// Class-legal-but-unbindable is exactly what PARITY_HARNESS.md calls
	// "decoration" — a preset must not include it.
	ShortfallHostBindingRequired PresetShortfallCause = "host-binding-required"
	// ShortfallNoManifestSection: the TUI subsystem has NO catalog capability
	// ID and no `harness.Document` key at all, so there is nothing a preset
	// (or any manifest) could select to reach it today.
	ShortfallNoManifestSection PresetShortfallCause = "no-manifest-section"
)

// PresetShortfallEntry documents ONE TUI behavior a compatibility preset
// deliberately does not reproduce, and why (D3). Capability is a stable
// catalog ID when one exists; for a subsystem with no catalog entry at all it
// is instead a stable, dotted, non-catalog label (never a Go type or file
// name) so the JSON shape stays uniform across causes.
type PresetShortfallEntry struct {
	Capability string               `json:"capability"`
	Cause      PresetShortfallCause `json:"cause"`
	Reason     string               `json:"reason"`
}

func mkShortfall(capability string, cause PresetShortfallCause, reason string) PresetShortfallEntry {
	return PresetShortfallEntry{Capability: capability, Cause: cause, Reason: reason}
}

// PresetSpec is one named, inspectable compatibility preset (D2). IDs is the
// full, ordered, explicit expansion; Shortfall is the documented list of
// TUI-documented capabilities this preset deliberately does not include.
type PresetSpec struct {
	Name      string
	IDs       []string
	Shortfall []PresetShortfallEntry
}

// copyPresetSpec returns a defensive copy of one preset spec so a caller can
// never mutate presetCatalog's backing arrays through a returned value.
func copyPresetSpec(s PresetSpec) PresetSpec {
	out := PresetSpec{Name: s.Name}
	if s.IDs != nil {
		out.IDs = make([]string, len(s.IDs))
		copy(out.IDs, s.IDs)
	}
	if s.Shortfall != nil {
		out.Shortfall = make([]PresetShortfallEntry, len(s.Shortfall))
		copy(out.Shortfall, s.Shortfall)
	}
	return out
}

// minimalPresetIDs is EXACTLY forge.DefaultTools' catalog-ID set (D4): the
// pure file read/search/mutation core the TUI, `-p`, and every other root
// register unconditionally (internal/tools/forge/registry.go:14-20;
// swarm-tui/internal/chat/sdk_integration.go's `forge.DefaultTools(forgeWorkspace)`
// call). It is a coincidence worth stating explicitly, not an accident: this
// is the one capability set every root already agrees on, which is exactly
// what makes it safe to be the small, closed, always-available default core.
var minimalPresetIDs = []string{
	"forge.read",        // registry.go:15 NewFSRead
	"forge.apply_patch", // registry.go:16 NewApplyPatchTool
	"forge.undo",        // registry.go:18 NewFSUndo
}

// tuiV1PresetIDs is the tui-v1 compatibility preset's full expansion (D3).
// Every entry's comment cites the PRIMARY source proving the TUI registers it
// by default, and every entry is in the Phase 10b BOUND set
// (client/harness_catalog_bind.go harnessToolBindings, commit 4f66969d) so
// selecting tui-v1 never "names a capability nothing can bind".
//
// NOTE: the grep family (forge.grep, forge.unified_grep, builtin.grep,
// forge.semantic_grep) and forge.semantic_rename were REMOVED from the
// product. Code search and file reading are done through the shell (rg,
// sed, cat), matching the Codex tool model. There is therefore no grep
// capability left to name in any preset.
var tuiV1PresetIDs = []string{
	// ---- forge default set: sdk_integration.go's unconditional
	// `for _, tool := range forge.DefaultTools(forgeWorkspace)` loop, which
	// runs whenever the ii workspace manager constructed (the ordinary case).
	"forge.read",
	"forge.apply_patch",
	"forge.undo",
	// builtin.bash: sdk_integration.go's unconditional bash registration —
	// background-wrapped `bgprocess.NewBackgroundBashTool` when a
	// BGProcessManager is present, else a plain `builtin.NewBashToolWithConfig`
	// fallback; EITHER WAY bash is always registered (report-w2.md /
	// report-w3.md "Background bash processes" rows). Only the plain-bash
	// catalog capability (builtin.bash) is class-legal/bound; the
	// background-wrapped variant and ReadBackgroundCommand have no catalog ID
	// at all — see tuiV1Shortfall's ShortfallNoManifestSection entries.
	"builtin.bash",
	// ii.task_manage: sdk_integration.go's unconditional
	// `ii.RegisterProductivityTools(toolRegistry)` call ("Keep TaskManage as
	// the only model-advertised task schema").
	"ii.task_manage",
	// NOTE: web.websearch is deliberately NOT here. The TUI does register it
	// unconditionally, so by documented-behavior it "belongs" in tui-v1 — but
	// client/harness_catalog_bind.go places it in harnessToolHostBindingReasons
	// (it needs an ambient credential at construction), not in the bound set.
	// Including it made `compatibility: tui-v1` COMPILE while a client could
	// never be CONSTRUCTED from it. A preset that cannot construct is exactly
	// the silent under-delivery this preset's shortfall list exists to prevent,
	// so it is recorded in tuiV1Shortfall as ShortfallHostBindingRequired
	// instead. Re-add it here only together with a real binding.
	// vault.* (5): builtin.ConfigureVault(opts.VaultProvider) at
	// sdk_integration.go is called UNCONDITIONALLY and its own doc comment
	// states the returned tools "are ALWAYS registered so agents know they
	// exist, even when locked" (vault_register.go's VaultTools/ConfigureVault).
	// vault.exec and vault.add are catalog class NEVER-DEFAULT: using tui-v1
	// with them active additionally requires
	// permissions.acknowledgeNeverDefault: [vault.exec, vault.add] (D1: the
	// preset does NOT silently include them — checkCapabilityPolicy still
	// demands the same explicit acknowledgement a manual selection would).
	// ALL FIVE vault.* catalog entries are CredentialBound=yes too (catalog.go
	// qHostCred), so — like web.websearch — a credential reference must also
	// be declared.
	"vault.exec",
	"vault.add",
	"vault.list",
	"vault.two_person_status",
	"vault.approve",
}

// tuiV1Shortfall is the documented, enumerable list of TUI behaviors tui-v1
// deliberately does NOT reproduce (D3), grouped by cause. It is not a claim
// of completeness over literally every line of the phase10a audit; it is the
// full set of GAP/DIVERGENT rows the five per-worker reports mark as an
// UNCONDITIONAL or default-path TUI registration (never a merely-possible /
// UNVERIFIED one — see each entry's citation), so nothing here overstates
// what the TUI actually does today.
var tuiV1Shortfall = []PresetShortfallEntry{
	// ---- ShortfallDeferDiscover: class-illegal, hard rejected with no
	// override at compile time (checkCapabilityPolicy, plan.go). ----
	mkShortfall("delegation.task", ShortfallDeferDiscover,
		"Task/SubagentOutput subagent delegation; TUI registers unconditionally off agentFactory (report-w3.md row 1, sdk_integration.go:1305-1356)"),
	mkShortfall("delegation.subagent_output", ShortfallDeferDiscover,
		"SubagentOutput; same TUI registration site as delegation.task (report-w3.md row 1)"),
	mkShortfall("delegation.delegate", ShortfallDeferDiscover,
		"Delegate forked Q&A agent; TUI registers unconditionally off the same factory (report-w3.md row 2, sdk_integration.go:1363-1405)"),
	mkShortfall("delegation.delegate_output", ShortfallDeferDiscover,
		"DelegateOutput; same TUI registration site as delegation.delegate (report-w3.md row 2)"),
	mkShortfall("background.background_task", ShortfallDeferDiscover,
		"BackgroundTask; TUI registers off sdkBgManager (report-w3.md row 4, sdk_integration.go:1595-1640)"),
	mkShortfall("background.task_output", ShortfallDeferDiscover,
		"TaskOutput; same sdkBgManager block as background.background_task (report-w3.md row 4)"),
	mkShortfall("background.wait_for_agent", ShortfallDeferDiscover,
		"wait_for_agent; same sdkBgManager block (report-w3.md row 4)"),
	mkShortfall("background.multi_agent_wait", ShortfallDeferDiscover,
		"multi_agent_wait; same sdkBgManager block (report-w3.md row 4)"),
	mkShortfall("schedule.cron_create", ShortfallDeferDiscover,
		"CronCreate; TUI registers off the cron scheduler block (report-w3.md row 6, sdk_integration.go:1540,1559-1593)"),
	mkShortfall("schedule.cron_list", ShortfallDeferDiscover,
		"CronList; same scheduler block (report-w3.md row 6)"),
	mkShortfall("schedule.cron_delete", ShortfallDeferDiscover,
		"CronDelete; same scheduler block (report-w3.md row 6)"),
	mkShortfall("schedule.wakeup", ShortfallDeferDiscover,
		"ScheduleWakeup; same scheduler block, plus a TUI-only prompt sink (report-w3.md row 6)"),
	mkShortfall("history.history_search", ShortfallDeferDiscover,
		"HistorySearch; TUI registers unconditionally (report-w5.md row 2, sdk_integration.go:2246-2252 and the now-removed out-of-plan harness branch injection fixed in Phase 10b)"),
	mkShortfall("history.history_get", ShortfallDeferDiscover,
		"HistoryGet; same TUI registration site as history.history_search (report-w5.md row 2)"),
	mkShortfall("a2a.protocol", ShortfallDeferDiscover,
		"8 a2a_* protocol tools, registered as a side effect of enabling A2A (report-w3.md row 8, internal/agent/agent.go:1061-1066)"),
	mkShortfall("plugins.load", ShortfallDeferDiscover,
		"Plugin-sourced command/agent/skill/hook/MCP composition; TUI builds PluginsManager unconditionally over the skills loader (report-w4.md row 3, sdk_integration.go:1940-1960)"),

	// ---- ShortfallHostBindingRequired: class-legal but this client cannot
	// construct it without a host binding this package does not have, per the
	// Phase 10b binder's harnessToolHostBindingReasons (commit 4f66969d,
	// client/harness_catalog_bind.go). ----
	mkShortfall("interactive.ask_user_question", ShortfallHostBindingRequired,
		"TUI registers when a QuestionBroker is available (report-w2.md row: QuestionBroker, sdk_integration.go:1029-1042); needs an interaction.UserInteractionBroker the harness client does not have"),
	mkShortfall("interactive.enter_plan_mode", ShortfallHostBindingRequired,
		"TUI registers when a PlanBroker is available (report-w2.md row: PlanBroker, sdk_integration.go:1045-1071); needs a plan.PlanBroker the harness client does not have"),
	mkShortfall("interactive.exit_plan_mode", ShortfallHostBindingRequired,
		"TUI registers alongside enter_plan_mode off the same broker; needs the same missing plan.PlanBroker"),
	mkShortfall("skills.skill", ShortfallHostBindingRequired,
		"Static skill invocation tool; TUI registers when skills initialize (report-w4.md row 1, PARITY_BASELINE.md row 276); on the harness client this ID must be reached via the plan's own `skills:` section, not `agent.tools` — the underlying capability is NOT unreachable, only unreachable via THIS preset's selection mechanism"),
	mkShortfall("skills.skill_manage", ShortfallHostBindingRequired,
		"Autoskills/curator; TUI builds it as one all-or-nothing block off hooksMgr (report-w4.md row 2, sdk_integration.go:1834-1932); needs a non-nil autogenskills.Service the harness client does not construct; also catalog class NEVER-DEFAULT"),
	mkShortfall("web.x_search", ShortfallHostBindingRequired,
		"TUI registers when SuperGrok OAuth or XAI_API_KEY is available (sdk_integration.go:1172-1181); needs an ambient/tool-level credential the harness closed path refuses to read implicitly"),
	mkShortfall("web.websearch", ShortfallHostBindingRequired,
		"TUI registers it unconditionally (\"Register web search tool with proper backend selection\", websearch.NewFromCoreConfig auto-selecting an exa/anthropic backend), so by documented behavior it belongs in tui-v1 — but client/harness_catalog_bind.go (Phase 10b) places it in harnessToolHostBindingReasons, not the bound set, because the backend needs an ambient credential at CONSTRUCTION time. Including it in the preset made `compatibility: tui-v1` compile while no client could be constructed from it (pinned by Phase 10d's TestTUIV1PresetPlanCompilesButClientConstructionFailsOnWebSearchBinding). Move it back into tuiV1PresetIDs only together with a real binding"),
	mkShortfall("web.xai_web_search", ShortfallHostBindingRequired,
		"TUI registers alongside x_search off the same credential check (sdk_integration.go:1182-1190); same missing tool-credential wiring"),
	mkShortfall("debug.debug_logs", ShortfallHostBindingRequired,
		"TUI registers when DebugMode is enabled (sdk_integration.go:1138-1146); needs a DebugInspectProvider exposing live TUI-internal state the harness client has no application/session introspection surface to supply"),
	mkShortfall("computeruse.computer", ShortfallHostBindingRequired,
		"TUI registers on Linux with computer-use enabled + an X11/Wayland backend (sdk_integration.go:1221-1255, report-w1.md/PARITY_BASELINE.md row 281); needs an os-input-executor the harness client must not open implicitly on an unknown host; also catalog class NEVER-DEFAULT"),
	mkShortfall("code.run_code", ShortfallHostBindingRequired,
		"TUI installs code mode hidden by default, visible only with the code-mode setting (PARITY_BASELINE.md row 280); needs a FINAL selected-tool-registry snapshot + sandbox + dispatch adapter the harness client does not construct"),
	mkShortfall("meta.tool_search", ShortfallHostBindingRequired,
		"TUI's persisted opt-in advanced tool mode (PARITY_BASELINE.md row 280, app_init.go:1452-1474); needs a registry-refresh-ordering guarantee this single-pass client construction cannot honor"),
	mkShortfall("mcp.prompts", ShortfallHostBindingRequired,
		"MCP prompt protocol API (GetPrompt); reachable on BOTH TUI and harness via the MCP manager API (report-w3.md row: MCP prompt API, DIVERGENT), but has no RuntimeAliases — it is not a constructible provider-visible tool, so no agent.tools selection can gate it"),

	// ---- ShortfallNoManifestSection: real, documented TUI behavior with NO
	// catalog capability ID and no harness.Document key at all — nothing a
	// preset (or any manifest) could select to reach it today. ----
	mkShortfall("provider.oauth", ShortfallNoManifestSection,
		"OAuth providers (ClaudeCode/Gemini/xAI SuperGrok); harness rejects every OAuth/keyless provider outright (report-w1.md row 2, client/harness_plan.go:60-70)"),
	mkShortfall("provider.reliability_stack", ShortfallNoManifestSection,
		"Retry/rate-limit/chain provider wrapper; harness uses the raw provider with no wrapper (report-w1.md row 4, sdk_integration.go:2008-2030 vs client/harness_plan.go:374)"),
	mkShortfall("context.assembly", ShortfallNoManifestSection,
		"CLAUDE.md/AGENTS.md/INDEX.md ambient discovery -> XML injected into the prompt; harness.Document has no `context:` key (report-w1.md row 16, sdk_integration_config.go:614-668)"),
	mkShortfall("context.orchestrator", ShortfallNoManifestSection,
		"Real-time per-turn context orchestrator + context-injecting provider wrapper (report-w1.md row 17, sdk_integration.go:1976-2022)"),
	mkShortfall("profiles.role_model_selection", ShortfallNoManifestSection,
		"Role-based model selection for subagents (role -> model aliases); harness agents[] has only `profile:`, no role alias field (report-w1.md row 11, sdk_integration.go:774-789,1323-1324)"),
	mkShortfall("permissions.policy", ShortfallNoManifestSection,
		"Permission policy level/timeoutSeconds/timeoutBehavior/overrides/rules; harness Permissions is 4 scalar fields (report-w2.md summary + row, internal/tools/permission_config.go:61-85 vs harness/types.go:113-145)"),
	mkShortfall("background.bash_lifecycle", ShortfallNoManifestSection,
		"Background-wrapped bash (auto-background threshold) + ReadBackgroundCommand; ReadBackgroundCommand has NO catalog entry at all (report-w3.md row 5, sdk_integration.go:982-1008)"),
	mkShortfall("a2a.transport", ShortfallNoManifestSection,
		"A2A runtime/transport wiring (handle, swarm name, listen address, workspace scope); agent.Config literal on the harness path has no A2A/Hub field (report-w3.md row 7, client/harness_plan.go:416-425)"),
	mkShortfall("hooks.builtin", ShortfallNoManifestSection,
		"~25 builtin hooks (task-enforcement, protected-branch, plan-mode, goal, steering, gold/silver/bronze, ...); harness deliberately never imports internal/hooks/builtin (report-w3.md row 15, swarm-tui/internal/chat/hooks/manager.go:48-81,137 vs client/harness_hooks.go:9)"),
	mkShortfall("skills.autoskills_config", ShortfallNoManifestSection,
		"Autoskills trigger budgets (toolCallBudget/workingBudget/maxNudgeIgnores/nudgeInterval), prompt-mutation declaration, and curator model reference; no `autoskills:` section exists (report-w4.md GAP 1, sdk_integration.go:1836-1932)"),
	mkShortfall("plugins.config", ShortfallNoManifestSection,
		"Which of the five plugin-contributed resource kinds (commands/agents/skills/hooks/MCP) is allowed; no `plugins:` section exists beyond the DEFER-DISCOVER plugins.load gate itself (report-w4.md GAP 2, sdk_integration.go:1941-1960)"),
	mkShortfall("compaction.auto_config", ShortfallNoManifestSection,
		"Auto-compaction threshold mode/value + continueIfRunning; no `compaction:` section exists and the harness main agent runs entirely unconfigured (report-w4.md GAP 3, sdk_integration.go:680-681,1716-1722)"),
	mkShortfall("observability.config", ShortfallNoManifestSection,
		"Structured logger/tracer/trace-sink configuration; harness.Document has no `observability:` key and the harness client falls back to noop (report-w5.md summary, client/harness_plan.go:209-216)"),
	mkShortfall("history.findings_capture_hooks", ShortfallNoManifestSection,
		"In-process findings capture + analysis hook kind; harness hooks are limited to command|script|http (report-w5.md GAP 3, hooks/manager.go:1150-1171,1240,1472-1474 vs harness/hooks.go:25,75,192)"),
	mkShortfall("daemon.harness_support", ShortfallNoManifestSection,
		"`swarm daemon` cannot run a harness plan at all — zero Harness references in its construction path (PARITY_HARNESS.md 'OTHER STRUCTURAL GAPS', report-w5.md GAP 4, swarm-tui/cmd/swarmos/daemon_cli.go:320-323)"),
}

// presetCatalog is the closed, named registry of compatibility presets.
// `minimal` and `tui-v1` are the ONLY two valid `compatibility:` values
// (D4/D3); any other value is rejected at compile time by
// resolveCompatibilityPreset.
var presetCatalog = map[string]PresetSpec{
	"minimal": {Name: "minimal", IDs: minimalPresetIDs, Shortfall: nil},
	"tui-v1":  {Name: "tui-v1", IDs: tuiV1PresetIDs, Shortfall: tuiV1Shortfall},
}

// KnownPresetNames returns the sorted set of valid `compatibility:` values.
func KnownPresetNames() []string {
	out := make([]string, 0, len(presetCatalog))
	for name := range presetCatalog {
		out = append(out, name)
	}
	// Only two entries today; sort for deterministic diagnostic text/tests
	// regardless of Go's randomized map iteration order.
	if len(out) > 1 && out[0] > out[1] {
		out[0], out[1] = out[1], out[0]
	}
	return out
}

// LookupPreset returns a defensive copy of one named preset's spec.
func LookupPreset(name string) (PresetSpec, bool) {
	s, ok := presetCatalog[name]
	if !ok {
		return PresetSpec{}, false
	}
	return copyPresetSpec(s), true
}

// resolveCompatibilityPreset implements D3/D4/D5. It decides the EFFECTIVE
// ToolSelection plan.go's resolveTools should validate:
//
//   - compatibility == "" (D4): the explicit selection passes through
//     UNCHANGED — this is the exact pre-10c code path, so a manifest that
//     omits `compatibility:` compiles to the exact pre-10c plan, digest
//     included.
//   - compatibility names an unknown preset: fail-closed with a diagnostic
//     naming the known set (KnownPresetNames()); the explicit selection is
//     returned unchanged so resolveTools can still surface ITS OWN
//     diagnostics deterministically (e.g. "agent.tools is required") rather
//     than silently swallowing that check.
//   - compatibility names a known preset AND explicit.Specified is true
//     (agent.tools present in the document, D5 option (a)): fail-closed —
//     preset + explicit `agent.tools` never silently merge.
//   - compatibility names a known preset AND explicit.Specified is false
//     (agent.tools omitted): the preset's ID list becomes the effective
//     selection, Specified=true, so it flows through resolveTools exactly
//     like a hand-written list (D1).
//
// It NEVER validates catalog membership or PolicyClass itself — resolveTools
// and checkCapabilityPolicy already do that for every selection, preset or
// not (D1), so duplicating that logic here would be a second, divergent gate.
func resolveCompatibilityPreset(sourcePath, compatibility string, explicit ToolSelection) (ToolSelection, PresetSpec, Diagnostics) {
	if compatibility == "" {
		return explicit, PresetSpec{}, nil
	}
	spec, known := presetCatalog[compatibility]
	if !known {
		d := newDiag("harness.compatibility.unknown", "compatibility",
			"unknown compatibility preset "+quote(compatibility)+"; known presets: "+strings.Join(KnownPresetNames(), ", "), sourcePath)
		return explicit, PresetSpec{}, Diagnostics{d}
	}
	if explicit.Specified {
		d := newDiag("harness.compatibility.conflictsWithTools", "agent.tools",
			"agent.tools must be omitted when compatibility "+quote(compatibility)+" is selected; "+
				"a preset and an explicit agent.tools selection never silently merge or override — "+
				"remove agent.tools (start from LookupPreset("+quote(compatibility)+").IDs and diverge explicitly "+
				"by removing compatibility instead) or remove compatibility", sourcePath)
		return explicit, PresetSpec{}, Diagnostics{d}
	}
	ids := make([]string, len(spec.IDs))
	copy(ids, spec.IDs)
	return ToolSelection{Specified: true, IDs: ids}, copyPresetSpec(spec), nil
}
