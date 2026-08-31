// Package hooks implements the event hook system (Ring 1).
// Hooks intercept and process events throughout the SDK lifecycle.
package hooks

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Manager implements the Registry interface.
// It manages hook registration, scoping, and execution.
type Manager struct {
	mu sync.RWMutex

	// Global hooks (apply to all events)
	global map[string]*hookEntry

	// Scoped hooks (apply to specific scope)
	// Structure: scope -> scopeID -> hookName -> entry
	scoped map[HookScope]map[string]map[string]*hookEntry

	// Priority-sorted cache for fast iteration
	sortedCache map[string][]Hook // scope:scopeID -> sorted hooks
	cacheValid  map[string]bool   // scope:scopeID -> validity

	// Executor for running hooks
	executor *Executor

	// Configuration
	config ManagerConfig

	// Global statistics
	stats *RegistryStats

	// obsView memoizes this manager's observational view (see
	// observational.go). It is one per manager, not one per consumer: the view
	// owns a goroutine and a bounded queue, and sub-agent spawns are unbounded,
	// so handing each spawn its own view would leak both.
	obsMu   sync.Mutex
	obsView *ObservationalView
}

// hookEntry contains a registered hook with metadata
type hookEntry struct {
	Hook             Hook
	Scope            HookScope
	ScopeID          string
	Enabled          bool
	PermissionPolicy HookPermissionPolicy
	Stats            *HookStats
	RegisteredAt     time.Time
}

// HookStats tracks statistics for a hook
type HookStats struct {
	ExecutionCount int64
	BlockedCount   int64
	ModifiedCount  int64
	ErrorCount     int64
	TotalTimeNS    int64
	LastExecutedAt time.Time
}

// RegistryStats tracks global registry statistics
type RegistryStats struct {
	TotalHooks      int
	EnabledHooks    int
	TotalExecutions int64
	TotalBlocked    int64
	TotalModified   int64
	TotalErrors     int64
}

// ManagerConfig configures the hook manager
type ManagerConfig struct {
	// MaxHooksPerScope limits hooks per scope
	MaxHooksPerScope int

	// EnableMetrics enables metrics collection
	EnableMetrics bool

	// EnableTracing enables distributed tracing
	EnableTracing bool

	// ErrorHandler is called when hooks error
	ErrorHandler func(hookName string, err error)

	// MaxExecutionTime is the timeout for hook execution
	MaxExecutionTime time.Duration

	// RealtimeCallback is called for real-time hook execution updates.
	// Spec: 020-hook-execution-realtime
	RealtimeCallback HookRealtimeCallback
}

// NewManager creates a new hook manager
func NewManager(config ManagerConfig) *Manager {
	// Set defaults
	if config.MaxHooksPerScope == 0 {
		config.MaxHooksPerScope = 100
	}
	if config.MaxExecutionTime == 0 {
		config.MaxExecutionTime = 10 * time.Minute
	}
	if config.ErrorHandler == nil {
		config.ErrorHandler = func(hookName string, err error) {
			// Default: silent (could log to stderr)
		}
	}

	m := &Manager{
		global:      make(map[string]*hookEntry),
		scoped:      make(map[HookScope]map[string]map[string]*hookEntry),
		sortedCache: make(map[string][]Hook),
		cacheValid:  make(map[string]bool),
		config:      config,
		stats:       &RegistryStats{},
	}

	// Initialize scoped maps
	m.scoped[ScopeGlobal] = make(map[string]map[string]*hookEntry)
	m.scoped[ScopeProject] = make(map[string]map[string]*hookEntry)
	m.scoped[ScopeMode] = make(map[string]map[string]*hookEntry)
	m.scoped[ScopeConversation] = make(map[string]map[string]*hookEntry)

	// Create executor
	m.executor = NewExecutor(m, config)

	return m
}

// Register adds a hook to the registry
func (m *Manager) Register(hook Hook, scope HookScope, scopeID string) error {
	if hook == nil {
		return fmt.Errorf("hook cannot be nil")
	}

	name := hook.Name()
	if name == "" {
		return fmt.Errorf("hook name cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check limits
	if err := m.checkLimits(scope, scopeID); err != nil {
		return err
	}

	policy := HookPermissionAllow
	if permissioned, ok := hook.(PermissionedHook); ok {
		policy = NormalizeHookPermissionPolicy(permissioned.HookPermissionPolicy())
	}

	entry := &hookEntry{
		Hook:             hook,
		Scope:            scope,
		ScopeID:          scopeID,
		Enabled:          true,
		PermissionPolicy: policy,
		Stats:            &HookStats{},
		RegisteredAt:     time.Now(),
	}

	// Register based on scope
	if scope == ScopeGlobal && scopeID == "" {
		// True global hook
		if _, exists := m.global[name]; exists {
			return fmt.Errorf("hook %s already registered globally", name)
		}
		m.global[name] = entry
	} else {
		// Scoped hook
		if _, exists := m.scoped[scope]; !exists {
			m.scoped[scope] = make(map[string]map[string]*hookEntry)
		}
		if _, exists := m.scoped[scope][scopeID]; !exists {
			m.scoped[scope][scopeID] = make(map[string]*hookEntry)
		}
		if _, exists := m.scoped[scope][scopeID][name]; exists {
			return fmt.Errorf("hook %s already registered in %s:%s", name, scope, scopeID)
		}
		m.scoped[scope][scopeID][name] = entry
	}

	// Invalidate cache
	m.invalidateCache()

	// Update stats
	m.stats.TotalHooks++
	m.stats.EnabledHooks++

	return nil
}

// Unregister removes a hook from the registry
func (m *Manager) Unregister(name string, scope HookScope, scopeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Try global first
	if entry, exists := m.global[name]; exists {
		if entry.Enabled {
			m.stats.EnabledHooks--
		}
		delete(m.global, name)
		m.stats.TotalHooks--
		m.invalidateCache()
		return nil
	}

	// Try scoped
	if scopeMap, exists := m.scoped[scope]; exists {
		if hookMap, exists := scopeMap[scopeID]; exists {
			if entry, exists := hookMap[name]; exists {
				if entry.Enabled {
					m.stats.EnabledHooks--
				}
				delete(hookMap, name)
				m.stats.TotalHooks--
				m.invalidateCache()
				return nil
			}
		}
	}

	return fmt.Errorf("hook %s not found in %s:%s", name, scope, scopeID)
}

// Emit sends an event through the hook chain
func (m *Manager) Emit(ctx context.Context, event Event) (*Event, error) {
	result, err := m.EmitWithResult(ctx, event)
	if err != nil {
		return nil, err
	}
	return result.FinalEvent, nil
}

// EmitWithResult sends an event through the hook chain and returns the full execution result.
// This includes individual hook outputs for UI display.
func (m *Manager) EmitWithResult(ctx context.Context, event Event) (*ExecutionResult, error) {
	// Get applicable hooks
	hooks := m.getApplicableHooks(event)
	if len(hooks) == 0 {
		return &ExecutionResult{
			FinalEvent:    &event,
			HooksExecuted: 0,
			HookOutputs:   []HookOutput{},
		}, nil
	}

	// Execute hooks
	result, err := m.executor.Execute(ctx, event, hooks)
	if err != nil {
		return result, err
	}

	// Update global stats
	m.mu.Lock()
	m.stats.TotalExecutions++
	if result.Blocked {
		m.stats.TotalBlocked++
	}
	if result.Modified {
		m.stats.TotalModified++
	}
	m.mu.Unlock()

	// Return result with blocking error if blocked
	if result.Blocked {
		return result, fmt.Errorf("event blocked by %s: %s", result.BlockedBy, result.BlockReason)
	}

	return result, nil
}

// List returns all registered hooks
func (m *Manager) List() []HookRegistration {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var registrations []HookRegistration

	// Add global hooks
	for _, entry := range m.global {
		registrations = append(registrations, m.entryToRegistration(entry))
	}

	// Add scoped hooks
	for scope, scopeMap := range m.scoped {
		for scopeID, hookMap := range scopeMap {
			for _, entry := range hookMap {
				if entry.Scope == scope && entry.ScopeID == scopeID {
					registrations = append(registrations, m.entryToRegistration(entry))
				}
			}
		}
	}

	return registrations
}

// ListByScope returns hooks registered in a specific scope
func (m *Manager) ListByScope(scope HookScope, scopeID string) []HookRegistration {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var registrations []HookRegistration

	// Include global hooks for all scopes
	if scope != ScopeGlobal {
		for _, entry := range m.global {
			registrations = append(registrations, m.entryToRegistration(entry))
		}
	}

	// Add scope-specific hooks
	if scopeMap, exists := m.scoped[scope]; exists {
		if hookMap, exists := scopeMap[scopeID]; exists {
			for _, entry := range hookMap {
				registrations = append(registrations, m.entryToRegistration(entry))
			}
		}
	}

	return registrations
}

// IsRegistered checks if a hook is registered
func (m *Manager) IsRegistered(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Check global
	if _, exists := m.global[name]; exists {
		return true
	}

	// Check all scopes
	for _, scopeMap := range m.scoped {
		for _, hookMap := range scopeMap {
			if _, exists := hookMap[name]; exists {
				return true
			}
		}
	}

	return false
}

// GetStats returns global registry statistics
func (m *Manager) GetStats() RegistryStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.stats
}

// Enable enables a hook
func (m *Manager) Enable(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry := m.findEntry(name)
	if entry == nil {
		return fmt.Errorf("hook %s not found", name)
	}

	if !entry.Enabled {
		entry.Enabled = true
		m.stats.EnabledHooks++
		m.invalidateCache()
	}

	return nil
}

// SetPermissionPolicy updates a hook's permission policy.
func (m *Manager) SetPermissionPolicy(name string, policy HookPermissionPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry := m.findEntry(name)
	if entry == nil {
		return fmt.Errorf("hook %s not found", name)
	}

	normalized := NormalizeHookPermissionPolicy(policy)
	if entry.PermissionPolicy != normalized {
		entry.PermissionPolicy = normalized
		m.invalidateCache()
	}

	return nil
}

// SetEnabled toggles a hook's enabled state at runtime.
// This is a thin convenience over Enable/Disable for callers that already
// hold a boolean (UI toggles, config reload, live key bindings).
// Hook dispatch consults the enabled bit under the manager's RWMutex on
// every event, so flipping mid-execution is race-safe: the next event sees
// the new state and no in-flight hook is interrupted.
func (m *Manager) SetEnabled(name string, enabled bool) error {
	if enabled {
		return m.Enable(name)
	}
	return m.Disable(name)
}

// Disable disables a hook
func (m *Manager) Disable(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry := m.findEntry(name)
	if entry == nil {
		return fmt.Errorf("hook %s not found", name)
	}

	if entry.Enabled {
		entry.Enabled = false
		m.stats.EnabledHooks--
		m.invalidateCache()
	}

	return nil
}

// Clear removes all hooks
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.global = make(map[string]*hookEntry)
	m.scoped = make(map[HookScope]map[string]map[string]*hookEntry)
	m.scoped[ScopeGlobal] = make(map[string]map[string]*hookEntry)
	m.scoped[ScopeProject] = make(map[string]map[string]*hookEntry)
	m.scoped[ScopeMode] = make(map[string]map[string]*hookEntry)
	m.scoped[ScopeConversation] = make(map[string]map[string]*hookEntry)

	m.stats = &RegistryStats{}
	m.invalidateCache()
}

// Private methods

func (m *Manager) checkLimits(scope HookScope, scopeID string) error {
	count := 0

	if scope == ScopeGlobal && scopeID == "" {
		count = len(m.global)
	} else {
		if scopeMap, exists := m.scoped[scope]; exists {
			if hookMap, exists := scopeMap[scopeID]; exists {
				count = len(hookMap)
			}
		}
	}

	if count >= m.config.MaxHooksPerScope {
		return fmt.Errorf("hook limit reached for %s:%s (max: %d)",
			scope, scopeID, m.config.MaxHooksPerScope)
	}

	return nil
}

func (m *Manager) invalidateCache() {
	m.sortedCache = make(map[string][]Hook)
	m.cacheValid = make(map[string]bool)
}

// getApplicableHooks resolves and caches the sorted hook list for event's
// cache key. It takes the FULL write lock (not RLock) because a cache miss
// writes to m.sortedCache/m.cacheValid below — this used to take only
// m.mu.RLock() while doing that write, a concurrent-map-write data race
// whenever two goroutines called EmitWithResult on the same *Manager at the
// same time (a realistic multi-conversation/delegate-inheritance shape: an
// IPC server, or several sub-agents sharing one manager). See
// observational.go's enrolledObservationalHooks, which was deliberately
// written as a SEPARATE pure-read implementation specifically to avoid
// calling this (then-racy) function from its own dispatch goroutine.
func (m *Manager) getApplicableHooks(event Event) []Hook {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Build cache key
	cacheKey := m.buildCacheKey(event)

	// Check cache
	if m.cacheValid[cacheKey] {
		return m.sortedCache[cacheKey]
	}

	// Collect applicable hooks
	var hooks []Hook

	// Add global hooks
	for _, entry := range m.global {
		if entry.Enabled && entry.PermissionPolicy != HookPermissionDeny && entry.Hook.Filter(event) {
			hooks = append(hooks, entry.Hook)
		}
	}

	// Add conversation-specific hooks
	if event.ConversationID != "" {
		if scopeMap, exists := m.scoped[ScopeConversation]; exists {
			if hookMap, exists := scopeMap[event.ConversationID]; exists {
				for _, entry := range hookMap {
					if entry.Enabled && entry.PermissionPolicy != HookPermissionDeny && entry.Hook.Filter(event) {
						hooks = append(hooks, entry.Hook)
					}
				}
			}
		}
	}

	// Add mode-specific hooks
	if event.ModeID != "" {
		if scopeMap, exists := m.scoped[ScopeMode]; exists {
			if hookMap, exists := scopeMap[event.ModeID]; exists {
				for _, entry := range hookMap {
					if entry.Enabled && entry.PermissionPolicy != HookPermissionDeny && entry.Hook.Filter(event) {
						hooks = append(hooks, entry.Hook)
					}
				}
			}
		}
	}

	// Sort by priority (descending)
	sort.Slice(hooks, func(i, j int) bool {
		return hooks[i].Priority() > hooks[j].Priority()
	})

	// Cache result
	m.sortedCache[cacheKey] = hooks
	m.cacheValid[cacheKey] = true

	return hooks
}

func (m *Manager) buildCacheKey(event Event) string {
	// The cache stores the result of evaluating every hook's Filter(event).
	// Any field a Filter can branch on must therefore be part of the key, or
	// the first event of a given type permanently decides the hook set.
	//
	// Tool name is such a field: hooks like sleep-blocker and
	// stdin-conflict-advisory only accept tool_name == "Bash"/"bash". Keying
	// solely on type/conversation/mode meant that if the first
	// tool.before_execute of a conversation was any non-Bash tool (Read,
	// TaskManage, Task, ...) those hooks were filtered out once and then never
	// re-consulted, so they silently never ran for the rest of the session.
	// That made them fire only in the rare conversations whose very first tool
	// call happened to be Bash, which is exactly the intermittency reported in
	// issue #118.
	return fmt.Sprintf("type:%s:conv:%s:mode:%s:tool:%s",
		event.Type, event.ConversationID, event.ModeID, eventToolName(event))
}

// eventToolName extracts the tool name from an event, tolerating both the
// "tool_name" and legacy "name" keys used by different emitters. It returns ""
// for events that carry no tool identity, which keeps a single stable cache
// bucket for all non-tool events.
func eventToolName(event Event) string {
	if event.Data == nil {
		return ""
	}
	if tn, ok := event.Data["tool_name"].(string); ok && tn != "" {
		return tn
	}
	if tn, ok := event.Data["name"].(string); ok {
		return tn
	}
	return ""
}

func (m *Manager) findEntry(name string) *hookEntry {
	// Check global
	if entry, exists := m.global[name]; exists {
		return entry
	}

	// Check all scopes
	for _, scopeMap := range m.scoped {
		for _, hookMap := range scopeMap {
			if entry, exists := hookMap[name]; exists {
				return entry
			}
		}
	}

	return nil
}

func (m *Manager) entryToRegistration(entry *hookEntry) HookRegistration {
	return HookRegistration{
		Hook:             entry.Hook,
		Scope:            entry.Scope,
		ScopeID:          entry.ScopeID,
		Enabled:          entry.Enabled,
		PermissionPolicy: entry.PermissionPolicy,
		ExecutionCount:   entry.Stats.ExecutionCount,
		BlockedCount:     entry.Stats.BlockedCount,
		ModifiedCount:    entry.Stats.ModifiedCount,
		ErrorCount:       entry.Stats.ErrorCount,
	}
}
