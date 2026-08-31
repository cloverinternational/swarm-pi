package journal

import "testing"

// TestValidateFieldNameAcceptsAllowlist proves ValidateFieldName accepts
// every single name present in AllowedTaskFields -- a real round-trip over
// the allowlist itself, not a hand-picked subset, so a future edit to
// AllowedTaskFields that forgets to keep ValidateFieldName in sync would be
// caught immediately.
func TestValidateFieldNameAcceptsAllowlist(t *testing.T) {
	for name := range AllowedTaskFields {
		if err := ValidateFieldName(name); err != nil {
			t.Errorf("ValidateFieldName(%q) = %v, want nil (name is in AllowedTaskFields)", name, err)
		}
	}
}

// TestValidateFieldNameRejectsKnownExclusions proves ValidateFieldName
// rejects concrete field names ADR-006 explicitly excludes from the
// journal ("metadata, legacy notes, typed_notes, and embedded audit_events
// are not recursively copied into the journal") plus raw_prompt, which is
// separately excluded by ADR-006's "Exclusions" section ("raw user
// prompts ... MUST NOT" appear in the journal).
func TestValidateFieldNameRejectsKnownExclusions(t *testing.T) {
	rejected := []string{"metadata", "notes", "typed_notes", "audit_events", "raw_prompt"}
	for _, name := range rejected {
		if AllowedTaskFields[name] {
			t.Fatalf("test fixture error: %q must not be in AllowedTaskFields", name)
		}
		if err := ValidateFieldName(name); err == nil {
			t.Errorf("ValidateFieldName(%q) = nil, want an error (field is not allowlisted)", name)
		}
	}
}

// TestValidateFieldNameRejectsEmpty proves an empty field name is rejected
// even though it is trivially "not in the map" -- this guards against a
// hypothetical future change to AllowedTaskFields that accidentally treats
// a missing/empty key as present via Go's map zero-value semantics.
func TestValidateFieldNameRejectsEmpty(t *testing.T) {
	if err := ValidateFieldName(""); err == nil {
		t.Errorf("ValidateFieldName(\"\") = nil, want an error")
	}
}

// TestAllowedTaskFieldsExactSet locks the allowlist to ADR-006's exact
// field list, so an accidental addition or removal is caught by CI rather
// than silently drifting from the ADR text.
func TestAllowedTaskFieldsExactSet(t *testing.T) {
	want := []string{
		"subject", "description", "status", "priority", "category",
		"active_form", "active", "depends_on", "blocks", "created_at",
		"updated_at", "completed_at", "source_turn", "last_seen", "owner_id",
		"sequence", "parent_id", "sibling_index", "origin_prompt_id",
		"plan_id", "decomposed_by", "decomposition_attempts",
		"last_decomposition_error",
	}
	if len(AllowedTaskFields) != len(want) {
		t.Fatalf("AllowedTaskFields has %d entries, want %d", len(AllowedTaskFields), len(want))
	}
	for _, name := range want {
		if !AllowedTaskFields[name] {
			t.Errorf("AllowedTaskFields missing expected field %q", name)
		}
	}
}
