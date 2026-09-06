package chat

import (
	"time"
)

// recordTokenChange logs a token count change event for profiling.
// It tracks when token counts change, what caused them, and detects "bounces"
// (unexpected decreases after increases that may indicate bugs).
func (a *App) recordTokenChange(prevCount, newCount int, source, context string) {
	// Skip if no actual change
	if prevCount == newCount {
		return
	}

	// Calculate delta
	delta := newCount - prevCount

	// Detect "bounce" - an unexpected decrease
	// A bounce is when we just increased the count and now it's decreasing
	isBounce := false
	if len(a.tokenChanges) > 0 {
		lastChange := a.tokenChanges[len(a.tokenChanges)-1]
		if lastChange.Delta > 0 && delta < 0 {
			// We just increased, now we're decreasing - that's a bounce
			isBounce = true
		}
	}

	// Create entry
	entry := TokenChangeEntry{
		Timestamp: time.Now(),
		PrevCount: prevCount,
		NewCount:  newCount,
		Delta:     delta,
		Source:    source,
		Context:   context,
		IsBounce:  isBounce,
	}

	// Keep last 10 changes
	if len(a.tokenChanges) >= 10 {
		a.tokenChanges = a.tokenChanges[1:]
	}
	a.tokenChanges = append(a.tokenChanges, entry)

	// Update last source
	a.lastTokenSource = source
	a.lastTokenChangeAt = entry.Timestamp

	// Log significant changes (especially bounces)
	if isBounce {
		logDebug("[TOKEN-PROFILING] BOUNCE DETECTED: %d -> %d (delta=%d, source=%s, context=%s)",
			prevCount, newCount, delta, source, context)
	} else if delta < -1000 || delta > 1000 {
		// Log large changes
		logDebug("[TOKEN-PROFILING] Large change: %d -> %d (delta=%d, source=%s)",
			prevCount, newCount, delta, source)
	}

	// Invalidate sidepanel cache when tokens change
	a.sidePanelCache.tokenCount = 0 // Force re-render
}

// absInt returns the absolute value of an integer
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
