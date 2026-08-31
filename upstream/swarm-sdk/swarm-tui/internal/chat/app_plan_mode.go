package chat

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
)

// planModePersisterMu serialises persister registration so the most recent
// active conversation owns the callback, avoiding two persisters racing on
// rapid conversation switches.
var planModePersisterMu sync.Mutex

// syncPlanModeSnapshotForConv hydrates the plan-mode hook state from the
// conversation's persisted metadata (if any) and registers a persister that
// writes future state transitions back to the same conversation.
//
// This prevents the "must enter plan mode" prompt from re-firing every time
// the TUI reattaches to a session that has already entered plan mode.
//
// When convID is empty the persister is cleared and plan-mode state is reset
// so a fresh session does not inherit the previous conversation's flags.
func (a *App) syncPlanModeSnapshotForConv(convID string) {
	planModePersisterMu.Lock()
	defer planModePersisterMu.Unlock()

	if a == nil || a.sdk == nil || convID == "" {
		// No conversation context: clear persister and reset state. The next
		// active conversation will hydrate before anything else runs.
		builtin.SetPlanModePersister(nil)
		builtin.HydratePlanModeSnapshot(builtin.PlanModeSnapshot{})
		return
	}

	// Hydrate from the conversation's stored snapshot (if any).
	conv := a.sdk.GetConversation(context.Background(), convID)
	snap := readPlanModeSnapshot(conv)
	builtin.HydratePlanModeSnapshot(snap)
	logDebug("[plan-mode] hydrated convID=%s in_plan_mode=%v first_tool_used=%v ever_used=%v",
		convID, snap.InPlanMode, snap.FirstToolUsed, snap.EverUsed)

	// Register a persister scoped to this conversation. Use the SDK pointer
	// captured at registration time so a delayed callback never writes to a
	// nil reference after teardown.
	sdk := a.sdk
	target := convID
	builtin.SetPlanModePersister(func(s builtin.PlanModeSnapshot) {
		if sdk == nil {
			return
		}
		ctx := context.Background()
		c := sdk.GetConversation(ctx, target)
		if c == nil {
			return
		}
		if c.Metadata.Custom == nil {
			c.Metadata.Custom = make(map[string]any)
		}
		c.Metadata.Custom[builtin.PlanModeSnapshotKey] = encodePlanModeSnapshot(s)
		if err := sdk.UpdateConversationMetadata(ctx, target, &c.Metadata); err != nil {
			logDebug("[plan-mode] failed to persist snapshot for conv=%s: %v", target, err)
		}
	})
}

// readPlanModeSnapshot pulls a PlanModeSnapshot out of conversation metadata.
// Returns a zero-value snapshot when nothing has been written yet, when the
// conversation is missing, or when the stored value is malformed.
func readPlanModeSnapshot(conv *conversation.Conversation) builtin.PlanModeSnapshot {
	if conv == nil || conv.Metadata.Custom == nil {
		return builtin.PlanModeSnapshot{}
	}
	raw, ok := conv.Metadata.Custom[builtin.PlanModeSnapshotKey]
	if !ok {
		return builtin.PlanModeSnapshot{}
	}
	// Round-trip through JSON to handle both map[string]any (from disk) and
	// the typed struct (from in-memory reuse).
	buf, err := json.Marshal(raw)
	if err != nil {
		return builtin.PlanModeSnapshot{}
	}
	var snap builtin.PlanModeSnapshot
	if err := json.Unmarshal(buf, &snap); err != nil {
		return builtin.PlanModeSnapshot{}
	}
	return snap
}

// encodePlanModeSnapshot encodes the snapshot as a plain map so the value
// survives the JSON round-trip used by Storage.Save without requiring callers
// to register the type with any decoder.
func encodePlanModeSnapshot(s builtin.PlanModeSnapshot) map[string]any {
	out := map[string]any{
		"in_plan_mode":    s.InPlanMode,
		"first_tool_used": s.FirstToolUsed,
		"ever_used":       s.EverUsed,
	}
	if !s.LastEntryAt.IsZero() {
		out["last_entry_at"] = s.LastEntryAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	return out
}
