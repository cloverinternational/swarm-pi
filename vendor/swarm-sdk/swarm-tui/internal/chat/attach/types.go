package attach

import (
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/state"
)

// Package attach is the shared contract for swarm-tui's Ctrl+Q "Attach & Monitor"
// screen. It defines Target, the unit both local sub-agents and remote peers are
// represented as, so the registry, live-peer-session, and UI workstreams can be
// developed in parallel against one fixed shape.
//
// THIS FILE'S CONTENT IS FIXED ACROSS ALL FOUR PARALLEL WORKSTREAMS — every
// workstream creates internal/chat/attach/types.go with EXACTLY this content
// (byte-for-byte, only this comment block may be trimmed) so that when the 4
// PRs merge into main sequentially, the only possible conflict is on this one
// file, and it is a pure identical-content add/add conflict: resolve by taking
// either side (they are identical) and moving on — do not redesign this type.
// TargetKind distinguishes an in-process sub-agent from a separate-process peer.
type TargetKind string

const (
	TargetKindSubAgent TargetKind = "subagent"
	TargetKindPeer     TargetKind = "peer"
)

// StateSnapshot is structured state exposed beside a rendered frame.
// Messages are already redacted by the automation inspector and are the
// transcript source; terminal frames are only an optional presentation path.
type StateSnapshot struct {
	Screen         string
	ConversationID string
	OperatingMode  string
	Workspace      string
	Branch         string
	Streaming      bool
	ActivityPhase  string
	ActivityLabel  string
	ActivityStatus string
	ActivityActive bool
	ModalID        string
	ModalOpen      bool
	Summary        string
	Preview        string
	Messages       []state.MessageState
	UpdatedAt      time.Time
	Stale          bool
}

type StateBadge string

const (
	StateBadgeRunning  StateBadge = "running"
	StateBadgeQuestion StateBadge = "question pending"
	StateBadgeApproval StateBadge = "approval pending"
	StateBadgePlan     StateBadge = "plan approval"
	StateBadgePlanning StateBadge = "planning"
	StateBadgeDone     StateBadge = "finished"
	StateBadgeFailed   StateBadge = "failed"
	StateBadgeStale    StateBadge = "stale"
	StateBadgeIdle     StateBadge = "idle"
)

// Badge classifies structured state for compact dashboard rendering.
func (s StateSnapshot) Badge(targetStatus string) StateBadge {
	modal := strings.ToLower(s.ModalID)
	switch {
	case strings.Contains(modal, "plan"):
		return StateBadgePlan
	case strings.Contains(modal, "question"):
		return StateBadgeQuestion
	case strings.Contains(modal, "approval") || strings.Contains(modal, "permission"):
		return StateBadgeApproval
	case strings.EqualFold(s.OperatingMode, "plan") && s.Streaming:
		return StateBadgePlanning
	case s.Stale:
		return StateBadgeStale
	case strings.EqualFold(targetStatus, "done"),
		strings.EqualFold(targetStatus, "completed"):
		return StateBadgeDone
	case strings.EqualFold(targetStatus, "failed"),
		strings.EqualFold(targetStatus, "cancelled"):
		return StateBadgeFailed
	case s.Streaming || targetLive(targetStatus):
		return StateBadgeRunning
	default:
		return StateBadgeIdle
	}
}

// Target is one attachable thing shown in the Ctrl+Q screen: either a local
// sub-agent (Task-tool delegate / background agent, running in-process in
// THIS swarm-tui process) or a swarm peer (a separate OS process, local or
// on the LAN, discovered via a2a.ListPeers).
type Target struct {
	Kind      TargetKind
	ID        string
	ParentID  string
	Name      string
	Status    string
	Steerable bool
	Machine   string

	// --- peer-only fields (zero value when Kind == TargetKindSubAgent) ---
	ControlSocket  string
	ServeURL       string
	Workspace      string
	Branch         string
	ConversationID string
	Progress       int
	Summary        string
	LastTool       string
	ToolCount      int

	// --- subagent-only fields (zero value when Kind == TargetKindPeer) ---
	AgentID  string
	Snapshot StateSnapshot
}

func targetLive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "streaming", "active", "busy", "online":
		return true
	default:
		return false
	}
}
