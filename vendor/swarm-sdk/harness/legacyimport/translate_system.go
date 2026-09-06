package legacyimport

import (
	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

// translateSystemDisplayAndHost classifies every SystemConfig field EXCEPT
// defaultProvider/defaultModel (handled in translateProvidersAndProfiles,
// where the chosen provider candidate is known) and defaultAgent (a
// selection-pointer, classified alongside agents.defaultAgent for symmetry
// in translateAgents). See report.go's Bucket for what each classification
// means.
func (t *translation) translateSystemDisplayAndHost(b *configbundle.ConfigBundle) {
	s := &b.System

	t.classify(BucketIgnored, "system.currentProvider", "", "",
		"transient session-current selection, not a configuration source of truth; system.defaultProvider is the migrated field")
	t.classify(BucketIgnored, "system.currentModel", "", "",
		"transient session-current selection, not a configuration source of truth; system.defaultModel is the migrated field")
	t.classify(BucketUnsupported, "system.defaultMode", "", "",
		"interactive plan/act/auto posture control; harness.Permissions has approvalMode but no plan/act/auto concept, and no other manifest key models it (closest kin: permissions.policy, see harness/presets.go tuiV1Shortfall)")
	t.classify(BucketIgnored, "system.defaultAgent", "", "",
		"selection pointer; harness has no 'default agent' register — the primary agent IS the document, and agents[] entries are addressed by id, never defaulted")

	if s.Theme != "" {
		t.doc.Interfaces.Default = "tui"
		t.doc.Interfaces.TUI = &harness.InterfaceTUI{Theme: s.Theme}
		t.classify(BucketMigrated, "system.theme", "", "interfaces.tui.theme", "direct field match (harness.InterfaceTUI.Theme)")
	} else {
		t.classify(BucketIgnored, "system.theme", "", "", "empty in the legacy store")
	}

	t.ignoreGroup(
		"presentation-only display setting; no manifest equivalent (harness.Interfaces models only theme/format, see harness/types.go InterfaceTUI/InterfacePrint)",
		"system.showThinking", "system.showTokenCount", "system.showToolOutput", "system.compactMode",
		"system.maxOutputLines", "system.syntaxHighlighting",
	)

	t.unsupportedGroup(
		"no manifest concept for a whole-agent tool-execution concurrency cap or per-tool timeout policy beyond harness.Permissions' 4 scalar fields (see harness/presets.go tuiV1Shortfall permissions.policy)",
		"system.maxConcurrentTools", "system.toolTimeout",
	)

	if s.EnableSandbox != nil {
		v := *s.EnableSandbox
		t.doc.Permissions.WorkspaceBoundary = &v
		t.classify(BucketLossy, "system.enableSandbox", "", "permissions.workspaceBoundary",
			"boolean posture preserved; harness.Permissions.WorkspaceBoundary (types.go) is a bare bool with no allowed-path list")
	} else {
		t.classify(BucketIgnored, "system.enableSandbox", "", "", "unset in the legacy store")
	}
	t.classify(BucketUnsupported, "system.sandboxPaths", "", "",
		"harness.Permissions.WorkspaceBoundary is a bare bool (see system.enableSandbox above); there is no allowlist-of-paths field to carry this into")

	t.classify(BucketUnsupported, "system.enableCodeMode", "", "",
		"code.run_code capability id; Phase 10c's tuiV1Shortfall classifies it ShortfallHostBindingRequired — needs a FINAL selected-tool-registry snapshot + sandbox + dispatch adapter the harness client does not construct (harness/presets.go)")

	t.unsupportedGroup(
		"compaction.auto_config: no autoCompaction/completion-confirmation manifest section exists at all (see harness/presets.go tuiV1Shortfall compaction.auto_config)",
		"system.completion_confirm", "system.completion_confirm_max", "system.proactive_summarize_threshold",
	)

	t.classify(BucketUnsupported, "system.memoryBackend", "", "",
		"no manifest section for a memory/history subsystem selection (closest kin: history.findings_capture_hooks, see harness/presets.go tuiV1Shortfall)")

	t.ignoreGroup("host editor/UI integration; not applicable to a headless harness manifest",
		"system.editor", "system.editorArgs", "system.externalEditorCmd")
	t.ignoreGroup("host session/UI behavior; presentation-only",
		"system.autoSaveConversations", "system.confirmBeforeExit")

	t.unsupportedGroup(
		"observability.config: harness.Document has no observability: key and the harness client falls back to noop logging (see harness/presets.go tuiV1Shortfall observability.config)",
		"system.enableLogging", "system.logLevel",
	)

	t.unsupportedGroup(
		"compaction.auto_config: no autoCompaction manifest section exists at all (see harness/presets.go tuiV1Shortfall compaction.auto_config)",
		"system.enableCompaction", "system.compactionThreshold", "system.warningThreshold", "system.preserveRecentMessages",
		"system.enableMicroCompaction", "system.microRetentionCount",
	)

	t.ignoreGroup("host-local performance cache; no behavioral/security meaning, presentation/performance-only",
		"system.enableCache", "system.cacheDir", "system.cacheMaxSizeMB", "system.cacheExpiryDays")

	t.ignoreGroup(
		"host cloud-sync/telemetry integration; harness has no cloud-sync concept and is deliberately host-only here",
		"system.syncConversations", "system.syncSettings", "system.syncSettingsScope", "system.syncSettingsTeamId",
		"system.syncProfiles", "system.encryptCloudData", "system.anonymousAnalytics",
	)

	if s.WebSearch != nil {
		t.classify(BucketLossy, "system.webSearch", "", "agent.tools (web.websearch id, when selected)",
			"web.websearch is class-legal and OPT-IN (harness/catalog.go), so its id compiles into agent.tools when selected, but Phase 10c's tuiV1Shortfall classifies it ShortfallHostBindingRequired — the harness client cannot construct it today — and its backend-selection fields (exa/anthropic, etc.) have no manifest representation at all")
	} else {
		t.classify(BucketIgnored, "system.webSearch", "", "", "unset in the legacy store")
	}
	t.classify(BucketUnsupported, "system.hybridConfig", "", "",
		"no manifest section for this host-specific routing feature; not covered by the Phase 10a audit's TUI-registration scope")
	t.classify(BucketUnsupported, "system.steeringConfig", "", "",
		"the steering.* capability family (ask_user, block_next_tool, observe_only, inject_system_note, refocus, log_concern, halt_peer_loop) is catalog class DEFER-DISCOVER (harness/catalog.go): a hard compile-time reject with no override, so no steering configuration can ever be expressed as a selectable tool today")

	t.unsupportedGroup("no voice/audio modality manifest section; harness.Interfaces (types.go) models only theme/format",
		"system.enableVoice", "system.voiceProvider")

	if len(s.Custom) > 0 {
		t.classify(BucketIgnored, "system.custom", itoa(len(s.Custom))+" key(s)", "",
			"opaque host-extension bag by design; harness has no equivalent open-ended extension point; only the KEY COUNT is reported here, never key names or values (defense against accidental secret exposure)")
	} else {
		t.classify(BucketIgnored, "system.custom", "", "", "empty in the legacy store")
	}
}
