package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// CacheDebugger captures and logs cache-related data at different transformation stages
type CacheDebugger struct {
	debugDir string
}

// NewCacheDebugger creates a new cache debugger
func NewCacheDebugger() *CacheDebugger {
	debugDir := filepath.Join(os.TempDir(), "cache-debug")
	os.MkdirAll(debugDir, 0755)
	return &CacheDebugger{debugDir: debugDir}
}

// LogRawAnthropicResponse logs the raw JSON from Anthropic API
func (cd *CacheDebugger) LogRawAnthropicResponse(resp any, eventType string) {
	filename := filepath.Join(cd.debugDir, fmt.Sprintf("1_raw_anthropic_%s_%d.json", eventType, time.Now().UnixNano()))
	data, _ := json.MarshalIndent(resp, "", "  ")
	os.WriteFile(filename, data, 0644)
}

// LogCanonicalFormat logs the canonical provider format
func (cd *CacheDebugger) LogCanonicalFormat(chatResp *provider.ChatResponse) {
	filename := filepath.Join(cd.debugDir, fmt.Sprintf("2_canonical_%d.json", time.Now().UnixNano()))

	canonical := map[string]any{
		"message":       chatResp.Message,
		"finish_reason": chatResp.FinishReason,
		"usage":         chatResp.Usage,
	}

	// Extract cache metrics if present
	if chatResp.Message.Metadata != nil {
		if cacheMetrics, ok := chatResp.Message.Metadata["cache_metrics"]; ok {
			canonical["cache_metrics"] = cacheMetrics
		}
	}

	data, _ := json.MarshalIndent(canonical, "", "  ")
	os.WriteFile(filename, data, 0644)
}

// LogTransformedFormat logs the final transformed conversation.Message format
func (cd *CacheDebugger) LogTransformedFormat(msg *conversation.Message) {
	filename := filepath.Join(cd.debugDir, fmt.Sprintf("3_transformed_%d.json", time.Now().UnixNano()))

	transformed := map[string]any{
		"role":     msg.Role,
		"content":  msg.Content,
		"metadata": msg.Metadata,
		"thinking": msg.Thinking,
	}

	data, _ := json.MarshalIndent(transformed, "", "  ")
	os.WriteFile(filename, data, 0644)
}

// ComparisonReport generates a comparison report
func (cd *CacheDebugger) ComparisonReport() string {
	files, _ := os.ReadDir(cd.debugDir)

	var report strings.Builder
	report.WriteString(fmt.Sprintf("=== Cache Debug Report ===\nDebug dir: %s\n\n", cd.debugDir))
	report.WriteString(fmt.Sprintf("Files logged: %d\n\n", len(files)))

	for _, f := range files {
		report.WriteString(fmt.Sprintf("- %s\n", f.Name()))
	}

	report.WriteString(fmt.Sprintf("\nOpen debug files:\nls -la %s/\n", cd.debugDir))
	report.WriteString(fmt.Sprintf("diff %s/1_raw_anthropic_* %s/2_canonical_*\n", cd.debugDir, cd.debugDir))

	return report.String()
}

// GetDebugDir returns the debug directory path
func (cd *CacheDebugger) GetDebugDir() string {
	return cd.debugDir
}

// LogStreamEvent logs a single stream event
func (cd *CacheDebugger) LogStreamEvent(rawJSON []byte, eventType string) {
	filename := filepath.Join(cd.debugDir, fmt.Sprintf("stream_event_%s_%d.json", eventType, time.Now().UnixNano()))

	var prettyJSON map[string]any
	json.Unmarshal(rawJSON, &prettyJSON)

	data, _ := json.MarshalIndent(prettyJSON, "", "  ")
	os.WriteFile(filename, data, 0644)
}
