package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func convertSDKMessages(sdkMessages []*conversation.Message) []*Message {
	result := make([]*Message, 0, len(sdkMessages))

	for _, sdkMsg := range sdkMessages {
		if isGeneratedCompactionMessage(sdkMsg) {
			// Compaction-generated handoff messages (LLM summary, restored files,
			// task list, mode context, recent-messages blocks) are provider-visible
			// but were previously dropped entirely from the TUI transcript, making
			// bad/thin compaction summaries invisible to the user. Instead of
			// silently skipping them, render them as a "system" message: the
			// existing chat renderer (app_chat_render.go) already collapses
			// Role=="system" messages to a single summary line by default and
			// only expands them in verbose mode (Ctrl+O / a.showFullToolOutput),
			// so reusing that role gives us "collapsed by default, inspectable
			// on demand" for free without touching any renderer file.
			//
			// This only affects what is displayed in the TUI transcript; it does
			// not change token counting or provider-visible behavior, both of
			// which remain governed by conversation.ActiveContextStart elsewhere.
			result = append(result, convertCompactionHandoffMessage(sdkMsg))
			continue
		}
		tuiMsg := &Message{
			Role:      string(sdkMsg.Role),
			Content:   sdkMsg.Content,
			Timestamp: sdkMsg.Timestamp,
			Metadata:  sdkMsg.Metadata,
			A2A:       sdkMsg.A2A,
		}

		// For assistant messages from history, mark as complete and set model
		if sdkMsg.Role == conversation.RoleAssistant {
			tuiMsg.IsComplete = true
			tuiMsg.Model = sdkMsg.Model

			// Check metadata for elapsed time (if it was saved)
			if sdkMsg.Metadata != nil {
				if elapsed, ok := sdkMsg.Metadata["elapsed_time_ns"].(float64); ok {
					tuiMsg.ElapsedTime = time.Duration(elapsed)
				}
			}
		}

		// Convert tool calls
		if len(sdkMsg.ToolCalls) > 0 {
			tuiMsg.ToolCalls = make([]ToolCallDisplay, len(sdkMsg.ToolCalls))
			for i, tc := range sdkMsg.ToolCalls {
				tuiMsg.ToolCalls[i] = ToolCallDisplay{
					ID:         tc.ID,
					Name:       tc.Name,
					Parameters: tc.Parameters,
				}
			}
		}

		// Convert tool results
		if len(sdkMsg.ToolResults) > 0 {
			tuiMsg.ToolResults = make([]ToolResultDisplay, len(sdkMsg.ToolResults))
			for i, tr := range sdkMsg.ToolResults {
				errorStr := ""
				if tr.Error != nil {
					errorStr = tr.Error.Message
				}
				tuiMsg.ToolResults[i] = ToolResultDisplay{
					CallID: tr.CallID,
					Output: tr.Output,
					Error:  errorStr,
				}
			}
		}

		// Get thinking content (direct field takes precedence over metadata)
		if sdkMsg.Thinking != "" {
			tuiMsg.Thinking = sdkMsg.Thinking
		} else if sdkMsg.Metadata != nil {
			// Fallback: check metadata for thinking
			if thinking, ok := sdkMsg.Metadata["thinking"].(string); ok {
				tuiMsg.Thinking = thinking
			}
		}

		result = append(result, tuiMsg)
	}

	return result
}

// compactionHandoffMarker prefixes the rendered content of a compaction-generated
// handoff message so it is visually distinguishable in the chat transcript, and
// so a follow-up collapsible renderer can key off the marker if desired.
const compactionHandoffMarker = "[Compaction handoff — context was summarized here]\n\n"

// compactionHandoffMetadataKey flags a converted *Message as a rendered
// compaction handoff, in addition to the pre-existing
// conversation.CompactionGeneratedMetadataKey carried through from the SDK
// message's own metadata. Renderers that want to special-case this message
// type (e.g. a future dedicated collapsible widget) can check either key.
const compactionHandoffMetadataKey = "compaction_handoff"

// convertCompactionHandoffMessage converts a compaction-generated SDK message
// (isGeneratedCompactionMessage == true) into a renderable *Message instead of
// dropping it. The message is tagged with Role "system" so it participates in
// the existing collapsed-by-default system-message rendering path, its
// original Content is preserved verbatim (prefixed with a short synthetic
// marker line for visual distinction), and its metadata (including
// conversation.CompactionGeneratedMetadataKey) is carried through unmodified
// plus the additional compactionHandoffMetadataKey flag.
func convertCompactionHandoffMessage(sdkMsg *conversation.Message) *Message {
	metadata := make(map[string]any, len(sdkMsg.Metadata)+1)
	for k, v := range sdkMsg.Metadata {
		metadata[k] = v
	}
	metadata[compactionHandoffMetadataKey] = true

	return &Message{
		Role:      string(conversation.RoleSystem),
		Content:   compactionHandoffMarker + sdkMsg.Content,
		Timestamp: sdkMsg.Timestamp,
		Metadata:  metadata,
		A2A:       sdkMsg.A2A,
	}
}

func isGeneratedCompactionMessage(msg *conversation.Message) bool {
	if msg == nil {
		return true
	}
	generated, _ := msg.Metadata[conversation.CompactionGeneratedMetadataKey].(bool)
	return generated
}

func cloneGeneratedCompactionMessages(messages []*conversation.Message) ([]*conversation.Message, error) {
	cloned := make([]*conversation.Message, len(messages))
	for i, msg := range messages {
		if msg == nil {
			return nil, fmt.Errorf("compaction message %d is nil", i)
		}
		cloned[i] = msg.Clone()
		if cloned[i].Metadata == nil {
			cloned[i].Metadata = make(map[string]any)
		}
		cloned[i].Metadata[conversation.CompactionGeneratedMetadataKey] = true
	}
	return cloned, nil
}

// getMetricsPath returns the directory where metrics should be stored
func getMetricsPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	metricsDir := filepath.Join(configDir, "swarm-tui", "metrics")
	// Create directory if it doesn't exist
	os.MkdirAll(metricsDir, 0755)
	return metricsDir
}

// truncateForLog is a helper function to truncate strings for logging
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen < 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// recordCacheMetricsFromResponse records cache operations from API response metadata
func (a *App) recordCacheMetricsFromResponse(metadata map[string]any) {
	if a.metrics == nil || metadata == nil {
		return
	}

	// Extract cache metrics from response
	cacheMetrics, ok := metadata["cache_metrics"].(map[string]any)
	if !ok {
		return
	}

	// Extract cache creation tokens (various TTL durations)
	cacheCreation := 0
	if val, ok := asInt(cacheMetrics["cache_creation_tokens"]); ok {
		cacheCreation += val
		a.metrics.Cache.RecordCacheWrite(true) // Successful cache write
	}
	if val, ok := asInt(cacheMetrics["cache_creation_5m_tokens"]); ok {
		cacheCreation += val
	}
	if val, ok := asInt(cacheMetrics["cache_creation_1h_tokens"]); ok {
		cacheCreation += val
	}

	// Extract cache read tokens
	cacheRead := 0
	if val, ok := asInt(cacheMetrics["cache_read_tokens"]); ok {
		cacheRead = val
		if val > 0 {
			// Cache hit - record successful read
			a.metrics.Cache.RecordCacheRead(true)
		}
	}

	// If we have cache creation but no reads, it's a cache miss (write without read)
	if cacheCreation > 0 && cacheRead == 0 {
		a.metrics.Cache.RecordCacheRead(false) // Cache miss
	}

	// Invalidate side panel cache so metrics bar updates
	if cacheCreation > 0 || cacheRead > 0 {
		a.sidePanelCache.valid = false
	}

	logDebug("[METRICS-CACHE] Recorded cache ops: creation=%d read=%d", cacheCreation, cacheRead)
}

// asInt coerces a JSON-decoded numeric value to int. Values that round-trip
// through encoding/json land as float64, not int, so a bare type assertion
// against int silently drops every real response: this accepts every numeric
// representation a metadata map can plausibly carry.
func asInt(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case float32:
		return int(v), true
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case json.Number:
		parsed, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	default:
		return 0, false
	}
}
