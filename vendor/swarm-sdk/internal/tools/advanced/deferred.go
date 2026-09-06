package advanced

import (
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ---------------------------------------------------------------------------
// Deferred tool loading
// ---------------------------------------------------------------------------

// DeferredRegistry wraps an existing tools.Registry and classifies each tool
// as either "eager" (always included in the provider context) or "deferred"
// (excluded from initial context, discoverable via ToolSearchTool).
//
// It does NOT modify the underlying registry — it only provides a read-only
// classification layer on top.
type DeferredRegistry struct {
	// registry is the underlying tool registry (read-only access).
	registry tools.Registry

	// mu protects concurrent access to the overrides map.
	mu sync.RWMutex

	// overrides lets callers force a tool into deferred or eager regardless
	// of what the tool's Deferrable interface says.
	overrides map[string]bool // name → shouldDefer
}

// NewDeferredRegistry creates a DeferredRegistry wrapping the given registry.
func NewDeferredRegistry(registry tools.Registry) *DeferredRegistry {
	return &DeferredRegistry{
		registry:  registry,
		overrides: make(map[string]bool),
	}
}

// ForceDefer overrides the tool's own Deferrable decision and forces it
// into the deferred (true) or eager (false) set.
func (dr *DeferredRegistry) ForceDefer(toolName string, deferred bool) {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	dr.overrides[toolName] = deferred
}

// ClearOverride removes a force-defer override for the named tool.
func (dr *DeferredRegistry) ClearOverride(toolName string) {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	delete(dr.overrides, toolName)
}

// ShouldDefer returns true if the named tool should be deferred.
// Priority: explicit override > Deferrable interface > default (eager).
func (dr *DeferredRegistry) ShouldDefer(toolName string) bool {
	// Check overrides first
	dr.mu.RLock()
	override, ok := dr.overrides[toolName]
	dr.mu.RUnlock()

	if ok {
		return override
	}

	// Check the tool's Deferrable interface
	tool, err := dr.registry.Get(toolName)
	if err != nil || tool == nil {
		return false // unknown tools are not deferred
	}

	if d, ok := tool.(Deferrable); ok {
		return d.ShouldDefer()
	}

	return false // default: eager
}

// SplitTools separates a list of provider.Tool into eager and deferred
// groups. The sdkTools map is keyed by tool name and used to check the
// Deferrable interface.
//
// The ToolSearchTool itself is always placed in the eager set.
func (dr *DeferredRegistry) SplitTools(providerTools []provider.Tool) (eager, deferred []provider.Tool) {
	eager = make([]provider.Tool, 0, len(providerTools))
	deferred = make([]provider.Tool, 0)

	for _, pt := range providerTools {
		// ToolSearchTool is always eager
		if pt.Name == ToolSearchName {
			eager = append(eager, pt)
			continue
		}

		if dr.ShouldDefer(pt.Name) {
			deferred = append(deferred, pt)
		} else {
			eager = append(eager, pt)
		}
	}

	return eager, deferred
}

// ClassifyAll returns AdvancedToolSpecs for all tools in the registry,
// each annotated with whether it is deferred.
func (dr *DeferredRegistry) ClassifyAll() []AdvancedToolSpec {
	names := dr.registry.List()
	specs := make([]AdvancedToolSpec, 0, len(names))
	for _, name := range names {
		tool, err := dr.registry.Get(name)
		if err != nil || tool == nil {
			continue
		}
		spec := BuildSpec(tool)
		// Apply override if present
		dr.mu.RLock()
		override, ok := dr.overrides[name]
		dr.mu.RUnlock()

		if ok {
			spec.ShouldDefer = override
		}
		specs = append(specs, spec)
	}
	return specs
}

// EagerToolNames returns the names of all tools that should be eagerly loaded.
func (dr *DeferredRegistry) EagerToolNames() []string {
	names := dr.registry.List()
	eager := make([]string, 0, len(names))
	for _, name := range names {
		if !dr.ShouldDefer(name) {
			eager = append(eager, name)
		}
	}
	return eager
}

// DeferredToolNames returns the names of all tools that are deferred.
func (dr *DeferredRegistry) DeferredToolNames() []string {
	names := dr.registry.List()
	deferred := make([]string, 0)
	for _, name := range names {
		if dr.ShouldDefer(name) {
			deferred = append(deferred, name)
		}
	}
	return deferred
}

// ContextStats returns a summary of how many tools are eager vs deferred
// and the estimated token savings.
type ContextStats struct {
	EagerCount           int `json:"eager_count"`
	DeferredCount        int `json:"deferred_count"`
	TotalCount           int `json:"total_count"`
	EagerEstimatedTokens int `json:"eager_estimated_tokens"`
	DeferredSavedTokens  int `json:"deferred_saved_tokens"`
	TotalEstimatedTokens int `json:"total_estimated_tokens"`
	SavingsPercent       int `json:"savings_percent"`
}

// CalculateStats computes context statistics for the current tool set.
func (dr *DeferredRegistry) CalculateStats() ContextStats {
	names := dr.registry.List()
	stats := ContextStats{TotalCount: len(names)}

	for _, name := range names {
		tool, err := dr.registry.Get(name)
		if err != nil || tool == nil {
			continue
		}

		tokens := EstimateToolTokens(tool)
		stats.TotalEstimatedTokens += tokens

		if dr.ShouldDefer(name) {
			stats.DeferredCount++
			stats.DeferredSavedTokens += tokens
		} else {
			stats.EagerCount++
			stats.EagerEstimatedTokens += tokens
		}
	}

	if stats.TotalEstimatedTokens > 0 {
		stats.SavingsPercent = (stats.DeferredSavedTokens * 100) / stats.TotalEstimatedTokens
	}

	return stats
}

// ---------------------------------------------------------------------------
// Standalone SplitTools — for use without a DeferredRegistry
// ---------------------------------------------------------------------------

// SplitToolsByDeferred is a convenience function that separates tools into
// eager and deferred groups without needing a DeferredRegistry. It checks
// each tool via the Deferrable interface.
//
// sdkToolsByName maps tool names to their tools.Tool instances so the
// Deferrable interface can be checked.
func SplitToolsByDeferred(providerTools []provider.Tool, sdkToolsByName map[string]tools.Tool) (eager, deferred []provider.Tool) {
	eager = make([]provider.Tool, 0, len(providerTools))
	deferred = make([]provider.Tool, 0)

	for _, pt := range providerTools {
		// ToolSearchTool is always eager
		if pt.Name == ToolSearchName {
			eager = append(eager, pt)
			continue
		}

		sdkTool, exists := sdkToolsByName[pt.Name]
		if !exists {
			eager = append(eager, pt) // unknown → eager for safety
			continue
		}

		if d, ok := sdkTool.(Deferrable); ok && d.ShouldDefer() {
			deferred = append(deferred, pt)
		} else {
			eager = append(eager, pt)
		}
	}

	return eager, deferred
}

// ---------------------------------------------------------------------------
// Intelligent default categorisation (Claude Code pattern)
// ---------------------------------------------------------------------------

// coreToolPatterns are substrings that identify tools which must always be
// eager. These are the tools the LLM uses on virtually every turn.
// Matching is case-insensitive and uses Contains.
var coreToolPatterns = []string{
	"bash",        // Shell execution — used constantly
	"read",        // File reading — used constantly
	"write",       // File writing
	"edit",        // File editing
	"grep",        // Code search
	"apply_patch", // Patch application
	"tool_search", // Meta-tool — must always be eager
	"task",        // Task management tools (Task, TaskCreate, TaskUpdate, TaskGet, TaskList, etc.)
	"background",  // Background task tools (Task, TaskOutput)
}

// alwaysDeferPatterns are substrings that identify tools which are good
// candidates for deferral. These are heavy, niche, or rarely-used tools.
var alwaysDeferPatterns = []string{
	"fullstack_project_init", // Very heavy (~954 tokens), rarely used
	"save_checkpoint",        // Dev workflow, not every-turn
	"register_deployment",    // Dev workflow, not every-turn
	"debug_logs",             // Debug only
}

// IsCoreTool returns true if the tool name matches a core tool pattern.
// Core tools are always eager regardless of token threshold.
func IsCoreTool(toolName string) bool {
	lower := strings.ToLower(toolName)
	for _, pattern := range coreToolPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// IsAlwaysDefer returns true if the tool name matches a pattern that should
// always be deferred when advanced tool mode is active.
func IsAlwaysDefer(toolName string) bool {
	lower := strings.ToLower(toolName)
	for _, pattern := range alwaysDeferPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// ApplySmartDefaults configures a DeferredRegistry with intelligent defaults
// matching the Claude Code pattern:
//
//  1. Core tools (bash, read, write, edit, grep, apply_patch) → always eager
//  2. Tools matching alwaysDeferPatterns → always deferred
//  3. Tools exceeding tokenThreshold → auto-deferred (unless core)
//  4. Everything else → eager
//
// Returns the number of tools deferred.
func ApplySmartDefaults(dr *DeferredRegistry, registry tools.Registry, tokenThreshold int) int {
	deferred := 0
	for _, name := range registry.List() {
		// Core tools are never deferred
		if IsCoreTool(name) {
			dr.ForceDefer(name, false)
			continue
		}

		// Always-defer tools
		if IsAlwaysDefer(name) {
			dr.ForceDefer(name, true)
			deferred++
			continue
		}

		// Token threshold check
		tool, err := registry.Get(name)
		if err != nil || tool == nil {
			continue
		}
		tokens := EstimateToolTokens(tool)
		if tokens > tokenThreshold {
			dr.ForceDefer(name, true)
			deferred++
		}
	}
	return deferred
}
