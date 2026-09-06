package attachcontract

import "fmt"

// VersionRange advertises one contiguous inclusive minor range
// [MinMinor, MaxMinor] supported for a single Major version. ADR-004
// "RPC contract": "A Swarm information/readiness method advertises
// supported RPC ranges and capabilities."
type VersionRange struct {
	Major    int `json:"major"`
	MinMinor int `json:"min_minor"`
	MaxMinor int `json:"max_minor"`
}

// Valid reports whether r describes a non-empty, non-negative range.
func (r VersionRange) Valid() bool {
	return r.Major >= 0 && r.MinMinor >= 0 && r.MaxMinor >= r.MinMinor
}

// Contains reports whether v falls within r.
func (r VersionRange) Contains(v Version) bool {
	return v.Major == r.Major && v.Minor >= r.MinMinor && v.Minor <= r.MaxMinor
}

// CapabilityAdvertisement is the DTO a Swarm information/readiness method
// returns: the version ranges it supports plus named optional-capability
// bits a caller may require before relying on additive behavior. ADR-004
// "Compatibility": a receiver "accepts a newer minor only when all required
// capabilities are understood."
type CapabilityAdvertisement struct {
	// SupportedRanges lists every major.minor range this endpoint accepts.
	// Most advertisements have exactly one entry per supported major.
	SupportedRanges []VersionRange `json:"supported_ranges"`
	// Capabilities is the set of named optional capability strings this
	// endpoint understands (e.g. "sequence_gap", "resume_cursor"). A caller
	// requiring one of these before relying on additive behavior checks it
	// with HasCapability or passes it to SelectVersion's requiredCapabilities.
	Capabilities []string `json:"capabilities,omitempty"`
}

// HasCapability reports whether a is advertising the named capability.
func (a CapabilityAdvertisement) HasCapability(name string) bool {
	for _, c := range a.Capabilities {
		if c == name {
			return true
		}
	}
	return false
}

// CompatibilityReason is a closed, stable set of server-defined error
// categories for version/capability negotiation failures. ADR-004 "RPC
// contract": "Compatibility failures use a stable server-defined error
// category and include supported ranges, without exposing request
// payloads or secrets." Values are wire-safe, fixed strings — never derived
// from caller-supplied request content.
type CompatibilityReason string

const (
	// ReasonUnknownMajor means the requested/remote major version is not
	// one this side supports at all. ADR-004 "Compatibility": "A receiver
	// rejects an unknown major before interpreting variant data."
	ReasonUnknownMajor CompatibilityReason = "unknown_major"
	// ReasonNoOverlap means both sides share a known major, but no minor
	// value is mutually supported within it.
	ReasonNoOverlap CompatibilityReason = "no_overlap"
	// ReasonMissingCapability means a version was otherwise selectable, but
	// the remote side does not advertise a capability the local side
	// requires before proceeding.
	ReasonMissingCapability CompatibilityReason = "missing_capability"
	// ReasonMalformedVersion means a version string failed strict
	// major.minor parsing (see version.go's ParseVersion).
	ReasonMalformedVersion CompatibilityReason = "malformed_version"
)

// CompatibilityError reports a version/capability negotiation failure using
// one of the CompatibilityReason categories above. Its Error() text is
// built ONLY from this struct's own typed fields (Reason, Requested — a
// version identifier, never a payload — Missing — a capability name, Supported
// — the advertised ranges): it never includes request payload content or
// secrets, satisfying ADR-004 "RPC contract" and "Security" ("avoids
// reflecting secret-bearing input in errors").
type CompatibilityError struct {
	// Reason is the closed-set error category.
	Reason CompatibilityReason
	// Supported is the set of ranges the erroring side advertised/accepts.
	Supported []VersionRange
	// Requested is the version identifier string that failed to negotiate
	// (e.g. "9.0"), when applicable. This is a version number, never
	// arbitrary request payload content.
	Requested string
	// Missing is the single required capability name that was absent, set
	// only when Reason == ReasonMissingCapability.
	Missing string
}

// Error implements the error interface using only this struct's typed
// fields — see the CompatibilityError doc comment for the no-payload/
// no-secret guarantee.
func (e *CompatibilityError) Error() string {
	switch e.Reason {
	case ReasonUnknownMajor:
		return fmt.Sprintf("attachcontract: unsupported major version %q (supported: %s)", e.Requested, formatRanges(e.Supported))
	case ReasonNoOverlap:
		return fmt.Sprintf("attachcontract: no overlapping version range for %q (supported: %s)", e.Requested, formatRanges(e.Supported))
	case ReasonMissingCapability:
		return fmt.Sprintf("attachcontract: missing required capability %q", e.Missing)
	case ReasonMalformedVersion:
		return fmt.Sprintf("attachcontract: malformed version string %q", e.Requested)
	default:
		return "attachcontract: version compatibility error"
	}
}

// formatRanges renders ranges as a stable, human-readable summary (e.g.
// "1.[0-3], 2.[0-1]") for inclusion in a CompatibilityError's message.
func formatRanges(ranges []VersionRange) string {
	if len(ranges) == 0 {
		return "none"
	}
	out := ""
	for i, r := range ranges {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%d.[%d-%d]", r.Major, r.MinMinor, r.MaxMinor)
	}
	return out
}
