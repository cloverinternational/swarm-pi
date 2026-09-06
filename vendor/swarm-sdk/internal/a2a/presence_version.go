package a2a

// PresenceSchemaVersion identifies the on-disk/wire shape of a PeerPresence
// record. It exists so a reader can distinguish a record written before
// schema versioning existed (every field present today, but no explicit
// version marker) from a record written by a build that knows about this
// versioning scheme and about BuildProvenance (build_provenance.go).
//
// Presence continues to report discoverable facts; a schema version alone
// never decides lifecycle. It only tells a reader how to interpret the
// record's other fields (in particular: whether BuildProvenance is expected
// to be populated, and therefore whether it is safe to treat BuildProvenance
// as authoritative over the legacy BinaryModTime heuristic for a freshness
// decision).
type PresenceSchemaVersion int

const (
	// PresenceSchemaVersionLegacy is the implicit version carried by every
	// presence record written before this field existed. Go's zero value for
	// PresenceSchemaVersion is deliberately equal to this constant: a record
	// decoded from disk (or from any other JSON boundary in this package)
	// whose "schema_version" key is absent therefore decodes as legacy
	// automatically, never as an error or as an unrecognized/invalid future
	// version. Legacy records may still carry BinaryModTime; they never carry
	// a populated, trustworthy BuildProvenance.
	PresenceSchemaVersionLegacy PresenceSchemaVersion = 0

	// PresenceSchemaVersionBuildProvenance is the schema introduced by
	// modernization-provenance.md's Phase 02 ("Version/split presence;
	// replace mtime heuristics with build provenance"). Records at this
	// version or later carry a populated BuildProvenance field that is
	// authoritative for same-build/different-build decisions; BinaryModTime
	// may still be present for backward-compatible reads during migration,
	// but it is no longer the sole or authoritative staleness signal for any
	// new decision built on top of this version.
	PresenceSchemaVersionBuildProvenance PresenceSchemaVersion = 1

	// CurrentPresenceSchemaVersion is the schema version this build writes
	// for every new or rewritten presence record. It is always the newest
	// defined PresenceSchemaVersion constant.
	CurrentPresenceSchemaVersion = PresenceSchemaVersionBuildProvenance
)

// IsLegacy reports whether v is the implicit pre-versioning schema — either
// because a record's "schema_version" key was absent at decode time (Go's
// zero value for an int-backed type already equals
// PresenceSchemaVersionLegacy) or because it was explicitly written as 0.
// Callers use this instead of a raw "== 0" comparison so the legacy
// definition stays in exactly one place.
func (v PresenceSchemaVersion) IsLegacy() bool {
	return v <= PresenceSchemaVersionLegacy
}

// KnownOrLegacy reports whether v is a value this build understands: either
// the documented legacy default or a defined, non-negative schema version at
// or below CurrentPresenceSchemaVersion. A record claiming a schema version
// newer than this build knows about is neither invalid nor silently
// coerced — callers that need to reject/ignore an unrecognized future
// version can use this predicate rather than trusting fields that a newer
// schema might have redefined.
func (v PresenceSchemaVersion) KnownOrLegacy() bool {
	return v >= PresenceSchemaVersionLegacy && v <= CurrentPresenceSchemaVersion
}

// NormalizePresenceSchemaVersion maps a decoded PresenceSchemaVersion to its
// documented, unambiguous form: any negative value (never legitimately
// produced by this package, but possible from a hostile or corrupt writer)
// collapses to the legacy default rather than underflowing comparisons or
// being trusted as a "version below legacy" sentinel.
func NormalizePresenceSchemaVersion(v PresenceSchemaVersion) PresenceSchemaVersion {
	if v < PresenceSchemaVersionLegacy {
		return PresenceSchemaVersionLegacy
	}
	return v
}
