package compaction

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/browser"
)

// CleanupBrowserProcesses terminates all active browser processes before compaction.
// This should be called at the start of compaction to prevent phantom processes
// and ensure a clean state for context serialization.
func CleanupBrowserProcesses(ctx context.Context) error {
	registry := browser.Global()
	active := registry.GetActive()

	if len(active) == 0 {
		return nil
	}

	// Log what we're cleaning up
	for _, p := range active {
		fmt.Printf("[compaction] Cleaning up browser session: %s (PID %d, tool: %s)\n",
			p.SessionID, p.PID, p.ToolType)
	}

	// Terminate all browser processes
	if err := registry.CleanupAll(); err != nil {
		return fmt.Errorf("browser cleanup failed: %w", err)
	}

	return nil
}
