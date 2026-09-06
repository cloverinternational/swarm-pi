package legacyimport

import (
	"sort"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

// mapHookType validates a legacy hook type against harness's three literal
// type strings (they happen to be spelled identically: "command"/"script"/
// "http" on both sides).
func mapHookType(legacyType string) (string, bool) {
	switch legacyType {
	case harness.HookTypeCommand, harness.HookTypeScript, harness.HookTypeHTTP:
		return legacyType, true
	default:
		return "", false
	}
}

func (t *translation) translateHooks(b *configbundle.ConfigBundle) {
	hc := &b.Hooks
	disabled := make(map[string]struct{}, len(hc.Disabled))
	for _, id := range hc.Disabled {
		disabled[id] = struct{}{}
	}

	if len(hc.Definitions) == 0 {
		t.classify(BucketIgnored, "hooks.definitions", "", "", "empty in the legacy store")
	}
	var entries []harness.HookEntry
	for _, hd := range hc.Definitions {
		item := hd.ID
		if _, off := disabled[hd.ID]; off {
			t.classify(BucketMigrated, "hooks.definitions", item, "",
				"disabled hook excluded from the proposal entirely (equivalent effect: it never runs)")
			continue
		}
		if hd.ID == "" || hd.Event == "" {
			t.classify(BucketUnsupported, "hooks.definitions", item, "", "hook entry is missing a required id or event; skipped")
			continue
		}
		hookType, ok := mapHookType(hd.Type)
		if !ok {
			t.classify(BucketUnsupported, "hooks.definitions", item, "",
				"unrecognized legacy hook type "+quoteName(hd.Type)+"; harness hooks only support command/script/http; skipped")
			continue
		}

		entry := harness.HookEntry{
			ID: hd.ID, Event: hd.Event, Scope: harness.HookScopeGlobal, Type: hookType,
			Priority: hd.Priority, TimeoutSeconds: hd.Timeout, Enabled: hd.Enabled,
		}
		switch hookType {
		case harness.HookTypeCommand:
			entry.Command = hd.Command
		case harness.HookTypeScript:
			entry.Path = hd.Script
		case harness.HookTypeHTTP:
			entry.URL = hd.URL
		}
		if len(hd.Environment) > 0 {
			keys := sortedMapKeys(hd.Environment)
			for _, v := range hd.Environment {
				t.addSensitive(v)
			}
			entry.Environment = keys
		}
		entries = append(entries, entry)

		reason := "id/event/type/command-or-script-path-or-url/priority/timeoutSeconds/enabled migrated; scope is defaulted to \"global\" (legacy HookDefinition has no scope/matcher concept at all, so global is the closest faithful default, never a narrowing); name is dropped (no equivalent field)"
		if len(hd.Environment) > 0 {
			reason += "; environment VARIABLE NAMES migrated as an allowlist (harness hooks.environment carries names only) — the legacy literal VALUES are never carried into the proposal or this report (D5)"
		}
		t.classify(BucketLossy, "hooks.definitions", item, "hooks["+itoa(len(entries)-1)+"]", reason)
	}
	t.doc.Hooks = entries

	if len(hc.Disabled) > 0 {
		t.classify(BucketMigrated, "hooks.disabled", itoa(len(hc.Disabled))+" id(s)", "(omission from hooks:)",
			"disabled hook ids are excluded from the emitted hooks: list entirely")
	} else {
		t.classify(BucketIgnored, "hooks.disabled", "", "", "empty in the legacy store")
	}
	t.classify(BucketIgnored, "hooks.mode", "", "",
		"merge-mode selector; harness.Document is a single flat document with no parent layer to merge against")
}

func (t *translation) translateSkills(b *configbundle.ConfigBundle) {
	sc := &b.Skills
	if len(sc.Installed) == 0 {
		t.classify(BucketIgnored, "skills.installed", "", "", "empty in the legacy store")
	}
	var entries []harness.SkillEntry
	seen := map[string]struct{}{}
	for _, sr := range sc.Installed {
		item := sr.ID
		if sr.Enabled != nil && !*sr.Enabled {
			t.classify(BucketMigrated, "skills.installed", item, "",
				"disabled skill excluded from the proposal entirely (equivalent effect: it is never selected)")
			continue
		}
		if sr.ID == "" {
			t.classify(BucketUnsupported, "skills.installed", "(unnamed)", "", "skill reference has no id; skipped")
			continue
		}
		if _, dup := seen[sr.ID]; dup {
			t.classify(BucketUnsupported, "skills.installed", item, "", "duplicate skill id; skipped")
			continue
		}
		seen[sr.ID] = struct{}{}
		entries = append(entries, harness.SkillEntry{ID: sr.ID, Path: sr.Path})
		target := "skills entry (id)"
		if sr.Path != "" {
			target = "skills entry (id + path)"
		}
		t.classify(BucketLossy, "skills.installed", item, target,
			"id (and path, when declared) migrated; name and source ('global'/'project'/'builtin') are dropped (no equivalent field on harness.SkillEntry)")
	}
	var roots []string
	for _, p := range sc.SearchPath {
		if p != "" {
			roots = append(roots, p)
		}
	}
	if len(sc.SearchPath) > 0 {
		t.classify(BucketMigrated, "skills.searchPath", itoa(len(sc.SearchPath))+" root(s)", "skills.searchRoots",
			"direct field match (harness.SkillsSection.SearchRoots)")
	} else {
		t.classify(BucketIgnored, "skills.searchPath", "", "", "empty in the legacy store")
	}
	t.doc.Skills = harness.SkillsSection{Entries: entries, SearchRoots: roots}
	t.classify(BucketIgnored, "skills.mode", "", "",
		"merge-mode selector; harness.Document is a single flat document with no parent layer to merge against")
}

func (t *translation) translateMcp(b *configbundle.ConfigBundle) {
	mc := &b.MCPServers
	disabled := make(map[string]struct{}, len(mc.Disabled))
	for _, id := range mc.Disabled {
		disabled[id] = struct{}{}
	}

	if len(mc.Servers) == 0 {
		t.classify(BucketIgnored, "mcpServers.servers", "", "", "empty in the legacy store")
	}
	var entries []harness.McpEntry
	for _, sd := range mc.Servers {
		item := sd.ID
		if _, off := disabled[sd.ID]; off {
			t.classify(BucketMigrated, "mcpServers.servers", item, "",
				"disabled server excluded from the proposal entirely (equivalent effect: it is never connected)")
			continue
		}
		if sd.Enabled != nil && !*sd.Enabled {
			t.classify(BucketMigrated, "mcpServers.servers", item, "", "server has enabled: false; excluded from the proposal entirely")
			continue
		}
		if sd.ID == "" {
			t.classify(BucketUnsupported, "mcpServers.servers", "(unnamed)", "", "server has no id; skipped")
			continue
		}
		if sd.Type == "oauth" {
			t.classify(BucketUnsupported, "mcpServers.servers", item, "",
				"mcp type \"oauth\" is explicitly rejected by the harness compiler (harness/mcp.go mcpTypeOAuth: \"a declared OAuth-authenticated server cannot be resolved without a working auth flow\"); skipped")
			continue
		}
		if sd.Type != harness.McpTypeStdio && sd.Type != harness.McpTypeHTTP && sd.Type != harness.McpTypeSSE {
			t.classify(BucketUnsupported, "mcpServers.servers", item, "", "unrecognized legacy mcp type "+quoteName(sd.Type)+"; skipped")
			continue
		}

		entry := harness.McpEntry{
			ID: sd.ID, Type: sd.Type, Command: sd.Command, Args: append([]string{}, sd.Args...),
			WorkDir: sd.WorkDir, URL: sd.URL, TimeoutSeconds: sd.Timeout,
			Tools: append([]string{}, sd.Tools...), ExcludeTools: append([]string{}, sd.ExcludeTools...),
		}
		var envNames []string
		if len(sd.Env) > 0 {
			envNames = sortedMapKeys(sd.Env)
			for _, v := range sd.Env {
				t.addSensitive(v)
			}
		}
		headerNote := ""
		if len(sd.Headers) > 0 {
			headerKeys := make([]string, 0, len(sd.Headers))
			for k := range sd.Headers {
				headerKeys = append(headerKeys, k)
			}
			sort.Strings(headerKeys)
			headers := make(map[string]string, len(headerKeys))
			for _, k := range headerKeys {
				v := sd.Headers[k]
				t.addSensitive(v)
				envName := "LEGACY_MCP_" + sanitizeEnvName(sd.ID) + "_" + sanitizeEnvName(k)
				envNames = append(envNames, envName)
				headers[k] = envName
			}
			entry.Headers = headers
			headerNote = "; header VALUES were literal in the legacy store and are never carried into the proposal (D5) — each header now references a synthesized env-var-name allowlist entry the operator must populate"
		}
		entry.Environment = dedupeSorted(envNames)
		entries = append(entries, entry)

		reason := "id/type/command/args/workDir/url/timeoutSeconds/tools/excludeTools migrated"
		if len(sd.Env) > 0 {
			reason += "; environment VARIABLE NAMES migrated as an allowlist — legacy literal VALUES are never carried into the proposal or this report (D5)"
		}
		reason += headerNote
		reason += "; name is dropped (no equivalent field); stdio entries remain subject to harness's PINNING RULE (harness/mcp.go isPinnedStdioCommand) and may fail to compile if the legacy command/args were not version-pinned"
		t.classify(BucketLossy, "mcpServers.servers", item, "mcp["+itoa(len(entries)-1)+"]", reason)
	}
	t.doc.Mcp = entries

	if len(mc.Disabled) > 0 {
		t.classify(BucketMigrated, "mcpServers.disabled", itoa(len(mc.Disabled))+" id(s)", "(omission from mcp:)",
			"disabled server ids are excluded from the emitted mcp: list entirely")
	} else {
		t.classify(BucketIgnored, "mcpServers.disabled", "", "", "empty in the legacy store")
	}
	t.classify(BucketIgnored, "mcpServers.mode", "", "",
		"merge-mode selector; harness.Document is a single flat document with no parent layer to merge against")

	if len(b.Credentials.MCPAuth) > 0 {
		keys := make([]string, 0, len(b.Credentials.MCPAuth))
		for k := range b.Credentials.MCPAuth {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			auth := b.Credentials.MCPAuth[k]
			t.addSensitive(auth.Token)
			for _, v := range auth.Headers {
				t.addSensitive(v)
			}
		}
		t.classify(BucketLossy, "credentials.mcpAuth", itoa(len(keys))+" server(s)", "mcp[].headers (env-var reference)",
			"credential EXISTENCE (server id + auth type) is reported; the legacy token/header VALUES are never carried into the proposal or this report (D5) — if the corresponding mcp server entry above declares headers, they already reference a synthesized env-var name the operator must populate")
	} else {
		t.classify(BucketIgnored, "credentials.mcpAuth", "", "", "empty in the legacy store")
	}
}
