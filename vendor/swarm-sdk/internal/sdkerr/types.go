// Package error provides error types and taxonomy for the SDK.
// All errors in the SDK are categorized to enable intelligent retry logic,
// circuit breakers, and graceful degradation.
package sdkerr

// Category represents the classification of an error.
// This determines how the error should be handled (retry, fail fast, escalate, etc.)
type Category int

const (
	// CategoryTransient indicates a temporary failure that may succeed on retry.
	// Examples: network timeouts, rate limits, temporary service unavailability.
	// Recovery: Retry with exponential backoff.
	CategoryTransient Category = iota

	// CategoryPermanent indicates a failure that will not succeed on retry.
	// Examples: invalid API key, malformed request, unsupported operation.
	// Recovery: Fail fast, alert user, do not retry.
	CategoryPermanent

	// CategorySteering indicates a failure where steering should decide the action.
	// Examples: low quality response, consensus failure, context window exceeded.
	// Recovery: Delegate to steering system for decision.
	CategorySteering

	// CategoryCritical indicates a system-level failure requiring immediate attention.
	// Examples: data corruption, security breach, system panic.
	// Recovery: Escalate immediately, stop processing, alert operators.
	CategoryCritical
)

// String returns the string representation of the error category.
func (c Category) String() string {
	switch c {
	case CategoryTransient:
		return "transient"
	case CategoryPermanent:
		return "permanent"
	case CategorySteering:
		return "steering"
	case CategoryCritical:
		return "critical"
	default:
		return "unknown"
	}
}
