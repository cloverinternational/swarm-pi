package approval

import (
	"context"
	"errors"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ToolsApprovalBroker adapts the headless approval broker to the SDK interface.
type ToolsApprovalBroker struct {
	broker *ApprovalBroker
	config tools.PermissionConfig
}

// NewToolsApprovalBroker wraps a headless approval broker.
func NewToolsApprovalBroker(broker *ApprovalBroker) *ToolsApprovalBroker {
	return &ToolsApprovalBroker{broker: broker}
}

// Request sends approval request to the underlying headless broker.
func (b *ToolsApprovalBroker) Request(ctx context.Context, req tools.PermissionApprovalRequest) (tools.ApprovalResponse, error) {
	if b == nil || b.broker == nil {
		return tools.ApprovalResponse{Outcome: tools.OutcomeDenied}, errors.New("approval broker not configured")
	}

	approvalReq := PermissionRequest{
		RequestID:      req.RequestID,
		ConversationID: req.ConversationID,
		Tool:           req.Tool,
		Permission:     req.Permission,
		Target:         req.Target,
		Reason:         req.Reason,
		Timeout:        req.Timeout,
		BatchID:        req.BatchID,
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
		}
	}

	resp, err := b.broker.Request(ctx, approvalReq)
	return tools.ApprovalResponse{
		Decision:     tools.Decision(resp.Decision),
		Outcome:      tools.Outcome(resp.Outcome),
		GrantedScope: resp.GrantedScope,
	}, err
}

// Respond forwards a decision to the underlying broker.
func (b *ToolsApprovalBroker) Respond(requestID string, decision tools.Decision) error {
	if b == nil || b.broker == nil {
		return errors.New("approval broker not configured")
	}
	return b.broker.Respond(requestID, Decision(decision))
}

// SetConfig updates broker configuration based on SDK permission settings.
func (b *ToolsApprovalBroker) SetConfig(config tools.PermissionConfig) {
	if b == nil || b.broker == nil {
		return
	}
	b.config = config

	brokerCfg := DefaultConfig()
	if config.TimeoutSeconds > 0 {
		brokerCfg.Timeout = config.TimeoutSeconds
	}
	if config.TimeoutBehavior != "" {
		brokerCfg.TimeoutBehavior = config.TimeoutBehavior
	}

	b.broker.SetConfig(brokerCfg)
}

// GetPendingRequests returns pending approval requests in SDK format.
func (b *ToolsApprovalBroker) GetPendingRequests() []tools.PermissionApprovalRequest {
	if b == nil || b.broker == nil {
		return nil
	}

	pending := b.broker.GetPendingRequests()
	out := make([]tools.PermissionApprovalRequest, 0, len(pending))
	for _, req := range pending {
		converted := tools.PermissionApprovalRequest{
			RequestID:      req.RequestID,
			ConversationID: req.ConversationID,
			Tool:           req.Tool,
			Permission:     req.Permission,
			Target:         req.Target,
			Reason:         req.Reason,
			Timeout:        req.Timeout,
			BatchID:        req.BatchID,
		}
		if req.Preview != nil {
			converted.Preview = &tools.ApprovalPreview{
				Type:    req.Preview.Type,
				Content: req.Preview.Content,
			}
		}
		if req.Context != nil {
			converted.Context = &tools.ApprovalContext{
				AgentID:         req.Context.AgentID,
				TaskDescription: req.Context.TaskDescription,
			}
		}
		out = append(out, converted)
	}
	return out
}
