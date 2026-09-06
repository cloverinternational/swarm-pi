package legacyimport

import (
	"sort"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

// providerCandidate is one translated profiles[]-shaped entry that could
// become the document's primary provider: section, sourced from EITHER
// providers.inline (legacyID = ProviderDefinition.ID) or profiles.inline
// (legacyID = ProfileDefinition.ID).
type providerCandidate struct {
	legacyID string
	kind     string
	model    string
	baseURL  string
	cred     *harness.Ref
}

// translateProvidersAndProfiles handles providers.*, profiles.*, and
// credentials.providerKeys together: they share one target (harness
// profiles[]) and one credential-existence concern (D5), and the primary
// provider: section must be chosen from among their combined candidates.
func (t *translation) translateProvidersAndProfiles(b *configbundle.ConfigBundle) {
	var profiles []harness.ProfileEntry
	profileIDs := map[string]struct{}{}
	var candidates []providerCandidate

	// --- providers.mode / providers.referencePath -----------------------
	if b.Providers.ReferencePath != "" {
		t.classify(BucketUnsupported, "providers.referencePath", "", "",
			"legacyimport reads exactly the one explicitly supplied ConfigBundle file (D1); it never follows a second referenced provider file")
	} else {
		t.classify(BucketIgnored, "providers.referencePath", "", "", "unset in the legacy store")
	}
	t.classify(BucketIgnored, "providers.mode", "", "",
		"load-strategy selector (reference/inline/merge); only the resulting providers.inline entries are migrated below, the strategy itself has no harness equivalent")

	// --- providers.inline -------------------------------------------------
	if len(b.Providers.Inline) == 0 {
		t.classify(BucketIgnored, "providers.inline", "", "", "empty in the legacy store")
	}
	for _, pd := range b.Providers.Inline {
		item := pd.ID
		if pd.Enabled != nil && !*pd.Enabled {
			t.classify(BucketMigrated, "providers.inline", item, "",
				"disabled provider entry excluded from the proposal entirely (equivalent effect: it is never selectable)")
			continue
		}
		if pd.ID == "" {
			t.classify(BucketUnsupported, "providers.inline", "(unnamed)", "",
				"provider entry has no id; skipped (an unnamed profile id cannot be referenced by anything)")
			continue
		}
		if _, dup := profileIDs[pd.ID]; dup {
			t.classify(BucketUnsupported, "providers.inline", item, "",
				"duplicate profile id after translation; skipped to avoid an ambiguous profiles[] entry")
			continue
		}
		kind := pd.Type
		if kind == "" {
			kind = pd.ID
		}
		model := ""
		if len(pd.Models) > 0 {
			model = pd.Models[0].ID
		}
		entry := harness.ProfileEntry{ID: pd.ID, Provider: kind, Model: model, BaseURL: pd.BaseURL}
		if key, ok := b.Credentials.ProviderKeys[pd.ID]; ok && key != "" {
			t.addSensitive(key)
			t.consumedProviderKeys[pd.ID] = struct{}{}
			envName := "LEGACY_" + sanitizeEnvName(pd.ID) + "_API_KEY"
			entry.Credential = &harness.Ref{Env: envName}
			t.classify(BucketLossy, "credentials.providerKeys", pd.ID, "profiles[\""+pd.ID+"\"].credential (env ref "+envName+")",
				"credential EXISTENCE and provenance migrated as an environment-variable REFERENCE; the legacy literal API key value is never carried into the proposal or this report (D5) — the operator must populate "+envName+" out of band")
		}
		profileIDs[pd.ID] = struct{}{}
		candidates = append(candidates, providerCandidate{legacyID: pd.ID, kind: kind, model: model, baseURL: pd.BaseURL, cred: entry.Credential})
		profiles = append(profiles, entry)

		reason := "id/provider(type)/baseURL/first-model migrated into a profiles[] entry"
		if len(pd.Models) > 1 {
			reason += " (models[0] of " + itoa(len(pd.Models)) + " chosen as the profile's model)"
		}
		reason += "; per-model metadata (contextWindow, pricing, vision/voice/streaming support, aliases), modelAliases, headers, and the opaque config map have no harness equivalent and are dropped"
		t.classify(BucketLossy, "providers.inline", item, "profiles[\""+pd.ID+"\"]", reason)
	}

	// --- profiles.mode / profiles.referencePath ---------------------------
	if b.Profiles.ReferencePath != "" {
		t.classify(BucketUnsupported, "profiles.referencePath", "", "",
			"legacyimport reads exactly the one explicitly supplied ConfigBundle file (D1); it never follows a second referenced profile file")
	} else {
		t.classify(BucketIgnored, "profiles.referencePath", "", "", "unset in the legacy store")
	}
	t.classify(BucketIgnored, "profiles.mode", "", "",
		"load-strategy selector (reference/inline/merge); only the resulting profiles.inline entries are migrated below, the strategy itself has no harness equivalent")

	// --- profiles.inline ---------------------------------------------------
	if len(b.Profiles.Inline) == 0 {
		t.classify(BucketIgnored, "profiles.inline", "", "", "empty in the legacy store")
	}
	for _, pd := range b.Profiles.Inline {
		item := pd.ID
		if pd.ID == "" {
			t.classify(BucketUnsupported, "profiles.inline", "(unnamed)", "", "profile definition has no id; skipped")
			continue
		}
		if _, dup := profileIDs[pd.ID]; dup {
			t.classify(BucketUnsupported, "profiles.inline", item, "",
				"duplicate profile id (collides with a providers.inline-derived profile or another profiles.inline entry); skipped to avoid an ambiguous profiles[] entry")
			continue
		}
		entry := harness.ProfileEntry{ID: pd.ID, Provider: pd.Provider, Model: pd.Model}
		if pd.MaxTokens > 0 {
			entry.Limits.MaxOutputTokens = pd.MaxTokens
		}
		profileIDs[pd.ID] = struct{}{}
		candidates = append(candidates, providerCandidate{legacyID: pd.ID, kind: pd.Provider, model: pd.Model})
		profiles = append(profiles, entry)

		reason := "id/provider/model migrated (profiles.inline has no baseURL field); maxTokens migrated into limits.maxOutputTokens"
		if pd.SystemPrompt != "" || len(pd.Tools) > 0 || len(pd.DisabledTools) > 0 {
			reason += "; systemPrompt/tools/disabledTools have no equivalent field on harness.ProfileEntry (prompts and tools are per-agent, not per-profile, in harness/agents.go) and are dropped"
		}
		reason += "; name/description/modelAlias/temperature/topP/vision/voice fields/capabilities/metadata have no equivalent field and are dropped"
		t.classify(BucketLossy, "profiles.inline", item, "profiles[\""+pd.ID+"\"]", reason)
	}

	t.doc.Profiles = profiles

	t.choosePrimaryProvider(b, candidates)
	t.translateCredentialsRemainder(b)
}

// choosePrimaryProvider selects the document's single provider: section from
// among the translated candidates, preferring an exact system.defaultProvider
// match, then falling back to the first declared candidate, then to
// system.defaultProvider/defaultModel used directly when there are no
// candidates at all. Every branch emits the system.defaultProvider/
// defaultModel classification (D2 — those two legacy fields are always
// accounted for exactly once).
func (t *translation) choosePrimaryProvider(b *configbundle.ConfigBundle, candidates []providerCandidate) {
	var chosen *providerCandidate
	if b.System.DefaultProvider != "" {
		for i := range candidates {
			if candidates[i].legacyID == b.System.DefaultProvider {
				chosen = &candidates[i]
				break
			}
		}
	}

	usedDefaultModel := false

	switch {
	case chosen != nil:
		t.doc.Provider = harness.Provider{ID: chosen.kind, Model: chosen.model, BaseURL: chosen.baseURL, Credential: chosen.cred}
		if b.System.DefaultModel != "" {
			t.doc.Provider.Model = b.System.DefaultModel
			usedDefaultModel = true
		}
		t.classify(BucketMigrated, "system.defaultProvider", "", "provider.id",
			"matched providers.inline/profiles.inline id \""+chosen.legacyID+"\"; provider.id/baseURL/credential taken from that entry")

	case len(candidates) > 0:
		c := candidates[0]
		t.doc.Provider = harness.Provider{ID: c.kind, Model: c.model, BaseURL: c.baseURL, Credential: c.cred}
		if b.System.DefaultModel != "" {
			t.doc.Provider.Model = b.System.DefaultModel
			usedDefaultModel = true
		}
		t.addSynthesized("provider: no system.defaultProvider matched a providers.inline/profiles.inline entry; used the first declared entry (\"" + c.legacyID + "\") instead")
		if b.System.DefaultProvider != "" {
			t.classify(BucketLossy, "system.defaultProvider", "", "",
				"names a provider not present in providers.inline/profiles.inline; ignored, the first declared entry was used instead (see Synthesized)")
		} else {
			t.classify(BucketIgnored, "system.defaultProvider", "", "",
				"unset in the legacy store; the first providers.inline/profiles.inline entry was used as provider: instead")
		}

	case b.System.DefaultProvider != "":
		t.doc.Provider = harness.Provider{ID: b.System.DefaultProvider, Model: b.System.DefaultModel}
		usedDefaultModel = b.System.DefaultModel != ""
		if key, ok := b.Credentials.ProviderKeys[b.System.DefaultProvider]; ok && key != "" {
			t.addSensitive(key)
			t.consumedProviderKeys[b.System.DefaultProvider] = struct{}{}
			envName := "LEGACY_" + sanitizeEnvName(b.System.DefaultProvider) + "_API_KEY"
			t.doc.Provider.Credential = &harness.Ref{Env: envName}
			t.classify(BucketLossy, "credentials.providerKeys", b.System.DefaultProvider, "provider.credential (env ref "+envName+")",
				"credential EXISTENCE and provenance migrated as an environment-variable REFERENCE; the legacy literal API key value is never carried into the proposal or this report (D5) — the operator must populate "+envName+" out of band")
		}
		t.classify(BucketMigrated, "system.defaultProvider", "", "provider.id",
			"no providers.inline/profiles.inline entries existed; system.defaultProvider used directly")

	default:
		t.classify(BucketIgnored, "system.defaultProvider", "", "",
			"unset in the legacy store, and no providers.inline/profiles.inline entries exist either; the proposal declares no provider.id at all (this is expected to fail compilation — see Report.Compile)")
	}

	if usedDefaultModel {
		t.classify(BucketMigrated, "system.defaultModel", "", "provider.model", "direct field match")
	} else if b.System.DefaultModel != "" {
		t.classify(BucketLossy, "system.defaultModel", "", "",
			"the chosen provider candidate's own model was used instead (system.defaultModel only applies when it names the chosen provider)")
	} else {
		t.classify(BucketIgnored, "system.defaultModel", "", "", "unset in the legacy store")
	}
}

// translateCredentialsRemainder classifies every credentials.* field NOT
// already handled inline above (providerKeys entries consumed by a matched
// provider/profile candidate are skipped here to avoid double-reporting).
func (t *translation) translateCredentialsRemainder(b *configbundle.ConfigBundle) {
	keys := make([]string, 0, len(b.Credentials.ProviderKeys))
	for k := range b.Credentials.ProviderKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if len(keys) == 0 {
		t.classify(BucketIgnored, "credentials.providerKeys", "", "", "empty in the legacy store")
	} else {
		anyRemaining := false
		for _, k := range keys {
			if _, done := t.consumedProviderKeys[k]; done {
				continue
			}
			anyRemaining = true
			v := b.Credentials.ProviderKeys[k]
			t.addSensitive(v)
			t.classify(BucketLossy, "credentials.providerKeys", k, "",
				"credential EXISTENCE reported; no providers.inline/profiles.inline/system.defaultProvider entry matched this provider id, so no profile/provider credential reference could be attached — the legacy VALUE is never carried into the proposal or this report (D5)")
		}
		if !anyRemaining {
			t.classify(BucketMigrated, "credentials.providerKeys", "", "",
				"every provider key matched a providers.inline/profiles.inline/system.defaultProvider entry; see the credentials.providerKeys entries recorded against those sections above")
		}
	}

	okeys := make([]string, 0, len(b.Credentials.OAuthTokens))
	for k := range b.Credentials.OAuthTokens {
		okeys = append(okeys, k)
	}
	sort.Strings(okeys)
	if len(okeys) > 0 {
		for _, k := range okeys {
			t.addSensitive(b.Credentials.OAuthTokens[k])
		}
		t.classify(BucketUnsupported, "credentials.oauthTokens", itoa(len(okeys))+" token(s)", "",
			"provider.oauth: harness rejects every OAuth/keyless provider outright (see harness/presets.go tuiV1Shortfall provider.oauth); token existence is reported by COUNT only, values are never carried into the proposal or this report (D5)")
	} else {
		t.classify(BucketIgnored, "credentials.oauthTokens", "", "", "empty in the legacy store")
	}

	t.classify(BucketIgnored, "credentials.inherit", "", "",
		"legacy global/project credential-layering artifact; harness.Document is a single flat manifest with no inheritance model to opt into")
}
