package builtin

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

const maxAnnoyanceFailureEntries = 1024

// AnnoyanceNudgeHook prompts the agent to report actionable product friction
// after a failed or degraded tool result. State is scoped by conversation and
// tool.
type AnnoyanceNudgeHook struct {
	mu          sync.Mutex
	lastFailure map[annoyanceFailureKey]string
}

type annoyanceFailureKey struct {
	conversation string
	tool         string
}

func NewAnnoyanceNudgeHook() *AnnoyanceNudgeHook {
	return &AnnoyanceNudgeHook{lastFailure: make(map[annoyanceFailureKey]string)}
}

func (h *AnnoyanceNudgeHook) Name() string { return "annoyance-nudge" }

func (h *AnnoyanceNudgeHook) Priority() int { return 20 }

func (h *AnnoyanceNudgeHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolAfterExecute ||
		event.Type == hooks.EventToolExecutionFailed
}

func (h *AnnoyanceNudgeHook) OnEvent(_ context.Context, event hooks.Event) (hooks.HookResult, error) {
	classification := hooks.ClassifyToolFriction(event)
	if hooks.NormalizeToolName(classification.ToolName) == "annoyed" {
		return hooks.Continue(), nil
	}
	key := annoyanceFailureKey{
		conversation: event.ConversationID,
		tool:         hooks.NormalizeToolName(classification.ToolName),
	}
	h.mu.Lock()
	if h.lastFailure == nil {
		h.lastFailure = make(map[annoyanceFailureKey]string)
	}
	if classification.CleanSuccess {
		delete(h.lastFailure, key)
		h.mu.Unlock()
		return hooks.Continue(), nil
	}
	if !classification.Friction {
		h.mu.Unlock()
		return hooks.Continue(), nil
	}
	if h.lastFailure[key] == classification.Fingerprint {
		h.mu.Unlock()
		return hooks.Continue(), nil
	}
	if len(h.lastFailure) >= maxAnnoyanceFailureEntries {
		clear(h.lastFailure)
	}
	h.lastFailure[key] = classification.Fingerprint
	h.mu.Unlock()

	message := fmt.Sprintf(
		`[ANNOYANCE REVIEW]
Tool: %q
Friction class: %q
Fingerprint: %q

Act as a demanding harness editor, not a complaint generator:
1. Decide whether this is a real product defect or inefficiency. Skip invalid input, expected test failures, permission denials, and user cancellations.
2. If actionable, call annoyed ONCE with category, observed behavior, expected behavior, bounded non-secret evidence, and objective acceptance tests.
3. The acceptance tests must include the exact reproduction shape plus one near-miss that must remain allowed. Do not publish vague frustration or duplicate this fingerprint.`,
		sanitizeAnnoyanceLabel(classification.ToolName),
		sanitizeAnnoyanceLabel(classification.Category),
		sanitizeAnnoyanceLabel(classification.Fingerprint),
	)
	return hooks.ContinueWithMessage(message), nil
}

func sanitizeAnnoyanceLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var sanitized strings.Builder
	sanitized.Grow(min(len(value), 64))
	separator := false
	for _, r := range value {
		if sanitized.Len() >= 64 {
			break
		}
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			sanitized.WriteRune(r)
			separator = false
		case r == '_', r == '-', r == '.':
			if sanitized.Len() > 0 && !separator {
				sanitized.WriteRune(r)
				separator = true
			}
		default:
			if sanitized.Len() > 0 && !separator {
				sanitized.WriteByte('-')
				separator = true
			}
		}
	}
	result := strings.Trim(sanitized.String(), "_.-")
	if result == "" {
		return "unknown"
	}
	return result
}
