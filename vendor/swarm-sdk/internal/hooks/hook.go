package hooks

import "context"

// Hook defines the interface for event hooks.
// Hooks intercept events and can observe, block, or modify them.
type Hook interface {
	// OnEvent is called when an event matching the filter occurs.
	// Returns a HookResult indicating what action to take.
	OnEvent(ctx context.Context, event Event) (HookResult, error)

	// Filter returns true if this hook should process the given event.
	// This allows hooks to selectively handle specific event types.
	Filter(event Event) bool

	// Priority returns the hook's priority (0-100, higher runs first).
	// Hooks with the same priority run in registration order.
	Priority() int

	// Name returns a unique identifier for this hook.
	Name() string
}

// HookResult indicates what action to take after a hook processes an event.
type HookResult struct {
	// Action determines how to proceed.
	Action HookAction

	// ModifiedEvent contains the modified event (only used if Action=Modify).
	ModifiedEvent *Event

	// Message provides context about the decision (for logging/debugging).
	Message string

	// Metadata contains additional information about the result.
	Metadata map[string]any
}

// HookAction represents the action a hook wants to take.
type HookAction int

const (
	// ActionContinue allows the event to proceed to the next hook.
	ActionContinue HookAction = iota

	// ActionBlock stops event processing immediately.
	// The event will not be processed further.
	ActionBlock

	// ActionModify replaces the event with a modified version.
	// The modified event proceeds to the next hook.
	ActionModify
)

// String returns the string representation of the action.
func (a HookAction) String() string {
	switch a {
	case ActionContinue:
		return "continue"
	case ActionBlock:
		return "block"
	case ActionModify:
		return "modify"
	default:
		return "unknown"
	}
}

// Continue creates a HookResult that allows processing to continue.
func Continue() HookResult {
	return HookResult{Action: ActionContinue}
}

// ContinueWithMessage creates a HookResult that continues with a message.
func ContinueWithMessage(message string) HookResult {
	return HookResult{
		Action:  ActionContinue,
		Message: message,
	}
}

// Block creates a HookResult that stops processing.
func Block(reason string) HookResult {
	return HookResult{
		Action:  ActionBlock,
		Message: reason,
	}
}

// Modify creates a HookResult that modifies the event.
func Modify(modifiedEvent *Event) HookResult {
	return HookResult{
		Action:        ActionModify,
		ModifiedEvent: modifiedEvent,
	}
}

// ModifyWithMessage creates a HookResult that modifies the event with a message.
func ModifyWithMessage(modifiedEvent *Event, message string) HookResult {
	return HookResult{
		Action:        ActionModify,
		ModifiedEvent: modifiedEvent,
		Message:       message,
	}
}
