package legacyimport_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/harness/legacyimport"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

func boolPtr(b bool) *bool { return &b }

// legacyFieldInventory is an INDEPENDENTLY authored, exhaustive enumeration
// of every JSON field path configbundle.ConfigBundle exposes at the
// top-level-and-SystemConfig granularity this package classifies at (list-
// shaped sections like agents.definitions/providers.inline/hooks.definitions
// are treated as one field each — their per-item sub-fields are documented
// in ClassificationEntry.Reason text, not as separate inventory paths). It
// is cross-checked against configbundle/types.go's own json tags by hand
// (see the Phase 11a SUMMARY); TestLegacyFieldInventoryIsFullyClassified
// then verifies the legacyimport package's own classifier output covers
// exactly this set on a maximal fixture, so an accidental omission in the
// classifier (a future field added to configbundle without a matching
// legacyimport classify call) fails a test instead of silently under-
// reporting migration coverage (D2).
func legacyFieldInventory() []string {
	return []string{
		"agents.defaultAgent",
		"agents.definitions",
		"agents.mode",
		"contextSources.mode",
		"contextSources.sources",
		"createdAt",
		"credentials.inherit",
		"credentials.mcpAuth",
		"credentials.oauthTokens",
		"credentials.providerKeys",
		"description",
		"environments.current",
		"environments.definitions",
		"hooks.definitions",
		"hooks.disabled",
		"hooks.mode",
		"mcpServers.disabled",
		"mcpServers.mode",
		"mcpServers.servers",
		"mergePolicy",
		"name",
		"profiles.inline",
		"profiles.mode",
		"profiles.referencePath",
		"prompts.custom",
		"prompts.mode",
		"prompts.overrides",
		"providers.inline",
		"providers.mode",
		"providers.referencePath",
		"schemaVersion",
		"skills.installed",
		"skills.mode",
		"skills.searchPath",
		"system.anonymousAnalytics",
		"system.autoSaveConversations",
		"system.cacheDir",
		"system.cacheExpiryDays",
		"system.cacheMaxSizeMB",
		"system.compactMode",
		"system.compactionThreshold",
		"system.completion_confirm",
		"system.completion_confirm_max",
		"system.confirmBeforeExit",
		"system.currentModel",
		"system.currentProvider",
		"system.custom",
		"system.defaultAgent",
		"system.defaultMode",
		"system.defaultModel",
		"system.defaultProvider",
		"system.editor",
		"system.editorArgs",
		"system.enableCache",
		"system.enableCodeMode",
		"system.enableCompaction",
		"system.enableLogging",
		"system.enableMicroCompaction",
		"system.enableSandbox",
		"system.enableVoice",
		"system.encryptCloudData",
		"system.externalEditorCmd",
		"system.hybridConfig",
		"system.logLevel",
		"system.maxConcurrentTools",
		"system.maxOutputLines",
		"system.memoryBackend",
		"system.microRetentionCount",
		"system.preserveRecentMessages",
		"system.proactive_summarize_threshold",
		"system.sandboxPaths",
		"system.showThinking",
		"system.showTokenCount",
		"system.showToolOutput",
		"system.steeringConfig",
		"system.syncConversations",
		"system.syncProfiles",
		"system.syncSettings",
		"system.syncSettingsScope",
		"system.syncSettingsTeamId",
		"system.syntaxHighlighting",
		"system.theme",
		"system.toolTimeout",
		"system.voiceProvider",
		"system.warningThreshold",
		"system.webSearch",
		"tools.custom",
		"tools.disabled",
		"tools.enabled",
		"tools.mode",
		"tools.overrides",
		"tools.permissionPolicy",
		"updatedAt",
	}
}

// secretSentinel* are recognizable, sufficiently long fake-secret strings
// planted into every credential-bearing legacy field a maximal fixture
// exercises. TestNoSecretLeak (importer_test.go) asserts none of them ever
// appears in a Result's Proposal.JSON or Report output (D5).
const (
	secretSentinelProviderKey = "sk-LEGACY-SENTINEL-PROVIDER-KEY-DO-NOT-LEAK-0001"
	secretSentinelOAuthToken  = "oauth-LEGACY-SENTINEL-TOKEN-DO-NOT-LEAK-0002"
	secretSentinelMCPToken    = "mcp-LEGACY-SENTINEL-TOKEN-DO-NOT-LEAK-0003"
	secretSentinelMCPHeader   = "Bearer-LEGACY-SENTINEL-HEADER-DO-NOT-LEAK-0004"
	secretSentinelHookEnv     = "hook-LEGACY-SENTINEL-ENV-VALUE-DO-NOT-LEAK-0005"
	secretSentinelMcpEnv      = "mcpenv-LEGACY-SENTINEL-VALUE-DO-NOT-LEAK-0006"
)

// buildMaximalBundle returns a configbundle.ConfigBundle with essentially
// every field populated, exercising every bucket (MIGRATED/LOSSY/
// UNSUPPORTED/IGNORED) and planting every secret sentinel above. It is used
// by the field-inventory coverage test, the D5 leak test, and the D1
// read-only test.
func buildMaximalBundle() *configbundle.ConfigBundle {
	return &configbundle.ConfigBundle{
		SchemaVersion: 1,
		Name:          "acme-legacy-config",
		Description:   "a maximal legacy bundle fixture",
		System: configbundle.SystemConfig{
			DefaultProvider:             "work-anthropic",
			DefaultModel:                "claude-override-model",
			CurrentProvider:             "work-anthropic",
			CurrentModel:                "claude-3",
			DefaultMode:                 "act",
			DefaultAgent:                "reviewer",
			Theme:                       "dark",
			ShowThinking:                boolPtr(true),
			ShowTokenCount:              boolPtr(true),
			ShowToolOutput:              boolPtr(true),
			CompactMode:                 boolPtr(false),
			MaxOutputLines:              500,
			SyntaxHighlighting:          boolPtr(true),
			MaxConcurrentTools:          4,
			ToolTimeout:                 30,
			EnableSandbox:               boolPtr(true),
			SandboxPaths:                []string{"/tmp/sandbox"},
			EnableCodeMode:              boolPtr(true),
			CompletionConfirm:           boolPtr(true),
			CompletionConfirmMax:        intPtr(5),
			ProactiveSummarizeThreshold: float64Ptr(0.8),
			MemoryBackend:               "sqlite",
			Editor:                      "vim",
			EditorArgs:                  "-R",
			ExternalEditorCmd:           "code --wait",
			AutoSaveConversations:       boolPtr(true),
			ConfirmBeforeExit:           boolPtr(true),
			EnableLogging:               boolPtr(true),
			LogLevel:                    "debug",
			EnableCompaction:            boolPtr(true),
			CompactionThreshold:         80,
			WarningThreshold:            70,
			PreserveRecentMessages:      10,
			EnableMicroCompaction:       boolPtr(true),
			MicroRetentionCount:         3,
			EnableCache:                 boolPtr(true),
			CacheDir:                    "/tmp/cache",
			CacheMaxSizeMB:              100,
			CacheExpiryDays:             7,
			SyncConversations:           boolPtr(true),
			SyncSettings:                boolPtr(true),
			SyncSettingsScope:           "team",
			SyncSettingsTeamID:          "team-1",
			SyncProfiles:                boolPtr(true),
			EncryptCloudData:            boolPtr(true),
			AnonymousAnalytics:          boolPtr(false),
			HybridConfig:                &core.HybridConfig{Mode: "hybrid"},
			WebSearch:                   &core.WebSearchConfig{},
			SteeringConfig:              &core.SteeringConfig{Enabled: true},
			EnableVoice:                 boolPtr(false),
			VoiceProvider:               "elevenlabs",
			Custom:                      map[string]any{"extra": "value"},
		},
		Agents: configbundle.AgentsConfig{
			DefaultAgent: "reviewer",
			Mode:         configbundle.MergeModeMerge,
			Definitions: []configbundle.AgentDefinition{
				{
					ID:           "reviewer",
					Name:         "Reviewer",
					Description:  "reviews code",
					SystemPrompt: "You are a careful reviewer.",
					ModelAlias:   "fast",
					Tools:        []string{"read", "bash", "totally-unknown-tool"},
					Capabilities: []string{"code-review"},
					Metadata:     map[string]string{"team": "platform"},
				},
			},
		},
		Profiles: configbundle.ProfilesConfig{
			Mode: configbundle.ProfileModeInline,
			Inline: []configbundle.ProfileDefinition{
				{
					ID:           "fast-profile",
					Name:         "Fast",
					Provider:     "anthropic",
					Model:        "claude-haiku",
					MaxTokens:    4096,
					SystemPrompt: "unused in harness profiles",
					Tools:        []string{"read"},
				},
			},
		},
		Prompts: configbundle.PromptsConfig{
			Custom: map[string]string{
				"default": "You are a careful, deterministic legacy-imported agent.",
				"other":   "an unused template",
			},
			Overrides: map[string]string{
				"greeting": "an unused override",
			},
			Mode: configbundle.MergeModeMerge,
		},
		Tools: configbundle.ToolsConfig{
			Enabled:  []string{"read", "apply_patch", "bash", "totally-unknown-tool", "vault_exec"},
			Disabled: []string{"web_fetch"},
			Custom: []configbundle.ToolDefinition{
				{ID: "custom-tool", Name: "Custom Tool", Type: "function"},
			},
			Overrides: map[string]configbundle.ToolOverride{
				"bash": {Enabled: boolPtr(true)},
			},
			PermissionPolicy: &core.PermissionConfig{},
			Mode:             configbundle.MergeModeMerge,
		},
		Hooks: configbundle.HooksConfig{
			Definitions: []configbundle.HookDefinition{
				{
					ID:       "pre-commit",
					Name:     "Pre Commit",
					Type:     "command",
					Event:    "pre.commit",
					Command:  "echo hi",
					Enabled:  boolPtr(true),
					Priority: 1,
					Timeout:  10,
					Environment: map[string]string{
						"HOOK_TOKEN": secretSentinelHookEnv,
					},
				},
				{
					ID:      "disabled-hook",
					Type:    "http",
					Event:   "post.commit",
					URL:     "https://example.com/hook",
					Enabled: boolPtr(true),
				},
			},
			Disabled: []string{"disabled-hook"},
			Mode:     configbundle.MergeModeAppend,
		},
		Skills: configbundle.SkillsConfig{
			Installed: []configbundle.SkillReference{
				{ID: "vhs", Name: "VHS", Path: "skills/vhs", Source: "project", Enabled: boolPtr(true)},
				{ID: "disabled-skill", Name: "Disabled", Enabled: boolPtr(false)},
			},
			SearchPath: []string{"skills/"},
			Mode:       configbundle.MergeModeAppend,
		},
		Providers: configbundle.ProvidersConfig{
			Mode: configbundle.ProviderModeInline,
			Inline: []configbundle.ProviderDefinition{
				{
					ID:      "work-anthropic",
					Name:    "Work Anthropic",
					Type:    "anthropic",
					Enabled: boolPtr(true),
					BaseURL: "https://api.anthropic.com",
					Models: []configbundle.ModelDefinition{
						{ID: "claude-3", Name: "Claude 3"},
					},
					ModelAliases: map[string]string{"fast": "claude-3"},
					Headers:      map[string]string{"X-Team": "platform"},
				},
				{
					ID:      "disabled-provider",
					Type:    "openai",
					Enabled: boolPtr(false),
				},
			},
		},
		Credentials: configbundle.CredentialsConfig{
			ProviderKeys: map[string]string{
				"work-anthropic":  secretSentinelProviderKey,
				"unmatched-cloud": secretSentinelProviderKey + "-unmatched",
			},
			OAuthTokens: map[string]string{
				"work-anthropic": secretSentinelOAuthToken,
			},
			MCPAuth: map[string]configbundle.MCPAuthConfig{
				"internal-mcp": {
					Type:    "bearer",
					Token:   secretSentinelMCPToken,
					Headers: map[string]string{"Authorization": secretSentinelMCPHeader},
				},
			},
			Inherit: true,
		},
		MCPServers: configbundle.MCPServersConfig{
			Servers: []configbundle.MCPServerDefinition{
				{
					ID:      "internal-mcp",
					Name:    "Internal MCP",
					Type:    "stdio",
					Enabled: boolPtr(true),
					Command: "/usr/local/bin/internal-mcp",
					Args:    []string{"--flag"},
					Env:     map[string]string{"MCP_SECRET": secretSentinelMcpEnv},
					Headers: map[string]string{"Authorization": secretSentinelMCPHeader},
					Timeout: 15,
					Tools:   []string{"lookup"},
				},
				{
					ID:      "oauth-mcp",
					Type:    "oauth",
					Enabled: boolPtr(true),
				},
				{
					ID:      "disabled-mcp",
					Type:    "http",
					URL:     "https://example.com/mcp",
					Enabled: boolPtr(true),
				},
			},
			Disabled: []string{"disabled-mcp"},
			Mode:     configbundle.MergeModeAppend,
		},
		ContextSources: configbundle.ContextSourcesConfig{
			Sources: []configbundle.ContextSourceDefinition{
				{ID: "rag-1", Name: "RAG", Type: "rag", Enabled: boolPtr(true)},
			},
			Mode: configbundle.MergeModeAppend,
		},
		Environments: configbundle.EnvironmentsConfig{
			Current: "production",
			Definitions: []configbundle.EnvironmentDefinition{
				{ID: "production", Name: "Production"},
			},
		},
		MergePolicy: configbundle.DefaultMergePolicy(),
	}
}

func intPtr(n int) *int             { return &n }
func float64Ptr(f float64) *float64 { return &f }

func writeFixture(t *testing.T, dir, name string, bundle *configbundle.ConfigBundle) string {
	t.Helper()
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// snapshotFile captures name/size/mtime/content-hash for a single file, used
// by TestReadOnly (D1).
type fileSnapshot struct {
	size  int64
	mtime time.Time
	hash  string
}

func snapshotFile(t *testing.T, path string) fileSnapshot {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(raw)
	return fileSnapshot{size: info.Size(), mtime: info.ModTime(), hash: hex.EncodeToString(sum[:])}
}

// TestReadOnly proves D1: importing a fixture directory leaves every file in
// it byte-identical (name unchanged, size unchanged, mtime unchanged,
// content hash unchanged), and creates no new file in that directory either.
func TestReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "legacy-config.json", buildMaximalBundle())

	before := snapshotFile(t, path)
	beforeEntries := readDirNames(t, dir)

	result, err := legacyimport.Import(legacyimport.ImportInput{ConfigBundlePath: path, Timestamp: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result == nil {
		t.Fatal("Import returned nil result")
	}

	after := snapshotFile(t, path)
	afterEntries := readDirNames(t, dir)

	if before != after {
		t.Fatalf("legacy file mutated by Import: before=%+v after=%+v", before, after)
	}
	if len(beforeEntries) != len(afterEntries) {
		t.Fatalf("directory entry count changed: before=%v after=%v (Import must never create a file)", beforeEntries, afterEntries)
	}
	for i := range beforeEntries {
		if beforeEntries[i] != afterEntries[i] {
			t.Fatalf("directory entries changed: before=%v after=%v", beforeEntries, afterEntries)
		}
	}
}

func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// TestLegacyFieldInventoryIsFullyClassified proves D2's total-classification
// claim: every field legacyFieldInventory() names is covered by at least one
// ClassificationEntry.Source when importing a maximal fixture, and no
// ClassificationEntry.Source names a field outside that independently
// authored inventory.
func TestLegacyFieldInventoryIsFullyClassified(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "legacy-config.json", buildMaximalBundle())

	result, err := legacyimport.Import(legacyimport.ImportInput{ConfigBundlePath: path})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	got := map[string]bool{}
	for _, e := range result.Report.Classification {
		for _, s := range e.Source {
			got[s] = true
		}
	}

	want := legacyFieldInventory()
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}

	var missing []string
	for _, w := range want {
		if !got[w] {
			missing = append(missing, w)
		}
	}
	var unexpected []string
	for s := range got {
		if !wantSet[s] {
			unexpected = append(unexpected, s)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	if len(missing) > 0 {
		t.Errorf("legacy fields with NO classification entry (D2 violation): %v", missing)
	}
	if len(unexpected) > 0 {
		t.Errorf("classification entries reference fields outside legacyFieldInventory (update the inventory or the classifier): %v", unexpected)
	}

	// Every bucket must be exercised by the maximal fixture.
	counts := map[legacyimport.Bucket]int{}
	for _, c := range result.Report.BucketCounts() {
		counts[c.Bucket] = c.Count
	}
	for _, b := range legacyimport.AllBuckets() {
		if counts[b] == 0 {
			t.Errorf("bucket %s has zero entries in the maximal fixture report; the fixture should exercise every bucket", b)
		}
	}
}

// TestUnknownLegacyKeyFailsLoudly proves the D2 "a legacy key that matches no
// bucket must FAIL the import loudly" requirement at the schema-drift layer:
// an unrecognized top-level JSON field is rejected by the strict decode
// before classification ever runs, with an error naming the file.
func TestUnknownLegacyKeyFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy-config.json")
	raw := []byte(`{"schemaVersion":1,"totallyUnknownLegacyField":true,"system":{}}`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := legacyimport.Import(legacyimport.ImportInput{ConfigBundlePath: path})
	if err == nil {
		t.Fatal("expected Import to fail loudly on an unknown legacy field, got nil error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error should name the malformed file %q, got: %v", path, err)
	}
}

// TestMissingFileFailsClosed proves the read itself fails closed (an error
// naming the file), never a silent skip.
func TestMissingFileFailsClosed(t *testing.T) {
	_, err := legacyimport.Import(legacyimport.ImportInput{ConfigBundlePath: filepath.Join(t.TempDir(), "does-not-exist.json")})
	if err == nil {
		t.Fatal("expected an error for a missing legacy file")
	}
}

// TestEmptyPathRejected proves Import fails closed when given no path at
// all, rather than falling back to a default/home-dir location (D1).
func TestEmptyPathRejected(t *testing.T) {
	_, err := legacyimport.Import(legacyimport.ImportInput{})
	if err == nil {
		t.Fatal("expected an error when ConfigBundlePath is empty")
	}
}

// representativeCompilingBundle is a smaller, realistic fixture whose
// proposal is expected to ACTUALLY COMPILE (D3): a resolvable provider with
// a credential reference, a real default systemPrompt, and only tui-v1-bound
// tool names.
func representativeCompilingBundle() *configbundle.ConfigBundle {
	return &configbundle.ConfigBundle{
		SchemaVersion: 1,
		Name:          "representative",
		System: configbundle.SystemConfig{
			DefaultProvider: "anthropic",
			DefaultModel:    "claude-sonnet",
		},
		Providers: configbundle.ProvidersConfig{
			Mode: configbundle.ProviderModeInline,
			Inline: []configbundle.ProviderDefinition{
				{
					ID: "anthropic", Type: "anthropic", Enabled: boolPtr(true),
					Models: []configbundle.ModelDefinition{{ID: "claude-sonnet", Name: "Claude Sonnet"}},
				},
			},
		},
		Prompts: configbundle.PromptsConfig{
			Custom: map[string]string{"default": "You are a deterministic test agent."},
		},
		Tools: configbundle.ToolsConfig{
			Enabled: []string{"read", "apply_patch", "bash"},
		},
	}
}

// TestRepresentativeFixtureCompiles proves D3's positive case: a realistic
// translated proposal is actually run through harness.CompileBytes and
// compiles successfully, with a non-empty digest and a non-nil Plan.
func TestRepresentativeFixtureCompiles(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "legacy-config.json", representativeCompilingBundle())

	result, err := legacyimport.Import(legacyimport.ImportInput{ConfigBundlePath: path, Timestamp: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !result.Report.Compile.Compiled {
		t.Fatalf("expected the representative fixture's proposal to compile; diagnostics: %v", result.Report.Compile.Diagnostics)
	}
	if result.Report.Compile.Digest == "" {
		t.Error("expected a non-empty compile digest")
	}
	if result.Plan == nil {
		t.Fatal("expected a non-nil Plan when Report.Compile.Compiled is true")
	}
	if result.Plan.ProviderID() != "anthropic" {
		t.Errorf("expected provider id anthropic, got %q", result.Plan.ProviderID())
	}
}

// TestUncompilableFixtureIsReportedHonestly proves D3's negative case: a
// legacy config with no provider information at all produces a Result (not
// an error) whose Report.Compile.Compiled is false, with real diagnostics
// naming the missing provider.
func TestUncompilableFixtureIsReportedHonestly(t *testing.T) {
	dir := t.TempDir()
	bundle := &configbundle.ConfigBundle{SchemaVersion: 1, Name: "no-provider"}
	path := writeFixture(t, dir, "legacy-config.json", bundle)

	result, err := legacyimport.Import(legacyimport.ImportInput{ConfigBundlePath: path})
	if err != nil {
		t.Fatalf("Import should not itself error for an uncompilable proposal: %v", err)
	}
	if result.Report.Compile.Compiled {
		t.Fatal("expected the empty fixture's proposal to fail to compile (no provider.id)")
	}
	if len(result.Report.Compile.Diagnostics) == 0 {
		t.Fatal("expected non-empty compile diagnostics")
	}
	found := false
	for _, d := range result.Report.Compile.Diagnostics {
		if strings.Contains(d, "provider.id") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a diagnostic mentioning provider.id, got: %v", result.Report.Compile.Diagnostics)
	}
	if result.Plan != nil {
		t.Error("expected a nil Plan when Report.Compile.Compiled is false")
	}
}

// TestRollbackPlanShape proves D4: RollbackPlan names the legacy file, is
// explicitly labelled a PLAN (never a backup), and its hash/size match the
// file that was actually read.
func TestRollbackPlanShape(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "legacy-config.json", representativeCompilingBundle())
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	wantHash := "sha256:" + hex.EncodeToString(sum[:])

	result, err := legacyimport.Import(legacyimport.ImportInput{ConfigBundlePath: path})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	rb := result.Report.Rollback
	if !strings.Contains(strings.ToLower(rb.Note), "plan") || !strings.Contains(strings.ToLower(rb.Note), "not a performed backup") {
		t.Errorf("rollback note must explicitly state it is a PLAN, not a performed backup; got: %q", rb.Note)
	}
	if len(rb.Files) != 1 {
		t.Fatalf("expected exactly one rollback file entry, got %d", len(rb.Files))
	}
	f := rb.Files[0]
	if f.Path != path {
		t.Errorf("rollback file path = %q, want %q", f.Path, path)
	}
	if f.ContentHash != wantHash {
		t.Errorf("rollback content hash = %q, want %q", f.ContentHash, wantHash)
	}
	if f.SizeBytes != int64(len(raw)) {
		t.Errorf("rollback size = %d, want %d", f.SizeBytes, len(raw))
	}
}

// TestNoSecretLeak proves D5: every secret sentinel planted in the maximal
// fixture (provider keys, OAuth tokens, MCP auth token/headers, hook/mcp
// environment values) appears in NEITHER the proposal JSON NOR the rendered
// report (text or JSON), at any nesting depth.
func TestNoSecretLeak(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "legacy-config.json", buildMaximalBundle())

	result, err := legacyimport.Import(legacyimport.ImportInput{ConfigBundlePath: path, Timestamp: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	reportJSON, err := result.Report.RenderJSON()
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	reportText := result.Report.Render()

	sentinels := []string{
		secretSentinelProviderKey,
		secretSentinelProviderKey + "-unmatched",
		secretSentinelOAuthToken,
		secretSentinelMCPToken,
		secretSentinelMCPHeader,
		secretSentinelHookEnv,
		secretSentinelMcpEnv,
	}
	haystacks := map[string][]byte{
		"proposal.json": result.Proposal.JSON,
		"report.json":   reportJSON,
		"report.txt":    []byte(reportText),
	}
	for _, sentinel := range sentinels {
		for name, h := range haystacks {
			if strings.Contains(string(h), sentinel) {
				t.Errorf("secret sentinel %q leaked into %s (D5 violation)", sentinel, name)
			}
		}
	}
}

// TestDeterministic proves the report and proposal are byte-for-byte
// reproducible from identical input, including an identical caller-supplied
// timestamp (this package itself never calls time.Now()).
func TestDeterministic(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "legacy-config.json", buildMaximalBundle())

	input := legacyimport.ImportInput{ConfigBundlePath: path, Timestamp: "2026-01-01T00:00:00Z", Name: "det-test"}

	r1, err := legacyimport.Import(input)
	if err != nil {
		t.Fatalf("Import #1: %v", err)
	}
	r2, err := legacyimport.Import(input)
	if err != nil {
		t.Fatalf("Import #2: %v", err)
	}

	if string(r1.Proposal.JSON) != string(r2.Proposal.JSON) {
		t.Error("Proposal.JSON is not deterministic across identical runs")
	}
	if r1.Report.Render() != r2.Report.Render() {
		t.Error("Report.Render() is not deterministic across identical runs")
	}
	j1, err := r1.Report.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	j2, err := r2.Report.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(j1) != string(j2) {
		t.Error("Report.RenderJSON() is not deterministic across identical runs")
	}
}

// TestCredentialReferenceNeverInline proves the proposal's provider
// credential is always a Ref (env-var reference), never an inline literal,
// whenever the legacy store declares a matching provider key (D5's
// structural guarantee, independent of the substring-scan in TestNoSecretLeak).
func TestCredentialReferenceNeverInline(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "legacy-config.json", buildMaximalBundle())

	result, err := legacyimport.Import(legacyimport.ImportInput{ConfigBundlePath: path})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	cred := result.Proposal.Document.Provider.Credential
	if cred == nil {
		t.Fatal("expected the proposal's provider.credential to be set (a matching legacy provider key exists)")
	}
	if cred.Inline != "" {
		t.Errorf("provider.credential.inline must never be set by legacyimport; got %q", cred.Inline)
	}
	if cred.Env == "" {
		t.Error("expected provider.credential.env to reference a synthesized environment variable name")
	}
}

var _ = harness.APIVersionV1Alpha1 // keep harness import used even if only for documentation in future edits
