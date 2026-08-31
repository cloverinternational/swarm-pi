package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/interaction"
	sdkvisual "github.com/Swarm-Code/mono/swarm-sdk/internal/interaction/visual"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/visual"
)

// notificationMsg delivers a notification to the TUI.
type notificationMsg struct {
	level   string
	message string
}

// TUIInteractionBroker implements interaction.UserInteractionBroker by composing
// the existing PermissionsBroker (for approvals) and QuestionBroker (for questions).
type TUIInteractionBroker struct {
	questionBroker *QuestionBroker
	permBroker     *PermissionsBroker
	planBroker     *PlanBroker
	dispatch       func(tea.Msg)
	visualReg      *visual.Registry
	sessionID      string
}

func (b *TUIInteractionBroker) SetPlanBroker(pb *PlanBroker) { b.planBroker = pb }

func (b *TUIInteractionBroker) CurrentPlanID(ctx context.Context, agentID string) string {
	if b.planBroker == nil {
		return ""
	}
	return b.planBroker.CurrentPlanID(ctx, agentID)
}

// NewTUIInteractionBroker creates a new interaction broker for the TUI.
func NewTUIInteractionBroker(questionBroker *QuestionBroker, permBroker *PermissionsBroker) *TUIInteractionBroker {
	return &TUIInteractionBroker{
		questionBroker: questionBroker,
		permBroker:     permBroker,
	}
}

// SetDispatcher wires notification delivery to the Bubble Tea runtime.
func (b *TUIInteractionBroker) SetDispatcher(dispatch func(tea.Msg)) {
	b.dispatch = dispatch
}

// SetVisualRegistry wires a visual picker registry. Nil disables visual_choice.
func (b *TUIInteractionBroker) SetVisualRegistry(r *visual.Registry) { b.visualReg = r }

// SetSessionID records the session ID used when Ensure()ing the visual server.
func (b *TUIInteractionBroker) SetSessionID(id string) { b.sessionID = id }

// AskQuestion implements interaction.UserInteractionBroker.
func (b *TUIInteractionBroker) AskQuestion(ctx context.Context, req interaction.QuestionRequest) (interaction.QuestionResponse, error) {
	if b.questionBroker == nil {
		return interaction.QuestionResponse{Canceled: true},
			interaction.NewError(interaction.OutcomeInteractiveUnavailable, fmt.Errorf("question broker not configured"))
	}
	if req.Type == interaction.QuestionTypeVisualChoice && b.visualReg != nil {
		return b.askVisual(ctx, req)
	}
	return b.questionBroker.Request(ctx, req)
}

func (b *TUIInteractionBroker) askVisual(ctx context.Context, req interaction.QuestionRequest) (interaction.QuestionResponse, error) {
	primitive, ok := req.Visual.(sdkvisual.VisualPrimitive)
	if !ok {
		return interaction.QuestionResponse{}, fmt.Errorf("visual: unsupported payload %T", req.Visual)
	}
	choices, keys, multiselect, description, err := visualTUIChoices(primitive)
	if err != nil {
		return interaction.QuestionResponse{}, err
	}
	if description != "" {
		if req.Metadata.Description != "" {
			req.Metadata.Description += "\n\n"
		}
		req.Metadata.Description += description
	}
	req.Choices = choices
	if multiselect {
		req.Type = interaction.QuestionTypeMultiChoice
	} else {
		req.Type = interaction.QuestionTypeChoice
	}
	response, err := b.questionBroker.Request(ctx, req)
	if err != nil {
		return response, err
	}
	selected := visualChoiceKeys(response, choices, keys)
	response.Answer = ""
	response.Answers = nil
	if len(selected) == 1 {
		response.Answer = selected[0]
	} else {
		response.Answers = selected
	}
	return response, nil
}

func visualTUIChoices(primitive sdkvisual.VisualPrimitive) ([]string, []string, bool, string, error) {
	var choices []string
	var keys []string
	multiselect := false
	description := ""
	switch value := primitive.(type) {
	case sdkvisual.Options:
		multiselect = value.Multiselect
		for _, item := range value.Items {
			choices = append(choices, terminalChoiceText(item.Label, item.Description, nil))
			keys = append(keys, item.Key)
		}
	case sdkvisual.Cards:
		multiselect = value.Multiselect
		for _, item := range value.Items {
			choices = append(choices, terminalChoiceText(item.Title, item.Description, item.Body))
			keys = append(keys, item.Key)
		}
	case sdkvisual.SplitCompare:
		for _, item := range value.Items {
			choices = append(choices, terminalChoiceText(item.Label, "", item.Body))
			keys = append(keys, item.Key)
		}
	case sdkvisual.Markdown, sdkvisual.Mermaid, sdkvisual.RawHTML:
		choices = []string{"Continue"}
		keys = []string{"continue"}
		description = terminalPrimitiveText(primitive)
	default:
		return nil, nil, false, "", fmt.Errorf("visual: unsupported primitive %s", primitive.Kind())
	}
	if len(choices) == 0 {
		return nil, nil, false, "", fmt.Errorf("visual: choice primitive has no items")
	}
	return choices, keys, multiselect, description, nil
}

func terminalChoiceText(label, description string, body sdkvisual.VisualPrimitive) string {
	parts := []string{label}
	if description != "" {
		parts = append(parts, description)
	}
	if bodyText := terminalPrimitiveText(body); bodyText != "" {
		parts = append(parts, bodyText)
	}
	return strings.Join(parts, "\n")
}

func terminalPrimitiveText(primitive sdkvisual.VisualPrimitive) string {
	switch value := primitive.(type) {
	case nil:
		return ""
	case sdkvisual.Markdown:
		return value.Content
	case sdkvisual.Mermaid:
		if value.Caption == "" {
			return value.Diagram
		}
		return value.Caption + "\n" + value.Diagram
	case sdkvisual.RawHTML:
		return value.Content
	case sdkvisual.Options:
		lines := make([]string, 0, len(value.Items))
		for _, item := range value.Items {
			lines = append(lines, terminalChoiceText(item.Label, item.Description, nil))
		}
		return strings.Join(lines, "\n\n")
	case sdkvisual.Cards:
		lines := make([]string, 0, len(value.Items))
		for _, item := range value.Items {
			lines = append(lines, terminalChoiceText(item.Title, item.Description, item.Body))
		}
		return strings.Join(lines, "\n\n")
	case sdkvisual.SplitCompare:
		lines := make([]string, 0, len(value.Items))
		for _, item := range value.Items {
			lines = append(lines, terminalChoiceText(item.Label, "", item.Body))
		}
		return strings.Join(lines, "\n\n")
	default:
		return ""
	}
}

func visualChoiceKeys(response interaction.QuestionResponse, choices, keys []string) []string {
	answers := response.Answers
	if len(answers) == 0 && response.Answer != "" {
		answers = []string{response.Answer}
	}
	selected := make([]string, 0, len(answers))
	for _, answer := range answers {
		for index, choice := range choices {
			if answer == choice {
				selected = append(selected, keys[index])
				break
			}
		}
	}
	return selected
}

// AskApproval implements interaction.UserInteractionBroker.
func (b *TUIInteractionBroker) AskApproval(ctx context.Context, req interaction.ApprovalRequest) (interaction.ApprovalResponse, error) {
	// Convert interaction.ApprovalRequest → tools.PermissionApprovalRequest
	toolReq := tools.PermissionApprovalRequest{
		RequestID:  req.ID,
		Tool:       req.Tool,
		Permission: req.Permission,
		Target:     req.Target,
		Reason:     req.Reason,
		Timeout:    req.Timeout,
	}
	if req.Preview != nil {
		toolReq.Preview = &tools.ApprovalPreview{
			Type:    req.Preview.Type,
			Content: req.Preview.Content,
		}
	}
	if req.Context != nil {
		toolReq.Context = &tools.ApprovalContext{
			AgentID:         req.Context.AgentID,
			TaskDescription: req.Context.TaskDescription,
		}
		toolReq.ConversationID = req.Context.ConversationID
	}

	// Send through the existing permission broker
	toolResp, err := b.permBroker.Request(ctx, toolReq)
	if err != nil {
		return interaction.ApprovalResponse{Canceled: true}, err
	}

	// Convert tools.ApprovalResponse → interaction.ApprovalResponse
	resp := interaction.ApprovalResponse{
		RespondedAt: time.Now(),
	}

	switch toolResp.Decision {
	case tools.DecisionApproveOnce:
		resp.Decision = interaction.ApprovalApprove
		resp.Scope = interaction.ApprovalScopeOnce
	case tools.DecisionApproveSession:
		resp.Decision = interaction.ApprovalApprove
		resp.Scope = interaction.ApprovalScopeSession
	case tools.DecisionApproveAlways:
		resp.Decision = interaction.ApprovalApprove
		resp.Scope = interaction.ApprovalScopeAlways
	case tools.DecisionDeny, tools.DecisionDenyStop:
		resp.Decision = interaction.ApprovalDeny
	default:
		resp.Decision = interaction.ApprovalDeny
	}

	return resp, nil
}

// Notify implements interaction.UserInteractionBroker.
func (b *TUIInteractionBroker) Notify(ctx context.Context, notification interaction.Notification) error {
	if b.dispatch != nil {
		level := string(notification.Level)
		if level == "" {
			level = "info"
		}
		b.dispatch(notificationMsg{
			level:   level,
			message: notification.Message,
		})
	}
	return nil
}
