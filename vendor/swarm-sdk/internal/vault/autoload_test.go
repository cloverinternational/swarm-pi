package vault

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

// TestAutoLoadTwoPersonNonInteractive verifies that a two-person vault on disk is
// picked up by the non-interactive loader and that its credential resolves as
// two-person (so the executor will refuse the normal path).
func TestAutoLoadTwoPersonNonInteractive(t *testing.T) {
	ws := t.TempDir()
	vaultDir := filepath.Join(ws, ".swarm", "vault")
	if err := os.MkdirAll(vaultDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Write a user identity file.
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	idPath := filepath.Join(vaultDir, "identity")
	if err := SaveIdentity(id, idPath); err != nil {
		t.Fatal(err)
	}

	// Seed a two-person store with one sensitive credential (2-of-2 for this id +
	// a second recipient).
	tpPath := filepath.Join(vaultDir, "twoperson.json")
	tps, err := NewTwoPersonStorage(tpPath)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := age.GenerateX25519Identity()
	recs := []recipientWithKey{
		{recipient: id.Recipient(), key: id.Recipient().String(), comment: "me"},
		{recipient: other.Recipient(), key: other.Recipient().String(), comment: "peer"},
	}
	cred := Credential{
		ID: "aws-prod", Kind: CredentialKindAWSSecretKey, Secret: "AKIAPRODSECRET",
		Scope: ScopeProject, Inject: InjectConfig{Method: InjectEnv, Target: "AWS"},
	}
	if err := tps.SealAndStore(context.Background(), cred, 2, recs); err != nil {
		t.Fatal(err)
	}

	// Auto-load with the workspace as projectID.
	paths := DefaultAutoLoadPaths(ws, ws, ws)
	// DefaultAutoLoadPaths puts identity under home/.swarm/vault; here home==ws.
	paths.IdentityPath = idPath
	paths.TwoPersonPath = tpPath

	provider, ok := AutoLoadProvider(paths)
	if !ok {
		t.Fatal("expected auto-load to produce a provider")
	}
	if !provider.IsEnabled() {
		t.Fatal("provider should be enabled")
	}
	if provider.GetIdentityPath() != idPath {
		t.Fatalf("identity path not carried: %q", provider.GetIdentityPath())
	}

	// In trusted-unlocked mode the executor no longer refuses two-person
	// credentials; it resolves and runs them directly. Verify the credential
	// is still resolvable through the auto-loaded provider.
	resolved, err := provider.GetVault().ResolveCredential(context.Background(), "aws-prod", "")
	if err != nil {
		t.Fatalf("expected two-person credential to resolve, got %v", err)
	}
	if resolved == nil || resolved.ID != "aws-prod" {
		t.Fatal("credential should not be nil")
	}
}

// TestAutoLoadNothingPresent returns not-ok when no material exists.
func TestAutoLoadNothingPresent(t *testing.T) {
	ws := t.TempDir()
	_, ok := AutoLoadProvider(DefaultAutoLoadPaths(ws, ws, ws))
	if ok {
		t.Fatal("expected no provider when no vault material exists")
	}
}
