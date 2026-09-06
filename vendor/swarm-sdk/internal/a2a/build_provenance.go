package a2a

import (
	"runtime/debug"
	"sync"
)

// BuildProvenance captures the identity of the binary that produced a
// presence record, sourced from runtime/debug.ReadBuildInfo() at process
// start. It exists to replace BinaryModTime (a wall-clock file-modification
// heuristic that a bind-mount, clock skew, container rebuild, or simple
// `touch` can trivially defeat) with a deterministic "is this genuinely the
// same build" answer for freshness/staleness decisions introduced from this
// phase onward. BinaryModTime may still be read for backward-compatible
// display/logging of legacy records; it is never authoritative for a new
// decision that has BuildProvenance available.
//
// BuildProvenance is deliberately local-presence-only: it is never included
// in remotePresenceProjection's allow-list (see discovery.go), matching the
// existing treatment of the pre-existing Version and BinaryModTime fields,
// which are also excluded from unauthenticated remote projection.
type BuildProvenance struct {
	// Available reports whether runtime/debug.ReadBuildInfo() succeeded for
	// this process. It is false under contexts where Go does not embed build
	// info (for example some `go run` invocations, or a binary built with
	// -buildvcs=false and no module information), and false is the
	// documented safe default: every other field is its zero value, and
	// SameBuild/DifferentBuild never report "same build" for an unavailable
	// value compared against anything (including another unavailable value)
	// — an indeterminate provenance is never treated as a positive match.
	Available bool `json:"available"`

	// GoVersion is the toolchain version that produced the binary (for
	// example "go1.23.0"), as reported by debug.BuildInfo.GoVersion.
	GoVersion string `json:"go_version,omitempty"`

	// MainVersion is the main module's version as reported by
	// debug.BuildInfo.Main.Version — a pseudo-version, a tag, or "(devel)"
	// for an untagged local build. It alone is not sufficient to distinguish
	// two local rebuilds (both report "(devel)"), which is why VCSRevision
	// and VCSModified below are also captured.
	MainVersion string `json:"main_version,omitempty"`

	// VCSRevision is the source-control revision embedded by the toolchain
	// (the "vcs.revision" build setting) when the binary was built from a
	// VCS checkout with revision embedding enabled. Empty when unavailable.
	VCSRevision string `json:"vcs_revision,omitempty"`

	// VCSModified records whether the toolchain detected uncommitted local
	// changes at build time (the "vcs.modified" build setting). A binary
	// built with local modifications is never considered the "same build" as
	// one built clean at the same revision, even though VCSRevision matches.
	VCSModified bool `json:"vcs_modified,omitempty"`
}

// readBuildInfo is a package-private seam over runtime/debug.ReadBuildInfo.
// Production code always uses the real function; tests override this var to
// deterministically exercise the ok=false fallback path, which `go test`
// itself does not reliably reproduce (test binaries normally carry build
// info even when an interactively `go run` binary would not).
var readBuildInfo = debug.ReadBuildInfo

// NewBuildProvenance computes a BuildProvenance for the current process from
// runtime/debug.ReadBuildInfo(). It never panics: when build info is
// unavailable (ReadBuildInfo's ok return is false, or it unexpectedly
// returns a nil *BuildInfo), it returns the documented safe default — a
// zero-value BuildProvenance with Available set to false — rather than
// crashing or fabricating identity.
func NewBuildProvenance() BuildProvenance {
	info, ok := readBuildInfo()
	if !ok || info == nil {
		return BuildProvenance{}
	}

	provenance := BuildProvenance{
		Available:   true,
		GoVersion:   info.GoVersion,
		MainVersion: info.Main.Version,
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			provenance.VCSRevision = setting.Value
		case "vcs.modified":
			provenance.VCSModified = setting.Value == "true"
		}
	}
	return provenance
}

// currentBuildProvenance caches this process's own build provenance: a
// running binary's build identity cannot change during its lifetime, so it
// is computed once (via sync.OnceValue) and reused by every caller instead
// of re-parsing debug.ReadBuildInfo() on every presence read/write.
var currentBuildProvenance = sync.OnceValue(NewBuildProvenance)

// CurrentBuildProvenance returns this process's build provenance, computed
// once from runtime/debug.ReadBuildInfo() and cached for the process's
// lifetime. Callers that publish local presence (JoinSwarm, heartbeat/status
// writers) use this to populate PeerPresence.BuildProvenance.
func CurrentBuildProvenance() BuildProvenance {
	return currentBuildProvenance()
}

// SameBuild reports whether provenance and other deterministically identify
// the same build. It never uses a time-based/fuzzy comparison: two records
// are the "same build" only when both are Available and agree on every
// identity field below. When either value is unavailable (Available is
// false), the comparison is indeterminate and SameBuild conservatively
// returns false — an unavailable provenance is never treated as matching
// anything, including another unavailable provenance, since neither side
// actually proved an identity.
//
// Comparison order: if both sides carry a non-empty VCSRevision, that alone
// is the deterministic identity signal (two binaries built from the exact
// same clean revision, or the exact same dirty revision, are the same
// build only when VCSModified also agrees). When VCSRevision is unavailable
// on either side (for example a build without VCS embedding), MainVersion
// is used instead — but two "(devel)" main versions never count as a match,
// since "(devel)" carries no real identity and treating it as one would let
// two genuinely different local builds compare equal.
func (provenance BuildProvenance) SameBuild(other BuildProvenance) bool {
	if !provenance.Available || !other.Available {
		return false
	}
	if provenance.VCSRevision != "" && other.VCSRevision != "" {
		return provenance.VCSRevision == other.VCSRevision &&
			provenance.VCSModified == other.VCSModified
	}
	if provenance.MainVersion == "" || other.MainVersion == "" {
		return false
	}
	if provenance.MainVersion == "(devel)" || other.MainVersion == "(devel)" {
		return false
	}
	return provenance.MainVersion == other.MainVersion
}

// DifferentBuild is the negation of SameBuild, provided so call sites making
// a staleness decision ("this peer is running a different build than me")
// can read naturally without double negatives. Per SameBuild's contract, two
// unavailable/indeterminate provenances report DifferentBuild == true (they
// never proved sameness), which is the fail-closed direction for a staleness
// decision: absent proof of being the same build, callers must not assume
// freshness.
func (provenance BuildProvenance) DifferentBuild(other BuildProvenance) bool {
	return !provenance.SameBuild(other)
}
