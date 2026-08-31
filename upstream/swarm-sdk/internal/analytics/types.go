package analytics

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

const SchemaVersion = 1

type EventEnvelope struct {
	SchemaVersion  int            `json:"schema_version"`
	EventID        string         `json:"event_id"`
	OccurredAt     time.Time      `json:"occurred_at"`
	IngestedAt     time.Time      `json:"ingested_at"`
	Source         string         `json:"source"`
	AppVersion     string         `json:"app_version"`
	MachineIDHash  string         `json:"machine_id_hash"`
	DeviceIDHash   string         `json:"device_id_hash,omitempty"`
	WorkspaceHash  string         `json:"workspace_hash,omitempty"`
	WorkspaceLabel string         `json:"workspace_label,omitempty"`
	ProjectHash    string         `json:"project_hash,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	TurnID         string         `json:"turn_id,omitempty"`
	EventType      string         `json:"event_type"`
	Payload        map[string]any `json:"payload,omitempty"`
	RedactionFlags []string       `json:"redaction_flags,omitempty"`
	ArchiveStatus  string         `json:"archive_status,omitempty"`
	ContentHash    string         `json:"content_hash,omitempty"`
}

type BatchRequest struct {
	SentAt time.Time       `json:"sent_at"`
	Events []EventEnvelope `json:"events"`
}

type BatchResponse struct {
	BatchID       string `json:"batch_id"`
	Accepted      int    `json:"accepted"`
	ArchiveStatus string `json:"archive_status"`
	ArtifactPath  string `json:"artifact_path"`
}

type ArtifactEnvelope struct {
	SchemaVersion  int            `json:"schema_version"`
	ArtifactID     string         `json:"artifact_id"`
	ArtifactType   string         `json:"artifact_type"`
	OccurredAt     time.Time      `json:"occurred_at"`
	IngestedAt     time.Time      `json:"ingested_at"`
	Source         string         `json:"source"`
	AppVersion     string         `json:"app_version"`
	MachineIDHash  string         `json:"machine_id_hash"`
	DeviceIDHash   string         `json:"device_id_hash,omitempty"`
	WorkspaceHash  string         `json:"workspace_hash,omitempty"`
	WorkspaceLabel string         `json:"workspace_label,omitempty"`
	ProjectHash    string         `json:"project_hash,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	ArtifactPath   string         `json:"artifact_path,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
	RedactionFlags []string       `json:"redaction_flags,omitempty"`
	ContentHash    string         `json:"content_hash,omitempty"`
}

type ArtifactBatchRequest struct {
	SentAt    time.Time          `json:"sent_at"`
	Artifacts []ArtifactEnvelope `json:"artifacts"`
}

func NewEventID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(buf[:])
}
