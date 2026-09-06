package legacyimport

import (
	"sort"
	"strconv"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

// translation accumulates the proposed harness.Document, the D2
// classification table, importer-synthesized (non-legacy-sourced) content
// notes, and every legacy string value that must never leak (D5) while
// walking one configbundle.ConfigBundle. It is a scratch/builder type, never
// exposed outside this package.
type translation struct {
	doc            harness.Document
	classification []ClassificationEntry
	synthesized    []string
	sensitive      []string

	// consumedProviderKeys tracks which credentials.providerKeys entries were
	// already attached to a profiles[]/provider credential reference, so the
	// remainder pass (translateCredentialsRemainder) does not double-report
	// the same legacy key under two different explanations.
	consumedProviderKeys map[string]struct{}
}

func newTranslation() *translation {
	t := &translation{consumedProviderKeys: map[string]struct{}{}}
	t.doc.APIVersion = harness.APIVersionV1Alpha1
	t.doc.Kind = harness.KindHarness
	return t
}

// classify records exactly one D2 classification entry. source is the exact
// dotted legacy JSON field path; item optionally names the specific element
// within a list-shaped field this entry concerns.
func (t *translation) classify(bucket Bucket, source, item, target, reason string) {
	t.classification = append(t.classification, ClassificationEntry{
		Source: []string{source},
		Item:   item,
		Bucket: bucket,
		Target: target,
		Reason: reason,
	})
}

// ignoreGroup / unsupportedGroup classify several legacy fields that share
// one reason, keeping section functions readable. Each field still gets its
// own ClassificationEntry (so legacyFieldInventory coverage is exact).
func (t *translation) ignoreGroup(reason string, sources ...string) {
	for _, s := range sources {
		t.classify(BucketIgnored, s, "", "", reason)
	}
}

func (t *translation) unsupportedGroup(reason string, sources ...string) {
	for _, s := range sources {
		t.classify(BucketUnsupported, s, "", "", reason)
	}
}

func (t *translation) addSynthesized(note string) {
	t.synthesized = append(t.synthesized, note)
}

// addSensitive records a legacy VALUE that must never appear in the
// proposal or report (D5). Very short strings are not tracked: they carry no
// practical secret entropy and tracking them risks unrelated false-positive
// substring collisions in assertNoSecretLeak (for example an empty string or
// a single-digit timeout value).
func (t *translation) addSensitive(v string) {
	if len(v) >= 6 {
		t.sensitive = append(t.sensitive, v)
	}
}

// translate walks every ConfigBundle section in a fixed, deterministic order
// and builds t.doc + t.classification + t.synthesized + t.sensitive. Order
// matters only for two things: (1) providers/profiles must resolve before
// nothing else depends on them (nothing currently does, agents[] intentionally
// does not reference profiles[] — see translateAgents), and (2) the section
// order is what makes Report.Classification's build-time order deterministic
// even before Report.sortedClassification defensively re-sorts it.
func (t *translation) translate(b *configbundle.ConfigBundle, input ImportInput) {
	t.translateMetadata(b, input)
	t.translateMergePolicy(b)
	t.translateSystemDisplayAndHost(b)
	t.translateProvidersAndProfiles(b)
	t.translateToolsSection(b)
	t.translateHooks(b)
	t.translateSkills(b)
	t.translateMcp(b)
	t.translatePrompts(b)
	t.translateAgents(b)
	t.translateContextSources(b)
	t.translateEnvironments(b)
}

func (t *translation) translateMetadata(b *configbundle.ConfigBundle, input ImportInput) {
	name := input.Name
	if name == "" {
		name = b.Name
	}
	if name == "" {
		name = "legacy-import"
		t.addSynthesized("metadata.name: no legacy name and no caller-supplied ImportInput.Name; used the fallback \"legacy-import\"")
	}
	t.doc.Metadata.Name = name
	t.doc.Metadata.Description = b.Description

	if b.Name != "" {
		t.classify(BucketMigrated, "name", "", "metadata.name", "direct field match (ConfigBundle.Name)")
	} else {
		t.classify(BucketIgnored, "name", "", "", "empty in the legacy store; metadata.name was synthesized instead (see Synthesized)")
	}
	if b.Description != "" {
		t.classify(BucketMigrated, "description", "", "metadata.description", "direct field match")
	} else {
		t.classify(BucketIgnored, "description", "", "", "empty in the legacy store")
	}
	t.classify(BucketIgnored, "schemaVersion", "", "", "schema-migration bookkeeping counter; harness documents version their SHAPE via apiVersion, not a bundle-internal counter")
	t.classify(BucketIgnored, "createdAt", "", "", "bookkeeping timestamp; not configuration, no manifest equivalent")
	t.classify(BucketIgnored, "updatedAt", "", "", "bookkeeping timestamp; not configuration, no manifest equivalent")
}

func (t *translation) translateMergePolicy(b *configbundle.ConfigBundle) {
	if b.MergePolicy != nil {
		t.classify(BucketUnsupported, "mergePolicy", "", "",
			"harness.Document (types.go) is a single flat document with no global/project layering or per-section merge-mode concept; ConfigBundle's whole merge-policy model has no manifest equivalent")
	} else {
		t.classify(BucketIgnored, "mergePolicy", "", "", "unset in the legacy store")
	}
}

// sanitizeEnvName turns an arbitrary legacy identifier into a safe
// environment-variable-name fragment: uppercase, [A-Z0-9_] only.
func sanitizeEnvName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "UNNAMED"
	}
	return out
}

// dedupeSorted returns a sorted, duplicate-free copy of in (nil for empty).
func dedupeSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	cp := append([]string{}, in...)
	sort.Strings(cp)
	out := make([]string, 0, len(cp))
	for i, v := range cp {
		if i == 0 || v != cp[i-1] {
			out = append(out, v)
		}
	}
	return out
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func itoa(n int) string { return strconv.Itoa(n) }
