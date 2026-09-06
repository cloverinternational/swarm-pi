// Package tui provides optimized initialization for the Swarm TUI
package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/pkg/fastjson"
	"github.com/Swarm-Code/mono/swarm-core/pkg/pool"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// OptimizationConfig holds all performance optimization settings for the TUI
type OptimizationConfig struct {
	EnableOptimizations bool

	// Pool sizes (0 means use defaults)
	ToolPoolSize        int
	AgentPoolSize       int
	HookPoolSize        int
	JSONPoolSize        int
	CompressionPoolSize int

	// Feature flags
	EnableJSONOptimization bool
	EnableCompression      bool
	EnableBatching         bool

	// CloudFlare/WASM mode
	IsCloudFlareMode bool
}

// DefaultOptimizationConfig returns default optimization settings
func DefaultOptimizationConfig() *OptimizationConfig {
	config := &OptimizationConfig{
		EnableOptimizations:    true,
		EnableJSONOptimization: true,
		EnableCompression:      true,
		EnableBatching:         true,
	}

	// Load from environment
	if val := os.Getenv("SWARM_OPTIMIZATIONS"); val == "0" || val == "false" {
		config.EnableOptimizations = false
		return config
	}

	// Pool sizes from environment
	if val, err := strconv.Atoi(os.Getenv("SWARM_POOL_SIZE_TOOLS")); err == nil && val > 0 {
		config.ToolPoolSize = val
	}
	if val, err := strconv.Atoi(os.Getenv("SWARM_POOL_SIZE_AGENTS")); err == nil && val > 0 {
		config.AgentPoolSize = val
	}
	if val, err := strconv.Atoi(os.Getenv("SWARM_POOL_SIZE_HOOKS")); err == nil && val > 0 {
		config.HookPoolSize = val
	}

	// CloudFlare mode detection
	if os.Getenv("CF_WORKER") == "1" || os.Getenv("WASM") == "1" {
		config.IsCloudFlareMode = true
		// Adjust defaults for constrained environment
		if config.ToolPoolSize == 0 {
			config.ToolPoolSize = 10
		}
		if config.AgentPoolSize == 0 {
			config.AgentPoolSize = 5
		}
		if config.HookPoolSize == 0 {
			config.HookPoolSize = 10
		}
		config.JSONPoolSize = 5
		config.CompressionPoolSize = 3
	}

	return config
}

// InitializeOptimizations sets up all performance optimizations
func InitializeOptimizations(ctx context.Context, config *OptimizationConfig) (*pool.Manager, error) {
	if !config.EnableOptimizations {
		return nil, nil
	}

	// Reuse the process-wide manager rather than building a second one.
	// main.go already calls pool.Default(), and NewManager() eagerly creates
	// the full DefaultConfigs set (tools/agents/hooks/json/compression), most
	// with WithPreAlloc. Constructing a second manager here therefore doubled
	// every worker queue and its ticktock + purgeStaleWorkers goroutines,
	// while the pool.Default() set — wired to nothing — sat idle for the whole
	// process lifetime. Close() is CompareAndSwap-guarded, so the defer in
	// main.go and the one on the returned manager are safe on the same value.
	poolManager := pool.Default()

	// Apply pool size overrides. These must use Resize, not CreatePool: the
	// manager constructor already created each default pool, so CreatePool
	// returns "pool %s already exists". That made every override path fail
	// closed — with SWARM_POOL_SIZE_* set (or CF_WORKER/WASM mode, which
	// assigns sizes automatically) InitializeOptimizations returned an error
	// and the tool/agent/hook pool globals below were never wired at all.
	resize := func(name string, poolType pool.PoolType, size int) error {
		if size <= 0 {
			return nil
		}
		if err := poolManager.Resize(poolType, size); err != nil {
			return fmt.Errorf("failed to resize %s pool: %w", name, err)
		}
		return nil
	}
	if err := resize("tools", pool.PoolTypeTools, config.ToolPoolSize); err != nil {
		return nil, err
	}
	if err := resize("agents", pool.PoolTypeAgents, config.AgentPoolSize); err != nil {
		return nil, err
	}
	if err := resize("hooks", pool.PoolTypeHooks, config.HookPoolSize); err != nil {
		return nil, err
	}

	// Initialize optimized JSON engine
	if config.EnableJSONOptimization {
		_ = fastjson.NewEngine(poolManager)
	}

	// Set up tool runtime pooling
	tools.SetGlobalPoolManager(poolManager)

	// Set up agent pooling
	agent.SetAgentPoolManager(poolManager)

	// Set up hook pooling
	hooks.SetPoolManager(poolManager)

	// Start monitoring
	go monitorPools(ctx, poolManager)

	return poolManager, nil
}

// monitorPools periodically logs pool statistics
func monitorPools(ctx context.Context, pm *pool.Manager) {
	// Only log in debug mode
	if os.Getenv("SWARM_DEBUG") != "1" {
		return
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			logPoolStats(pm)
		}
	}
}

// logPoolStats logs current pool statistics (only when SWARM_DEBUG=1)
func logPoolStats(pm *pool.Manager) {
	// Silently collect metrics - only log in debug mode
	// Debug mode check is done in monitorPools before calling this
}
