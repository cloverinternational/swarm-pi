// Package agent implements the agent runtime layer (Ring 2).
// Agents execute tasks using providers, tools, and memory while maintaining full observability.
package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/filetracker"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/managed"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/taskstore"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ToolFilterFunc is called by buildProviderTools after the full tool list is
// assembled but before it is sent to the provider. It receives all candidate
// provider.Tool values and returns the subset that should actually be included
// in the ChatRequest. This enables deferred tool loading: the filter strips
// tools classified as "deferred" so the LLM only sees the eager set, saving
// context tokens on every turn.
//
// The filter is optional — when nil, all tools are sent (legacy behaviour).
type ToolFilterFunc func(allTools []provider.Tool) []provider.Tool

// CodeModeInstaller is an interface for installing code mode on an agent's
// tool registry. Code mode wraps multiple tools into a single run_code tool,
// allowing the LLM to orchestrate tool calls with JavaScript code instead of
// one model round-trip per tool call.
//
// Implementations should:
//  1. Select which tools to sandbox inside run_code
//  2. Hide sandboxed tools from the registry's List() method
//  3. Register the run_code tool that can call sandboxed tools
//
// See tools/codemode package for the standard implementation.
type CodeModeInstaller interface {
	// Install wraps the given registry to enable code mode.
	// The executor is used to dispatch tool calls from inside the sandbox.
	// Returns a wrapped registry that should be used instead of the original.
	Install(registry tools.Registry, executor tools.Executor) (tools.Registry, error)
}

// Tool output limits to prevent context window bloat
// Token estimation: 1 token ≈ 4 characters
const (
	// CharsPerToken is the approximate character to token ratio
	CharsPerToken = 4

	// MaxToolOutputTokens is the maximum tokens allowed per tool result
	MaxToolOutputTokens = 25000

	// MaxToolOutputChars is calculated from token limit (25k tokens × 4 chars)
	MaxToolOutputChars = MaxToolOutputTokens * CharsPerToken // 100,000 chars

	// MaxToolOutputLines caps line count to prevent huge outputs
	// Assuming ~80 chars per line average, 100k chars ≈ 1250 lines, but we cap lower
	MaxToolOutputLines = 1000
)

// Helper function for debug logging
func getContextKeys(context map[string]any) []string {
	if context == nil {
		return nil
	}
	keys := make([]string, 0, len(context))
	for k := range context {
		keys = append(keys, k)
	}
	return keys
}

// MessageCallback is called when the agent generates a message during execution.
// This allows the agent to save messages to external storage (e.g., conversation manager).
type MessageCallback func(ctx context.Context, msg *conversation.Message) error

// CompactionPersistCallback commits an in-loop compacted generation to the
// host's durable conversation store.
type CompactionPersistCallback func(ctx context.Context, convID string, compacted []*conversation.Message, summary string, contextSize int) error

// MessageInjector is a callback that returns pending user messages to inject
// into the conversation between agent turns.  The callback should drain and
// return all pending messages (returning nil/empty when none are pending).
// It is called from the agent execution goroutine and must be thread-safe.
type MessageInjector func() []string

// RichMessageInjector is a callback that returns full canonical messages to inject
// between turns, preserving message role and metadata.
type RichMessageInjector func() []*conversation.Message

// IntermediateUpdate represents an update during agent execution for real-time UI updates.
type IntermediateUpdate interface {
	UpdateType() string
}

// ToolCallUpdate represents a tool call that's about to be executed.
type ToolCallUpdate struct {
	ID         string
	Name       string
	Parameters map[string]any
	Sequence   uint64 // SDK-assigned sequence for ordering (emission-time)
}

func (u ToolCallUpdate) UpdateType() string { return "tool_call" }

// ToolResultUpdate represents the result of a tool execution.
type ToolResultUpdate struct {
	ID            string
	Output        string
	Error         error
	ContentBlocks []any          // Content blocks from tool result (images, etc.)
	Metadata      map[string]any // Tool-specific metadata for rendering (e.g., diff data)
	Hosted        *hosted.ResultMetadata
	TaskClass     hosted.TaskClass
	Sequence      uint64 // SDK-assigned sequence for ordering (emission-time)
}

func (u ToolResultUpdate) UpdateType() string { return "tool_result" }

// ThinkingUpdate represents extended thinking content.
type ThinkingUpdate struct {
	Content  string
	Append   bool
	Sequence uint64 // SDK-assigned sequence for ordering (emission-time)
}

func (u ThinkingUpdate) UpdateType() string { return "thinking" }

// REMOVED: TokenEstimateUpdate
// Reason: We never estimate tokens. All providers (Anthropic, OpenAI, Gemini)
// return accurate token counts in their streaming responses within milliseconds.
// Using estimates caused bugs with 200x+ inflation for large tool results.
// See: TOKEN_COUNT_FIX_PLAN.md

// ContentUpdate represents streaming response content.
type ContentUpdate struct {
	Content  string
	Append   bool
	Sequence uint64 // SDK-assigned sequence for ordering (emission-time)
}

func (u ContentUpdate) UpdateType() string { return "content" }

// AssistantMessageUpdate signals completion of an assistant turn. Consumers
// should treat it as the authoritative finalization of that turn: any
// placeholder the UI mutated incrementally via ContentUpdate / ThinkingUpdate
// / Tool* can now be finalized from this payload. Fires once per turn after
// all deltas for that turn have been emitted. Exists so UIs never have to
// infer "turn complete" from a scalar return value — when providers deliver
// content in ways that bypass incremental callbacks (tool-only final turns,
// non-streaming single-shot responses), the incremental stream alone leaves
// the UI guessing. Mirrors the role of Claude Code's terminal `type:
// 'assistant'` stream event.
type AssistantMessageUpdate struct {
	Content      string // final textual content of this turn
	Thinking     string // final thinking content of this turn
	FinishReason string // "stop" / "tool_calls" / "length" / etc.
	Turn         int    // 1-based turn index in the current execution
	InputTokens  int    // full context sent to model for this turn (0 if unknown)
	OutputTokens int    // tokens generated this turn (0 if unknown)
	Sequence     uint64 // SDK-assigned sequence for ordering (emission-time)
}

func (u AssistantMessageUpdate) UpdateType() string { return "assistant_message" }

// TokenCountUpdate carries real token counts after each API call in the execute loop.
// This is the authoritative per-turn token count: input_tokens = full context sent to model.
// Emitted after every turn so the TUI can track context growth during multi-turn tool use.
type TokenCountUpdate struct {
	InputTokens          int     // Full context size sent to model (authoritative)
	OutputTokens         int     // Tokens generated this turn
	Turn                 int     // Which turn this came from (1-based)
	ContextWindow        int     // Model's total context window (e.g. 200000)
	EffectiveWindow      int     // Usable input budget after output reservation
	AutoCompactThreshold int     // Token count at which auto-compact fires
	PctUsed              float64 // InputTokens / ContextWindow * 100 (0 if InputTokens==0)
	// CacheReadTokens is the portion of the prompt served from the provider-side
	// prompt cache this turn. Populated for providers that report it (Anthropic,
	// OpenAI-compatible providers like Fireworks/Z.ai/Kimi when present).
	CacheReadTokens int
	// CacheCreationTokens is the portion of the prompt written to the prompt
	// cache this turn. Currently only populated by Anthropic.
	CacheCreationTokens int
	// UncachedInputTokens is the uncached delta of the prompt (InputTokens minus
	// cache read minus cache creation). Useful for matching the Anthropic
	// stream-json schema where input_tokens excludes the cached portion.
	UncachedInputTokens int
}

func (u TokenCountUpdate) UpdateType() string { return "token_count" }

// TurnUsageUpdate is the authoritative END-OF-TURN usage summary, emitted once
// after a full Execute/Chat completes (i.e. after the whole agent loop, not
// per provider call like TokenCountUpdate). It carries the totals from
// ExecuteResponse — including estimated cost when model pricing is available —
// so a streaming client can record final per-turn accounting without owning a
// catalog. It is additive: clients that only handle token_count are unaffected.
type TurnUsageUpdate struct {
	ConversationID string  `json:"ConversationID,omitempty"`
	TurnCount      int     `json:"TurnCount"`
	InputTokens    int     `json:"InputTokens"`
	OutputTokens   int     `json:"OutputTokens"`
	TotalTokens    int     `json:"TotalTokens"`
	CostUSD        float64 `json:"CostUSD"`        // 0 when pricing is unavailable
	FinishReason   string  `json:"FinishReason"`   // provider.FinishReason as string
	DurationMillis int64   `json:"DurationMillis"` // wall-clock turn duration
}

func (u TurnUsageUpdate) UpdateType() string { return "turn_usage" }

// CompactionNeededUpdate signals that the context is approaching the token limit
// and proactive compaction should be triggered before the next API call.
// This is emitted during multi-turn tool use cycles to prevent "prompt too long" errors.
type CompactionNeededUpdate struct {
	CurrentTokens int     // Current estimated token count (input + tool results)
	Threshold     int     // Token threshold that triggered this warning
	ContextLimit  int     // Model's context window limit
	PercentUsed   float64 // Percentage of context window used (0.0-1.0)
}

func (u CompactionNeededUpdate) UpdateType() string { return "compaction_needed" }

// CompactionDoneUpdate is emitted after the agent successfully compacts in-place
// via CompactFunc. The next API call will populate real token counts.
type CompactionDoneUpdate struct {
	TokensBefore int // inputTokens count before compaction
	TokensAfter  int // 0 until the next API call populates CurrentContextSize
	Turn         int // turn number when compaction occurred
}

func (u CompactionDoneUpdate) UpdateType() string { return "compaction_done" }

// CompactionFailedUpdate is emitted when an in-loop compaction attempt fails.
// Fatal reports whether the failure occurred at the hard blocking limit (the
// turn will abort with ContextPressureError) as opposed to the proactive
// threshold (the turn continues and compaction may be retried later).
type CompactionFailedUpdate struct {
	TokensBefore int    // inputTokens count when compaction was attempted
	Error        string // cause of the failure
	Fatal        bool   // true when the hard blocking limit was crossed
	Turn         int    // turn number when compaction failed
}

func (u CompactionFailedUpdate) UpdateType() string { return "compaction_failed" }

// ContextPressureError stops execution before an unsafe provider request. It
// preserves the compaction failure (when present) so clients can render the
// causal error instead of a later, less useful provider context-limit error.
type ContextPressureError struct {
	Stage         string
	CurrentTokens int
	Threshold     int
	ContextLimit  int
	Cause         error
}

func (e *ContextPressureError) Error() string {
	if e == nil {
		return "context compaction required"
	}
	base := fmt.Sprintf(
		"context compaction required at %d tokens (threshold %d, limit %d)",
		e.CurrentTokens,
		e.Threshold,
		e.ContextLimit,
	)
	if e.Stage != "" {
		base = e.Stage + ": " + base
	}
	if e.Cause != nil {
		return base + ": " + e.Cause.Error()
	}
	return base
}

func (e *ContextPressureError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// HookExecutionUpdate represents a hook execution (before or after a tool).
type HookExecutionUpdate struct {
	HookName   string // Name of the hook that executed
	ToolName   string // Name of the tool this hook is for
	ToolCallID string // ID of the specific tool call this hook is for
	Phase      string // "before" or "after"
	Success    bool   // Whether the hook succeeded
	Output     string // Hook output (stdout/stderr)
	Blocked    bool   // Whether the hook blocked execution
	Error      string // Error message if any
	// Timing and execution details.
	Duration          time.Duration // How long the hook ran
	ExitCode          int           // Process exit code
	MatchedPattern    string        // Regex pattern that triggered this hook
	TimeoutConfigured time.Duration // Configured timeout for this hook
	WorkingDir        string        // Working directory where hook ran
	// Real-time correlation fields for tracking in-progress hook events.
	HookID    string // Unique ID for correlating start/complete events
	Status    string // "started" or "completed"
	StartedAt int64  // Unix timestamp (milliseconds) when hook started
	Sequence  uint64 // SDK-assigned sequence for ordering (emission-time)
}

func (u HookExecutionUpdate) UpdateType() string { return "hook_execution" }

// ToolOutputChunk represents incremental stdout/stderr from a streaming tool.
type ToolOutputChunk struct {
	ID     string // Tool call ID
	Chunk  string // The output chunk (usually one line)
	Stream string // "stdout" or "stderr"
}

func (u ToolOutputChunk) UpdateType() string { return "tool_output_chunk" }

// HookOutputChunk represents incremental stdout/stderr from a running hook.
type HookOutputChunk struct {
	HookID    string // Correlation ID matching HookExecutionUpdate.HookID
	HookName  string // Name of the hook (for display with [hook-name] prefix)
	Chunk     string // The output chunk (buffered at 1KB or 100ms)
	IsStderr  bool   // true for stderr, false for stdout
	Timestamp int64  // Unix timestamp (milliseconds) when chunk was emitted
}

func (u HookOutputChunk) UpdateType() string { return "hook_output_chunk" }

// SubAgentUpdate wraps an update from a delegated sub-agent.
type SubAgentUpdate struct {
	AgentID   string             // ID of the sub-agent
	AgentName string             // Name of the sub-agent
	Update    IntermediateUpdate // The actual update from the sub-agent
}

func (u SubAgentUpdate) UpdateType() string { return "sub_agent_update" }

// FallbackUpdate is emitted when the fallback chain attempts or switches providers.
type FallbackUpdate struct {
	Provider     string        // Current attempt's provider
	Model        string        // Current attempt's model
	FromProvider string        // Primary provider that failed (populated on "success")
	FromModel    string        // Primary model that failed
	AttemptIndex int           // 0=primary, 1+=fallback index
	Status       string        // "attempting", "failed", "success"
	Error        error         // Error from failed attempt (nil on success/attempting)
	Duration     time.Duration // How long the attempt took
}

func (u FallbackUpdate) UpdateType() string { return "fallback" }

// ExhaustedUpdate is emitted when every model in the fallback chain has failed
// and the agent is pausing to ask the user to choose a different profile.
// The TUI should display a profile picker and write its choice to Response.
// The Response channel is buffered (size 1); send exactly once.
type ExhaustedUpdate struct {
	Provider string  // primary provider that failed
	Model    string  // primary model that failed
	Attempts int     // total HTTP-level attempts made across all chain entries
	Errors   []error // one error per chain entry that was tried
	Response chan FallbackDecision
}

func (u ExhaustedUpdate) UpdateType() string { return "exhausted" }

// FallbackDecision is the TUI's response to ExhaustedUpdate.
// Exactly one field should be set.
type FallbackDecision struct {
	// NewChain, if non-nil, tells the agent to retry with this chain.
	// Build it via profiles.Manager.ResolveChain after SetDefaultProfile.
	NewChain *fallback.Chain
	// Cancel, if true, aborts the current turn cleanly.
	Cancel bool
}

// HeartbeatUpdate is emitted on a fixed cadence by long-running operations
// (notably sub-agent execution) to prove liveness when the underlying LLM is
// in a long thinking block or otherwise silent. The TUI uses this to
// distinguish "callback wired but currently idle" from "callback never fired"
// — the latter looks identical to a hang and previously could last hours
// before the user noticed.
//
// Heartbeats carry no semantic content; consumers should update only the
// last-activity timestamp / "still alive" indicator and must not append a
// visible block per heartbeat.
type HeartbeatUpdate struct {
	ElapsedMs int64  // Milliseconds since the heartbeat-emitting operation started
	Source    string // Free-form label for the emitter, e.g. "delegate_task.sync"
	Sequence  uint64 // SDK-assigned sequence for ordering (emission-time)
}

func (u HeartbeatUpdate) UpdateType() string { return "heartbeat" }

// SubAgentCompleteUpdate is emitted exactly once when a synchronous sub-agent
// finishes execution. It carries the final aggregated stats so consumers can
// collapse the live transcript into a summary line ("Done (N tool uses   M
// tokens   Xs)") and stop showing the spinner.
//
// This is distinct from the inner AssistantMessageUpdate fired per turn:
// SubAgentCompleteUpdate is the terminal event for the entire sub-agent run.
// It is typically wrapped in agent.SubAgentUpdate so consumers can route it
// to the correct sub-agent display block by AgentID.
type SubAgentCompleteUpdate struct {
	TurnCount    int           // Total provider turns the sub-agent took
	ToolUseCount int           // Total tool calls the sub-agent made
	TokensUsed   int           // Total tokens consumed
	Duration     time.Duration // Total wall-clock time
	FinalMessage string        // Final assistant text (size-capped upstream)
	Error        string        // Non-empty when execution failed
	Sequence     uint64
}

func (u SubAgentCompleteUpdate) UpdateType() string { return "sub_agent_complete" }

// IntermediateCallback is called during execution to provide real-time updates.
type IntermediateCallback func(ctx context.Context, update IntermediateUpdate) error

// HookResult contains details about a hook execution for UI display.
type HookResult struct {
	HookName string // Name of the hook
	Success  bool   // Whether the hook succeeded
	Output   string // Hook output (shown in TUI as BlockHook)
	Blocked  bool   // Whether hook blocked execution
	Error    string // Error message if any
	// AdditionalContext is text to append to the current message (user message for
	// UserPromptSubmit, tool result for PostToolUse) so the model receives it as
	// part of normal conversation history — never the system prompt.
	AdditionalContext string
	// Timing and execution details.
	Duration          time.Duration
	ExitCode          int
	MatchedPattern    string
	TimeoutConfigured time.Duration
	WorkingDir        string
	// Metadata carries structured data surfaced by the hook (e.g. an auto-mode
	// suggestion or a recap summary) for UI consumers.
	Metadata map[string]any
}

// HooksManager interface for emitting hook events during tool execution.
// This allows hooks to be triggered before/after tool execution without importing the hooks package.
type HooksManager interface {
	// EmitToolBeforeExecute emits an event before a tool is executed.
	// Returns hook results for UI display and an error if a hook blocks the execution.
	EmitToolBeforeExecute(ctx context.Context, toolName string, params map[string]any) ([]HookResult, error)
	// EmitToolAfterExecute emits an event after a tool is executed.
	// Returns hook results for UI display.
	EmitToolAfterExecute(ctx context.Context, toolName string, params map[string]any, result any, err error) []HookResult
	// EmitProviderResponse emits a provider.after_response event with latency and token data.
	// Called after each provider round-trip so bronze captures per-request timing and model usage.
	EmitProviderResponse(ctx context.Context, providerName, model string, inputTokens, outputTokens int, durationMs int64)
}

// AutoCompactionThresholdMode describes how an automatic compaction threshold
// is interpreted. The agent resolves the descriptor against the active model at
// request time so provider/model switches cannot leave a stale token threshold.
type AutoCompactionThresholdMode string

const (
	AutoCompactionThresholdPercent     AutoCompactionThresholdMode = "percent"
	AutoCompactionThresholdFixedTokens AutoCompactionThresholdMode = "fixed_tokens"
)

// AutoCompactionThreshold is the provider-independent trigger descriptor.
// Percent values are fractions in (0, 1]; fixed-token values are positive
// absolute input-context token counts.
type AutoCompactionThreshold struct {
	Mode  AutoCompactionThresholdMode
	Value float64
}

// AutoCompactionConfig configures automatic context compaction for agents.
// When enabled, the agent will automatically compact the conversation context
// when it approaches the context window limit.
type AutoCompactionConfig struct {
	// EnableAutoCompaction enables automatic compaction when context approaches limit.
	EnableAutoCompaction bool
	// Threshold is resolved by the agent against the active model's real context
	// window. This is the canonical threshold contract for new callers.
	Threshold AutoCompactionThreshold
	// Deprecated: use Threshold with Mode=percent.
	// AutoCompactionThresholdPercent was the percentage of context window usage (0.0-1.0)
	// that triggered automatic compaction. It remains a compatibility fallback.
	AutoCompactionThresholdPercent float64
	// Deprecated: use Threshold with Mode=fixed_tokens. Kept so SDK clients
	// compiled against the partial absolute-token migration continue to work.
	AutoCompactThresholdTokens int
	// ContinueIfRunning determines whether to continue the conversation automatically
	// after compaction completes, without requiring user input.
	ContinueIfRunning bool

	// CompactFunc is called by the agent when the compaction threshold is crossed or
	// the blocking limit is reached. It receives the current messages slice and the
	// compaction context (containing file access records, todos, mode state, etc.)
	// and returns the compacted replacement slice. If nil, no in-agent compaction is
	// performed; the agent falls back to emitting CompactionNeededUpdate (backward compatible).
	CompactFunc func(ctx context.Context, messages []*conversation.Message, compCtx *compaction.CompactionContext) ([]*conversation.Message, error)
}

// DefaultAutoCompactionConfig returns sensible defaults for auto-compaction.
func DefaultAutoCompactionConfig() AutoCompactionConfig {
	return AutoCompactionConfig{
		EnableAutoCompaction:           false,
		AutoCompactionThresholdPercent: 0.9,
		ContinueIfRunning:              true,
	}
}

// Agent represents a running agent instance that can execute tasks.
// It combines a definition with runtime state and dependencies.
type Agent struct {
	// Immutable configuration
	definition       *Definition
	provider         provider.Provider
	providerRegistry *provider.SimpleRegistry // Feature 026: needed for pool provider switching
	chain            *fallback.Chain          // Feature 026
	noFallback       bool                     // When true, only the chain primary is attempted (no fallbacks)
	toolReg          tools.Registry
	credentialStore  *CredentialStore // Multi-credential rotation store
	visionRouter     *VisionRouter    // Routes vision-requiring operations to vision-capable model

	logger                    observability.Logger
	tracer                    observability.Tracer
	auditor                   observability.Auditor
	messageCallback           MessageCallback      // Optional callback to save messages
	intermediateCallback      IntermediateCallback // Optional callback for real-time updates
	compactionPersistCallback CompactionPersistCallback
	hooksManager              HooksManager        // Optional hooks manager for event emission
	toolChangeHandler         *ToolChangeHandler  // Handler for tool availability changes
	toolFilter                ToolFilterFunc      // Optional filter for deferred tool loading
	messageInjector           MessageInjector     // Optional callback to inject user messages between turns
	richMessageInjector       RichMessageInjector // Optional callback to inject full canonical messages between turns
	// DEPRECATED: Ephemeral system prompt injection breaks cache, is invisible,
	// and not persistent. Use hooks that return ContinueWithMessage() instead.
	ephemeralSystemFn           func([]*conversation.Message) string // Optional: returns text appended to system prompt per-request only (never stored)
	autoCompactionConfig        AutoCompactionConfig                 // Configuration for automatic context compaction
	completionConfirm           bool                                 // Ask the model to verify once more before returning completion
	completionConfirmMax        int                                  // Maximum completion-verification prompts per execution
	proactiveSummarizeThreshold float64                              // Optional earlier auto-compaction threshold as a context fraction
	microCompactor              *compaction.MicroCompactor           // Per-turn deterministic tool result trimming
	compactionService           *compaction.Service                  // Full compaction service (LLM summarization + restoration)
	fileTracker                 *filetracker.Recorder                // File access tracking for compaction recovery
	taskStore                   *taskstore.Store                     // Persistent task storage
	storagePath                 string                               // Base path for conversation storage (for TaskStore)
	workspacePath               string                               // Workspace root for A2A scope (distinct from storagePath)
	isHeadless                  bool                                 // True when running without UI (prevents interactive prompts)
	browserFamily               chrome.FamilyContext                 // Immutable inherited browser authority (zero for an unbound root)

	// Runtime state (protected by mutex)
	mu                  sync.RWMutex
	state               State
	conversationID      string
	memory              Memory
	turnCount           int
	toolCallsTotal      int    // total tool calls executed in the current Execute() lifecycle
	updateSequence      uint64 // monotonic sequence counter for ordering IntermediateUpdates (SDK→TUI)
	startedAt           time.Time
	lastActivityAt      time.Time
	inputTokens         int
	outputTokens        int
	lastRequestEstimate int
	// totalTokens is intentionally removed — it accumulated resp.Usage.Total across turns,
	// which double-counts because each turn's input_tokens already encodes all prior context.
	// Use Stats().TotalTokens which computes inputTokens + outputTokens on demand.
	totalCost        float64
	requestContext   map[string]any // Context from current ExecuteRequest
	responseMetadata map[string]any // Metadata from last provider response (includes cache metrics)

	// Per-request overrides captured from ExecuteRequest in execute() and
	// cleared on return. These shape a single turn without mutating the agent
	// definition, mirroring how requestContext is handled.
	reqSystemPromptOverride string // non-empty replaces definition.SystemPrompt for this turn
	reqDisableTools         bool   // true offers no tools to the provider for this turn
	reqDisableHooks         bool   // true skips before/after tool hook emission for this turn

	// observationalHooks is the blocking-INCAPABLE hook surface used by
	// sub-agent and background runs, which have no hooksManager by design.
	// See internal/agent/observational.go. Nil means "emits nothing"; there is
	// deliberately no separate enabled bool for this feature to lie about.
	observationalHooks ObservationalHooks

	// Execution control
	ctx    context.Context
	cancel context.CancelFunc

	// configuredContextWindow is set explicitly from the user's model config.
	// It takes priority over anything the provider's Capabilities() returns.
	// 0 means not configured — fall back to provider caps.
	configuredContextWindow int

	// initialized is set to true after the first successful Initialize() call.
	// Subsequent calls to Initialize() are no-ops (idempotent).
	initialized bool

	// Managed execution state (for ExecutionModeManaged)
	managedClient      *managed.Client // Reusable managed client
	managedSessionID   string          // Current session ID
	managedSessionPort int             // Port for the current session

	// A2A runtime state (optional).
	a2aRuntime *a2a.Runtime

	// promptTraceWriter, when set, receives a [PROMPT PROVENANCE] banner just
	// before each provider call. The banner is rendered from the
	// prompttrace.Collector attached to the per-turn context. When the writer
	// is nil the banner is suppressed entirely — including the cost of
	// attaching the collector — so production paths pay nothing.
	//
	// Set via SetPromptTraceWriter() from a caller that knows where stderr is
	// (typically the same writer used for the provider's RawDebugWriter).
	promptTraceWriter io.Writer

	// promptTraceSeedFn, when non-nil, is called by execute() right after
	// the per-request collector is attached to the context. It receives the
	// freshly-attached context so the caller can replay startup-time
	// contributions (skills injection, dream contract injection, headless
	// custom prompt, etc.) into the collector. Without this, those
	// startup-time sections would never appear in the banner because they
	// ran long before any per-request ctx existed.
	promptTraceSeedFn func(ctx context.Context)

	// traceCtx is the per-request context with a prompttrace.Collector
	// attached. execute() sets it before invoking executeLoop so that
	// buildProviderRequest can reach the collector without a signature
	// change (the existing signature is exercised by a race test and by
	// other callers). It is reset to nil when the request returns.
	traceCtx context.Context
}

// State represents the current execution state of an agent.
type State string

const (
	// StateIdle indicates the agent is ready but not executing.
	StateIdle State = "idle"

	// StateExecuting indicates the agent is actively processing a message.
	StateExecuting State = "executing"

	// StateStopped indicates the agent has been stopped.
	StateStopped State = "stopped"

	// StateError indicates the agent encountered a fatal error.
	StateError State = "error"
)

// ExecuteRequest contains parameters for executing an agent.
type ExecuteRequest struct {
	// Message is the user's input message.
	Message string

	// ConversationID links this execution to a conversation.
	// If empty, a new conversation will be created.
	ConversationID string

	// ConversationHistory contains the full conversation history.
	// If provided, the agent will use this as context for the current message.
	// If empty, agent will start with just the current message.
	ConversationHistory []*conversation.Message

	// Context provides additional context beyond conversation history.
	// This can include data from other agents, mode state, etc.
	Context map[string]any

	// MaxTurns limits the number of turns this execution can take.
	// 0 means use agent's default MaxTurns.
	MaxTurns int

	// Timeout limits total execution time.
	// 0 means use agent's default timeout.
	Timeout time.Duration

	// StoragePath is the base directory for conversation storage.
	// If empty, defaults to ~/.swarm/conversations/{ConversationID}/metadata
	// This is used for persisting tasks and compaction state.
	StoragePath string

	// SystemPromptOverride, when non-empty, replaces the agent definition's
	// system prompt for THIS execution only. The agent's stored definition is
	// not mutated, so concurrent/subsequent turns keep the configured prompt.
	// Use this for per-turn behaviour shaping (e.g. a daemon caller pinning a
	// one-shot instruction) without persisting a SetSystemPrompt change.
	SystemPromptOverride string

	// DisableTools, when true, runs this execution with NO tools offered to the
	// provider. The model cannot call tools; the turn becomes a single
	// text-only completion. The agent's tool registry is left untouched.
	DisableTools bool

	// DisableHooks, when true, skips before/after tool hook emission for THIS
	// execution. The agent's hooks manager is left attached for other turns.
	DisableHooks bool
}

// ExecuteResponse contains the result of an agent execution.
type ExecuteResponse struct {
	// Message is the agent's final response.
	Message string

	// ConversationID is the ID of the conversation this execution belongs to.
	ConversationID string

	// TurnCount is the number of turns taken (provider calls).
	TurnCount int

	// InputTokens is the total input tokens consumed.
	InputTokens int

	// OutputTokens is the total output tokens consumed.
	OutputTokens int

	// TokensUsed is the total tokens consumed.
	TokensUsed int

	// CostUSD is the estimated cost in USD.
	CostUSD float64

	// Duration is how long execution took.
	Duration time.Duration

	// FinishReason indicates why execution stopped.
	FinishReason provider.FinishReason

	// Metadata contains additional execution metadata.
	Metadata map[string]any
}

// Config holds all parameters for creating a new Agent.
//
// Required fields: Definition, Provider, ToolRegistry.
// All other fields default to noop/zero values and can be omitted for simple use cases.
//
// Construction-time vs. runtime configuration:
//   - Fields in Config are applied once at construction and become the agent's defaults.
//   - A small number of fields (Model, Provider, CredentialStore) can still be updated
//     at runtime via the corresponding Set* methods when the agent needs to switch mid-flight.
//   - Per-request callbacks (MessageCallback, IntermediateCallback, MessageInjector) are
//     intentionally NOT in Config — they are set per-execution and cleared after each call.
type Config struct {
	// ── Required ──────────────────────────────────────────────────────────────

	// Definition describes the agent (required).
	Definition *Definition
	// Provider is the LLM provider to use (required).
	Provider provider.Provider
	// ToolRegistry holds the tools available to this agent (required).
	// If nil and Tools is provided, a new empty registry is created automatically.
	ToolRegistry tools.Registry

	// Tools is a convenience list of tools to register at construction time.
	// When set, each tool is registered in ToolRegistry before the agent starts.
	// If ToolRegistry is nil, a new registry is created to hold them.
	// This removes the need to call ag.ToolRegistry().Register(t) after creation.
	//
	// Example:
	//
	//	ag, err := agent.New(agent.Config{
	//	    Provider: p,
	//	    Model:    "claude-sonnet-4-5",
	//	    Tools: []tools.Tool{
	//	        tools.Func[SearchParams]("search", "Search docs", searchFn),
	//	        tools.Typed[ReadParams](&ReadTool{}),
	//	    },
	//	})
	Tools []tools.Tool

	// ── Observability (all nil-safe; defaults to noop) ─────────────────────

	// Logger for structured log output. Defaults to a noop logger when nil.
	Logger observability.Logger
	// Tracer for distributed tracing. Defaults to a noop tracer when nil.
	Tracer observability.Tracer
	// Auditor for audit events. Defaults to a noop auditor when nil.
	Auditor observability.Auditor

	// ── Optional infrastructure ────────────────────────────────────────────

	// ProviderRegistry is needed for fallback-chain pool switching. Optional.
	ProviderRegistry *provider.SimpleRegistry

	// HooksManager enables before/after-tool hook event emission. Optional.
	HooksManager HooksManager

	// ToolFilter restricts which tools are sent to the provider per-request.
	// Useful for deferred/mode-filtered tool loading. Optional.
	ToolFilter ToolFilterFunc

	// DEPRECATED: Ephemeral system prompt injection breaks cache, is invisible,
	// and not persistent. Use hooks that return ContinueWithMessage() instead.
	// Kept for backward compatibility — no new code should use this.
	EphemeralSystemFn func([]*conversation.Message) string

	// AutoCompaction configures automatic context compaction. Optional.
	// Use DefaultAutoCompactionConfig() for sensible defaults.
	AutoCompaction AutoCompactionConfig

	// CompletionConfirm asks the model to verify the actual state before a
	// natural completion is returned. CompletionConfirmMax defaults to 1.
	CompletionConfirm    bool
	CompletionConfirmMax int

	// ProactiveSummarizeThreshold optionally lowers the existing automatic
	// compaction threshold. Values are fractions of the model context window.
	ProactiveSummarizeThreshold float64

	// CompactionService provides LLM-based conversation summarization. Optional.
	CompactionService *compaction.Service

	// FileTracker records file access for compaction recovery. Optional.
	// If nil, a fresh recorder is created automatically.
	FileTracker *filetracker.Recorder

	// TaskStore provides persistent task/todo storage. Optional.
	TaskStore *taskstore.Store

	// StoragePath is the base directory for conversation storage.
	// Used for A2A registry and other persistent state. Optional.
	StoragePath string

	// WorkspacePath is the workspace root path for A2A scope.
	// When set, this is used by EnableA2A for runtime initialization.
	// If not set, StoragePath is used as fallback. Optional.
	WorkspacePath string

	// A2A enables A2A-native peer communication for top-level agents. Optional.
	A2A *a2a.Config

	// CodeModeInstaller is an optional interface for installing code mode.
	// When provided, the agent will wrap its tool registry with code mode,
	// allowing the LLM to execute JavaScript code that orchestrates multiple
	// tool calls in parallel via Promise.all().
	//
	// Use codemode.Config to configure code mode options (timeout, selector, etc).
	// Example:
	//
	//	import "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/codemode"
	//	cfg := agent.Config{
	//	    Provider: p,
	//	    Model:    "claude-sonnet-4-5",
	//	    CodeModeInstaller: codemode.NewFromConfig(&codemode.Config{
	//	        Enabled: true,
	//	        Timeout: 30 * time.Second,
	//	    }),
	//	}
	CodeModeInstaller CodeModeInstaller
	// BrowserFamily is trusted runtime authority inherited by child agents.
	// It is never populated from provider requests, tool parameters, agent
	// definitions, or metadata. Root agents may remain unbound and receive
	// authority through the Go context for each top-level execution.
	BrowserFamily chrome.FamilyContext
}

// New creates a new agent instance.
// The agent is initialized but not started — call Initialize() before executing.
func New(cfg Config) (*Agent, error) {
	def := cfg.Definition
	prov := cfg.Provider
	provReg := cfg.ProviderRegistry
	toolReg := cfg.ToolRegistry
	logger := cfg.Logger
	tracer := cfg.Tracer
	auditor := cfg.Auditor

	// Apply noop defaults so callers don't have to wire up observability infrastructure.
	if logger == nil {
		logger = noop.NewLogger()
	}
	if tracer == nil {
		tracer = noop.NewTracer()
	}
	if auditor == nil {
		auditor = noop.NewAuditor()
	}

	// Auto-create a tool registry when cfg.Tools is provided but no registry is given.
	// This lets callers use Config{Tools: [...]} without also constructing a registry.
	if toolReg == nil {
		toolReg = tools.NewRegistry()
	}

	// Register tools declared in cfg.Tools into the registry.
	// This is the construction-time alternative to calling ag.ToolRegistry().Register(t)
	// after the agent is created.
	for _, t := range cfg.Tools {
		if err := toolReg.Register(t); err != nil {
			return nil, fmt.Errorf("agent.New: registering tool %q: %w", t.Name(), err)
		}
	}

	// Apply code mode if an installer is provided.
	// Code mode wraps the tool registry to enable JavaScript-based tool orchestration.
	// When enabled, selected tools are sandboxed inside a run_code tool,
	// allowing the LLM to batch multiple tool calls in parallel.
	if cfg.CodeModeInstaller != nil {
		// Create an executor from the registry for tool dispatch
		exec := tools.NewExecutor(toolReg, nil)
		wrappedReg, err := cfg.CodeModeInstaller.Install(toolReg, exec)
		if err != nil {
			return nil, fmt.Errorf("agent.New: installing code mode: %w", err)
		}
		toolReg = wrappedReg
	}

	// Validate definition
	if err := def.Validate(); err != nil {
		return nil, sdkerr.Permanent("agent.invalid_definition", err.Error())
	}

	// Validate provider matches definition.
	// When a fallback chain is set, the chain handles provider routing dynamically
	// (creating providers from the registry), so the initial provider may differ
	// from the definition's declared provider — this is expected.
	//
	// Prefer the injected registry's instance-scoped ProvidersMatch so that
	// runtime-registered compatibility (custom OpenAI-compatible providers, etc.)
	// is honored. When no registry was supplied, fall back to the built-in
	// (static) compatibility families via the package-level helper.
	if def.Chain == nil {
		matched := false
		if provReg != nil {
			matched = provReg.ProvidersMatch(def.Provider, prov.Name())
		} else {
			matched = provider.ProvidersMatch(def.Provider, prov.Name())
		}
		if !matched {
			return nil, sdkerr.Permanent("agent.provider_mismatch",
				fmt.Sprintf("definition requires provider '%s' but got '%s'", def.Provider, prov.Name()))
		}
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Create tool change handler
	toolChangeHandler := NewToolChangeHandler(def.Name)

	// Use provided FileTracker or auto-create one.
	fileTracker := cfg.FileTracker
	if fileTracker == nil {
		fileTracker = filetracker.NewRecorder()
	}

	// Initialize vision router if agent has vision support configured.
	// This allows non-vision primary models to route vision-requiring operations
	// (like reading image files) to a separate vision-capable model.
	var visionRouter *VisionRouter
	if def.HasVisionSupport() {
		routerCfg := VisionRouterConfig{
			VisionModel:      def.VisionModel,
			VisionChain:      def.VisionChain,
			ProviderRegistry: provReg,
			Logger:           logger,
		}
		var err error
		visionRouter, err = NewVisionRouter(routerCfg)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create vision router: %w", err)
		}
	}

	completionConfirmMax := cfg.CompletionConfirmMax
	if completionConfirmMax <= 0 {
		completionConfirmMax = 1
	}

	agent := &Agent{
		definition:       def,
		provider:         prov,
		providerRegistry: provReg,
		chain:            def.Chain, // Feature 026
		toolReg:          toolReg,
		visionRouter:     visionRouter,

		logger:            logger,
		tracer:            tracer,
		auditor:           auditor,
		state:             StateIdle,
		ctx:               ctx,
		cancel:            cancel,
		toolChangeHandler: toolChangeHandler,
		microCompactor:    compaction.NewMicroCompactor(),
		fileTracker:       fileTracker,

		// Apply construction-time optional configuration from Config.
		// All fields are nil/zero-safe: they remain unset when not provided.
		hooksManager:                cfg.HooksManager,
		toolFilter:                  cfg.ToolFilter,
		ephemeralSystemFn:           cfg.EphemeralSystemFn,
		autoCompactionConfig:        cfg.AutoCompaction,
		completionConfirm:           cfg.CompletionConfirm,
		completionConfirmMax:        completionConfirmMax,
		proactiveSummarizeThreshold: cfg.ProactiveSummarizeThreshold,
		compactionService:           cfg.CompactionService,
		taskStore:                   cfg.TaskStore,
		storagePath:                 cfg.StoragePath,
		workspacePath:               cfg.WorkspacePath,
		browserFamily:               cfg.BrowserFamily,
	}

	if cfg.A2A != nil {
		a2aCfg := cfg.A2A.Clone()
		if a2aCfg.RegistryPath == "" {
			// The A2A registry is a single global file under ~/.swarm; the
			// former per-workspace split is gone. Workspace separation is
			// handled via scope_key filtering inside the registry.
			a2aCfg.RegistryPath = paths.A2ARegistryFile()
		}
		if err := os.MkdirAll(filepath.Dir(a2aCfg.RegistryPath), 0o755); err != nil {
			cancel()
			return nil, fmt.Errorf("agent.New: create A2A state dir: %w", err)
		}
		a2aCfg.Descriptor = agentDescriptorForA2A(def)
		runtime, err := a2a.NewRuntime(*a2aCfg, nil, logger, tracer, func(ctx context.Context, msg *conversation.Message) (bool, error) {
			agent.mu.RLock()
			callback := agent.messageCallback
			agent.mu.RUnlock()
			if callback == nil {
				return false, nil
			}
			if err := callback(ctx, msg); err != nil {
				return false, err
			}
			return true, nil
		})
		if err != nil {
			cancel()
			return nil, fmt.Errorf("agent.New: create A2A runtime: %w", err)
		}
		runtime.SetRequestHandler(func(ctx context.Context, req *a2a.InboundRequest) (*conversation.Message, error) {
			conversationID := req.ConversationID
			if conversationID == "" {
				conversationID = agent.conversationID
			}
			inbound := req.ProjectedMessage.Clone()
			if inbound.Metadata == nil {
				inbound.Metadata = make(map[string]any)
			}
			inbound.Metadata["conversation_id"] = conversationID
			inbound.Metadata[conversation.A2APersistedMetadataKey] = true
			agent.mu.RLock()
			callback := agent.messageCallback
			agent.mu.RUnlock()
			if callback != nil {
				if err := callback(ctx, inbound); err != nil {
					return nil, err
				}
			}
			response, err := agent.ExecuteWhenIdle(ctx, ExecuteRequest{
				ConversationID:      conversationID,
				ConversationHistory: []*conversation.Message{inbound.Clone()},
			})
			if err != nil {
				return nil, err
			}
			return &conversation.Message{
				ID:        ensureMessageID(""),
				Timestamp: time.Now().UTC(),
				Role:      conversation.RoleAssistant,
				Content:   response.Message,
				AgentID:   agent.definition.ID,
				Provider:  agent.definition.Provider,
				Model:     agent.definition.Model,
			}, nil
		})
		// Keep the A2A runtime independent from model-facing tool exposure.
		// Callers that need outbound A2A tools can explicitly register
		// runtime.BuiltinTools() in their tool registry.
		agent.a2aRuntime = runtime
		agent.richMessageInjector = runtime.TakePendingMessages
		if err := runtime.Start(context.Background()); err != nil {
			runtime.Stop()
			cancel()
			return nil, fmt.Errorf("agent.New: start A2A runtime: %w", err)
		}
	}

	// Register handler as listener if registry supports it
	if observableReg, ok := toolReg.(tools.ObservableRegistry); ok {
		observableReg.AddListener(toolChangeHandler)
	}

	// Initialize eagerly so New() returns a ready-to-use agent.
	// Callers that previously called Initialize() manually will get a no-op.
	if err := agent.Initialize(); err != nil {
		agent.cancel()
		return nil, err
	}

	return agent, nil
}

// SetMessageCallback sets a callback function that will be called whenever the agent generates a message.
// This allows messages to be saved to external storage (e.g., conversation manager).

// EnableA2A creates and starts the A2A runtime with the given handle.
// If A2A is already enabled, this returns an error.
func (a *Agent) EnableA2A(ctx context.Context, handle string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.a2aRuntime != nil {
		return fmt.Errorf("A2A is already enabled")
	}

	// Build A2A config from available agent state
	workspacePath := a.workspacePath
	if workspacePath == "" {
		workspacePath = a.storagePath
	}
	cfg := a2a.Config{
		Handle:        handle,
		SessionID:     a.conversationID, // Use conversation ID as session ID
		WorkspacePath: workspacePath,    // Use workspace path (with fallback to storage)
		ListenAddress: ":0",             // Auto-assign port
	}

	// Create the runtime
	runtime, err := a2a.NewRuntime(cfg, nil, a.logger, a.tracer, nil)
	if err != nil {
		return fmt.Errorf("failed to create A2A runtime: %w", err)
	}

	// Start the runtime
	if err := runtime.Start(ctx); err != nil {
		return fmt.Errorf("failed to start A2A runtime: %w", err)
	}

	a.a2aRuntime = runtime
	return nil
}

// EnableA2AWithHub creates and starts the A2A runtime using an in-memory
// SimpleBackend backed by the provided WorkspaceHub instead of SQLite.
// The hub must already be started (StartOrConnect called) before this.
func (a *Agent) EnableA2AWithHub(ctx context.Context, handle string, hub *a2a.WorkspaceHub) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.a2aRuntime != nil {
		return fmt.Errorf("A2A is already enabled")
	}

	workspacePath := a.workspacePath
	if workspacePath == "" {
		workspacePath = a.storagePath
	}

	// Build in-memory backend backed by the workspace hub
	backend := a2a.NewSimpleBackend(hub, workspacePath, handle, a.conversationID)

	cfg := a2a.Config{
		Handle:        handle,
		SessionID:     a.conversationID,
		WorkspacePath: workspacePath,
		ListenAddress: ":0", // Auto-assign port
	}

	runtime, err := a2a.NewRuntime(cfg, backend, a.logger, a.tracer, nil)
	if err != nil {
		return fmt.Errorf("failed to create A2A runtime: %w", err)
	}

	if err := runtime.Start(ctx); err != nil {
		return fmt.Errorf("failed to start A2A runtime: %w", err)
	}

	// Register self in the backend so ListPeers includes this instance
	self := a2a.PeerIdentity{
		Handle:        handle,
		SessionID:     a.conversationID,
		WorkspacePath: workspacePath,
		ScopeKey:      a2a.NormalizeScopeKey("", workspacePath),
	}
	if _, err := backend.RegisterPeer(ctx, self, 3600); err != nil {
		a.logger.Warn(ctx, "agent.enable_a2a_hub.register_self_failed",
			observability.F("error", err.Error()))
	}

	a.a2aRuntime = runtime
	return nil
}

// DisableA2A stops the A2A runtime if it's running.
func (a *Agent) DisableA2A(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.a2aRuntime == nil {
		return fmt.Errorf("A2A is not enabled")
	}

	if err := a.a2aRuntime.Close(); err != nil {
		return fmt.Errorf("failed to stop A2A runtime: %w", err)
	}

	a.a2aRuntime = nil
	return nil
}

// Close releases resources associated with the agent, including stopping any
// active managed execution sessions. It should be called when the agent is
// no longer needed to ensure proper cleanup.
func (a *Agent) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Stop managed session if running
	if a.managedClient != nil && a.managedSessionID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := a.managedClient.StopSession(ctx, a.managedSessionID); err != nil {
			a.logger.Warn(ctx, "agent.close.session_stop_failed",
				observability.F("session_id", a.managedSessionID),
				observability.F("error", err.Error()))
		} else {
			a.logger.Info(ctx, "agent.close.session_stopped",
				observability.F("session_id", a.managedSessionID))
		}

		a.managedClient = nil
		a.managedSessionID = ""
		a.managedSessionPort = 0
	}

	// Cancel the agent's context
	a.cancel()

	if a.a2aRuntime != nil {
		if err := a.a2aRuntime.Close(); err != nil {
			a.logger.Warn(context.Background(), "agent.close.a2a_stop_failed",
				observability.F("error", err.Error()))
		}
		a.a2aRuntime = nil
	}

	return nil
}

// nextUpdateSequence returns the next monotonic sequence number for ordering
// IntermediateUpdates from SDK → TUI. This ensures proper rendering order
// regardless of transit delays through channels or network.
// The sequence is assigned at emission time in the SDK, not at reception time
// in the TUI, which prevents pre-tool hooks from appearing after tool results.
func (a *Agent) nextUpdateSequence() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.updateSequence++
	return a.updateSequence
}
