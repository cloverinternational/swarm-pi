package fallback

import (
	"context"
	"fmt"
	"time"
)

// ExecutionFunc represents a function that attempts to execute with a model.
// It should return nil on success, or an error to trigger the next fallback.
type ExecutionFunc func(ctx context.Context, provider, model string) error

// AttemptInfo records information about a single execution attempt.
type AttemptInfo struct {
	Provider  string
	Model     string
	Index     int // 0 = primary, 1+ = fallback index
	IsPrimary bool
	Duration  time.Duration
	Error     error
}

// Result captures the outcome of executing through a fallback chain.
type Result struct {
	Success      bool
	UsedProvider string
	UsedModel    string
	UsedIndex    int // 0 = primary succeeded, 1+ = fallback index that succeeded
	Attempts     []AttemptInfo
	FinalError   error
}

// UsedFallback returns true if a fallback was used (primary failed).
func (r Result) UsedFallback() bool {
	return r.Success && r.UsedIndex > 0
}

// AttemptCount returns the total number of attempts made.
func (r Result) AttemptCount() int {
	return len(r.Attempts)
}

// FailedAttempts returns the number of failed attempts.
func (r Result) FailedAttempts() int {
	count := 0
	for _, a := range r.Attempts {
		if a.Error != nil {
			count++
		}
	}
	return count
}

// NotificationMessage generates a user notification about fallback usage.
// Returns empty string if primary succeeded (no notification needed).
func (r Result) NotificationMessage() string {
	if !r.Success {
		if len(r.Attempts) == 0 {
			return "All models failed (no attempts made)"
		}
		return fmt.Sprintf("All %d model(s) failed. Last error: %v", len(r.Attempts), r.FinalError)
	}

	if r.UsedIndex == 0 {
		return "" // Primary succeeded, no notification needed
	}

	return fmt.Sprintf("Primary model failed, using fallback #%d (%s/%s) after %d failed attempt(s)",
		r.UsedIndex, r.UsedProvider, r.UsedModel, r.UsedIndex)
}

// StatusSummary returns a brief summary of the execution result.
func (r Result) StatusSummary() string {
	if !r.Success {
		return fmt.Sprintf("Failed after %d attempts", len(r.Attempts))
	}
	if r.UsedIndex == 0 {
		return "Success (primary model)"
	}
	return fmt.Sprintf("Success (fallback #%d)", r.UsedIndex)
}

// ProgressCallback is called during execution to report progress.
// Useful for updating UI with current attempt information.
type ProgressCallback func(attempt AttemptInfo)

// ExhaustedCallback is called when every entry in the chain has failed.
// It receives the Result of all failed attempts. If it returns a non-nil *Chain,
// Execute will try those entries before giving up permanently. If it returns nil,
// Execute returns the existing failure result immediately.
// This callback may block (e.g., waiting for a user prompt on a channel).
type ExhaustedCallback func(ctx context.Context, result Result) *Chain

// ExecuteOptions configures execution behavior.
type ExecuteOptions struct {
	// OnProgress is called before each attempt starts and after it completes.
	// Called with Error=nil before attempt, with actual error/nil after.
	OnProgress ProgressCallback

	// Timeout is the per-attempt timeout. Zero means no timeout.
	Timeout time.Duration

	// OnExhausted is called when every entry in the chain has failed.
	// If it returns a non-nil *Chain, those entries are tried once more
	// (without a further OnExhausted call, to prevent infinite loops).
	// Nil return means accept the failure and return immediately.
	OnExhausted ExhaustedCallback
}

// DefaultOptions returns sensible default execution options.
// No timeout is set by default to allow agents to run as long as needed.
// Timeouts should be explicitly set when needed, not enforced globally.
func DefaultOptions() ExecuteOptions {
	return ExecuteOptions{
		Timeout: 0, // No timeout by default - let agents run as long as needed
	}
}

// Execute tries each model in the chain until one succeeds.
// Returns a Result with details about all attempts made.
func Execute(ctx context.Context, chain *Chain, fn ExecutionFunc, opts ...ExecuteOptions) Result {
	result := Result{
		Attempts: make([]AttemptInfo, 0, chain.Len()),
	}

	if chain.IsEmpty() {
		result.FinalError = fmt.Errorf("fallback chain is empty")
		return result
	}

	// Merge options
	options := DefaultOptions()
	if len(opts) > 0 {
		options = opts[0]
	}

	for i, ref := range chain.All() {
		attempt := AttemptInfo{
			Provider:  ref.Provider,
			Model:     ref.Model,
			Index:     i,
			IsPrimary: i == 0,
		}

		// Notify progress: starting attempt
		if options.OnProgress != nil {
			options.OnProgress(attempt)
		}

		// Create timeout context if specified
		attemptCtx := ctx
		var cancel context.CancelFunc
		if options.Timeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, options.Timeout)
		}

		// Execute
		startTime := time.Now()
		err := fn(attemptCtx, ref.Provider, ref.Model)
		attempt.Duration = time.Since(startTime)

		if cancel != nil {
			cancel()
		}

		if err == nil {
			// Success!
			attempt.Error = nil
			result.Attempts = append(result.Attempts, attempt)
			result.Success = true
			result.UsedProvider = ref.Provider
			result.UsedModel = ref.Model
			result.UsedIndex = i

			// Notify progress: success
			if options.OnProgress != nil {
				options.OnProgress(attempt)
			}

			return result
		}

		// Failed - record and try next
		attempt.Error = err
		result.Attempts = append(result.Attempts, attempt)

		// Notify progress: failed
		if options.OnProgress != nil {
			options.OnProgress(attempt)
		}

		// Check if context was cancelled
		if ctx.Err() != nil {
			result.FinalError = fmt.Errorf("execution cancelled: %w", ctx.Err())
			return result
		}
	}

	// All attempts failed — give the caller a chance to extend the chain
	// (e.g. prompt the user to pick a different profile).
	// We pass no OnExhausted to the recursive call to prevent infinite loops.
	if options.OnExhausted != nil {
		if extended := options.OnExhausted(ctx, result); extended != nil {
			extended := Execute(ctx, extended, fn, ExecuteOptions{
				OnProgress: options.OnProgress,
				Timeout:    options.Timeout,
			})
			if extended.Success {
				return extended
			}
			// Merge the extended attempts into our result for a complete picture.
			result.Attempts = append(result.Attempts, extended.Attempts...)
			result.FinalError = extended.FinalError
			return result
		}
	}

	if len(result.Attempts) > 0 {
		lastAttempt := result.Attempts[len(result.Attempts)-1]
		result.FinalError = fmt.Errorf("all %d models failed, last error: %w", len(result.Attempts), lastAttempt.Error)
	} else {
		result.FinalError = fmt.Errorf("no models to try")
	}

	return result
}

// ExecuteWithResult is a helper that executes and returns both the execution result
// and any value produced by the execution function.
func ExecuteWithResult[T any](
	ctx context.Context,
	chain *Chain,
	fn func(ctx context.Context, provider, model string) (T, error),
	opts ...ExecuteOptions,
) (T, Result) {
	var value T

	execFn := func(ctx context.Context, provider, model string) error {
		var err error
		value, err = fn(ctx, provider, model)
		return err
	}

	result := Execute(ctx, chain, execFn, opts...)
	return value, result
}
