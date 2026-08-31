package silver

import (
	"time"
)

// DefaultSessionGap is the minimum time gap between events that triggers a
// new session boundary. If no events arrive for this duration, the next event
// starts a new session.
const DefaultSessionGap = 30 * time.Minute

// SessionDetectorConfig controls session boundary detection behavior.
type SessionDetectorConfig struct {
	// SessionGap is the minimum idle time between events to trigger a session split.
	// Defaults to DefaultSessionGap (30 minutes).
	SessionGap time.Duration

	// WindowDuration is the time window for grouping events within a session.
	// Defaults to DefaultWindowDuration (5 minutes).
	WindowDuration time.Duration
}

// DetectSessions identifies session boundaries within a chronologically sorted
// slice of bronze events. Sessions are split on:
//
//  1. Conversation ID changes — different conv_id means different session.
//  2. Time gaps — if more than SessionGap passes between events, start new session.
//  3. Agent stop events — agent.stopped signals session end.
//
// Each returned SessionBoundary includes pre-computed EventWindows.
func DetectSessions(events []BronzeEvent, cfg SessionDetectorConfig) []SessionBoundary {
	if len(events) == 0 {
		return nil
	}

	if cfg.SessionGap <= 0 {
		cfg.SessionGap = DefaultSessionGap
	}
	if cfg.WindowDuration <= 0 {
		cfg.WindowDuration = DefaultWindowDuration
	}

	var sessions []SessionBoundary
	var currentEvents []BronzeEvent
	currentConvID := events[0].ConversationID

	flush := func() {
		if len(currentEvents) == 0 {
			return
		}
		session := buildSession(currentEvents, currentConvID, cfg.WindowDuration)
		sessions = append(sessions, session)
		currentEvents = nil
	}

	for i, evt := range events {
		// Split condition 1: conversation ID change.
		if evt.ConversationID != "" && evt.ConversationID != currentConvID {
			flush()
			currentConvID = evt.ConversationID
		}

		// Split condition 2: time gap exceeds threshold.
		if i > 0 && evt.Timestamp.Sub(events[i-1].Timestamp) > cfg.SessionGap {
			flush()
			if evt.ConversationID != "" {
				currentConvID = evt.ConversationID
			}
		}

		currentEvents = append(currentEvents, evt)

		// Split condition 3: agent.stopped event signals session end.
		// Include this event in the current session, then flush.
		if evt.Type == "agent.stopped" {
			flush()
			// Next event (if any) will start a new session.
			if i+1 < len(events) && events[i+1].ConversationID != "" {
				currentConvID = events[i+1].ConversationID
			}
		}
	}

	// Flush remaining events.
	flush()

	return sessions
}

// buildSession constructs a SessionBoundary from a slice of events belonging
// to one session.
func buildSession(events []BronzeEvent, convID string, windowDuration time.Duration) SessionBoundary {
	if len(events) == 0 {
		return SessionBoundary{}
	}

	// Use the most common non-empty conversation ID if the provided one is empty.
	if convID == "" {
		convID = detectDominantConvID(events)
	}

	session := SessionBoundary{
		ConversationID: convID,
		StartTime:      events[0].Timestamp,
		EndTime:        events[len(events)-1].Timestamp,
		EventCount:     len(events),
		Windows:        WindowEvents(events, windowDuration),
	}

	return session
}

// detectDominantConvID finds the most common non-empty conversation ID
// in a slice of events.
func detectDominantConvID(events []BronzeEvent) string {
	counts := make(map[string]int)
	for _, evt := range events {
		if evt.ConversationID != "" {
			counts[evt.ConversationID]++
		}
	}

	bestID := ""
	bestCount := 0
	for id, count := range counts {
		if count > bestCount {
			bestID = id
			bestCount = count
		}
	}
	return bestID
}

// MergeSessions combines adjacent sessions that have the same conversation ID
// and are within the merge threshold of each other. This handles cases where
// agent.stopped fires mid-conversation (e.g., sub-agent completion) but the
// session logically continues.
func MergeSessions(sessions []SessionBoundary, mergeThreshold time.Duration) []SessionBoundary {
	if len(sessions) <= 1 {
		return sessions
	}
	if mergeThreshold <= 0 {
		mergeThreshold = 2 * time.Minute
	}

	var merged []SessionBoundary
	current := sessions[0]

	for i := 1; i < len(sessions); i++ {
		next := sessions[i]

		// Merge if same conversation and close in time.
		if next.ConversationID == current.ConversationID &&
			next.StartTime.Sub(current.EndTime) <= mergeThreshold {

			// Merge: extend current session.
			current.EndTime = next.EndTime
			current.EventCount += next.EventCount
			current.Windows = append(current.Windows, next.Windows...)
		} else {
			merged = append(merged, current)
			current = next
		}
	}
	merged = append(merged, current)

	return merged
}
