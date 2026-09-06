package legacyimport

import (
	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

// placeholderSystemPrompt is used only when no legacy default prompt can be
// found (see translatePrompts). It is importer-GENERATED content, not a
// translation of any legacy field, and is recorded in Synthesized rather
// than Classification.
const placeholderSystemPrompt = "# Imported from a legacy ConfigBundle\n\n" +
	"No default system prompt was found in prompts.overrides[\"system\"] or prompts.custom[\"default\"].\n" +
	"Replace this placeholder before using the proposal."

func (t *translation) translatePrompts(b *configbundle.ConfigBundle) {
	pc := &b.Prompts

	usedCustomDefault := false
	usedOverridesSystem := false

	if v, ok := pc.Overrides["system"]; ok && v != "" {
		t.doc.Agent.SystemPrompt = &harness.SystemPrompt{Inline: v}
		usedOverridesSystem = true
		t.classify(BucketLossy, "prompts.overrides", "system", "agent.systemPrompt.inline",
			"used as the primary agent's systemPrompt because it is the one prompts.* entry legacyimport recognizes as a plausible default; harness has no named-prompt-template registry, so every OTHER prompts.overrides key is dropped")
	} else if v, ok := pc.Custom["default"]; ok && v != "" {
		t.doc.Agent.SystemPrompt = &harness.SystemPrompt{Inline: v}
		usedCustomDefault = true
		t.classify(BucketLossy, "prompts.custom", "default", "agent.systemPrompt.inline",
			"used as the primary agent's systemPrompt because it is the one prompts.* entry legacyimport recognizes as a plausible default; harness has no named-prompt-template registry, so every OTHER prompts.custom key is dropped")
	} else {
		t.doc.Agent.SystemPrompt = &harness.SystemPrompt{Inline: placeholderSystemPrompt}
		t.addSynthesized("agent.systemPrompt: no legacy default prompt found in prompts.overrides[\"system\"] or prompts.custom[\"default\"]; a placeholder inline prompt was generated so the proposal has a chance to compile (agent.systemPrompt is required)")
	}

	customCount := len(pc.Custom)
	if usedCustomDefault {
		customCount--
	}
	if customCount > 0 {
		t.classify(BucketUnsupported, "prompts.custom", itoa(customCount)+" other template(s)", "",
			"harness has no named-prompt-template registry; only a single systemPrompt exists per agent/subagent entry (prompts.custom[\"default\"], when present, is the one exception used above)")
	} else if len(pc.Custom) == 0 {
		t.classify(BucketIgnored, "prompts.custom", "", "", "empty in the legacy store")
	}

	overridesCount := len(pc.Overrides)
	if usedOverridesSystem {
		overridesCount--
	}
	if overridesCount > 0 {
		t.classify(BucketUnsupported, "prompts.overrides", itoa(overridesCount)+" other override(s)", "",
			"harness ships no built-in prompts to override at all; only prompts.overrides[\"system\"], when present, is used above as a systemPrompt source")
	} else if len(pc.Overrides) == 0 {
		t.classify(BucketIgnored, "prompts.overrides", "", "", "empty in the legacy store")
	}

	t.classify(BucketIgnored, "prompts.mode", "", "",
		"merge-mode selector; harness.Document is a single flat document with no parent layer to merge against")
}

func (t *translation) translateAgents(b *configbundle.ConfigBundle) {
	ac := &b.Agents
	t.classify(BucketIgnored, "agents.defaultAgent", "", "",
		"selection pointer; harness has no 'default agent' register (see system.defaultAgent for the same reasoning)")

	if len(ac.Definitions) == 0 {
		t.classify(BucketIgnored, "agents.definitions", "", "", "empty in the legacy store")
	}
	var entries []harness.AgentEntry
	seen := map[string]struct{}{}
	for _, ad := range ac.Definitions {
		item := ad.ID
		if ad.ID == "" {
			t.classify(BucketUnsupported, "agents.definitions", "(unnamed)", "", "agent definition has no id; skipped")
			continue
		}
		if _, dup := seen[ad.ID]; dup {
			t.classify(BucketUnsupported, "agents.definitions", item, "", "duplicate agent id; skipped")
			continue
		}
		seen[ad.ID] = struct{}{}

		entry := harness.AgentEntry{ID: ad.ID}
		if ad.SystemPrompt != "" {
			entry.SystemPrompt = &harness.SystemPrompt{Inline: ad.SystemPrompt}
		}

		if len(ad.Tools) > 0 {
			var ids []string
			seenTool := map[string]struct{}{}
			for _, name := range ad.Tools {
				id, bucket, _, reason := mapLegacyToolName(name)
				t.classify(bucket, "agents.definitions", item+".tools["+name+"]", "", reason)
				if id == "" {
					continue
				}
				if _, dup := seenTool[id]; dup {
					continue
				}
				seenTool[id] = struct{}{}
				ids = append(ids, id)
			}
			entry.Tools = harness.ToolSelection{Specified: true, IDs: ids}
		}

		entries = append(entries, entry)

		reason := "id migrated as agents[].id"
		if ad.SystemPrompt != "" {
			reason += "; systemPrompt migrated as agents[].systemPrompt.inline"
		}
		if len(ad.Tools) > 0 {
			reason += "; tools migrated via the same catalog-alias mapping as tools.enabled (see the per-tool agents.definitions entries above)"
		}
		reason += "; name/description/modelAlias/capabilities/metadata have no equivalent AgentEntry field (harness/agents.go) and are dropped"
		t.classify(BucketLossy, "agents.definitions", item, "agents["+itoa(len(entries)-1)+"]", reason)
	}
	t.doc.Agents = entries

	t.classify(BucketIgnored, "agents.mode", "", "",
		"merge-mode selector; harness agents[] is a flat declared list with no merge/override semantics against a parent layer")
}

func (t *translation) translateContextSources(b *configbundle.ConfigBundle) {
	cs := &b.ContextSources
	if len(cs.Sources) > 0 {
		t.classify(BucketUnsupported, "contextSources.sources", itoa(len(cs.Sources))+" source(s)", "",
			"context.assembly: harness.Document has no context: key at all (CLAUDE.md/AGENTS.md/INDEX.md-style ambient discovery is TUI-private; see harness/presets.go tuiV1Shortfall context.assembly). Only source COUNT is reported, never id/path/url/config contents (defense against accidental secret exposure in the opaque config map)")
	} else {
		t.classify(BucketIgnored, "contextSources.sources", "", "", "empty in the legacy store")
	}
	t.classify(BucketIgnored, "contextSources.mode", "", "",
		"merge-mode selector; harness.Document has no context section to merge into")
}

func (t *translation) translateEnvironments(b *configbundle.ConfigBundle) {
	ec := &b.Environments
	if ec.Current != "" {
		t.classify(BucketUnsupported, "environments.current", "", "", "no named-environment-overlay concept exists in harness.Document")
	} else {
		t.classify(BucketIgnored, "environments.current", "", "", "unset in the legacy store")
	}
	if len(ec.Definitions) > 0 {
		t.classify(BucketUnsupported, "environments.definitions", itoa(len(ec.Definitions))+" definition(s)", "",
			"harness.Document (types.go) has exactly one flat set of provider/agent/tools/mcp sections with no named-environment overlay or inheritance concept at all; not covered by the Phase 10a audit (no TUI-side environments file was in its scope)")
	} else {
		t.classify(BucketIgnored, "environments.definitions", "", "", "empty in the legacy store")
	}
}
