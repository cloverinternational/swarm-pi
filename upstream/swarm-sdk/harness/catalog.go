package harness

// PolicyClass is the desired-harness default policy for a capability, taken
// verbatim from the Phase 0 PARITY_BASELINE authoritative policy table. It
// describes catalog/exposure intent, NOT whether a tool exists in the binary.
type PolicyClass string

const (
	// PolicyKeep: catalog-retained core capability. Inactive in the minimal
	// preset unless its stable ID is explicitly selected in agent.tools.
	PolicyKeep PolicyClass = "KEEP"
	// PolicyOptIn: available only when explicitly selected with its policy
	// (permission/egress/host binding) satisfied.
	PolicyOptIn PolicyClass = "OPT-IN"
	// PolicyDeferDiscover: exact capability/lifecycle unresolved; must be
	// discovered/closed before it can be a safe opt-in.
	PolicyDeferDiscover PolicyClass = "DEFER-DISCOVER"
	// PolicyNeverDefault: high-impact/compatibility-only; must never be a
	// default and requires an explicit named posture to select at all.
	PolicyNeverDefault PolicyClass = "NEVER-DEFAULT"
)

// Qualifier is a closed, PARITY_BASELINE-literal classification for one of the
// five "qualifier columns" (active-in-minimal-preset, host-bound,
// credential-bound, compatibility-only, presentation-only). PARITY_BASELINE
// cells are NOT uniformly yes/no — "often", "maybe", "partly", and "explicit
// required" all appear verbatim in the Policy classification table (Phase 0
// PARITY_BASELINE.md L404-L442). Flattening those to a bare bool would
// silently discard real baseline nuance (an "often" credential-bound
// capability is materially different from an unconditional "yes" one) —
// exactly the kind of truthfulness regression Phase 8c exists to prevent. A
// Qualifier is therefore a small closed string enum, copied verbatim
// (lower-cased, hyphenated) from the baseline cell text, never computed.
type Qualifier string

const (
	QNo               Qualifier = "no"
	QYes              Qualifier = "yes"
	QOften            Qualifier = "often"
	QMaybe            Qualifier = "maybe"
	QPartly           Qualifier = "partly"
	QExplicitRequired Qualifier = "explicit-required"
)

// Capability is stable, tool-construction-free metadata for one selectable
// capability. ID is a stable dotted identifier — never a Go type name. Runtime
// aliases record the concrete provider-visible names a later phase maps to, and
// are informational only (selection is always by exact ID).
type Capability struct {
	// ID is the stable selection identifier used in agent.tools.
	ID string `json:"id"`
	// RuntimeAliases are the runtime tool names this capability maps to. They
	// may collide across capabilities (for example three distinct "grep"
	// schemas); that is exactly why selection is by ID, never by alias.
	RuntimeAliases []string `json:"runtimeAliases,omitempty"`
	// Class is the default policy class from PARITY_BASELINE.
	Class PolicyClass `json:"class"`
	// SideEffect is a short non-ambiguous note on the capability's impact.
	SideEffect string `json:"sideEffect,omitempty"`

	// --- PARITY_BASELINE qualifier columns (Phase 8c; D4) ---------------
	// Each field is transcribed verbatim from the matching PARITY_BASELINE
	// "Policy classification" table column for this capability's row. Per
	// the table's own governing prose ("Unless a row narrows its roots, each
	// class applies independently to T, P, D, H, C, M, and S"), these
	// qualifiers are uniform across interfaces/roots for a given capability
	// — they refine the single Class, they never create a second compound
	// class or a per-root override table.

	// ActiveInMinimalPreset: whether this capability is active without an
	// explicit agent.tools selection. Baseline values are "no" for every
	// capability row (only the two core-config rows — provider/credential and
	// prompt/workspace/storage/limits, which are not agent-tool capabilities
	// at all — use "explicit required"; see the catalogExclusions doc below).
	ActiveInMinimalPreset Qualifier `json:"activeInMinimalPreset"`
	// HostBound: whether the capability requires a host-provided binding
	// (broker, adapter, listener, shell) to function.
	HostBound Qualifier `json:"hostBound"`
	// CredentialBound: whether the capability requires resolved credential
	// material (API key, vault secret, provider auth) to function.
	CredentialBound Qualifier `json:"credentialBound"`
	// CompatibilityOnly: whether the capability exists solely for
	// replay/migration compatibility with a hidden legacy path.
	CompatibilityOnly Qualifier `json:"compatibilityOnly"`
	// PresentationOnly: whether the capability's effect is host
	// presentation/UI only, with no other side effect.
	PresentationOnly Qualifier `json:"presentationOnly"`
}

// qual bundles the five PARITY_BASELINE qualifier columns so catalog rows
// that share one baseline row (nearly all of them: "Unless a row narrows its
// roots" applies to qualifiers too, not only Class) can be constructed
// without repeating five fields per entry and without transcription drift
// between sibling entries of the same baseline row.
type qual struct {
	active, host, credential, compat, presentation Qualifier
}

// mkCapability constructs one catalog entry from its stable ID, runtime
// aliases, policy class, shared qualifier bundle, and side-effect note. Pure
// data construction only — no I/O, no tool construction, no client/tui/
// internal imports.
func mkCapability(id string, aliases []string, class PolicyClass, q qual, sideEffect string) Capability {
	return Capability{
		ID:                    id,
		RuntimeAliases:        aliases,
		Class:                 class,
		SideEffect:            sideEffect,
		ActiveInMinimalPreset: q.active,
		HostBound:             q.host,
		CredentialBound:       q.credential,
		CompatibilityOnly:     q.compat,
		PresentationOnly:      q.presentation,
	}
}

// Shared qualifier bundles, one per distinct PARITY_BASELINE qualifier
// pattern actually used across the Policy classification table rows that map
// to agent-selectable capabilities. Named after the pattern, not the row, so
// reuse across sibling rows (e.g. shell/process tools and steering tools
// share host=yes/cred=no/pres=no) is visible at a glance.
var (
	qAllNo          = qual{QNo, QNo, QNo, QNo, QNo}      // no qualifier applies
	qCompatOnly     = qual{QNo, QNo, QNo, QYes, QNo}     // hidden legacy Edit/Write adapters
	qHostOnly       = qual{QNo, QYes, QNo, QNo, QNo}     // host-bound only (shell, steering)
	qHostOften      = qual{QNo, QOften, QNo, QNo, QNo}   // host-bound "often" (TaskManage)
	qCredOnly       = qual{QNo, QNo, QYes, QNo, QNo}     // credential-bound only (web search/fetch)
	qHostPresPartly = qual{QNo, QYes, QNo, QNo, QPartly} // host-bound, partly presentation-only
	qHostCred       = qual{QNo, QYes, QYes, QNo, QNo}    // host- and credential-bound
	qHostCredMaybe  = qual{QNo, QYes, QMaybe, QNo, QNo}  // host-bound, "maybe" credential-bound
	qHostCredOften  = qual{QNo, QYes, QOften, QNo, QNo}  // host-bound, "often" credential-bound
)

// catalog is the authoritative capability table. Entries, classes, and
// qualifiers are all derived from the PARITY_BASELINE "Policy classification"
// table (.swarm-p/harness-anywhere/phase0/PARITY_BASELINE.md L404-L442); no ID
// is invented from a Go type name. This is metadata only.
//
// # Phase 8c reconciliation (D1/D2/D4)
//
// Every row of the baseline table is accounted for by TestCatalogReconciles
// BaselineTable in catalog_test.go: it either maps to >=1 entry below, or is
// listed in catalogExcludedBaselineRows (below) with a documented reason.
// Existing entries carried over from Phase 1 keep their original ID, aliases,
// and Class UNCHANGED; only the five new qualifier fields were added to them.
// New entries added in this slice are marked "(Phase 8c)" in their comment.
var catalog = []Capability{
	// ---- KEEP: catalog-retained file/read/search core (exact selection) ----
	mkCapability("forge.read", []string{"Read"}, PolicyKeep, qAllNo, "read file/dir within path boundary"),
	mkCapability("forge.apply_patch", []string{"apply_patch"}, PolicyKeep, qAllNo, "file mutation; requires explicit mutation permission"),
	mkCapability("forge.undo", []string{"Undo"}, PolicyKeep, qAllNo, "file mutation (revert to snapshot); requires explicit mutation permission"),

	// ---- NEVER-DEFAULT: compatibility-only / high-impact host & UI control ----
	mkCapability("forge.edit", []string{"Edit"}, PolicyNeverDefault, qCompatOnly, "hidden legacy mutation adapter; replay/migration compatibility only"),
	mkCapability("forge.write", []string{"Write"}, PolicyNeverDefault, qCompatOnly, "hidden legacy mutation adapter; replay/migration compatibility only"),
	// browser: Phase 0 MINOR hygiene residual — NEVER-DEFAULT, unambiguous.
	// Baseline row "Browser/computer-use/deploy/database specialist tools"
	// (L435) also names deploy/database specialist tools; no concrete named
	// tool for those was found in the codebase as of this reconciliation (see
	// SUMMARY), so only the two discovered concrete tools are catalogued.
	mkCapability("builtin.agent_browser", []string{"agent_browser"}, PolicyNeverDefault, qHostCredMaybe, "browser control; NEVER-DEFAULT; process/network/UI side effects"),
	mkCapability("computeruse.computer", []string{"screenshot", "zoom", "mouse", "keyboard", "clipboard", "app", "access", "batch"}, PolicyNeverDefault, qHostCredMaybe, "OS/UI control; NEVER-DEFAULT; host-specific side effects"),
	mkCapability("vault.exec", []string{"vault_exec"}, PolicyNeverDefault, qHostCred, "secret use + host command execution"),
	mkCapability("vault.add", []string{"vault_add"}, PolicyNeverDefault, qHostCred, "secret mutation"),
	mkCapability("skills.skill_manage", []string{"SkillManage"}, PolicyNeverDefault, qHostCred, "autoskills/curator: prompt mutation, hooks, background model calls"),

	// ---- OPT-IN: explicit selection + policy required ----
	mkCapability("builtin.bash", []string{"bash"}, PolicyOptIn, qHostOnly, "shell/process execution; explicit command/path/approval policy required"),
	mkCapability("web.websearch", []string{"websearch"}, PolicyOptIn, qCredOnly, "network egress; explicit egress/credential policy"),
	mkCapability("web.web_fetch", []string{"web_fetch"}, PolicyOptIn, qCredOnly, "network egress; explicit egress policy"),
	mkCapability("web.x_search", []string{"x_search"}, PolicyOptIn, qCredOnly, "network egress; credential-bound"),
	mkCapability("web.xai_web_search", []string{"xai_web_search"}, PolicyOptIn, qCredOnly, "network egress; credential-bound"),
	mkCapability("web.anthropic_web_search", []string{"anthropic_web_search"}, PolicyOptIn, qCredOnly, "network egress; credential-bound"),
	mkCapability("ii.task_manage", []string{"TaskManage"}, PolicyOptIn, qHostOften, "task state; explicit persistence/scope required"),
	mkCapability("skills.skill", []string{"Skill"}, PolicyOptIn, qAllNo, "static skill; selected content hash enters provenance"),
	mkCapability("code.run_code", []string{"run_code"}, PolicyOptIn, qAllNo, "code mode collapses provider visibility; explicit opt-in only"),
	mkCapability("meta.tool_search", []string{"tool_search"}, PolicyOptIn, qAllNo, "discovery aid; may never promote a capability outside the active allowlist"),
	mkCapability("debug.debug_logs", []string{"debug_logs"}, PolicyOptIn, qHostPresPartly, "may expose sensitive internal state; redaction policy required"),
	mkCapability("vault.list", []string{"vault_list"}, PolicyOptIn, qHostCred, "vault inspection; explicit vault binding"),
	mkCapability("vault.two_person_status", []string{"vault_two_person_status"}, PolicyOptIn, qHostCred, "vault approval inspection"),
	mkCapability("vault.approve", []string{"vault_approve"}, PolicyOptIn, qHostCred, "vault approval inspection/contribution"),
	mkCapability("interactive.ask_user_question", []string{"ask_user_question"}, PolicyOptIn, qHostPresPartly, "host-bound question broker; missing broker must preflight-fail"),
	mkCapability("interactive.enter_plan_mode", []string{"enter_plan_mode"}, PolicyOptIn, qHostPresPartly, "host-bound plan broker"),
	mkCapability("interactive.exit_plan_mode", []string{"exit_plan_mode"}, PolicyOptIn, qHostPresPartly, "host-bound plan broker"),
	// (Phase 8c) Compaction — baseline row L424: "Separate context/output/total
	// budgets." CompactionTodoTool ("CompactionTodoUpdate") is the concrete
	// agent-visible tool injected into the summarization context
	// (internal/compaction/todo_tool.go:12,24).
	mkCapability("compaction.todo_update", []string{"CompactionTodoUpdate"}, PolicyOptIn, qAllNo, "compaction todo tool injected during context summarization; separate context/output/total budgets"),
	// (Phase 8c) MCP prompt API — baseline row L419: "Protocol API capability;
	// not provider-tool exposure." No concrete provider-visible tool name
	// exists (it is an MCP protocol API surface, not a tool call), so this
	// entry intentionally carries no RuntimeAliases.
	mkCapability("mcp.prompts", nil, PolicyOptIn, qHostCredOften, "MCP prompt protocol API; distinct from provider-tool exposure (no RuntimeAliases: protocol capability, not a callable tool)"),

	// ---- DEFER-DISCOVER: exact capability/lifecycle unresolved ----
	mkCapability("history.history_search", []string{"HistorySearch"}, PolicyDeferDiscover, qAllNo, "store scope/redaction contract unresolved"),
	mkCapability("history.history_get", []string{"HistoryGet"}, PolicyDeferDiscover, qAllNo, "store scope/redaction contract unresolved"),
	mkCapability("projectmemory.view", []string{"view_project_context"}, PolicyDeferDiscover, qAllNo, "public scope contract absent"),
	mkCapability("projectmemory.update", []string{"update_project_context"}, PolicyDeferDiscover, qAllNo, "public scope contract absent"),
	mkCapability("delegation.task", []string{"Task"}, PolicyDeferDiscover, qHostCred, "child capability intersection + lifecycle unresolved (parent-disabled widening)"),
	mkCapability("delegation.subagent_output", []string{"SubagentOutput"}, PolicyDeferDiscover, qHostCred, "child lifecycle unresolved"),
	mkCapability("delegation.delegate", []string{"Delegate"}, PolicyDeferDiscover, qHostCred, "question relay + lifecycle registry unresolved"),
	mkCapability("delegation.delegate_output", []string{"DelegateOutput"}, PolicyDeferDiscover, qHostCred, "question relay + lifecycle registry unresolved"),
	mkCapability("background.background_task", []string{"BackgroundTask"}, PolicyDeferDiscover, qHostCred, "persistence/leader/restart recovery unresolved"),
	mkCapability("background.task_output", []string{"TaskOutput"}, PolicyDeferDiscover, qHostCred, "background output lifecycle unresolved"),
	mkCapability("background.wait_for_agent", []string{"wait_for_agent"}, PolicyDeferDiscover, qHostCred, "background lifecycle unresolved"),
	mkCapability("background.multi_agent_wait", []string{"multi_agent_wait"}, PolicyDeferDiscover, qHostCred, "background lifecycle unresolved"),
	mkCapability("schedule.cron_create", []string{"CronCreate"}, PolicyDeferDiscover, qHostCred, "clock/persistence/prompt-sink unresolved"),
	mkCapability("schedule.cron_list", []string{"CronList"}, PolicyDeferDiscover, qHostCred, "clock/persistence unresolved"),
	mkCapability("schedule.cron_delete", []string{"CronDelete"}, PolicyDeferDiscover, qHostCred, "clock/persistence unresolved"),
	mkCapability("schedule.wakeup", []string{"ScheduleWakeup"}, PolicyDeferDiscover, qHostCred, "clock/persistence/prompt-sink unresolved"),
	mkCapability("steering.ask_user", []string{"ask_user"}, PolicyDeferDiscover, qHostOnly, "specialist steering; host/loop-control contract required"),
	mkCapability("steering.block_next_tool", []string{"block_next_tool"}, PolicyDeferDiscover, qHostOnly, "specialist steering; host/loop-control contract required"),
	// (Phase 8c) rest of the steering family (baseline L433 names all seven
	// tools explicitly): internal/tools/steeringtools/{observe,inject,refocus,
	// log_concern,halt_peer}.go.
	mkCapability("steering.observe_only", []string{"observe_only"}, PolicyDeferDiscover, qHostOnly, "specialist steering; host/loop-control contract required"),
	mkCapability("steering.inject_system_note", []string{"inject_system_note"}, PolicyDeferDiscover, qHostOnly, "specialist steering; host/loop-control contract required"),
	mkCapability("steering.refocus", []string{"refocus"}, PolicyDeferDiscover, qHostOnly, "specialist steering; host/loop-control contract required"),
	mkCapability("steering.log_concern", []string{"log_concern"}, PolicyDeferDiscover, qHostOnly, "specialist steering; host/loop-control contract required"),
	mkCapability("steering.halt_peer_loop", []string{"halt_peer_loop"}, PolicyDeferDiscover, qHostOnly, "specialist steering; host/loop-control contract required"),
	// (Phase 8c) A2A/swarm transport and tools — baseline row L430: "Tool
	// factory currently nil; host ownership unresolved." internal/a2a/
	// tools.go SwarmTools returns nil today (chatroom tools disabled); the
	// per-catalog design rule is that this is IRRELEVANT to whether an entry
	// belongs here (catalog records exposure intent, not binary presence).
	// Two entries group the two concrete tool families found in tools.go.
	mkCapability("a2a.protocol", []string{"a2a_list_agents", "a2a_fetch_agent_card", "a2a_send_message", "a2a_send_streaming_message", "a2a_get_task", "a2a_list_tasks", "a2a_cancel_task", "a2a_subscribe_task"}, PolicyDeferDiscover, qHostCredMaybe, "A2A protocol tool factory; host ownership unresolved"),
	mkCapability("a2a.swarm_chat", []string{"swarm_list_peers", "swarm_dm", "swarm_broadcast", "swarm_update_status"}, PolicyDeferDiscover, qHostCredMaybe, "swarm chatroom tools; SwarmTools() currently returns nil (disabled), host ownership unresolved"),
	// (Phase 8c) Plugins — baseline row L431: "Hook conversion incomplete; no
	// false active claim." internal/plugins composes commands/agents/skills/
	// hooks/MCP servers from a plugin manifest rather than exposing a single
	// named tool; one entry represents the plugin-sourced resource-composition
	// gate as a whole.
	mkCapability("plugins.load", nil, PolicyDeferDiscover, qHostCredMaybe, "plugin-sourced command/agent/skill/hook/MCP composition; hook conversion incomplete, no false active claim"),
}

// catalogIndex maps stable ID -> Capability for O(1) lookup. Built once.
var catalogIndex = func() map[string]Capability {
	m := make(map[string]Capability, len(catalog))
	for _, c := range catalog {
		m[c.ID] = c
	}
	return m
}()

// catalogExcludedBaselineRow pairs one verbatim PARITY_BASELINE "Capability"
// column value with the reason it is NOT an agent-selectable capability (and
// therefore has no catalog entry).
type catalogExcludedBaselineRow struct {
	// Row is the verbatim (trimmed) baseline "Capability" column text, used
	// to cross-check against the transcribed fixture in catalog_test.go so
	// the code comment and the test's exclusion list cannot silently drift
	// apart from each other.
	Row string
	// Reason is why this baseline row does not describe an agent-selectable
	// capability.
	Reason string
}

// catalogExcludedBaselineRows documents every PARITY_BASELINE "Policy
// classification" row that does NOT map to any catalog entry, with the
// reason it is not an agent-selectable capability. This is the D1 "documented
// exclusion" list; TestCatalogReconcilesBaselineTable in catalog_test.go
// cross-checks it against a transcribed copy of the same rows so exclusions
// cannot silently drift from this comment. Silent omission (neither a catalog
// entry nor a listed exclusion) is exactly the failure mode this slice exists
// to kill.
var catalogExcludedBaselineRows = []catalogExcludedBaselineRow{
	{"Explicit provider/model + typed credential reference", "core provider/credential config fields (Plan.providerID/model/credential; PARITY_BASELINE L406), not something selected via agent.tools; no capability ID applies."},
	{"Explicit prompt, workspace, storage, limits", "core runtime/prompt config fields (Plan.workspace/storage/promptText/limits; PARITY_BASELINE L407), not agent.tools-selectable; no capability ID applies."},
	{"Hooks", "configured via the declarative `hooks:` section (HookSpec / resolveHooks in hooks.go; PARITY_BASELINE L417), a separate selection axis from the agent.tools capability catalog; no single capability ID applies."},
	{"MCP tools/resource adapters", "per-server dynamic tool exposure gated by the declarative `mcp:` section (McpServerSpec / resolveMcp in mcp.go; PARITY_BASELINE L418) plus an exact post-discovery allowlist; the exposed tool set is server-specific and unbounded, so no single static capability ID can represent it (distinct from mcp.prompts, which is a fixed protocol API surface, not a per-server tool set)."},
	{"Profiles/fallback graphs", "configured via the declarative `profiles:`/`fallback:` sections (ProfileSpec/FallbackSpec in agents.go; PARITY_BASELINE L427), a provider/model selection graph, not an agent-tool capability; no capability ID applies."},
	{"Serve/listener interfaces", "the baseline's own rationale (\"Interface adapter concern, not core agent policy\"; PARITY_BASELINE L432) states this is a host/interface adapter concern (T/D/H/C/M/S listener startup), not an agent-selectable tool capability."},
	{"Credential resolution (reference only)", "core credential resolution behavior of Plan.credential (resolveSecret; PARITY_BASELINE L436), distinct from the vault.* operational tools which ARE catalogued; not itself a capability ID."},
	{"Yolo/automatic approval", "Permissions.ApprovalMode posture (normalizePermissions in plan.go defaults it to \"interactive\", never a yolo default; PARITY_BASELINE L440), not an agent-selectable tool capability."},
	{"Automatic global daemon startup", "host/daemon lifecycle behavior (PARITY_BASELINE L441), not an agent-selectable tool capability; no capability ID applies."},
	{"Ambient config/INDEX/MCP/skills/plugins/provider persistence", "ambient read/write behavior of legacy roots (PARITY_BASELINE L442); the harness design itself (no ambient INDEX/context/config merge, per the KEEP row rationale at L393-398) is the enforcement, not a catalog entry."},
}

// Catalog returns a copy of the stable capability metadata table.
func Catalog() []Capability {
	out := make([]Capability, len(catalog))
	copy(out, catalog)
	return out
}

// LookupCapability returns the capability metadata for a stable ID.
func LookupCapability(id string) (Capability, bool) {
	c, ok := catalogIndex[id]
	return c, ok
}

// KnownCapabilityID reports whether id is a stable capability ID in the catalog.
func KnownCapabilityID(id string) bool {
	_, ok := catalogIndex[id]
	return ok
}
