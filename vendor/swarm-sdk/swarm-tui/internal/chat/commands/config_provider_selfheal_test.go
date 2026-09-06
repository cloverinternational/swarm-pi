package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkprovider "github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestLoadProvidersReinjectsMissingClaudeCode is a regression test for the bug
// where a persisted providers.json that had lost the "ClaudeCode" OAuth entry
// never got it back — silently removing Claude Code from the TUI auth screen.
//
// LoadProviders only falls back to defaults when the file is absent; an existing
// file missing a built-in provider must be self-healed by applyProviderMigrations.
func TestLoadProvidersReinjectsMissingClaudeCode(t *testing.T) {
	cm := newTestConfigManager(t)

	// A persisted config that has every provider EXCEPT the ClaudeCode OAuth
	// entry — mirroring the real corrupted file observed on disk.
	raw := `[
      {"name":"OpenAI","display_name":"OpenAI (API Key)","type":"api_key","api_type":"openai","available":false,"models":[]},
      {"name":"Anthropic","display_name":"Anthropic (API Key)","type":"api_key","api_type":"anthropic","available":false,"models":[]}
    ]`
	path := filepath.Join(cm.configDir, "providers.json")
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatalf("write providers.json: %v", err)
	}

	got, err := cm.LoadProviders()
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}

	// The regressed provider must be back, and typed oauth so the auth screen
	// (which filters on type=="oauth") will list it.
	var claude *ProviderConfig
	for i := range got {
		if strings.EqualFold(got[i].Name, "ClaudeCode") {
			claude = &got[i]
			break
		}
	}
	if claude == nil {
		t.Fatal("ClaudeCode not re-injected — auth screen would still be missing Claude Code")
	}
	if claude.Type != "oauth" {
		t.Errorf("ClaudeCode.Type = %q, want oauth (else it is hidden from the auth screen)", claude.Type)
	}

	// Generalize: every built-in OAuth provider from the SDK catalog must be
	// present after load, not just ClaudeCode.
	for _, bp := range sdkprovider.BuiltinOAuthProviders() {
		if !providersContains(got, bp.Name) {
			t.Errorf("built-in OAuth provider %q missing after LoadProviders (self-heal failed)", bp.Name)
		}
	}

	// The repaired list must be persisted back to disk so the fix survives a
	// restart (LoadProviders saves when applyProviderMigrations reports changes).
	reread, err := cm.LoadProviders()
	if err != nil {
		t.Fatalf("LoadProviders (reread): %v", err)
	}
	if !providersContains(reread, "ClaudeCode") {
		t.Error("ClaudeCode not persisted after self-heal")
	}
}

// TestGetDefaultProvidersFromCatalog verifies the defaults path is driven by the
// SDK catalog, so defaults and the self-heal path share one source of truth.
func TestGetDefaultProvidersFromCatalog(t *testing.T) {
	cm := newTestConfigManager(t)
	defaults := cm.getDefaultProviders()

	for _, bp := range sdkprovider.BuiltinProviders() {
		found := false
		for _, p := range defaults {
			if strings.EqualFold(p.Name, bp.Name) {
				found = true
				if p.Type != bp.AuthType {
					t.Errorf("provider %q Type = %q, want %q (from catalog)", p.Name, p.Type, bp.AuthType)
				}
				if p.APIType != bp.APIType {
					t.Errorf("provider %q APIType = %q, want %q (from catalog)", p.Name, p.APIType, bp.APIType)
				}
				break
			}
		}
		if !found {
			t.Errorf("built-in provider %q from catalog missing in getDefaultProviders", bp.Name)
		}
	}
}
