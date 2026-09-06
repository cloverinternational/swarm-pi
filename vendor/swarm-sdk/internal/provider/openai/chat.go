package openai

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// chat implements synchronous chat completion.
func (p *Provider) chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	// Start span for this operation
	ctx, span := p.tracer.StartSpan(ctx, "openai.chat")
	defer span.End()

	// Set span attributes
	span.SetAttribute(observability.AttrProvider, "openai")
	span.SetAttribute(observability.AttrModel, req.Model)
	span.SetAttribute("message_count", len(req.Messages))

	// Log request
	p.logger.Info(ctx, "openai.chat.request",
		observability.F("model", req.Model),
		observability.F("message_count", len(req.Messages)),
	)

	// Translate canonical request to OpenAI format
	openaiReq, err := TranslateRequest(req)
	if err != nil {
		err = sdkerr.Wrap(
			err,
			"provider.openai.request_translation_failed",
			sdkerr.WithOperation("openai.chat"),
			sdkerr.WithComponent("provider.openai"),
			sdkerr.WithTraceFromContext(ctx),
		)
		span.RecordError(err)
		p.logger.Error(ctx, "openai.chat.translation_failed",
			observability.F("error", err.Error()),
		)
		return nil, err
	}

	// Make API request
	resp, err := p.client.Post(ctx, p.baseURL+"/chat/completions", openaiReq)
	if err != nil {
		err = sdkerr.Wrap(
			err,
			"provider.openai.request_failed",
			sdkerr.WithOperation("openai.chat"),
			sdkerr.WithComponent("provider.openai"),
			sdkerr.WithTraceFromContext(ctx),
		)
		span.RecordError(err)
		p.logger.Error(ctx, "openai.chat.request_failed",
			observability.F("error", err.Error()),
		)
		return nil, err
	}
	defer resp.Close()

	// Get raw body for debugging
	body, bodyErr := resp.Body()
	if bodyErr != nil {
		return nil, sdkerr.Wrap(
			bodyErr,
			"provider.openai.response_body_read_failed",
			sdkerr.WithOperation("openai.chat"),
			sdkerr.WithComponent("provider.openai"),
			sdkerr.WithTraceFromContext(ctx),
		)
	}

	// Log raw response for debugging
	p.logger.Debug(ctx, "openai.chat.raw_body",
		observability.F("body_length", len(body)),
		observability.F("body_preview", string(body[:min(len(body), 500)])),
	)

	// Parse response
	var openaiResp ChatCompletionResponse
	if err := resp.JSON(&openaiResp); err != nil {
		err = sdkerr.Wrap(
			err,
			"provider.openai.response_parse_failed",
			sdkerr.WithOperation("openai.chat"),
			sdkerr.WithComponent("provider.openai"),
			sdkerr.WithTraceFromContext(ctx),
		)
		span.RecordError(err)
		p.logger.Error(ctx, "openai.chat.parse_failed",
			observability.F("error", err.Error()),
			observability.F("body_preview", string(body[:min(len(body), 200)])),
		)
		return nil, err
	}

	// Debug: log the response structure
	p.logger.Debug(ctx, "openai.chat.parsed_response",
		observability.F("response_id", openaiResp.ID),
		observability.F("model", openaiResp.Model),
		observability.F("choices_count", len(openaiResp.Choices)),
	)

	// Log token parsing from JSON - supports both OpenAI and Anthropic-style formats
	inputTokens := openaiResp.Usage.InputCount()
	outputTokens := openaiResp.Usage.OutputCount()
	totalTokens := openaiResp.Usage.TotalCount()

	if totalTokens > 0 || inputTokens > 0 {
		p.logger.Debug(ctx, "openai.chat.parsing_tokens",
			observability.F("input_tokens", inputTokens),
			observability.F("output_tokens", outputTokens),
			observability.F("total_tokens", totalTokens),
			observability.F("format", func() string {
				if openaiResp.Usage.PromptTokens > 0 {
					return "openai"
				}
				return "anthropic-style"
			}()),
		)
	}

	// Translate OpenAI response to canonical format
	canonicalResp, err := TranslateResponse(&openaiResp)
	if err != nil {
		err = sdkerr.Wrap(
			err,
			"provider.openai.response_translation_failed",
			sdkerr.WithOperation("openai.chat"),
			sdkerr.WithComponent("provider.openai"),
			sdkerr.WithTraceFromContext(ctx),
		)
		span.RecordError(err)
		p.logger.Error(ctx, "openai.chat.response_translation_failed",
			observability.F("error", err.Error()),
		)
		return nil, err
	}

	// Log success with token usage
	if canonicalResp.Usage != nil {
		p.logger.Info(ctx, "openai.chat.success",
			observability.F("model", req.Model),
			observability.F("input_tokens", canonicalResp.Usage.Input),
			observability.F("output_tokens", canonicalResp.Usage.Output),
			observability.F("total_tokens", canonicalResp.Usage.Total),
			observability.F("finish_reason", string(canonicalResp.FinishReason)),
		)

		// Set token usage on span
		span.SetAttribute(observability.AttrTokens, canonicalResp.Usage.Total)
	}

	return canonicalResp, nil
}
