package attachclient

import (
	"encoding/json"
	"errors"
	"testing"
)

// ── Contract negotiation matrix (compatible/incompatible combinations) ──

func TestNegotiate_CompatibleExactMatch(t *testing.T) {
	client := []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}}
	peer := Capabilities{Ranges: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}}}
	got, err := Negotiate(client, peer, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != (Version{Major: 1, Minor: 0}) {
		t.Fatalf("got %v, want 1.0", got)
	}
}

func TestNegotiate_SelectsHighestMutualMinor(t *testing.T) {
	client := []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 5}}
	peer := Capabilities{Ranges: []VersionRange{{Major: 1, MinMinor: 2, MaxMinor: 3}}}
	got, err := Negotiate(client, peer, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != (Version{Major: 1, Minor: 3}) {
		t.Fatalf("got %v, want 1.3 (highest mutually supported minor)", got)
	}
}

func TestNegotiate_MultipleClientRangesPicksBestAcrossThem(t *testing.T) {
	client := []VersionRange{
		{Major: 1, MinMinor: 0, MaxMinor: 2},
		{Major: 2, MinMinor: 0, MaxMinor: 1},
	}
	peer := Capabilities{Ranges: []VersionRange{
		{Major: 1, MinMinor: 0, MaxMinor: 2},
		{Major: 2, MinMinor: 0, MaxMinor: 1},
	}}
	got, err := Negotiate(client, peer, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != (Version{Major: 2, Minor: 1}) {
		t.Fatalf("got %v, want the higher major 2.1", got)
	}
}

func TestNegotiate_UnknownMajorRejected(t *testing.T) {
	client := []VersionRange{{Major: 2, MinMinor: 0, MaxMinor: 0}}
	peer := Capabilities{Ranges: []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 5}}}
	_, err := Negotiate(client, peer, nil)
	var ce *CompatibilityError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CompatibilityError, got %v (%T)", err, err)
	}
	if ce.Category != CompatibilityUnknownMajor {
		t.Fatalf("got category %v, want %v", ce.Category, CompatibilityUnknownMajor)
	}
}

func TestNegotiate_NoOverlapRejected(t *testing.T) {
	client := []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 1}}
	peer := Capabilities{Ranges: []VersionRange{{Major: 1, MinMinor: 5, MaxMinor: 9}}}
	_, err := Negotiate(client, peer, nil)
	var ce *CompatibilityError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CompatibilityError, got %v (%T)", err, err)
	}
	if ce.Category != CompatibilityNoOverlap {
		t.Fatalf("got category %v, want %v", ce.Category, CompatibilityNoOverlap)
	}
}

func TestNegotiate_MissingRequiredCapabilityRejected(t *testing.T) {
	client := []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}}
	peer := Capabilities{
		Ranges:       []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}},
		Capabilities: []string{"events.replay"},
	}
	_, err := Negotiate(client, peer, []string{"events.replay", "approvals"})
	var ce *CompatibilityError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CompatibilityError, got %v (%T)", err, err)
	}
	if ce.Category != CompatibilityMissingCapability {
		t.Fatalf("got category %v, want %v", ce.Category, CompatibilityMissingCapability)
	}
	if len(ce.Missing) != 1 || ce.Missing[0] != "approvals" {
		t.Fatalf("got missing %v, want [approvals]", ce.Missing)
	}
}

func TestNegotiate_AllRequiredCapabilitiesPresentSucceeds(t *testing.T) {
	client := []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}}
	peer := Capabilities{
		Ranges:       []VersionRange{{Major: 1, MinMinor: 0, MaxMinor: 0}},
		Capabilities: []string{"events.replay", "approvals"},
	}
	got, err := Negotiate(client, peer, []string{"events.replay", "approvals"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != (Version{Major: 1, Minor: 0}) {
		t.Fatalf("got %v, want 1.0", got)
	}
}

func TestNegotiate_EmptyClientRangesIsMalformed(t *testing.T) {
	_, err := Negotiate(nil, Capabilities{}, nil)
	var ce *CompatibilityError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CompatibilityError, got %v (%T)", err, err)
	}
	if ce.Category != CompatibilityMalformedVersion {
		t.Fatalf("got category %v, want %v", ce.Category, CompatibilityMalformedVersion)
	}
}

// ── Version JSON encoding (ADR-004: "major.minor is a JSON string, never a
// bare number") ─────────────────────────────────────────────────────────

func TestVersion_MarshalJSON_IsString(t *testing.T) {
	b, err := json.Marshal(Version{Major: 1, Minor: 10})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `"1.10"` {
		t.Fatalf("got %s, want \"1.10\" as a JSON string (never a bare number)", b)
	}
}

func TestVersion_UnmarshalJSON_RoundTrips(t *testing.T) {
	var v Version
	if err := json.Unmarshal([]byte(`"3.7"`), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v != (Version{Major: 3, Minor: 7}) {
		t.Fatalf("got %v, want 3.7", v)
	}
}

func TestVersion_UnmarshalJSON_RejectsBareNumber(t *testing.T) {
	var v Version
	err := json.Unmarshal([]byte(`1.10`), &v)
	if err == nil {
		t.Fatalf("expected error unmarshaling a bare JSON number into Version, got nil (1.10 must never be confused with a decimal number)")
	}
}

func TestParseVersion_MalformedRejected(t *testing.T) {
	cases := []string{"", "1", "1.", ".1", "a.b", "1.-2", "-1.2", "1.2.3"}
	for _, s := range cases {
		if _, err := ParseVersion(s); err == nil {
			t.Errorf("ParseVersion(%q): expected malformed-version error, got nil", s)
		} else {
			var ce *CompatibilityError
			if !errors.As(err, &ce) || ce.Category != CompatibilityMalformedVersion {
				t.Errorf("ParseVersion(%q): got %v, want CompatibilityMalformedVersion", s, err)
			}
		}
	}
}
