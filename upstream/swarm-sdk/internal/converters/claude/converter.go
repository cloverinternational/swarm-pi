// Package claude provides import/export functionality for Claude Code conversations
package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// debugLog writes a message to the debug log file
func debugLog(message string) {
	logFile := "/tmp/swarm-import-debug.log"
	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	logEntry := fmt.Sprintf("[%s] %s\n", timestamp, message)

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(logEntry)
}

// ClaudeConversation represents a conversation in Claude Code format
type ClaudeConversation struct {
	ID         string          `json:"ID"`
	Title      string          `json:"Title,omitempty"`
	Status     string          `json:"Status"`
	Mode       string          `json:"Mode"`
	Messages   []ClaudeMessage `json:"Messages"`
	TokenCount int             `json:"TokenCount"`
	CreatedAt  int64           `json:"CreatedAt"` // Unix timestamp
	UpdatedAt  int64           `json:"UpdatedAt"` // Unix timestamp
	// Additional Claude-specific fields
	ProjectID string `json:"ProjectID,omitempty"`
}

// ClaudeMessage represents a message in Claude Code format
type ClaudeMessage struct {
	Role       string          `json:"Role"`
	Content    string          `json:"Content"`
	ToolUse    []ClaudeToolUse `json:"ToolUse,omitempty"`
	TokenCount int             `json:"TokenCount"`
	Timestamp  int64           `json:"Timestamp"` // Unix timestamp
}

// ClaudeToolUse represents a tool use in Claude Code format
type ClaudeToolUse struct {
	Name   string         `json:"Name"`
	Input  map[string]any `json:"Input"`
	Result any            `json:"Result,omitempty"`
	Status string         `json:"Status,omitempty"`
}

// ClaudeSessionIndex represents the sessions index file
type ClaudeSessionIndex struct {
	Sessions []ClaudeSessionEntry `json:"sessions"`
}

// ClaudeSessionEntry represents an entry in the sessions index
type ClaudeSessionEntry struct {
	SessionID      string `json:"sessionId"`
	ConversationID string `json:"conversationId"`
	Title          string `json:"title"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

// ClaudeCodeConverter handles conversion between Claude Code and Swarm formats
type ClaudeCodeConverter struct {
	claudeBaseDir string
}

// NewClaudeCodeConverter creates a new Claude Code converter
func NewClaudeCodeConverter() *ClaudeCodeConverter {
	homeDir, _ := os.UserHomeDir()
	return &ClaudeCodeConverter{
		claudeBaseDir: filepath.Join(homeDir, ".claude"),
	}
}

// NewClaudeCodeConverterWithBaseDir creates a converter with custom base directory
func NewClaudeCodeConverterWithBaseDir(baseDir string) *ClaudeCodeConverter {
	return &ClaudeCodeConverter{
		claudeBaseDir: baseDir,
	}
}

// ImportConversation converts from Claude Code format to Swarm SDK format
func (c *ClaudeCodeConverter) ImportConversation(claudeConv *ClaudeConversation) (*conversation.Conversation, error) {
	if claudeConv == nil {
		return nil, sdkerr.Permanent("converter.invalid_input", "Claude conversation is nil")
	}

	// Create new Swarm conversation
	conv := &conversation.Conversation{
		ID:        claudeConv.ID,
		Mode:      claudeConv.Mode,
		Status:    c.convertStatus(claudeConv.Status),
		CreatedAt: time.Unix(claudeConv.CreatedAt/1000, 0), // Convert from milliseconds
		UpdatedAt: time.Unix(claudeConv.UpdatedAt/1000, 0),
		Messages:  make([]*conversation.Message, 0, len(claudeConv.Messages)),
		Metadata: conversation.ConversationMetadata{
			Custom: make(map[string]any),
		},
	}

	// Store title in custom metadata if present
	if claudeConv.Title != "" {
		conv.Metadata.Custom["title"] = claudeConv.Title
	}

	// Convert workspace path from project ID if present
	if claudeConv.ProjectID != "" {
		conv.WorkspacePath = c.projectIDToPath(claudeConv.ProjectID)
		conv.Metadata.ProjectID = claudeConv.ProjectID
	}

	// Convert messages
	totalTokens := 0
	for i, claudeMsg := range claudeConv.Messages {
		msg := c.convertMessage(claudeMsg, i)
		conv.Messages = append(conv.Messages, msg)

		if msg.Tokens != nil {
			totalTokens += msg.Tokens.Total
		}
	}

	// Set token counts
	conv.TotalTokens = totalTokens
	if len(conv.Messages) > 0 {
		lastMsg := conv.Messages[len(conv.Messages)-1]
		if lastMsg.Role == conversation.RoleAssistant && lastMsg.Tokens != nil {
			conv.CurrentContextSize = lastMsg.Tokens.InputContextSize()
		}
	}

	return conv, nil
}

// ExportConversation converts from Swarm SDK format to Claude Code format
func (c *ClaudeCodeConverter) ExportConversation(conv *conversation.Conversation) (*ClaudeConversation, error) {
	if conv == nil {
		return nil, sdkerr.Permanent("converter.invalid_input", "Swarm conversation is nil")
	}

	claudeConv := &ClaudeConversation{
		ID:         conv.ID,
		Mode:       conv.Mode,
		Status:     c.convertStatusToCode(conv.Status),
		CreatedAt:  conv.CreatedAt.Unix() * 1000, // Convert to milliseconds
		UpdatedAt:  conv.UpdatedAt.Unix() * 1000,
		TokenCount: conv.TotalTokens,
		Messages:   make([]ClaudeMessage, 0, len(conv.Messages)),
	}

	// Extract title from custom metadata
	if title, ok := conv.Metadata.Custom["title"].(string); ok {
		claudeConv.Title = title
	}

	// Convert workspace path to project ID
	if conv.WorkspacePath != "" {
		claudeConv.ProjectID = c.pathToProjectID(conv.WorkspacePath)
	} else if conv.Metadata.ProjectID != "" {
		claudeConv.ProjectID = conv.Metadata.ProjectID
	}

	// Convert messages
	for _, msg := range conv.Messages {
		claudeMsg := c.convertMessageToCode(msg)
		claudeConv.Messages = append(claudeConv.Messages, claudeMsg)
	}

	return claudeConv, nil
}

// convertMessage converts a Claude message to Swarm format
func (c *ClaudeCodeConverter) convertMessage(claudeMsg ClaudeMessage, index int) *conversation.Message {
	msg := &conversation.Message{
		ID:        fmt.Sprintf("msg_%d_%d", claudeMsg.Timestamp, index),
		Timestamp: time.Unix(claudeMsg.Timestamp/1000, 0),
		Role:      c.convertRole(claudeMsg.Role),
		Content:   claudeMsg.Content,
	}

	// Set token usage if available
	if claudeMsg.TokenCount > 0 {
		msg.Tokens = &conversation.TokenUsage{
			Total: claudeMsg.TokenCount,
		}
	}

	// Convert tool uses
	if len(claudeMsg.ToolUse) > 0 {
		msg.ToolCalls = make([]conversation.ToolCall, 0, len(claudeMsg.ToolUse))
		msg.ToolResults = make([]conversation.ToolResult, 0, len(claudeMsg.ToolUse))

		for i, tool := range claudeMsg.ToolUse {
			// Create tool call
			toolCall := conversation.ToolCall{
				ID:         fmt.Sprintf("call_%d_%d", claudeMsg.Timestamp, i),
				Name:       tool.Name,
				Parameters: tool.Input,
			}
			msg.ToolCalls = append(msg.ToolCalls, toolCall)

			// Create tool result if present
			if tool.Result != nil {
				toolResult := conversation.ToolResult{
					CallID: toolCall.ID,
					Name:   tool.Name,
					Output: fmt.Sprintf("%v", tool.Result),
				}
				msg.ToolResults = append(msg.ToolResults, toolResult)
			}
		}
	}

	return msg
}

// convertMessageToCode converts a Swarm message to Claude Code format
func (c *ClaudeCodeConverter) convertMessageToCode(msg *conversation.Message) ClaudeMessage {
	claudeMsg := ClaudeMessage{
		Role:      c.convertRoleToCode(msg.Role),
		Content:   msg.Content,
		Timestamp: msg.Timestamp.Unix() * 1000, // Convert to milliseconds
	}

	// Set token count
	if msg.Tokens != nil {
		claudeMsg.TokenCount = msg.Tokens.Total
	}

	// Convert tool calls and results
	if len(msg.ToolCalls) > 0 {
		claudeMsg.ToolUse = make([]ClaudeToolUse, 0, len(msg.ToolCalls))

		// Create a map of call IDs to results
		resultMap := make(map[string]*conversation.ToolResult)
		for i := range msg.ToolResults {
			resultMap[msg.ToolResults[i].CallID] = &msg.ToolResults[i]
		}

		// Convert tool calls
		for _, toolCall := range msg.ToolCalls {
			tool := ClaudeToolUse{
				Name:   toolCall.Name,
				Input:  toolCall.Parameters,
				Status: "completed",
			}

			// Add result if available
			if result, ok := resultMap[toolCall.ID]; ok {
				tool.Result = result.Output
				if result.Error != nil {
					tool.Status = "failed"
					tool.Result = fmt.Sprintf("Error: %s", result.Error.Message)
				}
			}

			claudeMsg.ToolUse = append(claudeMsg.ToolUse, tool)
		}
	}

	return claudeMsg
}

// Status conversion helpers

func (c *ClaudeCodeConverter) convertStatus(claudeStatus string) conversation.Status {
	switch strings.ToLower(claudeStatus) {
	case "active":
		return conversation.StatusActive
	case "completed":
		return conversation.StatusCompleted
	case "failed":
		return conversation.StatusFailed
	case "archived":
		return conversation.StatusArchived
	default:
		return conversation.StatusActive
	}
}

func (c *ClaudeCodeConverter) convertStatusToCode(status conversation.Status) string {
	return string(status)
}

// Role conversion helpers

func (c *ClaudeCodeConverter) convertRole(claudeRole string) conversation.Role {
	switch strings.ToLower(claudeRole) {
	case "user":
		return conversation.RoleUser
	case "assistant":
		return conversation.RoleAssistant
	case "system":
		return conversation.RoleSystem
	case "tool":
		return conversation.RoleTool
	default:
		return conversation.RoleUser
	}
}

func (c *ClaudeCodeConverter) convertRoleToCode(role conversation.Role) string {
	return string(role)
}

// Path conversion helpers

// projectIDToPath converts Claude project ID to filesystem path
func (c *ClaudeCodeConverter) projectIDToPath(projectID string) string {
	// "-home-rincon-swarm" -> "/home/rincon/swarm"
	cleaned := strings.TrimPrefix(projectID, "-")
	if cleaned == "" {
		return "/"
	}
	return "/" + strings.ReplaceAll(cleaned, "-", "/")
}

// pathToProjectID converts filesystem path to Claude project ID
func (c *ClaudeCodeConverter) pathToProjectID(path string) string {
	// "/home/rincon/swarm" -> "-home-rincon-swarm"
	normalized := filepath.Clean(path)
	if normalized == "/" {
		return "-"
	}
	components := strings.Split(normalized, string(filepath.Separator))
	// Remove empty components
	filtered := make([]string, 0)
	for _, comp := range components {
		if comp != "" {
			filtered = append(filtered, comp)
		}
	}
	return "-" + strings.Join(filtered, "-")
}

// Batch operations

// ImportProjectConversations imports all conversations from a Claude Code project (JSONL format)
func (c *ClaudeCodeConverter) ImportProjectConversations(projectPath string) ([]*conversation.Conversation, error) {
	debugLog(fmt.Sprintf("=== ImportProjectConversations START ==="))
	debugLog(fmt.Sprintf("projectPath: %s", projectPath))

	projectID := c.pathToProjectID(projectPath)
	debugLog(fmt.Sprintf("projectID: %s", projectID))

	projectDir := filepath.Join(c.claudeBaseDir, "projects", projectID)
	debugLog(fmt.Sprintf("claudeBaseDir: %s", c.claudeBaseDir))
	debugLog(fmt.Sprintf("projectDir: %s", projectDir))

	// Try to read sessions-index.json first
	sessions, err := c.listProjectSessionsFromIndex(projectDir)
	if err != nil || len(sessions) == 0 {
		debugLog(fmt.Sprintf("Index read failed or empty (err=%v, count=%d), falling back to scanning", err, len(sessions)))
		// Fall back to scanning for .jsonl files
		sessions, err = c.listProjectSessionsByScanning(projectDir)
		if err != nil {
			debugLog(fmt.Sprintf("Scanning also failed: %v", err))
			return nil, sdkerr.Wrap(err, "converter.list_sessions_failed", sdkerr.WithAttr("path", projectDir))
		}
	}

	debugLog(fmt.Sprintf("Found %d sessions to import", len(sessions)))

	if len(sessions) == 0 {
		debugLog(fmt.Sprintf("No sessions found, returning error"))
		return nil, sdkerr.Permanent("converter.no_sessions", "No sessions found in project", sdkerr.WithAttr("path", projectPath))
	}

	convs := make([]*conversation.Conversation, 0, len(sessions))
	for i, session := range sessions {
		debugLog(fmt.Sprintf("Importing session %d: %s", i+1, session.FullPath))
		var conv *conversation.Conversation
		var err error
		// .json files were written by ExportToProject (ClaudeConversation format).
		// .jsonl files are native Claude Code session logs.
		if strings.HasSuffix(session.FullPath, ".json") {
			conv, err = c.ImportSingleConversation(session.FullPath)
		} else {
			conv, err = c.importJSONLSession(session.FullPath, projectPath)
		}
		if err != nil {
			debugLog(fmt.Sprintf("Error importing session: %v", err))
			// Log and continue with other sessions
			continue
		}
		debugLog(fmt.Sprintf("Successfully imported session %d with %d messages", i+1, len(conv.Messages)))
		convs = append(convs, conv)
	}

	debugLog(fmt.Sprintf("=== ImportProjectConversations END: imported %d conversations ===", len(convs)))
	return convs, nil
}

// ExportToProject exports conversations to Claude Code project format.
//
// Files are written to:
//
//	<claudeBaseDir>/projects/<projectID>/memory/conversations/<id>.json
//
// The sessions index written at:
//
//	<claudeBaseDir>/projects/<projectID>/sessions-index.json
//
// uses the ClaudeSessionsIndex schema (key "entries", with "fullPath") so that
// ImportProjectConversations / listProjectSessionsFromIndex can round-trip
// correctly.  The legacy ClaudeSessionIndex schema (key "sessions") is only
// used when reading real Claude Code data that predates this format.
func (c *ClaudeCodeConverter) ExportToProject(conversations []*conversation.Conversation, projectPath string) error {
	if len(conversations) == 0 {
		return sdkerr.Permanent("converter.no_conversations", "no conversations to export")
	}

	projectID := c.pathToProjectID(projectPath)
	claudeProjectDir := filepath.Join(c.claudeBaseDir, "projects", projectID)
	conversationsDir := filepath.Join(claudeProjectDir, "memory", "conversations")

	// Create directory structure
	if err := os.MkdirAll(conversationsDir, 0755); err != nil {
		return sdkerr.Wrap(err, "converter.mkdir_failed", sdkerr.WithAttr("path", conversationsDir))
	}

	// Build the sessions index using ClaudeSessionsIndex (the canonical read format).
	sessionsIndex := ClaudeSessionsIndex{
		Version: 1,
		Entries: make([]ClaudeSessionIndexEntry, 0, len(conversations)),
	}

	var errors []string

	for _, conv := range conversations {
		// Convert to Claude format
		claudeConv, err := c.ExportConversation(conv)
		if err != nil {
			errors = append(errors, fmt.Sprintf("failed to convert %s: %v", conv.ID, err))
			continue
		}
		claudeConv.ProjectID = projectID

		// Marshal and write conversation file
		data, err := json.MarshalIndent(claudeConv, "", "  ")
		if err != nil {
			errors = append(errors, fmt.Sprintf("failed to marshal %s: %v", conv.ID, err))
			continue
		}
		fileName := fmt.Sprintf("%s.json", conv.ID)
		filePath := filepath.Join(conversationsDir, fileName)
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			errors = append(errors, fmt.Sprintf("failed to write %s: %v", fileName, err))
			continue
		}

		// Build index entry with the absolute path so listProjectSessionsFromIndex
		// can find the file without scanning.
		summary := ""
		if t, ok := conv.Metadata.Custom["title"].(string); ok {
			summary = t
		}
		sessionsIndex.Entries = append(sessionsIndex.Entries, ClaudeSessionIndexEntry{
			SessionID:    conv.ID,
			FullPath:     filePath,
			FileMtime:    conv.UpdatedAt.Unix() * 1000,
			Summary:      summary,
			MessageCount: len(conv.Messages),
			ProjectPath:  projectPath,
		})
	}

	// Write sessions index
	indexData, err := json.MarshalIndent(sessionsIndex, "", "  ")
	if err != nil {
		return sdkerr.Wrap(err, "converter.index_marshal_failed")
	}
	indexPath := filepath.Join(claudeProjectDir, "sessions-index.json")
	if err := os.WriteFile(indexPath, indexData, 0644); err != nil {
		return sdkerr.Wrap(err, "converter.index_write_failed")
	}

	if len(errors) > 0 {
		return sdkerr.Permanent("converter.export_partial",
			fmt.Sprintf("exported with errors: %s", strings.Join(errors, "; ")))
	}
	return nil
}

// ImportSingleConversation imports a single conversation file
func (c *ClaudeCodeConverter) ImportSingleConversation(filePath string) (*conversation.Conversation, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, sdkerr.Wrap(err, "converter.read_file_failed", sdkerr.WithAttr("path", filePath))
	}

	var claudeConv ClaudeConversation
	if err := json.Unmarshal(data, &claudeConv); err != nil {
		return nil, sdkerr.Permanent("converter.parse_failed", fmt.Sprintf("failed to parse Claude conversation: %v", err), sdkerr.WithAttr("path", filePath))
	}

	// Try to infer project ID from file path
	if claudeConv.ProjectID == "" && strings.Contains(filePath, "/.claude/projects/") {
		parts := strings.Split(filePath, "/.claude/projects/")
		if len(parts) > 1 {
			projectParts := strings.Split(parts[1], "/")
			if len(projectParts) > 0 {
				claudeConv.ProjectID = projectParts[0]
			}
		}
	}

	return c.ImportConversation(&claudeConv)
}

// ExportSingleConversation exports a single conversation to a file
func (c *ClaudeCodeConverter) ExportSingleConversation(conv *conversation.Conversation, filePath string) error {
	claudeConv, err := c.ExportConversation(conv)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(claudeConv, "", "  ")
	if err != nil {
		return sdkerr.Permanent("converter.marshal_failed",
			fmt.Sprintf("failed to marshal conversation: %v", err))
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return sdkerr.Wrap(err, "converter.mkdir_failed", sdkerr.WithAttr("path", dir))
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return sdkerr.Wrap(err, "converter.write_file_failed", sdkerr.WithAttr("path", filePath))
	}
	return nil
}

// ListClaudeProjects returns a list of all Claude projects
func (c *ClaudeCodeConverter) ListClaudeProjects() ([]string, error) {
	projectsDir := filepath.Join(c.claudeBaseDir, "projects")
	debugLog(fmt.Sprintf("ListClaudeProjects: scanning directory: %s", projectsDir))

	files, err := ioutil.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			debugLog(fmt.Sprintf("Projects directory does not exist: %s", projectsDir))
			return []string{}, nil
		}
		debugLog(fmt.Sprintf("Error reading projects directory: %v", err))
		return nil, sdkerr.Wrap(err, "converter.list_projects_failed", sdkerr.WithAttr("path", projectsDir))
	}

	debugLog(fmt.Sprintf("Found %d entries in projects directory", len(files)))

	projects := make([]string, 0)
	for _, file := range files {
		if file.IsDir() {
			// Convert project ID to path
			projectPath := c.projectIDToPath(file.Name())
			debugLog(fmt.Sprintf("  Project: %s (ID=%s)", projectPath, file.Name()))
			projects = append(projects, projectPath)
		}
	}

	debugLog(fmt.Sprintf("ListClaudeProjects returning %d projects", len(projects)))
	return projects, nil
}

// Helper functions for JSONL session browsing and import

// ListProjectSessions returns all sessions in a project directory (public version for TUI)
func (c *ClaudeCodeConverter) ListProjectSessions(projectPath string) ([]ClaudeSessionIndexEntry, error) {
	debugLog(fmt.Sprintf("ListProjectSessions called with projectPath: %s", projectPath))

	projectID := c.pathToProjectID(projectPath)
	debugLog(fmt.Sprintf("Converted projectPath to projectID: %s", projectID))

	projectDir := filepath.Join(c.claudeBaseDir, "projects", projectID)
	debugLog(fmt.Sprintf("claudeBaseDir: %s", c.claudeBaseDir))
	debugLog(fmt.Sprintf("Constructed projectDir: %s", projectDir))

	// Check if projectDir exists
	if info, err := os.Stat(projectDir); err != nil {
		debugLog(fmt.Sprintf("projectDir does not exist or cannot be stat'd: %v", err))
	} else {
		debugLog(fmt.Sprintf("projectDir exists: isDir=%v, mode=%v", info.IsDir(), info.Mode()))
	}

	// Try to read sessions from index first
	sessions, err := c.listProjectSessionsFromIndex(projectDir)
	if err != nil {
		debugLog(fmt.Sprintf("listProjectSessionsFromIndex returned error: %v", err))
		return nil, err
	}

	debugLog(fmt.Sprintf("listProjectSessionsFromIndex returned %d sessions", len(sessions)))

	// If we got sessions from index, return them
	if len(sessions) > 0 {
		debugLog(fmt.Sprintf("Returning %d sessions from index", len(sessions)))
		return sessions, nil
	}

	// Fall back to scanning for .jsonl files if index is empty or missing
	debugLog("Index is empty, falling back to scanning for .jsonl files")
	sessions, err = c.listProjectSessionsByScanning(projectDir)
	debugLog(fmt.Sprintf("listProjectSessionsByScanning returned %d sessions, error: %v", len(sessions), err))
	return sessions, err
}

// listProjectSessionsFromIndex reads sessions from sessions-index.json
func (c *ClaudeCodeConverter) listProjectSessionsFromIndex(projectDir string) ([]ClaudeSessionIndexEntry, error) {
	indexPath := filepath.Join(projectDir, "sessions-index.json")
	debugLog(fmt.Sprintf("=== listProjectSessionsFromIndex START ==="))
	debugLog(fmt.Sprintf("Checking indexPath: %s", indexPath))

	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			debugLog(fmt.Sprintf("Index file does not exist: %s", indexPath))
			return nil, nil // No index file, will fall back to scanning
		}
		debugLog(fmt.Sprintf("ERROR reading index file: %v", err))
		return nil, err
	}

	debugLog(fmt.Sprintf("Index file found and read, size: %d bytes", len(data)))

	var index ClaudeSessionsIndex
	if err := json.Unmarshal(data, &index); err != nil {
		debugLog(fmt.Sprintf("ERROR parsing index JSON: %v", err))
		return nil, err
	}

	debugLog(fmt.Sprintf("Successfully parsed index, found %d entries", len(index.Entries)))

	// Verify and fix paths in entries
	validEntries := make([]ClaudeSessionIndexEntry, 0)
	for i, entry := range index.Entries {
		debugLog(fmt.Sprintf("[%d/%d] Entry: SessionID=%s", i+1, len(index.Entries), entry.SessionID))
		debugLog(fmt.Sprintf("       Original FullPath: %s", entry.FullPath))

		// Check if the path in index actually exists
		if _, err := os.Stat(entry.FullPath); err == nil {
			debugLog(fmt.Sprintf("       ✓ FullPath exists (verified)"))
			validEntries = append(validEntries, entry)
		} else {
			debugLog(fmt.Sprintf("       ✗ FullPath does not exist: %v", err))
			debugLog(fmt.Sprintf("       Checking for session directory alternative..."))

			// Try to find the actual JSONL file in the session directory
			sessionDir := filepath.Join(projectDir, entry.SessionID)
			subagentDir := filepath.Join(sessionDir, "subagents")

			debugLog(fmt.Sprintf("       Trying session dir: %s", sessionDir))

			// Check if session dir exists
			if info, err := os.Stat(sessionDir); err != nil || !info.IsDir() {
				debugLog(fmt.Sprintf("       Session dir does not exist: %v", err))
				continue // Skip this entry
			}

			// Check for subagents
			subagentFiles, err := ioutil.ReadDir(subagentDir)
			if err == nil && len(subagentFiles) > 0 {
				debugLog(fmt.Sprintf("       Found subagents dir with %d files", len(subagentFiles)))

				// Use first JSONL file found
				for _, subFile := range subagentFiles {
					if !subFile.IsDir() && strings.HasSuffix(subFile.Name(), ".jsonl") {
						correctedPath := filepath.Join(subagentDir, subFile.Name())
						debugLog(fmt.Sprintf("       ✓ Using subagent JSONL: %s", correctedPath))

						// Update entry with correct path
						entry.FullPath = correctedPath
						validEntries = append(validEntries, entry)
						break
					}
				}
			} else {
				debugLog(fmt.Sprintf("       No subagents found: %v", err))
			}
		}
	}

	debugLog(fmt.Sprintf("=== listProjectSessionsFromIndex COMPLETE ==="))
	debugLog(fmt.Sprintf("Results: %d/%d entries valid", len(validEntries), len(index.Entries)))
	for i, entry := range validEntries {
		debugLog(fmt.Sprintf("  Result[%d]: SessionID=%s, FullPath=%s", i, entry.SessionID, entry.FullPath))
	}

	return validEntries, nil
}

// listProjectSessionsByScanning scans for .jsonl files in the project directory
// Handles two patterns:
// 1. Direct JSONL files in project root: /projects/<id>/*.jsonl
// 2. Session directories with nested JSONL: /projects/<id>/<session-id>/subagents/*.jsonl
func (c *ClaudeCodeConverter) listProjectSessionsByScanning(projectDir string) ([]ClaudeSessionIndexEntry, error) {
	debugLog(fmt.Sprintf("=== listProjectSessionsByScanning START ==="))
	debugLog(fmt.Sprintf("Scanning directory: %s", projectDir))

	entries := make([]ClaudeSessionIndexEntry, 0)
	seenSessionIDs := make(map[string]bool)

	// Check if directory exists
	if info, err := os.Stat(projectDir); err != nil {
		debugLog(fmt.Sprintf("STAT ERROR: Directory does not exist: %v", err))
		return nil, err
	} else {
		debugLog(fmt.Sprintf("STAT OK: isDir=%v, mode=%v", info.IsDir(), info.Mode()))
	}

	// Read entries in project root
	files, err := ioutil.ReadDir(projectDir)
	if err != nil {
		debugLog(fmt.Sprintf("READDIR ERROR: %v", err))
		return nil, err
	}

	debugLog(fmt.Sprintf("READDIR OK: Found %d entries", len(files)))

	jsonlCount := 0
	sessionDirs := 0

	for i, file := range files {
		debugLog(fmt.Sprintf("[%d/%d] Entry: name=%s, isDir=%v, size=%d, modTime=%s",
			i+1, len(files), file.Name(), file.IsDir(), file.Size(), file.ModTime().Format("2006-01-02 15:04:05")))

		// Pattern 1: Direct JSONL files
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".jsonl") {
			fullPath := filepath.Join(projectDir, file.Name())
			sessionID := strings.TrimSuffix(file.Name(), ".jsonl")

			debugLog(fmt.Sprintf("  ✓ MATCH: Direct JSONL file"))
			debugLog(fmt.Sprintf("    sessionID: %s", sessionID))
			debugLog(fmt.Sprintf("    fullPath: %s (size=%d)", fullPath, file.Size()))

			// Count messages in the JSONL file
			messageCount := countMessagesInJSONL(fullPath)
			debugLog(fmt.Sprintf("    messageCount: %d (from parsing)", messageCount))

			entries = append(entries, ClaudeSessionIndexEntry{
				SessionID:    sessionID,
				FullPath:     fullPath,
				FileMtime:    file.ModTime().Unix() * 1000,
				MessageCount: messageCount,
			})
			seenSessionIDs[sessionID] = true
			jsonlCount++
		}

		// Pattern 2: Session directories (usually UUIDs) that might contain JSONL files
		if file.IsDir() && !strings.HasPrefix(file.Name(), ".") && file.Name() != "memory" {
			sessionDirPath := filepath.Join(projectDir, file.Name())
			debugLog(fmt.Sprintf("  → Checking session directory: %s", file.Name()))

			// Look for JSONL files in the session directory (directly or in subagents)
			sessionJSONLPath := filepath.Join(sessionDirPath, file.Name()+".jsonl")
			subagentDirPath := filepath.Join(sessionDirPath, "subagents")

			debugLog(fmt.Sprintf("    Looking for direct JSONL: %s", sessionJSONLPath))

			// Try direct JSONL first
			if fileInfo, err := os.Stat(sessionJSONLPath); err == nil && !fileInfo.IsDir() {
				debugLog(fmt.Sprintf("    ✓ FOUND direct JSONL at %s (size=%d)", sessionJSONLPath, fileInfo.Size()))
				entries = append(entries, ClaudeSessionIndexEntry{
					SessionID: file.Name(),
					FullPath:  sessionJSONLPath,
					FileMtime: fileInfo.ModTime().Unix() * 1000,
				})
				seenSessionIDs[file.Name()] = true
				jsonlCount++
			} else {
				debugLog(fmt.Sprintf("    ✗ No direct JSONL found: %v", err))
				debugLog(fmt.Sprintf("    Looking for subagents dir: %s", subagentDirPath))

				// Try subagents directory
				subagentFiles, err := ioutil.ReadDir(subagentDirPath)
				if err == nil {
					debugLog(fmt.Sprintf("    ✓ Found subagents directory with %d entries", len(subagentFiles)))
					// List all subagent files
					for j, sf := range subagentFiles {
						debugLog(fmt.Sprintf("      [%d/%d] subagent: %s (isDir=%v, size=%d)", j+1, len(subagentFiles), sf.Name(), sf.IsDir(), sf.Size()))
					}
					// Create entry for the session with first subagent JSONL
					foundSubagent := false
					for _, subagentFile := range subagentFiles {
						if !subagentFile.IsDir() && strings.HasSuffix(subagentFile.Name(), ".jsonl") {
							subagentPath := filepath.Join(subagentDirPath, subagentFile.Name())
							if !seenSessionIDs[file.Name()] {
								// Count messages in the subagent JSONL file
								messageCount := countMessagesInJSONL(subagentPath)
								debugLog(fmt.Sprintf("    ✓ Using subagent JSONL: %s (size=%d, messageCount=%d)", subagentPath, subagentFile.Size(), messageCount))

								entries = append(entries, ClaudeSessionIndexEntry{
									SessionID:    file.Name(),
									FullPath:     subagentPath,
									FileMtime:    subagentFile.ModTime().Unix() * 1000,
									MessageCount: messageCount,
								})
								seenSessionIDs[file.Name()] = true
								jsonlCount++
								sessionDirs++
								foundSubagent = true
								break
							}
						}
					}
					if !foundSubagent {
						debugLog(fmt.Sprintf("    ✗ No .jsonl files in subagents"))
					}
				} else {
					debugLog(fmt.Sprintf("    ✗ No subagents directory: %v", err))
				}
			}
		}
	}

	debugLog(fmt.Sprintf("=== listProjectSessionsByScanning COMPLETE ==="))
	debugLog(fmt.Sprintf("Results: %d direct JSONL files + %d session directories = %d total entries", jsonlCount-sessionDirs, sessionDirs, len(entries)))
	for i, entry := range entries {
		debugLog(fmt.Sprintf("  Result[%d]: sessionID=%s, fullPath=%s", i, entry.SessionID, entry.FullPath))
	}
	return entries, nil
}

// importJSONLSession imports a single JSONL session file and converts it to Swarm format
func (c *ClaudeCodeConverter) importJSONLSession(filePath string, projectPath string) (*conversation.Conversation, error) {
	debugLog(fmt.Sprintf("importJSONLSession: opening file: %s", filePath))

	file, err := os.Open(filePath)
	if err != nil {
		debugLog(fmt.Sprintf("Error opening file: %v", err))
		return nil, sdkerr.Wrap(err, "converter.open_file_failed", sdkerr.WithAttr("path", filePath))
	}
	defer file.Close()

	// Get file info for debugging
	if info, err := file.Stat(); err == nil {
		debugLog(fmt.Sprintf("File opened successfully, size: %d bytes", info.Size()))
	}

	conv := &conversation.Conversation{
		ID:            filepath.Base(filePath),
		Mode:          "auto",
		Status:        conversation.StatusActive,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		WorkspacePath: projectPath,
		Messages:      make([]*conversation.Message, 0),
		Metadata: conversation.ConversationMetadata{
			Custom: make(map[string]any),
		},
	}

	// Parse JSONL line by line
	scanner := bufio.NewScanner(file)
	// Increase buffer size to handle very large JSONL lines (some can be 10+ MB)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 100*1024*1024) // 100MB max line size

	var totalTokens int
	var lineCount int
	var userMsgs, assistantMsgs int

	for scanner.Scan() {
		lineCount++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var record ClaudeJSONLRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			debugLog(fmt.Sprintf("Line %d: Error parsing JSON: %v, first 100 chars: %s", lineCount, err, truncate(line, 100)))
			continue // Skip malformed lines
		}

		// Only process user and assistant messages
		switch record.Type {
		case "user":
			userMsgs++
			if record.Message != nil {
				msg := c.convertJSONLMessageToSwarm(record, len(conv.Messages))
				if msg != nil {
					conv.Messages = append(conv.Messages, msg)
					if msg.Tokens != nil {
						totalTokens += msg.Tokens.Total
					}
				}
			}
		case "assistant":
			assistantMsgs++
			if record.Message != nil {
				msg := c.convertJSONLMessageToSwarm(record, len(conv.Messages))
				if msg != nil {
					conv.Messages = append(conv.Messages, msg)
					if msg.Tokens != nil {
						totalTokens += msg.Tokens.Total
					}
				}
			}
		default:
			debugLog(fmt.Sprintf("Line %d: Skipping record type: %s", lineCount, record.Type))
		}
	}

	debugLog(fmt.Sprintf("Processed %d lines total: %d user messages, %d assistant messages, %d final messages", lineCount, userMsgs, assistantMsgs, len(conv.Messages)))

	if err := scanner.Err(); err != nil {
		debugLog(fmt.Sprintf("Scanner error: %v", err))
		return nil, sdkerr.Wrap(err, "converter.scan_file_failed", sdkerr.WithAttr("path", filePath))
	}

	conv.TotalTokens = totalTokens
	if len(conv.Messages) > 0 {
		lastMsg := conv.Messages[len(conv.Messages)-1]
		if lastMsg.Role == conversation.RoleAssistant && lastMsg.Tokens != nil {
			conv.CurrentContextSize = lastMsg.Tokens.InputContextSize()
		}
	}

	debugLog(fmt.Sprintf("importJSONLSession complete: %d messages, %d total tokens", len(conv.Messages), totalTokens))
	return conv, nil
}

// truncate truncates a string to maxLen characters
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// countMessagesInJSONL counts user and assistant messages in a JSONL file
func countMessagesInJSONL(filePath string) int {
	file, err := os.Open(filePath)
	if err != nil {
		return 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Increase buffer size to handle very large JSONL lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 100*1024*1024) // 100MB max line size

	messageCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var record ClaudeJSONLRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}

		// Count only user and assistant message types
		if record.Type == "user" || record.Type == "assistant" {
			messageCount++
		}
	}

	return messageCount
}

// convertJSONLMessageToSwarm converts a JSONL message record to Swarm format
func (c *ClaudeCodeConverter) convertJSONLMessageToSwarm(record ClaudeJSONLRecord, index int) *conversation.Message {
	if record.Message == nil {
		debugLog(fmt.Sprintf("convertJSONLMessageToSwarm: record.Message is nil"))
		return nil
	}

	msg := record.Message
	blocks, textContent, _ := ParseContentBlocks(msg.Content)

	debugLog(fmt.Sprintf("convertJSONLMessageToSwarm: role=%s, textLen=%d, blockCount=%d, usage={input=%d, output=%d}", msg.Role, len(textContent), len(blocks), func() int {
		if msg.Usage != nil {
			return msg.Usage.InputTokens
		}
		return 0
	}(), func() int {
		if msg.Usage != nil {
			return msg.Usage.OutputTokens
		}
		return 0
	}()))

	swarmMsg := &conversation.Message{
		ID:        fmt.Sprintf("msg_%s_%d", record.UUID, index),
		Timestamp: time.Now(),
		Role:      c.convertRole(msg.Role),
		Content:   textContent,
	}

	// Set token usage from message.usage
	if msg.Usage != nil {
		swarmMsg.Tokens = &conversation.TokenUsage{
			Input:  msg.Usage.InputTokens,
			Output: msg.Usage.OutputTokens,
			Total:  msg.Usage.InputTokens + msg.Usage.OutputTokens + msg.Usage.CacheReadInputTokens,
		}
	}

	// Extract tool calls and results from content blocks
	for _, block := range blocks {
		switch block.Type {
		case "tool_use":
			toolCall := conversation.ToolCall{
				ID:         block.ID,
				Name:       block.Name,
				Parameters: block.Input,
			}
			swarmMsg.ToolCalls = append(swarmMsg.ToolCalls, toolCall)
			debugLog(fmt.Sprintf("  Added tool_use: %s", block.Name))

		case "tool_result":
			toolResult := conversation.ToolResult{
				CallID: block.ToolUseID,
				Name:   block.Name,
				Output: block.Content,
				Error:  nil,
			}
			if block.IsError {
				toolResult.Error = &conversation.ToolError{
					Type:    "tool_error",
					Message: block.Content,
				}
			}
			swarmMsg.ToolResults = append(swarmMsg.ToolResults, toolResult)
			debugLog(fmt.Sprintf("  Added tool_result: %s", block.Name))

		case "thinking":
			swarmMsg.Thinking = block.Thinking
			debugLog(fmt.Sprintf("  Added thinking: %d chars", len(block.Thinking)))
		}
	}

	return swarmMsg
}

// ExportToClaudeCodeJSONL exports a Swarm conversation to Claude Code JSONL format
// and saves it to the Claude Code projects directory
func (c *ClaudeCodeConverter) ExportToClaudeCodeJSONL(conv *conversation.Conversation, projectPath string) (string, error) {
	if conv == nil {
		return "", sdkerr.Permanent("converter.invalid_input", "Swarm conversation is nil")
	}

	// Generate a filename based on the conversation ID
	filename := fmt.Sprintf("%s.jsonl", conv.ID)

	// Determine the output directory - either in the project or in default Claude Code location
	var outputDir string
	if projectPath != "" {
		// Save to the project directory directly
		outputDir = projectPath
	} else if conv.WorkspacePath != "" {
		// Use workspace path if available
		outputDir = conv.WorkspacePath
	} else {
		// Fall back to Claude Code default location
		projectID := c.pathToProjectID(conv.WorkspacePath)
		outputDir = filepath.Join(c.claudeBaseDir, "projects", projectID)
	}

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", sdkerr.Permanent("converter.mkdir_failed", err.Error())
	}

	outputPath := filepath.Join(outputDir, filename)

	// Create output file
	file, err := os.Create(outputPath)
	if err != nil {
		return "", sdkerr.Permanent("converter.file_create_failed", err.Error())
	}
	defer file.Close()

	debugLog(fmt.Sprintf("Exporting conversation %s to %s", conv.ID, outputPath))

	// Generate JSONL records - one for each message in the conversation
	sessionID := fmt.Sprintf("session-%s", conv.ID)

	for i, msg := range conv.Messages {
		record := &ClaudeJSONLRecord{
			Type:      "message",
			UUID:      fmt.Sprintf("msg-%s-%d", conv.ID, i),
			SessionID: sessionID,
			Timestamp: msg.Timestamp.Format(time.RFC3339),
		}

		// Convert the message to Claude Code format
		claudeMsg := c.convertMessageToCode(msg)
		record.Message = &ClaudeJSONLMessage{
			Role: claudeMsg.Role,
			Type: "message",
		}

		// Handle content - serialize as string for simple export
		if claudeMsg.Content != "" {
			contentBytes, _ := json.Marshal(claudeMsg.Content)
			record.Message.Content = json.RawMessage(contentBytes)
		}

		// Add token usage if available
		if msg.Tokens != nil {
			record.Message.Usage = &ClaudeJSONLUsage{
				InputTokens:  msg.Tokens.Input,
				OutputTokens: msg.Tokens.Output,
			}
		}

		// Marshal and write the record
		data, err := json.Marshal(record)
		if err != nil {
			return "", sdkerr.Permanent("converter.json_marshal_failed", err.Error())
		}

		if _, err := file.Write(data); err != nil {
			return "", sdkerr.Permanent("converter.file_write_failed", err.Error())
		}

		if _, err := file.WriteString("\n"); err != nil {
			return "", sdkerr.Permanent("converter.file_write_failed", err.Error())
		}

		debugLog(fmt.Sprintf("  Exported message %d: %s", i, record.UUID))
	}

	debugLog(fmt.Sprintf("Successfully exported %d messages to %s", len(conv.Messages), outputPath))
	return outputPath, nil
}
