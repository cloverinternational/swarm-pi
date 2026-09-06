// Harness Phase 2 client bridge — public construction options.
//
// This file adds the ONLY public entry points needed to drive a closed,
// deterministic client construction from an immutable *harness.Plan. It is
// gated entirely behind WithHarnessPlan: a Client built without it is
// byte-identical to the pre-harness client.New behavior.
//
// The construction context is intentionally carried in a single options field
// (options.harness) so the surgical edit to client.go stays to one struct field
// plus one closed-construction branch. WithHarnessPlan and the posture options
// all mutate that one field.
package client

import (
	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// harnessConstruction carries everything the closed harness construction path
// needs. It is populated by WithHarnessPlan and the posture options and read
// only by newClientFromHarness. Storing it behind a single options pointer
// keeps the client.go footprint minimal (one field, one branch).
type harnessConstruction struct {
	// plan is the immutable compiled harness plan. Non-nil plan activates the
	// closed-construction path in client.New.
	plan *harness.Plan

	// allowYolo is the explicit named posture required before an approvalMode of
	// "yolo" may be applied. Without it, a plan requesting yolo FAILS
	// construction (D2). yolo is never applied silently.
	allowYolo bool

	// bindings is the HOST-supplied runtime binding registry (Phase 9d). It is
	// a property of the hosting PROCESS, not of a manifest: it is set once, at
	// construction, by WithHarnessBindings and is read by the fail-closed
	// preflight in harness_bindings.go. Its zero value supplies NOTHING, which
	// is the safe default — a plan requiring a binding is then refused rather
	// than silently degraded (D3).
	bindings HarnessBindings

	// auditor is the HOST-supplied audit sink for privileged harness
	// operations (Phase 11b). It is a property of the hosting PROCESS,
	// exactly like bindings above — set once, at construction, by
	// WithHarnessAuditor and read only by the audit-emission helpers in
	// harness_audit.go. Its nil zero value is a silent, allocation-free
	// no-op: auditing is additive observability and must never gate or
	// alter whether an apply happens.
	auditor observability.Auditor

	// requireExecutableConsent is a persistent HOST posture. When true, a plan
	// containing command/script hooks or stdio MCP servers is refused unless
	// executableConsentDigest exactly matches the compiled plan digest.
	requireExecutableConsent bool
	executableConsentDigest  string
}

// WithHarnessPlan makes client.New construct a minimal, deterministic harness
// directly from an immutable *harness.Plan, without any TUI-private preassembly.
//
// When a plan is supplied, construction adopts a CLOSED posture: no auto-config,
// no INDEX.md, no ambient credentials, no default tools, no hooks, no
// skills/autoskills, no MCP discovery, no plugins, and no implicit provider
// fallback. Only the plan's declared provider/credential, system prompt,
// workspace, storage, limits, permissions, and EXACT selected tools apply.
//
// Passing a nil plan is a no-op (the normal client.New path runs unchanged).
func WithHarnessPlan(plan *harness.Plan) Option {
	return func(o *options) {
		if plan == nil {
			return
		}
		if o.harness == nil {
			o.harness = &harnessConstruction{}
		}
		o.harness.plan = plan
	}
}

// WithHarnessAllowYolo supplies the explicit named posture that a plan must have
// before an approvalMode of "yolo" is honored (D2). It is a deliberate,
// code-level opt-in: even a plan that declares yolo cannot auto-approve unless
// the operator also passes this option. Without it, yolo FAILS construction.
//
// This option only has meaning alongside WithHarnessPlan.
func WithHarnessAllowYolo() Option {
	return func(o *options) {
		if o.harness == nil {
			o.harness = &harnessConstruction{}
		}
		o.harness.allowYolo = true
	}
}

// WithHarnessBindings supplies the HOST implementations of the runtime bindings
// a compiled plan declares it requires (plan.md §3.3: "the active interface
// adapter supplies implementations").
//
// Each field of HarnessBindings is a narrow, single-purpose interface named
// after one harness.RuntimeBindingKind. A nil field is an ABSENCE, never a
// default: before execution, preflight compares Plan.RequiredRuntimeBindings()
// against this registry and REFUSES to construct or re-apply a plan whose
// requirements are not met (client.ErrHarnessBindingUnsatisfied). There is no
// bypass — supply the implementation or change the manifest.
//
// Calling it repeatedly REPLACES the registry wholesale rather than merging, so
// the supplied set is exactly what the caller last passed and cannot accumulate
// implementations from an earlier option by accident.
//
// This option only has meaning alongside WithHarnessPlan. A plan that requires
// zero bindings is entirely unaffected by it.
func WithHarnessBindings(bindings HarnessBindings) Option {
	return func(o *options) {
		if o.harness == nil {
			o.harness = &harnessConstruction{}
		}
		o.harness.bindings = bindings
	}
}

// WithHarnessRequireExecutableConsent opts this host into an exact-digest
// consent gate for local executable hooks and stdio MCP servers. The digest
// must be obtained from the already-compiled plan and approved out of band.
// It is host-owned state: manifests cannot acknowledge themselves.
func WithHarnessRequireExecutableConsent(digest string) Option {
	return func(o *options) {
		if o.harness == nil {
			o.harness = &harnessConstruction{}
		}
		o.harness.requireExecutableConsent = true
		o.harness.executableConsentDigest = digest
	}
}

// WithHarnessAuditor supplies the HOST implementation of the audit sink that
// records privileged harness operations — today, exactly ApplyHarnessPlan's
// four terminal outcomes via ApplyHarnessPlanAudited (see harness_audit.go).
//
// A nil auditor (the default; also what a plain Client that never calls this
// option has) is a silent no-op: no event is ever recorded, and — critically
// — auditing NEVER changes control flow. A failing, slow, or panicking
// auditor cannot fail, block, or alter an apply; see emitHarnessAuditEvent's
// doc for the exact guarantee.
//
// This option only has meaning alongside WithHarnessPlan. Calling it
// repeatedly REPLACES the auditor, mirroring WithHarnessBindings' replace
// (not merge) semantics.
func WithHarnessAuditor(auditor observability.Auditor) Option {
	return func(o *options) {
		if o.harness == nil {
			o.harness = &harnessConstruction{}
		}
		o.harness.auditor = auditor
	}
}
