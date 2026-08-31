package chat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/interaction"
)

// questionRequestMsg notifies the UI about a new question request.
type questionRequestMsg struct {
	request interaction.QuestionRequest
}

// questionResolvedMsg notifies the UI that a question resolved elsewhere (timeout/cancel).
type questionResolvedMsg struct {
	requestID string
	timedOut  bool
}

type questionWaiter struct {
	request interaction.QuestionRequest
	respCh  chan interaction.QuestionResponse
}

// QuestionBroker bridges SDK question requests into the TUI.
type QuestionBroker struct {
	mu       sync.Mutex
	pending  []interaction.QuestionRequest
	waiters  map[string]*questionWaiter
	dispatch func(tea.Msg)
}

// NewQuestionBroker creates a new question broker.
func NewQuestionBroker() *QuestionBroker {
	return &QuestionBroker{
		pending: make([]interaction.QuestionRequest, 0),
		waiters: make(map[string]*questionWaiter),
	}
}

// SetDispatcher wires the broker to the Bubble Tea runtime.
func (b *QuestionBroker) SetDispatcher(dispatch func(tea.Msg)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dispatch = dispatch
}

// Request sends a question request to the UI and blocks until a response, the
// request's explicit timeout, or explicit parent/session cancellation. Framework
// deadline expiry after accepted dispatch does not consume the requested duration.
func (b *QuestionBroker) Request(ctx context.Context, req interaction.QuestionRequest) (interaction.QuestionResponse, error) {
	if err := ctx.Err(); err != nil {
		return interaction.QuestionResponse{Canceled: true},
			interaction.NewError(interaction.OutcomeParentCanceled, err)
	}
	waiter := &questionWaiter{
		request: req,
		respCh:  make(chan interaction.QuestionResponse, 1),
	}

	b.mu.Lock()
	if _, exists := b.waiters[req.ID]; exists {
		b.mu.Unlock()
		return interaction.QuestionResponse{},
			interaction.NewError(interaction.OutcomeDeliveryFailure, fmt.Errorf("duplicate question request: %s", req.ID))
	}
	b.waiters[req.ID] = waiter
	b.pending = append(b.pending, req)
	dispatch := b.dispatch
	b.mu.Unlock()

	if dispatch == nil {
		b.mu.Lock()
		delete(b.waiters, req.ID)
		b.removeRequestLocked(req.ID)
		b.mu.Unlock()
		return interaction.QuestionResponse{
			Canceled: true,
		}, interaction.NewError(interaction.OutcomeInteractiveUnavailable, errors.New("question dispatcher not available"))
	}

	if err := dispatchQuestion(dispatch, req); err != nil {
		b.mu.Lock()
		delete(b.waiters, req.ID)
		b.removeRequestLocked(req.ID)
		b.mu.Unlock()
		return interaction.QuestionResponse{},
			interaction.NewError(interaction.OutcomeDeliveryFailure, err)
	}
	// Dispatch is the acceptance boundary. If the framework context became
	// terminal before or during dispatch, report that lifecycle outcome now
	// rather than allowing the request timer to call it a user timeout.
	if err := ctx.Err(); err != nil {
		b.resolveRequest(req.ID, false)
		return interaction.QuestionResponse{Canceled: true},
			interaction.NewError(interaction.OutcomeParentCanceled, context.Cause(ctx))
	}

	var timeout <-chan time.Time
	var timer *time.Timer
	if req.Timeout > 0 {
		timer = time.NewTimer(time.Duration(req.Timeout) * time.Second)
		timeout = timer.C
		defer timer.Stop()
	}
	cancelled := explicitQuestionCancellation(ctx)

	select {
	case resp := <-waiter.respCh:
		return resp, nil
	case <-timeout:
		b.resolveRequest(req.ID, true)
		return interaction.QuestionResponse{
			Timeout: true,
		}, nil
	case <-cancelled:
		b.resolveRequest(req.ID, false)
		return interaction.QuestionResponse{Canceled: true},
			interaction.NewError(interaction.OutcomeParentCanceled, context.Cause(ctx))
	}
}

// explicitQuestionCancellation ignores framework deadline expiry after dispatch
// while still honoring a direct cancel from the parent/session lifecycle.
func explicitQuestionCancellation(ctx context.Context) <-chan struct{} {
	cancelled := make(chan struct{})
	if ctx.Err() == context.Canceled {
		close(cancelled)
		return cancelled
	}
	if ctx.Done() == nil {
		return cancelled
	}
	go func() {
		<-ctx.Done()
		if ctx.Err() == context.Canceled {
			close(cancelled)
		}
	}()
	return cancelled
}

func (b *QuestionBroker) resolveRequest(requestID string, timedOut bool) {
	b.mu.Lock()
	delete(b.waiters, requestID)
	b.removeRequestLocked(requestID)
	dispatch := b.dispatch
	b.mu.Unlock()
	if dispatch != nil {
		dispatch(questionResolvedMsg{requestID: requestID, timedOut: timedOut})
	}
}

func dispatchQuestion(dispatch func(tea.Msg), req interaction.QuestionRequest) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("question dispatcher panicked: %v", recovered)
		}
	}()
	dispatch(questionRequestMsg{request: req})
	return nil
}

// Respond handles a response from the UI.
func (b *QuestionBroker) Respond(requestID string, resp interaction.QuestionResponse) error {
	b.mu.Lock()
	waiter, ok := b.waiters[requestID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("unknown question request: %s", requestID)
	}
	delete(b.waiters, requestID)
	b.removeRequestLocked(requestID)
	b.mu.Unlock()

	select {
	case waiter.respCh <- resp:
	default:
	}

	return nil
}

// GetPendingRequests returns all pending question requests.
func (b *QuestionBroker) GetPendingRequests() []interaction.QuestionRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]interaction.QuestionRequest, len(b.pending))
	copy(out, b.pending)
	return out
}

func (b *QuestionBroker) removeRequestLocked(requestID string) {
	for i, req := range b.pending {
		if req.ID == requestID {
			b.pending = append(b.pending[:i], b.pending[i+1:]...)
			break
		}
	}
}
