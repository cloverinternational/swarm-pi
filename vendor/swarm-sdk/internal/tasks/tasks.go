// Package tasks defines the public, host-facing surface for the task
// provenance + decomposition system used by the Swarm SDK.
//
// The package is deliberately small. It exposes:
//
//   - PromptID / PlanID minting helpers — stable, content-derived IDs that
//     identify the user prompt or plan-mode plan that originated a piece of
//     work. These are used throughout the system to build an auditable
//     session tree (prompt → plan → task → action).
//
//   - WithDecomposing / DecomposingFrom — context helpers used to scope
//     re-entrant decomposition. When a TaskDecomposer creates child tasks,
//     those children should not themselves trigger decomposition; the
//     scoped context value lets the post-create hook detect that.
//
//   - DecomposedTask — the value type a TaskDecomposer returns. It carries
//     only the *content* of a child task (subject, description, category);
//     the store is responsible for IDs, timestamps, and tree linkage. This
//     keeps decomposer implementations free of store internals.
//
//   - TaskDecomposer — the interface a host (typically the TUI) implements
//     to produce child action subtasks for a parent task that meets the
//     host's complexity threshold.
//
// Design intent: the SDK owns the data model and the mechanism. The host
// (TUI, CLI, or alternate front-end) owns the *policy* — when to decompose,
// what the threshold is, which subagent runner to dispatch.
package tasks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

// PromptIDPrefixLength is the number of hex characters of the sha256 digest
// used as the prompt-id suffix. 12 hex chars = 48 bits, which is collision-
// safe within a single conversation. The full PromptID is scoped by the
// conversation id (see MintPromptID) to avoid cross-conversation collisions.
const PromptIDPrefixLength = 12

// MintPromptID returns a stable, content-derived identifier for a user
// prompt. The id is conversation-scoped: identical prompt text in two
// different conversations produces two different ids.
//
// Canonicalisation:
//   - Whitespace is trimmed from both ends.
//   - Content is normalised to Unicode NFC so visually identical text with
//     different decomposition forms produces the same id.
//
// Format: "<convID>:<sha256-12hex>". Empty content returns "" — callers
// should treat that as "no prompt id available" rather than minting an id
// that aliases every other empty prompt.
func MintPromptID(convID, content string) string {
	canon := canonicalisePromptContent(content)
	if canon == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(canon))
	return convID + ":" + hex.EncodeToString(digest[:])[:PromptIDPrefixLength]
}

// canonicalisePromptContent applies the canonicalisation rules used by
// MintPromptID. Exposed package-internally for tests.
func canonicalisePromptContent(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	return norm.NFC.String(trimmed)
}

// MintPlanID returns a fresh, time-sortable plan identifier. We use uuid v7
// so plan ids minted earlier in a session sort lexicographically before
// later ones — useful when rendering the PlanIDHistory in the TUI.
//
// If uuid v7 generation fails for any reason (extremely unlikely in
// practice), MintPlanID falls back to uuid v4 so callers never receive an
// empty string.
func MintPlanID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

// decomposingKey is the unexported key used to stash the parent task id
// onto a context.Context while a TaskDecomposer is producing subtasks.
type decomposingKey struct{}

// WithDecomposing returns a derived context that carries parentTaskID as
// the active decomposition scope. Callers should pass this context to
// every TaskCreate invocation made on behalf of the decomposer; the
// post-create hook calls DecomposingFrom and skips re-decomposition when
// the value is set.
//
// An empty parentTaskID is a no-op: the input ctx is returned unchanged.
func WithDecomposing(ctx context.Context, parentTaskID string) context.Context {
	if parentTaskID == "" {
		return ctx
	}
	return context.WithValue(ctx, decomposingKey{}, parentTaskID)
}

// DecomposingFrom returns the parent task id stashed on ctx by
// WithDecomposing, or the empty string if the ctx is not currently inside a
// decomposition scope.
func DecomposingFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(decomposingKey{}).(string)
	return v
}

// DecomposedTask is the content payload a TaskDecomposer returns for each
// child task it wants the store to create. Identifiers, timestamps, and
// tree linkage (ParentID, OriginPromptID, PlanID, SiblingIndex,
// DecomposedBy) are filled in by the store when the subtask is persisted —
// implementations should leave those concerns alone.
type DecomposedTask struct {
	// Subject is the brief title in imperative form ("Run the migration").
	Subject string

	// Description is the detailed body for the subtask.
	Description string

	// Category is one of researching, planning, acting, verifying,
	// debugging, documenting. Empty defaults to "acting" at the store.
	Category string

	// ActiveForm is the present-continuous form shown in the spinner
	// ("Running the migration"). Optional.
	ActiveForm string
}

// ParentTask is the read-only view of the parent task that a
// TaskDecomposer receives. It is purposefully minimal so decomposer
// implementations stay decoupled from internal store types.
type ParentTask struct {
	ID             string
	Subject        string
	Description    string
	Category       string
	OriginPromptID string
	PlanID         string
}

// TaskDecomposer is implemented by a host that wants to produce child
// action subtasks for a parent task. The post-create hook calls Decompose
// at most once per parent (idempotency is enforced by the store via
// compare-and-swap on the parent's DecomposedBy field).
//
// Implementations should:
//   - Use ctx for cancellation; respect ctx.Done().
//   - Return between 2 and 6 atomic subtasks for non-trivial parents, or
//     an empty slice + nil error to signal "do not decompose this one".
//   - Surface real failures as an error; the store records the error on
//     the parent's LastDecompositionError field so the host can offer
//     retry to the user.
type TaskDecomposer interface {
	Decompose(ctx context.Context, parent ParentTask) ([]DecomposedTask, error)
}

// DecomposerFunc adapts an ordinary function to the TaskDecomposer
// interface, mirroring the common http.HandlerFunc pattern.
type DecomposerFunc func(ctx context.Context, parent ParentTask) ([]DecomposedTask, error)

// Decompose calls f.
func (f DecomposerFunc) Decompose(ctx context.Context, parent ParentTask) ([]DecomposedTask, error) {
	return f(ctx, parent)
}
