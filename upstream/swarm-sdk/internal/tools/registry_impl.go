package tools

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// SimpleRegistry implements the Registry and ObservableRegistry interfaces.
type SimpleRegistry struct {
	mu          sync.RWMutex
	tools       map[string]*ToolRegistration
	logger      observability.Logger
	tracer      observability.Tracer
	permissions PermissionChecker

	// Observer pattern support
	listeners     []ToolChangeListener
	changeTracker *ChangeTracker

	// Optional hooks manager for event emission
	hookManager HookEmitter

	// Execution configuration
	toolTimeout time.Duration // Default timeout for tool execution (0 = no timeout)

	// Statistics — use atomics so reads don't need the tool-map mutex.
	stats struct {
		executions           atomic.Int64
		successfulExecutions atomic.Int64
		failedExecutions     atomic.Int64
		cacheHits            atomic.Int64
		cacheMisses          atomic.Int64
	}
}

// HookEmitter interface for emitting events (subset of hooks.Manager).
type HookEmitter interface {
	Emit(ctx context.Context, event hooks.Event) (*hooks.Event, error)
}

// NewRegistry creates a new tool registry with noop observability.
// This is the zero-boilerplate entry point — no logger or tracer required.
// For production use with structured logging and tracing, use [NewSimpleRegistry].
//
// Example:
//
//	reg := tools.NewRegistry()
//	reg.Register(tools.Typed[MyParams](&MyTool{}))
func NewRegistry() *SimpleRegistry {
	return NewSimpleRegistry(nil, nil)
}

// NewSimpleRegistry creates a new simple tool registry.
// Pass nil for logger or tracer to use noop implementations.
// Default tool timeout is 5 minutes. Use SetToolTimeout to customize.
func NewSimpleRegistry(logger observability.Logger, tracer observability.Tracer) *SimpleRegistry {
	if logger == nil {
		logger = noop.NewLogger()
	}
	if tracer == nil {
		tracer = noop.NewTracer()
	}
	return &SimpleRegistry{
		tools:         make(map[string]*ToolRegistration),
		logger:        logger,
		tracer:        tracer,
		permissions:   NewRulesPermissionChecker(DefaultPermissionConfig()),
		listeners:     nil,
		changeTracker: NewChangeTracker(),
		toolTimeout:   5 * time.Minute, // Default: 5 minutes for long-running operations
	}
}

// SetHookManager sets the hooks manager for event emission.
func (r *SimpleRegistry) SetHookManager(manager HookEmitter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hookManager = manager
}

// SetToolTimeout sets the default timeout for tool execution.
// Set to 0 to disable timeout. Tools will respect the context timeout if provided.
func (r *SimpleRegistry) SetToolTimeout(timeout time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolTimeout = timeout
}

// GetToolTimeout returns the current tool execution timeout.
func (r *SimpleRegistry) ToolTimeout() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.toolTimeout
}

// GetChangeTracker returns the change tracker for this registry.
func (r *SimpleRegistry) ChangeTracker() *ChangeTracker {
	return r.changeTracker
}

// Register adds a tool to the registry.
func (r *SimpleRegistry) Register(tool Tool) error {
	return r.RegisterWithTrigger(tool, "programmatic", nil)
}

// RegisterWithTrigger adds a tool to the registry with trigger context.
func (r *SimpleRegistry) RegisterWithTrigger(tool Tool, trigger string, metadata map[string]any) error {
	r.mu.Lock()

	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		r.mu.Unlock()
		return sdkerr.Permanent("registry.duplicate_tool",
			fmt.Sprintf("tool %s already registered", name))
	}

	toolMetadata := &ToolMetadata{
		Source: ToolSourceBuiltin,
	}
	if provider, ok := tool.(MetadataProvider); ok {
		if provided := provider.ToolMetadata(); provided != nil {
			toolMetadata = cloneToolMetadata(provided)
		}
	}

	r.tools[name] = &ToolRegistration{
		Tool:     tool,
		Scope:    ScopeGlobal,
		Enabled:  true,
		Metadata: toolMetadata,
	}

	// Capture for notifications
	listeners := make([]ToolChangeListener, len(r.listeners))
	copy(listeners, r.listeners)
	hookManager := r.hookManager

	r.mu.Unlock()

	// Track the change
	r.changeTracker.RecordAdded(name, tool, trigger, metadata)

	// Log
	if r.logger != nil {
		r.logger.Info(context.Background(), "registry.tool_registered",
			observability.F("tool", name),
			observability.F("trigger", trigger))
	}

	// Notify listeners
	ctx := context.Background()
	change := ToolChange{
		Type:     ToolChangeAdded,
		ToolName: name,
		Tool:     tool,
		Metadata: metadata,
	}
	r.notifyListeners(ctx, listeners, trigger, change)

	// Emit hook event
	if hookManager != nil {
		r.emitToolEvent(ctx, hookManager, hooks.EventToolRegistered, name, trigger, metadata)
	}

	return nil
}

func cloneToolMetadata(input *ToolMetadata) *ToolMetadata {
	if input == nil {
		return nil
	}
	copyMeta := *input
	if input.Tags != nil {
		copyMeta.Tags = append([]string{}, input.Tags...)
	}
	return &copyMeta
}

// Unregister removes a tool from the registry.
func (r *SimpleRegistry) Unregister(name string) error {
	return r.UnregisterWithTrigger(name, "programmatic", nil)
}

// UnregisterWithTrigger removes a tool from the registry with trigger context.
func (r *SimpleRegistry) UnregisterWithTrigger(name string, trigger string, metadata map[string]any) error {
	r.mu.Lock()

	if _, exists := r.tools[name]; !exists {
		r.mu.Unlock()
		return sdkerr.Permanent("registry.tool_not_found",
			fmt.Sprintf("tool %s not found", name))
	}

	delete(r.tools, name)

	// Capture for notifications
	listeners := make([]ToolChangeListener, len(r.listeners))
	copy(listeners, r.listeners)
	hookManager := r.hookManager

	r.mu.Unlock()

	// Track the change
	r.changeTracker.RecordRemoved(name, trigger, metadata)

	// Log
	if r.logger != nil {
		r.logger.Info(context.Background(), "registry.tool_unregistered",
			observability.F("tool", name),
			observability.F("trigger", trigger))
	}

	// Notify listeners
	ctx := context.Background()
	change := ToolChange{
		Type:     ToolChangeRemoved,
		ToolName: name,
		Tool:     nil,
		Metadata: metadata,
	}
	r.notifyListeners(ctx, listeners, trigger, change)

	// Emit hook event
	if hookManager != nil {
		r.emitToolEvent(ctx, hookManager, hooks.EventToolUnregistered, name, trigger, metadata)
	}

	return nil
}

// Get retrieves a tool by name.
func (r *SimpleRegistry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	reg, exists := r.tools[name]
	if !exists {
		return nil, sdkerr.Permanent("registry.tool_not_found",
			fmt.Sprintf("tool %s not found", name))
	}

	if !reg.Enabled {
		return nil, sdkerr.Permanent("registry.tool_disabled",
			fmt.Sprintf("tool %s is disabled", name))
	}

	return reg.Tool, nil
}

// GetRegistration returns the tool registration metadata.
func (r *SimpleRegistry) Registration(name string) (*ToolRegistration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	reg, exists := r.tools[name]
	if !exists {
		return nil, false
	}

	copy := *reg
	return &copy, true
}

// SetPermissionChecker sets the permission checker for this registry.
func (r *SimpleRegistry) SetPermissionChecker(checker PermissionChecker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.permissions = checker
}

// GetPermissionChecker returns the permission checker for this registry.
func (r *SimpleRegistry) PermissionChecker() PermissionChecker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.permissions
}

// EnableTool enables a previously disabled tool.
func (r *SimpleRegistry) EnableTool(name string) error {
	return r.EnableToolWithTrigger(name, "programmatic", nil)
}

// EnableToolWithTrigger enables a tool with trigger context.
func (r *SimpleRegistry) EnableToolWithTrigger(name string, trigger string, metadata map[string]any) error {
	r.mu.Lock()

	reg, exists := r.tools[name]
	if !exists {
		r.mu.Unlock()
		return sdkerr.Permanent("registry.tool_not_found",
			fmt.Sprintf("tool %s not found", name))
	}

	// Only notify if actually changing state
	wasEnabled := reg.Enabled
	reg.Enabled = true
	tool := reg.Tool

	// Capture for notifications
	listeners := make([]ToolChangeListener, len(r.listeners))
	copy(listeners, r.listeners)
	hookManager := r.hookManager

	r.mu.Unlock()

	// Only notify if state actually changed
	if !wasEnabled {
		// Track the change
		r.changeTracker.RecordEnabled(name, tool, trigger, metadata)

		// Log
		if r.logger != nil {
			r.logger.Info(context.Background(), "registry.tool_enabled",
				observability.F("tool", name),
				observability.F("trigger", trigger))
		}

		// Notify listeners
		ctx := context.Background()
		change := ToolChange{
			Type:     ToolChangeEnabled,
			ToolName: name,
			Tool:     tool,
			Metadata: metadata,
		}
		r.notifyListeners(ctx, listeners, trigger, change)

		// Emit hook event
		if hookManager != nil {
			r.emitToolEvent(ctx, hookManager, hooks.EventToolEnabled, name, trigger, metadata)
		}
	}

	return nil
}

// DisableTool disables a tool, preventing it from being executed.
func (r *SimpleRegistry) DisableTool(name string) error {
	return r.DisableToolWithTrigger(name, "programmatic", nil)
}

// DisableToolWithTrigger disables a tool with trigger context.
func (r *SimpleRegistry) DisableToolWithTrigger(name string, trigger string, metadata map[string]any) error {
	r.mu.Lock()

	reg, exists := r.tools[name]
	if !exists {
		r.mu.Unlock()
		return sdkerr.Permanent("registry.tool_not_found",
			fmt.Sprintf("tool %s not found", name))
	}

	// Only notify if actually changing state
	wasEnabled := reg.Enabled
	reg.Enabled = false
	tool := reg.Tool

	// Capture for notifications
	listeners := make([]ToolChangeListener, len(r.listeners))
	copy(listeners, r.listeners)
	hookManager := r.hookManager

	r.mu.Unlock()

	// Only notify if state actually changed
	if wasEnabled {
		// Track the change
		r.changeTracker.RecordDisabled(name, tool, trigger, metadata)

		// Log
		if r.logger != nil {
			r.logger.Info(context.Background(), "registry.tool_disabled",
				observability.F("tool", name),
				observability.F("trigger", trigger))
		}

		// Notify listeners
		ctx := context.Background()
		change := ToolChange{
			Type:     ToolChangeDisabled,
			ToolName: name,
			Tool:     tool,
			Metadata: metadata,
		}
		r.notifyListeners(ctx, listeners, trigger, change)

		// Emit hook event
		if hookManager != nil {
			r.emitToolEvent(ctx, hookManager, hooks.EventToolDisabled, name, trigger, metadata)
		}
	}

	return nil
}

// IsToolEnabled checks if a tool is enabled.
func (r *SimpleRegistry) IsToolEnabled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	reg, exists := r.tools[name]
	if !exists {
		return false
	}

	return reg.Enabled
}

// HideTool marks a tool as hidden. Hidden tools remain enabled and executable
// but are excluded from List(), so they do not appear in the provider tool list
// sent to the LLM. This is useful for internal/management tools that should
// only be accessible to specific subsystems (e.g. MCP management tools used by
// a sidebar assistant) and not the main chat agent.
func (r *SimpleRegistry) HideTool(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	reg, exists := r.tools[name]
	if !exists {
		return sdkerr.Permanent("registry.tool_not_found",
			fmt.Sprintf("tool %s not found", name))
	}

	reg.Hidden = true
	return nil
}

// UnhideTool removes the hidden flag from a tool, making it visible in List() again.
func (r *SimpleRegistry) UnhideTool(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	reg, exists := r.tools[name]
	if !exists {
		return sdkerr.Permanent("registry.tool_not_found",
			fmt.Sprintf("tool %s not found", name))
	}

	reg.Hidden = false
	return nil
}

// List returns all registered tool names (only enabled, non-hidden tools).
// Names are sorted alphabetically for consistent ordering (required for cache stability).
func (r *SimpleRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name, reg := range r.tools {
		if reg.Enabled && !reg.Hidden {
			names = append(names, name)
		}
	}

	// Sort for consistent ordering - critical for Anthropic prompt caching
	sort.Strings(names)
	return names
}

// ListAll returns all registered tool names (including disabled tools).
// Names are sorted alphabetically for consistent ordering.
func (r *SimpleRegistry) ListAll() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}

	sort.Strings(names)
	return names
}

// GetIncludingDisabled retrieves a tool by name, even if disabled.
func (r *SimpleRegistry) IncludingDisabled(name string) (Tool, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	reg, exists := r.tools[name]
	if !exists {
		return nil, false, sdkerr.Permanent("registry.tool_not_found",
			fmt.Sprintf("tool %s not found", name))
	}

	return reg.Tool, reg.Enabled, nil
}

// ListByCategory returns tools in a specific category.
func (r *SimpleRegistry) ListByCategory(category string) []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]Tool, 0)
	for _, reg := range r.tools {
		if reg.Enabled && reg.Metadata != nil && reg.Metadata.Category == category {
			tools = append(tools, reg.Tool)
		}
	}

	return tools
}

// IsRegistered checks if a tool is registered.
func (r *SimpleRegistry) IsRegistered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.tools[name]
	return exists
}

// Execute executes a tool by name with the given parameters.
func (r *SimpleRegistry) Execute(ctx context.Context, name string, params map[string]any) (*ToolResult, error) {
	start := time.Now()

	// Get tool
	tool, err := r.Get(name)
	if err != nil {
		return nil, sdkerr.Wrap(
			err,
			"tools.registry.get_failed",
			sdkerr.WithOperation("tools.registry.execute"),
			sdkerr.WithComponent("tools.registry"),
			sdkerr.WithTraceFromContext(ctx),
			sdkerr.WithAttr("tool_name", name),
		)
	}

	// Snapshot registration metadata for permission context
	var reg *ToolRegistration
	r.mu.RLock()
	reg = r.tools[name]
	r.mu.RUnlock()

	// Start tracing
	if r.tracer != nil {
		var span observability.Span
		ctx, span = r.tracer.StartSpan(ctx, fmt.Sprintf("tool.%s", name))
		defer span.End()
		span.SetAttribute("tool_name", name)
	}

	// Validate parameters if the tool opts-in to validation.
	if vt, ok := tool.(ValidatableTool); ok {
		if err := vt.Validate(params); err != nil {
			r.incrementFailedExecutions()
			return nil, sdkerr.Permanent("tool.invalid_parameters",
				fmt.Sprintf("validation failed for %s: %v", name, err),
				sdkerr.WithOperation("tools.registry.execute"),
				sdkerr.WithComponent("tools.registry"),
				sdkerr.WithTraceFromContext(ctx),
				sdkerr.WithAttr("tool_name", name))
		}
	}

	// Enforce permissions if the tool declares any.
	var required []Permission
	if pt, ok := tool.(PermissionedTool); ok {
		required = pt.RequiresPermission()
	}
	if len(required) > 0 {
		checker := r.PermissionChecker()
		if checker == nil {
			r.incrementFailedExecutions()
			return nil, sdkerr.Permanent("tool.permission_checker_missing",
				fmt.Sprintf("permission checker not configured for tool %s", name),
				sdkerr.WithOperation("tools.registry.execute"),
				sdkerr.WithComponent("tools.registry"),
				sdkerr.WithTraceFromContext(ctx),
				sdkerr.WithAttr("tool_name", name))
		}

		scope := ScopeGlobal
		scopeID := ""
		if reg != nil {
			scope = reg.Scope
			scopeID = reg.ScopeID
		}

		ctxData := BuildPermissionContext(
			name,
			params,
			required,
			scope,
			scopeID,
			"",
			"",
			"",
		)

		// Install a denial-reason sink so a permission checker can hand back an
		// actionable reason (e.g. "this run is read-only") that we surface to
		// the model instead of a bare "permission denied".
		checkCtx, denialReason := WithDenialReasonSink(ctx)
		if !checker.CheckWithContext(checkCtx, required, ctxData) {
			r.incrementFailedExecutions()
			if r.logger != nil {
				r.logger.Warn(ctx, "registry.tool_permission_denied",
					observability.F("tool", name),
					observability.F("permissions", required))
			}
			msg := fmt.Sprintf("permission denied for tool %s", name)
			if denialReason != nil && *denialReason != "" {
				msg = fmt.Sprintf("permission denied for tool %s: %s", name, *denialReason)
			}
			return nil, sdkerr.Permanent("tool.permission_denied",
				msg,
				sdkerr.WithOperation("tools.registry.execute"),
				sdkerr.WithComponent("tools.registry"),
				sdkerr.WithTraceFromContext(ctx),
				sdkerr.WithAttr("tool_name", name))
		}

		// Permission was granted (including interactive user approval for
		// out-of-workspace paths). Inject the target path(s) into the context
		// so that tool-level path guards (e.g. forge FSWrite/FSPatch/FSRead)
		// can recognise these paths as explicitly approved and skip their
		// workspace boundary check.
		if approvedPath := extractApprovedPath(params); approvedPath != "" {
			ctx = WithApprovedPaths(ctx, approvedPath)
		}
	}

	// Apply timeout if configured and not already set on context.
	// Use the (possibly enriched) ctx that now carries approved paths.
	execCtx := ctx
	var cancel context.CancelFunc
	timeout := r.ToolTimeout()
	if timeout > 0 {
		// Only apply timeout if context doesn't already have a deadline
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			execCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()

			if r.logger != nil {
				r.logger.Debug(ctx, "registry.tool_timeout_applied",
					observability.F("tool", name),
					observability.F("timeout_seconds", timeout.Seconds()))
			}
		}
	}

	// Execute tool with timeout
	result, err := measuredExecute(execCtx, tool, name, params)

	duration := time.Since(start)

	// Update statistics
	r.incrementExecutions()
	if err != nil {
		r.incrementFailedExecutions()
		err = sdkerr.Wrap(
			err,
			"tools.registry.execution_failed",
			sdkerr.WithOperation("tools.registry.execute"),
			sdkerr.WithComponent("tools.registry"),
			sdkerr.WithTraceFromContext(execCtx),
			sdkerr.WithAttr("tool_name", name),
			sdkerr.WithAttr("duration_ms", duration.Milliseconds()),
		)

		if r.logger != nil {
			r.logger.Error(ctx, "registry.tool_execution_failed",
				observability.F("tool", name),
				observability.F("duration_ms", duration.Milliseconds()),
				observability.F("error", err.Error()))
		}

		return nil, err
	}

	r.incrementSuccessfulExecutions()

	if r.logger != nil {
		r.logger.Debug(ctx, "registry.tool_executed",
			observability.F("tool", name),
			observability.F("duration_ms", duration.Milliseconds()))
	}

	return result, nil
}

// Statistics helpers
func (r *SimpleRegistry) incrementExecutions() {
	r.stats.executions.Add(1)
}

func (r *SimpleRegistry) incrementSuccessfulExecutions() {
	r.stats.successfulExecutions.Add(1)
}

func (r *SimpleRegistry) incrementFailedExecutions() {
	r.stats.failedExecutions.Add(1)
}

// AddListener registers a listener for tool change notifications.
// Implements ObservableRegistry interface.
func (r *SimpleRegistry) AddListener(listener ToolChangeListener) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listeners = append(r.listeners, listener)
}

// RemoveListener unregisters a listener.
// Implements ObservableRegistry interface.
func (r *SimpleRegistry) RemoveListener(listener ToolChangeListener) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, l := range r.listeners {
		if l == listener {
			r.listeners = append(r.listeners[:i], r.listeners[i+1:]...)
			return
		}
	}
}

// NotifyBatchChange emits a batch change event for multiple tool changes.
// Implements ObservableRegistry interface.
func (r *SimpleRegistry) NotifyBatchChange(ctx context.Context, trigger string, contextData map[string]any, changes []ToolChange) {
	r.mu.RLock()
	listeners := make([]ToolChangeListener, len(r.listeners))
	copy(listeners, r.listeners)
	hookManager := r.hookManager
	r.mu.RUnlock()

	// Track all changes
	for _, change := range changes {
		switch change.Type {
		case ToolChangeAdded:
			r.changeTracker.RecordAdded(change.ToolName, change.Tool, trigger, change.Metadata)
		case ToolChangeRemoved:
			r.changeTracker.RecordRemoved(change.ToolName, trigger, change.Metadata)
		case ToolChangeEnabled:
			r.changeTracker.RecordEnabled(change.ToolName, change.Tool, trigger, change.Metadata)
		case ToolChangeDisabled:
			r.changeTracker.RecordDisabled(change.ToolName, change.Tool, trigger, change.Metadata)
		}
	}

	// Notify listeners with batch event
	event := ToolChangeEvent{
		Changes: changes,
		Trigger: trigger,
		Context: contextData,
	}

	for _, listener := range listeners {
		go func(l ToolChangeListener) {
			defer func() {
				if rec := recover(); rec != nil {
					r.logger.Error(context.Background(), "registry.listener_panic",
						observability.F("listener_type", fmt.Sprintf("%T", l)),
						observability.F("panic", fmt.Sprintf("%v", rec)),
					)
				}
			}()
			l.OnToolsChanged(ctx, event)
		}(listener)
	}

	// Emit batch hook event
	if hookManager != nil {
		toolNames := make([]string, len(changes))
		for i, c := range changes {
			toolNames[i] = c.ToolName
		}

		hookEvent := hooks.Event{
			ID:        fmt.Sprintf("tool-batch-%d", time.Now().UnixNano()),
			Type:      hooks.EventToolAvailabilityChanged,
			Timestamp: time.Now(),
			Data: map[string]any{
				"trigger":    trigger,
				"changes":    len(changes),
				"tool_names": toolNames,
			},
			Metadata: contextData,
		}
		hookManager.Emit(ctx, hookEvent)
	}
}

// notifyListeners sends a single change notification to all listeners.
func (r *SimpleRegistry) notifyListeners(ctx context.Context, listeners []ToolChangeListener, trigger string, change ToolChange) {
	event := ToolChangeEvent{
		Changes: []ToolChange{change},
		Trigger: trigger,
		Context: map[string]any{
			"tool_name": change.ToolName,
		},
	}

	for _, listener := range listeners {
		go func(l ToolChangeListener) {
			defer func() {
				if rec := recover(); rec != nil {
					r.logger.Error(context.Background(), "registry.listener_panic",
						observability.F("listener_type", fmt.Sprintf("%T", l)),
						observability.F("panic", fmt.Sprintf("%v", rec)),
					)
				}
			}()
			l.OnToolsChanged(ctx, event)
		}(listener)
	}
}

// emitToolEvent emits a hook event for a tool change.
func (r *SimpleRegistry) emitToolEvent(ctx context.Context, hookManager HookEmitter, eventType string, toolName string, trigger string, metadata map[string]any) {
	event := hooks.Event{
		ID:        fmt.Sprintf("tool-%s-%d", toolName, time.Now().UnixNano()),
		Type:      eventType,
		Timestamp: time.Now(),
		Data: map[string]any{
			"tool_name": toolName,
			"trigger":   trigger,
		},
		Metadata: metadata,
	}

	hookManager.Emit(ctx, event)
}

// incrementCacheHits and incrementCacheMisses are reserved for future cache instrumentation.

// extractApprovedPath returns the file-system path that the tool is about to
// operate on, so the registry can inject it into the context as an approved
// path after the permission check passes. This bridges the permission system
// and the tool-level path guards in forge tools (FSRead/FSWrite/FSPatch).
func extractApprovedPath(params map[string]any) string {
	if params == nil {
		return ""
	}
	for _, key := range []string{"file_path", "path"} {
		if v, ok := params[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
