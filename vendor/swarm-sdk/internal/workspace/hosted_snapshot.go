package workspace

import (
	"context"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
)

// SnapshotKind identifies the type of hosted snapshot operation.
type SnapshotKind string

const (
	SnapshotKindSnapshot   SnapshotKind = "snapshot"
	SnapshotKindCheckpoint SnapshotKind = "checkpoint"
)

// SnapshotExportFormat identifies the representation used when exporting snapshots.
type SnapshotExportFormat string

const (
	SnapshotExportFormatArchive    SnapshotExportFormat = "archive"
	SnapshotExportFormatDiff       SnapshotExportFormat = "diff"
	SnapshotExportFormatCheckpoint SnapshotExportFormat = "checkpoint"
)

// SnapshotCapableWorkspace is an additive hosted extension for workspaces that
// can create, clone, checkpoint, and export snapshots without changing the base
// Workspace contract.
type SnapshotCapableWorkspace interface {
	Workspace
	Snapshotter

	// CreateSnapshot creates a named snapshot or checkpoint.
	CreateSnapshot(ctx context.Context, request SnapshotRequest) (*SnapshotReference, error)

	// CloneSnapshot creates a new workspace derived from an existing snapshot.
	CloneSnapshot(ctx context.Context, request SnapshotCloneRequest) (Workspace, error)

	// ExportSnapshot exports a snapshot in an implementation-defined format.
	ExportSnapshot(ctx context.Context, request SnapshotExportRequest) (*SnapshotExport, error)
}

// SnapshotRequest describes a hosted snapshot creation request.
type SnapshotRequest struct {
	Name         string                    `json:"name,omitempty"`
	Kind         SnapshotKind              `json:"kind,omitempty"`
	Description  string                    `json:"description,omitempty"`
	BaseSnapshot string                    `json:"base_snapshot,omitempty"`
	Labels       map[string]string         `json:"labels,omitempty"`
	Execution    *hosted.ExecutionMetadata `json:"execution,omitempty"`
}

// SnapshotReference identifies a snapshot or checkpoint.
type SnapshotReference struct {
	ID           string            `json:"id,omitempty"`
	Name         string            `json:"name,omitempty"`
	Kind         SnapshotKind      `json:"kind,omitempty"`
	WorkspaceID  string            `json:"workspace_id,omitempty"`
	BaseSnapshot string            `json:"base_snapshot,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	Description  string            `json:"description,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}

// SnapshotCloneRequest describes a clone operation from an existing snapshot.
type SnapshotCloneRequest struct {
	SnapshotID string                    `json:"snapshot_id,omitempty"`
	Target     string                    `json:"target,omitempty"`
	ReadOnly   bool                      `json:"read_only,omitempty"`
	Execution  *hosted.ExecutionMetadata `json:"execution,omitempty"`
}

// SnapshotExportRequest describes an export from an existing snapshot.
type SnapshotExportRequest struct {
	SnapshotID    string                    `json:"snapshot_id,omitempty"`
	Format        SnapshotExportFormat      `json:"format,omitempty"`
	Target        string                    `json:"target,omitempty"`
	SinceSnapshot string                    `json:"since_snapshot,omitempty"`
	Execution     *hosted.ExecutionMetadata `json:"execution,omitempty"`
}

// SnapshotExport describes an exported snapshot artifact.
type SnapshotExport struct {
	Snapshot  SnapshotReference    `json:"snapshot"`
	Format    SnapshotExportFormat `json:"format"`
	URI       string               `json:"uri,omitempty"`
	CreatedAt time.Time            `json:"created_at"`
	Size      int64                `json:"size,omitempty"`
	Checksum  string               `json:"checksum,omitempty"`
	Metadata  map[string]string    `json:"metadata,omitempty"`
}
