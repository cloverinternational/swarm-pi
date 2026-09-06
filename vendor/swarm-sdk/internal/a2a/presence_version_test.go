package a2a

import "encoding/json"

import "testing"

// TestPresenceSchemaVersionConstants pins the documented ordering and
// numeric identity of the exported schema-version constants so a future
// edit cannot silently renumber them (which would flip every legacy/current
// comparison in this package without a compile error).
func TestPresenceSchemaVersionConstants(t *testing.T) {
	if PresenceSchemaVersionLegacy != 0 {
		t.Fatalf("PresenceSchemaVersionLegacy = %d, want 0 (must equal the Go zero value)", PresenceSchemaVersionLegacy)
	}
	if PresenceSchemaVersionBuildProvenance <= PresenceSchemaVersionLegacy {
		t.Fatalf("PresenceSchemaVersionBuildProvenance = %d, want a value greater than legacy (%d)", PresenceSchemaVersionBuildProvenance, PresenceSchemaVersionLegacy)
	}
	if CurrentPresenceSchemaVersion != PresenceSchemaVersionBuildProvenance {
		t.Fatalf("CurrentPresenceSchemaVersion = %d, want %d (PresenceSchemaVersionBuildProvenance)", CurrentPresenceSchemaVersion, PresenceSchemaVersionBuildProvenance)
	}
}

// TestPresenceSchemaVersionRoundTripsThroughJSON proves a non-legacy
// PresenceSchemaVersion value survives a JSON marshal/unmarshal round trip
// unchanged, both as a bare value and embedded in PeerPresence's
// "schema_version" field.
func TestPresenceSchemaVersionRoundTripsThroughJSON(t *testing.T) {
	data, err := json.Marshal(CurrentPresenceSchemaVersion)
	if err != nil {
		t.Fatalf("marshal bare PresenceSchemaVersion: %v", err)
	}
	var bare PresenceSchemaVersion
	if err := json.Unmarshal(data, &bare); err != nil {
		t.Fatalf("unmarshal bare PresenceSchemaVersion: %v", err)
	}
	if bare != CurrentPresenceSchemaVersion {
		t.Fatalf("bare PresenceSchemaVersion round trip = %d, want %d", bare, CurrentPresenceSchemaVersion)
	}

	peer := PeerPresence{
		Handle:        "schema-roundtrip",
		Type:          PeerTypeLocal,
		SchemaVersion: CurrentPresenceSchemaVersion,
	}
	peerData, err := json.Marshal(peer)
	if err != nil {
		t.Fatalf("marshal PeerPresence with SchemaVersion: %v", err)
	}
	var decoded PeerPresence
	if err := json.Unmarshal(peerData, &decoded); err != nil {
		t.Fatalf("unmarshal PeerPresence with SchemaVersion: %v", err)
	}
	if decoded.SchemaVersion != CurrentPresenceSchemaVersion {
		t.Fatalf("PeerPresence.SchemaVersion round trip = %d, want %d", decoded.SchemaVersion, CurrentPresenceSchemaVersion)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(peerData, &raw); err != nil {
		t.Fatalf("decode raw keys: %v", err)
	}
	if _, ok := raw["schema_version"]; !ok {
		t.Fatalf("marshaled local PeerPresence missing schema_version key: %s", peerData)
	}
}

// TestPresenceSchemaVersionMissingFieldIsLegacyNotInvalid proves the core
// migration-compatibility contract: a presence record that predates schema
// versioning (no "schema_version" key at all) decodes successfully — never
// an unmarshal error — and the resulting PresenceSchemaVersion is the
// documented legacy default, not some sentinel "invalid" or "unknown" value.
func TestPresenceSchemaVersionMissingFieldIsLegacyNotInvalid(t *testing.T) {
	legacyRecord := []byte(`{"handle":"legacy-peer","type":"local","status":"active"}`)

	var peer PeerPresence
	if err := json.Unmarshal(legacyRecord, &peer); err != nil {
		t.Fatalf("legacy record without schema_version failed to decode: %v", err)
	}
	if !peer.SchemaVersion.IsLegacy() {
		t.Fatalf("PeerPresence.SchemaVersion = %d for a record missing schema_version, want IsLegacy() true", peer.SchemaVersion)
	}
	if peer.SchemaVersion != PresenceSchemaVersionLegacy {
		t.Fatalf("PeerPresence.SchemaVersion = %d, want PresenceSchemaVersionLegacy (%d)", peer.SchemaVersion, PresenceSchemaVersionLegacy)
	}

	// A record that explicitly claims version 0 must be treated identically
	// to one that omits the key entirely — legacy either way.
	explicitZero := []byte(`{"handle":"legacy-peer-explicit","type":"local","status":"active","schema_version":0}`)
	var explicit PeerPresence
	if err := json.Unmarshal(explicitZero, &explicit); err != nil {
		t.Fatalf("explicit schema_version:0 record failed to decode: %v", err)
	}
	if !explicit.SchemaVersion.IsLegacy() {
		t.Fatalf("explicit schema_version:0 did not decode as legacy: %d", explicit.SchemaVersion)
	}
}

// TestPresenceSchemaVersionIsLegacyAndKnownOrLegacy exercises the predicate
// helpers directly across the legacy/current/future boundary.
func TestPresenceSchemaVersionIsLegacyAndKnownOrLegacy(t *testing.T) {
	future := CurrentPresenceSchemaVersion + 1
	cases := []struct {
		name       string
		version    PresenceSchemaVersion
		wantLegacy bool
		wantKnown  bool
	}{
		{"legacy", PresenceSchemaVersionLegacy, true, true},
		{"current", CurrentPresenceSchemaVersion, false, true},
		{"future-unknown", future, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.version.IsLegacy(); got != tc.wantLegacy {
				t.Fatalf("IsLegacy() = %v, want %v", got, tc.wantLegacy)
			}
			if got := tc.version.KnownOrLegacy(); got != tc.wantKnown {
				t.Fatalf("KnownOrLegacy() = %v, want %v", got, tc.wantKnown)
			}
		})
	}
}

// TestNormalizePresenceSchemaVersionClampsNegative proves a hostile/corrupt
// negative schema_version (never legitimately produced by this package)
// normalizes to the legacy default rather than being trusted as some
// "below legacy" sentinel that could bypass a KnownOrLegacy check.
func TestNormalizePresenceSchemaVersionClampsNegative(t *testing.T) {
	if got := NormalizePresenceSchemaVersion(-7); got != PresenceSchemaVersionLegacy {
		t.Fatalf("NormalizePresenceSchemaVersion(-7) = %d, want PresenceSchemaVersionLegacy (%d)", got, PresenceSchemaVersionLegacy)
	}
	if got := NormalizePresenceSchemaVersion(CurrentPresenceSchemaVersion); got != CurrentPresenceSchemaVersion {
		t.Fatalf("NormalizePresenceSchemaVersion(current) = %d, want unchanged %d", got, CurrentPresenceSchemaVersion)
	}
}
