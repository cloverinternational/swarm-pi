package analytics

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// Hook mirrors hook-pipeline events to the remote analytics dispatcher.
// It is intentionally low priority so earlier hooks can enrich event metadata.
type Hook struct {
	manager       *Manager
	workspacePath string
}

func NewHook(workspacePath string) *Hook {
	manager := DefaultManager()
	if manager == nil {
		return nil
	}
	return &Hook{manager: manager, workspacePath: workspacePath}
}

func (h *Hook) Name() string { return "remote-analytics" }

func (h *Hook) Priority() int { return 1 }

func (h *Hook) Filter(hooks.Event) bool { return h != nil && h.manager != nil }

func (h *Hook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	h.manager.CaptureHookEvent(event, h.workspacePath)
	return hooks.Continue(), nil
}
