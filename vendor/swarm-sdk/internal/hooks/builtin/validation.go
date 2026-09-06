package builtin

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// DomainValidationHook enforces custom domain rules.
type DomainValidationHook struct {
	name      string
	priority  int
	validator ValidatorFunc
}

// ValidatorFunc is a function that validates an event.
// Returns an error if validation fails.
type ValidatorFunc func(ctx context.Context, event hooks.Event) error

// NewDomainValidationHook creates a new validation hook.
func NewDomainValidationHook(name string, validator ValidatorFunc) *DomainValidationHook {
	return &DomainValidationHook{
		name:      name,
		priority:  50, // Medium priority
		validator: validator,
	}
}

// Name returns the hook name.
func (h *DomainValidationHook) Name() string {
	return h.name
}

// Priority returns the hook priority.
func (h *DomainValidationHook) Priority() int {
	return h.priority
}

// Filter checks if the event should be processed.
func (h *DomainValidationHook) Filter(event hooks.Event) bool {
	// Validate all events by default, validator can filter internally
	return true
}

// OnEvent processes the event.
func (h *DomainValidationHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	if h.validator == nil {
		return hooks.Continue(), nil
	}

	if err := h.validator(ctx, event); err != nil {
		return hooks.Block(err.Error()), nil
	}

	return hooks.Continue(), nil
}
