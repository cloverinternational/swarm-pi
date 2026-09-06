package attachclient

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Version is a client/server contract version expressed as major.minor,
// matching ADR-004's version model: "major.minor is the serialized schema
// version... The JSON spelling is a string so 1.10 cannot be confused with
// a decimal number."
//
// This is attachclient's own interim negotiation type. It is intentionally
// shaped to match ADR-004 so that swapping it for
// swarm-sdk/internal/attachcontract's canonical version type (once that
// P04.A-owned package exists/compiles) is a mechanical rename — see the
// package doc comment in client.go for why attachcontract is not imported
// this round.
type Version struct {
	Major int
	Minor int
}

func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// MarshalJSON encodes Version as ADR-004 requires: a JSON string, never a
// bare number.
func (v Version) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.String())
}

// UnmarshalJSON requires a JSON string and rejects a bare number or any
// malformed "major.minor" spelling (ADR-004 Verification: "malformed-version
// ... tests").
func (v *Version) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return &CompatibilityError{Category: CompatibilityMalformedVersion}
	}
	parsed, err := ParseVersion(s)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// ParseVersion parses "major.minor"; any other shape (missing dot,
// non-numeric, negative, extra segments, empty string) is a
// CompatibilityMalformedVersion error.
func ParseVersion(s string) (Version, error) {
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 2 {
		return Version{}, &CompatibilityError{Category: CompatibilityMalformedVersion}
	}
	major, errMaj := strconv.Atoi(parts[0])
	minor, errMin := strconv.Atoi(parts[1])
	if errMaj != nil || errMin != nil || major < 0 || minor < 0 {
		return Version{}, &CompatibilityError{Category: CompatibilityMalformedVersion}
	}
	return Version{Major: major, Minor: minor}, nil
}

// VersionRange advertises the inclusive minimum/maximum minor supported
// within one major version — ADR-004 RPC contract's "information/readiness
// method advertises supported ranges."
type VersionRange struct {
	Major    int
	MinMinor int
	MaxMinor int
}

// Capabilities is the full negotiation payload a peer advertises: its
// supported version ranges, named optional capabilities, and (if known) its
// daemon instance identity.
type Capabilities struct {
	InstanceID   string
	Ranges       []VersionRange
	Capabilities []string
}

// CompatibilityErrorCategory is ADR-004's "stable server-defined error
// category" for negotiation failures ("Compatibility failures use a stable
// server-defined error category and include supported ranges, without
// exposing request payloads or secrets.").
type CompatibilityErrorCategory string

const (
	// CompatibilityMalformedVersion: a version string could not be parsed
	// as "major.minor" at all.
	CompatibilityMalformedVersion CompatibilityErrorCategory = "malformed_version"
	// CompatibilityUnknownMajor: the peer advertises no range sharing any
	// major version this client supports. ADR-004 Compatibility: "A
	// receiver rejects an unknown major before interpreting variant data."
	CompatibilityUnknownMajor CompatibilityErrorCategory = "unknown_major"
	// CompatibilityNoOverlap: a shared major exists, but no minor range
	// overlaps.
	CompatibilityNoOverlap CompatibilityErrorCategory = "no_overlap"
	// CompatibilityMissingCapability: a version was selected but the peer
	// lacks a capability this client requires before issuing state-changing
	// methods.
	CompatibilityMissingCapability CompatibilityErrorCategory = "missing_capability"
)

// CompatibilityError is returned when negotiation cannot select a mutually
// supported version/capability set. It intentionally carries no request
// payloads or secrets (ADR-004 Security/RPC contract).
type CompatibilityError struct {
	Category  CompatibilityErrorCategory
	Requested Version
	Supported []VersionRange
	Missing   []string
}

func (e *CompatibilityError) Error() string {
	switch e.Category {
	case CompatibilityMalformedVersion:
		return "attachclient: malformed contract version"
	case CompatibilityUnknownMajor:
		return fmt.Sprintf("attachclient: incompatible contract: unknown major version; peer supports %v", e.Supported)
	case CompatibilityNoOverlap:
		return fmt.Sprintf("attachclient: incompatible contract: no overlapping minor range; peer supports %v", e.Supported)
	case CompatibilityMissingCapability:
		return fmt.Sprintf("attachclient: incompatible contract: negotiated %s but peer is missing required capabilities %v", e.Requested, e.Missing)
	default:
		return fmt.Sprintf("attachclient: incompatible contract (%s)", e.Category)
	}
}

// Negotiate selects the highest mutually supported minor within a shared
// major from clientSupported and peer.Ranges, then verifies peer advertises
// every capability in requiredCapabilities. It implements ADR-004
// Compatibility exactly: "peers select the highest mutually supported minor
// within a supported major. A receiver rejects an unknown major before
// interpreting variant data.".
//
// Hard invariant (package-boundaries.md): "Unknown protocol versions and
// capabilities fail with compatibility errors." — every failure path here
// returns a *CompatibilityError, never a bare/opaque error.
func Negotiate(clientSupported []VersionRange, peer Capabilities, requiredCapabilities []string) (Version, error) {
	if len(clientSupported) == 0 {
		return Version{}, &CompatibilityError{Category: CompatibilityMalformedVersion}
	}

	majorShared := false
	var best *Version
	for _, cr := range clientSupported {
		for _, pr := range peer.Ranges {
			if cr.Major != pr.Major {
				continue
			}
			majorShared = true
			lo, hi := cr.MinMinor, cr.MaxMinor
			if pr.MinMinor > lo {
				lo = pr.MinMinor
			}
			if pr.MaxMinor < hi {
				hi = pr.MaxMinor
			}
			if lo > hi {
				continue // major shared, but this pair's minor ranges don't overlap
			}
			candidate := Version{Major: cr.Major, Minor: hi}
			if best == nil || candidate.Major > best.Major ||
				(candidate.Major == best.Major && candidate.Minor > best.Minor) {
				best = &candidate
			}
		}
	}

	if !majorShared {
		return Version{}, &CompatibilityError{Category: CompatibilityUnknownMajor, Supported: peer.Ranges}
	}
	if best == nil {
		return Version{}, &CompatibilityError{Category: CompatibilityNoOverlap, Supported: peer.Ranges}
	}

	if len(requiredCapabilities) > 0 {
		have := make(map[string]bool, len(peer.Capabilities))
		for _, c := range peer.Capabilities {
			have[c] = true
		}
		var missing []string
		for _, rc := range requiredCapabilities {
			if !have[rc] {
				missing = append(missing, rc)
			}
		}
		if len(missing) > 0 {
			return Version{}, &CompatibilityError{
				Category:  CompatibilityMissingCapability,
				Requested: *best,
				Supported: peer.Ranges,
				Missing:   missing,
			}
		}
	}

	return *best, nil
}
