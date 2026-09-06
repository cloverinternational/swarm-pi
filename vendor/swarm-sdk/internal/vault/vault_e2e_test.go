package vault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCleartextVaultE2E verifies the full cleartext vault workflow:
// 1. Create a cleartext transparent store
// 2. Store a credential
// 3. Verify it persists as cleartext (not base64) on disk
// 4. List it through a trusted-unlocked vault
// 5. Resolve it through the executor with no approval gates
// 6. Execute a command with it and verify no restrictions fire
func TestCleartextVaultE2E(t *testing.T) {
	tmp := t.TempDir()
	vaultPath := filepath.Join(tmp, "credentials.json")

	// 1. Create cleartext store
	store, err := NewTransparentStorage(vaultPath)
	if err != nil {
		t.Fatalf("NewTransparentStorage: %v", err)
	}

	// 2. Store a credential
	cred := Credential{
		ID:     "test-api-key",
		Name:   "Test API Key",
		Kind:   CredentialKindAPIKey,
		Secret: "sk-test-12345",
		Scope:  ScopeGlobal,
		Inject: InjectConfig{Method: InjectEnv, Target: "TEST_API_KEY"},
	}
	ctx := context.Background()
	if err := store.Store(ctx, cred); err != nil {
		t.Fatalf("Store: %v", err)
	}
	t.Log("✓ Credential stored")

	// 3. Verify cleartext on disk (not base64)
	raw, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "sk-test-12345") {
		t.Fatalf("secret not stored as cleartext in file:\n%s", string(raw))
	}
	t.Log("✓ Secret stored as cleartext (not base64)")

	// 4. Build a trusted-unlocked vault + executor (same as AutoLoadProvider does)
	v := NewVault(store, VaultConfig{
		Enabled:         true,
		DefaultMode:     ModeYOLO,
		TrustedUnlocked: true,
	})
	exec := NewExecutor(v, VaultConfig{
		Enabled:         true,
		DefaultMode:     ModeYOLO,
		TrustedUnlocked: true,
	}, nil)
	provider := NewVaultProvider(exec, v, "")
	if !provider.IsEnabled() {
		t.Fatal("provider should be enabled")
	}
	t.Log("✓ Trusted-unlocked provider created")

	// 5. List through the vault
	creds, err := v.List(ctx, CredentialFilter{}, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(creds) != 1 || creds[0].ID != "test-api-key" {
		t.Fatalf("expected 1 cred 'test-api-key', got %#v", creds)
	}
	t.Log("✓ Listed credential through shared vault")

	// 6. Resolve through executor — should NOT get approval/two-person/tool/host errors
	resolved, err := v.ResolveCredential(ctx, "test-api-key", "")
	if err != nil {
		t.Fatalf("ResolveCredential: %v", err)
	}
	if resolved.Secret != "sk-test-12345" {
		t.Fatalf("resolved secret = %q, want sk-test-12345", resolved.Secret)
	}
	t.Log("✓ Resolved credential with secret intact")

	// 7. Execute a command — should run with no approval gates
	result, err := exec.Execute(ctx, ExecutionRequest{
		CredentialID: "test-api-key",
		Command:      "sh",
		Args:         []string{"-c", "echo $TEST_API_KEY"},
		Tool:         "bash",
	})
	if err != nil {
		t.Fatalf("Execute failed (should not have approval gates): %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", result.ExitCode)
	}
	if result.Stdout != "sk-test-12345\n" {
		t.Fatalf("stdout = %q, want 'sk-test-12345\\n'", result.Stdout)
	}
	t.Logf("✓ Executed command with credential: stdout=%q", result.Stdout)

	// 8. Verify a credential with allowedTools/hosts that would normally block
	//    still works in trusted-unlocked mode
	restrictedCred := Credential{
		ID:           "restricted-key",
		Kind:         CredentialKindAPIKey,
		Secret:       "restricted-secret",
		Scope:        ScopeGlobal,
		AllowedTools: []string{"not-bash"},
		AllowedHosts: []string{"example.com"},
		Inject:       InjectConfig{Method: InjectEnv, Target: "RESTRICTED_KEY"},
	}
	if err := store.Store(ctx, restrictedCred); err != nil {
		t.Fatalf("Store restricted: %v", err)
	}
	result2, err := exec.Execute(ctx, ExecutionRequest{
		CredentialID: "restricted-key",
		Command:      "sh",
		Args:         []string{"-c", "echo $RESTRICTED_KEY"},
		Tool:         "bash",
	})
	if err != nil {
		t.Fatalf("Execute restricted cred failed (trusted mode should bypass gates): %v", err)
	}
	if result2.ExitCode != 0 {
		t.Fatalf("restricted exit code = %d, want 0", result2.ExitCode)
	}
	t.Logf("✓ Restricted credential executed freely in trusted mode: stdout=%q", result2.Stdout)

	// 9. Verify legacy base64 file still loads
	legacyPath := filepath.Join(tmp, "legacy-credentials.json")
	legacyContent := `{"version":"1","updatedAt":"2026-01-01T00:00:00Z","credentials":{"legacy-key":{"kind":"api_key","value":"bGVnYWN5LXNlY3JldA==","injectMethod":"env","injectTarget":"LEGACY_KEY","scope":"global"}}}`
	if err := os.WriteFile(legacyPath, []byte(legacyContent), 0600); err != nil {
		t.Fatal(err)
	}
	legacyStore, err := NewTransparentStorage(legacyPath)
	if err != nil {
		t.Fatalf("NewTransparentStorage legacy: %v", err)
	}
	legacyCreds, err := legacyStore.List(ctx, CredentialFilter{})
	if err != nil {
		t.Fatalf("legacy List: %v", err)
	}
	if len(legacyCreds) != 1 || legacyCreds[0].Secret != "legacy-secret" {
		t.Fatalf("legacy creds = %#v, want secret 'legacy-secret'", legacyCreds)
	}
	t.Log("✓ Legacy base64 vault file still loads correctly")
}
