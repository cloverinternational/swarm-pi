package chat

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// CacheManager handles intelligent cache invalidation
type CacheManager struct {
	systemPromptHash    string
	toolDefinitionsHash string
	mcpServersHash      string
	stats               *CacheStats // Track invalidation counts
}

// NewCacheManager creates a new cache manager
func NewCacheManager() *CacheManager {
	stats, _ := LoadCacheStats()
	if stats == nil {
		stats = GetDefaultCacheStats()
	}

	return &CacheManager{
		stats: stats,
	}
}

// GetStats returns the cache statistics (implements CacheStatsProvider)
func (cm *CacheManager) GetStats() any {
	return cm.stats
}

// SaveStats persists the cache statistics
func (cm *CacheManager) SaveStats() error {
	return SaveCacheStats(cm.stats)
}

// ComputeSystemPromptHash generates a hash of the system prompt
func (cm *CacheManager) ComputeSystemPromptHash(systemPrompt string) string {
	if systemPrompt == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(systemPrompt))
	return fmt.Sprintf("%x", hash[:8]) // First 8 bytes (16 hex chars)
}

// ComputeToolDefinitionsHash generates a hash of tool definitions
// Tools are sorted by name to ensure consistent hashing
func (cm *CacheManager) ComputeToolDefinitionsHash(tools []provider.Tool) string {
	if len(tools) == 0 {
		return ""
	}

	// Sort tools by name for consistent hashing
	sortedTools := make([]provider.Tool, len(tools))
	copy(sortedTools, tools)
	sort.Slice(sortedTools, func(i, j int) bool {
		return sortedTools[i].Name < sortedTools[j].Name
	})

	// Create a canonical representation
	var parts []string
	for _, tool := range sortedTools {
		// Hash: name|description|parameters_json
		paramsJSON, _ := json.Marshal(tool.Parameters)
		part := fmt.Sprintf("%s|%s|%s", tool.Name, tool.Description, string(paramsJSON))
		parts = append(parts, part)
	}

	combined := strings.Join(parts, "||")
	hash := sha256.Sum256([]byte(combined))
	return fmt.Sprintf("%x", hash[:8])
}

// ComputeMCPServersHash generates a hash of MCP server configurations
func (cm *CacheManager) ComputeMCPServersHash(serverNames []string) string {
	if len(serverNames) == 0 {
		return ""
	}

	// Sort for consistent hashing
	sorted := make([]string, len(serverNames))
	copy(sorted, serverNames)
	sort.Strings(sorted)

	combined := strings.Join(sorted, ",")
	hash := sha256.Sum256([]byte(combined))
	return fmt.Sprintf("%x", hash[:8])
}

// ShouldInvalidateSystemPromptCache checks if system prompt cache should be invalidated
func (cm *CacheManager) ShouldInvalidateSystemPromptCache(newHash string) bool {
	if cm.systemPromptHash == "" {
		// First time, no invalidation needed
		cm.systemPromptHash = newHash
		return false
	}

	if cm.systemPromptHash != newHash {
		logDebug("[CACHE] System prompt changed: %s -> %s", cm.systemPromptHash, newHash)
		cm.systemPromptHash = newHash
		cm.stats.SystemPromptInvalidations++
		if err := cm.SaveStats(); err != nil {
			logDebug("[CACHE] Failed to save stats: %v", err)
		}
		return true
	}

	return false
}

// ShouldInvalidateToolCache checks if tool cache should be invalidated
func (cm *CacheManager) ShouldInvalidateToolCache(newHash string) bool {
	if cm.toolDefinitionsHash == "" {
		cm.toolDefinitionsHash = newHash
		return false
	}

	if cm.toolDefinitionsHash != newHash {
		logDebug("[CACHE] Tool definitions changed: %s -> %s", cm.toolDefinitionsHash, newHash)
		cm.toolDefinitionsHash = newHash
		cm.stats.ToolInvalidations++
		if err := cm.SaveStats(); err != nil {
			logDebug("[CACHE] Failed to save stats: %v", err)
		}
		return true
	}

	return false
}

// ShouldInvalidateMCPCache checks if MCP cache should be invalidated
func (cm *CacheManager) ShouldInvalidateMCPCache(newHash string) bool {
	if cm.mcpServersHash == "" {
		cm.mcpServersHash = newHash
		return false
	}

	if cm.mcpServersHash != newHash {
		logDebug("[CACHE] MCP servers changed: %s -> %s", cm.mcpServersHash, newHash)
		cm.mcpServersHash = newHash
		cm.stats.MCPInvalidations++
		if err := cm.SaveStats(); err != nil {
			logDebug("[CACHE] Failed to save stats: %v", err)
		}
		return true
	}

	return false
}

// GetCurrentHashes returns current hashes for debugging
func (cm *CacheManager) GetCurrentHashes() (systemPrompt, tools, mcp string) {
	return cm.systemPromptHash, cm.toolDefinitionsHash, cm.mcpServersHash
}

// ResetSessionStats resets only the session statistics (implements CacheStatsProvider)
func (cm *CacheManager) ResetSessionStats() {
	if cm.stats != nil {
		cm.stats.ResetSessionStats()
		if err := cm.SaveStats(); err != nil {
			logDebug("[CACHE] Failed to save stats: %v", err)
		}
	}
}

// ResetAllStats resets all statistics including per-model (implements CacheStatsProvider)
func (cm *CacheManager) ResetAllStats() {
	if cm.stats != nil {
		cm.stats.ResetAllStats()
		if err := cm.SaveStats(); err != nil {
			logDebug("[CACHE] Failed to save stats: %v", err)
		}
	}
}
