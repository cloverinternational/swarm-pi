package legacyimport

import (
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

// mutatingCapabilityIDs is the small set of catalog ids whose selection
// implies a mutation posture, used only to decide whether to synthesize
// permissions.allowMutation: true (see translateToolsSection). It is not a
// classification source of truth — the tool mapping itself is
// mapLegacyToolName / the catalog.
var mutatingCapabilityIDs = map[string]struct{}{
	"forge.apply_patch":     {},
	"forge.undo":            {},
	"forge.semantic_rename": {},
	"forge.edit":            {},
	"forge.write":           {},
}

func isMutatingCapability(id string) bool {
	_, ok := mutatingCapabilityIDs[id]
	return ok
}

// translateToolsSection builds agent.tools from tools.enabled (via
// mapLegacyToolName) and classifies every other ToolsConfig field.
func (t *translation) translateToolsSection(b *configbundle.ConfigBundle) {
	tc := &b.Tools

	var ids []string
	seen := map[string]struct{}{}
	if len(tc.Enabled) == 0 {
		t.classify(BucketIgnored, "tools.enabled", "", "agent.tools",
			"empty in the legacy store; the proposal declares agent.tools: [] (explicitly zero tools, matching the legacy 'nothing enabled' posture)")
	}
	for _, name := range tc.Enabled {
		id, bucket, target, reason := mapLegacyToolName(name)
		t.classify(bucket, "tools.enabled", name, target, reason)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	t.doc.Agent.Tools = harness.ToolSelection{Specified: true, IDs: ids}

	for _, id := range ids {
		if isMutatingCapability(id) {
			t.doc.Permissions.AllowMutation = true
			t.addSynthesized("permissions.allowMutation: set to true because a mutating capability (" + id + ") is selected in agent.tools")
			break
		}
	}

	if len(tc.Disabled) > 0 {
		t.classify(BucketUnsupported, "tools.disabled", itoa(len(tc.Disabled))+" id(s)", "",
			"harness.ToolSelection is allowlist-only (types.go); there is no denylist concept because everything not explicitly listed in agent.tools is already excluded by construction — a legacy 'open default minus these' posture has no open-default mode to subtract from")
	} else {
		t.classify(BucketIgnored, "tools.disabled", "", "", "empty in the legacy store")
	}
	if len(tc.Custom) > 0 {
		t.classify(BucketUnsupported, "tools.custom", itoa(len(tc.Custom))+" definition(s)", "",
			"harness's capability catalog (harness/catalog.go) is a closed, fixed set of stable ids; there is no user-defined/custom-tool schema to declare a new one")
	} else {
		t.classify(BucketIgnored, "tools.custom", "", "", "empty in the legacy store")
	}
	if len(tc.Overrides) > 0 {
		t.classify(BucketUnsupported, "tools.overrides", itoa(len(tc.Overrides))+" override(s)", "",
			"no per-tool override config (timeout/maxRetries/autoApprove/promptUser/params) exists on the harness Document")
	} else {
		t.classify(BucketIgnored, "tools.overrides", "", "", "empty in the legacy store")
	}
	if tc.PermissionPolicy != nil {
		t.classify(BucketUnsupported, "tools.permissionPolicy", "", "",
			"permissions.policy: harness.Permissions is 4 scalar fields (approvalMode/workspaceBoundary/allowMutation/acknowledgeNeverDefault); the legacy level/timeoutSeconds/timeoutBehavior/overrides/rules model has no manifest section (see harness/presets.go tuiV1Shortfall permissions.policy)")
	} else {
		t.classify(BucketIgnored, "tools.permissionPolicy", "", "", "unset in the legacy store")
	}
	t.classify(BucketIgnored, "tools.mode", "", "",
		"load-strategy/merge selector; harness.Document is a single flat document with no parent layer to merge against")
}

// mapLegacyToolName translates one legacy tool name into a catalog id and a
// D2 bucket, per the algorithm documented in catalog_mapping.go.
func mapLegacyToolName(name string) (id string, bucket Bucket, target string, reason string) {
	capa, ok := lookupCapabilityByLegacyName(name)
	if !ok {
		return "", BucketUnsupported, "", "no harness catalog capability matches legacy tool name " + quoteName(name)
	}

	switch capa.Class {
	case harness.PolicyDeferDiscover:
		return "", BucketUnsupported, "", "capability " + capa.ID + " has catalog class DEFER-DISCOVER (" + capa.SideEffect +
			"): a hard compile-time reject with no override (harness/plan.go checkCapabilityPolicy); it cannot be selected in agent.tools at all"
	case harness.PolicyNeverDefault:
		return "", BucketLossy, "", "capability " + capa.ID + " has catalog class NEVER-DEFAULT (" + capa.SideEffect +
			"); selecting it also requires permissions.acknowledgeNeverDefault, a deliberate high-impact posture legacyimport does not grant on the operator's behalf — omitted from the proposal; add " +
			capa.ID + " to agent.tools AND permissions.acknowledgeNeverDefault manually to restore it"
	}

	ambiguity := ""
	if strings.EqualFold(name, "grep") {
		ambiguity = " NOTE: legacy name \"grep\" is ambiguous across forge.grep/forge.unified_grep/builtin.grep (all three declare a \"grep\"-family runtime alias); " + capa.ID + " was chosen deterministically as the first catalog declaration that claims it — verify this matches the legacy tool's actual schema."
	}

	if tuiV1Bound(capa.ID) {
		return capa.ID, BucketMigrated, "agent.tools", "capability " + capa.ID +
			" is documented as TUI-default-registered and client-bindable as of Phase 10b (harness/presets.go tuiV1PresetIDs)." + ambiguity
	}
	if shortfallReason, ok := tuiV1ShortfallReason(capa.ID); ok {
		return capa.ID, BucketLossy, "agent.tools", "id compiles into agent.tools (class " + string(capa.Class) +
			") but " + shortfallReason + ambiguity
	}
	return capa.ID, BucketLossy, "agent.tools", "id compiles into agent.tools (class " + string(capa.Class) +
		") but is not part of the documented tui-v1 compatibility baseline (harness/presets.go); parity with the legacy tool of the same name is unverified beyond compiling." + ambiguity
}

func quoteName(s string) string { return `"` + s + `"` }
