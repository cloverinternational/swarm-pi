package client

import (
	"errors"
	"fmt"
)

// ContractViolation indicates a usage contract was violated.
// This is a PROGRAMMING ERROR - the caller must fix their code.
//
// Contract violations are intentional enforcement mechanisms to ensure
// agents use the SDK correctly. They provide clear, actionable error messages
// that guide agents toward the correct usage pattern.
//
// Example:
//
//	var cv *ContractViolation
//	if errors.As(err, &cv) {
//	    // Handle contract violation - agent code needs fixing
//	    fmt.Printf("Contract violated: %s\n", cv.Violation)
//	    fmt.Printf("Required: %s\n", cv.Required)
//	    fmt.Printf("Hint: %s\n", cv.Hint)
//	}
type ContractViolation struct {
	// Violation describes what contract was violated.
	Violation string
	// Required describes what is contractually required.
	Required string
	// Hint provides guidance on how to fix the violation.
	Hint string
}

// Error implements the error interface with a structured format.
func (e *ContractViolation) Error() string {
	return fmt.Sprintf("CONTRACT VIOLATION: %s\nRequired: %s\nHint: %s",
		e.Violation, e.Required, e.Hint)
}

// IsContractViolation checks if an error is a contract violation.
// Use this to programmatically detect contract violations in error handling.
func IsContractViolation(err error) bool {
	var cv *ContractViolation
	return errors.As(err, &cv)
}

// Must is a helper that panics on error for initialization code.
// Use sparingly - prefer handling errors explicitly.
//
// Example:
//
//	client := Must(client.New(client.WithProvider("anthropic", "claude-sonnet-4")))
//
// This is useful for initialization code where errors are fatal and represent
// programming mistakes that should be caught early.
func Must[T any](val T, err error) T {
	if err != nil {
		panic(err)
	}
	return val
}
