package builtin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

// setupPassphraseVaultProvider mimics swarm-tui/internal/chat/settings.
// VaultSettings.CreateVaultProvider + unlockWithPassphrase, to exercise
// vault_add/vault_list against the real TUI unlock path (a passphrase-based
// AgeStorage), the setup reported in the "vault_add causes a memory panic"
// bug report.
func setupPassphraseVaultProvider(t *testing.T, dir string) vault.VaultProvider {
	t.Helper()
	path := filepath.Join(dir, "global.vault")

	encryptor, err := vault.NewAgeEncryptorFromPassphrase("correct horse battery staple")
	if err != nil {
		t.Fatalf("NewAgeEncryptorFromPassphrase: %v", err)
	}
	storage, err := vault.NewAgeStorage(path, encryptor)
	if err != nil {
		t.Fatalf("NewAgeStorage: %v", err)
	}

	v := vault.NewVault(storage, vault.VaultConfig{Enabled: true, DefaultMode: vault.ModeBalanced})
	exec := vault.NewExecutor(v, vault.VaultConfig{Enabled: true, DefaultMode: vault.ModeBalanced}, nil)
	return vault.NewVaultProvider(exec, v, "")
}

// TestVaultAddThenListNoPanic is a smoke/regression test for the reported
// "vault_add causes a memory panic" bug: unlock a passphrase-based vault
// exactly as the TUI does, add a credential, list, lock/reopen, and list
// again — none of these should panic.
func TestVaultAddThenListNoPanic(t *testing.T) {
	dir := t.TempDir()
	provider := setupPassphraseVaultProvider(t, dir)
	prev := vault.GetDefaultVaultProvider()
	vault.SetDefaultVaultProvider(provider)
	defer vault.SetDefaultVaultProvider(prev)

	addTool := &VaultAddTool{}
	listTool := &VaultListTool{}
	ctx := context.Background()

	// 1. Fresh vault: list should be empty, no panic.
	if _, err := listTool.Run(ctx, VaultListParams{}); err != nil {
		t.Fatalf("first list: %v", err)
	}

	// 2. Add a plain credential.
	if _, err := addTool.Run(ctx, VaultAddParams{
		ID:     "github-token",
		Kind:   vault.CredentialKindBearerToken,
		Secret: "ghp_totallyfake",
		Target: "GITHUB_TOKEN",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// 3. List again — this is the exact call the user reported panicking on.
	if _, err := listTool.Run(ctx, VaultListParams{}); err != nil {
		t.Fatalf("second list: %v", err)
	}

	// 4. Restart the process's view of the vault: reopen AgeStorage from disk
	//    (simulates lock -> re-unlock in the same session) and list again.
	encryptor2, err := vault.NewAgeEncryptorFromPassphrase("correct horse battery staple")
	if err != nil {
		t.Fatalf("re-derive encryptor: %v", err)
	}
	storage2, err := vault.NewAgeStorage(filepath.Join(dir, "global.vault"), encryptor2)
	if err != nil {
		t.Fatalf("reopen vault: %v", err)
	}
	v2 := vault.NewVault(storage2, vault.VaultConfig{Enabled: true, DefaultMode: vault.ModeBalanced})
	exec2 := vault.NewExecutor(v2, vault.VaultConfig{Enabled: true, DefaultMode: vault.ModeBalanced}, nil)
	vault.SetDefaultVaultProvider(vault.NewVaultProvider(exec2, v2, ""))

	if _, err := listTool.Run(ctx, VaultListParams{}); err != nil {
		t.Fatalf("third list (after reopen): %v", err)
	}

	// 5. Sanity: file actually exists and is non-trivial.
	info, err := os.Stat(filepath.Join(dir, "global.vault"))
	if err != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty vault file, err=%v", err)
	}
}
