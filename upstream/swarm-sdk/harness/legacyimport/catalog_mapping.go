// catalog_mapping.go maps legacy TUI tool names onto the harness capability
// catalog (harness/catalog.go) and the tui-v1 compatibility preset
// (harness/presets.go), used by mapLegacyToolName (translate_tools.go) for
// both tools.enabled and agents[].tools translation.
//
// # The mapping algorithm
//
//  1. Case-insensitive match against every catalog capability's stable ID and
//     every RuntimeAliases entry (harness.Catalog()). RuntimeAliases MAY
//     collide across capabilities by design (see harness/catalog.go's own
//     doc comment: "three distinct 'grep' schemas"); a collision resolves
//     deterministically to the FIRST catalog entry (in harness.Catalog()'s
//     fixed declaration order) that claims the alias, and mapLegacyToolName
//     appends an explicit ambiguity note to the classification reason when
//     this happens (currently only "grep").
//  2. A matched capability's PolicyClass gates it exactly as
//     harness/plan.go's checkCapabilityPolicy would gate a hand-written
//     agent.tools entry (DEFER-DISCOVER hard-rejects; NEVER-DEFAULT requires
//     an explicit acknowledgement legacyimport does not grant automatically).
//  3. A class-legal id is MIGRATED when it is in the tui-v1 preset's bound ID
//     set (harness.LookupPreset("tui-v1").IDs — Phase 10b guarantees every one
//     of those compiles AND is client-constructible), LOSSY with a cited
//     reason when Phase 10c's tuiV1Shortfall documents why it is not (a
//     ShortfallHostBindingRequired entry keyed by the same capability id), and
//     LOSSY with a generic "unverified beyond compiling" reason otherwise.
package legacyimport

import (
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

// capabilityAliasIndex maps a lowercased catalog ID or RuntimeAlias to its
// Capability. Built once at package init from harness.Catalog(), which is
// itself a fixed literal (deterministic order) — see the package doc above
// for how alias collisions resolve.
var capabilityAliasIndex = buildCapabilityAliasIndex()

func buildCapabilityAliasIndex() map[string]harness.Capability {
	idx := make(map[string]harness.Capability)
	for _, c := range harness.Catalog() {
		key := strings.ToLower(c.ID)
		if _, exists := idx[key]; !exists {
			idx[key] = c
		}
		for _, alias := range c.RuntimeAliases {
			ak := strings.ToLower(alias)
			if _, exists := idx[ak]; !exists {
				idx[ak] = c
			}
		}
	}
	return idx
}

func lookupCapabilityByLegacyName(name string) (harness.Capability, bool) {
	c, ok := capabilityAliasIndex[strings.ToLower(name)]
	return c, ok
}

// tuiV1BoundSet / tuiV1ShortfallByCapability are derived once from
// harness.LookupPreset("tui-v1") (Phase 10c) — the authoritative,
// already-exported source of which ids compile+bind for the TUI-compatible
// baseline and which are documented as deliberately excluded, and why.
var tuiV1BoundSet, tuiV1ShortfallByCapability = loadTUIV1Preset()

func loadTUIV1Preset() (map[string]struct{}, map[string]string) {
	bound := make(map[string]struct{})
	shortfall := make(map[string]string)
	if spec, ok := harness.LookupPreset("tui-v1"); ok {
		for _, id := range spec.IDs {
			bound[id] = struct{}{}
		}
		for _, e := range spec.Shortfall {
			if e.Capability == "" {
				continue
			}
			shortfall[e.Capability] = string(e.Cause) + ": " + e.Reason
		}
	}
	return bound, shortfall
}

func tuiV1Bound(id string) bool {
	_, ok := tuiV1BoundSet[id]
	return ok
}

func tuiV1ShortfallReason(id string) (string, bool) {
	r, ok := tuiV1ShortfallByCapability[id]
	return r, ok
}
