package builtin

import (
	"context"
	"fmt"
	"regexp"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// SecurityAuditHook detects and handles sensitive information in events.
type SecurityAuditHook struct {
	name     string
	priority int
	patterns map[string]*regexp.Regexp
	action   SecurityAction
}

// SecurityAction defines what to do when PII is detected.
type SecurityAction int

const (
	// SecurityActionBlock blocks the event.
	SecurityActionBlock SecurityAction = iota

	// SecurityActionRedact replaces PII with [REDACTED].
	SecurityActionRedact
)

// NewSecurityAuditHook creates a new security hook.
func NewSecurityAuditHook(action SecurityAction) *SecurityAuditHook {
	h := &SecurityAuditHook{
		name:     "security_audit",
		priority: 100, // High priority
		patterns: make(map[string]*regexp.Regexp),
		action:   action,
	}

	// Add default patterns
	// Note: These are simplified patterns for demonstration.
	h.patterns["email"] = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	h.patterns["ipv4"] = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	h.patterns["credit_card"] = regexp.MustCompile(`\b(?:\d{4}[- ]?){3}\d{4}\b`)

	return h
}

// Name returns the hook name.
func (h *SecurityAuditHook) Name() string {
	return h.name
}

// Priority returns the hook priority.
func (h *SecurityAuditHook) Priority() int {
	return h.priority
}

// Filter checks if the event should be processed.
func (h *SecurityAuditHook) Filter(event hooks.Event) bool {
	// Check message events and tool executions
	switch event.Type {
	case hooks.EventMessageBeforeSend, hooks.EventMessageAdded, hooks.EventToolBeforeExecute:
		return true
	default:
		return false
	}
}

// OnEvent processes the event.
func (h *SecurityAuditHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	content := ""

	// Extract content based on event type
	if event.Data != nil {
		if msg, ok := event.Data["content"].(string); ok {
			content = msg
		}
	}

	if content == "" {
		return hooks.Continue(), nil
	}

	// Check for PII
	for piiType, pattern := range h.patterns {
		if pattern.MatchString(content) {
			if h.action == SecurityActionBlock {
				return hooks.Block(fmt.Sprintf("Security violation: %s detected", piiType)), nil
			}

			// Redact
			redacted := pattern.ReplaceAllString(content, "[REDACTED]")

			// Clone and modify event
			newEvent := event.Clone()
			if newEvent.Data == nil {
				newEvent.Data = make(map[string]any)
			}
			newEvent.Data["content"] = redacted

			return hooks.ModifyWithMessage(newEvent, fmt.Sprintf("Redacted %s", piiType)), nil
		}
	}

	return hooks.Continue(), nil
}
