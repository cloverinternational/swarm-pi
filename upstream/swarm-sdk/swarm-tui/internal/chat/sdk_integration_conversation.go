package chat

import (
	"context"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	tuianalytics "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/analytics"
)

// resumeConv, saveConv, createConv, addMsg are private dispatch helpers that
// delegate conversation persistence to the (mandatory) SDK client.

func (sdk *SDKIntegration) resumeConv(ctx context.Context, convID string) (*conversation.Conversation, error) {
	return sdk.sdkClient.ResumeConversation(ctx, convID)
}

func (sdk *SDKIntegration) saveConv(ctx context.Context, conv *conversation.Conversation) error {
	return sdk.sdkClient.SaveConversation(ctx, conv)
}

func (sdk *SDKIntegration) persistInLoopCompaction(
	ctx context.Context,
	convID string,
	compacted []*conversation.Message,
	summary string,
	contextSize int,
) error {
	if sdk == nil || sdk.sdkClient == nil {
		return fmt.Errorf("SDK integration is unavailable")
	}
	if convID == "" || len(compacted) == 0 {
		return fmt.Errorf("in-loop compaction requires a conversation and replacement messages")
	}
	conv := sdk.GetConversation(ctx, convID)
	if conv == nil {
		return fmt.Errorf("failed to load conversation %s", convID)
	}
	working := *conv
	working.Messages = append([]*conversation.Message(nil), conv.Messages...)
	if conv.CompactionState != nil {
		state := *conv.CompactionState
		working.CompactionState = &state
	}
	if conv.Summary != nil {
		summaryCopy := *conv.Summary
		summaryCopy.ModelsUsed = append([]string(nil), conv.Summary.ModelsUsed...)
		working.Summary = &summaryCopy
	}
	working.Metadata.Tags = append([]string(nil), conv.Metadata.Tags...)
	if conv.Metadata.Custom != nil {
		working.Metadata.Custom = make(map[string]any, len(conv.Metadata.Custom))
		for key, value := range conv.Metadata.Custom {
			working.Metadata.Custom[key] = value
		}
	}
	durableCompacted, err := cloneGeneratedCompactionMessages(compacted)
	if err != nil {
		return err
	}
	working.AdvanceActiveContext(durableCompacted, summary, contextSize)
	if err := sdk.saveConv(ctx, &working); err != nil {
		return fmt.Errorf("save compacted conversation: %w", err)
	}
	// Keep metadata refresh inside the persistence callback. Returning to the
	// agent loop first would let its next message write race a stale metadata
	// snapshot and potentially lose the newly appended turn.
	if err := sdk.RefreshConversationMetadata(ctx, convID); err != nil {
		sdk.logger.Warn(ctx, "conversation.compaction_metadata_refresh_failed",
			observability.F("conversation_id", convID),
			observability.F("error", err.Error()))
	}
	return nil
}

func (sdk *SDKIntegration) createConv(ctx context.Context, opts manager.CreateOptions) (*conversation.Conversation, error) {
	return sdk.sdkClient.CreateConversationWithOptions(ctx, opts)
}

func (sdk *SDKIntegration) addMsg(ctx context.Context, convID string, msg *conversation.Message) error {
	return sdk.sdkClient.AddMessageToConversation(ctx, convID, msg)
}

func (sdk *SDKIntegration) CreateConversation(ctx context.Context, mode string) (*conversation.Conversation, error) {
	return sdk.CreateConversationWithBranch(ctx, mode, "")
}

// CreateConversationWithBranch creates a new conversation with an optional git branch association
func (sdk *SDKIntegration) CreateConversationWithBranch(ctx context.Context, mode string, branch string) (*conversation.Conversation, error) {
	// Store workspace path and branch in metadata for workspace-scoped history
	custom := map[string]interface{}{
		"workspace_path": sdk.workspaceRoot,
	}
	if branch != "" {
		custom["git_branch"] = branch
	}

	metadata := &conversation.ConversationMetadata{
		Tags:   []string{"tui"},
		Custom: custom,
	}

	createOpts := manager.CreateOptions{
		Mode:          mode,
		WorkspacePath: sdk.ProjectRoot(),
		Metadata:      metadata,
		// Interactive TUI conversations previously carried NO origin key —
		// origin was stamped on headless runs only, so any stratified report had
		// to infer the TUI side from tags (PLAN.md gap G6). Declaring it here
		// makes the tagging symmetric; the value comes from the same vocabulary
		// the HistorySearch "origin" filter already accepts.
		Origin: conversation.OriginInteractive,
	}

	conv, err := sdk.createConv(ctx, createOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	sdk.logger.Info(ctx, "conversation.created",
		observability.F("conversation_id", conv.ID),
		observability.F("mode", mode),
		observability.F("branch", branch),
	)
	if manager := tuianalytics.DefaultManager(); manager != nil {
		manager.CaptureConversationCreated(conv.ID, mode, branch, sdk.workspaceRoot)
	}

	return conv, nil
}

// CreateHeadlessConversation creates a conversation for a non-interactive
// headless run (`swarm -p "<prompt>"`). It is identical to
// CreateConversationWithBranch except the conversation is tagged with
// conversation.HeadlessTag (and NOT "tui") and marked origin=headless, so the
// listing choke point (client.ListConversationsMeta) excludes it from the
// browsable TUI conversation picker AND the agent-facing HistorySearch tool.
// The conversation is still persisted and resumable by ID (`swarm -p --id`).
func (sdk *SDKIntegration) CreateHeadlessConversation(ctx context.Context, mode string) (*conversation.Conversation, error) {
	metadata := &conversation.ConversationMetadata{
		Tags: []string{conversation.HeadlessTag},
		Custom: map[string]interface{}{
			"workspace_path": sdk.workspaceRoot,
			"origin":         conversation.HeadlessTag,
		},
	}

	createOpts := manager.CreateOptions{
		Mode:          mode,
		WorkspacePath: sdk.ProjectRoot(),
		Metadata:      metadata,
		// Redundant with the origin key seeded in Custom just above, and
		// deliberately so: the declarative field is what the manager reads, and
		// StampJoinKeys will not overwrite the pre-seeded value. Both spell
		// "headless", so the persisted result for this path is unchanged.
		Origin: conversation.OriginHeadless,
	}

	conv, err := sdk.createConv(ctx, createOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create headless conversation: %w", err)
	}

	sdk.logger.Info(ctx, "conversation.created",
		observability.F("conversation_id", conv.ID),
		observability.F("mode", mode),
		observability.F("origin", conversation.HeadlessTag),
	)
	if manager := tuianalytics.DefaultManager(); manager != nil {
		manager.CaptureConversationCreated(conv.ID, mode, "", sdk.workspaceRoot)
	}

	return conv, nil
}

// ForkConversation creates a new conversation by cloning messages from a source conversation
// up to the specified messageCount. The original conversation is preserved.
func (sdk *SDKIntegration) ForkConversation(ctx context.Context, sourceConvID string, messageCount int) (*conversation.Conversation, error) {
	// Load source conversation
	sourceConv, err := sdk.resumeConv(ctx, sourceConvID)
	if err != nil {
		return nil, fmt.Errorf("failed to load source conversation for fork: %w", err)
	}

	// Build metadata for the fork
	custom := map[string]interface{}{
		"workspace_path": sdk.workspaceRoot,
		"forked_from":    sourceConvID,
		"fork_point":     messageCount,
	}
	// Preserve git branch from source
	if sourceConv.Metadata.Custom != nil {
		if branch, ok := sourceConv.Metadata.Custom["git_branch"].(string); ok && branch != "" {
			custom["git_branch"] = branch
		}
	}

	metadata := &conversation.ConversationMetadata{
		Tags:   []string{"tui", "fork"},
		Custom: custom,
	}

	// Create new conversation
	forkOpts := manager.CreateOptions{
		Mode:          sourceConv.Mode,
		WorkspacePath: sdk.ProjectRoot(),
		Metadata:      metadata,
		// A fork is created by a human in the TUI, so it is interactive. Note
		// that the fork's parent is recorded as forked_from (which
		// usageindex/parse.go already resolves into a ParentID) and NOT as
		// parent_conversation_id: that key means "the run that spawned this
		// child run", and conflating lineage with spawn attribution would make
		// both unreadable.
		Origin: conversation.OriginInteractive,
	}
	newConv, err := sdk.createConv(ctx, forkOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create forked conversation: %w", err)
	}

	// Clone messages up to messageCount
	srcMessages := sourceConv.GetMessages()
	count := messageCount
	if count > len(srcMessages) {
		count = len(srcMessages)
	}

	for i := 0; i < count; i++ {
		cloned := srcMessages[i].Clone()
		newConv.AddMessage(cloned)
	}

	// Save the new conversation with cloned messages
	if err := sdk.saveConv(ctx, newConv); err != nil {
		return nil, fmt.Errorf("failed to save forked conversation: %w", err)
	}

	sdk.logger.Info(ctx, "conversation.forked",
		observability.F("source_id", sourceConvID),
		observability.F("new_id", newConv.ID),
		observability.F("messages_copied", count),
		observability.F("source_total", len(srcMessages)),
	)
	if manager := tuianalytics.DefaultManager(); manager != nil {
		branch, _ := custom["git_branch"].(string)
		manager.CaptureConversationCreated(newConv.ID, sourceConv.Mode, branch, sdk.workspaceRoot)
	}

	return newConv, nil
}

// ResumeConversation loads an existing conversation
func (sdk *SDKIntegration) ResumeConversation(ctx context.Context, convID string) (*conversation.Conversation, error) {
	conv, err := sdk.resumeConv(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("failed to resume conversation: %w", err)
	}

	sdk.logger.Info(ctx, "conversation.resumed",
		observability.F("conversation_id", convID),
		observability.F("message_count", len(conv.Messages)),
	)
	if manager := tuianalytics.DefaultManager(); manager != nil {
		manager.CaptureConversationResumed(convID, len(conv.Messages))
	}

	return conv, nil
}

func (sdk *SDKIntegration) conversationExists(ctx context.Context, convID string) bool {
	if sdk == nil || convID == "" {
		return false
	}
	_, err := sdk.resumeConv(ctx, convID)
	return err == nil
}

// UpdateConversationMetadata updates the metadata for an existing conversation
func (sdk *SDKIntegration) UpdateConversationMetadata(ctx context.Context, convID string, metadata *conversation.ConversationMetadata) error {
	conv, err := sdk.resumeConv(ctx, convID)
	if err != nil {
		return fmt.Errorf("failed to load conversation for metadata update: %w", err)
	}

	conv.Metadata = *metadata

	if err := sdk.saveConv(ctx, conv); err != nil {
		return fmt.Errorf("failed to save conversation metadata: %w", err)
	}

	sdk.logger.Debug(ctx, "conversation.metadata_updated",
		observability.F("conversation_id", convID),
	)

	return nil
}

// SetConversationTitle sets a user-selected title for a conversation and saves it.
func (sdk *SDKIntegration) SetConversationTitle(ctx context.Context, convID string, title string) error {
	// Prefer the unified SDK client path (added 2026-05-28)
	if sdk.sdkClient != nil {
		return sdk.sdkClient.SetConversationTitle(ctx, convID, title)
	}

	// Legacy fallback: use the legacy manager/storage directly
	conv, err := sdk.resumeConv(ctx, convID)
	if err != nil {
		return fmt.Errorf("failed to load conversation for title update: %w", err)
	}

	conv.Title = title

	if err := sdk.saveConv(ctx, conv); err != nil {
		return fmt.Errorf("failed to save conversation title: %w", err)
	}

	sdk.logger.Debug(ctx, "conversation.title_updated",
		observability.F("conversation_id", convID),
		observability.F("title", title),
	)

	return nil
}

// RefreshConversationMetadata guarantees a useful title/recap and asks the
// configured provider to enhance it. Generation lives on the shared SDK client
// so TUI, daemon, managed, and direct SDK conversations follow one lifecycle.
func (sdk *SDKIntegration) RefreshConversationMetadata(ctx context.Context, convID string) error {
	if sdk.sdkClient == nil {
		return fmt.Errorf("conversation metadata refresh requires SDK client")
	}
	return sdk.sdkClient.RefreshConversationMetadata(ctx, convID)
}

// SetConversationSummary persists a generated recap for compatibility callers.
// Automatic generation is owned by RefreshConversationMetadata.
func (sdk *SDKIntegration) SetConversationSummary(ctx context.Context, convID string, recap string) error {
	// Prefer the unified SDK client path.
	if sdk.sdkClient != nil {
		return sdk.sdkClient.SetConversationSummary(ctx, convID, recap)
	}

	// Legacy fallback: use the legacy manager/storage directly.
	conv, err := sdk.resumeConv(ctx, convID)
	if err != nil {
		return fmt.Errorf("failed to load conversation for summary update: %w", err)
	}

	conv.EnsureSummary().Recap = recap

	if err := sdk.saveConv(ctx, conv); err != nil {
		return fmt.Errorf("failed to save conversation summary: %w", err)
	}

	sdk.logger.Debug(ctx, "conversation.summary_updated",
		observability.F("conversation_id", convID),
	)

	return nil
}

// AddMessage adds a message to a conversation
func (sdk *SDKIntegration) AddMessage(ctx context.Context, convID string, msg *conversation.Message) error {
	if err := sdk.addMsg(ctx, convID, msg); err != nil {
		return fmt.Errorf("failed to add message: %w", err)
	}

	sdk.logger.Debug(ctx, "message.added",
		observability.F("conversation_id", convID),
		observability.F("message_id", msg.ID),
		observability.F("role", string(msg.Role)),
	)
	if manager := tuianalytics.DefaultManager(); manager != nil {
		manager.CaptureMessage(convID, msg)
	}

	return nil
}

// AddToolResults adds tool results to the conversation
func (sdk *SDKIntegration) AddToolResults(ctx context.Context, convID string, results []conversation.ToolResult) error {
	// Create a user message with tool results
	msg := &conversation.Message{
		ID:          fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Timestamp:   time.Now(),
		Role:        conversation.RoleUser,
		Content:     "", // Tool results don't need text content
		ToolResults: results,
	}

	err := sdk.AddMessage(ctx, convID, msg)
	if err != nil {
		return fmt.Errorf("failed to add tool results: %w", err)
	}

	sdk.logger.Info(ctx, "tool_results.added",
		observability.F("conversation_id", convID),
		observability.F("result_count", len(results)),
	)

	return nil
}

// UpdateLastMessageCompletion updates the last assistant message's metadata with completion info.
// This stores elapsed_time_ns and model so they can be displayed when loading conversations.
// NOTE: This metadata is only used for TUI display - it is NOT sent to the LLM API.
func (sdk *SDKIntegration) UpdateLastMessageCompletion(ctx context.Context, convID string, elapsed time.Duration, model string) error {
	if sdk == nil || convID == "" {
		return nil
	}

	conv, err := sdk.resumeConv(ctx, convID)
	if err != nil {
		return fmt.Errorf("failed to resume conversation: %w", err)
	}

	// Find the last assistant message
	var lastAssistant *conversation.Message
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		if conv.Messages[i].Role == conversation.RoleAssistant {
			lastAssistant = conv.Messages[i]
			break
		}
	}

	if lastAssistant == nil {
		return nil // No assistant message to update
	}

	// Initialize metadata if nil
	if lastAssistant.Metadata == nil {
		lastAssistant.Metadata = make(map[string]interface{})
	}

	// Store elapsed time in nanoseconds (for precision when deserializing)
	lastAssistant.Metadata["elapsed_time_ns"] = float64(elapsed.Nanoseconds())

	// Store model if not already set on the message
	if lastAssistant.Model == "" && model != "" {
		lastAssistant.Model = model
	}

	if err := sdk.saveConv(ctx, conv); err != nil {
		return fmt.Errorf("failed to save conversation: %w", err)
	}

	sdk.logger.Debug(ctx, "message.completion_updated",
		observability.F("conversation_id", convID),
		observability.F("message_id", lastAssistant.ID),
		observability.F("elapsed_ms", elapsed.Milliseconds()),
		observability.F("model", model),
	)

	return nil
}

// GetMessages retrieves conversation messages
func (sdk *SDKIntegration) GetMessages(ctx context.Context, convID string) ([]*conversation.Message, error) {
	return sdk.GetMessagesWithOptions(ctx, convID, manager.GetMessagesOptions{})
}

// GetMessagesWithOptions retrieves conversation messages with pagination/filtering options
func (sdk *SDKIntegration) GetMessagesWithOptions(ctx context.Context, convID string, opts manager.GetMessagesOptions) ([]*conversation.Message, error) {
	messages, err := sdk.sdkClient.GetConversationMessages(ctx, convID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}

	return messages, nil
}

// repairOrphanedToolCalls scans conversation history for tool_use blocks without
// corresponding tool_result blocks and adds synthetic error results.
// This prevents HTTP 400 errors from Anthropic API which requires every tool_use
// to have a corresponding tool_result in the immediately following message.
func (sdk *SDKIntegration) repairOrphanedToolCalls(ctx context.Context, messages []*conversation.Message) []*conversation.Message {
	if len(messages) == 0 {
		return messages
	}

	// Build a set of all tool_result call IDs for quick lookup
	toolResultIDs := make(map[string]bool)
	for _, msg := range messages {
		for _, tr := range msg.ToolResults {
			toolResultIDs[tr.CallID] = true
		}
	}

	// Find orphaned tool calls (tool_use without matching tool_result)
	var orphanedCalls []struct {
		msgIndex int
		toolCall conversation.ToolCall
	}

	for i, msg := range messages {
		if msg.Role == conversation.RoleAssistant && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if !toolResultIDs[tc.ID] {
					orphanedCalls = append(orphanedCalls, struct {
						msgIndex int
						toolCall conversation.ToolCall
					}{msgIndex: i, toolCall: tc})
				}
			}
		}
	}

	if len(orphanedCalls) == 0 {
		return messages
	}

	sdk.logger.Warn(ctx, "repairing_orphaned_tool_calls",
		observability.F("orphaned_count", len(orphanedCalls)))

	// Group orphaned calls by the message index they follow
	// We need to insert tool results immediately after their corresponding assistant message
	orphansByMsgIndex := make(map[int][]conversation.ToolCall)
	for _, orphan := range orphanedCalls {
		orphansByMsgIndex[orphan.msgIndex] = append(orphansByMsgIndex[orphan.msgIndex], orphan.toolCall)
	}

	// Rebuild messages with synthetic tool results inserted
	var repairedMessages []*conversation.Message
	for i, msg := range messages {
		repairedMessages = append(repairedMessages, msg)

		// Check if this assistant message has orphaned tool calls
		if orphans, ok := orphansByMsgIndex[i]; ok {
			// Check if the next message already has some tool results
			// If so, we need to add the missing ones to that message
			if i+1 < len(messages) && len(messages[i+1].ToolResults) > 0 {
				// Add missing tool results to the existing tool result message
				for _, tc := range orphans {
					errorResult := conversation.ToolResult{
						CallID: tc.ID,
						Name:   tc.Name, // Required for Gemini API function_response.name field
						Output: fmt.Sprintf("Error: Tool call '%s' failed or was interrupted. The connection may have dropped or the tool execution was not completed.", tc.Name),
						Error: &conversation.ToolError{
							Type:    "tool.interrupted",
							Message: "Tool call was interrupted or failed to complete",
						},
					}
					messages[i+1].ToolResults = append(messages[i+1].ToolResults, errorResult)
					sdk.logger.Warn(ctx, "added_synthetic_tool_result_to_existing",
						observability.F("tool_id", tc.ID),
						observability.F("tool_name", tc.Name))
				}
			} else {
				// Create a new tool result message for all orphaned calls from this assistant message
				var syntheticResults []conversation.ToolResult
				for _, tc := range orphans {
					syntheticResults = append(syntheticResults, conversation.ToolResult{
						CallID: tc.ID,
						Name:   tc.Name, // Required for Gemini API function_response.name field
						Output: fmt.Sprintf("Error: Tool call '%s' failed or was interrupted. The connection may have dropped or the tool execution was not completed.", tc.Name),
						Error: &conversation.ToolError{
							Type:    "tool.interrupted",
							Message: "Tool call was interrupted or failed to complete",
						},
					})
					sdk.logger.Warn(ctx, "created_synthetic_tool_result",
						observability.F("tool_id", tc.ID),
						observability.F("tool_name", tc.Name))
				}

				syntheticMsg := &conversation.Message{
					ID:          fmt.Sprintf("repair_%d", time.Now().UnixNano()),
					Timestamp:   time.Now(),
					Role:        conversation.RoleTool,
					Content:     "", // Tool results don't need text content
					ToolResults: syntheticResults,
				}
				repairedMessages = append(repairedMessages, syntheticMsg)
			}
		}
	}

	sdk.logger.Info(ctx, "tool_call_repair_complete",
		observability.F("original_count", len(messages)),
		observability.F("repaired_count", len(repairedMessages)),
		observability.F("orphans_fixed", len(orphanedCalls)))

	return repairedMessages
}

func (sdk *SDKIntegration) SaveAssistantMessage(ctx context.Context, convID string, content string, usage *conversation.TokenUsage) error {
	assistantMsg := &conversation.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Timestamp: time.Now(),
		Role:      conversation.RoleAssistant,
		Content:   content,
		Tokens:    usage,
	}

	err := sdk.AddMessage(ctx, convID, assistantMsg)
	if err != nil {
		return fmt.Errorf("failed to save assistant message: %w", err)
	}

	sdk.logger.Info(ctx, "assistant.message.saved",
		observability.F("conversation_id", convID),
		observability.F("content_length", len(content)),
	)

	return nil
}

// CompleteConversation marks a conversation as completed
func (sdk *SDKIntegration) CompleteConversation(ctx context.Context, convID string) error {
	if err := sdk.sdkClient.CompleteConversation(ctx, convID); err != nil {
		return fmt.Errorf("failed to complete conversation: %w", err)
	}

	sdk.logger.Info(ctx, "conversation.completed",
		observability.F("conversation_id", convID),
	)
	if manager := tuianalytics.DefaultManager(); manager != nil {
		manager.CaptureConversationCompleted(convID)
	}

	return nil
}

// ListConversations retrieves lightweight conversation metadata for a specific
// workspace, sorted newest-first. This path is metadata-only: message bodies are
// NOT loaded, so it scales to hundreds of thousands of conversations. Callers
// that need message content must call ResumeConversation / GetMessages.
//
// Deprecated for list-everything use cases: prefer ListConversationsPage which
// applies proper LIMIT/OFFSET pagination at the storage layer.
func (sdk *SDKIntegration) ListConversations(ctx context.Context, workspacePath string) ([]*conversation.Conversation, error) {
	if sdk.sdkClient != nil {
		return sdk.sdkClient.ListConversationsMeta(ctx, workspacePath)
	}
	return sdk.ListConversationsPage(ctx, workspacePath, 0, 0)
}

// ListConversationsPage returns a metadata-only page of conversations sorted by
// UpdatedAt descending. limit == 0 means "no limit" (used by callers that want
// everything — but prefer pagination for large stores).
//
// Under the hood this uses storage.Query with ExcludeMessages=true, which runs
// the zero-allocation jsonparser extraction in pooled_loader.go. A metadata-only
// load of 10k conversations takes milliseconds; a full load of the same set
// could easily exceed 1 GB of allocated messages.
func (sdk *SDKIntegration) ListConversationsPage(ctx context.Context, workspacePath string, offset, limit int) ([]*conversation.Conversation, error) {
	filter := storage.Filter{
		WorkspacePath:   workspacePath,
		ExcludeMessages: true,
		SortBy:          "updated_at",
		SortOrder:       storage.SortDescending,
		Offset:          offset,
		Limit:           limit,
	}
	convs, err := sdk.sdkClient.Storage().Query(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}

	sdk.logger.Info(ctx, "conversations.listed",
		observability.F("count", len(convs)),
		observability.F("workspace", workspacePath),
		observability.F("offset", offset),
		observability.F("limit", limit),
	)
	return convs, nil
}

// CountConversations returns the total number of conversations in the given
// workspace without loading any message bodies. Best-effort — falls back to
// counting metadata-only Query results if the backend has no cheaper counter.
func (sdk *SDKIntegration) CountConversations(ctx context.Context, workspacePath string) (int, error) {
	filter := storage.Filter{
		WorkspacePath:   workspacePath,
		ExcludeMessages: true,
	}
	convs, err := sdk.sdkClient.Storage().Query(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("failed to count conversations: %w", err)
	}
	return len(convs), nil
}

func (sdk *SDKIntegration) GetConversation(ctx context.Context, convID string) *conversation.Conversation {
	if sdk == nil || convID == "" {
		return nil
	}
	conv, err := sdk.resumeConv(ctx, convID)
	if err != nil {
		return nil
	}
	return conv
}

func (sdk *SDKIntegration) CreateCompactedConversation(ctx context.Context, oldConvID string, messages []*conversation.Message) (string, error) {
	if sdk == nil || sdk.sdkClient == nil {
		return "", fmt.Errorf("SDK not initialized")
	}

	// Load the old conversation to preserve its metadata
	var oldConv *conversation.Conversation
	var oldMode string = "chat"
	var metadata *conversation.ConversationMetadata

	if oldConvID != "" {
		var err error
		oldConv, err = sdk.resumeConv(ctx, oldConvID)
		if err == nil {
			// Preserve the mode from the old conversation
			oldMode = oldConv.Mode
			if oldMode == "" {
				oldMode = "chat"
			}

			// Copy metadata from old conversation (critical for project association)
			metadata = &conversation.ConversationMetadata{
				UserID:    oldConv.Metadata.UserID,
				ProjectID: oldConv.Metadata.ProjectID,
				Tags:      oldConv.Metadata.Tags,
			}

			// Copy custom metadata (including workspace_path)
			if oldConv.Metadata.Custom != nil {
				metadata.Custom = make(map[string]interface{})
				for k, v := range oldConv.Metadata.Custom {
					metadata.Custom[k] = v
				}
			}

			// Add compaction metadata
			if metadata.Custom == nil {
				metadata.Custom = make(map[string]interface{})
			}
			// Drop the previous conversation's process-scoped join keys so the
			// compacted conversation is stamped with the CURRENT session and
			// HEAD rather than inheriting the old run's. Lineage (origin,
			// agent_id, compacted_from) is preserved. See
			// conversation.ClearProcessScopedJoinKeys.
			conversation.ClearProcessScopedJoinKeys(metadata.Custom)
			metadata.Custom["compacted_from"] = oldConvID
			metadata.Custom["compacted_at"] = time.Now().Format(time.RFC3339)
		} else {
			sdk.logger.Warn(ctx, "failed_to_load_old_conversation_for_metadata",
				observability.F("old_conversation_id", oldConvID),
				observability.F("error", err.Error()))
		}
	}

	// If we couldn't get metadata from old conversation, create default metadata
	// with workspace path from SDK to ensure project association
	if metadata == nil {
		metadata = &conversation.ConversationMetadata{
			Tags: []string{"tui", "compacted"},
			Custom: map[string]interface{}{
				"workspace_path": sdk.workspaceRoot,
				"compacted_from": oldConvID,
				"compacted_at":   time.Now().Format(time.RFC3339),
			},
		}
	}

	// Create new conversation with preserved metadata
	compactedOpts := manager.CreateOptions{
		Mode:          oldMode,
		WorkspacePath: sdk.ProjectRoot(),
		Metadata:      metadata,
	}
	conv, err := sdk.createConv(ctx, compactedOpts)
	if err != nil {
		return "", fmt.Errorf("failed to create compacted conversation: %w", err)
	}

	// Track total tokens for the new conversation
	totalTokens := 0

	// Add all compacted messages to the new conversation
	for _, msg := range messages {
		if addErr := sdk.addMsg(ctx, conv.ID, msg); addErr != nil {
			sdk.logger.Warn(ctx, "failed_to_add_compacted_message",
				observability.F("conversation_id", conv.ID),
				observability.F("error", addErr.Error()))
			// Continue adding other messages even if one fails
		} else if manager := tuianalytics.DefaultManager(); manager != nil {
			manager.CaptureMessage(conv.ID, msg)
		}

		// Accumulate tokens from message
		if msg.Tokens != nil {
			totalTokens += msg.Tokens.Total
		} else {
			// Estimate if not provided (4 chars ≈ 1 token)
			totalTokens += int(float64(len(msg.Content)) * compaction.TokensPerChar)
		}
	}

	// Update conversation token counts by reloading and saving
	if updatedConv, err := sdk.resumeConv(ctx, conv.ID); err == nil {
		updatedConv.TotalTokens = totalTokens
		updatedConv.CurrentContextSize = totalTokens
		if saveErr := sdk.saveConv(ctx, updatedConv); saveErr != nil {
			sdk.logger.Warn(ctx, "failed_to_save_token_count",
				observability.F("conversation_id", conv.ID),
				observability.F("error", saveErr.Error()))
		}
	}

	// Archive the old conversation (don't delete in case user wants to recover)
	if oldConvID != "" {
		archiveErr := sdk.sdkClient.ArchiveConversation(ctx, oldConvID)
		if archiveErr != nil {
			sdk.logger.Warn(ctx, "failed_to_archive_old_conversation",
				observability.F("old_conversation_id", oldConvID),
				observability.F("error", archiveErr.Error()))
		}
	}

	sdk.logger.Info(ctx, "compacted_conversation_created",
		observability.F("old_conversation_id", oldConvID),
		observability.F("new_conversation_id", conv.ID),
		observability.F("message_count", len(messages)),
		observability.F("total_tokens", totalTokens),
		observability.F("workspace_path", sdk.workspaceRoot))
	if manager := tuianalytics.DefaultManager(); manager != nil {
		manager.CaptureConversationCreated(conv.ID, oldMode, "", sdk.workspaceRoot)
	}

	return conv.ID, nil
}

// GetConversationPath returns the absolute path of the JSON file for a given
// conversation ID. Returns an empty string when the path cannot be resolved
// (e.g. the conversation does not exist or the underlying storage does not
// support path resolution). Used by the compaction system to embed the file
// path in pointer messages and summary handoffs.
func (sdk *SDKIntegration) GetConversationPath(id string) string {
	if id == "" {
		return ""
	}
	type pathResolver interface {
		ResolveConversationPath(id string) string
	}
	if pr, ok := sdk.sdkClient.Storage().(pathResolver); ok {
		return pr.ResolveConversationPath(id)
	}
	return ""
}

// SetConversationJSONPath forwards the absolute conversation JSON path to the
// underlying agent's micro-compactor. This ensures per-turn pointer messages
// reference the correct file after a conversation switch or compaction.
func (sdk *SDKIntegration) SetConversationJSONPath(path string) {
	if sdk.activeAgent() != nil {
		sdk.activeAgent().SetConversationJSONPath(path)
	}
}
