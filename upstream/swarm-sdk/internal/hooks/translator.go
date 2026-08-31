// Package hooks implements the event hook system.
// This file provides the HookTranslator for stable API versioning.
package hooks

import (
	"encoding/json"
	"time"
)

// Translator provides stable API translation between SDK internal types and hook types.
// This decouples hooks from SDK changes, ensuring backward compatibility.
type Translator interface {
	// ToHookInput converts event data to a hook-compatible input format.
	ToHookInput(eventName HookEventName, data map[string]any, sessionID, cwd string) (any, error)

	// FromHookResponse parses hook output and returns a typed response structure.
	FromHookResponse(eventName HookEventName, raw []byte) (*HookResponse, error)

	// ToHookLLMRequest converts SDK request types to stable hook LLM request format.
	ToHookLLMRequest(sdkRequest any) (*LLMRequest, error)

	// FromHookLLMRequest converts stable hook LLM request to SDK request types.
	FromHookLLMRequest(hookRequest *LLMRequest) (any, error)

	// ToHookLLMResponse converts SDK response types to stable hook LLM response format.
	ToHookLLMResponse(sdkResponse any) (*LLMResponse, error)

	// FromHookLLMResponse converts stable hook LLM response to SDK response types.
	FromHookLLMResponse(hookResponse *LLMResponse) (any, error)

	// Version returns the translator version for compatibility checks.
	Version() string
}

// DefaultTranslator is the standard translator implementation.
type DefaultTranslator struct {
	version string
}

// NewDefaultTranslator creates a new default translator.
func NewDefaultTranslator() *DefaultTranslator {
	return &DefaultTranslator{
		version: "v1",
	}
}

// Version returns the translator version.
func (t *DefaultTranslator) Version() string {
	return t.version
}

// ToHookInput converts event data to a hook-compatible input format.
func (t *DefaultTranslator) ToHookInput(eventName HookEventName, data map[string]any, sessionID, cwd string) (any, error) {
	base := HookInput{
		SessionID:     sessionID,
		Cwd:           cwd,
		HookEventName: string(eventName),
		Timestamp:     time.Now(),
	}

	switch eventName {
	case EventSessionStart:
		input := &SessionStartInput{HookInput: base}
		if source, ok := data["source"].(string); ok {
			input.Source = SessionStartSource(source)
		} else {
			input.Source = SessionSourceStartup
		}
		return input, nil

	case EventSessionEnd:
		input := &SessionEndInput{HookInput: base}
		if reason, ok := data["reason"].(string); ok {
			input.Reason = SessionEndReason(reason)
		} else {
			input.Reason = SessionEndOther
		}
		return input, nil

	case EventBeforeAgent:
		input := &BeforeAgentInput{HookInput: base}
		if prompt, ok := data["prompt"].(string); ok {
			input.Prompt = prompt
		}
		return input, nil

	case EventAfterAgent:
		input := &AfterAgentInput{HookInput: base}
		if prompt, ok := data["prompt"].(string); ok {
			input.Prompt = prompt
		}
		if response, ok := data["prompt_response"].(string); ok {
			input.PromptResponse = response
		}
		if stopActive, ok := data["stop_hook_active"].(bool); ok {
			input.StopHookActive = stopActive
		}
		return input, nil

	case EventBeforeTool:
		input := &BeforeToolInput{HookInput: base}
		if toolName, ok := data["tool_name"].(string); ok {
			input.ToolName = toolName
		}
		if toolInput, ok := data["tool_input"].(map[string]any); ok {
			input.ToolInput = toolInput
		}
		if mcpCtx, ok := data["mcp_context"].(map[string]any); ok {
			input.MCPContext = t.parseMCPContext(mcpCtx)
		}
		return input, nil

	case EventAfterTool:
		input := &AfterToolInput{HookInput: base}
		if toolName, ok := data["tool_name"].(string); ok {
			input.ToolName = toolName
		}
		if toolInput, ok := data["tool_input"].(map[string]any); ok {
			input.ToolInput = toolInput
		}
		if toolResponse, ok := data["tool_response"].(map[string]any); ok {
			input.ToolResponse = toolResponse
		}
		if mcpCtx, ok := data["mcp_context"].(map[string]any); ok {
			input.MCPContext = t.parseMCPContext(mcpCtx)
		}
		return input, nil

	case EventBeforeModel:
		input := &BeforeModelInput{HookInput: base}
		if llmReq, ok := data["llm_request"].(*LLMRequest); ok {
			input.LLMRequest = llmReq
		} else if llmReqMap, ok := data["llm_request"].(map[string]any); ok {
			input.LLMRequest = t.parseLLMRequestFromMap(llmReqMap)
		}
		return input, nil

	case EventAfterModel:
		input := &AfterModelInput{HookInput: base}
		if llmReq, ok := data["llm_request"].(*LLMRequest); ok {
			input.LLMRequest = llmReq
		}
		if llmResp, ok := data["llm_response"].(*LLMResponse); ok {
			input.LLMResponse = llmResp
		}
		return input, nil

	case EventBeforeToolSelection:
		input := &BeforeToolSelectionInput{HookInput: base}
		if llmReq, ok := data["llm_request"].(*LLMRequest); ok {
			input.LLMRequest = llmReq
		}
		return input, nil

	case EventPreCompress:
		input := &PreCompressInput{HookInput: base}
		if trigger, ok := data["trigger"].(string); ok {
			input.Trigger = PreCompressTrigger(trigger)
		} else {
			input.Trigger = CompressTriggerAuto
		}
		return input, nil

	case EventNotification:
		input := &NotificationInput{HookInput: base}
		if notifType, ok := data["notification_type"].(string); ok {
			input.NotificationType = NotificationType(notifType)
		}
		if message, ok := data["message"].(string); ok {
			input.Message = message
		}
		if details, ok := data["details"].(map[string]any); ok {
			input.Details = details
		}
		return input, nil

	default:
		// Return base input for unknown events
		return &base, nil
	}
}

// FromHookResponse parses raw hook output into a HookResponse structure.
func (t *DefaultTranslator) FromHookResponse(eventName HookEventName, raw []byte) (*HookResponse, error) {
	if len(raw) == 0 {
		return &HookResponse{Decision: DecisionAllow}, nil
	}

	var output HookResponse
	if err := json.Unmarshal(raw, &output); err != nil {
		// If not JSON, treat as system message
		return &HookResponse{
			Decision:      DecisionAllow,
			SystemMessage: string(raw),
		}, nil
	}

	return &output, nil
}

// ToHookLLMRequest converts SDK request types to stable hook LLM request format.
func (t *DefaultTranslator) ToHookLLMRequest(sdkRequest any) (*LLMRequest, error) {
	// Try to convert from map
	if reqMap, ok := sdkRequest.(map[string]any); ok {
		return t.parseLLMRequestFromMap(reqMap), nil
	}

	// Try direct type assertion
	if req, ok := sdkRequest.(*LLMRequest); ok {
		return req, nil
	}

	// Marshal and unmarshal for arbitrary types
	data, err := json.Marshal(sdkRequest)
	if err != nil {
		return nil, err
	}

	var req LLMRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}

	return &req, nil
}

// FromHookLLMRequest converts stable hook LLM request to SDK request types.
func (t *DefaultTranslator) FromHookLLMRequest(hookRequest *LLMRequest) (any, error) {
	// For now, return as-is. In the future, this would convert to SDK-specific types.
	return hookRequest, nil
}

// ToHookLLMResponse converts SDK response types to stable hook LLM response format.
func (t *DefaultTranslator) ToHookLLMResponse(sdkResponse any) (*LLMResponse, error) {
	// Try to convert from map
	if respMap, ok := sdkResponse.(map[string]any); ok {
		return t.parseLLMResponseFromMap(respMap), nil
	}

	// Try direct type assertion
	if resp, ok := sdkResponse.(*LLMResponse); ok {
		return resp, nil
	}

	// Marshal and unmarshal for arbitrary types
	data, err := json.Marshal(sdkResponse)
	if err != nil {
		return nil, err
	}

	var resp LLMResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// FromHookLLMResponse converts stable hook LLM response to SDK response types.
func (t *DefaultTranslator) FromHookLLMResponse(hookResponse *LLMResponse) (any, error) {
	// For now, return as-is. In the future, this would convert to SDK-specific types.
	return hookResponse, nil
}

// Helper functions

func (t *DefaultTranslator) parseMCPContext(data map[string]any) *MCPToolContext {
	ctx := &MCPToolContext{}
	if serverName, ok := data["server_name"].(string); ok {
		ctx.ServerName = serverName
	}
	if toolName, ok := data["tool_name"].(string); ok {
		ctx.ToolName = toolName
	}
	if command, ok := data["command"].(string); ok {
		ctx.Command = command
	}
	if args, ok := data["args"].([]any); ok {
		for _, arg := range args {
			if s, ok := arg.(string); ok {
				ctx.Args = append(ctx.Args, s)
			}
		}
	}
	if cwd, ok := data["cwd"].(string); ok {
		ctx.Cwd = cwd
	}
	if url, ok := data["url"].(string); ok {
		ctx.URL = url
	}
	if tcp, ok := data["tcp"].(string); ok {
		ctx.TCP = tcp
	}
	return ctx
}

func (t *DefaultTranslator) parseLLMRequestFromMap(data map[string]any) *LLMRequest {
	req := &LLMRequest{}
	if model, ok := data["model"].(string); ok {
		req.Model = model
	}
	if messages, ok := data["messages"].([]any); ok {
		for _, m := range messages {
			if msgMap, ok := m.(map[string]any); ok {
				msg := LLMMessage{}
				if role, ok := msgMap["role"].(string); ok {
					msg.Role = role
				}
				if content, ok := msgMap["content"].(string); ok {
					msg.Content = content
				}
				req.Messages = append(req.Messages, msg)
			}
		}
	}
	return req
}

func (t *DefaultTranslator) parseLLMResponseFromMap(data map[string]any) *LLMResponse {
	resp := &LLMResponse{}
	if text, ok := data["text"].(string); ok {
		resp.Text = text
	}
	if candidates, ok := data["candidates"].([]any); ok {
		for _, c := range candidates {
			if candMap, ok := c.(map[string]any); ok {
				cand := LLMCandidate{}
				if contentMap, ok := candMap["content"].(map[string]any); ok {
					if role, ok := contentMap["role"].(string); ok {
						cand.Content.Role = role
					}
					if parts, ok := contentMap["parts"].([]any); ok {
						for _, p := range parts {
							if s, ok := p.(string); ok {
								cand.Content.Parts = append(cand.Content.Parts, s)
							}
						}
					}
				}
				if reason, ok := candMap["finishReason"].(string); ok {
					cand.FinishReason = reason
				}
				resp.Candidates = append(resp.Candidates, cand)
			}
		}
	}
	return resp
}

// MapSwarmEventToHookEvent maps SwarmOS internal event types to hook event names.
func MapSwarmEventToHookEvent(swarmEvent string) HookEventName {
	mapping := map[string]HookEventName{
		EventToolBeforeExecute: EventBeforeTool,
		EventToolAfterExecute:  EventAfterTool,
		"user.prompt_submit":   EventBeforeAgent,
		"agent.stop":           EventAfterAgent,
		"session.start":        EventSessionStart,
		"session.end":          EventSessionEnd,
		"notification":         EventNotification,
		"compact.before":       EventPreCompress,
	}
	if mapped, ok := mapping[swarmEvent]; ok {
		return mapped
	}
	return HookEventName(swarmEvent)
}

// MapHookEventToSwarmEvent maps hook event names to SwarmOS internal event types.
func MapHookEventToSwarmEvent(hookEvent HookEventName) string {
	mapping := map[HookEventName]string{
		EventBeforeTool:   EventToolBeforeExecute,
		EventAfterTool:    EventToolAfterExecute,
		EventBeforeAgent:  "user.prompt_submit",
		EventAfterAgent:   "agent.stop",
		EventSessionStart: "session.start",
		EventSessionEnd:   "session.end",
		EventNotification: "notification",
		EventPreCompress:  "compact.before",
	}
	if mapped, ok := mapping[hookEvent]; ok {
		return mapped
	}
	return string(hookEvent)
}
