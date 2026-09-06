package a2a

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	a2apb "github.com/Swarm-Code/mono/swarm-sdk/internal/a2a/pb"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

// Tool is the shared interface for built-in A2A tools.
type Tool interface {
	tools.Tool
}

type runtimeTool struct {
	tools.BaseTool
	runtime *Runtime
	name    string
	desc    string
	params  any
	run     func(context.Context, map[string]any) (*tools.ToolResult, error)
}

func (t *runtimeTool) Name() string        { return t.name }
func (t *runtimeTool) Description() string { return t.desc }
func (t *runtimeTool) Parameters() any     { return t.params }
func (t *runtimeTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	return t.run(ctx, params)
}

func newListAgentsTool(runtime *Runtime) Tool {
	return &runtimeTool{
		runtime: runtime,
		name:    "a2a_list_agents",
		desc:    "List active A2A peer agents discovered in the same workspace or project scope, including their session IDs and RPC endpoints.",
		params: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		run: func(ctx context.Context, _ map[string]any) (*tools.ToolResult, error) {
			peers, err := runtime.ListAgents(ctx)
			if err != nil {
				return tools.NewErrorResult(err), nil
			}
			return jsonResult(CanonicalPeers(peers))
		},
	}
}

func newFetchAgentCardTool(runtime *Runtime) Tool {
	return &runtimeTool{
		runtime: runtime,
		name:    "a2a_fetch_agent_card",
		desc:    "Fetch a peer agent's A2A Agent Card from its well-known discovery endpoint.",
		params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"base_url": map[string]any{"type": "string", "description": "Peer base URL, without /.well-known/agent-card.json"},
			},
			"required": []string{"base_url"},
		},
		run: func(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
			baseURL, _ := params["base_url"].(string)
			card, err := runtime.FetchAgentCard(ctx, baseURL)
			if err != nil {
				return tools.NewErrorResult(err), nil
			}
			return protoResult(card)
		},
	}
}

func newSendMessageTool(runtime *Runtime, streaming bool) Tool {
	name := "a2a_send_message"
	desc := "Send an immediate A2A message to a peer agent."
	if streaming {
		name = "a2a_send_streaming_message"
		desc = "Send a streaming A2A message to a peer agent and project all returned task/message events."
	}
	return &runtimeTool{
		runtime: runtime,
		name:    name,
		desc:    desc + " Supports multipart content, contextId/taskId follow-ups, and referenceTaskIds for refinement.",
		params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"endpoint_url":          map[string]any{"type": "string"},
				"content":               map[string]any{"type": "string"},
				"parts":                 partSchema(),
				"context_id":            map[string]any{"type": "string"},
				"task_id":               map[string]any{"type": "string"},
				"reference_task_ids":    stringArraySchema(),
				"return_immediately":    map[string]any{"type": "boolean"},
				"history_length":        map[string]any{"type": "integer"},
				"accepted_output_modes": stringArraySchema(),
				"extensions":            stringArraySchema(),
			},
			"required": []string{"endpoint_url"},
		},
		run: func(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
			endpointURL, _ := params["endpoint_url"].(string)
			request, extensions, err := buildSendMessageRequest(params)
			if err != nil {
				return tools.NewErrorResult(err), nil
			}
			if !streaming {
				resp, err := runtime.SendMessage(ctx, endpointURL, request, extensions)
				if err != nil {
					return tools.NewErrorResult(err), nil
				}
				return protoResult(resp)
			}
			events, err := runtime.SendStreamingMessage(ctx, endpointURL, request, extensions)
			if err != nil {
				return tools.NewErrorResult(err), nil
			}
			return protoSliceResult(events)
		},
	}
}

func newGetTaskTool(runtime *Runtime) Tool {
	return &runtimeTool{
		runtime: runtime,
		name:    "a2a_get_task",
		desc:    "Get the latest state of a remote A2A task and project any new remote updates into the current conversation.",
		params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"endpoint_url":   map[string]any{"type": "string"},
				"task_id":        map[string]any{"type": "string"},
				"history_length": map[string]any{"type": "integer"},
				"extensions":     stringArraySchema(),
			},
			"required": []string{"endpoint_url", "task_id"},
		},
		run: func(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
			endpointURL, _ := params["endpoint_url"].(string)
			taskID, _ := params["task_id"].(string)
			req := &a2apb.GetTaskRequest{Id: taskID}
			if value, ok := int32Param(params["history_length"]); ok {
				req.HistoryLength = &value
			}
			task, err := runtime.GetTask(ctx, endpointURL, req, stringSlice(params["extensions"]))
			if err != nil {
				return tools.NewErrorResult(err), nil
			}
			return protoResult(task)
		},
	}
}

func newListTasksTool(runtime *Runtime) Tool {
	return &runtimeTool{
		runtime: runtime,
		name:    "a2a_list_tasks",
		desc:    "List remote A2A tasks with optional context and status filters.",
		params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"endpoint_url":      map[string]any{"type": "string"},
				"context_id":        map[string]any{"type": "string"},
				"status":            map[string]any{"type": "string", "description": "Example: TASK_STATE_WORKING"},
				"page_size":         map[string]any{"type": "integer"},
				"page_token":        map[string]any{"type": "string"},
				"history_length":    map[string]any{"type": "integer"},
				"include_artifacts": map[string]any{"type": "boolean"},
				"extensions":        stringArraySchema(),
			},
			"required": []string{"endpoint_url"},
		},
		run: func(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
			endpointURL, _ := params["endpoint_url"].(string)
			req := &a2apb.ListTasksRequest{
				ContextId: stringParam(params["context_id"]),
				PageToken: stringParam(params["page_token"]),
			}
			if value, ok := int32Param(params["page_size"]); ok {
				req.PageSize = &value
			}
			if value, ok := int32Param(params["history_length"]); ok {
				req.HistoryLength = &value
			}
			if _, present := params["include_artifacts"]; present {
				value := boolParam(params["include_artifacts"])
				req.IncludeArtifacts = &value
			}
			if status := strings.TrimSpace(stringParam(params["status"])); status != "" {
				if enum, ok := a2apb.TaskState_value[status]; ok {
					req.Status = a2apb.TaskState(enum)
				}
			}
			resp, err := runtime.ListTasks(ctx, endpointURL, req, stringSlice(params["extensions"]))
			if err != nil {
				return tools.NewErrorResult(err), nil
			}
			return protoResult(resp)
		},
	}
}

func newCancelTaskTool(runtime *Runtime) Tool {
	return &runtimeTool{
		runtime: runtime,
		name:    "a2a_cancel_task",
		desc:    "Cancel a remote A2A task and return the updated task snapshot.",
		params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"endpoint_url": map[string]any{"type": "string"},
				"task_id":      map[string]any{"type": "string"},
				"extensions":   stringArraySchema(),
			},
			"required": []string{"endpoint_url", "task_id"},
		},
		run: func(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
			task, err := runtime.CancelTask(ctx, stringParam(params["endpoint_url"]), &a2apb.CancelTaskRequest{
				Id: stringParam(params["task_id"]),
			}, stringSlice(params["extensions"]))
			if err != nil {
				return tools.NewErrorResult(err), nil
			}
			return protoResult(task)
		},
	}
}

func newSubscribeTaskTool(runtime *Runtime) Tool {
	return &runtimeTool{
		runtime: runtime,
		name:    "a2a_subscribe_task",
		desc:    "Subscribe to a remote A2A task's SSE stream and project all remote updates into the current conversation.",
		params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"endpoint_url": map[string]any{"type": "string"},
				"task_id":      map[string]any{"type": "string"},
				"extensions":   stringArraySchema(),
			},
			"required": []string{"endpoint_url", "task_id"},
		},
		run: func(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
			events, err := runtime.SubscribeToTask(ctx, stringParam(params["endpoint_url"]), &a2apb.SubscribeToTaskRequest{
				Id: stringParam(params["task_id"]),
			}, stringSlice(params["extensions"]))
			if err != nil {
				return tools.NewErrorResult(err), nil
			}
			return protoSliceResult(events)
		},
	}
}

func buildSendMessageRequest(params map[string]any) (*a2apb.SendMessageRequest, []string, error) {
	parts, err := parseParts(params["parts"])
	if err != nil {
		return nil, nil, err
	}
	if len(parts) == 0 {
		content := strings.TrimSpace(stringParam(params["content"]))
		if content == "" {
			return nil, nil, fmt.Errorf("content or parts is required")
		}
		parts = []*a2apb.Part{{
			Content:   &a2apb.Part_Text{Text: content},
			MediaType: "text/plain",
		}}
	}
	req := &a2apb.SendMessageRequest{
		Message: &a2apb.Message{
			MessageId:        ensureID(""),
			ContextId:        stringParam(params["context_id"]),
			TaskId:           stringParam(params["task_id"]),
			Role:             a2apb.Role_ROLE_USER,
			Parts:            parts,
			ReferenceTaskIds: stringSlice(params["reference_task_ids"]),
		},
		Configuration: &a2apb.SendMessageConfiguration{
			AcceptedOutputModes: stringSlice(params["accepted_output_modes"]),
			ReturnImmediately:   boolParam(params["return_immediately"]),
		},
	}
	if value, ok := int32Param(params["history_length"]); ok {
		req.Configuration.HistoryLength = &value
	}
	return req, stringSlice(params["extensions"]), nil
}

func parseParts(value any) ([]*a2apb.Part, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, nil
	}
	parts := make([]*a2apb.Part, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		part := &a2apb.Part{
			Filename:  stringParam(obj["filename"]),
			MediaType: stringParam(obj["media_type"]),
		}
		switch strings.ToLower(strings.TrimSpace(stringParam(obj["type"]))) {
		case "text":
			part.Content = &a2apb.Part_Text{Text: stringParam(obj["text"])}
			if part.MediaType == "" {
				part.MediaType = "text/plain"
			}
		case "url":
			part.Content = &a2apb.Part_Url{Url: stringParam(obj["url"])}
		case "raw":
			raw, err := base64.StdEncoding.DecodeString(stringParam(obj["raw_base64"]))
			if err != nil {
				return nil, fmt.Errorf("decode raw part: %w", err)
			}
			part.Content = &a2apb.Part_Raw{Raw: raw}
		case "data":
			value, err := structpb.NewValue(obj["data"])
			if err != nil {
				return nil, fmt.Errorf("encode data part: %w", err)
			}
			part.Content = &a2apb.Part_Data{Data: value}
			if part.MediaType == "" {
				part.MediaType = "application/json"
			}
		default:
			continue
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func protoResult(msg any) (*tools.ToolResult, error) {
	switch typed := msg.(type) {
	case *a2apb.AgentCard:
		raw, err := protojson.Marshal(typed)
		if err != nil {
			return nil, err
		}
		return tools.NewToolResult(string(raw)), nil
	case *a2apb.SendMessageResponse:
		raw, err := protojson.Marshal(typed)
		if err != nil {
			return nil, err
		}
		return tools.NewToolResult(string(raw)), nil
	case *a2apb.Task:
		raw, err := protojson.Marshal(typed)
		if err != nil {
			return nil, err
		}
		return tools.NewToolResult(string(raw)), nil
	case *a2apb.ListTasksResponse:
		raw, err := protojson.Marshal(typed)
		if err != nil {
			return nil, err
		}
		return tools.NewToolResult(string(raw)), nil
	default:
		return jsonResult(msg)
	}
}

func protoSliceResult(events []*a2apb.StreamResponse) (*tools.ToolResult, error) {
	if len(events) == 0 {
		return tools.NewToolResult("[]"), nil
	}
	payload := make([]json.RawMessage, 0, len(events))
	for _, event := range events {
		raw, err := protojson.Marshal(event)
		if err != nil {
			return nil, err
		}
		payload = append(payload, raw)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return tools.NewToolResult(string(raw)), nil
}

func jsonResult(value any) (*tools.ToolResult, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return tools.NewToolResult(string(raw)), nil
}

func partSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type":       map[string]any{"type": "string", "enum": []string{"text", "data", "url", "raw"}},
				"text":       map[string]any{"type": "string"},
				"url":        map[string]any{"type": "string"},
				"data":       map[string]any{},
				"raw_base64": map[string]any{"type": "string"},
				"filename":   map[string]any{"type": "string"},
				"media_type": map[string]any{"type": "string"},
			},
			"required": []string{"type"},
		},
	}
}

func stringArraySchema() map[string]any {
	return map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string"},
	}
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func stringParam(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func boolParam(value any) bool {
	flag, _ := value.(bool)
	return flag
}

func int32Param(value any) (int32, bool) {
	switch typed := value.(type) {
	case float64:
		return int32(typed), true
	case int:
		return int32(typed), true
	case int32:
		return typed, true
	default:
		return 0, false
	}
}

// ============================================================================
// Swarm Chatroom Tools
// ============================================================================

// SwarmToolInterface is implemented by Runtime for swarm tool execution.
type SwarmToolInterface interface {
	ListPeers(ctx context.Context) (*SwarmListPeersResult, error)
	SendDM(ctx context.Context, peerHandle, message string) (*SwarmDMResult, error)
	Broadcast(ctx context.Context, message string) (*SwarmBroadcastResult, error)
	UpdateStatus(ctx context.Context, status SwarmStatus, task string) (*SwarmUpdateStatusResult, error)
}

func newSwarmListPeersTool(runtime SwarmToolInterface) Tool {
	return &runtimeTool{
		name: "swarm_list_peers",
		desc: `List all connected peer agents in the swarm with their status, current task, and running time.

Use this to see who's online and what they're working on. Other agents can see your status too.

Returns:
- peers: List of connected agents with their handle, model, status, task, and running time
- self: Your own status as visible to others`,
		params: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		run: func(ctx context.Context, _ map[string]any) (*tools.ToolResult, error) {
			result, err := runtime.ListPeers(ctx)
			if err != nil {
				return tools.NewErrorResult(err), nil
			}
			return jsonResult(result)
		},
	}
}

func newSwarmDMTool(runtime SwarmToolInterface) Tool {
	return &runtimeTool{
		name: "swarm_dm",
		desc: `Send a direct message to a specific peer agent in the swarm.

Use this to coordinate privately with another agent, ask them a question, or collaborate on a shared task.

Parameters:
- peer_handle: The handle of the target peer (e.g., "agent-tui-DEF456")
- message: The message content to send

The peer agent will receive your message along with your current task context.`,
		params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"peer_handle": map[string]any{
					"type":        "string",
					"description": "The handle of the target peer agent",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "The message to send",
				},
			},
			"required": []string{"peer_handle", "message"},
		},
		run: func(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
			peerHandle := stringParam(params["peer_handle"])
			message := stringParam(params["message"])
			if peerHandle == "" {
				return tools.NewErrorResult(fmt.Errorf("peer_handle is required")), nil
			}
			if message == "" {
				return tools.NewErrorResult(fmt.Errorf("message is required")), nil
			}
			result, err := runtime.SendDM(ctx, peerHandle, message)
			if err != nil {
				return tools.NewErrorResult(err), nil
			}
			return jsonResult(result)
		},
	}
}

func newSwarmBroadcastTool(runtime SwarmToolInterface) Tool {
	return &runtimeTool{
		name: "swarm_broadcast",
		desc: `Send a message to all connected peer agents in the swarm.

Use this to share discoveries, ask for help, or announce progress to everyone.

Parameters:
- message: The message content to broadcast

All connected agents will receive your message along with your current task context.`,
		params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "The message to broadcast to all peers",
				},
			},
			"required": []string{"message"},
		},
		run: func(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
			message := stringParam(params["message"])
			if message == "" {
				return tools.NewErrorResult(fmt.Errorf("message is required")), nil
			}
			result, err := runtime.Broadcast(ctx, message)
			if err != nil {
				return tools.NewErrorResult(err), nil
			}
			return jsonResult(result)
		},
	}
}

func newSwarmUpdateStatusTool(runtime SwarmToolInterface) Tool {
	return &runtimeTool{
		name: "swarm_update_status",
		desc: `Update your status visible to other agents in the swarm.

Use this when you start or finish a significant task so others know what you're working on.

Parameters:
- status: Your availability - "idle", "working", "busy", or "away"
- task: Brief description of what you're working on (optional for idle/away)

Status meanings:
- idle: Not actively working, accepting DMs, can help peers
- working: Actively processing, accepting DMs but may be slow to respond
- busy: Deep focus work, auto-decline help requests with polite message
- away: User stepped away, pause notifications but keep connection`,
		params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"idle", "working", "busy", "away"},
					"description": "Your availability status",
				},
				"task": map[string]any{
					"type":        "string",
					"description": "Brief description of what you're working on",
				},
			},
			"required": []string{"status"},
		},
		run: func(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
			statusStr := stringParam(params["status"])
			task := stringParam(params["task"])
			status := SwarmStatus(statusStr)
			if status == "" {
				status = SwarmStatusIdle
			}
			result, err := runtime.UpdateStatus(ctx, status, task)
			if err != nil {
				return tools.NewErrorResult(err), nil
			}
			return jsonResult(result)
		},
	}
}

// SwarmTools returns all swarm chatroom tools.
// Disabled: broadcast, dm, list_peers, update_status are not exposed to agents.
func SwarmTools(_ SwarmToolInterface) []Tool {
	return nil
}
