package mode

import "maps"

// Transition defines a rule for moving between agent groups.
//
// Transitions are evaluated after a group completes to determine
// which group to execute next. Multiple transitions can exist from
// the same source group, with the first matching transition used.
type Transition struct {
	// Source group ID
	From string

	// Target group ID
	To string

	// Condition expression that must be true for transition
	// Examples:
	// - "consensus_reached"
	// - "validation_failed"
	// - "confidence > 0.8"
	// - "retry_count < 3"
	Condition string

	// Maximum retries for this transition path
	// If exceeded, transition fails and workflow stops
	MaxRetries int

	// Priority for this transition (higher = evaluated first)
	// Useful when multiple transitions match
	Priority int

	// Additional metadata
	Metadata map[string]any
}

// NewTransition creates a new transition.
func NewTransition(from, to, condition string) *Transition {
	return &Transition{
		From:       from,
		To:         to,
		Condition:  condition,
		MaxRetries: 0, // No retries by default
		Priority:   0, // Default priority
		Metadata:   make(map[string]any),
	}
}

// Clone creates a deep copy of the transition.
func (t *Transition) Clone() *Transition {
	clone := &Transition{
		From:       t.From,
		To:         t.To,
		Condition:  t.Condition,
		MaxRetries: t.MaxRetries,
		Priority:   t.Priority,
		Metadata:   make(map[string]any),
	}

	maps.Copy(clone.Metadata, t.Metadata)

	return clone
}

// Validate validates the transition.
func (t *Transition) Validate() error {
	if t.From == "" {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "transition source (from) cannot be empty",
		}
	}

	if t.To == "" {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "transition target (to) cannot be empty",
		}
	}

	if t.Condition == "" {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "transition condition cannot be empty",
		}
	}

	if t.MaxRetries < 0 {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "max_retries cannot be negative",
		}
	}

	return nil
}
