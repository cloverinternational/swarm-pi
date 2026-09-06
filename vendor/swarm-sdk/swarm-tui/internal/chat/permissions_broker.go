package chat

import (
	"context"
	"fmt"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// permissionApprovalMsg notifies the UI about a new approval request.
type permissionApprovalMsg struct {
	request tools.PermissionApprovalRequest
}

// permissionApprovalResolvedMsg notifies the UI that a request resolved elsewhere (timeout/cancel).
type permissionApprovalResolvedMsg struct {
	requestID string
	outcome   tools.Outcome
}

type approvalWaiter struct {
	request tools.PermissionApprovalRequest
	respCh  chan tools.ApprovalResponse
}

// PermissionsBroker bridges SDK approvals into the TUI.
type PermissionsBroker struct {
	mu       sync.Mutex
	pending  []tools.PermissionApprovalRequest
	waiters  map[string]*approvalWaiter
	config   tools.PermissionConfig
	store    *PermissionConfig
	dispatch func(tea.Msg)
}

// NewPermissionsBroker creates a broker with an optional config store.
func NewPermissionsBroker(store *PermissionConfig) *PermissionsBroker {
	return &PermissionsBroker{
		pending: make([]tools.PermissionApprovalRequest, 0),
		waiters: make(map[string]*approvalWaiter),
		store:   store,
	}
}

// SetDispatcher wires the broker to the Bubble Tea runtime.
func (b *PermissionsBroker) SetDispatcher(dispatch func(tea.Msg)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dispatch = dispatch
}

// Request sends approval request to UI and blocks until response or timeout.
func (b *PermissionsBroker) Request(ctx context.Context, req tools.PermissionApprovalRequest) (tools.ApprovalResponse, error) {
	waiter := &approvalWaiter{
		request: req,
		respCh:  make(chan tools.ApprovalResponse, 1),
	}

	b.mu.Lock()
	b.waiters[req.RequestID] = waiter
	b.pending = append(b.pending, req)
	dispatch := b.dispatch
	b.mu.Unlock()

	if dispatch == nil {
		b.mu.Lock()
		delete(b.waiters, req.RequestID)
		b.removeRequestLocked(req.RequestID)
		b.mu.Unlock()
		return tools.ApprovalResponse{
			Decision: tools.DecisionDeny,
			Outcome:  tools.OutcomeDenied,
		}, fmt.Errorf("approval dispatcher not available")
	}

	dispatch(permissionApprovalMsg{request: req})

	select {
	case resp := <-waiter.respCh:
		return resp, nil
	case <-ctx.Done():
		b.mu.Lock()
		b.removeRequestLocked(req.RequestID)
		dispatch = b.dispatch
		b.mu.Unlock()

		if dispatch != nil {
			dispatch(permissionApprovalResolvedMsg{requestID: req.RequestID, outcome: tools.OutcomeTimeout})
		}

		return tools.ApprovalResponse{
			Decision: tools.DecisionDeny,
			Outcome:  tools.OutcomeTimeout,
		}, ctx.Err()
	}
}

// Respond handles response from UI.
func (b *PermissionsBroker) Respond(requestID string, decision tools.Decision) error {
	return b.RespondWithContext(requestID, decision, "")
}

// RespondWithContext handles response from UI with optional user context.
func (b *PermissionsBroker) RespondWithContext(requestID string, decision tools.Decision, userContext string) error {
	b.mu.Lock()
	waiter, ok := b.waiters[requestID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("unknown approval request: %s", requestID)
	}
	delete(b.waiters, requestID)
	b.removeRequestLocked(requestID)
	b.mu.Unlock()

	resp := tools.ApprovalResponse{
		Decision:    decision,
		Outcome:     outcomeFromDecision(decision),
		UserContext: userContext,
	}

	select {
	case waiter.respCh <- resp:
	default:
	}

	return nil
}

// SetConfig updates permission configuration and persists to disk.
func (b *PermissionsBroker) SetConfig(config tools.PermissionConfig) {
	b.mu.Lock()
	b.config = config
	store := b.store
	b.mu.Unlock()

	if store != nil {
		store.SetConfig(config)
		if err := store.Save(); err != nil {
			logDebug("failed to save permission config: %v", err)
		}
	}
}

// GetPendingRequests returns all pending requests.
func (b *PermissionsBroker) GetPendingRequests() []tools.PermissionApprovalRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]tools.PermissionApprovalRequest, len(b.pending))
	copy(out, b.pending)
	return out
}

func (b *PermissionsBroker) removeRequestLocked(requestID string) {
	for i, req := range b.pending {
		if req.RequestID == requestID {
			b.pending = append(b.pending[:i], b.pending[i+1:]...)
			break
		}
	}
}

func outcomeFromDecision(decision tools.Decision) tools.Outcome {
	switch decision {
	case tools.DecisionApproveOnce, tools.DecisionApproveSession, tools.DecisionApproveAlways, tools.DecisionSaveProject:
		return tools.OutcomeApproved
	default:
		return tools.OutcomeDenied
	}
}
