package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// BatchListParams contains parameters for listing batches.
type BatchListParams struct {
	BeforeID *string // ID to use as cursor for pagination (before)
	AfterID  *string // ID to use as cursor for pagination (after)
	Limit    *int    // Number of items to return (1-1000, default 20)
}

// BatchListResponse represents a paginated list of batches.
type BatchListResponse struct {
	Data    []BatchResponse `json:"data"`
	HasMore bool            `json:"has_more"`
	FirstID *string         `json:"first_id,omitempty"`
	LastID  *string         `json:"last_id,omitempty"`
}

// CreateBatch creates a new message batch.
//
// The Message Batches API allows processing multiple Messages API requests at once.
// Once created, batches begin processing immediately and can take up to 24 hours to complete.
//
// Example:
//
//	batch, err := provider.CreateBatch(ctx, anthropic.BatchRequest{
//	    Requests: []anthropic.BatchItem{
//	        {
//	            CustomID: "request-1",
//	            Params: anthropic.MessageRequest{
//	                Model: "claude-3-5-sonnet-20241022",
//	                MaxTokens: 1024,
//	                Messages: []anthropic.Message{
//	                    {Role: "user", Content: "Hello!"},
//	                },
//	            },
//	        },
//	    },
//	})
func (p *Provider) CreateBatch(ctx context.Context, req BatchRequest) (*BatchResponse, error) {
	// Start observability span
	ctx, span := p.tracer.StartSpan(ctx, "anthropic.create_batch")
	defer span.End()

	span.SetAttribute("provider", "anthropic")
	span.SetAttribute("request_count", len(req.Requests))

	// Validate request
	if len(req.Requests) == 0 {
		return nil, sdkerr.Permanent(
			"anthropic.batch.empty_requests",
			"batch must contain at least one request",
		)
	}

	// Check for duplicate custom IDs
	seen := make(map[string]bool)
	for _, item := range req.Requests {
		if item.CustomID == "" {
			return nil, sdkerr.Permanent(
				"anthropic.batch.missing_custom_id",
				"all batch items must have a custom_id",
			)
		}
		if seen[item.CustomID] {
			return nil, sdkerr.Permanent(
				"anthropic.batch.duplicate_custom_id",
				fmt.Sprintf("duplicate custom_id: %s", item.CustomID),
			)
		}
		seen[item.CustomID] = true
	}

	p.logger.Info(ctx, "anthropic.create_batch.request",
		observability.F("request_count", len(req.Requests)),
	)

	// Build request with beta headers
	headers := make(map[string]string)
	if len(p.config.BetaHeaders) > 0 {
		headers["anthropic-beta"] = strings.Join(p.config.BetaHeaders, ",")
	}

	// Make API request
	reqBuilder := p.client.BuildRequest(ctx).
		Method("POST").
		URL(p.config.BaseURL + "/v1/messages/batches").
		Body(req)

	for k, v := range headers {
		reqBuilder = reqBuilder.Header(k, v)
	}

	resp, err := p.client.Do(ctx, reqBuilder)
	if err != nil {
		p.logger.Error(ctx, "anthropic.create_batch.request_failed",
			observability.F("error", err.Error()),
		)
		return nil, err
	}

	// Parse response
	var batch BatchResponse
	if err := resp.JSON(&batch); err != nil {
		p.logger.Error(ctx, "anthropic.create_batch.parse_failed",
			observability.F("error", err.Error()),
		)
		return nil, sdkerr.Permanent(
			"anthropic.batch.unmarshal_error",
			fmt.Sprintf("failed to parse batch response: %v", err),
		)
	}

	p.logger.Info(ctx, "anthropic.create_batch.success",
		observability.F("batch_id", batch.ID),
		observability.F("status", batch.ProcessingStatus),
	)

	return &batch, nil
}

// GetBatchStatus retrieves the current status of a message batch.
//
// This endpoint is idempotent and can be used to poll for batch completion.
// When the batch processing ends, the results_url field will be populated.
//
// Example:
//
//	status, err := provider.GetBatchStatus(ctx, "msg_batch_abc123")
func (p *Provider) BatchStatus(ctx context.Context, batchID string) (*BatchResponse, error) {
	// Start observability span
	ctx, span := p.tracer.StartSpan(ctx, "anthropic.get_batch_status")
	defer span.End()

	span.SetAttribute("provider", "anthropic")
	span.SetAttribute("batch_id", batchID)

	if batchID == "" {
		return nil, sdkerr.Permanent(
			"anthropic.batch.missing_batch_id",
			"batch ID is required",
		)
	}

	// Build request with beta headers
	headers := make(map[string]string)
	if len(p.config.BetaHeaders) > 0 {
		headers["anthropic-beta"] = strings.Join(p.config.BetaHeaders, ",")
	}

	// Make API request
	url := fmt.Sprintf("%s/v1/messages/batches/%s", p.config.BaseURL, batchID)
	reqBuilder := p.client.BuildRequest(ctx).
		Method("GET").
		URL(url)

	for k, v := range headers {
		reqBuilder = reqBuilder.Header(k, v)
	}

	resp, err := p.client.Do(ctx, reqBuilder)
	if err != nil {
		p.logger.Error(ctx, "anthropic.get_batch_status.request_failed",
			observability.F("error", err.Error()),
			observability.F("batch_id", batchID),
		)
		return nil, err
	}

	// Parse response
	var batch BatchResponse
	if err := resp.JSON(&batch); err != nil {
		p.logger.Error(ctx, "anthropic.get_batch_status.parse_failed",
			observability.F("error", err.Error()),
		)
		return nil, sdkerr.Permanent(
			"anthropic.batch.unmarshal_error",
			fmt.Sprintf("failed to parse batch response: %v", err),
		)
	}

	return &batch, nil
}

// CancelBatch initiates cancellation of a message batch.
//
// Batches can be canceled any time before processing ends. Once cancellation
// is initiated, the batch enters a "canceling" state. The system may complete
// any in-progress, non-interruptible requests before finalizing cancellation.
//
// Example:
//
//	canceled, err := provider.CancelBatch(ctx, "msg_batch_abc123")
func (p *Provider) CancelBatch(ctx context.Context, batchID string) (*BatchResponse, error) {
	// Start observability span
	ctx, span := p.tracer.StartSpan(ctx, "anthropic.cancel_batch")
	defer span.End()

	span.SetAttribute("provider", "anthropic")
	span.SetAttribute("batch_id", batchID)

	if batchID == "" {
		return nil, sdkerr.Permanent(
			"anthropic.batch.missing_batch_id",
			"batch ID is required",
		)
	}

	p.logger.Info(ctx, "anthropic.cancel_batch.request",
		observability.F("batch_id", batchID),
	)

	// Build request with beta headers
	headers := make(map[string]string)
	if len(p.config.BetaHeaders) > 0 {
		headers["anthropic-beta"] = strings.Join(p.config.BetaHeaders, ",")
	}

	// Make API request
	url := fmt.Sprintf("%s/v1/messages/batches/%s/cancel", p.config.BaseURL, batchID)
	reqBuilder := p.client.BuildRequest(ctx).
		Method("POST").
		URL(url)

	for k, v := range headers {
		reqBuilder = reqBuilder.Header(k, v)
	}

	resp, err := p.client.Do(ctx, reqBuilder)
	if err != nil {
		p.logger.Error(ctx, "anthropic.cancel_batch.request_failed",
			observability.F("error", err.Error()),
			observability.F("batch_id", batchID),
		)
		return nil, err
	}

	// Parse response
	var batch BatchResponse
	if err := resp.JSON(&batch); err != nil {
		p.logger.Error(ctx, "anthropic.cancel_batch.parse_failed",
			observability.F("error", err.Error()),
		)
		return nil, sdkerr.Permanent(
			"anthropic.batch.unmarshal_error",
			fmt.Sprintf("failed to parse batch response: %v", err),
		)
	}

	p.logger.Info(ctx, "anthropic.cancel_batch.success",
		observability.F("batch_id", batch.ID),
		observability.F("status", batch.ProcessingStatus),
	)

	return &batch, nil
}

// GetBatchResults retrieves the results of a completed message batch.
//
// Results are returned as JSONL (JSON Lines) format, with one result per line.
// Each result includes the custom_id and the response or error.
//
// This method automatically downloads and parses the results from the results_url.
//
// Example:
//
//	results, err := provider.GetBatchResults(ctx, "msg_batch_abc123")
//	for _, result := range results {
//	    if result.Result.Type == "succeeded" {
//	        fmt.Println(result.Result.Message.Content)
//	    }
//	}
func (p *Provider) BatchResults(ctx context.Context, batchID string) ([]BatchResultResponse, error) {
	// Start observability span
	ctx, span := p.tracer.StartSpan(ctx, "anthropic.get_batch_results")
	defer span.End()

	span.SetAttribute("provider", "anthropic")
	span.SetAttribute("batch_id", batchID)

	if batchID == "" {
		return nil, sdkerr.Permanent(
			"anthropic.batch.missing_batch_id",
			"batch ID is required",
		)
	}

	// First, get the batch status to retrieve results_url
	status, err := p.BatchStatus(ctx, batchID)
	if err != nil {
		return nil, err
	}

	// Check if batch has finished processing
	if status.ProcessingStatus != "ended" {
		return nil, sdkerr.Permanent(
			"anthropic.batch.not_completed",
			fmt.Sprintf("batch is still %s, cannot retrieve results yet", status.ProcessingStatus),
		)
	}

	// Check if results URL is available
	if status.ResultsURL == nil || *status.ResultsURL == "" {
		return nil, sdkerr.Permanent(
			"anthropic.batch.no_results_url",
			"batch has no results_url available",
		)
	}

	p.logger.Info(ctx, "anthropic.get_batch_results.downloading",
		observability.F("batch_id", batchID),
		observability.F("results_url", *status.ResultsURL),
	)

	// Build request with beta headers
	headers := make(map[string]string)
	if len(p.config.BetaHeaders) > 0 {
		headers["anthropic-beta"] = strings.Join(p.config.BetaHeaders, ",")
	}

	// Download results from results_url
	reqBuilder := p.client.BuildRequest(ctx).
		Method("GET").
		URL(*status.ResultsURL)

	for k, v := range headers {
		reqBuilder = reqBuilder.Header(k, v)
	}

	resp, err := p.client.Do(ctx, reqBuilder)
	if err != nil {
		p.logger.Error(ctx, "anthropic.get_batch_results.request_failed",
			observability.F("error", err.Error()),
			observability.F("batch_id", batchID),
		)
		return nil, err
	}

	// Get response body as bytes (JSONL format)
	bodyBytes, err := resp.Body()
	if err != nil {
		p.logger.Error(ctx, "anthropic.get_batch_results.read_failed",
			observability.F("error", err.Error()),
		)
		return nil, sdkerr.Wrap(err, "anthropic.batch.read_error")
	}

	// Parse JSONL response (one JSON object per line)
	lines := strings.Split(strings.TrimSpace(string(bodyBytes)), "\n")
	results := make([]BatchResultResponse, 0, len(lines))

	for i, line := range lines {
		if line == "" {
			continue
		}

		var result BatchResultResponse
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			p.logger.Error(ctx, "anthropic.get_batch_results.parse_line_failed",
				observability.F("line_number", i+1),
				observability.F("error", err.Error()),
			)
			return nil, sdkerr.Permanent(
				"anthropic.batch.unmarshal_error",
				fmt.Sprintf("failed to parse result line %d: %v", i+1, err),
			)
		}

		results = append(results, result)
	}

	p.logger.Info(ctx, "anthropic.get_batch_results.success",
		observability.F("batch_id", batchID),
		observability.F("result_count", len(results)),
	)

	return results, nil
}

// ListBatches retrieves a list of all message batches.
//
// Batches are returned in descending order of creation time (most recent first).
// Supports pagination using before_id, after_id, and limit parameters.
//
// Example:
//
//	// List first 10 batches
//	limit := 10
//	batches, err := provider.ListBatches(ctx, &anthropic.BatchListParams{
//	    Limit: &limit,
//	})
//
//	// Paginate forward
//	if batches.HasMore {
//	    next, err := provider.ListBatches(ctx, &anthropic.BatchListParams{
//	        AfterID: batches.LastID,
//	        Limit: &limit,
//	    })
//	}
func (p *Provider) ListBatches(ctx context.Context, params *BatchListParams) (*BatchListResponse, error) {
	// Start observability span
	ctx, span := p.tracer.StartSpan(ctx, "anthropic.list_batches")
	defer span.End()

	span.SetAttribute("provider", "anthropic")

	// Build URL with query parameters
	url := fmt.Sprintf("%s/v1/messages/batches", p.config.BaseURL)

	// Add query parameters
	queryParams := make([]string, 0)
	if params != nil {
		if params.BeforeID != nil && *params.BeforeID != "" {
			queryParams = append(queryParams, fmt.Sprintf("before_id=%s", *params.BeforeID))
		}
		if params.AfterID != nil && *params.AfterID != "" {
			queryParams = append(queryParams, fmt.Sprintf("after_id=%s", *params.AfterID))
		}
		if params.Limit != nil {
			if *params.Limit < 1 || *params.Limit > 1000 {
				return nil, sdkerr.Permanent(
					"anthropic.batch.invalid_limit",
					"limit must be between 1 and 1000",
				)
			}
			queryParams = append(queryParams, fmt.Sprintf("limit=%d", *params.Limit))
		}
	}

	if len(queryParams) > 0 {
		url = fmt.Sprintf("%s?%s", url, strings.Join(queryParams, "&"))
	}

	// Build request with beta headers
	headers := make(map[string]string)
	if len(p.config.BetaHeaders) > 0 {
		headers["anthropic-beta"] = strings.Join(p.config.BetaHeaders, ",")
	}

	// Make API request
	reqBuilder := p.client.BuildRequest(ctx).
		Method("GET").
		URL(url)

	for k, v := range headers {
		reqBuilder = reqBuilder.Header(k, v)
	}

	resp, err := p.client.Do(ctx, reqBuilder)
	if err != nil {
		p.logger.Error(ctx, "anthropic.list_batches.request_failed",
			observability.F("error", err.Error()),
		)
		return nil, err
	}

	// Parse response
	var listResp BatchListResponse
	if err := resp.JSON(&listResp); err != nil {
		p.logger.Error(ctx, "anthropic.list_batches.parse_failed",
			observability.F("error", err.Error()),
		)
		return nil, sdkerr.Permanent(
			"anthropic.batch.unmarshal_error",
			fmt.Sprintf("failed to parse batch list response: %v", err),
		)
	}

	p.logger.Info(ctx, "anthropic.list_batches.success",
		observability.F("batch_count", len(listResp.Data)),
		observability.F("has_more", listResp.HasMore),
	)

	return &listResp, nil
}
