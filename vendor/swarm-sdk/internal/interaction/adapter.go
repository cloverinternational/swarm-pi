package interaction

import (
	"context"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// BrokerAdapter adapts a UserInteractionBroker to the legacy tools.ApprovalBroker interface.
// This allows the permission system to use the new generic interaction broker while
// maintaining backward compatibility with existing code.
// This is Ring 1 - implementation of adapter pattern.
type BrokerAdapter struct {
	broker UserInteractionBroker
	config tools.PermissionConfig
}

// NewBrokerAdapter creates a new broker adapter.
func NewBrokerAdapter(broker UserInteractionBroker) *BrokerAdapter {
	return &BrokerAdapter{
		broker: broker,
		config: tools.DefaultPermissionConfig(),
	}
}

// Request implements tools.ApprovalBroker by adapting UserInteractionBroker.AskApproval.
func (a *BrokerAdapter) Request(ctx context.Context, req tools.PermissionApprovalRequest) (tools.ApprovalResponse, error) {
	// Convert tools.PermissionApprovalRequest to ApprovalRequest
	approvalReq := ApprovalRequest{
		ID:         req.RequestID,
		Tool:       req.Tool,
		Permission: req.Permission,
		Target:     req.Target,
		Reason:     req.Reason,
		Timeout:    req.Timeout,
	}

	if req.Preview != nil {
		approvalReq.Preview = &ApprovalPreview{
			Type:    req.Preview.Type,
			Content: req.Preview.Content,
		}
	}

	if req.Context != nil {
		approvalReq.Context = &ApprovalContext{
			AgentID:         req.Context.AgentID,
			TaskDescription: req.Context.TaskDescription,
			ConversationID:  req.ConversationID,
		}
	}

	// Ask user via broker
	resp, err := a.broker.AskApproval(ctx, approvalReq)
	if err != nil {
		return tools.ApprovalResponse{}, err
	}

	// Convert ApprovalResponse back to tools.ApprovalResponse
	decision := tools.DecisionDeny
	if resp.Canceled {
		decision = tools.DecisionDeny
	} else if resp.Timeout {
		decision = tools.DecisionDeny
	} else {
		switch resp.Decision {
		case ApprovalApprove:
			switch resp.Scope {
			case ApprovalScopeOnce:
				decision = tools.DecisionApproveOnce
			case ApprovalScopeSession:
				decision = tools.DecisionApproveSession
			case ApprovalScopeAlways:
				decision = tools.DecisionApproveAlways
			default:
				decision = tools.DecisionApproveOnce
			}
		case ApprovalDeny:
			decision = tools.DecisionDeny
		case ApprovalCancel:
			decision = tools.DecisionDeny
		}
	}

	return tools.ApprovalResponse{
		Decision: decision,
	}, nil
}

// Respond implements tools.ApprovalBroker (no-op for synchronous adapter).
func (a *BrokerAdapter) Respond(requestID string, decision tools.Decision) error {
	// Not needed for synchronous broker - user already responded in Request() call
	return nil
}

// SetConfig implements tools.ApprovalBroker.
func (a *BrokerAdapter) SetConfig(config tools.PermissionConfig) {
	a.config = config
}

// GetPendingRequests implements tools.ApprovalBroker (no-op for synchronous adapter).
func (a *BrokerAdapter) GetPendingRequests() []tools.PermissionApprovalRequest {
	// Not needed for synchronous broker
	return nil
}

// AskQuestion adapts the UserInteractionBroker.AskQuestion for direct use.
func (a *BrokerAdapter) AskQuestion(ctx context.Context, req QuestionRequest) (QuestionResponse, error) {
	return a.broker.AskQuestion(ctx, req)
}

// AskApproval adapts the UserInteractionBroker.AskApproval for direct use.
func (a *BrokerAdapter) AskApproval(ctx context.Context, req ApprovalRequest) (ApprovalResponse, error) {
	return a.broker.AskApproval(ctx, req)
}

// Notify adapts the UserInteractionBroker.Notify for direct use.
func (a *BrokerAdapter) Notify(ctx context.Context, notification Notification) error {
	return a.broker.Notify(ctx, notification)
}

// MockBroker is a simple mock implementation for testing.
// This is useful for testing without requiring a full TUI.
type MockBroker struct {
	// PendingQuestions stores questions that have been asked.
	PendingQuestions []QuestionRequest

	// PendingApprovals stores approvals that have been requested.
	PendingApprovals []ApprovalRequest

	// PendingNotifications stores notifications that were sent.
	PendingNotifications []Notification

	// AsyncInteractions stores hosted interaction handles.
	AsyncInteractions []hosted.InteractionHandle

	// QuestionResponses maps question ID to response.
	QuestionResponses map[string]QuestionResponse

	// ApprovalResponses maps approval ID to response.
	ApprovalResponses map[string]ApprovalResponse

	// DefaultQuestionResponse is used when no specific response is configured.
	DefaultQuestionResponse QuestionResponse

	// DefaultApprovalResponse is used when no specific response is configured.
	DefaultApprovalResponse ApprovalResponse
}

// NewMockBroker creates a new mock broker.
func NewMockBroker() *MockBroker {
	return &MockBroker{
		PendingQuestions:     make([]QuestionRequest, 0),
		PendingApprovals:     make([]ApprovalRequest, 0),
		PendingNotifications: make([]Notification, 0),
		AsyncInteractions:    make([]hosted.InteractionHandle, 0),
		QuestionResponses:    make(map[string]QuestionResponse),
		ApprovalResponses:    make(map[string]ApprovalResponse),
		DefaultQuestionResponse: QuestionResponse{
			Answer:      "default answer",
			Confirmed:   true,
			Canceled:    false,
			Timeout:     false,
			RespondedAt: time.Now(),
		},
		DefaultApprovalResponse: ApprovalResponse{
			Decision:    ApprovalApprove,
			Scope:       ApprovalScopeOnce,
			Canceled:    false,
			Timeout:     false,
			RespondedAt: time.Now(),
		},
	}
}

// AskQuestion implements UserInteractionBroker.
func (m *MockBroker) AskQuestion(ctx context.Context, request QuestionRequest) (QuestionResponse, error) {
	m.PendingQuestions = append(m.PendingQuestions, request)

	if resp, ok := m.QuestionResponses[request.ID]; ok {
		return resp, nil
	}

	return m.DefaultQuestionResponse, nil
}

// AskApproval implements UserInteractionBroker.
func (m *MockBroker) AskApproval(ctx context.Context, request ApprovalRequest) (ApprovalResponse, error) {
	m.PendingApprovals = append(m.PendingApprovals, request)

	if resp, ok := m.ApprovalResponses[request.ID]; ok {
		return resp, nil
	}

	return m.DefaultApprovalResponse, nil
}

// Notify implements UserInteractionBroker.
func (m *MockBroker) Notify(ctx context.Context, notification Notification) error {
	m.PendingNotifications = append(m.PendingNotifications, notification)
	return nil
}

// SetQuestionResponse configures the response for a specific question.
func (m *MockBroker) SetQuestionResponse(questionID string, response QuestionResponse) {
	m.QuestionResponses[questionID] = response
}

// SetApprovalResponse configures the response for a specific approval.
func (m *MockBroker) SetApprovalResponse(approvalID string, response ApprovalResponse) {
	m.ApprovalResponses[approvalID] = response
}

// GetQuestionsAsked returns all questions that were asked.
func (m *MockBroker) GetQuestionsAsked() []QuestionRequest {
	return m.PendingQuestions
}

// GetApprovalsRequested returns all approvals that were requested.
func (m *MockBroker) GetApprovalsRequested() []ApprovalRequest {
	return m.PendingApprovals
}

// GetNotificationsSent returns all notifications that were sent.
func (m *MockBroker) GetNotificationsSent() []Notification {
	return m.PendingNotifications
}

// AssertQuestionAsked asserts that a question with matching criteria was asked.
func (m *MockBroker) AssertQuestionAsked(questionType QuestionType) error {
	for _, q := range m.PendingQuestions {
		if q.Type == questionType {
			return nil
		}
	}
	return fmt.Errorf("no question of type %s was asked", questionType)
}

// AssertApprovalRequested asserts that an approval for a specific tool was requested.
func (m *MockBroker) AssertApprovalRequested(tool string) error {
	for _, a := range m.PendingApprovals {
		if a.Tool == tool {
			return nil
		}
	}
	return fmt.Errorf("no approval for tool %s was requested", tool)
}

// EnqueueQuestion implements AsyncBroker.
func (m *MockBroker) EnqueueQuestion(ctx context.Context, request QuestionRequest) (*hosted.InteractionHandle, error) {
	m.PendingQuestions = append(m.PendingQuestions, request)
	handle := newInteractionHandle(hosted.InteractionKindQuestion, request.ID, request.Hosted, len(m.AsyncInteractions)+1)
	m.AsyncInteractions = append(m.AsyncInteractions, *handle)
	return handle, nil
}

// EnqueueApproval implements AsyncBroker.
func (m *MockBroker) EnqueueApproval(ctx context.Context, request ApprovalRequest) (*hosted.InteractionHandle, error) {
	m.PendingApprovals = append(m.PendingApprovals, request)
	handle := newInteractionHandle(hosted.InteractionKindApproval, request.ID, request.Hosted, len(m.AsyncInteractions)+1)
	m.AsyncInteractions = append(m.AsyncInteractions, *handle)
	return handle, nil
}

// EnqueueNotification implements AsyncBroker.
func (m *MockBroker) EnqueueNotification(ctx context.Context, notification Notification) (*hosted.InteractionHandle, error) {
	m.PendingNotifications = append(m.PendingNotifications, notification)
	handle := newInteractionHandle(hosted.InteractionKindNotification, notification.ID, notification.Hosted, len(m.AsyncInteractions)+1)
	m.AsyncInteractions = append(m.AsyncInteractions, *handle)
	return handle, nil
}

// RespondQuestion implements AsyncBroker.
func (m *MockBroker) RespondQuestion(ctx context.Context, interactionID string, response QuestionResponse) error {
	m.QuestionResponses[interactionID] = response
	return nil
}

// RespondApproval implements AsyncBroker.
func (m *MockBroker) RespondApproval(ctx context.Context, interactionID string, response ApprovalResponse) error {
	m.ApprovalResponses[interactionID] = response
	return nil
}

// PendingInteractions implements AsyncBroker.
func (m *MockBroker) PendingInteractions(ctx context.Context) ([]hosted.InteractionHandle, error) {
	if len(m.AsyncInteractions) == 0 {
		return nil, nil
	}

	handles := make([]hosted.InteractionHandle, len(m.AsyncInteractions))
	copy(handles, m.AsyncInteractions)
	return handles, nil
}

func newInteractionHandle(
	kind hosted.InteractionKind,
	id string,
	metadata *hosted.InteractionMetadata,
	sequence int,
) *hosted.InteractionHandle {
	handle := &hosted.InteractionHandle{
		ID:       id,
		Kind:     kind,
		Status:   defaultInteractionStatus(kind),
		Sequence: int64(sequence),
	}

	if handle.ID == "" {
		handle.ID = fmt.Sprintf("%s-%d", kind, sequence)
	}
	handle.Cursor = &hosted.ReplayCursor{
		Value:    fmt.Sprintf("%d", sequence),
		Sequence: int64(sequence),
	}

	if metadata == nil {
		return handle
	}

	if metadata.ID != "" {
		handle.ID = metadata.ID
	}
	if metadata.Kind != "" {
		handle.Kind = metadata.Kind
	}
	if metadata.Status != "" {
		handle.Status = metadata.Status
	}
	handle.SessionID = metadata.SessionID
	if metadata.Cursor != nil {
		handle.Cursor = metadata.Cursor
	}
	handle.ResumeToken = metadata.ResumeToken
	handle.ExpiresAt = metadata.ExpiresAt

	return handle
}

func defaultInteractionStatus(kind hosted.InteractionKind) hosted.InteractionStatus {
	if kind == hosted.InteractionKindNotification {
		return hosted.InteractionStatusDelivered
	}
	return hosted.InteractionStatusPending
}
