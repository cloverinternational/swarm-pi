package visual

import (
	"encoding/json"
	"time"

	sdkvisual "github.com/Swarm-Code/mono/swarm-sdk/internal/interaction/visual"
)

type ScreenKind string

const (
	ScreenKindPending       ScreenKind = "pending"
	ScreenKindInformational ScreenKind = "informational"
	ScreenKindPlan          ScreenKind = "plan"
	ScreenKindResolved      ScreenKind = "resolved"
	ScreenKindTimedOut      ScreenKind = "timed_out"
	ScreenKindCancelled     ScreenKind = "cancelled"
	ScreenKindAcknowledged  ScreenKind = "acknowledged"
)

func (k ScreenKind) IsTerminal() bool {
	switch k {
	case ScreenKindResolved, ScreenKindTimedOut, ScreenKindCancelled, ScreenKindAcknowledged:
		return true
	default:
		return false
	}
}

type PlanState string

const (
	PlanStateProposed  PlanState = "proposed"
	PlanStateApproved  PlanState = "approved"
	PlanStateRejected  PlanState = "rejected"
	PlanStateAbandoned PlanState = "abandoned"
)

type Screen struct {
	ID             string                    `json:"id"`
	ParentScreenID string                    `json:"parentScreenID,omitempty"`
	AgentID        string                    `json:"agentID,omitempty"`
	Kind           ScreenKind                `json:"kind"`
	Title          string                    `json:"title"`
	Description    string                    `json:"description,omitempty"`
	Primitive      sdkvisual.VisualPrimitive `json:"-"`
	PrimitiveJSON  json.RawMessage           `json:"primitive,omitempty"`
	CreatedAt      time.Time                 `json:"createdAt"`
	ResolvedAt     *time.Time                `json:"resolvedAt,omitempty"`
	Answer         []string                  `json:"answer,omitempty"`
	UsedDefault    bool                      `json:"usedDefault,omitempty"`
	PlanState      PlanState                 `json:"planState,omitempty"`
	CSRF           string                    `json:"-"`
	HTML           string                    `json:"-"`
}
