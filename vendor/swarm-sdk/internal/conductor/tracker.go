package conductor

import (
	"context"
	"time"
)

// IssueTracker is the source-of-truth for work items.  Implementations wrap
// Forgejo/Gitea, GitHub, or Linear REST/GraphQL APIs.
type IssueTracker interface {
	// FetchCandidateIssues returns issues in active states that are eligible
	// for dispatch.  Implementations should apply any label/project filters
	// from WorkflowConfig.Tracker.
	FetchCandidateIssues(ctx context.Context) ([]Issue, error)

	// FetchIssuesByIDs returns current snapshots for a list of issue IDs.
	// Used by the orchestrator during reconciliation.
	FetchIssuesByIDs(ctx context.Context, ids []string) ([]Issue, error)

	// FetchTerminalIssueIDs returns IDs of issues currently in terminal states.
	// Used at startup to clean up stale workspaces.
	FetchTerminalIssueIDs(ctx context.Context) ([]string, error)

	// TransitionIssue moves an issue to the given state label.
	TransitionIssue(ctx context.Context, id, toState string) error

	// PostComment appends a comment to an issue.
	PostComment(ctx context.Context, id, body string) error
}

// Issue is the normalised issue record used by the orchestrator, prompt
// renderer, and observability surface.  It mirrors the Symphony SPEC §4.1.1
// Issue entity.
type Issue struct {
	// ID is the stable tracker-internal identifier (used as map key).
	ID string

	// Identifier is the human-readable ticket key, e.g. "SWRM-42".
	Identifier string

	// Title is the single-line issue title.
	Title string

	// Description is the optional Markdown body.
	Description string

	// State is the current tracker state name, e.g. "symphony:in-progress".
	State string

	// Labels lists all labels on the issue (normalised to lowercase).
	Labels []string

	// Priority: lower = higher priority for dispatch ordering.  Zero means
	// unset.
	Priority int

	// URL is the canonical web URL for the issue.
	URL string

	// BranchName is the tracker-suggested branch name when available.
	BranchName string

	// BlockedBy lists issue IDs that block this one.
	BlockedBy []BlockerRef

	CreatedAt time.Time
	UpdatedAt time.Time
}

// BlockerRef is a lightweight reference to a blocking issue.
type BlockerRef struct {
	ID         string
	Identifier string
	State      string
}

// ── Label-based state model ────────────────────────────────────────────────
// Both Forgejo and GitHub adapters map tracker state to labels on the issue.
// Teams are free to override these in WORKFLOW.md tracker.active_labels /
// tracker.terminal_labels, but the defaults below match the Symphony workflow.

const (
	LabelTodo        = "symphony:todo"
	LabelInProgress  = "symphony:in-progress"
	LabelHumanReview = "symphony:human-review"
	LabelMerging     = "symphony:merging"
	LabelDone        = "symphony:done"
	LabelCancelled   = "symphony:cancelled"
)

// DefaultActiveLabels is the default set of labels that mark an issue as
// eligible for dispatch (active state).
var DefaultActiveLabels = []string{
	LabelTodo,
	LabelInProgress,
	LabelHumanReview,
	LabelMerging,
}

// DefaultTerminalLabels is the default set of labels that mark an issue as
// terminal (no further work needed).
var DefaultTerminalLabels = []string{
	LabelDone,
	LabelCancelled,
}
