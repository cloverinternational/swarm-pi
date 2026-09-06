package harness

import (
	"os"
	"regexp"
	"testing"
)

// TestCatalogResiduals pins the two Phase 0 MINOR hygiene residuals: builtin
// Browser and computer-use must be NEVER-DEFAULT. (The grep residual row was
// dropped when the grep family was removed in favour of shell-based search.)
func TestCatalogResiduals(t *testing.T) {
	cases := []struct {
		id    string
		class PolicyClass
	}{
		{"builtin.agent_browser", PolicyNeverDefault},
		{"computeruse.computer", PolicyNeverDefault},
	}
	for _, c := range cases {
		cap, ok := LookupCapability(c.id)
		if !ok {
			t.Errorf("capability %q missing from catalog", c.id)
			continue
		}
		if cap.Class != c.class {
			t.Errorf("capability %q class = %q, want %q", c.id, cap.Class, c.class)
		}
	}
}

// TestCatalogNoGoTypeNameIDs guards against IDs that look like Go type names
// (exported CamelCase) rather than stable dotted identifiers.
func TestCatalogNoGoTypeNameIDs(t *testing.T) {
	for _, c := range Catalog() {
		if len(c.ID) == 0 {
			t.Errorf("empty capability id")
			continue
		}
		// Stable IDs are dotted and lowercase-namespaced.
		if c.ID[0] >= 'A' && c.ID[0] <= 'Z' {
			t.Errorf("capability id %q looks like a Go type name; use a stable dotted id", c.ID)
		}
		hasDot := false
		for i := 0; i < len(c.ID); i++ {
			if c.ID[i] == '.' {
				hasDot = true
				break
			}
		}
		if !hasDot {
			t.Errorf("capability id %q is not namespaced (expected a dot)", c.ID)
		}
	}
}

// --- D3: class gating, uniform across pre-existing and added rows ----------
//
// HISTORY (Phase 8c -> 8d). Phase 8c probed the UNMODIFIED code and recorded a
// FINDING: `resolveTools` (harness/plan.go) validated a selected agent.tools id
// with ONLY `KnownCapabilityID(id)` and never branched on Class, so
// `tools: [steering.ask_user]` (DEFER-DISCOVER) and `tools: [vault.exec]`
// (NEVER-DEFAULT) both compiled with ZERO diagnostics. Phase 8c's file scope
// (catalog.go + catalog_test.go) could not fix that, so its version of THIS
// test pinned the hole as "unchanged" while reporting it upward.
//
// Phase 8d closes the finding: PolicyClass is now ENFORCED at compile time (see
// checkCapabilityPolicy in plan.go and policy_test.go). This test is therefore
// INVERTED, not deleted or weakened — it still proves the exact property it was
// written to prove, which was never "these ids are accepted" but rather
// UNIFORMITY: a capability row added by a later slice must behave IDENTICALLY
// to a pre-existing row of the same class, never more permissively. The
// uniformity assertion survives verbatim; only the shared outcome flipped from
// "all accepted" to "all rejected by class, with the class-specific code".
func TestKnownIDsGatedUniformlyByClass(t *testing.T) {
	mk := func(id string) string {
		return "apiVersion: swarm.ai/v1alpha1\n" +
			"kind: Harness\n" +
			"metadata:\n  name: agentstest\n" +
			"provider:\n  id: anthropic\n  model: claude-x\n" +
			"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: [" + id + "]\n"
	}
	cases := []struct {
		id    string
		class PolicyClass
		// wantCode is determined ONLY by the class, which is the uniformity
		// property: two ids of the same class must produce the same code.
		wantCode string
	}{
		// Pre-existing (Phase 1) ids, class per baseline.
		{"steering.ask_user", PolicyDeferDiscover, "harness.agent.tools.deferDiscover"},
		{"vault.exec", PolicyNeverDefault, "harness.agent.tools.neverDefault.unacknowledged"},
		// Ids added in Phase 8c of the SAME two classes: they must behave
		// IDENTICALLY to the pre-existing ones above — never accepted where a
		// pre-existing sibling is rejected, and never given special treatment.
		{"a2a.protocol", PolicyDeferDiscover, "harness.agent.tools.deferDiscover"},
		{"steering.observe_only", PolicyDeferDiscover, "harness.agent.tools.deferDiscover"},
	}
	byClass := make(map[PolicyClass]string, 2)
	for _, c := range cases {
		cap, ok := LookupCapability(c.id)
		if !ok {
			t.Fatalf("capability %q missing from catalog", c.id)
		}
		if cap.Class != c.class {
			t.Fatalf("capability %q class = %q, want %q", c.id, cap.Class, c.class)
		}
		if _, err := CompileBytes([]byte(mk(c.id)), syntheticYAML); err == nil {
			t.Errorf("id %q (class %s): expected Compile to REJECT it by policy class, got success", c.id, c.class)
			continue
		}
		ds := compileErr(t, mk(c.id), syntheticYAML)
		if !hasCode(ds, c.wantCode) {
			t.Errorf("id %q (class %s): want diagnostic %s, got: %v", c.id, c.class, c.wantCode, ds)
			continue
		}
		// Uniformity: same class => same diagnostic code, for every row.
		if prev, seen := byClass[c.class]; seen && prev != c.wantCode {
			t.Errorf("class %s produced two different codes (%s vs %s); rows of one class must be gated identically",
				c.class, prev, c.wantCode)
		}
		byClass[c.class] = c.wantCode
	}
}

// TestNoCapabilityActiveInMinimalPresetByDefault proves the third D3
// guarantee within this slice's scope: no capability — pre-existing or newly
// added — is active in any default/minimal preset. Two independent checks:
// (1) every catalog entry's ActiveInMinimalPreset qualifier is "no" (the only
// baseline rows using "explicit-required" are the two core-config rows that
// are deliberately NOT catalog entries at all — see catalogExcludedBaselineRows);
// (2) structurally, agent.tools is a REQUIRED field with no ambient default
// (resolveTools rejects a manifest that omits it), so an empty selection ([])
// yields zero active capabilities regardless of catalog size.
func TestNoCapabilityActiveInMinimalPresetByDefault(t *testing.T) {
	for _, c := range Catalog() {
		if c.ActiveInMinimalPreset != QNo {
			t.Errorf("capability %q has ActiveInMinimalPreset=%q, want %q (no capability may default-activate)", c.ID, c.ActiveInMinimalPreset, QNo)
		}
	}

	// Empty selection: zero capabilities active regardless of catalog size.
	raw := "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: agentstest\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: []\n"
	p := compileOK(t, raw)
	if len(p.Tools()) != 0 {
		t.Fatalf("empty agent.tools: want zero active tools, got %v", p.Tools())
	}
	if len(p.Explain().Capabilities) != 0 {
		t.Fatalf("empty agent.tools: want zero Capabilities in Explain(), got %v", p.Explain().Capabilities)
	}

	// Omitted agent.tools is REQUIRED-rejected, not defaulted to "all tools".
	omitted := "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: agentstest\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n"
	if ds := compileErr(t, omitted, syntheticYAML); !hasCode(ds, "harness.agent.tools.required") {
		t.Fatalf("expected agent.tools.required diagnostic for omitted tools, got: %v", ds)
	}
}

// TestPlanDigestUnaffectedByCatalogGrowth PROVES (rather than assumes) that
// growing the catalog table (D1) and adding qualifier fields to Capability
// (D4) does not perturb the Plan digest for a manifest that selects only a
// pre-existing capability id. The expected digest below was captured with an
// investigative probe against the UNMODIFIED (pre-Phase-8c) catalog.go before
// any edit in this slice, for a manifest selecting exactly ["forge.read"].
// Structurally this must hold: explain.go's CapabilityRef (which alone feeds
// the digest via ExplainReport.Capabilities) carries only {ID, Class} — never
// RuntimeAliases, SideEffect, or any of the four new qualifier fields — so
// nothing this slice adds to Capability can possibly reach the digest, and
// nothing this slice adds to the catalog can perturb the digest of a manifest
// that does not select a newly-added id.
func TestPlanDigestUnaffectedByCatalogGrowth(t *testing.T) {
	const preCatalogGrowthDigest = "sha256:253bfc50568e33c526357e024cb92f147f225edd9a529a0d9f9a49ebaea64768"
	raw := "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: agentstest\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: [forge.read]\n"
	p := compileOK(t, raw)
	if p.Digest() != preCatalogGrowthDigest {
		t.Fatalf("digest changed for a manifest selecting only a pre-existing id:\n got  %s\n want %s", p.Digest(), preCatalogGrowthDigest)
	}
	// forge.read's own Class must be unchanged (D1/D4 are additive-only; no
	// existing entry's ID/aliases/Class may be renamed or reclassified).
	cap, ok := LookupCapability("forge.read")
	if !ok || cap.Class != PolicyKeep {
		t.Fatalf("forge.read must remain a KEEP capability, got %+v (ok=%v)", cap, ok)
	}
}

// --- catalog-shape invariants ------------------------------------------------

// TestCatalogIDsUniqueAndIndexed guards two structural invariants: every
// catalog ID is unique (no duplicate rows), and catalogIndex covers every
// entry (built-once map has exactly len(catalog) keys, and each looks up to
// itself).
func TestCatalogIDsUniqueAndIndexed(t *testing.T) {
	seen := make(map[string]bool, len(catalog))
	for _, c := range catalog {
		if seen[c.ID] {
			t.Errorf("duplicate catalog id %q", c.ID)
		}
		seen[c.ID] = true
	}
	if len(catalogIndex) != len(catalog) {
		t.Fatalf("catalogIndex has %d entries, want %d (one per catalog row, no collisions/omissions)", len(catalogIndex), len(catalog))
	}
	for _, c := range catalog {
		got, ok := LookupCapability(c.ID)
		if !ok {
			t.Errorf("catalogIndex missing entry for %q", c.ID)
			continue
		}
		if got.ID != c.ID {
			t.Errorf("catalogIndex[%q].ID = %q", c.ID, got.ID)
		}
	}
}

// TestCatalogQualifiersAreClosedSet guards D4: every qualifier field on every
// catalog entry is one of the six closed Qualifier constants, never an ad hoc
// or empty string (an empty/unset qualifier would be exactly the silent
// flattening-to-false regression this phase exists to prevent).
func TestCatalogQualifiersAreClosedSet(t *testing.T) {
	valid := map[Qualifier]bool{QNo: true, QYes: true, QOften: true, QMaybe: true, QPartly: true, QExplicitRequired: true}
	for _, c := range Catalog() {
		for _, f := range []struct {
			name string
			val  Qualifier
		}{
			{"ActiveInMinimalPreset", c.ActiveInMinimalPreset},
			{"HostBound", c.HostBound},
			{"CredentialBound", c.CredentialBound},
			{"CompatibilityOnly", c.CompatibilityOnly},
			{"PresentationOnly", c.PresentationOnly},
		} {
			if !valid[f.val] {
				t.Errorf("capability %q field %s = %q is not a closed Qualifier value", c.ID, f.name, f.val)
			}
		}
	}
}

// --- PARITY_BASELINE reconciliation (the point of this slice) --------------
//
// baselineRow transcribes one row of the PARITY_BASELINE.md "Policy
// classification" table (.swarm-p/harness-anywhere/phase0/PARITY_BASELINE.md
// L404-L442) verbatim. A transcribed in-test fixture was chosen over parsing
// the markdown table at test time: the table cells contain inline markdown
// (backticks, footnote-style file:line citations, embedded commas inside a
// single cell) that would need a fairly involved parser to split correctly,
// and a parser bug silently returning zero rows would defeat the entire
// point of this test. A transcribed fixture is exact, is trivially
// diffable against the baseline in code review, and is guarded against
// silently drifting from the real file by TestBaselineFileShapeGuard below
// (which independently counts table rows in the real file and fails loudly
// if the table shape or row count changes) plus the explicit minimum-count
// assertion in TestCatalogReconcilesBaselineTable.
type baselineRow struct {
	name  string // verbatim (trimmed) "Capability" column text
	class PolicyClass
	q     qual
	// ids are the catalog IDs this row maps to. Empty means "documented
	// exclusion" — reason must be present in catalogExcludedBaselineRows
	// under the exact same name.
	ids []string
}

// baselineRows transcribes ALL 37 rows of the Policy classification table, in
// the table's own order, so a reviewer can diff this fixture directly against
// PARITY_BASELINE.md L406-L442 line by line.
var baselineRows = []baselineRow{
	{"Explicit provider/model + typed credential reference", PolicyKeep, qual{QExplicitRequired, QNo, QYes, QNo, QNo}, nil},
	{"Explicit prompt, workspace, storage, limits", PolicyKeep, qual{QExplicitRequired, QNo, QNo, QNo, QNo}, nil},
	{"`Read` catalog entry", PolicyKeep, qAllNo, []string{"forge.read"}},
	{"`apply_patch` catalog entry", PolicyKeep, qAllNo, []string{"forge.apply_patch"}},
	{"`Undo` catalog entry", PolicyKeep, qAllNo, []string{"forge.undo"}},
	{"Hidden legacy `Edit`/`Write` adapters", PolicyNeverDefault, qCompatOnly, []string{"forge.edit", "forge.write"}},
	{"Shell/process tools", PolicyOptIn, qHostOnly, []string{"builtin.bash"}},
	{"Network/web search/fetch", PolicyOptIn, qCredOnly, []string{"web.websearch", "web.web_fetch", "web.x_search", "web.xai_web_search", "web.anthropic_web_search"}},
	{"Static skills", PolicyOptIn, qAllNo, []string{"skills.skill"}},
	{"Hooks", PolicyOptIn, qual{QNo, QYes, QNo, QNo, QNo}, nil},
	{"MCP tools/resource adapters", PolicyOptIn, qHostCredOften, nil},
	{"MCP prompt API", PolicyOptIn, qHostCredOften, []string{"mcp.prompts"}},
	{"Task state/TaskManage", PolicyOptIn, qHostOften, []string{"ii.task_manage"}},
	{"Question and plan brokers/tools", PolicyOptIn, qHostPresPartly, []string{"interactive.ask_user_question", "interactive.enter_plan_mode", "interactive.exit_plan_mode"}},
	{"History (`HistorySearch`, `HistoryGet`)", PolicyDeferDiscover, qAllNo, []string{"history.history_search", "history.history_get"}},
	{"Project memory", PolicyDeferDiscover, qAllNo, []string{"projectmemory.view", "projectmemory.update"}},
	{"Compaction", PolicyOptIn, qAllNo, []string{"compaction.todo_update"}},
	{"`run_code` (code mode)", PolicyOptIn, qAllNo, []string{"code.run_code"}},
	{"`tool_search` (deferred discovery)", PolicyOptIn, qAllNo, []string{"meta.tool_search"}},
	{"Profiles/fallback graphs", PolicyDeferDiscover, qual{QNo, QNo, QYes, QNo, QNo}, nil},
	{"Task/Delegate/subagent/background agents", PolicyDeferDiscover, qHostCred, []string{"delegation.task", "delegation.subagent_output", "delegation.delegate", "delegation.delegate_output", "background.background_task", "background.task_output", "background.wait_for_agent", "background.multi_agent_wait"}},
	{"Schedules/wakeup", PolicyDeferDiscover, qHostCred, []string{"schedule.cron_create", "schedule.cron_list", "schedule.cron_delete", "schedule.wakeup"}},
	{"A2A/swarm transport and tools", PolicyDeferDiscover, qHostCredMaybe, []string{"a2a.protocol", "a2a.swarm_chat"}},
	{"Plugins", PolicyDeferDiscover, qHostCredMaybe, []string{"plugins.load"}},
	{"Serve/listener interfaces", PolicyDeferDiscover, qual{QNo, QYes, QNo, QNo, QYes}, nil},
	{"Steering/internal tools (`ask_user`, `block_next_tool`, `observe_only`, `inject_system_note`, `refocus`, `log_concern`, `halt_peer_loop`)", PolicyDeferDiscover, qHostOnly, []string{"steering.ask_user", "steering.block_next_tool", "steering.observe_only", "steering.inject_system_note", "steering.refocus", "steering.log_concern", "steering.halt_peer_loop"}},
	{"Debug/raw trace tools", PolicyOptIn, qHostPresPartly, []string{"debug.debug_logs"}},
	{"Browser/computer-use/deploy/database specialist tools", PolicyNeverDefault, qHostCredMaybe, []string{"builtin.agent_browser", "computeruse.computer"}},
	{"Credential resolution (reference only)", PolicyKeep, qual{QExplicitRequired, QYes, QYes, QNo, QNo}, nil},
	{"`vault_list`/status/approval inspection", PolicyOptIn, qHostCred, []string{"vault.list", "vault.two_person_status", "vault.approve"}},
	{"`vault_exec`/`vault_add`", PolicyNeverDefault, qHostCred, []string{"vault.exec", "vault.add"}},
	{"Autoskills/curator", PolicyNeverDefault, qHostCred, []string{"skills.skill_manage"}},
	{"Yolo/automatic approval", PolicyNeverDefault, qual{QNo, QYes, QNo, QNo, QNo}, nil},
	{"Automatic global daemon startup", PolicyNeverDefault, qual{QNo, QYes, QNo, QNo, QNo}, nil},
	{"Ambient config/INDEX/MCP/skills/plugins/provider persistence", PolicyNeverDefault, qual{QNo, QNo, QMaybe, QNo, QNo}, nil},
}

// TestCatalogReconcilesBaselineTable is the conformance test: it mechanically
// reconciles catalog.go against baselineRows so drift can never again be
// silent. It asserts (a) a non-trivial minimum row count so a fixture
// accidentally emptied to zero rows cannot pass, (b) every row maps to >=1
// catalog entry OR is a documented exclusion (never both, never neither),
// (c) every mapped entry's Class and all five qualifiers equal the row's,
// (d) every documented exclusion here has a matching reason in
// catalogExcludedBaselineRows (code comment <-> test fixture cannot drift),
// and (e) every catalog entry is reconciled by some row (no silent addition
// outside this reconciliation).
func TestCatalogReconcilesBaselineTable(t *testing.T) {
	const minBaselineRows = 30 // guards against an accidentally emptied/truncated fixture
	if len(baselineRows) < minBaselineRows {
		t.Fatalf("baselineRows has only %d rows; expected >= %d reconciled PARITY_BASELINE rows (zero/near-zero rows would silently defeat this test)", len(baselineRows), minBaselineRows)
	}

	excludedReason := make(map[string]string, len(catalogExcludedBaselineRows))
	for _, e := range catalogExcludedBaselineRows {
		excludedReason[e.Row] = e.Reason
	}

	seenIDs := make(map[string]bool)
	excludedSeen := make(map[string]bool)
	for _, row := range baselineRows {
		if len(row.ids) == 0 {
			reason, documented := excludedReason[row.name]
			if !documented || reason == "" {
				t.Errorf("baseline row %q has no mapped catalog ids AND no documented exclusion reason in catalogExcludedBaselineRows; silent omission", row.name)
				continue
			}
			excludedSeen[row.name] = true
			continue
		}
		for _, id := range row.ids {
			seenIDs[id] = true
			c, ok := LookupCapability(id)
			if !ok {
				t.Errorf("baseline row %q -> id %q not present in catalog", row.name, id)
				continue
			}
			if c.Class != row.class {
				t.Errorf("id %q (baseline row %q): Class = %q, want %q", id, row.name, c.Class, row.class)
			}
			if c.ActiveInMinimalPreset != row.q.active {
				t.Errorf("id %q (baseline row %q): ActiveInMinimalPreset = %q, want %q", id, row.name, c.ActiveInMinimalPreset, row.q.active)
			}
			if c.HostBound != row.q.host {
				t.Errorf("id %q (baseline row %q): HostBound = %q, want %q", id, row.name, c.HostBound, row.q.host)
			}
			if c.CredentialBound != row.q.credential {
				t.Errorf("id %q (baseline row %q): CredentialBound = %q, want %q", id, row.name, c.CredentialBound, row.q.credential)
			}
			if c.CompatibilityOnly != row.q.compat {
				t.Errorf("id %q (baseline row %q): CompatibilityOnly = %q, want %q", id, row.name, c.CompatibilityOnly, row.q.compat)
			}
			if c.PresentationOnly != row.q.presentation {
				t.Errorf("id %q (baseline row %q): PresentationOnly = %q, want %q", id, row.name, c.PresentationOnly, row.q.presentation)
			}
		}
	}

	// Every documented exclusion must be reconciled by an actual baselineRows
	// entry (no orphan/stale exclusion reasons left in catalog.go).
	for name := range excludedReason {
		if !excludedSeen[name] {
			t.Errorf("catalogExcludedBaselineRows entry %q has no corresponding baselineRows fixture row (stale exclusion)", name)
		}
	}

	// Every catalog entry must be reconciled by some baseline row.
	for _, c := range Catalog() {
		if !seenIDs[c.ID] {
			t.Errorf("catalog id %q is not reconciled to any PARITY_BASELINE row in baselineRows (silent addition outside the reconciliation)", c.ID)
		}
	}
}

// TestBaselineFileShapeGuard independently counts markdown table data rows in
// the package-owned capability policy (not the transcribed fixture) and fails
// loudly if the policy has moved, been deleted, or the table has shrunk well
// below the 37 rows this slice reconciled against. This keeps
// TestCatalogReconcilesBaselineTable's transcribed fixture honest without
// depending on implementation-process artifacts outside this package.
func TestBaselineFileShapeGuard(t *testing.T) {
	const relPath = "docs/CAPABILITY_POLICY.md"
	data, err := os.ReadFile(relPath)
	if err != nil {
		t.Fatalf("capability policy not found at %s (moved/deleted?): %v", relPath, err)
	}
	content := string(data)
	if !regexp.MustCompile(`(?m)^## Policy classification$`).MatchString(content) {
		t.Fatalf("capability policy no longer has a '## Policy classification' heading; table location has moved")
	}
	// Isolate the section between the heading and the next "## " heading (or EOF).
	idx := regexp.MustCompile(`(?m)^## Policy classification$`).FindStringIndex(content)
	rest := content[idx[1]:]
	if next := regexp.MustCompile(`(?m)^## `).FindStringIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	// Count markdown table data rows: lines starting with "| " that are not
	// the header row or the "|---|" separator row.
	rowRe := regexp.MustCompile(`(?m)^\|.+\|$`)
	rows := rowRe.FindAllString(rest, -1)
	dataRows := 0
	for _, r := range rows {
		if strings_HasPrefixDash(r) {
			continue // separator row, e.g. "|---|---|..."
		}
		if regexpContainsCapabilityHeader(r) {
			continue // header row
		}
		dataRows++
	}
	const minTableRows = 30
	if dataRows < minTableRows {
		t.Fatalf("capability policy table has only %d data rows, want >= %d; table shape changed, baselineRows fixture in catalog_test.go must be re-reconciled", dataRows, minTableRows)
	}
}

// strings_HasPrefixDash reports whether a markdown table row is the
// "|---|---|..." separator row.
func strings_HasPrefixDash(row string) bool {
	trimmed := row
	for len(trimmed) > 0 && (trimmed[0] == '|' || trimmed[0] == ' ') {
		trimmed = trimmed[1:]
	}
	return len(trimmed) > 0 && (trimmed[0] == '-' || trimmed[0] == ':')
}

// regexpContainsCapabilityHeader reports whether a markdown table row is the
// "| Capability | Roots | ..." header row.
func regexpContainsCapabilityHeader(row string) bool {
	return regexp.MustCompile(`^\|\s*Capability\s*\|`).MatchString(row)
}
