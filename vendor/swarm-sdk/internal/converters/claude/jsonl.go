// Package claude provides JSONL types for the real Claude Code on-disk format
package claude

import (
	"encoding/json"
	"strings"
)

// ClaudeJSONLRecord represents one line in a .jsonl session file
type ClaudeJSONLRecord struct {
	Type        string              `json:"type"`
	UUID        string              `json:"uuid"`
	SessionID   string              `json:"sessionId"`
	Timestamp   string              `json:"timestamp"`
	ParentUUID  *string             `json:"parentUuid,omitempty"`
	IsSidechain bool                `json:"isSidechain"`
	UserType    string              `json:"userType,omitempty"`
	CWD         string              `json:"cwd,omitempty"`
	Version     string              `json:"version,omitempty"`
	GitBranch   string              `json:"gitBranch,omitempty"`
	AgentID     string              `json:"agentId,omitempty"`
	Message     *ClaudeJSONLMessage `json:"message,omitempty"`
	ToolUseID   string              `json:"toolUseID,omitempty"`
	RequestID   string              `json:"requestId,omitempty"`
}

// ClaudeJSONLMessage represents a message in Claude Code format
type ClaudeJSONLMessage struct {
	Role       string            `json:"role"`
	Content    json.RawMessage   `json:"content"` // can be string or []ContentBlock
	Model      string            `json:"model,omitempty"`
	ID         string            `json:"id,omitempty"`
	Type       string            `json:"type,omitempty"` // "message"
	Usage      *ClaudeJSONLUsage `json:"usage,omitempty"`
	StopReason string            `json:"stop_reason,omitempty"`
}

// ClaudeContentBlock represents a single content block in a message
type ClaudeContentBlock struct {
	Type      string         `json:"type"` // "text", "tool_use", "tool_result", "thinking"
	Text      string         `json:"text,omitempty"`
	Thinking  string         `json:"thinking,omitempty"`
	ID        string         `json:"id,omitempty"`   // tool_use id
	Name      string         `json:"name,omitempty"` // tool name
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"` // tool_result reference
	Content   string         `json:"content,omitempty"`     // tool_result content
	IsError   bool           `json:"is_error,omitempty"`
	Signature string         `json:"signature,omitempty"` // thinking signature
}

// ClaudeJSONLUsage represents token usage in a message
type ClaudeJSONLUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens"`
}

// ClaudeSessionsIndex represents the sessions-index.json file
type ClaudeSessionsIndex struct {
	Version int                       `json:"version"`
	Entries []ClaudeSessionIndexEntry `json:"entries"`
}

// ClaudeSessionIndexEntry represents one entry in sessions-index.json
type ClaudeSessionIndexEntry struct {
	SessionID    string `json:"sessionId"`
	FullPath     string `json:"fullPath"`
	FileMtime    int64  `json:"fileMtime"`
	FirstPrompt  string `json:"firstPrompt"`
	Summary      string `json:"summary"`
	MessageCount int    `json:"messageCount"`
	Created      string `json:"created"`
	Modified     string `json:"modified"`
	GitBranch    string `json:"gitBranch"`
	ProjectPath  string `json:"projectPath"`
	IsSidechain  bool   `json:"isSidechain,omitempty"`
}

// ParseContentBlocks parses the content field which can be either a string or array of blocks
func ParseContentBlocks(rawContent json.RawMessage) ([]ClaudeContentBlock, string, error) {
	// Try to parse as array first
	var blocks []ClaudeContentBlock
	if err := json.Unmarshal(rawContent, &blocks); err == nil && len(blocks) > 0 {
		// Successfully parsed as array
		var textContent strings.Builder
		for _, block := range blocks {
			if block.Type == "text" {
				textContent.WriteString(block.Text)
			}
		}
		return blocks, textContent.String(), nil
	}

	// Try to parse as string
	var textContent string
	if err := json.Unmarshal(rawContent, &textContent); err == nil {
		return nil, textContent, nil
	}

	// If neither works, return empty
	return nil, "", nil
}
