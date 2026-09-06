package backfill

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	sdkanalytics "github.com/Swarm-Code/mono/swarm-sdk/internal/analytics"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
)

type workspaceIndex struct {
	byProjectHash map[string]string
}

func (i *workspaceIndex) addWorkspace(workspace string) {
	if workspace == "" {
		return
	}
	if i.byProjectHash == nil {
		i.byProjectHash = make(map[string]string)
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	i.byProjectHash[projectHashForWorkspace(workspace)] = workspace
}

func (i workspaceIndex) workspaces() []string {
	seen := make(map[string]struct{}, len(i.byProjectHash))
	workspaces := make([]string, 0, len(i.byProjectHash))
	for _, workspace := range i.byProjectHash {
		if workspace == "" {
			continue
		}
		if _, ok := seen[workspace]; ok {
			continue
		}
		seen[workspace] = struct{}{}
		workspaces = append(workspaces, workspace)
	}
	slices.Sort(workspaces)
	return workspaces
}

func buildWorkspaceIndex(ctx context.Context, opts Options) (workspaceIndex, error) {
	index := workspaceIndex{byProjectHash: make(map[string]string)}
	if opts.Workspace != "" {
		index.addWorkspace(opts.Workspace)
	}
	store, err := storage.NewDirectoryFileStorage(storage.DirectoryFileStorageConfig{BaseDir: opts.ConversationsDir})
	if err != nil {
		return index, err
	}
	defer store.Close()
	convs, err := store.Query(ctx, storage.Filter{ExcludeMessages: true})
	if err != nil {
		return index, err
	}
	for _, conv := range convs {
		workspace := conversationWorkspace(conv)
		if workspace == "" {
			continue
		}
		index.addWorkspace(workspace)
	}
	return index, nil
}

func backfillConversations(ctx context.Context, opts Options, s *sender, index workspaceIndex) error {
	store, err := storage.NewDirectoryFileStorage(storage.DirectoryFileStorageConfig{BaseDir: opts.ConversationsDir})
	if err != nil {
		return err
	}
	defer store.Close()

	filter := storage.Filter{
		WorkspacePath: opts.Workspace,
		SortBy:        "created_at",
		SortOrder:     storage.SortAscending,
	}
	if !opts.Since.IsZero() {
		value := opts.Since.Unix()
		filter.CreatedAfter = &value
	}
	if !opts.Until.IsZero() {
		value := opts.Until.Unix()
		filter.CreatedBefore = &value
	}
	if opts.Scope == ScopeWorkspace && filter.WorkspacePath == "" {
		cwd, err := filepath.Abs(".")
		if err == nil {
			filter.WorkspacePath = cwd
		}
	}
	convs, err := store.Query(ctx, filter)
	if err != nil {
		return err
	}
	for _, conv := range convs {
		if s.result.limitReached(opts.Limit) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		s.result.ConversationsScanned++
		if err := backfillConversation(ctx, opts, s, index, conv); err != nil {
			s.result.Errors = append(s.result.Errors, err.Error())
		}
	}
	return nil
}

func backfillConversation(ctx context.Context, opts Options, s *sender, index workspaceIndex, conv *conversation.Conversation) error {
	if conv == nil || conv.ID == "" {
		return nil
	}
	workspacePath := conversationWorkspace(conv)
	projectHash := projectHashForWorkspace(workspacePath)
	if workspacePath != "" && projectHash != "" {
		index.addWorkspace(workspacePath)
	}
	sessionID := "backfill:" + conv.ID
	createdAt := conv.CreatedAt
	if createdAt.IsZero() && len(conv.Messages) > 0 {
		createdAt = conv.Messages[0].Timestamp
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	if err := s.enqueueEvent(sdkanalytics.EventEnvelope{
		EventID:        stableID("event", "conversation.created", conv.ID),
		OccurredAt:     createdAt.UTC(),
		SessionID:      sessionID,
		ConversationID: conv.ID,
		EventType:      "conversation.created",
		WorkspaceHash:  sdkanalytics.WorkspaceHash(s.cfg.WorkspaceNamespace, workspacePath),
		WorkspaceLabel: sdkanalytics.WorkspaceLabel(workspacePath),
		ProjectHash:    projectHash,
		Payload: map[string]any{
			"mode":           conv.Mode,
			"git_branch":     conversationBranch(conv),
			"workspace_path": workspacePath,
			"title":          conv.Title,
			"status":         string(conv.Status),
			"backfill":       true,
		},
	}, "conversation:"+conv.ID); err != nil {
		return err
	}
	if s.result.limitReached(opts.Limit) {
		return nil
	}
	firstProvider, firstModel := firstProviderModel(conv)
	if err := s.enqueueEvent(sdkanalytics.EventEnvelope{
		EventID:        stableID("event", "session.started", conv.ID),
		OccurredAt:     createdAt.UTC(),
		SessionID:      sessionID,
		ConversationID: conv.ID,
		EventType:      "session.started",
		WorkspaceHash:  sdkanalytics.WorkspaceHash(s.cfg.WorkspaceNamespace, workspacePath),
		WorkspaceLabel: sdkanalytics.WorkspaceLabel(workspacePath),
		ProjectHash:    projectHash,
		Payload: map[string]any{
			"workspace_path": workspacePath,
			"provider":       firstProvider,
			"model":          firstModel,
			"metadata":       map[string]any{"backfill": true},
		},
	}, "conversation:"+conv.ID); err != nil {
		return err
	}

	toolNames := make(map[string]string)
	toolInputs := make(map[string]map[string]any)
	for msgIndex, msg := range conv.Messages {
		if s.result.limitReached(opts.Limit) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if msg == nil {
			continue
		}
		messageID := backfillMessageID(conv.ID, msgIndex, msg)
		if err := enqueueMessageEvent(s, conv.ID, sessionID, workspacePath, projectHash, messageID, msg); err != nil {
			return err
		}
		for i, call := range msg.ToolCalls {
			callID := call.ID
			if callID == "" {
				callID = fmt.Sprintf("%s:call:%d", messageID, i)
			}
			toolNames[callID] = call.Name
			toolInputs[callID] = call.Parameters
			if s.result.limitReached(opts.Limit) {
				return nil
			}
			if err := s.enqueueEvent(sdkanalytics.EventEnvelope{
				EventID:        stableID("event", "tool.started", conv.ID, callID),
				OccurredAt:     msg.Timestamp.UTC(),
				SessionID:      sessionID,
				ConversationID: conv.ID,
				EventType:      "tool.started",
				WorkspaceHash:  sdkanalytics.WorkspaceHash(s.cfg.WorkspaceNamespace, workspacePath),
				WorkspaceLabel: sdkanalytics.WorkspaceLabel(workspacePath),
				ProjectHash:    projectHash,
				Payload: map[string]any{
					"tool_call_id": callID,
					"tool_name":    call.Name,
					"tool_input":   call.Parameters,
					"metadata":     map[string]any{"backfill": true},
				},
			}, "conversation:"+conv.ID); err != nil {
				return err
			}
		}
		for i, result := range msg.ToolResults {
			callID := result.CallID
			if callID == "" {
				callID = fmt.Sprintf("%s:result:%d", messageID, i)
			}
			toolName := result.Name
			if toolName == "" {
				toolName = toolNames[callID]
			}
			eventType := "tool.completed"
			errorMessage := ""
			if result.Error != nil {
				eventType = "tool.failed"
				errorMessage = result.Error.Message
			}
			if s.result.limitReached(opts.Limit) {
				return nil
			}
			if err := s.enqueueEvent(sdkanalytics.EventEnvelope{
				EventID:        stableID("event", eventType, conv.ID, callID, messageID),
				OccurredAt:     msg.Timestamp.UTC(),
				SessionID:      sessionID,
				ConversationID: conv.ID,
				EventType:      eventType,
				WorkspaceHash:  sdkanalytics.WorkspaceHash(s.cfg.WorkspaceNamespace, workspacePath),
				WorkspaceLabel: sdkanalytics.WorkspaceLabel(workspacePath),
				ProjectHash:    projectHash,
				Payload: map[string]any{
					"tool_call_id": callID,
					"tool_name":    toolName,
					"tool_input":   toolInputs[callID],
					"tool_output": map[string]any{
						"output":  result.Output,
						"content": result.Content,
					},
					"error":    errorMessage,
					"metadata": map[string]any{"backfill": true},
				},
			}, "conversation:"+conv.ID); err != nil {
				return err
			}
		}
	}

	if conv.Status != conversation.StatusActive && !s.result.limitReached(opts.Limit) {
		completedAt := conv.UpdatedAt
		if completedAt.IsZero() {
			completedAt = createdAt
		}
		if err := s.enqueueEvent(sdkanalytics.EventEnvelope{
			EventID:        stableID("event", "conversation.completed", conv.ID),
			OccurredAt:     completedAt.UTC(),
			SessionID:      sessionID,
			ConversationID: conv.ID,
			EventType:      "conversation.completed",
			WorkspaceHash:  sdkanalytics.WorkspaceHash(s.cfg.WorkspaceNamespace, workspacePath),
			WorkspaceLabel: sdkanalytics.WorkspaceLabel(workspacePath),
			ProjectHash:    projectHash,
			Payload: map[string]any{
				"status":   string(conv.Status),
				"backfill": true,
			},
		}, "conversation:"+conv.ID); err != nil {
			return err
		}
	}
	if !s.result.limitReached(opts.Limit) {
		stoppedAt := conv.UpdatedAt
		if stoppedAt.IsZero() {
			stoppedAt = createdAt
		}
		if err := s.enqueueEvent(sdkanalytics.EventEnvelope{
			EventID:        stableID("event", "session.stopped", conv.ID),
			OccurredAt:     stoppedAt.UTC(),
			SessionID:      sessionID,
			ConversationID: conv.ID,
			EventType:      "session.stopped",
			WorkspaceHash:  sdkanalytics.WorkspaceHash(s.cfg.WorkspaceNamespace, workspacePath),
			WorkspaceLabel: sdkanalytics.WorkspaceLabel(workspacePath),
			ProjectHash:    projectHash,
			Payload: map[string]any{
				"finish_reason": string(conv.Status),
				"metadata":      map[string]any{"backfill": true},
			},
		}, "conversation:"+conv.ID); err != nil {
			return err
		}
	}
	return nil
}

func backfillMessageID(convID string, index int, msg *conversation.Message) string {
	if msg == nil {
		return ""
	}
	if msg.ID != "" {
		return msg.ID
	}
	return stableID("message", convID, fmt.Sprint(index), string(msg.Role), msg.Content, msg.Timestamp.UTC().Format(time.RFC3339Nano))
}

func enqueueMessageEvent(s *sender, convID, sessionID, workspacePath, projectHash, messageID string, msg *conversation.Message) error {
	payload := map[string]any{
		"message_id":     messageID,
		"role":           string(msg.Role),
		"content":        msg.Content,
		"provider":       msg.Provider,
		"model":          msg.Model,
		"tool_calls":     msg.ToolCalls,
		"tool_results":   msg.ToolResults,
		"thinking":       msg.Thinking,
		"metadata":       msg.Metadata,
		"ordered_blocks": msg.OrderedBlocks,
		"backfill":       true,
	}
	if msg.ID == "" {
		payload["synthetic_message_id"] = true
	}
	if msg.Tokens != nil {
		payload["input_tokens"] = msg.Tokens.InputContextSize()
		payload["output_tokens"] = msg.Tokens.Output
	}
	occurredAt := msg.Timestamp
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return s.enqueueEvent(sdkanalytics.EventEnvelope{
		EventID:        stableID("event", "message.finalized", convID, messageID),
		OccurredAt:     occurredAt.UTC(),
		SessionID:      sessionID,
		ConversationID: convID,
		TurnID:         messageID,
		EventType:      "message.finalized",
		WorkspaceHash:  sdkanalytics.WorkspaceHash(s.cfg.WorkspaceNamespace, workspacePath),
		WorkspaceLabel: sdkanalytics.WorkspaceLabel(workspacePath),
		ProjectHash:    projectHash,
		Payload:        payload,
	}, "conversation:"+convID)
}

func conversationWorkspace(conv *conversation.Conversation) string {
	if conv == nil {
		return ""
	}
	if conv.WorkspacePath != "" {
		return conv.WorkspacePath
	}
	if conv.Metadata.Custom != nil {
		return mapString(conv.Metadata.Custom, "workspace_path", "workspace")
	}
	return ""
}

func conversationBranch(conv *conversation.Conversation) string {
	if conv == nil || conv.Metadata.Custom == nil {
		return ""
	}
	return mapString(conv.Metadata.Custom, "git_branch", "branch")
}

func firstProviderModel(conv *conversation.Conversation) (string, string) {
	if conv == nil {
		return "", ""
	}
	for _, msg := range conv.Messages {
		if msg == nil {
			continue
		}
		if msg.Provider != "" || msg.Model != "" {
			return msg.Provider, msg.Model
		}
	}
	return "", ""
}
