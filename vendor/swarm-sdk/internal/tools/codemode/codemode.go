// Package codemode provides code-mode execution for the swarm-sdk.
//
// Code mode wraps multiple tools into a single run_code tool, allowing the
// model to orchestrate tool calls with JavaScript code instead of one model
// round-trip per tool call. This enables:
//   - Batching: Multiple tool calls in one model turn via Promise.all()
//   - Local computation: Filter, transform, aggregate results in code
//   - Compact history: Fewer messages in conversation context
//
// Usage:
//
//	cm := codemode.New(codemode.WithSelector(codemode.AllTools()))
//	wrappedRegistry, err := cm.Install(registry, executor)
//	if err != nil {
//	    // handle error
//	}
//	// Use wrappedRegistry with the agent
package codemode

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/codemode/sandbox"
)

// CodeMode is the main configuration struct for code mode.
// It manages tool selection, sandbox creation, and the run_code tool.
type CodeMode struct {
	// selector determines which tools are sandboxed inside run_code.
	selector Selector

	// maxRetries is the maximum number of retries on syntax errors.
	maxRetries int

	// timeout is the maximum execution time per run_code call.
	timeout time.Duration

	// persist determines whether REPL state persists across calls.
	persist bool

	// sandboxFactory creates new sandboxes for each conversation.
	sandboxFactory sandbox.Factory

	// sandboxes maps conversationID -> *conversationSandbox.
	sandboxes sync.Map

	// nameMapping maps sanitized JS names to original tool names.
	nameMapping map[string]string
}

// Option configures a CodeMode instance.
type Option func(*CodeMode)

// WithSelector sets the tool selector.
func WithSelector(s Selector) Option {
	return func(cm *CodeMode) {
		cm.selector = s
	}
}

// WithMaxRetries sets the maximum number of retries.
func WithMaxRetries(n int) Option {
	return func(cm *CodeMode) {
		cm.maxRetries = n
	}
}

// WithTimeout sets the execution timeout.
func WithTimeout(d time.Duration) Option {
	return func(cm *CodeMode) {
		cm.timeout = d
	}
}

// WithPersist sets whether REPL state persists across calls.
func WithPersist(p bool) Option {
	return func(cm *CodeMode) {
		cm.persist = p
	}
}

// WithSandboxFactory sets a custom sandbox factory.
func WithSandboxFactory(f sandbox.Factory) Option {
	return func(cm *CodeMode) {
		cm.sandboxFactory = f
	}
}

// New creates a new CodeMode instance with the given options.
func New(opts ...Option) *CodeMode {
	cm := &CodeMode{
		selector:       AllTools{},
		maxRetries:     3,
		timeout:        60 * time.Second,
		persist:        true,
		sandboxFactory: func() sandbox.Sandbox { return sandbox.NewGojaSandbox() },
		nameMapping:    make(map[string]string),
	}
	for _, opt := range opts {
		opt(cm)
	}
	return cm
}

// conversationSandbox holds per-conversation sandbox state.
type conversationSandbox struct {
	sandbox sandbox.Sandbox
	tracer  *TraceCollector
}

// Install wraps a registry to enable code mode.
// It hides selector-matched tools and registers the run_code tool.
// The returned registry should be used with the agent instead of the original.
//
// The executor is used to dispatch tool calls from inside the sandbox.
// It should have access to all tools that might be sandboxed.
func (cm *CodeMode) Install(registry tools.Registry, executor tools.Executor) (tools.Registry, error) {
	// Create a wrapped registry that hides sandboxed tools
	wrapped := &codeModeRegistry{
		inner:    registry,
		selector: cm.selector,
		codemode: cm,
		executor: executor,
	}

	// Build the list of sandboxed tools and create name mapping
	sandboxedTools := cm.buildSandboxedToolList(registry)

	// Register the run_code tool on the wrapped registry
	runCodeTool := cm.createRunCodeTool(sandboxedTools, executor)

	if err := wrapped.registerRunCode(runCodeTool); err != nil {
		return nil, fmt.Errorf("failed to register run_code tool: %w", err)
	}

	return wrapped, nil
}

// buildSandboxedToolList builds ToolStubInfo for all sandboxed tools.
func (cm *CodeMode) buildSandboxedToolList(registry tools.Registry) []ToolStubInfo {
	var stubs []ToolStubInfo

	for _, name := range registry.List() {
		tool, err := registry.Get(name)
		if err != nil {
			continue
		}

		// Check if this tool should be sandboxed. shouldSandbox folds in
		// the selector, approval-gating, and control-plane exclusion so the
		// visible tool list here can never diverge from the registry views.
		if !cm.shouldSandbox(tool) {
			continue
		}

		// Sanitize the name
		sanitized := SanitizeName(name)
		if sanitized != name {
			cm.nameMapping[sanitized] = name
		}

		// Determine if async
		isAsync := cm.isAsyncTool(tool)

		// Extract parameter schema
		params := cm.extractSchema(tool.Parameters())

		// Extract return schema (may be nil)
		returns := cm.extractReturnSchema(tool)

		stubs = append(stubs, ToolStubInfo{
			Name:         sanitized,
			OriginalName: name,
			Description:  tool.Description(),
			IsAsync:      isAsync,
			Parameters:   params,
			Returns:      returns,
		})
	}

	return stubs
}

// requiresApproval checks if a tool requires approval or deferred execution.
func (cm *CodeMode) requiresApproval(tool tools.Tool) bool {
	// Check for permissioned tools that require approval
	if pt, ok := tool.(interface {
		RequiresApproval() bool
	}); ok && pt.RequiresApproval() {
		return true
	}

	// Check for deferred tools
	if dt, ok := tool.(interface {
		IsDeferred() bool
	}); ok && dt.IsDeferred() {
		return true
	}

	return false
}

// shouldSandbox is the single source of truth for whether a tool is wrapped
// inside run_code. A tool is sandboxed only if it matches the selector AND is
// neither approval-gated nor a control-plane tool. Centralizing the decision
// here keeps buildSandboxedToolList and the wrapped-registry's List/Get/
// Execute/IsRegistered paths perfectly consistent — a mismatch there is what
// makes a tool "vanish" (hidden from the LLM but still refused when called).
func (cm *CodeMode) shouldSandbox(tool tools.Tool) bool {
	if !cm.selector.Match(AdaptTool(tool)) {
		return false
	}
	if cm.requiresApproval(tool) {
		return false
	}
	if isControlPlaneTool(tool.Name()) {
		return false
	}
	return true
}

// isControlPlaneTool reports whether a tool governs the agent's own execution
// lifecycle rather than doing task work. These MUST remain directly callable
// (never hidden behind run_code) because the enforcement hooks that gate
// run_code are only satisfiable by calling them:
//
//   - Task tools (task_create/update): the task-enforcement hook blocks every
//     non-exempt tool — including run_code — until an in-progress task is
//     focused. If task_create is sandboxed inside run_code, the only way to
//     create a task is a tool that is itself blocked → hard deadlock.
//   - Skill tools (SkillManage/Skill): the autogenskills budget hook hard-
//     blocks after N run_code calls until a skill is created or invoked. If
//     those tools are sandboxed, the escape hatch is blocked → hard deadlock.
//   - Plan-mode + user-interaction tools: exempt in the same hooks and used to
//     resolve the plan/decision tree before any task exists.
//
// Names are normalized (lowercased, underscores stripped) to match both
// snake_case (task_create) and PascalCase (TaskCreate) tool identifiers.
func isControlPlaneTool(toolName string) bool {
	lower := strings.ToLower(strings.ReplaceAll(toolName, "_", ""))
	switch lower {
	case
		// Task management — keep in sync with the task-enforcement hook's exempt set.
		"taskmanage", "taskcreate", "taskupdate", "taskget", "tasklist",
		"todowrite", "todoread",
		// Skill management/invocation — keep in sync with the budget hook's exempt set.
		"skillmanage", "skillinvoke", "skillcall", "useskill", "skillexec", "skill",
		// Plan mode.
		"enterplanmode", "exitplanmode",
		// User interaction (no local state change; drives planning).
		"askuserquestion", "pushagentupdate":
		return true
	}
	return false
}

// isAsyncTool determines if a tool should be called asynchronously.
func (cm *CodeMode) isAsyncTool(tool tools.Tool) bool {
	// Check for ParallelCapable interface
	if pc, ok := tool.(interface {
		SupportsParallel() bool
	}); ok {
		return pc.SupportsParallel()
	}

	// Check optimization hints
	if hinted, ok := tool.(interface {
		OptimizationHints() *tools.OptimizationHints
	}); ok {
		hints := hinted.OptimizationHints()
		if hints != nil && hints.PreferSequential {
			return false
		}
	}

	// Default to async for better batching
	return true
}

// extractSchema converts a Parameters() result to a map[string]any.
func (cm *CodeMode) extractSchema(params any) map[string]any {
	if params == nil {
		return nil
	}
	if m, ok := params.(map[string]any); ok {
		return m
	}
	// Try other common types
	return nil
}

// extractReturnSchema extracts the return schema from a tool.
func (cm *CodeMode) extractReturnSchema(tool tools.Tool) map[string]any {
	// Check for return schema interface
	if rs, ok := tool.(interface {
		ReturnSchema() any
	}); ok {
		return cm.extractSchema(rs.ReturnSchema())
	}
	return nil
}

// createRunCodeTool creates the run_code tool instance.
func (cm *CodeMode) createRunCodeTool(stubs []ToolStubInfo, executor tools.Executor) *RunCodeTool {
	// Create sandbox for the tool
	sb := cm.sandboxFactory()

	// Create tracer
	tracer := NewTraceCollector("")

	// Create dispatch adapter
	dispatch := NewDispatchAdapter(executor, nil, tracer, cm.nameMapping)

	return NewRunCodeTool(stubs, sb, tracer, dispatch, cm.maxRetries)
}

// InstallInPlace registers the run_code tool directly into reg (hidden by
// default) and returns a toggle function. This is the preferred approach for
// the TUI: no wrapped registry is created, so no agent recreation is needed
// on toggle. The same registry instance is used throughout.
//
// toggle(true)  — codemode ON:  unhide run_code, hide all other visible tools.
// toggle(false) — codemode OFF: hide run_code, restore previously hidden tools.
//
// Hidden tools remain executable via Get() / Executor so the JS sandbox can
// still call them. They just don't appear in List() (the LLM tool list).
func (cm *CodeMode) InstallInPlace(reg *tools.SimpleRegistry, exec tools.Executor) (func(bool) error, error) {
	// build+register is called on every toggle-on so run_code always has fresh
	// stubs reflecting the current visible tool set.
	buildAndRegister := func() error {
		stubs := cm.buildSandboxedToolList(reg)
		runCodeTool := cm.createRunCodeTool(stubs, exec)
		_ = reg.Unregister("run_code") // remove stale version if present
		if err := reg.Register(runCodeTool); err != nil {
			return fmt.Errorf("codemode: register run_code: %w", err)
		}
		// Start hidden — becomes visible only when codemode is enabled.
		return reg.HideTool("run_code")
	}

	if err := buildAndRegister(); err != nil {
		return nil, err
	}

	// hiddenByCodeMode tracks which tools we hid so we can restore exactly
	// those on toggle-off (avoids unhiding tools hidden for other reasons).
	var hiddenByCodeMode []string

	toggle := func(enable bool) error {
		if enable {
			// Rebuild run_code with the current visible tool set.
			if err := buildAndRegister(); err != nil {
				return err
			}
			// Make run_code visible.
			if err := reg.UnhideTool("run_code"); err != nil {
				return fmt.Errorf("codemode: unhide run_code: %w", err)
			}
			// Hide every currently visible tool except run_code, tracking
			// each so we can restore them precisely on toggle-off.
			// Control-plane tools (task/skill/plan/user-interaction) stay
			// visible and directly callable: the enforcement hooks that gate
			// run_code are only satisfiable by calling those tools, so hiding
			// them behind the sandbox would deadlock the agent.
			hiddenByCodeMode = nil
			for _, name := range reg.List() {
				if name == "run_code" {
					continue
				}
				if isControlPlaneTool(name) {
					continue
				}
				if err := reg.HideTool(name); err == nil {
					hiddenByCodeMode = append(hiddenByCodeMode, name)
				}
			}
		} else {
			// Restore exactly the tools we hid.
			for _, name := range hiddenByCodeMode {
				_ = reg.UnhideTool(name)
			}
			hiddenByCodeMode = nil
			// Hide run_code again.
			_ = reg.HideTool("run_code")
		}
		return nil
	}

	return toggle, nil
}

// getSandbox returns (or creates) the sandbox for a conversation.
func (cm *CodeMode) getSandbox(conversationID string) *conversationSandbox {
	if !cm.persist {
		// No persistence, always create fresh
		return &conversationSandbox{
			sandbox: cm.sandboxFactory(),
			tracer:  NewTraceCollector(conversationID),
		}
	}

	// Check if exists
	if existing, ok := cm.sandboxes.Load(conversationID); ok {
		return existing.(*conversationSandbox)
	}

	// Create new
	cs := &conversationSandbox{
		sandbox: cm.sandboxFactory(),
		tracer:  NewTraceCollector(conversationID),
	}

	// Store (may race, but that's ok - we'll use one of them)
	actual, _ := cm.sandboxes.LoadOrStore(conversationID, cs)
	return actual.(*conversationSandbox)
}

// CloseSandbox closes the sandbox for a conversation.
func (cm *CodeMode) CloseSandbox(conversationID string) error {
	if existing, ok := cm.sandboxes.LoadAndDelete(conversationID); ok {
		return existing.(*conversationSandbox).sandbox.Close()
	}
	return nil
}

// CloseAll closes all sandboxes.
func (cm *CodeMode) CloseAll() error {
	var lastErr error
	cm.sandboxes.Range(func(key, value any) bool {
		if err := value.(*conversationSandbox).sandbox.Close(); err != nil {
			lastErr = err
		}
		return true
	})
	return lastErr
}

// codeModeRegistry wraps a Registry to hide sandboxed tools and add run_code.
type codeModeRegistry struct {
	inner    tools.Registry
	selector Selector
	codemode *CodeMode
	executor tools.Executor

	mu      sync.RWMutex
	runCode tools.Tool
}

// registerRunCode registers the run_code tool.
func (r *codeModeRegistry) registerRunCode(tool tools.Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.runCode != nil {
		return fmt.Errorf("run_code tool already registered")
	}

	r.runCode = tool
	return nil
}

// Register adds a tool to the registry.
// If the new tool would be sandboxed by the selector, the run_code stub list is
// refreshed so the JS sandbox immediately knows about it — this handles tools
// that are registered after Install() (e.g. MCP servers connecting asynchronously).
func (r *codeModeRegistry) Register(tool tools.Tool) error {
	if err := r.inner.Register(tool); err != nil {
		return err
	}
	// Refresh run_code stubs if the new tool would be sandboxed. shouldSandbox
	// excludes approval-gated and control-plane tools, so those never trigger
	// a refresh and remain directly callable.
	if r.codemode.shouldSandbox(tool) {
		r.refreshRunCode()
	}
	return nil
}

// refreshRunCode rebuilds the run_code tool from the current inner registry.
// Called automatically when new sandboxable tools are registered post-Install.
func (r *codeModeRegistry) refreshRunCode() {
	sandboxedTools := r.codemode.buildSandboxedToolList(r.inner)
	newRunCode := r.codemode.createRunCodeTool(sandboxedTools, r.executor)
	r.mu.Lock()
	r.runCode = newRunCode
	r.mu.Unlock()
}

// Unregister removes a tool from the registry.
func (r *codeModeRegistry) Unregister(name string) error {
	if name == "run_code" {
		r.mu.Lock()
		r.runCode = nil
		r.mu.Unlock()
		return nil
	}
	return r.inner.Unregister(name)
}

// Get retrieves a tool by name.
func (r *codeModeRegistry) Get(name string) (tools.Tool, error) {
	if name == "run_code" {
		r.mu.RLock()
		defer r.mu.RUnlock()
		if r.runCode != nil {
			return r.runCode, nil
		}
		return nil, fmt.Errorf("tool not found: %s", name)
	}

	// Check if hidden
	if r.inner.IsRegistered(name) {
		// Check if this tool is sandboxed (should be hidden)
		tool, err := r.inner.Get(name)
		if err != nil {
			return nil, err
		}
		if r.codemode.shouldSandbox(tool) {
			// Tool is sandboxed - return error as if not found
			return nil, fmt.Errorf("tool not found: %s (hidden by code mode)", name)
		}
	}

	return r.inner.Get(name)
}

// List returns all registered tool names (excluding hidden ones).
func (r *codeModeRegistry) List() []string {
	names := r.inner.List()

	// Filter out sandboxed tools
	var filtered []string
	for _, name := range names {
		tool, err := r.inner.Get(name)
		if err != nil {
			continue
		}
		if !r.codemode.shouldSandbox(tool) {
			filtered = append(filtered, name)
		}
	}

	// Add run_code
	filtered = append(filtered, "run_code")

	return filtered
}

// IsRegistered checks if a tool is registered.
func (r *codeModeRegistry) IsRegistered(name string) bool {
	if name == "run_code" {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.runCode != nil
	}

	tool, err := r.inner.Get(name)
	if err != nil {
		return false
	}

	// Hidden if sandboxed
	if r.codemode.shouldSandbox(tool) {
		return false
	}

	return r.inner.IsRegistered(name)
}

// HideTool marks a tool as hidden.
func (r *codeModeRegistry) HideTool(name string) error {
	return r.inner.HideTool(name)
}

// Execute implements tools.ExecutableRegistry.
func (r *codeModeRegistry) Execute(ctx context.Context, name string, params map[string]any) (*tools.ToolResult, error) {
	if name == "run_code" {
		r.mu.RLock()
		tool := r.runCode
		r.mu.RUnlock()
		if tool == nil {
			return nil, fmt.Errorf("tool not found: %s", name)
		}
		return tool.Execute(ctx, params)
	}

	// For other tools, check if sandboxed
	tool, err := r.inner.Get(name)
	if err != nil {
		return nil, err
	}

	if r.codemode.shouldSandbox(tool) {
		return nil, fmt.Errorf("tool %s is sandboxed and cannot be called directly", name)
	}

	// Use inner's Execute if available
	if exec, ok := r.inner.(tools.ExecutableRegistry); ok {
		return exec.Execute(ctx, name, params)
	}

	// Fallback to direct execution
	return tool.Execute(ctx, params)
}

// SetPermissionChecker implements tools.ExecutableRegistry.
func (r *codeModeRegistry) SetPermissionChecker(checker tools.PermissionChecker) {
	if exec, ok := r.inner.(tools.ExecutableRegistry); ok {
		exec.SetPermissionChecker(checker)
	}
}

// SystemPrompt returns the instruction block that must be appended to the
// agent's system prompt while code mode is enabled. Hiding the individual
// tools from the tool list is not enough on its own: without an explicit
// instruction, models often ignore run_code, hallucinate the old per-call
// tools, or give up when run_code errors once. This block tells the model
// exactly how to operate in code mode.
//
// It is intended to be wired via Agent.SetEphemeralSystemFn on enable and
// cleared (set nil) on disable, so it only ever appears while code mode is on.
const SystemPrompt = `# Code Mode is ACTIVE

Your individual tools are NOT available as direct tool calls right now. Instead,
every tool is exposed as a JavaScript function inside a single tool named
` + "`run_code`" + `. To DO anything (read files, run commands, search, etc.) you
MUST call ` + "`run_code`" + ` and write JavaScript that calls those functions.

Rules:
- Call ` + "`run_code`" + ` with a ` + "`code`" + ` string. The exact function signatures
  available inside the sandbox are listed in the ` + "`run_code`" + ` tool description.
- Async tool functions must be awaited: ` + "`const r = await read({file_path: \"...\"})`" + `.
- Batch independent calls for speed with ` + "`Promise.all`" + `:
  ` + "`const [a, b] = await Promise.all([read({...}), grep({...})]);`" + `
- Do local work in JS (filter/transform/aggregate) so only the final result
  returns to the conversation. The last expression's value is captured as output;
  use ` + "`console.log`" + ` for intermediate debugging.
- Do NOT ask for a tool to be called individually — it will not be offered.
  If ` + "`run_code`" + ` returns an error, read the error, fix the JavaScript, and
  call ` + "`run_code`" + ` again.`
