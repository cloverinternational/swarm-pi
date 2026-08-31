package attachcontract

import (
	"errors"
	"testing"
)

// TestParseVersionMalformedRejected is the malformed-version rejection
// table: every listed invalid form must fail ParseVersion with a
// *CompatibilityError whose Reason is ReasonMalformedVersion.
func TestParseVersionMalformedRejected(t *testing.T) {
	cases := []string{
		"",        // empty string
		"1",       // bare major, no minor
		"1.0.0",   // three components
		"v1.0",    // "v"-prefixed
		"1.x",     // non-numeric minor
		"x.0",     // non-numeric major
		"1.",      // empty minor component
		".0",      // empty major component
		"1. 0",    // internal whitespace
		" 1.0",    // leading whitespace
		"1.0 ",    // trailing whitespace
		"-1.0",    // signed major
		"1.-1",    // signed minor
		"1.0.",    // trailing dot after valid pair
		"a.b",     // both non-numeric
		"1,0",     // wrong separator
		"1.0.0.0", // four components
	}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			v, err := ParseVersion(s)
			if err == nil {
				t.Fatalf("ParseVersion(%q) = %v, nil; want error", s, v)
			}
			var ce *CompatibilityError
			if !errors.As(err, &ce) {
				t.Fatalf("ParseVersion(%q) error type = %T, want *CompatibilityError", s, err)
			}
			if ce.Reason != ReasonMalformedVersion {
				t.Errorf("ParseVersion(%q) Reason = %q, want %q", s, ce.Reason, ReasonMalformedVersion)
			}
		})
	}
}

// TestParseVersionValid proves well-formed strings parse correctly,
// including two-digit and zero components.
func TestParseVersionValid(t *testing.T) {
	cases := map[string]Version{
		"1.0":   {Major: 1, Minor: 0},
		"1.10":  {Major: 1, Minor: 10}, // must never be confused with decimal 1.1
		"0.0":   {Major: 0, Minor: 0},
		"12.34": {Major: 12, Minor: 34},
	}
	for s, want := range cases {
		got, err := ParseVersion(s)
		if err != nil {
			t.Fatalf("ParseVersion(%q) unexpected error: %v", s, err)
		}
		if got != want {
			t.Errorf("ParseVersion(%q) = %+v, want %+v", s, got, want)
		}
		if got.String() != s {
			t.Errorf("Version(%+v).String() = %q, want %q", got, got.String(), s)
		}
	}
}

// TestVersionJSONIsAlwaysString proves Version always marshals as a JSON
// string, and that decoding a bare JSON number is rejected rather than
// silently reinterpreted (e.g. "1.10" must never be confused with the
// decimal 1.1).
func TestVersionJSONIsAlwaysString(t *testing.T) {
	v := Version{Major: 1, Minor: 10}
	b, err := v.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(b) != `"1.10"` {
		t.Errorf("MarshalJSON = %s, want %q", b, `"1.10"`)
	}

	var decoded Version
	if err := decoded.UnmarshalJSON([]byte(`1.1`)); err == nil {
		t.Errorf("UnmarshalJSON accepted a bare JSON number; want rejection")
	}
	if err := decoded.UnmarshalJSON([]byte(`"1.10"`)); err != nil {
		t.Fatalf("UnmarshalJSON(%q): %v", `"1.10"`, err)
	}
	if decoded != v {
		t.Errorf("decoded = %+v, want %+v", decoded, v)
	}
}

// TestSelectVersionCompatibleNewerMinorAccepted proves the highest mutually
// supported minor within a supported major is selected.
func TestSelectVersionCompatibleNewerMinorAccepted(t *testing.T) {
	local := CapabilityAdvertisement{SupportedRanges: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 5}}}
	remote := CapabilityAdvertisement{SupportedRanges: []VersionRange{{Major: 1, MinMinor: 2, MaxMinor: 8}}}
	got, err := SelectVersion(local, remote, nil)
	if err != nil {
		t.Fatalf("SelectVersion: %v", err)
	}
	want := Version{Major: 1, Minor: 5}
	if got != want {
		t.Errorf("SelectVersion = %+v, want %+v", got, want)
	}
}

// TestSelectVersionNoOverlap proves two advertisements sharing a known
// major but disjoint minor ranges fail with ReasonNoOverlap.
func TestSelectVersionNoOverlap(t *testing.T) {
	local := CapabilityAdvertisement{SupportedRanges: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 2}}}
	remote := CapabilityAdvertisement{SupportedRanges: []VersionRange{{Major: 1, MinMinor: 5, MaxMinor: 9}}}
	_, err := SelectVersion(local, remote, nil)
	var ce *CompatibilityError
	if !errors.As(err, &ce) {
		t.Fatalf("SelectVersion error type = %T, want *CompatibilityError", err)
	}
	if ce.Reason != ReasonNoOverlap {
		t.Errorf("Reason = %q, want %q", ce.Reason, ReasonNoOverlap)
	}
}

// TestSelectVersionUnknownMajor proves an advertisement with no shared
// major at all fails with ReasonUnknownMajor, distinct from ReasonNoOverlap.
func TestSelectVersionUnknownMajor(t *testing.T) {
	local := CapabilityAdvertisement{SupportedRanges: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 2}}}
	remote := CapabilityAdvertisement{SupportedRanges: []VersionRange{{Major: 9, MinMinor: 0, MaxMinor: 2}}}
	_, err := SelectVersion(local, remote, nil)
	var ce *CompatibilityError
	if !errors.As(err, &ce) {
		t.Fatalf("SelectVersion error type = %T, want *CompatibilityError", err)
	}
	if ce.Reason != ReasonUnknownMajor {
		t.Errorf("Reason = %q, want %q", ce.Reason, ReasonUnknownMajor)
	}
}

// TestSelectVersionMissingRequiredCapability proves a version that would
// otherwise be selectable still fails when the remote lacks a capability
// the local side requires.
func TestSelectVersionMissingRequiredCapability(t *testing.T) {
	local := CapabilityAdvertisement{SupportedRanges: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 5}}}
	remote := CapabilityAdvertisement{
		SupportedRanges: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 5}},
		Capabilities:    []string{"resume_cursor"},
	}
	_, err := SelectVersion(local, remote, []string{"sequence_gap"})
	var ce *CompatibilityError
	if !errors.As(err, &ce) {
		t.Fatalf("SelectVersion error type = %T, want *CompatibilityError", err)
	}
	if ce.Reason != ReasonMissingCapability {
		t.Errorf("Reason = %q, want %q", ce.Reason, ReasonMissingCapability)
	}
	if ce.Missing != "sequence_gap" {
		t.Errorf("Missing = %q, want %q", ce.Missing, "sequence_gap")
	}
}

// TestDecodeEnvelopeRejectsUnknownMajorBeforeVariantData proves
// DecodeEnvelope rejects an envelope declaring an unsupported major before
// it ever attempts to interpret the payload — a payload malformed enough
// to break DecodePayload's known-kind case must NOT surface as a payload
// decode error when the major itself is already rejected first.
func TestDecodeEnvelopeRejectsUnknownMajorBeforeVariantData(t *testing.T) {
	// kind "sequence_gap" is a known kind whose payload here is garbage
	// (a string where GapPayload fields are expected) — if the major check
	// did not run first, this would fail as a "malformed sequence_gap
	// payload" decode error instead of an unknown-major CompatibilityError.
	raw := `{"schema_version":"9.0","event_id":"e","stream_id":"s","sequence":1,"kind":"sequence_gap","at":"2026-08-08T12:00:00Z","payload":"not-an-object"}`
	_, err := DecodeEnvelope([]byte(raw), []int{1})
	var ce *CompatibilityError
	if !errors.As(err, &ce) {
		t.Fatalf("DecodeEnvelope error type = %T, want *CompatibilityError; err=%v", err, err)
	}
	if ce.Reason != ReasonUnknownMajor {
		t.Errorf("Reason = %q, want %q", ce.Reason, ReasonUnknownMajor)
	}
}

// TestDecodeEnvelopeCompatibleMajorDecodesNormally proves a supported major
// proceeds to normal decode, including a known kind's payload.
func TestDecodeEnvelopeCompatibleMajorDecodesNormally(t *testing.T) {
	raw := `{"schema_version":"1.0","event_id":"e","stream_id":"s","sequence":1,"kind":"sequence_gap","at":"2026-08-08T12:00:00Z","payload":{"stream_id":"s","requested_cursor":1,"oldest_available_cursor":9}}`
	env, err := DecodeEnvelope([]byte(raw), []int{1})
	if err != nil {
		t.Fatalf("DecodeEnvelope: %v", err)
	}
	if env.Kind != KindSequenceGap {
		t.Errorf("Kind = %q, want %q", env.Kind, KindSequenceGap)
	}
}

// TestDecodeEnvelopeMalformedVersion proves an envelope whose
// schema_version fails strict parsing is rejected via ParseVersion's
// CompatibilityError before major-set membership is even checked.
func TestDecodeEnvelopeMalformedVersion(t *testing.T) {
	raw := `{"schema_version":"v1.0","event_id":"e","stream_id":"s","sequence":1,"kind":"k","at":"2026-08-08T12:00:00Z"}`
	_, err := DecodeEnvelope([]byte(raw), []int{1})
	var ce *CompatibilityError
	if !errors.As(err, &ce) {
		t.Fatalf("DecodeEnvelope error type = %T, want *CompatibilityError", err)
	}
	if ce.Reason != ReasonMalformedVersion {
		t.Errorf("Reason = %q, want %q", ce.Reason, ReasonMalformedVersion)
	}
}
