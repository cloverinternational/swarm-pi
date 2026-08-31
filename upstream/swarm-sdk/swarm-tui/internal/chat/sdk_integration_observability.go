package chat

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
)

// getProviderDebugInfo returns URL and headers for the current provider
func (sdk *SDKIntegration) getProviderDebugInfo() (baseURL string, headers map[string]string) {
	headers = map[string]string{
		"Content-Type": "application/json",
	}

	// Get provider config to determine the actual URL and headers
	providerCfg, err := getProviderConfigFromFile(sdk.GetProviderName())
	if err == nil && providerCfg != nil && providerCfg.BaseURL != "" {
		baseURL = providerCfg.BaseURL + "/chat/completions"
	}

	normalized := provider.NormalizeProviderName(sdk.GetProviderName())
	switch normalized {
	case "openai", "codex":
		if sdk.isOAuth && providerCfg != nil && strings.EqualFold(providerCfg.Type, "oauth") {
			oauthBaseURL := normalizeOpenAIOAuthBaseURL(providerCfg.BaseURL)
			if oauthBaseURL == "" {
				baseURL = "https://chatgpt.com/backend-api/codex/responses"
			} else {
				baseURL = strings.TrimRight(oauthBaseURL, "/") + "/responses"
			}
			headers["Authorization"] = "Bearer " + sdk.sanitizeAPIKey(sdk.authToken)
			var token *openai.OAuthToken
			var err error
			token, err = openai.GetStoredOAuthToken()
			if err == nil && token != nil {
				var accountID string = token.AccountID
				if accountID == "" && token.IDToken != "" {
					accountID = openai.ExtractAccountIDFromIDToken(token.IDToken)
				}
				if accountID != "" {
					headers["ChatGPT-Account-ID"] = accountID
				}
			}
			break
		}
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1/chat/completions"
		}
		headers["Authorization"] = "Bearer " + sdk.sanitizeAPIKey(sdk.authToken)
	case "gemini", "gemini-code-assist":
		if baseURL == "" {
			baseURL = "https://cloudcode-pa.googleapis.com/v1internal:generateContent"
		} else {
			baseURL = strings.TrimRight(baseURL, "/") + "/v1internal:generateContent"
		}
		headers["Authorization"] = "Bearer " + sdk.sanitizeAPIKey(sdk.authToken)
	case "cerebras":
		if baseURL == "" {
			baseURL = "https://api.cerebras.ai/v1/chat/completions"
		}
		headers["Authorization"] = "Bearer " + sdk.sanitizeAPIKey(sdk.authToken)
	case "z.ai", "zai":
		if baseURL == "" {
			baseURL = "https://api.z.ai/api/coding/paas/v4/chat/completions"
		}
		headers["Authorization"] = "Bearer " + sdk.sanitizeAPIKey(sdk.authToken)
	case "openrouter":
		if baseURL == "" {
			baseURL = "https://openrouter.ai/api/v1/chat/completions"
		}
		headers["Authorization"] = "Bearer " + sdk.sanitizeAPIKey(sdk.authToken)
	case "fireworks":
		if baseURL == "" {
			baseURL = "https://api.fireworks.ai/inference/v1/chat/completions"
		}
		headers["Authorization"] = "Bearer " + sdk.sanitizeAPIKey(sdk.authToken)
	case "groq":
		if baseURL == "" {
			baseURL = "https://api.groq.com/openai/v1/chat/completions"
		}
		headers["Authorization"] = "Bearer " + sdk.sanitizeAPIKey(sdk.authToken)
	case "together", "together-ai", "togetherai":
		if baseURL == "" {
			baseURL = "https://api.together.xyz/v1/chat/completions"
		}
		headers["Authorization"] = "Bearer " + sdk.sanitizeAPIKey(sdk.authToken)
	case "deepseek":
		if baseURL == "" {
			baseURL = "https://api.deepseek.com/v1/chat/completions"
		}
		headers["Authorization"] = "Bearer " + sdk.sanitizeAPIKey(sdk.authToken)
	case "perplexity":
		if baseURL == "" {
			baseURL = "https://api.perplexity.ai/chat/completions"
		}
		headers["Authorization"] = "Bearer " + sdk.sanitizeAPIKey(sdk.authToken)
	default: // anthropic
		baseURL = "https://api.anthropic.com/v1/messages"
		headers["anthropic-version"] = "2023-06-01"
		headers["x-api-key"] = sdk.sanitizeAPIKey(sdk.authToken)
	}

	return baseURL, headers
}

// GetModelContextWindow returns the context window for the current model
// Priority: 1. providers.json config (local takes priority), 2. Provider capabilities
// No hardcoded fallbacks - all values come from JSON config or provider
func (sdk *SDKIntegration) GetModelContextWindow() int {
	runtime := sdk.runtimeSnapshot()
	activeModel := runtime.Model
	if activeModel == "" {
		activeModel = sdk.currentModel
	}
	activeProviderName := runtime.ProviderName
	if activeProviderName == "" {
		activeProviderName = sdk.GetProviderName()
	}
	// Always log the lookup attempt
	sdk.debugLog(fmt.Sprintf("[ContextWindow] LOOKUP model='%s' provider='%s'", activeModel, activeProviderName))

	// The synchronized runtime snapshot is authoritative for the provider/model
	// currently serving requests and already includes explicit model overrides.
	if runtime.ContextWindow > 0 {
		return runtime.ContextWindow
	}

	// First, try to find the model in providers.json (local config takes priority)
	if activeModel != "" {
		cw := getModelContextWindow(activeProviderName, activeModel)
		sdk.debugLog(fmt.Sprintf("[ContextWindow] getModelContextWindow('%s', '%s') returned %d", activeProviderName, activeModel, cw))
		if cw > 0 {
			return cw
		}
	} else {
		sdk.debugLog("[ContextWindow] WARNING: sdk.currentModel is EMPTY!")
	}

	// Try sdkClient's provider info for context window.
	if sdk.sdkClient != nil {
		if cw := sdk.sdkClient.ProviderInfo().ContextWindow; cw > 0 {
			sdk.debugLog(fmt.Sprintf("[ContextWindow] FALLBACK to sdkClient.ProviderInfo: %d", cw))
			return cw
		}
	}

	// Second, try to get from the same synchronized provider slot used by
	// execution and compaction snapshots.
	if activeProvider := runtime.Provider; activeProvider != nil {
		caps := activeProvider.Capabilities()
		if caps.MaxContextWindow > 0 {
			sdk.debugLog(fmt.Sprintf("[ContextWindow] FALLBACK to provider caps: %d", caps.MaxContextWindow))
			return caps.MaxContextWindow
		}
	}

	// No context window found - return 0 to indicate unknown
	sdk.debugLog(fmt.Sprintf("[ContextWindow] NO CONTEXT WINDOW FOUND for '%s'", activeModel))
	return 0
}

// debugLog logs a message to the debug screen if available
func (sdk *SDKIntegration) debugLog(message string) {
	if sdk.debugScreen != nil {
		sdk.debugScreen.AddLog(message)
	}
}

// GetContextWindow returns the context window for the current model (deprecated, use GetModelContextWindow)
func (sdk *SDKIntegration) GetContextWindow() int {
	return sdk.GetModelContextWindow()
}

// captureRequest captures request details for debug screen
func (sdk *SDKIntegration) captureRequest(req provider.ChatRequest, model string, messageIndex int, startTime time.Time) DebugRequest {
	// Build request body JSON with tools
	reqBody := map[string]any{
		"model":      model,
		"messages":   sdk.messagesToJSON(req.Messages),
		"max_tokens": sdk.GetMaxTokens(),
		"stream":     true,
	}

	// Add tools if present
	if len(req.Tools) > 0 {
		reqBody["tools"] = sdk.toolsToJSON(req.Tools)
	}

	reqBodyJSON, _ := json.MarshalIndent(reqBody, "", "  ")

	// Get actual provider URL and headers
	baseURL, headers := sdk.getProviderDebugInfo()

	// Capture canonical conversation JSON
	canonicalConv := map[string]any{
		"message_count": len(req.Messages),
		"messages":      req.Messages,
		"system_prompt": req.SystemPrompt,
		"tools_count":   len(req.Tools),
	}
	canonicalJSON := marshalForDebug(canonicalConv)

	return DebugRequest{
		MessageIndex:  messageIndex,
		Timestamp:     startTime,
		Method:        "POST",
		URL:           baseURL,
		Headers:       headers,
		Body:          string(reqBodyJSON),
		CanonicalJSON: canonicalJSON,
		Model:         model,
		Metadata:      make(map[string]any),
	}
}

// captureResponse captures response details for debug screen
func (sdk *SDKIntegration) CaptureResponse(messageIndex int, content string, usage *conversation.TokenUsage, finishReason string) {
	// Publish token update for real-time UI updates
	if usage != nil {
		sdk.publishTokenUpdate(TokenUpdate{
			InputTokens:    usage.Input,
			OutputTokens:   usage.Output,
			TotalTokens:    usage.Input + usage.Output,
			IsStreaming:    finishReason == "",
			IsFinal:        finishReason != "",
			ConversationID: "", // Will be set by caller if needed
		})
	}

	if sdk.debugScreen == nil {
		return
	}

	duration := time.Since(sdk.lastReqStart)

	// Find the matching request and update it
	if len(sdk.debugScreen.requests) > 0 {
		lastReq := &sdk.debugScreen.requests[len(sdk.debugScreen.requests)-1]
		if lastReq.MessageIndex == messageIndex {
			respBody := map[string]any{
				"id":    fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				"type":  "message",
				"role":  "assistant",
				"model": lastReq.Model,
				"content": []map[string]any{
					{
						"type": "text",
						"text": content,
					},
				},
				"stop_reason": finishReason,
				"usage": map[string]int{
					"input_tokens":  usage.Input,
					"output_tokens": usage.Output,
				},
			}

			respJSON, _ := json.MarshalIndent(respBody, "", "  ")
			lastReq.Response = string(respJSON)
			lastReq.ResponseCode = 200
			lastReq.Duration = duration
			lastReq.TokensInput = usage.Input
			lastReq.TokensOutput = usage.Output
		}
	}
}

// captureError captures error details for debug screen
func (sdk *SDKIntegration) captureError(err error, messageIndex int, duration time.Duration) {
	if sdk.debugScreen == nil {
		return
	}

	if len(sdk.debugScreen.requests) > 0 {
		lastReq := &sdk.debugScreen.requests[len(sdk.debugScreen.requests)-1]
		if lastReq.MessageIndex == messageIndex {
			lastReq.Error = err.Error()
			lastReq.Duration = duration
			lastReq.ResponseCode = 500
		}
	}
}

// messagesToJSON converts messages to JSON-serializable format
func (sdk *SDKIntegration) messagesToJSON(messages []*conversation.Message) []map[string]string {
	result := make([]map[string]string, len(messages))
	for i, msg := range messages {
		result[i] = map[string]string{
			"role":    string(msg.Role),
			"content": msg.Content,
		}
	}
	return result
}

// toolsToJSON converts tools to JSON-serializable format
func (sdk *SDKIntegration) toolsToJSON(tools []provider.Tool) []map[string]any {
	result := make([]map[string]any, len(tools))
	for i, tool := range tools {
		result[i] = map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": tool.Parameters,
		}
	}
	return result
}

// sanitizeAPIKey shows only first/last 4 characters
func (sdk *SDKIntegration) sanitizeAPIKey(key string) string {
	if len(key) <= 12 {
		return "sk-ant-***"
	}
	return key[:7] + "..." + key[len(key)-4:]
}

// getToolsForRequest converts registered tools to provider.Tool format
func (sdk *SDKIntegration) getToolsForRequest() []provider.Tool {
	if sdk.toolRegistry == nil {
		return nil
	}

	toolNames := sdk.toolRegistry.List()
	tools := make([]provider.Tool, 0, len(toolNames))

	for _, name := range toolNames {
		tool, err := sdk.toolRegistry.Get(name)
		if err != nil {
			continue
		}

		tools = append(tools, provider.Tool{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  tool.Parameters(),
		})
	}

	return tools
}
