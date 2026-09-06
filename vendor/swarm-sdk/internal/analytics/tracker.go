package analytics

// Tracker is the minimal interface that swarm-sdk consumers (e.g. cloudsync)
// require from an analytics back-end.
//
// Implementations must be safe for concurrent use from multiple goroutines.
// All methods are fire-and-forget: errors are handled internally (logged,
// buffered, or dropped) so that a tracking failure never breaks the caller's
// critical path.
type Tracker interface {
	// CaptureSyncOperation records the outcome of a cloud-sync operation.
	//  operation — a stable identifier for the operation type (e.g. "push_conversation").
	//  status    — "success", "failure", or "warning".
	//  details   — arbitrary key/value metadata attached to the event.
	CaptureSyncOperation(operation, status string, details map[string]any)

	// Close flushes any buffered events and releases resources.
	// It is safe to call Close more than once; subsequent calls are no-ops.
	Close() error
}

// NoopTracker is a Tracker that discards all events.  It is the zero-value
// default for SDK components that do not yet have an analytics back-end
// injected, keeping those code paths free of nil-pointer guards.
type NoopTracker struct{}

var _ Tracker = NoopTracker{}

// CaptureSyncOperation discards the event.
func (NoopTracker) CaptureSyncOperation(_, _ string, _ map[string]any) {}

// Close is a no-op.
func (NoopTracker) Close() error { return nil }
