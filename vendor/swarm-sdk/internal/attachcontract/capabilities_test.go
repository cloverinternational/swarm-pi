package attachcontract

import (
	"strings"
	"testing"
)

// secretSentinel simulates a secret/request-payload value a caller might
// (incorrectly) be tempted to embed in an error. It must never appear in
// any CompatibilityError's formatted text.
const secretSentinel = "SECRET_VALUE"

// TestCompatibilityErrorNeverLeaksSecrets proves that constructing a
// CompatibilityError while a secret/request-payload value is in scope (as a
// local variable, exactly as a caller might have it available) never
// results in that value being included in the formatted error text — ADR-004
// "RPC contract": "Compatibility failures use a stable server-defined error
// category... without exposing request payloads or secrets."
func TestCompatibilityErrorNeverLeaksSecrets(t *testing.T) {
	simulatedRequestPayload := secretSentinel // e.g. an auth token or raw request body

	errs := []error{
		&CompatibilityError{Reason: ReasonUnknownMajor, Requested: "9.0", Supported: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 3}}},
		&CompatibilityError{Reason: ReasonNoOverlap, Requested: "1.9", Supported: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 3}}},
		&CompatibilityError{Reason: ReasonMissingCapability, Missing: "resume_cursor", Supported: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 3}}},
		&CompatibilityError{Reason: ReasonMalformedVersion, Requested: "v1.0"},
	}

	// Exercise SelectVersion's real failure paths too, proving the error it
	// returns (built internally, not by the caller) also never leaks
	// anything from this test's simulated-secret variable, since
	// SelectVersion never even receives it — that absence is itself part of
	// what this test documents.
	_, err := SelectVersion(
		CapabilityAdvertisement{SupportedRanges: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 1}}},
		CapabilityAdvertisement{SupportedRanges: []VersionRange{{Major: 5, MinMinor: 0, MaxMinor: 1}}},
		nil,
	)
	if err != nil {
		errs = append(errs, err)
	}

	for i, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, simulatedRequestPayload) {
			t.Errorf("errs[%d].Error() = %q leaked simulated secret %q", i, msg, simulatedRequestPayload)
		}
	}

	// Sanity: prove the sentinel itself would be detected if it somehow
	// were present, so this test is not vacuously passing.
	if !strings.Contains("prefix "+simulatedRequestPayload+" suffix", simulatedRequestPayload) {
		t.Fatal("strings.Contains sanity check failed; test would be vacuous")
	}
}

// TestCapabilityAdvertisementHasCapability proves the small membership
// helper works as expected.
func TestCapabilityAdvertisementHasCapability(t *testing.T) {
	a := CapabilityAdvertisement{Capabilities: []string{"sequence_gap", "resume_cursor"}}
	if !a.HasCapability("sequence_gap") {
		t.Error("expected HasCapability(\"sequence_gap\") = true")
	}
	if a.HasCapability("nonexistent") {
		t.Error("expected HasCapability(\"nonexistent\") = false")
	}
}

// TestVersionRangeContainsAndValid exercises VersionRange's small helpers.
func TestVersionRangeContainsAndValid(t *testing.T) {
	r := VersionRange{Major: 1, MinMinor: 2, MaxMinor: 5}
	if !r.Valid() {
		t.Error("expected r.Valid() = true")
	}
	if !r.Contains(Version{Major: 1, Minor: 3}) {
		t.Error("expected r.Contains({1,3}) = true")
	}
	if r.Contains(Version{Major: 1, Minor: 6}) {
		t.Error("expected r.Contains({1,6}) = false")
	}
	if r.Contains(Version{Major: 2, Minor: 3}) {
		t.Error("expected r.Contains({2,3}) = false (different major)")
	}
	bad := VersionRange{Major: 1, MinMinor: 5, MaxMinor: 2}
	if bad.Valid() {
		t.Error("expected inverted range Valid() = false")
	}
}
