package settings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

func TestVaultExecutionHelper(t *testing.T) {
	if os.Getenv("SWARM_TEST_VAULT_HELPER") != "1" {
		return
	}
	fmt.Print("delegated-ok")
	os.Exit(0)
}

func TestCreateVaultProviderIncludesConfiguredProjectStorage(t *testing.T) {
	state := NewState()
	settings := NewVaultSettings(state)
	root := t.TempDir()
	settings.SetProjectPath(root)

	encryptor, err := vault.NewAgeEncryptorFromPassphrase("test-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	store, err := vault.NewAgeStorage(filepath.Join(root, "project.vault"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Store(context.Background(), vault.Credential{
		ID:     "project-test",
		Kind:   vault.CredentialKindAPIKey,
		Secret: "not-a-real-secret",
		Scope:  vault.ScopeProject,
	}); err != nil {
		t.Fatal(err)
	}
	settings.projectStore = store

	provider := settings.CreateVaultProvider("")
	if provider == nil || !provider.IsEnabled() {
		t.Fatal("project provider is not enabled")
	}
	// In trusted-unlocked mode the provider exposes a shared empty project ID
	// so all local TUIs and agents see the same credentials.
	if provider.GetProjectID() != "" {
		t.Fatalf("project ID = %q, want empty (shared vault)", provider.GetProjectID())
	}
	creds, err := provider.GetVault().List(context.Background(), vault.CredentialFilter{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 || creds[0].ID != "project-test" {
		t.Fatalf("project credentials = %#v", creds)
	}
}

func TestCreateVaultProviderDelegatesOrdinaryApprovalToTUI(t *testing.T) {
	state := NewState()
	settings := NewVaultSettings(state)
	store, err := vault.NewTransparentStorage(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Store(context.Background(), vault.Credential{
		ID:     "sensitive-test",
		Kind:   vault.CredentialKindPassword,
		Secret: "1",
		Scope:  vault.ScopeGlobal,
		Inject: vault.InjectConfig{
			Method: vault.InjectEnv,
			Target: "SWARM_TEST_VAULT_HELPER",
		},
	}); err != nil {
		t.Fatal(err)
	}
	settings.transparentStore = store

	provider := settings.CreateVaultProvider("")
	if provider == nil || !provider.IsEnabled() {
		t.Fatal("transparent provider is not enabled")
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.GetExecutor().Execute(context.Background(), vault.ExecutionRequest{
		CredentialID: "sensitive-test",
		Command:      testBinary,
		Args:         []string{"-test.run=TestVaultExecutionHelper"},
		Tool:         "bash",
	})
	if err != nil {
		t.Fatalf("TUI provider returned a second vault approval/error: %v", err)
	}
	if got := strings.TrimSpace(result.Stdout); got != "delegated-ok" {
		t.Fatalf("delegated execution stdout = %q, want delegated-ok", got)
	}
}

func TestLockAllClearsProviderAndPublishesLockedState(t *testing.T) {
	state := NewState()
	settings := NewVaultSettings(state)
	store, err := vault.NewTransparentStorage(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	settings.transparentStore = store
	state.VaultState = "unlocked"

	var published vault.VaultProvider
	called := false
	settings.SetOnVaultUnlockCallback(func(provider vault.VaultProvider) {
		called = true
		published = provider
	})
	settings.LockAll(state)

	if !called {
		t.Fatal("lock callback was not called")
	}
	if published != nil {
		t.Fatalf("lock callback provider = %#v, want nil", published)
	}
	if settings.IsUnlocked() {
		t.Fatal("settings still report unlocked")
	}
	if state.VaultState != "locked" {
		t.Fatalf("VaultState = %q, want locked", state.VaultState)
	}
	if rendered := settings.Render(state, 80, 24); !strings.Contains(rendered, "[Enter] Enable") {
		t.Fatalf("locked transparent vault did not render enable action:\n%s", rendered)
	}

	called = false
	if !settings.HandleKey("enter", state) {
		t.Fatal("transparent vault re-enable key was not handled")
	}
	if !called || published == nil || !published.IsEnabled() {
		t.Fatal("transparent vault was not republished after re-enable")
	}
	if state.VaultState != "unlocked" {
		t.Fatalf("VaultState after re-enable = %q, want unlocked", state.VaultState)
	}
	if !settings.HandleKey("l", state) {
		t.Fatal("transparent vault lock key was not handled")
	}
	if settings.IsUnlocked() || state.VaultState != "locked" {
		t.Fatalf("transparent vault remained enabled after l: unlocked=%v state=%q", settings.IsUnlocked(), state.VaultState)
	}
}
