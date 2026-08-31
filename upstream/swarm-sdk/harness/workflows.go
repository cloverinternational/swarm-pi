package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/configformat"
)

// Phase 9c — declarative WORKFLOWS behind a public TYPED REFERENCE.
//
// This file is DECLARATION + RESOLUTION ONLY. It resolves a reference to an
// external workflow manifest, pins its identity, adapts its snake_case schema
// into the harness's camelCase surface FIELD BY FIELD, and gates what the
// adaptation is allowed to imply. It NEVER constructs a WorkflowEngine, a
// group, a coordinator, an agent, or a provider; it starts no goroutine, reads
// no clock, and executes nothing. Wiring is Phase 9d.
//
// D1 — REFERENCE, NOT RE-DECLARATION. plan.md Phase 9 action #1 allows "a public
// versioned spec OR typed reference" and states the goal as production
// orchestration "without embedding another graph language". A `workflows:` entry
// therefore names (id) + references (file) + PINS (expectedVersion) an existing
// workflow manifest, resolved with the same Phase 5a contained-file machinery
// skills/hooks use (resolveContainedFile: manifest-relative, traversal- and
// symlink-guarded). The harness schema gains NO groups, NO transitions, NO
// agents, and NO second DAG syntax.
//
// D2 — THE ADAPTER IS EXPLICIT AND ONE-DIRECTIONAL, AND THE TWO KEY SPACES ARE
// STRUCTURALLY SEPARATE. Three properties make "never concatenate raw schemas" a
// guarantee rather than an intention:
//
//  1. The harness Document (types.go) gains exactly ONE field, `workflows`, whose
//     element type is WorkflowEntry — three camelCase keys (id/file/
//     expectedVersion). No workflow key can enter the harness decode target,
//     because the harness decode target has nowhere to put one; the harness
//     manifest's strict decode is untouched and stays strict.
//  2. The workflow file is decoded into the PRIVATE wf* mirror DTOs below, whose
//     json tags are snake_case and which are unexported, so no caller can hand a
//     mirror value to the harness surface and no mirror type can appear in a
//     Plan field, an accessor, or Explain.
//  3. Every mirror field is read by exactly one mapping function which writes
//     into a DISTINCT camelCase Workflow*Spec type. There is no shared struct, no
//     embedding, no `map[string]any`, and no json.RawMessage on ANY plan-carried
//     type: map-shaped workflow fields are carried as sorted KEY LISTS only (see
//     workflowDroppedReasons), so a raw value cannot be smuggled through.
//
// Unknown keys in the referenced file are a hard ERROR (harness.workflows.file
// .unknownField), not a warning: the project's bias is fail-closed, the harness's
// own manifest already rejects unknown keys at every level, and an unknown key in
// a workflow file is exactly how a semantic the harness cannot see (the real
// `gates:` key in swarm-tui/workflows/multi_model_debate.yaml, which
// internal/mode's own DTOs do not declare either) would be silently accepted.
//
// D3 — IDENTITY IS PINNED. The referenced file's content hash is carried on the
// plan and therefore folded into the digest through ExplainReport, exactly like a
// skill or a prompt file: swapping the workflow file changes the plan's identity.
// `expectedVersion` is REQUIRED and ENFORCED against the file's own `version`.
// Justification: the workflow file is an EXTERNAL artifact that the harness
// manifest does not own, so if the version were optional a 1.0.0 -> 2.0.0 swap
// would change only a hash nobody reads before a run while the audited manifest
// still claimed the same orchestration. Requiring the version makes the manifest
// STATE the contract and makes the compiler verify it; a missing `version` in the
// file is an error too, because an unverifiable claim is the same lie.
//
// D5 — STEERING IS THE SECURITY SEAM, NOT A BACK DOOR. All seven steering tools
// are PolicyDeferDiscover in catalog.go, so since Phase 8d selecting one in
// `tools:` is a hard compile error with NO override. A referenced workflow must
// therefore never quietly grant what `tools:` cannot. Each steering feature takes
// exactly one of two honest routes, chosen per feature (and, where the truth is
// value-dependent, per VALUE — the same discipline as scheduleFacts):
//
//   - ROUTE (a) REJECT AT COMPILE TIME, naming the capability and its class, for
//     any feature that would require LOOP AUTHORITY OVER ANOTHER AGENT or would
//     call a model with a prompt the plan cannot hash. That is the steering.*
//     family's exact contract.
//   - ROUTE (b) RECORD A REQUIRED RUNTIME BINDING (Phase 9b's mechanism, plan.md
//     §3.3) for a feature that is ENGINE-SIDE: it offers no tool to any model,
//     grants no capability to any agent, and a conforming host could supply it.
//     Compilation succeeds; Phase 9d preflight must refuse to run without it.
//
// It is never route (c) "silently accepted as active". The per-feature choices are
// in gateWorkflowSteering / gateWorkflowGroupSteering, each with its reason inline.
//
// The same seam covers the two non-steering widening paths a workflow file has:
// `groups[].agents[].tools` (runtime tool names, gated through the catalog and
// the plan's already-resolved selection) and `entry_hooks`/`exit_hooks` (hook ids,
// gated against the plan's declared `hooks:`).

// --- workflow-specific runtime bindings ------------------------------------

// The Phase 9b RuntimeBindingKind vocabulary is EXTENDED here rather than forked:
// these are values of the same type, reported through the same
// Plan.RequiredRuntimeBindings(), and consumed by the same Phase 9d preflight.
// They are declared in this file (not schedules.go) because they are this
// slice's facts.
const (
	// BindingWorkflowEngine: the ability to RUN a declared workflow at all.
	BindingWorkflowEngine RuntimeBindingKind = "workflow.engine"
	// BindingWorkflowSteeringController: engine-side evaluation of rule-based
	// steering (conditions + loop actions) with no model-facing tool.
	BindingWorkflowSteeringController RuntimeBindingKind = "workflow.steeringController"
	// BindingWorkflowPlanValidator: a deterministic, prompt-free plan-validation
	// gate between groups.
	BindingWorkflowPlanValidator RuntimeBindingKind = "workflow.planValidator"
	// BindingWorkflowOutputSynthesizer: combining a group's agent outputs by a
	// declared synthesis/conflict strategy.
	BindingWorkflowOutputSynthesizer RuntimeBindingKind = "workflow.outputSynthesizer"
	// BindingWorkflowParameterProvider: supplying declared workflow parameters
	// WITHOUT asking a human, so an unattended run cannot silently need a TUI.
	BindingWorkflowParameterProvider RuntimeBindingKind = "workflow.parameterProvider"
	// BindingWorkflowHumanInterventionBroker: reaching a human mid-run for a
	// workflow that declares config.allow_human_intervention.
	BindingWorkflowHumanInterventionBroker RuntimeBindingKind = "workflow.humanInterventionBroker"
)

// workflowBindingOrder is the canonical reporting order for this slice's
// bindings. It is appended AFTER runtimeBindingOrder (see allBindingOrder) so a
// plan that declares no workflows reports byte-identical bindings to pre-9c.
var workflowBindingOrder = []RuntimeBindingKind{
	BindingWorkflowEngine,
	BindingWorkflowSteeringController,
	BindingWorkflowPlanValidator,
	BindingWorkflowOutputSynthesizer,
	BindingWorkflowParameterProvider,
	BindingWorkflowHumanInterventionBroker,
}

// allBindingOrder is the single canonical order over BOTH vocabularies. It is
// built by copying, never by appending to runtimeBindingOrder in place, so the
// Phase 9b global cannot be mutated by initialization order.
var allBindingOrder = func() []RuntimeBindingKind {
	out := make([]RuntimeBindingKind, 0, len(runtimeBindingOrder)+len(workflowBindingOrder))
	out = append(out, runtimeBindingOrder...)
	out = append(out, workflowBindingOrder...)
	return out
}()

// workflowBindingReasons states, per binding, WHAT IS MISSING TODAY, in the same
// operator-facing voice as runtimeBindingReasons.
var workflowBindingReasons = map[RuntimeBindingKind]string{
	BindingWorkflowEngine: "the harness package deliberately does not import internal/mode (the only in-tree workflow engine, internal/mode/workflow.go: " +
		"WorkflowEngine/Plan/Execute), so a compiled plan can NAME and PIN a workflow but cannot itself run one; a host must supply an engine",
	BindingWorkflowSteeringController: "rule-based steering is evaluated by the workflow engine, which this plan cannot construct; nothing in the harness " +
		"evaluates a steering condition or applies a steering action, so a declared rule changes nothing until a host supplies the controller",
	BindingWorkflowPlanValidator: "a between-group validation gate is engine-side (internal/mode group steering validate_plan) and has no harness counterpart; " +
		"without a host validator the declared gate would pass silently",
	BindingWorkflowOutputSynthesizer: "combining a group's outputs by a declared strategy is engine-side (internal/mode OutputStrategy/GroupSteeringConfig); " +
		"the harness resolves no group and combines no output, so the strategy is inert until a host supplies the synthesizer",
	BindingWorkflowParameterProvider: "a workflow parameter is a QUESTION (internal/mode yamlWorkflowParameter.question) and the in-tree path asks a human; " +
		"an unattended run therefore needs a non-interactive parameter provider, or it silently requires a TUI",
	BindingWorkflowHumanInterventionBroker: "config.allow_human_intervention lets the workflow pause for a human, and a broker reachable without a TTY is a " +
		"host binding the plan cannot supply (the catalog states the same rule for interactive.ask_user_question, catalog.go:188)",
}

// bindingReason resolves a reason across BOTH binding vocabularies, so a merged
// requirement list never reports an empty reason.
func bindingReason(kind RuntimeBindingKind) string {
	if r, ok := runtimeBindingReasons[kind]; ok {
		return r
	}
	return workflowBindingReasons[kind]
}

// --- the harness-facing declaration (camelCase, strict-decoded) -------------

// WorkflowEntry is one declared workflow REFERENCE. It has exactly three keys and
// deliberately no room for graph structure: id names the workflow inside the
// harness (it is what a schedules[].target of kind `workflow` resolves against),
// file is the manifest-relative reference, and expectedVersion is the pinned
// version enforced against the file's own `version` (D3).
type WorkflowEntry struct {
	ID              string `json:"id"`
	File            string `json:"file"`
	ExpectedVersion string `json:"expectedVersion"`
}

// --- the resolved, redacted, camelCase plan surface ------------------------

// WorkflowSpec is one resolved workflow carried on the immutable Plan. Every
// field is a non-secret id, reference, hash, version, enum value, or count: no
// system prompt body, no prompt template, no metadata VALUE, and no credential
// from the workflow file can reach it (see the wf* mirror mapping).
type WorkflowSpec struct {
	// ID is the harness-side id declared in `workflows[].id`.
	ID string `json:"id"`
	// File is the manifest-relative, normalized display label of the reference.
	File string `json:"file"`
	// ContentHash is "sha256:<hex>" of the referenced file's bytes (D3).
	ContentHash string `json:"contentHash"`
	// Version is the file's own declared version, verified equal to the entry's
	// expectedVersion.
	Version string `json:"version"`
	// Definition is the adapted, camelCase view of the referenced workflow.
	Definition WorkflowDefinitionSpec `json:"definition"`
	// Bindings are the runtime bindings this workflow requires, deduplicated and
	// ordered by allBindingOrder.
	Bindings []RuntimeBindingKind `json:"bindings,omitempty"`
	// DroppedFields records, VISIBLY, every workflow-file field this adapter
	// deliberately did not carry, with the reason. D2 forbids silently retaining
	// an unmapped field; this is the other half of that rule — an unmapped field
	// is either an error or an audited, reported drop.
	DroppedFields []WorkflowDroppedField `json:"droppedFields,omitempty"`
}

// WorkflowDroppedField is one deliberately-dropped workflow-file field.
type WorkflowDroppedField struct {
	// Field is the snake_case path in the WORKFLOW file (not the harness
	// manifest), so a reader can find it in the referenced artifact.
	Field string `json:"field"`
	// Reason states why it is not carried.
	Reason string `json:"reason"`
}

// WorkflowDefinitionSpec is the adapted workflow definition.
type WorkflowDefinitionSpec struct {
	ID           string                   `json:"id,omitempty"`
	Name         string                   `json:"name"`
	Config       WorkflowConfigSpec       `json:"config"`
	Groups       []WorkflowGroupSpec      `json:"groups"`
	Transitions  []WorkflowTransitionSpec `json:"transitions,omitempty"`
	EntryHooks   []string                 `json:"entryHooks,omitempty"`
	ExitHooks    []string                 `json:"exitHooks,omitempty"`
	Steering     *WorkflowSteeringSpec    `json:"steering,omitempty"`
	Parameters   []WorkflowParameterSpec  `json:"parameters,omitempty"`
	MetadataKeys []string                 `json:"metadataKeys,omitempty"`
}

// WorkflowConfigSpec is the adapted `config:` block.
type WorkflowConfigSpec struct {
	MaxDuration            string   `json:"maxDuration,omitempty"`
	AllowHumanIntervention *bool    `json:"allowHumanIntervention,omitempty"`
	FailOnSteeringBlock    *bool    `json:"failOnSteeringBlock,omitempty"`
	MaxRetries             *int     `json:"maxRetries,omitempty"`
	TimeoutBehavior        string   `json:"timeoutBehavior,omitempty"`
	CustomKeys             []string `json:"customKeys,omitempty"`
}

// WorkflowGroupSpec is the adapted group. Note what is NOT here: no execution
// plan, no wave assignment, no resolved agent object.
type WorkflowGroupSpec struct {
	ID             string                     `json:"id"`
	Name           string                     `json:"name"`
	Execution      string                     `json:"execution"`
	Agents         []WorkflowAgentSpec        `json:"agents"`
	DependsOn      []string                   `json:"dependsOn,omitempty"`
	Completion     WorkflowCompletionSpec     `json:"completion"`
	Timeout        string                     `json:"timeout,omitempty"`
	Steering       *WorkflowGroupSteeringSpec `json:"steering,omitempty"`
	Coordinator    *WorkflowAgentSpec         `json:"coordinator,omitempty"`
	OutputStrategy string                     `json:"outputStrategy,omitempty"`
	MetadataKeys   []string                   `json:"metadataKeys,omitempty"`
}

// WorkflowAgentSpec is the adapted agent definition. Prompt bodies are described
// by hash + byte count ONLY, exactly like Plan.promptHash/promptBytes, and
// prompt_variables/provider_config map values are reduced to sorted KEY LISTS.
type WorkflowAgentSpec struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Tools are the runtime tool names AS DECLARED in the workflow file.
	Tools []string `json:"tools,omitempty"`
	// ToolCapabilities are the stable catalog capability ids those names
	// resolved to. Reporting both is what makes the D5 gate auditable: a reader
	// can see the declared name and the capability it was checked as.
	ToolCapabilities          []string                       `json:"toolCapabilities,omitempty"`
	SystemPromptHash          string                         `json:"systemPromptHash,omitempty"`
	SystemPromptBytes         int                            `json:"systemPromptBytes,omitempty"`
	SystemPromptTemplateHash  string                         `json:"systemPromptTemplateHash,omitempty"`
	SystemPromptTemplateBytes int                            `json:"systemPromptTemplateBytes,omitempty"`
	PromptVariableKeys        []string                       `json:"promptVariableKeys,omitempty"`
	ProviderConfig            *WorkflowProviderConfigSpec    `json:"providerConfig,omitempty"`
	ContextSources            []string                       `json:"contextSources,omitempty"`
	Capabilities              *WorkflowAgentCapabilitiesSpec `json:"capabilities,omitempty"`
}

// WorkflowProviderConfigSpec is the adapted provider_config (internal/agent
// ProviderHints). The three cache-control maps and `custom` are carried as sorted
// key lists: their VALUES are arbitrary provider payload and are exactly where a
// credential would hide.
type WorkflowProviderConfigSpec struct {
	ThinkingEnabled         bool     `json:"thinkingEnabled,omitempty"`
	ThinkingBudget          int      `json:"thinkingBudget,omitempty"`
	SystemCacheControlKeys  []string `json:"systemCacheControlKeys,omitempty"`
	ToolCacheControlKeys    []string `json:"toolCacheControlKeys,omitempty"`
	MessageCacheControlKeys []string `json:"messageCacheControlKeys,omitempty"`
	CustomKeys              []string `json:"customKeys,omitempty"`
}

// WorkflowAgentCapabilitiesSpec is the adapted per-agent capabilities block.
// Pointers are preserved so "unset" stays distinguishable from "zero".
type WorkflowAgentCapabilitiesSpec struct {
	MaxTokens      *int     `json:"maxTokens,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
	MaxTurns       *int     `json:"maxTurns,omitempty"`
	TimeoutSeconds *int     `json:"timeoutSeconds,omitempty"`
	Streaming      *bool    `json:"streaming,omitempty"`
	CachePrompts   *bool    `json:"cachePrompts,omitempty"`
	ParallelTools  *bool    `json:"parallelTools,omitempty"`
}

// WorkflowCompletionSpec is the adapted completion criteria.
type WorkflowCompletionSpec struct {
	Type          string   `json:"type,omitempty"`
	Threshold     *float64 `json:"threshold,omitempty"`
	MinAgents     *int     `json:"minAgents,omitempty"`
	MaxFailures   *int     `json:"maxFailures,omitempty"`
	RequireOutput *bool    `json:"requireOutput,omitempty"`
}

// WorkflowGroupSteeringSpec is the adapted group steering block. CustomKeys
// exists for schema completeness and is always empty in a compiled plan: a
// non-empty group steering `custom` is rejected (D5, route (a)).
type WorkflowGroupSteeringSpec struct {
	ValidatePlan       *WorkflowValidationSpec `json:"validatePlan,omitempty"`
	SynthesisStrategy  string                  `json:"synthesisStrategy,omitempty"`
	ConflictResolution string                  `json:"conflictResolution,omitempty"`
	CustomKeys         []string                `json:"customKeys,omitempty"`
}

// WorkflowValidationSpec is the adapted validate_plan config. The prompt is
// described by hash + bytes only; a compiled plan never carries a prompt body.
type WorkflowValidationSpec struct {
	Type          string   `json:"type"`
	PromptHash    string   `json:"promptHash,omitempty"`
	PromptBytes   int      `json:"promptBytes,omitempty"`
	Rules         []string `json:"rules,omitempty"`
	MinConfidence float64  `json:"minConfidence,omitempty"`
}

// WorkflowTransitionSpec is the adapted transition. Condition is carried
// verbatim: it is an author-written expression over group state (for example
// "confidence > 0.8"), which is the audited part of a transition.
type WorkflowTransitionSpec struct {
	From         string   `json:"from"`
	To           string   `json:"to"`
	Condition    string   `json:"condition"`
	MaxRetries   int      `json:"maxRetries,omitempty"`
	Priority     int      `json:"priority,omitempty"`
	MetadataKeys []string `json:"metadataKeys,omitempty"`
}

// WorkflowSteeringSpec is the adapted workflow-level steering block. There is
// deliberately NO llmMetaAgent counterpart: its presence is a compile error
// (D5, route (a)), so the plan surface has nowhere to record one.
type WorkflowSteeringSpec struct {
	Type       string                     `json:"type"`
	Rules      []WorkflowSteeringRuleSpec `json:"rules,omitempty"`
	CustomKeys []string                   `json:"customKeys,omitempty"`
}

// WorkflowSteeringRuleSpec is one adapted steering rule. Action is carried
// because it is precisely what the D5 gate checks against the catalog.
type WorkflowSteeringRuleSpec struct {
	ID            string   `json:"id"`
	Condition     string   `json:"condition"`
	Action        string   `json:"action"`
	Priority      int      `json:"priority,omitempty"`
	ParameterKeys []string `json:"parameterKeys,omitempty"`
}

// WorkflowParameterSpec is one adapted workflow parameter. The question is a
// human-facing prompt string, so it is hashed rather than carried.
type WorkflowParameterSpec struct {
	ID           string                           `json:"id"`
	Type         string                           `json:"type"`
	Required     bool                             `json:"required,omitempty"`
	HasDefault   bool                             `json:"hasDefault,omitempty"`
	Choices      []string                         `json:"choices,omitempty"`
	QuestionHash string                           `json:"questionHash,omitempty"`
	Validation   *WorkflowParameterValidationSpec `json:"validation,omitempty"`
	MetadataKeys []string                         `json:"metadataKeys,omitempty"`
}

// WorkflowParameterValidationSpec is the adapted parameter validation block.
type WorkflowParameterValidationSpec struct {
	MinLength int     `json:"minLength,omitempty"`
	MaxLength int     `json:"maxLength,omitempty"`
	Pattern   string  `json:"pattern,omitempty"`
	Min       float64 `json:"min,omitempty"`
	Max       float64 `json:"max,omitempty"`
	Step      float64 `json:"step,omitempty"`
}

// --- the PRIVATE snake_case mirror of internal/mode's YAML DTOs -------------
//
// These types exist for ONE purpose: reading the referenced workflow file. They
// are unexported, they never appear on the Plan or in Explain, and they are the
// only place a snake_case workflow key is spelled. Field-for-field they mirror
// internal/mode/yaml.go (yamlModeDefinition & friends); TestWorkflowMirrorCovers
// ModeYAMLDTOs parses that file and fails loudly if it gains a key this mirror
// does not declare, so the adapter cannot silently go stale.
//
// Map-shaped fields are `map[string]json.RawMessage`, NOT `map[string]any`: the
// keys are what the plan carries, and typing the value as RawMessage means no
// decoded arbitrary value ever exists to be accidentally forwarded.

type wfDefinitionFile struct {
	ID          string                     `json:"id,omitempty"`
	Name        string                     `json:"name,omitempty"`
	Description string                     `json:"description,omitempty"`
	Version     string                     `json:"version,omitempty"`
	Config      wfConfig                   `json:"config,omitempty"`
	Groups      []wfGroup                  `json:"groups,omitempty"`
	Transitions []wfTransition             `json:"transitions,omitempty"`
	EntryHooks  []string                   `json:"entry_hooks,omitempty"`
	ExitHooks   []string                   `json:"exit_hooks,omitempty"`
	Steering    *wfSteering                `json:"steering,omitempty"`
	Parameters  []wfParameter              `json:"parameters,omitempty"`
	Metadata    map[string]json.RawMessage `json:"metadata,omitempty"`
}

type wfConfig struct {
	MaxDuration            string                     `json:"max_duration,omitempty"`
	AllowHumanIntervention *bool                      `json:"allow_human_intervention,omitempty"`
	FailOnSteeringBlock    *bool                      `json:"fail_on_steering_block,omitempty"`
	MaxRetries             *int                       `json:"max_retries,omitempty"`
	TimeoutBehavior        string                     `json:"timeout_behavior,omitempty"`
	Custom                 map[string]json.RawMessage `json:"custom,omitempty"`
}

type wfGroup struct {
	ID             string                     `json:"id,omitempty"`
	Name           string                     `json:"name,omitempty"`
	Description    string                     `json:"description,omitempty"`
	Execution      string                     `json:"execution,omitempty"`
	Agents         []wfAgent                  `json:"agents,omitempty"`
	DependsOn      []string                   `json:"depends_on,omitempty"`
	Completion     wfCompletion               `json:"completion,omitempty"`
	Timeout        string                     `json:"timeout,omitempty"`
	Steering       *wfGroupSteering           `json:"steering,omitempty"`
	Coordinator    *wfAgent                   `json:"coordinator,omitempty"`
	OutputStrategy string                     `json:"output_strategy,omitempty"`
	Metadata       map[string]json.RawMessage `json:"metadata,omitempty"`
}

type wfAgent struct {
	ID                   string                     `json:"id,omitempty"`
	Name                 string                     `json:"name,omitempty"`
	Description          string                     `json:"description,omitempty"`
	Provider             string                     `json:"provider,omitempty"`
	Model                string                     `json:"model,omitempty"`
	Tools                []string                   `json:"tools,omitempty"`
	SystemPrompt         string                     `json:"system_prompt,omitempty"`
	SystemPromptTemplate string                     `json:"system_prompt_template,omitempty"`
	PromptVariables      map[string]json.RawMessage `json:"prompt_variables,omitempty"`
	ProviderConfig       *wfProviderHints           `json:"provider_config,omitempty"`
	ContextSources       []string                   `json:"context_sources,omitempty"`
	Capabilities         *wfCapabilities            `json:"capabilities,omitempty"`
}

// wfProviderHints mirrors internal/agent.ProviderHints, which yamlAgentDefinition
// embeds by type. It is mirrored here (rather than imported) because harness
// production code must not import internal/*; the drift guard covers it too.
type wfProviderHints struct {
	ThinkingEnabled     bool                       `json:"thinking_enabled,omitempty"`
	ThinkingBudget      int                        `json:"thinking_budget,omitempty"`
	SystemCacheControl  map[string]json.RawMessage `json:"system_cache_control,omitempty"`
	ToolCacheControl    map[string]json.RawMessage `json:"tool_cache_control,omitempty"`
	MessageCacheControl map[string]json.RawMessage `json:"message_cache_control,omitempty"`
	Custom              map[string]json.RawMessage `json:"custom,omitempty"`
}

type wfCapabilities struct {
	MaxTokens      *int     `json:"max_tokens,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
	MaxTurns       *int     `json:"max_turns,omitempty"`
	TimeoutSeconds *int     `json:"timeout_seconds,omitempty"`
	Streaming      *bool    `json:"streaming,omitempty"`
	CachePrompts   *bool    `json:"cache_prompts,omitempty"`
	ParallelTools  *bool    `json:"parallel_tools,omitempty"`
}

type wfCompletion struct {
	Type          string   `json:"type,omitempty"`
	Threshold     *float64 `json:"threshold,omitempty"`
	MinAgents     *int     `json:"min_agents,omitempty"`
	MaxFailures   *int     `json:"max_failures,omitempty"`
	RequireOutput *bool    `json:"require_output,omitempty"`
}

type wfGroupSteering struct {
	ValidatePlan       *wfValidation              `json:"validate_plan,omitempty"`
	SynthesisStrategy  string                     `json:"synthesis_strategy,omitempty"`
	ConflictResolution string                     `json:"conflict_resolution,omitempty"`
	Custom             map[string]json.RawMessage `json:"custom,omitempty"`
}

type wfValidation struct {
	Type          string   `json:"type,omitempty"`
	Prompt        string   `json:"prompt,omitempty"`
	Rules         []string `json:"rules,omitempty"`
	MinConfidence float64  `json:"min_confidence,omitempty"`
}

type wfTransition struct {
	From       string                     `json:"from,omitempty"`
	To         string                     `json:"to,omitempty"`
	Condition  string                     `json:"condition,omitempty"`
	MaxRetries int                        `json:"max_retries,omitempty"`
	Priority   int                        `json:"priority,omitempty"`
	Metadata   map[string]json.RawMessage `json:"metadata,omitempty"`
}

type wfSteering struct {
	Type         string                     `json:"type,omitempty"`
	LLMMetaAgent *wfAgent                   `json:"llm_meta_agent,omitempty"`
	Rules        []wfSteeringRule           `json:"rules,omitempty"`
	Custom       map[string]json.RawMessage `json:"custom,omitempty"`
}

type wfSteeringRule struct {
	ID         string                     `json:"id,omitempty"`
	Condition  string                     `json:"condition,omitempty"`
	Action     string                     `json:"action,omitempty"`
	Priority   int                        `json:"priority,omitempty"`
	Parameters map[string]json.RawMessage `json:"parameters,omitempty"`
}

type wfParameter struct {
	ID          string                     `json:"id,omitempty"`
	Question    string                     `json:"question,omitempty"`
	Description string                     `json:"description,omitempty"`
	Type        string                     `json:"type,omitempty"`
	Required    bool                       `json:"required,omitempty"`
	Default     json.RawMessage            `json:"default,omitempty"`
	Choices     []string                   `json:"choices,omitempty"`
	Validation  *wfParameterValidation     `json:"validation,omitempty"`
	Metadata    map[string]json.RawMessage `json:"metadata,omitempty"`
}

type wfParameterValidation struct {
	MinLength int     `json:"min_length,omitempty"`
	MaxLength int     `json:"max_length,omitempty"`
	Pattern   string  `json:"pattern,omitempty"`
	Min       float64 `json:"min,omitempty"`
	Max       float64 `json:"max,omitempty"`
	Step      float64 `json:"step,omitempty"`
}

// --- closed enum vocabularies, transcribed from internal/mode --------------
//
// Each set is copied from the in-tree validator so the harness rejects exactly
// what the engine would reject, at COMPILE time instead of at run time. The
// file:line citation is the re-verification path.
var (
	// internal/mode/group.go:63-74 (ExecutionStrategy), validated at :136.
	wfExecutionStrategies = []string{"parallel", "sequential", "adversarial"}
	// internal/mode/group.go:83-91 (OutputStrategy).
	wfOutputStrategies = []string{"raw", "synthesize", "first"}
	// internal/mode/group.go:209-215 (CompletionCriteria.Type).
	wfCompletionTypes = []string{"all", "first", "majority", "consensus", "quality"}
	// internal/mode/mode.go:254-258 (validTimeoutBehaviors).
	wfTimeoutBehaviors = []string{"fail", "partial", "continue"}
	// internal/mode/mode.go:338-340 (SteeringConfig.Type).
	wfSteeringTypes = []string{"none", "rule-based", "llm-based", "hybrid"}
	// internal/mode/group.go:305-307 (GroupSteeringConfig.SynthesisStrategy).
	wfSynthesisStrategies = []string{"voting", "llm-synthesis", "first-wins", "best-of-n"}
	// internal/mode/group.go:309-311 (GroupSteeringConfig.ConflictResolution).
	wfConflictResolutions = []string{"majority", "weighted", "llm-decide"}
	// internal/mode/yaml.go:33 (yamlWorkflowParameter.Type).
	wfParameterTypes = []string{"text", "number", "choice", "multi_choice", "confirm"}
)

// inSet reports membership in a closed vocabulary.
func inSet(v string, set []string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// setList renders a vocabulary for a diagnostic.
func setList(set []string) string {
	quoted := make([]string, 0, len(set))
	for _, s := range set {
		quoted = append(quoted, quote(s))
	}
	return strings.Join(quoted, ", ")
}

// --- the audited drop list --------------------------------------------------

// workflowDroppedReasons is the CLOSED list of workflow-file fields this adapter
// deliberately does not carry, each with its reason. A drop is recorded on the
// WorkflowSpec only when the field is actually PRESENT in the referenced file, so
// the report describes the real artifact rather than the schema. Any workflow-file
// key that is neither mapped, nor in this list, nor rejected is impossible: an
// unknown key fails strict decode.
var workflowDroppedReasons = map[string]string{
	"description":                              "free prose with no bearing on identity or posture; the content hash already pins it",
	"groups[].description":                     "free prose with no bearing on identity or posture",
	"groups[].agents[].description":            "free prose with no bearing on identity or posture",
	"parameters[].description":                 "free prose with no bearing on identity or posture",
	"parameters[].default":                     "an arbitrary default VALUE may carry secret material; only its presence is carried (hasDefault)",
	"metadata{}":                               "arbitrary metadata VALUES may carry secret material; only sorted keys are carried",
	"config.custom{}":                          "arbitrary custom VALUES may carry secret material; only sorted keys are carried",
	"groups[].metadata{}":                      "arbitrary metadata VALUES may carry secret material; only sorted keys are carried",
	"groups[].agents[].prompt_variables{}":     "prompt variable VALUES are substituted into a prompt body; only sorted keys are carried",
	"groups[].agents[].provider_config{}":      "provider payload VALUES (cache-control blocks, custom keys) are exactly where credential material would hide; only sorted keys are carried",
	"transitions[].metadata{}":                 "arbitrary metadata VALUES may carry secret material; only sorted keys are carried",
	"steering.rules[].parameters{}":            "a steering action's parameter VALUES can contain injected instruction text; only sorted keys are carried",
	"parameters[].metadata{}":                  "arbitrary metadata VALUES may carry secret material; only sorted keys are carried",
	"groups[].agents[].system_prompt":          "a system prompt BODY never enters a compiled plan; it is carried as systemPromptHash + systemPromptBytes",
	"groups[].agents[].system_prompt_template": "a prompt TEMPLATE never enters a compiled plan; it is carried as systemPromptTemplateHash + systemPromptTemplateBytes",
	"groups[].steering.validate_plan.prompt":   "a validation prompt BODY never enters a compiled plan; it is carried as promptHash + promptBytes",
	"parameters[].question":                    "a human-facing question string is prompt text; it is carried as questionHash",
}

// wfDrops accumulates deduplicated drop records during one adaptation.
type wfDrops struct {
	seen map[string]struct{}
}

func newWfDrops() *wfDrops { return &wfDrops{seen: map[string]struct{}{}} }

// note records that a documented drop actually occurred. An undocumented field
// name is a programming error and is recorded with an explicit marker rather than
// silently ignored, so a missing reason shows up in the report and in tests.
func (d *wfDrops) note(field string, present bool) {
	if !present {
		return
	}
	d.seen[field] = struct{}{}
}

func (d *wfDrops) list() []WorkflowDroppedField {
	if len(d.seen) == 0 {
		return nil
	}
	fields := make([]string, 0, len(d.seen))
	for f := range d.seen {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	out := make([]WorkflowDroppedField, 0, len(fields))
	for _, f := range fields {
		reason, ok := workflowDroppedReasons[f]
		if !ok {
			reason = "UNDOCUMENTED DROP: no reason is registered in workflowDroppedReasons for this field"
		}
		out = append(out, WorkflowDroppedField{Field: f, Reason: reason})
	}
	return out
}

// sortedRawKeys returns the sorted key list of a mirror map WITHOUT touching a
// single value. This is the only way a map-shaped workflow field reaches a Plan.
func sortedRawKeys(m map[string]json.RawMessage) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- reading the referenced file -------------------------------------------

// decodeWorkflowFile converts the referenced workflow manifest to canonical JSON
// and strict-decodes it into the PRIVATE mirror. It mirrors strictDecode's rules
// (single document, unknown keys rejected, no trailing data) but writes into the
// mirror, never into Document: the two key spaces never meet.
func decodeWorkflowFile(sourcePath, fieldPath, filePath string, raw []byte) (*wfDefinitionFile, *Diagnostic) {
	format := configformat.FormatForPath(filePath)
	if format == configformat.FormatYAML && yamlHasMultipleDocuments(raw) {
		d := newDiag("harness.workflows.file.multipleDocuments", fieldPath,
			"referenced workflow file contains more than one YAML document; it must contain exactly one", sourcePath)
		return nil, &d
	}
	jsonBytes, err := configformat.ToJSON(raw, format)
	if err != nil {
		d := newDiag("harness.workflows.file.syntax", fieldPath,
			"referenced workflow file is not valid "+formatName(format), sourcePath)
		return nil, &d
	}
	dec := json.NewDecoder(bytes.NewReader(jsonBytes))
	dec.DisallowUnknownFields()
	var f wfDefinitionFile
	if err := dec.Decode(&f); err != nil {
		d := workflowDecodeError(sourcePath, fieldPath, err)
		return nil, &d
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		d := newDiag("harness.workflows.file.trailingData", fieldPath,
			"unexpected trailing data after the workflow document", sourcePath)
		return nil, &d
	}
	return &f, nil
}

// workflowDecodeError maps a decode failure onto the workflows namespace. An
// unknown key gets its own code and names the key, because that is the D2
// diagnostic: a workflow semantic the harness cannot see must not pass silently.
func workflowDecodeError(sourcePath, fieldPath string, err error) Diagnostic {
	msg := err.Error()
	if i := strings.Index(msg, "unknown field "); i >= 0 {
		key := strings.Trim(msg[i+len("unknown field "):], "\"")
		return newDiag("harness.workflows.file.unknownField", fieldPath,
			"referenced workflow file contains unknown key "+quote(key)+
				"; the harness adapter maps a CLOSED schema (internal/mode's YAML DTOs) and refuses to accept a key it cannot classify, "+
				"because a silently ignored workflow key is a semantic the plan would claim to have adapted but has not", sourcePath)
	}
	return newDiag("harness.workflows.file.invalid", fieldPath,
		"referenced workflow file could not be decoded into a workflow definition", sourcePath)
}

// --- D5: the capability seam ------------------------------------------------

// workflowEnv carries the document-scoped facts a workflow is gated against,
// resolved once and passed by value so every workflow entry is gated identically.
type workflowEnv struct {
	// selected is the plan's EFFECTIVE capability set: the primary agent's
	// resolved tools UNION every subagent's resolved tools. It is the exact set
	// that already survived the Phase 8d policy gate, so a workflow can be
	// checked against what the document is genuinely allowed to do rather than
	// against a re-derived opinion.
	selected map[string]struct{}
	// hookIDs is the plan's declared `hooks:` id set, which a workflow's
	// entry_hooks/exit_hooks must be a subset of.
	hookIDs map[string]struct{}
	// resolvable is false when an earlier section already failed. The id/tool
	// sets are then incomplete, and checking references against them would turn
	// one real error into a cascade of misleading "widens" diagnostics — the same
	// discipline as scheduleEnv.targetsResolvable and checkAcknowledgementsUsed.
	// Structural rules (enums, cycles, required fields) still run.
	resolvable bool
}

// capabilityAliasIndex maps a RUNTIME tool name (the names a workflow file's
// `tools:` list actually uses, e.g. "Read") to every catalog capability that
// claims it. It is derived from the catalog table, never hand-maintained, and
// deliberately keeps COLLISIONS as a list: catalog.go documents that aliases may
// collide across capabilities, and selection by alias must therefore be gated
// against ALL candidates rather than guessing one.
var capabilityAliasIndex = func() map[string][]Capability {
	m := make(map[string][]Capability)
	for _, c := range catalog {
		for _, a := range c.RuntimeAliases {
			m[a] = append(m[a], c)
		}
	}
	return m
}()

// resolveWorkflowToolName resolves one declared workflow tool name to the catalog
// candidates it must be gated as. An exact stable id wins; otherwise the runtime
// alias index is consulted. ok is false when the name is in neither.
func resolveWorkflowToolName(name string) (cands []Capability, ok bool) {
	if c, known := LookupCapability(name); known {
		return []Capability{c}, true
	}
	if cs, known := capabilityAliasIndex[name]; known {
		out := make([]Capability, len(cs))
		copy(out, cs)
		sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return out, true
	}
	return nil, false
}

// gateWorkflowAgentTools is the D5 gate for `groups[].agents[].tools` (and a
// coordinator's / a meta-agent's tools). A workflow agent's tool list is a
// CAPABILITY GRANT, so it is held to exactly the rules `tools:` is held to:
//
//   - a name in neither the catalog nor the alias index is REJECTED (the harness
//     cannot classify it, so it cannot allow it);
//   - a DEFER-DISCOVER capability is REJECTED with its class named, mirroring
//     checkCapabilityPolicy's D1 rule and its "no override exists" wording — this
//     is the branch that stops all seven steering tools entering through a
//     workflow file;
//   - a capability the plan has NOT already selected is REJECTED as WIDENING: a
//     referenced file must never enlarge the effective capability set beyond what
//     `tools:`/`permissions:` already audited.
//
// When a name is an ambiguous alias, EVERY candidate must pass; failing closed on
// the whole set is the only answer that does not guess which schema was meant.
func gateWorkflowAgentTools(sourcePath, field string, names []string, env workflowEnv) ([]string, Diagnostics) {
	if len(names) == 0 {
		return nil, nil
	}
	var ds Diagnostics
	ids := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" {
			ds = append(ds, newDiag("harness.workflows.agents.tools.empty", field+".tools",
				"workflow agent tool name is empty", sourcePath))
			continue
		}
		cands, known := resolveWorkflowToolName(name)
		if !known {
			ds = append(ds, newDiag("harness.workflows.agents.tools.unknown", field+".tools",
				"workflow agent tool "+quote(name)+" is not a capability id and not a known runtime alias in the capability catalog; "+
					"the harness cannot classify it, so it cannot be adapted as active", sourcePath))
			continue
		}
		for _, c := range cands {
			if c.Class == PolicyDeferDiscover {
				ds = append(ds, newDiag("harness.workflows.agents.tools.deferDiscover", field+".tools",
					"workflow agent tool "+quote(name)+" resolves to capability "+quote(c.ID)+", whose policy class is "+
						string(PolicyDeferDiscover)+" ("+c.SideEffect+"): it cannot be selected in agent.tools and it cannot be granted "+
						"through a referenced workflow either. There is no acknowledgement or override; it must first be reclassified in the "+
						"capability catalog", sourcePath))
				continue
			}
			if !env.resolvable {
				continue
			}
			if _, ok := env.selected[c.ID]; !ok {
				ds = append(ds, newDiag("harness.workflows.agents.tools.widensCapabilities", field+".tools",
					"workflow agent tool "+quote(name)+" resolves to capability "+quote(c.ID)+
						", which no agent in this document selects; a referenced workflow may never widen the effective capability set beyond the "+
						"audited agent.tools/agents[].tools selections, so select it explicitly or remove it from the workflow", sourcePath))
				continue
			}
			if _, dup := seen[c.ID]; !dup {
				seen[c.ID] = struct{}{}
				ids = append(ids, c.ID)
			}
		}
	}
	sort.Strings(ids)
	return ids, ds
}

// steeringLoopCapabilities are the catalog capabilities an LLM steering
// meta-agent's authority IS. They are named in the rejection diagnostic so the
// refusal cites the exact contract it is enforcing rather than a policy opinion.
var steeringLoopCapabilities = []string{
	"steering.observe_only",
	"steering.inject_system_note",
	"steering.refocus",
	"steering.block_next_tool",
	"steering.halt_peer_loop",
}

// steeringCapabilityClassList renders "id (CLASS)" for the diagnostic.
func steeringCapabilityClassList(ids []string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		class := string(PolicyDeferDiscover)
		if c, ok := LookupCapability(id); ok {
			class = string(c.Class)
		}
		parts = append(parts, quote(id)+" ("+class+")")
	}
	return strings.Join(parts, ", ")
}

// gateWorkflowSteering applies D5 to the WORKFLOW-LEVEL `steering:` block and
// returns the adapted spec plus the bindings it requires.
//
// PER-FEATURE DECISIONS (each stated with its reason):
//
//	steering absent / type "none"  -> ACCEPTED, no binding, no capability. There
//	                                  is nothing to steer with.
//	type "rule-based"              -> ROUTE (b) BINDING workflow.steeringController.
//	                                  Rules are evaluated ENGINE-SIDE: no tool is
//	                                  offered to any model and no capability is
//	                                  granted to any agent, and a conforming host
//	                                  could supply the controller.
//	type "llm-based" / "hybrid"    -> ROUTE (a) REJECT. Both require a meta-agent
//	                                  with authority over other agents' loops,
//	                                  which IS the steering.* family — all
//	                                  DEFER-DISCOVER, unselectable with no override.
//	llm_meta_agent present         -> ROUTE (a) REJECT, same reason, plus: it
//	                                  declares a provider/model/system_prompt
//	                                  agent outside the audited agents:/profiles:
//	                                  surface, so the plan could neither hash nor
//	                                  gate it.
//	rules[].action                 -> ROUTE (a) REJECT when the action names a
//	                                  catalog capability/alias (gated exactly like
//	                                  a tool: DEFER-DISCOVER refused, unselected
//	                                  capability refused as widening). Otherwise
//	                                  it is an engine action covered by the
//	                                  controller binding.
//	custom{}                       -> ROUTE (a) REJECT when non-empty: an
//	                                  arbitrary steering payload cannot be
//	                                  classified, so there is no honest binding to
//	                                  name for it either.
func gateWorkflowSteering(sourcePath, field string, s *wfSteering, env workflowEnv, drops *wfDrops) (*WorkflowSteeringSpec, []bindingUse, Diagnostics) {
	if s == nil {
		return nil, nil, nil
	}
	f := field + ".steering"
	var ds Diagnostics
	var uses []bindingUse

	typ := s.Type
	if typ == "" {
		typ = "none"
	}
	if !inSet(typ, wfSteeringTypes) {
		ds = append(ds, newDiag("harness.workflows.steering.type.unknown", f+".type",
			"unknown steering type "+quote(s.Type)+"; valid values are "+setList(wfSteeringTypes), sourcePath))
	}
	if s.LLMMetaAgent != nil {
		ds = append(ds, newDiag("harness.workflows.steering.llmMetaAgent.forbidden", f+".llm_meta_agent",
			"steering.llm_meta_agent is refused: a meta-agent that observes peers and redirects their loops requires "+
				steeringCapabilityClassList(steeringLoopCapabilities)+", and a "+string(PolicyDeferDiscover)+
				" capability cannot be selected in agent.tools with any acknowledgement or override, so a referenced workflow must not grant it either. "+
				"It would also introduce a provider/model/system_prompt agent outside the audited agents:/profiles: sections, whose prompt this plan "+
				"could neither hash nor gate", sourcePath))
	}
	if typ == "llm-based" || typ == "hybrid" {
		ds = append(ds, newDiag("harness.workflows.steering.llmSteeringForbidden", f+".type",
			"steering type "+quote(typ)+" is refused: it steers by asking a model to intervene in other agents' loops, which requires "+
				steeringCapabilityClassList(steeringLoopCapabilities)+"; those classes are unselectable in agent.tools with no override, so this "+
				"workflow cannot be adapted as active. Use "+quote("rule-based")+" steering, or remove the steering block", sourcePath))
	}
	if len(s.Custom) > 0 {
		ds = append(ds, newDiag("harness.workflows.steering.custom.unclassifiable", f+".custom",
			"steering.custom is refused when non-empty: an arbitrary steering payload cannot be classified against the capability catalog, so it can "+
				"neither be proven safe nor described by a required runtime binding; declared keys: "+
				strings.Join(sortedRawKeys(s.Custom), ", "), sourcePath))
	}

	spec := &WorkflowSteeringSpec{Type: typ}
	if len(s.Rules) > 0 {
		spec.Rules = make([]WorkflowSteeringRuleSpec, 0, len(s.Rules))
	}
	ruleIDs := make(map[string]struct{}, len(s.Rules))
	for i, r := range s.Rules {
		rf := f + ".rules[" + strconv.Itoa(i) + "]"
		if r.ID == "" {
			ds = append(ds, newDiag("harness.workflows.steering.rules.id.missing", rf+".id",
				"steering rule requires a non-empty id", sourcePath))
			continue
		}
		if _, dup := ruleIDs[r.ID]; dup {
			ds = append(ds, newDiag("harness.workflows.steering.rules.id.duplicate", rf+".id",
				"duplicate steering rule id "+quote(r.ID), sourcePath))
			continue
		}
		ruleIDs[r.ID] = struct{}{}
		if r.Condition == "" {
			ds = append(ds, newDiag("harness.workflows.steering.rules.condition.missing", rf+".condition",
				"steering rule "+quote(r.ID)+" requires a non-empty condition; a rule with no condition would fire unconditionally", sourcePath))
		}
		if r.Action == "" {
			ds = append(ds, newDiag("harness.workflows.steering.rules.action.missing", rf+".action",
				"steering rule "+quote(r.ID)+" requires a non-empty action", sourcePath))
		} else if cands, known := resolveWorkflowToolName(r.Action); known {
			// The sharpest back door: an action that NAMES a capability.
			for _, c := range cands {
				if c.Class == PolicyDeferDiscover {
					ds = append(ds, newDiag("harness.workflows.steering.rules.action.deferDiscover", rf+".action",
						"steering rule "+quote(r.ID)+" action "+quote(r.Action)+" names capability "+quote(c.ID)+", whose policy class is "+
							string(PolicyDeferDiscover)+" ("+c.SideEffect+"): a steering rule cannot grant what agent.tools cannot select, and no "+
							"acknowledgement or override exists", sourcePath))
					continue
				}
				if env.resolvable {
					if _, ok := env.selected[c.ID]; !ok {
						ds = append(ds, newDiag("harness.workflows.steering.rules.action.widensCapabilities", rf+".action",
							"steering rule "+quote(r.ID)+" action "+quote(r.Action)+" names capability "+quote(c.ID)+
								", which no agent in this document selects; a steering rule may not widen the effective capability set", sourcePath))
					}
				}
			}
		}
		drops.note("steering.rules[].parameters{}", len(r.Parameters) > 0)
		spec.Rules = append(spec.Rules, WorkflowSteeringRuleSpec{
			ID:            r.ID,
			Condition:     r.Condition,
			Action:        r.Action,
			Priority:      r.Priority,
			ParameterKeys: sortedRawKeys(r.Parameters),
		})
	}

	// ROUTE (b): rule-based steering needs an engine-side controller. Recorded
	// even when the rule list is empty, because `type: rule-based` itself claims
	// the engine will steer.
	if typ == "rule-based" {
		uses = append(uses, bindingUse{binding: BindingWorkflowSteeringController, field: f})
	}
	if ds.HasErrors() {
		return nil, nil, ds
	}
	return spec, uses, nil
}

// gateWorkflowGroupSteering applies D5 to a GROUP-level `steering:` block.
//
// PER-FEATURE DECISIONS:
//
//	validate_plan (prompt-free)    -> ROUTE (b) BINDING workflow.planValidator. A
//	                                  deterministic rules/confidence gate is
//	                                  engine-side and grants no tool.
//	validate_plan with a prompt    -> ROUTE (a) REJECT: it is an LLM judge whose
//	                                  prompt lives outside the audited plan, and
//	                                  its verdict gates another agent's next step
//	                                  (steering.block_next_tool, DEFER-DISCOVER).
//	synthesis_strategy /           -> ROUTE (b) BINDING workflow.outputSynthesizer
//	conflict_resolution               for the deterministic values. The two
//	                                  model-driven values ("llm-synthesis",
//	                                  "llm-decide") additionally REQUIRE the group
//	                                  to declare a coordinator, whose prompt IS
//	                                  hashed into the plan; without one the plan
//	                                  would promise a model call it cannot pin.
//	custom{}                       -> ROUTE (a) REJECT when non-empty (as above).
func gateWorkflowGroupSteering(sourcePath, field string, g wfGroup, env workflowEnv, drops *wfDrops) (*WorkflowGroupSteeringSpec, []bindingUse, Diagnostics) {
	s := g.Steering
	if s == nil {
		return nil, nil, nil
	}
	f := field + ".steering"
	var ds Diagnostics
	var uses []bindingUse
	spec := &WorkflowGroupSteeringSpec{
		SynthesisStrategy:  s.SynthesisStrategy,
		ConflictResolution: s.ConflictResolution,
	}

	if len(s.Custom) > 0 {
		ds = append(ds, newDiag("harness.workflows.groups.steering.custom.unclassifiable", f+".custom",
			"group steering.custom is refused when non-empty: an arbitrary steering payload cannot be classified against the capability catalog; "+
				"declared keys: "+strings.Join(sortedRawKeys(s.Custom), ", "), sourcePath))
	}

	if vp := s.ValidatePlan; vp != nil {
		vf := f + ".validate_plan"
		if vp.Prompt != "" {
			ds = append(ds, newDiag("harness.workflows.groups.steering.validatePlan.prompt.forbidden", vf+".prompt",
				"group steering.validate_plan carries a prompt, which is refused: it is an LLM judge whose prompt is not declared in this document "+
					"(so the plan can neither audit nor pin it) and whose verdict gates another agent's next step — that authority is "+
					steeringCapabilityClassList([]string{"steering.block_next_tool"})+", unselectable in agent.tools with no override. "+
					"Use a prompt-free rules/min_confidence validation instead", sourcePath))
		}
		if vp.Type == "" {
			ds = append(ds, newDiag("harness.workflows.groups.steering.validatePlan.type.missing", vf+".type",
				"group steering.validate_plan requires a non-empty type", sourcePath))
		}
		if vp.MinConfidence < 0 || vp.MinConfidence > 1 {
			ds = append(ds, newDiag("harness.workflows.groups.steering.validatePlan.minConfidence.invalid", vf+".min_confidence",
				"group steering.validate_plan min_confidence must be within [0,1]", sourcePath))
		}
		drops.note("groups[].steering.validate_plan.prompt", vp.Prompt != "")
		spec.ValidatePlan = &WorkflowValidationSpec{
			Type:          vp.Type,
			Rules:         copyStringSlice(vp.Rules),
			MinConfidence: vp.MinConfidence,
		}
		if vp.Prompt != "" {
			spec.ValidatePlan.PromptHash = hashString(vp.Prompt)
			spec.ValidatePlan.PromptBytes = len(vp.Prompt)
		}
		uses = append(uses, bindingUse{binding: BindingWorkflowPlanValidator, field: vf})
	}

	if s.SynthesisStrategy != "" {
		if !inSet(s.SynthesisStrategy, wfSynthesisStrategies) {
			ds = append(ds, newDiag("harness.workflows.groups.steering.synthesisStrategy.unknown", f+".synthesis_strategy",
				"unknown synthesis_strategy "+quote(s.SynthesisStrategy)+"; valid values are "+setList(wfSynthesisStrategies), sourcePath))
		} else {
			uses = append(uses, bindingUse{binding: BindingWorkflowOutputSynthesizer, field: f + ".synthesis_strategy"})
			if s.SynthesisStrategy == "llm-synthesis" && g.Coordinator == nil {
				ds = append(ds, newDiag("harness.workflows.groups.steering.llmSynthesisWithoutCoordinator", f+".synthesis_strategy",
					"synthesis_strategy "+quote("llm-synthesis")+" requires the group to declare a coordinator: otherwise the workflow would call a model "+
						"with a prompt this plan never hashed, and the plan's identity would not change when that prompt changed", sourcePath))
			}
		}
	}
	if s.ConflictResolution != "" {
		if !inSet(s.ConflictResolution, wfConflictResolutions) {
			ds = append(ds, newDiag("harness.workflows.groups.steering.conflictResolution.unknown", f+".conflict_resolution",
				"unknown conflict_resolution "+quote(s.ConflictResolution)+"; valid values are "+setList(wfConflictResolutions), sourcePath))
		} else {
			uses = append(uses, bindingUse{binding: BindingWorkflowOutputSynthesizer, field: f + ".conflict_resolution"})
			if s.ConflictResolution == "llm-decide" && g.Coordinator == nil {
				ds = append(ds, newDiag("harness.workflows.groups.steering.llmDecideWithoutCoordinator", f+".conflict_resolution",
					"conflict_resolution "+quote("llm-decide")+" requires the group to declare a coordinator, for the same reason as "+
						quote("llm-synthesis")+": an unpinned prompt would decide the outcome", sourcePath))
			}
		}
	}
	if ds.HasErrors() {
		return nil, nil, ds
	}
	return spec, uses, nil
}

// --- the field-by-field adapter --------------------------------------------

// adaptWorkflowAgent maps ONE wfAgent (a group agent or a coordinator) into a
// WorkflowAgentSpec. Every mirror field is either written to a named camelCase
// field, hashed, reduced to sorted keys, or recorded as an audited drop — there is
// no branch in which a mirror value is forwarded untouched.
func adaptWorkflowAgent(sourcePath, field string, a wfAgent, env workflowEnv, drops *wfDrops) (WorkflowAgentSpec, Diagnostics) {
	var ds Diagnostics
	if a.Name == "" {
		ds = append(ds, newDiag("harness.workflows.agents.name.missing", field+".name",
			"workflow agent requires a non-empty name", sourcePath))
	}
	if a.Provider == "" {
		ds = append(ds, newDiag("harness.workflows.agents.provider.missing", field+".provider",
			"workflow agent requires a non-empty provider; an agent with no provider could never run", sourcePath))
	}
	if a.Model == "" {
		ds = append(ds, newDiag("harness.workflows.agents.model.missing", field+".model",
			"workflow agent requires a non-empty model; an agent with no model could never run", sourcePath))
	}

	toolIDs, toolDS := gateWorkflowAgentTools(sourcePath, field, a.Tools, env)
	ds = append(ds, toolDS...)

	drops.note("groups[].agents[].description", a.Description != "")
	drops.note("groups[].agents[].system_prompt", a.SystemPrompt != "")
	drops.note("groups[].agents[].system_prompt_template", a.SystemPromptTemplate != "")
	drops.note("groups[].agents[].prompt_variables{}", len(a.PromptVariables) > 0)

	spec := WorkflowAgentSpec{
		ID:                 a.ID,
		Name:               a.Name,
		Provider:           a.Provider,
		Model:              a.Model,
		Tools:              copyStringSlice(a.Tools),
		ToolCapabilities:   toolIDs,
		PromptVariableKeys: sortedRawKeys(a.PromptVariables),
		ContextSources:     copyStringSlice(a.ContextSources),
	}
	if a.SystemPrompt != "" {
		spec.SystemPromptHash = hashString(a.SystemPrompt)
		spec.SystemPromptBytes = len(a.SystemPrompt)
	}
	if a.SystemPromptTemplate != "" {
		spec.SystemPromptTemplateHash = hashString(a.SystemPromptTemplate)
		spec.SystemPromptTemplateBytes = len(a.SystemPromptTemplate)
	}
	if pc := a.ProviderConfig; pc != nil {
		drops.note("groups[].agents[].provider_config{}",
			len(pc.SystemCacheControl) > 0 || len(pc.ToolCacheControl) > 0 || len(pc.MessageCacheControl) > 0 || len(pc.Custom) > 0)
		spec.ProviderConfig = &WorkflowProviderConfigSpec{
			ThinkingEnabled:         pc.ThinkingEnabled,
			ThinkingBudget:          pc.ThinkingBudget,
			SystemCacheControlKeys:  sortedRawKeys(pc.SystemCacheControl),
			ToolCacheControlKeys:    sortedRawKeys(pc.ToolCacheControl),
			MessageCacheControlKeys: sortedRawKeys(pc.MessageCacheControl),
			CustomKeys:              sortedRawKeys(pc.Custom),
		}
	}
	if c := a.Capabilities; c != nil {
		spec.Capabilities = &WorkflowAgentCapabilitiesSpec{
			MaxTokens:      copyIntPtr(c.MaxTokens),
			Temperature:    copyFloatPtr(c.Temperature),
			MaxTurns:       copyIntPtr(c.MaxTurns),
			TimeoutSeconds: copyIntPtr(c.TimeoutSeconds),
			Streaming:      copyBoolPtr(c.Streaming),
			CachePrompts:   copyBoolPtr(c.CachePrompts),
			ParallelTools:  copyBoolPtr(c.ParallelTools),
		}
	}
	return spec, ds
}

// adaptWorkflowConfig maps the `config:` block and validates every value the
// in-tree engine validates, at compile time.
func adaptWorkflowConfig(sourcePath, field string, c wfConfig, env workflowEnv, drops *wfDrops) (WorkflowConfigSpec, []bindingUse, Diagnostics) {
	f := field + ".config"
	var ds Diagnostics
	var uses []bindingUse
	if c.MaxDuration != "" {
		if _, err := time.ParseDuration(c.MaxDuration); err != nil {
			ds = append(ds, newDiag("harness.workflows.config.maxDuration.invalid", f+".max_duration",
				"config.max_duration "+quote(c.MaxDuration)+" is not a valid Go duration (for example "+quote("2h")+" or "+quote("30m")+")", sourcePath))
		}
	}
	if c.MaxRetries != nil && *c.MaxRetries < 0 {
		ds = append(ds, newDiag("harness.workflows.config.maxRetries.invalid", f+".max_retries",
			"config.max_retries must not be negative", sourcePath))
	}
	if c.TimeoutBehavior != "" && !inSet(c.TimeoutBehavior, wfTimeoutBehaviors) {
		ds = append(ds, newDiag("harness.workflows.config.timeoutBehavior.unknown", f+".timeout_behavior",
			"unknown config.timeout_behavior "+quote(c.TimeoutBehavior)+"; valid values are "+setList(wfTimeoutBehaviors), sourcePath))
	}
	// D5-adjacent: `allow_human_intervention: true` means the run can PAUSE for a
	// human. That is the interactive.ask_user_question contract, so it is held to
	// the same rule as a tool: the capability must already be selected (else the
	// workflow would widen what the document may do), and even then the broker is
	// a host binding preflight must supply.
	if c.AllowHumanIntervention != nil && *c.AllowHumanIntervention {
		const askCap = "interactive.ask_user_question"
		if env.resolvable {
			if _, ok := env.selected[askCap]; !ok {
				ds = append(ds, newDiag("harness.workflows.config.allowHumanIntervention.widensCapabilities", f+".allow_human_intervention",
					"config.allow_human_intervention is true, so the workflow may pause and ask a human, but no agent in this document selects "+
						quote(askCap)+"; a referenced workflow may not widen the audited capability set — select it explicitly or set the flag false", sourcePath))
			}
		}
		uses = append(uses, bindingUse{binding: BindingWorkflowHumanInterventionBroker, field: f + ".allow_human_intervention"})
	}
	drops.note("config.custom{}", len(c.Custom) > 0)
	spec := WorkflowConfigSpec{
		MaxDuration:            c.MaxDuration,
		AllowHumanIntervention: copyBoolPtr(c.AllowHumanIntervention),
		FailOnSteeringBlock:    copyBoolPtr(c.FailOnSteeringBlock),
		MaxRetries:             copyIntPtr(c.MaxRetries),
		TimeoutBehavior:        c.TimeoutBehavior,
		CustomKeys:             sortedRawKeys(c.Custom),
	}
	if ds.HasErrors() {
		return WorkflowConfigSpec{}, nil, ds
	}
	return spec, uses, nil
}

// adaptWorkflowGroups maps `groups:` and enforces group-level integrity:
// non-empty unique ids, a closed execution strategy, at least one agent, closed
// completion/output enums, a parseable timeout, and depends_on references that
// exist, are not self-references, and contain no cycle.
func adaptWorkflowGroups(sourcePath, field string, groups []wfGroup, env workflowEnv, drops *wfDrops) ([]WorkflowGroupSpec, map[string]struct{}, []bindingUse, Diagnostics) {
	var ds Diagnostics
	var uses []bindingUse
	if len(groups) == 0 {
		return nil, nil, nil, Diagnostics{newDiag("harness.workflows.groups.empty", field+".groups",
			"referenced workflow declares no groups; a workflow with no groups orchestrates nothing", sourcePath)}
	}

	ids := make(map[string]struct{}, len(groups))
	order := make([]string, 0, len(groups))
	fieldOf := make(map[string]string, len(groups))
	for i, g := range groups {
		gf := field + ".groups[" + strconv.Itoa(i) + "]"
		if g.ID == "" {
			ds = append(ds, newDiag("harness.workflows.groups.id.missing", gf+".id",
				"workflow group requires a non-empty id; depends_on and transitions reference groups by id", sourcePath))
			continue
		}
		if _, dup := ids[g.ID]; dup {
			ds = append(ds, newDiag("harness.workflows.groups.id.duplicate", gf+".id",
				"duplicate workflow group id "+quote(g.ID), sourcePath))
			continue
		}
		ids[g.ID] = struct{}{}
		order = append(order, g.ID)
		fieldOf[g.ID] = gf
	}

	specs := make([]WorkflowGroupSpec, 0, len(groups))
	edges := make(map[string][]string, len(groups))
	for i, g := range groups {
		gf := field + ".groups[" + strconv.Itoa(i) + "]"
		if g.ID == "" || fieldOf[g.ID] != gf {
			continue // already reported above
		}
		if !inSet(g.Execution, wfExecutionStrategies) {
			ds = append(ds, newDiag("harness.workflows.groups.execution.unknown", gf+".execution",
				"workflow group "+quote(g.ID)+" declares execution "+quote(g.Execution)+"; valid values are "+setList(wfExecutionStrategies), sourcePath))
		}
		if len(g.Agents) == 0 {
			ds = append(ds, newDiag("harness.workflows.groups.agents.empty", gf+".agents",
				"workflow group "+quote(g.ID)+" declares no agents", sourcePath))
		}
		if g.OutputStrategy != "" && !inSet(g.OutputStrategy, wfOutputStrategies) {
			ds = append(ds, newDiag("harness.workflows.groups.outputStrategy.unknown", gf+".output_strategy",
				"workflow group "+quote(g.ID)+" declares output_strategy "+quote(g.OutputStrategy)+"; valid values are "+setList(wfOutputStrategies), sourcePath))
		}
		if g.Timeout != "" {
			if _, err := time.ParseDuration(g.Timeout); err != nil {
				ds = append(ds, newDiag("harness.workflows.groups.timeout.invalid", gf+".timeout",
					"workflow group "+quote(g.ID)+" timeout "+quote(g.Timeout)+" is not a valid Go duration", sourcePath))
			}
		}
		if g.Completion.Type != "" && !inSet(g.Completion.Type, wfCompletionTypes) {
			ds = append(ds, newDiag("harness.workflows.groups.completion.type.unknown", gf+".completion.type",
				"workflow group "+quote(g.ID)+" declares completion.type "+quote(g.Completion.Type)+"; valid values are "+setList(wfCompletionTypes), sourcePath))
		}
		if t := g.Completion.Threshold; t != nil && (*t < 0 || *t > 1) {
			ds = append(ds, newDiag("harness.workflows.groups.completion.threshold.invalid", gf+".completion.threshold",
				"workflow group "+quote(g.ID)+" completion.threshold must be within [0,1]", sourcePath))
		}

		deps := make([]string, 0, len(g.DependsOn))
		for _, dep := range g.DependsOn {
			if dep == g.ID {
				ds = append(ds, newDiag("harness.workflows.groups.dependsOn.self", gf+".depends_on",
					"workflow group "+quote(g.ID)+" depends on itself", sourcePath))
				continue
			}
			if _, ok := ids[dep]; !ok {
				ds = append(ds, newDiag("harness.workflows.groups.dependsOn.unknown", gf+".depends_on",
					"workflow group "+quote(g.ID)+" depends on "+quote(dep)+", which is not a declared group id", sourcePath))
				continue
			}
			deps = append(deps, dep)
		}
		edges[g.ID] = deps

		gspec := WorkflowGroupSpec{
			ID:             g.ID,
			Name:           g.Name,
			Execution:      g.Execution,
			DependsOn:      deps,
			Timeout:        g.Timeout,
			OutputStrategy: g.OutputStrategy,
			MetadataKeys:   sortedRawKeys(g.Metadata),
			Completion: WorkflowCompletionSpec{
				Type:          g.Completion.Type,
				Threshold:     copyFloatPtr(g.Completion.Threshold),
				MinAgents:     copyIntPtr(g.Completion.MinAgents),
				MaxFailures:   copyIntPtr(g.Completion.MaxFailures),
				RequireOutput: copyBoolPtr(g.Completion.RequireOutput),
			},
		}
		drops.note("groups[].description", g.Description != "")
		drops.note("groups[].metadata{}", len(g.Metadata) > 0)

		gspec.Agents = make([]WorkflowAgentSpec, 0, len(g.Agents))
		for j, a := range g.Agents {
			aspec, ads := adaptWorkflowAgent(sourcePath, gf+".agents["+strconv.Itoa(j)+"]", a, env, drops)
			ds = append(ds, ads...)
			gspec.Agents = append(gspec.Agents, aspec)
		}
		if g.Coordinator != nil {
			cspec, cds := adaptWorkflowAgent(sourcePath, gf+".coordinator", *g.Coordinator, env, drops)
			ds = append(ds, cds...)
			gspec.Coordinator = &cspec
		}

		gsteer, gsUses, gsDS := gateWorkflowGroupSteering(sourcePath, gf, g, env, drops)
		ds = append(ds, gsDS...)
		uses = append(uses, gsUses...)
		gspec.Steering = gsteer

		specs = append(specs, gspec)
	}

	// Cycle detection reuses the SINGLE three-colour DFS detector (agents.go
	// detectDelegateCycles) rather than forking a second one: agents.go is not in
	// this slice's edit surface, so the detector cannot be parameterized, and its
	// diagnostic is re-namespaced here instead. The reported cycle PATH is
	// preserved verbatim, which is the part a reader needs.
	if cyc := detectDelegateCycles(sourcePath, order, edges, fieldOf); len(cyc) > 0 {
		ds = append(ds, renameGroupCycleDiags(cyc)...)
	}

	if ds.HasErrors() {
		return nil, nil, nil, ds
	}
	return specs, ids, uses, nil
}

// renameGroupCycleDiags re-namespaces the shared cycle detector's output for the
// workflow-group graph. The path text produced by the detector is kept; only the
// code, the field suffix, and the two fixed sentence fragments are rewritten.
func renameGroupCycleDiags(ds Diagnostics) Diagnostics {
	out := make(Diagnostics, len(ds))
	copy(out, ds)
	for i := range out {
		out[i].Code = "harness.workflows.groups.dependsOn.cycle"
		out[i].FieldPath = strings.TrimSuffix(out[i].FieldPath, ".delegates") + ".depends_on"
		out[i].Message = strings.Replace(out[i].Message, "agent delegation cycle detected:", "workflow group dependency cycle detected:", 1)
		out[i].Message = strings.Replace(out[i].Message,
			"delegation references must form a directed acyclic graph",
			"group depends_on references must form a directed acyclic graph", 1)
	}
	return out
}

// adaptWorkflowTransitions maps `transitions:` and requires both endpoints to name
// a declared group: a transition to a group that does not exist is a dangling
// reference of exactly the kind D4 refuses to put into a plan.
func adaptWorkflowTransitions(sourcePath, field string, ts []wfTransition, groupIDs map[string]struct{}, drops *wfDrops) ([]WorkflowTransitionSpec, Diagnostics) {
	if len(ts) == 0 {
		return nil, nil
	}
	var ds Diagnostics
	out := make([]WorkflowTransitionSpec, 0, len(ts))
	for i, t := range ts {
		tf := field + ".transitions[" + strconv.Itoa(i) + "]"
		if _, ok := groupIDs[t.From]; !ok {
			ds = append(ds, newDiag("harness.workflows.transitions.from.unknown", tf+".from",
				"transition from "+quote(t.From)+" is not a declared group id", sourcePath))
		}
		if _, ok := groupIDs[t.To]; !ok {
			ds = append(ds, newDiag("harness.workflows.transitions.to.unknown", tf+".to",
				"transition to "+quote(t.To)+" is not a declared group id", sourcePath))
		}
		if t.Condition == "" {
			ds = append(ds, newDiag("harness.workflows.transitions.condition.missing", tf+".condition",
				"transition "+quote(t.From)+" -> "+quote(t.To)+" requires a non-empty condition; an unconditional transition would fire always", sourcePath))
		}
		if t.MaxRetries < 0 {
			ds = append(ds, newDiag("harness.workflows.transitions.maxRetries.invalid", tf+".max_retries",
				"transition max_retries must not be negative", sourcePath))
		}
		drops.note("transitions[].metadata{}", len(t.Metadata) > 0)
		out = append(out, WorkflowTransitionSpec{
			From:         t.From,
			To:           t.To,
			Condition:    t.Condition,
			MaxRetries:   t.MaxRetries,
			Priority:     t.Priority,
			MetadataKeys: sortedRawKeys(t.Metadata),
		})
	}
	if ds.HasErrors() {
		return nil, ds
	}
	return out, nil
}

// adaptWorkflowParameters maps `parameters:`. Every declared parameter is an input
// the run needs before it can start, so a parameter list REQUIRES the
// workflow.parameterProvider binding: without it an unattended run silently needs
// a human to answer a question, which is exactly Phase 9's exit-gate hazard.
func adaptWorkflowParameters(sourcePath, field string, ps []wfParameter, drops *wfDrops) ([]WorkflowParameterSpec, []bindingUse, Diagnostics) {
	if len(ps) == 0 {
		return nil, nil, nil
	}
	var ds Diagnostics
	ids := make(map[string]struct{}, len(ps))
	out := make([]WorkflowParameterSpec, 0, len(ps))
	for i, p := range ps {
		pf := field + ".parameters[" + strconv.Itoa(i) + "]"
		if p.ID == "" {
			ds = append(ds, newDiag("harness.workflows.parameters.id.missing", pf+".id",
				"workflow parameter requires a non-empty id", sourcePath))
			continue
		}
		if _, dup := ids[p.ID]; dup {
			ds = append(ds, newDiag("harness.workflows.parameters.id.duplicate", pf+".id",
				"duplicate workflow parameter id "+quote(p.ID), sourcePath))
			continue
		}
		ids[p.ID] = struct{}{}
		if !inSet(p.Type, wfParameterTypes) {
			ds = append(ds, newDiag("harness.workflows.parameters.type.unknown", pf+".type",
				"workflow parameter "+quote(p.ID)+" declares type "+quote(p.Type)+"; valid values are "+setList(wfParameterTypes), sourcePath))
		}
		if (p.Type == "choice" || p.Type == "multi_choice") && len(p.Choices) == 0 {
			ds = append(ds, newDiag("harness.workflows.parameters.choices.missing", pf+".choices",
				"workflow parameter "+quote(p.ID)+" has type "+quote(p.Type)+" and must declare choices", sourcePath))
		}
		drops.note("parameters[].description", p.Description != "")
		drops.note("parameters[].question", p.Question != "")
		drops.note("parameters[].default", len(p.Default) > 0)
		drops.note("parameters[].metadata{}", len(p.Metadata) > 0)
		spec := WorkflowParameterSpec{
			ID:           p.ID,
			Type:         p.Type,
			Required:     p.Required,
			HasDefault:   len(p.Default) > 0,
			Choices:      copyStringSlice(p.Choices),
			MetadataKeys: sortedRawKeys(p.Metadata),
		}
		if p.Question != "" {
			spec.QuestionHash = hashString(p.Question)
		}
		if v := p.Validation; v != nil {
			spec.Validation = &WorkflowParameterValidationSpec{
				MinLength: v.MinLength,
				MaxLength: v.MaxLength,
				Pattern:   v.Pattern,
				Min:       v.Min,
				Max:       v.Max,
				Step:      v.Step,
			}
		}
		out = append(out, spec)
	}
	if ds.HasErrors() {
		return nil, nil, ds
	}
	return out, []bindingUse{{binding: BindingWorkflowParameterProvider, field: field + ".parameters"}}, nil
}

// --- small pointer copy helpers (defensive copies, never shared pointers) ---

func copyIntPtr(in *int) *int {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

func copyBoolPtr(in *bool) *bool {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

func copyFloatPtr(in *float64) *float64 {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

// --- top-level adaptation ---------------------------------------------------

// adaptWorkflowDefinition maps the whole mirrored file into the camelCase
// definition spec. It is the ONLY function that reads wfDefinitionFile's own
// fields, so the complete root-level mapping is auditable in one place.
func adaptWorkflowDefinition(sourcePath, field string, f *wfDefinitionFile, env workflowEnv, drops *wfDrops) (WorkflowDefinitionSpec, []bindingUse, Diagnostics) {
	var ds Diagnostics
	var uses []bindingUse

	if f.Name == "" {
		ds = append(ds, newDiag("harness.workflows.name.missing", field+".name",
			"referenced workflow requires a non-empty name", sourcePath))
	}

	cfg, cfgUses, cfgDS := adaptWorkflowConfig(sourcePath, field, f.Config, env, drops)
	ds = append(ds, cfgDS...)
	uses = append(uses, cfgUses...)

	groups, groupIDs, groupUses, groupDS := adaptWorkflowGroups(sourcePath, field, f.Groups, env, drops)
	ds = append(ds, groupDS...)
	uses = append(uses, groupUses...)

	transitions, transDS := adaptWorkflowTransitions(sourcePath, field, f.Transitions, groupIDs, drops)
	ds = append(ds, transDS...)

	steering, steerUses, steerDS := gateWorkflowSteering(sourcePath, field, f.Steering, env, drops)
	ds = append(ds, steerDS...)
	uses = append(uses, steerUses...)

	params, paramUses, paramDS := adaptWorkflowParameters(sourcePath, field, f.Parameters, drops)
	ds = append(ds, paramDS...)
	uses = append(uses, paramUses...)

	// entry_hooks / exit_hooks are hook IDS, and a hook RUNS SOMETHING. A workflow
	// must therefore not name a hook the audited `hooks:` section never declared:
	// that would be an unreviewed command executed on the plan's behalf.
	ds = append(ds, gateWorkflowHooks(sourcePath, field+".entry_hooks", "entryHooks", f.EntryHooks, env)...)
	ds = append(ds, gateWorkflowHooks(sourcePath, field+".exit_hooks", "exitHooks", f.ExitHooks, env)...)

	drops.note("description", f.Description != "")
	drops.note("metadata{}", len(f.Metadata) > 0)

	if ds.HasErrors() {
		return WorkflowDefinitionSpec{}, nil, ds
	}
	return WorkflowDefinitionSpec{
		ID:           f.ID,
		Name:         f.Name,
		Config:       cfg,
		Groups:       groups,
		Transitions:  transitions,
		EntryHooks:   copyStringSlice(f.EntryHooks),
		ExitHooks:    copyStringSlice(f.ExitHooks),
		Steering:     steering,
		Parameters:   params,
		MetadataKeys: sortedRawKeys(f.Metadata),
	}, uses, nil
}

// gateWorkflowHooks refuses a workflow hook reference that the plan's `hooks:`
// section does not declare.
func gateWorkflowHooks(sourcePath, fieldPath, kind string, ids []string, env workflowEnv) Diagnostics {
	if len(ids) == 0 || !env.resolvable {
		return nil
	}
	var ds Diagnostics
	for _, id := range ids {
		if id == "" {
			ds = append(ds, newDiag("harness.workflows."+kind+".empty", fieldPath,
				"workflow hook reference is empty", sourcePath))
			continue
		}
		if _, ok := env.hookIDs[id]; !ok {
			ds = append(ds, newDiag("harness.workflows."+kind+".undeclared", fieldPath,
				"workflow hook "+quote(id)+" is not declared in this document's hooks: section; a hook executes something, so a referenced workflow may "+
					"not introduce one the plan never audited", sourcePath))
		}
	}
	return ds
}

// --- resolution -------------------------------------------------------------

// resolveWorkflows validates and resolves the `workflows:` section: id integrity,
// the contained-file reference, the content hash, the version pin, and the full
// adaptation with its D5 gates. It returns the binding USES it accumulated so the
// caller can fold them into the plan's requirement list AFTER Phase 9b's schedules
// have populated it (resolveSchedules assigns p.bindings, so a merge cannot happen
// before it runs).
//
// Nothing here constructs an engine, a group, or an agent.
func resolveWorkflows(p *Plan, sourcePath, mdir string, entries []WorkflowEntry, env workflowEnv) ([]bindingUse, Diagnostics) {
	if len(entries) == 0 {
		// Omitted or `workflows: []`: behave exactly as pre-9c. Nothing is
		// carried, no provenance is added, and the digest is unchanged.
		return nil, nil
	}

	var ds Diagnostics
	var uses []bindingUse
	specs := make([]WorkflowSpec, 0, len(entries))

	ids := make(map[string]struct{}, len(entries))
	fieldOf := make(map[string]string, len(entries))
	for i, e := range entries {
		field := "workflows[" + strconv.Itoa(i) + "]"
		if e.ID == "" {
			ds = append(ds, newDiag("harness.workflows.id.missing", field+".id",
				"workflow entry requires a non-empty id", sourcePath))
			continue
		}
		if _, dup := ids[e.ID]; dup {
			ds = append(ds, newDiag("harness.workflows.id.duplicate", field+".id",
				"duplicate workflow id "+quote(e.ID), sourcePath))
			continue
		}
		ids[e.ID] = struct{}{}
		fieldOf[e.ID] = field
	}

	for i, e := range entries {
		field := "workflows[" + strconv.Itoa(i) + "]"
		if e.ID == "" || fieldOf[e.ID] != field {
			continue // already reported
		}
		if e.File == "" {
			ds = append(ds, newDiag("harness.workflows.file.missing", field+".file",
				"workflow "+quote(e.ID)+" requires a file reference to an existing workflow manifest", sourcePath))
			continue
		}
		if e.ExpectedVersion == "" {
			// D3: the pin is REQUIRED, so the audited manifest states the
			// contract instead of trusting whatever is on disk today.
			ds = append(ds, newDiag("harness.workflows.expectedVersion.missing", field+".expectedVersion",
				"workflow "+quote(e.ID)+" requires expectedVersion; the referenced file is an external artifact this manifest does not own, so the "+
					"version it is compiled against must be stated here and is verified against the file's own version", sourcePath))
			continue
		}

		// Phase 5a contained-file machinery: manifest-relative, traversal- and
		// symlink-guarded, fail-closed when missing or unreadable.
		data, abs, d := loadContainedFileBytes(sourcePath, field+".file", mdir, e.File)
		if d != nil {
			ds = append(ds, *d)
			continue
		}
		f, dd := decodeWorkflowFile(sourcePath, field+".file", abs, data)
		if dd != nil {
			ds = append(ds, *dd)
			continue
		}
		if f.Version == "" {
			ds = append(ds, newDiag("harness.workflows.version.missing", field+".file",
				"workflow "+quote(e.ID)+": the referenced file declares no version, so the expectedVersion pin "+quote(e.ExpectedVersion)+
					" cannot be verified; an unverifiable pin is the same lie as a silent mismatch", sourcePath))
			continue
		}
		if f.Version != e.ExpectedVersion {
			ds = append(ds, newDiag("harness.workflows.version.mismatch", field+".expectedVersion",
				"workflow "+quote(e.ID)+": expectedVersion is "+quote(e.ExpectedVersion)+" but the referenced file declares version "+quote(f.Version)+
					"; the manifest and the artifact disagree about which orchestration this plan runs", sourcePath))
			continue
		}

		drops := newWfDrops()
		def, defUses, defDS := adaptWorkflowDefinition(sourcePath, field, f, env, drops)
		if len(defDS) > 0 {
			ds = append(ds, defDS...)
			continue
		}

		// Every declared workflow requires an ENGINE by its mere existence, the
		// same "required by existence rather than by a dimension" discipline
		// Phase 9b applies to scheduler.singleOwner.
		wUses := append([]bindingUse{{binding: BindingWorkflowEngine, field: field}}, defUses...)
		uses = append(uses, wUses...)

		spec := WorkflowSpec{
			ID:            e.ID,
			File:          filepath.Clean(e.File),
			ContentHash:   hashBytes(data),
			Version:       f.Version,
			Definition:    def,
			DroppedFields: drops.list(),
		}
		seen := make(map[RuntimeBindingKind]struct{}, len(wUses))
		for _, u := range wUses {
			if _, dup := seen[u.binding]; dup {
				continue
			}
			seen[u.binding] = struct{}{}
			spec.Bindings = append(spec.Bindings, u.binding)
		}
		sortAllBindings(spec.Bindings)
		specs = append(specs, spec)
	}

	if ds.HasErrors() {
		return nil, ds
	}
	p.workflows = specs
	for _, s := range specs {
		p.addProvenance("workflows."+s.ID, "file", s.File)
	}
	return uses, nil
}

// sortAllBindings orders a binding list by allBindingOrder, so a list mixing
// Phase 9b scheduler bindings and this slice's workflow bindings is deterministic.
func sortAllBindings(b []RuntimeBindingKind) {
	rank := make(map[RuntimeBindingKind]int, len(allBindingOrder))
	for i, k := range allBindingOrder {
		rank[k] = i
	}
	sort.SliceStable(b, func(i, j int) bool { return rank[b[i]] < rank[b[j]] })
}

// mergeWorkflowBindings folds workflow binding uses into the plan's requirement
// list. It must run AFTER resolveSchedules, which ASSIGNS p.bindings from the
// schedule uses alone; merging here (rather than reaching into schedules.go) keeps
// Phase 9b's file untouched apart from D4's one branch.
//
// It is a strict no-op for zero uses, which is what keeps a plan that declares no
// workflows byte-identical to pre-9c.
func mergeWorkflowBindings(p *Plan, uses []bindingUse) {
	if len(uses) == 0 {
		return
	}
	byKind := make(map[RuntimeBindingKind][]string, len(uses))
	seen := make(map[bindingUse]struct{}, len(uses))
	add := func(kind RuntimeBindingKind, field string) {
		u := bindingUse{binding: kind, field: field}
		if _, dup := seen[u]; dup {
			return
		}
		seen[u] = struct{}{}
		byKind[kind] = append(byKind[kind], field)
	}
	// Existing (Phase 9b schedule) requirements keep their order and position.
	for _, r := range p.bindings {
		for _, f := range r.RequiredBy {
			add(r.Binding, f)
		}
	}
	for _, u := range uses {
		add(u.binding, u.field)
	}
	out := make([]RuntimeBindingRequirement, 0, len(byKind))
	for _, kind := range allBindingOrder {
		fields, ok := byKind[kind]
		if !ok {
			continue
		}
		out = append(out, RuntimeBindingRequirement{
			Binding:    kind,
			RequiredBy: fields,
			Reason:     bindingReason(kind),
		})
	}
	p.bindings = out
}

// --- D4: the schedules[].target.kind == workflow seam ------------------------

// resolveScheduleWorkflowTarget is the body of the ONE branch Phase 9b left for
// this slice to close (schedules.go resolveScheduleTarget). It lives here so
// schedules.go changes by exactly that branch.
//
// A workflow target is now RESOLVED rather than rejected. The reference INTEGRITY
// check (is this id declared in `workflows:`?) is deliberately a separate pass —
// checkScheduleWorkflowTargets — because resolveScheduleTarget's env is Phase 9b's
// scheduleEnv and extending that struct would mean editing more of schedules.go
// than D4 permits. The outcome is identical: an unknown id is a COMPILE ERROR with
// the same anti-cascade suppression as unknownAgent/unknownProfile.
//
// The enforcement class is stated HERE rather than looked up in scheduleFacts,
// because scheduleFacts has no (target, workflow) row and a missing row would
// yield an EMPTY enforcement class — a plan that reports nothing about what the
// runtime will do. Both bindings are honest: dispatching a NAMED target is the
// Phase 9b dispatcher gap, and running a workflow at all needs an engine.
func resolveScheduleWorkflowTarget(sourcePath, f, id, workflowID string) (ScheduleDimensionSpec, Diagnostics) {
	if workflowID == "" {
		return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.target.id.missing", f+".id",
			"schedule "+quote(id)+": target.id is required and must name a declared workflow id", sourcePath)}
	}
	return ScheduleDimensionSpec{
		Dimension:   ScheduleDimTarget,
		Value:       string(ScheduleTargetWorkflow),
		Enforcement: EnforcementBindingRequired,
		Bindings:    []RuntimeBindingKind{BindingSchedulerTargetDispatcher, BindingWorkflowEngine},
		Ref:         workflowID,
	}, nil
}

// checkScheduleWorkflowTargets closes D4's reference integrity: every schedule
// whose target kind is `workflow` must name a declared workflows[] id. It also
// returns the workflow.engine binding uses for those schedules, because
// collectBindingRequirements (schedules.go) can only emit bindings listed in
// Phase 9b's runtimeBindingOrder and would otherwise drop this slice's binding.
//
// resolvable mirrors scheduleEnv.targetsResolvable: when an earlier section has
// already failed, the declared id set is incomplete and checking against it would
// turn one real error into a cascade of misleading "unknown workflow" diagnostics.
func checkScheduleWorkflowTargets(p *Plan, sourcePath string, resolvable bool) ([]bindingUse, Diagnostics) {
	if len(p.schedules) == 0 {
		return nil, nil
	}
	declared := make(map[string]struct{}, len(p.workflows))
	for _, w := range p.workflows {
		declared[w.ID] = struct{}{}
	}
	var ds Diagnostics
	var uses []bindingUse
	for i, s := range p.schedules {
		tg, ok := s.Dimension(ScheduleDimTarget)
		if !ok || tg.Value != string(ScheduleTargetWorkflow) {
			continue
		}
		field := "schedules[" + strconv.Itoa(i) + "].target"
		uses = append(uses, bindingUse{binding: BindingWorkflowEngine, field: field})
		if !resolvable {
			continue
		}
		if _, known := declared[tg.Ref]; !known {
			ds = append(ds, newDiag("harness.schedules.target.unknownWorkflow", field+".id",
				"schedule "+quote(s.ID)+": target workflow "+quote(tg.Ref)+" is not declared in workflows[]", sourcePath))
		}
	}
	return uses, ds
}

// --- env construction -------------------------------------------------------

// effectiveCapabilityIDs returns the plan's EFFECTIVE capability set: the primary
// agent's resolved tools union every subagent's resolved tools. Both have already
// passed the Phase 8d policy gate, so this is the exact, audited vocabulary a
// referenced workflow may not exceed.
func effectiveCapabilityIDs(p *Plan) map[string]struct{} {
	out := make(map[string]struct{}, len(p.tools))
	for _, id := range p.tools {
		out[id] = struct{}{}
	}
	for _, sa := range p.subagents {
		for _, id := range sa.Tools {
			out[id] = struct{}{}
		}
	}
	return out
}

// idSetOfHooks exposes the declared hook id vocabulary as a set.
func idSetOfHooks(specs []HookSpec) map[string]struct{} {
	out := make(map[string]struct{}, len(specs))
	for _, h := range specs {
		out[h.ID] = struct{}{}
	}
	return out
}

// --- immutable accessors ----------------------------------------------------

// Workflows returns a DEEP copy of the resolved workflows carried on the plan, in
// manifest order. Every nested slice, map-derived key list, and pointer is copied,
// so a caller can never mutate the immutable Plan through a returned value.
func (p *Plan) Workflows() []WorkflowSpec {
	out := make([]WorkflowSpec, 0, len(p.workflows))
	for _, w := range p.workflows {
		out = append(out, copyWorkflowSpec(w))
	}
	return out
}

// Workflow returns one resolved workflow by id. ok is false when undeclared.
func (p *Plan) Workflow(id string) (WorkflowSpec, bool) {
	for _, w := range p.workflows {
		if w.ID == id {
			return copyWorkflowSpec(w), true
		}
	}
	return WorkflowSpec{}, false
}

func copyWorkflowSpec(w WorkflowSpec) WorkflowSpec {
	out := WorkflowSpec{
		ID:          w.ID,
		File:        w.File,
		ContentHash: w.ContentHash,
		Version:     w.Version,
		Bindings:    append([]RuntimeBindingKind(nil), w.Bindings...),
	}
	if len(w.DroppedFields) > 0 {
		out.DroppedFields = append([]WorkflowDroppedField(nil), w.DroppedFields...)
	}
	out.Definition = copyWorkflowDefinition(w.Definition)
	return out
}

func copyWorkflowDefinition(d WorkflowDefinitionSpec) WorkflowDefinitionSpec {
	out := WorkflowDefinitionSpec{
		ID:           d.ID,
		Name:         d.Name,
		Config:       copyWorkflowConfig(d.Config),
		EntryHooks:   copyStringSlice(d.EntryHooks),
		ExitHooks:    copyStringSlice(d.ExitHooks),
		MetadataKeys: copyStringSlice(d.MetadataKeys),
	}
	if len(d.Groups) > 0 {
		out.Groups = make([]WorkflowGroupSpec, 0, len(d.Groups))
		for _, g := range d.Groups {
			out.Groups = append(out.Groups, copyWorkflowGroup(g))
		}
	}
	if len(d.Transitions) > 0 {
		out.Transitions = make([]WorkflowTransitionSpec, 0, len(d.Transitions))
		for _, t := range d.Transitions {
			t.MetadataKeys = copyStringSlice(t.MetadataKeys)
			out.Transitions = append(out.Transitions, t)
		}
	}
	if d.Steering != nil {
		s := WorkflowSteeringSpec{Type: d.Steering.Type, CustomKeys: copyStringSlice(d.Steering.CustomKeys)}
		if len(d.Steering.Rules) > 0 {
			s.Rules = make([]WorkflowSteeringRuleSpec, 0, len(d.Steering.Rules))
			for _, r := range d.Steering.Rules {
				r.ParameterKeys = copyStringSlice(r.ParameterKeys)
				s.Rules = append(s.Rules, r)
			}
		}
		out.Steering = &s
	}
	if len(d.Parameters) > 0 {
		out.Parameters = make([]WorkflowParameterSpec, 0, len(d.Parameters))
		for _, prm := range d.Parameters {
			prm.Choices = copyStringSlice(prm.Choices)
			prm.MetadataKeys = copyStringSlice(prm.MetadataKeys)
			if prm.Validation != nil {
				v := *prm.Validation
				prm.Validation = &v
			}
			out.Parameters = append(out.Parameters, prm)
		}
	}
	return out
}

func copyWorkflowConfig(c WorkflowConfigSpec) WorkflowConfigSpec {
	return WorkflowConfigSpec{
		MaxDuration:            c.MaxDuration,
		AllowHumanIntervention: copyBoolPtr(c.AllowHumanIntervention),
		FailOnSteeringBlock:    copyBoolPtr(c.FailOnSteeringBlock),
		MaxRetries:             copyIntPtr(c.MaxRetries),
		TimeoutBehavior:        c.TimeoutBehavior,
		CustomKeys:             copyStringSlice(c.CustomKeys),
	}
}

func copyWorkflowGroup(g WorkflowGroupSpec) WorkflowGroupSpec {
	out := WorkflowGroupSpec{
		ID:             g.ID,
		Name:           g.Name,
		Execution:      g.Execution,
		DependsOn:      copyStringSlice(g.DependsOn),
		Timeout:        g.Timeout,
		OutputStrategy: g.OutputStrategy,
		MetadataKeys:   copyStringSlice(g.MetadataKeys),
		Completion: WorkflowCompletionSpec{
			Type:          g.Completion.Type,
			Threshold:     copyFloatPtr(g.Completion.Threshold),
			MinAgents:     copyIntPtr(g.Completion.MinAgents),
			MaxFailures:   copyIntPtr(g.Completion.MaxFailures),
			RequireOutput: copyBoolPtr(g.Completion.RequireOutput),
		},
	}
	if len(g.Agents) > 0 {
		out.Agents = make([]WorkflowAgentSpec, 0, len(g.Agents))
		for _, a := range g.Agents {
			out.Agents = append(out.Agents, copyWorkflowAgent(a))
		}
	}
	if g.Coordinator != nil {
		c := copyWorkflowAgent(*g.Coordinator)
		out.Coordinator = &c
	}
	if g.Steering != nil {
		s := WorkflowGroupSteeringSpec{
			SynthesisStrategy:  g.Steering.SynthesisStrategy,
			ConflictResolution: g.Steering.ConflictResolution,
			CustomKeys:         copyStringSlice(g.Steering.CustomKeys),
		}
		if g.Steering.ValidatePlan != nil {
			v := *g.Steering.ValidatePlan
			v.Rules = copyStringSlice(v.Rules)
			s.ValidatePlan = &v
		}
		out.Steering = &s
	}
	return out
}

func copyWorkflowAgent(a WorkflowAgentSpec) WorkflowAgentSpec {
	out := a
	out.Tools = copyStringSlice(a.Tools)
	out.ToolCapabilities = copyStringSlice(a.ToolCapabilities)
	out.PromptVariableKeys = copyStringSlice(a.PromptVariableKeys)
	out.ContextSources = copyStringSlice(a.ContextSources)
	if a.ProviderConfig != nil {
		pc := *a.ProviderConfig
		pc.SystemCacheControlKeys = copyStringSlice(pc.SystemCacheControlKeys)
		pc.ToolCacheControlKeys = copyStringSlice(pc.ToolCacheControlKeys)
		pc.MessageCacheControlKeys = copyStringSlice(pc.MessageCacheControlKeys)
		pc.CustomKeys = copyStringSlice(pc.CustomKeys)
		out.ProviderConfig = &pc
	}
	if a.Capabilities != nil {
		out.Capabilities = &WorkflowAgentCapabilitiesSpec{
			MaxTokens:      copyIntPtr(a.Capabilities.MaxTokens),
			Temperature:    copyFloatPtr(a.Capabilities.Temperature),
			MaxTurns:       copyIntPtr(a.Capabilities.MaxTurns),
			TimeoutSeconds: copyIntPtr(a.Capabilities.TimeoutSeconds),
			Streaming:      copyBoolPtr(a.Capabilities.Streaming),
			CachePrompts:   copyBoolPtr(a.Capabilities.CachePrompts),
			ParallelTools:  copyBoolPtr(a.Capabilities.ParallelTools),
		}
	}
	return out
}
