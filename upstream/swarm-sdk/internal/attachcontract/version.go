package attachcontract

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Version is a strict "major.minor" contract version per ADR-004's "Version
// model": "major.minor is the serialized schema version... The JSON
// spelling is a string so 1.10 cannot be confused with a decimal number."
type Version struct {
	Major int
	Minor int
}

// String renders v as "major.minor" (e.g. "1.0", "1.10").
func (v Version) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// MarshalJSON always encodes v as a JSON string, never a bare number.
func (v Version) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.String())
}

// UnmarshalJSON requires v to be a JSON string and strictly parses it with
// ParseVersion. A bare JSON number (e.g. 1.1) is rejected here at the JSON
// type level before ParseVersion even runs, so "1.10" can never be silently
// reinterpreted as the decimal 1.1.
func (v *Version) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("attachcontract: version must be a JSON string, not a number or other type: %w", err)
	}
	parsed, err := ParseVersion(s)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// ParseVersion strictly parses a "major.minor" string. It rejects:
//   - the empty string;
//   - a bare major with no minor component ("1");
//   - a three-(or more)-component version ("1.0.0");
//   - a "v"-prefixed version ("v1.0");
//   - non-numeric, signed, or otherwise non-digit components ("1.x", "-1.0");
//   - components with internal whitespace or empty components (".", "1.").
//
// On failure it returns a *CompatibilityError with Reason ==
// ReasonMalformedVersion, so callers can distinguish a malformed version
// from other negotiation failures using the same typed-error mechanism.
func ParseVersion(s string) (Version, error) {
	malformed := func() (Version, error) {
		return Version{}, &CompatibilityError{Reason: ReasonMalformedVersion, Requested: s}
	}
	if s == "" {
		return malformed()
	}
	parts := strings.Split(s, ".")
	if len(parts) != 2 {
		return malformed()
	}
	major, err := parseStrictNonNegativeInt(parts[0])
	if err != nil {
		return malformed()
	}
	minor, err := parseStrictNonNegativeInt(parts[1])
	if err != nil {
		return malformed()
	}
	return Version{Major: major, Minor: minor}, nil
}

// parseStrictNonNegativeInt accepts only a non-empty run of ASCII digits
// (rejecting empty strings, signs, whitespace, and non-digit characters
// such as a "v" prefix or leading/trailing dots).
func parseStrictNonNegativeInt(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("attachcontract: empty version component")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("attachcontract: non-numeric version component %q", s)
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// Compare returns -1 if v < other, 0 if v == other, and 1 if v > other,
// comparing (Major, Minor) lexicographically.
func (v Version) Compare(other Version) int {
	if v.Major != other.Major {
		if v.Major < other.Major {
			return -1
		}
		return 1
	}
	if v.Minor != other.Minor {
		if v.Minor < other.Minor {
			return -1
		}
		return 1
	}
	return 0
}

// SelectVersion picks the highest mutually supported minor within a
// mutually supported major, given the local side's and remote side's
// advertised CapabilityAdvertisement, and verifies every name in
// requiredCapabilities is present in remote.Capabilities. ADR-004
// "Compatibility": "peers select the highest mutually supported minor
// within a supported major. A receiver rejects an unknown major before
// interpreting variant data. It accepts a newer minor only when all
// required capabilities are understood."
//
// Failure modes, in the order checked:
//  1. remote's major is not one local supports at all -> ReasonUnknownMajor.
//  2. local and remote share a major, but no minor overlaps -> ReasonNoOverlap.
//  3. a version is selectable, but remote lacks a required capability ->
//     ReasonMissingCapability.
func SelectVersion(local, remote CapabilityAdvertisement, requiredCapabilities []string) (Version, error) {
	best, ok := bestOverlap(local.SupportedRanges, remote.SupportedRanges)
	if !ok {
		if !anyMajorOverlap(local.SupportedRanges, remote.SupportedRanges) {
			return Version{}, &CompatibilityError{Reason: ReasonUnknownMajor, Supported: local.SupportedRanges}
		}
		return Version{}, &CompatibilityError{Reason: ReasonNoOverlap, Supported: local.SupportedRanges}
	}
	for _, cap := range requiredCapabilities {
		if !remote.HasCapability(cap) {
			return Version{}, &CompatibilityError{Reason: ReasonMissingCapability, Missing: cap, Supported: local.SupportedRanges}
		}
	}
	return best, nil
}

// anyMajorOverlap reports whether a and b share at least one Major value,
// independent of whether their minor ranges overlap.
func anyMajorOverlap(a, b []VersionRange) bool {
	for _, ra := range a {
		for _, rb := range b {
			if ra.Major == rb.Major {
				return true
			}
		}
	}
	return false
}

// bestOverlap returns the highest Version mutually covered by some pair of
// ranges in a and b (same Major, overlapping [MinMinor, MaxMinor]).
func bestOverlap(a, b []VersionRange) (Version, bool) {
	var best Version
	found := false
	for _, ra := range a {
		for _, rb := range b {
			if ra.Major != rb.Major {
				continue
			}
			lo := ra.MinMinor
			if rb.MinMinor > lo {
				lo = rb.MinMinor
			}
			hi := ra.MaxMinor
			if rb.MaxMinor < hi {
				hi = rb.MaxMinor
			}
			if lo > hi {
				continue
			}
			cand := Version{Major: ra.Major, Minor: hi}
			if !found || cand.Compare(best) > 0 {
				best = cand
				found = true
			}
		}
	}
	return best, found
}

// DecodeEnvelope strictly decodes raw JSON bytes into an Envelope. It first
// extracts and strictly parses ONLY schema_version and rejects an
// unsupported major BEFORE unmarshalling the rest of the envelope (and
// therefore before DecodePayload ever interprets kind-specific variant
// data) — ADR-004 "Compatibility": "A receiver rejects an unknown major
// before interpreting variant data."
//
// supportedMajors is the closed set of majors this decoder accepts. Passing
// nil skips the major-compat check (accepts any well-formed major) and
// defers that decision to the caller; passing an empty non-nil slice
// rejects every major.
func DecodeEnvelope(data []byte, supportedMajors []int) (Envelope, error) {
	if len(data) > MaxEnvelopeBytes {
		return Envelope{}, fmt.Errorf("attachcontract: envelope exceeds max size (%d > %d bytes)", len(data), MaxEnvelopeBytes)
	}
	var probe struct {
		SchemaVersion *string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Envelope{}, fmt.Errorf("attachcontract: malformed envelope: %w", err)
	}
	if probe.SchemaVersion == nil || *probe.SchemaVersion == "" {
		return Envelope{}, fmt.Errorf("attachcontract: envelope missing required field %q", "schema_version")
	}
	ver, err := ParseVersion(*probe.SchemaVersion)
	if err != nil {
		return Envelope{}, err
	}
	if supportedMajors != nil && !containsInt(supportedMajors, ver.Major) {
		return Envelope{}, &CompatibilityError{Reason: ReasonUnknownMajor, Requested: *probe.SchemaVersion}
	}

	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
