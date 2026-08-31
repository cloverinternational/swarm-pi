package interaction

import "errors"

// Outcome is a machine-readable terminal state for an interactive request.
type Outcome string

const (
	OutcomeResponse               Outcome = "response"
	OutcomeRejected               Outcome = "rejected"
	OutcomeTimeout                Outcome = "timeout"
	OutcomeInteractiveUnavailable Outcome = "interactive_unavailable"
	OutcomeDeliveryFailure        Outcome = "delivery_failure"
	OutcomeParentCanceled         Outcome = "parent_canceled"
)

// Error reports a non-user terminal outcome while preserving its cause.
type Error struct {
	Outcome Outcome
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return string(e.Outcome)
	}
	return string(e.Outcome) + ": " + e.Cause.Error()
}

func (e *Error) Unwrap() error { return e.Cause }

// NewError creates a categorized interaction error.
func NewError(outcome Outcome, cause error) error {
	return &Error{Outcome: outcome, Cause: cause}
}

// OutcomeOf returns err's categorized outcome, or an empty string.
func OutcomeOf(err error) Outcome {
	var interactionError *Error
	if errors.As(err, &interactionError) {
		return interactionError.Outcome
	}
	return ""
}
