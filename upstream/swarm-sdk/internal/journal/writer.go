package journal

import "context"

// Writer is the narrow append interface for the execution journal.
// swarm-sdk/internal/journalstore.Store implements it (with
// `var _ journal.Writer = (*Store)(nil)` in that package); wiring call sites
// in swarm-sdk/internal/taskstore and swarm-sdk/internal/agent depend ONLY
// on this interface type, never on a concrete journalstore type, so
// taskstore/agent do not import journalstore's filesystem/fsync internals.
//
// This indirection is a deliberate Phase-05 fix for a field-naming
// divergence problem observed in Phase 04, where callers coupled directly
// to a concrete store type and its internal field names drifted out of sync
// with what callers expected. By fixing this interface's signatures here in
// the shared leaf package (see CONTRACT.md's "Shared type seam" and
// "Scope decision" sections for the full rationale) and requiring all
// producers/consumers to depend on Writer rather than journalstore.Store,
// a future rename or internal refactor of journalstore cannot silently
// break taskstore/agent call sites without also failing to satisfy this
// interface at compile time.
type Writer interface {
	// AppendTaskCreated durably appends a task.created record for the given
	// correlation identifiers and sanitized payload.
	AppendTaskCreated(ctx context.Context, corr Correlation, payload TaskCreatedPayload) error
	// AppendTaskFieldChanged durably appends a task.field_changed record.
	// Callers must invoke this once per changed field, in a stable
	// (alphabetical) field-name order, per ADR-006.
	AppendTaskFieldChanged(ctx context.Context, corr Correlation, payload TaskFieldChangedPayload) error
	// AppendTaskDeleted durably appends a task.deleted tombstone record.
	AppendTaskDeleted(ctx context.Context, corr Correlation, payload TaskDeletedPayload) error
}
