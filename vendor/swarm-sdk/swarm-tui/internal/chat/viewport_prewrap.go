package chat

import (
	tea "charm.land/bubbletea/v2"
)

// viewportPrewrappedMsg carries the result of a background line-wrapping pass.
// It is produced by prewrapViewportCmd and delivered to Update() by the Bubble
// Tea runtime, ensuring all writes to MessageList happen on the UI goroutine.
type viewportPrewrappedMsg struct {
	lines   []string
	mapping []int // raw line index -> cumulative wrapped line count (len == len(src)+1)
	width   int
	hash    uint64 // lastContentHash value captured at dispatch time
	request uint64 // dispatch generation; only the newest request owns in-flight state
}

// prewrapViewportCmd wraps all content lines in a background goroutine
// and returns a viewportPrewrappedMsg when done.
//
// Design notes:
//   - lines is a []string captured by value (slice header) from MessageList.lines.
//     The underlying array is immutable once SetContent replaces m.lines with a
//     new allocation, so the goroutine safely reads the old array.
//   - wrapLinesToWidth reuses the shared lineWrapCache, so most lines are
//     cache hits after the first pass or after content-only appends.
//   - hash is compared in SetPreWrappedLines; stale results are discarded.
//   - No join/split roundtrip: lines []string → wrapLinesToWidth → []string.
func prewrapViewportCmd(lines []string, width int, hash, request uint64) tea.Cmd {
	return func() tea.Msg {
		// Use the mapping-producing variant so the selection/mouse-mapping path
		// can convert a wrapped (visual) line index back to a raw line + segment.
		// This is what keeps ScreenToContent in lock-step with the rendered
		// pre-wrapped frame (which slices preWrappedLines by a WRAPPED YOffset).
		wrapped, mapping := wrapLinesToWidthWithMapping(lines, width)
		return viewportPrewrappedMsg{
			lines:   wrapped,
			mapping: mapping,
			width:   width,
			hash:    hash,
			request: request,
		}
	}
}

// nextViewportPrewrapCmd returns work for the current content/geometry target.
// An older request may still be running, but it must not prevent a replacement
// after a history load, resize, or content update changes that target.
func (a *App) nextViewportPrewrapCmd() tea.Cmd {
	if a == nil || a.msgViewport == nil || a.screen != ScreenChat {
		return nil
	}

	width := a.msgViewport.Width
	if !a.msgViewport.NeedsPrewrap(width) {
		return nil
	}

	lines, hash := a.msgViewport.LinesForPrewrap()
	if a.prewrapInFlight &&
		a.prewrapInFlightWidth == width &&
		a.prewrapInFlightHash == hash {
		return nil
	}

	a.prewrapRequestID++
	request := a.prewrapRequestID
	a.prewrapInFlight = true
	a.prewrapInFlightWidth = width
	a.prewrapInFlightHash = hash
	return prewrapViewportCmd(lines, width, hash, request)
}

// applyViewportPrewrap records a completed request. Only the newest request may
// populate the cache or clear in-flight state, so a slower stale completion
// cannot overwrite a newer usable cache. The return value reports whether the
// result matches the viewport as it exists now and is safe for scroll positioning.
func (a *App) applyViewportPrewrap(msg viewportPrewrappedMsg) bool {
	if a == nil || a.msgViewport == nil {
		return false
	}

	if msg.request != a.prewrapRequestID {
		return false
	}

	a.prewrapInFlight = false
	current := msg.hash == a.msgViewport.lastContentHash &&
		msg.width == a.msgViewport.Width
	if current {
		a.msgViewport.SetPreWrappedLines(msg.lines, msg.mapping, msg.width, msg.hash)
	}
	return current
}
