// Package chat — Phase 10d compatibility adapter (plan.md Phase 10 action #6's
// "convert" half: "convert NewSDKIntegrationWithOptions into a compatibility
// adapter that produces a plan/bindings, or retire it only after all call
// sites migrate"). This file adds the CONVERT half only:
// NewSDKIntegrationWithOptions is untouched (still exported, still the entry
// point every call site uses today — see D1 in the Phase 10d brief) and no
// legacy call site is migrated in this slice.
//
// # D1 — pure, independently testable
//
// BuildTUICompatPlan and BuildTUICompatHarnessBindings are the two pure
// functions this file exposes. Neither performs file I/O, a network call, or
// constructs a TUI, a provider, an agent, or a tool registry:
//   - BuildTUICompatPlan builds an in-memory harness.Document, marshals it to
//     JSON, and calls harness.CompileBytes — the harness package's own pure,
//     side-effect-free compiler (it reads no file; the manifest bytes are
//     already in memory). This is "the inputs from which a [plan] is
//     compiled" the Phase 10d brief permits as D1(a)'s deliverable shape.
//   - BuildTUICompatHarnessBindings reads only its SDKIntegrationOptions
//     argument and returns a value; it opens nothing and blocks on nothing.
//
// harness_compat_adapter_test.go exercises both directly with no *App, no
// SDKIntegration, no provider, and no sdkclient.Client anywhere in the test.
//
// # D2 — this file does not, by itself, remove any injection
//
// The prebuilt-injection enumeration the Phase 10d brief requires lives in
// this file's package doc for discoverability, but the actual finding and
// disposition of each site is recorded in the Phase 10d SUMMARY (this slice's
// final report), not here, because most of the candidate sites (SetVaultProvider,
// EnableAdvancedToolMode — see sdk_integration.go) sit OUTSIDE
// newHarnessSDKIntegration, the one function this slice's brief permits
// editing in sdk_integration.go. See the SUMMARY for the full enumeration.
//
// # D3 — reuses harness.LookupPreset("tui-v1") verbatim, never a second list
//
// BuildTUICompatPlan sets Document.Compatibility = "tui-v1" and leaves
// Document.Agent.Tools unset (harness/presets.go's resolveCompatibilityPreset
// requires exactly that — declaring both is a compile-time error,
// harness.compatibility.conflictsWithTools). The concrete catalog-id
// expansion is decided ENTIRELY by the harness package's own preset table
// (presets.go); this file never enumerates a capability id itself. See
// harness_compat_adapter_test.go's TestBuildTUICompatPlanSelectsExactlyTUIV1Preset
// for the equality proof.
//
// A REPORTED GAP (not patched here, per the brief: "if the preset is missing
// something the adapter needs, report it — do not add a competing list"):
// compiling a `compatibility: tui-v1` document SUCCEEDS (harness.CompileBytes
// has no knowledge of the client package's binder), but constructing a real
// client from that plan (sdkclient.New(sdkclient.WithHarnessPlan(plan)))
// FAILS TODAY, because tuiV1PresetIDs (harness/presets.go) includes
// "web.websearch", yet client/harness_catalog_bind.go's harnessToolBindings
// map (the Phase 10b BOUND set) does NOT contain "web.websearch" — it is
// listed only in harnessToolHostBindingReasons (HOST-BINDING-REQUIRED),
// exactly the bucket presets.go's own package doc (D3, test 3) says a tui-v1
// entry must NOT be in. This was verified empirically while building this
// adapter (compile succeeds; sdkclient.New then returns "harness: tool id
// \"web.websearch\" requires a host runtime binding (tool-credential) that
// this client does not supply"). It is a Phase 10c authoring inconsistency
// against Phase 10b's binder state, not a Phase 10d defect, and neither the
// harness nor client package may be modified by this slice (HARD INVARIANTS).
// It is reported here, in the Phase 10d SUMMARY, and belongs on the Phase 11
// backlog: either bind web.websearch's tool-credential requirement or remove
// it from tuiV1PresetIDs.
//
// # D4 — see harness_compat_adapter_test.go and the newHarnessSDKIntegration
// equality test this slice adds (TestHarnessBranchEffectiveToolsEqualPlanAuthorizedSet
// in harness_compat_adapter_test.go), which exercises the REAL harness branch
// (NewSDKIntegrationWithOptions with opts.HarnessPlan set) using the `minimal`
// preset (the one tui-v1-adjacent preset that IS fully constructible today,
// per the D3 gap above) and asserts the constructed registry's size, the
// live agent's provider-exposed tool count, and the plan's own
// SelectedCatalogIDs count are all EXACTLY equal — the same invariant B2
// (Phase 10a/10b) violated for HistorySearch/HistoryGet.
package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

// TUICompatOptions carries the small set of inputs BuildTUICompatPlan needs
// that SDKIntegrationOptions does not itself carry (a structured credential
// reference and an inline system prompt). It is intentionally NOT folded into
// SDKIntegrationOptions: that struct is the legacy composition's option bag
// (D2 of the Phase 10d brief keeps the legacy branch untouched), and adding
// harness-only fields to it would blur exactly the "plan is the sole source"
// boundary this slice exists to draw.
type TUICompatOptions struct {
	// SystemPrompt is the primary agent's inline system prompt. Required:
	// harness/plan.go's resolvePrompt requires exactly one of inline|file for
	// every harness document, and this adapter only ever produces inline.
	SystemPrompt string

	// CredentialEnvVar, when non-empty, is declared as the compiled plan's
	// provider.credential env reference (harness.Ref{Env: CredentialEnvVar}).
	// tui-v1 selects CredentialBound=yes capabilities (web.websearch,
	// vault.*; see harness/presets.go), and harness/plan.go's
	// checkCapabilityPolicy fails closed
	// (harness.agent.tools.credentialUnbound) when the document declares NO
	// credential reference at all. This adapter never reads an ambient
	// credential itself (that would reopen exactly the ambient-authority leak
	// B1, Phase 10a/10b, closed for the fallback chain) — the caller must
	// name an explicit env var, or BuildTUICompatPlan fails closed.
	CredentialEnvVar string

	// Name sets the compiled plan's metadata.name. Defaults to
	// "tui-v1-compat" when empty.
	Name string
}

// BuildTUICompatHarnessBindings is the D1(b) half of the adapter: the
// explicit sdkclient.HarnessBindings a caller should supply to
// sdkclient.WithHarnessBindings alongside the plan BuildTUICompatPlan
// produces.
//
// It returns the zero value today, and that is reported here rather than
// silently assumed: none of SDKIntegrationOptions' current host objects
// implement the narrow interfaces client/harness_bindings.go declares.
// ApprovalBroker is tools.ApprovalBroker (used only for the plan's own
// `interactive` approvalMode via SetPermissionChecker — an authorization
// CHECK on already-plan-selected tools, not a capability source);
// QuestionBroker and PlanBroker are TUI-private interactive brokers with no
// harness-side interface at all (see harness/presets.go's
// ShortfallHostBindingRequired entries for interactive.ask_user_question /
// interactive.enter_plan_mode). None of the nine scheduler bindings
// (client.HarnessSchedulerZonedClock and siblings) or
// client.HarnessApprovalBroker/HarnessApprovalUnattendedResolver have a TUI
// counterpart to adapt from at all.
//
// The zero value is the CORRECT, SAFE value for a plan that (like the one
// BuildTUICompatPlan produces) declares no schedules and stays in
// `interactive` approvalMode: client/harness_bindings.go's own documentation
// states the zero value "supplies nothing, which is the correct and safe
// default" for such a plan, and a plan that DOES require a binding this
// function does not supply is refused by the client's own fail-closed
// preflight (client.ErrHarnessBindingUnsatisfied) — never silently degraded.
// The `opts` parameter is accepted (rather than dropped) so this function's
// signature documents, in code, exactly what inputs were considered and
// found to have no harness-side counterpart today; a future phase that adds
// one extends this function's body, not its signature.
func BuildTUICompatHarnessBindings(opts SDKIntegrationOptions) sdkclient.HarnessBindings {
	_ = opts // no current TUI host object satisfies a harness binding interface; see doc above
	return sdkclient.HarnessBindings{}
}

// BuildTUICompatPlan is the D1(a) half of the adapter: it maps
// (providerName, model, opts, compat) into a compiled *harness.Plan built
// from the tui-v1 compatibility preset (D3 — see the package doc above for
// the reused-preset guarantee and the reported web.websearch gap).
//
// PURITY: no file I/O, no network call, no TUI/provider/agent/registry
// construction. harness.CompileBytes is the harness package's own pure
// compiler, operating only on the in-memory bytes built here.
//
// FAIL-CLOSED: any input this function cannot turn into a valid tui-v1 plan
// (missing provider/model, missing system prompt, missing credential env
// var, or a harness compiler diagnostic) is returned as a non-nil error and a
// nil plan. There is no fallback to legacy private composition — the HARD
// INVARIANT this slice's brief requires.
func BuildTUICompatPlan(providerName, model string, opts SDKIntegrationOptions, compat TUICompatOptions) (*harness.Plan, sdkclient.HarnessBindings, error) {
	bindings := BuildTUICompatHarnessBindings(opts)

	if strings.TrimSpace(providerName) == "" {
		return nil, bindings, fmt.Errorf("harness_compat_adapter: providerName is required")
	}
	if strings.TrimSpace(model) == "" {
		return nil, bindings, fmt.Errorf("harness_compat_adapter: model is required")
	}
	if strings.TrimSpace(compat.SystemPrompt) == "" {
		return nil, bindings, fmt.Errorf("harness_compat_adapter: TUICompatOptions.SystemPrompt is required (harness/plan.go requires agent.systemPrompt)")
	}
	if strings.TrimSpace(compat.CredentialEnvVar) == "" {
		// checkCapabilityPolicy (harness/plan.go) rejects tui-v1's
		// CredentialBound=yes ids (web.websearch, vault.*) outright when the
		// document declares no credential reference at all
		// (harness.agent.tools.credentialUnbound). Fail here, with the same
		// actionable framing, rather than let the caller hit that compiler
		// diagnostic unexplained.
		return nil, bindings, fmt.Errorf("harness_compat_adapter: TUICompatOptions.CredentialEnvVar is required for the tui-v1 preset (web.websearch and vault.* are credential-bound); this adapter never reads an ambient credential implicitly")
	}

	name := strings.TrimSpace(compat.Name)
	if name == "" {
		name = "tui-v1-compat"
	}

	doc := harness.Document{
		APIVersion: harness.APIVersionV1Alpha1,
		Kind:       harness.KindHarness,
		Metadata:   harness.Metadata{Name: name},
		Provider: harness.Provider{
			ID:         providerName,
			Model:      model,
			Credential: &harness.Ref{Env: compat.CredentialEnvVar},
		},
		Agent: harness.Agent{
			SystemPrompt: &harness.SystemPrompt{Inline: compat.SystemPrompt},
		},
		// D3: the preset supplies the tools selection; agent.tools MUST stay
		// unset (resolveCompatibilityPreset, harness/presets.go) or
		// compilation fails closed with
		// harness.compatibility.conflictsWithTools.
		Compatibility: "tui-v1",
		Permissions: harness.Permissions{
			ApprovalMode: "interactive",
			// tui-v1 selects the NEVER-DEFAULT vault.exec/vault.add ids
			// (harness/presets.go); checkCapabilityPolicy requires each to be
			// acknowledged individually — selection via the preset alone is
			// not sufficient (same class gate a hand-written `tools:` list
			// would need to satisfy, D1 in presets.go).
			AcknowledgeNeverDefault: []string{"vault.exec", "vault.add"},
		},
	}
	if root := strings.TrimSpace(opts.WorkspaceRoot); root != "" {
		doc.Runtime.Workspace = root
	}

	raw, err := json.Marshal(&doc)
	if err != nil {
		return nil, bindings, fmt.Errorf("harness_compat_adapter: marshal compatibility document: %w", err)
	}
	plan, err := harness.CompileBytes(raw, "tui-compat-adapter://"+name)
	if err != nil {
		return nil, bindings, fmt.Errorf("harness_compat_adapter: compile tui-v1 compatibility plan: %w", err)
	}
	return plan, bindings, nil
}
