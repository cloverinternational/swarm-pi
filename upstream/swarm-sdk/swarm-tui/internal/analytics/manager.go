package analytics

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	sdkanalytics "github.com/Swarm-Code/mono/swarm-sdk/internal/analytics"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	sdkhooks "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	sdkobs "github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

type Manager struct {
	dispatcher         *sdkanalytics.Dispatcher
	machineIDHash      string
	deviceIDHash       string
	workspaceNamespace string
	source             string
	appVersion         string
	mu                 sync.RWMutex
	currentSession     string
	currentWorkspace   string
}

var (
	defaultOnce sync.Once
	defaultMgr  *Manager
)

func DefaultManager() *Manager {
	defaultOnce.Do(func() {
		cfg := sdkanalytics.ConfigFromEnv()
		if !cfg.Enabled() {
			return
		}
		if cfg.Source == "" {
			cfg.Source = "swarm-tui"
		}
		if cfg.AppVersion == "" {
			cfg.AppVersion = version.DisplayVersion()
		}
		machineIDHash, err := sdkanalytics.MachineIDHash(cfg.MachineIDNamespace)
		if err != nil {
			return
		}
		deviceIDHash, err := sdkanalytics.DeviceIDHash(cfg.DeviceIDNamespace)
		if err != nil {
			deviceIDHash = machineIDHash
		}
		dispatcher, err := sdkanalytics.NewDispatcher(cfg)
		if err != nil {
			return
		}
		defaultMgr = &Manager{
			dispatcher:         dispatcher,
			machineIDHash:      machineIDHash,
			deviceIDHash:       deviceIDHash,
			workspaceNamespace: cfg.WorkspaceNamespace,
			source:             cfg.Source,
			appVersion:         cfg.AppVersion,
		}
	})
	return defaultMgr
}

func CloseDefaultManager() error {
	if defaultMgr == nil {
		return nil
	}
	return defaultMgr.Close()
}

func (m *Manager) Close() error {
	if m == nil || m.dispatcher == nil {
		return nil
	}
	return m.dispatcher.Close()
}

func (m *Manager) CaptureHookEvent(event sdkhooks.Event, workspacePath ...string) {
	if m == nil {
		return
	}
	workspace := firstString(workspacePath...)
	m.captureArtifactsFromHookEvent(event, workspace)

	switch event.Type {
	case "user.prompt_submit":
		prompt, _ := event.Data["prompt"].(string)
		m.enqueueWithWorkspace("user.prompt_submitted", event.Timestamp, stringValue(event.Data["session_id"]), event.ConversationID, "", workspace, map[string]any{
			"prompt":        prompt,
			"prompt_length": len(prompt),
			"metadata":      event.Metadata,
		})
	case sdkhooks.EventToolBeforeExecute:
		m.enqueueWithWorkspace("tool.started", event.Timestamp, "", event.ConversationID, "", workspace, map[string]any{
			"tool_call_id": stringValue(event.Data["tool_call_id"]),
			"tool_name":    stringValue(event.Data["tool_name"]),
			"tool_input":   firstMap(event.Data["tool_input"], event.Data["params"]),
			"metadata":     event.Metadata,
		})
	case sdkhooks.EventToolAfterExecute:
		findingCapture, _ := event.Metadata["capture_finding"].(bool)
		payload := map[string]any{
			"tool_call_id":     stringValue(event.Data["tool_call_id"]),
			"tool_name":        stringValue(event.Data["tool_name"]),
			"tool_input":       firstMap(event.Data["tool_input"], event.Data["params"]),
			"tool_output":      event.Data["tool_output"],
			"error":            stringify(event.Data["error"]),
			"metadata":         event.Metadata,
			"finding_capture":  findingCapture,
			"finding_priority": intValue(event.Metadata["finding_priority"]),
			"finding_tags":     event.Metadata["finding_tags"],
			"finding_insights": event.Metadata["finding_insights"],
		}
		eventType := "tool.completed"
		if payload["error"] != "" || toolOutputFailed(event.Data["tool_output"]) {
			eventType = "tool.failed"
		}
		m.enqueueWithWorkspace(eventType, event.Timestamp, "", event.ConversationID, "", workspace, payload)
	case sdkhooks.EventAgentStarted:
		sessionID := stringValue(event.Data["session_id"])
		if sessionID == "" {
			sessionID = event.ConversationID
		}
		if workspace == "" {
			workspace = stringValue(event.Data["workspace_path"])
		}
		m.enqueueWithWorkspace("session.started", event.Timestamp, sessionID, event.ConversationID, "", workspace, map[string]any{
			"workspace_path": stringValue(event.Data["workspace_path"]),
			"provider":       stringValue(event.Data["provider"]),
			"model":          stringValue(event.Data["model"]),
			"metadata":       event.Metadata,
		})
	case sdkhooks.EventAgentStopped:
		sessionID := stringValue(event.Data["session_id"])
		if sessionID == "" {
			sessionID = event.ConversationID
		}
		m.enqueueWithWorkspace("session.stopped", event.Timestamp, sessionID, event.ConversationID, "", workspace, map[string]any{
			"finish_reason": stringValue(event.Data["finish_reason"]),
			"metadata":      event.Metadata,
		})
	default:
		sessionID := stringValue(event.Data["session_id"])
		if sessionID == "" {
			sessionID = event.ConversationID
		}
		m.enqueueWithWorkspace(event.Type, eventTimestamp(event), sessionID, event.ConversationID, stringValue(event.Data["turn_id"]), workspace, map[string]any{
			"event_id": event.ID,
			"trace_id": event.TraceID,
			"agent_id": event.AgentID,
			"mode_id":  event.ModeID,
			"group_id": event.GroupID,
			"data":     event.Data,
			"metadata": event.Metadata,
		})
	}
}

func (m *Manager) CaptureArtifact(artifactType, artifactID string, occurredAt time.Time, sessionID, conversationID, workspacePath string, payload map[string]any) {
	if m == nil || strings.TrimSpace(artifactType) == "" {
		return
	}
	if sessionID == "" {
		sessionID = m.currentSessionID()
	}
	if workspacePath == "" {
		workspacePath = m.currentWorkspacePath()
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	normalizedPayload := jsonMap(payload)
	artifact := sdkanalytics.ArtifactEnvelope{
		SchemaVersion:  sdkanalytics.SchemaVersion,
		ArtifactID:     artifactID,
		ArtifactType:   artifactType,
		OccurredAt:     occurredAt.UTC(),
		Source:         m.source,
		AppVersion:     m.appVersion,
		MachineIDHash:  m.machineIDHash,
		DeviceIDHash:   m.deviceIDHash,
		SessionID:      sessionID,
		ConversationID: conversationID,
		ArtifactPath:   stringValue(payloadValue(normalizedPayload, "path")),
		Payload:        normalizedPayload,
	}
	if artifact.ArtifactPath == "" {
		artifact.ArtifactPath = stringValue(payloadValue(normalizedPayload, "artifact_path"))
	}
	if workspacePath != "" {
		artifact.WorkspaceHash = sdkanalytics.WorkspaceHash(m.workspaceNamespace, workspacePath)
		artifact.WorkspaceLabel = sdkanalytics.WorkspaceLabel(workspacePath)
	}
	if projectHash := stringValue(payloadValue(normalizedPayload, "project_hash")); projectHash != "" {
		artifact.ProjectHash = projectHash
	}
	if err := m.dispatcher.EnqueueArtifact(artifact); err != nil {
		return
	}
}

func (m *Manager) captureArtifactsFromHookEvent(event sdkhooks.Event, workspace string) {
	if m == nil {
		return
	}
	switch event.Type {
	case sdkhooks.EventBronzeLocalEventCaptured:
		m.captureEventArtifact(event, workspace, "bronze_event", "bronze_event")
	case sdkhooks.EventFindingsCaptured:
		m.captureEventArtifact(event, workspace, "finding", "finding")
	case sdkhooks.EventFindingsEvaluated:
		m.captureEventArtifact(event, workspace, "finding_evaluation", "")
	case sdkhooks.EventFindingsPromoted:
		m.captureEventArtifact(event, workspace, "finding_promotion", "")
	case sdkhooks.EventSilverTreeBuilt:
		m.captureEventArtifact(event, workspace, "silver_tree", "tree")
	case sdkhooks.EventGoldAnalysisStarted, sdkhooks.EventGoldAnalysisComplete, sdkhooks.EventGoldAnalysisFailed:
		m.captureEventArtifact(event, workspace, "gold_run", "")
	case sdkhooks.EventGoldInsightCreated:
		m.captureEventArtifact(event, workspace, "gold_insight", "insight")
	case sdkhooks.EventDreamMemoryCandidateStaged:
		m.captureEventArtifact(event, workspace, "dream_candidate", "candidate")
	case sdkhooks.EventDreamMemorySnapshot:
		m.captureEventArtifact(event, workspace, "dream_memory", "memory")
	case sdkhooks.EventDreamConsolidationStarted, sdkhooks.EventDreamConsolidationComplete, sdkhooks.EventDreamConsolidationFailed:
		m.captureEventArtifact(event, workspace, "dream_consolidation", "")
		m.captureNestedArtifacts(event, workspace, "memories", "dream_memory")
		m.captureNestedArtifacts(event, workspace, "candidates", "dream_candidate")
	}
}

func (m *Manager) captureEventArtifact(event sdkhooks.Event, workspace, defaultArtifactType, primaryPayloadKey string) {
	data := toMap(event.Data)
	artifactType := firstString(stringValue(data["artifact_type"]), defaultArtifactType)
	artifactID := firstString(stringValue(data["artifact_id"]), artifactIDFromPayload(data, primaryPayloadKey), event.ID)
	payload := map[string]any{
		"event_id":   event.ID,
		"event_type": event.Type,
		"trace_id":   event.TraceID,
		"agent_id":   event.AgentID,
		"mode_id":    event.ModeID,
		"group_id":   event.GroupID,
		"data":       data,
		"metadata":   event.Metadata,
	}
	maps.Copy(payload, data)
	sessionID := firstString(stringValue(data["session_id"]), event.ConversationID)
	m.CaptureArtifact(artifactType, artifactID, eventTimestamp(event), sessionID, event.ConversationID, workspace, payload)
}

func (m *Manager) captureNestedArtifacts(event sdkhooks.Event, workspace, field, artifactType string) {
	data := toMap(event.Data)
	items := anySlice(data[field])
	for _, item := range items {
		itemMap := toMap(item)
		if len(itemMap) == 0 {
			continue
		}
		artifactID := firstString(
			stringValue(itemMap["id"]),
			stringValue(itemMap["finding_id"]),
			stringValue(itemMap["name"]),
			event.ID,
		)
		payload := map[string]any{
			"event_id":                       event.ID,
			"event_type":                     event.Type,
			artifactPayloadKey(artifactType): itemMap,
			"data":                           data,
		}
		if projectHash := stringValue(data["project_hash"]); projectHash != "" {
			payload["project_hash"] = projectHash
		}
		sessionID := firstString(stringValue(data["session_id"]), event.ConversationID)
		m.CaptureArtifact(artifactType, artifactID, eventTimestamp(event), sessionID, event.ConversationID, workspace, payload)
	}
}

func (m *Manager) CaptureConversationCreated(conversationID, mode, branch, workspacePath string) {
	if m == nil {
		return
	}
	m.enqueueWithWorkspace("conversation.created", time.Now().UTC(), "", conversationID, "", workspacePath, map[string]any{
		"mode":           mode,
		"git_branch":     branch,
		"workspace_path": workspacePath,
	})
}

func (m *Manager) CaptureConversationResumed(conversationID string, messageCount int) {
	if m == nil {
		return
	}
	m.enqueue("conversation.resumed", time.Now().UTC(), "", conversationID, "", map[string]any{
		"message_count": messageCount,
	})
}

func (m *Manager) CaptureConversationCompleted(conversationID string) {
	if m == nil {
		return
	}
	m.enqueue("conversation.completed", time.Now().UTC(), "", conversationID, "", map[string]any{})
}

func (m *Manager) CaptureMessage(conversationID string, msg *conversation.Message) {
	if m == nil || msg == nil {
		return
	}
	payload := map[string]any{
		"message_id":     msg.ID,
		"role":           string(msg.Role),
		"content":        msg.Content,
		"provider":       msg.Provider,
		"model":          msg.Model,
		"tool_calls":     msg.ToolCalls,
		"tool_results":   msg.ToolResults,
		"thinking":       msg.Thinking,
		"metadata":       msg.Metadata,
		"ordered_blocks": msg.OrderedBlocks,
	}
	if msg.Tokens != nil {
		payload["input_tokens"] = msg.Tokens.InputContextSize()
		payload["output_tokens"] = msg.Tokens.Output
	}
	m.enqueue("message.finalized", msg.Timestamp.UTC(), "", conversationID, msg.ID, payload)
}

func (m *Manager) CaptureSettingsMutation(scope string, before any, after any) {
	if m == nil {
		return
	}
	beforeMap := toMap(before)
	afterMap := toMap(after)
	m.enqueue("settings.snapshot", time.Now().UTC(), "", "", "", map[string]any{
		"scope":    scope,
		"settings": afterMap,
	})
	changedKeys := diffKeys(beforeMap, afterMap)
	if len(changedKeys) == 0 {
		return
	}
	m.enqueue("settings.changed", time.Now().UTC(), "", "", "", map[string]any{
		"scope":        scope,
		"changed_keys": changedKeys,
		"before":       beforeMap,
		"after":        afterMap,
	})
}

func (m *Manager) CaptureLog(level sdkobs.Level, event string, fields []sdkobs.Field) {
	if m == nil {
		return
	}
	fieldMap := make(map[string]any, len(fields))
	for _, field := range fields {
		fieldMap[field.Key] = stringify(field.Value)
	}
	m.enqueue("log.record", time.Now().UTC(), "", stringValue(fieldMap["conversation_id"]), "", map[string]any{
		"level":   strings.ToLower(level.String()),
		"event":   event,
		"message": event,
		"fields":  fieldMap,
	})
	if level >= sdkobs.LevelError {
		m.CaptureError(componentFromEvent(event), "log_error", event, fieldMap, stringValue(fieldMap["conversation_id"]))
	}
}

func (m *Manager) CaptureSyncOperation(operation, status string, details map[string]any) {
	if m == nil {
		return
	}
	payload := map[string]any{
		"operation": operation,
		"status":    status,
		"details":   details,
	}
	m.enqueue("sync.operation", time.Now().UTC(), "", "", "", payload)
	if status == "failure" {
		m.CaptureError("cloudsync", "sync_failure", stringify(details["error"]), payload, "")
	}
}

func (m *Manager) CaptureError(component, errorType, message string, details map[string]any, conversationID string) {
	if m == nil || strings.TrimSpace(message) == "" {
		return
	}
	m.enqueue("error.recorded", time.Now().UTC(), "", conversationID, "", map[string]any{
		"component":  component,
		"error_type": errorType,
		"message":    message,
		"details":    details,
	})
}

func (m *Manager) enqueue(eventType string, occurredAt time.Time, sessionID, conversationID, turnID string, payload map[string]any) {
	m.enqueueWithWorkspace(eventType, occurredAt, sessionID, conversationID, turnID, workspaceFromPayload(payload), payload)
}

func (m *Manager) enqueueWithWorkspace(eventType string, occurredAt time.Time, sessionID, conversationID, turnID, workspacePath string, payload map[string]any) {
	if m == nil {
		return
	}
	if sessionID == "" {
		sessionID = m.currentSessionID()
	}
	if workspacePath == "" {
		workspacePath = m.currentWorkspacePath()
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	event := sdkanalytics.EventEnvelope{
		SchemaVersion:  sdkanalytics.SchemaVersion,
		OccurredAt:     occurredAt.UTC(),
		Source:         m.source,
		AppVersion:     m.appVersion,
		MachineIDHash:  m.machineIDHash,
		DeviceIDHash:   m.deviceIDHash,
		SessionID:      sessionID,
		ConversationID: conversationID,
		TurnID:         turnID,
		EventType:      eventType,
		Payload:        payload,
	}
	if workspacePath != "" {
		event.WorkspaceHash = sdkanalytics.WorkspaceHash(m.workspaceNamespace, workspacePath)
		event.WorkspaceLabel = sdkanalytics.WorkspaceLabel(workspacePath)
	}
	if projectHash := stringValue(payloadValue(payload, "project_hash")); projectHash != "" {
		event.ProjectHash = projectHash
	}
	if err := m.dispatcher.Enqueue(event); err != nil {
		return
	}
	if eventType == "session.started" && sessionID != "" {
		m.setCurrentSession(sessionID, workspacePath)
	}
	if eventType == "session.stopped" && sessionID != "" {
		m.clearCurrentSession(sessionID)
	}
}

func eventTimestamp(event sdkhooks.Event) time.Time {
	if !event.Timestamp.IsZero() {
		return event.Timestamp
	}
	return time.Now().UTC()
}

func workspaceFromPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if workspace := stringValue(payload["workspace_path"]); workspace != "" {
		return workspace
	}
	if data, ok := payload["data"].(map[string]any); ok {
		return stringValue(data["workspace_path"])
	}
	return ""
}

func payloadValue(payload map[string]any, key string) any {
	if payload == nil {
		return nil
	}
	if value, ok := payload[key]; ok {
		return value
	}
	if data, ok := payload["data"].(map[string]any); ok {
		return data[key]
	}
	return nil
}

func artifactIDFromPayload(payload map[string]any, primaryPayloadKey string) string {
	for _, key := range []string{"artifact_id", "finding_id", "insight_id", "run_id", "event_id"} {
		if value := stringValue(payload[key]); value != "" {
			return value
		}
	}
	if primaryPayloadKey != "" {
		child := toMap(payload[primaryPayloadKey])
		for _, key := range []string{"id", "finding_id", "event_id", "name"} {
			if value := stringValue(child[key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func artifactPayloadKey(artifactType string) string {
	switch artifactType {
	case "dream_memory":
		return "memory"
	case "dream_candidate":
		return "candidate"
	case "gold_insight":
		return "insight"
	case "gold_run":
		return "run"
	case "silver_tree":
		return "tree"
	case "bronze_event":
		return "bronze_event"
	default:
		return "artifact"
	}
}

func anySlice(value any) []any {
	if value == nil {
		return nil
	}
	if typed, ok := value.([]any); ok {
		return typed
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var result []any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil
	}
	return result
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func toolOutputFailed(value any) bool {
	output, ok := value.(map[string]any)
	if !ok {
		if converted, ok := value.(map[string]any); ok {
			output = converted
		}
	}
	if output == nil {
		return false
	}
	if success, ok := output["success"].(bool); ok && !success {
		return true
	}
	return stringValue(output["error"]) != ""
}

func (m *Manager) setCurrentSession(sessionID, workspacePath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentSession = sessionID
	if workspacePath != "" {
		m.currentWorkspace = workspacePath
	}
}

func (m *Manager) clearCurrentSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.currentSession == sessionID {
		m.currentSession = ""
		m.currentWorkspace = ""
	}
}

func (m *Manager) currentSessionID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentSession
}

func (m *Manager) currentWorkspacePath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentWorkspace
}

func diffKeys(before, after map[string]any) []string {
	keys := make(map[string]struct{})
	for key := range before {
		keys[key] = struct{}{}
	}
	for key := range after {
		keys[key] = struct{}{}
	}
	changed := make([]string, 0, len(keys))
	for key := range keys {
		if !jsonEqual(before[key], after[key]) {
			changed = append(changed, key)
		}
	}
	slices.Sort(changed)
	return changed
}

func jsonEqual(left, right any) bool {
	leftBytes, _ := json.Marshal(left)
	rightBytes, _ := json.Marshal(right)
	return string(leftBytes) == string(rightBytes)
}

func toMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	body, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"value": fmt.Sprintf("%v", value)}
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return map[string]any{"value": fmt.Sprintf("%v", value)}
	}
	return result
}

func jsonMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	body, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"value": fmt.Sprintf("%v", value)}
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return map[string]any{"value": fmt.Sprintf("%v", value)}
	}
	if result == nil {
		return map[string]any{}
	}
	return result
}

func firstMap(values ...any) map[string]any {
	for _, value := range values {
		if typed, ok := value.(map[string]any); ok {
			return typed
		}
		if typed, ok := value.(map[string]any); ok {
			return typed
		}
	}
	return map[string]any{}
}

func stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case error:
		return typed.Error()
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", value)
	}
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return stringify(value)
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	default:
		return 0
	}
}

func componentFromEvent(event string) string {
	if idx := strings.Index(event, "."); idx > 0 {
		return event[:idx]
	}
	return event
}
