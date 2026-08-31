package interaction

import (
	"context"
	"errors"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
)

// ErrAsyncBrokerUnsupported is returned when a broker does not support the
// additive async hosted interaction surface.
var ErrAsyncBrokerUnsupported = errors.New("interaction async broker unsupported")

// AsyncBroker is an additive hosted interaction surface for durable approvals,
// questions, notifications, and out-of-band responses from re-attached clients.
type AsyncBroker interface {
	UserInteractionBroker

	// EnqueueQuestion records a question for asynchronous delivery.
	EnqueueQuestion(ctx context.Context, request QuestionRequest) (*hosted.InteractionHandle, error)

	// EnqueueApproval records an approval request for asynchronous delivery.
	EnqueueApproval(ctx context.Context, request ApprovalRequest) (*hosted.InteractionHandle, error)

	// EnqueueNotification records a notification for asynchronous delivery.
	EnqueueNotification(ctx context.Context, notification Notification) (*hosted.InteractionHandle, error)

	// RespondQuestion completes a pending hosted question.
	RespondQuestion(ctx context.Context, interactionID string, response QuestionResponse) error

	// RespondApproval completes a pending hosted approval.
	RespondApproval(ctx context.Context, interactionID string, response ApprovalResponse) error

	// PendingInteractions returns pending or recently delivered interaction handles.
	PendingInteractions(ctx context.Context) ([]hosted.InteractionHandle, error)
}

// HostedEnvelope wraps a question request in the hosted protocol envelope.
func (r QuestionRequest) HostedEnvelope(sequence int64) *hosted.EventEnvelope {
	payload := r
	payload.Hosted = nil
	envelope := hosted.NewEnvelope(hosted.EventTypeQuestionRequest, payload).WithSequence(sequence)
	envelope.Interaction = normalizeInteractionMetadata(r.Hosted, hosted.InteractionKindQuestion, hosted.InteractionStatusPending)
	return envelope
}

// HostedEnvelope wraps a question response in the hosted protocol envelope.
func (r QuestionResponse) HostedEnvelope(sequence int64) *hosted.EventEnvelope {
	payload := r
	payload.Hosted = nil
	envelope := hosted.NewEnvelope(hosted.EventTypeQuestionResponse, payload).WithSequence(sequence)
	envelope.Interaction = normalizeInteractionMetadata(r.Hosted, hosted.InteractionKindQuestion, hosted.InteractionStatusAnswered)
	return envelope
}

// HostedEnvelope wraps an approval request in the hosted protocol envelope.
func (r ApprovalRequest) HostedEnvelope(sequence int64) *hosted.EventEnvelope {
	payload := r
	payload.Hosted = nil
	envelope := hosted.NewEnvelope(hosted.EventTypeApprovalRequest, payload).WithSequence(sequence)
	envelope.Interaction = normalizeInteractionMetadata(r.Hosted, hosted.InteractionKindApproval, hosted.InteractionStatusPending)
	return envelope
}

// HostedEnvelope wraps an approval response in the hosted protocol envelope.
func (r ApprovalResponse) HostedEnvelope(sequence int64) *hosted.EventEnvelope {
	payload := r
	payload.Hosted = nil
	envelope := hosted.NewEnvelope(hosted.EventTypeApprovalResponse, payload).WithSequence(sequence)
	envelope.Interaction = normalizeInteractionMetadata(r.Hosted, hosted.InteractionKindApproval, hosted.InteractionStatusAnswered)
	return envelope
}

// HostedEnvelope wraps a notification in the hosted protocol envelope.
func (n Notification) HostedEnvelope(sequence int64) *hosted.EventEnvelope {
	payload := n
	payload.Hosted = nil
	envelope := hosted.NewEnvelope(hosted.EventTypeNotification, payload).WithSequence(sequence)
	envelope.Interaction = normalizeInteractionMetadata(n.Hosted, hosted.InteractionKindNotification, hosted.InteractionStatusDelivered)
	return envelope
}

func normalizeInteractionMetadata(
	metadata *hosted.InteractionMetadata,
	kind hosted.InteractionKind,
	status hosted.InteractionStatus,
) *hosted.InteractionMetadata {
	if metadata == nil {
		return &hosted.InteractionMetadata{
			Kind:   kind,
			Status: status,
		}
	}

	cloned := *metadata
	if cloned.Kind == "" {
		cloned.Kind = kind
	}
	if cloned.Status == "" {
		cloned.Status = status
	}

	return &cloned
}
