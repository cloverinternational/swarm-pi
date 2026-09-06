package journal

import "fmt"

// TaskCreatedPayload is the sanitized initial-values payload for task.created.
// Fields is keyed by journaled field name (see AllowedTaskFields) with
// already-redacted string values.
type TaskCreatedPayload struct {
	TaskID string            `json:"task_id"`
	Fields map[string]string `json:"fields"`
}

// TaskFieldChangedPayload contains exactly one field name and its sanitized
// new value, per ADR-006 ("Multi-field store updates produce one record per
// changed field in a stable field-name order").
type TaskFieldChangedPayload struct {
	TaskID              string  `json:"task_id"`
	FieldName           string  `json:"field_name"`
	NewValue            string  `json:"new_value"`
	PreviousValueDigest *string `json:"previous_value_digest,omitempty"`
}

// TaskDeletedPayload is a tombstone; Reason is a closed enum-like string
// (e.g. "user_deleted", "superseded", "hierarchy_pruned").
type TaskDeletedPayload struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason"`
}

// AllowedTaskFields is the closed allowlist of journaled task fields from
// ADR-006's "Journal records" section. Unknown fields are denied by default.
var AllowedTaskFields = map[string]bool{
	"subject": true, "description": true, "status": true, "priority": true,
	"category": true, "active_form": true, "active": true, "depends_on": true,
	"blocks": true, "created_at": true, "updated_at": true, "completed_at": true,
	"source_turn": true, "last_seen": true, "owner_id": true, "sequence": true,
	"parent_id": true, "sibling_index": true, "origin_prompt_id": true,
	"plan_id": true, "decomposed_by": true, "decomposition_attempts": true,
	"last_decomposition_error": true,
}

// ValidateFieldName reports an error unless name is a member of
// AllowedTaskFields, per ADR-006's "Unknown fields are denied by default."
// This is a purely structural allowlist-membership check; it never inspects
// or redacts a field's value -- that is journalredact's responsibility
// (P05.B), invoked by callers before a payload reaches journal.Writer.
func ValidateFieldName(name string) error {
	if name == "" {
		return fmt.Errorf("journal: field name must not be empty")
	}
	if !AllowedTaskFields[name] {
		return fmt.Errorf("journal: field %q is not in AllowedTaskFields (unknown fields are denied by default)", name)
	}
	return nil
}
