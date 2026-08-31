package approval

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// IPCSender is a function that sends permission requests via IPC.
type IPCSender func(req *PermissionRequest) error

// OnResolvedCallback is called when a permission request is resolved.
type OnResolvedCallback func(requestID string, outcome Outcome)

// ApprovalBroker manages permission request lifecycle.
type ApprovalBroker struct {
	config     BrokerConfig
	ipcSender  IPCSender
	onResolved OnResolvedCallback

	// pending tracks all pending requests by request ID
	pending map[string]*pendingRequest

	// batches tracks batch groupings
	batches map[string][]string // batchID -> []requestIDs

	mu sync.RWMutex
}

// pendingRequest tracks a single pending request and its response channel.
type pendingRequest struct {
	request  PermissionRequest
	response chan ApprovalResponse
	batchID  string
}

// NewApprovalBroker creates a new approval broker with the given configuration.
func NewApprovalBroker(config BrokerConfig) *ApprovalBroker {
	return &ApprovalBroker{
		config:  config,
		pending: make(map[string]*pendingRequest),
		batches: make(map[string][]string),
	}
}

// SetIPCSender sets the function used to send IPC requests to the IDE.
func (b *ApprovalBroker) SetIPCSender(sender IPCSender) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ipcSender = sender
}

// SetConfig updates the broker configuration.
func (b *ApprovalBroker) SetConfig(config BrokerConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.config = config
}

// GetConfig returns a copy of the current broker configuration.
func (b *ApprovalBroker) GetConfig() BrokerConfig {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.config
}

// SetOnResolved sets the callback invoked when a permission request is resolved.
func (b *ApprovalBroker) SetOnResolved(callback OnResolvedCallback) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onResolved = callback
}

// Request sends an approval request and blocks until response or timeout.
func (b *ApprovalBroker) Request(ctx context.Context, req PermissionRequest) (ApprovalResponse, error) {
	// Fast path: if AutoApproveAll is set, skip the IPC round-trip entirely.
	b.mu.RLock()
	autoApprove := b.config.AutoApproveAll
	b.mu.RUnlock()
	if autoApprove {
		return ApprovalResponse{Outcome: OutcomeApproved}, nil
	}

	// Create response channel
	respChan := make(chan ApprovalResponse, 1)

	// Determine timeout
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = b.config.Timeout
	}
	if timeout <= 0 {
		timeout = 300 // Default 5 minutes
	}

	// Register pending request
	b.mu.Lock()
	// Check for batching opportunity while holding the write lock because
	// batching may update both existing pending requests and batch indexes.
	batchID := b.checkBatching(req)
	pr := &pendingRequest{
		request:  req,
		response: respChan,
		batchID:  batchID,
	}
	req.BatchID = batchID
	b.pending[req.RequestID] = pr

	if batchID != "" {
		b.batches[batchID] = append(b.batches[batchID], req.RequestID)
	}

	sender := b.ipcSender
	b.mu.Unlock()

	// Send via IPC
	if sender != nil {
		if err := sender(&req); err != nil {
			b.removePending(req.RequestID)
			return ApprovalResponse{Outcome: OutcomeDenied}, err
		}
	}

	// Wait for response or timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	select {
	case resp := <-respChan:
		return resp, nil
	case <-timeoutCtx.Done():
		b.removePending(req.RequestID)

		// Get callback under lock
		b.mu.RLock()
		callback := b.onResolved
		b.mu.RUnlock()

		if ctx.Err() != nil {
			// Context cancelled - treat as denied
			if callback != nil {
				callback(req.RequestID, OutcomeDenied)
			}
			return ApprovalResponse{Outcome: OutcomeDenied}, ctx.Err()
		}

		// Timeout - call synchronously since we're outside lock and about to return
		if callback != nil {
			callback(req.RequestID, OutcomeTimeout)
		}
		return ApprovalResponse{Outcome: OutcomeTimeout}, errors.New("permission request timed out")
	}
}

// checkBatching determines if this request should be batched with existing requests.
func (b *ApprovalBroker) checkBatching(req PermissionRequest) string {
	if !b.config.BatchingEnabled {
		return ""
	}

	// Look for similar pending requests
	for id, pr := range b.pending {
		if pr.request.Tool == req.Tool && pr.request.Permission == req.Permission {
			// Found a similar request - join its batch or create one
			if pr.batchID != "" {
				return pr.batchID
			}
			// Create new batch ID
			batchID := fmt.Sprintf("batch-%s-%d", req.Tool, time.Now().UnixNano())
			// Update existing request's batch ID
			pr.batchID = batchID
			pr.request.BatchID = batchID
			// Register batch
			if b.batches == nil {
				return batchID
			}
			b.batches[batchID] = []string{id}
			return batchID
		}
	}

	return ""
}

// Respond handles a response from the IDE for a specific request or batch.
func (b *ApprovalBroker) Respond(requestID string, decision Decision) error {
	b.mu.Lock()

	// Check if this is a batch ID
	if requestIDs, ok := b.batches[requestID]; ok {
		// Respond to all requests in the batch
		outcome := decisionToOutcome(decision)
		callback := b.onResolved
		// Collect IDs for callback (call outside lock)
		resolvedIDs := make([]string, 0, len(requestIDs))
		for _, id := range requestIDs {
			if pr, exists := b.pending[id]; exists {
				resp := ApprovalResponse{
					Decision: decision,
					Outcome:  outcome,
				}
				select {
				case pr.response <- resp:
				default:
				}
				delete(b.pending, id)
				resolvedIDs = append(resolvedIDs, id)
			}
		}
		delete(b.batches, requestID)
		b.mu.Unlock()

		// Notify resolved (outside lock)
		if callback != nil {
			for _, id := range resolvedIDs {
				callback(id, outcome)
			}
		}
		return nil
	}

	// Single request
	pr, ok := b.pending[requestID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("request %s not found", requestID)
	}

	outcome := decisionToOutcome(decision)
	resp := ApprovalResponse{
		Decision: decision,
		Outcome:  outcome,
	}

	// Set granted scope if approved
	switch decision {
	case DecisionApproveOnce:
		resp.GrantedScope = "once"
	case DecisionApproveSession:
		resp.GrantedScope = "session"
	case DecisionApproveAlways:
		resp.GrantedScope = "always"
	}

	// Remove from batch if applicable
	if pr.batchID != "" {
		if batch, exists := b.batches[pr.batchID]; exists {
			newBatch := make([]string, 0, len(batch)-1)
			for _, id := range batch {
				if id != requestID {
					newBatch = append(newBatch, id)
				}
			}
			if len(newBatch) == 0 {
				delete(b.batches, pr.batchID)
			} else {
				b.batches[pr.batchID] = newBatch
			}
		}
	}

	callback := b.onResolved
	delete(b.pending, requestID)
	b.mu.Unlock()

	// Send response
	select {
	case pr.response <- resp:
	default:
	}

	// Notify resolved (synchronous since we're outside lock)
	if callback != nil {
		callback(requestID, outcome)
	}

	return nil
}

// GetPendingRequests returns all currently pending requests.
func (b *ApprovalBroker) GetPendingRequests() []PermissionRequest {
	b.mu.RLock()
	defer b.mu.RUnlock()

	requests := make([]PermissionRequest, 0, len(b.pending))
	for _, pr := range b.pending {
		req := pr.request
		req.BatchID = pr.batchID
		requests = append(requests, req)
	}
	return requests
}

// removePending removes a request from the pending map.
func (b *ApprovalBroker) removePending(requestID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if pr, ok := b.pending[requestID]; ok {
		// Remove from batch if applicable
		if pr.batchID != "" {
			if batch, exists := b.batches[pr.batchID]; exists {
				newBatch := make([]string, 0, len(batch)-1)
				for _, id := range batch {
					if id != requestID {
						newBatch = append(newBatch, id)
					}
				}
				if len(newBatch) == 0 {
					delete(b.batches, pr.batchID)
				} else {
					b.batches[pr.batchID] = newBatch
				}
			}
		}
		delete(b.pending, requestID)
	}
}

// decisionToOutcome converts a decision to an outcome.
func decisionToOutcome(d Decision) Outcome {
	switch d {
	case DecisionApproveOnce, DecisionApproveSession, DecisionApproveAlways:
		return OutcomeApproved
	default:
		return OutcomeDenied
	}
}
