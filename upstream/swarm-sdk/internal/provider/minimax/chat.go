package minimax

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// chat sends a synchronous chat request and returns the complete response.
func (p *Provider) chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	// Translate request to MiniMax format
	minimaxReq, providerJSON, err := translateRequest(req, p.logger)
	if err != nil {
		return nil, err
	}

	// Store for debugging
	if rawJSON, err := json.Marshal(providerJSON); err == nil {
		p.lastProviderJSON = rawJSON
	}

	// Log request
	if p.logger != nil {
		p.logger.Debug(ctx, "minimax.chat.request",
			observability.F("model", minimaxReq.Model),
			observability.F("message_count", len(minimaxReq.Messages)),
		)
	}

	// Build and execute request using fluent API
	reqBuilder := p.client.BuildRequest(ctx).
		Method("POST").
		URL(p.config.BaseURL + "/v1/messages").
		Body(minimaxReq)

	resp, err := p.client.Do(ctx, reqBuilder)
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	// Check for HTTP errors
	if resp.IsError() {
		body, _ := resp.Body()
		return nil, p.handleAPIError(resp.StatusCode(), body)
	}

	// Parse response
	var minimaxResp MessageResponse
	if err := resp.JSON(&minimaxResp); err != nil {
		return nil, err
	}

	// Log response
	if p.logger != nil {
		p.logger.Debug(ctx, "minimax.chat.response",
			observability.F("model", minimaxResp.Model),
			observability.F("stop_reason", minimaxResp.StopReason),
			observability.F("input_tokens", minimaxResp.Usage.InputTokens),
			observability.F("output_tokens", minimaxResp.Usage.OutputTokens),
		)
	}

	// Translate response to canonical format
	return translateResponse(&minimaxResp)
}

// handleAPIError handles API error responses.
func (p *Provider) handleAPIError(statusCode int, body []byte) error {
	// Try to parse error response
	var errResp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Message != "" {
		return fmt.Errorf("minimax API error: %s - %s", errResp.Error.Type, errResp.Error.Message)
	}

	return fmt.Errorf("minimax API error: %s", string(body))
}
