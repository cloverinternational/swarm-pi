package harness

import (
	"bytes"
	"encoding/json"
)

// Document is the on-disk v1alpha1 harness document. Every struct is tagged only
// with json tags; YAML input is converted to canonical JSON before strict
// decoding, so the same tags drive both formats (see configformat).
//
// Only ToolSelection carries a custom json.Unmarshaler. Every other nested
// struct is decoded strictly (DisallowUnknownFields recurses), so an unknown key
// at any object level is rejected.
type Document struct {
	APIVersion  string      `json:"apiVersion"`
	Kind        string      `json:"kind"`
	Metadata    Metadata    `json:"metadata"`
	Runtime     Runtime     `json:"runtime"`
	Provider    Provider    `json:"provider"`
	Agent       Agent       `json:"agent"`
	Permissions Permissions `json:"permissions"`
	Interfaces  Interfaces  `json:"interfaces"`
	// Compatibility selects a named compatibility preset (Phase 10c; see
	// presets.go). It is a pure EXPANSION seam, never a bypass: when set, it
	// supplies the primary agent's `agent.tools` selection (which must then be
	// OMITTED — see D5 in presets.go) as an ordinary set of catalog IDs that
	// flows through the SAME resolveTools/checkCapabilityPolicy gate as any
	// hand-written `tools:` list. It is omitempty and unknown outside the
	// registered preset names ("minimal", "tui-v1"; KnownPresetNames()), so a
	// manifest that omits it compiles to the EXACT pre-10c plan, digest
	// included (D4).
	Compatibility string        `json:"compatibility,omitempty"`
	Skills        SkillsSection `json:"skills,omitempty"`
	Hooks         []HookEntry   `json:"hooks,omitempty"`
	Mcp           []McpEntry    `json:"mcp,omitempty"`
	// Profiles/Fallback/Agents are the Phase 8a declarative sections (see
	// agents.go). They are DECLARATION + RESOLUTION only: nothing is
	// constructed, spawned, or executed by compiling them.
	Profiles []ProfileEntry `json:"profiles,omitempty"`
	Fallback *FallbackEntry `json:"fallback,omitempty"`
	Agents   []AgentEntry   `json:"agents,omitempty"`
	// Budgets is the Phase 9a declarative budgets section (see budgets.go). It
	// is DECLARATION + RESOLUTION only: compiling it classifies and validates
	// bounds but enforces nothing (that is Phase 9d). Every kind inside it is
	// independently optional, so an omitted (or `budgets: {}`) section compiles
	// to exactly the pre-9a plan, digest included.
	Budgets BudgetsSection `json:"budgets,omitempty"`
	// Schedules is the Phase 9b declarative schedules section (see
	// schedules.go). It is DECLARATION + RESOLUTION only: compiling it
	// validates and classifies every operational dimension but constructs no
	// scheduler, registers no cron expression, reads no clock, and starts no
	// goroutine (wiring/preflight is Phase 9d). It is omitempty, so a manifest
	// without `schedules:` compiles to exactly the pre-9b plan, digest
	// included.
	Schedules []ScheduleEntry `json:"schedules,omitempty"`
	// Workflows is the Phase 9c declarative workflows section (see
	// workflows.go). Each entry is a TYPED REFERENCE to an external workflow
	// manifest — id + file + expectedVersion — and NOT a re-declaration of its
	// graph: the harness deliberately embeds no second graph language. It is
	// DECLARATION + RESOLUTION only: compiling it reads, pins, adapts, and gates
	// the referenced file but constructs no WorkflowEngine, group, coordinator,
	// or agent (wiring is Phase 9d).
	//
	// This field is the ONLY place a workflow can enter the harness document, and
	// its three camelCase keys are the ONLY workflow-related keys the harness
	// decode target has. A snake_case workflow key therefore cannot be absorbed
	// here — the strict decode of THIS document stays exactly as strict as before
	// (see the D2 note in workflows.go). It is omitempty, so a manifest without
	// `workflows:` compiles to exactly the pre-9c plan, digest included.
	Workflows []WorkflowEntry `json:"workflows,omitempty"`
}

// Metadata identifies the harness. Name is required and is the primary logical
// ID of the document.
type Metadata struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// Runtime holds host/session posture. Paths are manifest-relative and resolved
// against the manifest directory, never the process CWD.
type Runtime struct {
	Workspace string `json:"workspace,omitempty"`
	Storage   string `json:"storage,omitempty"`
	Watch     bool   `json:"watch,omitempty"`
}

// Provider selects the model provider and its typed credential reference. The
// credential is a reference (env/vault/file/inline), never a literal secret in a
// resolved plan's serialized form.
type Provider struct {
	ID         string `json:"id"`
	Model      string `json:"model"`
	BaseURL    string `json:"baseURL,omitempty"`
	Credential *Ref   `json:"credential,omitempty"`
}

// Agent configures the primary agent. Tools is REQUIRED for the primary agent
// (tri-state; see ToolSelection). SystemPrompt is a one-of inline|file.
type Agent struct {
	SystemPrompt *SystemPrompt `json:"systemPrompt,omitempty"`
	Tools        ToolSelection `json:"tools"`
	Limits       Limits        `json:"limits,omitempty"`
}

// SystemPrompt is a one-of: exactly one of Inline or File must be set.
type SystemPrompt struct {
	Inline string `json:"inline,omitempty"`
	File   string `json:"file,omitempty"`
}

// Limits are optional per-run bounds.
type Limits struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
	MaxTurns        int `json:"maxTurns,omitempty"`
	TimeoutSeconds  int `json:"timeoutSeconds,omitempty"`
}

// Permissions is the explicit, auditable execution posture. There is no implicit
// yolo default; approvalMode defaults to "interactive" when unset.
type Permissions struct {
	ApprovalMode      string `json:"approvalMode,omitempty"`
	WorkspaceBoundary *bool  `json:"workspaceBoundary,omitempty"`
	AllowMutation     bool   `json:"allowMutation,omitempty"`

	// AcknowledgeNeverDefault is the Phase 8d NEVER-DEFAULT posture: the
	// explicit, per-id acknowledgement required to select a NEVER-DEFAULT
	// capability in ANY `tools:` selection (primary agent or agents[] entry).
	//
	// Listing a NEVER-DEFAULT id in `tools:` is NECESSARY BUT NOT SUFFICIENT;
	// the same exact id must also appear here. The field is deliberately a
	// list of EXACT catalog ids and nothing else:
	//
	//   - there is NO blanket boolean and NO wildcard — "acknowledge
	//     everything" is unexpressible by construction, so the posture can
	//     never be satisfied except one high-impact capability at a time;
	//   - an id here that is not selected in any `tools:` is an ERROR
	//     (dangling acknowledgements would silently pre-authorize a future
	//     edit that adds the selection);
	//   - an id here whose catalog class is not NEVER-DEFAULT is an ERROR
	//     (this keeps the field honest and stops it becoming a junk drawer of
	//     unrelated ids).
	//
	// It lives under `permissions:` because it IS an execution posture — the
	// same auditable axis as approvalMode/allowMutation — and NOT under
	// `agent:`, so one document-scoped acknowledgement governs every agent
	// while remaining a single reviewable place to audit.
	//
	// It is `omitempty`: a manifest that selects no NEVER-DEFAULT capability
	// and omits the key marshals to the exact pre-8d canonical JSON, and
	// therefore keeps its exact pre-8d plan digest.
	AcknowledgeNeverDefault []string `json:"acknowledgeNeverDefault,omitempty"`
}

// Interfaces selects the presentation surface. Interface selection must never
// change provider, prompt, tools, or permissions.
type Interfaces struct {
	Default string          `json:"default,omitempty"`
	TUI     *InterfaceTUI   `json:"tui,omitempty"`
	Print   *InterfacePrint `json:"print,omitempty"`
}

// InterfaceTUI holds presentation-only options for the interactive surface.
type InterfaceTUI struct {
	Theme string `json:"theme,omitempty"`
}

// InterfacePrint holds options for one-shot/print mode.
type InterfacePrint struct {
	Format string `json:"format,omitempty"`
}

// ToolSelection encodes tri-state tool selection and deliberately does NOT reuse
// ToolHints' "empty-means-all" semantics:
//
//   - key absent         -> Specified=false        (invalid for the primary agent)
//   - "tools: []"        -> Specified=true, IDs=[]  (valid: zero active tools)
//   - "tools: [a, b]"    -> Specified=true, IDs set (exact capability IDs)
//
// The custom UnmarshalJSON is only invoked when the key is present, so Specified
// reliably distinguishes "absent" from "explicitly empty".
type ToolSelection struct {
	Specified bool
	IDs       []string
}

// UnmarshalJSON implements the tri-state decode. A JSON null is treated as
// "not specified" (equivalent to omitting the key), which keeps the required
// check meaningful for the primary agent.
func (t *ToolSelection) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if string(trimmed) == "null" {
		t.Specified = false
		t.IDs = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var ids []string
	if err := dec.Decode(&ids); err != nil {
		return err
	}
	t.Specified = true
	if ids == nil {
		ids = []string{}
	}
	t.IDs = ids
	return nil
}

// MarshalJSON mirrors the tri-state: not-specified -> null, else the ID array.
func (t ToolSelection) MarshalJSON() ([]byte, error) {
	if !t.Specified {
		return []byte("null"), nil
	}
	if t.IDs == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(t.IDs)
}
